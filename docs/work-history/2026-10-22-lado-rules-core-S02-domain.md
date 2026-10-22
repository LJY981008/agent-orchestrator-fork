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

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
