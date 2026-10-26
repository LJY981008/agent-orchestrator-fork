# 2026-10-26 LADO 로컬 설정 채택 — S02 저장소: 마이그레이션 0007·쿼리·sqlc

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§2) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

채택·항목·버전·바인딩·충돌 수락·스냅숏 고정을 담을 표를 LADO 마이그레이션 트랙에 더한다. 뒤 단계(store, 서비스)는 이 표와 쿼리 위에서만 움직인다.

## 범위

- 만든다: `backend/internal/storage/sqlite/migrations_lado/0007_lado_setup.sql`(플랜 §2 그대로, Down 포함), `backend/internal/storage/sqlite/queries/lado_setup.sql`(ASCII 만), `npm run sqlc` 로 재생성한 `gen/lado_setup.sql.go`, 마이그레이션 테스트.
- 하지 않는다: store 메서드·CDC 트리거(S04), 도메인 타입(S03).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `0006_lado_queue.sql` 과 `0005_lado_rules.sql` 의 꼴(머리 주석, `-- +goose Up/Down`, CHECK 관례, 인덱스 이름)을 따라 `0007_lado_setup.sql` 을 쓴다. 표·열·CHECK·인덱스는 플랜 §2 그대로. 업스트림 표에는 FOREIGN KEY 를 걸지 않는다(LADO 표끼리만).
2. `queries/lado_setup.sql` — 플랜 §2 의 목록을 이름이 하는 일을 말하게 쓴다(sqlc 주석 `-- name: X :one|:many|:exec|:execrows`). 최소한:
   - `InsertLadoSetupAdoption`, `MarkLadoSetupAdoptionPartial`, `ListLadoSetupAdoptions`, `GetLadoSetupAdoption`, `MarkLadoSetupAdoptionRestored`, `MarkLadoSetupItemsRestoredForAdoption`
   - `InsertLadoSetupItem`, `InsertLadoSetupItemVersion`, `LatestLadoSetupItemVersion`, `ListLiveLadoSetupItemsWithLatestVersion`(항목과 최신 버전을 한 줄로), `GetLadoSetupItem`, `ListLadoSetupItemVersions`, `GetLadoSetupItemVersion`, `SoftDeleteLadoSetupItem`
   - `UpsertLadoSetupBinding`, `DeleteLadoSetupBinding`, `ListLadoSetupGlobalBindings`, `ListLadoSetupBindingsForScope`(scope, scope_id)
   - `InsertLadoConflictAcceptance`, `RevokeLadoConflictAcceptance`, `ListLiveLadoConflictAcceptances`
   - `InsertLadoPolicySnapshotSetup`, `ListLadoPolicySnapshotSetupVersions`(snapshot id → 버전·항목 열)
   - `CountLadoPolicySnapshotSetupForItem`(쓰인 항목 판단)
   시각은 UTC 값을 그대로 넘긴다(`CAST(... AS TIMESTAMP)` 금지 — TDD §4.2). UPDATE 안의 하위 쿼리가 같은 이름의 열을 읽으면 표 이름으로 한정한다(sqlc "ambiguous column").
