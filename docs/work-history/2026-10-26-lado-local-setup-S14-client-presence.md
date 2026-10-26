# 2026-10-26 LADO 로컬 설정 채택 — S14 렌더러 데이터 층·오류·이벤트·데스크톱 확인 채널

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§14, §15 첫 항목) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

화면(S15·S16)이 쓸 데이터 층과, 채택·복원·코드 실행 변경을 데스크톱 앱의 메인 프로세스가 확인하고 서명하는 채널을 만든다. 렌더러는 무엇을 바꿀지만 말하고, 메인이 데몬에서 dry run 으로 대상을 다시 읽어 자기 카탈로그의 문장으로 묻는다(ADR-0002 의 꼴, 5·6단계의 채널과 같음).

## 범위

- 만든다: `frontend/src/renderer/lib/lado-setup.ts`(+ `lado-setup.test.ts`), `frontend/src/renderer/lib/lado-events.test.ts`(새 테스트 파일), `useLado.ts` 또는 새 `hooks/useLadoSetup.ts` 의 훅.
- 바꾼다: `lib/lado-errors.ts`(+ 테스트, 새 코드와 details 키), `lib/lado-events.ts`(`lado_setup`), `frontend/src/shared/lado-presence.ts`(채널·결과 타입), `frontend/src/main/lado-presence.ts`(+ 테스트, 세 채널), `frontend/src/main.ts`(등록 한 줄), `frontend/src/preload.ts`, 브리지 3곳(`renderer/lib/bridge.ts`, `renderer/test/setup.ts`, `e2e/support/fake-bridge.ts`), `scripts/sync-tray-catalog.mjs` 의 KEYS 와 재생성한 `src/main/tray-catalog.generated.ts`, 8개 로캘의 `lado.presence.setup*` 키.
- 하지 않는다: 화면 컴포넌트(S15·S16).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S13 의 operation id·schema 이름을 본다.
1. `lib/lado-setup.ts`(꼴은 `lib/lado-rules.ts`·`lib/lado.ts`): `schema.ts` 타입 별칭, 질의 키 `["lado","setup",…]`(인벤토리, 충돌(범위별), 항목, 채택), 클라이언트 함수(오류는 `ladoError`), 변경 흐름 `useLadoSetupChange` — dry run → 결과의 `needsPresence` 면 브리지 `lado.presence.changeSetup(request)`(브리지 없으면 dev presence 를 바라고 API 직접), 아니면 바로 적용 → 무효화(인벤토리, 충돌, effective, 태스크 목록). 채택·복원도 같은 꼴(`adoptSetup`, `restoreSetup` — 늘 확인).
2. `lado-errors.ts`: S13 의 `LADO_SETUP_*` 코드마다 번역 키, `detailValues` 에 새 details 키(`tool`, `kind`, `name`, `stage`, `item`, `sessions`, `processes`) — 목록 값은 읽기 좋게 이어 붙임. 인벤토리 사유 코드·상태·충돌 코드의 번역 함수(`ladoSetupReasonText`, `ladoSetupConflictText`).
3. `lado-events.ts`: `lado_setup` → 위 질의들과 그 프로젝트의 태스크·effective 무효화(`lado_rules` 처리 꼴).
4. 메인(`main/lado-presence.ts`, 플랜 §14):
   - `lado:presence:adoptSetup(entries)`: 입력 검사(배열, 항목마다 tool·kind·name·action 문자열, 최대 500개) → 데몬에 dry run → 응답 엄격 파싱(planSha256 64 hex, 도구별 개수, blocking 충돌 수) → 다시 쓰지 못한 hook 경로(`LADO_SETUP_HOOK_PATH_MOVED`)가 있으면 묻지 않고 거부(데몬도 409 로 거부); 그 밖의 blocking 충돌은 막지 않고 확인 창에 개수로 적는다(S07 After 8: 제자리에 둬도 같은 충돌이고, 채택해야 앱에서 끄거나 고칠 수 있다) → 확인 대화상자 → 새 증명 → `POST /lado/setup/adoptions {entries, planSha256}`.
   - `lado:presence:restoreSetup(adoptionId)`: id 검사 → `GET /lado/setup/adoptions/{id}` → "원래 위치로 <n>개 복원?" → 서명.
   - `lado:presence:changeSetup(request)`: 허용 경로·메서드 목록(`POST /lado/setup/items`, `PATCH /lado/setup/items/{id}`, `PUT /lado/setup/bindings`, `POST /lado/setup/items/{id}/drift`)과 본문 필드 검사 → 같은 요청을 `dryRun:true` 로 → 응답의 `changeSha256`·항목 이름·종류 → "이 변경은 명령을 실행하거나 접근을 넓힐 수 있습니다" 확인 → 같은 요청(`dryRun` 없이)에 서명.
   - 문장은 메인 카탈로그 키(기존 `approveTitle`/`approveDetail`/`approve` 꼴): `lado.presence.setupAdoptTitle`, `lado.presence.setupAdoptDetail`, `lado.presence.setupAdopt`, `lado.presence.setupRestoreTitle`, `lado.presence.setupRestoreDetail`, `lado.presence.setupRestore`, `lado.presence.setupChangeTitle`, `lado.presence.setupChangeDetail` (변경 버튼은 기존 `lado.presence.change`, 취소는 `lado.presence.cancel`). 앱 언어. 8개 로캘에 진짜 번역.
