# 2026-10-26 LADO 로컬 설정 채택 — S03 도메인: 항목·버전·트리·바인딩·인벤토리 용어

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 e·i, §3) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

뒤 단계 전부가 같은 이름과 규칙을 쓰도록 도메인 타입과 순수 함수를 먼저 고정한다: 도구·종류·내용 종류, 폴더 항목의 정규 매니페스트(해시가 버전이 된다), 바인딩 규칙, 인벤토리의 상태·사유 코드, OS 검증표, 데스크톱 확인이 필요한 변경의 판정.

## 범위

- 만든다: `backend/internal/domain/lado_setup.go`, `backend/internal/domain/lado_setup_test.go`; `domain/lado_presence.go` 에 세 동작.
- 하지 않는다: 저장(S04), 검사·채택 로직(S05 이후).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `domain/lado_rule.go`·`domain/lado_queue.go` 의 꼴(문자열 타입, 상수, `Validate`, `CanonicalJSON`/`SHA256`)을 따른다.
2. 타입(플랜 §3):
   - `LadoSetupTool`(`claude-code`, `codex` — `domain.AgentHarness` 값과 같은 문자열), `LadoSetupKind`(`instructions`, `setting`, `skill`, `agent`, `command`, `output_style`, `hook_script`, `mcp_server`), `LadoSetupContentKind`(`file`, `json`, `tree`).
   - `LadoSetupItem`(id, tool, kind, name, origin path, adoption id, created/deleted/restored 시각)과 `Live()`; `LadoSetupItemVersion`(id, item id, version, content kind, sha256, size, executable, actor, created at); `LadoSetupAdoption`(플랜 §2 열; state `completed`/`partial`); `LadoSetupBinding`(item id, scope `global`/`project`/`task`, scope id, state)와 `Validate()`(global 은 scope id 비고 on/off, project·task 는 scope id 필수와 force_on/force_off); `LadoConflictAcceptance`.
   - `LadoSetupTree{Entries []LadoSetupTreeEntry{Path, Mode ("file"|"exec"), Size, SHA256}}` — `Normalize()`(경로 순 정렬, 슬래시 구분, 상대 경로만: 빈 경로·`..`·절대·역슬래시·중복 거부), `CanonicalJSON()`, `SHA256()`.
   - 인벤토리: `LadoSetupEntry`(Tool, Kind, Name, Location(`claude_dir`|`claude_json`|`codex_home`|`project`), Path, Size, SHA256, Tracked, Status, Reason, DefaultAction(`move`|`keep`), ItemID, ProjectID)와 상태 상수(`managed`, `adoptable`, `not_adoptable`, `never`, `not_managed`, `repo_tracked`), 사유 상수(플랜 §3 목록 그대로), 기본 동작 규칙 `LadoSetupDefaultAction(tool, kind, name)`(결정 e: settings 키는 목록 안이면 move, 밖이면 keep; 나머지 move).
   - 변경 감지: `LadoSetupDrift{ItemID, Kind ("edited"|"new_local"), Tool, ItemKind, Name, Path, SHA256}`.
   - `LadoSetupDeliveryVerified(goos string, tool LadoSetupTool) bool` — `linux` 는 두 도구 다 true, 나머지 OS 는 false(플랜 결정 4; S01 결과가 다르면 그 결과를 따르고 After 에 적는다).
   - `LadoSetupNeedsPresence(kind LadoSetupKind, name string) bool` — 결정 i: `hook_script`, `mcp_server`, 그리고 `setting` 의 `hooks`, `permissions`, `env`, `statusLine`, `enabledPlugins`, `extraKnownMarketplaces`.
   - 로그인 키: `adapters/agentaccount/claude.go:32-41` 과 같은 값을 도메인 상수 `LadoSetupClaudeLoginKeys`(최상위 키)·`LadoSetupClaudeLoginEnv`(env 이름)로 둔다(domain 은 adapters 를 import 하지 않는다). 두 목록이 같은지는 S05 가 agentaccount 쪽 값과 비교하는 테스트로 묶는다.
