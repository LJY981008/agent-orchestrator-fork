# 2026-10-26 LADO 로컬 설정 채택 — S06 충돌 검사·수락 대조·AO 시스템 층 해시

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 g·j, §6) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-013: 채택 항목이 AO 가 넣는 것(hook, `ao` MCP 서버, `using-ao` 스킬, 예약 env)이나 LADO 규칙(권한 상한)과 부딪히면 실행 전에 보여 주고, 막아야 할 것은 막는다. 검사는 결정적이다(AI 비교는 나중). 경고는 수락할 수 있고, 수락은 항목 버전·범위에 묶여 스냅숏에 남는다. AO 가 모든 세션에 더하는 층의 해시도 여기서 만든다(스냅숏의 `aoSystem`).

## 범위

- 만든다: `service/ladosetup/conflicts.go`, `service/ladosetup/aosystem.go`, 테스트.
- 바꾼다(필요할 때만, diff 예산 안): AO 가 넣는 Claude hook 이벤트 목록·명령 접두사(`adapters/agent/claudecode/hooks.go`), Codex hook 이벤트(`adapters/agent/codex/hooks.go`), `using-ao` 스킬 바이트(`skillassets`)를 읽는 한 줄 접근자. 목록을 이 패키지에 복사하지 않는다.
- 하지 않는다: 스냅숏 반영(S11), HTTP(S13).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 입력: 한 범위(전역·프로젝트·태스크)의 켜진 관리 항목(도구별)과 그 최신 버전 내용, 원래 위치에 남은 설정(채택 안 된 settings 키·서버 — 채택 전에도 미리보기에서 같은 검사를 돌린다), 권한 상한 모드(호출자가 넘김 — `ladopolicy` 의 상한 규칙 값), 프로젝트의 저장소 `.claude/settings.json`(있으면), 켜진 플러그인의 hook(`<실제 Claude 폴더>/plugins/installed_plugins.json` 의 설치 경로 → `hooks/hooks.json`; 못 읽으면 info `LADO_SETUP_PLUGIN_HOOKS_UNREADABLE`).
2. 검사(플랜 §6 표 그대로): 코드·심각도·`details`(`item`, 그리고 필요한 것 — `event`, `matcher`, `rule`, `key`, `mode`, `ceiling`, `plugin`). `permissions.deny`/`allow` 패턴 대조는 Claude 의 규칙 문법(`Bash(ao:*)`, `Bash(ao *)`, `Bash(ao)`, `mcp__ao`, `mcp__ao__*`)을 대상으로 접두 일치. `PreToolUse` matcher 는 정규식처럼 쓰이므로 `Bash`·`*`·빈 matcher·`.*` 를 Bash 를 덮는 것으로 본다. 상한 비교는 `accept-edits < auto < bypass-permissions` 이고 Claude 의 `defaultMode` 값(`acceptEdits`, `auto`, `bypassPermissions`, `plan`, `default`)을 AO 모드로 옮겨 비교한다(`plan`·`default` 는 상한 위가 아님).
3. hook 경로(결정 g): `hooks` 와 `statusLine` 의 명령 문자열에서 원래 Claude 폴더의 옮겨질 경로(`~/.claude/…`, `$HOME/.claude/…`, `${HOME}/.claude/…`, 절대 폴더)를 찾는 함수 `movedPathReferences(command, claudeDir, movedNames)` 를 이 단계에 두고 S07 이 다시 쓰기에 쓴다. 다시 쓰지 못한 참조는 blocking `LADO_SETUP_HOOK_PATH_MOVED`.
4. 수락 대조: 경고 결과는 (code, item, detail, 항목 버전 sha256)이 같고 범위가 같거나 더 넓은(전역 ⊇ 프로젝트 ⊇ 태스크) 살아 있는 수락이 있으면 `accepted` 로 표시하고 수락 id 를 단다. blocking·info 수락 요청은 `LADO_SETUP_CONFLICT_NOT_ACCEPTABLE`.
5. AO 시스템 층(`aosystem.go`): 정규 JSON `{"claudeHookEvents":[...],"claudeHookCommandPrefix":"...","codexHookEvents":[...],"mcpServer":{"name":"ao","args":["mcp"]},"usingAoSkillSHA256":"..."}` 의 sha256. 입력은 AO 코드에서 읽는다(필요하면 내보내는 접근자를 한 줄씩 더함). 데몬당 한 번 계산.
6. 커밋 메시지 예: `feat(lado): conflict checks for adopted local setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S06-conflicts.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestConflictMCPServerNamedAO` — HARNESS §15 항목 13: Claude·Codex 어느 쪽이든 `ao`(대소문자 무관) 서버는 blocking
- `TestConflictChecksTable` — 플랜 §6 의 코드마다 걸리는 경우와 안 걸리는 경우(표 테스트)
- `TestConflictPluginHooksAreChecked` — 켜진 플러그인의 `UserPromptSubmit` hook 은 경고, 꺼진 플러그인은 검사 안 함, 못 읽으면 info
- `TestConflictAcceptanceMatching` — 버전이 바뀌면 수락이 풀림, 넓은 범위 수락이 좁은 범위를 덮음, blocking·info 수락 거부
- `TestMovedPathReferences` — 네 가지 경로 표기, 옮기지 않는 경로는 무시, 따옴표 안의 `~` 는 참조로 보지 않음
- `TestAOSystemHashFollowsAOSources` — 입력이 AO 의 실제 값과 같음, 입력 하나가 바뀌면 해시가 바뀜

