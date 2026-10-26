# 2026-10-26 LADO 로컬 설정 채택 — S10 Codex 전달: `-c` 덧씌우기·Chat MCP 목록·지침 절

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 c·d, "Facts measured", §10) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

Codex 는 로그인이 `CODEX_HOME` 경로에 묶여 있어 세션별 홈을 줄 수 없다. 그래서 실제 `CODEX_HOME` 을 그대로 두고, 채택한 MCP 서버는 TUI 에 `-c mcp_servers.<이름>=<인라인 표>`, Chat 에 AO 가 이미 `ao` 서버를 넘기는 MCP 목록으로, 채택한 `AGENTS.md` 는 두 경로 모두 AO 시스템 프롬프트의 절로 넘긴다(S01 이 `-c` 층을 실측). LADO 실행은 스냅숏의 고정 버전을 쓴다.

## 범위

- 만든다: `service/ladosetup/codex.go`(덧씌우기 값·서버 목록·지침 문구), 테스트.
- 바꾼다(훅 줄, diff 예산 안): `adapters/agent/codex/codex.go`(덧씌우기 훅 설정자와 `GetLaunchCommand`·복원 명령의 호출), `session_manager` 의 Chat MCP 목록을 만드는 곳(`chat_spawn.go` 두 곳, `agent_switching_chat.go` 한 곳 — `ao` 서버 옆), `service/ladotask/plan_text.go` 또는 `PromptSource` 가 있는 LADO 파일(Codex 세션의 지침 절).
- 하지 않는다: Claude(S09), 스냅숏 쓰기(S11), 배선(S13).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S01 의 `-c` 값 모양 메모를 먼저 본다.
1. `ladosetup` 쪽(`codex.go`):
   - `CodexOverrides(ctx, sessionID, clientRequestID, projectID) ([]string, error)` → `["-c", "mcp_servers.<이름>=<인라인 표>", …]`. 항목 고르기는 S09 의 규칙과 같다(실행이면 고정 버전, 아니면 그 프로젝트에서 켜진 지금 버전). 관리 항목이 없으면 빈 목록.
   - 인라인 표는 이 파일의 작은 정규 인코더로 쓴다(문자열은 TOML 기본 문자열 이스케이프, 정수·불리언·배열·중첩 표는 `{k = v, …}`, 키는 정렬, 키가 맨 이름이 아니면 따옴표). `github.com/pelletier/go-toml/v2` 로 `x = <값>` 을 다시 파싱해 원래 값과 같은지 테스트한다.
   - `CodexChatServers(ctx, …) ([]ports.ChatMCPServerConfig, error)` — 같은 항목을 `ports.ChatMCPServerConfig`(stdio 는 Command·Args·Env, http 는 Type·URL·Headers)로.
   - `CodexInstructions(ctx, …) (string, error)` — 켜진 `AGENTS.override.md` 가 있으면 그것, 없으면 `AGENTS.md` 의 텍스트.
