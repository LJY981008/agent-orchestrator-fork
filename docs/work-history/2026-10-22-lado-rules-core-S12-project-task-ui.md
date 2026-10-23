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

- 시작 HEAD: `2fd6adf6e4a3f9e317c2a88c7aadf2e17761e7b7`(`docs(lado): record S11`), 트리 깨끗.
- 프로젝트 설정에 규칙 페이지 없음(`ProjectSettingsSection` 에 `"rules"` 없음, `SettingsDialog` 에 LADO 줄 없음). `useLadoEffective`·`LadoEffective` 는 S10 에 있으나 쓰는 화면이 없다.
- 태스크 뷰 오른쪽 창은 Plan 하나(머리 `h3` + 리비전 Select). 승인은 `approveLadoPlan(revisionId, sha256, "")` — 렌더러가 규칙 해시를 보내지 않았고 테스트가 그 `""` 를 단언했다. 플랜 응답의 `approved` 는 유효한 승인이 있으면 규칙이 바뀌어도 참이라, Approve 대신 "Approved" 만 보여 `review_required`·`blocked` 태스크를 다시 승인할 길이 없었다.
- `LadoLanes` 의 `ladoTaskStatus` 는 두 새 상태를 몰라 default 갈래("Draft", muted)로 떨어졌다(레인 배치는 이미 approved 만 Queued).
- S11 `LadoRuleEditorDialog` 는 `scope: "global"` 로만 만든다.

## After — 수행 후 실측

- 코드 커밋 `3cb3734ca` `feat(lado): project and task rules, re-approval after rule changes`: 19 파일, +1733 −86.
  - `lado/LadoProjectRulesSection.tsx`(새): 카드 "Rules for this project"(머리 끝 ghost "New rule for this project") — 행 = 이름 + 상세 한 줄 `상태 · 출처`("On · from Global", "Off · set here", 상한은 상태 자리에 모드 "Auto · from Global") + 끝에 공유 `SettingsOptionMenu` Inherit/On/Off(aria "{{name}} for this project"), 보호 규칙은 muted "Always on"(컨트롤 없음). 카드 "From AO project config" — 결정에 필요한 한 줄("LADO includes these and cannot turn them off; change them in the project's AO config.")을 카드 위 줄에, 읽기 전용 행: Worker instructions N characters, Worker instructions file = 경로 mono · N characters 또는 error 색 "Could not read: {{reason}}", Orchestrator instructions N characters(숫자는 로캘 형식). 없으면 "The project's AO config adds no instructions." 바꾸는 동작은 `useLadoRuleChange` 하나로 한 번에 하나, 실패는 그 행 설명 아래 `role="alert"`, 영향이 있으면 `LadoRuleImpactDialog`. 행·AO 카드·상태 문구 도우미(`LadoEffectiveRuleRow`·`LadoAORulesSection`·`ladoEffectiveStateLabel`)를 export 해 태스크 뷰가 같이 쓴다.
  - `lado/LadoTaskRules.tsx`(새): Rules 탭 — "Runs with Codex · at most Auto"(에이전트 없음 "No agent chosen · at most …", 모드는 `permissionMode`), 문제 목록(`aria-label="Problems"`, 점 + 심각도 낱말 + 문장: blocking needs-you "Blocks the run", warning in-review "Warning", info idle "Note"), 승인 비교(일치 ready 점 "Approved with these rules" / 불일치 needs-you 점 "Rules changed since approval" + "Added:/Removed:/Changed:" 규칙 이름 — 지금 규칙에 없는 이름(보관된 규칙)이 있을 때만 `useLadoRules(true)` 로 읽는다 — 규칙은 같은데 스냅숏만 다르면 "No rule changed; the agent, its mode, the AO instructions or AO itself did." / 유효 승인은 있는데 `approved` 가 없으면 "Approved before rules existed" / 승인이 없으면 줄 없음), 카드 "Rules for this task"(같은 행, 출처에 "from the project" 추가, 취소·닫힌 태스크는 Select 잠금), AO 카드, 접히는 "Rule text for the worker"(`aria-expanded`, prompt 를 본문 글꼴 `whitespace-pre-wrap` 의 조용한 채움 블록으로, 없으면 "No rule adds text for the worker.").
  - `lado/LadoTaskDialog.tsx`: 오른쪽 창 머리에 `ui/tabs` Plan | Rules(트리거 글자는 안쪽 `<span>`), 리비전 Select 는 Plan 탭에서만. 승인 = `queryClient.fetchQuery({ queryKey: ladoEffectiveQueryKey(projectId, taskId), staleTime: 0 })` 로 Rules 탭과 같은 키를 새로 읽고(탭 캐시도 갱신) 그 `snapshotSha256` 을 `approve.mutate({ revisionId, sha256, policySha256 })` 로. 읽기 중에도 버튼은 "Approving…"·잠김, 읽기 실패는 푸터 alert. `canApprove` 는 `(!shown.approved || reapprove)` — `reapprove` = `review_required`·`blocked`; "Approved" 글자는 그 밖에서만. Plan 탭 위에 needs-you 상태 줄("The rules changed since this plan was approved. Check the Rules tab, then approve again." / blocked 판). `LADO_RULES_CHANGED` 는 alert 대신 푸터 `role="status"` "The rules just changed. Check them on the Rules tab, then approve again." + 그 태스크 유효 규칙 `invalidateQueries(refetchType: "all")`(탭이 닫혀 관찰자가 없어도 다시 읽는다). "Request changes" 는 메모 칸이 있는 Plan 탭으로 바꾼다.
  - `lado/LadoLanes.tsx`: `review_required` → "Rules need review", `blocked` → "Blocked", 둘 다 attention(needs-you 점) — Plan 레인 그대로, 칩 추가 없음.
  - `lado/LadoRuleEditorDialog.tsx`: `projectId` prop — 있으면 `scope: "project", scopeId, on: true`, 제목 "New rule for this project", "On for every LADO project" 스위치 숨김(데몬이 Global off + 그 프로젝트 force_on 으로 만든다).
  - `SettingsDialog.tsx`: LADO 줄 셋 — `useLadoMode(localProjectScope && knownLocal && !cloudProject ? projectId : undefined)`(이 컴퓨터 데몬이 아는 로컬 프로젝트만 묻는다), 로컬·비클라우드 블록 안 `if (ladoProject) projectSections.push({ id: "rules", label: t("lado.rules.title"), icon: ScrollText })`, environment 줄 꼴의 `activeProjectSection === "rules"` 렌더. `ui-store.ts`: `ProjectSettingsSection` 에 `"rules"`.
  - i18n 8 로캘: 기본 키 41개(복수형 1개 → en 42 항목, fr·es·pt-BR 은 `_many` 더해 43). 스크립트로 기존 키와 겹치지 않음·빈 값 없음·변수 집합 en 과 같음을 단언하고 파일 끝(S11 키 뒤)에 덧붙였다. ko 는 글로서리대로(규칙·상속·켜기/끄기·켜짐/꺼짐·확인 필요·차단됨·승인, task = 작업, worker = 워커).
