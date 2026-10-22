# 2026-10-22 LADO 규칙 코어 — S07 ladopolicy: 서비스·presence·영향·유효 미리보기

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§5 Service) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙을 만들고 고치고 켜고 끄는 서비스, 승인용 스냅숏과 현재 해시, 프로젝트·태스크의 유효 규칙 미리보기, 변경 전 영향 미리보기(dry run)를 만든다. 보호 규칙은 끌 수도 묶을 수도 없고, 권한 상한 값은 사람의 presence 증명이 있어야만 바뀐다(HARNESS §2·§14, "AI 는 보호 게이트를 바꾸지 못한다").

## 범위

- 만든다: `backend/internal/service/ladopolicy/service.go`, `impact.go`, 테스트 `service_test.go`(필요하면 `impact_test.go`).
- 서비스 의존(플랜 §5 `Deps`): `Store`(S03·S04 의 store 메서드 + `LadoTask`·`LadoPlanRevision`·`ListLiveLadoTasks`·`LadoPolicySnapshot`·`ListLadoModeProjectIDs`), `Projects`(`GetProject`), `Blobs`(`Get(sum, limit)` — 고정된 플랜 파일에서 `permission_ceiling` 을 읽는다), `AOBuild`, `Clock`, `NewID`, `Log`. 인터페이스는 이 패키지에 좁게 정의한다(`ladotask` 의 `Store` 처럼).
- 하지 않는다: ladotask 연결(S08), HTTP(S09), UI.

## 절차

