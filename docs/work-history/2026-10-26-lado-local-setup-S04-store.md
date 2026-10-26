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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 메서드 시그니처와 센티널 오류 이름)
