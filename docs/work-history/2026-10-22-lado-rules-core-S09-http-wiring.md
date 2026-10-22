# 2026-10-22 LADO 규칙 코어 — S09 HTTP 규칙 라우트·API spec·데몬 와이어링

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§7) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙 서비스를 `/api/v1/lado` 아래 라우트로 열고(TDD §13 Rules 행), API spec·TS 타입을 재생성하고, 데몬이 정책 서비스를 만들어 내장 규칙을 시드하고 ladotask 승인에 연결하게 한다. 이 단계가 끝나면 백엔드 규칙 코어가 끝까지 돈다.

## 범위

- 만든다: `backend/internal/httpd/controllers/lado_rules.go`, `lado_rules_dto.go`, 테스트 `lado_rules_test.go`.
- 고친다: `lado_projects.go`(`LadoController.Rules LadoRuleService` 필드, `Register` 에 7 라우트), `backend/internal/httpd/apispec/specgen/lado_operations.go`(7 operation + `ladoSchemaNames`), `backend/internal/httpd/api.go`(LADO 블록: `APIDeps.LadoRules`, 컨트롤러 생성자에 `Rules`), `backend/internal/daemon/lado_wiring.go`(`wireLadoWork` 가 정책 서비스를 만들고 `EnsureBuiltins` — 오류는 로그 — `ladotask.Deps.Policy` 에 넣고 세 번째 값으로 돌려준다), `backend/internal/daemon/daemon.go`(그 줄과 APIDeps 리터럴의 `LadoRules:` — 기존 LADO 훅 줄만), 테스트 `daemon/lado_wiring_test.go`.
- 재생성: `npm run api`.
- 하지 않는다: 렌더러·main(S10~S12). `httpd/controllers/dto.go` 는 건드리지 않는다(TDD §1.1).

## 절차

1. DTO 는 플랜 §7 JSON 그대로(camelCase). 응답 변환 함수는 `lado_plans.go` 의 `ladoTaskPlanResponse` 꼴. `LadoRuleService` 인터페이스는 컨트롤러가 쓰는 메서드만(서비스의 List·Rule·Create·Update·Delete·SetBinding·Effective).
2. `PATCH /lado/rules/{ruleId}` 는 본문을 먼저 통째로 읽고(최대 64 KiB — `readLadoPresenceBody` 는 4 KiB 라 같은 꼴의 함수를 하나 더 두거나 한도를 인자로) `decodeLadoBytes` 로 풀고 `Presence: c.presenceCheck(r, body)` 를 넘긴다 — 증명은 이 정확한 바이트에 서명된다.
3. `GET /lado/effective`: `projectId`·`taskId` 중 정확히 하나 — 둘 다 없거나 둘 다 있으면 400 `LADO_EFFECTIVE_SCOPE_REQUIRED`.
4. 서비스가 nil 이면 모든 규칙 라우트가 `apispec.NotImplemented`(라우트/spec 패리티 테스트가 nil 의존으로 라우터를 만든다).
5. specgen: operation id 는 플랜 §7 표 그대로, 태그 `lado`, 응답은 `ladoResps(...)`. PATCH 는 `LadoPresenceHeaderParam` 을 path params 에 같이 둔다(승인 라우트처럼).
6. 와이어링: `ladopolicy.New(ladopolicy.Deps{Store: store, Projects: store, Blobs: ladoblob.New(cfg.StateDir), AOBuild: cfg.AppVersion, Log: log})`. 데몬 생명주기에서 ctx 를 받을 수 없으면 짧은 타임아웃의 `context.Background()` 로 `EnsureBuiltins`.
7. `npm run api` 뒤 `npm --prefix frontend run typecheck` 가 통과해야 한다(새 타입만 생기고 기존 사용처는 그대로).

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): rules and effective preview routes` · Co-Authored-By 유지). openapi.yaml·schema.ts 는 Go 변경과 같은 커밋.
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S09-http-wiring.md` 로 전건 MET 를 확인한다.

## 필수 테스트

`backend/internal/httpd/controllers/`:
- `TestLadoAPI_RuleRoutesAnswerNotImplementedWithoutAService` — 7 라우트 모두 501
- `TestLadoAPI_ListRules` — 목록 JSON 형태(global·overrides·ceiling)
- `TestLadoAPI_CreateRuleDryRun` — `dryRun` 이 서비스로 가고 응답 `applied=false`·impact
- `TestLadoAPI_UpdateRulePassesTheExactBodyToPresence` — 가짜 서비스가 Presence 를 부르면 가짜 검증기가 받은 메서드·경로·본문이 요청 그대로
- `TestLadoAPI_DeleteRule` — 204, 서비스 오류는 오류 봉투(코드 유지)
- `TestLadoAPI_SetBinding` — 본문이 그대로 서비스로, 응답 impact
- `TestLadoAPI_Effective` — projectId 만 / taskId 만 성공, 둘 다 없음·둘 다 있음 400 `LADO_EFFECTIVE_SCOPE_REQUIRED`

`backend/internal/daemon/`:
- `TestWireLadoWorkSeedsBuiltinRules` — 와이어링 뒤 store 에 내장 규칙 3개, 반환된 규칙 서비스가 nil 이 아니다

## 수용 게이트

- [ ] S09-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S09-G1: 컨트롤러 필수 테스트 7종
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_RuleRoutesAnswerNotImplementedWithoutAService TestLadoAPI_ListRules TestLadoAPI_CreateRuleDryRun TestLadoAPI_UpdateRulePassesTheExactBodyToPresence TestLadoAPI_DeleteRule TestLadoAPI_SetBinding TestLadoAPI_Effective
  EXPECT: GO_TESTS_OK 7/7
- [ ] S09-G2: 데몬 와이어링 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/daemon/ TestWireLadoWorkSeedsBuiltinRules
  EXPECT: GO_TESTS_OK 1/1
- [ ] S09-G3: httpd 전체(라우트/spec 패리티·임베드 spec 포함)와 데몬 LADO 테스트 통과
  CHECK: cd backend && go test -count=1 ./internal/httpd/... && go test -count=1 -run 'Lado' ./internal/daemon/ && echo S09_HTTP_DAEMON_OK
  EXPECT: S09_HTTP_DAEMON_OK
- [ ] S09-G4: operation id 가 플랜 그대로다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/httpd/apispec/specgen/lado_operations.go '"listLadoRules"' '"createLadoRule"' '"getLadoRule"' '"updateLadoRule"' '"deleteLadoRule"' '"setLadoBinding"' '"getLadoEffective"'
  EXPECT: HAS_OK
- [ ] S09-G5: 생성 API 파일이 커밋과 같다
  CHECK: bash .claude/gate/lado-rules-core.sh api-clean
  EXPECT: API_CLEAN
- [ ] S09-G6: 프런트 타입체크 통과
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S09-G7: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S09-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
