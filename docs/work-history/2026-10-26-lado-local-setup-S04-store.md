# 2026-10-26 LADO 로컬 설정 채택 — S04 store: 채택 기록·버전·바인딩·수락·승인 고정·CDC

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§2, §4, §12 감사 동작) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

서비스가 쓸 저장 primitive 를 만든다. 쓰기마다 감사 이벤트(와 데스크톱 확인이 있으면 그 증명)가 같은 트랜잭션에 들어가고, 버전은 기준 버전을 확인하며, 승인 트랜잭션이 스냅숏이 쓴 setup 버전을 고정해 지워지지 않게 한다. 변경은 CDC 로 렌더러에 알린다.

## 범위

- 만든다: `backend/internal/storage/sqlite/store/lado_setup_store.go`(+ 테스트), `store/lado_plan_store.go` 의 승인 트랜잭션에 setup 고정, `storage/sqlite/lado_migrate.go` 의 `ladoCDCTriggers` 에 setup 트리거, 필요하면 `queries/lado_setup.sql` 에 쿼리 추가(ASCII)와 sqlc 재생성, `domain/lado_policy.go` 의 스냅숏 타입에 `SetupVersionIDs []string`.
- 하지 않는다: 서비스 로직(S05 이후), 스냅숏 내용 형식(S11).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 꼴은 `store/lado_rule_store.go`: `writeMu.LockContext` → `s.inTx` → 쓰기 + `appendLadoAuditEventTx`; 증명은 `insertLadoPresenceProof`(재사용은 `ErrLadoPresenceReplayed`); 센티널 오류는 `unwrapLadoSentinel` 로.
2. 메서드(플랜 §4; 이름은 이대로, 인자는 도메인 타입):
   - `RecordLadoSetupAdoption(ctx, adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error`
   - `MarkLadoSetupAdoptionPartial(ctx, id, message string) error`
   - `RestoreLadoSetupAdoption(ctx, id string, report []byte, at time.Time, event, proof) error` — 채택에 `restored_at`·보고서, 그 항목들에 `restored_at`. 이미 복원됨 → `ErrLadoSetupAlreadyRestored`.
   - `CreateLadoSetupItem(ctx, item, version, event, proof) error` — 살아 있는 같은 tool·kind·name 이 있으면 `ErrLadoSetupItemExists`.
   - `AddLadoSetupItemVersion(ctx, itemID string, baseVersion int, version, event, proof) error` — 기준이 최신이 아니면 `ErrLadoSetupItemChanged`(서비스가 409 `LADO_SETUP_ITEM_CHANGED` 로 바꿈), 살아 있지 않은 항목이면 `ErrLadoSetupItemNotLive`.
   - `DeleteLadoSetupItem(ctx, id string, at time.Time, event) error`(soft delete)
   - `SetLadoSetupBinding(ctx, b domain.LadoSetupBinding, inherit bool, event, proof) error` — inherit 면 행 삭제(전역은 행 삭제 = 켜짐).
   - `AcceptLadoConflict(ctx, a, event) error`, `RevokeLadoConflictAcceptance(ctx, id string, at time.Time, event) error`
   - 읽기: `ListLiveLadoSetupItems(ctx) ([]LadoSetupItemWithVersion, error)`, `LadoSetupItem(ctx, id) (item, versions, bool, error)`, `LadoSetupItemVersion(ctx, versionID)`, `ListLadoSetupBindings(ctx, scope, scopeID)`, `ListLadoSetupGlobalBindings(ctx)`, `ListLiveLadoConflictAcceptances(ctx)`, `ListLadoSetupAdoptions(ctx)`, `LadoSetupAdoption(ctx, id)`.
   - `LadoSetupVersionsForClientRequest(ctx, clientRequestID string) ([]LadoSetupItemWithVersion, bool, error)` — `lado:<entry>/<attempt>` → 항목 → 스냅숏 → 고정 버전(실행이 아니면 false). 파싱은 기존 `domain.ParseLadoClientRequestID`(이름은 코드에서 확인).
