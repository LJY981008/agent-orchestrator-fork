# 2026-10-27 LADO 검증·통합 승인 — S13 통합 승인·상태 게시·병합 게이트

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 h·i·m·n·t, §13) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

MERGE-001 과 PRD 관문 7·10: 사람이 데스크톱 확인으로 정확한 head SHA 의 통합을 승인해야만 병합이 된다. 승인은 사람이 본 검증 기록의 해시와 확인한 항목에 서명하고, 데몬은 서명 직전의 실제 head·보호·기준 브랜치를 다시 읽어 맞을 때만 기록한다. 기록 뒤 GitHub 에 `lado/integration` success 를 게시해 규칙셋이 병합을 풀게 하고, AO 의 병합 경로(데스크톱·모바일 병합 버튼)는 승인 없는 head 를 거절한다.

## 범위

- 만든다: `service/ladoverify/approve.go`, `mergegate.go`, `livehead.go`(포트), 테스트; `backend/internal/ports/lado_merge_gate.go`.
- 바꾼다(업스트림 훅, 작게): `service/pr/action_service.go`(게이트 필드·setter·호출), `httpd/controllers/prs.go`(`writePRError` 맨 앞에서 `*apierr.Error` 를 `envelope.WriteError` 로), 각 테스트 파일; `daemon/lado_verify_wiring.go`(게이트 설치·라이브 head 어댑터) — `daemon.go` 는 필요할 때만 한두 줄.
- 하지 않는다: HTTP 라우트(S15), 데스크톱 채널(S16).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `service/ladotask/plans.go` `ApprovePlan`(존재 증명 뒤 확인, actor `human`/`human:dev-presence`, payload `proof`·`dev`), `service/pr/action_service.go:95-155`, `ports/lado_gate.go`(nil 이면 업스트림 동작인 setter 패턴).
2. 라이브 head(`livehead.go`): `type LiveHeads interface { LiveHead(ctx context.Context, pr domain.PullRequest) (string, error) }` — 데몬 어댑터는 AO 의 SCM 읽기(`FetchPullRequests` 를 쓰는 리더, 병합 경로가 쓰는 것과 같은)로 PR 하나를 읽어 head 를 돌려준다.
3. `approve.go`(플랜 §1 m):
   ```go
   type ApproveInput struct {
       SessionID          domain.SessionID
       PRURL, HeadSHA     string
       VerificationSHA256 string
       Acknowledge        []string
       DryRun             bool
       Presence           func(action, subject string) (domain.LadoPresenceProof, error) // DryRun 이면 부르지 않는다
   }
   type ApproveResult struct {
       Verification domain.LadoVerification
       SHA256       string
       Confirm      []string
       Approval     *domain.LadoIntegrationApproval // DryRun 이면 nil
   }
   func (s *Service) ApproveIntegration(ctx context.Context, in ApproveInput) (ApproveResult, error)
   ```
   - 실제 요청은 존재 증명이 먼저(`integration.approve`, 대상 `<sessionId>@<headSha>`) — 실패면 다른 것을 읽지 않는다. 증명 nonce 가 이미 쓰였으면(`LadoPresenceProofUsed`) 바로 `LADO_PRESENCE_REPLAYED`.
   - 그다음(dry run 도): 실행·PR 읽기(PR 없음 → 409 `LADO_EXECUTION_NO_PR`, 다른 PR URL → 409 `LADO_INTEGRATION_HEAD_CHANGED`), `LiveHead` ≠ 요청 head → 409 `LADO_INTEGRATION_HEAD_CHANGED` `{headSha, currentHeadSha}`(dry run 은 저장된 head 와 다르면 같은 오류), 기준 브랜치 `Fetch`(실패는 기록만), 보호 캐시 `force` 읽기, 그 head 의 기록을 S11 의 조립 함수로 다시 계산·저장.
   - dry run: 기록·해시·`ConfirmCodes()` 를 돌려준다(통과 여부와 상관없이 — 확인 창이 판단).
   - 실제: 해시 ≠ 서명한 해시 → 409 `LADO_VERIFICATION_CHANGED`; 전체 결과 wait·fail → 409 `LADO_INTEGRATION_NOT_READY` `{reasons}`(wait·fail 항목 코드들); `Acknowledge` ≠ 확인 코드(집합) → 400 `LADO_INTEGRATION_ACK_MISMATCH` `{required, given}`; 그 head 승인 있음 → 409 `LADO_INTEGRATION_ALREADY_APPROVED`. 통과하면 `ApproveLadoIntegration`(승인 행: `status_state` = GitHub 면 `pending`, GitLab 이면 `not_applicable`; 감사 `integration.approved` `{task, session, pr, head, verification, acknowledged, proof, dev}`), 그다음 S11 의 `postIntegrationStatus` 를 바로 한 번(실패해도 승인은 남고 응답은 성공 + 상태 `failed`). `Kick()`.
