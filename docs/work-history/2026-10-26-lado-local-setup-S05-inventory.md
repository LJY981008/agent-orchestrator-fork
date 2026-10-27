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

- 시작 HEAD: `ab7433da9`(`docs(lado): record S04`), 트리 깨끗. 태그 `lado-step7-chain-def` = `0e7bf9ee8`.
- 환경: Linux(커널 7.0.0-31-generic), go1.27.1(스크래치 toolchain), git 2.43.0, umask 0022, TZ=UTC, LANG=C.UTF-8. 체인 러너의 `claude -p` 안(`CHAIN_RUN` 있음, `CLAUDE_CONFIG_DIR`·`CODEX_HOME` 없음). golangci-lint v2.13.2 는 rules-core 세션 스크래치의 빌드(`GOPROXY=off`).
- 출발점:
  - `service/ladosetup/` 에는 S01 의 `probe/`(테스트만)뿐이고 서비스 코드가 없다.
  - `adapters/agentaccount/claude.go` 는 로그인 키 목록(`claudeSettingsDropped`, `claudeCredentialEnv`)을 내보내지 않는다.
  - `procmem.Snapshot(ctx, nil)` → `*Table` → `All()`. Unix 는 `ps -axww -o …,args=`(명령줄 200자에서 자름), Windows 는 Toolhelp 의 실행 파일 이름만 준다.
  - 데몬은 `CLAUDE_CONFIG_DIR` 이 있으면 `.claude.json` 을 그 폴더 안에서 찾는다(`daemon.go` 의 `newAgentAccounts`).
  - store 에 이미 `ListProjects`(보관 안 된 프로젝트)·`ListAllSessions` 가 있고, 관찰자들(`observe/*`)이 같은 모양의 작은 인터페이스로 쓴다. scratch 프로젝트는 저장소가 없고 세션이 그 폴더에서 바로 돈다.
  - 분류 대상을 확인하려고 실제 `~/.claude`·`~/.codex` 의 항목 이름과 `~/.claude/settings.json` 의 키 이름만 읽었다(값·파일 내용은 읽지 않음, 쓰기 없음). 엔트리 목록 밖의 이름: `~/.claude` 에 `mcp-needs-auth-cache.json`, `state/`, `settings.json.bak-*`, `settings.local.json`, `.omc/`; `~/.codex` 에 `goals_1.sqlite`, `logs_2.sqlite-wal`, `memories/`, `vendor_imports/`, `installation_id`, `.personality_migration`.
- 앞 단계 메모 중 이 단계 몫:
  - S01·S02: 해당 없음(S09·S10·S13 몫).
  - S03: 줄은 `domain.LadoSetupEntry`, 기본 동작 `LadoSetupDefaultAction`, OS 검증 `LadoSetupDeliveryVerified`, 폴더 해시 `LadoSetupTree.SHA256()`·`Size()`; `ErrLadoSetupTreeInvalid` 면 채택 불가; 새 사유 코드는 도메인 목록과 `TestLadoSetupCodesAreStable` 표에 함께; 항목이 아닌 줄은 Kind 를 비움; 로그인 키·env 목록(`claude.go:32-41` 순서).
  - S04: managed 줄은 `ListLiveLadoSetupItems` 의 `Item.ID`·`Version.SHA256`.

## After — 수행 후 실측

- 코드 커밋 2건:
  - `a863bbfd5` `feat(lado): inventory of the local Claude Code and Codex setup` — 13 파일, +2303 −1.
  - `c5b9c0a91` `fix(lado): never block or follow a link when the setup scan reads a file` — 6 파일, +157 −11(리뷰 반영, 아래).
