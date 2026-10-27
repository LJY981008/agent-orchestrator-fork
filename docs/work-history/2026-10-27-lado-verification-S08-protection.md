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

- 시작: 2026-10-27, 브랜치 `lado8/S08`(lane 워크트리 `agent-orchestrator-fork-wt/lado8-S08`), HEAD `31668c4b3`(chore(lado): merge the S02 lane) — 이 lane 의 바탕은 S01·S02 뿐이다. S03~S07 은 다른 lane 에서 돈다.
- 환경: Linux, go1.27.1(체인 스크래치 툴체인), umask 0022, TZ=UTC. G0 `defs-unchanged` → `DEFS_UNCHANGED`.
- 출발점 실측:
  - `ports.LadoRepoClient` = `Tracker` + `CreateIssue`·`Login`·`CheckAccess`·`IssueComments`. 구현은 GitHub·GitLab 어댑터의 `LadoIssues`, 데몬 래퍼 `ladoRepoClient`(`ladoIssueOps` 를 통과), 테스트 가짜 2개(`ladoconnect` `fakeRepoClient`, `ladotask` `fakeTracker`) — 메서드를 더하면 가짜도 맞춰야 한다.
  - GitHub `LadoIssues.send` 는 상태 코드·머리글·본문을 돌려주고(`ladoAnswer`), `CheckAccess` 는 규칙 탐침을 `/rules/branches/` + `(&url.URL{Path: b}).EscapedPath()` 로 보낸다(`/` 는 그대로, `#`·`?`·공백은 % 인코딩). 다음 페이지는 `httpkit.ParseLinkNext` 가 base URL 상대 경로로 돌려준다(다른 호스트로 토큰이 가지 않는다).
  - `service/ladoverify` 패키지는 이 lane 에 아직 없다(S05 가 `git.go` 에 패키지 주석과 함께 만든다) → 이 단계는 `protection.go`·`protection_test.go` 만 더하고 패키지 주석·공용 도우미 이름을 피한다(합칠 때 충돌 방지).
  - domain(S02): `LadoProtectionDetails{Provider, Repo, Branch, Missing, RequiredChecks, Error}`, 코드 `LadoCodeUnprotected`·`LadoCodeProtectionUnknown`·`LadoCodeUnprotectedGitLab`, `LadoIntegrationStatusContext`(`lado/integration`); S02 박제의 `Repo` 는 `acme/demo`(Native).
- 앞 단계 메모 중 이 단계 몫: S02 의 세부 JSON 키·코드 이름을 그대로 쓴다. S01 메모는 저장 층이라 해당 없음.

## After — 수행 후 실측

- 커밋: `8580a6d7b` feat(lado): read branch rules and post the lado/integration status(코드) + 이 엔트리 커밋.
- 바꾼 것:
  - `ports/lado_issues.go`: `LadoRepoClient.BranchRules`·`PostCommitStatus`, `LadoBranchRules`·`LadoBranchRule`·`LadoRequiredCheck`·`LadoCommitStatus`, `ErrLadoUnsupported`, `*LadoHTTPError{Status, Message}`(`the host answered 422: …`), 규칙 종류 상수 `LadoBranchRulePullRequest|RequiredStatusChecks|NonFastForward|Deletion`.
  - `adapters/tracker/github/lado_integration.go`(새): `BranchRules` — `GET /repos/{o}/{r}/rules/branches/{branch}?per_page=100`, 브랜치는 `CheckAccess` 와 같은 `(&url.URL{Path: b}).EscapedPath()`, `Link` 의 `rel="next"` 를 `httpkit.ParseLinkNext`(base URL 상대 경로라 토큰이 다른 호스트로 안 감)로 끝까지, `type` 과 `required_status_checks` 의 `parameters`(그 종류일 때만 디코드)의 `context`·`integration_id` 만 읽는다. `PostCommitStatus` — `POST /repos/{o}/{r}/statuses/{sha}` 에 `{state, target_url(빈 값이면 생략), description, context}`, 201 만 성공. 실패 답은 `fmt.Errorf("github tracker: …: %w", *ports.LadoHTTPError)`.
  - `adapters/tracker/github/lado_issues.go`: `ladoAnswer` 에 보낸 `token` 필드(2줄) — `httpError()` 가 호스트 메시지에서 토큰을 `[redacted]` 로 바꾼다.
  - `adapters/tracker/gitlab/lado_integration.go`(새): 둘 다 요청 없이 `ports.ErrLadoUnsupported`.
  - `daemon/lado_repo_clients.go`: `ladoIssueOps` 에 두 메서드, `ladoRepoClient` 통과 메서드 2개.
  - `service/ladoverify/protection.go`(새, 패키지 주석 없음 — S05 `git.go` 몫): `RepoClients`, `ProtectionRead`, `Protection`·`NewProtection`·`Read`, `RequiredContexts`, `MissingProtection`, `MissingIntegrationCheck`, `EvaluateProtection`, `redactedErrorLine`.
  - 테스트: 필수 5개 + `TestProtectionReadsABranchOnceAtATime`(ladoverify), `TestLadoRepoClientsPassBranchRulesAndStatus`(daemon, 새 파일 `lado_repo_clients_test.go`). 인터페이스가 넓어져 `ladoconnect` `fakeRepoClient`·`ladotask` `fakeTracker` 에 두 메서드(`not used` 오류)를 더했다.
