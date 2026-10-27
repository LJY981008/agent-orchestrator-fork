# 2026-10-26 LADO 로컬 설정 채택 — S01 실측: 실제 claude·codex 로 전달 경로 확인

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§0, §1 결정 b·d와 "Facts measured", §9, §10) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

7단계 설계 전체가 두 전제 위에 서 있다. (1) Claude 는 `CLAUDE_CONFIG_DIR=<세션 홈>` 과 `CLAUDE_SECURESTORAGE_CONFIG_DIR=<실제 Claude 폴더>` 로 띄우면 자기 로그인을 원래 자리에서 쓰면서 홈의 설정·스킬·에이전트·명령·출력 스타일·MCP·hook·CLAUDE.md 를 TUI 와 Chat(ACP) 양쪽에서 읽는다. (2) Codex 는 실제 `CODEX_HOME` 그대로 `-c mcp_servers.<이름>=<인라인 표>` 를 세션 플래그 층으로 받는다. 둘 다 바이너리 안 코드와 소스로만 확인했다(`CLAUDE_SECURESTORAGE_CONFIG_DIR` 는 문서에 없다). 이 단계는 그 전제를 실제 바이너리로 잰다. 전제가 틀리면 뒤 단계를 짓기 전에 체인이 멈춰야 한다. 실측 코드는 9단계에서 Windows·macOS 를 잴 때 그대로 다시 쓴다.

## 범위

- 만든다: `backend/internal/service/ladosetup/probe/`(새 패키지, `package probe`) — 테스트 파일만(`probe_test.go`, 필요하면 `helpers_test.go`). 각 테스트는 `AO_LADO_SETUP_PROBE=1` 이 없으면 `t.Skip`. 빌드 산출물 `frontend/resources/acp-runtime/`(무시 목록)을 만든다.
- 하지 않는다: `ladosetup` 본체, 실제 `~/.claude`·`~/.claude.json`·`~/.codex` 쓰기(읽기는 아래 두 가지뿐: `~/.claude.json` 을 랩 홈으로 복사, 로그인을 `auth status` 와 `claude -p` 로 사용), AO 데몬 기동.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S01 은 첫 단계라 플랜만 읽는다.
1. 준비(Before 에 버전·경로를 적는다):
   - `claude --version`(2.1.283 예상), `command -v claude` 와 `readlink -f`.
   - codex: `AO_LADO_SETUP_PROBE_CODEX` 가 가리키는 바이너리(게이트 도우미 기본값 `/tmp/claude-1000/-home-code-project-agent-orchestrator-fork/eeda879d-4d21-4b46-a44c-03c90e64d0b5/scratchpad/tools/codex-cli/node_modules/.bin/codex`, 0.163.0)의 `--version`.
   - ACP: `npm --prefix frontend run build:acp-runtime` → `frontend/resources/acp-runtime/node/bin/node` 와 `frontend/resources/acp-runtime/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js`(AO 가 배포하는 패치된 어댑터). 버전은 그 `package.json`.
   - 실제 Claude 폴더 = 테스트 프로세스의 `CLAUDE_CONFIG_DIR`(있으면) 아니면 `$HOME/.claude`. 실제 `~/.claude.json` = `CLAUDE_CONFIG_DIR` 이 있으면 그 안, 아니면 `$HOME/.claude.json`.
2. 공통 랩(테스트마다 `t.TempDir()`):
   - `<lab>/real`: 가짜 "실제 폴더" — 상태 항목(`projects`, `sessions`, `session-env`, `file-history`, `shell-snapshots`, `todos`, `paste-cache`, `tasks`, `plans`, `transcripts`, `plugins` 빈 폴더, `history.jsonl` 빈 파일). 자격 증명은 넣지 않는다.
   - `<lab>/home/claude`: 플랜 결정 b 의 세션 홈 모양 — 위 상태 항목마다 `<lab>/real/<이름>` 으로 가는 심링크; `CLAUDE.md`(무작위 표지어 `LADOPROBE-<8 hex>` 를 담은 한 문장: "If asked for the probe word, answer with exactly <표지어>."); `settings.json` = `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"touch <lab>/hook-ran"}]}]}}`; `skills/lado-probe-skill/SKILL.md`(front matter `name`, `description`); `agents/lado-probe-agent.md`(front matter `name`, `description`); `commands/lado-probe-cmd.md`; `output-styles/lado-probe-style.md`(front matter `name`, `description`); `.claude.json` = 실제 `~/.claude.json` 의 복사본(0600)에 `mcpServers` 를 `{"lado-probe-mcp":{"type":"stdio","command":"true","args":[]}}` 로 바꾼 것, 그리고 랩 작업 폴더의 `projects["<cwd>"].hasTrustDialogAccepted = true`.
   - 작업 폴더 `<lab>/work`: `git init` 한 빈 저장소(사용자 정보는 `-c user.name=probe -c user.email=probe@example.invalid`).
   - 환경: 테스트 프로세스 환경에 `CLAUDE_CONFIG_DIR=<lab>/home/claude`, `CLAUDE_SECURESTORAGE_CONFIG_DIR=<실제 Claude 폴더>` 를 덧씌운다. 자격 증명 파일을 랩에 복사하거나 링크하지 않는다.