2. codex 어댑터 훅(`adapters/agent/codex/codex.go`): 패키지 수준 설정자 `SetLadoOverrides(func(ctx context.Context, sessionID string) ([]string, error))`(nil 이면 업스트림 그대로). `GetLaunchCommand` 와 복원 명령 빌더가 `providerArgs` 끝에 결과를 붙인다; 오류면 명령 만들기 실패. 세션 id 는 `cfg.SessionID`. (설정자 꼴이 이 패키지에 맞지 않으면 `Plugin` 필드 + 설정자로 해도 된다 — After 에 이유.) 덧씌우기 함수는 데몬 배선(S13)에서 `ladosetup` 을 감싸 세션 id 로 client request id·프로젝트를 읽는다.
3. Chat MCP 목록: `session_manager` 가 Codex 세션의 MCP 서버 목록을 만드는 세 곳(`ao` 서버를 넣는 곳)에서 선택적 훅 `ladoChatServers(ctx, rec) ([]ports.ChatMCPServerConfig, error)` 의 결과를 덧붙인다. Claude 세션에는 붙이지 않는다(홈의 `.claude.json` 으로 감). 이름이 `ao` 와 겹치는 서버는 S06 이 blocking 으로 막지만, 여기서도 `ao` 는 건너뛰고 로그를 남긴다.
4. 지침 절: LADO 프롬프트 출처(`ladotask.PromptSource` — 시스템 프롬프트의 추가 절을 주는 곳)가 Codex 세션이면 `## User instructions (managed by LADO)` 와 `CodexInstructions` 의 텍스트를 덧붙인다(빈 텍스트면 절 없음). 세션의 harness 는 세션 기록에서. 오케스트레이터·일반 세션에도 적용(LADO 모드와 무관), 실행은 고정 버전.
5. 커밋 메시지 예: `feat(lado): deliver managed setup to Codex sessions as overrides`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S10-codex-delivery.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestCodexOverridesInlineTables`(ladosetup, 격리) — stdio·url 서버, 중첩 표, 이스케이프가 필요한 문자열, 다시 파싱하면 같은 값, 이름 정렬, 관리 항목 없으면 빈 목록
- `TestCodexDeliveryUsesPinnedVersions`(ladosetup, 격리) — 실행은 고정 버전, 일반 세션은 지금 버전과 프로젝트 바인딩
- `TestCodexAdapterAppendsLadoOverrides`(adapters/agent/codex) — 새 실행·복원 argv 끝에 `-c` 쌍, 훅 오류는 실패, nil 훅은 업스트림 argv 그대로
- `TestChatMCPListsCarryAdoptedCodexServers`(session_manager) — Codex 세션은 `ao` 와 채택 서버, Claude 세션은 `ao` 만
- `TestPromptSectionCarriesAdoptedAgentsMd`(ladotask) — Codex 세션은 절이 있고 Claude 세션은 없음, 실행은 고정 텍스트, 빈 텍스트면 절 없음

## 수용 게이트

- [ ] S10-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S10-G1: ladosetup 필수 테스트 2개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestCodexOverridesInlineTables TestCodexDeliveryUsesPinnedVersions
  EXPECT: GO_TESTS_OK 2/2
- [ ] S10-G2: 어댑터 필수 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/adapters/agent/codex/ TestCodexAdapterAppendsLadoOverrides
  EXPECT: GO_TESTS_OK 1/1
- [ ] S10-G3: session_manager·ladotask 필수 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/session_manager/ TestChatMCPListsCarryAdoptedCodexServers && bash .claude/gate/lado-local-setup.sh go-tests ./internal/service/ladotask/ TestPromptSectionCarriesAdoptedAgentsMd
  EXPECT: GO_TESTS_OK 1/1
- [ ] S10-G4: 바뀐 패키지 전체 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/adapters/agent/codex/ ./internal/service/ladotask/ ./internal/adapters/chatdriver/codexappserver/
  EXPECT: GO_PKG_OK
- [ ] S10-G5: 업스트림 파일 수정은 작다(S09 몫 포함한 누적)
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 220 backend/internal/session_manager/manager.go backend/internal/session_manager/chat_spawn.go backend/internal/session_manager/agent_switching.go backend/internal/session_manager/agent_switching_chat.go backend/pkg/agentcreds backend/internal/adapters/agent/codex
  EXPECT: BUDGET_OK
- [ ] S10-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S10-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `45ad56ba7`(S09 기록), 트리 깨끗. Linux, go1.27.1, 체인 정의 `DEFS_UNCHANGED`. 이 기계에 codex 실행 파일 없음(실측은 S01 몫으로 끝남).
- 업스트림 예산 출발점: `diff-budget 220 …`(G5 목록) = `BUDGET_OK 102/220` — S06 `codex/hooks.go` +10, S09 manager 11·chat_spawn 9·agent_switching 3·agent_switching_chat 4·agentcreds 65. 남은 118줄 안에 codex 어댑터 훅·테스트, Chat 목록 세 곳이 들어가야 한다(테스트 파일도 `adapters/agent/codex` 폴더라 셈에 든다).
- 출발점 코드:
  - `GetLaunchCommand`·`GetRestoreCommand`(`adapters/agent/codex/codex.go:118`, `:147`)는 `providerArgs` 를 hook 플래그 → 터미널 플래그 → `model_reasoning_effort` 순으로 쌓아 `agentruntime` 에 넘긴다. 복원은 `codex resume <공통 -c…> <providerArgs> … <thread>` 꼴이라 `-c` 는 하위 명령 뒤에 온다(AO 의 다른 `-c` 와 같은 자리).
  - `codex.New()` 인스턴스가 넷이다(에이전트·리뷰어·Chat 드라이버 레지스트리, 데몬). 리뷰어(`adapters/reviewer/codex`)도 같은 `GetLaunchCommand`·복원 명령을 리뷰어 id 로 부른다.
  - Chat 목록은 `aoMCPServers(harness, env)` 를 `ChatStart.MCPServers` 에 넣는 세 곳(`chat_spawn.go:260` 스폰, `:541` 재개, `agent_switching_chat.go:398` 전환). codexappserver `threadConfig` 는 `ports.ChatMCPServerConfig` 의 command·args·env·url·http_headers 만 thread `config` 의 `mcp_servers` 로 옮긴다 — Codex 표의 다른 키(`enabled`, `enabled_tools`·`disabled_tools`, `startup_timeout_sec`, `env_vars`, `bearer_token_env_var`, `cwd`, `tools.*` 등)는 담을 자리가 없다.
  - 프롬프트: `buildSystemPrompt(ctx, kind, project, session, …)` → `ladoPromptSection` → `LadoPromptSection(ctx, kind, project, session)`(harness 없음), 프로젝트 없는 세션은 묻지 않는다. 호출 8곳. 스폰은 `buildSpawnTexts`(`manager.go:1164`)가 기록 생성 뒤·task preparation 승격(`:1213`) 전에 부른다 — New Task 작성기(`TaskComposer`)의 task preparation 기록은 harness 가 비어 있다. 에이전트 전환은 대상 프롬프트를 기록의 harness 가 바뀌기 전에 만든다(`agent_switching.go:1369`, `agent_switching_chat.go:187`).
- 앞 단계 메모 중 이 단계 몫: S01 — `-c mcp_servers.<이름>=<인라인 표>` 한 인자가 `sessionFlags` 층으로 들어감, 이름은 `[A-Za-z0-9_-]` 라 키 따옴표 불필요. S05 — Codex 서버 버전 내용은 정규 JSON(JSON → TOML 변환 필요, `json.Number` 로 정수·실수 구분). S07 — `codexServerOverride(name, content)` 가 이미 `-c` 값(키 정렬, 숫자는 쓴 그대로)을 만들고 `checkCodexOverride` 가 파싱 대조, 값은 `tomlInline`. S09 — 항목 고르기는 `s.sessionItems(ctx, domain.LadoSetupToolCodex, projectID, clientRequestID)` 그대로(실행은 고정 버전, 아니면 Global·Project 바인딩).

## After — 수행 후 실측

- 코드 커밋 `9265b2650` `feat(lado): deliver managed setup to Codex sessions as overrides` — 21 파일, +1031/−38.
  - `service/ladosetup/codex.go`(+216): `CodexOverrides(ctx, sessionID, clientRequestID, projectID) ([]string, error)`, `CodexChatServers(…) ([]ports.ChatMCPServerConfig, error)`, `CodexInstructions(…) (string, error)`. 공통 `codexItems` = S09 `sessionItems`(실행은 고정 버전, 아니면 Global·Project 바인딩) + 종류 거르기 + 이름 정렬(고정 목록은 순서가 없다) + blob 읽기. 오류 `CodeCodexFailed = "LADO_SETUP_CODEX_FAILED"`(500, details `{stage: items|content}`, 경로·원인은 데몬 로그만). `chatServer`: `url` 이 있으면 http(URL·Headers), 아니면 stdio(Command·Args·Env), 나머지 키는 `CodexConfig`.
  - `adapters/agent/codex`: `lado_overrides.go`(+23) `(*Plugin).SetLadoOverrides(func(ctx, sessionID string) ([]string, error))`·`appendLadoOverrides`; `codex.go` Plugin 필드 1줄 + `GetLaunchCommand`·`GetRestoreCommand` 각 3줄(providerArgs 끝 = `model_reasoning_effort` 뒤, 작업 공간 trust·모델·지침 파일·프롬프트·재개 thread 앞).
  - `ports/chat.go`(+4) `ChatMCPServerConfig.CodexConfig map[string]any`; `codexappserver/driver.go`(+3) `threadConfig` 가 `maps.Copy(entry, server.CodexConfig)` 를 먼저 하고 typed 필드가 덮는다.
  - `session_manager/lado_setup_codex.go`(+47): `LadoChatServers func(ctx, rec domain.SessionRecord) ([]ports.ChatMCPServerConfig, error)`, `SetLadoChatServers`, `chatMCPServers(ctx, rec, harness, env)` = `aoMCPServers` 뒤에 Codex 일 때만 LADO 서버, 이름이 `ao`(대소문자 무시)면 빼고 경고 로그. 훅 줄: `chat_spawn.go` 스폰(`prepareWorkspace` 뒤, 실패는 그 자리와 같은 롤백·`ErrSpawnPrepare`)과 재개(augment 바로 뒤, provider handoff 준비 전), `agent_switching_chat.go` 전환(대상 환경 augment 뒤 — 실패는 그 자리의 다른 오류처럼 전환 saga 가 원본을 되돌린다), `manager.go` 필드 1줄.
  - 지침 절: `ladotask.PromptSource` 에 `Setup`(`CodexInstructions`) 필드와 `CodexSection`; `Section(ctx, kind, project, session, harness)` = Codex 절 + 기존 절(둘 다 있으면 Codex 절 먼저, `\n\n`; Codex 절이 없으면 기존 값 그대로); `OrchestratorSection` 은 프로젝트가 없으면 "". `session_manager/lado_gate.go`: `LadoPromptSection` 에 harness 인자, `ladoPromptSection` 이 ctx 값(`withLadoPromptHarness`) 또는 세션 기록에서 harness 를 읽는다. 업스트림 한 줄씩: `buildSpawnTexts`(`cfg.Harness`), `prepareTargetActivation`(`sw.TargetHarness`), Chat 전환(`cfg.TargetHarness`).
- 확인한 것:
  - `-c` 값은 S07 의 `codexServerOverride`/`tomlInline` 그대로(키 정렬, `key = value` 를 `, ` 로, 맨 이름이 아닌 키는 기본 문자열 따옴표, 문자열은 TOML 기본 문자열 이스케이프, 숫자는 정규 JSON 에 쓰인 그대로). 엔트리의 "이 파일의 작은 정규 인코더"를 새로 쓰지 않고 재사용했다 — 채택 검증(`checkCodexOverride`)과 같은 인코더라 채택 때 검증한 값이 실행 때 나가는 값이다. go-toml 로 `x = <값>` 을 다시 파싱해 config.toml 의 표와 같음을 `TestCodexOverridesInlineTables` 가 본다(중첩 표, `"search.v2"` 따옴표 키, 정수·실수·불리언·배열, `\"`·`\\`·`\t`·`\n`·`\b`·제어 문자·한글).
  - 실행 경로: TUI 스폰·복원·재개·에이전트 전환·인터페이스 전환 preflight 는 모두 codex 어댑터 `GetLaunchCommand`/`GetRestoreCommand` 를 지난다. Chat 은 `StartChat` 세 곳(전수 grep: `chat_spawn.go` 2, `agent_switching_chat.go` 1)이 모두 `chatMCPServers` 를 지나고, 비동기 Chat 스폰은 `launchChatController` 를 쓴다. Chat side 대화(BTW)는 주 대화의 MCPServers 를 복제하므로 함께 받는다.
  - 테스트: 필수 5개 + `TestCodexInstructionsPreferTheOverride`, `TestCodexChatServersKeepTheTable`(ladosetup), `TestThreadConfigCarriesAdoptedServerTables`(codexappserver), `TestLadoPromptSectionKnowsTheHarness`(task preparation 스폰 + TUI 전환). 고친 기존 테스트: `lado_gate_test.go`(harness 인자, 프로젝트 없는 세션도 묻는다), ladotask 두 파일·daemon `lado_wiring_test.go`(인자 하나).
  - 변이 검사 10건(ctx harness 세 줄 각각, 재개·전환의 서버 목록, `ao` 대소문자, 지침 순서, `CodexConfig` 빠뜨림, Codex 절 빠뜨림, `maps.Copy` 빠뜨림)을 모두 테스트가 잡았다.
  - `go test -count=1`: session_manager·ladotask·adapters/agent/codex·codexappserver·daemon·ports·service/chat·adapters/reviewer/... 통과; 새 테스트들 `-race` 통과; ladosetup 전체(격리 HOME) 통과; `no-home-lookups backend/internal/service/ladosetup` → `NO_HOME_LOOKUPS`; golangci-lint v2.13.2(바뀐 패키지): 이 커밋 코드 0건, S09 테스트 1건(아래 남은 틈); `go.work.sum` 변화 없음.
  - 독립 리뷰(서브에이전트, 읽기 전용, `buildSystemPrompt` 8곳·`StartChat` 3곳 대조): 정확성 문제 없음.
