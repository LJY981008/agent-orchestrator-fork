# 2026-10-27 LADO 검증·통합 승인 — S02 도메인: 검증 기록·해시·상태·규칙 설정

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 b·c·e·l·o, §3) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

검증 기록(항목 8개, 결과, 코드, 세부)과 그 정규 JSON·해시를 도메인에 고정한다. 통합 승인은 이 해시에 서명하므로, 형식이 여기서 한 번 정해지면 바뀌지 않아야 한다. 태스크 상태(검증 중·통합 대기·통합 중·승인 없이 병합된 완료)도 저장 없이 사실에서 나오도록 `taskState` 를 고친다. 이후 단계가 쓸 이름을 이 단계가 정한다.

## 범위

- 만든다: `backend/internal/domain/lado_verify.go`(+ `lado_verify_test.go`).
- 바꾼다: `domain/lado_task.go`(상태·사유, `taskState`), `domain/lado_queue.go`(`LadoExecution`·`LadoExecutionPR` 새 사실), `domain/lado_rule.go`(규칙 id 4개), `domain/lado_presence.go`(동작 2개), `domain/lado_project.go`(검사 명령·시간 제한).
- 하지 않는다: 저장(S03), 서비스(S04~), HTTP enum(S15).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 이름(이후 단계가 그대로 쓴다):
   - `LadoVerifyFormat = "lado-verify/1"`; `LadoVerifyKind`(`required_checks`, `commands`, `commit_messages`, `paths`, `base_branch`, `protection`, `review`, `evidence`)와 순서 함수 `LadoVerifyKinds()`; `LadoVerifyOutcome`(`pass`, `wait`, `fail`, `confirm`, `info`); 플랜 §3 의 항목 코드 전부를 상수로(`LadoCodeChecksPending` 처럼), `not_required` 포함.
   - `LadoVerifyItem{Kind, Outcome, Code string; Details json.RawMessage}`(JSON `kind`, `outcome`, `code`, `details`)와 `NewLadoVerifyItem(kind, outcome, code, details any) (LadoVerifyItem, error)` — details 는 종류별 구조체를 `json.Marshal` 한 것.
   - 종류별 세부 구조체(필드 순서가 형식이다): `LadoRequiredChecksDetails{Required []string; RulesetRead bool; Checks []LadoVerifyCheck{Name, Status string; Required bool}}`, `LadoCommandsDetails{Runs []LadoVerifyCommandRun{Name, CheckRunID, Status string; ExitCode *int; DurationMS int64}}`, `LadoCommitMessagesDetails{Commits []LadoVerifyCommit{SHA, Subject, Result string}; PRTitle LadoVerifyText{Text, Result string}}`, `LadoPathsDetails{Protected []string; OutsidePlan []string; OutsideCount int}`, `LadoBaseBranchDetails{Branch, Remote string; Moved []LadoReflogEntry{Selector, Commit, Subject string}; AlreadyOnBase bool}`, `LadoProtectionDetails{Provider, Repo, Branch string; Missing []string; RequiredChecks []string; Error string}`, `LadoReviewDetails{Rounds int; RunID, Verdict, Harness, Note string}`, `LadoEvidenceDetails{FileVersion int; FileSHA256, FileHead string; Criteria []LadoEvidenceCriterion{Index int; State string; Refs []LadoEvidenceRef{Kind, Key, SHA string}}}`. 목록은 nil 대신 `[]` 로 직렬화되게(`orEmpty` 처럼) 만든다.
   - `LadoVerification{ExecutionSessionID SessionID; HeadSHA, PRURL string; Items []LadoVerifyItem; Notified []string; ComputedAt time.Time}` 와 메서드 `Overall() (LadoVerifyOutcome, string)`(fail > wait > confirm > pass; 사유는 첫 fail, 없으면 첫 wait 의 코드), `Content() ([]byte, error)`(`{"format","prUrl","headSha","items"}` 만, 시간·notified 없음), `SHA256() (string, error)`, `ConfirmCodes() []string`(confirm 항목 코드, 정렬·중복 없음).
   - 검사 실행: `LadoCheckRunStatus`(`running`, `passed`, `failed`, `timed_out`, `cancelled`, `error`), `LadoCheckRun{ID, ExecutionSessionID, HeadSHA, Name, Command; Status; ExitCode *int; LogSHA256, ErrorMessage string; StartedAt, FinishedAt time.Time}`.
   - 검사 명령: `LadoCheckCommand{Name, Command string}`(JSON `name`, `command`), `ValidateLadoCheckCommands([]LadoCheckCommand) *LadoCheckCommandProblem{Index int; Field, Reason string}`(플랜 §1 e 규칙), 상수 `LadoCheckCommandsMax = 10`, `LadoCheckTimeoutMin = 5`, `LadoCheckTimeoutMax = 120`, `LadoCheckTimeoutDefault = 30`.
   - 통합 승인: `LadoIntegrationStatusState`(`pending`, `posted`, `failed`, `not_applicable`), `LadoIntegrationApproval{ID, ExecutionSessionID, PRURL, HeadSHA, VerificationSHA256; Acknowledged []string; ProofNonce, Actor string; ApprovedAt time.Time; StatusState; StatusAttempts int; StatusPostedAt time.Time; StatusError string}`, `LadoIntegrationStatusContext = "lado/integration"`, `LadoIntegrationStatusMaxAttempts = 12`.
   - 근거: `LadoEvidenceState`(`linked`, `stale`, `claimed`, `inferred`, `file`), `LadoEvidenceRefKind`(`pr_check`, `review_run`, `artifact_version`, `lado_check_run`, `commit`, `claim`, `none`), `LadoEvidenceLink{ID, ExecutionSessionID, HeadSHA; Criterion int; State; RefKind; RefKey, SHA string; CreatedAt time.Time}`.
   - 리뷰 회차: `LadoExecutionReview{ReviewRunID string; ExecutionSessionID SessionID; HeadSHA string; Round int; CreatedAt, CancelledAt time.Time}`, `LadoReviewRoundCap = 3`, `LadoReviewStallAfter = 30 * time.Minute`.
   - 규칙 id: `LadoRuleIntegrationApproval = "integration.approval.required"`, `LadoRuleNoDirectWrite = "git.protected_branch.no_direct_write"`, `LadoRuleTestsMustPass = "test.command.must_pass"`, `LadoRuleProtectedPaths = "paths.protected"`. 설정: `LadoProtectedPathsConfig{Globs []string}`(JSON `globs`), `DefaultLadoProtectedPaths()`(플랜 §1 o 의 9개), `ValidateLadoProtectedPaths([]string) error`(1-50개, 각각 `affected_paths` 항목과 같은 규칙 — `lado_plan.go` 의 `validLadoAffectedPath` 를 쓴다); `LadoCommitLocaleConfig{Language string; AllowPrefixes []string}`(JSON `language`, `allowPrefixes`), `DefaultLadoCommitLocaleConfig()`(`ko`, 플랜 §1 f 의 11개).
   - 존재 증명 동작: `LadoPresenceIntegrationApprove = "integration.approve"`, `LadoPresenceCheckCommands = "project.check_commands"`.
