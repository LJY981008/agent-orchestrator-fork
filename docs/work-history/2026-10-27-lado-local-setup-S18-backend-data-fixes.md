# 2026-10-27 LADO 로컬 설정 채택 — S18 리뷰 반영(백엔드 ①): 데이터 보존·전달·확인 순서

> 체인 `docs/chains/lado-local-setup-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md` · 브랜치 `feat/lado-local-setup` · 소유자 승인 2026-10-27(수정 체인 S18~S20, "이대로 진행")

## 의도/목적 — 왜 이 작업을 하나

S01~S17 체인 뒤 리뷰(백엔드·프런트 읽기 전용 리뷰 2회)와 전체 CI 미러(17/18, lint 1건 실패)의 결과 중 **사용자 데이터를 잃거나 전달이 어긋나는 것**을 고친다. 소유자 결정(2026-10-27) 하나도 여기서 만든다: 자기 로그인 세션의 홈은 채택하지 않은 나머지 항목도 그대로 연결한다.

## 범위

- 고친다(리뷰 번호는 백엔드 리뷰 B, 위치는 리뷰 당시 HEAD 기준이므로 코드로 다시 찾는다):
  - **D1 소유자 결정 — 홈은 나머지도 연결**(`service/ladosetup/home.go`): 기준(자기 로그인의 실제 폴더 또는 계정 홈)의 **모든 항목**을 홈에 링크하되, 다음만 예외로 LADO 가 다룬다 — `.credentials.json`(링크 금지, 보안 저장 폴더 변수가 맡음), `backups/`(홈 자기 것), `.claude.json` 과 `.claude.json.*`(복사), `settings.json`(작성), 최상위 `*.md`(합침), 항목 폴더 `skills/ agents/ commands/ output-styles/ hooks/`(**그 종류에 켜진 관리 항목이 있을 때만** 합친 진짜 폴더, 없으면 기준 폴더로 가는 링크 하나 — 리뷰 B1 의 첫 고침). 그 밖의 링크 대상 목록을 고정하지 않는다(새 항목도 따라감). LADO 자신의 기록 파일(`placed.json`)은 홈 옆이라 해당 없음.
  - **B1 홈 정리 때 데이터 유실**: `CleanupHomes` 가 홈을 지우기 전에, 그리고 다시 맞추기(`buildHome`)가 LADO 것이 아닌 일반 파일을 치울 일이 생기기 전에 **구하기(rescue)**를 한다 — (a) 합친 항목 폴더 안에 기록(`placed.json`)에 없는 일반 파일·폴더(세션이 `/agents` 등으로 만든 것)는 기준의 같은 자리로 옮긴다(자리에 무언가 있으면 옆에 `<이름>.lado-restored`); (b) 홈 `.claude.json` 의 사용자 범위 `mcpServers` 중 기준에도 관리 항목에도 없는 서버(세션의 `claude mcp add -s user`)는 기준 `.claude.json` 에 **없을 때만** 넣는다(순서 보존 편집 `jsonWithMCPServer`, 임시 파일+rename, 원래 mode); (c) 세션이 바꾼 관리 사본(홈의 `CLAUDE.md` 등 — 해시가 그 버전과 다름)은 관리 폴더의 그 경로가 최신 버전 그대로일 때 그 내용으로 바꿔 써서 `edited` 변경 감지로 만들고, 관리 폴더가 이미 다르면 옆에 `<이름>.lado-session-<세션 id>` 로 둔다. (a)(b) 는 다음 검사에서 `new_local` 로, (c) 는 `edited` 로 보인다. 구하기에 실패한 홈은 지우지 않고 남긴다(경고 로그, 다음 정리에 다시).
  - **B2 편집이 유지 안 한 수정을 덮음**(`changes.go`): 관리 폴더에 그 항목의 `edited` 변경 감지가 있으면 그 항목의 편집과 삭제는 409 `LADO_SETUP_DRIFT_PENDING`(details `item`) — 먼저 유지 또는 되돌리기(바인딩·수락은 내용을 쓰지 않으므로 그대로 됨). dry run 의 "before" 는 관리 폴더의 지금 내용이 아니라 버전이므로 이 거부가 먼저다.
  - **B7 기록 실패와 확인 증명**(`adopt.go`, store, 마이그레이션): (1) 데스크톱 확인 증명의 서명·만료·재사용 여부를 **다시 검사하기 전에** 확인한다(증명 수명 60 s 안에 끝나게 — 프런트 리뷰가 짚은 만료 위험); (2) 원본을 지우기 **전에** 채택 행을 `pending` 으로, 증명 nonce 와 함께 한 트랜잭션에 넣는다(재사용은 여기서 거부되어 아무것도 지우지 않음); (3) 제거 뒤 `completed`/`partial` 로 마무리하며 항목·버전·감사를 넣는다; (4) 데몬이 시작할 때 남은 `pending` 은 `partial`(오류 "interrupted")로 바꾸고, 그런 채택은 매니페스트로 복원할 수 있어야 한다(항목이 없으면 매니페스트 항목 중 원래 자리가 비었고 백업이 있는 것을 복원 대상으로); (5) `putBack` 실패는 로그만이 아니라 오류 details(`putBackFailed: [경로…]`, 사용자 폴더 안 상대 경로)로 돌려준다. 마이그레이션 `0007_lado_setup.sql` 은 아직 병합 전이므로 그 파일의 `state` CHECK 에 `pending` 을 더하고 `npm run sqlc`, `TestLadoMigrateCreatesSetupTables` 를 맞춘다(새 마이그레이션을 만들지 않는다).
  - **B8 실행 고정 버전**(`home.go` `sessionItems`): LADO 실행이면 **고정 버전을 먼저** 찾는다 — 살아 있는 항목이 하나도 없어도 고정 버전으로 홈을 만든다. 고정이 비었는데(채택 전에 승인된 실행) 살아 있는 항목이 있으면 지금 버전으로 만들고 경고 로그(그 워커가 시작할 때 원래 자리에서 받던 것과 가장 가까움).
  - **B9 계정 이동 뒤 `.claude.json`**: 홈 기록에 기준 폴더 경로를 넣고, 기준이 바뀌면 `.claude.json` 을 새 기준에서 다시 복사한다(관리 서버 합치기는 그대로).
  - **B10 대화 기록 확인 범위**(`session_manager/agent_switching.go`): 링크된 `projects` 를 따라가는 허용은 설정 폴더가 LADO 홈일 때만. `ports.LadoSetupHome` 에 `OwnsHome(configDir string) bool` 을 더하고 nil 훅이면 업스트림 그대로.
  - **B11 복원이 LADO 에서 지운 항목을 되살림**(`restore.go`): `deleted_at` 이 있는 항목은 복원하지 않고 보고서에 `skipped_deleted` 로 싣는다. 채택 뒤의 편집은 원래 자리로 가지 않는다는 것도 보고서에 한 줄(`kept_in_lado`).
  - **B12 링크된 뿌리**(`scan_claude.go`, `scan_codex.go`, `adopt.go`): Claude 폴더·`~/.claude.json`·Codex 홈 자체가 심링크면 인벤토리에 사유 `root_is_link`, 채택은 409 `LADO_SETUP_ROOT_IS_LINK`(링크 너머 dotfiles 저장소를 지우지 않게). 복원은 막지 않는다.
  - **nit**: `pkg/agentcreds/resolve.go` 에 더한 약 40줄 로직을 같은 패키지의 LADO 파일(`lado_securestorage.go`)로 옮기고 업스트림 파일에는 한 줄 호출만; `lado_gate.go` 의 `projectID` 가드를 되살려 프롬프트를 만들 때마다 세션을 읽지 않게(세션 읽기는 Codex 지침이 필요할 때만).
  - **lint**: `session_manager/lado_setup_home_test.go:176` 의 변수 `real` 이름 바꾸기(revive redefines-builtin-id — CI lint 실패 원인).
