# 2026-10-22 LADO 규칙 코어 — S06 ladopolicy: 해석·프롬프트·스냅숏

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§5 Resolution·Snapshot) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙 코어의 심장을 만든다: 바인딩을 해석해 유효 규칙을 정하고(HARNESS §4), 에이전트가 그 규칙을 지킬 수 있는지 판정하고(§7), 워커가 받을 규칙 문구와 정규 스냅숏·해시를 만든다(§8). **순수 함수**라 DB 없이 테이블 테스트로 고정한다 — TDD §14 게이트 3 "Resolution = `service/ladopolicy` 테이블 테스트(HARNESS §15)" 가 이 단계다.

## 범위

- 만든다: `backend/internal/service/ladopolicy/resolve.go`, `prompt.go`, `snapshot.go`, 테스트 `resolve_test.go`, `snapshot_test.go`(프롬프트 골든 포함).
- 쓴다: S05 의 `Builtins`·`CapabilityOf`·`AORule`, S02 의 도메인 타입.
- 하지 않는다: store·서비스(S07), HTTP·UI.

## 절차

1. 플랜 §5 의 `AgentChoice`·`Input`·`Source`·`Entry`·`Resolution`·`Resolve` 를 그대로 만든다. 해석 순서 1~9 를 그대로 구현한다. 보호 규칙은 어떤 바인딩이 와도 ON(데이터가 잘못 들어 있어도 — 방어). 다른 프로젝트·다른 태스크의 바인딩은 무시.
2. `prompt.go`: 플랜 §5 4번의 정확한 문구. 규칙 사이 빈 줄 하나, 끝 줄바꿈 없음, 적용 규칙이 없으면 빈 문자열.
3. `snapshot.go`: `const Compiler = "lado-policy/1"`, `SnapshotContent` 와 하위 타입(필드 순서 = 형식), `encodeCanonical`(Encoder + `SetEscapeHTML(false)` + 줄바꿈 제거, 모든 슬라이스 non-nil). ON 규칙만 싣는다. `Resolution.JSON`·`SHA256`·`SnapshotRules()`·`Blocking()`.
4. 테스트 표의 각 행 이름에 근거를 적는다(아래 게이트가 이름을 찾는다): `PRD gate 3`, `HARNESS 15.1`, `HARNESS 15.2`, `HARNESS 15.3`, `HARNESS 15.7`, `HARNESS 15.8`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): resolve rules into a canonical snapshot` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S06-policy-resolve.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (`backend/internal/service/ladopolicy/`)

- `TestResolveBindings` — 표(서브테스트): `PRD gate 3` global on + project force_off + task force_on → ON(출처 task) / `HARNESS 15.1` global on + project off + task on → ON / `HARNESS 15.2` project inherit 가 global on·off 를 따른다(두 행) / §4 예: global off + project force_on → ON, global on + project force_off + task inherit → OFF / 다른 프로젝트의 force_off 는 무시 / `HARNESS 15.3` 보호 규칙은 force_off 행이 있어도 ON(출처 global) / 보관 규칙은 결과에 없다
- `TestResolveCapabilities` — `HARNESS 15.7`: cursor + ON 프롬프트 규칙 → blocking `LADO_AGENT_CAPABILITY_UNSUPPORTED`(prompt) + blocking unverified(permission_mode) / fx → warning degraded / gemini → blocking unverified / claude-code·codex → capability 문제 0 / 에이전트 없음 → blocking `LADO_AGENT_NOT_CHOSEN` / 태스크에 에이전트가 없으면 프로젝트 `Worker.Harness`(출처 project) / 프로젝트 범위는 에이전트 검사 없음
- `TestResolveConflicts` — `HARNESS 15.8`: A·B 둘 다 ON 이고 A 만 B 를 선언 → blocking 1건 rules [A,B] / 하나가 OFF → 0건
- `TestResolvePermissionMode` — 상한 auto + 플랜 bypass → auto + warning clamped / 플랜 빈 값·default → auto / 플랜 accept-edits → accept-edits / 상한 규칙 없음 → accept-edits + blocking `LADO_CEILING_MISSING`
- `TestResolveCodexAsksApproval` — codex + auto → info `LADO_CODEX_ASKS_APPROVAL`, codex + bypass → 없음
- `TestResolveAORulesFileUnreadable` — ReadError 있는 파일 지시문 → blocking `LADO_AO_RULES_FILE_UNREADABLE`(details path)
- `TestSnapshotIsDeterministic` — 같은 입력 두 번 → 같은 바이트·sha, 입력 규칙 순서를 섞어도 같다, `[]` 가 `null` 이 되지 않는다
- `TestSnapshotHashFollowsWhatApplies` — OFF 규칙 내용 변경 → sha 같음 / ON 규칙 지시문 변경 → 다름 / 에이전트 변경 → 다름 / AO 지시문 해시 변경 → 다름 / AOBuild 변경 → 다름
- `TestPromptText` — 규칙 2개 골든 문자열(플랜 문구 그대로), 규칙 0개 → 빈 문자열

## 수용 게이트

- [ ] S06-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S06-G1: 필수 테스트 9종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladopolicy/ TestResolveBindings TestResolveCapabilities TestResolveConflicts TestResolvePermissionMode TestResolveCodexAsksApproval TestResolveAORulesFileUnreadable TestSnapshotIsDeterministic TestSnapshotHashFollowsWhatApplies TestPromptText
  EXPECT: GO_TESTS_OK 9/9
- [ ] S06-G2: 수용 테스트 근거 행이 표에 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/resolve_test.go 'PRD gate 3' 'HARNESS 15.1' 'HARNESS 15.2' 'HARNESS 15.3' 'HARNESS 15.7' 'HARNESS 15.8'
  EXPECT: HAS_OK
- [ ] S06-G3: 형식 이름과 프롬프트 문구가 플랜 그대로다
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/snapshot.go '"lado-policy/1"' 'SetEscapeHTML(false)' && bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/prompt.go '## LADO rules' 'The developer approved these rules for this run. Follow them together with the project rules above.'
  EXPECT: HAS_OK
- [ ] S06-G4: 패키지 전체 통과(-race)
  CHECK: bash .claude/gate/lado-rules-core.sh go-race ./internal/service/ladopolicy/
  EXPECT: GO_RACE_OK
- [ ] S06-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S06-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `068d80c999acd101fd951c47329481cb771fa728`(`docs(lado): record S05`), 트리 깨끗.
- `service/ladopolicy` 에는 S05 의 `Builtins`·`CapabilityOf`·`ReadAORules` 만 있다(해석·프롬프트·스냅숏 없음). 도메인(S02)에 `LadoRule`(Global + Overrides), `LadoCeilingConfig`, `LadoCeilingModes`·`LadoPermissionRank`, `LadoPolicyProblem`, `LadoPolicySnapshotRule` 이 있다.
- 에이전트 출처를 코드에서 확인했다: `LadoTask.Agent`·`Model` 은 trim 된 자유 문자열(`ladotask/service.go` `UpdatePreferences`), 프로젝트 워커 에이전트는 `Config.Worker.Harness`, AO spawn 의 모델은 프로젝트 기본 위에 역할 override(`session_manager/manager.go` `effectiveAgentConfig`, `domain.ProjectConfig.DefaultWorkerReviewer` 와 같은 순서) 다.
- 필수 테스트 9종과 형식 골든 1종을 먼저 썼다. 구현을 쓴 뒤 `Resolve`·`ChooseAgent` 에 임시 조기 반환(`return Resolution{}`)을 넣어 컴파일은 되고 10종 모두 동작 FAIL(RED)인 것을 봤다(예: `PRD_gate_3_and_HARNESS_15.1…` `on false from {Scope: …}`, 모든 `HARNESS 15.7/15.8` 행). 조기 반환을 걷어낸 첫 실행에서 전부 PASS.

## After — 수행 후 실측

- 코드 커밋 `9e8fd8347` `feat(lado): resolve rules into a canonical snapshot`: 6 파일, +1467 −2
  - `resolve.go`: 플랜 §5 의 `AgentChoice`(`AgentFromTask`·`AgentFromProject`)·`Input`·`Source`·`Entry`·`Resolution`·`Resolve`·`Blocking()`·`SnapshotRules()` 와 problem code 상수 9개(`Problem*`), 그리고 `ChooseAgent(task, project)`. 해석 순서 1~9 를 그대로 따른다: 보관 안 된 규칙을 created_at → id 로 정렬, 보호 규칙은 바인딩과 상관없이 ON(global/on), 아니면 task → project → global 중 가까운 바인딩, 해석 대상이 아닌 프로젝트·태스크의 바인딩은 무시. 상한은 `agent.permission.ceiling` config 의 mode, 없거나 상한 모드가 아니면 `accept-edits` + blocking `LADO_CEILING_MISSING`. 태스크 범위는 `min(plan, ceiling)` 과 낮췄을 때 warning `LADO_PLAN_CEILING_CLAMPED{plan, ceiling}`, 에이전트 검사(`permission_mode` 항상, 프롬프트가 있으면 `prompt`), Codex + bypass 미만이면 info `LADO_CODEX_ASKS_APPROVAL{mode}`. 충돌은 ON 쌍마다 한 번 `LADO_RULES_CONFLICT`, 못 읽은 `agentRulesFile` 은 `LADO_AO_RULES_FILE_UNREADABLE{path, reason}`. problem 은 severity → code → rules 로 정렬.
  - `prompt.go`: 플랜 문구(`## LADO rules` + lead)와 규칙별 `### <name>` 절, 빈 줄 하나로 잇고 끝 줄바꿈 없음, 적용 규칙이 없으면 빈 문자열.
  - `snapshot.go`: `const Compiler = "lado-policy/1"`, `SnapshotContent`(플랜 필드 순서 그대로)와 `SnapshotAgent`·`SnapshotRule`·`SnapshotSource`·`SnapshotAORule`·`SnapshotCapability`, `encodeCanonical`(Encoder + `SetEscapeHTML(false)` + 끝 줄바꿈 제거). ON 규칙과 worker kind 의 AO 지시문만 싣고 모든 리스트는 `[]`.
  - `builtins.go`: 패키지 문서 주석에 해석을 더했다(코드 변경 없음).
  - 테스트: 필수 9종 + `TestSnapshotFormatIsLadoPolicy1`(정규 바이트 전체를 손으로 쓴 JSON 을 `json.Compact` 해 비교하는 형식 골든, `SnapshotRules()` 포함). 수용 근거 행 이름: `PRD gate 3 and HARNESS 15.1`, `HARNESS 15.2`(2행), `HARNESS 4`(2행), `HARNESS 15.3`(2행), `HARNESS 15.7`(2행), `HARNESS 15.8`(3행).
