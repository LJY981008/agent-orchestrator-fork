# 2026-10-22 LADO 규칙 코어 — S01 마이그레이션 0005·sqlc 쿼리

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§2) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙 코어의 저장 바닥을 깐다. 규칙·버전·바인딩·스냅숏 테이블(LADO 트랙 0005)과 그 쿼리를 만들고 sqlc 생성물을 커밋한다. 뒤 단계(S03·S04)가 이 쿼리로 store 를 짠다.

## 범위

- 만든다: `backend/internal/storage/sqlite/migrations_lado/0005_lado_rules.sql`(플랜 §2 SQL 그대로 + 짧은 머리 주석), `backend/internal/storage/sqlite/queries/lado_rules.sql`, 생성물 `backend/internal/storage/sqlite/gen/lado_rules.sql.go`(sqlc 가 다른 gen 파일도 고치면 그것도 커밋).
- 테스트: `backend/internal/storage/sqlite/lado_migrate_test.go` 에 `TestLadoMigrateCreatesRuleTables` 추가.
- 하지 않는다: store 메서드(S03·S04), CDC 트리거(S04), 도메인 타입(S02). 업스트림 마이그레이션·`migrate`·`db.go` 는 건드리지 않는다(LADO 트랙은 디렉터리만 늘면 된다).

## 절차

1. 기존 `0001`~`0004` 와 `lado_migrate.go`·`lado_migrate_test.go` 를 읽고 같은 꼴로 0005 를 쓴다. `-- +goose Down` 은 다섯 테이블을 역순으로 지운다.
2. `queries/lado_rules.sql` 을 쓴다 — **ASCII 만**(주석에도 `§`·한글 금지). 최소 쿼리:
   - `InsertLadoRule :exec`, `GetLadoRuleRow :one`, `ListLadoRuleRows :many`(`sqlc.arg(include_archived)` 로 보관 포함 여부, `ORDER BY created_at, id`), `SetLadoRuleArchivedAt :execrows`, `DeleteLadoRuleRow :execrows`
   - `InsertLadoRuleVersion :one`(RETURNING 전 열), `GetLatestLadoRuleVersion :one`(`ORDER BY number DESC LIMIT 1`), `ListLatestLadoRuleVersions :many`(규칙마다 최고 번호), `ListLadoRuleVersions :many`(규칙별 `ORDER BY number`), `DeleteLadoRuleVersions :exec`
   - `UpsertLadoRuleBinding :exec`(`ON CONFLICT (rule_id, scope, scope_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`), `DeleteLadoRuleBinding :execrows`, `DeleteLadoRuleBindingsForRule :exec`, `ListLadoRuleBindings :many`(`ORDER BY rule_id, scope, scope_id`)
   - `InsertLadoPolicySnapshot :one`, `InsertLadoPolicySnapshotRule :exec`, `GetLadoPolicySnapshotByApproval :one`, `ListLadoPolicySnapshotsByProject :many`(approvals→tasks 조인, 프로젝트 기준), `ListLadoPolicySnapshotsByTask :many`, `CountLadoSnapshotRulesForRule :one`
   - `ListLadoModeProjectIDs :many`(`SELECT project_id FROM lado_project_settings WHERE lado_mode = 1 ORDER BY project_id`)