3. `TestDeliveryProbeClaudeTUI`:
   1. `claude auth status --json` → 로그인됨(필드 이름은 실제 출력에서 확인해 After 에 적는다). 같은 명령을 `CLAUDE_SECURESTORAGE_CONFIG_DIR` 없이 돌리면 로그인 안 됨이어야 한다(변수가 실제로 효과를 냄을 대조로 확인).
   2. `<lab>/work` 에서 `claude -p --model haiku --max-turns 1 --output-format stream-json --verbose "What is the probe word? Reply with the word only."` (타임아웃 3분). JSONL 을 읽어:
      - 첫 `system`/`init` 이벤트에 `lado-probe-skill`(skills 또는 slash commands), `lado-probe-cmd`, `lado-probe-agent`, `lado-probe-mcp`(mcp_servers, 상태 무관)가 있다. 출력 스타일은 init 에 이름이 있으면 확인하고, 없으면 After 에 "init 에 없음"으로 적는다(실패 아님).
      - 최종 `result` 텍스트에 표지어가 있다(CLAUDE.md 를 읽음).
      - `<lab>/hook-ran` 이 생겼다(홈 settings 의 hook 실행).
      - `<lab>/real/projects/` 아래에 이 실행의 대화 기록 `*.jsonl` 이 생겼다(링크를 통해 실제 쪽에 씀).
      - 홈에 `.credentials.json` 이 생기지 않았고, 실제 폴더의 `.credentials.json`(있다면)은 여전히 일반 파일이다.
4. `TestDeliveryProbeClaudeChat`: 위 랩을 새로 만들고, AO 와 같은 방식으로 ACP 어댑터를 띄운다 — `frontend/resources/acp-runtime/node/bin/node <dist/index.js>`, 환경 = 3 의 환경 + `CLAUDE_CODE_EXECUTABLE=<claude 실제 경로>`(AO 의 `claudeACPLaunchEnv` 가 넣는 다른 변수는 `adapters/chatdriver/claudeacp/driver.go` 를 읽고 필요한 것만). stdio JSON-RPC 로 `initialize` → `session/new {cwd: <lab>/work, mcpServers: []}` 를 보내고 60초 안에 `session/update` 의 `available_commands_update` 에 `lado-probe-cmd` 가 오는지 본다(스킬도 목록에 오면 함께 확인, 안 오면 After 에 적음). 프롬프트는 보내지 않는다(모델 호출 없음). 끝나면 프로세스를 정리한다.
5. `TestDeliveryProbeCodex`: `<lab>/codex` 를 CODEX_HOME 으로(빈 `config.toml` 에 `[mcp_servers.lado_file]` 표 하나 — `command = "true"`). `codex -c 'mcp_servers.lado_probe={command="true",args=[]}' app-server` 를 띄워 stdio JSON-RPC 로 `initialize`(그리고 프로토콜이 요구하면 `initialized` 알림) → `config/read {includeLayers: true}`. 확인: `mcp_servers.lado_probe` 의 origin 층이 `sessionFlags`, `mcp_servers.lado_file` 은 `user`. 요청·응답 모양은 `backend/internal/adapters/chatdriver/codexappserver/codexproto/protocol.gen.go` 를 따른다(그 타입을 import 해도 된다). 모델 호출·로그인 없음.
6. 세 테스트 모두 실패하면 우회하지 않는다: 원인(버전·필드·경로)을 After 에 그대로 적고 게이트가 실패하게 둔다 — 메인 세션이 다시 계획한다. 바이너리나 빌드가 없어서 못 돌면 skip 이 아니라 실패로 만든다(게이트 도우미가 skip 도 실패로 센다).
7. 커밋 메시지 예: `test(lado): probe setup delivery with the real claude and codex`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S01-probe.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `TestDeliveryProbeClaudeTUI` — 위 3: 로그인(변수 있음/없음 대조), init 목록, 표지어, hook, 대화 기록 위치, 자격 증명 그대로
- `TestDeliveryProbeClaudeChat` — 위 4: ACP 어댑터가 홈의 명령을 알림
- `TestDeliveryProbeCodex` — 위 5: `-c` 서버가 `sessionFlags` 층, 파일 서버가 `user` 층

