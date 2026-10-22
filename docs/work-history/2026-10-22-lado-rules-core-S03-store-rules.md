# 2026-10-22 LADO 규칙 코어 — S03 store: 규칙·버전·바인딩·내장 시드

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§4) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙을 저장·조회·버전업·보관·삭제하고 바인딩을 바꾸는 store 계층을 만든다. 모든 쓰기는 감사 이벤트와 한 트랜잭션이다(HARNESS §8 쓰기 계약 4: 버전·바인딩·감사가 함께, 활성 버전에 대한 낙관적 동시성).

## 범위

- 만든다: `backend/internal/storage/sqlite/store/lado_rule_store.go`, 테스트 `backend/internal/storage/sqlite/store/lado_rule_store_test.go`.
- 메서드(플랜 §4): `EnsureLadoBuiltinRules`, `ListLadoRules`, `LadoRule`, `ListLadoRuleVersions`, `CreateLadoRule`, `AddLadoRuleVersion`, `SetLadoRuleArchived`, `SetLadoRuleBinding`, `ListLadoModeProjectIDs`. `DeleteLadoRule` 도 여기서 만들되 "스냅숏이 쓴 규칙 거부" 검사는 `CountLadoSnapshotRulesForRule` 로 넣고, 그 실증 테스트는 S04(스냅숏 쓰기가 생긴 뒤)가 한다.
- `unwrapLadoSentinel`(`lado_plan_store.go`)에 `domain.ErrLadoRuleChanged`, `domain.ErrLadoRuleReferenced` 를 더한다.
- 하지 않는다: 승인+스냅숏(`ApproveLadoPlan` 변경), plan facts, `ListLiveLadoTasks`, CDC — 전부 S04. 서비스 계층(S07).

## 절차

1. `lado_plan_store.go`·`lado_audit_store.go` 의 꼴을 따른다: `s.writeMu.LockContext` → `s.inTx(ctx, "<동작>", func(q *gen.Queries) error {...})` → 같은 tx 에서 `appendLadoAuditEventTx`. 읽기는 `s.qr`.
2. `ListLadoRules` 는 행·최신 버전(`ListLatestLadoRuleVersions`)·바인딩(`ListLadoRuleBindings`)을 읽어 `domain.LadoRule` 로 조립한다: global 바인딩 → `Global`, project/task 바인딩 → `Overrides`. 정렬 `created_at, id`. `includeArchived=false` 면 보관된 규칙을 뺀다.
3. `CreateLadoRule`: 규칙 행 + 버전 1(`Version.Number` 는 1 로 강제) + global 바인딩(`rule.Global`) + `rule.Overrides` 의 바인딩 + 감사 이벤트.
4. `AddLadoRuleVersion(v, base, proof, event)`: tx 안에서 최신 번호를 읽어 `base` 와 다르면 `domain.ErrLadoRuleChanged`; `proof != nil` 이면 `insertLadoPresenceProof`(재사용 nonce = `ErrLadoPresenceReplayed`); 버전 번호 = base+1 로 삽입; 감사 이벤트. 실패하면 아무것도 남지 않는다.
5. `SetLadoRuleBinding`: `State == inherit` 이면 행 삭제, 아니면 upsert. global 에 force_* 나 project 에 on 처럼 CHECK 가 막는 조합은 DB 오류가 그대로 올라온다(검증은 서비스가 먼저 한다).
6. `EnsureLadoBuiltinRules(seeds)`: 한 tx 에서 id 가 없는 것만 `CreateLadoRule` 과 같은 삽입 + 그 seed 의 이벤트. 추가한 id 목록을 돌려준다. 두 번 불러도 행·이벤트가 늘지 않는다.
7. 테스트는 `sqlitetest.MustOpen(t)` 로 연다. 감사 체인은 `VerifyLadoAuditChain` 으로 확인한다.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): store rules, versions and bindings` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S03-store-rules.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestEnsureLadoBuiltinRulesIsIdempotent` — 두 번 불러도 규칙·버전·바인딩·감사 이벤트 수가 그대로, 두 번째 반환은 빈 목록
- `TestLadoRuleRoundTrip` — 생성한 규칙을 `LadoRule`·`ListLadoRules` 로 읽으면 내용·sha256·Global·Overrides 가 같다
- `TestAddLadoRuleVersionRefusesStaleBase` — base 가 최신이 아니면 `ErrLadoRuleChanged`, 버전·감사 행이 늘지 않는다
- `TestAddLadoRuleVersionRefusesReplayedProof` — 같은 nonce 로 두 번째 버전업이 `ErrLadoPresenceReplayed`, 버전이 늘지 않는다
- `TestSetLadoRuleArchivedHidesRule` — 보관하면 기본 목록에서 빠지고 `includeArchived` 로는 보인다, 되돌리면 다시 보인다
- `TestSetLadoRuleBindingInheritDeletes` — force_off 저장 뒤 inherit 를 주면 바인딩 행이 사라진다
- `TestLadoRuleBindingCheckRejectsBadStates` — global+force_on, project+on 이 DB CHECK 로 거부된다
- `TestLadoRuleChangesAppendAuditEvents` — 생성·버전업·보관·바인딩마다 감사 이벤트 1건, `VerifyLadoAuditChain` 통과

## 수용 게이트

- [ ] S03-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S03-G1: 필수 테스트 8종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/storage/sqlite/store/ TestEnsureLadoBuiltinRulesIsIdempotent TestLadoRuleRoundTrip TestAddLadoRuleVersionRefusesStaleBase TestAddLadoRuleVersionRefusesReplayedProof TestSetLadoRuleArchivedHidesRule TestSetLadoRuleBindingInheritDeletes TestLadoRuleBindingCheckRejectsBadStates TestLadoRuleChangesAppendAuditEvents
  EXPECT: GO_TESTS_OK 8/8
- [ ] S03-G2: store 패키지의 LADO 테스트 전부 통과
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/store/ && echo S03_STORE_LADO_OK
  EXPECT: S03_STORE_LADO_OK
- [ ] S03-G3: store 메서드가 플랜 이름 그대로 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/storage/sqlite/store/lado_rule_store.go 'func (s *Store) EnsureLadoBuiltinRules(' 'func (s *Store) ListLadoRules(' 'func (s *Store) CreateLadoRule(' 'func (s *Store) AddLadoRuleVersion(' 'func (s *Store) SetLadoRuleArchived(' 'func (s *Store) DeleteLadoRule(' 'func (s *Store) SetLadoRuleBinding(' 'func (s *Store) ListLadoModeProjectIDs('
  EXPECT: HAS_OK
- [ ] S03-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S03-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
