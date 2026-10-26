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
- `src/renderer/components/lado/setup/LadoSetupAdoptDialog.test.tsx` — 항목·동작·다시 쓰기·경고·전제 조건 표시, 실행 중 목록이 있으면 확인 비활성, blocking 충돌이면 비활성, 확인이 `adoptSetup` 을 정확한 항목으로 부름
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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫, DESIGN.md 에서 따른 규칙)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S16 이 메뉴·충돌 카드에 이을 자리)
