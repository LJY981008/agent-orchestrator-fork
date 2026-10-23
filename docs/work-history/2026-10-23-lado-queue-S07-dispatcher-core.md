# 2026-10-23 LADO 대기열·디스패처 — S07 디스패처 핵심: 잡기·검사 6가지·스폰·연결·재시도

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 c·d·e·h·n, §7) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

승인된 태스크를 실제로 워커로 띄우는 디스패처의 핵심을 만든다. TDD §5.3 의 검사 순서를 지키고, 스폰 설정은 스냅숏(에이전트·모드)과 고정된 계획·이슈에서만 나온다. 수용 게이트 15(지원 안 되는 에이전트 → Blocked, 스폰 없음), 17(이슈 수정 → Review required, IssueContext 는 고정 스냅숏), 18(토큰 격리)을 여기서 증명한다.

## 범위

- 만든다: `backend/internal/service/ladoqueue/`(새 패키지) — `service.go`(Deps, New, Start, Tick), `dispatch.go`(검사·스폰·연결·재시도), `prompt.go`(워커 프롬프트), 테스트.
- 하지 않는다: 동시 실행 상한의 프로젝트별 집계·메모리 보류, 재시작 복구, 마감, 베이스 SHA 기록(S08 — 이 단계의 `Tick` 은 전체 상한만으로 잡는다), HTTP·데몬 연결(S09).

## 절차

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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