3. `domain/lado_presence.go`: `LadoPresenceSetupAdopt = "setup.adopt"`, `LadoPresenceSetupRestore = "setup.restore"`, `LadoPresenceSetupChange = "setup.change"`(기존 상수 이름 꼴을 따른다).
4. 커밋 메시지 예: `feat(lado): domain types for adopted local setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S03-domain.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoSetupTreeIsCanonical` — 순서가 달라도 같은 JSON·해시, 내용 하나가 바뀌면 다른 해시, 잘못된 경로 6가지 거부
- `TestLadoSetupBindingValidate` — 허용 4조합, 거부 조합(global force_on, project on, 빈 scope id 등)
- `TestLadoSetupItemLive` — 지움·복원 시각이 있으면 live 아님
- `TestLadoSetupDeliveryVerified` — linux 두 도구 true, windows·darwin·freebsd false
- `TestLadoSetupNeedsPresence` — 결정 i 의 표 전부(필요/불필요 양쪽)
- `TestLadoSetupDefaultAction` — 결정 e 의 move 목록과 keep(예 `theme`, `verbose`), 나머지 종류는 move
- `TestLadoSetupCodesAreStable` — 상태·사유·종류·동작 문자열을 그대로 고정(렌더러가 쓰는 값)

## 수용 게이트

- [ ] S03-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S03-G1: 필수 테스트 7개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/domain/ TestLadoSetupTreeIsCanonical TestLadoSetupBindingValidate TestLadoSetupItemLive TestLadoSetupDeliveryVerified TestLadoSetupNeedsPresence TestLadoSetupDefaultAction TestLadoSetupCodesAreStable
  EXPECT: GO_TESTS_OK 7/7
- [ ] S03-G2: 데스크톱 확인 동작 세 개가 도메인에 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has backend/internal/domain/lado_presence.go '"setup.adopt"' '"setup.restore"' '"setup.change"'
  EXPECT: HAS_OK
- [ ] S03-G3: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S03-G4: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `2825c31c3`(`docs(lado): record S02`), 트리 깨끗. 태그 `lado-step7-chain-def` = `0e7bf9ee8`.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 toolchain, PATH 맨 앞), umask 0022, TZ=UTC, LANG=C.UTF-8. golangci-lint v2.13.2 는 rules-core 세션 스크래치의 빌드(`GOPROXY=off`).
- 출발점: `backend/internal/domain/` 에 `lado_setup*.go` 없음, `LadoSetup` 이름 0개. `lado_presence.go` 의 동작 상수는 `plan.approve`, `project.lado_mode_off`, `rule.protected_change` 셋. `go test -count=1 ./internal/domain/` ok(0.1 s).
- 꼴: `lado_rule.go`(문자열 타입 + 상수, `Normalize`·`CanonicalJSON`(`SetEscapeHTML(false)`, 끝 줄바꿈 뺌)·`SHA256`, 타입 id `LadoRuleID`·`LadoRuleVersionID`, 범위 `LadoRuleScope`·바인딩 상태 `LadoBindingState`), `lado_queue.go`(`Validate` 는 센티널 `ErrLado…Invalid` 를 `%w` 로 감쌈, NULL 시각은 zero). 문제 꼴은 `domain.LadoPolicyProblem`, 문제 코드 상수는 `service/ladopolicy/resolve.go` 에 있다.
- 로그인 키 원본: `adapters/agentaccount/claude.go:32-41` — `claudeSettingsDropped` = `apiKeyHelper, awsAuthRefresh, awsCredentialExport, forceLoginMethod, forceLoginOrgUUID`, `claudeCredentialEnv` = `ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_BASE_URL, CLAUDE_CODE_USE_BEDROCK, CLAUDE_CODE_USE_VERTEX, CLAUDE_CODE_USE_FOUNDRY`(둘 다 내보내지 않음).
- 앞 단계 메모 중 이 단계 몫: S01 결과는 플랜 결정 4 와 같다(Linux 에서 두 도구 모두 전달 확인) → `LadoSetupDeliveryVerified` 는 엔트리대로. S02 메모: gen 모델 `LadoSetupAdoption`·`LadoSetupItem`·`LadoSetupItemVersion`·`LadoSetupBinding`·`LadoConflictAcceptance`(열 `os` → `Os`, `executable` → `int64`, `adoption_id` → `sql.NullString`, 끝 시각 → `sql.NullTime`), 전역 바인딩은 `scope_id = ''` 이어야 CHECK 를 지난다 — 도메인 타입은 이 열을 모두 담고, NULL 은 zero 값으로 둔다.
- 뒤 단계가 쓰는 이름(엔트리 S04·S05·S07·S08·S11·S12 에서 확인): S04 의 `SetLadoSetupBinding(ctx, b, inherit bool, …)`(inherit 는 따로 받음), S05 의 상태·사유 짝(`never/login_key`, `not_managed/unknown_location`, `not_adoptable/inline_table` …), S07 의 `keep` = 미리보기에만 보이고 아무것도 하지 않음, S12 의 `domain.LadoSetupNeedsPresence(kind, name)`.

