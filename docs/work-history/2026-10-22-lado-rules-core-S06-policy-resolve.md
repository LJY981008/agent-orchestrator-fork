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

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
