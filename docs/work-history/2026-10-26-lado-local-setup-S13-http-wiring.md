# 2026-10-26 LADO 로컬 설정 채택 — S13 HTTP 라우트·DTO·specgen·데몬 배선

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§13, §9·§10 의 훅, §1 결정 h·n) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

서비스를 데몬과 앱에 잇는다: `/api/v1/lado/setup` 라우트, 데스크톱 확인이 필요한 라우트의 증명 검사, 미리보기의 setup 필드, 그리고 데몬이 이미 찾는 Claude 폴더·`~/.claude.json`·Codex 홈을 `ladosetup` 에 넘기고 홈·덧씌우기 훅을 무엇이든 띄우기 전에 설치한다. 데몬 시작 때 검사·홈 정리를 돈다.

## 범위

- 만든다: `httpd/controllers/lado_setup.go`, `lado_setup_dto.go`, 테스트; `httpd/apispec/specgen/lado_operations.go` 의 operation·schema 이름; `daemon/lado_setup_wiring.go`(새 파일), 테스트.
- 바꾼다(훅 줄): `httpd/api.go`(APIDeps·API 필드·생성자·Register), `daemon/daemon.go`(배선 호출 한두 줄 — 가능한 한 `wireLadoWork` 안), 생성물 `openapi.yaml`·`frontend/src/api/schema.ts`(`npm run api`).
- 하지 않는다: 렌더러(S14 이후).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 컨트롤러(꼴은 `lado_rules.go`): 서비스 인터페이스를 이 파일에 선언, 서비스가 nil 이면 `apispec.NotImplemented`. 라우트는 플랜 §13 표 그대로. 요청 본문은 엄격하게(`INVALID_JSON`), 증명이 필요한 라우트(채택·복원·변경)는 원본 본문을 `readLadoPresenceBody` 로 읽어 `c.presenceCheck(r, body)` 를 서비스에 넘긴다(채택은 `dryRun:false` 일 때만, 변경은 서비스의 `needsPresence` 판단 — 증명이 없으면 서비스가 403). 응답 DTO 는 `lado_setup_dto.go`(업스트림 `dto.go` 금지).
2. 미리보기: `GET /lado/effective` 의 응답(`LadoEffectiveResponse`)에 setup 항목, `aoSystem`, 충돌·수락, 승인 뒤 setup 차이를 더한다(S11 의 `EffectiveView` 필드). 실행 패널(`GET /lado/executions/{sessionId}`)의 스냅숏 DTO 에 setup 항목 이름 목록.
3. 오류 코드: 플랜 §6·§7·§12 와 앞 단계가 만든 코드 + `LADO_SETUP_UNAVAILABLE`, `LADO_SETUP_NOT_FOUND`. details 는 문자열 맵(렌더러 번역이 쓰는 키: `tool`, `kind`, `name`, `reason`, `stage`, `item`, `sessions`, `processes`).
4. specgen: operation 마다 `{method, path, id, tag:"lado", summary, pathParams, reqBody, resps}`, 증명 라우트에 `LadoPresenceHeaderParam`, 새 DTO 를 `ladoSchemaNames` 에. `npm run api` 로 재생성.
5. 데몬 배선(`daemon/lado_setup_wiring.go`, `wireLadoWork` 에서 호출):
   - `ladosetup.New(Deps{Store: store, Blobs: <wireLadoWork 의 ladoblob>, StateDir: cfg.StateDir, ClaudeDir, ClaudeJSON, ClaudeDirFromEnv (daemon.go 1352 부근에서 이미 찾는 값), CodexHome (daemon.go 554 의 codexHome), Projects, Sessions, Processes: procmem, DevIgnoreProcesses: os.Getenv("AO_LADO_DEV_SETUP_IGNORE_PROCESSES") == "1", Log})`. 경로를 찾는 코드는 데몬 쪽에만 둔다.
   - `ladopolicy` 의 `Deps.Setup` 에 그 서비스를(정책 서비스를 만들 때). 
   - 훅 설치: `sessMgr.SetLadoSetupHome(setup)`(S09), codex 어댑터 `SetLadoOverrides(…)`(S10 — 세션 id 로 client request id·프로젝트를 읽어 `CodexOverrides` 를 부르는 감싸기), Chat MCP 목록 훅(S10), 지침 절(S10 의 PromptSource). 모두 세션이 스폰·복원되기 전(게이트 설치와 같은 자리 또는 그 직후).
   - 데몬 시작: 첫 검사(`Scan`)와 `CleanupHomes` 를 백그라운드로(시작을 막지 않음, 실패는 로그). 
   - APIDeps 의 `LadoSetup`.
