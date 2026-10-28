# 2026-10-27 LADO 검증·통합 승인 — S04 정책: 보호 규칙 4개·한국어 규칙 v2·lado-policy/3·보호 경로 변경

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 o·p, §14) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

HARNESS §14 의 보호 규칙 4개(통합 승인 필수, 기준 브랜치 직접 쓰기 금지, 테스트 통과, 보호 경로)를 늘 켜진 내장 규칙으로 등록하고, 한국어 커밋 규칙에 검사기를 붙인다. 스냅숏은 어느 검사기가 적용되는지 담도록 `lado-policy/3` 이 된다. 보호 경로 목록은 보호 규칙이라 데스크톱 확인으로만 바뀐다.

## 범위

- 바꾼다: `service/ladopolicy/builtins.go`, `service.go`(`EnsureBuiltins` 의 업그레이드, 보호 경로 변경), `snapshot.go`(`Compiler`, `Validators`), `prompt.go`(보호 경로 목록을 프롬프트에), `resolve.go`(필요하면), 테스트들(`builtins_test.go`, 스냅숏 박제 테스트 이름과 바이트), `daemon/lado_wiring_test.go`(`TestWireLadoWorkSeedsBuiltinRules` 의 기대값).
- 만들 수 있다: 규칙 버전을 `lado` 로 덧붙이는 store 메서드가 없으면 `store/lado_rule_store.go` 에 하나(`AppendLadoBuiltinRuleVersion` 처럼, 감사 이벤트 같은 트랜잭션).
- 하지 않는다: HTTP DTO·명세(S15), 렌더러(S18), 검사기 실행(S06·S11).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `Builtins()` 에 플랜 §1 o 의 네 규칙(보호, Global on, 이름·범주·enforcement·지침 그대로). `paths.protected` 의 config 는 `domain.DefaultLadoProtectedPaths()` 를 `{"globs":[…]}` 로. 영어 이름은 렌더러 표(`lib/lado-rules.ts` `seedName`, S18)가 그대로 쓸 문자열이다: "Integration approval required", "No direct writes to the base branch", "Tests must pass", "Protected paths".
2. 한국어 규칙 v2: 내장 정의가 버전 이력을 갖게 한다(예: `previous []domain.LadoRuleContent`). `EnsureBuiltins` 는 없는 규칙을 심는 것에 더해, 저장된 활성 버전의 sha256 이 이전 내장 버전 중 하나와 같으면 현재 내장 버전을 다음 번호로 `lado` 가 덧붙이고 감사 `rule.builtin_upgraded`(`{from, to}`)를 같은 트랜잭션에 쓴다. 개발자가 고친 규칙(다른 sha256)은 그대로 둔다. 두 번째 시작은 아무것도 안 쓴다. v2 = v1 + `validator` enforcement + config `domain.DefaultLadoCommitLocaleConfig()`.
3. 스냅숏: `Compiler = "lado-policy/3"`, `SnapshotContent` 의 `WorkflowGates` 바로 뒤에 `Validators []SnapshotValidator`(`ruleId`, `kind`, `config` — 원시 JSON). 켜진 규칙 중 `validator` enforcement 가 있는 것마다: `test.command.must_pass` → `checks_must_pass`(`{}`), `paths.protected` → `protected_paths`(그 버전의 config), `git.commit.message.korean` → `commit_message_locale`(그 버전의 config, 없으면 기본). 목록은 nil 아닌 `[]`. 형식 주석을 고친다.
4. 프롬프트: 켜진 규칙의 지침이 들어가는 `## LADO rules` 에서 `paths.protected` 부분 끝에 설정된 경로를 한 줄로 붙인다(`Protected paths: \`.github/**\`, …`). 다른 규칙은 그대로.
5. 보호 경로 변경: `UpdateRule`(또는 지금 천장을 바꾸는 경로)에 `paths.protected` 의 `Paths *domain.LadoProtectedPathsConfig` 입력을 더한다 — 천장과 같은 방식: `baseVersion` 확인(`LADO_RULE_CHANGED`), `domain.ValidateLadoProtectedPaths`(400 `LADO_PROTECTED_PATHS_INVALID` `{index, reason}`), 존재 증명 `rule.protected_change`(대상 규칙 id), 새 버전(config 만 바뀜)과 감사 `rule.protected_changed`(actor `human`·`human:dev-presence`, `{from, to}`). dry run 은 증명 없이 영향만. 다른 보호 규칙의 편집 거부는 그대로.
6. 기존 테스트의 내장 규칙 수(보호 3 → 7, 전체 4 → 8)와 스냅숏 박제를 새 값으로 고친다. 스냅숏 해시를 계산해 비교하는 다른 패키지 테스트(ladotask, ladoqueue)가 깨지면 해시를 하드코딩하지 말고 계산하도록 고친다.
7. 커밋 메시지 예: `feat(lado): the four protected verification gates and lado-policy/3`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S04-policy.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestBuiltinsIncludeTheVerificationGates` — 8개 행: id, 보호, Global, 범주, enforcement, config 기본값(보호 경로 9개, 한국어 v2 config)
- `TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule` — v1 그대로면 v2 가 `lado` 로 붙고 감사 1건; 고친 규칙은 그대로; 다시 시작하면 아무것도 없음; 없는 규칙은 v2 로 바로 심김
- `TestSnapshotFormatIsLadoPolicy3` — 고정 입력의 정규 JSON 바이트 박제(`validators` 위치·내용 포함)
- `TestValidatorsFollowTheOnRules` — 한국어 규칙 꺼짐 → `commit_message_locale` 없음, 켜짐 → 있음(설정 그대로), 보호 검사기 둘은 늘 있음, 고친 한국어 규칙에 `validator` 가 없으면 없음
- `TestProtectedPathsChangeNeedsPresence` — 증명 없음 거부, 증명 있으면 새 버전·감사 `human`, 재사용 증명 거부, 잘못된 경로 400, `baseVersion` 불일치 409, dry run 은 아무것도 안 씀
- `TestProtectedPathsPromptListsGlobs` — 워커 규칙 글에 설정 경로 줄

## 수용 게이트

- [ ] S04-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S04-G1: 필수 테스트 6개 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladopolicy/ TestBuiltinsIncludeTheVerificationGates TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule TestSnapshotFormatIsLadoPolicy3 TestValidatorsFollowTheOnRules TestProtectedPathsChangeNeedsPresence TestProtectedPathsPromptListsGlobs
  EXPECT: GO_TESTS_OK 6/6
- [ ] S04-G2: 규칙·태스크·디스패처 패키지 테스트가 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoqueue/
  EXPECT: GO_PKG_OK
- [ ] S04-G3: 데몬이 새 내장 규칙을 심는다
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/daemon/ TestWireLadoWorkSeedsBuiltinRules
  EXPECT: GO_TESTS_OK 1/1
- [ ] S04-G4: store 의 규칙 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/storage/sqlite/store/ 'LadoRule|LadoBuiltin|LadoPolicy'
  EXPECT: GO_RUN_OK
- [ ] S04-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S04-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `31668c4b3`(`chore(lado): merge the S02 lane`, 브랜치 `lado8/S04`, 트리 깨끗). S01·S02 커밋이 들어 있고 S03(store)은 다른 레인이라 이 브랜치에 없다.
- 환경: Ubuntu, go1.27.1(스크래치 툴체인), umask 0022, TZ=UTC, `CHAIN_RUN=1`. 실제 `~/.ao`·`~/.claude`·`~/.codex` 는 쓰지 않음(서비스 테스트는 `sqlitetest` 임시 DB).
- 기준 실측: `defs-unchanged` → `DEFS_UNCHANGED`; `go test -count=1 ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoqueue/` ok(5 s).
- 출발점(코드로 읽음):
  - `Builtins()` 4개 — `plan.approval.required`, `agent.permission.ceiling`, `git.commit.message.korean`(prompt 만, config `{}`), `setup.change.requires_presence`; 보호 3. `EnsureBuiltins` 는 없는 규칙만 심는다(`EnsureLadoBuiltinRules`).
  - store 에 `AddLadoRuleVersion(v, base, proof *…, event)` 가 이미 있다: base 번호 확인 + 버전 + (증명) + 감사가 한 트랜잭션, proof 는 nil 가능.
  - `Compiler = "lado-policy/2"`, `WorkflowGates` 바로 뒤가 `Capabilities`(validators 없음). `Update` 는 보호 규칙의 편집을 거부하고 천장 모드만 `changeCeiling` 으로 바꾼다(presence `rule.protected_change`, 감사 `rule.protected_changed`). `promptText` 는 규칙마다 `### 이름\n\n지침`.
  - 도메인(S02): 규칙 id 4개, `LadoProtectedPathsConfig`, `DefaultLadoProtectedPaths()`(9개), `LadoProtectedPathsMax`(50), `ValidateLadoProtectedPaths`(error 만 — index 없음), `LadoCommitLocaleConfig`, `DefaultLadoCommitLocaleConfig()`.