3. 저장소 루트에서 `npm run sqlc` → `gen/lado_rules.sql.go` 를 **직접 읽어** SQL 이 잘리지 않았는지 본다(`LIMIT 1`, `ON CONFLICT` 문장이 온전한지). 컴파일만으로는 이 버그를 못 잡는다.
4. 테스트 `TestLadoMigrateCreatesRuleTables`: `sqlitetest.MustOpen(t)`(또는 기존 LADO 마이그레이션 테스트의 방식)로 연 DB 에서 — LADO 트랙 최고 버전이 5, 다섯 테이블이 있다, 바인딩 CHECK 가 `('global','x','on')`·`('project','','force_on')`·`('task','t','on')`·`('global','','force_on')` 을 거부하고 `('global','','on')`·`('project','p','force_off')` 를 받는다, `lado_policy_snapshot_rules` 가 없는 버전 id 를 FOREIGN KEY 로 거부한다.
5. 커밋 메시지 예: `feat(lado): rule tables and queries`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(이 저장소 관례: 영어 conventional commit, Claude Code 의 Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다(같은 커밋 또는 뒤따르는 `docs(lado): record S01` 커밋). 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S01-migration.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoMigrateCreatesRuleTables` — 위 4번 전부

## 수용 게이트

- [ ] S01-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S01-G1: 마이그레이션 테스트가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesRuleTables
  EXPECT: GO_TESTS_OK 1/1
- [ ] S01-G2: 기존 LADO 마이그레이션·트리거 테스트 전부 통과
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ && echo S01_LADO_MIGRATE_OK
  EXPECT: S01_LADO_MIGRATE_OK
- [ ] S01-G3: sqlc 재생성이 커밋과 같다
  CHECK: bash .claude/gate/lado-rules-core.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S01-G4: LADO 쿼리 파일 ASCII
  CHECK: bash .claude/gate/lado-rules-core.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S01-G5: 생성 SQL 이 온전하다(sqlc 오프셋 버그 대조)
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/storage/sqlite/gen/lado_rules.sql.go 'ON CONFLICT (rule_id, scope, scope_id) DO UPDATE SET state = excluded.state' 'ORDER BY number DESC' 'LIMIT 1' 'FROM lado_project_settings'
  EXPECT: HAS_OK
- [ ] S01-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S01-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `855653a5c46bca4c071abfdf4683f6c51ece14f7`(`docs(lado): plan step 5 as a 13-step chain`), 트리 깨끗.
- LADO 트랙 최고 버전 4(`0004_lado_plans.sql`), 규칙 테이블·쿼리·gen 없음.
- 테스트를 먼저 쓰고 돌림 → `lado_goose_db_version = 4, want 0005 applied` 로 실패(RED 확인).

## After — 수행 후 실측

- 코드 커밋 `e8f8ca672` `feat(lado): rule tables and queries` — 5 파일, +927
  - `migrations_lado/0005_lado_rules.sql`: 플랜 §2 SQL 그대로(문장 사이 빈 줄만 추가) + 머리 주석, Down 은 다섯 테이블 역순 DROP
  - `queries/lado_rules.sql`: 엔트리의 최소 쿼리 24개 + `ListLadoRuleBindingsForRule`(규칙 하나의 바인딩) = 25개, ASCII 만
  - `gen/lado_rules.sql.go`(새), `gen/models.go`(`LadoRule`·`LadoRuleVersion`·`LadoRuleBinding`·`LadoPolicySnapshot`·`LadoPolicySnapshotRule` 추가). 다른 gen 파일 변화 없음
  - `lado_migrate_test.go`: `TestLadoMigrateCreatesRuleTables`
- gen 을 직접 읽어 확인: `ORDER BY number DESC` + `LIMIT 1`, `ON CONFLICT (rule_id, scope, scope_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`, `FROM lado_project_settings ... ORDER BY project_id` 모두 온전. `ListLadoRuleRows` 는 `CAST(?1 AS BOOLEAN)` → Go 인자 `includeArchived bool`.
- 일회용 스크래치 테스트(커밋 안 함, 실행 뒤 삭제)로 쿼리 동작 확인: 보관 필터 false 1행/true 2행, `created_at, id` 정렬, 최신 버전 3(단건·목록), upsert 가 on→off 갱신, LADO 모드 켠 프로젝트만 정렬 반환.
- 추가 실행: `go test -count=1 ./internal/storage/sqlite/ ./internal/storage/sqlite/sqlitetest/` ok(31.9s), `go test -count=1 -run Lado ./internal/storage/sqlite/store/` ok, `gofmt -l` 출력 없음.
- 게이트(HEAD `e8f8ca672`): `GATES=8 MET=8 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다(원장 `.claude/hooks/.state/gate-runs.jsonl`).

## 비교/회고

- 계획대로 끝났다. 막힌 곳·재작업 없음, sqlc 오프셋 버그도 재현되지 않았다(쿼리 파일 ASCII).
- 버전 검사는 `== 5` 대신 "0005 적용(최고 ≥ 5)"으로 썼다. 오늘 트리에서 최고는 5이고, 파일 최고 = 원장 최고는 `TestOpenAppliesLadoTrackInItsOwnVersionTable` 이 따로 본다. 0006 이 생겨도 이 테스트는 그대로 둔다.
- 이 테스트는 package `sqlite` 내부 테스트라 `sqlitetest`(sqlite 를 import)를 쓰면 import cycle 이다. 엔트리가 허용한 기존 방식 `openLadoDatabase(t, 1)`(업스트림 최신 스냅숏 + 실제 `Open` 1회, 약 1s)을 썼다.
- CHECK 거부 행은 수락 행보다 먼저 넣고 오류 문자열 `CHECK constraint failed` 로 확인했다. 그래서 `('global','','force_on')` 이 PK 충돌로 거부되는 거짓 통과가 없다. `('project','p','inherit')` 거부도 넣었다(플랜 §3: inherit 는 요청 전용, 저장하지 않음).
- FK 거부 뒤 같은 스냅숏에 실제 버전 id 를 넣어 성공시킨다. 그래서 거부 원인이 버전 FK 라는 것이 대조된다.
- `InsertLadoRule` 은 `archived_at` 을 받지 않는다. 새 규칙·내장 시드는 보관 상태로 생기지 않고, 보관은 `SetLadoRuleArchivedAt` 이 맡는다.
- 다음 단계 메모: gen 모델 이름(`gen.LadoRule` 등)이 S02 도메인 타입과 같으니 store 에서는 패키지 접두로 구분한다. `ListLadoPolicySnapshotsByProject/ByTask` 는 승인 시각 오름차순이라 가장 새 승인의 스냅숏이 마지막 행이다. 모든 프로젝트용 스냅숏 쿼리(`ListLiveLadoTasks`)가 필요하면 S04 엔트리 허용대로 거기서 추가한다.
