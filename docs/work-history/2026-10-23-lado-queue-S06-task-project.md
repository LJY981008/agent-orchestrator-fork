# 2026-10-23 LADO 대기열·디스패처 — S06 태스크·프로젝트 서비스: 승인이 대기열로·디스패치 입력·워커 문구·모드 스위치

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 a·b·j·l·m·n, §6) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

계획 승인이 실제로 대기열 항목을 만들게 하고, 디스패처가 쓸 입력(이슈 검사, 고정된 계획·이슈 문서)과 워커 규칙 문구를 태스크 서비스에서 제공한다. 취소는 실행 중인 세션까지 끈다. LADO 모드는 개발 플래그 없이 바뀌고, 프로젝트당 동시 실행 상한을 설정에 더한다.

## 범위

- 고친다: `backend/internal/service/ladotask/plans.go`(`ApprovePlan` 이 항목을 만들어 store 에 넘김), `service.go`(Store·Deps 인터페이스, `Cancel`, 재승인 허용 규칙), `plan_text.go` 근처(디스패치 입력 렌더링), `prompt` 쪽(`PromptSource.WorkerSection` 과 S05 의 `Section` 이 워커에 그것을 쓰게), `backend/internal/service/ladoproject/service.go`(모드 스위치 상시 허용, `maxConcurrent`), `backend/internal/daemon/lado_wiring.go`(`AO_LADO_DEV`·`ladoDevEnv`·`ladoDevMode` 와 그 경고 제거, 새 의존성 연결), 관련 테스트(`ladoproject`·`daemon/lado_wiring_test.go` 의 개발 플래그 사례는 새 규칙으로 고친다).
- 만든다: `ladotask` 의 `IssueCheck`, `RunInput`, `WorkerSection`, `Killer` 의존성(인터페이스 `Stop(ctx, sessionID) error`; 구현은 S08 의 디스패처, 이 단계에서는 nil 이면 끄지 않는다).
- 하지 않는다: 디스패처(S07·S08), HTTP DTO(S09 — 단, 컴파일이 되게 controller 쪽 호출 시그니처만 맞춘다), 렌더러.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
0-1. S03 뒤로 실패하는 테스트 6개를 이 단계에서 고친다(승인이 항목을 만들고 상태가 approved 대신 queued 가 되면서 생긴 것): ladotask `TestApprovePlanBindsTheExactRevision`·`TestApprovePlanStoresTheRuleSnapshot`·`TestTaskStateFollowsRuleChanges`·`TestReviewRequiredTaskCanBeApprovedAgain`, ladopolicy `TestDisablingKeepsHistoryAndSnapshots`·`TestImpactListsApprovedTasks`. 기대값을 새 상태에 맞추되, 영향 목록은 필터를 고쳐서(아래 2-1) 통과시킨다. G3 가 이 패키지들을 전부 돌린다.
1. `ApprovePlan`: 기존 검사·스냅숏 뒤 `domain.LadoQueueEntry{ID: "lado-queue-"+NewID(), TaskID, ProjectID: task.ProjectID, EnqueuedAt: now}` 를 만들어 `Store.ApproveLadoPlan(..., &snapshot, &entry)` 로 넘긴다. 감사 payload 에 `queueEntry` 를 더한다.
2. 승인 규칙(`openTask` 또는 승인 검사): 최신 리비전의 승인을 다시 하는 것은 review_required 와 failed 에서 허용, blocked 는 지금처럼 UI 만 막는다(데몬은 거절하지 않음 — 5단계 그대로). queued·dispatching 에서 **같은 리비전**을 다시 승인하면 409 `LADO_TASK_ALREADY_QUEUED`(details `{taskId, state}`), 새 리비전 승인은 허용(앞 항목 superseded). running·completed 에서는 **어떤 승인도** 409 `LADO_TASK_RUNNING`(details `{taskId, state}`) — 워커가 두 개 뜨지 않게(S02 메모). `openTask` 의 닫힌 이슈 판정은 `State() == ClosedUnmerged` 가 아니라 `task.Issue.State` 를 직접 본다(실행이 있으면 이슈가 닫혀도 running·completed 이기 때문).
2-1. `service/ladopolicy/impact.go` 의 승인된 태스크 필터(`Approved`·`Blocked` 만 보던 곳, 약 89행): 이제 승인된 태스크는 queued·dispatching·blocked·failed·review_required 다. 뜻은 "유효한 승인이 있고 아직 디스패치되지 않은 태스크"(실행 뒤에는 스냅숏이 고정) — 상태 목록을 고치거나 그 뜻으로 판정한다. 테스트 고정값이 아니라 필터를 고친다(S02 메모).
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
- ladotask `TestApprovalAgainFollowsTheTaskState` — review_required·failed 는 다시 승인됨, queued 의 같은 리비전은 `LADO_TASK_ALREADY_QUEUED`, queued 의 새 리비전은 승인(앞 항목 superseded), running·completed 는 `LADO_TASK_RUNNING`
- ladopolicy `TestImpactCountsQueuedAndFailedTasks` — 규칙 변경의 영향 목록에 queued·dispatching·failed·blocked·review_required 태스크가 들고, running·completed 태스크는 들지 않는다
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
- [ ] S06-G2b: 영향 필터 테스트
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladopolicy/ TestImpactCountsQueuedAndFailedTasks
  EXPECT: GO_TESTS_OK 1/1
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