- 새 패키지 `service/ladosetup`:
  - `service.go`: 작은 인터페이스 `Store`(`ListLiveLadoSetupItems`, `ListLadoSetupAdoptions`), `Blobs`(`Put`, `Get` — `*ladoblob.Store`), `Projects`(`ListProjects`), `Sessions`(`ListAllSessions`) — 블롭 말고는 `*sqlite.Store` 가 그대로 만족한다. `Deps` 는 엔트리 목록 그대로, `New` 의 기본값은 GOOS=`runtime.GOOS`, Processes=procmem, Clock, NewID=uuid, Log. 결과 `Inventory{Entries, Roots, Projects, Adoptions, OS, Verified, ScannedAt}`, `Root{Tool, Location, Path, Found, Error}`, `ProjectScan{ID, Name, Path, Error}`. 캐시: `Inventory(ctx)` 는 캐시가 있으면 바로, 없으면 검사(한 번에 하나 — 채널 세마포어라 기다리는 쪽은 ctx 로 포기), `Scan(ctx)` 는 다시 검사해 저장. 돌려주는 값은 복사본(목록과 `KeptInPlace` 까지), 도중 취소된 검사는 저장하지 않는다.
  - `inventory.go`: 검사 순서(관리 항목 → Claude 폴더 → `~/.claude.json` → Codex 폴더 → 프로젝트), 관리 줄(위치 `managed`, 경로는 HARNESS §9.3 배치 `managedPath`), 내용 해시 — 파일 바이트, 폴더는 링크를 따라가지 않는 WalkDir 로 만든 `LadoSetupTree`(실행 비트 → `exec`), JSON 값은 정규형(`UseNumber` 로 숫자는 쓴 그대로, 키 정렬, HTML 이스케이프 없음, 끝 줄바꿈 없음; 크기는 그 바이트 수). 파일은 `openRegular` 로 연다: 막히지 않게(`O_NONBLOCK`) 열고, 연 파일이 일반 파일인지 fstat 으로 보고, 항목 파일은 연 경로가 링크였으면 `errLink`(Lstat 과 `os.SameFile`).
  - `scan_claude.go`·`scan_codex.go`: 플랜 결정 e·d 와 엔트리 절차 2·3 그대로(다르게 한 것은 아래). Codex 서버는 이름 → 머리 줄(공백·따옴표·주석·CRLF 를 받는 정규식 `codexServerHeader`) → 비밀 값 → 링크 순으로 본다.
  - `scan_projects.go`: `git -C <dir> ls-files -z -- <pathspec>`(`GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=Never`, 10 s). 추적 파일 → `repo_tracked`(본 체크아웃 작업 트리 파일의 크기·sha256, 작업 트리에서 지워진 파일은 내용 없이), 루트의 `CLAUDE.local.md`·`.claude/settings.local.json` 이 추적되지 않으면 → `not_managed/worktrees_do_not_load`. 실패는 그 프로젝트의 `ProjectScan.Error` 에만 남는다.
  - `processes.go`: `RunningTools(ctx) (Running, error)` — `Running{Sessions []LiveSession{ID, ProjectID, Tool}, Processes []ToolProcess{procmem.Process, Tool}, ProcessesIgnored}`, 세션은 id 순, 프로세스는 PID 순. 프로그램(첫 토큰)의 base name(`/`·`\` 모두 구분자)이 정확히 `claude`·`claude.exe`·`codex`·`codex.exe`(그래서 데스크톱 앱 `Claude`·`Claude.exe` 는 아님)이거나, 역슬래시를 슬래시로 바꾼 명령줄에 표지가 있으면 그 도구. 데몬 PID(`os.Getpid()`) 제외. `Sessions` 가 없으면 오류 — 빠뜨린 배선이 조용히 전제 조건을 통과하지 않게.
- 도메인(`domain/lado_setup.go`): 위치 `LadoSetupLocationManaged = "managed"`, 사유 `LadoSetupReasonSymlink = "symlink"`·`LadoSetupReasonUnreadable = "unreadable"`, 필드 `LadoSetupEntry.KeptInPlace []string`, 항목이 아닌 줄의 Name 주석. `TestLadoSetupCodesAreStable` 표에 셋을 더했다.
- agentaccount(`claude.go` +7): `ClaudeLoginSettingKeys()`, `ClaudeLoginEnvNames()` — 복사본을 돌려주는 한 줄 함수 둘.
- 테스트: 필수 8개와 7개 더 — `TestInventoryDoesNotAdoptThroughALinkedSettingsFile`, `TestFileSumRefusesALink`, `TestInventoryRefusesWhatAVersionCannotHold`, `TestInventoryReadsClaudeJSONInsideTheClaudeFolderOnce`, `TestInventoryListsNothingForMissingFolders`, `TestInventoryKeepsTheLastScan`, `TestInventoryDoesNotWaitOnNamedPipes`(`fifo_test.go`, linux·darwin 빌드 태그). 모두 `t.TempDir()` 의 가짜 홈이고, managed 는 실제 store(`sqlitetest.MustOpen`)에 채택·버전 추가·생성·삭제를 해서 본다. 프로젝트 테스트의 "git 이 못 읽는 폴더"는 없는 저장소를 가리키는 `.git` 파일로 만들어 임시 폴더의 부모와 상관없이 실패한다.
- 엔트리와 다르게 한 것과 이유:
  1. 새 사유 코드 둘. `symlink` — 항목 폴더의 링크(엔트리가 허락한 것)에 더해, 링크인 `settings.json`·`~/.claude.json`·`config.toml` 의 키·서버: 읽기는 링크를 따라가지만 채택은 그 파일을 다시 쓰므로 링크나 그 대상이 바뀐다. `unreadable` — 열 수 없거나 파싱되지 않는 파일, 일반 파일이 아닌 것(FIFO 등), 트리 매니페스트가 받지 않는 이름(역슬래시, UTF-8 아님), JSON 이 담지 못하는 TOML 값(NaN). `unknown_location` 에 넣으면 "모르는 곳"과 "읽지 못함"이 섞인다.
  2. 위치 `managed`: 관리 항목은 네 위치 어디에도 없다(용어집의 "관리 폴더"). 경로는 HARNESS §9.3 배치 — Claude `…/lado/harness/claude/<이름>`, `settings.json#<키>`, `skills/<이름>` 등, `mcp.json#mcpServers.<이름>`; Codex `…/codex/<이름>`, `config.toml#mcp_servers.<이름>`.
  3. 필드 `KeptInPlace`: "로그인 env 는 … 세부로 표시"를 담을 칸이 도메인에 없었다. 값은 `LadoSetupClaudeLoginEnv` 순서로 걸러진 이름들.
  4. `env` 에 로그인 변수만 있으면 `never/login_key`(KeptInPlace 는 그 이름들). 채택 뒤 원래 settings.json 에 남는 로그인 전용 `env` 가 다음 검사에서 빈 `{}` 인 adoptable 로 떠서 S08 의 new_local 이 매번 생기는 것을 막는다. 링크인 settings.json 의 `env` 는 KeptInPlace 없이 `symlink`(리뷰 Minor 1).
  5. `RunningTools` 는 엔트리의 3값 대신 구조체 `Running` 을 돌려준다 — "dev 무시 사실을 결과에 표시"할 자리(`ProcessesIgnored`, S07 감사 payload 의 `processesIgnored`)가 필요했다. 세션·프로세스에 `Tool` 을 붙여 S07 이 채택 대상 도구로 거를 수 있다.
  6. `Projects`·`Sessions` 는 함수가 아니라 `*sqlite.Store` 모양 인터페이스(`ListProjects`, `ListAllSessions`) — 관찰자들과 같은 꼴이라 S13 은 store 를 그대로 넘긴다. 프로젝트 이름은 `DisplayName`, 없으면 경로의 base, 그것도 없으면 id.
  7. scratch 프로젝트는 읽지 않는다: 저장소가 없고 세션이 그 폴더에서 바로 돌아 결정 k 의 사유("워크트리가 읽지 않음")가 틀린다. workspace 프로젝트는 루트 저장소만 읽는다(남은 틈).
  8. ls-files pathspec 에 `CLAUDE.local.md`·`.claude/settings.local.json` 을 더했다: 엔트리 목록만으로는 그 파일을 추적하는 저장소에서도 "추적 안 됨"으로 잘못 나온다. 추적되면 `repo_tracked`. `*CLAUDE.md` 가 접미사로만 맞춘 `NOTCLAUDE.md` 같은 파일은 버린다.
  9. 엔트리 목록 밖의 상태 이름: Claude `mcp-needs-auth-cache.json`(실제 폴더에서 본 캐시); Codex `archived_sessions`, `session_index.jsonl`, `logs`, `cache`, `models_cache.json`, `shell_snapshots`, `tmp`, `installation_id`, `version.json`, 이름에 `.sqlite` 가 든 것(wal·shm·journal 포함), 점으로 시작하는 이름 → `never/app_state`. 그 밖의 모르는 것(`memories/`, `vendor_imports/`, `keybindings.json`, `state/` 등)은 엔트리대로 `unknown_location`.
  10. 항목 폴더 안의 점으로 시작하는 이름(`.DS_Store`, `.git`)은 항목이 아니라 `not_managed/unknown_location`(이름은 Claude 폴더 기준 경로 `skills/.DS_Store`, 폴더면 들어가지 않음). `agents/`·`commands/` 는 확장자와 상관없이 모든 파일이 항목이다(엔트리의 "항목마다").
  11. 루트(Claude 폴더, `~/.claude.json`, Codex 폴더)를 읽지 못하거나 `~/.claude.json` 이 파싱되지 않으면 검사 전체를 실패시키지 않고 `Root.Error` 에 남긴다(프로젝트 오류와 같은 꼴). 검사가 실패하는 것은 store·프로젝트 목록 오류와 취소뿐이다. `Roots` 는 "경로가 비면 없음"을 줄 없이도 보이게 한다(경로·Found).
  12. `CLAUDE_CONFIG_DIR` 이면 `ClaudeJSON` 이 Claude 폴더 안에 있으므로 폴더 검사에서는 그 파일을 건너뛰고 `claude_json` 으로만 읽는다.
  13. Codex 설정 키 줄과 같은 이름의 폴더 줄(`skills`)은 Name 이 같다 — `Path`(`config.toml#skills` 대 `skills`)로 구분한다.
- 리뷰(서브에이전트 `superpowers:code-reviewer`, sonnet, 전경, 읽기 전용 지시): Critical 1 — `settings.json`·`~/.claude.json`·`config.toml` 의 `os.ReadFile` 이 FIFO 에서 영원히 막히고, 검사 세마포어를 쥔 채라 이후 모든 검사가 멈춘다 → 반영(`openRegular`). Important 2 — 목록을 읽은 뒤 열기 전에 링크로 바뀐 파일을 따라가 해시한다 → 반영(연 파일과 경로의 `SameFile`); 링크인 `config.toml` 테스트가 없다 → 반영. Minor 2 — 링크인 settings.json 의 env 에 KeptInPlace 가 남는다, 복사본이 KeptInPlace 배열을 캐시와 공유한다 → 둘 다 반영. 모두 `c5b9c0a91`. 리뷰 뒤 `go.work.sum` 등 트리 변화 없음.
- 변이 검사(스크래치 사본으로 하나씩 바꾸고 되돌림, 커밋 안 함, 되돌린 뒤 `cmp` 로 확인): 컴파일되는 변이 27개 중 25개를 테스트가 잡았다 — 로그인 키, 로그인 env 걸러내기, 링크 거부(항목·열기 시점), FIFO 비차단, 일반 파일 확인, 인라인 표, 이름, 비밀 값(env·headers·token 각각), 데몬 PID, 끝난 세션, dev 무시, scratch 건너뛰기, 추적된 개인 파일, HTML 이스케이프, `UseNumber`, sqlite 이름, OS 검증, 점 이름, 실행 비트, `NOTCLAUDE.md`, 캐시 복사(목록·KeptInPlace). 남은 둘은 `Inventory` 의 잠금 앞·뒤 캐시 확인을 하나씩 뺀 것 — 남은 하나가 같은 답을 주는 동치 변이다(동시 첫 호출이 한 번 더 검사할 뿐).
- 확인한 명령: `go-pkg-isolated ./internal/service/ladosetup/ ./internal/domain/` → `GO_PKG_OK`; `go test -race -count=1 ./internal/service/ladosetup/` → ok 18.3 s(`TestInventoryMarksManagedItems` 가 race 에서 sqlitetest 템플릿을 만드느라 17 s, race 없이는 1 s 안); `go test ./internal/adapters/agentaccount/ ./internal/domain/` ok; golangci-lint v2.13.2(`GOPROXY=off`) `./internal/service/ladosetup/... ./internal/domain/... ./internal/adapters/agentaccount/...` 0 issues(처음 `nilerr` 1건 → env 값의 모양을 먼저 보게 고침); `gofmt -l` 출력 없음; `GOOS=windows`·`GOOS=darwin go vet ./internal/service/ladosetup/` ok — `syscall.O_NONBLOCK` 은 Windows 에서 0x800 으로 정의되고 `syscall.Open` 이 무시하며 `O_FILE_FLAG_OVERLAPPED`(0x40000000)와 겹치지 않음을 Go 소스로 확인.
- 게이트(HEAD `c5b9c0a91`, 엔트리 커밋 전): `GATES=6 MET=6 UNMET=0` — G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 8/8`, G2 `NO_HOME_LOOKUPS`, G3 `BUDGET_OK 7/15`, G4 `GO_BUILD_OK`, G5 `TREE_CLEAN`. 이 엔트리를 커밋한 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 인벤토리(Claude 폴더·`~/.claude.json`·Codex 폴더·프로젝트 저장소 파일), 상태와 사유, 실행 중 도구, 캐시, 필수 테스트 8개, agentaccount 접근자. 충돌·채택·HTTP 는 없다. 엔트리 밖으로 더한 것: 도메인 상수 셋과 필드 하나, 결과의 `Roots`·`Adoptions`(플랜 §5 의 결과 모양), 막히지 않고 링크를 따라가지 않는 열기, 테스트 7개.
- 남은 틈:
  - Windows 의 procmem 은 실행 파일 이름만 준다 — 네이티브 `claude.exe`·`codex.exe` 는 찾지만 Node 로 띄운 것(`node.exe` + `@anthropic-ai/claude-code`·`@openai/codex`·`claude-agent-acp`)은 표지로 못 찾는다. Windows 는 채택이 거절되는 OS 이니 9단계 실측 때 명령줄 읽기를 정한다. Unix 도 procmem 이 명령줄을 200자에서 자르므로 아주 긴 경로 뒤의 표지는 놓칠 수 있다.
  - workspace 프로젝트는 루트 저장소만 `git ls-files` 한다 — 자식 저장소의 CLAUDE.md 등은 보이지 않는다.
  - 조직 관리 설정(`managed_settings`)은 OS 정책 경로가 Deps 에 없어 검사하지 않는다(도메인 코드만 있음).
  - 폴더 항목 크기 상한이 없다 — 스킬 폴더에 `node_modules` 가 있으면 검사마다 전부 해시한다.
  - Codex 머리 줄 찾기는 줄 단위라 여러 줄 문자열 안의 `[mcp_servers.x]` 같은 줄에 속을 수 있다.
  - 저장소 추적 파일의 크기·해시는 본 체크아웃 작업 트리의 내용이다(워크트리가 받는 브랜치 내용과 다를 수 있음). 결정 l 대로 스냅숏에 들어가지 않는 표시용이다.
  - `Inventory` 의 잠금 뒤 캐시 재확인은 동시 첫 호출에서만 의미가 있고 테스트가 없다(위 변이 검사의 동치 변이).
- 다음 단계 메모:
  - **Deps 이름**(S13 배선): `Store`(`*sqlite.Store`), `Blobs`(ladoblob), `StateDir`, `ClaudeDir`, `ClaudeJSON`, `CodexHome`, `ClaudeDirFromEnv`, `GOOS`(비우면 `runtime.GOOS`), `Projects`·`Sessions`(둘 다 `*sqlite.Store` 그대로), `Processes`(nil → procmem), `DevIgnoreProcesses`, `Clock`, `NewID`, `Log`. `Sessions` 를 빠뜨리면 `RunningTools` 가 오류를 낸다. 데몬 시작 검사는 `Scan(ctx)`.
  - **캐시 무효화 지점**: 캐시를 지우는 함수는 아직 없다. LADO 가 관리 폴더나 원래 위치를 쓴 뒤(S07 채택, S08 복원·keep·revert, S12 변경) 같은 패키지에서 `s.Scan(ctx)` 를 부르면 새 결과가 캐시된다. 취소된 검사는 캐시되지 않는다.
  - **새 코드**(S13 DTO·S14 번역): 사유 `symlink`, `unreadable`; 위치 `managed`; 줄 필드 `KeptInPlace`(env 를 채택해도 제자리에 남는 로그인 변수 이름). 항목이 아닌 줄의 Name 은 파일 이름, Claude 폴더 기준 경로(`skills/.DS_Store`) 또는 설정 키라 Codex 설정 키 `skills` 와 폴더 `skills` 처럼 겹칠 수 있다 — 목록의 키는 `Path` 로(S13·S15).
  - **S06(충돌)**: settings 값과 MCP 서버 값은 정규 JSON(`canonicalJSON`/`marshalCanonical`: 키 정렬, 숫자 그대로). hooks·permissions 는 각각 `settings.json#hooks`·`#permissions` 한 setting 항목이다.
  - **S07(채택)**: 미리보기는 `s.Scan(ctx)` 뒤 `Entries` 에서 Location 이 `managed` 가 아니고 `Status == adoptable` 인 줄을 tool·kind·name 으로 찾는다(같은 이름의 managed 줄이 함께 있을 수 있다). 계획 해시의 `originPath` = 줄의 `Path`, `sha256` = 줄의 `SHA256` — 파일 바이트, 트리 `CanonicalJSON()`, JSON 값 `canonicalJSON` 의 sha 라서 같은 바이트를 블롭에 넣으면 버전 sha 가 인벤토리 sha 와 같다. Codex 서버의 내용은 디코드한 표를 `marshalCanonical` 한 JSON(내용 종류 `json`). `tomledit` 이 `codexServerHeader(name)` 과 같은 머리 줄 규칙을 쓰면 인벤토리가 adoptable 이라 한 서버를 늘 잘라낼 수 있다. env 를 옮긴 뒤 원래 settings.json 에 로그인 변수가 하나도 남지 않으면 `env` 키를 통째로 지운다(빈 `{}` 를 남기면 다음 검사에서 adoptable 로 떠 new_local 이 생긴다). 전제 조건은 `RunningTools` 의 `Running` — 세션·프로세스의 `Tool` 로 도구별로 거르고, `ProcessesIgnored` 를 감사 payload `processesIgnored` 로. 원본 읽기는 `fileSum`/`readRegular`(막히지 않음, 항목은 링크 거부)를 쓴다. 관리 경로는 `managedPath(stateDir, tool, kind, name)` 가 HARNESS §9.3 배치로 정해 두었다 — `managed.go` 가 다르게 정하면 이 함수를 함께 고친다. race 로 패키지 전체를 돌리면 `TestInventoryMarksManagedItems` 의 sqlitetest 템플릿 만들기에 17 s 가 든다(S07-G2).
  - **S08(변경 감지)**: 원래 위치에 같은 tool·kind·name 이 또 있으면 그 줄은 `adoptable`·ItemID 없음으로 나온다 — new_local 판단은 기준선과 비교해 S08 이 한다. 결과에 `Drift` 를 더하는 것도 S08.
  - **S09(홈)**: `env` 항목 내용에는 로그인 변수가 없다 — 홈의 settings.json 에 env 를 덮어쓸 때 기준 파일 env 의 로그인 변수를 지우지 말고 합친다(아니면 계정·API 키 로그인이 사라진다). 끝난 세션의 홈 정리는 `Deps.Sessions`(`ListAllSessions` 의 `IsTerminated`)로 할 수 있다.
  - **S10**: Codex 서버 버전 내용은 정규 JSON 이라 `-c` 인라인 표로 낼 때 JSON → TOML 변환이 필요하다(go-toml 디코드 값: 문자열·int64·float64·bool·배열·표, 숫자는 `json.Number` 로 읽으면 정수·실수를 가를 수 있다).
