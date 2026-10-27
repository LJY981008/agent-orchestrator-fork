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

- 시작 HEAD `31668c4b3`(`chore(lado): merge the S02 lane`), 브랜치 `feat/lado-verification`, 트리 깨끗. S01(`c5d7f22c6`)·S02(`fa1fc68d7`)가 모두 들어 있음.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 툴체인), Node v24.21.0, umask 0022, TZ=UTC, 체인 러너 안(`CHAIN_RUN=1`). 실제 `~/.ao`·`~/.claude`·`~/.codex` 는 건드리지 않음(store 테스트만).
- 기준 실측: `defs-unchanged` → `DEFS_UNCHANGED`; `go-run ./internal/storage/sqlite/store/ 'Lado'` → `GO_RUN_OK 64`(2.6 s).
- 출발점(코드로 읽음): `fillLadoExecutionPRs` 는 `ListPRFactsForSessions` 로 URL·번호·병합·닫힘만 옮김(제목은 `PRFacts` 에 없음). `InsertLadoExecution`·`LinkLadoExecution` 은 0008 의 기준 브랜치 열을 쓰지 않고, `ladoExecutionFromGen`·`ladoProjectSettingsFromGen` 은 새 열을 도메인으로 옮기지 않음. 검증 5표를 읽고 쓰는 쿼리·메서드 없음. `WritePR`(legacy upsert)는 `head_sha`·`target_branch`·`title` 을 쓰지 않는다 — 테스트는 `WriteSCMObservation` 으로 PR 을 쓴다.
- 앞 단계 메모 중 이 단계 몫: S01 — 새 쿼리는 전체 열을 나열해 모델 타입을 받는다; 기록 upsert 는 `ON CONFLICT … DO UPDATE` 로 UPDATE 트리거만 부른다; 승인 트랜잭션은 증명 행을 먼저(FK); 정산은 변경 이벤트를 내지 않는다. S02 — `content_json` 을 `struct{ Items }` 로 읽으면 같은 해시; sentinel 둘(`ErrLadoCheckRunFinished`, `ErrLadoIntegrationAlreadyApproved`)은 S03 몫; `Verification` 은 PR 의 현재 head 기록일 때만 채운다; `ExecutionSessionID` 는 `SessionID`.

## After — 수행 후 실측

- 코드 커밋 `4580e5890` `feat(lado): store verification records and integration approvals` — 12 파일 +3038/−68(gen 생성물 포함).
- 바꾼 것:
  - `queries/lado_verify.sql`(새, ASCII): 검사 실행 5(`InsertLadoCheckRun` 은 늘 `running`, `FinishLadoCheckRun` 은 `running` 행만, `CancelRunningLadoCheckRuns`, `GetLadoCheckRun`, `ListLadoCheckRunsForHead`), 기록 3(`GetLadoVerification`, `UpsertLadoVerification`, `ListLadoVerificationOutcomes` — 세션·head 두 `sqlc.slice`), 리뷰 5, 근거 3, `ListLadoCommitChecks`(`pr_checks`), `ListLadoExecutionPRs`(세션별 최신 먼저: `updated_at DESC, url DESC`), `ListLadoExecutionsToVerify`(`sqlc.embed(e)` + 최신 PR(`NOT EXISTS newer`, 같은 정의) + `session_terminated`; 열림 또는 정산 전 병합·닫힘; 시작 순; LIMIT), `SettleLadoExecution`(`settled_at` NULL 일 때만), 승인 5(`ListLadoIntegrationStatusRetries` 는 시도 상한을 인자로).
  - `queries/lado_queue.sql` `InsertLadoExecution` 에 열 3; `queries/lado_project_settings.sql` 에 `GetLadoProjectChecks`·`UpsertLadoProjectCheckCommands`·`UpsertLadoProjectCheckTimeout`(행이 없으면 LADO 모드 꺼짐 행).
  - `store/lado_verify_store.go`(새): 엔트리의 메서드 전부(시그니처는 회고의 다음 단계 메모).
  - `store/lado_queue_store.go`: `fillLadoExecutionFacts`(= `fillLadoExecutionPRs` + 새 `fillLadoVerification`)를 태스크 읽기(`fillLadoQueueFacts`)·실행 화면(`ladoExecutionWithPR` → `ladoExecutionWithFacts`)·`LadoExecutionsPastDeadline` 이 지난다; `fillLadoExecutionPRs` 는 새 LADO 쿼리로 head·대상·제목까지; `LinkLadoExecution` 이 기준 브랜치·원격·로컬 SHA 를 쓴다; `ladoExecutionFromGen` 이 새 열 4개를 옮긴다.
  - `store/lado_project_settings_store.go`: `LadoProjectChecks`·`SetLadoProjectCheckCommands`·`SetLadoProjectCheckTimeout`; `LadoProjectSettings` 가 두 값을 채운다(행 없으면 `[]`·30); `ladoProjectSettingsFromGen` 은 `(…, error)`(JSON 디코드). `lado_plan_store.go`: 그 호출 한 곳 + `unwrapLadoSentinel` 에 새 sentinel 2.
  - `domain/lado_verify.go`: `ErrLadoCheckRunFinished`, `ErrLadoIntegrationAlreadyApproved`, `ErrLadoVerificationChanged`.
  - `store/lado_verify_store_test.go`: 필수 10 + 보조 `TestListLadoCommitChecksReadsOneCommit`.
