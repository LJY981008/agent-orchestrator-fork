# 2026-10-22 LADO 규칙 코어 — S15 리뷰 반영: 프런트·문서

> 체인 `docs/chains/lado-rules-core-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§9 를 이 엔트리가 고친다) · 브랜치 `feat/lado-rules-core` · 소유자 승인 2026-10-22("체인 2단계")

## 의도/목적 — 왜 이 작업을 하나

프런트 리뷰(블로커 0, 메이저 2, 마이너 10)의 지적과 S14 의 결정을 반영한다. 두 메이저는 실제 결함이다: 차단된 태스크를 "이 규칙으로 승인됨"(초록)으로 보이고 쓸모없는 재승인을 권하며, 삭제된 규칙이 충돌 목록에 남으면 편집기에서 지울 수 없다.

## 범위 (지적 번호 = 리뷰 번호)

- **F1 (메이저)** — `components/lado/LadoTaskRules.tsx` 의 승인 비교: `approved.matches && approved.blocking` 이면 attention 톤의 새 문장(키 `lado.task.rules.approvedBlocking`, en "Approved with these rules, but they stop the run until the problems below are fixed")으로 보인다. `LadoTaskDialog.tsx` 의 재승인(`reapprove`)은 **`review_required` 일 때만** — blocked 는 같은 스냅숏을 다시 서명할 뿐이고, 문제를 고치면(에이전트·규칙 변경) 해시가 바뀌어 review_required 가 된다. blocked 상태 안내 문장이 있으면 "문제를 고치면 다시 승인할 수 있다" 로 맞춘다. **플랜 §9 의 "Approve plan is enabled again for review_required and blocked" 를 이 단계가 대체한다.**
- **F2 (메이저)** — `components/lado/LadoRuleEditorDialog.tsx` 충돌 선택: 현재 규칙 목록에 없는 id(삭제된 규칙)는 체크된 항목 "삭제된 규칙"(키 `lado.rules.editor.deletedRule`, `{{id}}` 를 mono 로)으로 보여 해제할 수 있게 하고, 요약도 그 이름을 쓴다. S14 의 B1 로 서버도 기존 id 를 받아들인다.
- **F3** — `lado_task` 이벤트가 그 태스크의 유효 규칙 쿼리(`["lado","effective",…]`)도 무효화한다(열린 규칙 탭이 낡지 않게).
- **F4** — 편집기에서 `LADO_RULE_CHANGED` 뒤 "다시 읽기" 가 사용자의 편집을 버리지 않는다: 새 활성 버전으로 `baseVersion` 을 올리고, 사용자가 바꾼 필드는 유지, 바뀌지 않은 필드는 새 버전 값으로. 상단 문장 "규칙이 그사이 바뀌었다 — 편집 내용은 남겼다, 확인 후 다시 저장". 기존 테스트(`LadoRuleEditorDialog.test.tsx` 의 편집 유실을 단언하는 것)를 고친다.
- **F5** — `lib/lado-rules.ts` `ladoProblemText` 가 capability 문제에 에이전트 표시 이름을 넣는다(렌더러에 있는 에이전트 표시 이름 함수를 찾아 쓴다; 없으면 하네스 id).
- **F6** — `components/lado/LadoLanes.tsx`: `review_required`·`blocked` 태스크에 계획 요청이 대기 중(`plan.requestPending`)이면 `planned` 처럼 muted "Plan requested".
- **F7** — `components/lado/LadoRuleRows.tsx`: 보호 규칙 행에도 행 메뉴(History 만)를 둬서 권한 상한의 변경 기록(`LadoRuleHistoryDialog`)을 열 수 있게.
- **F8** — `LadoTaskRules.tsx` 의 에이전트 줄·문제·승인 비교를 아래 카드 행과 같은 16px inset(`--settings-row-inset`)에 맞춘다.
- **F9 (한국어)** — `ko.json`: `lado.rules.actions` "{{name}} 작업" → "{{name}} 메뉴"(작업 = 이 포크의 task); `lado.view.permission` "최대 권한" → "권한 상한"; `lado.task.rules.runsWith` "{{agent}}에서 실행 · 최대 {{mode}}" → "{{agent}}(으)로 실행 · 권한 상한 {{mode}}"; `lado.presence.change` "바꾸기" → "변경"(바로 앞 영향 다이얼로그 `lado.rules.impact.change` 와 같은 낱말). `lado.presence.*` 를 바꿨으니 frontend/ 에서 `node scripts/sync-tray-catalog.mjs` 로 `tray-catalog.generated.ts` 재생성. 새 키(F1·F2·F4 문장)는 8개 로캘 전부 실제 번역.
- **F10 (DESIGN.md)** — 결정 로그 끝에 한 줄: `| 2026-10-22 | LADO fork: project Rules page, task view Plan | Rules tabs and the two new card states …` — 프로젝트 설정 규칙 페이지 구성, 태스크 뷰 오른쪽 창의 Plan | Rules 탭(2026-10-20 행의 리비전 선택은 Plan 탭 안), 카드의 "Rules need review"·"Blocked" 상태 줄(needs-you 점), 차단된 태스크는 다시 승인하지 않는다는 것과 그 이유.
- **F11** — `lib/event-transport.ts` 의 LADO 갱신 로직(약 35줄)을 `lib/lado-events.ts`(새 파일) 함수로 옮기고 event-transport 에는 호출 몇 줄만 남긴다(업스트림 파일은 훅 줄만 — TDD §1.2). F3 도 그 함수에서.
- **문서** — `docs/LJY-PRD/TDD.md` §5.2 "As built in step 5" 문단에 다음 문장을 **그대로** 넣는다: `Only a Review required task is approved again; a Blocked task is fixed first (agent or rules), which changes the rules hash and makes it Review required.` 그리고 `A rule deleted while an approval is in flight also answers LADO_RULES_CHANGED.` §9 에 `The AO instructions file is read the way AO reads it, symlinks included, so the snapshot hash matches what AO puts into the worker prompt.`
- 하지 않는다: 백엔드 코드(S14 끝), nit 12(`compactButtonClass`).

## 절차

1. **`DESIGN.md` 를 먼저 읽는다.** 공유 프리미티브만, 버튼 글자 스타일은 안쪽 `<span>`.
2. 지적마다 실패하는 테스트를 먼저 쓰고 고친다(아래 제목 그대로).
3. 렌더러 전체 vitest 로 회귀를 본다.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `fix(lado): review fixes for the rules UI` · Co-Authored-By 유지). 문서는 같은 커밋 또는 `docs(lado): …` 커밋.
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S15-frontend-fixes.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (제목 문자열 그대로 — 게이트가 찾는다)

- `LadoTaskRules.test.tsx`: `shows that the approved rules block the run`
- `LadoTaskDialog.test.tsx`: `does not offer approval again for a blocked task`
- `LadoRuleEditorDialog.test.tsx`: `lets a deleted conflicting rule be removed`, `keeps your edits when the rule changed meanwhile`
- `lib/event-transport.test.ts`: `refreshes a task's effective rules on lado_task events`
- `lib/lado-rules.test.ts`: `names the agent in capability problems`
- `LadoLanes.test.tsx`: `shows plan requested for a task whose rules need review`
- `settings/RulesSection.test.tsx`: `opens the history of a protected rule`

