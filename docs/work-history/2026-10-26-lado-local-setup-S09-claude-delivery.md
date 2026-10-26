# 2026-10-26 LADO 로컬 설정 채택 — S09 Claude 전달: 세션 설정 홈과 실행 경로 훅

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 a·b·c·f, "Facts measured", §9) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

채택으로 원래 자리에서 사라진 Claude 항목을 AO 가 띄우는 모든 Claude 세션이 계속 받게 한다. 세션마다 설정 홈을 만들고, 자격 증명은 `CLAUDE_SECURESTORAGE_CONFIG_DIR` 로 원래 자리에 둔다(S01 이 실측한 방식). LADO 실행은 스냅숏이 고정한 버전으로 홈을 만들어, 승인 뒤의 편집이 도는 워커에 닿지 않게 한다(HARNESS §15 항목 10·11·12 의 실행 쪽).

## 범위

- 만든다: `service/ladosetup/home.go`(홈 만들기·정리), `ports/lado_setup.go`(훅 인터페이스), `session_manager/lado_setup_home.go`(설정자와 적용 함수), 테스트.
- 바꾼다(훅 줄, diff 예산 안): `session_manager/manager.go`(`prepareWorkspace` 맨 앞 호출, 필드), `session_manager/chat_spawn.go`(`resumeChatController` 의 호출), `session_manager/agent_switching.go`(`safeNativeTranscriptPath`), 필요하면 `backend/pkg/agentcreds`(세션 환경의 `CLAUDE_SECURESTORAGE_CONFIG_DIR` 존중).
- 하지 않는다: Codex(S10), 스냅숏 내용·고정 쓰기(S11 — 이 단계는 S04 의 고정 읽기만 쓴다), 데몬 배선(S13).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S01 의 실측 결과(홈에 꼭 필요했던 것)를 먼저 본다.
1. 포트(`ports/lado_setup.go`): `LadoSetupHome interface { PrepareClaudeHome(ctx context.Context, in LadoSetupHomeInput) (map[string]string, error) }`, `LadoSetupHomeInput{SessionID domain.SessionID; ProjectID domain.ProjectID; ClientRequestID string; Env map[string]string}`. 돌려주는 맵은 바꿀 환경 변수(호출자가 `setProtectedEnv` 로 넣는다 — Windows 대소문자 무시 규칙 유지). 맵이 비면 아무것도 바꾸지 않는다.
2. `ladosetup.Service.PrepareClaudeHome`(결정 a·b·c):
   1. 살아 있는 관리 Claude 항목이 없으면 `nil, nil`(오늘과 같음).
   2. 기준: `Env` 에 `CLAUDE_CONFIG_DIR` 가 있으면 계정 홈(그 폴더와 그 안 `.claude.json`, 보안 저장소 변수는 Env 의 값 그대로); 없으면 자기 로그인(`Deps.ClaudeDir`, `Deps.ClaudeJSON`; 보안 저장소는 `ClaudeDirFromEnv` 면 `ClaudeDir`, 아니면 GOOS `darwin` 은 `""`, 나머지는 `ClaudeDir`).
   3. 항목: `ClientRequestID` 가 LADO 실행이면 S04 `LadoSetupVersionsForClientRequest` 의 고정 버전, 아니면 지금 버전 중 그 프로젝트에서 켜진 것(전역 바인딩 없음 = 켜짐, 프로젝트 force_on/off — 태스크는 실행만).
   4. 홈 `<state>/lado/homes/<session-id>/claude/`(0700)을 **맞춘다**(지우고 다시 만들지 않음): 상태 항목(플랜 "Facts measured" 의 계정 링크 목록, `plugins` 포함)은 기준의 같은 이름으로 가는 링크(기준에 없으면 기준에 빈 폴더·파일을 먼저 만든다 — AO 링크 도우미와 같은 동작), `skills/`·`agents/`·`commands/`·`output-styles/`·`hooks/` 는 진짜 폴더 안에 기준의 항목마다 링크 + 관리 항목마다 링크(트리 → `<state>/lado/harness/trees/<sha256>/` 를 blob 에서 한 번 만들어 둔 것, 파일 → blob 에서 복사; 이름이 겹치면 관리 항목), 최상위 `*.md` 는 기준 링크 + 관리 파일 복사, `settings.json` = 기준 파일(있는 그대로, 로그인 키 포함) 위에 켜진 settings 항목 키를 덮어쓴 것, `.claude.json` = 기준 파일 복사본에 `mcpServers` = 기준 서버 + 관리 서버(관리 우선), 0600. 홈 안에 Claude 가 만든 다른 항목은 그대로 둔다. 관리 항목에서 빠진 것(끈 항목)의 링크·파일은 지운다(링크는 따라가지 않고 링크만).
   5. 돌려주는 환경: `CLAUDE_CONFIG_DIR=<홈>`, `CLAUDE_SECURESTORAGE_CONFIG_DIR=<2 의 값>`.
   6. 오류는 500 `LADO_SETUP_HOME_FAILED`(details 에 단계 이름; 경로·내용 없음).
3. `session_manager`(새 파일 `lado_setup_home.go` + 훅 줄):
   - `SetLadoSetupHome(h ports.LadoSetupHome)`(설정자 꼴은 `SetWorkGate`), nil 이면 업스트림 그대로.
   - `applyLadoSetupHome(ctx, agent, id, env) error`: agent 가 Claude Code 일 때만; 세션 기록에서 프로젝트·client request id 를 읽어 훅을 부르고 돌려받은 키를 `setProtectedEnv` 로 넣는다.
   - 호출: `prepareWorkspace` 맨 앞(트러스트 쓰기 전 — 그래야 AO 가 홈의 `.claude.json` 에 트러스트를 쓴다)과 `resumeChatController` 의 컨트롤러 환경을 만들기 전. 둘 다 오류면 실행 실패.
   - 모든 실행 경로(스폰 TUI·Chat, 복원 TUI·Chat, 재개, 에이전트 전환 TUI·Chat, 인터페이스 전환, 계정 이동, 백그라운드 작업)를 표로 After 에 적고, 각 경로가 둘 중 어느 호출을 지나는지(또는 안 지나는지와 그 이유) 쓴다. AO 의 setup-token 주입(`augmentAgentRuntimeEnv`)은 훅보다 앞이어야 한다 — 순서가 그런지 확인.
