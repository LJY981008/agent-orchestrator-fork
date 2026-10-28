# 2026-10-27 LADO 검증·통합 승인 — S09 필수 검사·명령 판정·리뷰 정책

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 d·e·j, 소유자 결정 4, §9) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

PRD 관문 6: 워커가 `done` 이라고 보고해도 필수 검사가 실패하면 통합 대기로 가지 않는다 — 보고는 주장이고 결정적 검사가 정한다(TDD §8.4). 규칙셋이 요구하는 검사를 head 의 `pr_checks` 와 대조하고, 검사 명령 실행 결과로 `commands` 항목을 판정한다. 계획이 `review: required` 일 때만 AO 리뷰를 돌리고(소유자 결정 4), 회차 3번 안에 승인이 없으면 결론 없음으로 사람에게 넘긴다(VER-003).

## 범위

- 만든다: `service/ladoverify/checks.go`, `review.go`, 테스트.
- 하지 않는다: 리뷰를 실제로 시작·취소하는 일과 기록(S11 루프가 이 단계의 판정·동작을 실행), 데몬 어댑터(S12).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `checks.go`:
   ```go
   func EvaluateRequiredChecks(required []string, named bool, checks []domain.PullRequestCheck) (domain.LadoVerifyItem, error)
   type CommandsInput struct {
       Commands      []domain.LadoCheckCommand
       Runs          []domain.LadoCheckRun // 이 head 의 실행, 시작 순
       RequiredNamed bool                  // 규칙셋이 필수 검사를 이름 댔는가
       WorktreeCode  string                // WorktreeAtHead 결과
   }
   func EvaluateCommands(in CommandsInput) (domain.LadoVerifyItem, error)
   ```
   - 필수 검사(플랜 §1 d): `named` 면 각 이름(대소문자 무시)에 `passed`·`skipped` 행 → pass; 없음·`unknown`·`queued`·`in_progress` → wait `checks_pending`; `failed`·`cancelled` → fail `checks_failed`. `named` 가 아니면 기록된 검사 전부로 같은 규칙, 하나도 없으면 pass `none_reported`. 세부 `Checks` 에 모든 검사(`required` 표시), `Required` 에 이름, `RulesetRead`.
   - 명령(플랜 §1 e): 명령 없음 → `RequiredNamed` 면 pass `none_configured`, 아니면 confirm `no_tests`. 명령마다 이름별 최신 실행을 본다: 없음 → (워크트리 준비 안 됨이면 그 코드, `worktree_missing` 은 fail, `worktree_not_at_head` 는 wait) 아니면 wait `commands_running`; `running` → wait `commands_running`; `passed` → 통과; `timed_out` → fail `command_timed_out`; `failed`·`error` → fail `commands_failed`; `cancelled` → wait `commands_running`(다시 돌 차례). fail 이 wait 보다 앞선다. 세부 `Runs`.
2. `review.go`:
   ```go
   type Reviews interface {
       Trigger(ctx context.Context, worker domain.SessionID, prURL string, harness domain.AgentHarness) ([]string, error) // 새로 만든 run id
       Runs(ctx context.Context, worker domain.SessionID) ([]domain.ReviewRun, error)
       Cancel(ctx context.Context, worker domain.SessionID) error
   }
   type ReviewInput struct {
       Required bool
       Reviewer string // 계획의 reviewer 그대로
       PRURL, HeadSHA string
       Rounds []domain.LadoExecutionReview
       Runs   []domain.ReviewRun
       Ready  bool // required_checks·commands·commit_messages·paths 에 wait·fail 없음
       Now    time.Time
   }
   type ReviewAction struct {
       Trigger     bool
       Harness     domain.AgentHarness // 빈 값이면 AO 의 선택
       CancelRunID string              // 30분 넘게 도는 LADO 실행
   }
   func EvaluateReview(in ReviewInput) (domain.LadoVerifyItem, ReviewAction, error)
   ```
   플랜 §1 j: `Required` 가 아니면 info `not_required`, 동작 없음. head 의 실행(`TargetSHA` = head, PR URL 같음; 출처 무관) 중 최신 완료가 `approved` → pass; `changes_requested` → fail `changes_requested`(세부 `RunID`·`Verdict`). 그 밖: `Ready` 가 아니면 wait `review_pending`, 동작 없음; head 에 도는 실행이 있으면 wait — 그것이 LADO 회차이고 `Now - CreatedAt > domain.LadoReviewStallAfter` 면 `CancelRunID`; 회차 수 < `domain.LadoReviewRoundCap` 이면 wait + `Trigger`; 다 썼으면 confirm `review_inconclusive`. `Reviewer` 가 알려진 reviewer harness(`domain.ReviewerHarness(…).IsKnown()` 또는 그에 맞는 함수)면 `Harness` 로, 아니면 빈 값과 세부 `Note: "unknown reviewer <x>; AO chose"`. 세부 `Rounds`.
