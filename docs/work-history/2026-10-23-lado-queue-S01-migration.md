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

- 시작 HEAD: `1de13dad5ddbd5661f4728a7a6ceab1b17cc3ce8`(`docs(lado): plan step 6 as a 13-step chain`, 태그 `lado-step6-chain-def` 와 같음), 트리 깨끗, `defs-unchanged` → `DEFS_UNCHANGED`.
- LADO 트랙 최고 버전 5(`0005_lado_rules.sql`), 대기열·실행·디스패치 설정 테이블·쿼리·gen 없음, `lado_project_settings` 는 `project_id, lado_mode, updated_at` 세 열.
- 테스트를 먼저 쓰고 돌림(RED 확인):
  - `TestLadoMigrateCreatesQueueTables` → `lado_goose_db_version = 5, want 0006 applied`
  - `TestLadoQueueMigrationRoundTrip`(Down 확인용으로 함께 씀) → `read project settings: SQL logic error: no such column: max_concurrent (1)`
  - 함께 정리한 기존 `TestLadoMigrateCreatesRuleTables` 는 PASS 유지.

## After — 수행 후 실측

- 코드 커밋 `059f6bbab` `feat(lado): queue, execution and dispatch settings tables` — 7 파일, +1518 −30
  - `migrations_lado/0006_lado_queue.sql`: 플랜 §2 SQL 그대로 + 머리 주석(TDD §4.2·§5.3, 세션 FK 없음, project_id 복사, 행 없음 = 기본값). Down 은 Up 역순: `ALTER TABLE lado_project_settings DROP COLUMN max_concurrent` → `lado_dispatch_settings` → `lado_executions` → `lado_queue_entries` DROP.
  - `queries/lado_queue.sql`: 엔트리의 쿼리 29개 그대로(항목 7·디스패치 9·실행 8·설정 4·`UpsertLadoProjectMaxConcurrent` 1), ASCII 만. 열린 항목 조건은 부분 인덱스 `lado_queue_entries_open` 과 같은 6조건.
  - `queries/lado_project_settings.sql`: `GetLadoProjectSettings`·`UpsertLadoProjectSettings` 의 SELECT/RETURNING 에 `max_concurrent` 추가(아래 회고).
  - gen: `lado_queue.sql.go`(새), `models.go`(`LadoQueueEntry`·`LadoExecution`·`LadoDispatchSetting` 추가, `LadoProjectSetting` 에 `MaxConcurrent int64`), `lado_project_settings.sql.go`(Scan 에 열 추가). 다른 gen 파일 변화 없음.
  - `lado_migrate_test.go`: `TestLadoMigrateCreatesQueueTables`, `TestLadoQueueMigrationRoundTrip`, 도우미 `tableExists`·`insertLadoSnapshotFixture`·`ladoMigrationProvider`(규칙 테스트의 같은 시드·테이블 확인을 도우미로 옮김).
