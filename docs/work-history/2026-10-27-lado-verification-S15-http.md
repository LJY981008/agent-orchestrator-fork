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

- 시작 HEAD `078c3e237`(브랜치 `feat/lado-verification`, S13 엔트리 커밋 뒤; S14 는 레인에서 이미 병합 `2226bbfeb`), 트리 깨끗. Ubuntu, go1.27.1, node v24.21.0, 체인 무인 세션(lane-S15).
- 출발점 실측:
  - `GET /lado/executions/{sessionId}`: `LadoExecutionResponse` 에 검증·승인 없음, `LadoExecutionPRResponse` 는 url·number·merged·closed 뿐(store 는 head·대상·제목을 이미 채움).
  - 통합 승인·검사 다시 돌리기·로그·보호 상태 라우트 없음. ladoverify 에는 `ApproveIntegration`, `ProjectProtection`(프로젝트 없음도 일반 오류), `EvaluateProtection` 이 있고, head 의 기록·승인 읽기, 로그 읽기, 다시 돌리기는 없음. `Store` 인터페이스에 `GetLadoCheckRun` 없음.
  - 설정 DTO 에 `checkCommands`·`checkTimeoutMinutes` 없음(`ladoproject.UpdateInput` 은 이미 받음, 본문 한도 4 KiB). 규칙 PATCH 는 `paths` 를 읽지 않음(`ladopolicy.UpdateInput.Paths` 는 있음).
  - 태스크 DTO enum 에 `validating`·`integration_pending`·`integrating`·새 사유 없음. 병합 라우트 명세에 500 없음.
  - diff-budget: `api.go` 0/12, `daemon.go` 9/30.
- 앞 단계 메모 중 이 단계 몫: S02(상태·사유 enum 문자열), S04(`paths` DTO, 규칙 응답의 globs, 400 `LADO_PROTECTED_PATHS_INVALID`; `validators` 스키마는 스냅숏 내용을 내보내는 응답이 있을 때만), S07(설정 DTO 와 오류 세부), S08(보호 상태 매핑), S09(명령 세부의 "아직 안 돎" = `checkRunId` 빈 값), S10(근거 ref 표시), S11(코드 `verifying`, 다시 돌리기 = 취소된 실행 행 + `Kick`), S12(`ProjectProtection` → `EvaluateProtection`, 404·500 가르기), S13(`ApproveIntegration` 입력·결과·오류 코드, 병합 라우트 409·500), S14(`LadoSetupConflictResponse.details` 설명에 `commands`).

## After — 수행 후 실측

