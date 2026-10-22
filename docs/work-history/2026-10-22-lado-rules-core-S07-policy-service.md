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

- 시작 HEAD: `be6340936b24fc963378dadd234a9898bfc3d01b`(`docs(lado): record S06`), 트리 깨끗.
- `service/ladopolicy` 에는 S05·S06 의 순수 함수만 있다(`Builtins`·`CapabilityOf`·`ReadAORules`·`ChooseAgent`·`Resolve`·스냅숏). 서비스·store 연결 없음. store(S03·S04)에 규칙·버전·바인딩 쓰기(전부 감사 이벤트와 한 tx), `EnsureLadoBuiltinRules`, `ListLiveLadoTasks`, `LadoPolicySnapshot`, `ListLadoModeProjectIDs`, `ApproveLadoPlan(…, snapshot)` 가 있다.
- 코드에서 확인한 전제: `ListLiveLadoTasks` 는 닫힌 이슈의 태스크와 보관된 프로젝트의 태스크도 낸다(S04 메모), `ListLadoModeProjectIDs` 는 `projects` 와 조인하지 않는다(S03 메모), `GetProject` 는 보관된 프로젝트도 돌려준다(`ladotask.project()` 가 `ArchivedAt` 으로 거른다), `Plan.Latest` 는 리비전 전체(sha 포함)다. presence 오류는 `ladopresence` 가 403 `LADO_PRESENCE_REQUIRED`·`INVALID`·`EXPIRED`·`UNAVAILABLE`·`REPLAYED`(`Replayed()`) 로 낸다.
- 테스트는 구현 뒤에 썼다(TDD 의 RED 를 먼저 보지 않았다). 그래서 첫 실행 PASS 를 믿지 않고 아래 변이 검사로 테스트가 실제로 잡는지 확인했다.

## After — 수행 후 실측

- 코드 커밋 `7d32bc5ac` `feat(lado): rules service with effective preview and change impact`: 3 파일, +2337.
  - `service.go`: 좁은 의존 인터페이스 `Store`(규칙 쓰기·읽기 9개 + `ListLadoModeProjectIDs`·`ListLiveLadoTasks`·`LadoTask`·`LadoPolicySnapshot`), `Projects`(`GetProject`), `Blobs`(`Get`), `Deps{Store, Projects, Blobs, AOBuild, Clock, NewID, Log}`, `New`. 메서드 `EnsureBuiltins`·`List`·`Rule`·`Create`·`Update`·`Delete`·`SetBinding`·`Effective`·`Snapshot`·`CurrentSHA256` 와 플랜 §5 타입(`RuleView`·`CreateInput`·`UpdateInput`·`BindingInput`·`ChangeResult`·`EffectiveInput`·`EffectiveView`·`ApprovedView`), `LabelKey(scope, scopeId)`.
  - `impact.go`: `Impact`·`ImpactProject`·`ImpactTask` 와 영향 계산.
  - `service_test.go`: 필수 14종 + `TestSetBindingChecksScopeAndState`(바인딩 범위·상태 검증, 취소된 태스크는 inherit 만), `TestEffectiveNamesOneScope`(범위 없음·불일치·없는 범위). 실제 store(`sqlitetest.MustOpenAt` + 행 수·감사 로그를 읽는 raw 연결, store 테스트와 같은 방식)와 가짜 blobs, 태스크·리비전·승인 도우미(`task`·`plan`·`approve`·`approvedTask`).