- 하지 않는다: 보안 묶음(S19), 프런트(S20).

## 절차

0. 시작 전에 이 체인과 앞 체인(`docs/chains/lado-local-setup.tsv`)의 엔트리들, 특히 S07·S08·S09·S12·S13 의 `## 비교/회고` 와 "다음 단계 메모"를 읽는다(러너는 직전 커밋 요약만 넘겨 준다). 플랜 결정 a·b·c·f·n·o 를 다시 읽는다.
1. 위 항목마다 실패하는 테스트를 먼저 쓰고(아래 필수 테스트), 고친 뒤 통과를 본다. 기존 테스트가 이 변경으로 뜻이 바뀌면(예: 홈의 링크 목록 고정 테스트) 새 규칙으로 고치고 After 에 적는다.
2. 구하기(B1)는 사용자 폴더에 쓰는 새 경로이므로 채택·복원과 같은 안전 규칙을 따른다: 링크를 따라가지 않고, 덮어쓰지 않으며(`.lado-restored`), 쓴 뒤 해시를 확인하고, 실패는 그 홈을 남긴다.
3. 커밋 메시지 예: `fix(lado): keep what sessions create and pass the rest of the folder through`, `fix(lado): record adoptions before removing originals`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-local-setup-S18-backend-data-fixes.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

