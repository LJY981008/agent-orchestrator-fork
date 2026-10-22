# 2026-10-22 LADO 규칙 코어 — S02 도메인 타입·태스크 상태

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§3) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙·버전·바인딩·스냅숏의 도메인 어휘를 고정한다. 뒤 단계 전부가 이 이름과 시그니처에 기댄다. 태스크 상태에 규칙 스냅숏을 반영해 승인 뒤 규칙이 바뀌면 Review required, 승인 스냅숏에 막는 문제가 있으면 Blocked 로 파생되게 한다(TDD §3.2 — 저장하지 않고 읽을 때 파생).

## 범위

- 만든다: `backend/internal/domain/lado_rule.go`, `backend/internal/domain/lado_policy.go`, 테스트 `backend/internal/domain/lado_rule_test.go`.
- 고친다: `backend/internal/domain/lado_presence.go`(`LadoPresenceRuleChange = "rule.protected_change"`), `backend/internal/domain/lado_task.go`(플랜 §3 끝 문단: `Snapshot`, `PolicySHA256`, `RulesChanged`, 두 상태), `backend/internal/domain/lado_task_test.go`.
- 하지 않는다: store·서비스·HTTP·UI. 상태 문자열을 쓰는 렌더러는 S12 가 고친다(지금은 백엔드만).

## 절차

1. 플랜 §3 의 타입·상수·함수를 그대로 만든다. 이름을 바꾸지 않는다(뒤 단계 엔트리가 이 이름을 쓴다). 문서 주석은 `lado_task.go`·`lado_plan.go` 와 같은 밀도로 영어.
2. `LadoRuleContent.CanonicalJSON` 은 `LadoIssueSnapshot.CanonicalJSON` 과 같은 방식(Encoder + `SetEscapeHTML(false)` + 끝 줄바꿈 제거)이고 `Normalize` 를 먼저 거친다: 문자열 `strings.TrimSpace`, `Conflicts` 정렬·중복 제거, `Config` 는 `json.Compact`(비었거나 `null` 이면 `{}`), nil 슬라이스는 빈 슬라이스.
3. `LadoTask.State()` 의 승인 분기만 바꾼다: `t.Plan.ApprovalValid()` 이면 `RulesChanged()` → `LadoTaskReviewRequired`, `Plan.Snapshot.Blocking` → `LadoTaskBlocked`, 아니면 `LadoTaskApproved`. 취소·닫힘이 여전히 먼저다. 상태 목록 주석("Later build steps add …")을 맞게 고친다.
4. 테스트를 쓴다(아래 필수 테스트). 기존 `TestLadoTaskStateFollowsItsPlans` 는 그대로 통과해야 한다(PolicySHA256 이 비면 예전과 같다).

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): rule and snapshot domain types` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S02-domain.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoRuleContentHashIgnoresConflictOrder` — 충돌 목록 순서·중복만 다른 두 내용이 같은 sha256, 이름 한 글자 다르면 다른 sha256
- `TestLadoRuleContentHashCompactsConfig` — `{"mode": "auto"}` 와 `{"mode":"auto"}` 가 같은 sha256, 빈 Config 와 `{}` 가 같다
- `TestLadoRuleContentStrength` — workflow_gate·tool_policy → prevents, validator → detects, prompt 만 → advisory
- `TestLadoPermissionRank` — accept-edits < auto < bypass-permissions, `default`·빈 값·모르는 값 = 0
- `TestLadoTaskStateFollowsRuleSnapshots` — 표: 승인+일치 → approved / 승인+불일치 → review_required / 승인+스냅숏 없음+현재 해시 있음 → review_required / 승인+일치+blocking → blocked / 현재 해시 미계산 → 스냅숏만으로 판정(approved 또는 blocked) / 취소·닫힘이 이긴다

## 수용 게이트

- [ ] S02-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S02-G1: 필수 테스트 5종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/domain/ TestLadoRuleContentHashIgnoresConflictOrder TestLadoRuleContentHashCompactsConfig TestLadoRuleContentStrength TestLadoPermissionRank TestLadoTaskStateFollowsRuleSnapshots
  EXPECT: GO_TESTS_OK 5/5
- [ ] S02-G2: domain 패키지 전체 통과(4b 상태 테스트 포함)
  CHECK: bash .claude/gate/lado-rules-core.sh go-pkg ./internal/domain/
  EXPECT: GO_PKG_OK
- [ ] S02-G3: 계약 이름이 플랜 그대로 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/domain/lado_rule.go 'LadoRulePermissionCeiling' '"agent.permission.ceiling"' '"plan.approval.required"' '"git.commit.message.korean"' 'LadoCeilingModes' 'func LadoPermissionRank' 'ErrLadoRuleChanged' 'ErrLadoRuleReferenced' 'type LadoRuleSeed struct'
  EXPECT: HAS_OK
- [ ] S02-G4: 스냅숏 타입과 상태가 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/domain/lado_policy.go 'type LadoPolicySnapshot struct' 'type LadoPolicyProblem struct' 'type LadoPolicySnapshotRef struct' && bash .claude/gate/lado-rules-core.sh has backend/internal/domain/lado_task.go '"review_required"' '"blocked"' 'func (p LadoTaskPlan) RulesChanged() bool' && bash .claude/gate/lado-rules-core.sh has backend/internal/domain/lado_presence.go '"rule.protected_change"'
  EXPECT: HAS_OK
