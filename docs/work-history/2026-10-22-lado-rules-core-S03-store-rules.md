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

- 시작 HEAD: `1947797fb49aeabdd277f37af93042db93d4e171`(`docs(lado): record S02`), 트리 깨끗.
- store 에 규칙 메서드 없음. S01 의 `gen/lado_rules.sql.go` 쿼리와 S02 의 domain 타입만 있다. `unwrapLadoSentinel` 은 `ErrLadoPresenceReplayed`·`ErrLadoPlanSuperseded` 둘만 통과시킨다.
- 테스트와 store 를 함께 쓴 뒤 처음 돌렸다. 10개가 첫 실행에 통과해 RED 를 보지 못했다. 그래서 테스트가 결함을 잡는지 변이로 확인했다(After 참고).

## After — 수행 후 실측

- 코드 커밋 `ac4292507` `feat(lado): store rules, versions and bindings`: 3 파일, +952 −1
  - `lado_rule_store.go`(새): 플랜 §4 이름 그대로 메서드 10개. `EnsureLadoBuiltinRules`, `ListLadoRules`, `LadoRule`, `ListLadoRuleVersions`, `CreateLadoRule`, `AddLadoRuleVersion`, `SetLadoRuleArchived`, `DeleteLadoRule`, `SetLadoRuleBinding`, `ListLadoModeProjectIDs`. 쓰기는 모두 `writeMu` → `inTx` → 같은 tx 의 `appendLadoAuditEventTx` 이고, 읽기는 `s.qr` 다. 내부 함수는 `insertLadoRuleTx`(규칙 행·버전 1·바인딩, 생성과 시드가 같이 씀), `insertLadoRuleVersionTx`, `setLadoRuleBindingTx`(inherit 면 삭제), `readLadoRule`/`mustReadLadoRuleTx`, `ladoRuleFromGen`, `ladoRuleVersionFromGen`. 0/1 변환은 upstream `boolInt` 를 다시 쓴다.
  - `lado_plan_store.go`: `unwrapLadoSentinel` 에 `ErrLadoRuleChanged`·`ErrLadoRuleReferenced` 추가.
  - 테스트 `lado_rule_store_test.go`: 필수 8종에 `TestDeleteLadoRuleRemovesItsRows`(버전·바인딩·행 삭제, 다른 규칙은 그대로, 두 번째 삭제는 오류)와 `TestListLadoModeProjectIDs` 를 더했다. 행 수는 `openLadoAuditStore`(= `sqlitetest.MustOpenAt` + 읽기용 raw 연결)로 센다. 대상은 rules·versions·bindings·presence proofs·audit events 다.
- 플랜에 없어 이 단계에서 정한 것:
  - 버전의 sha256 은 store 가 정규화한 내용으로 계산한다. 호출자의 `SHA256` 필드는 쓰지 않는다. 저장되는 내용도 `Normalize()` 결과다(trim, conflicts 정렬·중복 제거, config compact).
  - `CreateLadoRule`·시드: 버전 1 의 `Number`=1·`CreatedAt`, 그리고 바인딩의 `UpdatedAt` 은 모두 규칙의 `CreatedAt` 이다. `RuleID` 는 규칙 id 로 채운다. inherit 인 Override 는 아무것도 저장하지 않는다.
  - Global 행이 없는 규칙은 `off` 로 읽는다. Global 행이 없어지는 길은 global+inherit 요청뿐이고, 서비스가 그 요청을 거부한다. `Overrides` 는 비었어도 nil 이 아니다(project → task, scope_id 순).
  - `AddLadoRuleVersion` 의 순서: 최신 번호 확인 → proof → 버전(base+1) → 감사. 규칙이 없거나 지워졌으면 `ErrLadoRuleChanged` 다(409 를 받고 다시 읽으면 404). 거부된 호출은 proof 도 남기지 않아 같은 nonce 를 다시 쓸 수 있다(테스트로 확인).
  - `SetLadoRuleArchived`·`DeleteLadoRule` 에 없는 id 가 오면 `LADO rule <id> does not exist` 일반 오류다(404 는 서비스가 먼저 확인한다). 내장 규칙 삭제 거부(`LADO_RULE_BUILTIN`)도 서비스 몫이다. store 는 스냅숏 사용(`CountLadoSnapshotRulesForRule` > 0 → `ErrLadoRuleReferenced`)만 본다.
  - 읽기는 `ladoTaskPlan` 처럼 tx 없이 쿼리 3개를 쓴다. 두 읽기 사이에 지워져 버전이 없는 규칙 행은 건너뛴다.
- 변이 확인(각각 고친 뒤 원복, `diff` 로 원복 확인): 기준 번호 확인 제거 → `TestAddLadoRuleVersionRefusesStaleBase` FAIL. 시드의 존재 조회 제거 → `TestEnsureLadoBuiltinRulesIsIdempotent` FAIL(`UNIQUE constraint failed: lado_rules.id`). inherit 도 upsert → `TestSetLadoRuleBindingInheritDeletes` FAIL. 보관에서 감사 이벤트 제거 → `TestLadoRuleChangesAppendAuditEvents` FAIL.
- 추가 실행: `gofmt -l` 출력 없음, `go build ./... && go vet ./...` ok, `go test -count=1 -run Lado ./internal/storage/sqlite/store/` ok(0.8s).
- 게이트(HEAD `ac4292507`): `GATES=6 MET=6 UNMET=0`. 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳·재작업 없음.
- RED 없이 바로 GREEN 이 나와서 변이로 테스트의 힘을 확인했다. 첫 변이 하나는 무효였다. 시드 루프에서 `continue` 만 끄면 nil `err` 가 바로 아래 `return err` 로 tx 를 일찍(성공으로) 끝내서 테스트가 그대로 통과했다. 조회 자체를 빼는 변이로 다시 해서 FAIL 을 확인했다. 변이는 "정말 그 동작만 없앴는가"까지 봐야 한다.
- CHECK 거부는 감싼 오류 안에서 `errors.As(*moderncsqlite.Error)` 와 `SQLITE_CONSTRAINT_CHECK` 로 확인한다. 연결이 확장 결과 코드를 쓰므로(기존 `isSQLitePrimaryKey` 와 같음) 문자열 비교는 필요 없다.
- 다음 단계 메모:
  - S04: `ApproveLadoPlan` 스냅숏, `DeleteLadoRule` 의 `ErrLadoRuleReferenced` 실증 테스트, 세 테이블의 CDC 트리거.
  - S07: `ListLadoModeProjectIDs` 는 S01 쿼리대로 `projects` 와 조인하지 않는다. AO 는 프로젝트를 지우지 않고 보관하므로, LADO 모드인 보관 프로젝트도 목록에 나온다. impact 가 `Projects` 로 이름을 찾을 때 걸러야 한다.
  - S07 서비스가 할 일: binding scope·state 검증, protected·archived·builtin 거부.
