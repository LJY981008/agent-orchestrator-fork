# 2026-10-26 LADO 대기열·디스패처 — S14 리뷰 반영: 백엔드

> 체인 `docs/chains/lado-queue-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(이 엔트리가 §1 결정 g 를 고친다) · 브랜치 `feat/lado-queue-dispatcher` · 소유자 승인 2026-10-26("체인 2단계", 시간 상한은 "PR 이 열리면 멈춤")

## 의도/목적 — 왜 이 작업을 하나

6단계 백엔드 리뷰(블로커 0, 메이저 3, 마이너 5)와 CI 미러의 gitleaks 실패를 고친다. 메이저 셋은 실제 결함이다: 상한이 찬 프로젝트에서 15초마다 잡고 놓는 변경 기록 폭주(AO change_log 상한 10만 행을 3~6일에 채움), 회차 시작 시각으로 계산해 이미 만료된 리스, 예약되지 않은 `lado:` id 로 남의 세션을 실행으로 연결하는 경로.

## 범위 (지적 번호 = 리뷰 번호)

- **B1 (메이저, 상한 폭주)** — `service/ladoqueue/admission.go`: 잡기 전에 상한이 찬 프로젝트 집합을 만든다(`perProject[p] >= cap(p)`, 상한은 회차마다 한 번 읽기). 그 프로젝트의 열린 항목은 `SetLadoQueueWaiting(ctx, slot_project, &p)`(이유가 바뀔 때만 씀). `queries/lado_queue.sql` 의 `ListClaimableLadoQueueEntries`·`ClaimLadoQueueEntry` 에 `AND project_id NOT IN (SELECT CAST(j.value AS TEXT) FROM json_each(sqlc.arg(full_projects)) AS j)`(빈 배열 `[]` 은 아무것도 빼지 않음; `queries/review.sql` 의 `json_each(?)` 꼴), store 시그니처에 `fullProjects []domain.ProjectID`. 같은 회차에 띄운 워커가 프로젝트를 채우면 그 프로젝트를 집합에 더한다. 잡은 뒤 hold 는 경쟁 대비로만 남긴다.
- **B2 (메이저, 만료된 리스)** — 잡을 때마다 리스를 지금 시각으로: `ClaimLadoQueueEntries(ctx, now, s.deps.Clock().UTC().Add(s.deps.Lease), 1, …)`. 복구·마감 뒤에 잡는 첫 항목도 마찬가지.
- **B3 (메이저, `lado:` 예약)** — 두 곳:
  - 게이트(`service/ladogate/gate.go`): client request id 가 LADO 형식(`domain.ParseLadoClientRequestID` 가 됨)이면 **프로젝트·모드 검사보다 먼저** 유효한 허가를 요구한다(없으면 `LADO_TASK_REQUIRED`). LADO 모드가 아닌 프로젝트·standalone 도 마찬가지.
  - 디스패처(`dispatch.go` 의 스폰 오류 뒤 조회, `recovery.go`): client request id 로 찾은 세션을 연결하기 전에 `rec.ProjectID == entry.ProjectID`, `rec.Kind == worker`, `rec.ClientRequestHash == 이 항목의 dispatch hash` 를 확인한다. 어긋나면 연결하지 않고 실패한 시도로 센다(오류로 돌려주지 않는다 — 복구가 모든 잡기를 막지 않게).
- **B4 (마이너 1, 태스크당 워커 하나)** — 검사 1 에서 태스크의 최신 실행이 다른 승인 아래에서 살아 있으면 hold(새 대기 이유 `run_live`, 1 min). 회차마다 정리: 취소된 태스크, 또는 항목이 대체·취소된 승인의 살아 있는 실행은 `Sessions.RequestKill`(실패하면 다음 회차에 다시). 감사 `execution.stopped`(actor `lado`, payload task·session·reason).
- **B5 (마이너 2, 복구의 kill 실패)** — `recovery.go`: 끝나지 않은 세션을 끄지 못했고 그 세션이 살아 있으면 리스를 놓지 않는다(오류를 돌려줘 다음 회차에 다시). TDD 의 "알려진 틈" 문장은 S15 가 고친다.
- **B6 (마이너 3, 소유자 결정: PR 이 열리면 시간 상한 멈춤)** — `deadline.go`: 실행 세션에 PR 이 있으면(상태 무관) 마감 처리 대상이 아니다(`LadoExecutionsPastDeadline` 결과에서 `exec.PR.URL != ""` 를 거르거나 쿼리에서). PR 이 열린 뒤 끝난 세션은 지금처럼 PR 상태로 completed/closed_unmerged.
- **B7 (마이너 4, 멈춘 실행의 복구)** — `session_manager` 의 사용자 복구 진입점 `RestoreWithMode`·`ResumeAgentWithMode` 첫머리에 게이트 훅 `AuthorizeRestore(ctx, id)`(인터페이스에 추가): 시간 초과로 끝났거나 태스크가 취소된 LADO 실행이면 409 `LADO_EXECUTION_FIXED`. `RestoreAll`(데몬 시작 때 살아 있던 세션 복구)은 건드리지 않는다. 두 테스트 게이트(no-op)도 갱신.
- **B8 (마이너 5, 끝없는 재확인)** — 이슈 읽기 실패의 hold 를 1, 2, 5, 15, 60 분으로 늘린다(`attempt` 가 아니라 항목의 연속 실패 수 — `waiting_reason` 과 `next_attempt_at` 으로 계산하거나 열을 더하지 말고 가장 작은 방법으로). 트래커가 이슈가 없다고 답하면(GitHub `ErrNotFound`·GitLab 의 같은 오류 — ladotask 가 `domain.ErrLadoIssueNotFound` 로 감싸 돌려준다) 항목을 failed 로(오류 문장 `LADO_ISSUE_NOT_FOUND: the issue no longer exists on the tracker`).
- **G (CI)** — `.github/.gitleaks.toml` 전역 allowlist `regexes` 에 테스트의 합성 토큰만 맞는 정규식 `'''ghp_[Ll]ado[0-9A-Za-z]{32}'''` 와 주석("LADO token-isolation tests use synthetic GitHub tokens"). 이미 커밋된 두 테스트(`ladoqueue/dispatch_test.go`, `ladotask/dispatch_test.go`)의 토큰이 이 꼴인지 확인한다.
- **N (니트)** — 표시 이름은 100자(rune)로 자른다(HTTP 경로의 `maxDisplayNameLen` 과 같은 값); `Tick` 은 설정 읽기가 실패해도 복구·마감을 먼저 한다; 안 쓰는 코드 삭제(`LadoExecutionByClientRequest`·`GetLadoExecutionByEntry`, `ListLadoQueueEntries`, `ListLadoQueueEntriesForTask`, `SetLadoProjectMaxConcurrent :execrows` — 쓰는 곳이 없는지 확인 뒤); `daemon/lado_wiring_test.go` 의 `AO_LADO_DEV` 잔재 제거; 태스크 DTO 의 `queue` 에 `enqueuedAt` 추가(S15 의 대기열 정렬이 쓴다) 후 `npm run api`.
- 하지 않는다: 렌더러·문서(S15), 마스터 플랜 문서.

## 절차

0. 시작 전에 S01~S13 엔트리의 `## 비교/회고` 와 "다음 단계 메모"를 읽는다(러너는 직전 커밋 요약만 넘겨 준다). 리뷰 원문 요약은 이 엔트리의 범위 목록이 전부다.
1. 지적마다 실패하는 테스트 → 고침 → 통과 순서. 테스트 이름은 아래 필수 테스트 그대로.
2. 쿼리를 고치면 ASCII 만, `npm run sqlc` 뒤 gen 을 직접 읽어 `json_each` 문장이 온전한지 본다.
3. 업스트림 훅(B7 의 `manager.go` 두 진입점, `ports/lado_gate.go` 는 LADO 파일)은 몇 줄씩만 — G9 가 잰다.
4. 커밋을 지적 묶음별로 나눠도 된다(예: `fix(lado): stop the at-cap claim churn and stale leases`, `fix(lado): reserve LADO client request ids`, `fix(lado): one worker per task and restore guards`, `chore(lado): allow the synthetic test tokens in gitleaks`).

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다. 회고에 S15 가 알아야 할 것(새 대기 이유 `run_live`, 새 오류 문장, `enqueuedAt`, 시간 상한 규칙)을 "다음 단계 메모"로 적는다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-queue-S14-backend-fixes.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- ladoqueue `TestFullProjectsAreNotClaimed` — 상한이 찬 프로젝트의 대기 항목은 여러 회차 동안 잡히지 않고(attempt_count 그대로) `slot_project` 를 한 번만 쓴다(change_log 행 수로 확인 — 실제 store), 다른 프로젝트 항목은 잡힌다(B1)
- ladoqueue `TestLeaseStartsAtTheClaim` — 앞 스폰이 시계를 2분 넘게 움직여도 다음 항목의 리스는 그 항목을 잡은 시각 + Lease(B2)
- ladogate `TestLadoIDsNeedAGrantEverywhere` — LADO 형식 id 는 standalone·LADO 모드 아닌 프로젝트에서도 허가 없이 거부(B3)
- ladoqueue `TestForeignSessionIsNotAdopted` — 같은 id 의 다른 프로젝트·다른 해시·오케스트레이터 세션은 연결되지 않고 시도로 센다; 스폰 오류 뒤 조회와 복구 둘 다(B3)
- ladoqueue `TestOneWorkerPerTask` — 다른 승인의 실행이 살아 있으면 `run_live` 로 hold(B4)
- ladoqueue `TestSweepStopsUnwantedRuns` — 취소된 태스크·대체된 승인의 살아 있는 실행을 끄고 감사; kill 실패는 다음 회차에 다시(B4)
- ladoqueue `TestRecoveryKeepsTheLeaseWhenTheStopFails` — B5
- ladoqueue `TestDeadlineStopsOnlyBeforeAPR` — PR 이 있으면 마감이 지나도 끄지 않는다, 없으면 끈다(B6)
- session_manager `TestRestoreAsksTheLadoGate` — `RestoreWithMode`·`ResumeAgentWithMode` 가 게이트 거부면 그 오류(B7)
- ladogate `TestAuthorizeRestoreRefusesStoppedRuns` — 시간 초과·취소된 태스크의 실행은 거부, 살아 있던 실행·LADO 아닌 세션은 통과(B7)
- ladoqueue `TestUnreadableIssueBacksOff` — 1, 2, 5, 15, 60 분(B8)
- ladoqueue `TestDeletedIssueFailsTheEntry` — 트래커 not found → failed 와 오류 문장(B8)

