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

- 시작 HEAD: `724a5fd971a0797b3384ae508223888d2d8bf5b7`(`docs(lado): record S10`), 트리 깨끗.
- 설정 카탈로그에 규칙 페이지 없음(`GlobalSettingsSection` id 13개). S10 데이터 계층(`useLadoRules`·`useLadoRule`·`useDeleteLadoRule`·`useLadoRuleChange`·`ladoRuleName`·`ladoRuleDescription`·`ladoStrengthLabel`·`changeLadoCeiling`)은 있으나 쓰는 화면이 없다. 로캘의 `lado.rules.*` 는 builtin 6·strength 3 뿐이고 내장 분류(`workflow`·`tools`·`git`) 번역도 없다.
- `GlobalSettingsForm.test.tsx` 는 "all" 보기로 카탈로그 항목을 전부 렌더한다 — 데몬을 읽는 새 페이지가 들어가면 그 읽기 실패 alert 가 기존 alert 단언과 겹친다(아래 After). 앱의 `SettingsDialog` 는 늘 한 페이지만 넘기므로 "all" 은 테스트에서만 쓰인다.

## After — 수행 후 실측

- 코드 커밋 `8d495b02c` `feat(lado): Rules settings section`: 19 파일, +2086 −9.
  - `settings/RulesSection.tsx`: 바깥 `SettingsSection`(제목 "Rules", 자기 페이지에선 페이지 제목과 같아 숨김, `sectionId="rules"`) + 툴바 `SettingsSearch`("Search rules", 끝에 ghost "New rule") → 본문에 카드들: "Protected rules", 분류별 카드(만든 순서, 분류 없음은 "Other" 로 마지막), 접힌 "Archived"(개수·`aria-expanded` 버튼). 검색은 표시 이름·저장 이름·분류 라벨·지시문을 대소문자 없이 본다. 로딩·읽기 실패(`ladoErrorText`)·일치 없음 문구. 바꾸는 동작(Global 스위치·Archive·Unarchive·상한)은 전부 섹션의 `useLadoRuleChange` 하나로 한 번에 하나씩 — 바뀌는 행의 컨트롤만 잠그고, 실패는 그 행 설명 아래 `role="alert"`, `LADO_RULE_CHANGED` 면 목록을 다시 읽는다. `pending` 이면 `LadoRuleImpactDialog`(Change → `confirm()`, Cancel/Esc → `cancel()`, 적용 실패는 다이얼로그 안). Delete 는 `ConfirmDialog` destructive → `useDeleteLadoRule`; `LADO_RULE_REFERENCED` 면 "Archive it instead" 문장 + Delete 비활성. 편집·기록 다이얼로그는 열 때마다 key 로 다시 마운트.
  - `lado/LadoRuleRows.tsx`: `LadoProtectedRuleRow`(이름·내장 설명, 끝에 muted "Always on" 또는 상한의 공유 `SettingsOptionMenu` — Accept edits/Auto/Bypass permissions, AO 프로젝트 설정 라벨 재사용; 같은 값이면 아무 일도 없음; 스위치·메뉴 없음), `LadoRuleRow`(이름, 상세 한 줄 = [내장 설명] [Archived 에서만 분류] · 강도 · override 요약, `Switch` aria-label "{{name}} for every LADO project", 행 메뉴 Edit·History·Archive·구분선·Delete(파괴적 색) / 보관된 행은 Unarchive·구분선·Delete, 내장 규칙은 Delete 없음). 도우미 `ladoCategoryKey`·`ladoCategoryLabel`·`ladoCeilingModeLabel`·`ladoOverrideSummary` export.
  - `lado/LadoRuleEditorDialog.tsx`: 설정 다이얼로그 틀(`settingsDialog*`), Name·Category(선택)·Instruction(textarea)·Conflicts with(체크 메뉴 = `SettingsMenuTrigger` + `DropdownMenuItem role="menuitemcheckbox"`·`aria-checked`·선택해도 열린 채; 보호 규칙·자기 자신은 없고, 보관된 규칙은 이미 고른 것만), 만들 때만 "On for every LADO project" 스위치(기본 켜짐, scope global). 클라이언트 검증 없음 — dry run 의 데몬 코드 9개를 필드로 매핑해 그 필드 바로 아래 `role="alert"`(+ `aria-invalid`·`aria-describedby`), 필드를 고치면 지운다. 나머지 코드는 푸터 왼쪽. `LADO_RULE_CHANGED` 는 폼 상단 상태 문구 + "Reload"(`fetchLadoRule` → 초안·baseVersion 을 최신으로, 목록 무효화). 편집은 바뀐 필드만 보내고 바뀐 게 없으면 Save 비활성. 영향이 있으면 자체 `LadoRuleImpactDialog`(중첩), Cancel 이면 편집 내용이 남는다. 저장 중엔 닫기·Esc 막음.
  - `lado/LadoRuleHistoryDialog.tsx`: 버전 목록(최신 먼저, "Version N"·"Current"·로캘 시각·actor 라벨: lado→LADO, api→API, human→Confirmed on the desktop, human:dev-presence→Confirmed in development; 모르는 값은 그대로) + 고른 버전 내용(Name·Category·[Permission ceiling]·Instruction·Conflicts with, 빈 값 "None").
  - `lado/LadoRuleImpactDialog.tsx`: `ConfirmDialog` 위에 제목·설명 한 줄·승인된 태스크 공유 목록(제목 + mono `acme/demo#12` — 이슈 ref 에서 provider 와 `@host` 를 뗀 값), Change/Cancel. 닫히는 동안 마지막 영향을 유지.
  - `settingsCatalog.tsx`: `agents` 다음에 `{ id: "rules", icon: ScrollText, label: t("lado.rules.title"), render: RulesSection }`. `ui-store.ts`: `GlobalSettingsSection` 에 `"rules"`.
  - i18n 8 로캘: 기본 키 51개(복수형 4개 → en 55 항목, fr·es·pt-BR 은 `_many` 더해 59). 스크립트로 빈 값 없음·변수 집합 en 과 같음을 단언하고 파일 끝에 덧붙였다. ko 는 기존 LADO 문구대로 task 를 "작업"으로(규칙·보호 규칙·권한 상한·보관·승인·켜짐/꺼짐).
  - `DESIGN.md` 결정 로그 끝 `| 2026-10-22 | LADO fork: Settings → Rules is one page: …` 한 줄.
  - `GlobalSettingsForm.test.tsx`: `RulesSection` 을 표식 div 로 mock(같은 파일의 `HarnessSettingsSection` mock 과 같은 이유) — 안 하면 "all" 보기의 규칙 읽기 실패 alert 때문에 언어·소리 저장 실패 테스트 2개가 "Found multiple elements with the role alert" 로 실패했다.
