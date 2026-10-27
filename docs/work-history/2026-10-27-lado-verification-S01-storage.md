# 2026-10-27 LADO 검증·통합 승인 — S01 저장소: 마이그레이션 0008·변경 이벤트

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 a·e·h·m, §2) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

8단계의 모든 기록(검사 실행, head 별 검증 기록, 리뷰 회차, 근거 연결, 통합 승인)과 디스패치 때의 기준 브랜치 사실, 프로젝트 검사 명령이 들어갈 자리를 LADO 마이그레이션 트랙에 만든다. 화면이 바뀐 사실을 바로 받도록 변경 이벤트(`lado_execution`)도 여기서 건다. 이후 단계는 이 스키마 위에서만 일한다.

## 범위

- 만든다: `backend/internal/storage/sqlite/migrations_lado/0008_lado_verify.sql`(플랜 §2 그대로, Down 포함), `storage/sqlite/lado_migrate.go` 의 변경 이벤트 트리거(새 빌더 + 목록 항목), 테스트(`lado_migrate_test.go`).
- 생성물: `npm run sqlc` 로 `gen/models.go` 등(손으로 고치지 않는다).
- 하지 않는다: 쿼리 파일과 store 메서드(S03), 도메인 타입(S02).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S01 은 첫 단계라 읽을 것이 없다.
1. 기존 마이그레이션 `0002`·`0004`·`0006`·`0007` 과 `lado_migrate.go`(`ladoCDCTriggers`, `ladoExecutionCDCTrigger`, `ladoPlanCDCTrigger`)를 읽고 형식(주석, CHECK, 인덱스 이름, Down 순서)을 따른다.
2. `0008_lado_verify.sql` 을 플랜 §2 대로 쓴다: `lado_executions` 에 `base_branch`, `base_remote`, `dispatch_local_base_sha`, `settled_at`; `lado_project_settings` 에 `check_commands`(JSON, 기본 `[]`), `check_timeout_minutes`(5-120, 기본 30); 새 표 5개(`lado_check_runs`, `lado_verifications`, `lado_execution_reviews`, `lado_evidence_links`, `lado_integration_approvals`)와 인덱스. 업스트림 표로 가는 FOREIGN KEY 는 없다(LADO 표끼리만). Down 은 표를 지우고 열 6개를 역순으로 DROP COLUMN.
3. 변경 이벤트(플랜 §2 "Change events"): `execution_session_id` 로 세션을, `lado_executions` 를 거쳐 task 와 project 를 찾는 빌더를 하나 더한다(예: `ladoExecutionChildCDCTrigger(name, table, event, when string)`). `change_log.session_id` 는 `sessions(id)` 를 참조하므로 기존 `ladoExecutionCDCTrigger` 처럼 세션 행이 있을 때만 넣고, payload 는 늘 `{"kind":"lado_execution","sessionId":…,"taskId":…,"projectId":…}`. 트리거:
   - `lado_cdc_verifications_insert`, `lado_cdc_verifications_update`(`outcome`, `reason`, `sha256` 중 하나가 바뀔 때만 — `computed_at`·`notified_json` 만 바뀌면 없음)
   - `lado_cdc_integration_approvals_insert`, `lado_cdc_integration_approvals_update`(`status_state` 가 바뀔 때만)
   - `lado_cdc_check_runs_insert`, `lado_cdc_check_runs_update`(`status` 가 바뀔 때만)
   이름은 `lado_cdc_*` 이고 `ladoCDCTriggers` 목록에만 산다(마이그레이션에 두지 않는다). 근거 연결·리뷰 회차·설정 표는 이벤트가 없다(`TestLadoMigrateCreatesQueueTables` 가 설정 표 트리거 0 을 요구).
