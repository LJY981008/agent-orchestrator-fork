# 2026-10-26 LADO 로컬 설정 채택 — S15 화면: 설정 → 로컬 설정(인벤토리·충돌·채택·복원)

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 h·p, §15) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

개발자가 한곳에서 자기 Claude Code·Codex 설정을 보고(무엇이 어디 있고, 채택할 수 있는지와 이유), 충돌을 확인하고, 채택하고, 되돌린다. 채택은 사용자 파일을 지우는 일이므로 대화상자가 무엇이 어디로 가는지, 직접 실행에는 더 이상 안 보인다는 것, 전제 조건이 왜 막는지를 분명히 보여 주고 데스크톱 확인으로 끝난다.

## 범위

- 만든다: `frontend/src/renderer/components/settings/LocalSetupSection.tsx`, `components/lado/setup/`(새 폴더 — `LadoSetupItemList.tsx`, `LadoSetupConflictsCard.tsx`, `LadoSetupAdoptDialog.tsx`, `LadoSetupAdoptionsCard.tsx`), 각 테스트.
- 바꾼다(훅 줄): `settingsCatalog.tsx`(Rules 다음에 `localSetup` 항목), `stores/ui-store.ts`(섹션 id `localSetup`), `components/GlobalSettingsForm.test.tsx`(다른 LADO 섹션처럼 모의), 8개 로캘(`lado.setup.*` 키, 기존 LADO 키 뒤에), `DESIGN.md` 결정 기록 한 줄.
- 하지 않는다: 편집기·이력·변경 감지 검토·프로젝트·태스크 화면(S16).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). **`DESIGN.md` 를 먼저 읽는다**(설정 모달 구성, 문장형 대소문자, 공유 목록 규칙, 카드·행·메뉴, 위험 동작 확인). 기준 구현은 `RulesSection.tsx`·`RunsSection.tsx`.
1. 섹션 구성(위에서 아래로):
   - 머리 줄: 섹션 설명 한 문장("Claude Code와 Codex 설정을 이 앱 한곳에서 관리합니다. 채택한 항목은 이 앱이 띄우는 세션에만 적용됩니다."), 마지막 검사 시각(시계 시각), "다시 검사".
   - OS 줄: 검증됨이면 조용히, 아니면 경고 톤으로 "이 OS에서는 아직 전달을 확인하지 않아 채택할 수 없습니다"(Windows·macOS) — 채택 버튼 비활성과 이유.
   - 충돌 카드(있을 때만): 차단·경고·정보 순, 행마다 문장(`ladoSetupConflictText`), 대상 항목, 동작(끄기 — 전역 바인딩 off, 편집 — S16 의 편집기를 여는 자리(이 단계에서는 버튼 숨김), 수락 — 경고만).
   - 도구별 카드(Claude Code, Codex): 종류별 묶음(지침, 설정, 스킬, 에이전트, 명령, 출력 스타일, hook 스크립트, MCP 서버), 행 = 이름, 한 줄 상태(관리됨·켜짐/꺼짐, 채택 가능, 채택 불가 + 사유 문장, 관리 안 함 + 사유, 저장소 파일), 위치(짧은 경로), 관리 항목은 전역 스위치(코드 실행 종류를 켜면 데스크톱 확인 — S14 흐름), 메뉴(이 단계: 삭제(확인 대화상자) — 편집·이력·변경 감지는 S16 이 더함). 오래 걸리지 않게 "관리 안 함/절대 채택 안 함"은 기본 접힘.
   - "채택…" 버튼(채택 가능 항목이 있을 때) → `LadoSetupAdoptDialog`: 항목 목록(체크, 기본 동작 move/keep 선택), 다시 쓰기 목록(전·후), 추가될 충돌, 경고 문단(직접 실행에서는 더 이상 안 보임, 백업과 복원), 전제 조건(실행 중인 세션·프로세스 목록 — 있으면 "닫고 다시 시도"), 확인 → S14 의 `adoptSetup` → 결과(옮긴 수, 검증 결과, 실패면 단계와 경로).
   - 채택 기록 카드: 채택마다 시각, 항목 수, 상태(완료·일부·복원됨), "복원"(S14 의 `restoreSetup`) → 결과 보고(옆에 둔 파일 경로 포함).
