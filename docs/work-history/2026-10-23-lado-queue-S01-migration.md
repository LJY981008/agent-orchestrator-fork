# 2026-10-23 LADO 대기열·디스패처 — S01 마이그레이션 0006·sqlc 쿼리

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§2) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

대기열·실행·디스패치 설정의 저장 바닥을 깐다. LADO 트랙 0006 으로 `lado_queue_entries`·`lado_executions`·`lado_dispatch_settings` 를 만들고 `lado_project_settings` 에 프로젝트당 동시 실행 상한 열을 더한다. 뒤 단계(S03·S04)가 이 쿼리로 store 를 짠다.

## 범위

- 만든다: `backend/internal/storage/sqlite/migrations_lado/0006_lado_queue.sql`(플랜 §2 SQL 그대로 + 짧은 머리 주석 + 실제 Down), `backend/internal/storage/sqlite/queries/lado_queue.sql`, 생성물 `gen/lado_queue.sql.go`(sqlc 가 `gen/models.go` 나 다른 gen 파일을 고치면 그것도 커밋).
- 테스트: `backend/internal/storage/sqlite/lado_migrate_test.go` 에 `TestLadoMigrateCreatesQueueTables`.
- 하지 않는다: store 메서드(S03·S04), CDC 트리거(S03), 도메인 타입(S02). 업스트림 마이그레이션·`migrate`·`db.go` 는 건드리지 않는다.

## 절차

1. `0001`~`0005` 와 `lado_migrate.go`·`lado_migrate_test.go` 를 읽고 같은 꼴로 0006 을 쓴다. Down 은 `ALTER TABLE lado_project_settings DROP COLUMN max_concurrent` 와 세 테이블 DROP(역순). SQLite 의 `ADD COLUMN ... CHECK` 가 받아들여지는지 테스트로 확인한다(안 되면 CHECK 를 빼고 서비스에서 검증하고 After 에 적는다).
2. `queries/lado_queue.sql` — **ASCII 만**(주석에도 `§`·한글 금지). 최소 쿼리(이름 그대로):
   - 항목: `InsertLadoQueueEntry :exec`, `SupersedeOpenLadoQueueEntries :execrows`(task_id, superseded_at; 열린 항목만 — 플랜 §2 의 open 조건), `GetLadoQueueEntry :one`, `ListLadoQueueEntriesForTask :many`(enqueued_at DESC), `ListLadoQueueEntriesForTasks :many`(`sqlc.slice`), `ListLadoQueueEntries :many`(프로젝트 필터 `sqlc.narg`, enqueued_at), `CancelOpenLadoQueueEntriesForTask :execrows`
   - 디스패치: `ListClaimableLadoQueueEntries :many`(열림 + (lease_token IS NULL OR lease_expires_at <= ?) + (next_attempt_at IS NULL OR next_attempt_at <= ?) + `project_id IN (SELECT project_id FROM lado_project_settings WHERE lado_mode = 1)`, enqueued_at, id, LIMIT ?), `ClaimLadoQueueEntry :execrows`(id + 위 조건 재확인 → lease_token, claimed_at, lease_expires_at, attempt_count + 1, waiting_reason = ''), `ReleaseLadoQueueEntry :execrows`(id+token → 리스 비움, next_attempt_at, waiting_reason), `HoldLadoQueueEntry :execrows`(id+token → 리스 비움, attempt_count - 1, waiting_reason, next_attempt_at), `MarkLadoQueueEntryReviewRequired :execrows`, `MarkLadoQueueEntryBlocked :execrows`, `FailLadoQueueEntry :execrows`(failed_at, error_message), `SetLadoQueueEntryDispatched :execrows`(id+token → dispatched_session_id, 리스 비움), `ListLeasedLadoQueueEntries :many`
   - 실행: `InsertLadoExecution :exec`, `GetLadoExecution :one`, `GetLadoExecutionByEntry :one`, `ListLadoExecutionsForTasks :many`, `CountLiveLadoExecutionsByProject :many`(`lado_executions` JOIN `sessions` ON `sessions.id = lado_executions.session_id` WHERE `sessions.is_terminated = 0` GROUP BY project), `CountActiveLadoLeasesByProject :many`(lease_token NOT NULL AND lease_expires_at > ?), `ListLadoExecutionsPastDeadline :many`(deadline_at <= ? AND timed_out_at IS NULL AND 세션 살아 있음), `MarkLadoExecutionTimedOut :execrows`
   - 설정: `GetLadoDispatchSettings :one`, `UpsertLadoDispatchSettings :exec`(`ON CONFLICT (id) DO UPDATE SET ...`), `SetLadoProjectMaxConcurrent :execrows`(행이 없으면 서비스가 먼저 만든다 — 아래 3), `GetLadoProjectMaxConcurrent :one`
   - 실제 열 이름은 `sessions` 마이그레이션에서 확인한다(`is_terminated`, `project_id`). 쿼리가 업스트림 테이블을 읽는 것은 괜찮다(FK 만 금지).
