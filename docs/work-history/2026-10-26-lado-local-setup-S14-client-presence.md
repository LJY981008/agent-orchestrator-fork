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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S15·S16 이 쓸 훅 이름·질의 키·번역 함수)