## After — 수행 후 실측

- 코드 커밋 2건:
  - `ee79088d4` `feat(lado): domain types for adopted local setup` — 3 파일, +1034 −4.
  - `8a26cc0bc` `feat(lado): sum a setup tree's file sizes` — 2 파일, +15 −1(리뷰 지적 반영, 아래).
- `domain/lado_setup.go`(587 줄):
  - 열거: `LadoSetupTool`(`LadoSetupToolClaudeCode`/`Codex` = `LadoSetupTool(HarnessClaudeCode/HarnessCodex)`), `LadoSetupKind`(`…Instructions`, `…Setting`, `…Skill`, `…Agent`, `…Command`, `…OutputStyle`, `…HookScript`, `…MCPServer`), `LadoSetupContentKind`(`LadoSetupContentFile`/`JSON`/`Tree`), `LadoSetupFileMode`(`LadoSetupModeFile`/`Exec`), `LadoSetupAdoptionState`(`LadoSetupAdoptionCompleted`/`Partial`), `LadoSetupLocation`(`…ClaudeDir`/`ClaudeJSON`/`CodexHome`/`Project`), `LadoSetupStatus`(`LadoSetupStatusManaged` … `RepoTracked`, 6개), `LadoSetupReason`(`LadoSetupReasonCredentials` … `ManagedSettings`, 플랜 §3 의 15개 그대로), `LadoSetupAction`(`…Move`/`Keep`), `LadoSetupDriftKind`(`LadoSetupDriftEdited`/`NewLocal`).
  - 타입 id: `LadoSetupItemID`, `LadoSetupItemVersionID`, `LadoSetupAdoptionID`, `LadoConflictAcceptanceID`.
  - 행 타입(0007 의 열 전부, NULL 은 zero 값): `LadoSetupItem` + `Live()`, `LadoSetupItemVersion`, `LadoSetupAdoption`(`RestoreReport json.RawMessage`), `LadoSetupBinding` + `Validate()`(`ErrLadoSetupBindingInvalid`), `LadoConflictAcceptance`.
  - 트리: `LadoSetupTree{Entries []LadoSetupTreeEntry}`(JSON `{"entries":[{"path","mode","size","sha256"}]}`), `Normalize() (LadoSetupTree, error)`, `CanonicalJSON() ([]byte, error)`, `SHA256() (string, error)`, `Size() int64`, `ErrLadoSetupTreeInvalid`.
  - 인벤토리·변경 감지: `LadoSetupEntry{Tool, Kind, Name, Location, Path, Size, SHA256, Tracked, Status, Reason, DefaultAction, ItemID, ProjectID}`, `LadoSetupDrift{ItemID, Kind, Tool, ItemKind, Name, Path, SHA256}`.
  - 함수: `LadoSetupDefaultAction(tool, kind, name)`, `LadoSetupNeedsPresence(kind, name)`, `LadoSetupDeliveryVerified(goos, tool)`; 목록 `LadoSetupClaudeLoginKeys`, `LadoSetupClaudeLoginEnv`(`claude.go:32-41` 과 같은 값·순서).
