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

- 시작 HEAD: `1120b802e`(`docs(lado): hand the S07 flush gap to S12` — 메인 세션이 S12 엔트리만 고친 커밋, 그 앞이 `997f69f0a` S08 기록), 트리 깨끗. G5 diff 예산 기준 `BUDGET_OK 0/140`, G0 `DEFS_UNCHANGED`.
- 환경: Linux 7.0.0-31-generic, go1.27.1(스크래치 toolchain), umask 0022, `CHAIN_RUN=1`. `CLAUDE_CONFIG_DIR`·`CLAUDE_SECURESTORAGE_CONFIG_DIR`·`CODEX_HOME` 없음.
- 출발점 실측(코드 읽기):
  1. 실행 경로의 환경 순서: `runtimeEnv`(계정 변수는 `applyAgentAccountEnv` 가 넣음) → `augmentAgentRuntimeEnv`(setup-token 주입 — `CLAUDE_CONFIG_DIR` 가 있으면 건너뜀) → TUI 는 `ValidateLaunchAuth` → `prepareWorkspace`(`GetAgentHooks` → `PreLaunch` 가 `<CLAUDE_CONFIG_DIR>/.claude.json`, 없으면 `~/.claude.json` 에 트러스트). `prepareWorkspace` 가 받은 `env` 맵을 바꾸면 TUI 실행(`superviseAgentProcess(agent, id, env, argv)`)에 그대로 간다.
  2. Chat 은 다르다: 드라이버가 `PrepareControllerEnv` 가 돌려준 환경으로 `cfg.Env` 를 **갈아 끼운다**(`adapters/chatdriver/acp/driver.go` `connectProcess` 의 `cfg.Env = env`; 테스트 대역 `prepareTestChatStart` 도 같음). 그 환경은 `prepareChatControllerEnv` 가 `runtimeEnv` 로 새로 만들고 호출자가 그 뒤 augment 한다. 그래서 `prepareWorkspace` 가 바꾼 `env` 는 Chat 프로세스에 닿지 않는다 — 콜백은 세 곳: Chat 스폰(`chat_spawn.go` `launchChatController`), Chat 재개(`resumeChatController`), Chat 전환 대상(`agent_switching_chat.go`).
  3. `resumeChatController` 는 `prepareWorkspace` 를 부르지 않는다(플랜 사실 그대로). 인터페이스 전환·계정 이동은 `startTransitionTarget` → `relaunchSessionWithPolicy` 로 TUI 복원 또는 `resumeChatController` 를 탄다.
  4. `safeNativeTranscriptPath`(`agent_switching.go:1759`)는 `EvalSymlinks(configDir)` 아래로 풀리는 기록만 받는다 — 계정 폴더의 `projects` 링크(→ 실제 `~/.claude/projects`)로 풀리는 기록은 지금도 거부된다. configDir 는 늘 훅 전 환경으로 `nativeConfigDir` 가 계산한다(기준 폴더).
  5. 자격 증명 파일을 읽는 곳은 `pkg/agentcreds` `readCredentialsFile` 하나 → `claudeConfigDir(opts)` = `ConfigDir` > `CLAUDE_CONFIG_DIR` > `$HOME/.claude`. 홈으로 띄운 세션 환경이면 `<홈>/.credentials.json`(없음)을 본다.
  6. `ports.Agent` 에는 하네스 이름이 없다. 데몬 레지스트리(`agentRegistry`)는 하네스마다 같은 인스턴스(포인터)를 준다.
