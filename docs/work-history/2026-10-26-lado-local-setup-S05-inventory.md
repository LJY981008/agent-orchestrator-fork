# 2026-10-26 LADO 로컬 설정 채택 — S05 인벤토리: 검사·상태·사유·실행 중 도구

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 e·h·k·l, §5) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-010: 개발자의 Claude Code·Codex 설정이 어디에 무엇이 있는지 한곳에 보여 준다. 항목마다 채택할 수 있는지와, 못 하면 그 이유를 안정된 코드로 준다. 채택·복원의 전제 조건(실행 중인 도구)도 여기서 잰다. 이 패키지는 경로를 스스로 찾지 않고 데몬이 주는 경로만 쓴다(실제 `~/.claude` 를 테스트가 건드릴 수 없게).

## 범위

- 만든다: `backend/internal/service/ladosetup/`(새 패키지) — `service.go`(Deps, New, 캐시), `inventory.go`, `scan_claude.go`, `scan_codex.go`, `scan_projects.go`, `processes.go`, 테스트.
- 바꾼다(필요할 때만): `adapters/agentaccount/claude.go` 에 로그인 키 목록을 내보내는 한 줄 접근자(이미 내보내면 손대지 않음).
- 하지 않는다: 충돌(S06), 채택(S07), HTTP(S13).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `Deps`(플랜 §5): `Store`(S04 읽기 — 이 패키지 안의 작은 인터페이스), `Blobs`(`*ladoblob.Store` 를 만족하는 인터페이스), `StateDir`, `ClaudeDir`, `ClaudeJSON`, `CodexHome`, `ClaudeDirFromEnv bool`, `GOOS`(기본 `runtime.GOOS`), `Projects`(로컬 프로젝트 id·이름·경로 목록), `Sessions`(살아 있는 AO 세션의 id·harness), `Processes`(`func(ctx) ([]procmem.Process, error)`, 기본 `procmem.Snapshot`), `Clock`, `NewID`, `Log`, `DevIgnoreProcesses bool`(데몬이 `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1` 을 읽어 넘김 — 이 패키지는 환경 변수를 읽지 않는다). 경로가 비면 그 도구는 "없음"으로 나온다.
2. Claude 검사(`scan_claude.go`, 결정 e):
   - 최상위 `*.md` → `instructions`(adoptable, move).
   - `settings.json` 의 키마다 `setting`: 로그인 키(`domain.LadoSetupClaudeLoginKeys`) → `never/login_key`; `env` → adoptable(로그인 env 는 항목 내용에서 빠지고 제자리에 남는다고 Path·사유 아닌 세부로 표시); 나머지 → adoptable, 기본 동작은 `domain.LadoSetupDefaultAction`.
   - `skills/`, `agents/`(하위 경로 포함), `commands/`(하위 경로 포함), `output-styles/`, `hooks/` 의 항목마다 해당 종류(파일 → 파일 sha256, 폴더 → `LadoSetupTree` 의 sha256). 심링크 항목은 따라가지 않고 `not_adoptable`(사유 코드는 `unknown_location` 대신 새 코드가 필요하면 S03 목록에 더하고 After 에 적는다).
   - `plugins/` → `never/plugins_stay`(한 줄), 상태 항목(`projects`, `sessions`, `history.jsonl`, `todos`, `file-history`, `shell-snapshots`, `session-env`, `paste-cache`, `tasks`, `plans`, `transcripts`, `statsig`, `ide`, `debug`, `telemetry`, `cache`, `downloads`, `backups`, `stats-cache.json`, 점으로 시작하는 상태 파일) → `never/app_state`, `.credentials.json` → `never/credentials`, 그 밖 → `not_managed/unknown_location`.
   - `~/.claude.json`: `mcpServers` 의 서버마다 `mcp_server`(adoptable); 나머지 키는 한 줄 `never/app_state`.