2. `LadoExecution` 에 `BaseBranch`, `BaseRemote`, `DispatchLocalBaseSHA string`, `SettledAt time.Time`, `Verification LadoExecutionVerification{HeadSHA string; Outcome LadoVerifyOutcome; Reason string}`(PR 의 현재 head 기록, 없으면 0 값), `Integration LadoExecutionIntegration{HeadApproved bool; StatusState LadoIntegrationStatusState; MergedHeadApproved bool}`; `LadoExecutionPR` 에 `HeadSHA`, `TargetBranch`, `Title`. `LadoProjectSettings` 에 `CheckCommands []LadoCheckCommand`, `CheckTimeoutMinutes int`.
3. 상태(플랜 §1 l): `LadoTaskValidating = "validating"`, `LadoTaskIntegrationPending = "integration_pending"`, `LadoTaskIntegrating = "integrating"`; 사유 `LadoValidatingVerifying = "verifying"`, `LadoIntegratingStatusNotPosted = "status_not_posted"`, `LadoCompletedWithoutApproval = "merged_without_approval"`(검증 중 사유는 항목 코드 그대로). `taskState()` 순서: 병합됨 → Completed(병합된 head 승인 없으면 사유 `merged_without_approval`); 닫힘 → Closed unmerged; 시간 초과 → Failed `timed_out`; PR 없이 세션 끝 → Failed `ended_without_pr`; PR 열림 → 현재 head 에 승인 → Integrating(상태가 `failed` 면 사유 `status_not_posted`), 아니면 기록 없음·head 다름 → Validating `verifying`, 기록 wait/fail → Validating(기록 사유), pass/confirm → Integration pending; PR 없음 → Running. `StateAt` 의 "현재 실행" 판단(`current`)은 새 상태도 살아 있는 실행으로 본다(Running 과 같이 취급: Validating·Integration pending·Integrating 은 이전 승인의 실행이어도 보인다). 주석을 새 의미로 고친다.
4. 의도한 변화 두 가지: PR 이 열린 실행은 이제 Running 이 아니라 Validating(`verifying`, 기록이 생기기 전)이고, 승인 없이 병합된 PR 은 사유 `merged_without_approval` 가 붙은 Completed 다. 기존 테스트 중 "열린 PR = Running", "병합된 PR = 사유 없는 Completed" 를 기대하는 곳은 새 사실(기록·승인)을 넣거나 새 상태·사유를 기대하도록 고친다 — 도메인·`ladotask`·`ladoqueue`·`ladopolicy`·LADO 컨트롤러 테스트(G2·G3). 상태를 읽는 코드도 같이: `service/ladotask/plans.go` `approvable` 은 Validating·Integration pending·Integrating 을 Running 처럼 거절(`LADO_TASK_RUNNING`), `service/ladopolicy/impact.go` `awaitsDispatch` 는 새 상태를 디스패치 대기로 보지 않는다. HTTP enum 과 렌더러 문구는 S15·S16 몫.
5. 커밋 메시지 예: `feat(lado): verification records and the states after a pull request`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S02-domain.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoVerificationContentIsTheFormat` — 고정 예시 기록의 `Content()` 바이트와 `SHA256()` 을 박제(형식 고정); 시간·`Notified` 를 바꿔도 해시 같음; 세부 한 글자 바뀌면 해시 다름; 빈 목록은 `[]`
- `TestLadoVerificationOverall` — fail > wait > confirm > pass, 사유는 첫 fail(없으면 첫 wait) 코드, info 는 무시
- `TestLadoVerificationConfirmCodes` — confirm 코드만, 정렬·중복 제거
- `TestValidateLadoCheckCommands` — 개수 11, 빈 이름, 41자 이름, 허용 안 된 문자, 중복 이름, 빈 명령, 2001자 명령, NUL; 올바른 목록은 nil
- `TestLadoProtectedPathsConfig` — 기본 9개, 0개·51개·`..`·`/abs`·깨진 패턴 거부
- `TestLadoTaskStateFollowsVerification` — 위 절차 3 의 경우 전부(기록 없음, wait, fail, pass, confirm, head 다른 기록, 현재 head 승인, 상태 게시 실패, 병합+승인, 병합+승인 없음, 닫힘, PR 없음 Running, 시간 초과)와 각 사유

## 수용 게이트

- [ ] S02-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S02-G1: 필수 테스트 6개 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/domain/ TestLadoVerificationContentIsTheFormat TestLadoVerificationOverall TestLadoVerificationConfirmCodes TestValidateLadoCheckCommands TestLadoProtectedPathsConfig TestLadoTaskStateFollowsVerification
  EXPECT: GO_TESTS_OK 6/6
- [ ] S02-G2: 도메인과 상태를 읽는 LADO 패키지의 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/domain/ ./internal/service/ladotask/ ./internal/service/ladoqueue/ ./internal/service/ladopolicy/
  EXPECT: GO_PKG_OK
- [ ] S02-G3: LADO 컨트롤러 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/httpd/controllers/ 'TestLado'
  EXPECT: GO_RUN_OK
- [ ] S02-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S02-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `d53600b3b`(브랜치 `lado8/S02`, 트리 깨끗, 태그 `lado-step8-chain-def` 와 같음). Ubuntu, go1.27.1(스크래치 툴체인), umask 022, TZ=UTC. 실제 `~/.ao`·`~/.claude`·`~/.codex` 는 건드리지 않음(도메인·서비스 테스트만).
- 앞 단계 메모: 이 레인(lane-S02)은 체인 첫 단계로 돈다 — 브랜치에 S01 커밋이 없고 S01 엔트리의 Before/After/회고는 비어 있다. 따를 메모 없음. 도메인은 저장소에 기대지 않으므로 S01 없이 진행(이름은 플랜 §2 의 열·표와 맞춤).
- 출발점(코드로 다시 읽음):
  - `lado_task.go` `taskState`: 병합 → Completed ""; 닫힘 → Closed unmerged; 시간 초과 → Failed `timed_out`; PR 없이 세션 끝 → Failed `ended_without_pr`; 그 밖(열린 PR 포함) → Running. `stateAt` 의 `current` = 같은 승인 ∨ Running ∨ Completed.
  - `LadoExecutionPR{URL, Number, Merged, Closed}`; `LadoExecution` 에 기준 브랜치·원격·로컬 커밋·정산·검증·승인 사실 없음. `LadoProjectSettings{ProjectID, LadoMode, MaxConcurrent, UpdatedAt}`.
  - 규칙 id 4개(`plan.approval.required`, `agent.permission.ceiling`, `git.commit.message.korean`, `setup.change.requires_presence`), 존재 증명 동작 6개. `lado_plan.go` `validLadoAffectedPath` 있음(빈 글롭은 통과 — 계획 읽기가 trim·빈 항목 거부를 먼저 함).
  - `ladotask/plans.go` `approvable`: live ∨ Running ∨ Completed → `LADO_TASK_RUNNING`. `ladopolicy/impact.go` `awaitsDispatch`: 이미 허용 목록(Queued·Dispatching·Blocked·Failed·Review required)이라 새 상태는 자동으로 빠짐.
  - 옛 기대를 가진 테스트(바꾼 뒤 돌려서 찾음): 도메인 `TestLadoTaskStateAfterDispatch` 4건(열린 PR = Running)·`TestLadoTaskStateReason` "completed"(사유 ""), 컨트롤러 `TestLadoAPI_TaskCarriesQueueAndExecution`(lado-task-1 running, lado-task-3 completed ""), store `TestLadoTaskReadsCarryQueueAndExecution`(lado-task-2 completed ""). `ladotask`·`ladoqueue`·`ladopolicy` 테스트는 그대로 통과(열린 PR·병합 사유를 보는 곳 없음).

## After — 수행 후 실측

- 커밋: `fa1fc68d7` feat(lado): verification records and the states after a pull request · 이 엔트리 커밋.
- 바꾼 것:
  - 새 `domain/lado_verify.go`: 형식·종류·결과·항목 코드 전부, `LadoVerifyItem`·`NewLadoVerifyItem`, 종류별 세부 구조체 8개(+ 하위 `LadoVerifyCheck`·`LadoVerifyCommandRun`·`LadoVerifyCommit`·`LadoVerifyText`·`LadoReflogEntry`·`LadoEvidenceCriterion`·`LadoEvidenceRef`), `LadoVerification`(`Overall`·`Content`·`SHA256`·`ConfirmCodes`), 검사 실행·검사 명령(+검사)·통합 승인·근거·리뷰 회차 타입과 상수. 테스트 `lado_verify_test.go`.
  - `lado_task.go`: 상태 3개·사유 3개, `taskState`(절차 3 순서), `stateAt` 의 `current` 에 새 상태, `StateAt`·`StateReason` 주석. `lado_queue.go`: `LadoExecution` 새 사실(`BaseBranch`, `BaseRemote`, `DispatchLocalBaseSHA`, `SettledAt`, `Verification`, `Integration`), `LadoExecutionPR` 에 `HeadSHA`·`TargetBranch`·`Title`, 새 타입 `LadoExecutionVerification`·`LadoExecutionIntegration`. `lado_rule.go`: 규칙 id 4개, 보호 경로·커밋 로캘 설정. `lado_presence.go`: 동작 2개. `lado_project.go`: `CheckCommands`·`CheckTimeoutMinutes`.
  - `ladotask/plans.go` `approvable`: Validating·Integration pending·Integrating 도 `LADO_TASK_RUNNING`(주석 고침). `ladopolicy/impact.go`: 주석만.
  - 뜻이 바뀌어 고친 기존 테스트: 도메인 `TestLadoTaskStateAfterDispatch`(열린 PR 4건 → Validating), `TestLadoTaskStateReason`("completed" 는 병합 head 승인 있음 → "", 새 "merged without approval"·"validating" 줄), 컨트롤러 `TestLadoAPI_TaskCarriesQueueAndExecution`(lado-task-1 `validating`/`verifying`, lado-task-3 `completed`/`merged_without_approval`), store `TestLadoTaskReadsCarryQueueAndExecution`(lado-task-2 사유 `merged_without_approval`).
- 엔트리와 다르게 한 것과 이유:
  1. `LadoVerifyItem.Kind`·`Outcome` 은 `string` 이 아니라 `LadoVerifyKind`·`LadoVerifyOutcome`(JSON 은 같음) — 이름 붙은 상수와 바로 비교되게. 서명은 `NewLadoVerifyItem(kind LadoVerifyKind, outcome LadoVerifyOutcome, code string, details any)`.
  2. `NewLadoVerifyItem` 은 마샬 전에 거른다: 모르는 종류·결과, 코드 없는 wait·fail·confirm, 다른 종류의(포인터 포함) 세부. `details == nil` → `{}`. 세부는 `json.Marshal` 이 아니라 HTML 을 이스케이프하지 않는 `ladoCanonicalJSON`(다른 LADO 정규형과 같게; 형식 테스트가 `<`·`&` 를 그대로 박제).
  3. nil 목록 → `[]` 는 구조체마다 `MarshalJSON` 이 아니라 `NewLadoVerifyItem` 의 타입 스위치 `normalizeLadoVerifyDetails` 한 곳(중첩 `LadoEvidenceCriterion.Refs` 포함, 호출자 슬라이스는 복사). 세부는 늘 `NewLadoVerifyItem` 으로 만든다(직접 `json.Marshal` 하면 nil 이 `null`).
  4. `Content()` 는 빈 `Details` 를 `{}` 로 쓰고 항목 순서는 고치지 않는다(루프가 `LadoVerifyKinds()` 순으로 만든다; 순서가 바뀌면 해시가 바뀜 — 테스트).
  5. `LadoEvidenceCriterion.State`·`LadoEvidenceRef.Kind` 는 `LadoEvidenceState`·`LadoEvidenceRefKind`(값이 정확히 그 타입의 값). 검사 상태·커밋 결과(`LadoVerifyCheck.Status`, `LadoVerifyCommandRun.Status`, `LadoVerifyCommit.Result`, `LadoVerifyText.Result`)는 엔트리대로 string(S06 의 `ok`·`not_korean`·`unclear`·`body_unclear` 는 도메인 상수 아님).
  6. `LadoCheckRun`·`LadoIntegrationApproval`·`LadoEvidenceLink` 의 `ExecutionSessionID` 는 `SessionID`(`LadoVerification`·`LadoExecutionReview` 와 같게), `ID` 는 string.
  7. 검사 명령 규칙의 세부: 사유 상수 `LadoCheckCommandTooMany`(`too_many`, Index 10 = 넘친 첫 항목, Field "")·`Required`(`required`)·`TooLong`(`too_long`)·`Invalid`(`invalid`, 패턴 밖 이름·NUL 명령)·`Duplicate`(`duplicate`); Field 는 `name`·`command`. 이름 중복은 대소문자 무시, 공백뿐인 명령은 `required`, 길이는 문자(rune) 수. 상수 `LadoCheckNameMaxChars = 40`·`LadoCheckCommandMaxChars = 2000` 를 더함. `*LadoCheckCommandProblem` 에 `Error()` 는 없음.
  8. 보호 경로·커밋 로캘 설정은 `lado_rule.go`(`LadoCeilingConfig` 옆), 그 테스트는 `lado_rule_test.go`. `DefaultLadoProtectedPaths()` 는 부를 때마다 새 `[]string`. 더한 이름 `LadoProtectedPathsMax = 50`, `ErrLadoProtectedPathsInvalid`(→ `LADO_PROTECTED_PATHS_INVALID`, `%w` 로 감쌈). 빈 글롭·앞뒤 공백 글롭도 거부(계획 읽기가 trim 하고 빈 항목을 거부한 뒤 `validLadoAffectedPath` 를 부르는 것과 같은 결과; 서비스가 먼저 trim 하면 된다).
  9. `taskState` 의 모서리: PR head 를 모르면(빈 값) 기록이 있어도 `verifying`; wait·fail 인데 사유가 비면 `verifying`; Integrating 사유는 상태 `failed` 일 때만 `status_not_posted`(`pending`·`posted`·`not_applicable` 은 ""). 승인이 있으면 기록이 그 뒤 fail 이어도 Integrating(승인이 이김).
  10. 테스트를 더함: `TestNewLadoVerifyItemRefusesMistakes`, `TestDefaultLadoCommitLocaleConfig`, `ladotask` `TestApprovalRefusedWhileThePRIsVerifiedOrIntegrated`(세션이 끝난 뒤의 세 상태도 409 `LADO_TASK_RUNNING` — `approvable` 을 되돌리면 3/3 실패함을 확인).
  11. 엔트리 목록 밖 store 테스트 하나를 고침(위 `TestLadoTaskReadsCarryQueueAndExecution`): 이 레인의 store 는 승인을 채우지 못하므로(S03) 새 사유를 기대.
  12. `approvable` 거절 문구("This task's worker still runs, or its work merged…")와 렌더러 `lado.error.taskRunning` 은 그대로 둠 — 문구는 S16.
- 테스트 순서: 필수 테스트를 실패부터 보지 못했다 — 새 타입을 쓰는 테스트라 구현 전에는 컴파일되지 않음. 형식 테스트는 기대 JSON 을 손으로 먼저 쓰고 코드 출력이 그와 바이트 단위로 같음을 확인한 뒤 해시를 박제(python `hashlib` 으로 같은 문자열의 sha256 을 따로 계산해 일치).
- 확인한 명령과 결과: `go test -count=1` 백엔드 전체 — sqlite 밖 모든 패키지 ok(1m38s), `./internal/storage/sqlite/...` 3 패키지 ok. `golangci-lint` v2.13.2(오프라인 빌드본, `GOPROXY=off`) `./internal/domain/... ./internal/service/ladotask/... ./internal/service/ladopolicy/... ./internal/storage/sqlite/store/... ./internal/httpd/controllers/...` → 0 issues. gofmt 깨끗.
- 게이트: `gate-check.sh --run` 6/6 MET(HEAD `fa1fc68d7`). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 전부(절차 1~4), 필수 테스트 6개 + 보조 테스트 3개. 저장(S03)·서비스(S04~)·HTTP enum(S15)·문구(S16)는 손대지 않음. 엔트리 밖은 "다르게 한 것" 2·7·8·9 의 검사와 11 의 store 테스트.
- 남은 틈:
  - store(S03 전)는 `PR.HeadSHA`·`Verification`·`Integration` 을 채우지 않는다 → 이 브랜치에서는 열린 PR 의 태스크가 늘 Validating `verifying`, 병합된 태스크가 늘 `merged_without_approval` 이다. S03 이 채워야 Integration pending·Integrating·사유 없는 Completed 가 나온다.
  - 태스크 DTO 의 `state` enum 에 `validating`·`integration_pending`·`integrating` 이 아직 없다(컨트롤러는 이미 그 문자열을 보낸다; 런타임 enum 검사 없음) — S15. 렌더러 라벨·톤과 `lado.error.taskRunning` 문구 — S16.
  - `Content()` 는 항목 순서·종류 중복을 검사하지 않는다(만드는 쪽 책임). `ConfirmCodes` 는 빈 코드도 그대로 담는다 — `NewLadoVerifyItem` 이 confirm 에 코드를 요구하므로 실제로는 없음.
- 다음 단계 메모(이름은 그대로 쓴다):
  - **형식·박제**: `LadoVerifyFormat = "lado-verify/1"`; `Content()` = `{"format","prUrl","headSha","items":[{"kind","outcome","code","details"}]}`(HTML 이스케이프 없음, 시간·`Notified`·세션 없음). 박제 해시 `f19d30a036e0d25407bad04ee036e3dc2b61543b64f216d58f92a9ca86c3ffbf`(`lado_verify_test.go` `fixedLadoVerification`, 1900 바이트). 세부 JSON 키: required_checks `required, rulesetRead, checks[{name,status,required}]`; commands `runs[{name,checkRunId,status,exitCode(null 가능),durationMs}]`; commit_messages `commits[{sha,subject,result}], prTitle{text,result}`; paths `protected, outsidePlan, outsideCount`; base_branch `branch, remote, moved[{selector,commit,subject}], alreadyOnBase`; protection `provider, repo, branch, missing, requiredChecks, error`; review `rounds, runId, verdict, harness, note`; evidence `fileVersion, fileSha256, fileHead, criteria[{index,state,refs[{kind,key,sha}]}]`.
  - **항목**: `LadoVerifyKind` = `LadoVerifyRequiredChecks|Commands|CommitMessages|Paths|BaseBranch|Protection|Review|Evidence`, `LadoVerifyKinds()`; `LadoVerifyOutcome` = `LadoVerifyPass|Wait|Fail|Confirm|Info`; 코드 `LadoCodeNotRequired`, `LadoCodeChecksPending|ChecksFailed|NoneReported`, `LadoCodeCommandsRunning|WorktreeNotAtHead|CommandsFailed|CommandTimedOut|WorktreeMissing|NoTests|NoneConfigured`, `LadoCodeCommitMessagesFailed|CommitMessagesUnclear`, `LadoCodeProtectedPath|OutsidePlan`, `LadoCodeBaseMoved|ReflogOff|AlreadyOnBase|NoLocalBranch`, `LadoCodeUnprotected|ProtectionUnknown|UnprotectedGitLab`, `LadoCodeReviewPending|ChangesRequested|ReviewInconclusive`. `NewLadoVerifyItem(kind, outcome, code, details any)`(세부는 값 타입 구조체 또는 nil). 세부 구조체 `LadoRequiredChecksDetails`, `LadoCommandsDetails`, `LadoCommitMessagesDetails`, `LadoPathsDetails`, `LadoBaseBranchDetails`, `LadoProtectionDetails`, `LadoReviewDetails`, `LadoEvidenceDetails`.
  - **기록**: `LadoVerification{ExecutionSessionID, HeadSHA, PRURL, Items, Notified, ComputedAt}` · `Overall() (LadoVerifyOutcome, string)`(info 는 결과가 되지 않음 → `outcome` 열 CHECK 와 맞음) · `Content()`·`SHA256()` · `ConfirmCodes()`(빈 목록은 `[]string{}`). S03: `content_json` 을 `json.Unmarshal` 해 `struct{ Items []domain.LadoVerifyItem }` 로 읽으면 같은 해시가 나온다(테스트).
  - **검사 실행·명령**: `LadoCheckRunStatus` = `LadoCheckRunRunning|Passed|Failed|TimedOut|Cancelled|Error`; `LadoCheckRun{ID, ExecutionSessionID SessionID, HeadSHA, Name, Command, Status, ExitCode *int, LogSHA256, ErrorMessage, StartedAt, FinishedAt}`. `LadoCheckCommand{Name, Command}`(JSON `name`·`command`), `ValidateLadoCheckCommands` → `*LadoCheckCommandProblem{Index, Field, Reason}`(S07·S15 → 400 `LADO_CHECK_COMMANDS_INVALID {index, field, reason}`), 사유 `LadoCheckCommandTooMany|Required|TooLong|Invalid|Duplicate`, 한도 `LadoCheckCommandsMax`(10)·`LadoCheckNameMaxChars`(40)·`LadoCheckCommandMaxChars`(2000)·`LadoCheckTimeoutMin|Max|Default`(5·120·30). `LadoProjectSettings.CheckCommands`·`CheckTimeoutMinutes`(S03: 행 없으면 `[]`·`LadoCheckTimeoutDefault`).
  - **승인·근거·리뷰**: `LadoIntegrationStatusState` = `LadoIntegrationStatusPending|Posted|Failed|NotApplicable`, `LadoIntegrationStatusContext`, `LadoIntegrationStatusMaxAttempts`(12), `LadoIntegrationApproval{ID, ExecutionSessionID, PRURL, HeadSHA, VerificationSHA256, Acknowledged, ProofNonce, Actor, ApprovedAt, StatusState, StatusAttempts, StatusPostedAt, StatusError}`. `LadoEvidenceState` = `LadoEvidenceLinked|Stale|Claimed|Inferred|File`; `LadoEvidenceRefKind` = `LadoEvidenceRefPRCheck|ReviewRun|ArtifactVersion|CheckRun`(값 `lado_check_run`)`|Commit|Claim|None`; `LadoEvidenceLink{ID, ExecutionSessionID, HeadSHA, Criterion, State, RefKind, RefKey, SHA, CreatedAt}`. `LadoExecutionReview{ReviewRunID, ExecutionSessionID, HeadSHA, Round, CreatedAt, CancelledAt}`, `LadoReviewRoundCap`(3), `LadoReviewStallAfter`(30분). S03 이 더할 sentinel(`ErrLadoCheckRunFinished`, `ErrLadoIntegrationAlreadyApproved`)은 아직 없다.
  - **규칙(S04)**: `LadoRuleIntegrationApproval`, `LadoRuleNoDirectWrite`, `LadoRuleTestsMustPass`, `LadoRuleProtectedPaths`(모두 `LadoRuleID`); `LadoProtectedPathsConfig{Globs}`(JSON `globs`), `DefaultLadoProtectedPaths() []string`(9개), `LadoProtectedPathsMax`, `ValidateLadoProtectedPaths(globs) error`(`ErrLadoProtectedPathsInvalid`); `LadoCommitLocaleConfig{Language, AllowPrefixes}`(JSON `language`·`allowPrefixes`), `DefaultLadoCommitLocaleConfig()`. 존재 증명 `LadoPresenceIntegrationApprove`(`integration.approve`), `LadoPresenceCheckCommands`(`project.check_commands`).
  - **실행·상태(S03·S12·S15)**: `LadoExecution.BaseBranch|BaseRemote|DispatchLocalBaseSHA|SettledAt`, `Verification LadoExecutionVerification{HeadSHA, Outcome, Reason}`(PR 의 현재 head 기록일 때만 채운다; 다른 head 의 기록을 넣어도 `taskState` 가 `verifying` 으로 본다), `Integration LadoExecutionIntegration{HeadApproved, StatusState, MergedHeadApproved}`; `LadoExecutionPR.HeadSHA|TargetBranch|Title`. 상태 `LadoTaskValidating`(`validating`), `LadoTaskIntegrationPending`(`integration_pending`), `LadoTaskIntegrating`(`integrating`); 사유 `LadoValidatingVerifying`(`verifying`) 또는 기록의 사유(항목 코드), `LadoIntegratingStatusNotPosted`(`status_not_posted`), `LadoCompletedWithoutApproval`(`merged_without_approval`). S15 의 상태 enum·사유 enum 은 이 문자열 그대로.