- 시작 HEAD `7345f17e6`(`docs(lado): record S05`), 트리 깨끗. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1. `defs-unchanged` → `DEFS_UNCHANGED`.
- 기준선: ladotask 4·ladopolicy 2 실패(엔트리 절차 0-1 의 6개 그대로 — 승인된 태스크가 `review_required`(`approved_before_queue`)), `ladoproject` ok, `go test -count=1 ./internal/httpd/controllers/ ./internal/daemon/` ok.
- 출발점: `ApprovePlan` 이 store 에 `nil` 항목을 넘김. `openTask` 는 `State() == ClosedUnmerged` 로 닫힌 이슈를 봄. 재승인 규칙 없음(같은 리비전을 몇 번이든 승인 가능). `PromptSource.Section` 은 워커에 `""`. `Cancel` 은 store 취소만. `ladoproject` 는 `Deps.ModeSwitch`(= `AO_LADO_DEV`)가 꺼져 있으면 `LADO_MODE_UNAVAILABLE`, `ladoMode` 없으면 `LADO_MODE_REQUIRED`. `impact.go:89` 필터는 `Approved`·`Blocked` 만. `domain.LadoProjectSettings` 에 상한 없음.
- `AO_LADO_DEV`·`LADO_MODE_UNAVAILABLE` 의 흔적: `ladoproject/service.go`·테스트, `daemon/lado_wiring.go`·테스트, `controllers/lado_projects_test.go`(오류 봉투 예시 코드), `lado_projects_dto.go` 설명(S09), `openapi.yaml`·`schema.ts`(S09), `LADO.md`·`LADO.ko.md`·`TDD.md`(S13).

## After — 수행 후 실측