## 수용 게이트

- [ ] S14-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S14-G1: 디스패처 필수 테스트 9개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladoqueue/ TestFullProjectsAreNotClaimed TestLeaseStartsAtTheClaim TestForeignSessionIsNotAdopted TestOneWorkerPerTask TestSweepStopsUnwantedRuns TestRecoveryKeepsTheLeaseWhenTheStopFails TestDeadlineStopsOnlyBeforeAPR TestUnreadableIssueBacksOff TestDeletedIssueFailsTheEntry
  EXPECT: GO_TESTS_OK 9/9
- [ ] S14-G2: 게이트·복구 훅 테스트 3개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladogate/ TestLadoIDsNeedAGrantEverywhere TestAuthorizeRestoreRefusesStoppedRuns && bash .claude/gate/lado-queue.sh go-tests ./internal/session_manager/ TestRestoreAsksTheLadoGate
  EXPECT: GO_TESTS_OK 1/1
- [ ] S14-G3: 디스패처 패키지가 race 로 통과
  CHECK: bash .claude/gate/lado-queue.sh go-race ./internal/service/ladoqueue/
  EXPECT: GO_RACE_OK
- [ ] S14-G4: LADO 백엔드 패키지 전체
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/service/ladogate/ ./internal/service/ladotask/ ./internal/service/ladopolicy/ ./internal/service/ladoproject/ ./internal/httpd/controllers/ ./internal/daemon/
  EXPECT: GO_PKG_OK
