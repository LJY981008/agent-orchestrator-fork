# 2026-10-22 LADO 규칙 코어 — S11 설정 → 규칙 UI

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§9 Settings → Rules) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

개발자가 규칙을 한곳에서 관리하는 화면을 만든다(HARNESS §12, PRD §6 Rules manager): 보호 규칙은 항상 켜져 있음을 보이고 권한 상한만 데스크톱 확인으로 바꾸며, 일반 규칙은 전역으로 켜고 끄고, 만들고 고치고, 기록을 보고, 보관·삭제한다. 끄기·보관·삭제는 서로 다른 동작으로 둔다.

## 범위

- 만든다: `frontend/src/renderer/components/settings/RulesSection.tsx`, `frontend/src/renderer/components/lado/LadoRuleRows.tsx`, `LadoRuleEditorDialog.tsx`, `LadoRuleHistoryDialog.tsx`, `LadoRuleImpactDialog.tsx`, 테스트 `components/settings/RulesSection.test.tsx`, `components/lado/LadoRuleEditorDialog.test.tsx`.
- 고친다: `components/settings/settingsCatalog.tsx`(항목 `id: "rules"`, lucide 아이콘 하나, 라벨 `t("lado.rules.title")`), `stores/ui-store.ts`(`GlobalSettingsSection` 에 `"rules"`), 8개 로캘 파일(이 화면 문자열 전부 — 실제 번역), `DESIGN.md` 결정 로그 끝에 한 줄.
- 쓴다: S10 의 `lib/lado.ts`·`hooks/useLado.ts`·`lib/lado-rules.ts`(`useLadoRuleChange`, 내장 규칙 이름 키, 강도 라벨, 오류 문장).
- 하지 않는다: 프로젝트 설정·태스크 뷰·보드(S12).

## 절차

1. **먼저 `DESIGN.md` 를 읽는다**(§6 토큰, §7 타이포, §9 Modal composition 의 Settings 문단·Settings language·Shared-list rule, §13 반패턴). 기존 설정 섹션(`SettingsSection` 을 쓰는 `components/lado/LadoBackgroundSection.tsx`, `StayAwakeSection.tsx`)과 shadcn 프리미티브(`components/ui/{switch,select,dropdown-menu,dialog,input,label,button}.tsx`)만 쓴다. 새 프리미티브·새 토큰 금지.
2. 구성(플랜 §9): 섹션 제목 "Rules"(ko 규칙), 검색 입력("Search rules"), 카드 "Protected rules" — Plan approval required(행 끝 muted "Always on"), Permission ceiling(행 끝 공유 Select: accept-edits / auto / bypass-permissions, 고르면 `changeLadoCeiling` → main 네이티브 확인; 취소면 원래 값), 분류별 카드 — 행: 이름, 상세 한 줄(분류 · 강도 · override 요약 "On in 1 project · Off in 2 tasks"), 끝에 Switch(global) + 행 메뉴(Edit, History, Archive, 구분선, Delete). "Archived" 는 접힌 그룹(Unarchive, Delete). 섹션 머리의 조용한 "New rule" 버튼. 카드는 외곽선 없이 채움 배경 + 행 사이 hairline, 행 세로 패딩 10px, 텍스트 inset 16px(DESIGN.md 2026-10-14 결정). 문장형 대소문자, 설명 문구는 결정에 필요할 때만.
3. 내장 규칙 이름·설명은 `lado.rules.builtin.{planApproval,permissionCeiling,koreanCommits}.*` 키로 번역해 보이고, 사용자 규칙은 저장된 이름 그대로.
4. 바꾸는 동작은 전부 `useLadoRuleChange` 로: dry run → 승인된 태스크가 영향받으면 `LadoRuleImpactDialog`(제목, 태스크 제목 + 이슈 번호 mono 목록, "Change"/"Cancel") → 확인 시 적용. 영향이 없으면 다이얼로그 없이 적용.
5. `LadoRuleEditorDialog`: 만들기·고치기 공용. 이름, 분류(선택), 지시문(textarea), 충돌 규칙(체크 메뉴 — 보호 규칙은 고를 수 없다), 만들 때만 "On for every LADO project" 스위치(기본 켜짐). 저장 실패는 데몬 오류 코드를 `lado-errors` 로 번역해 해당 필드 바로 아래에 보인다. 낡은 버전(`LADO_RULE_CHANGED`)은 폼 상단 상태 문구 + 다시 읽기.
6. `LadoRuleHistoryDialog`: 버전 목록(번호·시각·actor) + 선택한 버전 내용. 현재 버전 표시.
7. Delete 는 파괴적 색 + 확인. `LADO_RULE_REFERENCED` 면 "보관하라" 문장.
8. `<button>` 안 글자 스타일은 안쪽 `<span>` 에(전역 버튼 규칙이 Tailwind 글꼴 클래스를 무시한다).
9. DESIGN.md 결정 로그 끝에 `| 2026-10-22 | LADO fork: Settings → Rules … | … |` 한 줄(구성·보호 행·상한 확인·끄기/보관/삭제 분리와 그 이유).

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): Rules settings section` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S11-settings-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (제목 문자열 그대로 — 게이트가 찾는다)

- `RulesSection.test.tsx`: `lists protected rules as always on`, `changes the permission ceiling through the desktop confirmation`, `turns a rule off globally after a dry run`, `asks before a change that affects approved tasks`, `archives and restores a rule`, `deletes an unused rule after confirmation`, `filters rules by search`
- `LadoRuleEditorDialog.test.tsx`: `creates a rule with its instruction`, `shows the daemon validation message next to the field`

## 수용 게이트

- [ ] S11-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S11-G1: 필수 테스트 제목이 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/settings/RulesSection.test.tsx 'lists protected rules as always on' 'changes the permission ceiling through the desktop confirmation' 'turns a rule off globally after a dry run' 'asks before a change that affects approved tasks' 'archives and restores a rule' 'deletes an unused rule after confirmation' 'filters rules by search' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/lado/LadoRuleEditorDialog.test.tsx 'creates a rule with its instruction' 'shows the daemon validation message next to the field'
  EXPECT: HAS_OK
- [ ] S11-G2: 새 테스트·설정 다이얼로그·i18n 테스트 통과
  CHECK: bash .claude/gate/lado-rules-core.sh vitest src/renderer/components/settings/RulesSection.test.tsx src/renderer/components/lado/LadoRuleEditorDialog.test.tsx src/renderer/components/SettingsDialog.test.tsx src/renderer/i18n/renderer-coverage.test.ts src/renderer/i18n/instance.test.ts
  EXPECT: VITEST_OK
- [ ] S11-G3: 카탈로그·섹션 id·결정 로그가 이어졌다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/components/settings/settingsCatalog.tsx 'id: "rules"' 'RulesSection' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/stores/ui-store.ts '"rules"' && bash .claude/gate/lado-rules-core.sh has DESIGN.md '| 2026-10-22 | LADO fork: Settings → Rules'
  EXPECT: HAS_OK
- [ ] S11-G4: 타입체크
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S11-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
