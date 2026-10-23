# 2026-10-23 LADO 대기열·디스패처 — S06 태스크·프로젝트 서비스: 승인이 대기열로·디스패치 입력·워커 문구·모드 스위치

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 a·b·j·l·m·n, §6) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

계획 승인이 실제로 대기열 항목을 만들게 하고, 디스패처가 쓸 입력(이슈 검사, 고정된 계획·이슈 문서)과 워커 규칙 문구를 태스크 서비스에서 제공한다. 취소는 실행 중인 세션까지 끈다. LADO 모드는 개발 플래그 없이 바뀌고, 프로젝트당 동시 실행 상한을 설정에 더한다.

## 범위

- 고친다: `backend/internal/service/ladotask/plans.go`(`ApprovePlan` 이 항목을 만들어 store 에 넘김), `service.go`(Store·Deps 인터페이스, `Cancel`, 재승인 허용 규칙), `plan_text.go` 근처(디스패치 입력 렌더링), `prompt` 쪽(`PromptSource.WorkerSection` 과 S05 의 `Section` 이 워커에 그것을 쓰게), `backend/internal/service/ladoproject/service.go`(모드 스위치 상시 허용, `maxConcurrent`), `backend/internal/daemon/lado_wiring.go`(`AO_LADO_DEV`·`ladoDevEnv`·`ladoDevMode` 와 그 경고 제거, 새 의존성 연결), 관련 테스트(`ladoproject`·`daemon/lado_wiring_test.go` 의 개발 플래그 사례는 새 규칙으로 고친다).
- 만든다: `ladotask` 의 `IssueCheck`, `RunInput`, `WorkerSection`, `Killer` 의존성(인터페이스 `Stop(ctx, sessionID) error`; 구현은 S08 의 디스패처, 이 단계에서는 nil 이면 끄지 않는다).
- 하지 않는다: 디스패처(S07·S08), HTTP DTO(S09 — 단, 컴파일이 되게 controller 쪽 호출 시그니처만 맞춘다), 렌더러.

## 절차