## 수용 게이트

- [ ] S06-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S06-G1: 필수 테스트 6개가 격리된 홈에서 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestConflictMCPServerNamedAO TestConflictChecksTable TestConflictPluginHooksAreChecked TestConflictAcceptanceMatching TestMovedPathReferences TestAOSystemHashFollowsAOSources
  EXPECT: GO_TESTS_OK 6/6
- [ ] S06-G2: 플랜의 충돌 코드가 모두 코드에 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has backend/internal/service/ladosetup/conflicts.go LADO_SETUP_MCP_NAME_TAKEN LADO_SETUP_SKILL_NAME_TAKEN LADO_SETUP_ENV_RESERVED LADO_SETUP_DENIES_AO LADO_SETUP_HOOK_PATH_MOVED LADO_SETUP_DENIES_GIT LADO_SETUP_HOOK_MAY_BLOCK_TOOLS LADO_SETUP_HOOK_ADDS_PROMPT_TEXT LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING LADO_SETUP_HOOK_SHARES_AO_EVENT LADO_SETUP_PROJECT_OVERRIDES
  EXPECT: HAS_OK
- [ ] S06-G3: 업스트림 접근자 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 30 backend/internal/adapters/agent/claudecode backend/internal/adapters/agent/codex backend/internal/skillassets
  EXPECT: BUDGET_OK
- [ ] S06-G4: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S06-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S06-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `25b703c8a` (`docs(lado): record S05`), 브랜치 `feat/lado-local-setup`, 트리 깨끗. Linux, go1.27.1(스크래치 툴체인이 PATH 에 있음), golangci-lint v2.13.2 는 이전 세션의 빌드본을 `GOPROXY=off` 로 다시 씀.
- 출발점: `service/ladosetup` 에는 인벤토리·실행 중 도구만 있고 충돌 코드·검사·AO 시스템 층이 없다. 도메인에는 문제 꼴 `LadoPolicyProblem`, 수락 `LadoConflictAcceptance`, 상한 순위 `LadoPermissionRank`(accept-edits 1, auto 2, bypass-permissions 3, 그 밖 0)가 있다.
- AO 소스(접근자 없음): Claude `claudeManagedHooks`(11 이벤트)·`claudeHookCommandPrefix = "ao hooks claude-code "`(`adapters/agent/claudecode/hooks.go`, 둘 다 비공개), Codex `codexManagedHooks`(7 이벤트, 비공개 타입), `skillassets` 의 embed `files`(비공개, `SkillName` 만 공개), `ao` MCP 서버는 `session_manager/chat_spawn.go` `aoMCPServers` 의 리터럴 `Name: "ao"`, `Args: []string{"mcp"}`. 예산 대상 세 폴더는 태그 이후 0줄.
- 플러그인 파일 모양은 실제 폴더에서 읽기만 해서 봤다(쓰기·복사 없음): `plugins/installed_plugins.json` 은 `{"version":2,"plugins":{"<이름>@<마켓>":[{"scope":"user","installPath":"/abs",…}]}}`, 플러그인의 `hooks/hooks.json` 은 settings 의 hooks 와 같은 `{"hooks":{<이벤트>:[{matcher, hooks:[{type, command}]}]}}`.
- 앞 단계 메모 중 이 단계 몫: S03 — 결과는 `domain.LadoPolicyProblem` 꼴(`Details["item"]` = 항목 id), 수락은 `domain.LadoConflictAcceptance{Code, ItemID, Detail, ContentSHA256, Scope, ScopeID}`, 넓이는 Global ⊇ Project ⊇ Task. S05 — settings 값·MCP 서버 값은 정규 JSON(`canonicalJSON`), `hooks`·`permissions` 는 각각 한 setting 항목.

