# 2026-10-23 LADO 대기열·디스패처 — S05 워커 게이트: 허가·턴별 상한·실행 고정·규칙 문구 훅

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 d·i·j, §5) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

LADO 프로젝트에서 워커를 띄울 유일한 길(디스패처가 지금 쥔 리스의 허가)을 열고, 실행 중인 세션이 승인된 권한 상한·에이전트·화면 모드를 벗어나지 못하게 한다. 워커 시스템 프롬프트에 규칙 문구를 넣을 훅도 세션 id 를 받게 바꾼다.

## 범위

- 고친다(LADO 파일): `backend/internal/ports/lado_gate.go`(인터페이스에 세 메서드 — 플랜 §5 시그니처 그대로), `backend/internal/service/ladogate/gate.go`(허가·상한·고정; 의존성 추가), `backend/internal/session_manager/lado_gate.go`(`LadoPromptSection` 새 시그니처, `ladoPromptSection` 이 워커에도 부른다), `backend/internal/service/chat/lado_gate.go`, `backend/internal/service/ladotask` 의 `PromptSource`(새 시그니처에 맞추는 얇은 메서드 `Section(ctx, kind, projectID, sessionID)` — 오케스트레이터는 기존 문구, 워커는 이 단계에서 `""`; 실제 워커 문구는 S06), 두 테스트 게이트(`session_manager/lado_gate_test.go`, `service/chat/lado_gate_test.go`)의 no-op 메서드, `backend/internal/daemon/lado_wiring.go`(게이트에 store 를 허가·정책 읽기로 넘김).
- 고친다(업스트림 훅, 각각 몇 줄): `session_manager/agent_switching.go` 의 `SwitchAgent` 첫머리에서 `AuthorizeExecutionChange`; `session_manager/interface_transition.go` 의 `StartInterfaceTransition` 첫머리에서 같은 호출; `session_manager/manager.go` 의 LADO 섹션 호출에 kind·sessionID 를 넘김; `service/chat/service.go` 의 `SetTurnSettings`(컨트롤러가 쓰기 전) → `ClampTurnSettings`, `SetConfigOption`(provider 호출 전, 선택이 승인 모드로 풀릴 때) → `AuthorizeConfigMode`. 업스트림 파일 합계 변경 50줄 이내(게이트 G5).
- 만든다(store): 게이트·프롬프트가 쓸 읽기 `LadoEntryPolicyByClientRequest(ctx, clientRequestID string) (domain.LadoEntryPolicy{Ceiling domain.PermissionMode; Prompt string}, bool, error)` — client request id → 항목 → 스냅숏 JSON 의 `permissionCeiling`·`prompt`. 실행 행이 아직 없어도(스폰 도중) 항목으로 찾는다. 도메인 타입 `LadoEntryPolicy` 는 `domain/lado_queue.go` 에.
- 테스트: 아래 필수 테스트. 기존 `ladogate/gate_test.go:70` 근처의 "`lado:entry-1` 은 거부" 사례는 새 형식·허가 규칙에 맞춰 고친다(허가 없는 `lado:` id 는 여전히 거부).
- 하지 않는다: 디스패처(S07·S08), 워커 규칙 문구의 실제 내용(S06), HTTP(S09).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 플랜 §5 를 읽는다. `AuthorizeSpawn` 순서: 프로젝트 없음 → 통과; LADO 모드 아님 → 통과; 자동화 → `LADO_AUTOMATION_REFUSED`(그대로); 오케스트레이터 → 통과; 워커는 `domain.ParseLadoClientRequestID(cfg.ClientRequestID)` 가 되고 `Grants.LadoDispatchGrant(ctx, entry, attempt, now)` 가 `Active` 이고 `ProjectID == cfg.ProjectID` 이고 `Hash == cfg.ClientRequestHash` 일 때만 통과, 아니면 `LADO_TASK_REQUIRED`(기존 메시지·details 유지). 허가 읽기 오류는 거부(`LADO gate: %w`).
2. `ClampTurnSettings(ctx, target, s)`: `Sessions.GetSession(target)` → 레코드의 `ClientRequestID` → `LadoEntryPolicyByClientRequest`. 실행이 아니면 `s` 그대로. 실행이면 `s.Approval` 이 비었으면 그대로(대화의 시작 모드 = 스냅숏 모드), `default` 이거나 `domain.LadoPermissionRank(s.Approval) > domain.LadoPermissionRank(ceiling)` 이면 `s.Approval = ceiling`. `default` 의 rank 가 0 이어도 상한 위로 친다(플랜 §1 i — Codex 의 default 는 bypass).
3. `AuthorizeConfigMode(ctx, target, mode)`: 실행이고 mode 가 `default` 이거나 상한보다 높으면 409 `LADO_APPROVAL_ABOVE_CEILING`, details `{sessionId, mode, ceiling}`. `AuthorizeExecutionChange(ctx, target)`: 실행이면 409 `LADO_EXECUTION_FIXED`, details `{sessionId}`. 둘 다 실행이 아니면 nil, 읽기 오류는 거부.
4. 훅: chat `SetTurnSettings` 는 게이트가 있으면 `ClampTurnSettings` 결과로 저장한다(오류면 그대로 반환). `SetConfigOption` 은 선택이 승인 모드로 풀리는 경우(`settingsFromConfigOptions` 가 `ApprovalMode` 를 정하는 경로 — 코드를 읽고 provider 호출 **전**에 그 모드를 알아내는 가장 작은 방법을 고른다)에만 `AuthorizeConfigMode`. `SwitchAgent`·`StartInterfaceTransition` 은 맨 앞에서 `AuthorizeExecutionChange`. 게이트가 nil 이면 업스트림 동작 그대로.
5. 프롬프트 훅: `LadoPromptSection` 타입을 `func(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) string` 로. `buildSystemPrompt` 의 호출부는 이미 kind·sessionID 를 갖고 있다. `ladoPromptSection` 의 "오케스트레이터만" 조건을 지우고 전부 소스에 맡긴다(소스가 kind 로 가른다). 복구·재개·에이전트 전환 재조립 경로는 모두 `buildSystemPrompt` 를 다시 부르므로 따로 고칠 것이 없다 — 테스트로 확인한다.
6. ladotask(`TestApprovePlanBindsTheExactRevision`·`TestApprovePlanStoresTheRuleSnapshot`·`TestTaskStateFollowsRuleChanges`·`TestReviewRequiredTaskCanBeApprovedAgain`)와 ladopolicy(`TestDisablingKeepsHistoryAndSnapshots`·`TestImpactListsApprovedTasks`)의 실패 6개는 S03 의 승인 시그니처 변경에서 온 것이고 S06 이 고친다. 이 단계에서는 건드리지 않는다.
7. 커밋 메시지 예: `feat(lado): dispatch grant, turn-setting cap and fixed executions in the worker gate`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S05-gate.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- ladogate `TestAuthorizeSpawnNeedsAnActiveGrant` — 허가 있음 → 통과; id 없음·형식 틀림·리스 만료·다른 프로젝트·해시 불일치·허가 읽기 오류 → 거부; 오케스트레이터·LADO 모드 꺼진 프로젝트는 그대로(게이트 1)
- ladogate `TestClampTurnSettingsCapsExecutions` — 상한 위 → 상한, `default` → 상한, 비어 있음·상한 이하 → 그대로, 실행 아닌 세션 → 그대로
- ladogate `TestAuthorizeConfigModeAndExecutionChange` — 두 메서드의 거부·통과·읽기 오류
- store `TestLadoEntryPolicyByClientRequest` — 실행 행 없이 항목만 있어도 상한·문구를 찾는다; 형식 아님·없는 항목 → not found
- session_manager `TestSwitchAgentAsksTheLadoGate` — 게이트 거부면 전환 없이 그 오류
- session_manager `TestStartInterfaceTransitionAsksTheLadoGate` — 같은 꼴
- session_manager `TestWorkerSystemPromptAsksForTheLadoSection` — 워커 스폰과 복구 재조립 모두 소스가 kind·sessionID 와 함께 불리고 돌려준 문구가 시스템 프롬프트에 들어간다
- chat `TestSetTurnSettingsUsesTheLadoCap` — 저장되는 approval 이 게이트가 돌려준 값
- chat `TestSetConfigOptionAsksTheLadoGate` — 승인 모드 선택이 거부되면 provider 를 부르지 않는다