- 앞 단계 메모 중 이 단계 몫:
  - S01: 꼭 필요한 건 두 변수; `.claude.json` 복사본은 서버·온보딩·트러스트용; Claude 는 홈에 `backups/`·`cache/`·`.claude.json.lock` 을 만들고 `.claude.json` 을 다시 쓴다(재조정이 지우지 않음); 홈에 `.credentials.json` 은 생기지 않는다; 상태 링크는 링크 그대로 남고 그 안에 쓴다.
  - S03: blob 매니페스트는 `Normalize()` 뒤 sha 대조(`treeOfVersion` 이 함).
  - S04: `LadoSetupVersionsForClientRequest` 의 bool 은 "실행인가" — 실행이면 빈 고정도 그대로(지금 버전으로 대신하지 않음), 고정 버전은 live 와 무관; 실행이 아니면 `ListLiveLadoSetupItems` + `ListLadoSetupGlobalBindings` + `ListLadoSetupBindings(project)`.
  - S05: env 를 덮어쓸 때 기준 파일 env 의 로그인 변수를 합친다; 정리는 `Deps.Sessions` 의 `IsTerminated`.
  - S07: 검증 자리 `verifyAdopted`(상수 `Check…` 추가), 트리는 `treeOfVersion`, 경로 함수 `managedPath`·`managedRoot`·`managedKeyFile`.
  - S08: 홈은 live 목록과 고정 버전만, 관리 폴더에서 만들지 않는다.

## After — 수행 후 실측

- 코드 커밋 `db0986c70` `feat(lado): deliver managed setup to Claude sessions through a config home` — 14 파일, +2192/−18.
  - `ports/lado_setup.go`(+30): `LadoSetupHome{PrepareClaudeHome}`, `LadoSetupHomeInput{SessionID, ProjectID, ClientRequestID, Env}`(Env 는 읽기만).
  - `service/ladosetup/home.go`(+1154): `PrepareClaudeHome`(포트 구현, `var _ ports.LadoSetupHome`), 항목 고르기 `sessionItems(ctx, tool, project, clientRequestID)`(S10 이 그대로 쓴다), 기준 찾기 `homeBase`, 홈 맞추기 `buildHome`(계획 `homePlan` → 놓기 `place` → 낡은 것 지우기 `removeStale` → `.claude.json` 서버 `reconcileConfig` → 기록 `placed.json`), 트리 `materializeTree`(`<state>/lado/harness/trees/<sha256>/`, 매번 해시 확인·틀리면 다시 만듦, 임시 폴더 + rename), `CleanupHomes`, 채택 검증 `verifyHome`/`probeHome`/`checkHomeItem`. 오류는 500 `LADO_SETUP_HOME_FAILED` details `{stage}`(items·base·content·settings·entries·claude_json·record), 경로·원인은 데몬 로그에만.
  - `adopt.go`: `CheckHome = "home"`, `verifyAdopted` 가 Claude 항목마다 홈 검사. `managed.go`: 트리 파일 쓰기를 `writeTreeFiles` 로 빼서 관리 폴더와 트리가 같이 쓴다. `service.go`: Store 에 `ListLadoSetupGlobalBindings`·`ListLadoSetupBindings`·`LadoSetupVersionsForClientRequest`, Service 에 `homesMu`·`homesPrepared`.
  - `session_manager/lado_setup_home.go`(+81): `SetLadoSetupHome`, `applyLadoSetupHome(ctx, agent, id, env)`(Claude Code 일 때만, 세션 기록의 프로젝트·client request id, 결과는 `setProtectedEnv`), `isClaudeCodeAgent`(레지스트리의 Claude Code 인스턴스와 비교 — 비교할 수 없는 타입이면 false), `ladoProjectsLinkHolds`.
  - 훅 줄: `manager.go` 필드 1 + `prepareWorkspace` 맨 앞 4줄; `chat_spawn.go` 두 콜백(Chat 스폰·재개)에 각 4줄; `agent_switching_chat.go` 콜백 4줄; `agent_switching.go` `safeNativeTranscriptPath` 조건 1줄; `pkg/agentcreds/resolve.go` `claudeCredentialsDir`·`lookupEnv`.
