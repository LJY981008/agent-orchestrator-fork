# 2026-10-27 LADO 검증·통합 승인 — S03 store: 기록·승인 트랜잭션·실행 읽기

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 a·e·h·j·k·l·m, §2, §4) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

S01 의 표를 읽고 쓰는 store 메서드를 만든다. 통합 승인은 증명 nonce·승인 행·감사 이벤트를 한 트랜잭션에 쓰고(재사용된 증명과 같은 head 의 두 번째 승인은 아무것도 쓰지 않는다), 태스크 목록과 실행 화면 읽기는 PR 의 현재 head·대상 브랜치·제목, 그 head 의 검증 결과, 승인 사실을 함께 실어 S02 의 상태가 저장 없이 나오게 한다.

## 범위

- 만든다: `backend/internal/storage/sqlite/queries/lado_verify.sql`(ASCII 만), `store/lado_verify_store.go`(+ `lado_verify_store_test.go`).
- 바꾼다: `store/lado_queue_store.go`(`fillLadoExecutionPRs` 새 필드, 새 `fillLadoVerification`, `LinkLadoExecution` 의 기준 브랜치 열), `store/lado_project_settings_store.go`(검사 명령·시간 제한), 필요한 기존 쿼리(`queries/lado_queue.sql`, `queries/lado_project_settings.sql`).
- 하지 않는다: 서비스 로직(S05~), HTTP(S15).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 기존 패턴을 읽는다: `lado_plan_store.go` `ApproveLadoPlan`(증명 삽입 `insertLadoPresenceProof` → `ErrLadoPresenceReplayed`, 감사 `appendLadoAuditEventTx`), `lado_setup_store.go`, `lado_queue_store.go`(`fillLadoQueueFacts`, `fillLadoExecutionPRs`, `LinkLadoExecution`), `pr_store.go`(`ListPRFactsForSessions`). 쿼리는 하위 쿼리 열을 표 이름으로 한정하고 시간은 UTC 로 넘긴다(`CAST(... AS TIMESTAMP)` 금지).
2. 메서드(이름은 이후 단계가 그대로 쓴다; 모두 `*Store`):
   - 검사 실행: `StartLadoCheckRun(ctx, run domain.LadoCheckRun) error`, `FinishLadoCheckRun(ctx, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage string, at time.Time) error`(running 이 아닌 행은 바꾸지 않고 `domain.ErrLadoCheckRunFinished` 같은 오류), `CancelRunningLadoCheckRuns(ctx, at time.Time) (int, error)`, `ListLadoCheckRuns(ctx, session domain.SessionID, headSHA string) ([]domain.LadoCheckRun, error)`(시작 순), `GetLadoCheckRun(ctx, id string) (domain.LadoCheckRun, bool, error)`.
   - 검증 기록: `UpsertLadoVerification(ctx, v domain.LadoVerification) (changed bool, err error)` — 결과·사유·해시 중 하나가 바뀌었을 때 `changed`(이때만 변경 이벤트가 난다), `notified_json` 은 넘긴 목록으로; `GetLadoVerification(ctx, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error)`.
   - 리뷰 회차: `AddLadoExecutionReview(ctx, r domain.LadoExecutionReview) error`, `ListLadoExecutionReviews(ctx, session domain.SessionID) ([]domain.LadoExecutionReview, error)`, `MarkLadoExecutionReviewCancelled(ctx, runID string, at time.Time) error`.
   - 근거: `ReplaceLadoEvidenceLinks(ctx, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error`(그 head 의 행을 지우고 한 트랜잭션에 새로), `ListLadoEvidenceLinks(ctx, session, headSHA) ([]domain.LadoEvidenceLink, error)`.
   - PR 검사: `ListLadoCommitChecks(ctx, prURL, commit string) ([]domain.PullRequestCheck, error)`(`pr_checks` 의 `pr_url`·`commit_hash` — LADO 쿼리가 업스트림 표를 읽는 것은 괜찮다).
   - 검증 대상: `ListLadoExecutionsToVerify(ctx, limit int) ([]domain.LadoExecution, error)` — 세션의 최신 PR 이 열려 있거나, 병합·닫힘인데 `settled_at` 이 NULL 인 실행, 시작 순, 채워진 PR 사실(`HeadSHA`, `TargetBranch`, `Title`, `Merged`, `Closed`) 포함; `SettleLadoExecution(ctx, session domain.SessionID, at time.Time, audit *domain.LadoAuditEvent) error`(감사 이벤트가 있으면 같은 트랜잭션).
   - 통합 승인: `ApproveLadoIntegration(ctx, a domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error` — 한 트랜잭션: 증명(재사용 → `domain.ErrLadoPresenceReplayed`), 승인(같은 실행·head → 새 `domain.ErrLadoIntegrationAlreadyApproved`), 감사; 실패하면 아무것도 남지 않는다. `SetLadoIntegrationStatus(ctx, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error`, `GetLadoIntegrationApproval(ctx, session, headSHA) (domain.LadoIntegrationApproval, bool, error)`, `ListLadoIntegrationStatusRetries(ctx, limit int) ([]domain.LadoIntegrationApproval, error)`(`pending`·`failed` 이고 시도 < 12).
   - 프로젝트 검사 설정: `LadoProjectChecks(ctx, projectID) ([]domain.LadoCheckCommand, int, error)`(행 없으면 `[]`, 30), `SetLadoProjectCheckCommands(ctx, projectID string, cmds []domain.LadoCheckCommand, proof *domain.LadoPresenceProof, audit domain.LadoAuditEvent) error`(증명이 있으면 같은 트랜잭션에 nonce), `SetLadoProjectCheckTimeout(ctx, projectID string, minutes int, audit domain.LadoAuditEvent) error`. 기존 `LadoProjectSettings` 읽기는 두 값을 함께 채운다. 새 sentinel 오류는 `domain` 에 둔다.
