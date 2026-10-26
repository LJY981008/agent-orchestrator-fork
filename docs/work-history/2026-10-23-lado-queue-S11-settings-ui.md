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

- HEAD `f259ff13f`(S10 기록). 트리 깨끗함.
- `components/settings/RunsSection.tsx`·`components/lado/LadoProjectSection.tsx` 없음. `settingsCatalog.tsx` 항목 14개(규칙 다음은 billing). 프로젝트 설정의 LADO 쪽 페이지는 LADO 모드 프로젝트의 "규칙" 하나뿐이고, LADO 모드를 켜고 끄는 화면이 앱에 없었다(S06 이 `AO_LADO_DEV` 를 지운 뒤로는 API 로만).
- S10 이 남긴 데이터 층: `useLadoDispatchSettings`·`useUpdateLadoDispatchSettings`·`useSetLadoMode`·`useUpdateLadoProjectSettings`, `setLadoMode`(끄기는 브리지 확인, 취소면 null), 오류 코드 문구(`LADO_TRACKER_INTAKE_ENABLED`·`LADO_DISPATCH_SETTINGS_INVALID` 등). 프로젝트 설정 쿼리는 `useLadoModeState` 안에만 있어 `maxConcurrent`·오류 상태를 읽을 훅이 없었다.
- `DESIGN.md` 결정 로그에 `LADO fork: Runs settings and project LADO section` 행 없음(마지막 행은 2026-10-22 프로젝트 규칙).
- 기준 테스트: S10 기록대로 프런트엔드 전체 516 파일, 8581 통과·7 skip.

## After — 수행 후 실측

- 코드 커밋 `944fb722a` `feat(lado): runs settings and the project LADO section` — 19 파일, +804 −21.
  - `components/settings/RunsSection.tsx`(새): 설정 → 실행. 제목 아래 muted 한 줄(LADO 모드 프로젝트의 승인한 작업에만 적용 — 섹션 `toolbar`, 카드까지 12px) 뒤 카드 하나에 "동시 실행"(공유 settings select 1-8), "최대 실행 시간"(끔·1·2·4·8·12·24시간 = 0·60·120·240·480·720·1440분, 시간 문구는 복수형 키 `lado.runs.hours`), "메모리가 부족하면 기다리기"(스위치 + 설명 한 줄). 바꾸면 저장된 값 셋에 그 값을 넣어 `useUpdateLadoDispatchSettings().mutate` — 세 값 함께. 저장 중에는 세 컨트롤 모두 비활성이고 고른 값이 보이며, 실패하면 저장된 값으로 돌아가고 바꾼 행 아래에 `ladoErrorText`(role=alert). 같은 값을 고르면 아무것도 보내지 않는다. 읽기 중 "Loading…", 읽기 실패는 오류 문구. 1-8 선택지는 `ladoConcurrentRunOptions` 로 내보내 프로젝트 쪽이 같이 쓴다.
  - `components/lado/LadoProjectSection.tsx`(새): 프로젝트 설정 → LADO. 제목 없는 카드 하나(다른 프로젝트 페이지처럼 `titleHidden`)에 "LADO 모드" 스위치(설명: 켜면 이 프로젝트의 워커는 승인한 계획으로만 시작)와 "이 프로젝트의 동시 실행"(1-8). 켜기는 `useSetLadoMode(id).mutate(true)` → 직접 PUT, 끄기는 `mutate(false)` → 브리지 확인. 스위치는 데몬이 가진 값만 보인다 — 확인을 묻는 동안·취소 뒤에도 켜진 채(비활성은 묻는 동안만). 상한은 모드가 꺼져 있어도 바뀌고, 저장 중 고른 값이 보인다. 둘 중 하나가 저장 중이면 둘 다 비활성, 한쪽을 바꾸면 다른 쪽의 지난 오류를 지운다.
  - `settingsCatalog.tsx`: 규칙 다음에 `runs`(아이콘 `Gauge`, 라벨 `lado.runs.title`). `SettingsDialog.tsx`: 로컬 프로젝트(데몬이 아는 로컬, 클라우드·원격 아님)면 모드와 상관없이 `lado` 페이지(아이콘 `ShieldCheck`)를 규칙 앞에 넣고, 본문은 `LadoProjectRulesSection` 과 같은 자리·같은 조건으로 렌더. `ui-store.ts`: `GlobalSettingsSection` 에 `"runs"`, `ProjectSettingsSection` 에 `"lado"`(타입 등록에 필요해 목록 밖 파일을 고침).
  - `hooks/useLado.ts`: `useLadoProjectSettings(projectId)` 를 더하고 `useLadoModeState` 가 그것을 쓴다(같은 키·옵션 — 페이지가 `maxConcurrent` 와 읽기 오류를 봐야 해서; 목록 밖 파일).
  - 로캘 8개 × 14키(`lado.runs.*` 9, `lado.project.*` 5; es·fr·pt-BR 은 `lado.runs.hours_many` 1개 더). 한국어: 실행·동시 실행·최대 실행 시간·끔·메모리가 부족하면 기다리기·LADO 모드·이 프로젝트의 동시 실행. 조사 앞 변수 없음(`{{count}}시간`, `{{min}}~{{max}} 사이에서`).
  - 테스트: `RunsSection.test.tsx` 7개(기본값 표시, 선택지 1-8·끔~24시간, 세 컨트롤 변경마다 세 값 함께 저장, 같은 값은 저장 안 함, 바꾼 행 아래 오류와 저장값 복귀, 저장 중 비활성과 고른 값 표시, 읽기 실패), `LadoProjectSection.test.tsx` 8개(표시, 켜기 직접 PUT·브리지 호출 없음, 끄기 브리지·취소 시 켜진 채·PUT 없음, 묻는 동안 켜진 채 비활성, intake 오류 문구, 상한 저장(모드 꺼짐), 상한 거절 문구, 설정 대화상자 등록 — 로컬은 모드와 상관없이 LADO·켜면 규칙이 뒤따름·원격·클라우드는 없음), `SettingsDialog.test.tsx` +1(실행이 규칙 바로 다음, 그 페이지가 열림). `GlobalSettingsForm.test.tsx` 는 RulesSection 처럼 RunsSection 을 스텁(전체 보기 페이지가 데몬을 읽어 오류 alert 가 하나 더 생겨 기존 2개가 깨졌음).
  - `DESIGN.md` 결정 로그 끝에 `| 2026-10-23 | LADO fork: Runs settings and project LADO section … |` 한 행(실행 섹션 구성, 프로젝트 LADO 섹션과 끌 때 데스크톱 확인·취소면 켜진 채, 상한 1-8 의 이유 — 로컬 한 사람 기준).