- 커밋: `8c2de3ec6` feat(lado): routes for verification, integration approval and project checks · 이 엔트리 커밋.
- 바꾼 것:
  - 새 `controllers/lado_verify.go`: `LadoVerifyService`(`Head`, `ApproveIntegration`, `RunChecksAgain`, `CheckLog`, `Protection`), `registerVerify`(라우트 4개). `approveIntegration` 은 원시 본문(4 KiB)을 읽고 엄격 디코딩한 뒤 dry run 이 아니면 그 바이트로 만든 `presenceCheck(r, body)` 를 넘긴다. `runChecks` 202, `getCheckLog` 는 `text/plain; charset=utf-8` + `nosniff`, `getProjectProtection` 은 `refresh` bool 질의(아니면 400 `INVALID_QUERY`). 매핑: `ladoVerificationResponse`(세부 JSON 을 typed union 으로 디코드, `evidence` 는 evidence 항목의 criteria), `ladoIntegrationApprovalResponse`, `ladoBranchProtectionResponse`(`EvaluateProtection` 의 코드 → 상태, 세부의 redacted 오류).
  - 새 `controllers/lado_verify_dto.go`: 스키마 18개(설정·규칙 DTO 의 2개와 함께 새 이름 20개, 회고의 목록).
  - `lado_queue.go`·`_dto.go`: 실행 응답에 `verification`·`integration`(PR 의 현재 head 것, 없으면 생략; `Verify` 가 nil 이면 둘 다 없음), PR 에 `headSha`·`targetBranch`·`title`(omitempty).
  - `lado_projects.go`·`_dto.go`: `LadoController.Verify`, `Register` 가 `registerVerify` 를 부름, 설정 GET/PUT 에 `checkCommands`(`LadoCheckCommandBody{name, command}`)·`checkTimeoutMinutes`, PUT 본문 한도 128 KiB(`maxLadoSettingsBody`, 명령 10개 × 2000자를 이스케이프해도 들어가게). 증명은 기존대로 closure 로 넘겨 서비스가 명령이 바뀔 때만 부른다.
  - `lado_rules.go`·`_dto.go`: `LadoRulePaths{globs}`, `UpdateLadoRuleRequest.paths` → `in.Paths`, 규칙·버전 응답의 `paths`(paths.protected 만).
  - `lado_tasks_dto.go`: state enum +3, stateReason enum +14, 설명 갱신. `lado_setup_dto.go`: conflict `details` 설명에 `commands`.
  - specgen `lado_operations.go`: operation 4개(`approveLadoIntegration`, `runLadoChecks`, `getLadoCheckLog` text/plain, `getLadoProjectProtection`), 스키마 이름 20개, 설정 PUT·규칙 PATCH·실행 GET 요약. `build.go`: 병합 라우트에 500 한 줄(`// LADO fork`).
  - ladoverify: 새 `views.go`(`Head`, `CheckLog`, `RunAgain`·`RunChecksAgain`, `Protection`), `Store` 에 `GetLadoCheckRun` 한 줄, 테스트 fake 에 같은 메서드.
  - 업스트림 훅: `api.go` +3/−2(필드·주석·생성자 한 줄), `daemon.go` +1(`LadoVerify: ladoVerify`).
  - 생성물(`npm run api`): `openapi.yaml` +914/−12, `schema.ts` +626/−8.
  - 기존 테스트: `LadoProjectSettingsResponse` 가 slice 를 가져 `!=` 비교가 컴파일되지 않음 → `lado_projects_test.go` 1곳, `lado_queue_test.go` 3곳을 `reflect.DeepEqual` 로.
- 엔트리와 다르게 한 것과 이유:
  1. 통합 승인의 증명은 컨트롤러가 본문 디코딩 전에 확인하지 않는다. 대상 `<sessionId>@<headSha>` 의 head 가 본문에 있어서, 정확한 본문 바이트를 담은 closure 를 넘기고 서비스가 다른 무엇보다 먼저 부른다(S13 이 만든 방식, 설정 adoption 과 같음). 그 전의 디코딩은 엄격 디코딩뿐이라 부작용이 없다(알 수 없는 필드는 400, 서비스·증명 모두 안 부름 — 테스트).
  2. 서비스에 메서드 4개를 새 파일로 더했다(엔트리 "필요하면 최소만"): 실행 화면용 head 의 기록·승인 읽기, 로그 읽기, 다시 돌리기(S11 메모대로 끝난 명령마다 `cancelled` 실행 행 + `Kick`; 실행 중인 명령은 두고, 안 돈·취소된 명령은 이미 대상), 보호 상태(프로젝트 없음·보관됨 404 `PROJECT_NOT_FOUND`, 읽기 실패 500 `PROJECT_LOAD_FAILED`·`LADO_VERIFY_FAILED`). 기존 함수는 바꾸지 않았다.
  3. `verification.evidence` 는 `lado_evidence_links` 가 아니라 기록의 evidence 항목 세부(criteria)에서 만든다: 해시가 덮는 같은 기록이라 실행 화면과 승인 응답이 한 함수로 만들고, `Store` 인터페이스를 더 넓히지 않는다. 내용은 링크 표와 같다(링크 표의 파일 행 criterion 0 은 evidence 세부의 `fileVersion`·`fileSha256`·`fileHead`).
  4. 항목 `details` 는 `json.RawMessage`(TS `unknown`)가 아니라 모든 kind 의 키를 모은 typed union(`LadoVerifyItemDetailsResponse`, 모두 omitempty) — S17 이 타입으로 읽게. kind 사이 키 충돌은 같은 뜻의 `branch` 하나. 빠진 키 = 빈 값·false·0.
  5. 로그가 없는 실행(아직 도는 중, 출력 없음)은 200 빈 본문. Blobs 가 없으면 503 `LADO_VERIFY_UNAVAILABLE`, blob 읽기 실패는 500 `LADO_VERIFY_FAILED`.
  6. 다시 돌리기 202 본문 `{headSha, commands}`; 명령이 없는 프로젝트도 `commands: []` 로 202(엔트리에 없는 409 를 만들지 않음).
  7. 병합 라우트 명세의 500 은 업스트림 `build.go` 한 줄(범위 목록 밖이지만 "명세의 응답 코드 목록에 … 500 을 올바르게" 와 S13 메모 몫).
  8. golangci-lint gosec G705(로그 `Write`)는 `//nolint:gosec` 주석(text/plain + nosniff) — `sessions.go` 의 기존 예와 같은 형식.