- [ ] S02-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S02-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `89d7deaf49fbb054cbec76ee8ee089dccf74bc18`(`docs(lado): record S01`), 트리 깨끗.
- domain 에 규칙·스냅숏 타입 없음. `LadoTaskState` 는 draft·planned·approved·cancelled·closed_unmerged 다섯, `LadoTaskPlan` 은 Request·Latest·Approval 만.
- 테스트를 먼저 쓰고 돌림 → RED 1: 컴파일 실패(`undefined: domain.LadoRuleContent` 등).
- 타입·상수·필드만 넣고 `Normalize` 는 텍스트 trim 만, `State()` 는 그대로 둔 채 다시 돌림 → RED 2(단언 실패): conflicts 미정렬·`"enforcement":null`·`"config":null`, 빈·공백 Config 는 빈 바이트의 sha256(`e3b0c442…`), 상태 표 5행이 `approved`(want review_required/blocked).

## After — 수행 후 실측

- 코드 커밋 `cab25a118` `feat(lado): rule and snapshot domain types` — 6 파일, +524 −5
  - `lado_rule.go`(새): 플랜 §3 이름 그대로 — ID·scope·binding·enforcement·strength 타입, 내장 id 3개, `LadoRuleContent`(`Normalize`·`CanonicalJSON`·`SHA256`·`Strength`·`Has`), `LadoRuleVersion`·`LadoRuleBinding`·`LadoRule`·`LadoRuleSeed`·`LadoCeilingConfig`·`LadoCeilingModes`·`LadoPermissionRank`·`ErrLadoRuleChanged`·`ErrLadoRuleReferenced`. 플랜이 이름을 주지 않은 값 상수는 이렇게 지었다: `LadoScopeGlobal|Project|Task`, `LadoBindingOn|Off|ForceOn|ForceOff|Inherit`, `LadoEnforcementPrompt|Validator|ToolPolicy|WorkflowGate`, `LadoStrengthPrevents|Detects|Advisory`
  - `lado_policy.go`(새): `LadoPolicySnapshotID`, `LadoProblemSeverity`(`LadoSeverityBlocking|Warning|Info`), `LadoPolicyProblem`, `LadoPolicySnapshotRule`, `LadoPolicySnapshot`, `LadoPolicySnapshotRef`
  - `lado_presence.go`: `LadoPresenceRuleChange = "rule.protected_change"`, 묶음 주석의 "in step 4b" 제거
  - `lado_task.go`: `LadoTaskPlan.Snapshot`·`PolicySHA256`, `RulesChanged()`, `LadoTaskReviewRequired`·`LadoTaskBlocked`, `State()` 승인 분기 3개(바뀜 → review_required, blocking → blocked, 아니면 approved). 취소·닫힘이 여전히 먼저. 상태 목록·`LadoTaskPlan` 주석 갱신
  - 테스트: 필수 5종 + `TestLadoRuleContentIsCanonicalJSON`(정확한 바이트·HTML 문자 그대로·trim·nil 슬라이스 → `[]`·빈 Config → `{}`·호출자 슬라이스 불변·두 번 정규화해도 같은 해시)
- 정규 형태: `{"name":…,"category":…,"instruction":…,"enforcement":[…],"config":{…},"conflictsWith":[정렬·중복 제거]}`.
- 추가 실행: `go test -count=1 ./internal/domain/` ok, `gofmt -l` 출력 없음, `go vet ./internal/domain/` ok, `go test ./internal/service/ladotask/` ok, `-run Lado` 로 `httpd/controllers`·`storage/sqlite/store` ok. 아직 아무도 `Snapshot`·`PolicySHA256` 을 채우지 않으므로 실행 동작은 그대로다.
- 게이트(HEAD `cab25a118`): `GATES=7 MET=7 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳·재작업 없음.
- RED 2 에서 확인한 것: `encoding/json` 은 Marshaler(`json.RawMessage`) 출력을 스스로 compact 한다. 그래서 공백만 다른 `{"mode": "auto"}` 는 Normalize 없이도 같은 해시였다. 실제 위험은 빈·공백 RawMessage 였다. Encode 가 실패해 버퍼가 비고, 서로 다른 내용이 모두 빈 바이트의 sha256 이 된다. `Normalize` 의 `json.Compact` 가 비었거나 `null` 이거나 JSON 이 아닌 Config 를 `{}` 로 바꿔 이를 막는다. 저장되는 `config_json` 도 Normalize 결과라 DB 의 `json_valid` CHECK 와 맞는다.
- JSON 이 아닌 Config 를 `{}` 로 바꾸는 것은 플랜에 없던 결정이다. Config 를 만드는 곳은 코드(내장 시드·`CeilingMode`)와 `json_valid` 가 지키는 DB 뿐이라 실제로는 생기지 않는다. 생기더라도 상한 설정이 사라져 S06 의 `LADO_CEILING_MISSING`(blocking)으로 닫힌 쪽으로 실패한다.
- Enforcement 는 정렬하지 않는다. 플랜의 Normalize 목록에 없고 정규 형태가 해시 계약이기 때문이다. 지금은 모든 규칙이 enforcement 하나라 차이가 없다. 나중에 정렬을 넣으면 저장된 버전 해시가 모두 바뀐다.
- `LadoPermissionRank` 는 `slices.Index(LadoCeilingModes, m) + 1` 이다. 목록 자체가 순위라 둘이 어긋날 수 없다.
- 상태 우선순위는 플랜 순서대로 rules changed 가 blocking 보다 먼저다. 막힌 승인 뒤 규칙이 바뀌면 review_required 가 된다(다시 승인하면 새 스냅숏이 생긴다). 4b 승인(스냅숏 없음)은 현재 해시가 채워지면 review_required, 채워지지 않으면 예전처럼 approved 다.
- 다음 단계 메모: S03 부터 위 값 상수 이름을 그대로 쓴다. `LadoTaskPlan.Snapshot` 은 S04 store, `PolicySHA256` 은 S08 task 서비스가 채운다. 태스크 응답의 state 목록은 S08/S09, 렌더러 상태 문자열은 S12 가 고친다.