- 엔트리와 다르게 한 것과 이유:
  1. 프로젝트 id 는 `projectID string` 이 아니라 `domain.ProjectID`(`LadoProjectChecks`, `SetLadoProjectCheckCommands`, `SetLadoProjectCheckTimeout`) — 같은 파일과 `ladoproject.Store` 인터페이스의 다른 메서드가 모두 `domain.ProjectID` 이고, S07 이 그 인터페이스에 더한다.
  2. `ApproveLadoIntegration` 은 엔트리의 두 거절에 더해 트랜잭션 안에서 그 head 의 저장된 기록 sha256 이 `a.VerificationSHA256` 인지 본다(기록이 없거나 다르면 새 `domain.ErrLadoVerificationChanged`). `ApproveLadoPlan` 이 최신 개정을 트랜잭션 안에서 다시 보는 것과 같은 이유 — 서비스의 확인과 트랜잭션 사이에 루프가 그 head 의 기록을 바꿀 수 있다. S13 엔트리는 승인 전에 다시 계산한 기록을 저장하므로 맞물린다. 거절 순서: 증명 재사용 → 이미 승인 → 기록 다름.
  3. `UpsertLadoVerification` 의 `Notified`: nil 이면 저장된 목록을 유지(새 기록은 `[]`), nil 이 아닌 목록(빈 목록 포함)은 그대로 저장 — 엔트리의 "넘긴 목록으로"와 플랜 §4 의 "unless given" 을 함께 만족. `changed` 는 새 기록이거나 outcome·reason·sha256 이 바뀔 때(트리거 조건과 같음 — 테스트가 변경 이벤트 수로 확인). `computed_at` 은 매번 갱신(이벤트 없음).
  4. `AddLadoExecutionReview` 는 회차가 1..3 이고 그 실행의 "기존 수 + 1" 일 때만 저장한다(트랜잭션 안 COUNT) — 표에 (실행, 회차) UNIQUE 가 없어서. S11 엔트리의 "회차 = 기존 수 + 1" 과 같다.
  5. `fillLadoExecutionPRs` 는 `ListPRFactsForSessions` 대신 새 LADO 쿼리 `ListLadoExecutionPRs` 를 쓴다(제목이 `PRFacts` 에 없고, 업스트림 쿼리는 리뷰·댓글 집계까지 해 무겁다). 최신 = `updated_at` 최대, 같으면 `url` 큰 것 — `ListLadoExecutionsToVerify` 의 `NOT EXISTS` 와 같은 정의.
  6. `ListLadoExecutionsToVerify` 는 PR 사실 외에 `SessionTerminated` 와 검증 사실(`Verification`·`Integration`)까지 채운다(실행 화면 읽기와 DeepEqual — 테스트). 프로젝트 id 는 싣지 않는다(도메인 `LadoExecution` 에 필드 없음; 루프는 태스크로 읽는다).
  7. 경계 동작: `StartLadoCheckRun` 은 상태를 늘 running 으로 넣고 다른 상태가 주어지면 거부; `FinishLadoCheckRun` 은 running·빈 상태·0 시각을 거부하고 모르는 id 는 일반 오류(sentinel 아님); `MarkLadoExecutionReviewCancelled` 는 이미 표시된 run 이면 nil(첫 시각 유지), 모르는 run 은 오류; `SettleLadoExecution` 은 이미 정산됐거나 실행이 아니면 아무것도 쓰지 않고 nil(감사도 한 번만); `SetLadoIntegrationStatus` 는 모르는 id 면 오류; `ReplaceLadoEvidenceLinks` 는 세션·head 가 빈 링크를 호출 값으로 채우고 다른 값을 대면 거부.
  8. `LadoExecutionsPastDeadline` 도 같은 채움 경로를 지난다("어디서나 같은 상태").
  9. 테스트는 모두 `newTestStore`(= `sqlitetest.MustOpen`) — 실행은 실제 경로(승인 → claim → `LinkLadoExecution`)로 만들고, 감사 사슬 길이는 원시 연결 대신 탐침 이벤트 id 의 차이로 잰다.
