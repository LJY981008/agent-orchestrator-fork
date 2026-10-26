# 2026-10-26 LADO 로컬 설정 채택 — S07 채택: 미리보기·전제 조건·백업·복사·제거·기록

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 e·f·g·h, §7) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-011 의 본체: 고른 항목을 백업하고, 관리 폴더와 blob 으로 옮기고, 원래 자리에서 지운다. 사용자의 실제 파일을 지우는 유일한 코드이므로 순서가 전부다 — 백업과 매니페스트를 다시 읽어 검증하기 전에는 아무것도 지우지 않고, 자격 증명과 앱 상태는 절대 건드리지 않는다(PRD gate 14 의 앞 절반, HARNESS §15 항목 9).

## 범위

- 만든다: `service/ladosetup/adopt.go`(미리보기·적용 흐름), `backup.go`(백업·매니페스트·검증), `managed.go`(관리 폴더 쓰기와 blob·트리 저장), `jsonedit.go`(순서를 지키는 JSON 객체 키 빼기·넣기), `tomledit.go`(`[mcp_servers.<이름>]` 블록 줄 단위 빼기·넣기), `rewrite.go`(hook 경로 다시 쓰기 — S06 의 참조 함수 사용), 테스트.
- 하지 않는다: 복원·변경 감지(S08), 세션 전달(S09·S10), 데스크톱 확인 검증(S13 컨트롤러 — 이 단계의 서비스는 호출자가 넘긴 증명 결과 `Presence func(action, subject) (proof, error)` 를 부른다, `ladopolicy` 의 꼴).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 요청: `AdoptRequest{Entries []{Tool, Kind, Name, Action ("move"|"keep")}, PlanSHA256 string, DryRun bool, Presence}`. `keep` 은 미리보기에만 보이고 아무것도 하지 않는다.
2. 미리보기(플랜 §7.1): 다시 검사(S05) → 항목마다 adoptable 인지(아니면 400 `LADO_SETUP_NOT_ADOPTABLE` + details `{tool, kind, name, reason}`) → 관리 경로, 전달 방식(`home`|`overlay`), hook 다시 쓰기 목록(전·후 명령), 추가될 충돌(S06), 직접 실행 경고 문구 코드, 전제 조건 결과(OS·AO 세션·바깥 프로세스 목록), `planSha256`(정규 JSON — 플랜 §7.1 의 모양, 항목은 tool·kind·name 순). 미리보기는 아무것도 쓰지 않는다.
3. 적용(플랜 §7.2-7.7) 순서를 코드 구조로 지킨다 — 단계마다 함수 하나, 앞 단계의 성공 결과만 다음 단계에 넘긴다:
   1. 다시 검사하고 `planSha256` 재계산 → 다르면 409 `LADO_SETUP_CHANGED`. 전제 조건 → 409 `LADO_SETUP_OS_UNVERIFIED` / `LADO_SETUP_TOOL_RUNNING`(details: 세션 id·프로세스 PID 와 이름). 데스크톱 확인(`Presence(setup.adopt, planSha256)`).
   2. 백업(`backup.go`): `<state>/lado/backups/<adoption-id>/files/<root>/<상대 경로>` 에 원본 바이트·mode·mtime 을 그대로, 키를 뺄 파일(`settings.json`, `~/.claude.json`, `config.toml`)은 통째로. `manifest.json`(플랜 §7.3 의 필드). 파일 fsync, 폴더 fsync. 그다음 백업 전체를 다시 읽어 sha256 을 원본·매니페스트와 대조 — 하나라도 다르면 오류로 끝(아무것도 지우지 않음). 권한 폴더 0700·파일 0600.
   3. 복사(`managed.go`): blob(`ladoblob`) — 파일 바이트, JSON 값(정규 JSON), 트리(파일마다 blob + 매니페스트 blob). 관리 폴더 `<state>/lado/harness/claude/` 와 `codex/` 를 그 버전들로 다시 쓴다(`settings.json` = 채택 키만, `mcp.json` = `{"mcpServers":{...}}`, `config.toml` = 채택 표만, 파일·폴더는 그대로; 실행 비트는 매니페스트의 `exec`). 쓴 뒤 해시 대조.
   4. 제거: 파일·폴더 항목 삭제(그릇 폴더는 남김 — 결정 f), 키 파일은 `jsonedit`/`tomledit` 로 채택 키만 빼고 같은 폴더의 임시 파일에 쓴 뒤 rename(원래 mode). `settings.json` 의 `env` 항목은 로그인 env 만 남긴다. 실패하면 그 자리에서 멈춘다.
   5. 기록: store `RecordLadoSetupAdoption`(채택 `completed`, 항목, 첫 버전 — actor 는 증명의 actor, 감사 `setup.adopted` payload: 항목 수, 도구별 수, 다시 쓰기 수, `processesIgnored`). 제거 중 실패면 그때까지 옮긴 항목으로 `partial` 과 오류 문장을 기록한다(복원 가능). 변경 감지 기준선(원래 위치에 남은 것의 해시)을 `<state>/lado/setup-baseline.json` 에 쓴다.
   6. 검증(구조): 임시 폴더에 자기 로그인용 홈을 S09 이전 최소 형태로 만들 수 없으므로, 이 단계에서는 관리 폴더의 모든 채택 항목이 기록된 해시와 같은지와 Codex 서버마다 TOML 인라인 값이 파싱되는지만 확인해 응답에 넣는다(S09 가 홈 검증을 더한다 — 다음 단계 메모).