## 수용 게이트

- [ ] S05-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S05-G1: 게이트 테스트 3개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladogate/ TestAuthorizeSpawnNeedsAnActiveGrant TestClampTurnSettingsCapsExecutions TestAuthorizeConfigModeAndExecutionChange
  EXPECT: GO_TESTS_OK 3/3
- [ ] S05-G2: store·session_manager·chat 훅 테스트
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/storage/sqlite/store/ TestLadoEntryPolicyByClientRequest && bash .claude/gate/lado-queue.sh go-tests ./internal/session_manager/ TestSwitchAgentAsksTheLadoGate TestStartInterfaceTransitionAsksTheLadoGate TestWorkerSystemPromptAsksForTheLadoSection && bash .claude/gate/lado-queue.sh go-tests ./internal/service/chat/ TestSetTurnSettingsUsesTheLadoCap TestSetConfigOptionAsksTheLadoGate
  EXPECT: GO_TESTS_OK 2/2
- [ ] S05-G3: 게이트·데몬 패키지 전체 통과(ladotask·ladopolicy 는 S03 이 남긴 실패 6개를 S06 이 고치므로 여기서 돌리지 않는다 — 컴파일은 G6 이 본다)
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/service/ladogate/ ./internal/daemon/
  EXPECT: GO_PKG_OK