## After — 수행 후 실측

- 코드 커밋 2건:
  - `949e6a1a7` `feat(lado): conflict checks for adopted local setup` — 7 파일, +1752.
  - `bd0f3a53b` `fix(lado): read extra separators in moved hook paths` — 3 파일, +84 −21(리뷰 반영, 아래).
- `service/ladosetup/conflicts.go`:
  - 코드 상수 12개(플랜 §6 의 11개 + `LADO_SETUP_PLUGIN_HOOKS_UNREADABLE`)와 심각도 표 `conflictSeverity`, `CodeConflictNotAcceptable = "LADO_SETUP_CONFLICT_NOT_ACCEPTABLE"`, 센티널 `ErrConflictNotAcceptable`, `checkAcceptable(code)`(경고가 아니거나 모르는 코드면 그 오류).
  - 입력 `ConflictInput{ProjectID, TaskID, Setup []SetupContent, Moved []string, Ceiling, ProjectSettings []byte, Acceptances}`, 한 줄 `SetupContent{ItemID, Tool, Kind, Name, Path, SHA256, Content}`. 결과 `Conflict{Code, Severity, ItemID, Path, Detail, ContentSHA256, Details, AcceptanceID}`, `Accepted()`, `Problem()`(규칙 문제 꼴). 진입점 `(*Service).Conflicts(in) []Conflict` — 플러그인 hook 은 `Deps.ClaudeDir` 에서 읽는다. 결과는 중복 없이(code·item·path·detail) 심각도 → 코드 → 항목 → 경로 → detail 순.
  - 검사: MCP 서버 이름 `ao`(대소문자 무관, 두 도구); Claude 스킬·명령 이름 `using-ao`(`skillassets.SkillName`, `.md` 뗌, 하위 폴더 명령은 이름에 폴더가 붙어 해당 없음); `env` 의 `AO_*`·`CLAUDE_CONFIG_DIR`·`CLAUDE_SECURESTORAGE_CONFIG_DIR`·`CODEX_HOME`(대문자로 비교); `permissions.deny` 의 규칙을 접두로 읽어 ao·`mcp__ao` 와 git·gh 를 덮는지(`bashRuleCovers`, `mcpRuleCovers`); `permissions.defaultMode` 를 AO 모드로 옮겨 상한과 비교; hooks 의 `PreToolUse` matcher(`matcherCoversAOTools`), `UserPromptSubmit`·`SessionStart` 명령 hook, AO 이벤트(`claudecode.ManagedHookEvents()`); hooks·`statusLine` 명령의 옮겨진 경로; 저장소 `.claude/settings.json` 의 같은 최상위 키; `enabledPlugins` 가 켠 플러그인의 hook.
  - 수락 대조 `accept`: 경고이고 항목이 있으며, 취소되지 않은 수락의 code·item·detail·content sha 가 같고 범위가 덮으면(`ConflictInput.covers`: Global 은 어디서나, Project 는 그 프로젝트와 그 태스크, Task 는 그 태스크) `AcceptanceID` 를 단다. 입력 순서(store 는 오래된 것부터)의 첫 수락.
  - 경로 참조 `movedPathReferences(command, claudeDir, movedNames) []pathRef` — `pathRef{Start, End, Name, Replacement}`. POSIX 따옴표를 읽는 `shellBytes`(작은따옴표, 큰따옴표 안에서는 `$`·백틱·`"`·역슬래시·줄바꿈 앞의 역슬래시만 이스케이프, 따옴표 밖 역슬래시, 역슬래시-줄바꿈은 줄 잇기) 위에서 네 표기를 찾고, `[Start, End)` 는 폴더 부분(`~/.claude`, `$HOME/.claude`, `${HOME}/.claude`, 절대 폴더 — 뒤의 `/` 제외), `Replacement` 는 그 자리에 쓸 `$CLAUDE_CONFIG_DIR` 를 시작·끝의 따옴표 상태에 맞춰 감싼 것(`configDirQuoted`, 예: `bash ~/.claude/hooks/x.sh` → `bash "$CLAUDE_CONFIG_DIR"/hooks/x.sh`, `"$HOME"/.claude/…` → `"$CLAUDE_CONFIG_DIR"/…`, `'<절대>/…'` → `''"$CLAUDE_CONFIG_DIR"'/…'`).