- 플랜에 없어 이 단계에서 정한 것:
  - `ChooseAgent` 를 순수 함수로 이 패키지에 뒀다(필수 테스트의 "태스크에 에이전트가 없으면 프로젝트 `Worker.Harness`" 가 여기서 검사된다; S07 이 그대로 부른다). 하네스와 모델은 한 출처에서만 온다: 태스크가 에이전트를 고르면 태스크 모델, 아니면 프로젝트 워커 하네스와 `Worker.AgentConfig.Model`(없으면 `AgentConfig.Model`), 둘 다 없으면 빈 값.
  - problem 의 `rules` 는 원인이 되는 규칙이다: 상한 없음·낮춤 → `[agent.permission.ceiling]`, capability `prompt` → 프롬프트에 들어간 ON 규칙, `permission_mode` → ON tool_policy 규칙(내장 상한), 충돌 → `[A, B]`. `LADO_AGENT_NOT_CHOSEN`·`LADO_CODEX_ASKS_APPROVAL`·`LADO_AO_RULES_FILE_UNREADABLE` 은 rules 없음. rules 는 정렬하고 없으면 nil(`omitempty`). capability problem details 는 `{agent, capability}`.
  - 상한 규칙은 ON 여부와 상관없이 읽는다(보호 규칙이라 늘 ON 이지만, 데이터가 잘못돼도 상한은 걸린다). 플랜 상한이 상한 모드가 아니면(빈 값·`default`·알 수 없는 값) 규칙 상한으로 돈다 — 알 수 없는 값은 `ParseLadoPlan` 이 이미 거부한다.
  - `Entry.Project`·`Task` 는 해석 대상 범위의 force_on/force_off 만 보여 준다. 다른 상태(잘못된 데이터)는 inherit 로 친다. 보호 규칙도 저장된 바인딩을 그대로 보여 주되 적용하지 않는다. Global 이 `on` 이 아니면(빈 값 포함) OFF, 출처 state `off`.
  - 에이전트가 없으면 capability 검사를 하지 않고 `LADO_AGENT_NOT_CHOSEN` 하나만 낸다(S05 메모대로 unverified 중복 없음). `Capabilities` 순서는 `prompt`(필요할 때) → `permission_mode`. 프로젝트 범위는 검사 없음, `Mode = Ceiling`, 스냅숏 agent 는 받은 값 그대로.
  - 스냅숏: AO 지시문은 kind → path 로 정렬(입력 순서와 무관), 에이전트 출처(`task`/`project`)는 싣지 않는다(하네스·모델이 같으면 sha 같음). 규칙 name·enforcement 는 `Normalize()` 한 내용, sha256 은 버전에 저장된 값. `workflowGates` 는 workflow_gate 를 가진 ON 규칙 id(엔트리 순서). 하위 타입 필드는 문자열 대신 도메인 타입이다(JSON 동일).
  - 프롬프트 제목의 규칙 이름은 공백을 한 칸으로 접어 한 줄로 만든다(`strings.Fields`). 지시문은 trim.
  - `Resolution` 의 `Entries`·`Capabilities`·`Problems` 는 nil 이 아니다(API DTO 가 `null` 을 내지 않게).