- [ ] S05-G4: session_manager 와 chat 의 LADO 테스트가 race 로 통과
  CHECK: cd backend && go test -race -count=1 -run 'Lado' ./internal/session_manager/ ./internal/service/chat/ && echo S05_HOOKS_RACE_OK
  EXPECT: S05_HOOKS_RACE_OK
- [ ] S05-G5: 업스트림 훅이 작다
  CHECK: bash .claude/gate/lado-queue.sh diff-budget 50 backend/internal/session_manager/agent_switching.go backend/internal/session_manager/interface_transition.go backend/internal/session_manager/manager.go backend/internal/service/chat/service.go
  EXPECT: BUDGET_OK
- [ ] S05-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S05-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `925a76178`(`docs(lado): record S04`), 트리 깨끗. 환경 umask 0022·TZ=UTC, go1.27.1. `defs-unchanged` → `DEFS_UNCHANGED`, `diff-budget 50 …`(G5 의 네 파일) → `BUDGET_OK 0/50`.
- 게이트: `ports.WorkGate` 는 `AuthorizeSpawn`·`AuthorizeSend` 둘, `ladogate.New(settings, sessions)`. LADO 프로젝트의 워커는 전부 `LADO_TASK_REQUIRED`(`gate_test.go` 의 "`lado:entry-1` 거부" 사례가 고정). 턴 설정·config 옵션·에이전트 전환·인터페이스 전환에는 LADO 훅이 없다.
- 프롬프트 훅: `LadoPromptSection func(ctx, projectID) (string, error)`, `ladoPromptSection` 은 오케스트레이터만 부른다. `TestLadoPromptSectionReachesOnlyOrchestrators` 가 워커 제외와 "읽기 오류면 섹션을 조용히 빼지 않음"을 고정. 데몬은 `PromptSource.OrchestratorSection` 을 꽂는다.
- store: S04 의 `LadoDispatchGrant` 는 있으나, client request id 로 항목 스냅숏의 상한·문구를 읽는 메서드가 없다(`GetLadoQueueEntryPlan` 은 스냅숏 sha256 만).
- 기준선: `go test -count=1 ./internal/service/ladogate/ ./internal/daemon/` ok, `-run 'Lado' ./internal/session_manager/ ./internal/service/chat/` ok. 알려진 실패는 S03 이 남긴 6개(ladotask 4·ladopolicy 2, S06 몫).
- 테스트 순서: 필수 테스트는 구현 직후에 썼다(RED 먼저가 아님). 대신 업스트림 훅을 하나씩 끄는 변이로 훅 테스트가 잡는지 확인했다(After).