`service/ladosetup`(격리 홈):

- `TestHomePassesTheRestOfTheFolderThrough` — D1: 기준의 `keybindings.json`·`rules/`·알 수 없는 폴더는 링크, `.credentials.json` 은 없음, `backups/` 는 홈 것, 관리 항목이 없는 종류의 폴더는 기준으로 가는 링크 하나, 있는 종류만 합친 폴더
- `TestCleanupRescuesWhatSessionsCreated` — B1 (a)(b)(c): 세션이 만든 에이전트·MCP 서버·바꾼 CLAUDE.md 가 정리 뒤 기준·관리 폴더에 남고 다음 검사에 new_local/edited 로 보임, 자리가 차 있으면 `.lado-restored`, 구하기 실패면 홈이 남음
- `TestEditRefusesPendingDrift` — B2: edited 상태에서 edit·delete 409, 유지·되돌리기 뒤에는 됨
- `TestAdoptionRecordsPendingBeforeRemoving` — B7: 제거 함수가 불릴 때 `pending` 행과 nonce 가 이미 있음, 재사용 증명은 아무것도 지우지 않음, 증명은 다시 검사 전에 확인
- `TestInterruptedAdoptionCanBeRestored` — B7 (4): `pending` 으로 멈춘 채택이 시작 때 `partial` 이 되고 매니페스트로 복원됨
- `TestPutBackFailuresAreReported` — B7 (5)
- `TestExecutionHomeUsesPinsWithoutLiveItems` — B8 둘 다(고정만 있음, 고정 없음+살아 있음)
- `TestHomeRecopiesConfigWhenTheBaseChanges` — B9
- `TestRestoreSkipsItemsDeletedInLADO` — B11
- `TestLinkedRootsRefuseAdoption` — B12

`session_manager`:

- `TestSafeNativeTranscriptPathFollowsProjectsLink` — B10 로 바뀐 규칙: LADO 홈일 때만 링크 허용, 계정 폴더는 업스트림 그대로(기존 테스트를 고침)

`storage/sqlite`:

- `TestLadoMigrateCreatesSetupTables` — `pending` 상태 허용

## 수용 게이트

- [ ] S18-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S18-G1: ladosetup 필수 테스트 10개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestHomePassesTheRestOfTheFolderThrough TestCleanupRescuesWhatSessionsCreated TestEditRefusesPendingDrift TestAdoptionRecordsPendingBeforeRemoving TestInterruptedAdoptionCanBeRestored TestPutBackFailuresAreReported TestExecutionHomeUsesPinsWithoutLiveItems TestHomeRecopiesConfigWhenTheBaseChanges TestRestoreSkipsItemsDeletedInLADO TestLinkedRootsRefuseAdoption
  EXPECT: GO_TESTS_OK 10/10
- [ ] S18-G2: session_manager·마이그레이션 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/session_manager/ TestSafeNativeTranscriptPathFollowsProjectsLink && bash .claude/gate/lado-local-setup.sh go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesSetupTables
  EXPECT: GO_TESTS_OK 1/1
- [ ] S18-G3: ladosetup 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S18-G4: 바뀐 패키지 전체 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/session_manager/ ./pkg/agentcreds/ ./internal/storage/sqlite/store/ ./internal/service/ladotask/ ./internal/daemon/
  EXPECT: GO_PKG_OK
- [ ] S18-G5: lint 통과(CI 실패 원인 포함)
  CHECK: bash .claude/gate/lado-local-setup.sh go-lint ./internal/service/ladosetup/... ./internal/session_manager/... ./pkg/agentcreds/... ./internal/storage/sqlite/... ./internal/service/ladotask/...
  EXPECT: GO_LINT_OK
- [ ] S18-G6: sqlc 재생성이 커밋과 같고 쿼리 파일은 ASCII
  CHECK: bash .claude/gate/lado-local-setup.sh sqlc-clean && bash .claude/gate/lado-local-setup.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S18-G7: agentcreds 업스트림 파일은 한 줄 호출만 남는다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 12 backend/pkg/agentcreds/resolve.go
  EXPECT: BUDGET_OK
- [ ] S18-G8: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S18-G9: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S18-G10: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 리뷰 항목마다 코드에서 찾은 지금 위치와 재현)

## After — 수행 후 실측

(커밋, 항목마다 고친 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S19 가 이어받을 확인 순서·pending 채택, S20 이 보일 보고서 항목 `skipped_deleted`·`kept_in_lado`·`putBackFailed`·`root_is_link`·`LADO_SETUP_DRIFT_PENDING`)
