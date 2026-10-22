# 2026-10-22 LADO 규칙 코어 — S04 store: 승인 스냅숏·plan facts·CDC

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§2 CDC, §4) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙 스냅숏을 계획 승인과 **한 트랜잭션**으로 저장하고(TDD §5.2), 태스크 읽기가 승인 스냅숏을 실어 S02 의 상태 파생(Review required·Blocked)이 돌게 한다. 규칙 변경이 화면에 바로 보이도록 CDC(`lado_rules`)를 붙인다.

## 범위

- 고친다: `backend/internal/storage/sqlite/store/lado_plan_store.go` — `ApproveLadoPlan(ctx, approval, proof, event, snapshot *domain.LadoPolicySnapshot)`: nil 이면 4b 와 같고, 아니면 승인 삽입 뒤 같은 tx 에서 스냅숏(`ApprovalID` = approval.ID)과 `lado_policy_snapshot_rules` 행. `ladoTaskPlan`·`ladoProjectPlans` 가 최신 승인의 `Plan.Snapshot`(ID·SHA256·Blocking)을 채운다.
- 만든다/고친다: `LadoPolicySnapshot(ctx, approvalID)`(전체 내용), `ListLiveLadoTasks(ctx)`(모든 프로젝트, 취소 안 됨, 이슈 연결됨, plan facts 포함 — `ListLadoTasksToObserve` 는 plan facts 가 없으니 새로 만든다. 쿼리가 더 필요하면 `queries/lado_rules.sql` 또는 `lado_tasks.sql` 에 ASCII 로 추가하고 `npm run sqlc`).
- 고친다: `backend/internal/storage/sqlite/lado_migrate.go` `ladoCDCTriggers` 에 플랜 §2 CDC 트리거 7개(이름 그대로). 마이그레이션에는 넣지 않는다.
- 고친다(컴파일 유지): `backend/internal/service/ladotask/service.go` 의 `Store` 인터페이스와 `plans.go` 의 호출(지금은 `nil` 을 넘긴다 — 진짜 스냅숏은 S08), ladotask 테스트의 가짜 store 가 있으면 시그니처만 맞춘다.
- 하지 않는다: 정책 해석·서비스(S05~S07), ladotask 승인 로직(S08).

## 절차

1. `ApproveLadoPlan` 의 tx 순서: 최신 리비전 재확인 → 증명 nonce → 승인 → (snapshot 이 있으면) 스냅숏 행 → 스냅숏 규칙 행 → 감사 이벤트. 어느 하나라도 실패하면 전부 롤백(nonce 도 남지 않는다).
2. plan facts: 프로젝트 목록 읽기는 `ListLadoPolicySnapshotsByProject` 한 번으로, 단건은 `GetLadoPolicySnapshotByApproval` 로. 승인이 없거나 스냅숏이 없으면 `Snapshot` 은 빈 값.
3. `DeleteLadoRule` 의 참조 검사(S03 에서 만든 것)를 실제 스냅숏으로 실증한다.
4. CDC: `ladoPlanCDCTrigger` 처럼 문자열 생성 함수를 두고(`ladoRulesCDCTrigger(name, event)` — LADO 모드 프로젝트마다 한 행, `ladoBindingCDCTrigger(name, event, row)` — global/project/task 분기), payload `json_object('kind', 'lado_rules', 'projectId', projects.id)`. 업스트림 `change_log` 재빌드 규칙(TDD §4.3)을 따른다: 이름은 `lado_cdc_*`, 마이그레이션에 넣지 않는다.
5. 기존 `TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds` 등 LADO 트리거 테스트가 그대로 통과해야 한다.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): store rule snapshots with plan approvals` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S04-store-snapshots.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (`backend/internal/storage/sqlite/store/`)

- `TestApproveLadoPlanStoresTheRuleSnapshot` — 스냅숏과 규칙 행이 승인과 함께 저장되고 `LadoPolicySnapshot` 으로 바이트 그대로 읽힌다
- `TestApproveLadoPlanRollsBackWhenTheSnapshotFails` — 스냅숏 삽입이 실패하면(예: 같은 스냅숏 id 재사용) 승인·nonce·감사 이벤트가 하나도 남지 않는다
- `TestLadoTaskReadsCarryTheRuleSnapshot` — `LadoTask`·`ListLadoTasks` 가 최신 승인의 `Plan.Snapshot`(sha256·blocking)을 싣는다, 스냅숏 없는 승인은 빈 값
- `TestDeleteLadoRuleRefusesARuleASnapshotUsed` — 스냅숏이 쓴 규칙 삭제 = `ErrLadoRuleReferenced`, 안 쓴 규칙은 삭제되고 감사 이벤트 1건
- `TestListLiveLadoTasksCarriesPlanFacts` — 여러 프로젝트의 살아 있는 태스크가 plan facts(최신 리비전·승인·스냅숏)와 함께 나오고 취소된 태스크는 빠진다
- `TestLadoRuleChangesPublishToLadoProjects` — global 바인딩 변경은 LADO 모드 프로젝트마다 `lado_rules` 1행(LADO 모드가 아닌 프로젝트 0), 프로젝트 바인딩은 그 프로젝트만, 태스크 바인딩은 그 태스크의 프로젝트만, 버전 추가는 LADO 모드 프로젝트마다

## 수용 게이트

- [ ] S04-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S04-G1: 필수 테스트 6종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/storage/sqlite/store/ TestApproveLadoPlanStoresTheRuleSnapshot TestApproveLadoPlanRollsBackWhenTheSnapshotFails TestLadoTaskReadsCarryTheRuleSnapshot TestDeleteLadoRuleRefusesARuleASnapshotUsed TestListLiveLadoTasksCarriesPlanFacts TestLadoRuleChangesPublishToLadoProjects
  EXPECT: GO_TESTS_OK 6/6
- [ ] S04-G2: store·sqlite 의 LADO 테스트 전부 통과(4a·4b CDC·트리거 테스트 포함)
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/store/ ./internal/storage/sqlite/ && echo S04_LADO_STORAGE_OK
  EXPECT: S04_LADO_STORAGE_OK
- [ ] S04-G3: ladotask 가 새 시그니처로 그대로 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-pkg ./internal/service/ladotask/
  EXPECT: GO_PKG_OK
- [ ] S04-G4: CDC 트리거 이름이 플랜 그대로다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/storage/sqlite/lado_migrate.go lado_cdc_rules_insert lado_cdc_rules_update lado_cdc_rules_delete lado_cdc_rule_versions_insert lado_cdc_rule_bindings_insert lado_cdc_rule_bindings_update lado_cdc_rule_bindings_delete "'lado_rules'"
  EXPECT: HAS_OK
- [ ] S04-G5: sqlc 재생성이 커밋과 같고 쿼리는 ASCII
  CHECK: bash .claude/gate/lado-rules-core.sh sqlc-clean && bash .claude/gate/lado-rules-core.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S04-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S04-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