3. 승인 트랜잭션(`ApproveLadoPlan`): 스냅숏 행 다음에 `snapshot.SetupVersionIDs` 의 버전이 모두 있는지 확인하고 `lado_policy_snapshot_setup` 를 넣는다. 없는 버전 → 규칙 버전이 없을 때와 같은 오류로 아무것도 쓰지 않는다(nonce 포함; TDD §5.2). `SetupVersionIDs` 가 비면 아무것도 넣지 않는다(지금까지의 승인과 같음).
4. CDC(`ladoCDCTriggers`, 마이그레이션 금지): `lado_setup_items` insert·update, `lado_setup_item_versions` insert, `lado_setup_bindings` insert·update·delete, `lado_conflict_acceptances` insert·update 가 LADO 모드 프로젝트마다 `session_updated`(session_id NULL) 한 줄, payload `{"kind":"lado_setup","projectId":...}`. project 바인딩은 그 프로젝트만, task 바인딩은 그 태스크의 프로젝트만(규칙 바인딩 트리거 `ladoBindingCDCTrigger` 꼴). 트리거 이름은 `lado_cdc_*`.
5. 커밋 메시지 예: `feat(lado): store adopted setup items, versions and bindings`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S04-store.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `TestLadoSetupAdoptionRecordsItemsVersionsAndAudit` — 한 트랜잭션: 쓰기 하나가 실패하면 감사 포함 아무것도 남지 않음, 증명 nonce 재사용 거부
- `TestLadoSetupVersionNeedsTheLatestBase` — 기준 버전 틀림 → `ErrLadoSetupItemChanged`, 지운 항목 → `ErrLadoSetupItemNotLive`
- `TestLadoSetupBindingsSetAndInherit` — 전역 off/on, 프로젝트·태스크 force, inherit 는 행 삭제
- `TestLadoSetupRestoreMarksItems` — 채택·항목에 복원 시각, 두 번째 복원 거부, 버전은 남음
- `TestLadoSetupAcceptances` — 수락·취소, 취소된 것은 live 목록에 없음
- `TestApproveLadoPlanPinsSetupVersions` — 고정 행이 생김, 없는 버전이면 아무것도 쓰지 않음(승인·nonce·대기열 항목 없음)
- `TestLadoSetupVersionsForClientRequest` — 실행의 고정 버전, 실행 아닌 id 는 false
- `TestLadoSetupCDCPublishesPerLadoProject` — 항목·버전·바인딩·수락이 LADO 모드 프로젝트마다 `lado_setup` 한 줄, 모드 꺼진 프로젝트는 없음, 프로젝트 바인딩은 그 프로젝트만

## 수용 게이트

- [ ] S04-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S04-G1: 필수 테스트 8개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/storage/sqlite/store/ TestLadoSetupAdoptionRecordsItemsVersionsAndAudit TestLadoSetupVersionNeedsTheLatestBase TestLadoSetupBindingsSetAndInherit TestLadoSetupRestoreMarksItems TestLadoSetupAcceptances TestApproveLadoPlanPinsSetupVersions TestLadoSetupVersionsForClientRequest TestLadoSetupCDCPublishesPerLadoProject
  EXPECT: GO_TESTS_OK 8/8
- [ ] S04-G2: 기존 LADO store 테스트가 그대로 통과(승인 트랜잭션을 건드림)
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/storage/sqlite/store/ TestApproveLadoPlanRecordsProofAndAuditTogether TestApproveLadoPlanRefusesASupersededRevision TestLadoTaskReadsCarryPlanState
  EXPECT: GO_TESTS_OK 3/3
- [ ] S04-G3: sqlc 재생성이 커밋과 같고 쿼리 파일은 ASCII
  CHECK: bash .claude/gate/lado-local-setup.sh sqlc-clean && bash .claude/gate/lado-local-setup.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S04-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S04-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `8c6b07ae3`(`docs(lado): record S03`), 트리 깨끗. 태그 `lado-step7-chain-def` = `0e7bf9ee8`. 단계 도중 소유자 커밋 `afa0f12d9`(`docs(lado): widen the setup presence list before S12` — S12 엔트리와 플랜 한 줄씩, 이 단계 범위 밖)가 들어왔다.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 toolchain, PATH 맨 앞), umask 0022, TZ=UTC, LANG=C.UTF-8. golangci-lint v2.13.2 는 rules-core 세션 스크래치의 빌드(`GOPROXY=off`).