- 홈 모양(`<state>/lado/homes/<id>/claude/`, 폴더 0700, 파일 0600·실행 0700): 상태 링크 12개(`projects`…`plugins`, `history.jsonl`; 기준에 없으면 기준에 빈 폴더·파일 0700/0600), `skills/ agents/ commands/ output-styles/ hooks/` 는 진짜 폴더에 기준 항목마다 링크 + 관리 항목(트리 → 링크, 파일 → 복사, 이름 겹치면 관리 항목; `agents/team/b.md` 처럼 깊은 이름은 기준의 `team` 링크를 그 안 항목 링크들로 펼친 진짜 폴더 안에), 최상위 `*.md` 는 기준 링크 + 관리 복사, `settings.json` = 기준 파일 그대로(로그인 키 포함) + 켜진 settings 키(env 는 기준 env 의 로그인 변수를 합침), `.claude.json` = 처음엔 기준 복사본, 그다음부터는 홈의 사본을 두고 `mcpServers` 만 맞춤(기준 서버 + 관리 서버, 관리 우선; 전에 LADO 가 넣었고 이제 안 주는 서버는 빼고, 세션이 더한 서버는 둠). 기록 `<state>/lado/homes/<id>/placed.json`(`lado-setup-home/1`: 놓은 항목 경로와 종류, 넣은 서버 이름).
- 실행 경로 표 (훅 = `prepareWorkspace` 맨 앞 / Chat 콜백 = `PrepareControllerEnv` 안, augment 뒤):

| 경로 | 지나는 호출 | 비고 |
|---|---|---|
| 스폰 TUI (`manager.go` Spawn) | `prepareWorkspace` | `prepareWorkerLaunchEnv` → augment → `ValidateLaunchAuth`(기준 로그인으로 확인, 같은 자격 증명) → 훅 → 트러스트 |
| 스폰 Chat (`launchChatController`, 비동기 스폰도 이 함수) | `prepareWorkspace`(env, 트러스트가 홈에) + Chat 콜백(컨트롤러 환경) | 드라이버는 콜백 환경으로 뜬다 |
| 복원 TUI (`RestoreWithMode` → `relaunchSession…`) | `prepareWorkspace` | |
| 복원 Chat (같은 함수 → `resumeChatController`) | Chat 콜백 | |
| 재개 (`ResumeAgentWithMode`, 깨어남 재연결·시작 점검의 `resumeChatController`) | TUI: `prepareWorkspace` / Chat: Chat 콜백 | 살아 있는 호스트에 다시 붙을 때는 콜백이 안 불린다(프로세스가 이미 홈 환경) |
| 에이전트 전환 TUI 대상 (`agent_switching.go:760`) | `prepareWorkspace`(target.env) | 원본 환경·`nativeConfigDir` 는 훅 전(기준 폴더) |
| 에이전트 전환 Chat 대상 (`agent_switching_chat.go`) | `prepareWorkspace`(targetSetupEnv) + Chat 콜백 | |
| 전환 되돌리기 (`agent switch rollback` → `relaunchSession`) | 복원과 같음 | |
| 인터페이스 전환 TUI↔Chat (`startTransitionTarget` → `relaunchSessionWithPolicy`) | 대상 TUI: `prepareWorkspace` / 대상 Chat: Chat 콜백 | `preflightInterfaceTarget` 의 인증 확인은 훅 전 |
| 계정 이동 (같은 경로, `setAccountOverride`) | 위와 같음 | 기준이 새 계정 폴더로 바뀌면 상태 링크를 다시 건다 |
| 백그라운드 작업 (`runRecordBackgroundTask`, 제목·제안) | 안 지남 | 작업자 세션이 아니라 임시 폴더의 한 번짜리 호출(세션 hook 도 끔). 채택된 항목 없이 기준으로 돈다 |
| 리뷰어 (`reviewer` 런처) | 안 지남 | 플랜대로 8단계 몫 |

