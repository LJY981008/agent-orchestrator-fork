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

(단계 시작 때 채운다: 시작 HEAD, 환경, 바이너리·어댑터 버전과 경로, 실제 Claude 폴더와 `~/.claude.json` 의 경로)

## After — 수행 후 실측

(커밋, 각 실측의 실제 출력 요약 — `auth status` 필드, init 이벤트의 목록 키와 값, ACP 알림 모양, `config/read` origin 모양 —, 엔트리와 다르게 한 것과 이유, 게이트 결과)

## 비교/회고

(전제 (1)·(2)가 맞았는지, 틀린 부분, 남은 틈, 다음 단계 메모 — 특히 S09(홈 모양에서 꼭 필요했던 것), S10(`-c` 값 모양), S13(ACP 경로), 9단계의 Windows·macOS 실측에 쓸 명령)