- 출발점: S02 의 `queries/lado_setup.sql`(쿼리 26개)과 gen 은 있고 store 메서드는 없다. `ladoCDCTriggers` 에 setup 트리거 없음(규칙 트리거는 `ladoRulesCDCTrigger`·`ladoBindingCDCTrigger`, payload 는 상수 `ladoRulesChangeInsert`). 도메인에 store 센티널 없음, `domain.LadoPolicySnapshot` 에 setup 필드 없음. `ApproveLadoPlan` 은 `checkLadoSnapshotRulesTx` → 증명 → 승인 → `insertLadoPolicySnapshotTx`(스냅숏·규칙 행) → 대기열 → 감사 순.
- 꼴: `store/lado_rule_store.go`(`writeMu.LockContext` → `inTx` → 쓰기 + `appendLadoAuditEventTx`, 기준 버전 확인은 tx 안에서 증명보다 먼저, 센티널은 `unwrapLadoSentinel`), `store/lado_queue_store.go` 의 `LadoEntryPolicyByClientRequest`(파싱 → 항목 → 스냅숏, 실행이 아니면 false).
- 앞 단계 메모 중 이 단계 몫: S02 — 쿼리 이름·인자, 두 embed 행은 매핑 함수 하나로, `LadoSetupVersionsForClientRequest` 는 새 쿼리 없이 `ParseLadoClientRequestID` → `GetLadoQueueEntry` → `ListLadoPolicySnapshotSetupVersions`, nullable 시각은 UTC, 승인 "아무것도 쓰지 않음" 테스트 표에 `lado_policy_snapshot_setup` 추가. S03 — 도메인 ↔ gen 짝(`AdoptionID ""` ↔ NULL, 끝 시각 zero ↔ NULL, `Size` ↔ `SizeBytes`, `Executable` ↔ 0/1, `OS` ↔ `Os`, `RestoreReport nil` ↔ `''`), 쓰기 전 `b.Validate()`(inherit 면 부르지 않음), 센티널 넷은 `domain/lado_setup.go` 끝에.

## After — 수행 후 실측

- 코드 커밋 `8776f83b1` `feat(lado): store adopted setup items, versions and bindings` — 7 파일, +1500 −31.
  - `store/lado_setup_store.go`(새로): 쓰기 9개와 읽기 9개(시그니처는 회고의 다음 단계 메모). 쓰기마다 `writeMu` + 한 트랜잭션 + 감사 이벤트, 증명이 있으면 같은 트랜잭션(재사용 → `ErrLadoPresenceReplayed`). 지움·취소는 `endLadoSetupRow`(CAS `:execrows`, 0 행이면 센티널) 하나로.
  - `store/lado_plan_store.go`: `insertLadoPolicySnapshotTx` 가 규칙 행 다음에 `SetupVersionIDs` 마다 `CountLiveLadoSetupItemVersion`(버전이 있고 항목이 live) → 0 이면 `ErrLadoRulesChanged` → `InsertLadoPolicySnapshotSetup`. 비면 아무것도 넣지 않는다. `LadoPolicySnapshot` 읽기가 `SetupVersionIDs` 를 채운다(tool·kind·name 순, 고정이 없으면 nil). `unwrapLadoSentinel` 에 새 센티널 다섯.
  - `storage/sqlite/lado_migrate.go`: 트리거 8개 — `lado_cdc_setup_items_insert`·`_update`, `lado_cdc_setup_item_versions_insert`(LADO 모드 프로젝트마다), `lado_cdc_setup_bindings_insert`·`_update`·`_delete`, `lado_cdc_conflict_acceptances_insert`·`_update`(범위별: 전역 → LADO 모드 프로젝트마다, 프로젝트 → 그 프로젝트, 태스크 → 태스크의 프로젝트). payload `{"kind":"lado_setup","projectId":…}`, `session_updated`, session_id NULL. 헬퍼는 kind 를 받게 일반화: 상수 `ladoRulesChangeInsert` → `ladoProjectChangeInsert(kind)`, `ladoRulesCDCTrigger` → `ladoModeCDCTrigger(kind, …)`, `ladoBindingCDCTrigger(kind, …)`. 규칙 트리거의 SQL 은 글자 그대로 같다.
  - `domain/lado_setup.go`: `LadoSetupItemWithVersion{Item, Version}`, 센티널 `ErrLadoSetupItemExists`·`ErrLadoSetupItemChanged`·`ErrLadoSetupItemNotLive`·`ErrLadoSetupAlreadyRestored`·`ErrLadoConflictAcceptanceNotLive`. `domain/lado_policy.go`: `LadoPolicySnapshot.SetupVersionIDs []LadoSetupItemVersionID`, `ErrLadoRulesChanged` 의 설명과 문구("a rule version or setup item the snapshot used no longer exists").
  - 테스트: 필수 8개(`store/lado_setup_store_test.go`), 기존 `TestApproveLadoPlanRollsBackWhenTheSnapshotFails`·`TestApproveLadoPlanRefusesARuleDeletedMeanwhile` 의 표 목록에 `lado_policy_snapshot_setup`.
