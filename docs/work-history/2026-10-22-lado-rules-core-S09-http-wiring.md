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

- 시작 HEAD: `0e28f4c3ffbcb9eed38d4c85f9c832a0f2eb4ad5`(`docs(lado): record S08`), 트리 깨끗.
- 규칙 라우트 없음. `LadoController` 에 `Rules` 필드가 없고, `wireLadoWork(cfg, store, projects, work, log)` 는 `(connections, tasks)` 두 값을 돌려주며 `ladotask.Deps.Policy` 를 비워 두었다 — 운영 경로의 승인은 500 `LADO_RULES_UNAVAILABLE`(S08 메모의 의도된 중간 상태). `readLadoPresenceBody` 는 4 KiB 고정.
- 전제 확인: 플랜 §7 의 `cfg.AppVersion` 은 없다. 데몬이 아는 앱 버전은 `cfg.Telemetry.AppVersion`(`AO_TELEMETRY_APP_VERSION`)이고, `frontend/src/main.ts` 가 텔레메트리 설정과 무관하게 늘 `app.getVersion()` 으로 넣는다. presence 검증기는 헤더가 없을 때 dev 모드면 dev proof 를 내므로(`ladopresence.Verify`), 컨트롤러는 `Presence` 를 nil 로 두면 안 된다(브라우저의 dev PATCH 가 막힌다). swaggest 는 Go map 을 nullable 로 내보낸다(`details` 가 `| null` 이 됨) — `nullable:"false"` 태그로 끈다. 데몬 호출 지점에 데몬 수명 `ctx` 가 있다.
- 테스트는 구현 뒤에 썼다. 첫 PASS 를 믿지 않고 변이 검사로 확인했다(After).

## After — 수행 후 실측

- 코드 커밋 `46b97bf76` `feat(lado): rules and effective preview routes`: 13 파일, +3134 −55(openapi.yaml +1041·schema.ts +836 같은 커밋).
  - `lado_rules.go`: `LadoRuleService`(List·Rule·Create·Update·Delete·SetBinding·Effective), 7 핸들러, 응답 변환(`ladoRuleResponse`·`ladoRuleChangeResponse`·`ladoEffectiveResponse`), 도우미 `ladoRuleID`·`ladoRuleIDs`·`ladoStrings[T ~string]`(nil 대신 `[]`)·`ladoRuleCeiling`. 서비스 nil → 7 라우트 모두 `apispec.NotImplemented`.
  - `lado_rules_dto.go`: 플랜 §7 JSON 그대로(camelCase) + 경로/쿼리 파라미터 `LadoRuleIDParam`·`ListLadoRulesQuery`·`LadoEffectiveQuery`. 닫힌 값 집합(scope·state·strength·severity·capability·AO 지시 kind·ceiling mode)은 `enum` 태그로 TS 유니온이 된다. 슬라이스에는 enum 을 달지 않았다(저장소 선례 없음) — `enforcement` 등은 `string[]`.
  - `PATCH /lado/rules/{ruleId}`: `readLadoPresenceBody(w, r, maxLadoSmallBody)`(64 KiB)로 통째로 읽고 `decodeLadoBytes` → `Presence: c.presenceCheck(r, body)`. `readLadoPresenceBody` 는 한도를 인자로 받게 바꾸고 기존 두 곳(승인·모드 끄기)은 `maxLadoPresenceBody`(4 KiB) 그대로 넘긴다.
  - `GET /lado/effective`: `projectId`·`taskId` 중 정확히 하나가 아니면(둘 다 없음·둘 다 있음·빈 값) 400 `LADO_EFFECTIVE_SCOPE_REQUIRED`, 서비스 호출 0.
  - specgen: 7 operation(id 는 플랜 표 그대로, 태그 `lado`, `ladoResps(...)`), PATCH 는 `LadoPresenceHeaderParam` 을 path params 에 함께. `ladoSchemaNames` 24개 추가.
  - `api.go`: `APIDeps.LadoRules`, 컨트롤러 생성자에 `Rules`. `daemon.go`: 기존 LADO 훅 두 줄(`wireLadoWork` 호출의 세 번째 값 `ladoRules`·`ctx` 인자, APIDeps 의 `LadoRules:`).
  - `lado_wiring.go`: `wireLadoWork(ctx, cfg, store, projects, work, log) (connections, tasks, rules)`. `ladoblob.New(cfg.StateDir)` 하나를 정책·태스크 서비스가 함께 쓰고, `ladopolicy.New(Deps{Store, Projects, Blobs, AOBuild: cfg.Telemetry.AppVersion, Log})` → `EnsureBuiltins(ctx)`(오류는 `log.Error`, 데몬은 계속 — 시드 실패면 ceiling 이 없어 해석이 blocking 문제를 내고 앱이 보여 준다) → `ladotask.Deps.Policy`.
  - 테스트: 필수 8종(컨트롤러 7 + `TestWireLadoWorkSeedsBuiltinRules`: 내장 3개 v1, 두 번째 와이어링(재시작)도 그대로, 반환 서비스의 project effective 가 규칙 3개·ceiling `auto`·`aoBuild` = 설정값) + 추가 `TestWireLadoWorkGivesTasksTheRules`(규칙 이전의 승인 — 스냅숏 없음 — 을 store 에 직접 만들고 와이어링 뒤 `tasks.List` 가 `PolicySHA256` 을 채워 `review_required` 로 보이는지: `Policy` 연결의 유일한 관측점). 기존 `TestWireLadoWorkBuildsConnectionsAndTasks` 는 호출부만 세 값·`ctx` 로.
