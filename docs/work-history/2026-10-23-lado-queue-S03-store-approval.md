# 2026-10-23 LADO 대기열·디스패처 — S03 store: 승인 트랜잭션의 대기열 항목·태스크 읽기·CDC

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 a·b, §2 CDC, §4 첫 두 항목) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

계획 승인이 같은 트랜잭션에서 대기열 항목을 쓰고(이전 열린 항목은 superseded), 태스크 읽기가 최신 승인의 항목과 최신 실행(세션 종료 여부·PR 사실 포함)을 채우게 한다. 대기열·실행 행이 바뀌면 렌더러가 알 수 있게 CDC 를 건다.

## 범위

- 고친다: `backend/internal/storage/sqlite/store/lado_plan_store.go`(`ApproveLadoPlan` 에 `entry *domain.LadoQueueEntry` 인자 — 플랜 §4), `lado_task_store.go`(`LadoTask`·`ListLadoTasks`·`ListLiveLadoTasks` 가 `Queue`·`Execution` 을 채움; `CancelLadoTask` 가 열린 항목도 취소), `backend/internal/storage/sqlite/lado_migrate.go`(`ladoCDCTriggers` 에 대기열·실행 트리거 — 마이그레이션 파일에는 넣지 않는다), 이 시그니처를 쓰는 곳(`service/ladotask` 의 Store 인터페이스와 호출부는 **컴파일이 되게 nil 을 넘기는 정도만**; 실제 항목 생성은 S06).
- 만든다: `backend/internal/storage/sqlite/store/lado_queue_store.go`(이 단계에서는 읽기 도우미와 CDC 에 필요한 것만; 디스패처 primitive 는 S04), 필요하면 `queries/lado_queue.sql` 에 쿼리 추가(ASCII, `npm run sqlc`, gen 직접 읽기).
- 테스트: `store/lado_queue_store_test.go`(새), 기존 `lado_plan_store_test.go`·`lado_task_store_test.go` 의 `ApproveLadoPlan` 호출부 갱신, `storage/sqlite/lado_migrate_test.go` 의 트리거 목록 테스트가 새 트리거를 기대하게 갱신.
- 하지 않는다: claim/lease/link 등(S04), 서비스(S06), HTTP(S09).

## 절차