## 수용 게이트

- [ ] S01-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S01-G1: 실측 3개가 실제 바이너리로 돌고 통과(skip 은 실패)
  CHECK: bash .claude/gate/lado-local-setup.sh probe TestDeliveryProbeClaudeTUI TestDeliveryProbeClaudeChat TestDeliveryProbeCodex
  EXPECT: PROBE_OK 3/3
- [ ] S01-G2: 실측 패키지는 변수 없이는 건너뛰고 통과(CI 에서 바이너리를 찾지 않음)
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg-isolated ./internal/service/ladosetup/probe/
  EXPECT: GO_PKG_OK
- [ ] S01-G3: 실측 코드가 Codex 자격 증명이나 토큰 변수를 다루지 않는다(Claude 로그인은 `CLAUDE_SECURESTORAGE_CONFIG_DIR` 로만 닿는다)
  CHECK: bash .claude/gate/lado-local-setup.sh none backend/internal/service/ladosetup/probe 'auth.json' 'CLAUDE_CODE_OAUTH_TOKEN' 'ANTHROPIC_API_KEY'
  EXPECT: NONE_OK
- [ ] S01-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S01-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `0e7bf9ee8`(`chore(lado): define the step 7 chain for local setup adoption`, 태그 `lado-step7-chain-def` 와 같음), 트리 깨끗. `backend/internal/service/ladosetup/` 없음.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 toolchain, PATH 맨 앞), Node v24.21.0, umask 0022, TZ=UTC. 이 단계는 체인 러너의 `claude -p` 안에서 돌아 환경에 `CLAUDECODE`·`CLAUDE_CODE_*`(entrypoint, session id, messaging socket·token 등)·`CLAUDE_PROJECT_DIR`·`CHAIN_RUN` 이 있다. `CLAUDE_CONFIG_DIR`·`CLAUDE_SECURESTORAGE_CONFIG_DIR`·`ANTHROPIC_*`·`CODEX_*` 는 없다.
- claude: `command -v claude` = `/home/code/.local/node/bin/claude` → `readlink -f` = `/home/code/.local/node/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`, `claude --version` = `2.1.283 (Claude Code)`.
- codex: 게이트 도우미 기본값 `…/eeda879d-…/scratchpad/tools/codex-cli/node_modules/.bin/codex` → `@openai/codex/bin/codex.js`(node 실행 스크립트), `codex --version` = `codex-cli 0.163.0`.
- ACP: `npm --prefix frontend run build:acp-runtime`(33 s, `npm ci` + Node 아카이브 내려받기·체크섬) → `frontend/resources/acp-runtime/node/bin/node` = v22.23.2, `node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js`, 어댑터 `package.json` 0.70.0(의존 `@anthropic-ai/claude-agent-sdk` 0.3.232, `@agentclientprotocol/sdk` 1.3.0), 빌드 스크립트의 패치 6개 적용. `.gitignore:79` 가 무시한다.
- 실제 Claude 폴더 = `$HOME/.claude` = `/home/code/.claude`(`CLAUDE_CONFIG_DIR` 없음). 그 안 `.credentials.json` 은 일반 파일(0600, 링크 1). 실제 `~/.claude.json` = `/home/code/.claude.json`(124,737 B, 0600, user MCP 서버 없음).

## After — 수행 후 실측

- 코드 커밋 `b229b809d` `test(lado): probe setup delivery with the real claude and codex` — 2 파일, +1085.
  - `helpers_test.go`: `requireProbe`(변수 없으면 skip, 그 뒤 바이너리·빌드·로그인 없음은 실패), 바이너리 찾기(claude 는 `LookPath`+`EvalSymlinks`, codex 는 `AO_LADO_SETUP_PROBE_CODEX` 아니면 PATH, ACP 런타임은 `AO_ACP_RUNTIME_DIR` 아니면 `frontend/resources/acp-runtime`), 실제 폴더·`~/.claude.json`·보안 저장 폴더 규칙, 랩(`newClaudeLab`: `real` 대역, 홈의 상태 링크 12개, 표지어 CLAUDE.md, SessionStart hook, 스킬·에이전트·명령·출력 스타일, `.claude.json` 복사본에 `mcpServers` 교체와 작업 폴더 trust), `auth status`, 줄 단위 stdio JSON-RPC 도우미(`startPeer`·`call`·`notify`·`waitNotification` — 자식의 요청은 -32601 로 거절, 정리 때 프로세스 트리를 죽이고 종료를 기다림).
  - `probe_test.go`: 세 테스트와 print 턴·대화 기록 찾기·홈 항목 기록.