4. `npm run sqlc` 후 `gen/` 차이를 읽는다(모델 필드 추가만 있어야 한다). 쿼리 파일은 이 단계에서 만들지 않는다.
5. 커밋 메시지 예: `feat(lado): storage for verification and integration approvals`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S01-storage.md` 로 전건 MET 를 확인한다.

## 필수 테스트

`storage/sqlite`(마이그레이션 스냅숏에서 시작하는 기존 방식, `migratedDatabaseSnapshot` 또는 `sqlitetest`):

- `TestLadoMigrateCreatesVerifyTables` — 표·열·기본값(`check_commands` = `[]`, `check_timeout_minutes` = 30, `status_attempts` = 0), CHECK 거부(잘못된 `outcome`, `status`, `status_state`, `criterion` < 0, `round` 4, `check_timeout_minutes` 4, JSON 아닌 `content_json`), `proof_nonce` 가 `lado_presence_proofs` 를 참조하고 UNIQUE, `(execution_session_id, head_sha)` 승인 UNIQUE
- `TestLadoVerifyTablesPublishExecutionEvents` — 검증 기록 삽입·결과 변경은 `lado_execution` 이벤트(세션·task·project), `computed_at` 만 바꾸면 없음; 승인 삽입·`status_state` 변경, 검사 실행 삽입·`status` 변경도 같은 형식; 세션 행이 없으면 `session_id` NULL 로 들어가고 payload 는 세션을 이름
- `TestLadoMigrate0008DownRemovesWhatItAdded` — 트랙을 0007 로 내리면 새 표 5개와 열 6개가 없고, 다시 올리면 돌아온다(기존 데이터 행은 남는다)

기존 `TestLadoMigrateCreatesQueueTables`, `TestLadoMigrateCreatesSetupTables`, `TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds` 가 그대로 통과해야 한다.

## 수용 게이트

- [ ] S01-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S01-G1: 필수 테스트 3개와 기존 LADO 마이그레이션 테스트 3개가 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesVerifyTables TestLadoVerifyTablesPublishExecutionEvents TestLadoMigrate0008DownRemovesWhatItAdded TestLadoMigrateCreatesQueueTables TestLadoMigrateCreatesSetupTables TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds
  EXPECT: GO_TESTS_OK 6/6
- [ ] S01-G2: sqlc 생성물이 커밋과 같다
  CHECK: bash .claude/gate/lado-verification.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S01-G3: 트리거는 목록에만 있고 마이그레이션에는 없다
  CHECK: bash .claude/gate/lado-verification.sh none backend/internal/storage/sqlite/migrations_lado/0008_lado_verify.sql "CREATE TRIGGER"
  EXPECT: NONE_OK
- [ ] S01-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S01-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `d53600b3b`(`chore(lado): define the step 8 chain for verification and integration approval`, 태그 `lado-step8-chain-def` 와 같음), 트리 깨끗.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1, Node v24.21.0, umask 0022, TZ=UTC, 체인 러너의 `claude -p` 안(`CHAIN_RUN` 있음). sqlc 는 `npm run sqlc`(= `go run …/sqlc@v1.31.1 generate`).
- 출발점: LADO 트랙 마지막 = `0007_lado_setup.sql`. `lado_executions` 열 13개(`session_id` … `timed_out_at`), `lado_project_settings` 열 4개(`project_id, lado_mode, updated_at, max_concurrent`). `ladoCDCTriggers` 의 실행 이벤트는 `ladoExecutionCDCTrigger`(실행 행 자신, `NEW.session_id`) 둘뿐.
- 기준 실측: `bash .claude/gate/lado-verification.sh defs-unchanged` → `DEFS_UNCHANGED`; `go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesQueueTables TestLadoMigrateCreatesSetupTables TestMigrateDropsLadoChangeTriggersBeforeUpstreamRebuilds TestLadoQueueMigrationRoundTrip` → `GO_TESTS_OK 4/4`(6 s).
- 앞 단계 메모: 없음(첫 단계).

## After — 수행 후 실측

- 코드 커밋 `c5d7f22c6` `feat(lado): storage for verification and integration approvals` — 8 파일, +631/−27.
  - `migrations_lado/0008_lado_verify.sql`: 플랜 §2 그대로(열 6개, 표 5개, 인덱스 `lado_check_runs_by_head`·`lado_evidence_links_by_head`). 머리 주석은 0004·0006·0007 형식(ASCII, "TDD section"). Down = 표 5개를 만든 역순으로 DROP, 이어 열 6개를 역순으로 DROP COLUMN.
  - `lado_migrate.go`: 빌더 `ladoExecutionChildCDCTrigger(name, event, when)` — `NEW.execution_session_id` 로 `lado_executions` → `lado_tasks` → `projects` 를 잇고, `change_log.session_id` 는 `(SELECT sessions.id FROM sessions WHERE sessions.id = NEW.execution_session_id)`(세션 행 없으면 NULL), payload 는 늘 `{"kind":"lado_execution","sessionId","taskId","projectId"}`. 목록에 트리거 6개: `lado_cdc_verifications_insert`, `lado_cdc_verifications_update`(`outcome`·`reason`·`sha256`), `lado_cdc_integration_approvals_insert`, `lado_cdc_integration_approvals_update`(`status_state`), `lado_cdc_check_runs_insert`, `lado_cdc_check_runs_update`(`status`).
  - `lado_migrate_test.go`: 필수 테스트 3개와 도우미(`ladoVerifyTables`, `ladoVerifyColumns`, `ladoVerifyHead`, `insertLadoExecutionFixture`, `insertLadoPresenceProof`, `insertLadoVerifyRow`).
  - `queries/lado_project_settings.sql`·`queries/lado_queue.sql`: 기존 쿼리 4개의 열 목록에 새 열을 더함(아래 1).
  - `gen/`: `npm run sqlc` 생성물 — `models.go` 에 모델 5개와 `LadoExecution`·`LadoProjectSetting` 필드, 쿼리 6개가 새 열을 읽음(함수 시그니처는 그대로).
- 엔트리와 다르게 한 것과 이유:
  1. **기존 쿼리 파일 4곳을 고쳤다**(새 쿼리는 만들지 않음). 처음 `npm run sqlc` 에서 차이가 "모델 필드 추가만"이 아니었다: sqlc 는 열 목록이 표의 전체 열과 같을 때만 모델 타입을 돌려주는데, 열이 늘자 `GetLadoProjectSettings`·`UpsertLadoProjectSettings`(RETURNING)·`ListLiveLadoExecutions`·`ListLadoExecutionsPastDeadline` 이 새 `…Row` 타입을 돌려주게 되어 store 5곳(`lado_plan_store.go:424`, `lado_project_settings_store.go:26,47`, `lado_queue_store.go:466,483`)이 빌드를 깼다. store 를 고치는 대신 네 쿼리의 열 목록에 새 열을 더해 모델 타입을 되찾았다(ASCII 확인). `sqlc.embed(e)` 를 쓰는 `GetLadoExecution`·`ListLadoExecutionsForTasks` 는 저절로 새 열을 읽는다.
  2. 빌더 시그니처는 엔트리 예시 `(name, table, event, when)` 대신 이웃 빌더(`ladoExecutionCDCTrigger`, `ladoPlanCDCTrigger`)와 같은 `(name, event, when)` — `event` 가 이미 `AFTER … ON <표>` 를 담는다.
  3. 이벤트 테스트에 엔트리에 없던 확인 둘: S03 의 기록 upsert 모양(`INSERT … ON CONFLICT (execution_session_id, head_sha) DO UPDATE`)이 UPDATE 트리거만 부른다(바뀐 것 없는 upsert → 이벤트 없음, outcome 이 바뀐 upsert → 1개), 승인의 재시도(`status_attempts`·`status_error` 만 바뀜)와 검사 실행의 `log_sha256` 만 바뀜 → 이벤트 없음. 표 테스트에는 `notified_json`·`acknowledged_json` 기본 `[]`, 64자 head 허용, 다섯 표의 `execution_session_id` FK, 기록의 (실행, head) PRIMARY KEY 를 더했다.
- 확인한 명령과 결과:
  - `go-tests` 필수 3 + 기존 3 → `GO_TESTS_OK 6/6`(처음 실행부터 통과).
  - `go test -count=1 ./internal/storage/sqlite/` 전체 ok(36 s — `TestLadoQueueMigrationRoundTrip`(DownTo 5)·`TestReopenLeavesLadoTrackUnchanged`·업그레이드 경로 포함).
  - `go test -count=1 -run Lado ./internal/storage/sqlite/store/` ok(PASS 64), `go test -count=1 ./internal/service/lado...` 9 패키지 ok.
  - `go-lint ./internal/storage/sqlite/` → `GO_LINT_OK`, `gofmt -l` 출력 없음, 0008 은 ASCII 만.
- 게이트(HEAD `c5d7f22c6`, 엔트리 커밋 전): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 6/6`, G2 `SQLC_CLEAN`, G3 `NONE_OK`, G4 `GO_BUILD_OK`. 엔트리 커밋 뒤 `gate-check.sh --run` 으로 전건을 다시 돌려 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비: 엔트리 범위(마이그레이션 0008, 변경 이벤트 트리거 6개, sqlc, 필수 테스트 3)를 다 했다. 도메인 타입(S02)·쿼리와 store 메서드(S03)는 손대지 않았다 — 기존 쿼리 4곳의 열 목록만 넓혔다(After 1).
- 남은 틈: `InsertLadoExecution` 은 아직 새 열(`base_branch`, `base_remote`, `dispatch_local_base_sha`)을 쓰지 않아 기본값 `''` 로 들어간다. store 의 `ladoExecutionFromGen`·`ladoProjectSettingsFromGen` 은 새 필드를 아직 도메인으로 옮기지 않는다(S02·S03 몫).
- 다음 단계 메모:
  - **이름**: 표 `lado_check_runs`, `lado_verifications`(PK `(execution_session_id, head_sha)`), `lado_execution_reviews`(PK `review_run_id`), `lado_evidence_links`, `lado_integration_approvals`(UNIQUE `proof_nonce`, UNIQUE `(execution_session_id, head_sha)`); 열 `lado_executions.base_branch`·`base_remote`·`dispatch_local_base_sha`(TEXT, 기본 `''`)·`settled_at`(NULL), `lado_project_settings.check_commands`(JSON TEXT, 기본 `[]`)·`check_timeout_minutes`(5-120, 기본 30). 트리거 빌더 `ladoExecutionChildCDCTrigger`, 트리거 이름은 After 참고.
  - **sqlc 모델**(`gen`): `LadoCheckRun`(`ExitCode sql.NullInt64`, `FinishedAt sql.NullTime`), `LadoVerification`(`PRURL`, `ContentJson`, `Sha256`, `NotifiedJson`, `ComputedAt`), `LadoExecutionReview`(`Round int64`, `CancelledAt sql.NullTime`), `LadoEvidenceLink`(`Criterion int64`, `RefKind`, `RefKey`, `Sha`), `LadoIntegrationApproval`(`VerificationSha256`, `AcknowledgedJson`, `ProofNonce`, `StatusState`, `StatusAttempts int64`, `StatusPostedAt sql.NullTime`, `StatusError`); `LadoExecution` 에 `BaseBranch`, `BaseRemote`, `DispatchLocalBaseSha`, `SettledAt sql.NullTime`; `LadoProjectSetting` 에 `CheckCommands string`, `CheckTimeoutMinutes int64`. 상태 값은 모두 plain `string`(sqlc override 없음).
  - **S03(쿼리)**: sqlc 는 열 목록(또는 RETURNING)이 표 전체와 같을 때만 모델 타입을 돌려준다 — 새 쿼리는 `sqlc.embed(x)` 나 전체 열 목록을 쓰면 모델 타입을 받는다. 위 네 쿼리는 이제 전체 열을 나열하므로, 이 표들에 열을 또 더하면 같은 일이 생긴다. 기록 upsert 는 `ON CONFLICT (execution_session_id, head_sha) DO UPDATE` 로 UPDATE 트리거만 부른다(테스트로 확인) — `notified_json` 을 지키려면 SET 에서 빼면 된다. 승인 트랜잭션은 `lado_presence_proofs` 행을 먼저 넣어야 한다(FK); UNIQUE 위반 메시지는 `UNIQUE constraint failed: lado_integration_approvals.proof_nonce`(재사용)와 `…execution_session_id, lado_integration_approvals.head_sha`(같은 head 두 번 → `LADO_INTEGRATION_ALREADY_APPROVED`).
  - **S05(디스패치 기록)**: `InsertLadoExecution` 에 세 base 열을 더하거나 따로 UPDATE 한다. 실행 행 UPDATE 는 `timed_out_at` 이 바뀔 때만 이벤트를 낸다.
  - **트리거와 Down**: SQLite 는 트리거가 이름 대는 열을 DROP COLUMN 하지 못한다(sqlite 3.45 로 확인: `error in trigger t after drop column: no such column: OLD.settled_at`). 뒤 단계가 `lado_cdc_*` 트리거에 0008 의 열(`settled_at` 등)을 조건으로 넣으면 그 트리거가 있는 DB 에서 0008 Down 이 깨진다 — `TestLadoMigrate0008DownRemovesWhatItAdded` 가 잡는다. 정산(settle)은 지금 이벤트를 내지 않는다.
  - **테스트 도우미**(package `sqlite` 안): `insertLadoExecutionFixture`(실행 `session`, task `task`, 프로젝트 `p`, 세션 행 없음), `insertLadoVerifyRow(t, db, table)`, `insertLadoPresenceProof`. store 테스트는 자기 도우미(`insertLadoExecution` 등)를 쓴다.