1. `ApproveLadoPlan(ctx, approval, proof, event, snapshot, entry)`: 기존 트랜잭션 순서(최신 리비전 확인 → 스냅숏 규칙 확인 → 증명 → 승인 → 스냅숏 → 감사) 안에서 스냅숏을 쓴 뒤 `SupersedeOpenLadoQueueEntries(task, approvedAt)` 와 `InsertLadoQueueEntry(entry)` 를 한다. `entry.PlanApprovalID`·`PolicySnapshotID` 는 store 가 방금 쓴 승인·스냅숏 id 로 채운다(인자 값과 다르면 덮어쓴다). `snapshot == nil` 또는 `entry == nil` 이면 항목을 쓰지 않는다. 어느 하나가 실패하면 전부 롤백(감사 체인 포함).
2. 태스크 읽기: 최신 승인(`Plan.Approval`)의 항목 하나와 태스크의 최신 실행 하나를 채운다. 실행에는 `sessions.is_terminated` 와 그 세션의 최신 PR 사실(`ListPRFactsForSession` 이 쓰는 원천에서 URL·번호·merged·closed)을 넣는다. 프로젝트 목록 읽기는 N+1 없이 태스크 묶음 단위 쿼리(`ListLadoQueueEntriesForTasks`·`ListLadoExecutionsForTasks`)와 세션 묶음 PR 읽기(`ListPRFactsForSessions`)로.
3. `CancelLadoTask`: 같은 트랜잭션에서 열린 항목에 `cancelled_at`. 실행 중인 세션을 끄는 것은 서비스(S06)의 몫.
4. CDC(플랜 §2): `lado_queue_entries` insert 와, 플랜 §2 의 열 중 하나라도 바뀌는 update 에 `{"kind":"lado_task","taskId":...,"projectId":...}`. `lado_executions` insert 와 `timed_out_at` update 에 `{"kind":"lado_execution","sessionId":...,"taskId":...,"projectId":...}`. 기존 트리거 빌더(`lado_migrate.go:91-144` 근처)의 꼴을 따른다(프로젝트 행마다 한 줄인지 등 — 기존 lado_task 트리거와 같게). 설정 테이블에는 트리거가 없다. `dropLadoCDCTriggers` 목록도 갱신.
5. 커밋 메시지 예: `feat(lado): queue entries in the approval and task reads with queue and execution`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S03-store-approval.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestApproveLadoPlanWritesQueueEntry` — 항목이 승인·스냅숏 id 와 함께 쓰인다; 두 번째 승인이 앞 항목을 superseded 로; nil 스냅숏이면 항목 없음; 승인이 실패(교체된 리비전)하면 항목도 없음
- `TestLadoTaskReadsCarryQueueAndExecution` — 단건·목록·live 읽기 모두 `Queue`(최신 승인의 것만)·`Execution`(세션 종료 여부, PR merged/closed) 를 채운다
- `TestCancelLadoTaskCancelsOpenEntries` — 열린 항목만 취소, 이미 dispatched 인 항목은 그대로
- `TestLadoQueueChangesPublishEvents` — 항목 insert/상태 update → `lado_task`; 무관한 열 update → 이벤트 없음; 실행 insert·timed_out → `lado_execution`; 설정 쓰기 → 이벤트 없음

## 수용 게이트

- [ ] S03-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S03-G1: 필수 테스트 4개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/storage/sqlite/store/ TestApproveLadoPlanWritesQueueEntry TestLadoTaskReadsCarryQueueAndExecution TestCancelLadoTaskCancelsOpenEntries TestLadoQueueChangesPublishEvents
  EXPECT: GO_TESTS_OK 4/4
- [ ] S03-G2: LADO store·마이그레이션 테스트 전부 통과
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ && echo S03_LADO_STORE_OK
  EXPECT: S03_LADO_STORE_OK
- [ ] S03-G3: sqlc 재생성이 커밋과 같다
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S03-G4: LADO 쿼리 파일 ASCII
  CHECK: bash .claude/gate/lado-queue.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S03-G5: CDC 트리거가 마이그레이션이 아니라 lado_migrate.go 에 있다
  CHECK: bash .claude/gate/lado-queue.sh none backend/internal/storage/sqlite/migrations_lado/0006_lado_queue.sql 'CREATE TRIGGER' && bash .claude/gate/lado-queue.sh has backend/internal/storage/sqlite/lado_migrate.go 'lado_queue_entries' 'lado_execution'
  EXPECT: HAS_OK
- [ ] S03-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S03-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `44e042ff6`(메인 세션의 `docs(lado): carry S02 notes into the later step entries` — S04~S13 엔트리만, S03 파일은 안 건드림; 그 앞이 `47e287b8a` `docs(lado): record S02`), 트리 깨끗. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1. `defs-unchanged` → `DEFS_UNCHANGED`.
- store: `ApproveLadoPlan(ctx, approval, proof, event, snapshot)` — 항목 인자 없음. 태스크 읽기(`LadoTask`·`ListLadoTasks`·`ListLiveLadoTasks`·`SetLadoTaskPreferences`)는 `Plan` 만 채움(`Queue`·`Execution` 은 늘 zero). `cancelLadoTask` 는 트랜잭션 없는 한 문장(`CancelLadoTask`), 항목은 건드리지 않음. `lado_queue_store.go` 없음.
- 쿼리: `ListLadoExecutionsForTasks` 는 `lado_executions` 열만(세션 종료 여부 없음). 승인·태스크 읽기가 쓰는 대기열 쿼리는 아직 호출부 없음.
- CDC: `ladoCDCTriggers` 13개(tasks 2·plan 4·rules 4·bindings 3) — 대기열·실행 트리거 없음. `dropLadoCDCTriggers` 는 `GLOB 'lado_cdc_*'` 로 지우므로 이름 목록이 없다(엔트리 절차 4의 "목록 갱신"은 할 것이 없음).
- `lado_migrate_test.go` 에 운영 트리거 목록을 기대하는 테스트는 없다(픽스처 트리거로 drop/재생성만 본다).
- 기준선 실패(S02 After 의 기록을 그대로 따름, 이 세션에서 다시 돌리지는 않음): store `TestLadoTaskReadsCarryPlanState`·`TestLadoTaskReadsCarryTheRuleSnapshot`(`approved` 기대), ladotask 4·ladopolicy 2(S06 몫).
- 테스트 순서: 필수 테스트를 코드 뒤에 썼다(RED 먼저가 아님). 대신 아래 After 의 변이 13개로 테스트가 실제로 잡는지 확인했다.

## After — 수행 후 실측

- 코드 커밋 `3425b6e04` `feat(lado): queue entries in the approval and task reads with queue and execution` — 13 파일, +849 −82.
  - `store/lado_queue_store.go`(새): `queueLadoApprovalTx`(supersede → 스냅숏과 항목이 있으면 insert), `fillLadoQueueFacts`(태스크 묶음의 `Queue`·`Execution`), 변환 `ladoQueueEntryFromGen`·`ladoExecutionFromGen`(S04 재사용).
  - `store/lado_plan_store.go`: `ApproveLadoPlan(..., snapshot, entry *domain.LadoQueueEntry)` — 스냅숏을 쓴 뒤, 감사 전에 `queueLadoApprovalTx`. 순서: 최신 리비전 → 스냅숏 규칙 → 증명 → 승인 → 스냅숏 → supersede·항목 → 감사, 한 트랜잭션.
  - `store/lado_task_store.go`: `withLadoTaskFacts`(단건 = 계획 + 대기열 사실, `LadoTask`·`SetLadoTaskPreferences`·`CancelLadoTask` 반환값), 목록 둘은 끝에 `fillLadoQueueFacts`. `cancelLadoTask` 는 `inTx` 안에서 `CancelLadoTask` + `CancelOpenLadoQueueEntriesForTask`.
  - `queries/lado_queue.sql`: `ListLadoExecutionsForTasks` 를 `sqlc.embed(e)` + `NOT EXISTS (살아 있는 sessions 행) AS session_terminated` 로. gen 은 `ListLadoExecutionsForTasksRow{LadoExecution, SessionTerminated bool}`(직접 읽어 SQL 온전 확인 — `ORDER BY e.task_id, e.started_at DESC, e.session_id DESC` 끝까지). 다른 호출부 없음.
  - `lado_migrate.go`: 트리거 4개 — `lado_cdc_queue_entries_insert`·`_update`(플랜 §2 의 9열 `IS NOT` 비교; `ladoPlanCDCTrigger` 재사용, NULL 세션·`lado_task`), `lado_cdc_executions_insert`·`_update`(`WHEN OLD.timed_out_at IS NOT NEW.timed_out_at`; 새 빌더 `ladoExecutionCDCTrigger`, `lado_execution` + `sessionId`·`taskId`·`projectId`). 프로젝트 행이 없으면 행 없음(기존 꼴). 설정 테이블에는 트리거 없음.
  - `service/ladotask`: Store 인터페이스 시그니처, `ApprovePlan` 은 `nil` 항목(S06 이 채움). `ladopolicy` 테스트 픽스처·`daemon/lado_wiring_test.go` 는 `nil` 하나 더.
  - 테스트: `store/lado_queue_store_test.go`(새) — 필수 4개와 도우미(`openLadoQueueStore` = `foreign_keys(ON)` raw 연결, `mustQueueLadoPlan`, `insertLadoExecution`·`dispatchLadoQueueEntry`(S04 의 link 전이라 gen 쿼리·raw UPDATE 로), `mustLadoSession`, `mustWriteLadoPR`, `ladoQueueRows`). `lado_plan_store_test.go` — 호출부에 `nil` 항목, `mustApproveLadoPlan` 은 항상 `lado-queue-<n>` 항목을 넘김(store 는 스냅숏이 있을 때만 씀), S02 가 짚은 두 테스트의 기대를 `queued`(스냅숏 + 항목)·`review_required`(스냅숏 없음)로. `lado_migrate_test.go` — `TestLadoMigrateCreatesQueueTables` 가 새 트리거 4개 존재·설정 테이블 트리거 0개를 본다.
- 엔트리가 정하지 않은 것을 다음처럼 정했다(테스트로 고정).
  1. supersede 는 스냅숏·항목이 없어도 한다(결정 a "더 새 승인이 열린 항목을 대체"). 항목 insert 만 둘 다 있을 때.
  2. 항목은 id 만 호출자의 것: `TaskID`(승인의 태스크)·`ProjectID`(트랜잭션 안에서 읽은 태스크 행 — 플랜 §2 "project_id is copied from the task")·`PlanApprovalID`·`PolicySnapshotID`·`EnqueuedAt`(= 승인 시각, 스냅숏 `created_at` 과 같은 꼴)을 store 가 채운다. 테스트가 틀린 값을 넘겨 덮어쓰기를 확인.
  3. 세션 행이 없는 실행은 종료로 본다(`CountLiveLadoExecutionsByProject` 와 같음). AO 는 seed 상태 세션만 지운다.
  4. PR 은 기존 `ListPRFactsForSessions`(세션 목록과 같은 원천·순서 `pr.updated_at DESC`)의 첫 행. 단건 읽기도 같은 묶음 경로(태스크 1개)를 탄다.
  5. 대기열 사실은 승인이 있는 태스크만 읽는다(승인 없으면 항목도 실행도 없음) — IN 목록이 작아진다. 쿼리 수는 태스크 수와 무관하게 항목·실행·PR 각 1번.
  6. 실행 트리거의 `change_log.session_id` 는 `(SELECT sessions.id FROM sessions WHERE sessions.id = NEW.session_id)`. `change_log.session_id` 는 `sessions(id)` FK 라, 세션 행이 없는 실행을 그대로 쓰면 실행 insert 자체가 실패한다(LADO 테이블은 sessions FK 없음, TDD §4.2). payload `sessionId` 는 늘 채운다.
  7. 승인 한 번은 `lado_task` 이벤트 2개(승인 insert + 항목 insert). 렌더러가 프로젝트 단위로 모은다.
- 확인: 필수 4개 PASS(`-v` 로 4개 모두 실행 확인). 변이 13개 모두 잡힘 — 트리거 열 추가/누락, 실행 트리거 세션 직접 쓰기, 실행 update 무필터, 가장 오래된 PR, 마지막 실행 우선, supersede 없음, 스냅숏 없이 항목, 호출자 프로젝트, live 읽기 누락, 항목 취소 누락, 세션 종료 누락(취소 변이 첫 판은 컴파일 실패라 동작 변이로 다시 함). `go test -count=1 -run 'Lado'` sqlite·store ok, store 패키지 전체 ok(7.3s), golangci-lint v2.13.2 `./internal/storage/sqlite/... ./internal/service/ladotask/... ./internal/service/ladopolicy/...` 0 issues, gofmt 없음, `go build ./... && go vet ./...` ok.
- 독립 리뷰 서브에이전트(Sonnet) 1회: 버그 없음, nit 1(항목 취소가 태스크 행 결과와 무관하게 도는 이유) → 주석으로 반영.
- 다른 패키지: `ladoproject`·`ladogate`·`ladoconnect`·`ladopresence` ok, `-run 'Lado'` 의 `daemon`·`httpd/controllers`·`session_manager`·`service/chat` ok. 의도된 깨짐은 S02 기록과 같은 6개뿐(메시지도 같음: ladotask `plans_test.go:586,628,725,807`, ladopolicy `service_test.go:900,1251`) — 서비스가 아직 `nil` 항목을 넘겨 승인된 태스크가 `review_required`(`approved_before_queue`). S06 몫.
- 게이트(HEAD `3425b6e04`): `GATES=8 MET=8 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: `withLadoTaskFacts` 도우미, `SetLadoTaskPreferences` 반환값도 대기열 사실을 채움(S02 메모), `TestLadoMigrateCreatesQueueTables` 의 트리거 확인(엔트리가 말한 "트리거 목록 테스트" 가 없어서 대신 더함), 쿼리 하나 변경(`ListLadoExecutionsForTasks`, 새 쿼리 없음).
- 엔트리 절차 4의 `dropLadoCDCTriggers` 목록 갱신은 할 것이 없었다: 이름 패턴(`lado_cdc_*`)으로 지운다.
- `ladoPlanCDCTrigger` 는 이름과 달리 `NEW.task_id` 의 태스크 변경을 낸다 — 대기열 항목에 그대로 썼다(이름 바꾸기는 범위 밖).
- 다음 단계 메모:
  - S04: supersede(`ApproveLadoPlan`)·취소(`CancelLadoTask`)는 리스를 비우지 않고, `SetLadoQueueEntryDispatched` 는 `id + lease_token` 만 본다. 스폰 중(리스 중)에 대체·취소된 항목도 link 가 된다 — 세션은 이미 있으니 실행은 기록하는 편이 맞고(고아 세션 방지), 그 세션을 끌지는 S07/S08(디스패처)·S06(취소 killer)이 정한다. link 결과에 "항목이 이미 대체·취소됨" 을 돌려주면 디스패처가 바로 끌 수 있다.
  - S04 절차 10(S02 메모 "태스크를 돌려주는 메서드는 모두 채울 것"): S03 이 이미 했다 — `SetLadoTaskPreferences`(`withLadoTaskFacts`)·`CancelLadoTask`(`LadoTask` 재읽기) 반환값이 `Queue`·`Execution` 을 채우고, 각각 `TestLadoTaskReadsCarryQueueAndExecution`·`TestCancelLadoTaskCancelsOpenEntries` 가 본다. `CreateLadoTask`·`LinkLadoTaskIssue` 는 승인이 있을 수 없어 채울 것이 없고, `ListPendingLadoTasks`·`ListLadoTasksToObserve` 는 원래 계획 사실도 채우지 않는다(관찰용).
  - S04: `LadoExecution(ctx, sessionID)`·`LadoExecutionByClientRequest` 도 `SessionTerminated`·PR 을 채울 것(`GetLadoExecution` 은 실행 열만 — `ListLadoExecutionsForTasks` 처럼 `sqlc.embed` + `NOT EXISTS`, PR 은 `ListPRFactsForSession` 첫 행). 변환은 `ladoExecutionFromGen`·`ladoQueueEntryFromGen` 재사용.
  - S04 테스트: `lado_queue_store_test.go` 의 raw 쓰기(`insertLadoExecution`·`dispatchLadoQueueEntry`)를 store primitive 로 바꿀 수 있다. raw 연결은 `foreign_keys(ON)` 이어야 change_log FK 를 store 와 같이 본다.
  - S06: `ApprovePlan` 은 `&domain.LadoQueueEntry{ID: "lado-queue-" + NewID()}` 만 넘기면 된다(나머지는 store 가 채움; `ProjectID` 를 넘겨도 무시). 실패 중인 6개를 고칠 것.
  - S10: `lado_execution` 이벤트는 `change_log.session_id` 를 채우므로 렌더러의 일반 세션 경로도 `sessionId` 를 본다. `lado-events.ts` 가 `lado_execution` 을 받기 전까지는 일반 세션 갱신만 일어난다(무해).
