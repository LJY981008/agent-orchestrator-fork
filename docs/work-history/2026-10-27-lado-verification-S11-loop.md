# 2026-10-27 LADO 검증·통합 승인 — S11 검증 루프: 기록·해시·수정 요청·상태 재게시·정산

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 a·b·c·e·j·k·l·m·r, §11) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

S05~S10 의 판정 함수를 묶어 데몬 루프로 돌린다: PR 이 열린 실행마다 현재 head 의 항목 8개를 판정하고, 빠진 일(검사 명령, 리뷰, 근거 고정)을 시작하고, head 별 검증 기록을 저장한다. LADO 자신의 항목이 처음 실패하면 워커에게 한 번 알린다(HARNESS §5 `block_and_request_fix`). 게시 못 한 `lado/integration` 상태를 다시 시도하고, 병합·닫힌 PR 은 한 번 정산한다(승인 없이 병합됐으면 감사).

## 범위

- 만든다: `service/ladoverify/service.go`(Deps, New, Start, Tick, Wait, Kick), `record.go`(한 실행의 기록 조립), `notify.go`(수정 요청), `status.go`(상태 재게시 — 게시 자체는 S13 이 쓰는 같은 함수), `settle.go`, 테스트(`service_test.go` — 가짜 의존성).
- 하지 않는다: 데몬 배선(S12), 승인·병합 게이트(S13), HTTP(S15).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `service/ladoqueue/service.go`(`Deps`, `New`, `Start`/`Tick`/`Wait` 패턴, 15 s), `service/ladotask/planwatch.go`.
2. `Deps`(이 패키지 안의 좁은 인터페이스로; 이름은 S12 가 맞춘다):
   - `Store` — S03 의 메서드 중 쓰는 것만.
   - `Tasks`(`GetLadoTask`), `Plans`(실행의 승인된 개정을 파싱한 `domain.LadoPlan`), `Snapshots`(스냅숏 id → 정규 JSON 바이트; `compiler`, `validators`, `agent.harness` 만 읽는다), `Projects`(프로젝트·작업 공간 저장소: 이슈 저장소의 폴더와 작업 공간 안 접두, 프로젝트 env, provider), `Sessions`(세션 레코드: 워크트리 경로 `Metadata.WorkspacePath`, 살아 있는지).
   - `Git`(S05), `Clients`(S08 `RepoClients`), `Reviews`(S09), `Artifacts`·`Blobs`(S10), `Sender`(`Send(ctx, id, message) error`), `Clock`, `NewID`, `Log`, `Env func() []string`(기본 `os.Environ`).
3. `Tick(ctx)`: 시작할 때 한 번 `CancelRunningLadoCheckRuns`(이전 데몬이 남긴 running). 그다음 `ListLadoExecutionsToVerify(ctx, 50)` 각 실행을 독립적으로(하나의 오류는 로그만 남기고 다음으로). 끝에 상태 재게시. `Start` 는 15 s 마다와 `Kick()`(버퍼 1 채널) 때, `Wait` 는 돌던 패스와 실행 중 명령을 기다린다(명령은 ctx 취소로 cancelled).
4. 한 실행(`record.go`, 플랜 §1 a·b):
   - 병합·닫힘 → 정산(`settle.go`): 병합인데 `MergedHeadApproved` 가 아니면 감사 `execution.merged_without_approval`(actor `lado`, `{task, session, pr, head}`)과 함께 `SettleLadoExecution`; 아니면 감사 없이. 끝.
   - 열림: head = `PR.HeadSHA`(비면 건너뜀). 저장소 폴더·작업 공간 접두·플랜·스냅숏 검사기(lado-policy/2 면 없음)·세션을 읽는다.
   - 순서대로: `required_checks`(protection 캐시의 `RequiredContexts` + `ListLadoCommitChecks`), `commands`(설정 + 이 head 의 실행 + `WorktreeAtHead`; 실행 안 된 명령이 있고 워크트리가 준비됐고 실행 칸이 비었으면 첫 명령을 시작: `StartLadoCheckRun` → 고루틴 `RunCommand`(env = `CommandEnv(Env(), 프로젝트 env)`, 시간 = 설정 분) → 로그 blob → `FinishLadoCheckRun` → `Kick()`), `commit_messages`, `paths`(merge-base 는 `refs/remotes/<remote>/<target>` 로, 실패하면 PR `base_sha`), `base_branch`(실행의 `BaseBranch`·`BaseRemote`, 비면 PR 대상 브랜치·`origin`), `protection`(캐시 읽기, force 아님), `review`(동작 실행: Trigger → 만든 run 마다 `AddLadoExecutionReview`(회차 = 기존 수 + 1…); CancelRunID → `Reviews.Cancel` + `MarkLadoExecutionReviewCancelled`), `evidence`(`PinEvidence` → `ResolveEvidence` → `ReplaceLadoEvidenceLinks`).
   - 판정 함수의 오류는 그 항목을 wait(코드 `verifying`)로 두고 로그 — 기록은 늘 8항목.
   - 기록: `domain.LadoVerification` 조립 → 이전 기록의 `Notified` 이어받기 → 수정 요청(아래) → `UpsertLadoVerification`.