- 엔트리와 다르게(또는 더) 한 것과 이유:
  1. `lado_issues.go`(범위 목록 밖, LADO 파일)에 `ladoAnswer.token` — 오류가 "토큰을 절대 담지 않는다"를 호스트가 머리글을 되돌려 보내는 경우까지 지키려고. 테스트가 422 메시지에 Authorization 값을 되돌려 보내고 오류에 없음을 확인한다(가림을 빼면 실패하는 것 변이로 확인).
  2. 포트에 규칙 종류 상수, `protection.go` 에 `MissingProtection`(공개)·`MissingIntegrationCheck` — 어댑터·판정·S15 보호 경로·화면이 같은 문자열을 쓰게.
  3. "같은 키는 한 번만": 키별 mutex 를 읽는 동안 잡는다 → 기다린 비강제 호출은 캐시를 받고, 강제 호출은 차례로 다시 읽는다(겹치지 않음). 기다린 강제 호출이 앞 읽기를 나눠 받는 singleflight 는 결정적으로 시험할 수 없어 넣지 않았다(강제 읽기는 승인·dry run·Check again·모드 켜기라 드물다).
  4. 실패도 ttl 동안 캐시한다(마지막 읽기 = 캐시). 단 호출자가 취소한 읽기(`ctx.Err() != nil`)는 남기지 않는다 — HTTP 요청 취소가 10분 동안 `protection_unknown` 을 만들지 않게.
  5. 페이지 상한 10(1000 규칙): 넘으면 오류 — 일부 목록은 `pull_request` 같은 규칙을 놓칠 수 있어 답으로 쓰지 않는다(→ `protection_unknown`, 마지막 성공 유지).
  6. 보내기 전 거절: 빈 브랜치, GitHub 이 아닌 저장소(`ErrWrongProvider`), 소문자 hex 40/64 가 아닌 sha(쓰기 경로에 다른 경로가 끼지 않게).
  7. `EvaluateProtection`: GitLab 을 오류보다 먼저 본다(`ErrLadoUnsupported` 가 `protection_unknown` 이 되지 않게). 세부 `Repo` = `repo.Native`(S02 박제와 같음). `Error` = 한 줄·300자 + `…`·토큰 모양(`gh[pousr]_…`, `github_pat_…`, `glpat-…`, `Bearer …`) 가림. `lado/integration` 은 대소문자 무시로 찾는다(GitHub 문맥 비교와 같음).
- 확인한 명령과 결과:
  - `go test -count=1 -v -run 'TestBranchRulesReadsActiveRules|TestPostCommitStatus' ./internal/adapters/tracker/github/` → 하위 테스트 11개 PASS(페이지 상한 포함).
  - `go test -race -count=3 ./internal/service/ladoverify/ ./internal/adapters/tracker/github/ ./internal/adapters/tracker/gitlab/` → ok.
  - `go test -count=1 ./internal/service/ladotask/ ./internal/service/ladoconnect/ ./internal/adapters/tracker/... ./internal/ports/...` → 모두 ok.
  - `lado-verification.sh go-lint ./internal/service/ladoverify/... ./internal/adapters/tracker/github/... ./internal/adapters/tracker/gitlab/... ./internal/ports/...` → `GO_LINT_OK`.
  - 변이: 가림을 빼면 `TestPostCommitStatus/unprocessable` 실패("holds the token"), 읽는 동안 잠금을 풀면 `TestProtectionReadsABranchOnceAtATime` 실패("host reads = 8, want 1") — 둘 다 원복 후 통과.