## After — 수행 후 실측

- 코드 커밋 `05bbc2e6d` `feat(lado): dispatch grant, turn-setting cap and fixed executions in the worker gate` — 20 파일, +929 −59. 리뷰 반영 `6f223001f` `test(lado): pin the config-mode probe per harness and the no-ceiling refusals` — 3 파일, +56 −1.
  - `ports/lado_gate.go`: `WorkGate` 에 플랜 §5 시그니처 그대로 `ClampTurnSettings`·`AuthorizeConfigMode`·`AuthorizeExecutionChange`.
  - `service/ladogate/gate.go`: 의존성 `Grants`(`LadoDispatchGrant`)·`Policies`(`LadoEntryPolicyByClientRequest`), `New(settings, sessions, grants, policies)`. `AuthorizeSpawn` 은 절차 1 순서 그대로이고, 워커는 `granted`(id 파싱 → `time.Now()` 로 허가 읽기 → Active·프로젝트·해시 셋 다)일 때만 통과, 허가 읽기 오류는 `LADO gate: %w` 로 거부. 세 메서드는 공통 `execution(target)`(세션 → `ClientRequestID` → 항목 정책)과 `aboveCeiling` 을 쓴다. 거부는 409 `LADO_APPROVAL_ABOVE_CEILING`(details `sessionId·mode·ceiling`)·`LADO_EXECUTION_FIXED`(`sessionId`).
  - store: `LadoEntryPolicyByClientRequest` + 새 쿼리 `GetLadoQueueEntrySnapshot`(항목의 `policy_snapshot_id` 로 JOIN 한 `snapshot_json` 한 열; ASCII, gen 은 `npm run sqlc` 로 만들고 읽어 확인). 스냅숏 JSON 에서 `permissionCeiling`·`prompt` 만 푼다(store 가 `ladopolicy` 를 import 하지 않게 익명 구조체). 도메인 `LadoEntryPolicy{Ceiling, Prompt}`.
  - session_manager: `LadoPromptSection func(ctx, kind, projectID, sessionID) (string, error)`, `ladoPromptSection` 은 오케스트레이터 조건을 지우고 소스에 넘긴다(프로젝트 없는 세션만 거른다). `authorizeLadoExecutionChange` 도우미. 업스트림: `SwitchAgent`·`StartInterfaceTransition` 맨 앞에 4줄씩, `manager.go` 의 호출에 `sessionID`(2줄 변경).
  - chat: `lado_gate.go` 에 `clampLadoTurnSettings`(`ConversationSettings` ↔ `ChatTurnSettings` 변환, 결과를 저장값으로)·`authorizeLadoConfigOption`·`ladoConfigMode`(결정 3). 업스트림 `service.go` 10줄(훅 둘, 주석 포함).
  - ladotask `PromptSource.Section(ctx, kind, projectID, sessionID)`: 오케스트레이터 → `OrchestratorSection`, 그 밖 → `""`. 데몬: `ladogate.New(store, store, store, store)`, `SetLadoPrompt(PromptSource{Settings: store}.Section)`.
  - 테스트: 필수 9개 + `TestLadoPromptSectionComesFromTheSource`(옛 `…ReachesOnlyOrchestrators` 를 새 뜻으로: 소스가 kind 별로 답하고, 프로젝트 없는 세션은 묻지 않고, 읽기 오류는 두 kind 모두 실패) + `TestLadoConfigModeReadsEveryApprovalControl`(하네스 6종 17사례: 게이트가 묻는 모드 = 제공자 응답에서 기록될 모드). 기존 사례 고침: `TestAuthorizeSpawnRefusesEveryWorkerPathInALadoProject` 의 `lado:entry-1` → `lado:entry-1/1` + 해시, 허가 없음으로 거부; `TestPromptSourceAddsTheSectionOnlyInLadoMode` 에 `Section` 두 줄; `TestWireLadoInstallsOneGateOnEverySendAndSpawnPath` 에 새 시그니처·워커 섹션 없음·허가 없는 `lado:` id 거부.