- [ ] S14-G5: LADO store·마이그레이션 테스트와 session_manager·chat 의 LADO 테스트(race)
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ && go test -race -count=1 -run 'Lado|TestRestoreAsksTheLadoGate' ./internal/session_manager/ ./internal/service/chat/ && echo S14_STORE_HOOKS_OK
  EXPECT: S14_STORE_HOOKS_OK
- [ ] S14-G6: sqlc 재생성이 커밋과 같고 쿼리 ASCII, json_each 가 온전
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean && bash .claude/gate/lado-queue.sh queries-ascii && bash .claude/gate/lado-queue.sh has backend/internal/storage/sqlite/gen/lado_queue.sql.go 'json_each'
  EXPECT: HAS_OK
- [ ] S14-G7: API 생성물이 커밋과 같다(enqueuedAt)
  CHECK: bash .claude/gate/lado-queue.sh api-clean && bash .claude/gate/lado-queue.sh has frontend/src/api/schema.ts 'enqueuedAt'
  EXPECT: HAS_OK
- [ ] S14-G8: gitleaks 가 브랜치 커밋 전부에서 깨끗하다(CI 와 같은 방식)
  CHECK: bash .claude/gate/lado-queue.sh gitleaks
  EXPECT: GITLEAKS_OK
- [ ] S14-G9: 업스트림 훅이 작다
  CHECK: bash .claude/gate/lado-queue.sh diff-budget 30 backend/internal/session_manager/manager.go backend/internal/session_manager/agent_switching.go backend/internal/session_manager/interface_transition.go backend/internal/service/chat/service.go backend/internal/daemon/daemon.go backend/internal/httpd/api.go
  EXPECT: BUDGET_OK
