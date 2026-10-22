# 2026-10-22 LADO 규칙 코어 — S12 프로젝트 규칙·태스크 뷰 규칙 탭·재승인·레인

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§9 Project settings·Task view·Board) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

규칙을 프로젝트와 태스크 범위에서 덮어쓰고(INHERIT/FORCE_ON/FORCE_OFF — HAR-003), 태스크마다 "이번 실행에 무엇이 적용되는가"(유효 미리보기 — HAR-005)와 "승인 때와 지금이 같은가"를 보인다. 규칙이 승인 뒤 바뀌었거나 승인 스냅숏이 실행을 막으면 보드와 태스크 뷰가 그렇게 말하고, 같은 계획을 다시 승인할 수 있게 한다. 승인은 이제 화면이 본 규칙 스냅숏 sha256 을 함께 보낸다.

## 범위

- 만든다: `frontend/src/renderer/components/lado/LadoProjectRulesSection.tsx`, `LadoTaskRules.tsx`, 테스트 `LadoProjectRulesSection.test.tsx`, `LadoTaskRules.test.tsx`.
- 고친다: `components/SettingsDialog.tsx`(로컬·클라우드 아님·원격 아님 + LADO 모드인 프로젝트에만 project 섹션 `{ id: "rules", … }` 추가와 렌더 — 기존 훅 줄 꼴로 최소 변경), `stores/ui-store.ts`(`ProjectSettingsSection` 에 `"rules"`), `components/lado/LadoTaskDialog.tsx`(+테스트), `components/lado/LadoLanes.tsx`(+테스트), 8개 로캘 파일.
- 쓴다: S10 데이터 계층, S11 의 `LadoRuleRows`·`LadoRuleEditorDialog`·`LadoRuleImpactDialog`.
- 하지 않는다: 문서(S13).

## 절차

1. **먼저 `DESIGN.md` 를 읽는다**(설정 모달 구성, 공유 목록, 보드·카드 규칙, 2026-10-20 태스크 뷰 결정 행). 공유 프리미티브(`ui/select`, `ui/tabs`, `ui/dialog`)만 쓴다.
2. 프로젝트 설정 → 규칙: 카드 "Rules for this project" — 행마다 이름, 상세 한 줄(유효 상태 · 출처 "On · from Global"/"Off · set here"), 끝에 공유 Select Inherit / On / Off(보호 규칙은 muted "Always on"). 카드 "From AO project config" — 읽기 전용 행(작업 지시문 N자, 지시문 파일 경로 mono + N자 또는 읽기 오류, 오케스트레이터 지시문 N자; 끌 수 없다는 사실은 결정에 필요한 한 줄로만). "New rule for this project" 는 S11 편집 다이얼로그를 project 범위로 연다. 변경은 `useLadoRuleChange`.
3. 태스크 뷰: 오른쪽 창 머리에 탭 Plan | Rules(`ui/tabs`). Rules 탭 = `LadoTaskRules`: 에이전트·모드 한 줄("Runs with Codex · at most auto"), 문제 목록(blocking 은 needs-you 점, warning 은 caution, info 는 muted — 색만으로 말하지 않고 문장 포함), 승인 비교(일치 "Approved with these rules" / 불일치 "Rules changed since approval" + 추가·제거·변경 규칙 이름 / 스냅숏 없음 "Approved before rules existed"), 태스크 override Select, AO 지시문(읽기 전용), 접히는 "Rule text for the worker"(prompt 를 mono 가 아닌 본문 글꼴의 조용한 블록으로).
4. 승인: 승인 직전에 `fetchLadoEffective(taskId)` 를 새로 읽어 그 `snapshotSha256` 을 `approveLadoPlan(revisionId, sha256, policySha256)` 에 넘긴다(데스크톱은 main 이 스스로 다시 읽어 서명한다 — 렌더러 값은 브라우저 경로용). Approve plan 은 상태가 `review_required`·`blocked` 일 때도 켜진다(최신·유효 리비전일 때). `LADO_RULES_CHANGED` 는 "규칙이 방금 바뀌었다, 다시 확인" 상태 문구 + 유효 규칙 재조회.
5. 보드: `LadoLanes` 의 상태 줄 — `review_required` → "Rules need review"(attention), `blocked` → "Blocked"(attention), 둘 다 Plan 레인(이미 approved 만 Queued). 카드에 상태 칩을 더 얹지 않는다(점 + 한 줄).
6. 문자열은 8개 로캘 전부. `<button>` 글자 스타일은 안쪽 `<span>`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): project and task rules, re-approval after rule changes` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S12-project-task-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (제목 문자열 그대로 — 게이트가 찾는다)

- `LadoProjectRulesSection.test.tsx`: `overrides a rule for this project`, `shows protected rules as always on`, `lists AO project instructions read-only`, `creates a rule for this project`, `adds a Rules page to local LADO projects only`
- `LadoTaskRules.test.tsx`: `shows the problems that block the run`, `overrides a rule for this task`, `shows what changed since approval`
- `LadoTaskDialog.test.tsx`: `approves with the rule snapshot hash`, `offers approval again when the rules need review`
- `LadoLanes.test.tsx`: `shows rules that need review and blocked tasks in the Plan lane`

## 수용 게이트

- [ ] S12-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S12-G1: 필수 테스트 제목이 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoProjectRulesSection.test.tsx 'overrides a rule for this project' 'shows protected rules as always on' 'lists AO project instructions read-only' 'creates a rule for this project' 'adds a Rules page to local LADO projects only' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoTaskRules.test.tsx 'shows the problems that block the run' 'overrides a rule for this task' 'shows what changed since approval' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoTaskDialog.test.tsx 'approves with the rule snapshot hash' 'offers approval again when the rules need review' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoLanes.test.tsx 'shows rules that need review and blocked tasks in the Plan lane'
  EXPECT: HAS_OK
- [ ] S12-G2: 새·바뀐 테스트와 설정 다이얼로그·i18n 테스트 통과
  CHECK: bash .claude/gate/lado-rules-core.sh vitest src/renderer/components/lado/LadoProjectRulesSection.test.tsx src/renderer/components/lado/LadoTaskRules.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/SettingsDialog.test.tsx src/renderer/i18n/renderer-coverage.test.ts src/renderer/i18n/instance.test.ts
  EXPECT: VITEST_OK
- [ ] S12-G3: 승인이 스냅숏 해시를 싣고 레인이 새 상태를 안다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoTaskDialog.tsx 'policySha256' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoLanes.tsx 'review_required' 'blocked' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/stores/ui-store.ts '"rules"'
  EXPECT: HAS_OK
- [ ] S12-G4: 렌더러 전체 vitest 통과(회귀 확인)
  CHECK: cd frontend && npx vitest run src/renderer 2>&1 | tail -6 | grep -E 'Test Files .* passed' | grep -v failed && echo S12_RENDERER_VITEST_OK
  EXPECT: S12_RENDERER_VITEST_OK
- [ ] S12-G5: 타입체크
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S12-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