- 변이 확인(44개, 각각 적용 → 테스트 → 원복): 처음 실행에서 M17(problem rules 미정렬)·M34(스냅숏의 Problems nil 보정 제거)가 생존, M09·M37 은 변이 자체가 빌드 실패라 다시 썼다. M17: 충돌 픽스처가 생성 순서 = id 순서라 정렬이 필요 없었다 → 생성 순서를 id 역순으로 바꿨다. 그러자 M13(한쪽 방향만 보는 충돌 검사)이 생존해 "a newer rule naming an older one" 행을 더했다. M37(AO 지시문 미정렬)은 픽스처에 worker kind 가 하나뿐이라 생존 → `fullInput` 에 `worker_instructions_file` 을 더하고 골든을 고쳤다. M34 는 `Resolve` 가 이미 `Problems` 를 비지 않게 만들어 보정이 군더더기였다 → 보정을 빼고 `Resolve` 초기화를 변이해 kill 확인. `Capabilities` 비-nil 초기화(M44)는 단언을 더해 kill. 최종 44/44 kill(보호 규칙 강제, 다른 범위 바인딩, task/project 우선순위, 보관, 정렬, fallback·clamp·min, NOT_CHOSEN, prompt 요구 조건, Codex 경계, 충돌 방향·OFF, problem 정렬, Blocking, 모델 fallback, 잘못된 상태, 범위별 검사, AO kind, degraded 심각도, 프롬프트 문구·끝 줄바꿈·제목·빈 결과, HTML 이스케이프, 끝 줄바꿈, null, OFF 규칙·orchestrator 제외, gates, model·AOBuild, SnapshotRules).
- 추가 실행: `gofmt -l` 출력 없음. golangci-lint v2.13.2(저장소 `.golangci.yml`, 이전 단계가 빌드한 바이너리, `GOPROXY=off`) `./internal/service/ladopolicy/...` → `0 issues`. `go vet` ok.
- 게이트(HEAD `9e8fd8347`): `GATES=7 MET=7 UNMET=0`. 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 구현은 첫 실행부터 통과했고, 테스트 구멍(M17·M13·M37)은 변이 검사로만 드러났다. "자연스러운" 픽스처(생성 순서 = id 순서, AO kind 하나)는 정렬·방향 버그를 가린다 — 순서를 다루는 코드는 픽스처 순서를 일부러 거꾸로 둔다.
- 스냅숏 형식은 필수 테스트(결정성·해시)만으로는 필드 이름·순서가 바뀌어도 통과한다. 형식 골든을 하나 더해 `lado-policy/1` 바이트를 고정했다.
- golangci-lint 바이너리를 실행할 때 `GOFLAGS=-mod=mod` 를 주면 go.work 작업 공간 모드에서 "`-mod may only be set to readonly or vendor`" 로 실패한다. 실행은 `GOPROXY=off` 만 주면 된다(`-mod=mod` 는 빌드할 때만).
- 다음 단계 메모(S07):
  - 태스크 에이전트는 `ChooseAgent(task, project)`. 프로젝트 범위 `Input.Agent` 는 스냅숏 agent 에 그대로 실리므로 한 가지로 고정해야 프로젝트 범위 sha(Impact 비교)가 흔들리지 않는다.
  - `Input.PlanCeiling` = 리비전 블롭의 `domain.ParseLadoPlan(...).PermissionCeiling`, `AOBuild` = `config.AppVersion`, `AORules` = `ReadAORules(project)`.
  - 저장: `Resolution.JSON` → `Content`, `SHA256`, `Blocking()`, `SnapshotRules()` → `lado_policy_snapshot_rules`. 저장된 `Content` 는 `json.Unmarshal` 로 `SnapshotContent` 에 그대로 돌아온다(테스트로 확인) — `ApprovedView` 의 added/removed/changed 는 `Content.Rules` 의 `ruleId`·`versionId` 로 비교하면 된다.
  - problem code 는 `ladopolicy.Problem*` 상수로 쓸 수 있다.