5. preload·브리지 3곳·`main.ts` 등록: 기존 채널(`approvePlan`, `setLadoMode`)과 같은 꼴.
6. `node scripts/sync-tray-catalog.mjs` 로 생성 파일 갱신(손으로 고치지 않음).
7. 커밋 메시지 예: `feat(lado): renderer data and desktop confirmation for local setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S14-client-presence.md` 로 전건 MET 를 확인한다. typecheck 가 7분쯤 걸리므로 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `src/renderer/lib/lado-setup.test.ts` — dry run 뒤 확인이 필요하면 브리지 채널, 아니면 바로 적용; 무효화 키; 오류 변환
- `src/renderer/lib/lado-events.test.ts` — `lado_setup` 이 인벤토리·충돌·effective·태스크를 무효화, 다른 kind 는 건드리지 않음
- `src/renderer/lib/lado-errors.test.ts` — 새 코드와 details(목록 값 포함)의 문장, 모르는 코드는 데몬 문장
- `src/main/lado-presence.test.ts` — 세 채널: 잘못된 입력 거부, 대상 다시 읽기, 다시 못 쓴 hook 경로가 있으면 묻지 않음(다른 blocking 충돌은 창에 개수), 취소면 서명 안 함, 서명한 본문이 정확히 그 요청

## 수용 게이트

- [ ] S14-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S14-G1: 필수 테스트 파일 4개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/lib/lado-setup.test.ts src/renderer/lib/lado-events.test.ts src/renderer/lib/lado-errors.test.ts src/main/lado-presence.test.ts
  EXPECT: VITEST_OK
- [ ] S14-G2: 데스크톱 확인 문장 키가 카탈로그 목록과 8개 로캘에 모두 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has frontend/scripts/sync-tray-catalog.mjs '"lado.presence.setupAdoptTitle"' '"lado.presence.setupRestoreTitle"' '"lado.presence.setupChangeTitle"' && bash .claude/gate/lado-local-setup.sh locales lado.presence.setupAdoptTitle lado.presence.setupAdoptDetail lado.presence.setupAdopt lado.presence.setupRestoreTitle lado.presence.setupRestoreDetail lado.presence.setupRestore lado.presence.setupChangeTitle lado.presence.setupChangeDetail
  EXPECT: LOCALES_OK
- [ ] S14-G3: 세 채널이 preload 와 공유 타입에 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has frontend/src/shared/lado-presence.ts 'lado:presence:adoptSetup' 'lado:presence:restoreSetup' 'lado:presence:changeSetup'
  EXPECT: HAS_OK
- [ ] S14-G4: 프런트 typecheck
  CHECK: bash .claude/gate/lado-local-setup.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S14-G5: 업스트림 프런트 수정은 작다
  CHECK: bash .claude/gate/lado-local-setup.sh diff-budget 60 frontend/src/main.ts frontend/src/preload.ts frontend/src/renderer/lib/bridge.ts frontend/src/renderer/test/setup.ts frontend/e2e/support/fake-bridge.ts frontend/src/renderer/lib/event-transport.ts
  EXPECT: BUDGET_OK
