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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — `Reviews` 를 S12 가 어떻게 맞출지(TriggerOutcome 에서 만든 run id 얻는 법), reviewer harness 판정 함수)
