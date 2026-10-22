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

- 시작 HEAD: (단계 세션이 `git rev-parse HEAD` 를 적는다)

## After — 수행 후 실측

(단계 세션이 채운다: 커밋 SHA, 바뀐 파일, 게이트 결과)

## 비교/회고

(단계 세션이 채운다)