- [ ] S14-G6: 전체 빌드·vet(백엔드가 그대로)
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S14-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `f9fda693c`(docs(lado): record S13), 트리 깨끗, 브랜치 `feat/lado-local-setup`. Node v24.21.0, vitest 4.1.8. `diff-budget` 대상 6파일은 태그 이후 0/60, S14-G0 `DEFS_UNCHANGED`.
- 출발점: 렌더러에 setup 데이터 층·훅·번역이 없고, 메인 확인 채널은 셋(`approvePlan`, `changeRule`, `setLadoMode`)뿐. S13 의 `npm run api` 로 `LadoEffectiveResponse.setup`, `approved.setup*`/`aoSystemSha256`/`accepted`, 실행 스냅숏 `setup` 이 필수 필드가 돼 기존 테스트 fixture 6곳이 `npm run typecheck` 를 깨고 있었다(S13 은 프런트 typecheck 를 돌리지 않음).
- 앞 단계 메모 중 이 단계 몫: S04(CDC payload `{"kind":"lado_setup","projectId"}`), S05(사유 `symlink`·`unreadable`), S06(충돌 13 코드·details 키), S07(코드 7개·`not_found`·`already_managed`·details `stage`/`sessions`/`processes`), S08(복원 코드·상태 4개), S09·S10(`HOME_FAILED`·`CODEX_FAILED` 의 `stage`), S11(`LADO_SETUP_LOAD_FAILED`), S12(dry run → 확인 → 적용 흐름, 변경 reason 들, 경고 2개), S13(operation id·schema 이름, 메인 채널 모양, 오류 코드·details 키·이벤트 무효화 대상).

## After — 수행 후 실측

