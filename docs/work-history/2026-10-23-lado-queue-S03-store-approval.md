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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