- 확인한 명령과 결과:
  - 새 테스트 11개: 첫 실행부터 통과(1.1 s). 테스트가 실제로 잡는지 변이 14개로 확인(채움 끄기, 해시 확인 빼기, notified 덮어쓰기, changed 늘 true, 기준 브랜치 안 씀, 최신 PR 거르기 빼기(gen SQL), 병합 head 승인 false, 회차 확인 빼기, 정산 감사 두 번, 검사 명령 증명 무시, 끝난 실행 다시 끝냄(gen SQL), 근거 지우기 빼기, 재게시 목록 상태 거르기 빼기, 제목 빼기) → 14개 모두 해당 테스트 실패, 원본 복구 확인.
  - `npm run sqlc` 뒤 gen 을 읽음: `lado_verify.sql.go` 의 SQL 이 쿼리 파일 전문과 같음(잘림 없음), 두 slice 쿼리는 `/*SLICE:…*/?` 를 각각 치환, `ListLadoExecutionsToVerifyRow` = `LadoExecution` + `PR*` + `SessionTerminated`.
  - `go test -count=1 ./...`(backend 전체) 모두 ok(1m55s). `golangci-lint`(오프라인 빌드본) `./internal/storage/sqlite/store/... ./internal/domain/...` → 0 issues. gofmt 깨끗.
