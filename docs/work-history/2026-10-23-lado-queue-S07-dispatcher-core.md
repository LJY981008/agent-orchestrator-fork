# 2026-10-23 LADO 대기열·디스패처 — S07 디스패처 핵심: 잡기·검사 6가지·스폰·연결·재시도

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 c·d·e·h·n, §7) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

승인된 태스크를 실제로 워커로 띄우는 디스패처의 핵심을 만든다. TDD §5.3 의 검사 순서를 지키고, 스폰 설정은 스냅숏(에이전트·모드)과 고정된 계획·이슈에서만 나온다. 수용 게이트 15(지원 안 되는 에이전트 → Blocked, 스폰 없음), 17(이슈 수정 → Review required, IssueContext 는 고정 스냅숏), 18(토큰 격리)을 여기서 증명한다.

## 범위

- 만든다: `backend/internal/service/ladoqueue/`(새 패키지) — `service.go`(Deps, New, Start, Tick), `dispatch.go`(검사·스폰·연결·재시도), `prompt.go`(워커 프롬프트), 테스트.
- 하지 않는다: 동시 실행 상한의 프로젝트별 집계·메모리 보류, 재시작 복구, 마감, 베이스 SHA 기록(S08 — 이 단계의 `Tick` 은 전체 상한만으로 잡는다), HTTP·데몬 연결(S09).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 의존성은 이 패키지 안의 작은 인터페이스로 정의한다(플랜 §7): `Store`(S04 primitive + `LadoTask(ctx, id)` + `LadoDispatchSettings`), `Tasks`(`IssueCheck`, `RunInput` — `*ladotask.Service` 가 만족), `Policy`(`CurrentSHA256(ctx, task)`), `Sessions`(`Spawn(ctx, ports.SpawnConfig) (domain.Session, int, int, error)`, `RequestKill`), `Projects`(워커가 시작하는 베이스 브랜치 — `project.Config.WorktreeBaseBranch()` 를 읽는 가장 작은 인터페이스; 워크스페이스 프로젝트에서 AO 가 베이스를 어떻게 고르는지 코드로 확인하고 After 에 적는다), `Clock`, `NewID`, `Log`, `Interval`(기본 15 s), `Lease`(기본 2 min). 자동화 스케줄러(`service/automation/scheduler.go`)의 꼴을 따른다.
2. `Tick(ctx, now)`: (S08 자리: 복구·마감) → 전체 상한에서 살아 있는 실행·리스를 뺀 만큼 `ClaimLadoQueueEntries` → 잡은 항목마다 `dispatch`. 오류는 모아서 돌려준다(`errors.Join`).
3. `dispatch(entry)` 검사 순서(플랜 §7):
   1. `Store.LadoTask` 로 태스크를 읽어 승인이 유효하고 항목의 승인이 태스크의 최신 승인인지 → 아니면 `MarkLadoQueueEntryReviewRequired(plan_superseded)`.
   2. `Tasks.IssueCheck`: 오류 → `HoldLadoQueueEntry(issue_unreadable, now+1m)`; 이슈 done/cancelled → 항목 취소(`CancelLadoQueueEntry(ctx, id, token)` — S04 에 없으니 이 단계에서 store·쿼리에 더하고 store 테스트 한 줄을 붙인다; 태스크는 Closed unmerged); 해시 ≠ 리비전의 `IssueSnapshotSHA256` → review `issue_edited`.
   3. `Policy.CurrentSHA256(task)` ≠ 스냅숏 sha256 → review `rules_changed`. 오류는 hold(`retry`, now+1m).
   4. 리비전 `BaseRef` ≠ 워커가 시작하는 베이스 → blocked `base_ref_mismatch`.
   5. (S08 자리: 상한·메모리)
   6. 스냅숏 `Blocking` → blocked `blocking_snapshot`; 스냅숏 에이전트가 `claude-code`·`codex` 가 아님 → blocked `agent_unsupported`(스냅숏 capability 와 같은 판단 — `ladopolicy.CapabilityOf` 를 써도 된다).