1. 플랜 §5 Service 의 메서드·입력·결과 타입을 그대로 만든다. 오류는 `apierr`(코드·메시지·details) — 코드 목록은 플랜 그대로.
2. `EnsureBuiltins`: `Builtins()` 에 버전 id(`lado-rule-version-<uuid>`)·시각을 붙여 seed 와 감사 이벤트(`rule.created`, actor `lado`, payload `{"version":1,"builtin":true}`)를 만들어 store 에 넘긴다.
3. 쓰기 경로마다 감사 이벤트(플랜 §5 Audit actions, actor `api`; presence 쓰기는 `human`/`human:dev-presence`). 보호 규칙 규칙(플랜 §5 둘째 불릿)을 정확히: 이름·분류·지시문·충돌·보관 = `LADO_RULE_PROTECTED`, ceiling 값만 presence 로.
4. dry run: 쓰지 않고 `Impact` 만 계산(presence 도 요구하지 않는다). 실제 적용도 쓰기 **전에** 같은 영향을 계산해 돌려준다.
5. `Effective`: 프로젝트 범위(`ProjectID`) 또는 태스크 범위(`TaskID` — 프로젝트는 태스크에서). 태스크의 유효한 승인이 있으면 `Approved`(저장 스냅숏 내용, `Matches`, 추가·제거·변경된 규칙 id).
6. `Snapshot(task, rev)`·`CurrentSHA256(task)`: 같은 입력 → 같은 sha. 에이전트·플랜 상한은 플랜 §5 마지막 불릿대로. 프로젝트 기록은 `GetProject`, AO 지시문은 `ReadAORules`.
7. 테스트는 `sqlitetest.MustOpen(t)` 의 실제 store 와 가짜 blobs 로 쓴다(ladotask 테스트의 태스크·리비전 만드는 도우미를 참고해 이 패키지에 작은 도우미를 둔다).

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): rules service with effective preview and change impact` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S07-policy-service.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (`backend/internal/service/ladopolicy/`)

- `TestCreateRuleAtEachScope` — global on/off, project(global off + project force_on), task(global off + task force_on), 취소된 태스크 = `LADO_TASK_CANCELLED`, 검증 오류 코드(이름 없음·길이 초과·모르는 충돌·자기 충돌·보호 규칙과 충돌)
- `TestUpdateRuleAddsAVersion` — 지시문 변경 → 버전 2·감사 `rule.updated`, 내용이 같으면 버전이 늘지 않는다
- `TestUpdateRuleRefusesAStaleBase` — `BaseVersion` 이 낡으면 409 `LADO_RULE_CHANGED`
- `TestProtectedRulesRefuseBindingsArchiveAndDelete` — `HARNESS 15.3`: 보호 규칙의 task force_off·global off·보관·삭제·이름 변경이 모두 `LADO_RULE_PROTECTED`(삭제는 내장이라 `LADO_RULE_BUILTIN` 도 허용 — 어느 쪽이든 거부)
- `TestCeilingChangeNeedsPresence` — `HARNESS 15.3`(AI 발 우회): Presence 없이 ceiling 변경 = 403 `LADO_PRESENCE_REQUIRED`, 버전·감사 0
- `TestCeilingChangeWithProofRecordsAudit` — 증명이 있으면 버전 2(`{"mode":"bypass-permissions"}`), 감사 `rule.protected_changed` actor `human`, 증명 nonce 저장
- `TestCeilingChangeRefusesReplayedProof` — 같은 nonce 재사용 = 403 `LADO_PRESENCE_REPLAYED`
- `TestDryRunWritesNothing` — 생성·수정·바인딩 dry run 뒤 규칙·버전·바인딩·감사 수가 그대로, `Applied=false`
- `TestDisablingKeepsHistoryAndSnapshots` — `HARNESS 15.4`: 규칙을 끄고 보관해도 버전 이력이 그대로 읽히고, 이미 저장된 승인 스냅숏 바이트·sha 가 바뀌지 않는다
- `TestDeleteRule` — 안 쓴 사용자 규칙 삭제 성공 / 스냅숏이 쓴 규칙 = `LADO_RULE_REFERENCED` / 내장 = `LADO_RULE_BUILTIN`
- `TestEnsureBuiltinsSeedsOnce` — 두 번 불러도 3개, 감사 3건
- `TestEffectiveBlocksAnUnsupportedAgent` — `HARNESS 15.7`: cursor 태스크 + ON 한국어 규칙 → `Blocking()` 참, claude-code 로 바꾸면 거짓
- `TestSnapshotMatchesCurrentSHA` — 승인용 `Snapshot` 의 sha = `CurrentSHA256`, 태스크 바인딩을 바꾸면 달라진다
- `TestImpactListsApprovedTasks` — 승인 스냅숏이 현재와 같은 태스크가 있으면, 그 태스크에 닿는 바인딩 dry run 의 `Impact.Tasks` 에 나온다(닿지 않는 변경에는 안 나온다)

## 수용 게이트

- [ ] S07-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S07-G1: 필수 테스트 14종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladopolicy/ TestCreateRuleAtEachScope TestUpdateRuleAddsAVersion TestUpdateRuleRefusesAStaleBase TestProtectedRulesRefuseBindingsArchiveAndDelete TestCeilingChangeNeedsPresence TestCeilingChangeWithProofRecordsAudit TestCeilingChangeRefusesReplayedProof TestDryRunWritesNothing TestDisablingKeepsHistoryAndSnapshots TestDeleteRule TestEnsureBuiltinsSeedsOnce TestEffectiveBlocksAnUnsupportedAgent TestSnapshotMatchesCurrentSHA TestImpactListsApprovedTasks
  EXPECT: GO_TESTS_OK 14/14
- [ ] S07-G2: 서비스 메서드가 플랜 이름 그대로다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/service.go 'func (s *Service) EnsureBuiltins(' 'func (s *Service) List(' 'func (s *Service) Create(' 'func (s *Service) Update(' 'func (s *Service) Delete(' 'func (s *Service) SetBinding(' 'func (s *Service) Effective(' 'func (s *Service) Snapshot(' 'func (s *Service) CurrentSHA256('
  EXPECT: HAS_OK
- [ ] S07-G3: 패키지 전체 통과(-race)
  CHECK: bash .claude/gate/lado-rules-core.sh go-race ./internal/service/ladopolicy/
  EXPECT: GO_RACE_OK
- [ ] S07-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S07-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
