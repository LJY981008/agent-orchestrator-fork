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
- [ ] S18-G7: agentcreds 업스트림 파일은 main 대비 한두 줄 호출만 남는다(로직은 LADO 파일)
  CHECK: n=$(git diff --numstat main HEAD -- backend/pkg/agentcreds/resolve.go | awk '{print $1+$2}'); [ "${n:-0}" -le 12 ] && echo "AGENTCREDS_SMALL ${n:-0}"
  EXPECT: AGENTCREDS_SMALL
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

- 시작 HEAD `6996e6710`(feat/lado-local-setup, 트리 깨끗). Ubuntu, go1.27.1, Node v24.21.0. 실제 `~/.claude`·`~/.claude.json`·`~/.codex` 는 읽지도 쓰지도 않음(테스트는 `t.TempDir()` 홈 + 격리 HOME).
- 리뷰 항목의 지금 위치(코드로 다시 찾음):
  - D1·B1: `home.go` `homePlan` 은 `claudeStateDirs`+`history.jsonl`+최상위 `*.md`+항목 폴더 다섯만 홈에 둔다 — `keybindings.json`·`rules/`·알 수 없는 폴더는 홈에 없음(S09 회고 첫 틈). 항목 폴더는 관리 항목이 없어도 늘 합친 진짜 폴더. `CleanupHomes` 는 `os.RemoveAll` 뿐이라 합친 폴더 안 세션이 만든 파일, 홈 `.claude.json` 에 `claude mcp add -s user` 한 서버, 홈 사본 `CLAUDE.md` 의 수정이 사라진다. `place` 도 LADO 가 만든 폴더 자리에 링크·파일을 놓을 때 `RemoveAll` 로 그 안의 세션 파일을 지운다.
  - B2: `changes.go` `prepareEdit`·`prepareDelete` 에 변경 감지 확인 없음 — 편집 뒤 `syncManaged` 가 관리 사본을 덮고, 삭제는 `removeManaged`.
  - B7: `adopt.go` `Adopt` = 쓰기 잠금 → `plan`(다시 검사) → `confirm`(해시·전제 조건 → 증명) → 백업 → 복사 → 제거 → `record`(채택·항목·버전·증명·감사 한 트랜잭션). 증명이 다시 검사 뒤라 60 s 만료 위험, 재사용 증명은 제거 뒤 `record` 에서야 거부(되돌리기에 기댐), 제거 중 데몬이 멈추면 행이 없어 복원 불가, `putBack` 실패는 로그만.
  - B8: `sessionItems` 는 live 항목이 없으면 고정 버전을 보기 전에 전달 없음, 고정이 비면 빈 홈.
  - B9: 홈 기록(`placed.json`)에 기준이 없어 계정이 바뀌어도 `.claude.json` 사본(이전 계정의 `oauthAccount`)이 남음.
  - B10: `agent_switching.go` `safeNativeTranscriptPath` 의 LADO 줄이 모든 설정 폴더(계정 폴더 포함)에서 링크된 `projects` 를 따라감.
  - B11: `restore.go` `restoreTarget` 이 `deleted_at` 항목도 복원.
  - B12: `claudeDir`·`codexHome` 이 `os.ReadDir` 로 링크된 뿌리를 따라가 그 아래를 adoptable 로 올림; `~/.claude.json` 링크는 서버마다 `symlink`.
  - nit: `pkg/agentcreds/resolve.go` main 대비 39+1 줄. `lado_gate.go` 는 S10 이 `projectID` 가드를 빼서 프롬프트를 만들 때마다 `GetSession`.
  - lint: `session_manager/lado_setup_home_test.go:176` 변수 `real`(revive redefines-builtin-id).

## After — 수행 후 실측