- 게이트(코드 커밋 전 작업 트리에서): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 2/2`, G2 `GO_TESTS_OK 1/1`, G3 `GO_TESTS_OK 2/2`, G4 `GO_RUN_OK 17`, G5 `GO_BUILD_OK`. 최종 HEAD 에서 `gate-check.sh --run` 으로 다시 기록한다(G6 포함).

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~5 전부, 필수 테스트 5개 + 보조 2개. 언제 읽고 게시할지(S11·S13), 토큰 찾기 배선(S12), 보호 상태 경로(S15)는 손대지 않았다. 실제 GitHub·GitLab 요청 없음(모두 `httptest`·가짜).
- 남은 틈:
  - 규칙이 `lado/integration` 을 `integration_id`(특정 앱)와 함께 요구하면 LADO 가 토큰으로 올린 상태가 그 검사를 채우지 못한다 — 지금은 이름만 보고 보호됨으로 본다. `IntegrationID` 는 포트에 실려 있으니 판정·안내가 필요하면 S13·S19 에서.
  - GitHub Free 비공개 저장소에서 규칙 엔드포인트가 403(업그레이드 안내)을 주는지 빈 목록을 주는지는 실측하지 않았다(실제 GitHub 금지) — 403 이면 `protection_unknown`, 빈 목록이면 `unprotected`, 둘 다 확인. 메인 세션 랩(소유자 승인 뒤)에서 확인할 것.
  - 401 은 `ErrLadoTokenRejected` 를 감싸지 않는다(`*LadoHTTPError{401}` 만).
  - 캐시 항목은 지우지 않는다(저장소·브랜치 수만큼, 프로젝트 수에 묶임).
  - `ladoverify` 패키지 주석은 이 lane 에 없다(S05 `git.go`).
- 다음 단계 메모(이름은 그대로 쓴다):
  - **포트**: `ports.LadoBranchRules{Rules []LadoBranchRule}`, `LadoBranchRule{Type string; RequiredChecks []LadoRequiredCheck}`(필수 검사는 `required_status_checks` 일 때만), `LadoRequiredCheck{Context string; IntegrationID *int64}`, `LadoCommitStatus{State, Context, Description, TargetURL}`; `LadoRepoClient.BranchRules(ctx, repo, branch)`, `.PostCommitStatus(ctx, repo, sha, status)`. 규칙 종류 상수 `ports.LadoBranchRule{PullRequest,RequiredStatusChecks,NonFastForward,Deletion}`. 새 `LadoRepoClient` 가짜는 두 메서드가 필요하다(가짜는 `ports.LadoRepoClient` 를 품고 필요한 것만 덮으면 된다 — `protection_test.go` `protectionClient`).
  - **오류**: GitLab → `ports.ErrLadoUnsupported`(요청 없음). 호스트의 거절 → `errors.As(err, &*ports.LadoHTTPError)`(`Status`, `Message`; 토큰은 `[redacted]`), 문자열 예 `github tracker: post the lado/integration status: the host answered 422: Validation Failed`. 그 밖(전송·디코드·보내기 전 거절)은 일반 오류. `PostCommitStatus` 는 201 만 성공, sha 는 소문자 hex 40/64.
  - **캐시**(S11·S12·S13): `ladoverify.RepoClients{ForRepo(ctx, repo) (ports.LadoRepoClient, error)}` — 토큰 인자 없음, S12 가 데몬 `ladoRepoClients.ForRepo(ctx, repo, token)` + 저장된 토큰으로 맞춘다. `NewProtection(clients, now, 10*time.Minute)`; 루프는 `Read(ctx, repo, branch, false)`, 승인·dry run·Check again·LADO 모드 켜기는 `true`. 키 = `(domain.LadoRepoRef(repo), branch)`(브랜치는 대소문자 그대로). 실패도 ttl 동안 캐시, 호출자 취소는 캐시 안 함; 실패면 `Rules` = 마지막 성공(없으면 빈 것), `Err` 함께, `ReadAt` = 그 시도 시각.
  - **필수 검사(S09·S11)**: `RequiredContexts(read.Rules)` → `(contexts, named)`, `lado/integration`(대소문자 무시) 빼고 규칙 순서·첫 철자로 중복 제거, `named == len(contexts) > 0` → S09 `EvaluateRequiredChecks(required, named, checks)`. 세부 `RulesetRead` 는 `read.Err == nil` 로.
  - **판정**: `EvaluateProtection(read)` — GitLab 먼저 → confirm `unprotected_gitlab`; `Err` → confirm `protection_unknown`(`Error` 한 줄); `MissingProtection(rules)` 가 비면 pass, 아니면 confirm `unprotected`(`Missing` 순서 `pull_request`, `required_status_checks:lado/integration` = `MissingIntegrationCheck`, `non_fast_forward`, `deletion`). 세부 `Repo` = `repo.Native`, `RequiredChecks` = `RequiredContexts`.
  - **S11 상태 게시**: 한 줄 오류(토큰 없음)는 `redactedErrorLine(err)`(한 줄·300자 + `…`·토큰 모양 가림)를 다시 쓰면 된다.
  - **S15 보호 경로**: state pass→`protected`, `unprotected`→`unprotected`, `protection_unknown`→`unknown`, `unprotected_gitlab`→`unsupported`; `checkedAt` = `ReadAt`, `missing`·`requiredChecks`·`error` 는 항목 세부 그대로.
  - **패키지 안 이름**(lane 합칠 때 충돌 주의): `RepoClients`, `ProtectionRead`, `Protection`, `NewProtection`, `MissingIntegrationCheck`, `RequiredContexts`, `MissingProtection`, `EvaluateProtection`, `protectionKey`, `protectionEntry`, `redactedErrorMax`, `redactTokenPattern`, `redactedErrorLine`; 테스트 `protectionRepo`, `protectionRules`, `protectionAllTypes`, `protectionDetails`, `protectionDetailsEqual`, `protectionOrEmpty`, `protectionClock`, `protectionHost`, `protectionClient`.