- [ ] S14-G10: 개발 플래그 잔재 없음, 안 쓰는 쿼리 삭제
  CHECK: bash .claude/gate/lado-queue.sh none backend/internal/daemon 'AO_LADO_DEV"' && bash .claude/gate/lado-queue.sh none backend/internal/storage/sqlite/queries/lado_queue.sql 'name: ListLadoQueueEntriesForTask ' 'name: GetLadoExecutionByEntry '
  EXPECT: NONE_OK
- [ ] S14-G11: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S14-G12: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `adc84a959`(`docs(lado): plan the step 6 review fixes as a 2-step chain`), 트리 깨끗. umask 0022, go1.27.1, Node 24.21.0. diff-budget 0/30.
- `gitleaks`(CI 방식, main..HEAD 33 커밋) → `leaks found: 2`: `ladoqueue/dispatch_test.go` 의 `ghp_LadoTestToken…`, `ladotask/dispatch_test.go` 의 `ghp_ladoSecretToken…`. 둘 다 `ghp_` 뒤 37자, `[Ll]ado` 뒤 33자 — 새 허용 정규식 `ghp_[Ll]ado[0-9A-Za-z]{32}` 에 맞는다.
- 잔재: `daemon/lado_wiring_test.go:88` 의 `t.Setenv("AO_LADO_DEV", "")`. 안 쓰는 코드 확인 — `ListLadoQueueEntriesForTask`(사용처 없음), `GetLadoExecutionByEntry`(`LadoExecutionByClientRequest` 만, 그것은 store 테스트만), `ListLadoQueueEntries`(store 테스트 도우미 1곳), `SetLadoProjectMaxConcurrent` 쿼리(CDC 테스트 1줄; store 는 Upsert).
- 출발점(코드로 확인): `admit` 은 상한 찬 프로젝트 항목도 회차마다 claim → hold(`next_attempt_at` = 다음 틱). 리스 = 회차 `now` + Lease. 게이트는 LADO 모드 프로젝트에서만 허가를 본다. 스폰 오류 뒤·복구는 id 로 찾은 세션을 확인 없이 연결. 복구의 kill 실패는 로그만. 마감은 PR 과 무관. `RestoreWithMode`·`ResumeAgentWithMode` 에 LADO 훅 없음. 이슈 읽기 실패는 늘 1분, 트래커 404 도 `LADO_ISSUE_READ_FAILED` 하나(`domain.ErrLadoIssueNotFound` 없음).
- AO 쪽: `RequestKill` 은 종료 의도를 저장한 뒤 돌아온다 — kill 이 성공한 세션은 다음 회차의 살아 있는 실행 목록에 없다(정리 감사가 한 번만 쓰이는 근거). 세션 서비스 표시 이름 상한 `maxDisplayNameLen = 100`(`service/session/service.go:27`, 넘으면 `DISPLAY_NAME_TOO_LONG`).
- 필수 테스트 12개 없음.

## After — 수행 후 실측

- 커밋 3개:
  - `1e417070e` `chore(lado): allow the synthetic test tokens in gitleaks` — 전역 allowlist `regexes` 에 주석 + `'''ghp_[Ll]ado[0-9A-Za-z]{32}'''`.
  - `8a163e43d` `fix(lado): review fixes for the queue dispatcher` — 37 파일 +1345 −400.
  - `97bf07477` `fix(lado): keep the issue read backoff across earlier waits` — 독립 리뷰 지적 1(아래).