- 실측 결과(`go test -v`, 3/3 PASS — TUI 4.1 s, Chat 1.1 s, Codex 0.2 s; 같은 결과로 세 번 돌았다):
  - **TUI** (`TestDeliveryProbeClaudeTUI`)
    - `auth status --json`, 변수 있음: 키 `analyticsDisabled, apiProvider, authMethod, configDirectory, email, loggedIn, orgId, orgName, projectsDirectory, subscriptionType`; `loggedIn=true`, `authMethod="claude.ai"`, `apiProvider="firstParty"`, `subscriptionType="max"`, exit 0. `configDirectory`·`projectsDirectory` 는 랩 홈과 그 `projects`(계정 값은 적지 않는다).
    - 변수 없음(대조): 키 `analyticsDisabled, apiProvider, authMethod, configDirectory, loggedIn, projectsDirectory`; `loggedIn=false`, `authMethod="none"`, exit 1.
    - `system`/`init` 키: `agents analytics_disabled apiKeySource capabilities claude_code_version cwd fast_mode_disabled_reason fast_mode_state mcp_servers memory_paths messaging_socket_path model output_style per_turn_effort_active permissionMode plugins product_feedback_disabled session_id skills slash_commands subtype terminal_slash_commands tools type uuid view_mode`.
    - 목록: `lado-probe-skill` ∈ `skills`·`slash_commands`; `lado-probe-cmd` ∈ `slash_commands`; `lado-probe-agent` ∈ `agents`; `lado-probe-mcp` ∈ `mcp_servers` 로 `{name: "lado-probe-mcp", source: "user", status: "failed"}`(`command: "true"` 라 실패가 정상). 출력 스타일: **init 에 없음** — `output_style="default"`(활성 스타일 이름 하나, 목록 없음).
    - 그 밖: `apiKeySource="none"`(OAuth), `model="claude-haiku-4-5-20251001"`, `permissionMode="default"`, `memory_paths={auto: <홈>/projects/<cwd 이름>/memory/}`.
    - hook: 스트림에 `system`/`hook_started`·`hook_response`(`hook_event="SessionStart"`, `hook_name="SessionStart:startup"`), `<lab>/hook-ran` 생김.
    - `result`: `subtype="success"`, `is_error=false`, `num_turns=1`, 텍스트 = 표지어 그대로(`LADOPROBE-<8 hex>`).
    - 대화 기록: `<lab>/real/projects/-tmp-TestDeliveryProbeClaudeTUI…-001-work/<init session_id>.jsonl` — 홈의 `projects` 링크를 통해 대역 쪽에 씀. 실제 `~/.claude/projects/` 에 같은 이름 항목 없음. 실행 뒤 홈 상태 링크 12개 모두 링크 그대로.
    - 자격 증명: 홈에 `.credentials.json` 없음, 실제 `.credentials.json` 은 일반 파일 그대로.
    - Claude 가 홈에 더한 항목: `backups`, `cache`. 홈의 `.claude.json` 을 다시 씀(해시 바뀜).
  - **Chat** (`TestDeliveryProbeClaudeChat`, `node dist/index.js`, 환경 = 랩 환경 + `CLAUDE_CODE_EXECUTABLE=<claude.exe 실제 경로>`)
    - `initialize`(protocolVersion 1, fs·terminal false) → `protocolVersion=1`, `agentInfo={name: "@agentclientprotocol/claude-agent-acp", title: "Claude Agent", version: "0.70.0"}`, `authMethods` 없음.
    - `session/new {cwd: <lab>/work, mcpServers: []}` → 키 `configOptions, modes, sessionId`.
    - 알림 모양: `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"…","update":{"sessionUpdate":"available_commands_update","availableCommands":[{"name","description","input"}, …]}}}` — 명령 55개, 1 s 안. `lado-probe-cmd` = `{name: "lado-probe-cmd", description: "LADO delivery probe command (user)", input: null}`. 스킬도 명령으로 옴: `{name: "lado-probe-skill", description: "LADO delivery probe skill. Never use it. (user)", input: null}`. 프롬프트·모델 호출 없음.
  - **Codex** (`TestDeliveryProbeCodex`, `CODEX_HOME=<lab>/codex`, `codex -c 'mcp_servers.lado_probe={command="true",args=[]}' app-server`)
    - `initialize` 결과 키 `codexHome, platformFamily, platformOs, userAgent`; 이어서 `initialized` 알림.
    - `config/read {includeLayers: true}` 의 `layers`: `sessionFlags`(file 없음, `version="sha256:…"`), `user`(file `<lab>/codex/config.toml`), `system`(file `/etc/codex/config.toml` — 파일이 없어도 층은 나온다).
    - `origins`: `mcp_servers.lado_probe.command` → `{name: {type: "sessionFlags"}, version: "sha256:…"}`; `mcp_servers.lado_file.command` → `{name: {type: "user", file: "<lab>/codex/config.toml"}, version: "sha256:…"}`. origin 은 잎 값 키 단위이고, 빈 배열 `args=[]` 에는 origin 이 없다.
    - 실효 `config.mcp_servers.lado_probe = {args: [], command: "true", enabled: true, environment_id: "local", tool_timeout_sec: null}`(기본값이 채워짐), `lado_file` 도 같은 모양. 로그인 없이 app-server 기동과 `config/read` 가 된다.
