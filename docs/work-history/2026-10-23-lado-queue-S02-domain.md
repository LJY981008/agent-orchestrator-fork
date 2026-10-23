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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
