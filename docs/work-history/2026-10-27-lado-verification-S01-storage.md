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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 열·표 이름, 트리거 빌더 이름, sqlc 모델 이름)