## 수용 게이트

- [ ] S15-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S15-G1: 필수 테스트 제목이 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoTaskRules.test.tsx 'shows that the approved rules block the run' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoTaskDialog.test.tsx 'does not offer approval again for a blocked task' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoRuleEditorDialog.test.tsx 'lets a deleted conflicting rule be removed' 'keeps your edits when the rule changed meanwhile' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/event-transport.test.ts "refreshes a task's effective rules on lado_task events" && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/lado-rules.test.ts 'names the agent in capability problems' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoLanes.test.tsx 'shows plan requested for a task whose rules need review' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/settings/RulesSection.test.tsx 'opens the history of a protected rule'
  EXPECT: HAS_OK
- [ ] S15-G2: 바뀐 테스트·i18n·트레이 카탈로그 통과
  CHECK: bash .claude/gate/lado-rules-core.sh vitest src/renderer/components/lado/LadoTaskRules.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoRuleEditorDialog.test.tsx src/renderer/lib/event-transport.test.ts src/renderer/lib/lado-rules.test.ts src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/settings/RulesSection.test.tsx src/renderer/i18n/renderer-coverage.test.ts src/renderer/i18n/instance.test.ts src/main/tray-catalog.test.ts src/main/lado-presence.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G3: 한국어 문구가 고쳐졌다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/i18n/ko.json '"lado.rules.actions": "{{name}} 메뉴"' '"lado.view.permission": "권한 상한"' '"lado.presence.change": "변경"' '(으)로 실행' && ! grep -q '"{{agent}}에서 실행' frontend/src/renderer/i18n/ko.json && echo S15_KO_OK
  EXPECT: S15_KO_OK
- [ ] S15-G4: event-transport 는 훅 줄만(5단계 시작점 855653a5c 대비 변경 12줄 이하)
  CHECK: n=$(git diff --numstat 855653a5c46bca4c071abfdf4683f6c51ece14f7 HEAD -- frontend/src/renderer/lib/event-transport.ts | awk '{print $1+$2}'); [ "${n:-0}" -le 12 ] && test -f frontend/src/renderer/lib/lado-events.ts && echo "S15_EVENTS_HOOK_OK $n"
  EXPECT: S15_EVENTS_HOOK_OK
- [ ] S15-G5: DESIGN.md 결정 로그·TDD 문장
  CHECK: bash .claude/gate/lado-rules-core.sh has DESIGN.md '| 2026-10-22 | LADO fork: project Rules page' && bash .claude/gate/lado-rules-core.sh has docs/LJY-PRD/TDD.md 'Only a Review required task is approved again' 'A rule deleted while an approval is in flight also answers LADO_RULES_CHANGED' 'symlinks included'
  EXPECT: HAS_OK
- [ ] S15-G6: 렌더러 전체 vitest 통과(회귀 확인)
  CHECK: cd frontend && npx vitest run src/renderer 2>&1 | tail -6 | grep -E 'Test Files .* passed' | grep -v failed && echo S15_RENDERER_VITEST_OK
  EXPECT: S15_RENDERER_VITEST_OK
- [ ] S15-G7: 타입체크
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S15-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
