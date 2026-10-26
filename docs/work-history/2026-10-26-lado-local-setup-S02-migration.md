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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 특히 S04 에 쿼리 이름·인자 순서·:execrows 여부)