- 엔트리와 다르게 한 것과 이유:
  1. id 인자는 S03 의 id 타입(`LadoSetupAdoptionID`, `LadoSetupItemID`, `LadoSetupItemVersionID`, `LadoConflictAcceptanceID`)이다 — 엔트리의 "id string" 대신 "인자는 도메인 타입" 을 따랐다. 같은 이유로 `SetupVersionIDs` 는 `[]string` 이 아니라 `[]domain.LadoSetupItemVersionID`(같은 구조체의 규칙 버전 id 처럼), 복원 보고서는 `[]byte` 와 같은 바이트인 `json.RawMessage`(도메인 `RestoreReport` 의 타입).
  2. 센티널 하나 더: `ErrLadoConflictAcceptanceNotLive`(없거나 이미 취소된 수락의 취소). 두 번 취소하는 경합을 서비스가 500 이 아닌 코드로 바꿀 수 있게.
  3. 바인딩·수락 쓰기는 살아 있는 항목만 받는다(inherit 포함, `ErrLadoSetupItemNotLive`). 지우거나 복원한 항목은 해석되지 않아 바인딩이 의미 없고 감사·CDC 만 남기 때문. 엔트리에 없던 확인이다.
  4. 승인 고정은 버전이 있는지에 더해 그 항목이 live 인지 본다(S02 가 이 용도로 만든 `CountLiveLadoSetupItemVersion`). 해석 뒤 지운 항목의 버전을 고정하면 꺼진 항목이 스냅숏에 켜진 채 남기 때문 — 규칙이 지워졌을 때와 같은 `ErrLadoRulesChanged`. 확인 위치는 엔트리대로 스냅숏 행 다음(트랜잭션이 되돌리므로 nonce 도 남지 않음).
  5. 수락 CDC 도 바인딩처럼 범위별이다. 엔트리는 "LADO 모드 프로젝트마다" 만 적었지만 수락도 scope·scope_id 가 있어 플랜 §2 의 "like the rule triggers" 를 따랐다.
  6. `RecordLadoSetupAdoption` 은 트랜잭션 전에 항목마다 첫 버전이 정확히 하나인지 확인한다(버전 없는 live 항목은 목록에서 안 보이면서 이름만 막는다). 항목의 `AdoptionID` 와 버전 번호 1 은 store 가 채운다. `CreateLadoSetupItem` 은 버전의 ItemID·번호 1, `AddLadoSetupItemVersion` 은 번호 base+1 을 채운다(규칙 store 와 같음). 쓰기는 `error` 만 돌려준다(엔트리 시그니처).
  7. 끝 시각이 0 인 지움·복원·취소는 거부한다 — `ladoNullTime` 이 0 을 NULL 로 바꿔 행은 살아 있는데 감사만 "지움" 으로 남는 것을 막는다.
  8. `MarkLadoSetupAdoptionPartial` 은 엔트리 시그니처대로 감사 이벤트 없이 UPDATE 하나다.
  9. 이름 충돌은 미리 조회하지 않고 live 색인(`lado_setup_items_live`)의 UNIQUE 위반을 `ErrLadoSetupItemExists` 로 바꾼다(PK 위반은 SQLite 가 따로 보고하므로 섞이지 않음). 쿼리 추가·sqlc 재생성은 필요 없었다.