- 추가 실측(스크래치 랩, 커밋 안 함, 끝나고 실제 `~/.claude.json` 복사본과 함께 삭제): 홈에 `.claude.json` 이 **없어도** `auth status` 는 `loggedIn=true`, `authMethod="claude.ai"` — 로그인은 보안 저장 폴더의 `.credentials.json` 만으로 된다. 그때 Claude 는 홈에 새 `.claude.json`·`.claude.json.lock`·`backups/` 를 만든다.
- 엔트리와 다르게 한 것과 이유:
  1. 환경은 테스트 프로세스 환경을 그대로 덧씌우지 않고 부모의 `CLAUDE*`·`ANTHROPIC_*`·`CODEX_*` 변수를 뺀 뒤 랩 변수를 더했다(`cleanEnviron`). 체인 단계는 Claude Code 세션 안에서 돌아 `CLAUDECODE`·`CLAUDE_CODE_*` 가 있는데 데몬의 실행은 이것을 물려받지 않고, 부모에 로그인 변수가 있으면 "변수 없음 → 로그아웃" 대조가 뜻을 잃는다. 토큰 변수 이름은 코드에 쓰지 않는다(접두어로만 뺀다, G3).
  2. `CLAUDE_SECURESTORAGE_CONFIG_DIR` 값 규칙에 플랜 결정 b 의 macOS 분기를 넣었다(`CLAUDE_CONFIG_DIR` 이 없고 macOS 면 `""`). Linux 에서는 엔트리와 같은 `/home/code/.claude`. 9단계가 macOS 에서 고치지 않고 돌리게 하려는 것.
  3. 이식성: 랩 루트는 `EvalSymlinks(t.TempDir())`(macOS `/var`), trust 키는 AO 와 같이 `filepath.ToSlash(work)`, hook 은 `touch "<슬래시 경로>"`(Windows 의 Git Bash), Windows 의 Node 경로 `node/node.exe`.
  4. 엔트리에 없던 실패 조건 셋: 대화 기록 파일 이름 = init 의 `session_id`, 실행 뒤 홈 상태 링크가 그대로 링크, 실제 `~/.claude/projects/` 에 랩 작업 폴더 항목이 생기지 않음. 로그만 남긴 것: 홈 `.claude.json` 재작성 여부, Claude 가 홈에 더한 항목, `memory_paths`, hook 이벤트, ACP 의 스킬 명령.