3. 실행 읽기: `LinkLadoExecution` 은 `exec.BaseBranch`, `BaseRemote`, `DispatchLocalBaseSHA` 를 쓴다. `fillLadoExecutionPRs` 는 `HeadSHA`, `TargetBranch`, `Title` 을 채운다(`ListPRFactsForSessions` 에 없으면 `ListPRsBySession` 같은 읽기를 쓴다). 새 `fillLadoVerification` 은 PR 의 현재 head 에 대해 검증 기록(`Verification`), 승인(`Integration.HeadApproved`, `StatusState`), 병합된 PR 이면 그 head 의 승인(`MergedHeadApproved`)을 채운다. 태스크 목록·태스크 하나·실행 화면 읽기가 모두 이 경로를 지나야 한다(상태가 어디서나 같게).
4. `npm run sqlc` → `gen/` 를 읽어 SQL 이 잘리지 않았는지 확인(ASCII 규칙).
5. 커밋 메시지 예: `feat(lado): store verification records and integration approvals`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S03-store.md` 로 전건 MET 를 확인한다.

## 필수 테스트

모두 `sqlitetest.MustOpen(t)`:

- `TestLadoCheckRunsLifecycle` — 시작·끝(끝난 행은 다시 못 끝냄)·시작 시 running 취소·head 별 목록 순서
- `TestLadoVerificationUpsertReportsChanges` — 같은 결과·해시는 `changed=false`, 결과·해시 변경은 true, `notified` 보존
- `TestLadoExecutionReviewsCountRounds` — 회차 기록·목록·취소 표시
- `TestLadoEvidenceLinksReplaceForHead` — 같은 head 는 통째로 바뀌고 다른 head 는 남음
- `TestApproveLadoIntegrationRecordsProofAndAuditTogether` — 증명·승인·감사가 함께; 재사용 증명과 같은 head 두 번째 승인은 아무것도 안 씀(감사 사슬 길이 그대로)
- `TestLadoIntegrationStatusRetries` — `pending`·`failed` 이고 12회 미만만 목록, 상태 갱신
- `TestLadoProjectCheckSettings` — 기본값, 증명과 함께 명령 저장(+감사), 시간 제한 저장(+감사)
- `TestLadoExecutionReadsCarryVerificationFacts` — 태스크 목록·태스크·실행 화면이 PR head·대상·제목, 현재 head 의 기록 결과·사유, 승인과 상태 게시 상태, 병합된 head 승인을 싣고, S02 의 `State()` 가 각 경우 Validating·Integration pending·Integrating·Completed 를 낸다
- `TestLinkLadoExecutionRecordsBaseBranch` — 기준 브랜치·원격·로컬 SHA 저장과 읽기
- `TestListLadoExecutionsToVerify` — 열린 PR, 정산 전 병합·닫힘 포함, 정산된 것·PR 없는 것 제외, limit

## 수용 게이트

- [ ] S03-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S03-G1: 필수 테스트 10개 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/storage/sqlite/store/ TestLadoCheckRunsLifecycle TestLadoVerificationUpsertReportsChanges TestLadoExecutionReviewsCountRounds TestLadoEvidenceLinksReplaceForHead TestApproveLadoIntegrationRecordsProofAndAuditTogether TestLadoIntegrationStatusRetries TestLadoProjectCheckSettings TestLadoExecutionReadsCarryVerificationFacts TestLinkLadoExecutionRecordsBaseBranch TestListLadoExecutionsToVerify
  EXPECT: GO_TESTS_OK 10/10
- [ ] S03-G2: store 의 기존 LADO 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/storage/sqlite/store/ 'Lado'
  EXPECT: GO_RUN_OK
- [ ] S03-G3: sqlc 생성물이 커밋과 같다
  CHECK: bash .claude/gate/lado-verification.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S03-G4: 쿼리 파일은 ASCII 만
  CHECK: bash .claude/gate/lado-verification.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S03-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S03-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 메서드 시그니처, 새 sentinel 오류, 채우는 읽기 경로)