- `service/ladosetup/aosystem.go`: `AOSystem{ClaudeHookEvents, ClaudeHookCommandPrefix, CodexHookEvents, MCPServer{Name, Args}, UsingAoSkillSHA256}` — 정규 JSON 은 엔트리의 키 순서 그대로(필드 선언 순), 이벤트는 정렬·중복 제거, HTML 이스케이프 없음. `CurrentAOSystem() (AOSystem, string, error)` 는 `sync.OnceValues` 로 프로세스(데몬)당 한 번 계산한 층의 복사본과 sha256. 스킬 sha 는 embed 파일들의 `LadoSetupTree` 매니페스트 sha(폴더 항목과 같은 방식, `treeOfFS`).
- 업스트림 접근자(28줄/30): `claudecode.ManagedHookEvents()`·`claudecode.HookCommandPrefix()`, `codex.ManagedHookEvents()`, `skillassets.Files()`(`fs.Sub(files, SkillName)`).
- 엔트리와 다르게 한 것과 이유:
  1. 입력의 "켜진 관리 항목"과 "원래 위치에 남은 설정"을 한 목록 `Setup` 으로 받는다 — 검사가 같고, 항목 id 가 없는 줄은 `Path` 로 이름 붙는다(`Details["origin"]`). 무엇이 실효인지(관리 항목이 같은 키를 덮는지 등)는 호출자(S07·S11)가 정한다.
  2. 옮겨진 이름은 입력 `Moved`(Claude 폴더 기준 슬래시 경로)로 받는다 — 미리보기(S07)는 옮길 줄을, 실행 때(S11)는 관리 항목의 경로를 넘긴다.
  3. `detail` 은 결과를 가르는 값들을 빈 것 빼고 공백으로 이은 것: env → 변수 이름, deny → 규칙, `PreToolUse` → matcher, 프롬프트·AO 이벤트 → 이벤트, 옮겨진 경로 → `<이벤트> <이름>`(statusLine 은 이름), 플러그인 결과는 앞에 플러그인 이름(`notes@market UserPromptSubmit`), 하나뿐인 코드(MCP·스킬·defaultMode·project override)는 `""`. `Details` 에는 `path`(옮겨진 이름)와 `origin`(항목 없는 줄의 위치)을 더했다.
  4. 플러그인: `enabledPlugins` 값이 JSON `true` 인 것만, 설치는 scope `user`(또는 scope 없는 1판 객체 꼴)만. 사용자 설치가 없거나, 목록·`hooks/hooks.json` 을 못 읽으면 info(`plugin`), hooks 파일이 없으면 결과 없음. 결과는 `enabledPlugins` 항목(그 버전 sha)에 붙는다. 옮겨진 경로 검사는 플러그인 hook 에 하지 않는다(플러그인 안의 경로).
  5. `ao` MCP 서버의 이름·인자는 `aosystem.go` 상수다 — 접근자 목록에 `session_manager` 가 없어서. 대신 테스트가 `session_manager/chat_spawn.go` 를 `go/parser` 로 읽어 `aoMCPServers` 의 리터럴과 같은지 본다.
  6. `PreToolUse` matcher: 비었거나 `*` 면 전부, 글자·숫자·`_`·`|` 만이면 정확한 이름 목록(`Bash`, `mcp__ao`, `mcp__ao__<도구>`), 그 밖은 정규식으로 `Bash`·`mcp__ao__` 에 대 보고 식 안에 `mcp__ao`(뒤가 `__` 또는 단어 경계)가 있는지도 본다. Go 가 컴파일하지 못하는 식(JS 문법)은 덮는 것으로 본다(경고라 수락 가능).
  7. defaultMode 의 모르는 값(`dontAsk` 등)은 상한 아래, 상한이 비었거나 상한 모드가 아니면 검사하지 않는다. `Details` 의 `mode` 는 Claude 값, `ceiling` 은 AO 값.
  8. `=`·`:` 뒤의 `~` 는 대입이 아니어도(`--config=~/…`) 참조로 센다 — 셸은 펼치지 않지만 프로그램이 펼칠 수 있고, 다시 쓴 값은 어느 쪽이든 맞다(리뷰 Important 를 이 이유로 받지 않음, 주석·테스트로 명시).