- 커밋: `1d398dfd4` fix(lado): record adoptions before removing originals · `9cd7992cf` fix(lado): keep what sessions create and pass the rest of the folder through · `e71e23eef` refactor(lado): keep LADO's credential and prompt hooks small · `51c40d769` fix(lado): give an unpinned execution only the setup made after its rules · 이 엔트리 커밋.
- 항목마다 고친 것:
  - D1(`home.go` `homePlan`·`homeOwn`): 기준의 모든 항목을 링크하고 `.credentials.json`·`settings.json`(작성)·`backups/`·`.claude.json`·`.claude.json.*` 만 뺀다. 항목 폴더는 그 종류에 세션이 받는 관리 항목이 있을 때만 합친 진짜 폴더, 없으면 기준 폴더 링크 하나(기준에 없으면 상태 항목처럼 먼저 만듦). 목록 고정 없음.
  - B1(새 `home_rescue.go`): `rescueHome`(정리 때)·`rescueReplaced`(다시 맞추기가 LADO 항목을 바꾸거나 `removeStale` 이 치우기 전). (a) 기록에 없는 일반 파일·폴더 → 기준 같은 자리, 차 있으면 `<이름>.lado-restored`(`besidePath`), 같은 파일 시스템이면 hard link·rename, 아니면 복사 → 해시 확인 → `placeStaged` → 원본 지움; (b) 홈 `.claude.json` 서버 중 기록(`Servers`)에 없는 것 → 기준 `.claude.json` 에 없을 때만 `jsonWithMCPServer`, `replaceFile`(임시+rename, 원래 mode, 링크면 쓰지 않음); (c) 기록에 새 `Copies`(키 → 항목 id·버전 sha) — 홈 사본 해시가 다르면 관리 사본이 최신 버전 그대로일 때 그 내용으로 바꿔 씀(→ `edited`), 아니면 `<관리 경로>.lado-session-<세션 id>`. 실패한 홈은 남기고 경고(다음 정리에 다시). 정리는 쓰기 잠금 안.
  - B2(`changes.go` `refuseOverDrift`): `editedDrift` 에 걸리면 편집·삭제(dry run 포함) 409 `LADO_SETUP_DRIFT_PENDING`(details `item`), 바인딩·수락은 그대로.
  - B7: (1) `adoptionApproval` — 항목 검사·`planSha256`·presence → `available` → 증명(서명·만료) → `LadoPresenceProofUsed`(재사용 → 403 REPLAYED), 모두 잠금·다시 검사 전에; (2) `begin` — 백업·복사 뒤, 제거 전에 `BeginLadoSetupAdoption`(pending 행+증명 한 트랜잭션, 재사용이면 아무것도 지우지 않음); (3) `record` — `CompleteLadoSetupAdoption`(pending → completed/partial, 항목·버전·감사), 하나도 못 지웠으면 `AbandonLadoSetupAdoption`(행 삭제); (4) `RecoverAdoptions` → `MarkInterruptedLadoSetupAdoptions`(남은 pending → partial, 문구 "Adoption was interrupted: …"), 데몬 `startLadoSetup` 첫 줄(쓰기 잠금 안이라 진행 중 채택과 겹치지 않음); 항목 없는 채택의 복원은 `removedOriginals`(매니페스트 move 항목 중 백업이 온전하고 자리가 빈 것 — 키는 `keyInPlace` 아님); (5) `putBack` 이 실패 경로(루트 기준 상대)를 돌려주고 `details.putBackFailed`·`state: "partial"`, 행은 `MarkLadoSetupAdoptionPartial`(모두 되돌리면 행 삭제). 마이그레이션 `0007` `state` CHECK 에 `pending`, 쿼리 4개(`CompleteLadoSetupAdoption`·`DeletePendingLadoSetupAdoption`·`MarkPendingLadoSetupAdoptionsInterrupted`·`CountLadoPresenceProofsByNonce`), `RecordLadoSetupAdoption` 은 Begin+Complete 를 한 트랜잭션으로(기존 store 테스트용).
  - B8(`sessionItems`): 실행이면 고정을 먼저 — 있으면 live 가 없어도 그것으로. 고정이 비면 아래 "다르게 한 것" 7.
  - B9: 기록에 `base`·`baseConfig`; 기준이 바뀌면 세션이 더한 서버를 이전 기준에 넣고(못 넣으면 새 사본으로 가져감) `.claude.json` 을 새 기준에서 다시 복사, 관리 서버 합치기는 그대로.
  - B10: `ports.LadoSetupHome.OwnsHome`(서비스: `<state>/lado/homes/<id>/claude` 와 정확히 같을 때), `safeNativeTranscriptPath` 는 업스트림으로 되돌리고 LADO 판정은 `Manager.ladoSafeNativeTranscriptPath`(lado_setup_home.go) — nil 훅이나 LADO 홈이 아니면 업스트림 그대로.
  - B11: `deleted_at` 항목은 `skipped_deleted`(키 묶음에서 빠져 파일 통째 복원도 일어나지 않음), 버전이 2 이상이면 `note: "kept_in_lado"`, 감사 payload `skippedDeleted`.
  - B12: `linkedRoots`(뿌리 자체만 Lstat; 링크된 Claude 폴더 안의 `.claude.json` 도) → adoptable 이던 줄은 `not_adoptable`/`root_is_link`, `findAdoptable` 이 409 `LADO_SETUP_ROOT_IS_LINK`(details tool·kind·name·root). 복원은 그대로(링크 너머로 되돌림).
  - nit: 로직을 `pkg/agentcreds/lado_securestorage.go`(+테스트 `lado_securestorage_test.go`)로, `resolve.go` 는 main 대비 1+1, `resolve_test.go` 는 main 과 같음. `lado_gate.go` 가드 되살림 + 세션 읽기 없앰.
  - lint: 위 테스트를 다시 쓰며 변수 `real` 없어짐(`base`).