- 커밋: `b471a9e12 feat(lado): renderer data and desktop confirmation for local setup`, 그리고 이 엔트리 커밋.
- 바꾼 것:
  1. `renderer/lib/lado-setup.ts`(새): `schema.ts` 타입 별칭, 질의 키 `ladoSetupQueryRoot ["lado","setup"]` 아래 `inventory`, `items/<projectId>/<taskId>`, `conflicts/<projectId>/<taskId>`, `item/<id>`, `adoption/<id>`, `adoption-preview/<entries>`; 클라이언트 함수 15개(오류는 `ladoError`); `previewLadoSetupAdoption`, `adoptLadoSetup(entries, planSha256)`·`restoreLadoSetup(id)`(브리지 → 없으면 API 직접, 취소면 null); 변경 요청 합집합 `LadoSetupChangeRequest`(`op`: create·edit·delete·binding·drift·accept·revoke), `previewLadoSetupChange`, `ladoSetupPresenceRequest`(메인이 받는 `{method, path, body}`, 확인이 필요할 수 있는 넷만), `useLadoSetupChange`(dry run → 영향이 승인된 태스크에 닿으면 `confirm` 대기 → `needsPresence` 면 브리지 `changeSetup`, 없으면 API 직접 → 무효화 `["lado","setup"]`·effective·tasks·task), `invalidateLadoSetupQueries`.
  2. `renderer/hooks/useLadoSetup.ts`(새): `useLadoSetupInventory`, `useScanLadoSetup`(결과를 인벤토리 캐시에), `useLadoSetupItems(scope)`, `useLadoSetupConflicts(scope)`, `useLadoSetupItem`, `useLadoSetupAdoption`, `useLadoSetupAdoptionPreview(entries)`(창 포커스 재조회 끔), `useAdoptLadoSetup`·`useRestoreLadoSetup`(성공·실패 모두 무효화 — 부분 채택·부분 복원도 바뀐 것이 있음).
  3. `lado-errors.ts`: `LADO_SETUP_*` 24코드 + 메인의 `LADO_SETUP_ADOPTION_ID_INVALID` + `INVALID_JSON` 번역; 단계·상태별 문장(채택 partial·record, 복원 manifest·record); `detailValues(t, code, details)` 에 `tool`(제품 이름), `kind`(번역), `name`, `stage`, `item`, `key`, `path`, `sessions`(`id (도구)`), `processes`(`이름 (PID n)`), `running`(둘을 한 줄로), `tools` — setup 코드의 `reason` 은 번역 문장; 결과는 `trim()`(사유 없는 문장의 끝 공백). 새 함수 `ladoSetupKindLabel`, `ladoSetupReasonText(t, reason, details)`(인벤토리 사유 17 + 채택 2 + 변경 12), `ladoSetupConflictText`(12 코드, 플러그인이면 "From the plugin …" 덧붙임, Claude 의 `bypassPermissions` 등은 AO 모드 이름), `ladoSetupRestoreStatusText`, `ladoSetupWarningText`. `ladoPolicyProblemText` 는 setup 충돌 코드를 `ladoSetupConflictText` 로 넘긴다(effective 의 problems 에 setup 충돌이 섞여 오므로).
  4. `lado-events.ts`: `lado_setup` → `["lado","setup"]` 전체, 프로젝트가 있으면 열린 태스크 화면, 프로젝트마다 effective·태스크 목록(`lado_rules` 꼴).
  5. `shared/lado-presence.ts`: 채널 셋과 요청·결과 타입. `main/lado-presence.ts`: 채널 등록을 `handle(channel, run, failed)` 하나로 묶고 셋 추가.
     - `adoptSetup`: 항목 검사(1~500개, tool·kind·action 은 목록 값, name 1~1024자, 그 밖의 필드는 버림) → `POST /lado/setup/adoptions {entries, dryRun:true}`(서명 없음) → 엄격 파싱(planSha256 64 hex, 미리보기 항목이 요청 항목과 정확히 같고 동작도 같음, 중복 없음, 전제 조건 모양) → 데몬 거부 순서대로 묻지 않고 거부: 옮길 것 없음(`ADOPTION_INVALID`), 다시 못 쓴 hook 경로(`HOOK_PATH_MOVED`, 그 충돌의 details), 미검증 OS(`OS_UNVERIFIED {os, tools}`), 실행 중 도구(`TOOL_RUNNING {sessions, processes}`) → 확인 창(도구별 옮길 수, 제자리 수, plan sha 앞 12자, 그 밖의 blocking 충돌 수) → `{entries, planSha256}` 서명.
     - `restoreSetup`: id 검사 → `GET …/adoptions/{id}` → 이미 복원(`ALREADY_RESTORED`)·백업 못 읽음(`RESTORE_FAILED stage manifest`)이면 묻지 않음 → "채택 ID·되돌릴 항목 수" 확인 → 빈 본문 `POST …/restore` 서명.
     - `changeSetup`: 허용 경로·메서드 넷과 각 본문 필드만(모르는 필드·`dryRun` 은 거부), 바인딩은 켜기만(Global `on`, 프로젝트·태스크 `force_on`), drift 는 keep 만 → 같은 본문 + `dryRun:true` → 응답의 op·applied=false·changeSha256·항목(만들기는 tool·kind·name, 그 밖은 id 가 요청과 같아야 함) → `needsPresence` 면 "도구 · 종류 · 이름 / 무엇을 하는지 / change sha256" 확인 후 서명, 아니면 묻지도 서명하지도 않고 같은 요청.
     - setup 호출의 제한 시간은 120 s(`SETUP_TIMEOUT_MS`; 미리보기가 폴더·프로젝트를 다시 검사하고 채택이 파일을 복사·제거함), 나머지는 10 s 그대로.
  6. preload·브리지 3곳(`bridge.ts`, `test/setup.ts`, `e2e/support/fake-bridge.ts` 두 곳): `adoptSetup(entries)`, `restoreSetup(adoptionId)`, `changeSetup(request)`.
  7. `scripts/sync-tray-catalog.mjs` KEYS 에 `lado.presence.setup*` 15개와 `lado.setup.kind.*` 8개, `node scripts/sync-tray-catalog.mjs` 로 생성 파일 재생성.
  8. 8개 로캘에 키 103개(종류 8, 오류 28, 사유 31, 충돌 13, 복원 상태 4, 경고 4, 데스크톱 확인 15) — 스크립트로 자리표시자 일치와 한국어 "자리표시자 바로 뒤 조사 없음"을 검사하며 기존 LADO 키 뒤에 붙임.
  9. `lado.ts`: `ladoError` export(새 파일이 같은 오류 변환을 씀).
  10. S13 이 필수로 만든 필드를 기존 fixture 6곳에 더함: `main/lado-presence.test.ts`·`LadoProjectRulesSection.test.tsx`·`LadoTaskDialog.test.tsx`·`LadoTaskRules.test.tsx` 의 effective `setup`, `LadoTaskRules.test.tsx` 의 `approved.setup*`·`aoSystemSha256`·`accepted`, `LadoExecutionPanel.test.tsx` 의 스냅숏 `setup: []`.