3. 프로젝트 설정 행이 없는 프로젝트의 상한 저장: `lado_project_settings` 행이 없으면 `lado_mode = 0` 으로 만들고 상한을 쓰는 upsert 쿼리 `UpsertLadoProjectMaxConcurrent :exec` 를 쓴다(`ON CONFLICT (project_id) DO UPDATE SET max_concurrent = excluded.max_concurrent, updated_at = excluded.updated_at`).
4. 저장소 루트에서 `npm run sqlc` → `gen/lado_queue.sql.go` 를 **직접 읽어** SQL 이 잘리지 않았는지 본다(`ON CONFLICT`, `LIMIT`, `JOIN sessions`, `GROUP BY` 문장이 온전한지). 컴파일만으로는 이 버그를 못 잡는다.
5. 테스트 `TestLadoMigrateCreatesQueueTables`: LADO 트랙 최고 버전이 6; 세 테이블이 있다; `lado_project_settings.max_concurrent` 기본값 1; `lado_dispatch_settings` 가 `id = 2` 를 거부하고 `max_concurrent` 0·9 를 거부; `lado_queue_entries` 가 같은 `plan_approval_id` 두 번을 거부(UNIQUE)하고 없는 approval id 를 FOREIGN KEY 로 거부; `lado_executions` 가 같은 `queue_entry_id` 두 번을 거부.
6. 기존 트리거·마이그레이션 테스트(`TestOpenRecreatesLadoChangeTriggersAfterUpstream` 등)가 그대로 통과하는지 돌린다.
7. 커밋 메시지 예: `feat(lado): queue, execution and dispatch settings tables`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(이 저장소 관례: 영어 conventional commit, Claude Code 의 Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다(같은 커밋 또는 뒤따르는 `docs(lado): record S01` 커밋). 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S01-migration.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoMigrateCreatesQueueTables` — 위 5번 전부

## 수용 게이트

- [ ] S01-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S01-G1: 마이그레이션 테스트가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesQueueTables
  EXPECT: GO_TESTS_OK 1/1
- [ ] S01-G2: 기존 LADO 마이그레이션·트리거 테스트 전부 통과
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ && echo S01_LADO_MIGRATE_OK
  EXPECT: S01_LADO_MIGRATE_OK
- [ ] S01-G3: sqlc 재생성이 커밋과 같다
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S01-G4: LADO 쿼리 파일 ASCII
  CHECK: bash .claude/gate/lado-queue.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S01-G5: 생성 SQL 이 온전하다(sqlc 오프셋 버그 대조)
  CHECK: bash .claude/gate/lado-queue.sh has backend/internal/storage/sqlite/gen/lado_queue.sql.go 'ON CONFLICT (id) DO UPDATE SET' 'ON CONFLICT (project_id) DO UPDATE SET max_concurrent' 'is_terminated = 0' 'GROUP BY' 'attempt_count = attempt_count + 1'
  EXPECT: HAS_OK
- [ ] S01-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S01-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다: 시작 HEAD, 트리 상태, 먼저 쓴 테스트의 RED 결과)

## After — 수행 후 실측

(커밋 해시와 내용, 직접 확인한 생성 SQL, 추가로 돌린 테스트)

## 비교/회고

(계획과 달라진 점, 다음 단계가 알아야 할 것)