- 엔트리·플랜이 정하지 않았거나 다르게 적은 것:
  1. **`LadoPromptSection` 은 `(string, error)` 를 유지**했다(엔트리 절차 5·플랜 §5 는 `string`). 오류를 버리면 S06 의 워커 문구 읽기가 DB 오류에서 조용히 비어 워커가 규칙 문구 없이 뜨고(플랜 §1 j 와 반대), 5단계의 fail-closed(오케스트레이터 섹션을 조용히 빼지 않음, 테스트로 고정)도 깨진다. 스폰 실패는 디스패처가 재시도하고, 복원 실패는 지금 오케스트레이터와 같은 경로다. `PromptSource.Section` 도 같은 반환. 리뷰도 이 편을 지지했다.
  2. 상한 비교: `LadoPermissionRank` 가 0 인 모드(지금은 `default` 뿐 — 대화 설정 라우트가 `PermissionMode.Valid()` 의 네 모드만 받음)를 모두 상한 위로 쳤다. 엔트리 식("`default` 이거나 rank 초과")과 같은 결과이고 모르는 모드에도 안전하다. 상한이 유효하지 않은 스냅숏(rank 0)의 실행은 세 메서드 모두 오류로 거부한다(추측해 깎지 않음). 디스패치 검사 6 이 그런 스냅숏을 막으므로 정상 경로에는 없다.
  3. config 모드를 provider 호출 **전**에 아는 방법: 선택값 하나짜리 옵션을 `permissionConfigOptions(harness, …)` 에 통과시켜 `PermissionMode` 를 읽는다 — `settingsFromConfigOptions` 가 응답에서 쓰는 같은 표. provider 를 한 번 더 부르지 않고(`ListConfigOptions` 없음), 모드가 아닌 옵션(model 등)은 게이트를 묻지 않는다. 훅 위치는 `requireChatSession` 바로 뒤라 거부 때 컨트롤러도 띄우지 않는다.
  4. "실행" 판정 = 세션의 `ClientRequestID` 가 항목을 찾는 것(세션 종류·LADO 모드를 보지 않음). 그래서 LADO 모드를 끈 프로젝트의 실행도 계속 고정·상한이다(플랜 §1 l "running executions go on" — 스냅숏대로 돈다). 시도 번호는 보지 않는다(모든 시도가 같은 스냅숏). 대체된 항목도 정책을 준다 — 그 항목이 띄운 워커는 그 스냅숏으로 돈다(store 테스트).
  5. `ClampTurnSettings` 결과는 approval 만이 아니라 model·effort 까지 저장값에 되돌린다("결과로 저장"). 게이트는 approval 만 바꾼다.
- 확인:
  - 필수 9개 PASS(게이트 스크립트가 `-v` 로 이름별 확인). 변이: `SwitchAgent`·`StartInterfaceTransition` 훅을 끄면 두 테스트 FAIL, chat 훅 둘을 끄면 두 테스트 FAIL(저장 approval `bypass-permissions`, 거부 없음). 되돌린 뒤 diff 는 훅 줄뿐.
  - `go test -count=1 ./internal/session_manager/ ./internal/service/chat/ ./internal/ports/ ./internal/domain/` ok(38.9s·66.1s), `-run 'Lado' ./internal/storage/sqlite/...` ok, G4 race ok(chat 19.0s), golangci-lint v2.13.2(오프라인 빌드) 변경 패키지 8묶음 0 issues, gofmt 없음.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker·should-fix 없음. nit 3(하네스별 probe 표 테스트, `(string, error)` 이유 주석, 상한 없는 스냅숏을 다른 두 메서드에서도 확인) — 모두 `6f223001f` 로 반영. 리뷰가 확인한 것: Copilot 의 상승 경로(`allow_all`·autopilot)는 LADO 와 무관하게 `copilotConfigOptionAllowed` 가 막고, 승인은 턴 설정이라 clamp 가 덮는다.
  - 실패는 S03 이 남긴 6개 그대로(ladotask 4·ladopolicy 2) — S06 몫.
  - 업스트림 diff 22/50(chat `service.go` 10, `agent_switching.go` 4, `interface_transition.go` 4, `manager.go` 2+2).
