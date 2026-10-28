# 2026-10-27 LADO 검증·통합 승인 — S12 상태와 데몬 배선

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 a·i·l, §12) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

S11 의 루프를 데몬에 붙인다: 실제 store, 프로젝트·세션 읽기, 세션 매니저(워커에게 보내기), AO 리뷰 서비스, 산출물 캡처러, LADO 저장소 클라이언트와 저장된 토큰을 어댑터로 맞추고, 디스패처 뒤에 시작하고 종료 때 기다린다. LADO 모드를 켜면 그 프로젝트 저장소의 보호 상태를 새로 읽는다. 실제 store 를 통한 읽기에서 태스크 상태가 검증 기록대로 나오는지 끝까지 확인한다.

## 범위

- 만든다: `backend/internal/daemon/lado_verify_wiring.go`(+ `lado_verify_wiring_test.go`).
- 바꾼다: `daemon/lado_wiring.go`(`wireLadoWork` 가 검증 서비스를 만들고 돌려줌), `daemon/lado_plans_wiring.go`(`ladoWorkDeps` 에 리뷰 매니저·SCM 읽기), `daemon/daemon.go`(호출부: `reviewSvc` 넘기기, 시작·종료 — 몇 줄), `service/ladoproject/service.go`(LADO 모드를 켤 때 부르는 선택적 콜백 하나, 예: `Deps.OnLadoModeOn func(projectID string)`).
- 하지 않는다: 승인·병합 게이트 설치(S13), 리뷰어 지침 훅(S14), HTTP(S15).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S11 회고의 Deps 목록이 기준이다.
1. 읽기: `daemon/lado_wiring.go`(`wireLadoWork`), `daemon/lado_plans_wiring.go`(`ladoWorkDeps`, `ladoArtifacts`, `ladoMessageSender`), `daemon/lado_repo_clients.go`, `daemon/lado_tokens.go`(저장된 토큰을 찾는 법), `daemon/daemon.go:686` 근처(`wireLadoWork` 호출, `reviewSvc` 가 보이는 곳), 디스패처 시작·종료 줄.
2. 어댑터(`lado_verify_wiring.go`):
   - `Reviews` ← `reviewsvc.Manager`: `Trigger` 는 `TriggerRequested(ctx, worker, TriggerRequest{Source: "manual", PRURL, RejectReviewedHead: true, Harness})`; 만든 run id 는 결과에서(없으면 호출 전후 `ListReviewRunsBySession` 의 차이로); `Runs` 는 store 의 `ListReviewRunsBySession`; `Cancel` 은 매니저의 취소.
   - `Artifacts` ← `ladoArtifacts`(이미 있는 것), `Blobs` ← LADO blob store, `Sender` ← `ladoMessageSender`(세션 매니저), `Sessions` ← store `GetSession`, `Projects` ← 디스패처가 쓰는 프로젝트 읽기(경로·종류·작업 공간 저장소·env), `Plans` ← 실행 개정의 고정 blob 을 읽어 도메인 계획 파서로, `Snapshots` ← store 의 스냅숏 읽기, `Clients` ← `ladoRepoClients` + 호스트의 저장된 토큰(`ladotask` 가 토큰을 얻는 방식 그대로), `Git` ← `ladoverify.ExecGit{}`.
   - 서비스는 `wireLadoWork` 안, 디스패처 뒤에서 만들고 `Start(ctx)`; 종료 때 `Wait()`(디스패처와 같은 자리).