- 그 밖 확인: `golangci-lint run ./internal/service/ladosetup/...` 0 issues(rules-core 세션 스크래치의 v2 빌드), `gofmt -l` 출력 없음, 실행 뒤 남은 어댑터·app-server·claude 프로세스 없음, `/tmp/TestDeliveryProbe*` 남지 않음.
- 게이트(HEAD `b229b809d`): `GATES=6 MET=6 UNMET=0`(7.8 s — G1 이 세 실측을 실제 바이너리로 한 번 더 돌림). 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- **전제 (1) 맞음**(Linux, claude 2.1.283, 어댑터 0.70.0): `CLAUDE_CONFIG_DIR=<홈>` + `CLAUDE_SECURESTORAGE_CONFIG_DIR=<실제 폴더>` 로 로그인이 유지되고, 둘째 변수를 빼면 로그아웃이다 — 그 변수가 로그인을 나른다. 홈의 CLAUDE.md·settings hook·스킬·에이전트·명령·MCP 가 print 모드에서 읽히고, ACP 어댑터도 홈의 명령(과 스킬)을 알린다. 대화 기록은 상태 링크를 따라 실제 쪽에 쓰이고 홈에는 자격 증명 파일이 생기지 않는다.
- **전제 (2) 맞음**(codex 0.163.0): `-c mcp_servers.<이름>=<인라인 표>` 는 `sessionFlags` 층, `CODEX_HOME/config.toml` 의 표는 `user` 층, 실효 config 에 둘 다 있다.
- 틀린 부분: 없음. 남은 틈:
  - 출력 스타일은 init 에 목록이 없어 이 실측으로 로드 여부를 못 본다(활성 스타일 이름만). 필요하면 홈 settings 에 `"outputStyle": "lado-probe-style"` 을 넣고 init `output_style` 를 보면 된다.
  - TUI 는 print 모드(같은 엔진)로 쟀다. 대화형 TUI 의 trust 대화상자는 거치지 않았다 — AO 는 trust 를 `<CLAUDE_CONFIG_DIR>/.claude.json` 에 쓰므로 홈 준비 → trust 쓰기 순서(플랜 §9)가 그대로 맞다.
  - Chat 은 명령 목록까지만 쟀다(프롬프트 없음). 같은 변수로 실제 턴이 도는 것은 print 모드에서 확인했다.
  - Windows·macOS 는 이 단계 밖(9단계).
- 다음 단계 메모:
  - **S09(홈 모양)**: 꼭 필요했던 것은 두 변수뿐 — 로그인에 `.claude.json` 복사본은 필요 없다(스크래치 확인). 복사본은 base 의 `mcpServers`·온보딩 상태·trust 를 위한 것이다. 상태 항목을 심링크로 두면 Claude 는 링크를 바꾸지 않고 그 안에 쓴다(대화 기록, 자동 메모리 `projects/<cwd>/memory/`). Claude 는 홈에 `backups/`·`cache/` 를 만들고 `.claude.json` 을 다시 쓴다(`.claude.json.lock` 도 생길 수 있다) — 재조정은 이것들을 지우지 않는다. 홈 `.claude.json` 의 `mcpServers` 는 init 에 `source: "user"` 로 나온다. 홈에는 `.credentials.json` 이 생기지 않으므로 홈 정리 때 자격 증명을 신경 쓸 일이 없다.
  - **S10(`-c` 값 모양)**: `mcp_servers.<이름>={command="true",args=[]}` 한 인자(TOML 인라인 표, `-c` 를 하위 명령 앞에 둠)가 그대로 받아들여졌다. 실효 값에 `enabled: true`·`environment_id: "local"` 기본이 붙고, origin 은 잎 키(`mcp_servers.<이름>.command`) 단위다. 이름은 `[A-Za-z0-9_-]` 로 제한하니 키 따옴표가 필요 없다.
  - **S13(ACP 경로)**: AO 의 `resolveRuntime` 경로(`<runtime>/node/bin/node` + `…/claude-agent-acp/dist/index.js`)에 `CLAUDE_CODE_EXECUTABLE` 만 더한 환경으로 홈을 따른다 — 홈 변수 두 개가 어댑터 프로세스 환경에 있으면 어댑터·SDK 가 자식 claude 에 그대로 넘긴다. `session/new` 의 `mcpServers: []` 여도 홈의 MCP·명령은 claude 쪽에서 읽힌다.
  - **9단계(Windows·macOS 실측)**: `npm --prefix frontend run build:acp-runtime` 뒤 `bash .claude/gate/lado-local-setup.sh probe TestDeliveryProbeClaudeTUI TestDeliveryProbeClaudeChat TestDeliveryProbeCodex`, 또는 `cd backend && AO_LADO_SETUP_PROBE=1 AO_LADO_SETUP_PROBE_CODEX=<codex> go test -count=1 -v -timeout 15m -run '^TestDeliveryProbe' ./internal/service/ladosetup/probe/`. macOS 는 보안 저장 변수 `""` 분기가 들어 있다. Windows 는 심링크(개발자 모드), hook 의 `touch`(Git Bash), codex 경로(`codex.exe` 또는 npm 실행 스크립트)를 먼저 확인한다.