- 앞 단계 메모 중 이 단계 몫: S02 "규칙(S04)" 의 이름을 그대로 쓴다; "빈 글롭·앞뒤 공백 글롭도 거부 — 서비스가 먼저 trim 하면 된다" → 서비스가 trim 한 뒤 검사. S01 메모는 이 단계와 무관(마이그레이션·쿼리 손대지 않음).

## After — 수행 후 실측

- 커밋: `78204cc6c` feat(lado): the four protected verification gates and lado-policy/3(9 파일, +1012/−105) · 이 엔트리 커밋.
- 바꾼 것:
  - `builtins.go`: `builtinDef{rule, previous []domain.LadoRuleContent}`, `builtinDefs()`(시드 순서), `Builtins()` 는 그 rule 들. 8개 = 기존 4 + `integration.approval.required`(workflow, `[workflow_gate, prompt]`), `git.protected_branch.no_direct_write`(git, `[workflow_gate, prompt]`), `test.command.must_pass`(workflow, `[validator, prompt]`), `paths.protected`(git, `[validator, prompt]`, config `{"globs":[기본 9개]}`) — 모두 보호·Global on, 이름·지침은 플랜 §1 o. 한국어 규칙의 현재 내용 = 내장 v2(enforcement `[prompt, validator]`, config = `DefaultLadoCommitLocaleConfig()` 의 JSON), `previous` = v1. 도우미 `configJSON`.
  - `service.go`: `EnsureBuiltins` 가 심은 뒤 규칙을 다시 읽고, 활성 버전 sha256 이 `previous` 중 하나인 내장 규칙에 현재 내용을 `AddLadoRuleVersion(next, 활성 번호, nil, 감사)` 로 덧붙인다 — 감사 `rule.builtin_upgraded`, actor `lado`, payload `{"from": n, "to": n+1}`. 그 사이 개발자가 고쳤으면(`ErrLadoRuleChanged`) 로그만 남기고 둔다. `UpdateInput.Paths *domain.LadoProtectedPathsConfig`; 보호 규칙 중 천장과 `paths.protected` 만 설정이 바뀐다; `changeProtectedPaths`(trim → `checkProtectedPaths` → 400 `{index, reason}`); 천장과 같은 몸통 `changeSetting`(같은 설정 → 아무것도 안 씀, dry run → 영향만·증명 없음, 증명 없음 403 `LADO_PRESENCE_REQUIRED`, 증명 → 새 버전 actor `human`/`human:dev-presence`, 감사 `rule.protected_changed {from, to, version, proof, dev, reason}`, 재사용 → `LADO_PRESENCE_REPLAYED`). `LADO_RULE_PROTECTED` 문구에 보호 경로를 더함.
  - `snapshot.go`: `Compiler = "lado-policy/3"`, `WorkflowGates` 바로 뒤 `Validators []SnapshotValidator`(`validators`), `SnapshotValidator{RuleID, Kind, Config json.RawMessage}`, `ValidatorKind` 상수 3개, `snapshotValidator`. 형식·인코딩 주석.
  - `prompt.go`: `paths.protected` 부분의 지침 끝에 ``\nProtected paths: `g1`, `g2`…``(설정 순서; 글롭이 없으면 줄 없음).
  - 테스트: 필수 6개 + 기존 기대값(내장 4→8, 보호 3→7, 프롬프트 규칙 = 한국어 + 게이트 4, 박제 `lado-policy/2` → `TestSnapshotFormatIsLadoPolicy3`, `TestBuiltinsAreTheStepFiveCatalog` → `TestBuiltinsIncludeTheVerificationGates` 로 교체, `TestResolveBindings`·`TestResolveCapabilities`·`TestPromptText`·`TestEffectiveBlocksAnUnsupportedAgent`·`TestSnapshotMatchesCurrentSHA`·`TestEnsureBuiltinsSeedsOnce`·`TestDryRunWritesNothing`), 도우미 `newBareServiceFixture`·`seedStepSevenBuiltins`·`upgradeEvents`·`withoutPromptRules`. `daemon/lado_wiring_test.go` 는 8개 규칙·8 entries.