2. 상태별 화면: 로딩, 오류(데몬 문장), 빈 상태(두 도구 다 설정 없음), 미검증 OS.
3. 문자열은 8개 로캘(한국어는 플랜 §0 용어집, 자리표시자 뒤 조사 금지). 하드코딩 영어 금지(커버리지 테스트).
4. `DESIGN.md` 결정 기록에 한 줄(기존 2026-10-22·10-23 LADO 줄 꼴, 날짜 칸 다음에 `LADO fork: Settings → Local setup` 으로 시작): 섹션 구성, 채택 대화상자, 미검증 OS 표시, 복원.
5. 커밋 메시지 예: `feat(lado): Settings → Local setup with adoption and restore`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S15-settings-ui.md` 로 전건 MET 를 확인한다. typecheck 가 7분쯤 걸리므로 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `src/renderer/components/settings/LocalSetupSection.test.tsx` — 상태 4종, 도구·종류 묶음, 사유 문장, 전역 스위치가 바인딩 변경을 부름(코드 종류는 확인 흐름), 미검증 OS 에서 채택 비활성과 이유, 다시 검사
- `src/renderer/components/lado/setup/LadoSetupAdoptDialog.test.tsx` — 항목·동작·다시 쓰기·경고·전제 조건 표시, 실행 중 목록이 있으면 확인 비활성, 다시 못 쓴 hook 경로(`LADO_SETUP_HOOK_PATH_MOVED`)가 있으면 비활성, 그 밖의 blocking 충돌은 경고 톤으로 보이되 막지 않음(S07 After 8), 확인이 `adoptSetup` 을 정확한 항목으로 부름
- `src/renderer/components/lado/setup/LadoSetupAdoptionsCard.test.tsx` — 복원이 `restoreSetup` 을 부르고 보고를 보여 줌, 복원된 채택에는 버튼 없음
- `src/renderer/components/lado/setup/LadoSetupConflictsCard.test.tsx` — 심각도 순서, 경고만 수락 가능, 끄기가 바인딩 off
- `src/renderer/i18n/renderer-coverage.test.ts` — 하드코딩 영어 없음(기존 테스트)

## 수용 게이트

- [ ] S15-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S15-G1: 필수 테스트 파일 5개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/settings/LocalSetupSection.test.tsx src/renderer/components/lado/setup/LadoSetupAdoptDialog.test.tsx src/renderer/components/lado/setup/LadoSetupAdoptionsCard.test.tsx src/renderer/components/lado/setup/LadoSetupConflictsCard.test.tsx src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G2: 설정 목록·섹션 id 에 로컬 설정이 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has frontend/src/renderer/stores/ui-store.ts '"localSetup"' && bash .claude/gate/lado-local-setup.sh has frontend/src/renderer/components/settings/settingsCatalog.tsx 'localSetup' 'LocalSetupSection'
  EXPECT: HAS_OK
- [ ] S15-G3: 기존 설정 화면 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/settings/RulesSection.test.tsx src/renderer/components/GlobalSettingsForm.test.tsx
  EXPECT: VITEST_OK
- [ ] S15-G4: DESIGN.md 결정 기록에 로컬 설정 줄
  CHECK: bash .claude/gate/lado-local-setup.sh has DESIGN.md 'LADO fork: Settings → Local setup'
  EXPECT: HAS_OK
- [ ] S15-G5: 프런트 typecheck
  CHECK: bash .claude/gate/lado-local-setup.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S15-G6: 업스트림 설정 파일 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 40 frontend/src/renderer/components/settings/settingsCatalog.tsx frontend/src/renderer/stores/ui-store.ts frontend/src/renderer/components/GlobalSettingsForm.test.tsx frontend/src/renderer/components/SettingsDialog.tsx
  EXPECT: BUDGET_OK
- [ ] S15-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `efe6d9e33`(docs(lado): record S14), 트리 깨끗, 브랜치 `feat/lado-local-setup`. Node v24.21.0, vitest 4.1.8, `CHAIN_RUN=1`, `GATE_TIMEOUT=1800`, umask 0022. diff-budget 대상 4파일은 태그 이후 0/40, S15-G0 `DEFS_UNCHANGED`.
- 출발점: S14 의 렌더러 데이터 층(`lib/lado-setup.ts`, `hooks/useLadoSetup.ts`, `lado-errors.ts` 의 setup 번역, 데스크톱 확인 채널 셋)은 있고 화면은 없다. 전역 설정 목록은 Rules 다음 Runs 이고, `SettingsDialog.test.tsx` 의 LADO 테스트가 "Runs 가 Rules 바로 다음"을 단언한다.
- 앞 단계 메모 중 이 단계 몫: S02(채택 목록은 오래된 것부터 → 화면이 뒤집는다), S05(목록 키는 `path`, 사유 `symlink`·`unreadable`, 위치 `managed`), S07 After 8(다시 못 쓴 hook 경로만 채택을 막고, 그 밖의 blocking 은 보이기만), S08(복원 보고의 `beside`·`path` 는 루트 기준 → 인벤토리 `roots` 로 앞을 붙임, 같은 이름의 live 항목이 있으면 `already_managed`), S10(Codex 지침 짝 채택·keep 경고 문구), S13(인벤토리 하나로 OS 줄·목록·전역 스위치·채택 카드, 충돌은 `GET …/conflicts`, 이름은 `details.item`/`itemId` → `items[]`), S14(훅·함수 이름, dry run → 영향 확인 → 데스크톱 확인 흐름, `lado.setup.*` 의 겹치지 않는 하위 이름, 미리보기 debounce).
- DESIGN.md 에서 따른 규칙: Settings 모달 구성(제목 하나, 카드 = 채운 배경 + 항목 사이 hairline, 행 세로 10px, 16px inset), 문장형 대소문자, 공유 목록(카드 안 종류 묶음은 조용한 캡션 + 구분선, 카드 안 카드 없음), 위험 동작(삭제는 파괴 색 + 확인 대화상자), 비활성 이유를 보임, 색은 의미에만(차단 needs-you, 경고 caution, 정보 idle), 공유 primitive(SettingsSection·SettingsRow·SettingsOptionMenu·Switch·DropdownMenu·Checkbox·ConfirmDialog·설정 대화상자 틀), `<button>` 글꼴은 안쪽 `<span>`. 기준 구현 RulesSection(카드·접힌 Archived·행 메뉴·영향 대화상자)과 RunsSection(제목 아래 설명 한 줄).

## After — 수행 후 실측

- 커밋: `f7ec168be feat(lado): Settings → Local setup with adoption and restore`, 그리고 이 엔트리 커밋.
- 바꾼 것:
  1. `components/settings/LocalSetupSection.tsx`(새): 머리 줄(설명 한 문장, 마지막 검사 시계 시각 `formatResetTime`, "다시 검사", 채택 가능 항목이 있을 때 "채택…"), 미검증 OS 면 caution 색 OS 줄과 채택 버튼 비활성(`aria-describedby` = 그 줄), 상태 넷(로딩, 오류 = `ladoErrorText`, 빈 상태 = 두 도구 모두 카드에 보일 줄·채택·루트 오류가 없음, 미검증 OS), 그 아래 충돌 카드 → 도구 카드 둘 → 채택 기록 카드. 채택 대화상자는 열 때마다 key 로 다시 마운트.
  2. `components/lado/setup/lado-setup-view.ts`(새): 도구·종류 순서와 묶음 제목, 이름 키(`tool/kind/name`), 카드에 보일 상태(managed·adoptable·not_adoptable)와 접힘 그룹(never·not_managed·repo_tracked), 채택 후보(원래 위치의 adoptable 중 live 항목과 이름이 겹치지 않는 것), 한 줄 상태 문장, 짧은 경로(루트 폴더 이름·프로젝트 이름 아래, 관리 항목은 `lado/…`), 루트 기준 경로(복원 보고·멈춘 채택), OS 이름, 심각도 순서·점·단어.
  3. `LadoSetupItemList.tsx`(새): 도구 카드 하나 — 루트 읽기 오류 줄, 종류 묶음(캡션 + 항목 사이 구분선), 행(이름, 상태 줄, mono 짧은 경로, 실패는 행 아래). 관리 항목은 전역 스위치(`useLadoSetupChange` binding: dry run 뒤 코드 실행 종류 켜기는 데스크톱 확인)와 메뉴(삭제 → 파괴 확인 대화상자 → `{op:"delete"}`), 승인된 작업에 닿는 변경은 영향 대화상자, 카드 아래 "관리 안 함 n" 접힘 그룹.
  4. `LadoSetupConflictsCard.tsx`(새): 차단 → 경고 → 정보(같은 심각도는 데몬 순서), 점 + 심각도 단어 + `ladoSetupConflictText`, 대상(도구 · 종류 · 이름, 항목이 없으면 짧은 경로), 수락됨 표시, "끄기"(Global on 인 항목만, binding off), "수락"(아직 수락 안 된 경고이고 항목이 있을 때, scope global). 편집 버튼은 그리지 않는다.
  5. `LadoSetupAdoptDialog.tsx`(새): 설정 대화상자 틀. 후보 전부 기본 동작으로 체크된 채 시작, "모든 항목" 체크(일부면 indeterminate), 행마다 Move/Keep 공유 select. 미리보기는 선택이 300 ms 멈춘 뒤(첫 미리보기는 즉시), 새 것을 기다리는 동안 이전 미리보기를 보인다. 본문: 경고 문단(옮길 것이 있을 때), 항목(옮길 곳 mono, keep 은 "제자리 + 사본 관리"), Codex 지침 짝·keep 경고(렌더러 판단), 다시 쓰는 명령(전·후 mono), 이 채택의 충돌(HOOK_PATH_MOVED 는 "채택을 막음" + needs-you 점, 그 밖의 blocking 은 caution 점에 "Blocks the run", 경고·정보 그대로), 실행 중 세션·프로그램과 "다시 확인", 프로그램을 안 찾는 데몬(랩) 메모, 그 밖의 경고. 확인 비활성 이유(데몬 거부 순서): 고른 것 없음 → 옮길 것 없음 → 확인 중(미리보기 오류는 본문에) → 다시 못 쓴 hook 경로(`LADO_SETUP_HOOK_PATH_MOVED` 오류 문장) → 미검증 OS → 실행 중. 확인 → `useAdoptLadoSetup`(데스크톱이 다시 미리 보고 서명, 취소면 그대로) → 결과(채택 수, 옮김·제자리 수, 검사 통과 또는 실패 검사마다 문장, 경고) 또는 partial(오류 문장 + 멈춘 단계 + 루트 기준 경로). 그 밖의 거부는 바닥 줄에 두고 선택을 바꾸면 지운다. 확인 중에는 미리보기를 끈다(무효화 뒤 헛 미리보기 방지).
  6. `LadoSetupAdoptionsCard.tsx`(새): 최근 것부터, 행 = 채택 시각(시계 시각, 전체는 title), "항목 n개 · 완료 / 일부만 채택됨 / 복원됨 시각"(partial 의 데몬 메시지는 title), 복원 안 된 채택에만 "복원"(`useRestoreLadoSetup`, 데스크톱 확인, 취소면 그대로) → 행 아래 보고(경로 mono·상태 문장·옆에 둔 사본 경로·실패 사유·경고). 실패는 행 아래.
  7. 훅 줄: `settingsCatalog.tsx` Rules 다음 `localSetup`(아이콘 FolderCog), `ui-store.ts` 섹션 id, `GlobalSettingsForm.test.tsx` 모의 하나(주석 한 줄 갱신) — diff-budget 16/40.
  8. 8개 로캘에 `lado.setup.{section,group,status,list,delete,impact,conflicts,adopt,check,stage,adoptions}.*` 90키(복수형 셋, es·fr·pt-BR 는 `_many` 를 더해 93) — 스크립트로 자리표시자 일치·한국어 자리표시자 바로 뒤 조사 없음·기존 키와 겹침 없음을 확인하고 기존 LADO 키 뒤에 붙였다.
  9. `DESIGN.md` 결정 기록에 2026-10-26 `LADO fork: Settings → Local setup …` 한 줄(섹션 구성, 채택 대화상자, 미검증 OS 표시, 복원).
  10. 테스트: 필수 파일 4개(LocalSetupSection 10, LadoSetupAdoptDialog 11, LadoSetupAdoptionsCard 5, LadoSetupConflictsCard 7) + `test/lado-setup-fixtures.ts`(인벤토리·충돌·미리보기 빌더, 모의 apiClient 에 경로별로 답하는 가짜 데몬 `routeDaemon`/`daemonCalls`). `apiClient` 만 모의해 S14 데이터 층(`useLadoSetupChange` 의 dry run → 확인 흐름 포함)을 실제로 지나게 했다.
- 엔트리와 다르게 한 것과 이유:
  1. `SettingsDialog.test.tsx` 의 LADO 테스트("Runs 가 Rules 바로 다음")를 "Rules, Local setup, Runs 순"으로 고쳤다 — 플랜 결정 p 의 순서라 그대로면 실패한다(엔트리 파일 목록 밖, diff-budget 대상 아님).
  2. `LadoRuleImpactDialog.tsx` 에 선택 prop `title` — setup 변경의 영향 확인이 "승인된 작업의 규칙을 바꿀까요?"로 읽히지 않게 `lado.setup.impact.title`("승인된 작업의 로컬 설정을 바꿀까요?"). 설명 문장은 같다.
  3. 공유 보기 함수는 `components/lado/setup/lado-setup-view.ts`, 테스트 빌더는 기존 fixture 폴더의 `test/lado-setup-fixtures.ts` 에 두었다(엔트리 파일 목록 밖).
  4. 저장소 추적 파일(`repo_tracked`)과 프로젝트의 비추적 파일은 도구 카드 본문이 아니라 접힘 그룹에 넣었다 — 프로젝트마다 늘어나는 읽기 전용 줄이고, 다룰 곳은 S16 의 프로젝트 규칙 페이지("From the repository" 카드)다. 상태 줄은 "저장소 파일".
  5. 대화상자 후보에서 live 항목과 이름이 같은 adoptable 줄을 뺐다 — 데몬 미리보기가 `already_managed` 로 요청 전체를 거부하므로. 목록에서는 그 줄을 "채택 불가 · LADO가 이미 같은 이름의 항목을 관리합니다."로 보인다.
  6. 검증된 OS 에서는 OS 줄을 그리지 않는다("조용히": 결정에 쓰이지 않는 문구는 없앤다, DESIGN Settings language).
  7. 채택 실패 가운데 partial(`details.state === "partial"`)만 결과 화면(멈춘 단계·경로, "완료")으로 바꾸고, 그 밖의 거부(설정이 바뀜, 실행 중, 기록 실패로 되돌림 등)는 선택 화면 바닥 줄에 두어 다시 시도할 수 있게 했다 — partial 은 다시 채택이 아니라 복원이 돌아가는 길이다.
  8. 랩 확인(렌더러만, lab 레시피의 "Renderer-only check"): dev-web + Playwright `route` 로 setup 경로만 가짜 응답해 라이트 테마 1280 px 로 섹션·접힘 그룹·채택 기록·대화상자를 봤다. 대화상자 본문이 `<fieldset>` 이라 스크롤되지 않고 바닥 버튼 아래로 넘치는 것을 찾아, 스크롤은 바깥 `div` 가, 비활성화는 안쪽 fieldset 이 맡게 고쳤다(고친 뒤 다시 봄). 콘솔 오류는 스텁 밖 경로의 404 뿐. dev-web 은 PID 로 멈췄고 `.playwright-mcp/` 는 스크래치로 옮긴 뒤 지웠다.
- 확인한 명령과 결과:
  - `npx vitest run` 필수 5파일 → 34 통과. 이웃 묶음(`src/renderer/i18n`, RulesSection, GlobalSettingsForm, SettingsDialog, `components/lado`, RunsSection, `main/tray-catalog`) → 21 files, 263 통과.
  - 변이 검사(모두 되돌림 확인): 다시 못 쓴 hook 경로가 막지 않게 → AdoptDialog 1 실패; 그 밖의 blocking 도 막게 → 1 실패; blocking 도 수락 가능하게 → ConflictsCard 1 실패; 복원된 채택에도 복원 버튼 → AdoptionsCard 1 실패; 전역 스위치가 늘 on 을 보냄 → LocalSetupSection 1 실패. debounce 를 없애는 변이는 통과한다(성능 성질 — 미리보기가 선택을 따르는지는 테스트가 잡는다).
  - `npm run typecheck` 7분 40초 깨끗(커밋 전), 게이트 G5(typecheck + typecheck:e2e) 7분 50초.
- 게이트: 코드 커밋 `f7ec168be` 에서 `gate-check.sh --run … --only` 로 G0~G6 각각 MET(G6 `BUDGET_OK 16/40`). 엔트리 커밋 뒤 G0~G7 을 다시 돌린 결과가 그 커밋의 증거다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 섹션(머리 줄·OS 줄·상태 넷), 충돌 카드, 도구별 항목(종류 묶음·상태·사유·전역 스위치·삭제), 채택 대화상자, 채택 기록과 복원, DESIGN.md 줄, 8개 로캘, 필수 테스트 파일 5개. 편집기·이력·변경 감지 검토·프로젝트·태스크 화면은 없다(S16). 엔트리 밖은 After 의 "다르게 한 것" 1·2·3·8.
- 남은 틈:
  - 실제 데스크톱 앱에서 데몬과 함께 채택·복원·확인 창을 끝까지 돌려 보지 않았다(실제 도구 폴더 금지, 랩은 렌더러 + 가짜 응답). 메인 세션의 랩(격리 데몬 + 임시 HOME)으로 확인할 것.
  - 다크 테마·좁은 폭·한국어 화면은 보지 않았다(라이트 1280 px, 영어).
  - 수락한 경고의 취소(revoke)는 화면에 없다(엔트리 동작 목록 밖) — 지금은 API 로만 되돌린다.
  - 충돌 카드는 Global 범위뿐이다(프로젝트·태스크 범위는 S16 화면 몫).
  - drift 를 따로 보이지 않는다: new_local 은 원래 위치의 adoptable 줄로 보이고 채택할 수 있으며, edited 는 S16 의 변경 감지 검토 몫.
  - 프로젝트 검사 오류(`projects[].error`)는 보이지 않는다 — 그 프로젝트의 저장소 파일 줄이 빠질 수 있다.
  - 관리 항목의 짧은 경로에 실제 폴더 이름 `lado/harness/…` 가 그대로 보인다(디스크 경로라 바꾸지 않았다).
  - 미리보기는 선택이 바뀔 때마다(300 ms 멈춘 뒤) 데몬 전체 검사를 다시 돈다 — 프로젝트가 많으면 느릴 수 있고, S14 의 증명 수명 60 s 틈도 그대로다.
- 다음 단계 메모:
  - **S16(메뉴·충돌 카드)**: 행 메뉴는 `LadoSetupItemList.tsx` 의 `EntryRow` 안 `DropdownMenuContent` — 편집·이력·변경 감지 동작을 삭제 위에 더하고 삭제 앞에 `DropdownMenuSeparator`(지금은 삭제 하나라 없음). 행의 항목 id 는 인벤토리 `entries[].itemId`, Global 상태는 `items[]`. 충돌 카드의 "편집"은 `LadoSetupConflictsCard.tsx` 동작 칸(`canTurnOff`·`canAccept` 옆)에 `conflict.itemId` 로 편집기를 여는 버튼을 더하면 되고, 수락 취소를 더하려면 `acceptanceId` 가 있는 행에 `{op:"revoke", acceptanceId}`. 영향 확인은 `LadoRuleImpactDialog` 에 `title={t("lado.setup.impact.title")}`.
  - **S16(재사용)**: `lado-setup-view.ts` 의 `ladoSetupShortPath`·`ladoSetupRootedPath`·`ladoSetupStatusText`·`ladoSetupConflictsBySeverity`·`ladoSetupSeverityDotClass`·`ladoSetupSeverityKeys`·`ladoSetupGroupLabel`, 테스트는 `test/lado-setup-fixtures.ts`(`setupInventory`·`setupEntry`·`setupItem`·`setupConflict`·`setupChangeResult`, `routeDaemon`·`daemonCalls`; `vi.mock` 은 `api-client` 원본을 펼치고 `apiClient` 만 바꾼다). 프로젝트 규칙 페이지의 "From the repository" 카드는 인벤토리 `entries` 중 `location === "project"` 이고 `projectId` 가 그 프로젝트인 줄. 문자열은 이 단계가 `lado.setup.{section,group,status,list,delete,impact,conflicts,adopt,check,stage,adoptions}.*` 를 썼으니 S16 은 `lado.setup.editor.*`·`history.*`·`drift.*`·`project.*`·`task.*` 처럼 겹치지 않게.
  - **S17(문서)**: 설정 순서 Rules → Local setup → Runs, 채택 대화상자는 다시 못 쓴 hook 경로만 막음(그 밖의 blocking 은 caution 톤), partial 은 결과 화면에서 복원으로 안내, OS 줄은 미검증 OS 에서만, 위 남은 틈(실기 랩 미확인, revoke 화면 없음).