4. 스폰 설정(플랜 §7): `Kind: worker`, `ProjectID`, `Harness`·`AgentConfig.Model` = 스냅숏 agent, `AgentConfig.Permissions` = 스냅숏 permission mode(비거나 `default` 면 스폰하지 않고 blocked `blocking_snapshot` — 상한이 정상이면 생기지 않는 경우), `RequestedMode: chat`, `IssueID: task.IssueRef`, `IssueContext`·계획 = `Tasks.RunInput`, `ClientRequestID: domain.LadoClientRequestID(entry.ID, entry.AttemptCount)`, `ClientRequestHash: domain.LadoDispatchHash(rev.SHA256, snapshot.SHA256)`, `DisplayName: 이슈 제목`, `Branch` 비움(AO 기본 이름 짓기).
5. 프롬프트(`prompt.go`, 영어 — 문장 그대로):
   ```
   You are implementing a plan the developer approved in LADO for <issue ref>: <issue title>.
   Follow the approved plan below and keep to its scope. When you open the pull request, link the issue in its description (for example "Closes #<number>").

   ## Approved plan (revision <n>, sha256 <first 12 hex>)

   <the plan file text, unchanged>
   ```
6. 스폰 뒤: `LinkLadoExecution(entry, token, LadoExecution{SessionID, TaskID, QueueEntryID, PlanRevisionID, PlanApprovalID, PolicySnapshotID, Attempt, StartedAt: now, DeadlineAt: now+maxRunMinutes 또는 0})` — 스폰 뒤의 쓰기는 `context.WithoutCancel(ctx)`. `ErrLadoLeaseLost` 면 로그만(복구가 이어 붙인다 — S08).
7. 스폰 오류: 시도 1·2 실패 → `ReleaseLadoQueueEntry(retry, now+30s / now+2m)`; 시도 3 실패 → `FailLadoQueueEntry(오류 문장)`. 오류 문장은 `apierr` 의 메시지·코드만(토큰·환경 값 금지).
8. 로그·오류·스폰 설정 어디에도 토큰이 들어가지 않는다(게이트 18). 테스트는 가짜 트래커 클라이언트·가짜 Tasks 가 토큰 문자열을 가진 상태에서 스폰 설정 전체(`fmt.Sprintf("%+v", cfg)`)와 캡처한 로그에 그 문자열이 없음을 단언한다.
9. 단위 테스트는 패키지 안의 가짜 Store·Tasks·Policy·Sessions·Projects 로(자동화 스케줄러 테스트 꼴), 시계 주입.
10. 커밋 메시지 예: `feat(lado): dispatcher that spawns approved tasks`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S07-dispatcher-core.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestDispatchSpawnsAnApprovedTask` — 스폰 설정의 모든 필드(Kind, Chat, 에이전트·모델·모드, IssueID, IssueContext=고정 스냅숏, 프롬프트 문장과 계획, client request id·해시), 실행 연결, 마감 = 시작 + 설정 분
- `TestDispatchStopsAtTheFirstFailingCheck` — 검사 1·2(읽기 실패/닫힘/수정)·3·4·6 각각: 맞는 결과(review·blocked·hold·취소)와 스폰 없음, 앞 검사가 실패하면 뒤 검사는 부르지 않음
- `TestIssueEditedAfterApprovalNeedsReview` — 게이트 17: 승인 뒤 본문 수정 → review `issue_edited`, 스폰 없음
- `TestUnsupportedAgentBlocksWithoutSpawning` — 게이트 15: 스냅숏이 차단(지원 안 되는 에이전트) → blocked, 스폰 없음
- `TestDispatchRetriesThenFails` — 30 s, 2 min 뒤 다시, 세 번째 실패 → failed 와 오류 문장
- `TestDispatchKeepsTokensOut` — 게이트 18: 스폰 설정·로그·실패 문장에 토큰 없음
- `TestTickClaimsWithinTheGlobalCap` — 전체 상한만큼만 잡는다(살아 있는 실행·리스 포함)

## 수용 게이트

- [ ] S07-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S07-G1: 필수 테스트 7개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladoqueue/ TestDispatchSpawnsAnApprovedTask TestDispatchStopsAtTheFirstFailingCheck TestIssueEditedAfterApprovalNeedsReview TestUnsupportedAgentBlocksWithoutSpawning TestDispatchRetriesThenFails TestDispatchKeepsTokensOut TestTickClaimsWithinTheGlobalCap
  EXPECT: GO_TESTS_OK 7/7
- [ ] S07-G2: 디스패처 패키지가 race 로 통과
  CHECK: bash .claude/gate/lado-queue.sh go-race ./internal/service/ladoqueue/
  EXPECT: GO_RACE_OK
- [ ] S07-G3: 프롬프트 문장이 엔트리와 같다
  CHECK: bash .claude/gate/lado-queue.sh has backend/internal/service/ladoqueue/prompt.go 'You are implementing a plan the developer approved in LADO for' 'keep to its scope' '## Approved plan (revision'
  EXPECT: HAS_OK
- [ ] S07-G4: 더한 쿼리까지 sqlc 재생성이 커밋과 같고 ASCII
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean && bash .claude/gate/lado-queue.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S07-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S07-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `7f1041f68`(`docs(lado): record S06`), 트리 깨끗. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1, Node 24. `defs-unchanged` → `DEFS_UNCHANGED`.
- 출발점: `backend/internal/service/ladoqueue` 없음. store·쿼리에 항목 하나를 취소하는 primitive 없음(`CancelOpenLadoQueueEntriesForTask` 는 태스크 단위·토큰 없음). 디스패처를 부르는 곳 없음(데몬 배선은 S09).
- 앞 단계 메모 중 이 단계 몫: S01(시각은 UTC, claim 은 `waiting_reason` 을 비우고 `next_attempt_at` 은 남김, hold 는 attempt −1), S04(claim 은 `leaseUntil > now`·빈 토큰 거부, Release `(id, token, next, waiting)` 와 Hold `(id, token, waiting, next)` 의 인자 순서, link 에 의미 있는 필드, link 는 스폰 중 대체·취소를 알려 주지 않으니 link 뒤 `LadoTask` 재읽기, 취소 primitive 는 `writeLadoRow` + `id AND lease_token` 꼴, 잡은 리스도 슬롯으로 셈), S05(client request id·해시 공식, 게이트가 `time.Now()` 로 리스를 보므로 리스 안에서 스폰 시작), S06(`IssueCheck`·`RunInput` 에 검사 1 의 같은 task 값, `IssueCheck` 오류는 apierr 을 감쌈, 스냅숏 `Content` 는 `ladopolicy.SnapshotContent`, 스폰 중 경쟁은 link 뒤 재확인).
- AO 가 워커의 베이스를 고르는 방식(코드로 확인 — 엔트리 절차 1 의 요구):
  - `ProjectConfig.WorktreeBaseBranch()`(`domain/projectconfig.go:197`)는 `DefaultBranch` 를 주되 `auto` 면 `""`. 새 프로젝트의 기본값이 `auto`(`DefaultProjectConfig`).
  - 모든 스폰(동기 `manager.go:1305`, Chat 비동기 `chat_spawn_async.go:168`, 작업 준비 `task_preparation.go:90`)이 `refreshDefaultBranchesBestEffort` 로 저장소마다 워크스페이스 어댑터의 `ResolveDefaultBranch(path, configured)` 를 부른다. 단일 저장소: `(project.Path, WorktreeBaseBranch())`. 워크스페이스 프로젝트: 루트 `(project.Path, WorktreeBaseBranch())` + 자식마다 `(project.Path/child.RelativePath, child.DefaultBranch)`(`needs_init` 자식 제외).
  - gitworktree `ResolveDefaultBranch`(`workspace.go:169`): 빈 값이면 `gitdefault.Resolve`(원격 HEAD 실시간 조회와 그 브랜치 fetch, 안 되면 캐시된 원격 HEAD) → `refs/remotes/<remote>/<branch>`. 이름이 있으면 `<remote>/<branch>`(그 원격이 실제로 있을 때만 원격으로 읽음, 아니면 origin) → `refs/remotes/...` 가 있으면 그것, 없으면 `refs/heads/<branch>`.
  - `createSessionWorkspace` 는 이 해석된 `BaseRef` 로 worktree 를 만든다(워크스페이스 자식은 `BaseBranch: ""` + 자식의 `BaseRef`). 이슈 저장소가 자식인 워크스페이스 프로젝트에서 워커가 그 저장소에서 시작하는 브랜치는 자식의 `DefaultBranch`(등록 때 기록, 비면 그 저장소의 기본)다.
  - 결론: 엔트리 문장대로 `WorktreeBaseBranch()` 만 비교하면 기본 설정(`auto`)의 프로젝트는 늘 `""` 와 비교된다 — After 결정 1.

## After — 수행 후 실측

- 코드 커밋 `c88e6bc94` `feat(lado): dispatcher that spawns approved tasks` — 9 파일, +1899 −5.
  - `ladoqueue/service.go`: 의존 인터페이스 `Store`(`LadoTask`·`LadoDispatchSettings`·`CountLiveLadoExecutions`·claim·release·hold·review·blocked·fail·cancel·link), `Tasks`(`IssueCheck`·`RunInput`), `Policy`(`CurrentSHA256`), `Sessions`(`Spawn`·`RequestKill` — `sessionmanager.KillResult`), `Projects`(`GetProject`·`ListWorkspaceRepos` — store), `Branches`(`ResolveDefaultBranch` — 워크스페이스 어댑터). `Deps` 기본값: Clock `time.Now`, NewID `uuid.NewString`(리스 토큰), Interval 15 s, Lease 2 min. `Start` 는 곧바로 `Tick` 후 Interval 마다, 끝나면 닫히는 채널. `Tick` = 설정 → `CountLiveLadoExecutions`(실행 + 살아 있는 리스) → `free = MaxConcurrent − taken` 이 0 이하면 끝 → `ClaimLadoQueueEntries(now, now+Lease, free)` → 항목마다 `dispatch`; ctx 가 끝났으면 남은 항목은 되돌림(시도 반환). 돌려주는 오류는 실패한 쓰기뿐(`errors.Join`). S08 자리 주석 2곳(슬롯 세기 전 복구·마감, 검사 5).
  - `ladoqueue/dispatch.go`: 검사 1(태스크 읽기 실패·없음 → `retry` 1분 hold; 태스크 취소 → 리스만 놓기; 승인 무효 또는 최신 승인 아님 → review `plan_superseded`), 2(`IssueCheck` 오류 → hold `issue_unreadable` 1분; done·cancelled → `CancelLadoQueueEntry(now)`; 해시 ≠ `Plan.Latest.IssueSnapshotSHA256` → review `issue_edited`), 3(`CurrentSHA256` 오류 → hold `retry` 1분; ≠ `Plan.Snapshot.SHA256` → review `rules_changed`), 4(`workerBase` 오류 → hold `retry` 1분; `namesBase` 거짓 → blocked `base_ref_mismatch`), 5(주석), 6(`RunInput` → `Snapshot.Blocking` → blocked `blocking_snapshot`; `CapabilityOf(agent, permission_mode) ≠ supported` → blocked `agent_unsupported`; 모드 `""`·`default` → blocked `blocking_snapshot`). 스폰 설정은 엔트리 절차 4 그대로(`AgentConfig{Model, Permissions}`, `RequestedMode: chat`, `DisplayName` = 이슈 제목 한 줄, `Branch` 비움). 스폰 뒤 `link`(WithoutCancel): `StartedAt` = 스폰이 돌아온 뒤의 시계, `DeadlineAt` = + `MaxRunMinutes`(0 이면 없음); `ErrLadoLeaseLost` 는 Warn 만, 다른 오류는 Tick 의 오류. 스폰 오류 → `attemptFailed`: 시도 1·2 → release `retry`(+30 s / +2 min), 시도 3 → fail(문장, 시각).
  - `problem(err, fallback)`: apierr 이면 `CODE: message`(URL userinfo 제거, 공백 한 줄, 512 바이트), 아니면 고정 영어 문장. 트래커·규칙·git·스폰·RunInput 오류는 로그와 항목에 이것만 들어간다.
  - `ladoqueue/prompt.go`: 엔트리 문장 그대로. `<number>` 는 `ParseLadoIssueRef` + `LadoIssueNumber`(0 이면 괄호 예시 생략), 제목은 한 줄로, sha 는 앞 12자. AO 가 그 뒤에 `IssueContext`(고정 스냅숏)를 붙인다(`buildTaskPrompt`).
  - store: 쿼리 `CancelLadoQueueEntry`(:execrows, `id AND lease_token`, 리스 비우고 `cancelled_at`), `Store.CancelLadoQueueEntry(ctx, id, token, at)`(시각 0 거부), `TestLadoQueueReleaseHoldAndMarks` 에 cancel 사례(토큰 틀림·소진·상태 `cancelled`·시각 필수). gen 은 `npm run sqlc` 로 재생성해 읽음(ASCII).
  - 테스트: 필수 7개 + `TestTickHandsBackClaimsWhenStopping`(4 사례), `TestDispatchStopsAWorkerItsTaskNoLongerWants`, `TestDispatchLeavesALostClaimToRecovery`, `TestBaseCheckFindsTheBranchAOStartsFrom`(비교 표 14줄 + 설정 브랜치·워크스페이스·자식 없음), `TestStartDispatchesAtOnceAndStops`. `fakes_test.go` 는 자동화 스케줄러 테스트 꼴의 가짜(리스 토큰을 store 처럼 검사)와, 실제 `store.Store`·`ladotask.Service`·`ladopolicy.Service`·`session.Service`·`router.Workspace` 가 인터페이스를 만족하는지 컴파일 단언.
- 엔트리·플랜이 정하지 않았거나 다르게 적은 것:
  1. **베이스는 AO 스폰과 같은 호출로 해석한다.** `Projects` 는 `GetProject`·`ListWorkspaceRepos`(store 가 만족 — S08 의 저장소 경로에도 쓴다), 해석은 새 의존 `Branches`(워크스페이스 어댑터의 `ResolveDefaultBranch`)에 같은 `(dir, configured)` 를 넘긴다(Before 의 마지막 항목). 비교 `namesBase`: `base_ref` 가 해석된 `Branch`·`refs/heads/<Branch>`·`<Remote>/<Branch>`·`refs/remotes/<Remote>/<Branch>`·`BaseRef` 중 하나면 같다. 태그·다른 원격·다른 브랜치는 `base_ref_mismatch`. `auto` 해석은 원격 HEAD 조회와 fetch(네트워크)를 한다 — AO 스폰이 바로 뒤에 같은 일을 하므로 늘어나는 일은 한 번. 30 s 상한, 실패는 `retry` 1분 hold(시도 안 씀). `Branches` 가 nil 이면 모든 항목이 이 검사에서 기다린다(S09 배선).
  2. `CancelLadoQueueEntry` 는 시각 인자를 받는다(`(ctx, id, token, at)` — `FailLadoQueueEntry` 와 같은 꼴, 주입 시계).
  3. 검사 1 에서 태스크가 취소돼 있으면 review 표시 대신 리스만 놓는다: `CancelLadoTask` 가 같은 트랜잭션에서 열린 항목(리스 중 포함)을 이미 취소했다.
  4. **link 뒤 재확인**(S04·S06 메모): 스폰 중에 태스크가 취소되거나 새 승인이 생기면 link 뒤 `LadoTask` 를 다시 읽고 `RequestKill`. 실행 기록은 남는다. kill 오류는 로그만.
  5. `RunInput` 오류와 스냅숏 해독 실패는 시도 실패로 센다(엔트리는 스폰 오류만 적음) — 고정 자료가 상했으면 세 번 뒤 Failed 로 끝나고 끝없이 기다리지 않는다.
  6. 멈추는 중(ctx 끝남): 남은 항목, 검사가 끊긴 항목, 스폰 직전 항목은 대기 이유 없이 되돌린다(hold `""`, 시도 반환). 항목을 정리하는 쓰기는 모두 `WithoutCancel` 이라 리스를 남기지 않는다. 스폰 자체가 끊긴 오류는 시도로 센다.
  7. 재시도·`StartedAt` 시각은 Tick 의 `now` 가 아니라 스폰이 돌아온 뒤의 시계다(스폰이 오래 걸려도 마감·대기가 실제 시각 기준). 검사의 1분 hold 는 Tick 의 `now`.
  8. 오류 문장: AO 는 `WORKSPACE_CREATE_FAILED` 등에 `err.Error()` 를 메시지로 넣고(`toSpawnAPIError`), ladotask 의 `LADO_ISSUE_READ_FAILED` 도 클라이언트 오류 문장을 넣는다 — git 원격 URL 이 들 수 있어 URL 자격 증명을 지운다. store 읽기 오류(로컬 SQLite)는 로그에 그대로 둔다.
- 확인:
  - 필수 7개 PASS, 패키지 race PASS. 변이 23개 모두 테스트가 잡음: 취소 검사·최신 승인·이슈 해시·규칙·베이스·에이전트·`default` 모드·닫힌 이슈 취소·이슈 hold 가 시도를 씀·워크스페이스 자식 브랜치·마감·재시도 간격·시도 상한·`problem` 원문·URL 자격 증명·link 오류 삼킴·link 뒤 재확인·전체 상한·멈춤 되돌림 3곳(Tick·검사·스폰 직전)·RunInput 중 멈춤·Closes 예시.
  - `go test -run 'Lado' ./internal/storage/sqlite/store/` 54개 PASS. golangci-lint v2.13.2(오프라인 빌드 재사용) ladoqueue·store 0 issues, gofmt 없음, `go build ./... && go vet ./...` 통과.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker·should-fix 없음. 검사 순서, 시도 계산, client request id·해시 = 게이트 허가, 베이스 해석이 AO 와 같음, 토큰 격리, 취소 경쟁을 코드로 따라가 확인. nit 1(store 읽기 오류를 `problem` 없이 로그 — 위 결정 8 대로 남김).
- 게이트(HEAD `c88e6bc94`): `GATES=7 MET=7 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: `Branches` 의존(After 1), link 뒤 재확인(4), 멈춤 때 되돌림(6), URL 자격 증명 제거(8), 테스트 5개. 엔트리와 다르게 한 것은 베이스 비교(1)와 취소 primitive 의 시각 인자(2).
- 남은 틈(기록만):
  - 지워진 이슈(404)도 `LADO_ISSUE_READ_FAILED` 라 `issue_unreadable` 로 1분마다 다시 읽는다(엔트리대로 일시·영구 오류를 가르지 않음). 태스크 추적 루프가 그런 이슈를 어떻게 보이는지에 따라 끝없이 기다릴 수 있다.
  - 스폰이 오류를 돌려줬지만 AO 가 세션을 committed 로 남긴 경우를 보지 않는다(자동화 스케줄러는 스폰 오류 뒤 세션을 찾아 연결한다). 다음 시도는 새 id 라 AO 가 막지 않으므로, 앞 세션이 살아 있으면 워커가 둘이 될 수 있다.
  - 재시도 중인 항목에는 오류 문장이 없다(release 는 `error_message` 를 쓰지 않음 — S04 메모). 실패한 항목에만 있다.
