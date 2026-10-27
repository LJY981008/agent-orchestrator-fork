# 2026-10-27 LADO 검증·통합 승인 — S15 HTTP: 라우트·DTO·명세·생성물

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 e·l·m·o, §15) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

검증·승인을 앱이 쓸 수 있게 `/api/v1/lado/…` 에 내놓는다: 실행 화면에 검증 기록과 승인, 통합 승인(존재 증명), 검사 다시 돌리기와 로그, 저장소 보호 상태, 프로젝트 검사 명령(명령 변경은 존재 증명), 보호 경로 변경(존재 증명). 새 태스크 상태·사유와 오류 코드를 API enum 에 넣고 OpenAPI 와 `schema.ts` 를 다시 만든다.

## 범위

- 만든다: `httpd/controllers/lado_verify.go`, `lado_verify_dto.go`, 테스트.
- 바꾼다: `lado_queue.go`/`lado_queue_dto.go`(실행 응답), `lado_projects.go`/`lado_projects_dto.go`(설정·보호 라우트), `lado_rules.go`/DTO(`paths`), `lado_tasks_dto.go`(상태·사유 enum), `httpd/apispec/specgen/lado_operations.go`, `httpd/api.go`(`APIDeps`·컨트롤러에 검증 서비스 — 업스트림 훅, 작게), `daemon/daemon.go`(`APIDeps` 줄), 생성물(`npm run api`).
- 하지 않는다: 렌더러(S16~), 서비스 로직 바꾸기(필요하면 회고에 남기고 최소만).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `lado_plans.go`(`readLadoPresenceBody`, `presenceCheck`, `decodeLadoBytes` 엄격 디코딩, 증명은 본문 디코딩 전에), `lado_setup.go`(dry run 질의, `needsPresence`), `lado_queue.go`(`getExecution`), `specgen/lado_operations.go` 의 operation 형식과 스키마 이름 등록, `TestRouteSpecParity`(서비스가 nil 이어도 라우트는 걸리고 `apispec.NotImplemented`).
2. 라우트(플랜 §15):
   - `GET /lado/executions/{sessionId}`: `verification`(head, outcome, reason, sha256, items(kind·outcome·code·details), evidence(기준별), computedAt) 과 `integration`(현재 head 의 승인: approvedAt, actor, acknowledged, statusState, statusError) 을 더한다. 둘 다 없으면 생략.
   - `POST /lado/executions/{sessionId}/integration-approve`: 본문 `{prUrl, headSha, verificationSha256, acknowledge, dryRun}`; `dryRun` 이면 증명 없이; 아니면 증명(`integration.approve`, 대상 `<sessionId>@<headSha>`)을 본문 디코딩 전에 확인하고 정확한 본문 바이트를 넘긴다. 응답: 기록·해시·confirm·승인.
   - `POST /lado/executions/{sessionId}/checks/run` → 202(`LADO_EXECUTION_NO_PR` 409).
   - `GET /lado/executions/{sessionId}/checks/{checkRunId}/log` → `text/plain; charset=utf-8`(blob, 1 MiB 이하), 404 `LADO_CHECK_RUN_NOT_FOUND`(다른 실행의 실행 id 도 404).
   - `GET /lado/projects/{projectId}/protection?refresh=` → 저장소마다 `{repository, branch, state(protected|unprotected|unknown|unsupported), missing, requiredChecks, checkedAt, error}`.
   - `GET, PUT /lado/projects/{projectId}/settings`: `checkCommands`, `checkTimeoutMinutes` 추가; PUT 의 증명은 명령이 바뀔 때 서비스가 묻는다(`presenceCheck` 를 넘기는 기존 방식).
   - `PATCH /lado/rules/{ruleId}`: `paths: {globs}`(보호 경로, 천장과 같은 증명 경로).
   - 태스크 DTO 의 `state` enum 에 `validating`, `integration_pending`, `integrating`; `stateReason` enum 에 플랜 §3 의 새 사유 전부.
   - 오류 코드는 서비스가 낸 `apierr` 그대로(플랜 §15 목록) — 명세의 응답 코드 목록에 409·400·404·500·503 을 올바르게.