- 테스트 순서: 필수 테스트는 새 DTO·라우트·서비스 메서드를 쓰므로 구현 전에는 컴파일되지 않아 실패부터 보지 못했다. 실제 store·서비스로 확인: 실행 화면(ladoqueue + ladoverify + sqlite: 옛 head 기록은 안 보임 → 현재 head 기록 → 승인과 실패한 상태 게시, `Verify` 없으면 두 필드 없음), 다시 돌리기(unit 완료·lint 실행 중·e2e 미실행 → unit·e2e, `CommandsToRun` 으로 루프가 다시 돌릴 것까지), 로그(메모리 blob, 다른 실행의 id 404), 검사 명령(ladoproject + 실제 증명 검사기), 보호 경로(ladopolicy + `EnsureBuiltins` + 실제 증명 검사기). 통합 승인 두 테스트와 보호 상태는 가짜 서비스 + 실제 증명 검사기·실제 `EvaluateProtection`.
- 확인한 명령과 결과: `go build ./... && go vet ./...` ok; `go test` `./internal/httpd/controllers/ ./internal/httpd/apispec/... ./internal/httpd/` 전체 ok; `./internal/service/ladoverify/` 격리 ok; 데몬 `-run Lado` 격리 21 ok; golangci-lint v2.13.2(`./internal/httpd/... ./internal/service/ladoverify/... ./internal/daemon/...`) G705 고친 뒤 GO_LINT_OK; gofmt 깨끗; `npm run api` 뒤 `typecheck:e2e` 오류 0, `typecheck` 오류 8(회고).
- 게이트: `gate-check.sh --run` 9/9 MET(HEAD `8c2de3ec6`; G1 8/8, G6 `api.go` 5/12·`daemon.go` 10/30). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~5 전부, 필수 테스트 8개. 렌더러(S16~)는 손대지 않음. 서비스는 새 파일의 읽기와 다시 돌리기만(다르게 한 것 2).
- 남은 틈:
  - 렌더러 `typecheck` 가 빨갛다(8개, 아래) — S16 이 고칠 것.
  - 다시 돌리기는 표식 실행 행(`cancelled`, error_message "The developer asked LADO to run the command again.")을 남겨 검사 실행 기록에 보인다. 표식과 이전 실행의 시작 시각이 같으면 `newestCheckRun` 이 목록 뒤의 것을 고른다(실제 시계에서는 사실상 없음).
  - 다시 돌리기 직후 기록의 commands 항목이 wait 라 태스크가 Validating 으로 돌아간다(의도). 이미 승인된 head 는 Integrating 그대로(승인은 head 별).
  - 다시 돌리기의 409 `LADO_EXECUTION_NO_PR` 문구는 승인과 같은 "…to integrate."(공유 함수 `noOpenPR`). 렌더러는 코드로 번역.
  - 실행 화면은 검증 읽기가 실패하면 실행 응답 전체가 500 `LADO_VERIFY_FAILED`.
  - 보호 상태의 저장소 순서는 리졸버 순서. 기준 브랜치를 못 푼 저장소는 `branch` 빈 값의 `unknown`.
  - 스냅숏 `validators` 스키마는 넣지 않았다 — 스냅숏 내용을 그대로 내보내는 응답이 없다(실행 응답은 규칙·프롬프트·설정 항목만).
