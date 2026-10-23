# 2026-10-22 LADO 규칙 코어 — S05 ladopolicy: 내장 규칙·capability 표·AO 지시문

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§5 앞부분) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

해석기(S06)가 읽을 순수 재료 세 가지를 만든다: 5단계의 내장 규칙 목록(보호 게이트 2 + 한국어 커밋 1), 에이전트별로 LADO 가 검증한 capability 표(HARNESS §7 — 검증 안 된 것은 막는다), AO 프로젝트 지시문(`agentRules`·`agentRulesFile`·`orchestratorRules`)을 LADO 가 Project 항목으로 읽는 방법(HARNESS §2, HAR-001).

## 범위

- 만든다: 새 패키지 `backend/internal/service/ladopolicy/` — `builtins.go`(패키지 문서 주석은 여기 한 번만), `capabilities.go`, `aorules.go`, 테스트 `builtins_test.go`, `capabilities_test.go`, `aorules_test.go`.
- 하지 않는다: 해석·스냅숏(S06), 서비스·store 연결(S07). DB·HTTP 의존 없음 — 이 단계 코드는 순수 함수다.

## 절차

1. `builtins.go`: `func Builtins() []domain.LadoRule` — 플랜 §5 표 그대로 3개. 각 규칙은 `Builtin: true`, 보호 여부, `Global`(on/on/off), `Version.Number = 1`, `Version.Actor = "lado"`, `Version.Content`(이름·분류·지시문·enforcement·config), `Version.SHA256 = Content.SHA256()`. 버전 id·시각은 서비스(S07)가 붙인다. 이름·지시문 문자열은 플랜 그대로(영어).
2. `capabilities.go`: `type CapabilityClass string`(`CapabilityPrompt = "prompt"`, `CapabilityPermissionMode = "permission_mode"`), `type CapabilityStatus string`(`supported|degraded|unsupported|unverified`), `type Capability struct { Class CapabilityClass; Status CapabilityStatus }`, `func CapabilityOf(h domain.AgentHarness, c CapabilityClass) CapabilityStatus`. 표: claude-code·codex 둘 다 supported, cursor prompt unsupported, fx prompt degraded, 나머지와 빠진 칸 unverified. 표 위 주석에 근거(플랜 §5 Capabilities 문단의 플래그·필드, `adapters/agent/cursor/cursor.go`·`adapters/agent/fx/fx.go` 주석)를 적는다. `domain.HarnessClaudeCode`·`HarnessCodex`·`HarnessCursor`·`HarnessFX` 상수를 쓴다.
3. `aorules.go`: `type AORuleKind string`(`worker_instructions`, `worker_instructions_file`, `orchestrator_instructions`), `type AORule struct { Kind AORuleKind; Path string; SHA256 string; Chars int; ReadError string }`, `func ReadAORules(project domain.ProjectRecord) []AORule`. 순서는 위 kind 순. 텍스트는 `strings.TrimSpace` 뒤 sha256(hex)·글자 수(rune). 파일은 `session_manager/prompt.go` 의 `projectRelativeFile`·`buildProjectRules` 와 같은 규칙(절대 경로·`..` 거부, `project.Path` 아래)으로 읽고 4 MiB 초과·읽기 실패·경로 거부는 `ReadError` 에 사유. 그 함수는 비공개라 가져올 수 없으니 작은 사본을 두고 주석으로 출처를 밝힌다(업스트림 파일은 고치지 않는다). 빈 설정은 항목을 만들지 않는다.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): built-in rules, agent capabilities and AO instructions` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S05-policy-catalog.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (`backend/internal/service/ladopolicy/`)

- `TestBuiltinsAreTheStepFiveCatalog` — id 3개(정확히), 보호 2·일반 1, ceiling config `{"mode":"auto"}`, 한국어 규칙은 global off·prompt 만·지시문 일치, 모든 sha256 이 `Content.SHA256()` 과 같다
- `TestCapabilityTable` — claude-code·codex 의 두 class supported, cursor prompt unsupported, fx prompt degraded, gemini·빈 값은 unverified, cursor permission_mode 는 unverified
- `TestReadAORulesHashesTrimmedText` — 앞뒤 공백만 다른 지시문이 같은 sha256, 글자 수는 rune 기준, 파일 지시문은 프로젝트 아래 파일을 읽는다
- `TestReadAORulesRefusesPathsOutsideTheProject` — `../x.md`·절대 경로는 ReadError 가 있고 파일을 읽지 않는다
- `TestReadAORulesReportsUnreadableFiles` — 없는 파일·4 MiB 초과는 ReadError, 다른 항목은 그대로 나온다

## 수용 게이트

- [ ] S05-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S05-G1: 필수 테스트 5종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladopolicy/ TestBuiltinsAreTheStepFiveCatalog TestCapabilityTable TestReadAORulesHashesTrimmedText TestReadAORulesRefusesPathsOutsideTheProject TestReadAORulesReportsUnreadableFiles
  EXPECT: GO_TESTS_OK 5/5
- [ ] S05-G2: 패키지 전체 통과(-race)
  CHECK: bash .claude/gate/lado-rules-core.sh go-race ./internal/service/ladopolicy/
  EXPECT: GO_RACE_OK
- [ ] S05-G3: 계약 이름이 플랜 그대로다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/builtins.go 'func Builtins() []domain.LadoRule' 'Permission ceiling' 'Commit messages in Korean' && bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/capabilities.go 'func CapabilityOf(' '"permission_mode"' '"unverified"' && bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/aorules.go 'func ReadAORules(' '"worker_instructions_file"'
  EXPECT: HAS_OK
- [ ] S05-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S05-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `2ba15b0a28a3324f3d75bf8c120b580e1611efa2`(`docs(lado): record S04`), 트리 깨끗.
- `backend/internal/service/ladopolicy/` 는 없다. 내장 규칙 id 상수·`LadoRuleContent`·`LadoCeilingConfig` 는 S02 도메인에 있고, store(S03)는 버전을 저장할 때 내용을 `Normalize()` 하고 sha256 을 다시 계산한다.
- AO 지시문은 `session_manager/prompt.go` 의 비공개 `buildProjectRules`·`projectRelativeFile` 만 읽는다(크기 제한 없음).
- capability 근거를 코드에서 확인했다: Claude `--append-system-prompt-file`(`adapters/agent/claudecode/claudecode.go`)·ACP `_meta.systemPrompt`(`adapters/chatdriver/claudeacp/driver.go`), Codex `-c model_instructions_file`(`pkg/agentruntime/command.go`)·app-server `developerInstructions`(`adapters/chatdriver/codexappserver/driver.go`), 권한 모드 매핑은 `pkg/agentruntime/command.go`(TUI)와 두 Chat 드라이버. Cursor 는 시스템 프롬프트 플래그가 없고(`adapters/agent/cursor/cursor.go`), fx 는 지시문을 첫 사용자 턴으로 보낸다(`adapters/agent/fx/fx.go`).
- 필수 테스트 5종을 먼저 썼다. 첫 실행은 `no non-test Go files` 빌드 실패였고, 선언만 둔 빈 스텁으로 다시 돌려 5종 모두 동작 FAIL(RED)을 봤다(예: `Builtins() returned 0 rules, want 3`, `CapabilityOf("claude-code", "prompt") = "", want "supported"`, `ReadAORules() = [], want one entry`).

## After — 수행 후 실측

- 코드 커밋 `57571c6ce` `feat(lado): built-in rules, agent capabilities and AO instructions`: 6 파일, +603
  - `builtins.go`(패키지 문서 주석은 여기만): `Builtins()` 는 플랜 §5 표 순서로 3개다. `plan.approval.required`(workflow, workflow_gate, 보호, global on), `agent.permission.ceiling`(tools, tool_policy, 보호, global on, config `{"mode":"auto"}`), `git.commit.message.korean`(git, prompt, 일반, global off, 플랜 지시문 그대로). 내용은 `Normalize()` 한 형태라 store 가 돌려줄 값과 같다. `Version{RuleID, Number: 1, SHA256: Content.SHA256(), Actor: "lado"}` 이고 버전 id·시각·`CreatedAt` 은 비워 둔다(S07). 호출마다 새 값을 만든다.
  - `capabilities.go`: `CapabilityClass`(`prompt`, `permission_mode`), `CapabilityStatus`(supported, degraded, unsupported, unverified), `Capability`, `CapabilityOf`. 표는 claude-code·codex 두 칸 supported, cursor prompt unsupported, fx prompt degraded이고 표 위 주석에 위 근거 파일을 적었다. 표에 없는 에이전트·칸(빈 harness, 모르는 class 포함)은 unverified 다.
  - `aorules.go`: `AORuleKind` 3종, `AORule{Kind, Path, SHA256, Chars, ReadError}`, `ReadAORules(project)`. kind 순서이고, 텍스트는 `TrimSpace` 뒤 sha256(hex)·rune 수다. 파일 경로는 `projectRelativeFile` 사본(출처 주석, 업스트림 파일 미수정)으로 검사하고 `io.LimitReader(4 MiB + 1)` 로 읽는다.
  - 테스트: 필수 5종. 거부 테스트는 거부되지 않으면 읽혔을 파일(프로젝트 밖 `x.md`, 절대 경로가 프로젝트 아래로 join 된 위치의 파일)을 실제로 둔다. 4 MiB 경계는 sparse 파일(`os.Truncate`)로 정확히 4 MiB(읽음)와 1바이트 초과(ReadError)를 둘 다 본다.
- 플랜에 없어 이 단계에서 정한 것:
  - `Path` 는 설정값을 `TrimSpace` 한 그대로다(Clean·구분자 변환 없음). 읽기 실패에도 채운다(UI 가 경로를 보여 준다).
  - 설정된 파일의 내용이 공백뿐이면 항목은 남는다(`Chars` 0, 빈 문자열의 sha256). 항목을 만들지 않는 "빈 설정" 은 설정값 자체가 비었거나 공백뿐일 때다.
  - `ReadError` 문구: 경로 거부는 AO 와 같은 문장(`path must be repo-relative and must not escape the project root`, `project path is required`), 없는 파일은 `file not found`, 초과는 `larger than 4 MiB`, 그 밖은 `fs.PathError` 에서 경로를 뗀 OS 사유(폴더는 Linux 에서 `is a directory`). 프로젝트 절대 경로를 넣지 않아 S06 problem details 와 스냅숏 sha 가 프로젝트 위치에 따라 바뀌지 않는다(테스트가 확인).
  - 4 MiB 제한은 LADO 만의 것이다. AO `buildProjectRules` 는 제한 없이 읽으므로, 4 MiB 를 넘는 파일은 AO 가 보내더라도 LADO 에서는 ReadError(→ S06 blocking)다.
  - `ReadAORules` 는 항목이 없어도 nil 이 아닌 빈 슬라이스를 돌려준다.
  - ceiling config 는 리터럴 ``json.RawMessage(`{"mode":"auto"}`)`` 다. `LadoCeilingConfig` 를 Marshal 하는 도우미는 인자가 늘 같아(unparam) 뺐다.
  - 정규 파일 검사(FIFO 가 open 에서 멈추는 것 대비)는 넣었다가 뺐다. 폴더는 읽기 오류로 이미 잡혀 테스트로 드러나지 않았고, AO 의 읽기와 같게 두는 쪽을 골랐다.
- 변이 확인(각각 고친 뒤 원복, FAIL 줄까지 확인): M1 파일 텍스트를 trim 하지 않음 → `TestReadAORulesHashesTrimmedText` FAIL. M2 글자 수를 바이트로 → 같은 테스트 FAIL. M3 `..` 검사 제거 → `TestReadAORulesRefusesPathsOutsideTheProject`(parent, parent after a folder) FAIL. M4 절대 경로 검사 제거 → 처음엔 **생존**(절대 경로가 프로젝트 아래로 join 돼 `file not found` 로 우연히 통과). join 된 위치에 파일을 두게 테스트를 고친 뒤 `absolute_path` FAIL. M5 크기 제한 없음 → `over_4_MiB` FAIL. M6 경계 `>=` → `exactly_4_MiB` FAIL. M7 없는 파일 오류에 경로 노출 → `missing_file` FAIL. M8 파일 읽기 실패 때 다른 항목 누락 → 3개 하위 테스트 FAIL. M9 kind 순서 바꿈 → 두 테스트 FAIL. M10 표에 없으면 supported → `TestCapabilityTable` FAIL. M11 cursor permission_mode supported → 같은 테스트 FAIL. M12 한국어 규칙 global on, M13 상한 bypass, M14 sha 불일치, M15 정규화 안 함 → 각각 `TestBuiltinsAreTheStepFiveCatalog` FAIL. M16 정규 파일 검사 제거 → 생존(위 결정대로 코드에서 뺐다). M17 공백뿐인 파일은 항목 없음 → `TestReadAORulesHashesTrimmedText` FAIL(이 단언은 구현 뒤 더했다).
- 추가 실행: `gofmt -l` 출력 없음. golangci-lint v2.13.2(저장소 `.golangci.yml`) `./internal/service/ladopolicy/...` → `0 issues`. `go build ./... && go vet ./...` ok.
- 게이트(HEAD `57571c6ce`): `GATES=6 MET=6 UNMET=0`. 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳·재작업 없음. 빈 스텁으로 동작 RED 를 먼저 봐서, 컴파일 RED 만 본 S04 보다 테스트가 무엇을 잡는지 분명했다.
- 변이 검사가 테스트 구멍 하나(M4)와 테스트로 드러나지 않는 코드 하나(M16)를 찾았다. "거부" 테스트는 거부되지 않았을 때 읽힐 파일을 실제로 둬야 다른 이유(없는 파일)로 통과하는 일이 없다.
- golangci-lint 를 `go run …@v2.13.2` 로 돌리면 오프라인(`GOPROXY=off`)에서 deprecation 조회로 실패한다. 모듈 캐시 디렉터리에서 `go build -o <scratchpad>/golangci-lint ./cmd/golangci-lint` 로 빌드하면 오프라인으로 돈다.
- 다음 단계 메모:
  - S06: 읽히지 않은 worker 파일 항목은 `ReadError != ""` 이고 `SHA256` 이 빈 값이다. `LADO_AO_RULES_FILE_UNREADABLE {path, reason}` 의 reason 은 `ReadError` 를 그대로 쓰면 된다(절대 경로 없음). 스냅숏에는 worker kind 만 싣는다(`orchestrator_instructions` 제외).
  - S06: `CapabilityOf` 는 빈 harness 에도 unverified 를 돌려준다. 에이전트가 없을 때는 `LADO_AGENT_NOT_CHOSEN` 만 내도록 capability 검사 전에 갈라야 unverified 가 겹쳐 나오지 않는다.
  - S07: `Builtins()` 결과에 버전 id(`lado-rule-version-<uuid>`)·시각을 붙여 seed 로 넘기면 된다. 내용은 이미 정규화돼 있고, 결과를 고쳐도 다음 호출에 새지 않는다.