- B1: 두 쿼리에 `AND project_id NOT IN (SELECT CAST(j.value AS TEXT) FROM json_each(sqlc.arg(full_projects)) AS j)`. `npm run sqlc` 뒤 gen 을 읽어 두 문장이 온전함(`json_each(?2)`·`json_each(?5)`, `FullProjects interface{}`)을 확인. store `ClaimLadoQueueEntries(ctx, now, leaseUntil, limit, fullProjects []domain.ProjectID, newToken)` — 빈 목록·nil 은 `[]`. `admit`: 상한은 회차마다 프로젝트당 한 번(`capOf`); claim 전에 `perProject[p] >= cap(p)` 인 프로젝트를 빼고 `SetLadoQueueWaiting(slot_project, &p)`(같은 이유면 쓰지 않음); 상한을 못 읽은 프로젝트도 그 회차엔 뺀다(문제로 기록); 띄운 워커가 프로젝트를 채우면 더한다; 잡은 뒤 hold 는 경쟁 대비로만(그때도 뺀다). 상한 찬 프로젝트 항목의 `next_attempt_at` 은 이제 비어 있다(시각이 아니라 자리를 기다림).
- B2: claim 마다 `leaseEnd(now)` = 시계(회차 `now` 보다 이르면 `now`) + Lease.
- B3: 게이트 `AuthorizeSpawn` 이 먼저 허가를 읽고, LADO 형식 id 인데 허가가 없으면 프로젝트·모드와 무관하게 `LADO_TASK_REQUIRED`(문장 "Client request ids that start with lado: are LADO's…", details `{projectId, clientRequestId}`). 허가가 있으면 기존 검사가 이어진다(LADO 모드의 자동화 거부 그대로). 디스패처 `startedFor` = 같은 프로젝트 + worker + 해시 일치(빈 해시 아님). 스폰 오류 뒤 어긋나면 못 찾은 것으로 보고 `attemptFailed`. 복구는 기대 해시를 `LadoDispatchGrant` 에서 읽고(Store 인터페이스에 더함), 어긋나면 리스 유효 여부와 무관하게 `attemptFailed("another session holds the attempt's client request id")` — 연결·kill 없음, 오류 없음.
- B4: 검사 1 — 태스크 최신 실행이 살아 있고 승인이 다르면 `recheck(run_live)`(1분). 새 `sweep.go` `stopUnwanted`: 새 쿼리 `ListLiveLadoExecutions`·store `LiveLadoExecutions`(PR 사실 없음) → 실행마다 `LadoTask` → `unwanted`(취소 → `task_cancelled`, 최신 승인이 다름 → `approval_superseded`) → `RequestKill` → 성공하면 `AppendLadoAuditEvent(execution.stopped)`(actor `lado`, subject `execution`/세션 id, payload `{task, session, reason}`); 실패는 로그만, 세션이 살아 있으니 다음 회차에 다시. link 뒤 `stopIfUnwanted` 도 같은 `unwanted`·`stopRun`(감사 포함). Tick 순서: 설정 → recover → stopOverdue → stopUnwanted → admit.
- B5: `stopUnfinished` 가 kill 실패 시 세션을 다시 읽어 끝났으면 놓고, 살아 있거나 못 읽으면 `stop session <id>: <CODE: message>` 를 돌려준다 → 리스 유지, 그 회차 claim 없음.
- B6: `stopOverdue` 가 `exec.PR.URL != ""` 면 건너뛴다(상태 무관, Go 에서 거름). `deadline.go` 머리 주석에 소유자 결정(2026-10-26).
- B7: `ports.WorkGate.AuthorizeRestore`. 게이트 `New` 에 다섯째 인자 `Executions`(`LadoExecution`·`LadoTask`; 데몬은 store). 시간 초과 → `timed_out`, 태스크 취소 → `task_cancelled`, 409 `LADO_EXECUTION_FIXED` details `{sessionId, reason}`(에이전트·인터페이스 고정 거부와 같은 코드라 `reason` 으로 가른다). `manager.go` 두 진입점 첫머리 훅 각 4줄(diff-budget 8/30), `lado_gate.go` 에 `authorizeLadoRestore`. `RestoreAll` 은 그대로. 두 테스트 게이트에 메서드.
- B8: `issueRecheckAfter` = 1, 2, 5, 15, 60분(마지막 반복). 연속 실패 수는 디스패처 메모리(`issueMisses`, 항목별) — 검사 2 직전에 꺼내고(지움) 읽기 실패 때만 +1 로 넣는다. ladotask `issueReadFailed` 는 `github.ErrNotFound`·`gitlab.ErrNotFound` 면 `fmt.Errorf("%w: %w", domain.ErrLadoIssueNotFound, apierr)`(HTTP 는 `errors.As` 라 응답 그대로). 디스패처는 `errors.Is` → `fail("LADO_ISSUE_NOT_FOUND: the issue no longer exists on the tracker")`.
- N: 표시 이름 `displayName` 100 rune. Tick 은 설정 읽기가 실패해도 recover·stopOverdue·stopUnwanted 를 돌리고 admit 만 건너뛴다 — 설정을 모르면 relink 는 하지 않고(마감을 못 정함) 리스를 다음 회차로. 삭제: store `LadoExecutionByClientRequest`, 쿼리 `GetLadoExecutionByEntry`·`ListLadoQueueEntries`·`ListLadoQueueEntriesForTask`·`SetLadoProjectMaxConcurrent`(테스트 도우미 `ladoQueueRows` 는 raw SELECT + `GetLadoQueueEntry`, CDC 테스트의 그 줄 삭제, `TestLadoExecutionByClientRequest` → `TestLadoExecutionReadsTheSessionFacts`). `AO_LADO_DEV` 잔재 삭제. 태스크 DTO `queue.enqueuedAt`(필수) → `npm run api`.
- 테스트: 필수 12개 + `TestTickStopsOverdueRunsWithoutSettings`·`TestDisplayNameFitsAO`·`TestIssueBackoffSurvivesAnEarlierWait`, store claim 테스트에 full 프로젝트 단언, ladotask 에 not-found 2종. 고친 기존 테스트: `TestAdmissionHoldsAtTheCaps/the project cap`(claim 되지 않아 `next_attempt_at` 이 비어 있음), 복구·스폰 오류 테스트의 세션에 worker·해시, claim 호출 6곳에 `nil`. 실제 store 픽스처에 `queueTask`(다른 태스크를 같은 방식으로 승인)·`taskChanges`(change_log 의 `lado_task` 행 수).
- 확인: ladoqueue race PASS(22.9 s), G4 6패키지 PASS, store `-run Lado` + session_manager·chat race PASS, `go build ./... && go vet ./...` PASS, sqlc·api 재생성 깨끗, gitleaks(브랜치 35 커밋) 0.
- 프런트 타입체크(6분 44초)는 예상대로 14건 실패 — 렌더러는 S15 몫: `lib/lado-errors.ts:302` `reasonKeys` 에 `run_live` 없음, `LadoLanes.test.tsx`(126·167·197–204)·`LadoTaskDialog.test.tsx`(424·450·466) 의 queue 고정값에 `enqueuedAt` 없음.
- 독립 리뷰(Sonnet 서브에이전트, 약 2.3달러) 지적 3: (1) 이슈 실패 수를 검사 1 전에 꺼내 run_live·태스크 읽기 재시도가 백오프를 1분으로 되돌림 → 고침(`97bf07477`). (2) kill 은 됐는데 감사 쓰기가 실패하면 세션이 목록에서 빠져 다시 쓰이지 않음 → 기록만(아래 틈). (3) 끌 수 없는 미완 세션 하나가 모든 프로젝트의 claim 을 막음 → B5 엔트리대로 — 기록만.
- 게이트(HEAD `97bf07477`, 깨끗한 트리): `GATES=13 MET=13 UNMET=0`. 이 엔트리 커밋 뒤 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 테스트 3개(설정 없는 회차, 표시 이름, 백오프 보존), 상한을 못 읽은 프로젝트를 그 회차에 빼기, 설정을 모를 때 relink 미루기, `AuthorizeRestore` details 의 `reason`, store `LiveLadoExecutions`·쿼리 `ListLiveLadoExecutions`(정리에 필요). 엔트리와 다르게 한 것: 복구의 남의 세션은 리스가 유효해도 바로 실패한 시도로 센다(그 id 로는 이 시도가 성공할 수 없음).
- B8 의 계산 방식: `waiting_reason`·`next_attempt_at` 으로는 연속 실패 수를 복원할 수 없다 — claim 이 `waiting_reason` 을 비우고, hold 가 시작된 시각은 어디에도 남지 않는다. 열을 더하지 않는 가장 작은 방법으로 디스패처 메모리에 센다. 데몬이 다시 시작하면 1분부터. 대기 중에 대체·취소돼 다시 잡히지 않는 항목의 수는 재시작까지 남는다(항목당 정수 하나).
- 남은 틈(기록만):
  - kill 이 성공한 뒤 `execution.stopped` 감사 쓰기가 실패하면 그 세션은 다음 회차 목록에 없어 감사가 다시 쓰이지 않는다. 마감의 kill → 시간 초과 표시도 S08 부터 같은 꼴(표시가 실패하면 그 실행은 `ended_without_pr` 로 보인다).
  - 끌 수 없는 미완 세션 하나가 복구 오류로 남는 동안 모든 프로젝트의 claim 이 선다(B5 엔트리대로, "리스가 워커를 숨길 수 있음" 규칙). 프로젝트 단위로 막으려면 복구 오류를 프로젝트별로 나눠야 한다.
  - 재승인 뒤 옛 승인의 실행(예: PR 없이 끝난 실행)을 사람이 복구하면 다음 회차 정리가 그 세션을 끈다(`approval_superseded`). `AuthorizeRestore` 는 시간 초과·취소만 거절한다(엔트리 B7 범위).
  - `slot_project` 표시도 `slot_global`·`memory_tight` 처럼 기한 전인 `retry`·`issue_unreadable` 의 이유를 덮는다(다음 시각은 남음).
  - 삭제된 이슈로 실패한 항목의 태스크 `stateReason` 은 `attempts_exhausted` 다(실패 이유를 따로 두지 않음) — 원인은 `queue.error` 에 있다.
