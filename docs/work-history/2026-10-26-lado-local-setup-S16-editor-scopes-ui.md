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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫, DESIGN.md 에서 따른 규칙)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S17 문서에 적을 화면 구성)
