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

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