3. Codex 검사(`scan_codex.go`, 결정 d·e): `AGENTS.md`·`AGENTS.override.md` → `instructions`. `config.toml` 은 `github.com/pelletier/go-toml/v2` 로 읽고, `mcp_servers` 의 서버마다: 파일에 `[mcp_servers.<이름>]` 머리 줄이 있으면 표 형식, 없으면 `not_adoptable/inline_table`; 이름이 `[A-Za-z0-9_-]+` 가 아니면 `name_unsupported`; `env` 에 값이 있거나 `http_headers`·`bearer_token` 이 있으면 `literal_credentials`; 아니면 adoptable. 그 밖의 키: `projects`·`notice`·`hooks`(와 `hooks.state`)·`skills` → `not_managed/tool_state`, `model`·`model_reasoning_effort` → `not_managed/ao_sets_per_session`, 나머지 → `not_managed/unknown_location`. 폴더 `skills/` → `not_adoptable/no_tui_path`, `plugins/` → `no_tui_path`, `prompts/` → `deprecated`, `rules/` → `tool_state`, `auth.json`·`secrets/` → `never/credentials`, sqlite·로그·세션 → `never/app_state`.
4. 프로젝트 검사(`scan_projects.go`, 결정 k·l): 로컬 프로젝트마다 본 체크아웃에서 `git ls-files -z -- '*CLAUDE.md' 'AGENTS.md' '*/AGENTS.md' '.claude/settings.json' '.mcp.json' '.claude/skills/*' '.claude/agents/*' '.claude/commands/*' '.codex/*'`(프롬프트 끔, 10초) → `repo_tracked`(크기·sha256, 읽기 전용). 추적되지 않은 `.claude/settings.local.json`·`CLAUDE.local.md` → `not_managed/worktrees_do_not_load`. 프로젝트 하나의 실패는 그 프로젝트 오류로만 남는다.
5. 관리 항목: S04 의 살아 있는 항목은 `managed`(ItemID, 최신 버전 sha256). 원래 위치에 같은 이름이 또 있으면 그 줄은 S08 의 변경 감지가 다룬다 — 여기서는 원래 위치 줄을 `adoptable` 로 둔다.
6. 실행 중 도구(`processes.go`, 결정 h): `RunningTools(ctx) (sessions []LiveSession, processes []procmem.Process, err)` — AO 세션은 harness 가 `claude-code`/`codex` 이고 끝나지 않은 것; 프로세스는 첫 토큰의 base name 이 `claude`, `claude.exe`, `codex`, `codex.exe` 이거나 명령줄에 `@anthropic-ai/claude-code`, `@openai/codex`, `claude-agent-acp` 가 있는 것(데몬 자신의 PID 제외). `DevIgnoreProcesses` 면 프로세스 목록은 비우고 그 사실을 결과에 표시한다.
7. 캐시: `Inventory(ctx)` 는 마지막 검사 결과(없으면 검사), `Scan(ctx)` 는 다시 검사. 결과에 `OS`, `Verified`(`domain.LadoSetupDeliveryVerified`), `ScannedAt`.
8. `adapters/agentaccount` 가 로그인 키 목록을 내보내지 않으면 `LoginSettingKeys()`·`LoginEnvNames()` 같은 접근자를 한 줄씩 더하고(diff 예산 안), `TestClaudeLoginKeysMatchAgentAccount` 가 도메인 상수와 같음을 확인한다.
9. 커밋 메시지 예: `feat(lado): inventory of the local Claude Code and Codex setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S05-inventory.md` 로 전건 MET 를 확인한다.

## 필수 테스트

모두 `t.TempDir()` 의 가짜 홈으로(실제 경로 금지):

- `TestInventoryListsClaudeItems` — 종류마다 한 개 이상, 상태·사유·기본 동작, 폴더 항목의 트리 해시, 심링크 항목
- `TestInventoryKeepsLoginKeysAndCredentialsOut` — 로그인 키·`.credentials.json`·상태 항목은 `never`, `env` 항목 내용에 로그인 env 없음
- `TestInventoryListsCodexItems` — AGENTS.md, 표 형식 서버(adoptable), 인라인(`inline_table`), 비밀 값(`literal_credentials`), 이름(`name_unsupported`), 그 밖의 키·폴더 사유
- `TestRepoTrackedFilesAreNeverAdoptable` — 임시 git 저장소: 추적된 CLAUDE.md·`.mcp.json`·`.claude/settings.json` 은 `repo_tracked`, 추적 안 된 `settings.local.json` 은 `worktrees_do_not_load`(HARNESS §15 항목 14 의 앞 절반)
- `TestInventoryMarksManagedItems` — store 의 살아 있는 항목은 `managed` 와 ItemID
- `TestRunningToolsAreFound` — 이름·명령줄 표지·데몬 PID 제외·끝난 세션 제외·dev 무시
- `TestInventoryOSVerification` — GOOS linux/windows/darwin 의 Verified
- `TestClaudeLoginKeysMatchAgentAccount` — 도메인 상수 = agentaccount 목록

## 수용 게이트

- [ ] S05-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S05-G1: 필수 테스트 8개가 격리된 홈에서 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestInventoryListsClaudeItems TestInventoryKeepsLoginKeysAndCredentialsOut TestInventoryListsCodexItems TestRepoTrackedFilesAreNeverAdoptable TestInventoryMarksManagedItems TestRunningToolsAreFound TestInventoryOSVerification TestClaudeLoginKeysMatchAgentAccount
  EXPECT: GO_TESTS_OK 8/8
- [ ] S05-G2: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S05-G3: 업스트림 agentaccount 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 15 backend/internal/adapters/agentaccount
  EXPECT: BUDGET_OK
- [ ] S05-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S05-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — Deps 이름, 캐시 무효화 지점, 새 사유 코드)