- 게이트(HEAD `4580e5890`, 엔트리 커밋 전): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 10/10`, G2 `GO_RUN_OK 75`, G3 `SQLC_CLEAN`, G4 `QUERIES_ASCII`, G5 `GO_BUILD_OK`, G6 `TREE_CLEAN`. 엔트리 커밋 뒤 `gate-check.sh --run` 으로 전건을 다시 돌려 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~5 전부, 필수 테스트 10 + 보조 1. 서비스(S05~)·HTTP·DTO(S15)는 손대지 않음. 엔트리 밖: `ErrLadoVerificationChanged` 와 승인 트랜잭션 안의 기록 확인(After 2), 회차 순서 확인(After 4).
- 남은 틈:
  - 실행 응답 DTO 는 아직 새 사실(head·대상·제목·검증·승인)을 내보내지 않는다(S15). 태스크 상태는 이제 store 를 통해 Validating·Integration pending·Integrating 을 낸다 — 상태 enum·렌더러 문구는 S15·S16.
  - "최신 PR" 은 동점(`updated_at` 같음)을 `url` 로 가르지만 업스트림 `ListPRFactsBySessions` 는 동점 기준이 없다 — 같은 시각의 PR 둘을 가진 세션이면 AO 화면과 LADO 가 다른 PR 을 볼 수 있다(LADO 안에서는 한 정의).
  - 근거 링크 id 는 표 전체에서 유일해야 한다(PK) — 다른 head 에 같은 id 를 쓰면 거부된다. 상태 재게시의 "5분에 한 번" 은 store 가 거르지 않는다(마지막 시도 시각 열 없음 — S11 이 메모리로).
- 다음 단계 메모(이름·시그니처 그대로; 모두 `*Store`, 첫 인자 ctx):
  - 검사 실행: `StartLadoCheckRun(ctx, run domain.LadoCheckRun) error`(ID·세션·head·이름·명령·StartedAt; 상태는 늘 running) · `FinishLadoCheckRun(ctx, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage string, at time.Time) error`(running 이 아닌 행 → `domain.ErrLadoCheckRunFinished`) · `CancelRunningLadoCheckRuns(ctx, at time.Time) (int, error)`(모든 실행의 running → cancelled, `finished_at` = at) · `ListLadoCheckRuns(ctx, session domain.SessionID, headSHA string) ([]domain.LadoCheckRun, error)`(시작 순, 없으면 빈 slice) · `GetLadoCheckRun(ctx, id string) (domain.LadoCheckRun, bool, error)`.
  - 기록: `UpsertLadoVerification(ctx, v domain.LadoVerification) (changed bool, err error)` — content·sha256·outcome·reason 은 store 가 `v.Content()`·`v.SHA256()`·`v.Overall()` 로 만든다; 세션·head·`ComputedAt` 필수; `Notified` nil = 유지 · `GetLadoVerification(ctx, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error)`(Items 는 저장된 정규 JSON 그대로라 같은 `SHA256()`; `Notified` 는 없으면 `[]string{}`).
  - 리뷰: `AddLadoExecutionReview(ctx, r domain.LadoExecutionReview) error`(Round = 기존 수 + 1, 1..3, 아니면 오류) · `ListLadoExecutionReviews(ctx, session domain.SessionID) ([]domain.LadoExecutionReview, error)`(회차 순) · `MarkLadoExecutionReviewCancelled(ctx, runID string, at time.Time) error`.
  - 근거: `ReplaceLadoEvidenceLinks(ctx, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error`(한 트랜잭션에 지우고 넣기) · `ListLadoEvidenceLinks(ctx, session domain.SessionID, headSHA string) ([]domain.LadoEvidenceLink, error)`(criterion, id 순).
  - PR 검사: `ListLadoCommitChecks(ctx, prURL, commit string) ([]domain.PullRequestCheck, error)`(이름 순; URL 은 `pr.url` 정본, 실행의 `PR.URL` 이 그것).
  - 검증 대상·정산: `ListLadoExecutionsToVerify(ctx, limit int) ([]domain.LadoExecution, error)`(limit ≤ 0 → nil; PR·`SessionTerminated`·`SettledAt`·`Verification`·`Integration` 채움) · `SettleLadoExecution(ctx, session domain.SessionID, at time.Time, audit *domain.LadoAuditEvent) error`(한 번만 쓰고 그때만 감사; 정산은 변경 이벤트 없음).
  - 승인: `ApproveLadoIntegration(ctx, a domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error` — ID·세션·head·`ApprovedAt` 필수, 승인의 nonce 는 proof 의 것, `StatusState`(`pending`/`not_applicable`)·`StatusAttempts` 는 호출자; 거절 `domain.ErrLadoPresenceReplayed` → `domain.ErrLadoIntegrationAlreadyApproved` → `domain.ErrLadoVerificationChanged`(**승인 전에 다시 계산한 기록을 `UpsertLadoVerification` 으로 저장할 것**), 거절은 증명·승인·감사를 남기지 않는다 · `SetLadoIntegrationStatus(ctx, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error`(postedAt 0 → NULL) · `GetLadoIntegrationApproval(ctx, session domain.SessionID, headSHA string) (domain.LadoIntegrationApproval, bool, error)` · `ListLadoIntegrationStatusRetries(ctx, limit int) ([]domain.LadoIntegrationApproval, error)`(pending·failed 이고 시도 < `domain.LadoIntegrationStatusMaxAttempts`, 승인 시각 순).
  - 프로젝트 검사: `LadoProjectChecks(ctx, projectID domain.ProjectID) ([]domain.LadoCheckCommand, int, error)` · `SetLadoProjectCheckCommands(ctx, projectID domain.ProjectID, cmds []domain.LadoCheckCommand, proof *domain.LadoPresenceProof, audit domain.LadoAuditEvent) error`(검증은 호출자 `domain.ValidateLadoCheckCommands`; nil → `[]`; 재사용 증명 → `domain.ErrLadoPresenceReplayed`) · `SetLadoProjectCheckTimeout(ctx, projectID domain.ProjectID, minutes int, audit domain.LadoAuditEvent) error`(5..120 밖은 표 CHECK 오류 — 서비스가 먼저 400). 변경 시각 = 감사 이벤트 시각; `LadoProjectSettings` 도 두 값을 채운다; 설정 표는 변경 이벤트 없음.
  - 실행 읽기: `LinkLadoExecution` 이 `exec.BaseBranch`·`BaseRemote`·`DispatchLocalBaseSHA` 를 저장한다(S05 디스패처가 채워 넘김; 다시 링크하면 첫 값 유지). 채우는 경로 = `fillLadoExecutionFacts` — `fillLadoQueueFacts` 를 지나는 모든 태스크 읽기(`LadoTask`·`ListLadoTasks`·`ListLiveLadoTasks`·`SetLadoTaskPreferences`·`CancelLadoTask`), `LadoExecution`(실행 화면·게이트), `LadoExecutionsPastDeadline`; `ListLadoExecutionsToVerify` 는 `fillLadoVerification` 만(PR 은 쿼리에서). `LiveLadoExecutions` 는 여전히 PR 없이. PR head 가 비면(AO 가 아직 못 봄) 검증 사실 없음 → Validating `verifying`.
  - 새 sentinel(`domain/lado_verify.go`, `unwrapLadoSentinel` 에 등록): `ErrLadoCheckRunFinished`, `ErrLadoIntegrationAlreadyApproved`(→ 409 `LADO_INTEGRATION_ALREADY_APPROVED`), `ErrLadoVerificationChanged`(→ 409 `LADO_VERIFICATION_CHANGED`).
  - 테스트 도우미(`store_test`, `lado_verify_store_test.go`): `openLadoVerifyStore`(프로젝트 mer, LADO 모드 켬), `mustLadoExecution(t, s, key, started, terminated)`, `mustWriteLadoHeadPR(t, s, session, number, state, head, at, checks...)`(`WriteSCMObservation` 으로 — `WritePR` 은 head·대상·제목을 쓰지 않는다), `ladoVerification(t, session, prURL, head, at, items...)`, `ladoIntegrationApproval`, `mustApproveLadoIntegration`(증명 "nonce-<id>"), `ladoAuditProbe`, `ladoExecutionEvents`, `ladoHead(c)`.
