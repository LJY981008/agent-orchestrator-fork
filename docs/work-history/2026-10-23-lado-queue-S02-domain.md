# 2026-10-23 LADO 대기열·디스패처 — S02 도메인: 대기열 항목·실행·설정·태스크 상태

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 b·d·f·g·k, §3) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

대기열 항목·실행·디스패치 설정의 도메인 타입과, 디스패치 뒤까지 넓힌 태스크 상태 파생을 만든다. 모든 뒤 단계가 이 이름을 쓴다.

## 범위

- 만든다: `backend/internal/domain/lado_queue.go` — `LadoQueueEntryID`, `LadoQueueEntry`, `LadoQueueState` 와 `(e LadoQueueEntry) State(now time.Time) LadoQueueState`, 이유 상수(플랜 §3 의 문자열 그대로), `LadoExecution`·`LadoExecutionPR`, `LadoDispatchSettings`·`DefaultLadoDispatchSettings()`·`Validate()`, `LadoDispatchGrant{ProjectID ProjectID; Hash string; Active bool}`, client request id 도우미 `LadoClientRequestID(entry LadoQueueEntryID, attempt int) string`·`ParseLadoClientRequestID(s string) (LadoQueueEntryID, int, bool)`·`LadoDispatchHash(planSHA256, snapshotSHA256 string) string`(플랜 §1 d), 오류 `ErrLadoLeaseLost`.
- 고친다: `backend/internal/domain/lado_task.go` — `LadoTask` 에 `Queue LadoQueueEntry`, `Execution LadoExecution`; 상태 상수 `LadoTaskQueued`, `LadoTaskDispatching`, `LadoTaskRunning`, `LadoTaskFailed`, `LadoTaskCompleted`; `State()` 를 플랜 §3 순서로; `StateReason() string`. `LadoTaskApproved` 상수는 남기되 `State()` 가 더는 돌려주지 않는다(주석으로 이유).
- 테스트: `backend/internal/domain/lado_queue_test.go`(새), `lado_task_test.go`(기존 상태 테스트를 새 순서에 맞게 고친다).
- 하지 않는다: store·서비스·HTTP. `LadoTaskApproved` 를 쓰는 곳(렌더러·DTO enum)은 뒤 단계가 고친다 — 이 단계에서 컴파일만 깨지지 않게 한다.

## 절차

1. 플랜 §3 을 읽고, `lado_task.go`·`lado_plan.go`·`lado_policy.go`·`lado_rule.go`(`LadoPermissionRank`) 의 기존 꼴을 따른다.
2. `LadoQueueEntry.State(now)` 우선순위: superseded → cancelled → dispatched → failed → review_required → blocked → dispatching(lease_token 있고 lease_expires_at > now) → waiting(waiting_reason 이 있거나 next_attempt_at > now) → queued. 만료된 리스는 dispatching 이 아니다.
3. `LadoTask.State()` 는 플랜 §3 의 9단계 순서 그대로. 요점:
   - 실행이 있으면(`Execution.SessionID != ""`) PR merged → completed, PR closed(머지 안 됨) → closed_unmerged, `TimedOutAt` 이 있거나 세션이 끝났고 PR 이 없으면 → failed, 아니면 running.
   - 실행이 없을 때 이슈가 닫혔으면 closed_unmerged.
   - 유효한 승인 + 규칙 변경 → review_required(`rules_changed`), 항목의 review 이유 → review_required. 유효한 승인 + 차단 스냅숏 → blocked(`blocking_snapshot`), 항목의 blocked 이유 → blocked. 항목 failed → failed. 유효한 승인인데 항목이 없음 → review_required(`approved_before_queue`).
   - 항목 dispatching → dispatching, queued·waiting → queued. 리비전 있음 → planned, 없음 → draft.
   - 항목은 **최신 승인의 항목**만 본다(store 가 그렇게 채운다 — S03). 그래도 도메인은 `Queue.PlanApprovalID != Plan.Approval.ID` 이면 항목을 없는 것으로 친다.
   - 현재 시각이 필요하므로 `State()` 는 `StateAt(now time.Time)` 를 부르는 얇은 래퍼로 두고, `State()` 는 `time.Now()` 를 쓴다(테스트는 `StateAt`).