- 리뷰(서브에이전트 `superpowers:code-reviewer`, sonnet, 전경, 읽기 전용 지시; 함수를 따로 떼어 bash 와 대조): "Critical" — `~/.claude//hooks/x.sh`·`~/.claude/./hooks/x.sh` 처럼 같은 파일을 가리키는 표기를 놓침 → 반영(이름 앞·조각 사이의 `/`·`./` 연속을 건너뜀, `..` 은 풀지 않음). Important — `--flag=~` 과잉 탐지 → 위 8 의 이유로 유지. Minor — Codex 이벤트 단언이 같은 접근자끼리 비교 → 반영(가짜 `codex` 를 PATH 에 두고 `codex.New().GetLaunchCommand` 의 `-c hooks.<이벤트>=` 를 읽어 비교, Windows 는 PATH 전에 다른 곳을 찾아 건너뜀). 그 밖(`configDirQuoted` 아홉 조합, 규칙·matcher, 수락, 플러그인 꼴, 빈 문자열·끝 역슬래시·닫히지 않은 따옴표에서 패닉 없음)은 문제없다는 보고. 모두 `bd0f3a53b`.
- 변이 검사(스크래치 사본으로 하나씩 바꾸고 되돌림, `cmp` 로 복원 확인, 커밋 안 함): 컴파일되는 변이 32개 중 31개를 잡았다 — MCP 대소문자, 수락의 sha·프로젝트·태스크 범위·심각도, `$HOME` 확장·작은따옴표, 이름 경계, 앞 경로 문자, 따옴표 맞춤, 플러그인 켜짐·scope·hooks 없음, `AO_` 접두, Bash 정확·접두·`:*`, mcp 규칙, 상한 없음, 명령 hook 만, 정규식 탐침, 단순 matcher, `.md` 떼기, 스킬 해시 필드, Codex 이벤트·접두·MCP 인자, 이벤트 정렬, 반환값 복사, `/`·`./` 건너뛰기. 남은 하나는 `~` 의 따옴표 확인을 뺀 것 — 단어 시작의 인접 검사와 따옴표 밖 `/` 검사가 같은 경우를 이미 막는 동치 변이다.
- 확인한 명령: `go-pkg-isolated ./internal/service/ladosetup/ ./internal/adapters/agent/claudecode/ ./internal/adapters/agent/codex/ ./internal/skillassets/` → `GO_PKG_OK`(수정 뒤 ladosetup 다시 `GO_PKG_OK`); `go test -race -count=1 -run '^(TestConflict|TestMovedPath|TestAOSystem)' ./internal/service/ladosetup/` ok 1.0 s; golangci-lint v2.13.2 `./internal/service/ladosetup/... ./internal/adapters/agent/claudecode/... ./internal/adapters/agent/codex/... ./internal/skillassets/...` 0 issues(처음 goimports 1건 → `gofmt -w`); `gofmt -l` 출력 없음; `GOOS=windows`·`GOOS=darwin go vet ./internal/service/ladosetup/` ok.
- 게이트(HEAD `bd0f3a53b`, 엔트리 커밋 전): `GATES=7 MET=7 UNMET=0` — G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 6/6`, G2 `HAS_OK`, G3 `BUDGET_OK 28/30`, G4 `NO_HOME_LOOKUPS`, G5 `GO_BUILD_OK`, G6 `TREE_CLEAN`. 이 엔트리를 커밋한 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 충돌 검사 11 코드와 플러그인 info, 수락 대조, 옮겨질 경로 참조 함수, AO 시스템 층 해시, 필수 테스트 6개, 한 줄 접근자. 스냅숏 반영·HTTP·수락 저장은 없다. 엔트리 밖으로 더한 것은 `Replacement`(S07 이 그대로 끼울 값)와 `/`·`./` 읽기.
- 남은 틈:
  - Windows 표기(`%USERPROFILE%\.claude\…`, 역슬래시 경로)와 PowerShell 따옴표는 읽지 않는다 — `shellBytes` 는 POSIX 다. Windows 채택은 9단계까지 거절이라 지금은 문제없다.
  - `~`·`$HOME` 표기는 늘 `<홈>/.claude` 로 본다. 데몬의 Claude 폴더가 `CLAUDE_CONFIG_DIR` 에서 왔고 그게 `<홈>/.claude` 가 아니면 다른 폴더를 가리키는 참조까지 잡는다(과잉). `..`, `$HOME//.claude`, `cd ~/.claude && ./hooks/x.sh`, 변수에 담은 경로는 놓친다. `host:~/.claude/…`(원격 경로)도 잡는다.
  - 플러그인: project·local scope 설치, 저장소 settings 의 `enabledPlugins`, `.claude-plugin/plugin.json` 안의 hooks 는 읽지 않는다. 플러그인 hook 경고의 수락은 `enabledPlugins` 버전에 묶여 플러그인이 업데이트돼도 풀리지 않는다 — 플랜 결정 2 의 "설치된 것의 해시"(S11 스냅숏)가 다시 보기를 맡는다.
  - `permissions.allow`·`ask` 는 검사하지 않는다(`ask` 의 ao 명령은 작업자를 멈출 수 있다). 8단계의 allow 대 보호 게이트 검사가 `bashRuleCovers`·`mcpRuleCovers` 를 다시 쓸 수 있다.
  - Codex hook 은 채택 대상이 아니라 Codex 쪽 hook 검사는 없다(층 해시에만 Codex 이벤트).
  - 저장소 `.claude/settings.json` 은 최상위 키만 본다(병합되는 `permissions`·`hooks` 도 "덮음" info).