- 엔트리와 다르게 한 것과 이유:
  1. `main.ts` 는 바꾸지 않았다 — 등록은 이미 한 줄(`registerLadoPresenceIpc`)이고 새 채널은 그 안에서 등록된다.
  2. 데스크톱 확인 키를 8개보다 7개 더 만들었다: `setupAdoptBlocking`(기존 `approveRulesBlocking` 꼴), 변경 줄 6개(`setupChangeCreate`·`Edit`·`OnGlobal`·`OnProject`·`OnTask`·`KeepDrift`) — 렌더러가 고른 경로가 무엇을 하는지 메인이 자기 문장으로 보여 주려고(ADR-0002 "메인이 서명하는 것을 보여 줌"). 종류 이름은 렌더러 키 `lado.setup.kind.*` 를 트레이 카탈로그에 함께 넣어 쓴다(`PresenceMessageKey` 를 그 접두어까지 넓힘).
  3. 채택은 HOOK_PATH_MOVED 말고도 데몬이 거부하는 나머지(옮길 것 없음·미검증 OS·실행 중 도구)도 묻지 않고 거부한다 — 확인을 헛되게 하지 않으려고(`approvePlan` 의 latest·valid 꼴). 복원도 이미 복원·백업 못 읽음은 묻지 않는다.
  4. `changeSetup` 은 끄기·inherit·revert·삭제·수락을 받지 않는다(결정 i: 확인이 필요 없음). 메인의 dry run 이 `needsPresence:false` 면(그사이 상태가 바뀜) 창 없이 서명 없는 같은 요청을 보낸다 — 렌더러가 직접 보낼 수 있는 요청이라 권한이 늘지 않는다.
  5. `useLadoSetupChange` 에 규칙과 같은 영향 확인(`pending`/`confirm`/`cancel`)을 넣었다 — S15 의 전역 스위치와 S16 편집기의 `LadoRuleImpactDialog` 꼴 확인이 그대로 쓴다. 확인 뒤에는 그 dry run 의 `needsPresence` 로 바로 적용한다(다시 dry run 하지 않음).
  6. 제한 시간을 setup 호출만 120 s 로(위 5). 증명 수명(60 s)은 그대로 — 데몬은 다시 검사한 뒤 증명을 확인하므로 검사가 60 s 를 넘으면 `LADO_PRESENCE_EXPIRED` 가 날 수 있다(아래 남은 틈).
  7. 필수 테스트 파일 4개 외에 기존 fixture 6곳을 고쳤다(위 10) — 그대로면 S14-G4 가 실패한다.
- 확인한 명령과 결과:
  - `npx vitest run src/renderer/lib/lado-setup.test.ts src/renderer/lib/lado-events.test.ts src/renderer/lib/lado-errors.test.ts src/main/lado-presence.test.ts src/main/tray-catalog.test.ts` → 5 files, 97 tests 통과(새 테스트 블록: lado-setup 14, lado-events 4, lado-errors 6, lado-presence 22).
  - 이웃 묶음 `npx vitest run src/renderer/lib/event-transport src/renderer/i18n src/main/tray src/preload src/renderer/lib/lado src/main/lado src/renderer/hooks` → 91 files, 925 tests 통과. 고친 fixture 의 컴포넌트 테스트 5 files, 101 tests 통과.
  - 변이 검사: `invalidate(ladoSetupQueryRoot)` 를 지우면 lado-events 테스트 3개 실패; 메인의 hook 경로 거부를 끄거나 `needsPresence` 와 상관없이 서명하게 하면 lado-presence 테스트 2개 실패(되돌림 확인).
  - `npm run typecheck` 7분 21~26초(처음 두 번은 기존 fixture 오류 → 고친 뒤 깨끗), `npm run typecheck:e2e` 8초 깨끗.
  - diff-budget: `preload.ts` 15 + `fake-bridge.ts` 18 + `bridge.ts` 3 + `test/setup.ts` 3 = 39/60(`main.ts`·`event-transport.ts` 0).
  - 게이트: 코드 커밋 `b471a9e12` 에서 `gate-check.sh --run … --only` 로 G0~G7 각각 → 8/8 MET(G4 7분 33초). 엔트리 커밋 뒤 다시 돌린 결과는 그 커밋의 증거로 남는다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 데이터 층(클라이언트·질의 키·변경 흐름), 훅, 오류·사유·충돌·상태·경고 번역, `lado_setup` 이벤트, 메인 채널 셋, preload·브리지 3곳, 트레이 카탈로그, 8개 로캘, 필수 테스트 파일 4개. 화면은 없다. 엔트리 밖은 After 의 "다르게 한 것" 2·3·5·6·7.