- SQLite 가 `ADD COLUMN ... NOT NULL DEFAULT 1 CHECK (max_concurrent BETWEEN 1 AND 8)` 를 받는다(테스트에서 0·9 갱신이 `CHECK constraint failed`). 열 자신의 CHECK 라 `DROP COLUMN` 도 된다(라운드트립 테스트). CHECK 를 빼고 서비스에서 검증할 필요 없음.
- gen 을 직접 읽어 확인(오프셋 버그 없음): `LIMIT ?2`, `JOIN sessions s ON s.id = e.session_id` + `WHERE s.is_terminated = 0` + `GROUP BY t.project_id`, `ON CONFLICT (id) DO UPDATE SET` 네 열, `ON CONFLICT (project_id) DO UPDATE SET max_concurrent = excluded.max_concurrent, updated_at = excluded.updated_at`, `attempt_count = attempt_count + 1`·`- 1`, `IN (/*SLICE:task_ids*/?)` 모두 온전. 같은 이름 인자는 `?1`·`?2` 재사용으로 나온다(`ClaimLadoQueueEntry` 의 `Now`).
- 첫 생성에서 `ListLadoQueueEntries(ctx, projectID interface{})` 가 나왔다(`sqlc.narg(project_id) IS NULL` 이 앞이라 열 문맥이 없음). 순서를 `project_id = sqlc.narg(project_id) OR sqlc.narg(project_id) IS NULL` 로 바꿔 `sql.NullString` 이 되게 했다.
- 일회용 스모크 테스트(커밋 안 함, 실행 뒤 삭제)로 29개 쿼리를 실제 스키마에 돌림 — PASS: 설정 행 없음·모드 꺼짐이면 claimable 0·claim 0; claim CAS(리스 중 0행, 만료 뒤 재claim 1행, attempt 1→3); hold 는 attempt −1·리스 비움·대기 끝나기 전 claimable 0; release 는 attempt 유지, 낡은 토큰 0행; review/blocked/fail/dispatched 는 토큰 필요; 실행 insert·조회·`ListLadoExecutionsForTasks`(빈 슬라이스 0행); live 수는 세션 종료 뒤 0; 마감 지난 목록은 timed out 뒤 빠짐, `MarkLadoExecutionTimedOut` 두 번째 0행; supersede/cancel 은 열린 항목만; `ListLadoQueueEntries` NULL·`p`·없는 프로젝트 필터; 설정 upsert 두 번·cap 9 거부; 프로젝트 cap 기본 1, 행 없는 `Set` 0행, `Upsert` 가 모드 0 행 생성, 모드 변경이 cap 유지.
- 추가 실행: `go test -count=1 ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ ./internal/storage/sqlite/sqlitetest/` ok(32.5s·7.9s·0.5s), `golangci-lint run --path-mode=abs ./internal/storage/sqlite/...` 0 issues, `gofmt -l` 출력 없음.
- 게이트(HEAD `059f6bbab`): `GATES=8 MET=8 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다(원장 `.claude/hooks/.state/gate-runs.jsonl`).

## 비교/회고

- 계획대로 끝났다. 쿼리는 엔트리 목록 그대로 29개, 추가 쿼리 없음.
- 계획에 없던 수정 하나: `max_concurrent` 열이 생기면 sqlc 가 `GetLadoProjectSettings`·`UpsertLadoProjectSettings` 를 모델이 아닌 `...Row` 타입으로 바꿔 `store/lado_project_settings_store.go` 의 `ladoProjectSettingsFromGen(row gen.LadoProjectSetting)` 컴파일이 깨진다. store 대신 쿼리 두 개의 열 목록에 `max_concurrent` 를 더해 모델 타입을 유지했다(store·도메인 변경 없음). INSERT 는 그대로라 새 행은 기본값 1, 모드 변경은 cap 을 건드리지 않는다.
- 버전 검사는 5단계 S01 의 규칙 테스트와 같이 "0006 적용(최고 ≥ 6)"으로 썼다. 파일 최고 = 원장 최고는 `TestOpenAppliesLadoTrackInItsOwnVersionTable` 이 본다. 0007 이 생겨도 이 테스트는 그대로 둔다.
- "실제 Down" 을 확인하려고 `TestLadoQueueMigrationRoundTrip` 을 더했다(goose Provider `DownTo(5)` → 테이블·열 사라짐 → 0006 이전 모양의 프로젝트 행 → `Up` → 그 행의 cap 1). `-run 'Lado'` 에 걸리므로 G2 가 함께 돈다.
- 리스는 `lease_token`·`claimed_at`·`lease_expires_at` 세 열을 함께 쓰고 함께 비운다(업스트림 `automation_runs` 와 같은 꼴). supersede/cancel 은 리스를 비우지 않으므로, 리스 중에 대체·취소된 항목도 `ListLeasedLadoQueueEntries` 에 나와 복구가 정리한다.
- 다음 단계 메모:
  - 시각 인자 타입: nullable 열과 비교하는 `now` 는 `sql.NullTime`(`ListClaimableLadoQueueEntries`·`ClaimLadoQueueEntry`·`CountActiveLadoLeasesByProject`·`ListLadoExecutionsPastDeadline`), 토큰은 `sql.NullString`. 모든 시각은 UTC 로 넘긴다(문자열 비교). `AS TIMESTAMP` CAST 는 쓰지 말 것(NUMERIC 친화라 `'2026-10-23 10:00:00+00:00'` 이 정수 2026 이 된다 — SQLite 3.45 에서 확인).
  - `ClaimLadoQueueEntry` 는 `waiting_reason` 을 비우고 `next_attempt_at` 은 남긴다. `HoldLadoQueueEntry` 는 attempt −1(claim 이 센 1을 돌려줌).
  - `CountLiveLadoExecutionsByProject` 의 프로젝트는 `lado_tasks.project_id`(문자열)다. `sessions` 행이 없는 실행은 살아 있지 않은 것으로 센다.
  - `ListLadoExecutionsForTasks`·`GetLadoExecution` 은 `lado_executions` 열만 준다. S03 의 `SessionTerminated`·PR 사실은 S03 이 쿼리를 더하거나 따로 읽는다.
  - `ListLadoQueueEntries` 는 상태 필터 없이 모든 항목(대체·취소·디스패치 포함)을 준다. 대기열 화면에 무엇을 보일지는 S04 `ListLadoQueue` 가 정한다.
  - supersede/cancel 은 엔트리대로 열린 항목만 바꾼다. review required·blocked·failed 항목은 그대로 남는다(태스크 상태는 최신 승인의 항목만 본다).