4. `mergegate.go`(플랜 §1 n): `func (s *Service) AuthorizeMerge(ctx context.Context, pr domain.PullRequest, headSHA string) error` — PR 의 세션이 LADO 실행이거나 LADO 모드 프로젝트의 세션이면 그 실행(들)의 해당 head 승인이 있어야 한다: 없음 → `apierr.Conflict("LADO_INTEGRATION_APPROVAL_REQUIRED", …, {prUrl, headSha, sessionId})`; 읽기 오류 → `apierr.Internal("LADO_MERGE_GATE_FAILED", …)`(닫힌 쪽으로 실패). 그 밖의 PR 은 nil.
5. 업스트림 훅:
   - `ports/lado_merge_gate.go`: `type LadoMergeGate interface { AuthorizeMerge(ctx context.Context, pr domain.PullRequest, headSHA string) error }`.
   - `action_service.go`: `ActionService` 에 필드와 `SetMergeGate(g ports.LadoMergeGate)`(nil 이면 지금과 같음, 동시성 안전하게 — 다른 setter 처럼), `Merge` 의 저장된 head 확인 바로 뒤, 라이브 읽기 앞에서 `if gate != nil { if err := gate.AuthorizeMerge(ctx, tracked, expectedHead); err != nil { return MergeResult{}, err } }`.
   - `prs.go` `writePRError` 맨 앞: `var apiErr *apierr.Error; if errors.As(err, &apiErr) { envelope.WriteError(w, r, err); return }`.
   - 데몬: PR 액션 서비스가 만들어진 뒤, 검증 서비스가 생기면 `SetMergeGate(verify)`(배선 파일에서; daemon.go 는 변수 전달 한두 줄).