3. `npm run sqlc` → `gen/` 의 새 파일을 읽어 SQL 이 잘리지 않았는지 확인한다(ASCII 만이어도 확인).
4. `storage/sqlite/lado_migrate_test.go` 에 `TestLadoMigrateCreatesSetupTables` 를 `TestLadoMigrateCreatesQueueTables` 꼴로 더한다: 여섯 표와 열, `lado_setup_items_live` 부분 유일 인덱스(살아 있는 같은 tool·kind·name 두 번째 삽입 거부, 지운 뒤에는 허용), 바인딩 CHECK(global 은 scope_id '' 와 on/off 만, project/task 는 scope_id 필수와 force_on/force_off 만), `lado_policy_snapshot_setup` 의 FK, 마이그레이션 Down 뒤 표가 없음. 스냅숏에서 시드하는 기존 도우미를 쓴다(`migratedDatabaseSnapshot`, 새 `Open` 반복 금지).
5. 커밋 메시지 예: `feat(lado): storage for adopted local setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S02-migration.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `TestLadoMigrateCreatesSetupTables` — 위 4 의 모든 단언

## 수용 게이트

- [ ] S02-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S02-G1: 마이그레이션 테스트가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/storage/sqlite/ TestLadoMigrateCreatesSetupTables TestLadoMigrateCreatesQueueTables TestLadoMigrateCreatesRuleTables
  EXPECT: GO_TESTS_OK 3/3
- [ ] S02-G2: 마이그레이션 파일이 플랜의 표를 모두 만든다
  CHECK: bash .claude/gate/lado-local-setup.sh has backend/internal/storage/sqlite/migrations_lado/0007_lado_setup.sql 'CREATE TABLE lado_setup_adoptions' 'CREATE TABLE lado_setup_items' 'CREATE TABLE lado_setup_item_versions' 'CREATE TABLE lado_setup_bindings' 'CREATE TABLE lado_conflict_acceptances' 'CREATE TABLE lado_policy_snapshot_setup' 'lado_setup_items_live' '-- +goose Down'
  EXPECT: HAS_OK
- [ ] S02-G3: sqlc 재생성이 커밋과 같고 쿼리 파일은 ASCII
  CHECK: bash .claude/gate/lado-local-setup.sh sqlc-clean && bash .claude/gate/lado-local-setup.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S02-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S02-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `050d8e112`(`docs(lado): record S01`), 트리 깨끗. 태그 `lado-step7-chain-def` = `0e7bf9ee8`.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 toolchain, PATH 맨 앞), Node v24.21.0, umask 0022, TZ=UTC. sqlc 는 `npm run sqlc`(= `go run …/sqlc@v1.31.1 generate`, 8.7 s).
- 출발점: LADO 트랙 0001~0006, `lado_goose_db_version` 최고 6. `lado_setup_*`·`lado_conflict_acceptances`·`lado_policy_snapshot_setup` 표, `queries/lado_setup.sql`, `gen/lado_setup.sql.go` 없음. `TestLadoMigrateCreatesQueueTables`·`TestLadoMigrateCreatesRuleTables`·`TestLadoQueueMigrationRoundTrip` 통과(1.1 s).
- 앞 단계 메모 중 이 단계 몫: S01 회고의 다음 단계 메모는 S09·S10·S13·9단계 몫이라 S02 에 해당 없음. 플랜 §0 공통 규칙(쿼리 ASCII, `gen/` 손수정 금지, 업스트림 표 FK 금지)과 TDD §4.2 의 두 함정(UPDATE 하위 쿼리의 같은 이름 열은 표로 한정, `CAST(... AS TIMESTAMP)` 금지)을 따른다.

## After — 수행 후 실측

- 코드 커밋 `984043662` `feat(lado): storage for adopted local setup` — 5 파일, +1233.
  - `migrations_lado/0007_lado_setup.sql`: 플랜 §2 의 6 표·열·CHECK·`lado_setup_items_live` 부분 유일 인덱스를 그대로, 머리 주석은 0005·0006 꼴(ASCII, HARNESS §8~10·§13, 바인딩 없음 = 켜짐, scope_id 에 FK 없음, 고정 버전은 계속 읽힘). Down 은 역순 `DROP TABLE` 6개(자식 먼저라 `foreign_keys=ON` 에서도 행이 든 채로 지워진다).
  - `queries/lado_setup.sql`: 쿼리 26개(엔트리 최소 목록 24 + 아래 2). 하위 쿼리는 `v2.item_id` 처럼 모두 별칭으로 한정, 시각 CAST 없음.
  - `gen/lado_setup.sql.go`(새), `gen/models.go`(모델 6개 추가: `LadoConflictAcceptance`, `LadoPolicySnapshotSetup`, `LadoSetupAdoption`, `LadoSetupBinding`, `LadoSetupItem`, `LadoSetupItemVersion`).
  - `lado_migrate_test.go`: `TestLadoMigrateCreatesSetupTables`.
- TDD: 테스트를 먼저 써서 `lado_goose_db_version = 6, want 0007 applied` 로 실패하는 것을 본 뒤 마이그레이션을 썼다.
- `TestLadoMigrateCreatesSetupTables` 단언: 6 표의 열(순서까지); 각 표의 FK 가 `lado_` 표만 가리킴(`pragma_foreign_key_list`); 부분 유일 인덱스 — 살아 있는 같은 tool·kind·name 두 번째 삽입 UNIQUE 거부, 같은 이름의 다른 kind 허용, `deleted_at` 뒤 허용, `restored_at` 뒤 허용; 없는 채택을 가리키는 항목 FK 거부; 버전 UNIQUE(item_id, version); CHECK 거부 11개(채택 state·plan_sha256 길이·restore_report JSON, 항목 tool·kind·빈 name, 버전 0·content_kind·executable 2, 수락 content_sha256 길이·scope); 바인딩 CHECK 거부 8 조합(global+scope_id, global+force_off, project/task+빈 scope_id, project+on, task+off, inherit, 모르는 scope)과 허용 3 조합; `lado_policy_snapshot_setup` FK(없는 버전, 없는 스냅숏)와 같은 고정 두 번 거부; 행이 든 채 `DownTo(6)` 뒤 6 표 없음, `Up` 뒤 다시 있음. 시드는 기존 `openLadoDatabase(t, 1)`(= `migratedDatabaseSnapshot` + `Open` 1회).
- gen 확인: 생성된 SQL 26개를 쿼리 파일 본문(주석 줄 제외)과 python 으로 대조해 모두 같음(embed 쿼리는 sqlc 가 SELECT 목록을 열로 펼치므로 `FROM` 이하 비교). 잘림 없음, 두 `sqlc.embed` 는 행 타입에 `LadoSetupItem`·`LadoSetupItemVersion` 두 필드로 나옴.
- 쿼리 의미 확인(스크래치 테스트, 커밋 안 함, 지움): 26 쿼리 전부를 실제 마이그레이션 DB 에서 돌림 — 최신 버전 선택, live 필터, 정렬(tool·kind·name 의 알파벳 순: `claude-code` < `codex`, `command` < `skill`), CAS 쿼리의 영향 행 1 → 0, 복원 뒤에도 고정 버전이 읽힘, `MarkLadoSetupItemsRestoredForAdoption` 이 지운 항목까지 셈, 시각 왕복(`2026-10-26T10:00:00.123456789Z` 로 저장, UTC 로 읽힘).
- 엔트리와 다르게 한 것과 이유: 쿼리 2개를 더했다. `GetLiveLadoSetupItemByName`(tool, kind, name) — S04 `CreateLadoSetupItem` 이 `ErrLadoSetupItemExists` 를 제약 오류 문자열이 아니라 트랜잭션 안 조회로 판단하게. `CountLiveLadoSetupItemVersion`(id) — 승인 트랜잭션의 고정 확인용, 규칙의 `CountLadoRuleVersion` 짝. setup 버전은 지워지지 않아 "있음"만으로는 진행 중 삭제·복원을 못 잡으므로 항목이 live 인지까지 본다(규칙 삭제 = 버전 삭제와 같은 뜻). 테스트의 추가 단언(FK 범위, CHECK 11개, Up 재적용)은 플랜 §2 를 그대로 옮겼는지 보려는 것.
- 확인한 명령: `go test -count=1 ./internal/storage/sqlite/...` 전부 ok(sqlite 32.3 s, sqlitetest 0.6 s, store 7.7 s) — 기존 `TestLadoQueueMigrationRoundTrip`(`DownTo(5)`)이 0007 Down 도 지나간다. golangci-lint v2.13.2(rules-core 스크래치 빌드, `GOPROXY=off`) `./internal/storage/sqlite/...` 0 issues. `gofmt -l` 출력 없음. 마이그레이션·쿼리 파일에 비 ASCII 없음.
- 게이트(HEAD `984043662`): `GATES=6 MET=6 UNMET=0`(13.8 s). 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 마이그레이션·쿼리·gen·마이그레이션 테스트. store 메서드·CDC 트리거·도메인 타입은 없다.
- 남은 틈:
  - CDC 트리거 없음(S04 몫, `ladoCDCTriggers`). 0007 Down 은 표와 함께 그 표의 트리거도 지운다(SQLite `DROP TABLE`).
  - 인덱스는 플랜 §2 의 `lado_setup_items_live` 하나뿐이다. `lado_setup_bindings (scope, scope_id)` 조회와 `lado_policy_snapshot_setup.item_version_id` 조인은 색인 없이 훑는다(규칙에는 `lado_rule_bindings_by_scope` 가 있다). 항목이 수십 개라 문제없고, 커지면 새 마이그레이션으로 더한다.
  - `ListLadoSetupAdoptions` 는 오래된 것부터(created_at, id). 화면이 최근 것부터 원하면 서비스나 S15 에서 뒤집는다.
- 다음 단계 메모:
  - **S03(도메인)**: gen 모델은 `LadoSetupAdoption`, `LadoSetupItem`, `LadoSetupItemVersion`, `LadoSetupBinding`, `LadoConflictAcceptance`, `LadoPolicySnapshotSetup`. 열 `os` → 필드 `Os`, `executable` → `int64`(0/1), `adoption_id` → `sql.NullString`, 끝 시각들 → `sql.NullTime`. 전역 바인딩은 `scope_id = ''` 이어야 CHECK 를 지난다.
  - **S04(store)** — 쿼리 이름·인자(gen Params 필드 순)·반환:
    - 채택: `InsertLadoSetupAdoption :exec {ID, CreatedAt, Actor, Os, PlanSha256, ManifestSha256, State, ErrorMessage}`(state 를 받으므로 처음부터 `partial` 로도 기록 가능; restored_at·restore_report 는 기본값) · `MarkLadoSetupAdoptionPartial :execrows {ErrorMessage, ID}` · `ListLadoSetupAdoptions :many`(인자 없음) · `GetLadoSetupAdoption :one (id)` · `MarkLadoSetupAdoptionRestored :execrows {RestoredAt sql.NullTime, RestoreReport, ID}` — `restored_at IS NULL` CAS, 0 이면 Get 으로 없음과 `ErrLadoSetupAlreadyRestored` 를 가른다; restore_report 는 `''` 또는 JSON(CHECK) · `MarkLadoSetupItemsRestoredForAdoption :execrows {RestoredAt sql.NullTime, AdoptionID sql.NullString}` — 지운 항목도 포함, 이미 복원된 것은 제외.
    - 항목: `InsertLadoSetupItem :exec {ID, Tool, Kind, Name, OriginPath, AdoptionID sql.NullString, CreatedAt}`(살아 있는 같은 이름이면 `UNIQUE constraint failed: lado_setup_items.tool, lado_setup_items.kind, lado_setup_items.name`) · `GetLadoSetupItem :one (id)` · `GetLiveLadoSetupItemByName :one {Tool, Kind, Name}`(없으면 `sql.ErrNoRows`) · `SoftDeleteLadoSetupItem :execrows {DeletedAt sql.NullTime, ID}` — live 만, 0 이면 없음 또는 live 아님.
    - 버전: `InsertLadoSetupItemVersion :exec {ID, ItemID, Version, ContentKind, Sha256, SizeBytes, Executable, Actor, CreatedAt}`(UNIQUE(item_id, version) 가 마지막 벽) · `LatestLadoSetupItemVersion :one (itemID)` — 기준 버전 확인 · `ListLadoSetupItemVersions :many (itemID)` version 오름차순 · `GetLadoSetupItemVersion :one (id)` · `CountLiveLadoSetupItemVersion :one (id)` — 버전이 있고 항목이 live 면 1 · `ListLiveLadoSetupItemsWithLatestVersion :many`(인자 없음) — 행 `{LadoSetupItem, LadoSetupItemVersion}`, tool·kind·name 순.
    - 바인딩: `UpsertLadoSetupBinding :exec {ItemID, Scope, ScopeID, State, UpdatedAt}`(ON CONFLICT 는 UPDATE 트리거를 탄다) · `DeleteLadoSetupBinding :execrows {ItemID, Scope, ScopeID}` · `ListLadoSetupGlobalBindings :many`(인자 없음) · `ListLadoSetupBindingsForScope :many {Scope, ScopeID}` — 둘 다 item_id 순.
    - 수락: `InsertLadoConflictAcceptance :exec {ID, Code, ItemID, Detail, ContentSha256, Scope, ScopeID, Actor, CreatedAt}` · `RevokeLadoConflictAcceptance :execrows {RevokedAt sql.NullTime, ID}` — `revoked_at IS NULL` CAS · `ListLiveLadoConflictAcceptances :many`(인자 없음) created_at, id 순.
    - 고정: `InsertLadoPolicySnapshotSetup :exec {SnapshotID, ItemVersionID}` · `ListLadoPolicySnapshotSetupVersions :many (snapshotID)` — 행은 위 live 목록과 같은 두 embed 필드, tool·kind·name·id 순, 지우거나 복원한 항목의 고정 버전도 나온다 · `CountLadoPolicySnapshotSetupForItem :one (itemID)`.
    - 두 embed 행 타입은 이름만 다르고 필드가 같다: `(gen.LadoSetupItem, gen.LadoSetupItemVersion)` 을 받는 매핑 함수 하나로 둘 다 처리한다.
    - `LadoSetupVersionsForClientRequest` 는 새 쿼리 없이 `domain.ParseLadoClientRequestID` → 기존 `GetLadoQueueEntry` 의 `PolicySnapshotID` → `ListLadoPolicySnapshotSetupVersions`.
    - 시각은 `.UTC()` 로 넘기고 nullable 시각 인자는 `sql.NullTime{Time: at.UTC(), Valid: true}`.
    - 승인 트랜잭션의 "아무것도 쓰지 않음" 테스트 표 목록(`store/lado_plan_store_test.go:478`, `:525`)에 `lado_policy_snapshot_setup` 를 더한다.
    - `TestLadoMigrateCreatesSetupTables` 는 트리거 유무를 단언하지 않는다(CDC 는 S04 테스트 몫). 트리거가 생겨도 이 테스트의 Down·Up 은 그대로 돈다.
    - 0007 은 아직 병합 전이라 S04 가 표를 고쳐야 하면 같은 파일을 고치고 `npm run sqlc`·이 테스트를 다시 돌린다(병합 뒤에는 새 마이그레이션).