- 코드 커밋 `79eb0edb1` `feat(lado): queue on approval, dispatch inputs, worker rule text and the mode switch` — 20 파일, +1178 −185.
  - ladotask `plans.go`: `ApprovePlan` 이 승인 id 다음에 `lado-queue-<NewID>` 항목(TaskID·ProjectID·EnqueuedAt)을 만들어 `ApproveLadoPlan(..., &snapshot, &entry)`, 감사 payload 에 `queueEntry`. 승인 규칙 `approvable(task)` 는 계획 검사(`LADO_PLAN_CHANGED`·`SUPERSEDED`·`INVALID`) 뒤, blob·규칙 해석 앞에서 `withPolicy` 한 태스크로 판정. `openTask` 의 닫힌 이슈는 `task.Issue.State`(done·cancelled), 취소는 `CancelledAt`. `snapshotOf(req)` 는 새 `pinnedIssue(sum)` 에 맡김.
  - ladotask `dispatch.go`(새 파일): `IssueCheck{SHA256, State}` — `readIssueWithComments`(관찰 기록 포함) + `NewLadoIssueSnapshot(...).SHA256()`, 오류는 `check the issue of task <id>: %w`(apierr 를 감쌈 — `errors.As` 로 코드가 보인다). `RunInput{PlanText, IssueContext, Revision, Approval, Snapshot}` — 태스크의 최신 승인(`task.Plan.Approval`) → 리비전(sha 일치 확인) → `Blobs.Get(rev.SHA256, LadoPlanMaxBytes)`, `pinnedIssue(rev.IssueSnapshotSHA256)` 를 `renderIssueFile` 그대로(선택은 `{Omitted: snapshot.OmittedComments}`), `Store.LadoPolicySnapshot(approval.ID)`. 이슈를 다시 읽지 않는다.
  - ladotask `plan_text.go`: `PromptSource` 에 `Sessions`(`GetSession`)·`Policies`(`LadoEntryPolicyByClientRequest`). `WorkerSection` = 세션 → client request id → 항목 스냅숏 `prompt` 그대로(이미 `## LADO rules` 머리가 있다 — `ladopolicy.promptText`). 세션 없음·`lado:` 아님·모르는 항목·문구 없음 → `""`, 읽기 오류는 그대로(S05 결정 1). `Section` 은 kind 로 나눈다.
  - ladotask `service.go`: Store 에 `LadoPolicySnapshot`, `Killer` 인터페이스(`Stop(ctx, sessionID) error`)와 `Deps.Killer`(nil 이면 끄지 않음). `Cancel` 은 store 취소 뒤 `Execution.SessionID != "" && !SessionTerminated` 면 `Killer.Stop(context.WithoutCancel(ctx), id)`, 오류는 Warn 로그만 — 태스크를 그대로 돌려준다. 다시 취소하면 다시 끈다.
  - ladopolicy `impact.go`: `task.Plan.RulesChanged() || !awaitsDispatch(task)` 면 뺀다. `awaitsDispatch` = 상태가 queued·dispatching·blocked·failed·review_required 이고 그 승인의 실행이 없음(`Execution.PlanApprovalID != Approval.ID`). 테스트 픽스처 `approve` 는 서비스처럼 항목(`lado-queue-<rev>`)을 넘긴다.
  - ladoproject: `Deps.ModeSwitch`·`LADO_MODE_UNAVAILABLE`·`LADO_MODE_REQUIRED` 삭제. `UpdateInput.MaxConcurrent *int`; 둘 다 없으면 400 `LADO_SETTINGS_EMPTY`, 1-8 밖이면 400 `LADO_DISPATCH_SETTINGS_INVALID`(details `{min, max}`) — 둘 다 프로젝트 조회 전. 모드 먼저(`setMode`: 켜기는 intake 거부, 끄기는 presence) 그다음 상한(`SetLadoProjectMaxConcurrent` + 감사 `project.max_concurrent_changed`, actor `api`, payload `{from, to}`, 저장 뒤 다시 읽어 돌려줌). 현재 값과 같으면 쓰지 않는다.
  - domain/store: `LadoProjectSettings.MaxConcurrent`, store 가 채움(행 없으면 `LadoDefaultProjectMaxConcurrent` = 1; S04 메모의 첫 안). store 테스트 두 곳에 단언 추가.
  - daemon: `ladoDevEnv`·`ladoDevMode`·경고 삭제, `wireLado(store, sessions, chat)`(쓰지 않게 된 `log` 인자 삭제 — `unparam`) + `PromptSource{Settings, Sessions, Policies: store}`, `ladoproject.New(Deps{Store, Projects})`. `AO_LADO_DEV_PRESENCE` 파싱은 `lado_plans_wiring.go` 의 `ladoDevPresenceOn` 으로 옮김(테스트도). `daemon.go` 1줄(호출 인자).
  - 테스트: 필수 9개 + `TestWireLadoChangesLadoModeWithoutADevFlag`(옛 `…KeepsTheModeSwitchOffByDefault` 대체), `TestWireLadoGivesADispatchedWorkerItsRules`(실제 store 로 배선 확인), `TestUpdateSettingsAcceptsAnUnchangedValue`(옛 `…WithoutTheSwitch`). 지운 것: `TestUpdateSettingsRequiresLadoMode`·`…RefusesAChangeWithoutTheSwitch`·`…TurnsLadoModeOn`·`…RefusesLadoModeWhileTrackerIntakeIsOn`(뒤 둘은 `TestModeSwitchNeedsNoDevFlag` 로 흡수). 고친 것: 실패 6개의 기대값(`approved` → `queued`), 컨트롤러 봉투 예시 코드 `LADO_MODE_UNAVAILABLE` → `LADO_TRACKER_INTAKE_ENABLED`(G4 가 `backend/internal` 전체를 본다).