6. 커밋 메시지 예: `feat(lado): local setup routes and daemon wiring`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S13-http-wiring.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

`httpd/controllers`:

- `TestLadoAPI_SetupInventory` — 인벤토리·검사 라우트의 응답 모양(상태·사유·OS 검증)
- `TestLadoAPI_AdoptNeedsPresence` — dry run 은 증명 없이 됨, 실제 채택은 증명 없이 403, 증명이 있으면 서비스가 정확한 본문과 확인을 받음
- `TestLadoAPI_RestoreNeedsPresence` — 복원도 같은 규칙
- `TestLadoAPI_SetupChangesPassTheExactBody` — 만들기·편집·바인딩·변경 감지 라우트가 원본 본문과 확인 함수를 서비스에 넘김, 오류 envelope 의 code·details
- `TestLadoAPI_SetupRoutesWithoutService` — 서비스 nil 이면 모든 setup 라우트가 NotImplemented(스펙에 operation 이 있음)

`daemon`:

- `TestWireLadoInstallsTheSetupHooks` — 배선 뒤 세션 매니저의 홈 훅, codex 덧씌우기, Chat MCP 훅, 정책의 SetupSource 가 모두 설치됨, 시작 검사가 돎

## 수용 게이트

- [ ] S13-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S13-G1: 컨트롤러 필수 테스트 5개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_SetupInventory TestLadoAPI_AdoptNeedsPresence TestLadoAPI_RestoreNeedsPresence TestLadoAPI_SetupChangesPassTheExactBody TestLadoAPI_SetupRoutesWithoutService
  EXPECT: GO_TESTS_OK 5/5
- [ ] S13-G2: 데몬 필수 테스트와 기존 LADO 배선 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/daemon/ TestWireLadoInstallsTheSetupHooks TestWireLadoInstallsOneGateOnEverySendAndSpawnPath TestWireLadoWorkStartsTheDispatcher
  EXPECT: GO_TESTS_OK 3/3
- [ ] S13-G3: 라우트·스펙 짝과 생성물 일치(httpd 전체)
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/httpd/...
  EXPECT: GO_PKG_OK
- [ ] S13-G4: npm run api 가 커밋과 같다
  CHECK: bash .claude/gate/lado-local-setup.sh api-clean
  EXPECT: API_CLEAN
- [ ] S13-G5: 업스트림 데몬·httpd 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 60 backend/internal/daemon/daemon.go backend/internal/httpd/api.go backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/controllers/dto.go
  EXPECT: BUDGET_OK
- [ ] S13-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S13-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `048da14e1`(docs(lado): record S12), 트리 깨끗, 브랜치 `feat/lado-local-setup`. go1.27.1(스크래치 toolchain), Node v24.21.0, umask 0022, `go build ./...` 통과.
- 출발점: `ladosetup` 에 화면이 읽을 것(항목 상세, 채택 상세, 범위별 항목)이 없고 `ScopeConflicts` 는 범위 확인·권한 천장 없이 돈다. `wireLadoWork` 는 setup 서비스를 만들지 않아 S09·S10 의 훅(`SetLadoSetupHome`, codex `SetLadoOverrides`, `SetLadoChatServers`, `PromptSource.Setup`)이 모두 비어 있고, 정책에도 `Deps.Setup` 이 없다. `diff-budget` 대상 4파일은 태그 이후 0/60.
- 앞 단계 메모 중 이 단계 몫: S05(Deps 이름, 시작 `Scan`), S06(충돌 DTO 필드·details 키), S07(partial 은 결과와 오류를 함께, Presence 클로저, Blobs), S08(시작 `Scan` 이면 drift 까지, 복원 오류 코드, 채택 상세의 보고서), S09(`SetLadoSetupHome`, `CleanupHomes` 시작과 검사 뒤, `ClaudeDirFromEnv`, HOME_FAILED 는 `errors.As`), S10(Codex `-c` 감싸기, `SetLadoChatServers`, `SetLadoPrompt` 다시 걸기), S11(`EffectiveView` 필드, `ScopeConflicts` 천장, `Deps.Setup`), S12(라우트 → Op 표, `SetPolicy`, 오류 status, `ChangeResult` DTO).