- 다음 단계 메모:
  - S08: 자리는 `service.go` 의 Tick 주석(슬롯 세기 전 복구·마감)과 `dispatch.go` 검사 5 주석. 지금 Tick 은 `MaxConcurrent − taken` 만큼 한 번에 잡는다 — 프로젝트 상한·메모리 보류를 넣을 때 S08 절차 1 의 "하나씩 잡기"로 바꾼다. hold 는 `s.hold(ctx, entry, waiting, until)`(시도 반환, WithoutCancel), 멈춤은 `s.handBack`. 베이스 기록: `workerBase` 가 해석한 `ports.WorkspaceDefaultBranch.BaseRef`(예 `refs/remotes/origin/main`)가 AO 가 실제로 worktree 를 만드는 ref 다 — `workerBase` 가 `dir` 도 돌려주게 하면 `RevParse(dir, base.BaseRef)` 로 dispatch SHA 를 얻는다(ladotask 의 `RevParse` 는 `ref`, `origin/<ref>` 순). `link` 의 실행 값에 `DispatchBaseSHA`·`BaseCommitsAhead` 를 채운다. 위 둘째 틈은 recovery 에 더할 `GetSessionByClientRequestID` 를 스폰 오류 뒤에도 부르면 막힌다(committed → link, uncommitted → 다음 시도). Killer 의 `Stop` 은 `Deps.Sessions.RequestKill` 하나면 된다.
  - S09: `ladoqueue.New(Deps{Store: store, Tasks: ladoTasks, Policy: ladoPolicy, Sessions: sessionService, Projects: store, Branches: <session manager 에 넘기는 그 워크스페이스 라우터>, Log})` — `Branches` 를 빠뜨리면 모든 항목이 베이스 검사에서 `retry` 로 기다리며 "no branch resolver is wired" 를 남긴다. `Start` 는 태스크 서비스 뒤, 종료 때 돌려받은 채널을 기다린다. 대기열 DTO 의 항목 오류는 `CODE: message` 꼴 또는 고정 영어 문장.
  - S10·S12: 실패 문장은 위 꼴이다 — 코드로 번역하거나 그대로 보인다.
  - S13: 베이스 비교 규칙(AO 스폰과 같은 해석, `namesBase` 의 다섯 형태), 검사가 못 돌면 1분 대기(시도 안 씀), link 뒤 재확인, 오류 문장 규칙(apierr 코드·메시지, URL 자격 증명 제거)을 TDD §5.3 에 적는다.