- 테스트: 필수 11개 + 추가 3개 — 승인이 규칙을 읽다 `LADO_RULES_CHANGED` 를 만나면 상태 문구·재조회·버튼 다시 켜짐, 워커 문구 접기/펼치기(mono 아님)와 AO 행, 취소된 태스크의 Select 잠금. 기존 "shows the plan and approves the exact revision" 은 셋째 인자를 `""` 에서 스냅숏 해시로 바꿨다. "approves with the rule snapshot hash" 는 Rules 탭이 본 해시(d…)와 승인 직전 해시(e…)를 달리 주고 e… 가 가는지, 그 읽기가 승인보다 먼저인지(`invocationCallOrder`) 본다. "adds a Rules page to local LADO projects only" 는 `SettingsPageTestHarness` 로 LADO 로컬(있음·클릭 시 페이지)·비LADO 로컬·원격·클라우드(없음, 데몬에 묻지도 않음)를 본다.
- 변이 검사 17개(사본 → 변이 → 해당 테스트 파일만 → 복원): 승인 전 새로 읽기 생략(staleTime Infinity), 재승인 막기, 규칙 변경을 alert 로, 변경 뒤 재조회 안 함, review 톤 muted, blocked 를 Queued 로, 보호 행에 Select, 프로젝트 binding 을 task 로, 편집기가 projectId 무시, 클라우드에도 LADO 질의, 모든 프로젝트에 Rules, 심각도 낱말 제거, 태스크 binding 을 project 로, 보관 규칙 이름 안 읽음, "before rules" 숨김, 취소 태스크 편집 가능, AO 읽기 오류 숨김 — 17개 모두 죽었다. 생존 0.
- 게이트(HEAD `3cb3734ca`, 트리 깨끗, `--only` 로 하나씩): G0·G1·G2·G3·G6 MET(수 초), G4 렌더러 전체 vitest MET(1분 24초), G5 타입체크(+e2e) MET(6분 58초). 이 엔트리를 커밋한 뒤 전 게이트를 다시 돌려 증거를 새 HEAD 에 묶는다.
- 하지 않음: 랩 스크린숏(체인 밖 메인 세션 몫 — jsdom 은 버튼 타이포그래피를 못 본다; 이 단계의 버튼 안 글자 스타일은 모두 안쪽 `<span>`), DESIGN.md 결정 행(S11 의 한 행이 이 표면들의 몫이고 문서는 S13), 백엔드 변경 없음, gitleaks.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 플랜에 없어 이 단계에서 정한 것:
  - 프로젝트 설정과 태스크 Rules 탭의 규칙 행은 하나(`LadoEffectiveRuleRow` = `SettingsRow` + `SettingsOptionMenu`). 엔트리는 `ui/select` 를 예로 들었지만 이 행들은 설정 행이고, S11 이 상한 행에 쓴 설정 선택기와 같아야 한다(DESIGN.md "Settings … selectors use the same shared menu/select primitive", S11 회고의 메모). 태스크 뷰 머리의 리비전 선택은 원래대로 `ui/select`.
  - 상세 줄의 "상태" 자리: 상한은 켜짐/꺼짐이 아니라 모드("Auto")를 말한다 — 항상 켜진 규칙의 쓸모 있는 상태는 그 값이다. 상한 값 변경은 Settings → Rules 에만 있다(데스크톱 확인).
  - AO 카드의 "끌 수 없다" 한 줄은 카드 위(`SettingsSection toolbar`)에 두고 어디서 바꾸는지까지 말한다 — 결정에 필요한 다음 행동.
  - 승인 직전 읽기는 Rules 탭과 같은 쿼리 키의 `fetchQuery(staleTime 0)` — 진행 중인 탭 읽기와 겹치면 그것을 쓰고, 읽은 값으로 탭도 맞춘다. 데스크톱은 main 이 다시 읽어 서명하므로 이 값은 브라우저 경로용이지만 늘 보낸다(하나의 경로).
  - `LADO_RULES_CHANGED` 뒤 재조회는 `refetchType: "all"` — 승인은 대개 Plan 탭에서 하므로 그 쿼리에 관찰자가 없어 보통 무효화로는 다시 읽지 않는다(처음 테스트가 이것을 잡았다).
  - Plan 탭(기본 탭)에도 review_required·blocked 상태 줄을 둔다 — 그 탭의 푸터 버튼만으로는 이미 승인된 계획을 왜 다시 승인하는지 알 수 없다.
  - 문제의 심각도는 점 색과 낱말 둘 다("Blocks the run · …") — 색만으로 말하지 않는다.
  - 스냅숏은 다른데 바뀐 규칙이 없을 때(에이전트·모드·AO 지시문·AO 빌드, 결정 g)의 한 줄을 더했다 — 없으면 "Rules changed since approval" 아래가 비어 거짓처럼 보인다.
  - `SettingsDialog` 는 이 컴퓨터 데몬이 목록에 가진, 클라우드가 아닌 프로젝트만 LADO 모드를 묻는다(`knownLocal && !cloudProject`) — 클라우드 목록이 오기 전 클라우드 id 로 데몬에 묻지 않는다.
- 관찰: 타입체크가 7분 반 걸렸다(다른 체인의 `claude -p` 와 docker VM 이 함께 돎). 게이트 G5 는 도구 한도(10분)에 닿을 수 있어 백그라운드로 띄우고 전경에서 그 PID 가 끝나기를 기다렸다.
- 다음 단계 메모(S13 문서): 프로젝트 Rules 페이지는 로컬 LADO 프로젝트에만, 태스크 뷰 Plan | Rules 탭, 승인이 렌더러에서도 `policySha256` 을 보낸다, review_required·blocked 의 재승인과 보드 문구("Rules need review"·"Blocked", Plan 레인), `LADO_RULES_CHANGED` 처리.
