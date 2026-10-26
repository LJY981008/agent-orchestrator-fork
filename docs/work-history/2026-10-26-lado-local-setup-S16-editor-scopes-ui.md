# 2026-10-26 LADO 로컬 설정 채택 — S16 화면: 편집기·이력·변경 감지·만들기, 프로젝트·태스크·실행 화면

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(소유자 결정 3, §1 결정 l·m, §15) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

관리 항목을 앱 안에서 바꾸는 화면을 마저 만든다: 텍스트 항목 편집(차이 미리보기 → 영향 → 필요하면 데스크톱 확인), 이력, 변경 감지 검토(유지/되돌리기), 새 항목 만들기. 그리고 범위별 화면 — 프로젝트 설정 → 규칙의 setup 상속/켜기/끄기와 저장소 파일 카드(편집 = 새 태스크), 태스크 규칙 탭과 실행 패널의 setup — 을 잇는다.

## 범위

- 만든다: `components/lado/setup/LadoSetupItemEditor.tsx`, `LadoSetupHistoryDialog.tsx`, `LadoSetupDriftReview.tsx`, `LadoSetupCreateDialog.tsx`, 각 테스트(이력은 편집기 테스트에 포함 가능).
- 바꾼다: `LocalSetupSection.tsx`·`LadoSetupItemList.tsx`·`LadoSetupConflictsCard.tsx`(S15 — 메뉴에 편집·이력·변경 감지, 충돌 카드의 편집, "새 항목"), `components/lado/LadoProjectRulesSection.tsx`(+ 테스트), `components/lado/LadoTaskRules.tsx`(+ 테스트), `components/lado/LadoExecutionPanel.tsx`(+ 테스트), `lib/lado-errors.ts` 의 `rules_changed` 문구("규칙이나 로컬 설정이 바뀜"), 8개 로캘, `DESIGN.md` 결정 기록 한 줄.
- 하지 않는다: 백엔드.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). **`DESIGN.md` 를 먼저 읽는다.** 기준 구현은 `LadoRuleEditorDialog.tsx`, `LadoRuleHistoryDialog.tsx`, `LadoRuleImpactDialog.tsx`.
1. 편집기(텍스트 항목만 — 소유자 결정 3): Markdown 종류는 기존 `MarkdownEditor`, JSON(settings 키·MCP 서버)은 같은 CodeMirror 기반의 단순 편집기(새 의존성 금지 — 이미 있는 `@codemirror/*` 만), 저장 전 `@pierre/diffs` 로 옛·새 차이, dry run 의 충돌 증감과 영향(승인된 태스크 목록 → `LadoRuleImpactDialog` 꼴의 확인), `needsPresence` 면 데스크톱 확인(S14 흐름), 기준 버전 충돌(409)이면 다시 읽기 안내. 폴더 항목은 파일 목록(읽기 전용)과 "관리 폴더에서 고친 뒤 변경 감지로 반영" 안내.
2. 이력: 버전마다 번호·시각·actor·내용(텍스트) 또는 파일 목록(폴더).
3. 변경 감지 검토: `edited` 는 관리 내용과 최신 버전의 차이 + 유지(코드 종류는 확인)·되돌리기; `new_local` 은 "원래 위치에 새 항목" + 채택 대화상자(S15)로 가는 동작.
4. 새 항목: 종류 선택(지침, 설정 키, 한 파일 스킬, 에이전트, 명령, 출력 스타일, MCP 서버) → 이름 → 내용 → 저장(편집기와 같은 흐름).
5. 프로젝트 설정 → 규칙(`LadoProjectRulesSection.tsx`): 기존 규칙 행과 같은 꼴로 관리 setup 항목 행(상속/켜기/끄기, 실제 상태와 출처), 그리고 읽기 전용 "저장소에서" 카드(저장소 추적 파일: 경로·크기, "도구가 작업 폴더에서 읽음"). 그 카드의 "편집"은 그 프로젝트의 New Task 작성 창을 연다 — 첫 줄 `Update <경로>`, 본문은 비움(작성 창을 여는 기존 경로를 찾아 쓴다; 없으면 전역 New Task 대화상자를 프로젝트를 정해 여는 기존 함수).
6. 태스크 규칙 탭(`LadoTaskRules.tsx`): 유효 setup 항목(출처 포함), 태스크 단위 상속/켜기/끄기, 승인 스냅숏과 지금의 setup 차이(추가·제거·변경), 수락한 충돌.
7. 실행 패널(`LadoExecutionPanel.tsx`): 스냅숏의 setup 항목 이름(접힘).
8. 문구: `rules_changed` 이유 문장을 "규칙이나 로컬 설정이 바뀌었습니다" 꼴로 8개 로캘 모두(레인 카드·태스크 화면이 같은 키를 쓰면 한 곳).
9. `DESIGN.md` 결정 기록에 한 줄: `LADO fork: local setup editor` 로 시작 — 편집기·차이·변경 감지·프로젝트·태스크의 setup.
10. 커밋 메시지 예: `feat(lado): edit managed setup and show it per project and task`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S16-editor-scopes-ui.md` 로 전건 MET 를 확인한다. typecheck 가 7분쯤 걸리므로 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `src/renderer/components/lado/setup/LadoSetupItemEditor.test.tsx` — 차이 표시, dry run 뒤 저장, 코드 종류는 확인 흐름, 409 안내, 폴더 항목은 읽기 전용, 이력 목록
- `src/renderer/components/lado/setup/LadoSetupDriftReview.test.tsx` — edited 의 유지·되돌리기, new_local 의 채택으로 가기
- `src/renderer/components/lado/setup/LadoSetupCreateDialog.test.tsx` — 종류별 입력, 이름 겹침 오류 표시
- `src/renderer/components/lado/LadoProjectRulesSection.test.tsx` — setup 행의 상속/켜기/끄기, 저장소 카드의 편집이 작성 창을 그 프로젝트·제목으로 엶(HARNESS §15 항목 14 의 뒷절반)
- `src/renderer/components/lado/LadoTaskRules.test.tsx` — setup 차이와 태스크 단위 바인딩
- `src/renderer/components/lado/LadoExecutionPanel.test.tsx` — setup 이름
- `src/renderer/i18n/renderer-coverage.test.ts` — 하드코딩 영어 없음

## 수용 게이트

- [ ] S16-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S16-G1: 새 화면 테스트 파일 3개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/lado/setup/LadoSetupItemEditor.test.tsx src/renderer/components/lado/setup/LadoSetupDriftReview.test.tsx src/renderer/components/lado/setup/LadoSetupCreateDialog.test.tsx
  EXPECT: VITEST_OK
- [ ] S16-G2: 범위 화면 테스트와 커버리지 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/lado/LadoProjectRulesSection.test.tsx src/renderer/components/lado/LadoTaskRules.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S16-G3: S15 화면 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/settings/LocalSetupSection.test.tsx src/renderer/components/lado/setup/LadoSetupAdoptDialog.test.tsx src/renderer/components/lado/setup/LadoSetupAdoptionsCard.test.tsx src/renderer/components/lado/setup/LadoSetupConflictsCard.test.tsx
  EXPECT: VITEST_OK
- [ ] S16-G4: DESIGN.md 결정 기록에 편집기 줄
  CHECK: bash .claude/gate/lado-local-setup.sh has DESIGN.md 'LADO fork: local setup editor'
  EXPECT: HAS_OK
- [ ] S16-G5: 프런트 typecheck
  CHECK: bash .claude/gate/lado-local-setup.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S16-G6: 새 의존성 없음
  CHECK: git diff --quiet lado-step7-chain-def HEAD -- frontend/package.json frontend/package-lock.json && echo NO_NEW_DEPS
  EXPECT: NO_NEW_DEPS
- [ ] S16-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `2739c2a64`(docs(lado): record S15), 트리 깨끗, 브랜치 `feat/lado-local-setup`. Linux, Node v24.21.0, vitest 4.1.8.
- 출발점: S15 화면(설정 → 로컬 설정의 충돌 카드·도구 카드·채택 대화상자·채택 기록)은 있고, 행 메뉴는 삭제뿐, 충돌 카드에 편집 없음, 손으로 바뀐 관리 사본(edited drift)을 보이는 곳 없음, 새 항목 만들기 없음. 프로젝트 규칙 페이지·태스크 규칙 탭·실행 패널에는 setup 이 없다(fixture 의 빈 `setup` 뿐). New Task 요청(`requestNewTask(projectId, hostId)`)에는 미리 채울 문장 칸이 없고 `TaskComposerView` 만 `initialPrompt` 를 받는다. `MarkdownEditor` 는 열리면 늘 스스로 포커스를 가져간다. 새 의존성 없이 쓸 수 있는 것: `@codemirror/{commands,language,search,state,view}`(JSON 언어 패키지 없음), `@pierre/diffs/react` 의 `MultiFileDiff`(옛·새 내용으로 차이를 계산).
- 앞 단계 메모 중 이 단계 몫: S08(new_local 의 `itemId` 는 같은 이름의 live 항목 → 그대로는 채택 불가), S11(태스크 탭은 꺼진 것을 포함한 `Items` 와 `Task` 상태, 차이는 `SetupAdded/Removed/Changed`, `installedSha256` 으로 "설치된 플러그인이 바뀜"), S12(차이는 `before.text`/`after.text`, 폴더는 `files`, dry run 의 새 id 는 비어 있음, `PREVIEW_INCOMPLETE`, 만들기에 hook 스크립트 없음), S13(프로젝트 페이지는 `GET /lado/setup/items?projectId=`, 태스크 탭은 effective `setup.items`·`approved.setup*`, 편집기·이력은 `GET /lado/setup/items/{id}` 의 `versions[].content`, 폴더는 `managedPath`, 실행 패널은 `snapshot.setup`), S14(미리보기 `previewLadoSetupChange`, 저장 `useLadoSetupChange`, drift `{op:"drift", input:{action, sha256}}`, 만들기 `{op:"create"}`, 409 문장), S15(행 메뉴·충돌 카드 동작 칸 자리, `lado-setup-view.ts` 재사용, `test/lado-setup-fixtures.ts`, 문자열 하위 이름 `editor·history·drift·project·task`), S11 의 review 이유 문구(S16 몫).
- DESIGN.md 에서 따른 규칙: 설정 대화상자 틀(제목 하나·닫기·본문·바닥 동작), 카드 = 채운 배경 + 항목 사이 hairline, 행 세로 10px·16px inset, 문장형 대소문자, 메뉴는 묶고 파괴 동작은 구분선 뒤 한 번, 파괴 동작은 파괴 색, 비활성 이유, 색은 의미에만(심각도 점), mono 는 경로·코드에만, 결정을 바꾸는 문장만, 공유 primitive(SettingsSection·SettingsRow·SettingsOptionMenu·DropdownMenu·Dialog·`LadoRuleImpactDialog`), `<button>` 글꼴은 안쪽 `<span>`, 스크롤은 fieldset 이 아닌 바깥 div. 기준 구현 `LadoRuleEditorDialog`(기준 버전 → 다시 읽기, 편집 유지), `LadoRuleHistoryDialog`(버전 목록 + 고른 버전 내용), `LadoRuleImpactDialog`(승인된 태스크 확인).

## After — 수행 후 실측

- 커밋: `966b997e9 feat(lado): edit managed setup and show it per project and task`, 그리고 이 엔트리 커밋.
- 바꾼 것:
  1. `components/lado/setup/LadoSetupItemEditor.tsx`(새): 넓은 설정 대화상자. 텍스트 항목은 편집 → "변경 확인"(PATCH dry run: `@pierre/diffs` 차이, 이 변경으로 생기는·없어지는 충돌, 경고, 데스크톱 확인이 필요하면 그 문장) → 저장(`useLadoSetupChange`: 승인된 태스크에 닿으면 `LadoRuleImpactDialog` 꼴 확인, `needsPresence` 면 S14 데스크톱 확인, 취소면 그대로). Markdown 종류는 `MarkdownEditor`, settings 키·MCP 서버는 JSON, hook 스크립트는 일반 텍스트로 CodeMirror 코드 편집기. JSON 이 아니면 데몬에 묻기 전에 칸 아래 오류. 409 `LADO_SETUP_ITEM_CHANGED` 면 "다시 불러오기" 안내와 버튼(검토·저장 비활성) → 최신 버전을 기준으로 편집을 유지하고 다시 검토. 내용 거부(`CONTENT_INVALID`·`LOGIN_KEY`)는 칸 아래, 그 밖은 바닥 줄. 폴더 항목은 읽기 전용(파일 목록·크기·실행 파일 표시, 관리 폴더 경로, "관리 폴더에서 고친 뒤 변경 감지에서 유지" 안내, 닫기만).
  2. `LadoSetupHistoryDialog.tsx`(새): 버전 최신순(번호·현재·시각·actor — 규칙의 actor 문구), 고른 버전의 텍스트(mono) 또는 파일 목록, 못 읽은 버전·빈 버전 문장.
  3. `LadoSetupDriftReview.tsx`(새): "Changes found" 카드(drift 가 있을 때만, 충돌 카드 다음) — edited 는 "검토"(keep dry run 으로 최신 버전과 관리 사본의 차이, "세션은 유지하기 전까지 버전 n 을 받음", "버전 n+1 로 유지"(주 동작, 코드 종류는 데스크톱 확인)·"버전 n 으로 되돌리기"(파괴 색)), new_local 은 "원래 위치에 새로 생김" + "채택…"(채택 대화상자를 그 항목만 고른 채로), 같은 이름 관리 항목이 있으면 사유 문장만. 폴더 drift 는 추가·제거·변경된 파일 경로.
  4. `LadoSetupCreateDialog.tsx`(새): 도구(Claude Code·Codex) → 종류(Codex 는 지침·MCP 서버, hook 스크립트 없음) → 이름(종류별 예시 placeholder, mono) → 내용(종류별 편집기, 스킬은 front matter 틀) → 같은 검토 → 만들기. `ITEM_EXISTS`·이름 `CHANGE_INVALID` 는 이름 칸 아래(포커스), 내용 거부는 내용 칸 아래.
  5. `LadoSetupContent.tsx`(새, 공유): CodeMirror 코드 편집기(줄 번호·undo·검색, 접근 이름·`aria-invalid` 를 Compartment 로), 내용 칸, 텍스트를 JSON 값으로, 차이(`MultiFileDiff`, unified, 파일 머리 없음, AO 표면 CSS), 파일 목록, dry run 검토, 넓은 대화상자 class. `lado-setup-view.ts` 에 만들 수 있는 종류·JSON/Markdown 종류·폴더 판정·항목 정렬·크기(Intl unit).
  6. `LadoSetupScopeRows.tsx`(새): `LadoSetupScopeCard`(프로젝트·태스크 범위 행: 이름, "도구 · 종류 · 켜짐/꺼짐 · 출처", 공유 select 상속/켜기/끄기, 바인딩은 dry run → 영향 확인 → 데스크톱 확인), `LadoSetupProjectSection`(`GET …/items?projectId=`, 관리 항목이 없으면 안 보임), `LadoSetupRepositorySection`(인벤토리의 그 프로젝트 `repo_tracked` 줄: mono 경로·도구·종류·크기, 검사 오류 줄, "도구가 작업 폴더에서 읽음" 안내, "편집" = `requestNewTask(projectId, undefined, "Update <경로>")`).
  7. S15 파일: `LadoSetupItemList` 행 메뉴 편집·기록·변경 확인(edited 일 때만)·구분선·삭제, edited 항목의 상태 줄에 "LADO 밖에서 바뀜"; `LadoSetupConflictsCard` 에 항목이 있는 충돌의 "편집"; `LocalSetupSection` 머리 줄에 "새 항목", drift 카드, drift 에서 온 채택; `LadoSetupAdoptDialog` 선택 prop `only`.
  8. 범위 화면: `LadoProjectRulesSection` 규칙 카드 다음에 setup 카드와 저장소 카드; `LadoTaskRules` 에 "이 작업의 로컬 설정" 카드(작업자 도구 항목, 취소·닫힌 태스크는 읽기 전용), 승인 비교 아래 "추가·제거·바뀐 로컬 설정"(이름은 지금 항목과 승인 스냅숏, `enabledPlugins` 의 설치 해시만 바뀌면 "(설치된 플러그인이 바뀜)"), 수락한 경고 줄; `LadoExecutionPanel` 에 스냅숏 setup 이 있을 때만 "로컬 설정 · 항목 n개"(접힘, 펴면 이름 · 종류).
  9. 문구: 8개 로캘에 새 키 72개(`lado.setup.{editor,diff,history,drift,create,project,task}.*`, `list.reviewChange`, `conflicts.editLabel`, `lado.run.setup`; 스크립트로 자리표시자 일치·한국어 자리표시자 바로 뒤 조사 없음·기존 키와 겹침 없음 확인), `rules_changed` 이유와 같은 뜻 문장 6개를 "규칙이나 로컬 설정이 바뀜" 꼴로.
  10. `DESIGN.md` 결정 기록에 2026-10-26 `LADO fork: local setup editor, drift and the project and task setup …` 한 줄.
  11. 업스트림 훅 줄: `ui-store`(`newTaskRequest.prompt`, `requestNewTask(projectId, hostId?, prompt?)`), `GlobalNewTaskDialog`(+3), `NewTaskDialog`(+3), `TaskComposer`(+4, 미리 채운 문장은 dirty), `MarkdownEditor`(`autoFocus` 기본 true).
- 엔트리와 다르게 한 것과 이유:
  1. 작성 창을 문장과 함께 여는 기존 경로가 없어(`requestNewTask` 는 프로젝트만) 위 11의 한두 줄씩으로 `TaskComposerView.initialPrompt`(이미 있던 prop)까지 넘겼다. 첫 줄은 로캘을 따른다(영어 `Update <path>`, 한국어 `<path> 수정`) — 사람이 고칠 이슈 제목이라.
  2. `MarkdownEditor` 에 `autoFocus` — 새 항목 대화상자에서 내용 편집기가 이름 칸의 포커스를 가져가지 않게(Radix 는 안에 포커스가 있으면 자동 포커스를 건너뛴다).
  3. 저장 때 dry run 이 한 번 더 돈다(PATCH 셋: 검토, 저장 흐름의 dry run, 실제) — `useLadoSetupChange` 를 바꾸지 않고, 저장 직전 상태로 영향·확인을 정하려고.
  4. 되돌리기는 따로 확인 대화상자를 두지 않았다 — 잃을 차이를 보여 주는 검토 대화상자 자체가 결정 지점이고, 버튼이 "버전 n 으로 되돌리기"(파괴 색)라.
  5. 엔트리 목록 밖 파일: 공유 조각 `LadoSetupContent.tsx`·`LadoSetupScopeRows.tsx`, `LadoSetupAdoptDialog.tsx`(`only`), 업스트림 훅 4파일, `lado-errors.test.ts`·`LadoTaskDialog.test.tsx`·`GlobalNewTaskDialog.test.tsx`(바뀐 문장과 미리 채운 문장), `test/lado-setup-fixtures.ts`(항목 상세 빌더).
  6. `rules_changed` 이유(`lado.reason.rules_changed`)만이 아니라 같은 뜻의 태스크 화면·승인 오류·규칙 탭 문장도 바꿨다. 보드 카드의 상태 이름 "Rules need review" 는 그대로.
  7. 태스크 탭의 수락한 경고는 문장에 충돌 details 가 필요해, 수락이 있을 때만 `GET …/conflicts?projectId&taskId` 를 읽는다.
  8. 첫 실행에서 만들기 테스트가 실제 버그를 잡았다: 스킬의 front matter 틀이 다른 종류로 바꿔도 남아 JSON 이 깨졌다 → 손대지 않은 틀은 다른 종류에서 지운다.
- 확인한 명령과 결과:
  - 새 테스트 13개(LadoSetupItemEditor 6 = 편집기 5 + 이력 1, LadoSetupDriftReview 5, LadoSetupCreateDialog 2), 기존 파일에 더함: LadoProjectRulesSection +2, LadoTaskRules +1(+취소 태스크의 setup select 비활성), LadoExecutionPanel +1, GlobalNewTaskDialog +1, lado-errors 단언 +2.
  - `npx vitest run src/renderer` → 413 files, 6608 passed(6 skipped), 1분 28초.
  - `npm run typecheck` 7분 54초 깨끗(커밋 전).
  - frontend 에는 eslint 설정이 없어 lint 는 돌리지 않았다.
- 게이트: 코드 커밋 `966b997e9` 에서 `gate-check.sh --run … --only` 로 G0·G1·G2·G3·G4·G6 MET. G5(typecheck)·G7 과 전체는 엔트리 커밋 뒤 다시 돌린 결과가 그 커밋의 증거다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 편집기(차이·충돌 증감·영향·데스크톱 확인·409·폴더 읽기 전용), 이력, 변경 감지 검토(유지·되돌리기, new_local 채택), 새 항목, 프로젝트 규칙의 setup 행과 저장소 카드(편집 = 새 태스크), 태스크 규칙 탭, 실행 패널, `rules_changed` 문구, DESIGN.md 줄, 8개 로캘, 필수 테스트 파일 7개. 엔트리 밖은 After 의 "다르게 한 것" 1·2·5·6.
- 남은 틈:
  - 실제 앱 화면(라이트·다크, 좁은 폭, 한국어, 설정 모달 위 New Task)은 보지 않았다 — 렌더러 테스트만. 메인 세션 랩(격리 데몬 + 임시 HOME, lado-lab-recipe)으로 편집·유지·되돌리기·만들기·데스크톱 확인을 끝까지 볼 것.
  - New Task 작성 창은 프로젝트 설정 모달 위에 열린다(설정은 닫지 않음) — 두 모달이 겹치는 모양은 실기에서 확인 필요.
  - 저장 때 dry run 이 두 번이라, 검토와 저장 사이에 상태가 바뀌면 화면의 차이는 검토 때 것이고 영향·확인은 저장 때 것이다.
  - drift 검토의 미리보기 질의는 `["lado","setup"]` 밖(`lado-setup-drift-preview`)이라 열린 동안 다른 변경이 와도 다시 미리 보지 않는다(유지는 sha 가 다르면 409 로 막힘).
  - 수락 취소(revoke)는 여전히 화면에 없다. 충돌 카드는 Global 범위뿐(태스크 탭은 수락한 경고만 보임).
  - 프로젝트 setup 카드는 관리 항목이 없으면 안 보인다(결정 a). 저장소 카드는 추적 파일만 — 비추적 개인 파일(결정 k)은 설정 → 로컬 설정의 접힘 그룹에만.
  - 편집기 높이는 고정(h-72), 차이는 unified 만.
- 다음 단계 메모(S17 문서에 적을 화면 구성):
  - 설정 → 로컬 설정: 머리 줄(설명·마지막 검사·다시 검사·채택…·새 항목), 미검증 OS 줄, 충돌(차단 → 경고 → 정보, 편집·끄기·수락), 변경 감지(Changes found: edited 검토 = 유지 n+1 / 되돌리기 n, new_local = 그 항목만 채택), 도구 카드(종류 묶음, 행 메뉴 편집·기록·변경 확인·삭제), 채택 기록과 복원.
  - 편집기: 텍스트 항목만(Markdown·JSON·스크립트), 변경 확인(dry run) → 저장(영향 확인 → 데스크톱 확인), 기준 버전 409 → 다시 불러오기(편집 유지), 폴더 항목은 파일 목록 + 관리 폴더 경로 + 변경 감지 안내. 새 항목은 hook 스크립트를 만들 수 없음.
  - 프로젝트 설정 → 규칙: 규칙 카드 → 이 프로젝트의 로컬 설정(두 도구, 상속/켜기/끄기) → 저장소에서(추적 파일, 편집 = 그 프로젝트의 New Task, 첫 줄 `Update <경로>` 꼴, 로캘을 따름) → AO 프로젝트 설정.
  - 태스크 규칙 탭: 승인 비교 아래 로컬 설정 추가·제거·변경(설치된 플러그인 변경 표시), 수락한 경고, 이 작업의 로컬 설정 카드. 실행 패널: 로컬 설정 n개(접힘). review 이유 문구 "규칙이나 로컬 설정이 바뀜"(상태 이름 "Rules need review" 는 그대로).
  - 업스트림 훅: `requestNewTask(projectId, hostId?, prompt?)` 와 `TaskComposer`·`NewTaskDialog` 의 `initialPrompt`, `MarkdownEditor` 의 `autoFocus`.