- 엔트리와 다르게 한 것과 이유:
  1. `.claude.json.*` 는 복사하지 않고 홈 자기 것(잠금·백업 파일이라 복사하면 오히려 틀림).
  2. 구하기 대상에 홈 맨 위의 기록 없는 `*.md` 와, LADO 링크 자리를 세션이 원자적 쓰기로 바꾼 파일·폴더를 더함(둘 다 지우면 잃음). 대상 폴더가 기준에서도 폴더면 통째 `.lado-restored` 대신 항목 단위로 합침.
  3. 다시 맞추기 중 구하기가 실패하면 그 실행이 `LADO_SETUP_HOME_FAILED`(stage `entries`) — 세션 것을 지우고 뜨지 않게.
  4. B8 고정이 빈 실행: "지금 버전"이 아니라 스냅숏이 찍힌 뒤 만든 live 항목만(`LadoSetupPinnedAt`, 쿼리 `GetLadoPolicySnapshotCreatedAt`). 그 전부터 있던 항목을 고정하지 않았다면 그 태스크에서 꺼진 것이라 — 엔트리 그대로면 태스크가 모두 끈 실행이 다시 받는다. 아무것도 남지 않으면 홈 없음(업스트림 실행).
  5. 증명 재사용 사전 확인에 store 읽기 `LadoPresenceProofUsed` 를 더함(`begin` 의 삽입이 최종 판정).
  6. `kept_in_lado` 는 상태가 아니라 항목의 `note`(그 항목도 원래 바이트로 `restored` 되므로).
  7. `~/.claude.json` 자체가 링크면 서버마다 `symlink` 가 아니라 `root_is_link`(그 파일이 뿌리). `Root` DTO 에는 링크 표시를 넣지 않음(API 변경 최소, 사유는 줄마다).
  8. DTO `LadoSetupAdoptionResponse.state` enum 에 `pending` → `npm run api`(openapi.yaml +4−2, schema.ts +2−2), `npm --prefix frontend run typecheck` 통과.
  9. lado_gate 세션 읽기는 `ladotask.PromptSource.CodexSection` 으로 옮김: 하네스가 ""(행이 이름)일 때만 읽고, Codex 가 아니면 ""(그 함수는 원래 ClientRequestID 때문에 행을 읽음). 프로젝트 없는 세션은 다시 LADO 절이 없음 — S10 다르게 한 것 4 되돌림.
  10. B10 은 호출 5곳을 LADO 메서드로 바꿈(agent_switching.go main 대비 6+6 = 이 5줄 + S10 의 1줄).