- 엔트리와 다르게 한 것과 이유:
  1. store 메서드를 만들지 않았다 — 기존 `AddLadoRuleVersion`(proof nil)이 "버전 + 감사 한 트랜잭션"과 base 번호 확인을 이미 한다. 버전은 불변이라 base 번호가 같으면 비교한 그 내용이 아직 활성이다.
  2. 감사 `{from, to}` 는 저장된 버전 번호(내장 버전 번호가 아님) — 개발자가 v1 내용으로 되돌려 만든 v3 같은 이력에서도 맞게. 보통은 1→2.
  3. 없는 한국어 규칙은 내장 v2 의 **내용**을 버전 번호 1 로 심는다(store 가 첫 버전을 1 로 둔다; `rule.created` payload 도 그대로 `{"version":1,"builtin":true}`).
  4. 보호 경로 지침은 플랜의 "…names them: {globs}." 대신 절차 4대로 고정 문장 "Do not change protected paths unless the approved plan names them." 에 프롬프트가 `Protected paths: …` 한 줄을 붙인다 — 지침(버전 해시)은 그대로, 글롭은 설정을 따른다.
  5. `{index, reason}` 는 서비스가 계산한다(도메인 함수는 index 를 주지 않음): 빈 목록 `{0, required}`, 50 초과 `{50, too_many}`(넘친 첫 항목 — 검사 명령의 `too_many` 와 같은 방식), 나쁜 글롭 `{i, invalid}`(그 글롭 하나만 도메인 함수로 다시 본 첫 i). 각 글롭을 먼저 trim(S02 메모). 중복 글롭은 거부하지 않는다(도메인 규칙 그대로). 다른 규칙에 `Paths` 를 보내면 같은 코드에 `{rule}`(천장의 `LADO_RULE_CEILING_INVALID {rule}` 와 같은 방식). 도메인은 고치지 않음.
  6. 천장 변경과 경로 변경의 공통 몸통을 `changeSetting` 으로 뺐다(실제 호출처 2곳). 천장의 동작·문구·감사 payload 는 그대로(기존 천장 테스트 통과, 변이로도 확인).
  7. `paths.protected` 에 `CeilingMode` → 400 `LADO_RULE_CEILING_INVALID`(설정이 바뀌는 보호 규칙이라 403 이 아니라 "모드 없음"); 다른 보호 규칙에 `Paths` → 403 `LADO_RULE_PROTECTED`(기존 검사 순서 유지).
  8. validators 는 엔트리 순서(생성 시각, id) — 스냅숏 `rules`·`workflowGates` 와 같은 순서. 세 내장 규칙 밖의 규칙에는 검사기가 없다(사용자 규칙은 validator 를 가질 수 없음).
  9. 새 게이트 4개가 prompt enforcement 라 이제 모든 태스크의 워커 글이 비지 않는다 → prompt capability 문제의 규칙 목록 = 한국어 + 게이트 4(cursor·fx·gemini 기대값). cursor·fx 는 이미 permission_mode unverified 로 막혀 있어 차단 여부는 같다. "프롬프트 규칙 없음" 경우는 입력에서 prompt 규칙을 빼서(`withoutPromptRules`) 뜻을 지켰다.
  10. ladotask·ladoqueue 테스트는 고칠 것이 없었다(해시 하드코딩 없음 — `ladoqueue/fakes_test.go` 는 `ladopolicy.Compiler` 와 상수 해시 id 를 쓴다).
