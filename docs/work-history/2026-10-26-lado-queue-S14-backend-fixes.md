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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