4. `StateReason()`: review_required·blocked 는 이유 문자열, failed 는 `attempts_exhausted`(항목 실패)·`timed_out`·`ended_without_pr`, queued 는 항목의 waiting_reason(없으면 ""), 나머지 "".
5. `LadoDispatchSettings.Validate()`: 상한 1-8, 분은 {0, 60, 120, 240, 480, 720, 1440} 중 하나, 아니면 `apierr.Invalid("LADO_DISPATCH_SETTINGS_INVALID", ...)` — 도메인이 apierr 를 쓰지 않는 꼴이면 sentinel 오류로 두고 서비스(S06)가 변환한다(기존 꼴을 따르고 After 에 적는다). 기본값 2, 480, true.
6. `LadoDispatchHash` = hex(sha256(planSHA256 + "\n" + snapshotSHA256)). `ParseLadoClientRequestID("lado:<entry>/<n>")` — `lado:` 접두, 슬래시 하나, 1 이상의 10진 정수만 받는다.
7. 커밋 메시지 예: `feat(lado): queue and execution domain types and task states`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S02-domain.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoQueueEntryState` — 9개 상태 각각, 만료된 리스는 dispatching 이 아님, 미래의 next_attempt_at 은 waiting
- `TestLadoTaskStateAfterDispatch` — running, completed(PR merged), closed_unmerged(PR closed), failed(timed out / 세션 끝 + PR 없음), 실행이 있으면 이슈가 닫혀도 completed 가 이긴다
- `TestLadoTaskStateBeforeDispatch` — 9단계 순서 전부: review(rules_changed) 가 blocked 보다 먼저, approved_before_queue, 다른 승인의 항목은 무시, dispatching·queued·waiting, failed(attempts_exhausted)
- `TestLadoTaskStateReason` — 상태별 이유 문자열
- `TestLadoDispatchSettingsValidate` — 기본값, 경계(0·1·8·9, 허용되지 않는 분)
- `TestLadoClientRequestID` — 만들기·파싱 왕복, 잘못된 꼴 거부, 해시가 입력 순서에 민감

## 수용 게이트

- [ ] S02-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S02-G1: 필수 테스트 6개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/domain/ TestLadoQueueEntryState TestLadoTaskStateAfterDispatch TestLadoTaskStateBeforeDispatch TestLadoTaskStateReason TestLadoDispatchSettingsValidate TestLadoClientRequestID
  EXPECT: GO_TESTS_OK 6/6
- [ ] S02-G2: 도메인 패키지 전체 통과
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/domain/
  EXPECT: GO_PKG_OK
- [ ] S02-G3: 이유 문자열이 플랜과 같다
  CHECK: bash .claude/gate/lado-queue.sh has backend/internal/domain/lado_queue.go '"rules_changed"' '"issue_edited"' '"plan_superseded"' '"approved_before_queue"' '"blocking_snapshot"' '"base_ref_mismatch"' '"agent_unsupported"' '"slot_global"' '"slot_project"' '"memory_tight"' '"issue_unreadable"' '"retry"' '"lado_mode_off"'
  EXPECT: HAS_OK
- [ ] S02-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S02-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `6152b694a`(`docs(lado): record S01`), 트리 깨끗, `defs-unchanged` → `DEFS_UNCHANGED`. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1.
- 도메인: `lado_queue.go` 없음. `LadoTask` 에 `Queue`·`Execution` 없음, `State()` 는 5단계 순서(cancelled → closed_unmerged → review_required(규칙 변경) → blocked(차단 스냅숏) → approved → planned → draft), `StateAt`·`StateReason` 없음.
- 기준선 모두 ok: `go test -count=1 ./internal/domain/`, `ladotask`·`ladopolicy`·`ladoproject` 전체, `-run 'Lado'` 의 `storage/sqlite/store`·`httpd/controllers`·`daemon`.
- `approved` 상태에 기대는 곳: 프로덕션 `service/ladopolicy/impact.go:89`(규칙 변경 영향 필터), DTO enum `httpd/controllers/lado_tasks_dto.go:35`, 다른 패키지 테스트 8개(After).
- 테스트 먼저(RED): 필수 테스트 6개와 기존 상태 테스트 갱신을 쓰고 돌림 → 빌드 실패(`undefined: domain.LadoQueueEntry` 등). API 를 빈 본문으로 두고 다시 → 7개 FAIL(`State = "approved", want "queued"`, `LadoClientRequestID = "", want lado:lado-queue-7f3a/2`, `defaults = {MaxConcurrent:0 ...}` 등 — 기능이 없어서 실패).

## After — 수행 후 실측

- 코드 커밋 `3092b53c9` `feat(lado): queue and execution domain types and task states` — 5 파일, +982 −26.
  - `domain/lado_queue.go`(새): `LadoQueueEntryID`, `LadoQueueEntry`(0006 의 열 전부, NULL 은 zero time, `DispatchedSessionID SessionID`), `LadoQueueState` 9개와 `State(now)`(절차 2의 우선순위; 리스는 `LeaseExpiresAt.After(now)`, 대기는 `WaitingReason != "" || NextAttemptAt.After(now)` — S01 claim 쿼리의 `<= now` 와 맞물림), 이유 상수(review 4·blocked 3·waiting 6 = 플랜 §3 문자열 그대로, 실패 3 `attempts_exhausted`·`timed_out`·`ended_without_pr`), `LadoExecution`(열 전부 + 읽기 사실 `SessionTerminated`·`PR LadoExecutionPR{URL, Number, Merged, Closed}`, `BaseCommitsAhead *int` — 도메인의 다른 nullable 정수와 같은 꼴), `LadoDispatchSettings`·`DefaultLadoDispatchSettings()`(2·480·true)·`Validate()`, `LadoMaxConcurrent = 8`, `LadoRunMinuteChoices`, `LadoDispatchGrant`, `LadoClientRequestID`·`ParseLadoClientRequestID`(`lado:` 접두, 빈 항목 id 거부, 시도는 `strconv.Itoa` 왕복이 같은 1 이상 — 부호·앞자리 0·두 번째 슬래시 거부)·`LadoDispatchHash`, `ErrLadoLeaseLost`.
  - `domain/lado_task.go`: `Queue`·`Execution`, 상태 상수 5개, `LadoTaskApproved` 는 남기고 주석으로 이유, `State()` = `StateAt(time.Now())`, `StateReason()`. 둘 다 내부 `stateAt(now)` 하나에서 나와 상태와 이유가 어긋나지 않는다.
  - 테스트: `lado_queue_test.go`(새 3개 — 항목 상태는 모든 사실을 켠 항목에서 이기는 사실을 하나씩 지우며 9단계 순서 전체를 확인), `lado_task_test.go`(새 3개 + 도우미 `queuedLadoTask`·`dispatchedLadoTask`·`approveLadoTaskAgain`; `TestLadoTaskStateFollowsRuleSnapshots` 를 새 순서로 — 스냅숏 있는 승인은 항목이 있어 `approved` → `queued`, 스냅숏 없는 승인 → `review_required`), `lado_plan_test.go` 의 `TestLadoTaskStateFollowsItsPlans`(유효 승인 → `queued`, 승인이 무효면 항목이 있어도 `planned`).
- `Validate()`: 도메인은 apierr 를 쓰지 않는다(`AgentConfig.Validate`·`ProjectConfig.Validate` 꼴) → sentinel `ErrLadoDispatchSettingsInvalid` 를 `%w` 로 감싼 문장(`invalid dispatch settings: maxConcurrent must be 1 to 8, got 9`). 서비스(S06·S09)가 `apierr.Invalid("LADO_DISPATCH_SETTINGS_INVALID", err.Error(), nil)` 로 바꾼다. 프로젝트 상한(S06)은 `domain.LadoMaxConcurrent` 로 같은 범위를 본다.
- 확인: `go test -count=1 ./internal/domain/` ok, `gofmt -l` 없음, `go vet` ok, golangci-lint v2.13.2 `./internal/domain/...` 0 issues. 독립 리뷰 서브에이전트 1회 — 지적 중 둘을 반영(회고 1·2, 테스트 추가), `retry` 주석 정정(S07 은 정책 읽기 오류에 `retry` 로 hold 한다).
- 다른 패키지의 의도된 깨짐(엔트리: "컴파일만 깨지지 않게"): 아래 8개가 `approved` 를 기대하다 `review_required`(`approved_before_queue`)를 받는다 — 승인이 아직 항목을 쓰지 않으므로(S03 store, S06 서비스). 나머지는 ok: `ladoconnect`·`ladogate`·`ladopresence`·`ladoproject` 전체, `-run 'Lado'` 의 `storage/sqlite`·`httpd/controllers`·`daemon`·`session_manager`·`service/chat`.
  - store(S03-G2 가 돈다): `TestLadoTaskReadsCarryPlanState`(`lado_plan_store_test.go:213`), `TestLadoTaskReadsCarryTheRuleSnapshot`(`:581`).
  - ladotask(S06): `TestApprovePlanBindsTheExactRevision`(`plans_test.go:586`), `TestApprovePlanStoresTheRuleSnapshot`(`:628`), `TestTaskStateFollowsRuleChanges`(`:725`), `TestReviewRequiredTaskCanBeApprovedAgain`(`:807`).
  - ladopolicy(S06): `TestDisablingKeepsHistoryAndSnapshots`(`service_test.go:900`), `TestImpactListsApprovedTasks`(`:1251`) — 이것은 테스트가 아니라 프로덕션 필터 `impact.go:89` 탓(회고 메모).
- 게이트(HEAD `3092b53c9`): `GATES=6 MET=6 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 이름: 실패 이유 상수 3개(`StateReason` 이 돌려주는 문자열), `LadoMaxConcurrent`·`LadoRunMinuteChoices`, `ErrLadoDispatchSettingsInvalid`.
- 플랜 §3 의 9단계 순서를 따르되, 문장이 정하지 않은 경우를 다음처럼 정하고 테스트로 고정했다.
  1. 실행은 최신 승인의 것이거나, 아직 돌거나(running) 머지됐을(completed) 때만 센다. 머지 없이 끝난 옛 승인의 실행은 기록일 뿐이라, 실패 뒤 다시 승인한 태스크가 새 항목대로 Queued 로 보인다(플랜 §6 재승인 허용, §10 "Failed … enables Approve plan again"). 문자 그대로("실행이 있으면")면 새 실행이 생길 때까지 Failed 에 머문다.
  2. 실패한 실행(시간 초과, PR 없이 종료) + 닫힌 이슈 → closed_unmerged. TDD §3.2 "Failed … and the task is not closed" 와 결정 k "the issue closed while no execution runs" 를 엔트리 문장("실행이 있으면 … failed")보다 앞세웠다. 돌거나 머지된 실행은 그대로 이슈보다 먼저다(필수 테스트 "completed 가 이긴다").
  3. 항목의 사실(review·blocked·failed·dispatching·queued)은 최신 승인이 유효할 때만 센다 — 5단계의 "규칙 사실은 유효한 승인에만" 과 같은 규칙. 새 리비전이 고정되면 항목이 남아도 Planned 다. 문자 그대로면 상한이 찬 동안 낡은 항목 때문에 Queued 에 머물고, blocked 항목은 UI 가 승인을 막아(S06) 새 리비전을 승인할 수 없다. 그래서 `plan_superseded` 는 태스크 상태에는 나오지 않고 항목(대기열 화면)에만 남는다.
  4. 유효한 승인의 항목이 없거나, 다른 승인의 것이거나, superseded·cancelled 이면 review_required(`approved_before_queue`). cancelled 는 디스패처가 닫힌 이슈 때문에 항목을 취소한 뒤 이슈가 다시 열린 경우로, 다시 승인하면 대기열에 든다.
  5. dispatched 항목인데 읽기에 실행이 없으면 running(store 가 한 트랜잭션으로 연결하므로 생기지 않을 방어). 항목의 review·blocked·failed 는 원시 열이 아니라 `Queue.State(now)` 로 본다(S01 쿼리가 열린 항목에만 표시하므로 열은 서로 배타).
