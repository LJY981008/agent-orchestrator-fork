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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 오류 코드와 세부 키, S15 가 쓸 메서드, 업스트림 줄 수)