## After — 수행 후 실측

- 커밋: `21b47f466 feat(lado): local setup routes and daemon wiring`, 그리고 이 엔트리 커밋.
- 바꾼 것:
  1. `httpd/controllers/lado_setup.go`(새): 컨트롤러 쪽 `LadoSetupService`(`*ladosetup.Service` 가 구현), 라우트 15개, `LadoController.Register` 에 `c.registerSetup(r)` 한 줄, `LadoController.Setup` 필드. 채택·복원·변경 라우트는 원본 본문을 `readLadoPresenceBody` 로 읽어 `decodeLadoBytes`(엄격, `INVALID_JSON`) 뒤 `c.presenceCheck(r, body)` 를 넘긴다. 채택은 `dryRun:false` 일 때만 Presence 를 채운다(미리보기는 증명을 보지 않음).
  2. `lado_setup_dto.go`(새): DTO 49종(아래 schema 이름).
  3. `lado_rules.go`·`lado_rules_dto.go`: `LadoEffectiveResponse.setup`(`items`, `aoSystemSha256`, `conflicts`, `accepted`), `approved` 에 `setupAdded/Removed/Changed`·`setup`(승인 스냅숏의 항목)·`aoSystemSha256`·`accepted`. 영향·문제 DTO 만들기를 `ladoImpactResponse`·`ladoPolicyProblems` 로 뽑아 setup 과 공유.
  4. `lado_queue.go`·`lado_queue_dto.go`: 실행 스냅숏 DTO 에 `setup[]{itemId, tool, kind, name, version}`.
  5. specgen `lado_operations.go`: `ladoSetupOperations()` 15개(tag `lado`, presence 헤더는 채택·복원·만들기·편집·바인딩·drift), `ladoSchemaNames` 49개. `npm run api` 로 `openapi.yaml`·`schema.ts` 재생성.
  6. `httpd/api.go`: `APIDeps.LadoSetup` 과 생성자 한 줄(라우트 등록은 LadoController 안이라 api.go 의 Register 는 그대로).
  7. `service/ladosetup/views.go`(새): `Overview(ctx, rescan)`(인벤토리 + Global 의 관리 항목 상태 + 채택별 항목 수, rescan 이면 `CleanupHomes`), `ScopeItems`(두 도구 항목의 범위별 상태), `Item`(모든 버전의 내용, 지우거나 복원한 항목도), `Adoption`(항목·백업 매니페스트 요약·복원 보고), `viewScope`(프로젝트·태스크 확인, 태스크는 그 프로젝트로; 다른 프로젝트면 400 `LADO_SETUP_SCOPE_INVALID`). `ScopeConflicts` 는 같은 범위 확인을 하고, 천장이 비면 정책에 묻는다(`Policy.Ceiling`). `restore.go` 의 매니페스트 읽기를 `readBackupManifest` 로 뽑아 복원과 상세가 공유. `ToolProcess.Program()`.
  8. `service/ladopolicy/setup.go`: `Service.Ceiling(ctx)`(보호 규칙이라 모든 범위에서 같은 천장).
  9. `daemon/lado_setup_wiring.go`(새): `deviceClaudeFolder`(계정 카탈로그 `newAgentAccounts` 와 공유 — 같은 값), `ladoDeviceFolders(codexHome)`, `wireLadoSetup`(서비스 + 훅 넷: `SetLadoSetupHome`, `SetLadoChatServers`(Codex Chat 서버), `SetLadoPrompt`(Setup 을 단 `PromptSource` 로 wireLado 의 것을 바꿈), codex 어댑터 `SetLadoOverrides`(세션 id → `GetSession` → 없으면 없음 → `CodexOverrides`)), `startLadoSetup`(백그라운드 `Scan` → `CleanupHomes`, 실패는 로그, done 채널). `wireLadoWork` 는 ladoblob 다음에 setup 을 만들고 `ladopolicy.Deps.Setup` 과 `setup.SetPolicy(rules)` 를 건 뒤 다섯째 값으로 돌려준다. `ladoWorkDeps.setup`. `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1` → `DevIgnoreProcesses`(경고 로그).
  10. `daemon.go`(13줄): 호출에 `setup: ladoSetupDeps{folders: ladoDeviceFolders(codexHome), sessions: sessMgr, agents: agents}`, `ladoSetupDone := startLadoSetup(...)`, `APIDeps.LadoSetup`, 종료 때 `<-ladoSetupDone`, `newAgentAccounts` 가 `deviceClaudeFolder()` 를 씀. `lifecycle_wiring.go` 의 `sessionLifecycle` 에 setter 2줄(컴파일 때 확인).