- 엔트리·플랜이 정하지 않았거나 다르게 적은 것:
  1. **살아 있는 세션이 있으면 어떤 승인도 `LADO_TASK_RUNNING`**. 엔트리는 running·completed 만 적었으나, 도메인 `taskState` 는 PR 이 머지 없이 닫히면(또는 시간 초과면) 세션이 살아 있어도 closed_unmerged(failed)를 준다 — 그 상태에서 새 승인이 두 번째 워커를 띄울 수 있다(독립 리뷰 blocker). 그래서 "태스크의 최신 실행 세션이 끝나지 않음"을 상태와 함께 본다(details 의 `state` 는 그때의 파생 상태, 예 `failed`·`closed_unmerged`). 결과: failed(시간 초과)·closed_unmerged 의 재승인은 세션이 끝난 뒤에만 된다. completed 는 세션이 끝나도 거절.
  2. `approvable` 은 `(task)` 만 받는다. queued·dispatching 이면 승인이 최신 리비전을 덮고 `ApprovePlan` 은 최신 리비전만 받으므로 "같은 리비전" 조건은 늘 참이다(리뷰 nit). 새 리비전이 고정되면 태스크가 planned 라 이 분기에 오지 않고, 그 승인이 앞 항목을 superseded 로 만든다(테스트).
  3. Gate 10 재사용 proof 확인(`TestApprovePlanBindsTheExactRevision`)은 규칙을 바꿔 재승인이 허용되는 상태에서 한다. queued 의 같은 리비전은 이제 store 가 proof 를 보기 전에 `LADO_TASK_ALREADY_QUEUED` 로 거절되기 때문.
  4. `IssueContext` 는 `renderIssueFile` 출력 그대로(머리 `# Issue <ref>, as LADO pinned it`, 태스크 id, 스냅숏 sha256, LADO 신뢰 경계). AO 가 그것을 `## Issue Context` + 자기 경계로 다시 감싼다. 스냅숏은 신뢰 못 할 댓글 수를 저장하지 않아 그 줄은 워커 문서에 없다(리뷰 nit, 주석으로 남김).
  5. `RunInput` 은 리비전의 `IssueSnapshotSHA256`(고정 때 요청의 값을 복사한 것)으로 스냅숏을 읽는다 — 디스패처 검사 2 가 비교하는 바로 그 해시. 요청 행을 다시 읽지 않는다. 승인 유효성은 보지 않는다(검사 1 의 몫, 같은 태스크 값을 넘길 것).
  6. 프로젝트 설정의 부분 쓰기: 모드가 거절되면 상한도 쓰지 않는다(테스트). 모드 저장 뒤 상한 저장이 실패하면 모드는 남는다 — 주석에 적음(리뷰 nit).
  7. 영향 목록의 review_required 는 규칙 외 이유(`issue_edited`·`approved_before_queue` 등)만 든다 — 규칙이 이미 바뀐 태스크는 지금처럼 뺀다(`TestImpactListsApprovedTasks` 의 "needs review already"). 필수 테스트에 "실행 뒤 failed(시간 초과)는 빠짐" 사례를 더했다.
- 확인:
  - 필수 9개 PASS. 변이 11개 모두 잡힘: `approvable` 끔·`openTask` 를 `State()` 로·항목 nil·Killer 끔·`WorkerSection` 빈 값·`RunInput` 이 이슈를 다시 읽음·옛 영향 필터·실행 검사 없음·`RulesChanged` 검사 없음(`TestImpactListsApprovedTasks`)·상한 검증 없음·살아 있는 세션 검사 없음.
  - `go test -count=1` ladotask·ladoproject·ladopolicy·controllers·daemon·ladogate·domain ok, `-run 'Lado'` 의 `storage/sqlite/...`·`session_manager`·`service/chat` ok, golangci-lint v2.13.2(오프라인 빌드 재사용) 변경 패키지 7묶음 0 issues, gofmt 없음.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker 1(위 결정 1 — 반영, 테스트로 고정), nit 3(결정 2 반영, 4·6 주석). IssueCheck 해시·RunInput 고정 데이터·WorkerSection 의 스냅숏 출처·영향 필터는 문제없음으로 확인.