- 플랜에 없어 이 단계에서 정한 것(S08~S10 이 그대로 쓴다):
  - 오류 Kind: `LADO_RULE_PROTECTED`·`LADO_RULE_BUILTIN` = 403(Forbidden), `LADO_RULE_REFERENCED`·`LADO_TASK_CANCELLED`·`LADO_PLAN_BLOB_MISSING` = 409. 새 코드 `LADO_EFFECTIVE_SCOPE_INVALID`(400: 범위 없음, 또는 다른 프로젝트의 태스크). Effective 의 없는 프로젝트·태스크도 404 `LADO_SCOPE_NOT_FOUND`. presence 는 `LADO_PRESENCE_REQUIRED`(Presence nil) 와 `ladopresence.Replayed()`, Presence 함수가 낸 오류는 그대로 돌려준다. details 키: `rule`·`max`·`mode`·`scope`·`scopeId`·`state`·`task`·`baseVersion`·`revision`·`projectId`·`taskId`.
  - `Store` 에 `LadoPlanRevision` 은 넣지 않았다: 태스크의 `Plan.Latest` 가 리비전 전체이고 `Snapshot` 은 리비전을 받는다(좁은 인터페이스).
  - 사용자 규칙: enforcement `[prompt]`, config `{}`. id `lado-rule-<id>`, 버전 `lado-rule-version-<id>`, 스냅숏 `lado-snapshot-<id>`. `On` 은 Global 에서만 쓴다. Create dry run 의 id 는 예약되지 않는다.
  - 검사 순서(Update): 빈 요청 → 없는 규칙 → 보호 규칙(이름·분류·지시문·충돌·보관 필드가 **있기만 해도**, 값이 같아도 거부; `plan.approval.required` 의 CeilingMode 도 PROTECTED) → 다른 규칙의 CeilingMode = 400 `LADO_RULE_CEILING_INVALID` → 낡은 base(dry run 도) → 모드·내용 검사. 상한 모드는 `LadoCeilingModes` 만(`default` 거부).
  - 아무것도 바뀌지 않는 요청(내용 sha·보관 상태·바인딩 상태가 같음)은 쓰지 않고 감사도 없으며 `Impact` 는 비고 `Applied = !DryRun`. 같은 상한 모드는 presence 를 묻지 않는다. presence 는 dry run 반환 뒤에만 묻는다.
  - 충돌 목록은 요청이 `Conflicts` 를 보낼 때만 검사한다(Create 는 늘): 지운 규칙을 이름에 둔 규칙도 지시문은 고칠 수 있다. 이름·지시문 검사는 내용이 바뀔 때만.
  - 한 요청의 내용 수정 + 보관은 버전 → 보관 순서의 두 쓰기(감사 2건, 한 tx 아님). 보관된 규칙의 내용 수정은 허용, 바인딩은 409 `LADO_RULE_ARCHIVED`.
  - 바인딩 범위: 보관된 프로젝트·이슈 없는 태스크 = 404, 취소된 태스크는 inherit(제거)만 받는다.
  - 감사 payload(subject_kind `rule`): `rule.created {version, scope, scopeId, global}`(내장은 정확히 `{"version":1,"builtin":true}`), `rule.updated {version, sha256, reason}`, `rule.archived`/`rule.unarchived {version, reason}`, `rule.deleted {version, name}`, `rule.binding_set {scope, scopeId, from, to}`, `rule.protected_changed {from, to, version, proof, dev, reason}`. 상한 버전의 actor 도 `human`/`human:dev-presence`.
  - Impact: LADO 모드이고 보관되지 않은 프로젝트만, 태스크도 그 프로젝트의 것만(LADO 모드가 꺼진 프로젝트의 태스크는 보드에 없으므로). 태스크는 현재 sha 를 `PolicySHA256` 에 넣은 `State()` 가 approved·blocked(유효 승인, 스냅숏 = 지금, 이슈 열림)이고 변경 후 sha 가 다를 때 나온다. 고정 플랜을 못 읽는 태스크는 경고 로그 후 뺀다(한 핀 때문에 규칙 변경이 막히지 않게). 순서: 프로젝트 id 순, 태스크는 `ListLiveLadoTasks` 순.
  - Effective: 태스크 범위는 `Plan.Latest`(유효 승인이면 승인된 리비전)로 해석하므로 `Effective.SHA256 == Snapshot(task, latest).SHA256 == CurrentSHA256(task)` — UI 가 `policySha256` 으로 보낼 값. 유효 승인이면 `Task.Plan.PolicySHA256` 을 채워 `Task.State()` 가 최신이다. `EffectiveView` 는 `Resolution` 을 임베드하고 `Agent`(출처 포함)·`AORules`(orchestrator 포함 전부)·`Task`·`Approved` 를 더했다. 프로젝트 범위: 보관 프로젝트 404, 에이전트는 프로젝트 워커(`ChooseAgent(빈 태스크, project)` — Impact 비교와 같은 값). `ApprovedView.Changed` = 같은 규칙의 다른 버전 **또는** 다른 바인딩 출처. 목록은 정렬, nil 아님.
  - 고정 플랜을 못 읽으면 `Snapshot`·`CurrentSHA256`·`Effective` 는 409 `LADO_PLAN_BLOB_MISSING`. 플랜 상한 캐시는 리비전 id 별 map + mutex(읽기 실패는 캐시하지 않음).
