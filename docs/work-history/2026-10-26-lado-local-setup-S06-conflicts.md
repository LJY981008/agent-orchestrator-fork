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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 검사 함수의 입력 모양, S07 이 쓸 경로 참조 함수, S10·S11 이 쓸 해시 함수)