- augment 순서: 모든 경로에서 `augmentAgentRuntimeEnv` 가 훅보다 앞이다 — `prepareWorkspace` 를 부르는 곳은 다 그 전에 augment 하고(이 단계 전 줄 번호로 manager 스폰 1436→1448, 복원 3518→3529, chat_spawn 231→237, 전환 1391/agent_switching_chat 43), Chat 콜백은 `prepareChatControllerEnv` → augment → 훅 순서. 그래서 자기 로그인 없는 컴퓨터의 setup-token 주입(`CLAUDE_CONFIG_DIR` 가 없을 때만)이 오늘처럼 일어난다.
- 자격 증명을 세션 환경으로 찾는 AO 코드(확인한 것):
  - `pkg/agentcreds` `readCredentialsFile` → **고침**: `claudeCredentialsDir` 가 `CLAUDE_SECURESTORAGE_CONFIG_DIR`(정의돼 있으면 그 값, 빈 값이면 `~/.claude`)를 먼저 보고, 없으면 지금처럼 `claudeConfigDir`. 정의 여부는 launch 환경(`CommandEnv`, `WithClaudeSettings` 도 먼저 읽는 곳) → 주입된 Env(빈 값은 정의 안 됨으로) → 프로세스 환경 순. 이 함수를 지나는 것: `claudecode` `ValidateLaunchAuth`(스폰·복원·전환·인터페이스 전환·claudeacp 드라이버 `Launch`), `AuthStatus…`, `ProviderCatalogFingerprint`·모델 목록, `AugmentRuntimeEnv`(홈 전이고 `CLAUDE_CONFIG_DIR` 있으면 안 부름). 계정 세션은 두 변수가 같은 폴더라 동작이 그대로다.
  - `pkg/agentcreds` `ResolveClaudeSettings` → 홈의 `settings.json` 을 읽는다 — Claude 가 읽는 파일이 그것이라 그대로가 맞다(고치지 않음).
  - `claudecode` `claudeConfigDir()`·`claudeLocalAuthStatus`(`~/.claude.json`) → 데몬 환경만 본다(세션 환경 아님), 그대로.
  - `claudecode` `NativeConversationExists`·`NativeSessionConfigDir` → 대화 기록(`projects`)만 본다 — 홈의 `projects` 링크로 기준 쪽이 보인다, 그대로.
  - `agentcreds` `readKeychain`(macOS) → 접미사 없는 서비스 이름만 읽는다(업스트림 그대로; 자기 로그인 홈은 `""` 라 같은 항목). `service/usage` 수집기 → 데몬 환경의 실제 `projects/`, 그대로.
- 엔트리와 다르게 한 것과 이유:
  1. **Chat 은 `prepareWorkspace`·`resumeChatController` 본문이 아니라 컨트롤러 환경 콜백 세 곳에서도 훅을 부른다**(Before 2). 엔트리대로 `resumeChatController` 의 `env` 에만 넣으면 드라이버가 그 환경을 콜백 환경으로 갈아 끼워 홈이 프로세스에 닿지 않는다. Chat 스폰·Chat 전환도 같은 이유(그 둘의 `prepareWorkspace` 호출은 홈을 만들고 트러스트를 홈에 쓰는 몫). 콜백은 augment 뒤라 위 순서도 지킨다. 그래서 엔트리 목록에 없던 `agent_switching_chat.go` 를 4줄 고쳤다(G5 예산 목록 밖, S10-G5 누적 예산엔 들어 있음). 컨트롤러를 다시 만들 때도(재구성·포크) 콜백이 다시 불려 홈을 다시 맞춘다.
  2. `.claude.json` 맞추기: 처음만 기준 복사본, 그 뒤엔 홈의 사본을 두고 서버만 맞춘다(플랜 결정 b "Claude 가 바꾼 것은 홈에 남는다"). 홈 사본이 깨졌으면(JSON 객체 아님·링크) 기준에서 다시 복사; 기준이 잠깐 깨졌으면 홈 사본이 있을 때는 서버를 그대로 두고 경고, 없으면 `claude_json` 단계 오류.
  3. 기준 찾기 세부: Env 에 `CLAUDE_CONFIG_DIR` 가 있고 보안 저장소 변수가 없으면 그 값(쓴 그대로)을 보안 저장소로 — Claude 가 그 폴더에 로그인을 두기 때문. 상대 경로 `CLAUDE_CONFIG_DIR` 이거나 이미 LADO 홈을 가리키면 홈을 만들지 않고 그대로 띄운다(경고 로그). Windows 는 변수 이름 대소문자를 무시하고 찾는다.
  4. 정리의 경합 막기: `PrepareClaudeHome` 이 세션마다 준비 시각을 기록하고, `CleanupHomes` 는 10분(`homeGrace`) 안에 준비된 홈은 세션이 끝난 것으로 읽혀도 남긴다 — 복원은 홈을 만든 뒤에야 세션을 살아 있음으로 바꾼다. 결정과 삭제는 같은 잠금 안.
  5. 놓기 규칙: LADO 가 놓지 않은 항목(기록에 없음)은 링크(잃는 게 없음)와 빈 폴더만 비켜 준다; Claude 가 만든 파일·폴더는 그대로 두고 경고. `settings.json` 은 기록과 상관없이 늘 LADO 것(세션이 `/model` 등으로 고쳐도 다음 실행에 다시 만든다). 낡은 항목 지우기는 중간 폴더가 링크면 건너뛴다(링크 너머 기준을 지우지 않게). 실패하면 이전 기록과 합친 기록을 남겨 다음 실행이 치울 수 있게 했다.
  6. 이름 확인: instructions 는 `*.md` 파일 이름 하나, 중첩 없는 폴더(skills·output-styles·hooks)는 한 단계, agents·commands 는 슬래시 경로; `..`·빈 단계·역슬래시는 거부(`content` 단계). 세션 id 는 `[A-Za-z0-9][A-Za-z0-9._-]*`.
  7. 필수 밖 테스트 보강: `TestClaudeHomeSecureStorageFollowsTheLogin` 에 계정의 빈 값·보안 변수 없는 폴더·Windows 대소문자·LADO 홈을 가리키는 환경, `TestAccountLinkerCreatesNothingAfterAdoption` 에 링커가 거는 이름 = 홈의 상태 목록 + 항목 폴더(목록 고정), `TestHomesOfEndedSessionsAreRemoved` 에 복원 중 유예와 유예 뒤 삭제, `TestHomeReconcileKeepsSessionFiles` 에 Project force_off/force_on 과 다른 프로젝트, agentcreds `TestCredentialsFileFollowsSecureStorageDir`(설정·빈 값·없음).
