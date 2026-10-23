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

- 시작 HEAD: `64365dc7e4e50233d2f02ca15dff3ec1bff7e3ec`(`docs(lado): record S03`), 트리 깨끗.
- `ApproveLadoPlan(ctx, approval, proof, event)` 는 승인·증명·감사만 저장한다. 스냅숏 테이블(S01)과 쿼리 `InsertLadoPolicySnapshot`·`InsertLadoPolicySnapshotRule`·`GetLadoPolicySnapshotByApproval`·`ListLadoPolicySnapshotsByProject` 는 있지만 store 가 쓰지 않는다. 스냅숏 규칙 행을 읽는 쿼리와 모든 프로젝트의 살아 있는 태스크를 읽는 쿼리는 없다.
- `LadoTaskPlan.Snapshot` 은 S02 에서 생겼지만 어떤 읽기도 채우지 않는다. `ladoCDCTriggers` 에는 태스크·플랜 트리거 6개뿐이다.
- 필수 테스트 6종을 먼저 썼다. 첫 실행은 컴파일 실패(`too many arguments in call to s.ApproveLadoPlan`)로 RED.

## After — 수행 후 실측

- 코드 커밋 `041306367` `feat(lado): store rule snapshots with plan approvals`: 12 파일, +657 −19
  - `lado_plan_store.go`: `ApproveLadoPlan(..., snapshot *domain.LadoPolicySnapshot)`. tx 순서는 최신 리비전 재확인 → 증명 nonce → 승인 → (snapshot 이 있으면) `insertLadoPolicySnapshotTx`(스냅숏 행 → 규칙 행) → 감사 이벤트다. `LadoPolicySnapshot(ctx, approvalID)` 는 스냅숏 행과 규칙 행을 읽는다(규칙은 rule id 순). `ladoTaskPlan` 은 최신 승인의 스냅숏을 `GetLadoPolicySnapshotByApproval` 로, `ladoProjectPlans` 는 `ListLadoPolicySnapshotsByProject` 한 번으로 읽어 승인 id 로 찾는다.
  - `lado_task_store.go`: `ListLiveLadoTasks` 는 새 쿼리 `ListLiveLadoTasks`(이슈 연결·취소 안 됨, `project_id, created_at, id` 순)로 태스크를 읽고, 프로젝트마다 `ladoProjectPlans` 를 한 번씩 불러 plan facts 를 붙인다. 단건·프로젝트 목록과 조립 경로가 같아 결과가 갈라지지 않는다.
  - `lado_migrate.go`: 트리거 7개 `lado_cdc_rules_{insert,update,delete}`, `lado_cdc_rule_versions_insert`, `lado_cdc_rule_bindings_{insert,update,delete}`(delete 는 OLD). 생성 함수는 `ladoRulesCDCTrigger(name, event)`(LADO 모드이고 등록된 프로젝트마다 한 행)와 `ladoBindingCDCTrigger(name, event, row)`(global → LADO 모드 프로젝트마다, project → 그 프로젝트, task → 그 태스크의 프로젝트)이고, 공통 INSERT 머리는 `ladoRulesChangeInsert` 다. payload 는 `json_object('kind', 'lado_rules', 'projectId', projects.id)`, `session_id` 는 NULL. 마이그레이션에는 넣지 않았다.
  - 쿼리(ASCII): `lado_rules.sql` 에 `ListLadoPolicySnapshotRules`, `lado_tasks.sql` 에 `ListLiveLadoTasks`. `npm run sqlc` 뒤 gen 의 SQL 이 잘리지 않았음을 읽어 확인했다.
  - `ladotask`: `Store` 인터페이스 시그니처만 바꾸고 `plans.go` 는 `nil` 을 넘긴다. ladotask 테스트는 실제 store(`sqlitetest.MustOpen`)를 써서 가짜 store 수정은 없었다.
  - 테스트: 필수 6종을 더하고, 기존 승인 테스트 7곳에는 `nil` 을 더했다. 도우미는 `ladoPolicySnapshot`(내용의 sha256 계산), `mustApproveLadoPlan`, `countLadoTables`.