- 라우트 표(모두 `/api/v1` 아래, 500·501 공통; P = presence 헤더):

  | 라우트 | operation id | 요청 → 응답 schema | 상태 |
  |---|---|---|---|
  | `GET /lado/setup/inventory` | `getLadoSetupInventory` | — → `LadoSetupInventoryResponse` | 200 |
  | `POST /lado/setup/scan` | `scanLadoSetup` | — → `LadoSetupInventoryResponse` | 200 |
  | `GET /lado/setup/items?projectId=&taskId=` | `listLadoSetupItems` | `LadoSetupScopeQuery` → `LadoSetupItemListResponse` | 200 400 404 |
  | `POST /lado/setup/items` (P) | `createLadoSetupItem` | `CreateLadoSetupItemRequest` → `LadoSetupChangeResponse` | 200 400 403 409 503 |
  | `GET /lado/setup/items/{itemId}` | `getLadoSetupItem` | — → `LadoSetupItemDetailResponse` | 200 404 503 |
  | `PATCH /lado/setup/items/{itemId}` (P) | `updateLadoSetupItem` | `UpdateLadoSetupItemRequest` → `LadoSetupChangeResponse` | 200 400 403 404 409 503 |
  | `DELETE /lado/setup/items/{itemId}?dryRun=` | `deleteLadoSetupItem` | `LadoSetupDryRunQuery` → `LadoSetupChangeResponse` | 200 400 404 503 |
  | `POST /lado/setup/items/{itemId}/drift` (P) | `resolveLadoSetupDrift` | `ResolveLadoSetupDriftRequest` → `LadoSetupChangeResponse` | 200 400 403 404 409 503 |
  | `PUT /lado/setup/bindings` (P) | `setLadoSetupBinding` | `SetLadoSetupBindingRequest` → `LadoSetupChangeResponse` | 200 400 403 404 409 503 |
  | `GET /lado/setup/conflicts?projectId=&taskId=` | `listLadoSetupConflicts` | `LadoSetupScopeQuery` → `LadoSetupConflictListResponse` | 200 400 404 |
  | `POST /lado/setup/conflicts/acceptances` | `acceptLadoSetupConflict` | `AcceptLadoSetupConflictRequest` → `LadoSetupChangeResponse` | 200 400 404 409 503 |
  | `DELETE /lado/setup/conflicts/acceptances/{acceptanceId}?dryRun=` | `revokeLadoSetupConflictAcceptance` | `LadoSetupDryRunQuery` → `LadoSetupChangeResponse` | 200 400 404 503 |
  | `POST /lado/setup/adoptions` (P, dryRun 이면 없음) | `adoptLadoSetup` | `AdoptLadoSetupRequest` → `LadoSetupAdoptResponse` | 200 400 403 409 503 |
  | `GET /lado/setup/adoptions/{adoptionId}` | `getLadoSetupAdoption` | — → `LadoSetupAdoptionDetailResponse` | 200 404 503 |
  | `POST /lado/setup/adoptions/{adoptionId}/restore` (P) | `restoreLadoSetupAdoption` | 본문 없음 또는 `{}` → `LadoSetupRestoreResponse` | 200 400 403 404 409 503 |

