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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 라우트 표, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S14 가 쓸 operation id·schema 이름·오류 코드와 details 키)
