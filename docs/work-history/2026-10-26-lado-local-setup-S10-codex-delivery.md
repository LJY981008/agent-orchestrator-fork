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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S13 에 넘길 설정자 이름과 감쌀 함수)