- `domain/lado_presence.go`: `LadoPresenceSetupAdopt = "setup.adopt"`, `LadoPresenceSetupRestore = "setup.restore"`, `LadoPresenceSetupChange = "setup.change"` 와 각 동작이 서명하는 subject(계획 sha256, 채택 id, 변경 sha256) 주석.
- TDD: 테스트 파일을 먼저 써서 `undefined: domain.LadoSetupTreeEntry …` 컴파일 실패를 봤고, 타입·상수와 빈 동작(stub)으로 동작 테스트 6개가 단언에서 실패하는 것(코드 고정 테스트만 통과)을 본 뒤 구현해 7/7 통과. `Size()` 도 단언을 먼저 더해 `tree.Size undefined` 를 본 뒤 구현.
- 변이 검사(스크래치 사본으로 가드 하나씩 빼고 되돌림, 커밋 안 함): `..` 단독·`../` 접두·NUL·역슬래시·UTF-8·clean·파일/폴더 겹침·중복·정렬·대문자 hex 허용·Codex 설정 move·빈 item id·지움만 보는 Live·linux 의 모르는 도구·모든 설정에 확인 필요·전역 force 허용 — 18개 모두 테스트가 실패로 잡았다(처음 `..` 단독 경로 케이스가 없어 테스트에 더함).
- 엔트리와 다르게 한 것과 이유:
  1. **타입 id** 를 뒀다(`LadoRuleID`·`LadoQueueEntryID` 와 같은 꼴). id 가 넷(항목·버전·채택·수락)이라 서로 섞이지 않게. S04 엔트리의 `id string` 인자는 이 타입으로 받거나 `string(id)` 로 gen 에 넘긴다.
  2. 바인딩·수락의 범위와 상태는 **규칙의 `LadoRuleScope`·`LadoBindingState` 를 그대로** 쓴다(값과 HARNESS §4 해석이 같다). 새 상수를 만들지 않았다.
  3. 트리 세 메서드는 **오류를 돌려준다** — 잘못된 매니페스트에는 해시가 없다. 엔트리의 거부 목록(빈 경로·`..`·절대·역슬래시·중복) 밖에 clean 이 아닌 경로(`.`, `./a`, `a//b`, `a/`), NUL, UTF-8 아닌 바이트, 파일이면서 폴더인 경로(`a` 와 `a/b`), file/exec 밖의 mode, 음수 크기, 소문자 64 hex 가 아닌 sha256 도 거부한다: 한 폴더가 한 가지 바이트로만 해시되고(JSON 은 잘못된 UTF-8 을 U+FFFD 로 바꿔 두 이름이 겹칠 수 있다), 홈에 쓸 때 `filepath.Join` 이 폴더 밖으로 나가지 않게. 형식 태그는 넣지 않았다(엔트리의 모양 그대로).
  4. `LadoSetupTree.Size()` 를 더했다(리뷰 Minor 2): 버전의 `Size` 를 "트리면 파일 크기 합"으로 정했으니 S05·S07 이 같은 합을 쓰게.
  5. `Validate()` 는 빈 item id 도 거부한다. 필수 테스트의 "허용 4조합"은 (범위, 상태)의 유효 조합 전부인 6개(전역 on/off, 프로젝트·태스크 force_on/force_off)로 확인했다.
  6. `LadoSetupDefaultAction` 의 이동 목록은 Claude settings.json 키이므로 **Claude 가 아닌 도구의 setting 은 keep**(지금은 그런 항목이 없다).
  7. `TestLadoSetupCodesAreStable` 은 상태·사유·종류·동작 밖에 도구·내용 종류·파일 mode·채택 state·위치·변경 감지 종류·presence 동작도 고정하고, 도구 값 = `AgentHarness` 값을 확인한다.
  8. 플랜 §3 의 충돌 코드는 도메인에 두지 않았다 — 엔트리 목록에 없고, 기존 문제 코드가 `service/ladopolicy/resolve.go` 에 있는 꼴대로 S06 이 서비스에 둔다. store 센티널 오류도 S04 몫으로 남겼다.
  9. gosec G101(이름의 Credentials + 문자열 값)이 `LadoSetupReasonLiteralCredentials` 를 잡아 기존 LADO 꼴(`controllers/lado_connections.go:31`)대로 줄 끝 `//nolint:gosec // G101: a reason code, not a credential.` 를 달았다.
  10. `LadoSetupDeliveryVerified` 는 엔트리대로(S01 결과가 플랜 결정 4 와 같다: Linux 에서 두 도구 확인).