- 플랜에 없어 이 단계에서 정한 것:
  - `POST /lado/rules` 는 적용·dry run 모두 200. dry run 에 201 Created 는 틀리고, 성공 상태 하나가 `ladoResps` 와 클라이언트를 단순하게 한다. 무엇이 일어났는지는 `applied` 가 말한다.
  - `updatedAt` = 활성 버전의 생성 시각(도메인 `LadoRule` 에 Global 바인딩 시각이 없다). `ceiling` 은 `agent.permission.ceiling` 이고 설정이 상한 모드일 때만. `rule`(변경 응답)은 결과에 규칙이 없을 때만 빠진다(실제로는 늘 있다). `problems[].rules`·`details` 는 없으면 `[]`·`{}` — `details` 에 `nullable:"false"` 를 달아 TS 타입이 `{[key]: string}`(null 없음).
  - 서비스의 `LADO_EFFECTIVE_SCOPE_INVALID`(다른 프로젝트의 태스크)는 컨트롤러가 둘 다 있는 요청을 먼저 막으므로 HTTP 로는 닿지 않는다(서비스 쪽 방어로 남김).
  - 헤더 없는 ceiling PATCH 의 403 `LADO_PRESENCE_REQUIRED` 는 검증기 문구("Approve this in the AO desktop app…")로 나간다 — 서비스의 ceiling 문구는 `Presence == nil` 일 때뿐인데, dev presence 를 살리려고 컨트롤러는 nil 을 넘기지 않는다.
- 변이 검사(10회, 복사 → 변이 → 해당 테스트 → 복원): `Policy: rules` 제거·`EnsureBuiltins` 제거·`AOBuild` 제거·presence 에 다른 바이트·effective 에서 둘 다 허용·override label 제거·`DryRun` 무시·nil 가드 제거·삭제 응답 200·`details` null — 10개 모두 죽었다. 생존 0.
- 추가 실행: `gofmt -l` 출력 없음. golangci-lint(저장소 설정, 앞 단계 세션이 빌드한 바이너리, `GOPROXY=off`) `./internal/httpd/... ./internal/daemon/...` → `0 issues`. `go test -count=1 ./internal/daemon/`(패키지 전체) ok. `npm --prefix frontend run typecheck`+`typecheck:e2e` 통과(약 6분, 게이트 실행 시간의 대부분).
- 게이트(HEAD `46b97bf76`, 코드 커밋 직후 깨끗한 트리): `GATES=9 MET=9 UNMET=0`(G1 `GO_TESTS_OK 7/7`, G2 `1/1`, 6분 30초). 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 플랜과 다른 것은 `cfg.AppVersion` → `cfg.Telemetry.AppVersion`(필드 위치)과 `wireLadoWork` 의 `ctx` 첫 인자(데몬 ctx 가 호출 지점에 있어 Background+타임아웃이 필요 없었다) 두 가지.
- 필수 테스트만으로는 `ladotask.Deps.Policy` 연결이 어디서도 관측되지 않았다(변이 "Policy 제거" 가 살아남는다). 승인된 태스크의 상태가 유일한 관측점이라, 스냅숏 없는 승인을 store 로 직접 만들어 `review_required` 를 보는 테스트를 하나 더 두었다. 이 테스트는 동시에 "규칙 이전 승인은 이 빌드로 재시작하면 모두 다시 승인해야 한다"는 설계 결과를 고정한다.
- presence 경로 테스트는 진짜 HMAC 서명 대신 기록만 하는 가짜 검증기를 써서 "검증기가 받은 메서드·경로·본문 = 보낸 그대로"를 직접 단언했다. 공백·키 순서가 이상한 본문을 보내 재인코딩이 끼면 바로 깨진다.
- 다음 단계 메모:
  - S10: 타입은 `schema.ts` 의 `LadoRuleResponse`·`LadoRuleListResponse`·`LadoRuleDetailResponse`·`LadoRuleChangeResponse`·`LadoEffectiveResponse`·`CreateLadoRuleRequest`·`UpdateLadoRuleRequest`·`SetLadoBindingRequest`. 생성도 200. `lib/lado-errors.ts` 에 `LADO_EFFECTIVE_SCOPE_REQUIRED`(400)와 플랜 §5 의 규칙 오류 코드들, `INVALID_QUERY`(includeArchived). `LADO_PRESENCE_REQUIRED` 문구는 승인 전용이 아니게(ceiling 변경에도 나간다). main 의 ceiling 서명 본문 `{"baseVersion":n,"ceiling":{"mode":…}}` 은 보낸 바이트 그대로 서명해야 한다(64 KiB 한도).
  - S12: 이 빌드로 데몬이 뜨면 규칙 이전의 승인(스냅숏 없음)은 모두 `review_required` 다 — 태스크 뷰의 재승인 경로가 필요하다(플랜대로). `effective` 응답에는 태스크 상태가 없다(플랜 §7 그대로); 상태는 태스크 응답에서 읽는다.
  - 게이트 실행 한 번에 타입체크가 6분 남짓 걸린다 — S10~S12 는 엔트리 커밋 뒤 재실행까지 두 번이니 시간을 잡아 둔다.