3. LADO 모드 켜기: `ladoproject` 의 새 콜백을 데몬이 `verify.RefreshProtection(projectID)`(백그라운드, 실패는 로그) 로 채운다 — `RefreshProtection` 은 S11 서비스에 더한다: 그 프로젝트의 저장소마다 기준 브랜치 보호를 `force` 로 읽는다.
4. `daemon.go` 편집은 호출부 몇 줄만(diff 예산 G4).
5. 커밋 메시지 예: `feat(lado): start the verification loop with the daemon`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S12-states-wiring.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestWireLadoWorkStartsTheVerifier`(`daemon`) — 실제 store 에 실행·열린 PR 행·`pr_checks` 를 넣고 배선된 서비스의 첫 패스가 그 head 의 기록을 쓴다; 종료가 패스를 기다린다
- `TestTaskStateFollowsVerificationThroughReads`(`service/ladotask`, 실제 store) — 기록 wait → 태스크 목록·태스크·실행 화면 모두 Validating(같은 사유); pass → Integration pending; 승인 행 → Integrating; PR 병합(승인 있음) → Completed; 승인 없는 병합 → Completed `merged_without_approval`
- `TestLadoModeOnRefreshesProtection`(`service/ladoproject`) — 모드를 켜면 콜백이 그 프로젝트 id 로 한 번, 끄거나 이미 켜져 있으면 없음
- 기존 `TestWireLadoInstallsOneGateOnEverySendAndSpawnPath`, `TestWireLadoWorkStartsTheDispatcher` 가 그대로 통과

## 수용 게이트

- [ ] S12-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S12-G1: 배선 테스트와 기존 배선 테스트가 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/daemon/ TestWireLadoWorkStartsTheVerifier TestWireLadoInstallsOneGateOnEverySendAndSpawnPath TestWireLadoWorkStartsTheDispatcher
  EXPECT: GO_TESTS_OK 3/3
- [ ] S12-G2: 읽기를 통한 상태 테스트가 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladotask/ TestTaskStateFollowsVerificationThroughReads
  EXPECT: GO_TESTS_OK 1/1
- [ ] S12-G3: 모드 켜기 콜백 테스트와 프로젝트 설정 패키지 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladoproject/ TestLadoModeOnRefreshesProtection
  EXPECT: GO_TESTS_OK 1/1
- [ ] S12-G4: daemon.go 편집은 작다
  CHECK: bash .claude/gate/lado-verification.sh diff-budget 25 backend/internal/daemon/daemon.go
  EXPECT: BUDGET_OK
- [ ] S12-G5: 데몬의 LADO 테스트 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/daemon/ 'Lado'
  EXPECT: GO_RUN_OK
- [ ] S12-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S12-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `7a0d85bff`(S11 기록 커밋), 브랜치 `feat/lado-verification`, 통합 워크트리. Ubuntu, go1.27.1 linux/amd64.
- 출발점:
  - `wireLadoWork` 는 5개(connections·tasks·rules·queue·setup)를 돌려주고 검증 서비스를 만들지 않음. daemon.go 686-687 호출, 887 `ladoQueue.Start`, 1228 `<-ladoQueueDone`.
  - `ladoverify.Service` 는 `Start`·`Tick`·`Wait`·`Kick` 만; `RefreshProtection` 없음, 저장소의 기준 브랜치를 푸는 의존성 없음.
  - `ladoproject.Service` 는 LADO 모드를 켤 때 아무에게도 알리지 않음. 이 서비스는 `wireLado`(daemon.go:617)에서, 검증 서비스 자리(686)보다 먼저 생김.
  - AO 의 PR 리더 `prReader` 는 daemon.go:847(`newMultiSCMProvider`), `wireLadoWork` 뒤에 생김. `reviewSvc`(`reviewsvc.Manager`)는 582 에서 생겨 686 에서 보임.
- 앞 단계 메모 중 이 단계 몫: S11 회고의 Deps 목록(Store·Tasks·Snapshots·Projects·Sessions ← store, Plans 어댑터, Clients ← `ladoRepoClients` + 저장된 토큰, Reviews ← S09 어댑터, Artifacts ← `ladoArtifacts`, Blobs, Sender ← `Send(ctx, id, msg, nil)`, 나머지 nil, 디스패처 뒤 `Start`, `<-ladoQueueDone` 자리에서 `Wait`); S09 메모(트리거 요청 모양, run id 는 `CreatedRuns`/`Run`); S08 메모(`RepoClients` 는 토큰 인자 없음, 모드 켜기는 `Read(…, true)`); S03 메모(`UpsertLadoVerification`, `ApproveLadoIntegration`, 실행 읽기가 검증 사실을 채움).

## After — 수행 후 실측

- 커밋: `21d570bdd feat(lado): start the verification loop with the daemon`(코드·테스트), 이 엔트리는 그 뒤 커밋.
- 바꾼 것:
  - `daemon/lado_verify_wiring.go`(새): `wireLadoVerify(store, blobs, clients, work, log)` 가 `ladoverify.Deps` 를 채워 `New`. 어댑터 `ladoReviews`(Trigger·Runs·Cancel), `ladoApprovedPlans`(개정 → blob → `ParseLadoPlan`, 영향 경로가 없으면 오류), `ladoVerifyClients`(`ForRepo(ctx, repo, "")` = 호스트의 토큰 사슬, 저장된 토큰 포함 — `ladotask` 와 같음), `ladoVerifySender`. `Branches` ← `work.branches`(세션 매니저). 끝에 `work.ladoModes.SetOnLadoModeOn(verify.RefreshProtection)`.
  - `daemon/lado_wiring.go`: `wireLadoWork` 가 구조체 `ladoWork{connections, tasks, rules, queue, setup, verify}` 를 돌려준다; 검증 서비스는 디스패처 뒤에 만든다.
  - `daemon/lado_plans_wiring.go`: `ladoWorkDeps.reviews reviewsvc.Manager`, `ladoWorkDeps.ladoModes ladoModeHook`.
  - `daemon/daemon.go`(diff-budget 8/25): `lado := wireLadoWork(…, reviews: reviewSvc, ladoModes: ladoProjects}, log)` 와 같은 변수 이름으로 풀기(`ladoConnections, …, ladoVerify := lado.connections, …`), `ladoQueue.Start(ctx)` 바로 뒤 `ladoVerify.Start(ctx)`, 종료 때 `<-ladoSetupDone` 뒤 `ladoVerify.Wait()`.
  - `service/ladoverify/service.go`: 포트 `Branches`·`Deps.Branches`; `Start` 가 루프의 ctx 를 기억하고 루프가 끝날 때 지움; `RefreshProtection(projectID)`(백그라운드, wg 로 추적, 실패는 로그; `Start` 전·정지 뒤는 아무것도 안 함); `ProjectProtection(ctx, projectID, force)`(프로젝트의 저장소마다 기준 브랜치 보호 읽기); `projectRepos`, `baseBranchOf`.
  - `service/ladoproject/service.go`: `SetOnLadoModeOn(func(domain.ProjectID))`(atomic), `setMode` 의 켜기 저장이 성공한 뒤에만 부름.
  - 테스트: `TestWireLadoWorkStartsTheVerifier`·`TestLadoReviewsAskAOAndReturnTheCreatedRuns`(daemon), `TestTaskStateFollowsVerificationThroughReads`(ladotask `verify_state_test.go`), `TestLadoModeOnRefreshesProtection`(ladoproject), `TestRefreshProtectionReadsEachBaseBranchAgain`(ladoverify; 가짜 호스트에 `ruleReads`). 기존 `wireLadoWork` 호출 테스트 5곳은 구조체에서 꺼내게 고침.
- 엔트리와 다르게 한 것과 이유:
  1. 콜백은 `Deps.OnLadoModeOn` 대신 setter `SetOnLadoModeOn`: 프로젝트 설정 서비스가 daemon.go:617 `wireLado` 에서 검증 서비스(686)보다 먼저 생긴다. `SetKiller`·`SetPolicy` 와 같은 늦은 연결이고, 데몬은 `ladoWorkDeps.ladoModes` 로 넘겨 `wireLadoVerify` 안에서 설치한다.
  2. `ladoWorkDeps` 에 SCM 읽기를 넣지 않음: S11 `Deps` 에 라이브 PR 리더가 없어 쓸 곳이 없고, `prReader` 는 daemon.go:847 에서 `wireLadoWork` 뒤에 생긴다. 라이브 head 어댑터는 S13 이 병합 게이트와 함께 단다.
  3. 반환을 6개 튜플 대신 구조체 `ladoWork`: golangci `gocritic tooManyResultsChecker`(결과 5개 초과). daemon.go 는 한 줄로 같은 이름에 풀어 다른 줄을 건드리지 않았다.
  4. `RefreshProtection` 의 기준 브랜치는 디스패처와 같은 리졸버(`Branches` = 세션 매니저 `ResolveDefaultBranch`, 저장소 폴더와 설정된 브랜치)로 푼다; 리졸버가 없으면 프로젝트가 정한 브랜치, 그것도 없으면 오류 읽기. 그 목록을 `ProjectProtection` 으로 공개해 S15 의 보호 경로가 그대로 쓰게 했다.
  5. 리뷰 run id 는 결과에서만 얻는다(`CreatedRuns[i].ID`, 비었고 `Created` 면 `Run.ID`) — 호출 전후 차이는 필요 없었다.
  6. 데몬 테스트는 GitLab 저장소(`BranchRules` 가 요청 없이 `ErrLadoUnsupported`)와 없는 프로젝트 폴더(git 프로세스가 시작되지 않음)로 썼다: G1 이 격리 없는 `go-tests` 이고 네트워크·실제 저장소 금지라서. git 이 읽는 항목은 `verifying` 대기이고, 테스트는 기록의 required_checks(통과한 `unit`)·protection(`unprotected_gitlab`)·review(고정된 계획의 `not_required`)·실행 읽기의 wait 를 본다.
- 확인한 명령과 결과:
  - `bash .claude/gate/lado-verification.sh go-pkg-isolated ./internal/service/ladoverify/ ./internal/service/ladotask/ ./internal/service/ladoproject/` → `GO_PKG_OK`
  - `go-race-isolated ./internal/service/ladoverify/ ./internal/service/ladoproject/` → `GO_RACE_OK`; `go test -race` 로 데몬 새 테스트·디스패처 테스트와 ladotask 새 테스트 → ok
  - `go-run ./internal/daemon/ 'Lado'` → `GO_RUN_OK 19`
  - `go-lint ./internal/daemon/... ./internal/service/ladoverify/... ./internal/service/ladoproject/... ./internal/service/ladotask/...` → 처음 2건(결과 6개, `nilerr`)을 고친 뒤 `GO_LINT_OK`
  - `go-build` → `GO_BUILD_OK`; `diff-budget 25 backend/internal/daemon/daemon.go` → `BUDGET_OK 8/25`
- 게이트: 코드 커밋 `21d570bdd` 에서 `gate-check.sh --run` → G0~G7 전부 MET(`GATES=8 MET=8 UNMET=0`). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 기록.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~5 전부, 필수 테스트 3개 + 보조 2개. 승인·병합 게이트(S13), 리뷰어 지침(S14), HTTP(S15)는 손대지 않았다. 범위 밖: ladoverify 의 `Branches`·`ProjectProtection`(`RefreshProtection` 에 필요), 기존 테스트의 `wireLadoWork` 호출 5곳.
- 남은 틈:
  - `RefreshProtection` 은 리졸버의 기준 브랜치를, 루프는 PR 의 대상 브랜치를 읽는다 — PR 이 다른 브랜치를 겨누면 켜기 새로 읽기가 루프의 캐시 항목을 데우지 못한다(10분 TTL 로 결국 다시 읽음).
  - `Start` 전·정지 뒤의 LADO 모드 켜기는 새로 읽지 않는다 — 데몬은 HTTP 를 `Start` 뒤에 열므로 실제로는 없음.
  - 데몬 배선 테스트는 git 항목을 실제로 계산하지 않는다(격리 없는 게이트) — 그 계산은 ladoverify 의 격리 테스트가 맡는다.
  - 리졸버는 로컬 읽기라 원격 기본 브랜치가 바뀐 직후엔 옛 브랜치를 읽을 수 있다(디스패처와 같은 성질).
- 다음 단계 메모:
  - **변수 이름(daemon.go)**: `ladoVerify`(`*ladoverify.Service`) — 686-688 에서 `lado := wireLadoWork(…)` 뒤 `ladoConnections, ladoTasks, ladoRules, ladoQueue, ladoSetup, ladoVerify := lado.connections, …`. 시작 `ladoVerify.Start(ctx)` 는 889(`ladoQueue.Start` 바로 뒤), 대기 `ladoVerify.Wait()` 는 1232-1233(`<-ladoSetupDone // LADO fork` 뒤 주석 한 줄 + 호출). 테스트에서는 `wireLadoWork(…).verify`.
  - **daemon.go 에서 바꾼 줄**: 686-688(호출·풀기·`reviews: reviewSvc, ladoModes: ladoProjects`), 889, 1232-1233. G4 남은 예산 17줄(같은 25줄 예산이면).
  - **S13**: 병합 게이트와 라이브 head 는 `prReader`(daemon.go:848)·`prActions`(851 근처)가 생긴 뒤라 daemon.go 에 한 줄이 필요하다 — 예 `lado_verify_wiring.go` 에 `installLadoMergeGate(prActions, prReader, ladoVerify)` 를 두고 `prActions` 가 nil 이 아닐 때만. 검증 서비스에 라이브 head 포트를 나중에 넣으려면 setter 가 필요(지금 `Deps` 에 없음). 승인 기록 뒤 `Kick()`.
  - **S15**: 보호 경로는 `ladoVerify.ProjectProtection(ctx, projectID, refresh)` → `[]ladoverify.ProtectionRead`(저장소마다, 리졸버 순서; 기준 브랜치를 못 풀면 `Branch` 빈 값에 `Err` 만) → `EvaluateProtection(read)` 로 상태. 오류는 프로젝트 없음·읽기 실패의 일반 오류라 컨트롤러가 404·500 으로 가려야 한다.
  - **리뷰 run id 를 얻은 방법**: `TriggerRequested` 결과의 `CreatedRuns[i].ID`(비었고 `Created` 면 `Run.ID`); AO 의 거절(`ErrReviewAlreadyRunning`, `ErrHeadAlreadyReviewed`)은 그대로 오류.
  - **LADO 모드 켜기**: `ladoproject.Service.SetOnLadoModeOn` — 켜기 저장이 성공한 뒤 한 번; 이미 켜짐·끄기·거절·저장 실패는 부르지 않는다. 콜백은 막지 않아야 한다(`RefreshProtection` 은 곧바로 돌아온다).
  - **패키지 안 새 이름**(뒤 단계 충돌 주의): daemon `ladoWork`, `wireLadoVerify`, `ladoModeHook`, `ladoReviewRuns`, `ladoReviews`, `ladoPlanRevisions`, `ladoApprovedPlans`, `ladoVerifyClients`, `ladoVerifySender`; 테스트 `ladoVerifyPlan`, `seedLadoVerifyExecution`, `writeLadoVerifyPR`, `ladoModeRecorder`, `ladoReviewManager`. ladoverify `Branches`, `repoPlace`, `projectRepos`, `baseBranchOf`, `baseResolveTimeout`, 필드 `runCtx`; 테스트 `verifyBranches`, `verifyHost.ruleReads`·`branchReads`. ladotask 테스트 `verifyRecord`, `verifyItem`, `stateReasons`, `wantStateReason`, `writeHeadPR`.