- 엔트리가 정하지 않았거나 다르게 한 것:
  1. 목록 밖 파일 3개: `hooks/useLado.ts`(훅 1개), `stores/ui-store.ts`(섹션 id 2개), `GlobalSettingsForm.test.tsx`(스텁 3줄).
  2. 스위치와 선택의 표시 규칙을 나눴다. LADO 모드 스위치는 데몬 값만(확인 대화상자가 떠 있는 동안 꺼진 것처럼 보이면 거짓 상태), 실행 설정·상한은 저장 중 고른 값(되돌릴 수 있는 선호이고, 안 그러면 선택이 저장될 때까지 옛 값으로 튄다).
  3. 상한 행의 거절 문구: 데몬은 프로젝트 상한도 `LADO_DISPATCH_SETTINGS_INVALID`(details `{min, max}`)로 거절하는데, S10 문구는 "최대 실행 시간은 목록에서 고르세요" 까지 말한다. 이 페이지에는 실행 시간이 없어 상한 행만 새 키 `lado.project.maxConcurrentInvalid`("동시 실행은 {{min}}~{{max}} 사이에서 고르세요.")를 쓴다(리뷰 반영). 선택지가 데몬 범위와 같아 실제로는 닿기 어렵다.
  4. 설명 문장은 엔트리의 "이 값은 … 적용된다" 를 행 설명이 아니라 섹션 머리 한 줄로 뒀다(세 값 모두에 걸리므로).