3. 커밋 메시지 예: `feat(lado): required checks, check command results and the review policy`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S09-checks-review.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestFailingRequiredCheckKeepsValidatingDespiteReport` — 관문 6: 워커 보고(`ao report --done`)가 있어도 LADO 는 보고를 읽지 않는다 — 실패한 필수 검사 → `required_checks` fail, 그 기록의 `Overall()` fail, 그 기록을 가진 실행의 태스크 상태는 Validating `checks_failed`(S02 의 상태 함수로)
- `TestRequiredChecksFromTheRuleset` — 이름 댄 검사: pass·wait(없음, 진행 중)·fail; 필수 아닌 실패는 세부에만; `lado/integration` 은 목록에 없다(S08 의 `RequiredContexts`)
- `TestRequiredChecksWithoutARuleset` — 모두 대상, 하나도 없으면 pass `none_reported`, 진행 중이면 wait
- `TestCommandsItem` — none_configured / no_tests / 실행 없음(워크트리 준비·안 됨·없음) / running / passed / failed / timed_out / cancelled 순서와 fail 우선
- `TestReviewPolicyRounds` — 필요 없음 info; 준비 안 됨 wait·동작 없음; 준비·실행 없음 → Trigger; head 승인 → pass; 변경 요청 → fail; 실패한 실행 뒤 → 다음 Trigger; 3회차 소진 → confirm `review_inconclusive`; 30분 넘은 LADO 실행 → `CancelRunID`; 다른 head 의 결과는 무시; 모르는 reviewer → AO 선택과 Note

## 수용 게이트

- [ ] S09-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S09-G1: 필수 테스트 5개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestFailingRequiredCheckKeepsValidatingDespiteReport TestRequiredChecksFromTheRuleset TestRequiredChecksWithoutARuleset TestCommandsItem TestReviewPolicyRounds
  EXPECT: GO_TESTS_OK 5/5
- [ ] S09-G2: 패키지 전체가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg-isolated ./internal/service/ladoverify/
  EXPECT: GO_PKG_OK
- [ ] S09-G3: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S09-G4: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작: 2026-10-27, 브랜치 `lado8/S09`(lane 워크트리 `agent-orchestrator-fork-wt/lado8-S09`), HEAD `526dc7493`(chore(lado): merge the S14 lane) — 바탕은 S01~S05·S08·S14. S06(검사기)·S07(명령 실행기)은 다른 lane 이라 이 lane 에 없다 → S07 의 `WorktreeAtHead` 를 부르지 않고 그 답(코드 문자열)만 `CommandsInput.WorktreeCode` 로 받는다.
- 환경: Linux, go1.27.1(체인 스크래치 툴체인). G0 `defs-unchanged` → `DEFS_UNCHANGED`, 기준선 `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`.
- 출발점 실측:
  - domain(S02): `LadoRequiredChecksDetails{Required, RulesetRead, Checks[{Name, Status, Required}]}`, `LadoCommandsDetails{Runs[{Name, CheckRunID, Status, ExitCode *int, DurationMS}]}`, `LadoReviewDetails{Rounds, RunID, Verdict, Harness, Note}`, 코드 상수, `LadoReviewRoundCap`(3)·`LadoReviewStallAfter`(30분), `LadoExecutionReview`, `LadoCheckRun`·`LadoCheckRunStatus`, `LadoCheckCommand`. 태스크 상태는 `LadoTask.StateAt`·`StateReason`(내부 `taskState`).
  - AO 리뷰: `reviewsvc.TriggerRequest.Harness` 는 `domain.ReviewerHarness`(워커의 `AgentHarness` 와 일부러 나눈 어휘, `IsKnown()`). `TriggerOutcome` 은 `reviewcore.TriggerResult` 를 품어 `CreatedRuns []domain.ReviewRun`·`Run`·`Created` 를 준다. `RejectReviewedHead` 는 head 에 running 이거나 verdict(approved·changes_requested)가 있는 run 이 있으면 `ErrReviewAlreadyRunning`·`ErrHeadAlreadyReviewed` 로 거절한다(`headHasReview`, PR URL·`target_sha` 정확 비교) → 실패·취소된 run 뒤에는 다시 트리거된다. store `ListReviewRunsBySession` 은 최신 먼저.
  - `pr_checks` PK `(pr_url, name, commit_hash)`(이름 정확), 상태 7개(`unknown, queued, in_progress, passed, failed, skipped, cancelled`); `ListLadoCommitChecks(prURL, commit)` 는 name, created_at 순.
  - 계획의 reviewer 는 trim 한 자유 문자열(`domain/lado_plan.go` `review()`), 빈 값 가능.
- 앞 단계 메모 중 이 단계 몫: S08 — `RequiredContexts(read.Rules)` → `(contexts, named)`, 세부 `RulesetRead` 는 `read.Err == nil`. S02 — 세부 JSON 키·코드 이름 그대로. S03 — `ListLadoCommitChecks`, `ListLadoCheckRuns`(시작 순), `ListLadoExecutionReviews`(회차 순), `AddLadoExecutionReview`(Round = 기존 수 + 1). S05 — 테스트 시각 `t0`. S01·S04·S14 메모는 해당 없음.

## After — 수행 후 실측

- 커밋: `9ac9b65b4` feat(lado): required checks, check command results and the review policy(코드) + 이 엔트리 커밋.
- 바꾼 것:
  - `service/ladoverify/checks.go`(새): `EvaluateRequiredChecks(required []string, named, rulesetRead bool, checks []domain.PullRequestCheck)`, `CommandsInput`, `EvaluateCommands`, `CommandsToRun`; 내부 `requiredCheckOutcome`, `newestCheckRun`, `commandOutcome`.
  - `service/ladoverify/review.go`(새): `Reviews`(`Trigger`·`Runs`·`Cancel`), `ReviewInput`, `ReviewAction`, `ReviewReady`, `EvaluateReview`; 내부 `planReviewer`, `reviewRoundsUsed`, `newerVerdict`, `stalledRound`.
  - 테스트: 필수 5개(`checks_test.go` 4, `review_test.go` 1) + 보조 `TestReviewReadyWaitsForTheOtherItems`.
- 엔트리와 다르게(또는 더) 한 것과 이유:
  1. `EvaluateRequiredChecks` 에 `rulesetRead bool` 인자 — 엔트리 시그니처로는 세부 `RulesetRead` 를 채울 길이 없다(`named` 가 거짓이어도 읽기는 성공했을 수 있다). S08 메모대로 호출자가 `read.Err == nil` 을 넘긴다.
  2. `Reviews.Trigger` 의 harness 와 `ReviewAction.Harness` 의 타입은 `domain.ReviewerHarness`(엔트리: `domain.AgentHarness`) — AO 의 `TriggerRequest.Harness` 가 그 타입이고, domain 이 두 어휘를 일부러 나눈다(리뷰어 전용 도구는 워커가 아님). S12 가 변환 없이 넘긴다.
  3. `lado/integration` 행(대소문자 무시)은 판정에도 세부 `Checks` 에도 넣지 않는다 — LADO 가 승인 뒤 올리는 상태라, 규칙이 이름을 대지 않을 때 다른 무엇이 그 이름으로 pending 을 올리면 승인 전 영원히 wait(교착)이고, 게시 뒤 AO 가 관찰하면 기록 해시가 바뀐다. 엔트리의 "모든 검사"에서 이것만 뺀다.
  4. 같은 이름(대소문자 무시)의 행이 여럿이면 최악을 따른다(하나라도 실패 → fail): PK 가 정확한 이름이라 대소문자만 다른 두 행이 같은 head 에 있을 수 있다.
  5. 세부 `Checks` 는 이름(바이트 순)으로 안정 정렬 — 해시가 읽은 순서에 흔들리지 않게.
  6. 명령: 최신 실행이 `cancelled` 면 "실행 없음"과 같게 다룬다 — 워크트리가 준비됐으면 엔트리대로 wait `commands_running`, 아니면 다시 돌 수 없으므로 워크트리 코드(`worktree_not_at_head` wait, `worktree_missing` fail).
  7. 코드가 여럿이면 명령 순서에서 첫 fail(없으면 첫 wait)의 코드. 모르는 `WorktreeCode`·실행 상태는 오류. 실행이 다 기록된 명령은 워크트리가 사라져도 그 결과대로다.
  8. 세부 `Runs`: 아직 안 돈 명령은 `checkRunId`·`status` 빈 문자열, `exitCode` null, `durationMs` 0; 도는 실행도 `durationMs` 0(시간이 흘러 해시가 바뀌지 않게).
  9. `CommandsToRun(commands, runs)`(공개, 엔트리 밖) — `EvaluateCommands` 와 같은 규칙(이름 정확히 일치, 최신 실행, cancelled 는 다시)으로 아직 돌릴 명령을 순서대로 준다. S11 이 "실행 안 된 명령"을 같은 정의로 고르게.
  10. `ReviewReady(items)`(공개, 엔트리 밖) — `Ready` 의 정의(4개 항목에 wait·fail 없음)를 한곳에.
  11. 리뷰 세부: `Harness` 는 결정한 run 이 있으면 그 run 의 리뷰어, 없으면 LADO 가 요청할 리뷰어(모르면 빈 값); `RunID`·`Verdict` 는 결정한 run 만; `Rounds` 는 서로 다른 회차 번호 수; `Note` 는 결과와 무관하게 모르는 리뷰어면 늘. 리뷰어 비교는 공백을 한 칸으로 줄이고 소문자로(`Codex` → `codex`); 빈 리뷰어는 AO 선택, Note 없음. 필요한 리뷰인데 PR URL·head 가 비면 오류.
  12. 판정은 verdict(approved·changes_requested)만 본다(AO `headHasReview` 와 같은 기준, 상태 무관); 도는 run = 상태 running·verdict 없음. 같은 시각의 두 판정은 changes_requested 가 이긴다(보수적, 입력 순서 무관).
  13. 멈춘 회차: 도는 run 중 LADO 회차(run id 일치, `CancelledAt` 없음)이고 run 의 `CreatedAt` 에서 30분 **초과**인 것 중 가장 오래된 하나. 이미 취소 표시한 회차가 아직 running 이면 다시 취소하지 않고 기다린다. 엔트리 순서대로 `Ready` 가 아니면 취소도 하지 않는다.
- 확인한 명령과 결과:
  - gofmt·`go vet ./internal/service/ladoverify/` → 깨끗. `lado-verification.sh go-lint ./internal/service/ladoverify/...` → `GO_LINT_OK`.
  - 변이 7개(checks: wait 를 fail 앞에, cancelled 를 통과로, `lado/integration` 셈, `worktree_missing` 무시; review: 정체 `>=`, head 필터 빼기, 회차 `<=`) → 모두 해당 테스트 실패, 원복 뒤 `cmp` 로 같음 확인.
- 게이트(코드 커밋 전 작업 트리에서): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 5/5`, G2 `GO_PKG_OK`, G3 `GO_BUILD_OK`. 최종 HEAD 에서 `gate-check.sh --run` 으로 다시 기록한다(G4 포함).

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~2 전부, 필수 테스트 5 + 보조 1. 리뷰 시작·취소·기록(S11), 데몬 어댑터(S12)는 손대지 않았다. 엔트리 밖 공개 함수 2개(`CommandsToRun`, `ReviewReady`).
- 남은 틈:
  - 명령은 이름으로만 실행과 맞춘다(엔트리대로) — 같은 이름의 명령 글을 바꾸면 이 head 의 옛 결과가 남는다(새 head 부터 새 명령; 지금 head 는 "Run again"). 대소문자만 바꾼 이름(`Test`→`test`)은 옛 실행과 안 맞아 다시 돈다.
  - `Ready` 가 아닐 때는 멈춘 LADO 회차도 취소하지 않는다(엔트리 순서). 이미 취소 표시한 회차의 run 이 AO 에서 끝내 running 으로 남으면 항목은 계속 wait 다.
  - 한 트리거가 run 을 여럿(리뷰어 여럿) 만들고 S11 이 run 마다 `AddLadoExecutionReview` 하면 회차를 그만큼 쓴다(store 가 Round = 기존 수 + 1). 한 트리거 = 한 회차로 하려면 S11·store 가 같은 회차 번호를 허락해야 한다 — `reviewRoundsUsed` 는 서로 다른 번호를 세므로 그대로 맞는다.
  - 리뷰어는 AO id(소문자 비교)만 안다 — "Claude Code" 같은 표시 이름은 모르는 리뷰어로 Note.