- 테스트 순서: 필수 6개를 구현 뒤에 써서 첫 실행부터 통과했다. 대신 변이 6건으로 실패를 확인: 업그레이드 끔 → `TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule` FAIL; 경로 검증 끔 → `TestProtectedPathsChangeNeedsPresence` FAIL; presence subject 바꿈 → 경로·천장 테스트 FAIL; validators 를 안 넣음 → `TestValidatorsFollowTheOnRules`·`TestSnapshotFormatIsLadoPolicy3` FAIL; 경로 줄을 안 붙임 → `TestProtectedPathsPromptListsGlobs`·`TestPromptText`·형식 FAIL; 다른 규칙의 `Paths` 거부 끔 → 경로 테스트 FAIL. 원본 복구 확인(diff 없음).
- 확인한 명령과 결과: `go test -count=1 ./...`(backend 전체) 실패 0(2m04s); `go-tests` 필수 6 → `GO_TESTS_OK 6/6`; `go-pkg` 3 패키지 → `GO_PKG_OK`; `go-run` store → `GO_RUN_OK 11`; `go-build` → `GO_BUILD_OK`; `go-lint ./internal/service/ladopolicy/... ./internal/daemon/...` → `GO_LINT_OK`; gofmt 깨끗.
- 게이트(HEAD `78204cc6c`, 엔트리 커밋 전): `gate-check.sh --run` → GATES=7 MET=7. 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남긴다.

## 비교/회고