- 엔트리와 다르게 한 것과 이유:
  1. 플랜 표 밖 라우트 `GET /lado/setup/items?projectId=&taskId=` 를 더했다 — S11 회고대로 effective 미리보기는 작업자 도구의 항목만 주어, S16 의 프로젝트 규칙 페이지(두 도구 항목의 상속/켜기/끄기)가 읽을 길이 없었다. 인벤토리 응답에도 Global 상태(`items`)와 채택별 `itemCount` 를 실어 S15 가 한 번에 그린다.
  2. 경로 매개변수 이름은 `{id}` 대신 `{itemId}`·`{adoptionId}`·`{acceptanceId}`(기존 LADO 라우트 꼴).
  3. DELETE 두 개(항목 지우기, 수락 취소)의 dry run 은 쿼리 `dryRun`, 응답은 204 대신 200 + 변경 결과(영향·pins 를 보여야 해서). 결정 i 대로 확인이 필요 없어 스펙에 presence 헤더가 없다(서비스에는 빈 본문 확인을 그대로 넘김).
  4. drift 본문은 `{action: keep|revert, sha256, dryRun}`. 모르는 action 은 컨트롤러가 400 `LADO_SETUP_CHANGE_INVALID`(details `field: action`, `reason: unknown`).
  5. 복원 본문은 없음 또는 `{}` 만(필드가 있으면 `INVALID_JSON`), 증명은 그 바이트에.
  6. S11 의 `ScopeConflicts` 에 범위 확인과 정책 천장을 넣었다 — 컨트롤러가 천장을 몰라 Global 충돌 목록에 `LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING` 이 빠지던 틈(S11·S12 남은 틈). `ladosetup.Policy` 에 `Ceiling` 을 더하고 S12 테스트 가짜에 한 메서드를 더했다. 범위가 없거나 보관된 프로젝트는 404 `LADO_SCOPE_NOT_FOUND`(S12 의 코드 그대로).
  7. 부분 채택과 실패한 복원은 결과 없이 오류 envelope 만 보낸다 — details(`stage`·`adoption`·`state`, 복원은 `stage`·`item`)로 충분하고, 서비스가 다시 검사하므로 화면은 인벤토리를 다시 읽으면 된다.
  8. 오류 details 는 서비스가 만든 그대로 낸다(`sessions`·`processes` 는 객체 목록이라 문자열 맵으로 바꾸지 않음). 충돌 결과의 `details` 만 문자열 맵.
  9. 시작 검사는 `wireLadoWork` 안이 아니라 `startLadoSetup` — `ladoTasks.Start` 옆에서 부르고 종료 때 기다린다(디스패처 Start 꼴, 테스트도 배선 뒤 그것을 부름). 테스트 수명보다 오래 도는 고루틴을 남기지 않으려고.
  10. 미리보기의 프로그램은 PID·프로그램 이름만(`ToolProcess.Program()`), 명령줄은 내보내지 않는다(테스트로 고정 — 비밀이 실릴 수 있음).
- 확인한 명령과 결과:
  - `go test -count=1 -run TestLadoAPI_ ./internal/httpd/controllers/` → ok. 새 테스트 7개(필수 5 + `TestLadoAPI_SetupViews`, `TestLadoAPI_EffectiveShowsTheSetup`) PASS. 기존 `TestLadoAPI_Effective` 는 `approved` 의 새 필드(빈 목록)를 기대값에 더했고, `TestLadoAPI_ExecutionDetail` 에 스냅숏 setup 이름 확인을 더했다.
  - 데몬: 필수 `TestWireLadoInstallsTheSetupHooks` — 임시 홈의 도구 폴더로 배선 → 훅 넷이 같은 서비스로 설치, 시작 검사가 그 Claude 폴더를 읽고 끝난 세션 홈을 지움, 만든 Codex 서버가 `-c mcp_servers.search={url = "https://search"}`·Chat 서버로, AGENTS.md 가 지침 절로, CLAUDE.md 가 홈으로 감, 정책이 setup 을 해석(`aoSystemSha256`), Global 충돌에 정책 천장(auto) 위 `defaultMode` 경고. `go test -count=1 ./internal/daemon/` 전체 ok.
  - `go-tests-isolated ./internal/service/ladosetup/ TestViewsShowItemsVersionsAndAdoptions TestScopeViewsCheckTheScope` → GO_TESTS_OK 2/2; `go-pkg-isolated` ladosetup·ladopolicy·ladotask → GO_PKG_OK.
  - golangci-lint v2.13.2(캐시 바이너리) daemon·httpd·ladosetup·ladopolicy → 0 issues(처음 나온 `config` import 가림 2건을 고침). gofmt 깨끗. `go.work.sum` 변화 없음.
  - diff-budget: `daemon.go` 13 + `api.go` 5 = 18/60(`lifecycle_wiring.go` 7줄은 예산 밖 파일).
  - 게이트: 코드 커밋 `21b47f466` 에서 `gate-check.sh --run` → GATES=8 MET=8. 엔트리 커밋 뒤 다시 돌린 결과는 그 커밋의 증거로 남는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 라우트 15·DTO·증명 라우트·effective 의 setup·실행 패널 setup 이름·specgen·`npm run api`·데몬 배선(경로는 데몬에서, 훅 넷, 정책 연결, 시작 검사와 홈 정리), 필수 테스트 6개(+4: 컨트롤러 2, 서비스 views 2). 렌더러는 없다. 엔트리 밖은 After 의 "다르게 한 것" 1·6·9·10 과 `deviceClaudeFolder` 공유.