- 리뷰(서브에이전트 `superpowers:code-reviewer`, sonnet, 전경): Critical·Important 없음. Minor 3 — (1) 트리 경로의 대소문자 겹침(`SKILL.md`·`skill.md`) 미거부 → 받지 않음(매니페스트는 한 기계에서 만들고 그 기계에서 쓴다; 회고의 틈에 적음), (2) 트리 크기 합 도우미 → 반영(`8a26cc0bc`), (3) 엔트리 미작성 → 이 기록. 리뷰가 돌린 명령 뒤 `go.work.sum` 에 222 줄이 붙어 있어 커밋하지 않고 `git checkout -- go.work.sum` 으로 되돌렸다(내 lint·vet 다시 실행으로는 재현 안 됨).
- 확인한 명령: `go test -count=1 ./internal/domain/` ok(0.1 s), `go build ./... && go vet ./...` 8 s, `gofmt -l internal/domain/` 출력 없음, golangci-lint v2.13.2(`GOPROXY=off`) `./internal/domain/...` 0 issues, `bash .claude/gate/lado-local-setup.sh gitleaks` → `GITLEAKS_OK`(main..HEAD, CI 와 같은 설정). 실제 claude 2.1.283 바이너리에 `otelHeadersHelper`·`enableAllProjectMcpServers`·`enabledMcpjsonServers` 문자열이 있음을 읽기로 확인(회고 참고).
- 게이트(HEAD `8a26cc0bc`, 엔트리 커밋 전): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 7/7`, G2 `HAS_OK`, G3 `GO_BUILD_OK`(4건 8 s). G4 는 이 엔트리를 커밋한 뒤 `gate-check.sh --run` 전건으로 확인하고 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 도메인 타입·상수·순수 함수와 필수 테스트 7개, presence 동작 3개. 저장·검사·채택 로직은 없다. 엔트리 밖으로 더한 것은 타입 id, 두 센티널 오류, 트리의 추가 거부와 `Size()` 뿐(위 1·3·4).
- 남은 틈:
  - 트리는 대소문자만 다른 두 경로와 Windows 에서 쓸 수 없는 이름(`:`, `*`, `CON` 등)을 거부하지 않는다. Linux 폴더에서 만든 매니페스트를 그 기계에서만 쓰므로 지금은 문제없고, Windows·macOS 실측(9단계) 때 채택 검사에서 다룬다.
  - 결정 i 의 확인 필요 목록은 플랜 그대로다. 그 밖에도 명령을 돌리거나 접근을 넓히는 Claude 설정 키가 있다: `otelHeadersHelper`(스크립트 실행), `enableAllProjectMcpServers`·`enabledMcpjsonServers`(프로젝트 MCP 서버 승인) — 셋 다 이동 기본 목록 밖이라 채택 미리보기의 기본은 keep 이지만, 옮기거나 앱에서 만들고 고치면 확인이 필요 없다. 로그인 키 `apiKeyHelper`·`awsAuthRefresh`·`awsCredentialExport` 도 명령을 돌리지만 `never/login_key` 라 채택되지 않는다. 목록을 넓힐지는 소유자 결정 — 넓히면 `ladoSetupPresenceSettings` 한 줄과 `TestLadoSetupNeedsPresence` 표만 바꾸면 된다.
  - 충돌 코드·store 센티널 오류는 아직 없다(S06·S04).
- 다음 단계 메모:
  - **S04(store)**: 도메인 ↔ gen — `LadoSetupItem.AdoptionID ""` ↔ `sql.NullString{}`, `DeletedAt`·`RestoredAt`·`RevokedAt` zero ↔ `sql.NullTime{}`, `LadoSetupItemVersion.Size` ↔ `SizeBytes`, `Executable bool` ↔ `int64` 0/1, `LadoSetupAdoption.OS` ↔ `Os`, `RestoreReport nil` ↔ `''`, 범위·상태는 `string(b.Scope)`·`string(b.State)`. 쓰기 전에 `b.Validate()`(inherit 일 때는 부르지 않고 행을 지운다). 센티널 오류 `ErrLadoSetupItemExists`·`ErrLadoSetupItemChanged`·`ErrLadoSetupItemNotLive`·`ErrLadoSetupAlreadyRestored` 는 `ErrLadoRuleChanged` 가 `lado_rule.go` 에 있듯 `domain/lado_setup.go` 끝에 더하면 서비스가 store 를 import 하지 않고 비교한다. 채택 state 는 `domain.LadoSetupAdoptionCompleted`/`Partial`.
  - **S05(인벤토리)**: 줄은 `domain.LadoSetupEntry`, 상태·사유 상수는 위 목록. 설정 키의 기본 동작은 `domain.LadoSetupDefaultAction(domain.LadoSetupToolClaudeCode, domain.LadoSetupKindSetting, key)`, 검증은 `domain.LadoSetupDeliveryVerified(GOOS, tool)`. 폴더 항목 해시: `LadoSetupTree{Entries: [{Path: 슬래시 상대 경로, Mode: LadoSetupModeFile|LadoSetupModeExec, Size, SHA256: 파일 바이트의 소문자 hex}]}` 의 `SHA256()`, 크기는 `Size()`. `SHA256()` 이 `ErrLadoSetupTreeInvalid` 면(예: Linux 파일 이름의 역슬래시, UTF-8 아닌 이름) 그 폴더는 채택할 수 없다 — 사유 코드가 새로 필요하면(심링크 포함) `lado_setup.go` 의 사유 목록과 `TestLadoSetupCodesAreStable` 표에 함께 더한다. Kind 는 항목이 아닌 줄(자격 증명·앱 상태)에서 비운다. 로그인 키(`TestClaudeLoginKeysMatchAgentAccount` 가 비교할 값, `claude.go:32-41` 순서): `LadoSetupClaudeLoginKeys` = `apiKeyHelper, awsAuthRefresh, awsCredentialExport, forceLoginMethod, forceLoginOrgUUID`; `LadoSetupClaudeLoginEnv` = `ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_BASE_URL, CLAUDE_CODE_USE_BEDROCK, CLAUDE_CODE_USE_VERTEX, CLAUDE_CODE_USE_FOUNDRY`. agentaccount 는 아직 둘 다 내보내지 않는다.
  - **S06(충돌)**: 문제 꼴은 기존 `domain.LadoPolicyProblem`(`Details["item"]` = 항목 id). 수락은 `domain.LadoConflictAcceptance{Code, ItemID, Detail, ContentSHA256, Scope, ScopeID}` — 범위 넓이 비교는 `LadoScopeGlobal ⊇ LadoScopeProject ⊇ LadoScopeTask`.
  - **S07(채택)**: 동작 `domain.LadoSetupActionMove`/`Keep`, presence `domain.LadoPresenceSetupAdopt`(subject = planSha256). 버전 `SHA256` 은 blob 이름이다: 파일 바이트, JSON 값, 트리면 `CanonicalJSON()` 바이트(그 sha256 = `SHA256()`)를 blob 으로 넣는다. 파일 항목의 실행 비트는 `Executable`, 트리는 항목별 `Mode`.
  - **S08(복원·변경 감지)**: `domain.LadoSetupDrift{ItemID, Kind: LadoSetupDriftEdited|LadoSetupDriftNewLocal, Tool, ItemKind, Name, Path, SHA256}` — new_local 은 같은 tool·kind·name 의 live 항목이 있으면 ItemID 를 단다. presence `domain.LadoPresenceSetupRestore`(subject = 채택 id).
  - **S09(홈)**: blob 에서 읽은 매니페스트는 JSON 으로 풀고 `Normalize()` 로 경로를 검사한 뒤(밖으로 나가는 경로 거부) `SHA256()` 이 버전의 sha256 과 같은지 보고 쓴다.
  - **S12(변경)**: `domain.LadoSetupNeedsPresence(kind, name)`, presence `domain.LadoPresenceSetupChange`(subject = changeSha256). 바인딩 입력 확인은 `LadoSetupBinding.Validate()`(400 코드는 S12·S13 이 정한다).
