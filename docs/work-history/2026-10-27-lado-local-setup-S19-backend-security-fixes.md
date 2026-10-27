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

(단계 시작 때 채운다: 시작 HEAD, 환경, 리뷰 항목마다 코드에서 찾은 지금 위치와 재현)

## After — 수행 후 실측

(커밋, 항목마다 고친 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S20 이 쓸 새 코드·details(`LADO_SETUP_PATH_USED_IN_PLACE`, `LADO_SETUP_PAIR_REQUIRED`, `provider_login_env`, 복원 `preconditions`), 확인 창이 다룰 Op 목록)