- `StateReason()` 은 `time.Now()` 를 읽는다. 상태와 이유가 시계 때문에 어긋나지 않는다(리스 중인 항목의 waiting_reason 은 claim 이 비운다). 주입된 시계가 필요한 단계가 생기면 그때 `StateReasonAt` 를 더한다.
- 다음 단계 메모:
  - S03: 태스크 읽기의 `Queue` 는 `PlanApprovalID == Plan.Approval.ID` 인 항목(도메인은 다른 것을 무시), `Execution` 은 `PlanApprovalID` 를 반드시 채운다(위 1이 본다). PR 은 URL 이 비면 "PR 없음". 위 store 테스트 2개는 기대값을 `queued`(스냅숏 + 항목) 또는 `review_required`(스냅숏 없음)로 고친다. 태스크를 돌려주는 다른 store 메서드(`SetLadoTaskPreferences`·`CancelLadoTask` 등)도 Queue·Execution 을 채우지 않으면 승인된 태스크가 `approved_before_queue` 로 보인다.
  - S06: `ladopolicy/impact.go:89` 의 필터(`Approved`·`Blocked` 만)를 고친다 — 이제 승인된 태스크는 queued·dispatching·blocked·failed·review_required 다. 원래 뜻은 "유효한 승인의 스냅숏이 아직 지금 규칙과 맞는, 아직 디스패치되지 않은 태스크"(실행 뒤에는 스냅숏이 고정). 테스트 고정값이 아니라 필터를 고칠 것. `openTask`(`plans.go:467`)는 닫힌 이슈를 `State() == ClosedUnmerged` 로만 알아채는데, 실행이 돌거나 머지된 태스크는 이슈가 닫혀도 running·completed 다 — `Issue.State` 를 직접 볼 것. 같은 리비전 재승인만 막으면 Running 중 새 리비전 승인이 두 번째 워커를 띄울 수 있다(S07 검사 1 은 통과) — queued·dispatching·running·completed 에서는 승인 자체를 거절하는 편이 안전하다.
  - S09: DTO `state` enum(`lado_tasks_dto.go:35`)에 새 상태 5개, 이유는 `StateReason()`.
  - S10: `approved_before_queue` 문구는 "6단계 전 승인" 이 아니라 "대기열에 없음 — 다시 승인하면 대기열에 듦" 뜻으로(위 4).