- 확인:
  - 필수 테스트 2파일 15개 통과(7 + 8), `SettingsDialog.test.tsx`·`GlobalSettingsForm.test.tsx`·`CloudProjectSettingsForm.test.tsx`·`shell-new-session-shortcut.test.tsx` 197개 통과, i18n `instance`·`renderer-coverage` 통과.
  - 프런트엔드 전체 `npx vitest run`: 518 파일, 8598 통과·7 skip(리뷰 반영 전; 반영 뒤 관련 4파일 36개 다시 통과). `npm run typecheck` 6분 36초 통과(리뷰 반영 전; 반영 뒤는 G5 가 다시 확인). `go build ./... && go vet ./...` 통과(백엔드 변경 없음).
  - 변이 7개 모두 테스트가 잡음: 세 값 대신 바꾼 값만 보냄, 저장 중 스위치 활성, 오류를 모든 행에 표시, 저장 중 고른 값 대신 옛 값 표시, 확인 중 스위치를 미리 뒤집음, 모드 변경을 상한 PUT 으로 보냄, 저장 중 상한 선택 활성.
  - 격리 랩(스크래치 데이터의 `ao daemon` + dev-web + Playwright, 실제 `~/.ao` 안 건드림): 실행 페이지(라이트), 선택 메뉴, "12시간" 저장 → 데몬 `{"maxConcurrent":2,"maxRunMinutes":720,"holdOnMemoryTight":true}`; 프로젝트 LADO 페이지 꺼짐 → 켜기(규칙 메뉴가 생김) → 상한 3(데몬 `{"ladoMode":true,"maxConcurrent":3}`) → 끄기(dev presence, 데몬 false, 규칙 메뉴 사라짐); 한국어·다크 실행 페이지. 스크린숏은 세션 스크래치에만 두고 워크트리에 남기지 않았다. Electron 의 네이티브 확인 대화상자 자체는 랩에서 보지 않았다(dev-web 에는 브리지가 없음 — 테스트와 S10 메인 테스트 몫).
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker 없음. should-fix 1 — 상한 거절 문구가 실행 시간을 말함 → 위 3으로 고침. nit 2 — de·fr 의 "켜면" 문장이 파일의 기존 꼴("Wenn aktiviert", "Quand … est activé")과 다름 → 반영. nit 3 — ja·ko·zh-CN 의 `lado.runs.hours_one` 은 쓰이지 않는 키 → 그대로 둠(`instance.test` 가 영어 키 전부를 모든 로캘에 요구하고, `lado.rules.ao.chars_one` 이 같은 선례).
- 게이트(HEAD `944fb722a`): 7건 모두 MET(7분 47초). 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 훅 1개(`useLadoProjectSettings`), 섹션 id 2개, 상한 행 전용 거절 문구 1키, 같은 값 무시, 설정 대화상자 등록 테스트. 엔트리와 다르게 한 것은 설명 문장 자리(After 결정 4) 하나.
- 남은 틈(기록만):
  - `LADO_DISPATCH_SETTINGS_INVALID` 를 두 경로가 함께 쓴다. 렌더러는 화면 자리로 문구를 가른다. 데몬이 프로젝트 상한에 따로 코드를 주면 `LadoProjectSection` 의 분기를 지우고 `lado-errors.ts` 맵에 넣는다.
  - 설정 "전체 보기"(검색 등 `section: "all"`)도 이제 디스패치 설정을 읽는다. 데몬이 없으면 그 자리에 오류 한 줄이 생긴다(규칙 섹션과 같음).
  - 실제 Electron 앱의 끄기 확인 대화상자를 이 단계에서 눈으로 보지 않았다(브리지 계약과 메인 쪽은 S10 테스트가 덮음).
- 다음 단계 메모:
  - S12: `DESIGN.md` 결정 로그의 마지막 행이 이 단계 행이다 — 그 아래에 S12 행을 더한다. 대기 이유 `lado_mode_off` 카드에서 설정으로 보내려면 `useUiStore.getState().openProjectSettings(projectId, { section: "lado" })` 가 LADO 페이지를 연다(링크를 둘지는 S12 판단). 프로젝트 설정 전체가 필요하면 `useLadoProjectSettings(projectId)`(모드·상한·읽기 상태). `GlobalSettingsForm.test.tsx` 는 Rules·Runs 를 스텁한다 — 전체 보기 페이지에 LADO 컴포넌트를 더하면 같이 스텁할 것.
  - S12 랩: 데몬은 `<빌드한 ao> daemon`(숨은 하위 명령, 전경 실행)에 `AO_LADO_DEV_PRESENCE=1`(브라우저에서 끄기·승인); `AO_LADO_DEV` 는 사라져 필요 없고 LADO 모드는 이제 프로젝트 설정 → LADO 에서 켠다. 프로젝트 등록은 `POST /api/v1/projects {"path","projectId":"ao-demo","name"}`. Playwright MCP 의 `.playwright-mcp/` 는 이 워크트리 루트에 생기고 gitignore 되지 않는다 — tree-clean 게이트 전에 지울 것.
  - S13: TDD·LADO 문서에 설정 → 실행(세 값, 선택지 1-8·분 목록, 함께 저장)과 프로젝트 설정 → LADO(모드 스위치가 `AO_LADO_DEV` 를 대신함, 끄기는 데스크톱 확인, 상한은 모드와 무관)를 적는다.