6. 커밋 메시지 예: `feat(lado): integration approval, the lado/integration status and the merge gate`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S13-approval.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestIntegrationApprovalNeedsPresence` — 증명 없으면 아무것도 읽지·쓰지 않고 거부; dry run 은 증명 없이 기록·해시·확인 코드
- `TestIntegrationApprovalChecksHeadHashAndAcknowledgements` — 라이브 head 다름 409(두 SHA), 해시 다름 409, wait·fail 409(사유), 확인 불일치 400(required·given), 이미 승인 409
- `TestIntegrationApprovalRecordsAndPostsStatus` — 승인 행·감사·`posted`; 게시 실패면 승인은 남고 `failed`·오류 한 줄; GitLab 은 `not_applicable`; 기준 브랜치 fetch 를 먼저 함
- `TestIntegrationApprovalRefusesReplayedProof` — 같은 nonce 두 번째는 `LADO_PRESENCE_REPLAYED`, 쓰기 없음
- `TestApprovalNeedsTheBaseMoveAcknowledged` — 관문 8 뒷부분: `base_moved` 가 있는 기록에서 그 코드 없는 승인은 400, 있으면 통과
- `TestMergeGateNeedsAnApprovalForTheHead` — LADO 실행 PR: 승인 없음 409(코드·세부), 다른 head 승인 409, 그 head 승인 nil; LADO 모드 프로젝트의 다른 세션 PR 409; LADO 아닌 프로젝트 nil; 읽기 오류 500
- `TestMergeAsksTheGate`(`service/pr`) — nil 게이트는 지금과 같음; 거절하면 라이브 읽기·병합 호출이 없음
- `TestPRMergeRendersLadoRefusal`(`httpd/controllers`) — 게이트의 `apierr` 가 409 와 코드·세부로 나가고, 기존 오류 매핑은 그대로

## 수용 게이트

- [ ] S13-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S13-G1: 승인·게이트 테스트 6개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestIntegrationApprovalNeedsPresence TestIntegrationApprovalChecksHeadHashAndAcknowledgements TestIntegrationApprovalRecordsAndPostsStatus TestIntegrationApprovalRefusesReplayedProof TestApprovalNeedsTheBaseMoveAcknowledged TestMergeGateNeedsAnApprovalForTheHead
  EXPECT: GO_TESTS_OK 6/6
- [ ] S13-G2: PR 서비스 훅 테스트와 패키지 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/pr/ TestMergeAsksTheGate
  EXPECT: GO_TESTS_OK 1/1
- [ ] S13-G3: PR 서비스 패키지 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/pr/
  EXPECT: GO_PKG_OK
- [ ] S13-G4: 병합 라우트가 LADO 거절을 그대로 낸다
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/httpd/controllers/ 'TestPRMergeRendersLadoRefusal|TestPR'
  EXPECT: GO_RUN_OK
- [ ] S13-G5: 업스트림 훅은 작다(action_service.go ≤ 20, prs.go ≤ 8)
  CHECK: bash .claude/gate/lado-verification.sh diff-budget 20 backend/internal/service/pr/action_service.go && bash .claude/gate/lado-verification.sh diff-budget 8 backend/internal/httpd/controllers/prs.go
  EXPECT: BUDGET_OK
- [ ] S13-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S13-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `80e690537`(S12 기록 커밋), 브랜치 `feat/lado-verification`, 통합 워크트리. Ubuntu, go1.27.1 linux/amd64. 트리 깨끗함, `defs-unchanged` → `DEFS_UNCHANGED`.
- 출발점:
  - `service/pr/action_service.go` `Merge`: 저장된 PR 확인 → 저장된 head 확인(121-123) → `fetchMergeReadiness`(라이브 읽기) → 병합. 거부할 훅 없음, `ActionService` 에 setter 없음.
  - `httpd/controllers/prs.go` `writePRError`: PR sentinel 6개만 매핑, 그 밖(`*apierr.Error` 포함)은 500 `PR_OPERATION_FAILED`.
  - `ladoverify.Service`: `verifyHead(ctx, exec, verifyOptions{forceProtection, startWork})`, `postIntegrationStatus` 는 있음. 승인·병합 게이트·라이브 head 포트 없음; `Store` 포트에 승인 메서드 없음. 테스트 가짜 `verifyStore.GetPR` 는 늘 없음, `verifyGit.Fetch` 는 아무것도 안 함.
  - daemon.go: `ladoVerify` 688, `prReader` 848, `prActions` 851. 태그 대비 diff: action_service.go 0/20, prs.go 0/8, daemon.go 8(S12).
- 앞 단계 메모 중 이 단계 몫: S11(다시 계산은 `verifyHead(…, {forceProtection: true})`, exec 의 `PR.HeadSHA` 를 라이브 head 로, `startWork` false; 게시는 `postIntegrationStatus`; 승인 행 `pending`/`not_applicable`; 승인 뒤 `Kick()`; `verifyHead` 안에서 다시 부르지 말 것), S12(`lado_verify_wiring.go` 에 `installLadoMergeGate(prActions, prReader, ladoVerify)`, 라이브 head 는 setter), S03(`ApproveLadoIntegration` 의 거절 순서 replayed → already → changed, 다시 계산한 기록을 먼저 저장; `LadoPresenceProofUsed`), S05(`Fetch` 뒤 `EvaluateBase`), S08(`redactedErrorLine`), S02(`ConfirmCodes()`, presence 동작 `integration.approve`).

## After — 수행 후 실측

- 커밋: `9d9982362 feat(lado): integration approval, the lado/integration status and the merge gate`(코드·테스트), 이 엔트리는 그 뒤 커밋.
- 바꾼 것:
  - `ports/lado_merge_gate.go`(새): `LadoMergeGate.AuthorizeMerge(ctx, pr, headSHA)`.
  - `service/pr/lado_merge_gate.go`(새): `(*ActionService).SetMergeGate`(`atomic.Pointer`, nil = 업스트림), `ladoMergeGate.authorize`, `LadoSCMRef(pr)`(병합 경로의 `scmRepoForPR` 로 만든 참조). `action_service.go`(diff 5/20): 필드 `ladoMerge` + 저장된 head 확인 바로 뒤·라이브 읽기 앞의 호출.
  - `httpd/controllers/prs.go`(diff 5/8): `writePRError` 맨 앞에서 `*apierr.Error` 는 `envelope.WriteError`.
  - `service/ladoverify/approve.go`(새): `ApproveInput`, `ApproveResult`, `ApproveIntegration` — 실제 요청은 증명(`integration.approve`, `<session>@<head>`)과 nonce 재사용 확인이 먼저, 그다음 실행·PR(`GetPR` 의 AO 행)·`LiveHead`·기준 브랜치 fetch·보호 강제 읽기·기록 다시 계산과 저장; dry run 은 기록·해시·확인 코드; 실제는 해시 → wait·fail → 확인 코드 → 기존 승인 순으로 거절, 통과하면 `ApproveLadoIntegration`(GitHub `pending`/GitLab `not_applicable`, 감사 `integration.approved`) → GitHub 면 `postIntegrationStatus` 한 번 → `Kick()`.
  - `service/ladoverify/mergegate.go`(새): `AuthorizeMerge` — 실행의 PR 은 그 head·그 PR 의 승인이 있어야, 실행이 아닌 세션은 LADO 모드 프로젝트면 거절, 그 밖은 nil, 읽기 오류는 500.
  - `service/ladoverify/livehead.go`(새): 포트 `LiveHeads`, `SetLiveHeads`.
  - `service/ladoverify/service.go`: `Store` 에 `LadoPresenceProofUsed`·`ApproveLadoIntegration`·`GetLadoIntegrationApproval`·`LadoProjectSettings`, `Service.liveHeads`. `record.go`: `verifyOptions.fetchBase` 와 `fetchBase`(기준 브랜치, 다르면 PR 대상도; 실패는 로그).
  - `daemon/lado_verify_wiring.go`: `installLadoMergeGate(actions, reader, verify)`, 어댑터 `ladoLiveHeads`(`prsvc.LadoSCMRef` → `FetchPullRequests` 한 건 → head). `daemon.go` 한 줄(prActions 블록 바로 뒤), 태그 대비 9.
  - 테스트: ladoverify `approve_test.go`(필수 5)·`mergegate_test.go`(필수 1); pr `lado_merge_gate_test.go`(`TestMergeAsksTheGate`); controllers `prs_lado_test.go`(`TestPRMergeRendersLadoRefusal`); daemon `lado_merge_gate_wiring_test.go`(보조 2: 실제 store 로 AO 병합 409 → 승인 저장 → 병합, 라이브 head 어댑터). `service_test.go` 가짜 확장: `newVerifyFixture(t, adjust...)`, `verifyStore` 의 `proofs`·`ladoModes`, `GetPR` 이 실행의 PR 행을 답함, `verifyGit` 이 저장소별 작업 순서(`opsOf`)를 남기고 `Fetch` 도 기록.
- 엔트리와 다르게 한 것과 이유:
  1. setter·게이트 보관은 `action_service.go` 가 아니라 새 LADO 파일 `service/pr/lado_merge_gate.go` 에: 업스트림 파일은 필드 2줄 + 호출 3줄로 줄고, 다른 LADO setter(`session_manager/lado_gate.go`, `chat/lado_gate.go`)와 같은 자리다.
  2. 라이브 head 어댑터의 SCM 참조는 `prsvc.LadoSCMRef`(새 LADO 파일에서 비공개 `scmRepoForPR` 를 감쌈) — "병합 경로가 쓰는 것과 같은" 참조를 복제 없이.
  3. 라이브 head 포트는 `Deps` 가 아니라 `SetLiveHeads`: `prReader` 가 검증 서비스 뒤에 생긴다(S12 메모). 리더가 없거나 읽기가 실패하면 503 `LADO_VERIFY_UNAVAILABLE`(오류 원문은 로그만).
  4. fetch 는 `verifyHead` 의 옵션 `fetchBase` 로, `headInputs` 뒤·평가 앞에: 저장소 폴더·원격·브랜치를 `headInputs` 가 정한다. 기준 브랜치와 PR 대상이 다르면 둘 다 fetch 한다(merge-base 가 대상을 읽음).
  5. 실행이 없으면 404 `LADO_EXECUTION_NOT_FOUND`(ladoqueue 와 같은 코드), 병합·닫힌 PR 과 AO 행이 없는 PR 도 409 `LADO_EXECUTION_NO_PR`. 다른 PR URL 의 `LADO_INTEGRATION_HEAD_CHANGED` 세부에는 `prUrl`·`currentPrUrl` 도 넣었다.
  6. 요청 head 는 trim·소문자로 맞춘 뒤 증명 subject·비교·저장에 쓴다; 병합 게이트도 소문자로 비교한다.
  7. 병합 게이트는 승인의 PR URL 도 본다(같은 세션·같은 head 의 다른 PR 은 409). AO 에 기록이 없는 세션의 PR 은 프로젝트를 알 수 없어 nil(`pr.session_id` 는 CASCADE FK 라 실제로는 없음). 실행의 PR 은 프로젝트 모드와 상관없이 게이트를 지난다.
  8. GitLab 승인은 이미 `not_applicable` 이라 `postIntegrationStatus` 를 부르지 않는다(쓰기 없음).
  9. "아무것도 읽지 않음" 은 의존성이 nil 인 서비스(읽으면 panic)로 증명하고, 픽스처로 기록·git·호스트·라이브 head 읽기·승인·감사가 없음을 본다. 필수 밖 보조 테스트 2개(데몬)를 더했다 — 가짜만으로는 실제 store 의 승인 트랜잭션과 게이트 읽기가 맞물리는지 못 보므로.
- 확인한 명령과 결과:
  - 게이트 G0~G6(코드 커밋 뒤): `DEFS_UNCHANGED`, `GO_TESTS_OK 6/6`, `GO_TESTS_OK 1/1`, `GO_PKG_OK`, `GO_RUN_OK 12`, `BUDGET_OK 5/20`·`BUDGET_OK 5/8`, `GO_BUILD_OK`.
  - `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`; `go-race-isolated ./internal/service/ladoverify/ ./internal/service/pr/` → `GO_RACE_OK`; `go-lint` (ladoverify·pr·ports·controllers·daemon) → `GO_LINT_OK`.
  - `go test -count=1 ./internal/httpd/...` → 전부 ok(`TestRouteSpecParity` 포함); `go test -count=1 ./internal/daemon/ ./internal/service/ladotask/ ./internal/service/ladoqueue/` → ok.
  - 변이 확인: `fetchBase` 호출을 지우면 `TestIntegrationApprovalRecordsAndPostsStatus` 가 "git operations = [merge-base …], want each request to start with the fetch of origin main" 으로 실패(되돌림).
- 게이트: 코드 커밋에서 G0~G6 MET, G7 은 이 엔트리 커밋 뒤 `gate-check.sh --run` 으로 확인·기록.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~6 전부, 필수 테스트 8개 + 보조 2개(daemon). HTTP 라우트(S15)·데스크톱 채널(S16)·명세는 손대지 않았다. 범위 밖 변경은 기존 ladoverify 테스트 가짜의 확장뿐.
- 남은 틈:
  - 승인이 기록을 다시 계산·저장한 뒤 저장 트랜잭션 전에 루프가 같은 head 의 다른 기록을 저장하면 409 `LADO_VERIFICATION_CHANGED` — 올바른 거절이지만 개발자는 다시 확인해야 한다.
  - fetch(최대 30 s)와 보호 읽기를 `verifyMu` 안에서 하므로 승인 요청 동안 루프 패스가 기다린다.
  - 호스트 오류 문구가 요청마다 바뀌면(요청 id 를 담는 오류 등) `protection_unknown` 세부가 달라져 dry run 과 승인의 해시가 어긋나고 승인이 계속 `LADO_VERIFICATION_CHANGED` 가 될 수 있다. 실측 없음.
  - 실제 요청은 라이브 head 만 비교하고(엔트리대로) 저장된 head 는 dry run 만 비교한다.
  - `LADO_VERIFY_UNAVAILABLE` 는 SCM 제공자 없음과 호스트 읽기 실패를 같은 코드로 낸다(문구만 다름).
  - AO 병합 라우트의 OpenAPI 에 409 `LADO_INTEGRATION_APPROVAL_REQUIRED`·500 `LADO_MERGE_GATE_FAILED` 가 아직 없다(S15). 실제 GitHub 규칙셋 위의 병합 확인은 수동(플랜 §1 Out).
- 다음 단계 메모:
  - **S15 가 쓸 메서드**: `(*ladoverify.Service).ApproveIntegration(ctx, ladoverify.ApproveInput{SessionID, PRURL, HeadSHA, VerificationSHA256, Acknowledge, DryRun, Presence})` → `ladoverify.ApproveResult{Verification domain.LadoVerification, SHA256 string, Confirm []string, Approval *domain.LadoIntegrationApproval}`(dry run 이면 `Approval` nil). `Presence` 는 실제 요청에서 다른 무엇보다 먼저 한 번 불린다(동작 `integration.approve`, subject `<sessionId>@<소문자 headSha>`); dry run 은 부르지 않는다 — 컨트롤러는 dry run 이 아닐 때만 증명 확인 closure 를 넘기면 된다. 오류는 모두 `*apierr.Error` 라 `envelope.WriteError` 그대로.
  - **오류 코드와 세부 키**: 403 `LADO_PRESENCE_REQUIRED`(Presence nil), 증명 확인 오류는 그대로(`LADO_PRESENCE_INVALID`·`EXPIRED`·`UNAVAILABLE`), 403 `LADO_PRESENCE_REPLAYED`; 404 `LADO_EXECUTION_NOT_FOUND`; 409 `LADO_EXECUTION_NO_PR` `{sessionId}`(PR 없음·병합·닫힘); 409 `LADO_INTEGRATION_HEAD_CHANGED` `{headSha, currentHeadSha}`(PR 이 다르면 `prUrl`, `currentPrUrl` 도); 503 `LADO_VERIFY_UNAVAILABLE`; 500 `LADO_VERIFY_FAILED`; 409 `LADO_VERIFICATION_CHANGED`(세부 없음); 409 `LADO_INTEGRATION_NOT_READY` `{reasons}`(wait·fail 항목 코드, 항목 순서, 중복 없음, `verifying` 가능); 400 `LADO_INTEGRATION_ACK_MISMATCH` `{required, given}`(둘 다 정렬·중복 없음·null 아님); 409 `LADO_INTEGRATION_ALREADY_APPROVED`(세부 없음). 병합 라우트: 409 `LADO_INTEGRATION_APPROVAL_REQUIRED` `{prUrl, headSha, sessionId}`, 500 `LADO_MERGE_GATE_FAILED`.
  - **승인 응답에 실을 것**: 승인 id 접두 `lado-integration-`, `Actor` `human`/`human:dev-presence`, `Acknowledged` = 확인 코드, 바로 게시한 뒤의 상태 `posted`(시도 1, 게시 시각) / `failed`(시도 1, 한 줄 오류) / `pending`(게시 결과를 기록하지 못함 — 루프가 다시) / `not_applicable`(GitLab, 시도 0). 감사 `integration.approved`, subject kind `execution`, payload `{task, session, pr, head, verification, acknowledged, proof, dev}`. dry run 도 기록을 저장한다(루프와 같은 행).
  - **S16**: 데스크톱은 dry run 의 `prUrl`·`headSha`·`verificationSha256`·`confirm`(= `acknowledge`) 로 본문에 서명한다. dry run 은 PR 의 저장된 head 와 라이브 head 가 모두 요청과 같아야 통과한다.
  - **업스트림 줄 수**(태그 대비): `action_service.go` 5/20, `prs.go` 5/8, `daemon.go` 9(S12 8 + 1).
  - **배선**: `ladoverify.Service` 가 `ports.LadoMergeGate`; 데몬 `installLadoMergeGate(prActions, prReader, ladoVerify)`; `prsvc.(*ActionService).SetMergeGate`, `prsvc.LadoSCMRef`.
  - **패키지 안 새 이름**(뒤 단계 충돌 주의): ladoverify `ApproveInput`, `ApproveResult`, `ApproveIntegration`, `integrationPresence`, `integrationExecution`, `approvable`, `recordApproval`, `notReadyReasons`, `codeSet`, `noOpenPR`, `headChanged`, `verificationChanged`, `alreadyApproved`, `verifyFailed`, `AuthorizeMerge`, `inLadoProject`, `approvalRequired`, `mergeGateFailed`, `LiveHeads`, `SetLiveHeads`, `liveHeadReader`, `fetchBase`; 테스트 `approveFixture`, `newApproveFixture`, `approveHeads`, `presenceProof`, `requireAPIError`, `requireDetails`, `proofBlindStore`, `failingGateStore`, `failingSessions`, `errGateRead`, `verifyStore` 의 `setLadoMode`·`approvalCount`, `verifyReads.addSession`, `verifyGit.opsOf`. daemon `ladoMergeGated`, `installLadoMergeGate`, `ladoPRReader`, `ladoLiveHeads`, 테스트 `ladoSCM`. pr `ladoMergeGate`, `LadoSCMRef`, 테스트 `fakeLadoMergeGate`, `countingSCMAction`.
  - **S19(문서)**: 다르게 한 것 3·5·7·8(503 코드, 병합·닫힌 PR, 게이트의 PR URL 확인·기록 없는 세션, GitLab 게시 없음)과 dry run 의 저장된 head 비교를 TDD §7·§13 에 적을 것.
