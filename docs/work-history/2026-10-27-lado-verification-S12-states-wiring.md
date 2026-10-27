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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 서비스 변수 이름(S13·S15 가 쓸), daemon.go 에서 바꾼 줄, 리뷰 run id 를 얻은 방법)