- 엔트리와 다르게 한 것과 이유:
  1. **Chat 경로에 `ports.ChatMCPServerConfig.CodexConfig` 를 더하고 codexappserver `threadConfig` 가 합친다**(엔트리 목록 밖 업스트림 두 파일 +7, G5 예산 목록 밖). typed 필드만 넘기면 `enabled = false`, `enabled_tools`/`disabled_tools`, `tools.*` 승인 모드, `startup_timeout_sec`, `env_vars`, `bearer_token_env_var`, `cwd` 가 Chat 에서 빠져 꺼 둔 서버가 켜지고 막아 둔 도구가 열린다. TUI 의 `-c` 는 표 전체를 나르니 이로써 두 경로가 같다. ACP 드라이버는 이 필드를 보지 않고, 채택 서버는 Codex 세션에만 붙는다.
  2. **어댑터 훅은 패키지 전역이 아니라 Plugin 필드 + 설정자**(엔트리가 허용한 대안). `codex.New()` 인스턴스가 넷(에이전트·리뷰어·Chat 드라이버 레지스트리, 데몬)이라 전역이면 리뷰어 런처도 리뷰어 id 로 훅을 부르고(플랜은 리뷰어 = 8단계), 한 프로세스에서 데몬을 여럿 띄우는 데몬 테스트끼리 경합·간섭한다. 필드는 S13 이 세션을 띄우는 인스턴스에만 건다.
  3. **harness 는 세션 기록이 기본이되, 스폰과 에이전트 전환 대상은 호출 자리에서 넘긴다**(업스트림 한 줄씩 셋, ctx 값). 기록만 보면 New Task 작성기(task preparation)로 띄운 Codex 세션은 첫 실행에 절이 없고(그 기록은 프롬프트를 만든 뒤에야 harness 를 얻는다), 전환 대상은 원래 harness 로 묻는다(Claude → Codex 는 절이 빠지고 Codex → Claude 는 Codex 지침이 Claude 에 간다). `buildSystemPrompt` 시그니처를 바꾸면 업스트림 8곳 + 테스트 10곳이라 ctx 값을 골랐다.
  4. **프로젝트 없는 세션도 LADO 절을 묻는다**(`projectID == ""` 가드를 `lado_gate.go` 에서 `OrchestratorSection` 으로 옮김) — S09 홈이 프로젝트 없는 Claude 세션에도 Global 항목을 주는 것과 맞춘다. 기존 테스트의 "프로젝트 없으면 묻지 않음" 단언을 고쳤다.
  5. Codex 지침 고르기는 Codex 와 같은 "글자가 있는 첫 파일"(AGENTS.override.md → AGENTS.md, 공백뿐이면 다음). 절 순서는 Codex 지침 먼저, LADO 규칙·오케스트레이터 절 다음.
  6. Chat 목록의 `ao` 건너뛰기는 대소문자 무시(S06 blocking 과 같은 기준).