- 남은 틈:
  - 실제 데몬을 띄워 HTTP 로 채택·복원하는 실측은 안 했다(실제 도구 폴더 금지). S15 이후 lab 레시피(격리 데몬 + 임시 HOME)로.
  - 세션 라우트가 spawn 오류를 낼 때 `LADO_SETUP_HOME_FAILED`·`LADO_SETUP_CODEX_FAILED` 가 그대로 나오는지는 `wrapSpawnStage`(`%w: %w`)와 `envelope.WriteError`(`errors.As`)까지만 확인했다 — 세션 컨트롤러 테스트는 없다.
  - S12 의 `scopeProblems`(변경 dry run 의 충돌 증감)는 여전히 천장 없이 비교한다. 정책 천장 길이 생겼으니 고치려면 그 `ConflictInput` 에 `s.ceiling(ctx)` 한 줄(그러면 `DEFAULT_MODE_ABOVE_CEILING` 이 증감에 나옴).
  - `Overview` 는 채택마다 store 질의 1회(채택은 몇 개뿐).
  - 검사 라우트의 홈 정리 실패는 로그만 남고 응답에 없다.
  - 채택 상세의 `backup` 은 매니페스트 전체다(partial 채택에서 지우지 못한 원본도) — 복원 대상은 `items`.
  - S10 의 lint 1건(`session_manager/lado_setup_home_test.go:176` 변수 `real`)은 범위 밖이라 그대로다.