4. `safeNativeTranscriptPath`(`session_manager/agent_switching.go`): 세션 설정 폴더 안이거나, 그 폴더의 `projects` 링크가 가리키는 실제 폴더 안으로 풀리는 대화 기록을 받는다(그 밖은 지금처럼 거부).
5. 자격 증명을 세션 환경의 `CLAUDE_CONFIG_DIR` 로 찾는 AO 코드를 찾아(`backend/pkg/agentcreds` 의 resolve 등) 목록을 After 에 적고, 홈에서 잘못된 곳을 보는 것은 `CLAUDE_SECURESTORAGE_CONFIG_DIR`(정의돼 있으면 그 값, 빈 값이면 `~/.claude`)를 먼저 보게 고친다.
6. 정리: `CleanupHomes(ctx)` — 세션이 끝났거나 없는 홈을 지운다(`os.RemoveAll` 은 링크를 따라가지 않음 — 링크 대상이 남는지 테스트). S13 이 데몬 시작과 검사 뒤에 부른다.
7. S07 의 검증에 홈 검사를 더한다: 임시 폴더에 자기 로그인용 홈을 만들어 채택 항목마다 해시가 맞는지(다음 단계 메모로 받은 자리).
8. 커밋 메시지 예: `feat(lado): deliver managed setup to Claude sessions through a config home`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S09-claude-delivery.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

`service/ladosetup`(격리된 홈):

- `TestClaudeHomeCarriesManagedItems` — HARNESS §15 항목 10: 자기 로그인 기준과 계정 기준 각각에서 상태 링크, 합친 폴더, settings 덮어쓰기, `.claude.json` 서버 합치기, 관리 항목 우선
- `TestClaudeHomeSecureStorageFollowsTheLogin` — 자기 로그인(실제 폴더 값), `CLAUDE_CONFIG_DIR` 에서 온 폴더, darwin 의 `""`, 계정은 Env 값 그대로
- `TestNoHomeWithoutManagedItems` — 관리 항목이 없으면 환경을 바꾸지 않고 폴더도 안 만듦
- `TestDirectUseNoLongerSeesAdoptedItems` — 채택 뒤 실제 폴더에는 그 항목이 없고 홈에는 있음
- `TestAccountLinkerCreatesNothingAfterAdoption` — HARNESS §15 항목 11: 채택 뒤 실제 `agentaccount` 의 Claude 준비(Prepare)를 가짜 홈에서 돌려도 실제 폴더 목록이 그대로
- `TestExecutionHomeUsesPinnedVersions` — HARNESS §15 항목 12 의 실행 쪽: 승인 뒤 새 버전이 생겨도 실행 홈은 고정 버전
- `TestHomeReconcileKeepsSessionFiles` — 다시 맞춰도 Claude 가 홈에 만든 파일은 남고, 끈 항목은 빠짐
- `TestHomesOfEndedSessionsAreRemoved` — 끝난 세션의 홈 삭제, 링크 대상(실제 `projects/` 등)은 그대로

`session_manager`:

- `TestPrepareWorkspaceBuildsTheLadoHomeFirst` — 훅이 트러스트 쓰기 전에 불리고, 홈 `.claude.json` 에 트러스트가 들어가며, 훅 오류는 실행 실패, nil 훅은 업스트림 그대로
- `TestChatResumeBuildsTheLadoHome` — Chat 재개 경로가 훅을 부름
- `TestSafeNativeTranscriptPathFollowsProjectsLink` — 링크된 `projects` 안의 기록은 받고, 그 밖은 거부

## 수용 게이트

- [ ] S09-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S09-G1: ladosetup 필수 테스트 8개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestClaudeHomeCarriesManagedItems TestClaudeHomeSecureStorageFollowsTheLogin TestNoHomeWithoutManagedItems TestDirectUseNoLongerSeesAdoptedItems TestAccountLinkerCreatesNothingAfterAdoption TestExecutionHomeUsesPinnedVersions TestHomeReconcileKeepsSessionFiles TestHomesOfEndedSessionsAreRemoved
  EXPECT: GO_TESTS_OK 8/8
- [ ] S09-G2: session_manager 필수 테스트 3개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/session_manager/ TestPrepareWorkspaceBuildsTheLadoHomeFirst TestChatResumeBuildsTheLadoHome TestSafeNativeTranscriptPathFollowsProjectsLink
  EXPECT: GO_TESTS_OK 3/3
- [ ] S09-G3: ladosetup 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S09-G4: session_manager·agentcreds 패키지 전체 통과(훅이 업스트림 동작을 바꾸지 않음)
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/session_manager/ ./pkg/agentcreds/
  EXPECT: GO_PKG_OK
- [ ] S09-G5: 업스트림 파일 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 140 backend/internal/session_manager/manager.go backend/internal/session_manager/chat_spawn.go backend/internal/session_manager/agent_switching.go backend/pkg/agentcreds
  EXPECT: BUDGET_OK
- [ ] S09-G6: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S09-G7: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S09-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 실행 경로 표, 자격 증명 읽는 곳 목록, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S13 배선에 넘길 것: 훅 설정자 이름, 정리 함수, 홈 폴더 위치)