- 범위 대비 결과: 절차 1~7 전부와 필수 테스트 6개. HTTP DTO·명세(S15), 렌더러(S18), 검사기 실행(S06·S11)은 손대지 않았다. store·domain·마이그레이션 변경 없음.
- 남은 틈:
  - `PATCH /lado/rules/{ruleId}` 컨트롤러는 아직 `paths` 를 읽지 않는다(`UpdateInput.Paths` 를 채우지 않음), 규칙 응답 DTO 에 보호 경로가 없다 — S15.
  - 렌더러 `lib/lado-rules.ts` 표에 새 게이트 4개가 없다 — S18. `LADO_RULE_PROTECTED`·`LADO_PROTECTED_PATHS_INVALID` 의 렌더러 문구 — S16.
  - 업그레이드는 데몬 시작 때만 돈다. 한국어 규칙이 켜진 승인된 태스크는 업그레이드 뒤 Review required 가 된다(플랜 §1 p 의 의도) — 미리보기는 없다.
  - 기존 DB 에서 새 게이트 4개는 업그레이드 시각에 생겨 그전 사용자 규칙보다 뒤 순서다(엔트리 순서 = 생성 시각) — 프롬프트·validators 순서가 새 DB 와 다를 수 있다(해시 비교는 같은 DB 안이라 무해).
  - 글롭 안의 백틱은 프롬프트 줄의 코드 표기를 깬다(검증이 막지 않음; 실제로는 거의 없음).
- 다음 단계 메모(이름 그대로 쓴다):
  - **검사기 kind**(`ladopolicy`): `ValidatorKind` = `ValidatorChecksMustPass`(`checks_must_pass`, config `{}`), `ValidatorProtectedPaths`(`protected_paths`, `domain.LadoProtectedPathsConfig`), `ValidatorCommitMessageLocale`(`commit_message_locale`, `domain.LadoCommitLocaleConfig`; 버전 config 가 `{}` 면 기본값이 들어간다). `SnapshotContent.Validators []SnapshotValidator{RuleID, Kind, Config json.RawMessage}`(JSON `ruleId`·`kind`·`config`), 늘 nil 아님, `WorkflowGates` 바로 뒤. S06·S11: 실행 스냅숏을 `json.Unmarshal` 해 `Compiler` 가 `lado-policy/3` 이면 validators 를 읽고, `lado-policy/2` 면 `commit_messages` 는 `not_required`, `paths` 는 `outside_plan` 만(플랜 §1 g·p). 한국어 검사기는 validators 에 `commit_message_locale` 이 있을 때만(규칙 꺼짐·개발자가 고친 v1 규칙이면 없음).
  - **업그레이드**: `Service.EnsureBuiltins`(데몬 시작 `wireLadoWork`), 정의 `builtinDefs()` / `builtinDef.previous`, 감사 `rule.builtin_upgraded` actor `lado` `{from, to}`(저장 버전 번호). 내장 내용을 또 바꾸면 옛 내용을 `previous` 에 넣는다(`TestBuiltinsIncludeTheVerificationGates` 의 config 바이트 박제가 알려 준다).
  - **변경 입력**: `ladopolicy.UpdateInput.Paths *domain.LadoProtectedPathsConfig`(나머지는 천장과 같음: `BaseVersion`, `Reason`, `DryRun`, `Presence`). 오류: 400 `LADO_PROTECTED_PATHS_INVALID` details `{index, reason}`(reason `required`·`too_many`·`invalid`) 또는 다른 규칙이면 `{rule}`; 403 `LADO_PRESENCE_REQUIRED`("Change the protected paths in the AO desktop app. It asks you to confirm, then sends the change itself."); 403 `LADO_PRESENCE_REPLAYED`; 409 `LADO_RULE_CHANGED {baseVersion}`. presence 동작 `rule.protected_change`, subject `paths.protected`. 감사 `rule.protected_changed` payload `{from: [...], to: [...], version, proof, dev, reason}`(to 는 trim 된 글롭). 같은 글롭이면 아무것도 안 쓰고 `Applied: true`.
  - **S15 가 DTO 에 더할 것**: `UpdateLadoRuleRequest` 에 `paths`(`{globs: string[]}`) → `in.Paths`; 규칙·버전 응답에 보호 경로(`ladoRuleCeiling` 처럼 `paths.protected` 일 때만 config 의 globs); 명세 오류에 400 `LADO_PROTECTED_PATHS_INVALID`; 스냅숏 내용을 내보내는 응답이 있으면 `validators` 스키마(`ruleId`, `kind` enum 3개, `config` 객체).
  - **S16·S18**: 내장 영어 이름(`seedName`) = "Integration approval required", "No direct writes to the base branch", "Tests must pass", "Protected paths". 데스크톱 `changeRule` 은 `PATCH` 본문 `{"baseVersion": n, "paths": {"globs": [...]}}` 에 서명한다. 워커 글의 줄 형식은 `Protected paths: ` + 백틱으로 감싼 글롭을 `, ` 로 이음.