- 다음 단계 메모:
  - **S07(채택)**: 미리보기는 `s.Conflicts(ConflictInput{Setup: …, Moved: …, Ceiling: …})`. `Setup` 은 옮길 줄과 남을 줄을 `SetupContent{Tool, Kind, Name, Path: 줄의 Path, SHA256: 줄의 SHA256, Content}`(항목 id 없음)로 — `Content` 는 settings·서버의 정규 JSON(env 는 로그인 변수를 뺀 값, 인벤토리가 해시한 것). `Moved` 는 이동 줄 중 Claude 폴더 파일·폴더 종류의 폴더 기준 경로: instructions 는 이름 그대로(`CLAUDE.md`), 나머지는 `claudeItemDirs` 의 폴더 + `/` + 이름(`hooks/x.sh`, `skills/review`, `agents/a/b.md`). 다시 쓰기(결정 g)는 hooks 값(이벤트 → 그룹 → `hooks[].command`, `claudeHooks` 가 읽는 꼴)과 `statusLine.command` 마다 `movedPathReferences` 를 끝에서부터 `cmd[:Start] + Replacement + cmd[End:]` 로 끼우고, 미리보기에 참조마다(이름, 바꾸기 전·후) 싣는다. 다시 쓴 값으로 검사하면 남는 `LADO_SETUP_HOOK_PATH_MOVED` 는 "다시 못 쓴 참조"(제자리에 남는 hooks·statusLine 등)뿐이다. `ClaudeDirFromEnv` 이면 `~`·`$HOME` 표기가 그 폴더인지 이 패키지는 모른다(홈을 찾지 않는다): `[Start, End)` 가 `~`·`$` 로 시작하는 참조를 버리거나(위 과잉), 데몬이 홈도 `Deps` 로 넘기게 한다(S13 배선).
  - **S11(정책)**: 실효 setup → `SetupContent{ItemID, Tool, Kind, Name, SHA256: 버전 sha, Content: settings·서버면 블롭 바이트}`(다른 종류는 이름만), `Moved` = 관리 Claude 파일·폴더 항목의 경로(위와 같이), `Ceiling` = 해석의 `Ceiling`, `ProjectSettings` = 프로젝트의 `.claude/settings.json`(없으면 nil), `Acceptances` = `ListLiveLadoConflictAcceptances`, 범위 `ProjectID`·`TaskID`. 문제 = 수락 안 된 결과의 `Problem()`, `acceptedConflicts` = 수락된 결과의 `{Code, ItemID, Detail, AcceptanceID}`. `aoSystem.sha256` = `CurrentAOSystem()` 의 둘째 값(오류면 해석 실패로). 원래 위치에 남은 설정을 넣을지는 S11 이 정한다 — 넣으면 수락할 수 없는(항목 없음) 경고가 늘 남는다.
  - **S10**: 쓸 것 없음. 해시 함수는 `CurrentAOSystem()` 하나.
  - **S12(수락)**: 요청 확인은 `checkAcceptable(code)` → `ErrConflictNotAcceptable` → `CodeConflictNotAcceptable`. 저장할 `Detail`·`ContentSHA256` 은 지금 결과(`Conflict.Detail`, `ContentSHA256`)에서 가져온다. 항목 없는 결과는 수락할 수 없다(FK).
  - **S13·S14(DTO·번역)**: 결과 필드 `code, severity, itemId, path, detail, contentSha256, details, acceptanceId`; 번역할 코드 13개(12 + `LADO_SETUP_CONFLICT_NOT_ACCEPTABLE`), details 키 `item, origin, event, matcher, rule, key, mode, ceiling, plugin, path`.
