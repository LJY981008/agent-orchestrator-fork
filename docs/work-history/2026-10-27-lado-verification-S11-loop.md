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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — Deps 인터페이스 목록과 메서드, Kick 이 불리는 곳, S12 가 붙일 어댑터)
