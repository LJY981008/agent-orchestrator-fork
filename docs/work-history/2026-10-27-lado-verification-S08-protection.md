# 2026-10-27 LADO 검증·통합 승인 — S08 원격 보호: 브랜치 규칙·커밋 상태 포트·protection 항목

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 d·i·m, §8) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

TDD §7.1: 기준 브랜치의 GitHub 규칙셋을 읽어 (1) 필수 검사 목록을 얻고 (2) 원격이 보호되는지(PR 필수, `lado/integration` 필수, 강제 푸시·삭제 차단) 판정한다. 통합 승인 뒤에는 그 head 에 `lado/integration` success 상태를 게시한다. 요청은 LADO 의 저장된 토큰으로, 상태 코드를 지키는 LADO 클라이언트(`LadoIssues`)를 거친다. 보호가 안 되면(무료 플랜 비공개 저장소, GitLab) "보호 안 됨: 감지만" 으로 매번 확인을 받는다.

## 범위

- 바꾼다: `ports/lado_issues.go`(`LadoRepoClient` 에 두 메서드와 타입), `daemon/lado_repo_clients.go`(통과 메서드), GitHub·GitLab 어댑터에 새 파일.
- 만든다: `adapters/tracker/github/lado_integration.go`, `adapters/tracker/gitlab/lado_integration.go`, `service/ladoverify/protection.go`, 테스트.
- 하지 않는다: 언제 읽고 게시할지(S11·S13), 토큰 찾기 배선(S12).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `adapters/tracker/github/lado_issues.go`(`send`, `ladoAnswer`, `CheckAccess` 의 규칙 탐침과 브랜치 escape), `daemon/lado_repo_clients.go`(`ladoIssueOps`, `ForRepo`).
2. 포트:
   ```go
   type LadoBranchRule struct {
       Type           string             // "pull_request", "required_status_checks", "non_fast_forward", "deletion", …
       RequiredChecks []LadoRequiredCheck // required_status_checks 일 때만
   }
   type LadoRequiredCheck struct { Context string; IntegrationID *int64 }
   type LadoBranchRules struct { Rules []LadoBranchRule }
   type LadoCommitStatus struct { State, Context, Description, TargetURL string }
   var ErrLadoUnsupported = errors.New("lado: not supported for this provider")
   // LadoRepoClient 에 더한다:
   BranchRules(ctx context.Context, repo domain.TrackerRepo, branch string) (LadoBranchRules, error)
   PostCommitStatus(ctx context.Context, repo domain.TrackerRepo, sha string, status LadoCommitStatus) error
   ```
   오류는 HTTP 상태를 담고(예: `ports.LadoHTTPError{Status int; Message string}`) 토큰을 절대 담지 않는다.
3. GitHub(`lado_integration.go`): `GET /repos/{o}/{r}/rules/branches/{branch}`(브랜치는 `CheckAccess` 와 같은 escape; 페이지가 있으면 `per_page=100` 로 끝까지), 응답 항목의 `type` 과 `parameters.required_status_checks[].context/integration_id` 만 읽는다. `POST /repos/{o}/{r}/statuses/{sha}` 에 `{state, context, description, target_url}`(빈 `target_url` 은 보내지 않는다); 201 성공, 그 밖은 상태 담은 오류. GitLab: 둘 다 `ErrLadoUnsupported`.
4. `protection.go`:
   ```go
   type RepoClients interface {
       ForRepo(ctx context.Context, repo domain.TrackerRepo) (ports.LadoRepoClient, error)
   }
   type ProtectionRead struct {
       Provider domain.TrackerProvider
       Repo     domain.TrackerRepo
       Branch   string
       Rules    ports.LadoBranchRules
       Err      error
       ReadAt   time.Time
   }
   type Protection struct { /* 캐시: (저장소, 브랜치) → 마지막 읽기, 마지막 성공 */ }
   func NewProtection(clients RepoClients, now func() time.Time, ttl time.Duration) *Protection
   func (p *Protection) Read(ctx context.Context, repo domain.TrackerRepo, branch string, force bool) ProtectionRead
   func RequiredContexts(rules ports.LadoBranchRules) (contexts []string, named bool) // lado/integration 제외, 대소문자 무시 중복 제거
   func EvaluateProtection(read ProtectionRead) (domain.LadoVerifyItem, error)
   ```
   - 캐시는 10분(호출자가 ttl 을 준다); `force` 면 다시 읽는다; 읽기 실패면 마지막 성공을 `Rules` 에 두고 `Err` 를 함께(필수 검사는 마지막 성공을 쓰고, protection 항목은 confirm `protection_unknown`). 동시에 같은 키는 한 번만 읽는다(mutex).
   - 판정(플랜 §1 i): GitLab → confirm `unprotected_gitlab`; 오류 → confirm `protection_unknown`(세부 `Error` 는 한 줄, 토큰 없음); `pull_request`, `required_status_checks`+`lado/integration`, `non_fast_forward`, `deletion` 이 모두 → pass; 아니면 confirm `unprotected`, `Missing` 에 빠진 것(`pull_request`, `required_status_checks:lado/integration`, `non_fast_forward`, `deletion`), `RequiredChecks` 에 이름.
5. 커밋 메시지 예: `feat(lado): read branch rules and post the lado/integration status`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S08-protection.md` 로 전건 MET 를 확인한다.

## 필수 테스트

어댑터는 `httptest` 서버로(실제 GitHub 금지):

- `TestBranchRulesReadsActiveRules`(`adapters/tracker/github`) — 경로(`release/1.0` 같은 `/` 브랜치 포함)·인증 머리글, 규칙 종류와 필수 검사 이름·integration_id, 페이지 넘김, 404·403 오류의 상태
- `TestPostCommitStatus`(`adapters/tracker/github`) — POST 경로·본문(JSON 필드)·201 성공, 422 오류, 오류 문자열에 토큰 없음
- `TestGitLabIntegrationIsUnsupported`(`adapters/tracker/gitlab`) — 둘 다 `ErrLadoUnsupported`
- `TestProtectionItem`(`service/ladoverify`) — 보호됨 pass; 빠진 것마다 confirm `unprotected` 와 목록; 오류 → `protection_unknown`; GitLab → `unprotected_gitlab`; `RequiredContexts` 는 `LADO/Integration` 도 뺀다
- `TestProtectionCacheRefreshes`(`service/ladoverify`) — ttl 안에는 한 번 읽음, 지나면 다시, `force` 는 즉시, 실패 뒤에도 마지막 성공 규칙 유지

## 수용 게이트

- [ ] S08-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S08-G1: GitHub 어댑터 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/adapters/tracker/github/ TestBranchRulesReadsActiveRules TestPostCommitStatus
  EXPECT: GO_TESTS_OK 2/2
- [ ] S08-G2: GitLab 어댑터 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/adapters/tracker/gitlab/ TestGitLabIntegrationIsUnsupported
  EXPECT: GO_TESTS_OK 1/1
- [ ] S08-G3: protection 테스트가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestProtectionItem TestProtectionCacheRefreshes
  EXPECT: GO_TESTS_OK 2/2
- [ ] S08-G4: 데몬의 LADO 테스트가 그대로 통과(클라이언트 래퍼)
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/daemon/ 'Lado'
  EXPECT: GO_RUN_OK
- [ ] S08-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S08-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 포트 타입 이름, 오류 타입, 캐시 키)