- 남은 틈:
  - 실제 데스크톱 앱에서 채택·복원·변경 확인을 띄워 보지 않았다(실제 도구 폴더 금지) — S15 이후 lab 레시피(격리 데몬 + 임시 HOME)로 확인할 것.
  - 증명 수명 60 s: 데몬은 채택·변경 때 다시 검사한 뒤 증명을 확인한다. 프로젝트가 많아 검사(프로젝트마다 `git ls-files` 10 s 상한)가 60 s 를 넘으면 `LADO_PRESENCE_EXPIRED` 가 난다. 데몬 상한(120 s)까지 늘리거나 데몬이 증명을 먼저 확인하게 바꿀 수 있다.
  - 메인 확인 창은 채택 항목 이름을 나열하지 않는다(도구별 수만) — 항목이 수백 개일 수 있어서. 무엇이 옮겨지는지는 S15 대화상자가 보여 준다.
  - 변경 확인 창의 프로젝트·태스크는 id 로만 나온다(이름을 다시 읽지 않음).
  - `useLadoSetupAdoptionPreview` 는 POST dry run 을 질의로 쓴다 — `lado_setup` 이벤트가 오면 다시 검사한다(의도). 항목 선택이 바뀔 때마다 데몬이 전체를 다시 검사하므로 S15 에서 debounce 를 고려.
  - 목록 값(`sessions`·`processes`·`tools`)은 ", " 로 잇는다(로캘별 구분자 아님). PID 는 모든 언어에서 "PID".
  - es·fr·de·pt-BR 의 "Skill" 은 Claude Code 기능 이름이라 번역하지 않았다(ko·ja 는 음역).
- 다음 단계 메모:
  - **S15(설정 → 로컬 설정)**: 인벤토리 `useLadoSetupInventory()`, 다시 검사 `useScanLadoSetup()`, Global 충돌 `useLadoSetupConflicts()`, 채택 미리보기 `useLadoSetupAdoptionPreview(entries)`(또는 `previewLadoSetupAdoption`), 채택 `useAdoptLadoSetup().mutateAsync({entries, planSha256: preview.planSha256})` → `null` 이면 취소, 결과 `LadoSetupAdoptResult`(`items`, `verification`, `warnings`), 실패는 `LadoApiError`(부분 채택은 `details.state === "partial"`). 복원 `useRestoreLadoSetup().mutateAsync(adoptionId)` → `null` 취소, 결과 `report.entries[].status` 는 `ladoSetupRestoreStatusText`. 전역 스위치·끄기·수락은 `useLadoSetupChange().change({op: "binding", input: {itemId, scope: "global", scopeId: "", state}})` / `{op: "accept", input: {itemId, code, detail, scope, scopeId}}` — 결과 `status` 가 `confirm` 이면 `pending.result.impact` 로 `LadoRuleImpactDialog` 꼴 확인 뒤 `confirm()`, `cancelled` 는 데스크톱 확인 취소. 문장: 사유 `ladoSetupReasonText(t, entry.reason)`, 충돌 `ladoSetupConflictText(t, conflict)`(대상 항목은 `conflict.itemId`/`details.item` → 인벤토리 `items[]`), 경고 `ladoSetupWarningText(t, code)`, 종류 `ladoSetupKindLabel(t, kind)`(묶음 제목이 복수형이 필요하면 S15 가 따로 키를 만든다), 오류 `ladoErrorText(t, error)`. 화면 문자열 키는 `lado.setup.*` 접두어를 S14 가 썼다(`kind`·`reason`·`conflict`·`restore`·`warning`) — S15 는 겹치지 않는 하위 이름(`lado.setup.section.*` 등)을 쓸 것.
  - **S16(편집기·범위)**: 편집기 미리보기 `previewLadoSetupChange({op: "edit", itemId, input: {baseVersion, content}})`(차이 `before.text`/`after.text`, 충돌 증감, 영향) → 저장은 `useLadoSetupChange().change(같은 요청)`. 이력 `useLadoSetupItem(itemId)`, 범위별 항목 `useLadoSetupItems({projectId})`·`({projectId, taskId})`, 범위 충돌 `useLadoSetupConflicts(scope)`, drift 유지·되돌리기 `{op: "drift", itemId, input: {action, sha256}}`, 만들기 `{op: "create", input: {tool, kind, name, content}}`, 지우기 `{op: "delete", itemId}`. 409 `LADO_SETUP_ITEM_CHANGED` 는 `ladoErrorText` 가 "다시 열어 최신 확인" 문장. effective 의 problems 에 섞인 setup 충돌은 이제 `ladoProblemText` 가 번역한다.
  - **S17(문서)**: ADR-0002 의 채널 표에 셋(`lado:presence:adoptSetup`·`restoreSetup`·`changeSetup`)과 "메인이 거부하는 경우(묻지 않음)", setup 호출 제한 시간 120 s, 위 증명 수명 틈.