- 뜻이 바뀌어 고친 기존 테스트: `TestAdoptionPutsBackWhenRecordFails`(재사용 증명은 이제 아무것도 쓰기 전에 거부 → 기록 실패 store 로 되돌리기 확인 + 행 없음), `TestChangeDryRunWritesNothing`(drift 있는 CLAUDE.md 편집은 409, 편집 dry run 은 `model`), `TestInventoryDoesNotAdoptThroughALinkedSettingsFile`(링크된 `~/.claude.json` → `root_is_link`), `TestLadoPromptSectionComesFromTheSource`·`TestLadoPromptSectionKnowsTheHarness`(가드·행 읽기 위치), `TestSafeNativeTranscriptPathFollowsProjectsLink`(LADO 홈만 링크 허용, 계정·LADO 없음은 업스트림), `TestLadoMigrateCreatesSetupTables`(pending 행), domain 문자열 표(`pending`·`root_is_link`), `codex_section_test.go`(하네스 "" 는 행이 정함).
- 테스트 순서: 필수 테스트를 실패부터 보지 못했다 — 구현을 먼저 하고 바로 뒤에 썼다(새 API 를 써서 이전 코드에서는 컴파일되지 않음). 처음 실행에서 `TestAdoptionRecordsPendingBeforeRemoving`(증명 action 빈 값 — 테스트 쪽)·`TestInterruptedAdoptionCanBeRestored`(뿌리 폴더 mtime 비교 — 비교 범위를 옮긴 경로로) 를 고쳐 통과.
- 확인한 명령과 결과: 격리 HOME `go test -count=1` — `./internal/service/ladosetup/`(전체), `./internal/session_manager/` `./pkg/agentcreds/` `./internal/storage/sqlite/store/` `./internal/service/ladotask/` `./internal/daemon/` `./internal/storage/sqlite/` `./internal/httpd/...` `./internal/domain/` `./internal/service/ladopolicy/` 모두 ok. lint: 게이트 패턴과 `./internal/daemon/...` `./internal/httpd/controllers/...` `./internal/ports/...` `./internal/domain/...` 모두 GO_LINT_OK(처음 `restore.go` wastedassign 1건 고침). `npm run sqlc` 두 번(쿼리 ASCII). typecheck 통과.
- 게이트: `gate-check.sh --run` 11/11 MET(HEAD `e71e23eef`), 엔트리 커밋 `5f10ca147` 뒤 다시 11/11 MET. 이 줄을 고친 커밋 뒤에 한 번 더 돌려 증거를 그 HEAD 에 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 전부(D1·B1·B2·B7 (1)~(5)·B8·B9·B10·B11·B12·nit 둘·lint), 필수 테스트 12개. 엔트리 밖은 "다르게 한 것" 2·3·4·5·7·9. 보안 묶음(S19)·프런트(S20)는 손대지 않음.
- 남은 틈:
  - 세션이 홈 `settings.json` 에서 바꾼 것(`/model` 등)과 트리 링크를 통해 쓴 것은 구하지 않는다(다음 실행에 덮이거나 다시 만듦 — S09 틈 그대로).
  - 기준 `.claude.json` 이 링크면 서버를 넣지 않는다(링크를 따라 쓰지 않음) → 그런 세션 서버가 있는 홈은 정리 때마다 남는다. 기준 쓰기와 Claude 직접 사용이 겹칠 수 있다(`replaceFile` 비교로 덮어쓰기만 막음).
  - 자리 확인과 rename·link 사이 경합은 `placeStaged` 와 같은 꼴(정리는 끝난 세션의 홈만, 기준은 직접 사용 중일 수 있음).
  - 기록에 기준이 없는 S18 이전 홈은 (a)(b) 를 건너뛰고 (c) 만 한다(병합 전 브랜치라 lab 홈뿐).
  - pending 에서 멈춘 채택이 쓴 관리 사본·blob 은 남는다(항목이 없어 전달 안 됨, 같은 이름의 다음 채택이 덮음).
  - 항목 없는 채택의 복원 대상은 "자리가 비었는지"로 고른다 — 그 사이 같은 이름을 다시 만든 것은 대상이 아니다(그대로 둠).
  - 고정이 빈 실행의 판정은 스냅숏 `created_at` 과 항목 `created_at`(같은 데몬 시계) 비교다.
  - 변경(changes)의 증명은 아직 dry run 계산 뒤에 확인한다(`changeApproval`) — 채택만 "증명 먼저".
- 다음 단계 메모:
  - **S19**: 채택 확인 순서 = `adoptionApproval`(항목·`planSha256`·presence 필수·`available`·증명 서명/만료·`LadoPresenceProofUsed`) → `lockWrite` → `plan`(다시 검사) → `confirm`(해시·`HOOK_PATH_MOVED`·OS·실행 중) → 백업 → 복사 → `begin`(pending+증명) → 제거 → `record`(Complete/Abandon/MarkPartial). 새 거절(미검증 OS 만들기·옮길 스크립트 참조·Codex args 비밀)은 `confirm`·`plan`·changes 쪽에 넣고, 증명은 다시 검사 앞에 둔 채로. 변경의 확인 순서를 같게 하려면 `Apply`→`changeApproval` 자리. 복원 조회 전제 조건은 `restoreTarget`(항목 없는 채택은 `removedOriginals`).
  - **S20**: 복원 보고서 `entries[].status` 새 값 `skipped_deleted`, `entries[].note` = `kept_in_lado`; 채택 오류 `LADO_SETUP_ADOPTION_FAILED` details `putBackFailed: [루트 기준 상대 경로]` + `state: "partial"`(stage `record`); 인벤토리 사유 `root_is_link`; 코드 `LADO_SETUP_ROOT_IS_LINK`(409, details tool·kind·name·root), `LADO_SETUP_DRIFT_PENDING`(409, details item — 편집기에서 변경 감지 검토로); 채택 `state` `pending`(진행 중), "interrupted" 문구의 `partial` 은 `itemCount` 0 이어도 복원 가능 — 카드가 0 이라고 복원을 숨기지 말 것. 복원 감사 payload `skippedDeleted`.
  - **S20 문서(as built)**: HARNESS §9.2 pending·증명 먼저·시작 때 interrupted, §9.4 홈은 나머지도 연결(예외 목록)·정리와 다시 맞추기 때 구하기(`.lado-restored`, `.lado-session-<id>`)·기준이 바뀌면 `.claude.json` 다시 복사, §9.7 복원은 지운 항목 건너뜀·`kept_in_lado`, §9 실행 고정(비면 스냅숏 뒤 항목만); TDD §1.2 훅 줄: `agent_switching.go` 호출 5곳·`lado_setup_home.go` 의 `ladoSafeNativeTranscriptPath`, `pkg/agentcreds/lado_securestorage.go`; 프로젝트 없는 세션은 LADO 절이 없음(S10 결정 4 되돌림).