- 플랜에 없어 이 단계에서 정한 것:
  - 스냅숏의 `ApprovalID`·`CreatedAt` 은 store 가 승인에서 가져온다(호출자 값은 무시). 스냅숏은 승인 tx 에서 고정되기 때문이다. `ID`·`SHA256`·`Content`·`Blocking`·`Rules` 는 호출자 값 그대로 저장한다. store 는 sha256 을 다시 계산하지 않는다(정규 바이트로 계산하는 것은 S06/S07 서비스).
  - `LadoPolicySnapshot` 의 `Rules` 는 저장 순서가 아니라 rule id, version id 순이다(테이블에 순서 열이 없다).
  - plan facts 의 `Snapshot` 은 "최신 승인의" 스냅숏이다. 그 승인에 스냅숏이 없으면 이전 승인에 있었어도 빈 값이다. 그래서 프로젝트 목록은 스냅숏을 승인 id 로 찾는다(순서대로 덮어쓰면 틀린다. 변이 M1 이 잡는다).
  - CDC 에는 변경 여부를 보는 `WHEN` 을 두지 않았다. 바인딩 upsert 가 기존 행을 고치면 UPDATE 트리거가 돈다(SQLite UPSERT 규칙, 테스트로 확인). project 바인딩은 그 프로젝트가 LADO 모드가 아니어도 발행한다(플랜 §2 문구 그대로). 등록되지 않은 프로젝트와 대기 태스크에는 행이 없다.
  - 규칙 생성은 규칙·버전·Global 바인딩 트리거로 LADO 프로젝트마다 3행, 삭제는 바인딩 삭제와 규칙 삭제로 2행 이상이 생긴다. 렌더러는 같은 kind 를 무효화만 하므로 중복은 해가 없다.
- 변이 확인(각각 고친 뒤 원복): M1 프로젝트 목록이 승인 id 대신 순서로 스냅숏을 덮어씀 → `TestLadoTaskReadsCarryTheRuleSnapshot` FAIL. M2 스냅숏 삽입 오류 무시 → `TestApproveLadoPlanRollsBackWhenTheSnapshotFails` FAIL. M3 global 바인딩이 모든 프로젝트에 발행 → `TestLadoRuleChangesPublishToLadoProjects` FAIL(`off`·`plain` 포함). M4 delete 트리거가 NEW 를 씀 → 같은 테스트 FAIL(`no such column: NEW.scope`). M5 live 목록에서 plan facts 를 뺌 → `TestListLiveLadoTasksCarriesPlanFacts` FAIL.
- 추가 실행: `gofmt -l` 출력 없음, `go test -count=1 ./internal/storage/...` 전부 ok(sqlite 32s, store 7s), `go build ./... && go vet ./...` ok.
- 게이트(HEAD `041306367`): `GATES=8 MET=8 UNMET=0`. 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳·재작업 없음. 처음 RED 는 시그니처 컴파일 실패라 동작의 RED 는 보지 못했다. 그래서 S03 처럼 변이 5개로 테스트의 힘을 확인했다.
- 첫 M1 변이는 컴파일 오류로 끝나 무효였다(rc=1 인데 `--- FAIL` 줄이 없음). 고쳐서 다시 돌렸다. 변이 결과는 rc 만 보지 말고 FAIL 줄과 메시지까지 본다.
- `ListLiveLadoTasks` 는 live 태스크 전용 쿼리(리비전·승인·스냅숏) 대신 프로젝트별 `ladoProjectPlans` 재사용을 골랐다. 쿼리 수는 1 + 프로젝트 수 × 4 지만 조립 경로가 하나이고, 테스트가 live 목록과 단건 읽기의 plan facts 를 `reflect.DeepEqual` 로 비교한다.
- 다음 단계 메모:
  - S07: impact 는 `ListLiveLadoTasks` 의 `Plan.ApprovalValid()` 와 `Plan.Snapshot.SHA256` 을 쓰면 된다. 닫힌 이슈의 태스크도 목록에 나온다(취소만 빠진다). 프로젝트 등록 여부는 보지 않으므로, 보관된 프로젝트의 태스크도 나온다(`ListLadoModeProjectIDs` 와 같은 주의).
  - S08: `ApproveLadoPlan` 에 `&snapshot` 을 넘기면 된다. `ApprovalID`·`CreatedAt` 은 채우지 않아도 된다. 스냅숏 id 를 다시 쓰면 일반 오류(500 경로)가 난다. 서비스가 매번 새 `lado-snapshot-<uuid>` 를 만들면 생기지 않는다.
  - S10: 규칙 생성·삭제 한 번에 프로젝트마다 `lado_rules` 이벤트가 여러 개 온다. 무효화를 묶을지 본다.