5. 수정 요청(`notify.go`, 플랜 §1 r): `commands`(fail), `commit_messages`(fail), `paths`(`protected_path`), `review`(`changes_requested`) 가 fail 이고 `Notified` 에 `<kind>` 가 없으면, 세션이 살아 있을 때 `Sender.Send` 로 한 메시지(영어, ≤ 4 KiB): head 7자, 항목, 무엇이 실패했는지(명령은 이름·종료 코드·로그 끝 1 KiB, 커밋은 SHA 7자·제목, 경로는 목록, 리뷰는 본문 2 KiB — 리뷰 본문은 `Reviews.Runs` 의 run `Body`), 끝에 "Push a fix; LADO checks the new head.". 보내기에 성공하면 `<kind>` 를 `Notified` 에 더한다(실패면 다음 패스에 다시).
6. 상태 재게시(`status.go`): `ListLadoIntegrationStatusRetries` 각 승인을 같은 승인에 대해 5분에 한 번(메모리의 마지막 시도 시각; 재시작하면 바로) `postIntegrationStatus` — S13 이 승인 직후에도 부를 함수: GitHub 면 `PostCommitStatus(success, "lado/integration", "Approved in LADO")` → `posted`·게시 시각, 실패 → `failed`·시도+1·한 줄 오류(토큰 없음); GitLab → `not_applicable`. 12번째 실패 뒤로는 목록에서 빠진다.
7. 커밋 메시지 예: `feat(lado): the verification loop`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S11-loop.md` 로 전건 MET 를 확인한다.

## 필수 테스트

가짜 의존성으로(git 은 임시 저장소 또는 가짜, 실제 네트워크 없음):

- `TestTickBuildsARecordForTheHead` — 8항목 순서, 해시가 패스 사이에 같고, 결과가 같으면 `changed=false`, head 가 바뀌면 새 기록
- `TestTickRunsCommandsOncePerHead` — 명령 2개가 차례로 한 번씩, 실행 칸 하나, 끝나면 Kick, 같은 head 에서 다시 안 돎, 새 head 면 다시
- `TestTickSendsOneFixRequestPerHeadAndItem` — 실패한 명령·커밋·보호 경로·변경 요청마다 한 번, 내용 상한, 끝난 세션엔 안 보냄, 보내기 실패면 다음 패스에 다시
- `TestTickTriggersReviewWhenReady` — 준비되면 Trigger 와 회차 기록, 오래 돈 LADO 실행 취소
- `TestTickRetriesStatusPosting` — 실패한 게시를 5분 뒤 다시, 12회 뒤 멈춤, GitLab 은 `not_applicable`
- `TestTickSettlesMergedWithoutApproval` — 감사 한 번, 정산 뒤 목록에서 빠짐, 승인된 병합은 감사 없음
- `TestTickGoesOnAfterABrokenExecution` — 한 실행의 오류가 다른 실행을 막지 않음
- `TestStartCancelsLeftoverCheckRuns` — 시작 때 running → cancelled, 다시 돈다

## 수용 게이트

- [ ] S11-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S11-G1: 필수 테스트 8개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestTickBuildsARecordForTheHead TestTickRunsCommandsOncePerHead TestTickSendsOneFixRequestPerHeadAndItem TestTickTriggersReviewWhenReady TestTickRetriesStatusPosting TestTickSettlesMergedWithoutApproval TestTickGoesOnAfterABrokenExecution TestStartCancelsLeftoverCheckRuns
  EXPECT: GO_TESTS_OK 8/8
- [ ] S11-G2: 패키지가 -race 로 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-race-isolated ./internal/service/ladoverify/
  EXPECT: GO_RACE_OK
- [ ] S11-G3: 린트(CI 와 같은 golangci-lint)
  CHECK: bash .claude/gate/lado-verification.sh go-lint ./internal/service/ladoverify/...
  EXPECT: GO_LINT_OK
- [ ] S11-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S11-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `c557e0718`(브랜치 `feat/lado-verification`, 트리 깨끗). S01~S10·S14 가 병합돼 있다.
- 환경: Ubuntu, go1.27.1(PATH 의 스크래치 툴체인), umask 022·TZ=UTC·LANG=C.UTF-8 로 맞춰 실행.
- 출발점 실측: `go build ./...` 초록(1.2 s, 캐시); `defs-unchanged` → `DEFS_UNCHANGED`; `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`. `service/ladoverify` 에는 판정 함수(`EvaluateBase`·`EvaluateCommits`·`EvaluatePaths`·`EvaluateRequiredChecks`·`EvaluateCommands`·`EvaluateProtection`·`EvaluateReview`·`ResolveEvidence`)와 `RunCommand`·`WorktreeAtHead`·`Protection`·`PinEvidence` 만 있고 서비스·루프는 없다.
- 출발점 코드: 디스패처 `ladoqueue.Service` 는 `Start(ctx) <-chan struct{}`·`Tick(ctx, now)` 이고 Wait·Kick 은 없다(데몬은 `<-ladoQueueDone` 로 기다림). `domain.LadoExecutionPR` 에는 `base_sha` 가 없다(`Store.GetPR(url)` 의 `domain.PullRequest.BaseSHA` 로 읽어야 함). 스냅숏은 store 가 승인 id 로 읽는다(`LadoPolicySnapshot(ctx, approvalID)`). 데몬의 sender 는 `Send(ctx, id, message, attachment)`.
- 앞 단계 메모 중 이 단계 몫: S03 — store 메서드 시그니처(`UpsertLadoVerification` 의 changed·`Notified` nil 유지, `AddLadoExecutionReview` 회차 = 기존 수 + 1, `SettleLadoExecution` 감사 한 번, `ListLadoIntegrationStatusRetries` 시도 < 12, "5분에 한 번" 은 S11 이 메모리로). S05 — `EvaluateBase` 는 `Branch` 가 비면 오류 → PR target·`origin` 으로 채운다; merge-base 는 `MergeBase(dir, "refs/remotes/<remote>/<target>", head)`. S06 — `EvaluateCommits(cfg, commits, prTitle)`·`EvaluatePaths(PathsInput{PrefixPaths(접두, …), …})`, 수정 요청 세부(`not_korean` 커밋·PR 제목, `Protected`). S07 — `RunCommand` 는 막는다(고루틴), 결과 → `FinishLadoCheckRun`; **첫 명령 뒤 워크트리가 더러워질 수 있으니 그 head 의 실행이 있으면 깨끗함 검사를 건너뛸 것**. S08 — `NewProtection(clients, now, 10*time.Minute)`, 루프는 `Read(…, false)`, 상태 게시 오류는 `redactedErrorLine`. S09 — 필수 검사·명령·리뷰 호출 순서, 트리거한 패스는 wait 그대로, 리뷰 본문은 `Reviews.Runs` 의 `Body`. S10 — `PinEvidence` → `ParseEvidence` → `ResolveEvidence` → 링크마다 ID·시각 채워 `ReplaceLadoEvidenceLinks`, 해시 불일치는 경고 + 파일 없음.

## After — 수행 후 실측

- 커밋: `be1e1049e` feat(lado): the verification loop (코드), 이 엔트리는 뒤 커밋.
- 바꾼 것(모두 `backend/internal/service/ladoverify/`):
  1. `service.go`(새): 좁은 인터페이스 `Store`·`Tasks`·`Plans`·`Snapshots`·`Projects`·`Sessions`·`Sender`, `Deps`, `New`, `Start(ctx)`(곧바로 한 패스, 그 뒤 `Interval`(15 s)마다와 `Kick` 때), `Wait()`(루프와 실행 중 명령), `Kick()`(버퍼 1, 넘치면 버림), `Tick(ctx)`(첫 패스에 `CancelRunningLadoCheckRuns` 한 번 → `ListLadoExecutionsToVerify(50)` 각 실행 → 상태 재게시; 실행별 오류는 세션 id 를 붙여 모아 돌려주고 루프가 로그).
  2. `record.go`(새): `verifyHead(ctx, exec, verifyOptions{forceProtection, startWork})` — 입력 읽기(`headInputs`: 태스크 → 프로젝트·작업 공간 자식 폴더·접두·세션 워크트리·살아 있음·env, 승인된 계획, 스냅숏 validators, 검사 명령·시간, target·base·remote) → 8항목을 고정 순서의 단계 표로 계산(오류 → wait `verifying`) → 이전 기록의 `Notified` 이어받기 → 근거 항목이 바뀌었을 때만 `ReplaceLadoEvidenceLinks` → 수정 요청 → `UpsertLadoVerification`. merge-base 는 `refs/remotes/<remote>/<target>`, 실패하면 `Store.GetPR` 의 `BaseSHA`. 명령은 `CommandsToRun` 의 첫 것을 실행 칸이 비고 워크트리가 준비됐을 때 `StartLadoCheckRun` → 고루틴 `Run`(env = `CommandEnv(Env(), 프로젝트 env)`, 시간 = 설정 분) → 출력 blob → `FinishLadoCheckRun`(`WithoutCancel` + 10 s) → 칸 비움 → `Kick`. 리뷰는 `EvaluateReview` 의 동작대로 Trigger → run 마다 `AddLadoExecutionReview`(회차 = 기존 수 + 1 + i), 또는 `Reviews.Cancel` → `MarkLadoExecutionReviewCancelled`.
  3. `notify.go`(새): `requestFixes` — `commands`(fail)·`commit_messages`(fail)·`paths`(`protected_path`)·`review`(`changes_requested`), `Notified` 에 없고 세션이 살아 있을 때 항목마다 한 메시지, 보내기 성공 때만 `Notified` 에 더함. 형식 `LADO checked head <7자> of your pull request, and the <kind> item failed: <무엇>.` + 빈 줄 + 세부 + 빈 줄 + `Push a fix; LADO checks the new head.`, 전체 ≤ 4 KiB, 로그 끝 ≤ 1 KiB, 리뷰 본문 ≤ 2 KiB(글자 경계에서 자르고 `…`).
  4. `status.go`(새): `retryStatuses`(같은 승인은 메모리의 마지막 시도부터 5분), `postIntegrationStatus(ctx, approval)` — GitHub `success`·`lado/integration`·`Approved in LADO` → `posted`·시도 + 1·게시 시각, 실패 → `failed`·시도 + 1·`redactedErrorLine`, GitLab 또는 `ErrLadoUnsupported` → `not_applicable`(시도 그대로). 저장소 = 승인 → `Store.LadoExecution` → 태스크의 `Repo`.
  5. `settle.go`(새): 병합인데 `MergedHeadApproved` 가 아니면 감사 `execution.merged_without_approval`(actor `lado`, subject `execution`/세션, payload `{task, session, pr, head}`)와 함께, 아니면 감사 없이 `SettleLadoExecution`.
  6. `runner.go`: `WorktreeAtHead` 는 `worktreeAt(…, clean=true)` 를 부른다(+10/−1). 루프는 그 head 의 첫 실행 뒤로 `clean=false`(HEAD 만).
  7. `service_test.go`(새): 가짜 의존성(`verifyStore` 는 S03 규칙대로 changed·`Notified` 유지·회차 순서·한 번 정산·시도 < 12, `verifyReads`, `verifyGit`, `verifyHost`, `verifyReviews`, `verifySender`, `verifyRunner`)과 필수 8개; `var _ Store = (*store.Store)(nil)` 등 다섯 줄로 실제 store 가 인터페이스를 채움을 고정.
- 엔트리와 다르게 한 것과 이유:
  - 인터페이스 이름은 실제 store 메서드에 맞췄다: `Tasks.LadoTask`(엔트리 `GetLadoTask`), `Snapshots.LadoPolicySnapshot(ctx, approvalID)`(store 는 스냅숏을 승인 id 로 읽고 `Content` 가 정규 JSON — 엔트리의 "스냅숏 id → 바이트" 대신), `Projects` 는 디스패처와 같은 `GetProject`·`ListWorkspaceRepos`, `Sessions.GetSession`. S12 는 이 다섯과 `Store` 에 `store` 를 그대로 넘긴다. `Store` 에는 엔트리 목록 밖의 `LadoExecution`(상태 재게시의 저장소 찾기)과 `GetPR`(`LadoExecutionPR` 에 `base_sha` 가 없음)을 더했다.
  - 스냅숏의 `agent.harness` 는 읽지 않는다 — 이 단계에서 쓰는 곳이 없다(validators 만).
  - `Deps.Run`(nil → `RunCommand`)·`Deps.Interval` 을 더했다: 테스트가 명령의 끝을 손으로 정한다(플랜 §11 의 "runner"). `Start` 는 채널을 돌려주지 않고 `Wait()` 로 기다린다(엔트리대로).
  - 워크트리 준비: head 의 첫 실행 전에는 깨끗함 + HEAD(`WorktreeAtHead`), 뒤로는 HEAD 만(S07 메모 — 빌드 산출물이 다음 명령을 영영 막지 않게, 그러나 로컬에서 HEAD 를 옮긴 워크트리에서는 돌지 않게).
  - `commands` 수정 요청은 그 head 의 실행이 하나도 돌지 않고 지금 시작할 명령도 없을 때(실행 칸·워크트리) 보낸다 — head·항목당 한 번이므로 실패한 명령을 모두 담으려고.
  - 입력(태스크·프로젝트·계획·스냅숏·세션·검사 설정·target) 읽기 실패는 그 실행의 오류(기록 안 씀, 다음 실행 계속), 항목 계산 실패만 wait `verifying`. 근거 고정 오류도 `verifying`(엔트리 규칙), 해시 불일치는 경고 + 파일 없음.
  - 같은 근거 항목이면 링크를 다시 쓰지 않는다(S10 메모); `PinEvidence` 는 매 패스 읽는다(새 검사·실행으로 해결이 바뀌므로).
  - 동시성: 패스끼리 `passMu`, 기록 계산끼리 `verifyMu`(S13 승인 재계산과 패스), 실행 칸·첫 취소·상태 시각은 `mu`. 첫 패스의 남은 실행 취소가 성공하기 전에는 명령을 시작하지 않는다.
  - 상태 재게시: 시도 시각을 저장소 찾기 전에 적는다(찾기가 깨져도 5분에 한 번), 게시 중 ctx 가 끝나면 시도로 세지 않는다.
- 확인한 명령과 결과(umask 022, TZ=UTC, LANG=C.UTF-8):
  - `go-tests-isolated ./internal/service/ladoverify/ <필수 8>` → `GO_TESTS_OK 8/8`; `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`
  - `go-race-isolated ./internal/service/ladoverify/` → `GO_RACE_OK`; `go test -race -count=30 -run 'TestTick|TestStart'` → ok(2.6 s)
  - `go-lint ./internal/service/ladoverify/...` → `GO_LINT_OK`; `go-build` → `GO_BUILD_OK`
  - 변이 18건(이미 알린 항목 거르기, 끝난 세션, 명령 정착 대기, 실행 칸, 첫 실행 뒤 깨끗함 검사 양쪽, 재게시 간격 둘, GitLab 분기, 승인된 병합의 감사, 남은 실행 취소, 오류 → verifying, Notified 이어받기, 근거 링크 조건, 회차 번호, 로그 끝 자르기 등) → 모두 실패로 잡힘, 원복 뒤 통과.
  - 리뷰 서브에이전트(전경, sonnet): 도달 가능한 버그 없음. 이론상 한도가 `…`(3 바이트)보다 작으면 `headBytes`·`tailBytes` 가 한도를 넘지만 호출 한도는 모두 1 KiB 이상.
  - 처음 돌렸을 때 테스트가 흔들렸다: `Tick` 이 돌아온 뒤 고루틴이 `Run` 을 부르므로 runner 호출 수를 곧바로 보면 경쟁 → 테스트는 runner 호출을 기다리고, "시작 안 함" 은 store 의 실행 수(동기)로 센다.
- 게이트 결과: 코드 커밋 `be1e1049e` 위에서 G0~G4 를 하나씩 돌려 모두 기대값(`DEFS_UNCHANGED`, `GO_TESTS_OK 8/8`, `GO_RACE_OK`, `GO_LINT_OK`, `GO_BUILD_OK`). G5 와 전건은 이 엔트리를 커밋한 뒤 `gate-check.sh --run` 으로 다시 확인한다.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~7 전부, 필수 테스트 8개. 데몬 배선(S12)·승인·병합 게이트(S13)·HTTP(S15)는 손대지 않았다. 범위 밖 변경은 `runner.go` 의 `worktreeAt` 하나.
- 남은 틈:
  - 수정 요청을 보낸 뒤 `UpsertLadoVerification` 이 실패하면 다음 패스가 같은 요청을 다시 보낸다(중복 가능, 누락은 없음).
  - 명령이 도는 동안 워커가 워크트리를 바꾸면 결과에 섞일 수 있다(시작 때만 본다).
  - "Run again"(S15) 경로가 아직 없다: `CommandsToRun` 은 실행이 없거나 최신 실행이 `cancelled` 인 명령만 다시 돌린다.
  - 작업 공간 프로젝트에서 세션의 최신 PR 이 이슈 저장소가 아닌 다른 자식 저장소에 있으면 git 읽기가 실패해 그 항목들이 계속 `verifying` 이다(플랜 §5 가 이슈 저장소를 정함).
  - GitLab 도 보호 읽기에서 클라이언트를 부른다(`ErrLadoUnsupported` 가 10분 캐시) — 판정은 `unprotected_gitlab` 이라 무해.
  - Windows 실측 없음(가짜 runner 라 테스트는 OS 무관).
- 다음 단계 메모:
  - **Deps(S12)**: `Store`·`Tasks`·`Snapshots`·`Projects`·`Sessions` ← `store`(*store.Store) 그대로(테스트가 고정). `Plans` ← 어댑터 `ApprovedPlan(ctx, revisionID domain.LadoPlanRevisionID) (domain.LadoPlan, error)`: `store.LadoPlanRevision` → blob(`rev.SHA256`) → `domain.ParseLadoPlan(content, domain.LadoPlanExpectations{})`(승인된 개정이라 문제 목록은 버림). `Clients` ← `ladoverify.RepoClients`(S08 메모: 데몬 `ladoRepoClients.ForRepo(ctx, repo, token)` + 저장된 토큰). `Reviews` ← S09 메모의 어댑터. `Artifacts` ← `ladoArtifacts{capturer, store}`, `Blobs` ← LADO blob store, `Sender` ← `Send(ctx, id, msg)` = 세션 매니저 `Send(ctx, id, msg, nil)`. `Git`·`Run`·`Env`·`Clock`·`NewID`·`Interval` 은 nil(기본값), `Log` ← 데몬 로거. `New(deps)` → `Start(ctx)` 를 디스패처 뒤에, 종료 때 ctx 취소 뒤 `Wait()`(`<-ladoQueueDone` 자리). S12 의 `RefreshProtection(projectID)` 은 저장소·기준 브랜치마다 `s.protection.Read(ctx, repo, branch, true)`.
  - **Kick 이 불리는 곳**: 지금은 검사 명령 고루틴이 끝을 기록하고 칸을 비운 뒤 하나. S13 은 승인 기록 뒤에 부른다.
  - **S13**: 다시 계산은 `s.verifyHead(ctx, exec, verifyOptions{forceProtection: true})`(exec 의 `PR.HeadSHA` 를 라이브 head 로; `startWork` 는 false 권장 — 요청 ctx 로 시작한 명령은 요청이 끝나면 취소된다) → 저장된 기록을 돌려준다(`SHA256()`·`ConfirmCodes()`). 입력 읽기 실패는 오류(→ 500 `LADO_VERIFY_FAILED`). `verifyHead` 는 `verifyMu` 를 잡으니 그 안에서 다시 부르지 말 것. 게시는 `s.postIntegrationStatus(ctx, approval)` → 기록된 승인(상태·시도·게시 시각·오류)을 돌려주고 시도 시각을 적어 루프가 5분 뒤에 다시 한다. 승인 행은 `pending`(GitHub)/`not_applicable`(GitLab)로 넣으면 된다.
  - **S15**: 항목 코드에 `verifying`(wait, 모든 항목)을 enum 에 더할 것. "Run again" 은 기존 store 메서드로 가능: 현재 head 의 명령마다 `StartLadoCheckRun` + `FinishLadoCheckRun(cancelled)` 행을 지금 시각으로 넣으면 최신이 `cancelled` 라 루프가 순서대로 다시 돌린다 → `Kick()`(서비스 메서드로 감싸고, 실행 중인 명령은 그대로 둠).
  - **S16·S17 문구**: 수정 요청은 영어 고정 형식(위). 세부 `commandsFailure` 줄: `<이름> exited with <n>.`, `ran longer than its time limit.`, `could not run: <오류>`, `did not run: LADO found no worktree of this session.`
  - **패키지 안 이름**(뒤 단계 충돌 주의): `Service`, `Deps`, `Store`, `Tasks`, `Plans`, `Snapshots`, `Projects`, `Sessions`, `Sender`, `New`, `verifyOptions`, `headInputs`, `validatorSet`, `branchFacts`, `evaluation`, `verifyHead`, `verifyExecution`, `readPlaces`, `readValidators`, `evaluate`, `verifyingItem`, `branch`, `mergeBase`, `commandsItem`, `startCommand`, `freeSlot`, `finishCommand`, `reviewItem`, `reviewAction`, `evidenceItem`, `sameItem`, `requestFixes`, `fixable`, `fixMessage`, `commandsFailure`, `logTail`, `checkRunByID`, `commitsFailure`, `reviewFailure`, `fitFixMessage`, `headBytes`, `tailBytes`, `shortCommit`, `retryStatuses`, `postIntegrationStatus`, `postStatus`, `approvalRepo`, `settle`, `cancelLeftovers`, `noRepoClients`, `worktreeAt`; 상수 `defaultInterval`, `verifyBatch`, `statusBatch`, `protectionTTL`, `finishTimeout`, `fixMessageMax`, `fixLogTailMax`, `fixReviewBodyMax`, `fixFooter`, `cutMark`, `statusRetryAfter`, `integrationStatusState`, `integrationStatusDescription`; 테스트 `verifyFixture`, `newVerifyFixture`, `verifyStore`, `verifyReads`, `verifyGit`, `verifyHost`, `verifyClient`, `verifyPost`, `verifyReviews`, `verifyTrigger`, `verifySender`, `verifyRunner`, `verifyHead1`~`4`, `verifyBase`, `verifyKoreanCommit`, `verifyKey`, `verifyItemOf`, `verifySHA`, `verifySnapshotContent`, `verifyExit`, `between`, `mapsEqual`, `waitUntil`.