- 테스트: 필수 9개 + 추가 4개 — 상한 데스크톱 취소면 값 유지·목록 안 다시 읽음, 승인이 쓴 규칙 삭제는 보관 문장 + Delete 비활성, 실패한 변경이 그 행 아래, 편집은 바뀐 필드만 보내고 낡은 버전은 Reload 뒤 새 baseVersion 으로 저장. 상한 테스트는 `changeLadoCeiling` 을 mock 하지 않고 브리지 `lado.presence.changeRule` 을 spy 해 dry run 1회(PATCH `dryRun:true`) 뒤 데스크톱 경로로 바뀌는지 본다.
- 변이 검사 8개(사본 → 변이 → 해당 테스트 파일만 → 복원): 내장 규칙에 Delete 노출, 확인 없이 삭제, 필드 오류를 푸터로, 검색에서 지시문 제외, 영향 다이얼로그 안 열림, 보호 행에 스위치, 만들기에서 On 스위치 무시, Reload 가 baseVersion 을 안 바꿈 — 8개 모두 죽었다. 생존 0.
- 추가 실행: 전체 vitest 514 파일 8534 통과(7 skip). 게이트의 vitest 묶음 + `GlobalSettingsForm.test.tsx` 통과. typecheck(+e2e) 6분 36초 통과. 백엔드 변경 없음.
- 게이트(HEAD `8d495b02c`, 트리 깨끗): `GATES=6 MET=6 UNMET=0`(6분 40초). 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.
- 하지 않음: 랩 스크린숏(체인 밖 메인 세션 몫 — jsdom 은 버튼 타이포그래피를 못 본다; 이 단계의 버튼 안 글자 스타일은 모두 안쪽 `<span>` 에 두었다), gitleaks.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 플랜에 없어 이 단계에서 정한 것:
  - 상세 줄의 분류: 분류별 카드 제목이 이미 분류를 말하므로 행에선 뺐고(DESIGN.md "같은 라벨을 되풀이하지 않는다"), 여러 분류가 섞이는 Archived 에서만 넣었다. 내장 규칙 설명(한국어 커밋)은 같은 줄 앞에 둔다.
  - 내장 규칙의 메뉴엔 Delete 가 없다 — 데몬이 늘 `LADO_RULE_BUILTIN` 으로 거절하는 동작을 보이지 않는다.
  - "New rule" 은 섹션 `action` 이 아니라 검색창 끝(`SettingsSearch trailing`, 단축키 페이지의 "Reset all" 과 같은 자리)에 — 제목이 숨는 자기 페이지에서 `action` 줄과 툴바의 `-mt-6` 이 겹친다.
  - 충돌 규칙 체크 메뉴는 새 프리미티브 없이 `DropdownMenuItem` 에 `role="menuitemcheckbox"` 를 준다.
  - 편집기는 이름·지시문 필수 같은 검증을 따로 하지 않고 데몬 dry run 의 코드를 그대로 필드에 붙인다(검증 규칙이 한 곳). 낡은 버전의 "Reload" 는 초안을 최신 버전으로 바꾼다 — 남의 변경 위에 덮어쓰지 않기 위해.
  - 안쪽 카드 제목은 `SettingsPageTitleContext` 를 비워 렌더 — 사용자가 분류를 "Rules" 로 지어도 제목이 숨지 않게.
  - 스위치는 낙관적으로 움직이지 않는다: 적용·무효화·다시 읽기 뒤에야 바뀐다(진실한 상태). 섹션이 변경 훅 하나를 나눠 쓰므로 변경은 한 번에 하나.
- 관찰: 편집기 테스트("creates a rule with its instruction")의 앞 두 번 실행에서 Radix(FocusScope·Presence·Menu) act 경고가 stderr 에 나왔고, 그 테스트만 돌리거나 이후 5회 실행에선 0 — 타이밍성 잡음, 테스트는 늘 통과.
- 다음 단계 메모(S12):
  - `LadoRuleEditorDialog` 는 지금 Global 에서만 만든다(`scope: "global"`, `on` 스위치). 프로젝트 설정의 "New rule for this project" 는 scope·scopeId prop 을 더해 `scope: "project"` 로 보내고 그때는 `on` 스위치를 숨기면 된다(데몬이 Global off + 그 프로젝트 force_on 으로 만든다).
  - 행 도우미 `ladoCategoryLabel`·`ladoOverrideSummary`·`ladoCeilingModeLabel` 과 `LadoRuleImpactDialog` 는 프로젝트 규칙 행·태스크 Rules 탭에서 그대로 쓸 수 있다. Inherit/On/Off 공유 Select 는 `SettingsOptionMenu`.
  - 게이트 typecheck 는 여전히 6분 30초 남짓.