- 게이트(HEAD `79eb0edb1`): `GATES=8 MET=8 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: `domain.LadoProjectSettings.MaxConcurrent`(S04 메모 첫 안), `pinnedIssue`, 데몬 배선 테스트 1개, `wireLado` 의 `log` 인자 삭제(daemon.go 1줄). 엔트리와 다르게 한 것은 After 결정 1(살아 있는 세션이면 거절) 하나 — 엔트리의 목적("워커가 두 개 뜨지 않게")을 상태 목록보다 앞세웠다.
- 이 단계 뒤 실패로 남은 테스트 없음(S03 이 남긴 6개 해소).
- 다음 단계 메모:
  - S07: `Tasks` 는 `*ladotask.Service` 의 `IssueCheck(ctx, task) (ladotask.IssueCheck, error)`·`RunInput(ctx, task) (ladotask.RunInput, error)`. 둘 다 검사 1 에서 읽은 같은 `task` 값을 넘길 것(`RunInput` 은 `task.Plan.Approval` 을 쓰고 유효성은 다시 보지 않는다). `IssueCheck` 오류는 `fmt.Errorf("check the issue of task %s: %w")` 로 감싼 apierr(`LADO_ISSUE_READ_FAILED`·`LADO_TRACKER_UNAVAILABLE`) — 메시지에 트래커 답이 들고 토큰은 들지 않는다. `IssueCheck` 가 이슈 상태를 기록하므로 닫힌 이슈의 태스크는 그 즉시 Closed unmerged 로 보인다(실행이 없을 때); 항목 취소는 다시 잡히지 않게 하는 몫. `RunInput.Snapshot.Content` 를 `ladopolicy.SnapshotContent` 로 풀어 agent·permission mode 를 읽는다. 승인 규칙이 "태스크의 최신 실행 세션이 살아 있으면 거절" 이므로, 새 승인과 살아 있는 워커가 겹치는 것은 스폰 중 경쟁뿐이다(link 뒤 태스크 재확인 — S04 메모).
  - S08: `ladotask.Killer` = `Stop(ctx, sessionID) error`. `Cancel` 은 최신 실행의 세션이 끝나지 않았으면 부른다(`context.WithoutCancel`, 오류는 로그). 마감 처리에서 세션이 실제로 끝나지 않으면(kill 실패) 그 태스크는 failed 로 보이지만 재승인이 `LADO_TASK_RUNNING` 으로 막힌다 — 마감 kill 이 세션 종료까지 가게 할 것.
  - S09: `Deps.Killer` 연결(디스패처가 태스크 서비스 뒤에 만들어지면 setter 필요). `ladoproject.UpdateInput.MaxConcurrent` 와 `domain.LadoProjectSettings.MaxConcurrent` 는 있으니 DTO 만 더하면 된다(컨트롤러는 아직 `MaxConcurrent` 를 넘기지 않음). `lado_projects_dto.go` 의 `ladoMode` 설명에 `AO_LADO_DEV=on` 문장이 남아 있다(`openapi.yaml`·`schema.ts` 에도) — S09 가 지우고 `npm run api`. 새 오류 코드: 400 `LADO_SETTINGS_EMPTY`, 400 `LADO_DISPATCH_SETTINGS_INVALID`(details `{min, max}`), 409 `LADO_TASK_ALREADY_QUEUED`·`LADO_TASK_RUNNING`(details `{taskId, state}`); 사라진 코드 `LADO_MODE_REQUIRED`·`LADO_MODE_UNAVAILABLE`. `daemon.go` 는 이 단계에서 1줄 바뀌었다(S09 의 diff-budget 30 에 2줄).
  - S10·S12: 위 오류 코드의 문구. 승인 버튼은 running·completed 외에 "실행 세션이 살아 있는" failed·closed_unmerged 에서도 데몬이 거절한다 — 태스크 DTO 의 `execution` 사실(세션 종료 여부)로 막거나 오류를 보여 줄 것.
  - S13: `AO_LADO_DEV` 는 `LADO.md`·`LADO.ko.md`·`TDD.md` 에 남아 있다. 결정 1(살아 있는 세션이면 승인 거절)을 TDD §5.2/§5.3 에 적을 것.
