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

1. 플랜 §5 를 읽는다. `AuthorizeSpawn` 순서: 프로젝트 없음 → 통과; LADO 모드 아님 → 통과; 자동화 → `LADO_AUTOMATION_REFUSED`(그대로); 오케스트레이터 → 통과; 워커는 `domain.ParseLadoClientRequestID(cfg.ClientRequestID)` 가 되고 `Grants.LadoDispatchGrant(ctx, entry, attempt, now)` 가 `Active` 이고 `ProjectID == cfg.ProjectID` 이고 `Hash == cfg.ClientRequestHash` 일 때만 통과, 아니면 `LADO_TASK_REQUIRED`(기존 메시지·details 유지). 허가 읽기 오류는 거부(`LADO gate: %w`).
2. `ClampTurnSettings(ctx, target, s)`: `Sessions.GetSession(target)` → 레코드의 `ClientRequestID` → `LadoEntryPolicyByClientRequest`. 실행이 아니면 `s` 그대로. 실행이면 `s.Approval` 이 비었으면 그대로(대화의 시작 모드 = 스냅숏 모드), `default` 이거나 `domain.LadoPermissionRank(s.Approval) > domain.LadoPermissionRank(ceiling)` 이면 `s.Approval = ceiling`. `default` 의 rank 가 0 이어도 상한 위로 친다(플랜 §1 i — Codex 의 default 는 bypass).
3. `AuthorizeConfigMode(ctx, target, mode)`: 실행이고 mode 가 `default` 이거나 상한보다 높으면 409 `LADO_APPROVAL_ABOVE_CEILING`, details `{sessionId, mode, ceiling}`. `AuthorizeExecutionChange(ctx, target)`: 실행이면 409 `LADO_EXECUTION_FIXED`, details `{sessionId}`. 둘 다 실행이 아니면 nil, 읽기 오류는 거부.
4. 훅: chat `SetTurnSettings` 는 게이트가 있으면 `ClampTurnSettings` 결과로 저장한다(오류면 그대로 반환). `SetConfigOption` 은 선택이 승인 모드로 풀리는 경우(`settingsFromConfigOptions` 가 `ApprovalMode` 를 정하는 경로 — 코드를 읽고 provider 호출 **전**에 그 모드를 알아내는 가장 작은 방법을 고른다)에만 `AuthorizeConfigMode`. `SwitchAgent`·`StartInterfaceTransition` 은 맨 앞에서 `AuthorizeExecutionChange`. 게이트가 nil 이면 업스트림 동작 그대로.
5. 프롬프트 훅: `LadoPromptSection` 타입을 `func(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) string` 로. `buildSystemPrompt` 의 호출부는 이미 kind·sessionID 를 갖고 있다. `ladoPromptSection` 의 "오케스트레이터만" 조건을 지우고 전부 소스에 맡긴다(소스가 kind 로 가른다). 복구·재개·에이전트 전환 재조립 경로는 모두 `buildSystemPrompt` 를 다시 부르므로 따로 고칠 것이 없다 — 테스트로 확인한다.
6. 커밋 메시지 예: `feat(lado): dispatch grant, turn-setting cap and fixed executions in the worker gate`.

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
- [ ] S05-G3: 게이트·LADO 관련 패키지 전체 통과
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/service/ladogate/ ./internal/service/ladotask/ ./internal/daemon/
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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
