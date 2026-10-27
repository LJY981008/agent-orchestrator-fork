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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 타입·상수·메서드 이름 목록, 박제한 해시)