- 확인한 명령과 결과: `gofmt -l`(바뀐 패키지) 출력 없음; `go vet` 4 패키지 통과; 기존 `TestAdoption|TestRestore|TestDrift` 통과(채택 검증에 홈 검사가 붙은 뒤에도 모든 검사 OK — `TestAdoptionNeverTouchesCredentials` 가 전부 채택하고 검증 OK 를 확인, 실제 폴더 쪽 `projects`·`history.jsonl` 그대로); `go test ./pkg/agentcreds/` 통과.
- 게이트(HEAD `db0986c70`, 엔트리 미커밋 상태): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 8/8`, G2 `GO_TESTS_OK 3/3`, G3 `GO_RACE_OK`(32 s), G4 `GO_PKG_OK`(40 s), G5 `BUDGET_OK 88/140`(manager 11, chat_spawn 9, agent_switching 3, agentcreds 65), G6 `NO_HOME_LOOKUPS`, G7 `GO_BUILD_OK`. G8 은 이 엔트리 커밋 뒤 `gate-check.sh --run` 으로 전건 확인한다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 세션 설정 홈(상태 링크·합친 폴더·settings·`.claude.json`·실행은 고정 버전), session_manager 훅(`prepareWorkspace` 맨 앞, Chat 재개), 대화 기록 확인, 자격 증명 읽는 곳, 홈 정리, 채택 검증의 홈 검사, 필수 테스트 11개(+ agentcreds 1개). Codex·스냅숏 쓰기·데몬 배선은 없다. 엔트리 밖은 After 의 "다르게 한 것" 1~7.
- 남은 틈:
  - 홈에 링크하지 않는 기준 항목: 상태 목록·항목 폴더·`*.md`·settings·`.claude.json` 밖의 것(`keybindings.json`, `rules/`, `statsig/` 등)은 홈에 없다 — AO 계정 폴더와 같은 목록이라 계정 세션도 지금 그렇다. 자기 로그인 세션은 채택 뒤부터 사용자 키 바인딩 등을 잃을 수 있다. 넣으려면 `claudeStateDirs` 한 줄(소유자 결정).
  - 세션이 홈 안의 관리 사본(`CLAUDE.md` 메모리 편집, `settings.json` 의 `/model`)을 고치면 다음 실행에 되돌아간다 — 변경 감지는 관리 폴더와 원래 위치만 본다. 트리 폴더는 세션끼리 같은 것을 링크하므로 한 세션이 그 안에 쓰면 다음 실행의 해시 확인 전까지 다른 세션에도 보인다(그때 다시 만든다).
  - 기록(`placed.json`)을 잃으면 그 전에 놓은 일반 파일을 "Claude 것"으로 보고 남긴다(꺼진 항목의 사본이 남을 수 있음) — 실패 땐 합친 기록을 남겨 줄였다. LADO 가 만든 폴더 자리에 링크가 와야 하면 그 폴더를 통째로 지운다(그 안에 Claude 가 만든 것도).
  - 백그라운드 작업(제목·제안)은 채택 항목 없이 돈다. 프록시처럼 채택된 `env` 가 있어야 API 에 닿는 환경이면 제목 생성만 실패할 수 있다.
  - 큰 트리(스킬 안 `node_modules`)는 실행마다 해시한다. Windows: 심링크(개발자 모드)·대소문자 외는 9단계 실측 몫, 모드 비교는 Windows 에서 건너뛴다.
  - 같은 세션의 홈을 동시에 두 번 맞추는 경로는 없다고 보고 세션별 잠금은 두지 않았다(트리 만들기만 rename 으로 경합에 안전).
- 다음 단계 메모:
  - **S10(Codex)**: 항목 고르기는 `s.sessionItems(ctx, domain.LadoSetupToolCodex, projectID, clientRequestID)` 를 그대로 — 두 번째 값이 false 면 live Codex 항목이 없는 것(덧씌우기 없음). 실행이면 고정 버전(지운 항목 포함), 아니면 Global·Project 바인딩. `agent_switching_chat.go` 는 이 단계가 4줄 썼다(S10-G5 누적에 들어감).
  - **S11(정책)**: 실효 setup 의 바인딩 해석은 `offForProject`(Project 가 Global 을 이김, 행 없음 = 켜짐)와 같아야 한다 — 태스크 범위를 더할 때 같은 순서로(Global → Project → Task). 실행 홈은 `LadoSetupVersionsForClientRequest` 만 보므로 스냅숏이 고정한 것이 곧 홈 내용이다.
  - **S12(변경)**: 바인딩·버전이 바뀌면 다음 실행(또는 Chat 컨트롤러 재구성)에 홈이 맞춰진다 — 도는 세션은 그대로(결정 c). 홈 쪽에 할 일 없음.
  - **S13(배선)**: 훅 설치 `sessMgr.SetLadoSetupHome(setupSvc)`(게이트 설치와 같은 자리, 스폰·복원 전). 정리 `setupSvc.CleanupHomes(ctx)` — 데몬 시작(첫 `Scan` 뒤, 백그라운드, 실패는 로그)과 검사 뒤; `Deps.Sessions` 가 없으면 오류. 홈 위치 `<state>/lado/homes/<session-id>/claude/`(+ 옆에 `placed.json`), 트리 `<state>/lado/harness/trees/<sha256>/`, 채택 검증 임시 폴더 `<state>/lado/verify-home-*`(끝나면 지움). `Deps.ClaudeDirFromEnv`·`GOOS` 를 데몬이 넘겨야 보안 저장소 값이 맞다(macOS `""`). 오류 코드 `LADO_SETUP_HOME_FAILED`(500, details `stage`)는 스폰·복원 오류로 감싸여 나간다 — HTTP 매핑이 `errors.As(*apierr.Error)` 로 꺼내는지 확인.
  - **S14(번역)**: 코드 `LADO_SETUP_HOME_FAILED`, details 키 `stage`(items·base·content·settings·entries·claude_json·record), 채택 검증 검사 이름 `home`.
  - **S17(문서)**: TDD §1.2 훅 줄 표에 `session_manager` 의 Chat 콜백 세 곳(`chat_spawn.go` 둘, `agent_switching_chat.go`)과 `prepareWorkspace`, `agent_switching.go` 대화 기록, `pkg/agentcreds`; §9 에 위 실행 경로 표와 "Chat 드라이버는 콜백 환경으로 뜬다", 홈 모양과 `placed.json`, 백그라운드 작업은 홈 없음; 남은 틈의 첫 항목(링크 안 하는 기준 항목)은 소유자 확인 사항으로.