- 게이트(HEAD `05bbc2e6d`): `GATES=8 MET=8 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 쿼리 1개(`GetLadoQueueEntrySnapshot`), 테스트 2개(옛 프롬프트 테스트를 새 뜻으로 바꾼 것, 하네스별 probe 표), 데몬 배선 테스트 보강. 엔트리와 다르게 한 것은 After 결정 1(`(string, error)`) 하나.
- 남은 틈(이 단계 범위 밖, 기록만):
  - `SetConfigOption` 은 provider 응답의 현재 모드를 그대로 기록한다. 모드가 아닌 옵션을 바꿨는데 provider 가 스스로 상한 위 모드를 보고하면 그 값이 다음 턴 approval 이 된다. 지금 매핑되는 하네스(Claude·OpenCode·Agy·CommandCode)는 모드 옵션 말고는 모드를 바꾸지 않아 실제 경로는 없다. 막으려면 기록 직전에도 clamp 해야 하고 업스트림 줄이 더 든다.
  - Claude 의 ExitPlanMode 같은 provider 자체 권한 흐름은 AO 를 거치지 않는다(플랜 범위 밖).
- 다음 단계 메모:
  - S06: `PromptSource.Section` 의 워커 분기를 `WorkerSection(ctx, sessionID)` 로 바꾼다. 스폰 때 `buildSystemPrompt` 전에 세션 행(`ClientRequestID` 포함)이 이미 저장된다(`CreateClientRequestSession` → `buildSpawnTexts`; chat 스폰도 저장된 `rec` 로 부른다). 그래서 `WorkerSection` 은 `GetSession` → `LadoEntryPolicyByClientRequest` 로 충분하다. `PromptSource` 에 세션·정책 읽기 필드를 더하고 `wireLado` 의 `PromptSource{Settings: store}` 에 store 를 넘긴다. 반환은 `(string, error)` — 읽기 오류는 그대로 돌려준다(결정 1). 빈 `Prompt` 는 `""`(섹션 생략, 플랜 §1 j). 섹션 머리(예 `## LADO rules`)를 붙일지는 S06 이 정한다.
  - S07: 스폰 `SpawnConfig` 의 `ClientRequestID = domain.LadoClientRequestID(entry.ID, entry.AttemptCount)`(claim 이 센 시도 수와 같아야 허가가 Active), `ClientRequestHash = domain.LadoDispatchHash(planSHA, snapshotSHA)`. `sessionsvc.Service.Spawn` 은 받은 해시를 그대로 쓴다(본문 해시는 HTTP 라우트만 만든다). 게이트가 `time.Now()` 로 리스를 보므로 리스(2분) 안에 스폰을 시작할 것.
  - S09·S10: 새 오류 코드 `LADO_APPROVAL_ABOVE_CEILING`·`LADO_EXECUTION_FIXED` 는 `apierr.Conflict` 다. 세션 라우트는 `toAPIError` → `envelope.WriteError`, 대화 라우트는 `writeConversationError` 의 default 분기 → `envelope.WriteError` 로 나가므로 409 가 될 것이다(코드로만 따라감 — HTTP 테스트로는 확인 안 함, S09 몫). 렌더러 `lado-errors.ts` 와 문구에 더한다(S10).