- 다음 단계 메모(이름 그대로):
  - **S11(루프)**:
    - required_checks: `contexts, named := RequiredContexts(read.Rules)` → `EvaluateRequiredChecks(contexts, named, read.Err == nil, ListLadoCommitChecks(pr.URL, head))`.
    - commands: `EvaluateCommands(CommandsInput{Commands, Runs: ListLadoCheckRuns(session, head), RequiredNamed: named, WorktreeCode: WorktreeAtHead(…)})`; 시작할 것은 `CommandsToRun(commands, runs)` 의 첫 것, `WorktreeCode == ""` 이고 실행 칸이 빌 때만.
    - review: `EvaluateReview(ReviewInput{Required: plan.ReviewRequired, Reviewer: plan.Reviewer, PRURL, HeadSHA, Rounds: ListLadoExecutionReviews(session), Runs: Reviews.Runs(session), Ready: ReviewReady(앞 항목들), Now})`. `Trigger` → `Reviews.Trigger(ctx, session, prURL, action.Harness)` 가 준 run id 마다 `AddLadoExecutionReview{ReviewRunID, ExecutionSessionID, HeadSHA, CreatedAt}`; `CancelRunID` → `Reviews.Cancel(ctx, session)` 성공 뒤 `MarkLadoExecutionReviewCancelled(runID, now)`. 트리거한 패스의 기록은 wait `review_pending` 그대로 두고 다음 패스가 run 을 본다.
    - 수정 요청의 리뷰 본문: 세부 `RunID` 의 run 을 `Reviews.Runs` 에서 찾아 `Body`.
  - **S12(어댑터)**: `Trigger` = `reviewSvc.TriggerRequested(ctx, worker, reviewsvc.TriggerRequest{Harness: harness, Source: domain.ReviewTriggerManual, PRURL: prURL, RejectReviewedHead: true})` → 만든 run id 는 `outcome.CreatedRuns[i].ID`(비었고 `outcome.Created` 면 `outcome.Run.ID`); `ErrReviewAlreadyRunning`·`ErrHeadAlreadyReviewed` 거절은 오류로 넘기고 S11 은 로그만(다음 패스가 Runs 로 본다). `Runs` = store `ListReviewRunsBySession`(순서 무관), `Cancel` = 매니저 `Cancel(ctx, worker)`(결과는 버림).
  - **reviewer 판정**: 내부 `planReviewer` = 공백 정리 + 소문자 → `domain.ReviewerHarness(…).IsKnown()`; 모르면 Note `unknown reviewer <x>; AO chose`(S17 번역은 이 형식을 접두로).
  - **S15·S17**: 명령 세부의 "아직 안 돎" = `checkRunId` 빈 값(`status` 도 빈 값). `required_checks` 세부의 `required` 는 규칙이 이름을 안 댄 경우 모든 검사에 true; `lado/integration` 은 세부에 없다.
  - **패키지 안 이름**(lane 합칠 때 충돌 주의): `EvaluateRequiredChecks`, `requiredCheckOutcome`, `CommandsInput`, `EvaluateCommands`, `CommandsToRun`, `newestCheckRun`, `commandOutcome`, `Reviews`, `ReviewInput`, `ReviewAction`, `ReviewReady`, `EvaluateReview`, `planReviewer`, `reviewRoundsUsed`, `newerVerdict`, `stalledRound`; 테스트 `headCheckRows`, `requiredChecksDetailsOf`, `expectItemOutcome`, `checkRunAt`, `reviewRunOn`, `ladoRoundFor`, `reviewDetailsOf`, `reviewTestPR`.