- 확인한 명령과 결과:
  - `go test -count=1 ./internal/storage/... ./internal/domain/...` → 모두 ok(sqlite 32.6 s, store 8.0 s) — 규칙 CDC(`TestLadoRuleChangesPublishToLadoProjects`)와 마이그레이션 테스트 포함.
  - 변이 7건, 각각 해당 필수 테스트가 FAIL: 승인 live 확인 제거, 수락 update 트리거 제거, 수락 insert 트리거를 범위 없이, 기준 버전 확인 약화, 복원 항목 표시를 다른 채택으로, 항목에 채택 id 를 안 채움, 바인딩 live 확인 제거. 아무것도 바꾸지 않은 대조군은 PASS. 파일은 모두 되돌렸다(diff 확인).
  - golangci-lint v2.13.2 `run --path-mode=abs ./internal/storage/sqlite/... ./internal/domain/...` → 처음 `dupl` 2건(지움·취소) → `endLadoSetupRow` 로 묶은 뒤 0 issues.
  - 게이트(코드 커밋 뒤 직접): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 8/8`, G2 `GO_TESTS_OK 3/3`, G3 `SQLC_CLEAN`·`QUERIES_ASCII`, G4 `GO_BUILD_OK`, G5 `TREE_CLEAN`. 엔트리 커밋 뒤 `gate-check.sh --run` 으로 다시 기록한다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — store 메서드, 승인 트랜잭션의 setup 고정, 고정 읽기, CDC, 스냅숏 필드, 필수 테스트 8개. 서비스 로직·스냅숏 내용 형식은 없다. 엔트리 밖으로 더한 것은 위 2·3·6·7 뿐.
- 남은 틈:
  - 프로젝트 범위의 바인딩·수락은 LADO 모드가 꺼진 프로젝트에도 그 프로젝트로 한 줄 낸다(규칙 바인딩 트리거와 같은 꼴). 테스트는 이 경우를 단언하지 않는다.
  - 수락의 범위 조합(전역은 scope_id `''`, 프로젝트·태스크는 id 필수)은 표 CHECK 도 store 도 확인하지 않는다 — 서비스(S12)가 확인한다.
  - 항목 update 트리거에 WHEN 이 없다: 항목 행은 지움·복원 때만 바뀐다. 다른 열을 바꾸는 쿼리가 생기면 WHEN 을 붙인다.
  - `MarkLadoSetupAdoptionPartial` 은 감사가 없다. 채택이 제거 중 멈추면 `RecordLadoSetupAdoption` 에 처음부터 `State: partial`·`ErrorMessage` 로 넣어 감사 `setup.adopted` 와 한 트랜잭션에 두는 편이 낫다(S07).
- 다음 단계 메모 — 메서드 시그니처(모두 `*sqlite.Store`, `ctx context.Context` 첫 인자 생략)와 센티널:
  - 쓰기: `RecordLadoSetupAdoption(adoption domain.LadoSetupAdoption, items []domain.LadoSetupItem, versions []domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error` · `MarkLadoSetupAdoptionPartial(id domain.LadoSetupAdoptionID, message string) error` · `RestoreLadoSetupAdoption(id domain.LadoSetupAdoptionID, report json.RawMessage, at time.Time, event, proof) error` · `CreateLadoSetupItem(item domain.LadoSetupItem, version domain.LadoSetupItemVersion, event, proof) error` · `AddLadoSetupItemVersion(itemID domain.LadoSetupItemID, baseVersion int, version domain.LadoSetupItemVersion, event, proof) error` · `DeleteLadoSetupItem(id domain.LadoSetupItemID, at time.Time, event) error` · `SetLadoSetupBinding(b domain.LadoSetupBinding, inherit bool, event, proof) error` · `AcceptLadoConflict(a domain.LadoConflictAcceptance, event) error` · `RevokeLadoConflictAcceptance(id domain.LadoConflictAcceptanceID, at time.Time, event) error`.
  - 읽기: `ListLiveLadoSetupItems() ([]domain.LadoSetupItemWithVersion, error)`(최신 버전, tool·kind·name 순) · `LadoSetupItem(id) (domain.LadoSetupItem, []domain.LadoSetupItemVersion, bool, error)`(live 아니어도, 버전은 오래된 것부터) · `LadoSetupItemVersion(id domain.LadoSetupItemVersionID) (domain.LadoSetupItemVersion, bool, error)` · `ListLadoSetupBindings(scope domain.LadoRuleScope, scopeID string) ([]domain.LadoSetupBinding, error)` · `ListLadoSetupGlobalBindings() ([]domain.LadoSetupBinding, error)`(행 없음 = 켜짐) · `ListLiveLadoConflictAcceptances() ([]domain.LadoConflictAcceptance, error)`(오래된 것부터) · `ListLadoSetupAdoptions() ([]domain.LadoSetupAdoption, error)`(오래된 것부터) · `LadoSetupAdoption(id) (domain.LadoSetupAdoption, bool, error)` · `LadoSetupVersionsForClientRequest(clientRequestID string) ([]domain.LadoSetupItemWithVersion, bool, error)` · 승인은 `LadoPolicySnapshot(approvalID).SetupVersionIDs`.
  - 센티널(`domain`): `ErrLadoSetupItemExists`(live 이름 충돌 — 생성·채택), `ErrLadoSetupItemChanged`(기준 버전이 최신이 아님 → 409 `LADO_SETUP_ITEM_CHANGED`), `ErrLadoSetupItemNotLive`(없음·지움·복원 — 새 버전, 지움, 바인딩, 수락), `ErrLadoSetupAlreadyRestored`(두 번째 복원), `ErrLadoConflictAcceptanceNotLive`(없거나 취소된 수락의 취소), 그리고 기존 `ErrLadoPresenceReplayed`, `ErrLadoSetupBindingInvalid`(`Validate` 가 `%w` 로 감쌈, inherit 아닐 때만), 승인의 `ErrLadoRulesChanged`. 없는 채택의 partial·복원은 센티널 없는 오류 — 서비스가 먼저 `LadoSetupAdoption` 으로 404 를 가른다.
  - **S05**: managed 줄은 `ListLiveLadoSetupItems` 의 `Item.ID`·`Version.SHA256`.
  - **S07**: items 의 `AdoptionID` 와 versions 의 `Version` 은 store 가 덮어쓴다. versions 마다 `ItemID` 는 채워 보낸다. 제거 중 실패는 위 남은 틈의 방식(처음부터 partial)이 감사와 한 트랜잭션이다.
  - **S08**: 복원은 채택의 모든 항목(지운 것도)에 같은 `restored_at`, 버전은 남음. report `nil` → `''`. 유지(keep)는 `AddLadoSetupItemVersion(itemID, 최신 번호, …)`.
  - **S09**: `LadoSetupVersionsForClientRequest` 의 bool 은 "LADO 실행인가" 다 — 실행이면 고정이 없어도 `(빈 목록, true)` 이고 그때 홈은 관리 항목 없이 만드는 것이 결정 c 와 맞다(지금 버전으로 대신하지 않는다). 항목은 지금 상태(지움·복원 시각 포함)로 오지만 고정 버전은 live 여부와 상관없이 쓴다. 실행이 아니면 `ListLiveLadoSetupItems` + `ListLadoSetupGlobalBindings` + `ListLadoSetupBindings(LadoScopeProject, projectID)`.
  - **S11**: `ladotask` 승인이 `snapshot.SetupVersionIDs` 를 채우면 `ApproveLadoPlan` 이 고정한다. 끝난 항목·없는 버전은 `ErrLadoRulesChanged`(기존 409 `LADO_RULES_CHANGED` 경로). 승인 뒤 바뀐 것 비교는 `LadoPolicySnapshot(approvalID).SetupVersionIDs` 와 `LadoSetupItemVersion`.
  - **S12**: `SetLadoSetupBinding` 은 inherit=true 면 `b.State` 를 보지 않고 행을 지운다(전역 inherit = 행 삭제 = 켜짐). 수락 범위 조합은 서비스가 확인한다.
  - **S13·S14**: CDC 는 `session_updated`, session_id NULL, payload `{"kind":"lado_setup","projectId":…}` — 항목·버전은 LADO 모드 프로젝트마다, 바인딩·수락은 범위의 프로젝트. 채택 행 자체와 partial 표시는 아무것도 내지 않는다.