- 게이트(HEAD `9265b2650`, 엔트리 미커밋): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 2/2`, G2 `GO_TESTS_OK 1/1`, G3 `GO_TESTS_OK 1/1` ×2, G4 `GO_PKG_OK`, G5 `BUDGET_OK 205/220`(codex 폴더 87 = S06 10 + 이 단계 77, session_manager 네 파일 53 = S09 27 + 이 단계 26, agentcreds 65), G6 `GO_BUILD_OK`. G7 은 이 엔트리 커밋 뒤 `gate-check.sh --run` 으로 전건 확인한다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 덧씌우기 값·서버 목록·지침 문구(ladosetup), codex 어댑터 훅(실행·복원), Chat 목록 세 곳, PromptSource 지침 절, 필수 테스트 5개(+4). Claude·스냅숏 쓰기·배선은 없다. 엔트리 밖은 After 의 "다르게 한 것" 1~4.
- 남은 틈:
  - 배선 전이라 세 전달 모두 아직 꺼져 있다(S13).
  - 짝 채택: AGENTS.md 와 AGENTS.override.md 중 하나만 채택하면 Codex 가 CODEX_HOME 에 남은 쪽을 스스로 읽고 LADO 가 채택한 쪽을 절로 더해, 채택 전(글자가 있는 override 가 AGENTS.md 를 가림)과 달리 둘 다 적용된다. 지침을 keep(제자리 복사)으로 채택해도 Codex 가 원본을 읽고 절이 같은 글을 한 번 더 준다.
  - 지침은 시스템 프롬프트 절로 들어간다. Codex 자신은 AGENTS.md 를 사용자 메시지(`<user_instructions>`)로 넣으니 무게가 조금 다르다(플랜 결정 d 그대로). 저장소의 AGENTS.md 는 지금처럼 Codex 가 읽는다.
  - TOML `2.0` 처럼 정수 값인 실수는 정규 JSON 을 거쳐 `2`(정수)로 나간다(S05 정규화, 채택 검증도 같은 값으로 본다). Codex 의 숫자 필드(시간 제한 등)는 serde `f64` 라 정수를 받는다고 보지만 실측은 안 했다.
  - 리뷰어 런처·백그라운드 작업(제목·제안)은 채택 서버·지침 없이 돈다(플랜대로 8단계, S09 와 같음).
  - Chat 목록은 `StartChat` 때 정해져 컨트롤러 재구성(편집·되감기·포크)에도 그대로 쓰인다 — 도는 세션은 다음 재개 전까지 옛 목록(결정 c 의 "도는 세션은 그대로").
  - S09 테스트의 lint 1건: `session_manager/lado_setup_home_test.go:176` 변수 `real`(revive redefines-builtin-id). 이 단계 범위 밖이라 두었다 — CI lint 가 실패하니 리뷰 수정 단계에서 이름을 바꾼다.
- 다음 단계 메모:
  - **S11(정책)**: Codex 실효 setup 도 같은 `sessionItems` 규칙(Project 가 Global 을 이김, 행 없음 = 켜짐). 스냅숏이 Codex 항목 버전을 고정하면 실행의 `-c`·Chat 서버·지침 절이 그 버전으로 나간다(`TestCodexDeliveryUsesPinnedVersions` 의 `pinningStore` 가 그 모양).
  - **S13(배선)** — 설정자 셋과 감쌀 함수:
    - TUI: 세션을 띄우는 에이전트 레지스트리의 codex 인스턴스(`agents.Agent(domain.HarnessCodex)`)를 `interface{ SetLadoOverrides(func(context.Context, string) ([]string, error)) }` 로 단언해 건다. 함수: 세션 id 로 `store.GetSession` → 오류면 오류, 없으면 `nil, nil`(리뷰어 id 등), 있으면 `setupSvc.CodexOverrides(ctx, rec.ID, rec.ClientRequestID, rec.ProjectID)`. 리뷰어 레지스트리·Chat 드라이버·데몬의 다른 `codex.New()` 에는 걸지 않는다.
    - Chat: `sessMgr.SetLadoChatServers(func(ctx, rec) { return setupSvc.CodexChatServers(ctx, rec.ID, rec.ClientRequestID, rec.ProjectID) })` — `SetLadoSetupHome` 과 같은 자리(스폰·복원 전).
    - 지침: `ladotask.PromptSource{…, Setup: setupSvc}`. 지금 `wireLado` 가 `SetLadoPrompt` 를 `wireLadoWork`(ladosetup 를 만들 자리)보다 먼저 거니, setup 서비스를 먼저 만들거나 `SetLadoPrompt` 를 다시 건다.
    - 오류 `LADO_SETUP_CODEX_FAILED`(500, details `stage` items|content)는 스폰·복원·Chat 시작 오류로 감싸여 나간다 — HTTP 매핑이 `errors.As(*apierr.Error)` 로 꺼내는지 S09 의 `LADO_SETUP_HOME_FAILED` 와 함께 확인.
  - **S14(번역)**: 코드 `LADO_SETUP_CODEX_FAILED`, details `stage`(items·content).
  - **S15(채택 화면)**: 위 짝 채택·keep 지침의 경고 문구(둘 다 채택을 권하거나, 하나만 고르면 둘 다 적용된다고 알림).
  - **S17(문서)**: TDD §1.2 훅 줄 표에 codex 어댑터(Plugin 필드, 실행·복원 두 곳), `ports.ChatMCPServerConfig.CodexConfig` + codexappserver `threadConfig`, session_manager Chat 목록 세 곳, `buildSpawnTexts`·`prepareTargetActivation`·Chat 전환의 harness 한 줄; HARNESS §9 에 Codex 전달 표(TUI `-c`·Chat thread config·지침 절)와 "글자가 있는 첫 파일" 규칙, 위 짝 채택 주의.