4. 응답: 채택 id, 항목(새 item id), 다시 쓰기, 검증 결과, 경고. 도중 실패의 응답은 apierr(코드 `LADO_SETUP_ADOPTION_FAILED`, details `{stage, path}` — 경로는 사용자 폴더 안 상대 경로만).
5. `jsonedit`: 최상위 객체의 키 순서를 지키며 키를 빼고 넣는다(값의 원래 바이트는 `json.Indent` 로 2칸 들여쓰기), 마지막 줄바꿈 유지. `tomledit`: `[mcp_servers.<이름>]` 머리 줄부터 다음 표 머리 줄 직전까지와 `[mcp_servers.<이름>.<하위>]` 블록을 뺀다; 넣기는 파일 끝에 백업한 블록 텍스트를 붙인다. 둘 다 표 테스트.
6. Windows 를 위한 것: 경로는 `filepath`, 잠긴 파일 삭제 실패는 4 의 멈춤으로(테스트는 지우기 함수를 가짜로 바꿔 실패를 낸다).
7. 커밋 메시지 예: `feat(lado): adopt local setup with a verified backup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S07-adoption.md` 로 전건 MET 를 확인한다.

## 필수 테스트

모두 `t.TempDir()` 의 가짜 홈과 실제 store(`sqlitetest.MustOpen`) 또는 가짜 store 로:

- `TestAdoptionPreviewHashesThePlan` — 같은 선택 → 같은 해시, 파일 하나 바뀜 → 다른 해시, adoptable 아님 → 400, 미리보기는 아무것도 쓰지 않음(홈·state 폴더 목록 그대로)
- `TestAdoptionBacksUpBeforeRemoving` — 제거 함수가 불릴 때 매니페스트 검증이 이미 끝남(순서 기록), 백업 검증을 깨뜨리면(바이트 하나 바꿈) 원본은 그대로·기록 없음
- `TestAdoptionNeverTouchesCredentials` — `.credentials.json`, `auth.json`, `projects/`, `history.jsonl`, `~/.claude.json` 의 다른 키가 바이트·mtime 그대로
- `TestAdoptionRemovesOnlyAdoptedKeys` — `settings.json` 의 나머지 키 순서·값, 로그인 키·로그인 env 유지, `~/.claude.json` 의 다른 서버·키 유지, `config.toml` 의 주석·다른 표 유지
- `TestAdoptionKeepsContainers` — 모든 스킬을 채택해도 `skills/` 폴더는 남음(빈 폴더)
- `TestAdoptionRewritesHookPaths` — `~/.claude/hooks/x.sh` 가 `"$CLAUDE_CONFIG_DIR"/hooks/x.sh` 로, 미리보기에 전·후, 다시 쓰지 못한 참조는 blocking 으로 거부
- `TestAdoptionRefusesWhilePreconditionsFail` — 미검증 OS, 살아 있는 AO 세션, 바깥 프로세스 각각 409 와 목록, dev 무시면 프로세스만 통과하고 감사에 표시
- `TestAdoptionPartialFailureLeavesRestore` — 제거 셋째 항목에서 실패 → `partial`·오류 문장, 앞 두 항목만 관리됨, 셋째 원본은 그대로
- `TestAdoptionPlanMismatchWritesNothing` — 미리보기 뒤 파일이 바뀌면 409 `LADO_SETUP_CHANGED`, 백업 폴더도 안 생김
- `TestJSONEditKeepsOrder`, `TestTOMLEditRemovesOnlyTheTable` — 위 5 의 표 테스트

## 수용 게이트

- [ ] S07-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S07-G1: 필수 테스트 11개가 격리된 홈에서 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestAdoptionPreviewHashesThePlan TestAdoptionBacksUpBeforeRemoving TestAdoptionNeverTouchesCredentials TestAdoptionRemovesOnlyAdoptedKeys TestAdoptionKeepsContainers TestAdoptionRewritesHookPaths TestAdoptionRefusesWhilePreconditionsFail TestAdoptionPartialFailureLeavesRestore TestAdoptionPlanMismatchWritesNothing TestJSONEditKeepsOrder TestTOMLEditRemovesOnlyTheTable
  EXPECT: GO_TESTS_OK 11/11
- [ ] S07-G2: 패키지 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S07-G3: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S07-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S07-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 매니페스트 모양, 관리 폴더 쓰기 함수, 기준선 파일, S09 의 홈 검증 자리)