- 다음 단계 메모:
  - **새 스키마 이름(S16 `lib/lado.ts`)**: `LadoVerificationResponse`, `LadoVerifyItemResponse`, `LadoVerifyItemDetailsResponse`, `LadoVerifyCheckResponse`, `LadoVerifyCommandRunResponse`, `LadoVerifyCommitResponse`, `LadoVerifyTextResponse`, `LadoReflogEntryResponse`, `LadoEvidenceCriterionResponse`, `LadoEvidenceRefResponse`, `LadoIntegrationApprovalResponse`, `ApproveLadoIntegrationRequest`, `LadoIntegrationApproveResponse`, `LadoRunChecksResponse`, `LadoBranchProtectionResponse`, `LadoProjectProtectionResponse`, `LadoCheckCommandBody`, `LadoRulePaths`, 파라미터 `LadoCheckRunLogParams`, `LadoProjectProtectionQuery`. operationId `approveLadoIntegration`, `runLadoChecks`, `getLadoCheckLog`(text/plain), `getLadoProjectProtection`.
  - **응답 모양**: 실행 `verification{headSha, prUrl, outcome, reason?, sha256, items[{kind, outcome, code?, details}], evidence[{index, state, refs[{kind, key, sha}]}], computedAt}`(현재 head 기록이 없으면 생략), `integration{id, headSha, verificationSha256, acknowledged, actor, approvedAt, statusState, statusAttempts, statusPostedAt?, statusError?}`, `pr.headSha?·targetBranch?·title?`. 승인 `{verification, verificationSha256, confirm, approval?}`(dry run 은 approval 없음). 데스크톱(S16)은 dry run 의 `verification.prUrl`·`verification.headSha`·`verificationSha256`·`confirm` 으로 `{prUrl, headSha, verificationSha256, acknowledge}` 에 서명(`dryRun` 은 빼거나 false). 다시 돌리기 202 `{headSha, commands}`. 로그 200 text/plain(빈 본문 가능, 최대 1 MiB). 보호 `{projectId, repositories[{repository, branch, state, missing, requiredChecks, checkedAt?, error?}]}`. 설정 `{projectId, ladoMode, maxConcurrent, checkCommands[{name, command}], checkTimeoutMinutes}`; 명령 변경은 `checkCommands` 만 담은 PUT 본문에 서명(플랜 §16). 규칙 `paths?{globs}`(paths.protected 만, 버전에도), PATCH `{"baseVersion": n, "paths": {"globs": [...]}}`.
  - **오류 코드(라우트별)**: 승인 — 400 `INVALID_JSON`·`LADO_INTEGRATION_ACK_MISMATCH{required, given}`, 403 `LADO_PRESENCE_REQUIRED`·`LADO_PRESENCE_INVALID`·`LADO_PRESENCE_EXPIRED`·`LADO_PRESENCE_UNAVAILABLE`·`LADO_PRESENCE_REPLAYED`, 404 `LADO_EXECUTION_NOT_FOUND`, 409 `LADO_EXECUTION_NO_PR{sessionId}`·`LADO_INTEGRATION_HEAD_CHANGED{headSha, currentHeadSha[, prUrl, currentPrUrl]}`·`LADO_VERIFICATION_CHANGED`·`LADO_INTEGRATION_NOT_READY{reasons}`·`LADO_INTEGRATION_ALREADY_APPROVED`, 500 `LADO_VERIFY_FAILED`, 503 `LADO_VERIFY_UNAVAILABLE`. 다시 돌리기 — 404 `LADO_EXECUTION_NOT_FOUND`, 409 `LADO_EXECUTION_NO_PR`, 500 `LADO_VERIFY_FAILED`. 로그 — 404 `LADO_CHECK_RUN_NOT_FOUND`, 500 `LADO_VERIFY_FAILED`, 503 `LADO_VERIFY_UNAVAILABLE`. 보호 — 400 `INVALID_QUERY`, 404 `PROJECT_NOT_FOUND`, 500 `PROJECT_LOAD_FAILED`·`LADO_VERIFY_FAILED`. 설정 — 400 `LADO_SETTINGS_EMPTY`·`LADO_CHECK_COMMANDS_INVALID{index, field, reason}`·`LADO_DISPATCH_SETTINGS_INVALID`, 403 증명 코드. 규칙 — 400 `LADO_PROTECTED_PATHS_INVALID{index, reason}` 또는 `{rule}`, 403 증명 코드, 409 `LADO_RULE_CHANGED`. 병합 — 409 `LADO_INTEGRATION_APPROVAL_REQUIRED{prUrl, headSha, sessionId}`, 500 `LADO_MERGE_GATE_FAILED`. 서비스가 없으면 모든 LADO 라우트 501.
  - **렌더러 typecheck 오류(S16 몫; `npm --prefix frontend run typecheck` 8개, `typecheck:e2e` 0개)**:
    - `src/renderer/lib/lado-errors.ts(424,7)` TS2740 — 사유 `Record` 에 새 사유 14개가 없음(verifying, checks_pending, checks_failed, commands_running, worktree_not_at_head, commands_failed, command_timed_out, worktree_missing, commit_messages_failed, protected_path, review_pending, changes_requested, status_not_posted, merged_without_approval).
    - 설정 응답 fixture 에 `checkCommands`·`checkTimeoutMinutes` 가 없음(TS2739·TS2322·TS2345): `src/main/lado-presence.test.ts(154,2)`, `src/renderer/components/lado/LadoProjectRulesSection.test.tsx(449,84)`, `src/renderer/components/lado/LadoProjectSection.test.tsx(50,7)`·`(51,7)`·`(227,84)`, `src/renderer/components/lado/LadoSessionLine.test.tsx(69,61)`·`(107,61)`.
    - 새 상태 셋은 typecheck 오류를 내지 않는다(상태 라벨 표가 완전한 `Record` 가 아님) — S16·S17 이 라벨·톤을 직접 더해야 한다.
  - **패키지 안 새 이름**(뒤 단계 충돌 주의): controllers `LadoVerifyService`, `registerVerify`, `approveIntegration`, `runChecks`, `getCheckLog`, `getProjectProtection`, `ladoSessionID`, `ladoOrEmptyStrings`, `ladoProtectionStates`, `ladoBranchProtectionResponse`, `ladoVerificationResponse`, `ladoHeadVerificationResponse`, `ladoIntegrationApprovalResponse`, `ladoRulePaths`, `maxLadoSettingsBody`, 경로 상수 `ladoIntegrationApprovePath`·`ladoChecksRunPath`·`ladoCheckLogPath`·`ladoProjectProtectionPath`; 테스트 `fakeLadoVerifyService`, `ladoVerifyHead`, `ladoVerifyOldHead`, `mustLadoItem`, `ladoVerifyRecord`, `ladoRecordSHA`, `ladoApproveResult`, `ladoApproveBody`, `writeLadoHeadPR`, `setLadoChecks`, `startLadoCheckRun`, `mapLadoBlobs`. ladoverify `HeadView`, `Head`, `CheckLog`, `RunAgain`, `RunChecksAgain`, `runAgainNote`, `Protection`.
  - **S19(docs)**: TDD §13 라우트 표에 이 단계의 라우트·본문·코드, "다르게 한 것" 1·3·5·6 을 적을 것.