- 다음 단계 메모(S15):
  - 타입체크가 지금 14건 깨져 있다(After) — 먼저 `reasonKeys` 에 `run_live` 와 8개 로캘 문구, 렌더러 테스트 고정값에 `enqueuedAt` 을 더한다.
  - 새 대기 이유 `run_live`: 태스크의 다른 승인의 워커가 아직 돈다(1분마다 다시 봄, `nextAttemptAt` 있음). 정리가 그 워커를 끄면 다음 회차에 시작한다.
  - `slot_project` 항목은 이제 `nextAttemptAt` 이 없다(상한 찬 프로젝트는 잡지 않음) — 이유로만 보인다. 상한 찬 프로젝트의 15초 이벤트도 사라졌다.
  - 새 오류 문장: 항목 `error` = `LADO_ISSUE_NOT_FOUND: the issue no longer exists on the tracker`(태스크는 failed, `stateReason` `attempts_exhausted`), 남의 세션 때문에 실패한 시도의 마지막 오류 = `another session holds the attempt's client request id`. 게이트 거부: LADO 형식 id 의 `LADO_TASK_REQUIRED`(details `clientRequestId`), 복구 거부 409 `LADO_EXECUTION_FIXED` details `{sessionId, reason: timed_out | task_cancelled}` — 에이전트·인터페이스 고정 거부(details 에 `reason` 없음)와 같은 코드라 `reason` 으로 문구를 가른다.
  - `queue.enqueuedAt`(RFC 3339, 필수) — 디스패처는 오래된 것부터 잡는다(같은 시각이면 id 순).
  - 시간 상한 규칙: 실행 세션에 PR 이 생기면(상태 무관) 마감이 지나도 끄지 않는다. `deadlineAt` 은 그대로 오므로 패널은 `pr` 가 있으면 "PR 이 열려 시간 상한을 적용하지 않습니다".
  - 감사 `execution.stopped`(actor `lado`, payload `{task, session, reason: task_cancelled | approval_superseded}`).
  - TDD 의 "알려진 틈" 중 고쳐진 것: 상한 찬 프로젝트 claim 폭주(S08), 지워진 이슈 1분 반복(S07), 만료 리스의 미완 세션 kill 실패(S08). 위 남은 틈은 새로 적을 것.