1. `ApprovePlan`: 기존 검사·스냅숏 뒤 `domain.LadoQueueEntry{ID: "lado-queue-"+NewID(), TaskID, ProjectID: task.ProjectID, EnqueuedAt: now}` 를 만들어 `Store.ApproveLadoPlan(..., &snapshot, &entry)` 로 넘긴다. 감사 payload 에 `queueEntry` 를 더한다.
2. 재승인 규칙(`openTask` 또는 승인 검사): 최신 리비전의 승인을 다시 하는 것은 review_required 와 failed 에서 허용, blocked 는 지금처럼 UI 만 막는다(데몬은 거절하지 않음 — 5단계 그대로). 다른 상태(queued·dispatching·running 등)에서 같은 리비전을 다시 승인하면 409 `LADO_TASK_ALREADY_QUEUED`(details `{taskId, state}`).
3. `IssueCheck(ctx, task)`: `readIssueWithComments` 와 `domain.NewLadoIssueSnapshot(task.IssueRef, title, body, comments)` 로 해시(= `View` 의 IssueChanged 계산과 같은 방법), 이슈 상태를 함께 돌려준다. 읽은 상태는 `ObserveLadoTaskIssue` 로 기록한다(닫힌 이슈를 태스크가 Closed unmerged 로 보이게). 읽기 실패는 감싼 오류(토큰을 절대 넣지 않는다).
4. `RunInput(ctx, task)`: 항목의 승인이 가리키는 리비전의 고정 계획 바이트(`Blobs.Get(rev.SHA256, LadoPlanMaxBytes)`), 그 리비전 요청의 고정 이슈 스냅숏(blob)을 `renderIssueFile` 과 같은 방식(모든 텍스트 펜스)으로 렌더한 `IssueContext`, 리비전·승인·스냅숏(`Store.LadoPolicySnapshot(approvalID)`). 프롬프트 문장은 디스패처가 만든다(S07).
5. `WorkerSection(ctx, sessionID)`: 세션 레코드의 client request id → `LadoEntryPolicyByClientRequest` → 스냅숏 `prompt`(비었으면 ""). 그 밖의 세션은 "". S05 의 `Section` 이 워커일 때 이것을 부르게 바꾼다.
6. `Cancel`: store 가 열린 항목을 취소한 뒤(S03), 태스크에 살아 있는 실행이 있으면 `Killer.Stop(ctx, sessionID)`. 이슈는 건드리지 않는다. Killer 오류는 취소를 되돌리지 않고 로그만(취소는 이미 기록됨) — 응답에는 태스크를 그대로 돌려준다.
7. `ladoproject`: `Deps.ModeSwitch` 와 `LADO_MODE_UNAVAILABLE` 경로를 없앤다. `UpdateInput` 에 `MaxConcurrent *int`. `ladoMode`·`maxConcurrent` 중 하나는 있어야 한다(둘 다 없으면 `LADO_SETTINGS_EMPTY` 400 — 기존 `LADO_MODE_REQUIRED` 를 대체). 상한은 1-8(`LADO_DISPATCH_SETTINGS_INVALID`), `SetLadoProjectMaxConcurrent` + 감사. 모드 끄기의 presence 요구는 그대로. 설정 응답(`Settings`)에 `MaxConcurrent`.
8. `lado_wiring.go`: 개발 플래그 상수·읽기·경고·`ladoDevMode` 를 지우고 테스트도 지운다/고친다. `AO_LADO_DEV_PRESENCE` 는 **그대로**.
9. 커밋 메시지 예: `feat(lado): queue on approval, dispatch inputs, worker rule text and the mode switch`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S06-task-project.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- ladotask `TestApprovePlanQueuesTheTask` — 항목이 생기고 태스크가 queued; 재승인은 앞 항목을 superseded 로
- ladotask `TestApprovalAgainFollowsTheTaskState` — review_required·failed 는 다시 승인됨, queued 는 `LADO_TASK_ALREADY_QUEUED`
- ladotask `TestIssueCheckHashesTrustedComments` — 바뀌지 않은 이슈는 리비전의 해시와 같다(신뢰 댓글 포함); 본문 수정 → 다름; 닫힘 → 상태 done 이 기록됨; 읽기 실패 오류에 토큰 없음
- ladotask `TestRunInputCarriesThePinnedPlanAndIssue` — 계획 바이트가 blob 과 같고 IssueContext 가 고정 스냅숏을 펜스로 담는다(이슈를 다시 읽지 않는다)
- ladotask `TestWorkerSectionComesFromTheSnapshot` — `lado:` 세션은 스냅숏 문구, 문구 없는 스냅숏 → "", 다른 세션 → ""
- ladotask `TestCancelStopsTheRunningExecution` — Killer 가 세션 id 로 불리고 트래커에는 쓰기가 없다
- ladoproject `TestModeSwitchNeedsNoDevFlag` — 개발 플래그 없이 켜고, 끄기는 여전히 presence 를 요구, 트래커 intake 가 켜져 있으면 켜기 거부
- ladoproject `TestUpdateSettingsSetsMaxConcurrent` — 범위 검사, 감사 이벤트, 둘 다 없으면 400

## 수용 게이트

- [ ] S06-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S06-G1: ladotask 필수 테스트 6개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladotask/ TestApprovePlanQueuesTheTask TestApprovalAgainFollowsTheTaskState TestIssueCheckHashesTrustedComments TestRunInputCarriesThePinnedPlanAndIssue TestWorkerSectionComesFromTheSnapshot TestCancelStopsTheRunningExecution
  EXPECT: GO_TESTS_OK 6/6
- [ ] S06-G2: ladoproject 필수 테스트 2개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladoproject/ TestModeSwitchNeedsNoDevFlag TestUpdateSettingsSetsMaxConcurrent
  EXPECT: GO_TESTS_OK 2/2
- [ ] S06-G3: LADO 서비스·컨트롤러·데몬 패키지 전체 통과
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/service/ladotask/ ./internal/service/ladoproject/ ./internal/service/ladopolicy/ ./internal/httpd/controllers/ ./internal/daemon/
  EXPECT: GO_PKG_OK
- [ ] S06-G4: 개발 플래그가 코드에서 사라졌다(presence 개발 플래그는 남는다)
  CHECK: bash .claude/gate/lado-queue.sh none backend/internal 'ladoDevEnv' 'LADO_MODE_UNAVAILABLE' 'Set AO_LADO_DEV=on' && bash .claude/gate/lado-queue.sh has backend/internal/daemon/lado_plans_wiring.go 'AO_LADO_DEV_PRESENCE'
  EXPECT: HAS_OK
- [ ] S06-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S06-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