3. `httpd/api.go` 와 `daemon.go` 는 필드·생성자·`Register` 의 몇 줄만(G6). LAN 차단은 기존 `/api/v1/lado` 접두로 이미 된다.
4. `npm run api` → `openapi.yaml`, `frontend/src/api/schema.ts`. 렌더러 typecheck 는 새 enum 때문에 S16 이 고칠 때까지 빨갛게 남을 수 있다(이 단계 게이트에 typecheck 없음) — 회고에 그 오류 목록을 남긴다.
5. 커밋 메시지 예: `feat(lado): routes for verification, integration approval and project checks`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S15-http.md` 로 전건 MET 를 확인한다.

## 필수 테스트

`httpd/controllers`:

- `TestLadoAPI_IntegrationApproveNeedsPresence` — 증명 없음 403, dry run 은 증명 없이 200
- `TestLadoAPI_IntegrationApprovePassesTheExactBody` — 존재 증명 확인이 경로·정확한 본문 바이트를 받고, 서비스 입력이 본문 그대로
- `TestLadoAPI_ExecutionCarriesVerification` — 실행 응답의 `verification`·`integration` 필드, 없으면 생략
- `TestLadoAPI_RunChecksAgain` — 202, PR 없음 409
- `TestLadoAPI_CheckLog` — 본문·content type, 다른 실행의 id 404
- `TestLadoAPI_ProjectProtection` — 저장소별 상태, `refresh` 전달
- `TestLadoAPI_CheckCommandsNeedPresence` — 명령 변경은 증명 경로, 시간 제한만은 증명 없이, 잘못된 목록 400 세부
- `TestLadoAPI_ProtectedPathsChangeNeedsPresence` — `paths` PATCH 는 증명 필요, 잘못된 경로 400

기존 `TestRouteSpecParity`(`httpd/apispec`), `TestBuild_MatchesEmbedded`(`specgen`) 통과.

## 수용 게이트

- [ ] S15-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S15-G1: 컨트롤러 테스트 8개 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_IntegrationApproveNeedsPresence TestLadoAPI_IntegrationApprovePassesTheExactBody TestLadoAPI_ExecutionCarriesVerification TestLadoAPI_RunChecksAgain TestLadoAPI_CheckLog TestLadoAPI_ProjectProtection TestLadoAPI_CheckCommandsNeedPresence TestLadoAPI_ProtectedPathsChangeNeedsPresence
  EXPECT: GO_TESTS_OK 8/8
- [ ] S15-G2: 라우트와 명세가 맞는다
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/httpd/apispec/ TestRouteSpecParity
  EXPECT: GO_TESTS_OK 1/1
- [ ] S15-G3: 생성된 명세가 내장본과 같다
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/httpd/apispec/specgen/ TestBuild_MatchesEmbedded
  EXPECT: GO_TESTS_OK 1/1
- [ ] S15-G4: `npm run api` 생성물이 커밋과 같다
  CHECK: bash .claude/gate/lado-verification.sh api-clean
  EXPECT: API_CLEAN
- [ ] S15-G5: LADO 컨트롤러 테스트 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/httpd/controllers/ 'TestLado'
  EXPECT: GO_RUN_OK
- [ ] S15-G6: api.go·daemon.go 편집은 작다
  CHECK: bash .claude/gate/lado-verification.sh diff-budget 12 backend/internal/httpd/api.go && bash .claude/gate/lado-verification.sh diff-budget 30 backend/internal/daemon/daemon.go
  EXPECT: BUDGET_OK
- [ ] S15-G7: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S15-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 새 스키마 이름, 렌더러 typecheck 오류 목록(S16 몫), 오류 코드 목록)