- 변이 검사(44회, 각각 적용 → 패키지 테스트 → 원복): 첫 묶음 35개 중 28 kill, 5개는 변이가 변수 미사용으로 빌드 실패(다시 써서 kill), 2개 생존 — Effective 의 프로젝트/태스크 불일치 검사 제거, Impact 가 보관 프로젝트를 포함 → `TestEffectiveNamesOneScope` 와 `TestImpactListsApprovedTasks` 끝(보관 프로젝트) 단언을 더해 kill. 변이 전에 범위 부족도 메웠다: 보관 규칙 바인딩 거부, 보관 해제, `Changed`(새 버전·출처 변경, 승인 상태로 돌아가면 다시 `Matches`), 같은 상태 바인딩 무기록, 바인딩 검증 표, `plan.approval.required` 의 CeilingMode. 최종 생존 0.
- 추가 실행: `gofmt -l` 출력 없음. golangci-lint v2.13.2(저장소 `backend/.golangci.yml`, 이전 단계가 빌드한 바이너리, `GOPROXY=off`) `./internal/service/ladopolicy/...` → prealloc 1건(`diffRules` 의 `removed`) 고친 뒤 `0 issues`.
- 게이트(HEAD `7d32bc5ac`, 코드 커밋 직후 깨끗한 트리): `GATES=6 MET=6 UNMET=0`(G1 `GO_TESTS_OK 14/14`). 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 구현과 테스트 모두 첫 실행에서 통과했지만, 테스트를 구현 뒤에 쓴 만큼 그 통과는 근거가 아니었다 — 변이 검사가 실제 구멍 2개(범위 불일치, 보관 프로젝트)를 찾았다. 서비스처럼 거부 분기가 많은 코드는 "필수 테스트 이름"만 채우면 분기 절반이 무검증으로 남는다.
- 변이 스크립트의 `if false {` 같은 치환은 조건에만 쓰이던 변수를 미사용으로 만들어 빌드가 깨진다. 변수를 남기는 꼴(`task.ID == ""`, `… || in.ProjectID != ""`)로 써야 동작 변이가 된다.
- 다음 단계 메모:
  - S08: `ladotask.Policy` 는 이 서비스의 `Snapshot(ctx, task, rev)`·`CurrentSHA256(ctx, task)` 그대로다. `Snapshot.ApprovalID` 는 비워 두면 store 가 승인 id·시각을 넣는다. 승인 검사 = `Snapshot(task, rev).SHA256 == policySHA256`. `CurrentSHA256` 은 부를 때마다 `ListLadoRules` + `GetProject` + `ReadAORules`(파일 읽기)를 하므로 목록에서 태스크마다 부르면 N 배다 — 목록이 느려지면 한 번 읽은 규칙·프로젝트로 여러 태스크를 해석하는 도우미를 이 패키지에 더한다(지금은 단순함 우선).
  - S09: `RuleView.Labels` 키는 `LabelKey(scope, scopeId)`. `ChangeResult.Rule` 은 dry run 이면 "바뀐 뒤 모습"이다. 응답의 `agent.source` 는 `EffectiveView.Agent.Source`, `aoRules[].chars/readError` 는 `EffectiveView.AORules`, `approved.revision` 은 `RevisionNumber`. HTTP 상태는 apierr Kind 그대로(위 목록). `PATCH` 의 Presence 는 `Verifier.Verify(header, method, path, body, action, subject)` 를 감싼 함수로 넘긴다.
  - S10: `lib/lado-errors.ts` 에 위 새 코드(`LADO_EFFECTIVE_SCOPE_INVALID`, `LADO_SCOPE_NOT_FOUND`, `LADO_RULE_*`, `LADO_BINDING_*`, `LADO_PLAN_BLOB_MISSING`)를 넣는다.
