# 2026-10-23 LADO 대기열·디스패처 — S11 설정 → 실행, 프로젝트 설정 → LADO

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 f·g·l·m, §10 첫 두 항목과 DESIGN.md 행) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

소유자가 화면에서 동시 실행 상한·최대 실행 시간·메모리 대기를 정하고(2026-10-23 결정 "화면으로"), 프로젝트마다 LADO 모드를 켜고 끄며 프로젝트당 상한을 정하게 한다. LADO 모드 스위치가 앱에 처음 생긴다.

## 범위

- 만든다: `frontend/src/renderer/components/settings/RunsSection.tsx`(+ 테스트), `frontend/src/renderer/components/lado/LadoProjectSection.tsx`(+ 테스트).
- 고친다: `components/settings/settingsCatalog.tsx`(규칙 다음에 "실행" 섹션 등록), `components/SettingsDialog.tsx`(프로젝트 설정에 LADO 섹션 — `LadoProjectRulesSection` 을 등록한 자리와 같은 방식, 로컬 프로젝트만), 8개 로캘 파일, `DESIGN.md`(결정 로그 끝에 한 행).
- 하지 않는다: 보드·태스크 뷰·세션 패널(S12).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. **`DESIGN.md` 를 먼저 읽는다** — 설정 모달 구성, 행 패턴, 스위치·선택 컴포넌트, sentence case, 공유 목록 규칙. `RulesSection.tsx`·`LadoProjectRulesSection.tsx`·`GeneralSettingsSection.tsx` 의 행 꼴(`settings-grouped-rows`, `settings-row-bar` 등)을 그대로 쓴다. shadcn 프리미티브(`components/ui/*`)를 쓴다.
2. 설정 → **실행**(en "Runs"): 카드 하나에 세 행 — "동시 실행"(1-8 선택), "최대 실행 시간"(끔·1·2·4·8·12·24시간 선택; 값은 분), "메모리가 부족하면 기다리기"(스위치, 설명 한 줄). 바꾸면 바로 `updateLadoDispatchSettings` 로 저장(세 값 함께), 실패하면 행 아래에 데몬 문구(`ladoErrorText`), 저장 중에는 컨트롤 비활성. 설명 문장: 이 값은 LADO 모드 프로젝트의 승인된 작업에만 적용된다.
3. 프로젝트 설정 → **LADO**: "LADO 모드" 스위치 — 켜기는 바로 `setLadoMode(id, true)`(트래커 intake 가 켜져 있으면 데몬 오류 문구를 보여 줌), 끄기는 `setLadoMode(id, false)` → 데스크톱 확인이 취소되면 스위치가 다시 켜진 상태로. "이 프로젝트의 동시 실행"(1-8 선택) → `updateLadoProjectSettings`. 모드가 꺼져 있을 때도 상한은 바꿀 수 있다. 설명: 켜면 이 프로젝트의 워커는 승인한 계획으로만 시작한다.
4. 문구는 8개 로캘에 실제 번역으로, 한국어는 용어집(실행, 동시 실행, 최대 실행 시간, LADO 모드). 조사 바로 앞에 변수를 두지 않는다.
5. `DESIGN.md` 결정 로그 끝에 한 행: `| 2026-10-23 | LADO fork: Runs settings and project LADO section | ... |` — 실행 섹션 구성, 프로젝트 LADO 섹션(모드 스위치는 끌 때 데스크톱 확인, 취소하면 켜진 채), 상한 선택이 1-8 인 이유(로컬 한 사람 기준).
6. 커밋 메시지 예: `feat(lado): runs settings and the project LADO section`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S11-settings-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/components/settings/RunsSection.test.tsx` — 기본값 표시, 각 컨트롤 변경이 세 값을 함께 저장, 오류 문구 표시, 저장 중 비활성
- `src/renderer/components/lado/LadoProjectSection.test.tsx` — 켜기 직접 호출, 끄기 브리지 확인·취소 시 켜진 채, intake 오류 문구, 상한 저장
- `src/renderer/components/SettingsDialog.test.tsx` 또는 settingsCatalog 테스트 — 실행 섹션이 규칙 다음에 보인다(기존 테스트 파일에 사례를 더한다; 없으면 새 파일)

## 수용 게이트

- [ ] S11-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S11-G1: 새 화면 테스트 2개 파일 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/components/settings/RunsSection.test.tsx src/renderer/components/lado/LadoProjectSection.test.tsx
  EXPECT: VITEST_OK
- [ ] S11-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S11-G3: DESIGN.md 결정 로그 행
  CHECK: bash .claude/gate/lado-queue.sh has DESIGN.md 'LADO fork: Runs settings and project LADO section'
  EXPECT: HAS_OK
- [ ] S11-G4: 렌더러 전체 테스트(설정·LADO 묶음)
  CHECK: cd frontend && npx vitest run src/renderer/components/settings src/renderer/components/lado src/renderer/lib && echo S11_VITEST_OK
  EXPECT: S11_VITEST_OK
- [ ] S11-G5: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S11-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
