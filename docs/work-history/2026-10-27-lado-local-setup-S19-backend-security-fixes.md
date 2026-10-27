# 2026-10-27 LADO 로컬 설정 채택 — S19 리뷰 반영(백엔드 ②): 자격 증명·데스크톱 확인·Codex 비밀

> 체인 `docs/chains/lado-local-setup-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md` · 브랜치 `feat/lado-local-setup` · 소유자 승인 2026-10-27(수정 체인 S18~S20, "이대로 진행")

## 의도/목적 — 왜 이 작업을 하나

리뷰 결과 중 **로그인·자격 증명을 깨거나 확인 없이 코드를 실행할 수 있는 것**을 고친다. 루프백 API 는 인증이 없고 워커도 닿으므로, 코드를 실행하거나 접근을 넓히는 변경이 데스크톱 확인 없이 지나가면 프롬프트 주입된 워커가 앞으로의 모든 Claude 세션에 코드를 심을 수 있다. 소유자 결정(2026-10-27): 확인이 필요한 종류는 **되돌리기를 뺀 모든 변경**(만들기·편집·켜기·끄기·삭제·상속·변경 감지 유지)에 확인을 받는다.

## 범위

- 고친다(백엔드 리뷰 B, 위치는 코드로 다시 찾는다):
  - **P1 소유자 결정 + B5 — 확인 판정을 내용 기준 허용 목록으로**(`domain/lado_setup.go`, `changes.go`, 바인딩·삭제·유지 경로):
    - 확인이 필요한 항목(이하 "확인 종류"): `hook_script`, `mcp_server`; `setting` 은 **안전 키 허용 목록 밖의 모든 키**(허용 목록: `theme`, `verbose`, `outputStyle`, `model`, `modelSettings`, `includeCoAuthoredBy`, `showClearContextOnPlanAccept`, `inputNeededNotifEnabled`, `agentPushNotifEnabled`, `preferredNotifChannel`, `spinnerTipsEnabled`, `cleanupPeriodDays`, `language` — 그 밖, 예 `disableAllHooks`·`otelHeadersHelper`·`enableAllProjectMcpServers`·`enabledMcpjsonServers` 는 확인); `command`·`skill`·`agent` 는 그 버전 내용의 front matter 에 `allowed-tools`·`hooks` 가 있거나 본문에 셸 실행 표기(`` !` ``)가 있을 때.
    - 확인 종류의 변경은 되돌리기(`drift_revert`)를 뺀 모든 Op(만들기·편집·삭제·모든 바인딩 상태·변경 감지 유지)에서 `needsPresence = true`. 판정 함수는 하나(`domain.LadoSetupNeedsPresence(kind, name, content)` 꼴)로 모으고 S12 의 "켜질 때만" 분기를 없앤다.
  - **B3 옮긴 스크립트 참조를 모든 곳에서**(`conflicts.go`, `adopt.go`): `movedPathReferences` 를 hooks·statusLine 만이 아니라 **모든 settings 키의 모든 문자열 값**(제자리에 남는 로그인 키 `apiKeyHelper`·`awsAuthRefresh`·`awsCredentialExport`, `otelHeadersHelper`, `env` 값 포함)과 **모든 MCP 서버의 `command`·`args`·`env`·`url`** 에 돌린다. 셸 명령인 값(hooks·statusLine·`otelHeadersHelper`)이 함께 옮겨지면 지금처럼 `"$CLAUDE_CONFIG_DIR"/…` 로 다시 쓰고, 제자리에 남는 값(로그인 키·keep 항목)이나 셸을 거치지 않는 값(MCP `command`/`args`)이 옮겨질 파일을 가리키면 blocking `LADO_SETUP_PATH_USED_IN_PLACE`(details `key`, `path`) — 그 파일을 제자리에 두게 한다.
  - **B4 로그인 env**(`domain/lado_setup.go`, `scan_claude.go`, `changes.go`): `env` 항목에서 빼고 제자리에 두는 변수를 접두·이름 규칙으로 넓힌다 — `ANTHROPIC_` 전부, `CLAUDE_CODE_USE_*`, `CLAUDE_CODE_OAUTH_TOKEN`, `AWS_` 전부, `CLOUD_ML_REGION`, `VERTEX_REGION_*`, `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`, `AZURE_` 전부, `HTTP_PROXY`·`HTTPS_PROXY`·`NO_PROXY`(대소문자 둘 다). 공급자 스위치(`CLAUDE_CODE_USE_BEDROCK`·`_VERTEX`·`_FOUNDRY`)가 켜져 있으면 `env` 전체를 채택 불가(사유 `provider_login_env`). 만들기·편집도 같은 규칙으로 거부(`LADO_SETUP_LOGIN_KEY`). S05 의 `TestClaudeLoginKeysMatchAgentAccount` 는 "도메인 목록 ⊇ agentaccount 목록"으로 바꾼다.
  - **B6 Codex 비밀**(`scan_codex.go`, `codex.go`, `managed.go`): `literalCredentials` 에 `args` 안의 헤더·토큰 표기(`Authorization`, `Bearer `, `--header`/`-H` 다음 값, `token=`, `api_key`/`apikey`, `secret`, `password`, `x-api-key`, 대소문자 무시)와 `url` 의 쿼리 문자열·사용자 정보를 더한다(채택·만들기 거부). `checkCodexOverride` 와 그 밖의 오류 문장에 서버 내용을 넣지 않는다(이름과 코드만).
  - **천장**: 변경 dry run 의 충돌 증감(`scopeProblems`)에 권한 천장을 넣는다(`ConflictInput.Ceiling` = 정책의 천장, S13 메모의 한 줄).
  - **AGENTS 짝**: Codex 홈에 `AGENTS.md` 와 `AGENTS.override.md` 가 둘 다 있으면 미리보기·채택은 둘을 함께만(하나만 고르면 400 `LADO_SETUP_PAIR_REQUIRED`, details `name`, `pair`); 지침 절은 Codex 처럼 override 가 있으면 그것.
  - **미검증 OS 의 만들기**: `create` 는 `domain.LadoSetupDeliveryVerified` 가 거짓이면 409 `LADO_SETUP_OS_UNVERIFIED`(채택과 같은 코드).
  - **복원 조회의 전제 조건**: `GET /lado/setup/adoptions/{id}` 응답에 `preconditions`(실행 중인 AO 세션·바깥 프로세스, 채택 미리보기와 같은 모양) — 프런트(S20)가 확인 창 전에 보여 준다. DTO·specgen·`npm run api`.
  - **nit**: 바깥 프로세스 표지에 `@anthropic-ai/claude-agent-sdk` 를 더한다.
  - **증명 먼저(변경)**: S18 회고의 마지막 틈 — 변경(`changes.go` 의 `changeApproval`)도 채택처럼 데스크톱 확인 증명의 서명·만료·재사용을 dry run 계산(영향·검사) **전에** 확인한다(증명 수명 60 s). 테스트 `TestEveryChangeToCodeRunningItemsNeedsPresence` 에 '느린 영향 계산에도 증명이 만료되지 않음' 사례를 더한다.
- 하지 않는다: 데이터·전달(S18 — 끝남), 프런트(S20).

## 절차

0. 시작 전에 이 체인 S18 과 앞 체인 엔트리들(특히 S03·S05·S06·S07·S10·S12·S13)의 `## 비교/회고` 와 "다음 단계 메모"를 읽는다. S18 이 바꾼 확인 순서(증명 먼저 → 다시 검사 → pending 기록 → 제거)를 깨지 않는다.
1. 항목마다 실패하는 테스트를 먼저 쓰고 고친다. 확인 판정 표 테스트(`TestLadoSetupNeedsPresence`)는 새 규칙(허용 목록·내용 기준·모든 Op)으로 다시 쓴다.
2. HTTP 쪽은 응답 DTO 와 컨트롤러 테스트(`TestLadoAPI_SetupChangesPassTheExactBody` 등)가 새 코드·details 를 그대로 싣는지만 고친다.
3. 커밋 메시지 예: `fix(lado): confirm every change to code-running setup`, `fix(lado): keep provider logins and Codex secrets in place`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-local-setup-S19-backend-security-fixes.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

`domain`:

- `TestLadoSetupNeedsPresence` — 새 표: 허용 목록 키는 확인 없음, 그 밖 키(`disableAllHooks` 등) 확인, hook·MCP 확인, command·skill·agent 는 `allowed-tools`·`hooks`·`` !` `` 가 있을 때만

`service/ladosetup`(격리 홈):

- `TestEveryChangeToCodeRunningItemsNeedsPresence` — 소유자 결정: 확인 종류의 만들기·편집·켜기·끄기·삭제·상속·유지는 증명 없이 403, 되돌리기는 증명 없이 됨, 안전 키는 모두 증명 없이 됨
- `TestMovedPathReferencesEverywhere` — B3: 로그인 키·env 값·MCP command/args 가 옮길 스크립트를 가리키면 blocking, 함께 옮기는 셸 값은 다시 쓰기
- `TestProviderLoginEnvStaysInPlace` — B4: `AWS_*`·Vertex·Foundry·프록시 변수는 env 항목에 없고 원래 파일에 남음, 공급자 스위치가 켜지면 env 채택 불가, 만들기·편집 거부
- `TestCodexSecretsInArgsAreNotAdoptable` — B6: 헤더·토큰 args, 쿼리 문자열 URL 은 `literal_credentials`, 오류 문장에 내용 없음
- `TestChangeDryRunChecksTheCeiling` — 천장 위 `defaultMode` 가 충돌 증감에 나옴
- `TestAgentsPairIsAdoptedTogether` — 짝 규칙과 지침 절의 override 우선
- `TestCreateRefusedOnUnverifiedOS`

`httpd/controllers`:

- `TestLadoAPI_AdoptionShowsRestorePreconditions` — 복원 조회에 실행 중 목록

## 수용 게이트

- [ ] S19-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S19-G1: 도메인 표 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/domain/ TestLadoSetupNeedsPresence
  EXPECT: GO_TESTS_OK 1/1
- [ ] S19-G2: ladosetup 필수 테스트 7개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestEveryChangeToCodeRunningItemsNeedsPresence TestMovedPathReferencesEverywhere TestProviderLoginEnvStaysInPlace TestCodexSecretsInArgsAreNotAdoptable TestChangeDryRunChecksTheCeiling TestAgentsPairIsAdoptedTogether TestCreateRefusedOnUnverifiedOS
  EXPECT: GO_TESTS_OK 7/7
- [ ] S19-G3: 컨트롤러 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_AdoptionShowsRestorePreconditions TestLadoAPI_SetupChangesPassTheExactBody TestLadoAPI_AdoptNeedsPresence
  EXPECT: GO_TESTS_OK 3/3
- [ ] S19-G4: ladosetup 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S19-G5: 바뀐 패키지 전체 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/domain/ ./internal/httpd/... ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/adapters/agent/codex/
  EXPECT: GO_PKG_OK
- [ ] S19-G6: npm run api 가 커밋과 같다
  CHECK: bash .claude/gate/lado-local-setup.sh api-clean
  EXPECT: API_CLEAN
- [ ] S19-G7: lint 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-lint ./internal/service/ladosetup/... ./internal/domain/... ./internal/httpd/... ./internal/service/ladopolicy/...
  EXPECT: GO_LINT_OK
- [ ] S19-G8: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S19-G9: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S19-G10: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: 세션 시작 스냅숏은 `b0c4eeec5`(S18 게이트 기록). 그 직후 메인 세션이 `287a237c7`(10:50 KST)로 이 엔트리 범위에 "증명 먼저(변경)" 한 줄을 더했다 — 실제 시작점은 `287a237c7`, 트리 깨끗.
- 환경: Ubuntu, go1.27.1, 실제 도구 폴더·`~/.ao` 는 건드리지 않음(모든 테스트는 `t.TempDir()` 홈, 게이트는 `go-tests-isolated`).
- 재현은 코드 읽기와 그때의 테스트 기대값으로 했다(고치기 전 코드에서 새 테스트를 따로 돌려 보지는 않음; 순서 테스트 둘은 아래에 적은 대로 옛 순서에서 실패하는 모양으로 짰다).
- 항목별 지금 위치:
  - P1·B5: `domain/lado_setup.go:572-597` `LadoSetupNeedsPresence(kind, name)` — hook·MCP 와 settings 거부 목록 9개(`ladoSetupPresenceSettings`)만. `changes.go:500`(만들기)·`:551`(편집)·`:617-633`(바인딩은 on/force_on 과 "켜지는 inherit" 만, `onIn` 으로 판정)·`prepareDelete`(판정 없음)·`:979`(drift keep), `drift.go:229`(`keepDrift`). 옛 `TestChangePresenceRules` 가 "hook script 삭제·Global off·force_off 는 확인 없이 api" 를 기대 — 리뷰가 짚은 틈 그대로. `disableAllHooks`·`sandbox` 같은 키, front matter `allowed-tools`/`hooks`·`` !` `` 가 있는 command·skill·agent 는 확인 없이 만들어졌다.
  - B3: 옮긴 경로 찾기는 `conflicts.go:435`(hooks)·`:480`(statusLine)뿐, 다시 쓰기는 `rewrite.go:32` `rewritable = hooks, statusLine`. `adopt.go:551-563` 의 제자리 검사는 status adoptable 인 키만 — 로그인 키(`apiKeyHelper` 등, never)·옮기는 env 에 남는 로그인 변수·MCP 서버의 command/args/env/url 은 아무도 보지 않았다.
  - B4: `domain/lado_setup.go:612-618` `LadoSetupClaudeLoginEnv` 7개 이름(agentaccount 목록과 같음). 쓰는 곳 `scan_claude.go:197-226`(`withoutLoginEnv`)·`changes.go:1338`·`adopt.go:991`·`restore.go:876`·`home.go:709`. `AWS_*`·Vertex·Azure·프록시 변수는 env 와 함께 옮겨졌고, 공급자 스위치가 켜진 env 도 채택 가능했다.
  - B6: `scan_codex.go:167-179` `literalCredentials` 는 env 값·`http_headers`·`bearer_token` 만. `managed.go:421`·`:433` `checkCodexOverride` 오류가 `%w`(TOML 오류)와 `got/want` 서버 내용을 그대로 실었다(로그·검증 결과로 감).
  - 천장: `changes.go:1039-1062` `scopeProblems` 의 `ConflictInput` 에 `Ceiling` 없음(`ChangeResult` 주석도 "left out").
  - AGENTS 짝: 검사 없음. `codex.go:73-90` `CodexInstructions` 는 이미 override(내용 있음) 우선.
  - 미검증 OS 만들기: `prepareCreate`(`changes.go:476`)에 OS 검사 없음.
  - 복원 조회: `views.go:205` `Adoption` 에 전제 조건 없음; `restore.go:307` `confirmRestore` 안에서만 계산.
  - 증명 먼저(변경): `Apply`(`changes.go:241-294`)·`applyDrift` 는 잠금 → prepare → preview(충돌 증감·영향) → `changeApproval` 순 — 증명(60 s)을 느린 계산 뒤에 확인, 재사용 검사는 store 쓰기 때만.
  - nit: `processes.go:53-55` 표지에 `@anthropic-ai/claude-agent-sdk` 없음.

## After — 수행 후 실측

- 커밋(모두 `feat/lado-local-setup`):
  - `3e4cd1740` fix(lado): confirm every change to code-running setup — P1·B5·B4·천장·미검증 OS 만들기·nit
  - `731d961e5` fix(lado): keep moved-script references and Codex secrets safe — B3·B6
  - `92da19a33` fix(lado): adopt Codex AGENTS files together and show restore preconditions — AGENTS 짝·복원 조회 `preconditions`·DTO·`npm run api`
  - `fec629cf0` docs(lado): describe the setup confirmation rule as it now works — 내장 규칙 문장·라우트 요약
  - `c6befc870` fix(lado): check a setup change's proof before its slow checks — 증명 먼저(변경)
- 항목별로 고친 것:
  - **P1·B5**: 판정 하나 `domain.LadoSetupNeedsPresence(kind, name, content)` — hook·MCP 항상, setting 은 안전 키 13개(`ladoSetupSafeSettings`) 밖 전부, command·skill·agent 는 내용(skill 은 `SKILL.md`)의 front matter 키 `allowed-tools`/`hooks`(대소문자·`-`/`_` 무시) 또는 `` !` ``. 읽을 수 없는 내용·파싱 안 되는 front matter 는 확인 필요. 서비스는 `versionNeedsPresence`(blob 에서 읽음, 못 읽으면 true)·`managedNeedsPresence`(drift 사본). 만들기(새 내용)·편집(이전 **또는** 새 내용)·삭제·모든 바인딩 상태(noop 은 제외)·drift keep(최신 또는 관리 사본) 에 적용, revert·수락·철회는 없음. S12 의 "켜질 때만" 분기와 `onIn` 을 지웠다.
  - **B3**: 새 blocking 코드 `LADO_SETUP_PATH_USED_IN_PLACE`(details `key`·`path`, 그리고 `item` 또는 `origin`). 검사 `pathsUsed` 가 hooks·statusLine 밖 settings 값의 모든 문자열(`env.KEY`, `permissions.allow[0]` 꼴의 key)과 MCP 서버(두 도구)의 `command`·`args[i]`·`env.KEY`·`url` 을 본다. `rewritable` 에 `otelHeadersHelper`(문자열 값)를 더해 함께 옮기면 `"$CLAUDE_CONFIG_DIR"/…` 로 다시 쓴다. 채택 충돌은 제자리에 남는 키 항목을 status adoptable·never·not_adoptable 모두에서 읽고(`inPlaceValue`: 안 옮기면 원래 값 그대로, 옮기는 env 면 남는 로그인 변수만) `movedPathCodes`(HOOK_PATH_MOVED·PATH_USED_IN_PLACE)를 남긴다. `confirm` 이 둘 다 409 로 거부.
  - **B4**: `domain.LadoSetupIsClaudeLoginEnv(name)`(대소문자 무시: `ANTHROPIC_`·`CLAUDE_CODE_USE_`·`AWS_`·`VERTEX_REGION_`·`AZURE_` 접두, `CLAUDE_CODE_OAUTH_TOKEN`·`CLOUD_ML_REGION`·`GOOGLE_APPLICATION_CREDENTIALS`·`GOOGLE_CLOUD_PROJECT`·`HTTP_PROXY`·`HTTPS_PROXY`·`NO_PROXY`)가 옛 목록을 대신한다(쓰는 곳 다섯 모두). `LadoSetupIsClaudeProviderSwitchOn` 이 켜져 있으면 env 는 `not_adoptable`/`provider_login_env`(새 사유). 만들기·편집은 `LADO_SETUP_LOGIN_KEY`. S05 테스트는 "agentaccount 의 각 이름이 도메인 판정에 든다"로.
  - **B6**: `literalCredentials` 에 `secretArgs`(표지 `authorization`·`bearer `·`token=`·`api_key`·`apikey`·`api-key`·`x-api-key`·`secret`·`password`, `--header`/`-H`/`--token` 다음 값과 `=`·붙여 쓴 꼴, 문자열 아닌 인자)와 url 의 사용자 정보·쿼리(파싱 실패나 `?`·`@` 도). `checkCodexOverride` 오류는 서버 이름만.
  - **천장**: `scopeProblems` 가 `s.ceiling(ctx)` 를 `ConflictInput.Ceiling` 에 넣는다.
  - **AGENTS 짝**: `checkInstructionPairs` — Codex 홈에 짝이 있으면(상태 무관) 같은 action 으로 둘 다 요청해야 하고, 아니면 400 `LADO_SETUP_PAIR_REQUIRED`(details `name`, `pair`). 미리보기와 채택 모두(`plan` 안, S18 순서상 증명 확인 뒤). 지침 절은 원래대로 override(내용 있음) 우선 — 테스트로 고정.
  - **미검증 OS 만들기**: `prepareCreate` 에서 409 `LADO_SETUP_OS_UNVERIFIED`(details `os`, `tools`), dry run 도.
  - **복원 조회**: `AdoptionView.Preconditions`(`restorePreconditions` — `confirmRestore` 와 같은 함수, `unverified` 는 늘 비움). 복원됐거나 백업을 못 읽으면 nil. DTO `LadoSetupAdoptionDetailResponse.preconditions?`(채택 미리보기의 `LadoSetupPreconditionsResponse`, 공통 변환 `ladoSetupPreconditionsResponse`).
  - **증명 먼저(변경)**: 실제 요청은 잠금 전에 한 번 prepare 해 확인이 필요하면 `changeApproval`(서명·만료는 presence 함수, 재사용은 `LadoPresenceProofUsed` → `LADO_PRESENCE_REPLAYED`)을 먼저 하고, 잠금 안에서 다시 prepare(그 사이 바뀌어 새로 확인이 필요하면 그때 받음) → preview → 쓰기. drift keep 은 preview 전에 확인.
  - **nit**: 표지에 `@anthropic-ai/claude-agent-sdk`.
- 엔트리와 다르게 한 것과 이유:
  1. hooks·statusLine 이 옮긴 경로를 다시 쓰지 못할 때(제자리 keep 포함)는 원래 코드 `LADO_SETUP_HOOK_PATH_MOVED` 그대로 두고, 그 밖의 값만 `PATH_USED_IN_PLACE` 로 했다 — 기존 테스트·S14 메인 채널이 그 코드를 쓴다. 둘 다 blocking, 둘 다 적용 거부.
  2. 충돌 수락·철회는 확인 없음 그대로(소유자 결정의 Op 목록에 없음, 실행되는 것을 바꾸지 않음).
  3. 편집은 이전·새 내용 중 하나라도 확인 종류면 확인(안전하게 만드는 편집도 확인 종류의 변경).
  4. 로그인 env 판정은 프록시만이 아니라 전부 대소문자 무시(Windows 와 `checkSettingValue` 의 기존 꼴). 공급자 스위치는 `""`·`0`·`false`·`no`·`off`·null·0·false 만 꺼짐, 그 밖(읽을 수 없는 값 포함)은 켜짐.
  5. AGENTS 짝은 action 이 다르면 같은 오류, 짝이 채택 불가 상태여도 혼자는 채택 안 됨.
  6. 내장 규칙 `setup.change.requires_presence` 의 문장을 새 규칙으로 고쳤다(`ladopolicy/builtins.go`, 카탈로그 테스트). `EnsureBuiltins` 는 없는 것만 심으므로 이미 심은 DB 는 옛 문장을 유지한다.
  7. 테스트 이름: `TestChangePresenceRules` → `TestEveryChangeToCodeRunningItemsNeedsPresence`(사례 확장 + 증명 먼저 하위 테스트), `TestBindingTurningOnACodeItemNeedsPresence` → `TestEveryBindingOfACodeItemNeedsPresence`. 더한 테스트: domain `TestLadoSetupClaudeLoginEnv`, service `TestAdoptionViewShowsRestorePreconditions`, 충돌 표에 PATH_USED_IN_PLACE 행 7개, 컨트롤러 표에 새 코드 4개(OS_UNVERIFIED·LOGIN_KEY·PATH_USED_IN_PLACE·PAIR_REQUIRED).
- 확인한 명령과 결과:
  - `go test ./internal/service/ladosetup/` ok(27 s), `go test ./internal/domain/ ./internal/httpd/... ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/adapters/agent/codex/ ./internal/adapters/agentaccount/` ok
  - `npm run api` → `openapi.yaml`·`schema.ts` 갱신 후 커밋, `npm --prefix frontend run typecheck` 통과
  - 게이트: `fec629cf0` 뒤 한 번(11/11 MET, 2 분 20 초), 마지막 코드 커밋 `c6befc870` 뒤 다시 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-local-setup-S19-backend-security-fixes.md` → G0~G10 모두 MET, `GATES=11 MET=11 UNMET=0`. 엔트리 커밋 뒤 같은 명령으로 증거를 HEAD 에 다시 남긴다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 전부(P1·B5, B3, B4, B6, 천장, AGENTS 짝, 미검증 OS 만들기, 복원 조회 전제 조건, nit, 그리고 메인 세션이 더한 "증명 먼저(변경)"), 필수 테스트 9개(+ domain 1, service 1, 표 행들). 데이터·전달(S18)·프런트(S20)는 손대지 않음. 엔트리 밖은 After "다르게 한 것" 1~7.
- 남은 틈:
  - 판정은 휴리스틱이다: front matter 는 `allowed-tools`/`hooks` 키와 `` !` `` 만 본다(agent 의 `tools` 는 좁히는 것이라 셈하지 않음). 여러 파일 skill 은 `SKILL.md` 만 본다 — 폴더 안 스크립트만으로는 확인 종류가 아니다. Codex 비밀 표지는 부분 문자열이라 `secret` 이 든 평범한 경로 인자도 거절된다(보수 쪽).
  - `PATH_USED_IN_PLACE` 는 채택뿐 아니라 범위 충돌(`scopeProblems`·effective)에도 나온다 — 관리 항목 값(예: `permissions.allow` 의 `Bash(~/.claude/hooks/x.sh)`)이 옮긴 경로를 가리키면 blocking 문제로 스냅숏에 들어간다. 실제로 깨진 참조라 그대로 두었다.
  - 로그인 env 판정이 넓어져서, S19 이전(lab) 채택에서 env 와 함께 옮겨진 `AWS_REGION` 같은 변수는 복원·홈 병합이 이제 "로그인 변수"로 본다(`restore.go` `adoptedEnv`, `home.go` `withLoginEnvOf`). 병합 전 브랜치라 lab 데이터뿐.
  - 변경의 실제 요청은 잠금 전에 prepare 를 한 번 더 한다(store·blob 읽기 두 번). 증명은 잠금 대기·검사 전에 확인하고, 그 뒤 store 는 만료를 다시 보지 않고 증명을 기록만 한다(재사용은 그 트랜잭션이 다시 막음). 잠금 안의 prepare 가 새로 확인을 요구하면(그 사이 항목이 바뀜) 그때 증명을 받는데, 이 경우만 검사 전 순서가 아니다 — 대신 preview 전이다.
  - 내장 규칙 문장은 새로 심는 DB 에서만 바뀐다(이미 심은 lab DB 는 옛 문장).
  - 복원 조회의 `preconditions` 는 조회 때마다 프로세스 목록(`procmem`)을 읽는다.
- 다음 단계 메모:
  - **S20(프런트)**:
    - 새 코드·details: `LADO_SETUP_PATH_USED_IN_PLACE`(채택 409, details `key`·`path`·`origin`|`item`; 충돌 코드로도 나옴, blocking — 메인 `adoptSetup` 은 `LADO_SETUP_HOOK_PATH_MOVED` 처럼 묻지 않고 거부해야 함), `LADO_SETUP_PAIR_REQUIRED`(400, details `name`·`pair` — 채택 창에서 AGENTS 둘을 함께 고르게), `LADO_SETUP_OS_UNVERIFIED` 가 이제 `POST /lado/setup/items`(dry run 포함)에서도 나옴(details `os`·`tools`), `LADO_SETUP_LOGIN_KEY` 의 `key` 는 이제 `AWS_REGION`·`https_proxy` 같은 이름도, 인벤토리 사유 새 값 `provider_login_env`(공급자 스위치가 켜진 env), `literal_credentials` 는 Codex args 의 헤더·토큰과 url 쿼리·사용자 정보도 뜻함, `LADO_PRESENCE_REPLAYED` 가 변경에서 검사 전에 나올 수 있음.
    - 복원 조회 `GET /lado/setup/adoptions/{id}` 의 `preconditions?` = `{os, unverified: [], sessions[{id, tool}], processes[{pid, name, tool}], processesIgnored, ok}` — 복원됐거나 백업을 못 읽으면 없음. 확인 창 전에 보여 주고 `ok` 가 거짓이면 메인 `restoreSetup` 이 묻지 않고 거부할 수 있다.
    - 확인 창이 다룰 Op: dry run 의 `needsPresence` 가 확인 종류(hook·MCP·안전 키 밖 setting·도구/훅/`` !` `` 가 있는 command·skill·agent)에서 **만들기·편집·삭제·바인딩(on·off·force_on·force_off·inherit, noop 제외)·drift keep** 에 참; drift revert·충돌 수락·철회는 늘 거짓. **`DELETE /api/v1/lado/setup/items/{itemId}` 를 메인 `changeSetup` 허용 목록에 넣어야 한다**(지금 목록은 POST items·PATCH items/{id}·PUT bindings·POST drift 뿐) — 렌더러의 삭제·끄기·inherit 도 dry run 의 `needsPresence` 를 따라 확인 창으로 가야 한다(“끄기는 확인 없음” 가정 금지).
    - 안전 키(확인 없음): `theme`, `verbose`, `outputStyle`, `model`, `modelSettings`, `includeCoAuthoredBy`, `showClearContextOnPlanAccept`, `inputNeededNotifEnabled`, `agentPushNotifEnabled`, `preferredNotifChannel`, `spinnerTipsEnabled`, `cleanupPeriodDays`, `language`.
    - 내장 규칙 `setup.change.requires_presence` 문장이 바뀌었다 — 로캘에 번역문이 있으면 맞출 것.
  - **문서(as built)**: HARNESS §9.5/결정 i — 소유자 결정 2026-10-27(되돌리기 뺀 모든 변경, 안전 키 허용 목록, 내용 기준 판정), 결정 g — `otelHeadersHelper` 다시 쓰기와 `PATH_USED_IN_PLACE`, 결정 e — 로그인 env 규칙과 `provider_login_env`, 결정 d — Codex args·url 비밀, AGENTS 짝, 만들기도 검증된 OS 만, 복원 조회 전제 조건, 변경도 증명 먼저.