- 다음 단계 메모:
  - **S14(데이터 층·번역·메인 채널)**:
    - operation id: 위 라우트 표. schema 이름(요청): `AdoptLadoSetupRequest`(`entries: LadoSetupAdoptEntryRequest[]`, `planSha256?`, `dryRun?`), `CreateLadoSetupItemRequest`, `UpdateLadoSetupItemRequest`, `SetLadoSetupBindingRequest`, `ResolveLadoSetupDriftRequest`, `AcceptLadoSetupConflictRequest`; 쿼리 `LadoSetupScopeQuery`, `LadoSetupDryRunQuery`. 응답: `LadoSetupInventoryResponse`(`entries`, `roots`, `projects`, `drift`, `adoptions[].itemCount`, `items`, `os`, `verified{tool: bool}`, `scannedAt`), `LadoSetupItemListResponse`, `LadoSetupConflictListResponse`(`LadoSetupConflictResponse`), `LadoSetupAdoptResponse`(`preview: LadoSetupAdoptionPreviewResponse` — `planSha256`, `entries`, `rewrites`, `conflicts`, `preconditions{unverified, sessions, processes, processesIgnored, ok}`, `warnings`), `LadoSetupAdoptionDetailResponse`(`adoption`, `items`, `backupReadable`, `backup`, `report?`), `LadoSetupRestoreResponse`, `LadoSetupItemDetailResponse`(`versions[]{version, content?}`, `managedPath`), `LadoSetupChangeResponse`(`op`, `changeSha256`, `needsPresence`, `applied`, `actor`, `item`, `version?`, `before?`, `after?`, `conflictsAdded`, `conflictsRemoved`, `impact`, `pins`, `binding?`, `acceptance?`, `warnings`), effective `LadoEffectiveSetupResponse`, `LadoSnapshotSetupItemResponse`, `LadoSetupAcceptedConflictResponse`, 실행 `LadoExecutionSetupItemResponse`.
    - 메인 채널: `adoptSetup` 은 `POST /api/v1/lado/setup/adoptions` 에 `{entries, dryRun:true}` → `preview.planSha256`, 도구별 개수는 `preview.entries[].tool`, `preview.conflicts[]` 중 `code == LADO_SETUP_HOOK_PATH_MOVED` 면 묻지 않고 거부, 그 밖의 `severity == blocking` 은 개수만 → 같은 경로에 `{entries, planSha256}` 서명. `restoreSetup` 은 `GET /api/v1/lado/setup/adoptions/{id}` 의 `items.length`(또는 `adoption.itemCount`) → 본문 없이 `POST …/restore` 서명. `changeSetup` 허용 목록은 `POST /api/v1/lado/setup/items`, `PATCH /api/v1/lado/setup/items/{itemId}`, `PUT /api/v1/lado/setup/bindings`, `POST /api/v1/lado/setup/items/{itemId}/drift`; dry run 응답의 `changeSha256`·`needsPresence`·`item.name`·`item.kind`.
    - 오류 코드(번역): `LADO_SETUP_UNAVAILABLE`(503), `LADO_SETUP_NOT_FOUND`(404), `LADO_SETUP_SCOPE_INVALID`(400, 새), `LADO_SCOPE_NOT_FOUND`(404), `LADO_SETUP_ADOPTION_INVALID`, `LADO_SETUP_NOT_ADOPTABLE`, `LADO_SETUP_CHANGED`, `LADO_SETUP_OS_UNVERIFIED`, `LADO_SETUP_TOOL_RUNNING`, `LADO_SETUP_ADOPTION_FAILED`, `LADO_SETUP_HOOK_PATH_MOVED`, `LADO_SETUP_ALREADY_RESTORED`, `LADO_SETUP_RESTORE_FAILED`, `LADO_SETUP_DRIFT_UNREADABLE`, `LADO_SETUP_ITEM_CHANGED`, `LADO_SETUP_CHANGE_INVALID`, `LADO_SETUP_CONTENT_INVALID`, `LADO_SETUP_LOGIN_KEY`, `LADO_SETUP_ITEM_EXISTS`, `LADO_SETUP_FOLDER_ITEM`, `LADO_SETUP_CONFLICT_NOT_FOUND`, `LADO_SETUP_CONFLICT_NOT_ACCEPTABLE`, `LADO_SETUP_HOME_FAILED`, `LADO_SETUP_CODEX_FAILED`, `LADO_SETUP_LOAD_FAILED`, 공유 `LADO_BINDING_SCOPE_INVALID`·`LADO_BINDING_STATE_INVALID`·`LADO_TASK_CANCELLED`·`LADO_PRESENCE_*`, 컨트롤러 `INVALID_JSON`·`INVALID_QUERY`. 경고 `LADO_SETUP_DIRECT_USE_STOPS`, `LADO_SETUP_BASELINE_FAILED`, `LADO_SETUP_MANAGED_WRITE_FAILED`, `LADO_SETUP_PREVIEW_INCOMPLETE`.
    - details 키: `tool`, `kind`, `name`, `reason`, `stage`, `item`, `sessions[{id, tool}]`, `processes[{pid, name, tool}]`, `adoption`, `state`, `root`, `path`, `planSha256`, `os`, `tools`, `op`, `field`, `key`, `limit`, `version`, `code`, `detail`, `scope`, `scopeId`, `projectId`, `taskId`, `task`, `sha256`, `error`.
    - `lado_setup` 이벤트 무효화 대상: 인벤토리(Global `items` 포함), 범위별 `items`·`conflicts`, 항목 상세, effective, 태스크 목록.
  - **S15**: 인벤토리 하나로 OS 줄(`os`, `verified`), 항목 목록(키는 `path`), Global 스위치(`items[].global`·`on`), 채택 카드(`adoptions[].itemCount`·`state`·`restoredAt`)를 그린다. 충돌 카드는 `GET …/conflicts`(Global), 항목 이름은 `details.item` → 인벤토리 `entries[].itemId`/`items[]`.
  - **S16**: 프로젝트 규칙 페이지는 `GET /lado/setup/items?projectId=`(두 도구, `project`·`source`), 태스크 탭은 effective `setup.items`(작업자 도구)와 `approved.setupAdded/Removed/Changed`(이름은 `approved.setup` 과 `setup.items`), 편집기·이력은 `GET /lado/setup/items/{itemId}` 의 `versions[].content.text`/`files`, 폴더 항목은 `managedPath`, 실행 패널은 `snapshot.setup`.
  - **S17(문서)**: TDD §13 에 위 라우트 표와 더한 `GET /lado/setup/items`; HARNESS §9 에 데몬 배선 순서(wireLado → wireLadoWork 의 wireLadoSetup → 세션 복원 전), 시작 `startLadoSetup`, `deviceClaudeFolder` 공유; §10 에 Global 충돌 목록의 천장은 정책 `Ceiling`.
