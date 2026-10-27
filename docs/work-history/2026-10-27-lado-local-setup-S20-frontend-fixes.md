# 2026-10-27 LADO 로컬 설정 채택 — S20 리뷰 반영(프런트·문서): 확인 창·복원·화면 다듬기·as built

> 체인 `docs/chains/lado-local-setup-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§14, §15) · 브랜치 `feat/lado-local-setup` · 소유자 승인 2026-10-27(수정 체인 S18~S20, "이대로 진행")

## 의도/목적 — 왜 이 작업을 하나

프런트 리뷰(읽기 전용)의 지적과, S18·S19 가 바꾼 백엔드 규칙(확인 종류의 모든 변경에 확인, 복원 조회의 전제 조건, 새 오류 코드·보고서 항목)을 화면과 데스크톱 확인 채널에 맞춘다. 마지막으로 이 수정 라운드의 결정을 문서에 as built 로 남긴다.

## 범위

- 고친다(프런트 리뷰 F, 위치는 코드로 다시 찾는다):
  - **F1 메인이 상속을 거부**(`frontend/src/main/lado-presence.ts`의 바인딩 검사와 `turnsOn`): 소유자 결정대로 확인 종류는 모든 바인딩 상태가 확인을 받으므로, 메인은 전역 `on`/`off`, 프로젝트·태스크 `inherit`/`force_on`/`force_off` 를 모두 받는다(범위 id 검사 그대로). 서명은 **자기 dry run 이 `needsPresence` 라고 할 때만**. 삭제(`DELETE /lado/setup/items/{id}`)와 변경 감지 유지도 허용 경로에 넣는다(되돌리기는 확인 없음). 창 문장: "Turn on", "Turn off", "Follow Global (turns it on/off) for project/task …", "Delete …" — 8개 로캘과 트레이 KEYS. 메인 테스트의 상속 거부 사례를 뒤집는다.
  - **F2 복원 전에 실행 중 목록**: S19 의 복원 조회 `preconditions` 를 메인이 확인 창 전에 보고, 실행 중이면 묻지 않고 거부(채택과 같음). 채택 기록 카드의 복원 버튼 옆에 실행 중 목록과 "다시 확인".
  - **F3 복원 창의 채택 이름**: UUID 대신 채택 시각(앱 언어 형식)과 항목 수.
  - **F4 만들기·편집 창에 내용**: 메인이 자기 dry run 에서 받은 데몬의 정규 내용(after)을 제어·양방향 문자를 지운 한 줄 발췌(최대 300자, 넘으면 "…")로 보이고, 이 변경이 더하는 blocking 충돌 수를 적는다.
  - **F5 코드 없는 메인 실패**: "보냈지만 답을 읽지 못함" 류 실패에 코드 `LADO_PRESENCE_ANSWER_UNREADABLE`(번역 포함)을 주고, `useLadoSetupChange` 가 오류 때도 setup 질의를 무효화(채택·복원 훅처럼).
  - **F6 "다시 검사"**: 인벤토리뿐 아니라 충돌·항목·effective 질의도 무효화.
  - **F7 충돌 카드의 편집기**: 목록이 비어 일찍 돌아가도 카드가 연 편집기·대화상자가 닫히지 않게(대화상자를 early return 밖에서 렌더).
  - **F8 접근성**: 반복되는 "끄기"·"수락"·"복원" 버튼에 항목 이름을 담은 `aria-label`.
  - **F9 미검증 OS 의 새 항목**: OS 줄을 이유로 "새 항목" 비활성(데몬도 S19 에서 거부).
  - **수락 취소 화면**: 충돌 카드에 수락한 경고와 "수락 취소"(`DELETE /lado/setup/conflicts/acceptances/{id}` — 확인 없음).
  - **새 보고서·코드 보이기**: 복원 보고서의 `skipped_deleted`·`kept_in_lado`, 채택 오류의 `putBackFailed`, 사유 `root_is_link`·`provider_login_env`, 코드 `LADO_SETUP_DRIFT_PENDING`(편집기에서 "먼저 유지 또는 되돌리기" 안내와 변경 감지 검토로 가는 버튼), `LADO_SETUP_PATH_USED_IN_PLACE`, `LADO_SETUP_PAIR_REQUIRED`, `LADO_SETUP_ROOT_IS_LINK`.
- 문서(as built — 영어, 기존 "As built in step 7" 문단을 고치거나 덧붙임): TDD §9·§13·§15 항목 7, HARNESS §9.2(pending 채택, 증명 먼저), §9.4(홈은 나머지도 연결, 정리 때 구하기), §9.5(확인 종류와 허용 목록, 모든 변경에 확인 — 소유자 결정 2026-10-27), §9.7(복원이 지운 항목을 건너뜀), §10(새 충돌 코드), ADR-0005 Consequences(공급자 로그인 env 는 제자리, Codex args 비밀 거부, 링크된 뿌리 거절). `DESIGN.md` 결정 기록에 한 줄(`LADO fork: local setup review fixes` 로 시작 — 복원 전 실행 중 목록, 확인 창의 내용 발췌, 수락 취소).
- 하지 않는다: 백엔드 로직(S18·S19 — 끝남).

## 절차

0. 시작 전에 S18·S19 와 앞 체인의 S14·S15·S16 엔트리 `## 비교/회고` 와 "다음 단계 메모"를 읽는다. **`DESIGN.md` 를 먼저 읽는다.**
1. 항목마다 실패하는 테스트를 먼저 쓰고 고친다. 메인 채널 테스트(`lado-presence.test.ts`)는 새 허용 범위와 "dry run 이 확인 필요라고 할 때만 서명"을 함께 고정한다.
2. 문자열은 8개 로캘(한국어는 플랜 §0 용어집, 자리표시자 바로 뒤 조사 금지), 메인 문장은 트레이 KEYS 와 `node scripts/sync-tray-catalog.mjs`.
3. 커밋 메시지 예: `fix(lado): confirm every setup change at the desktop and show what is signed`, `docs(lado): record the step 7 review fixes`.

## 마무리 (모든 단계 공통)

1. 코드·문서 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-local-setup-S20-frontend-fixes.md` 로 전건 MET 를 확인한다. typecheck 가 10분 가까이 걸리므로 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

- `src/main/lado-presence.test.ts` — F1(모든 바인딩 상태·삭제·유지를 받고, dry run 이 확인 필요일 때만 서명), F2(실행 중이면 묻지 않음), F3(시각), F4(발췌·blocking 수), F5(코드)
- `src/renderer/lib/lado-setup.test.ts` — F5 오류 때 무효화, F6 무효화 범위
- `src/renderer/components/lado/setup/LadoSetupAdoptionsCard.test.tsx` — F2 실행 중 목록·다시 확인, 보고서의 `skipped_deleted`·`kept_in_lado`
- `src/renderer/components/lado/setup/LadoSetupConflictsCard.test.tsx` — F7, F8, 수락 취소
- `src/renderer/components/settings/LocalSetupSection.test.tsx` — F9, 새 사유 문장
- `src/renderer/components/lado/setup/LadoSetupItemEditor.test.tsx` — `LADO_SETUP_DRIFT_PENDING` 안내
- `src/renderer/lib/lado-errors.test.ts` — 새 코드·details
- `src/renderer/i18n/renderer-coverage.test.ts` — 하드코딩 영어 없음

## 수용 게이트

- [ ] S20-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S20-G1: 메인·데이터 층 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/main/lado-presence.test.ts src/renderer/lib/lado-setup.test.ts src/renderer/lib/lado-errors.test.ts
  EXPECT: VITEST_OK
- [ ] S20-G2: 화면 테스트와 커버리지 통과
  CHECK: bash .claude/gate/lado-local-setup.sh vitest src/renderer/components/lado/setup/LadoSetupAdoptionsCard.test.tsx src/renderer/components/lado/setup/LadoSetupConflictsCard.test.tsx src/renderer/components/settings/LocalSetupSection.test.tsx src/renderer/components/lado/setup/LadoSetupItemEditor.test.tsx src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S20-G3: 새 오류 코드가 번역 표에 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has frontend/src/renderer/lib/lado-errors.ts LADO_PRESENCE_ANSWER_UNREADABLE LADO_SETUP_DRIFT_PENDING LADO_SETUP_PATH_USED_IN_PLACE LADO_SETUP_PAIR_REQUIRED LADO_SETUP_ROOT_IS_LINK
  EXPECT: HAS_OK
- [ ] S20-G4: 문서에 이 라운드의 결정
  CHECK: bash .claude/gate/lado-local-setup.sh has docs/LJY-PRD/HARNESS.md 'LADO_SETUP_PATH_USED_IN_PLACE' 'provider_login_env' 'pending' && bash .claude/gate/lado-local-setup.sh has DESIGN.md 'LADO fork: local setup review fixes'
  EXPECT: HAS_OK
- [ ] S20-G5: 프런트 typecheck
  CHECK: bash .claude/gate/lado-local-setup.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S20-G6: 비밀 값 없음(CI 와 같은 gitleaks, main..HEAD)
  CHECK: bash .claude/gate/lado-local-setup.sh gitleaks
  EXPECT: GITLEAKS_OK
- [ ] S20-G7: 전체 빌드·vet(백엔드가 그대로)
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S20-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `eddd5e018`(docs(lado): record S19), 트리 깨끗. Linux(Ubuntu), Node·vitest 4.1.8, Go 는 스크래치 도구 체인. 실제 도구 폴더·`~/.ao` 는 건드리지 않음(렌더러·메인 테스트는 가짜 데몬).
- 읽은 것: S14·S15·S16·S17·S18·S19 의 `## 비교/회고`·"다음 단계 메모", `DESIGN.md` 전체.
- 항목별 지금 위치(코드에서 찾음):
  - F1: `frontend/src/main/lado-presence.ts` `setupChange`·`turnsOn`(전역 `on`, 범위 `force_on` 만), DELETE 미허용, `changeLine` 은 켜기 문장만. 렌더러 `ladoSetupPresenceRequest`(lib/lado-setup.ts)는 delete 에 null.
  - F2·F3: `restoreSetup`·`restoreSubject` — `preconditions` 를 읽지 않고, 창 문장은 `Adoption ID: {{id}}`.
  - F4: `changeSubject` 는 이름·sha 만, `after`·`conflictsAdded` 를 읽지 않음.
  - F5: `failure(undefined, "…could not be read.")` 14곳(승인·규칙·모드·설정 채널). `useLadoSetupChange` 는 성공 때만 무효화.
  - F6: `hooks/useLadoSetup.ts` `useScanLadoSetup` 은 인벤토리 `setQueryData` 만.
  - F7: `LadoSetupConflictsCard.tsx` 가 `conflicts.length === 0` 에서 편집기·영향 대화상자까지 함께 반환.
  - F8: 충돌 카드 끄기·수락, 채택 카드 복원 버튼에 이름 없음.
  - F9: `LocalSetupSection.tsx` "새 항목"은 OS 와 무관하게 켜짐.
  - 수락 취소: 데이터 층 `{op: "revoke"}` 만 있고 화면 없음.
- S18·S19 가 넘긴 것: 복원 보고서 `skipped_deleted`·`note: kept_in_lado`(서비스 `RestoredEntry`), 채택 오류 `details.putBackFailed`·`state: partial`, 사유 `root_is_link`·`provider_login_env`, 코드 `LADO_SETUP_DRIFT_PENDING`(details `item`)·`LADO_SETUP_PATH_USED_IN_PLACE`(details `key`·`path`·`origin`|`item`, 충돌 코드로도)·`LADO_SETUP_PAIR_REQUIRED`(`name`·`pair`)·`LADO_SETUP_ROOT_IS_LINK`(`tool`·`kind`·`name`·`root`), 복원 조회 `preconditions?`, 채택 `state: pending`.
- 발견한 틈: HTTP DTO `LadoSetupRestoredEntryResponse` 에 `note` 가 없고 `status` enum 에 `skipped_deleted` 가 없었다(서비스는 내지만 컨트롤러가 버림) — 화면이 보일 수 없음.

## After — 수행 후 실측

- 커밋: `21eca09f9` fix(lado): confirm every setup change at the desktop and show what is signed · `dfa1af691` docs(lado): record the step 7 review fixes · 이 엔트리 커밋.
- 항목마다 고친 것:
  - **F1**: 메인 `setupChange` 가 DELETE `items/{id}`(본문 없음; dry run 은 `?dryRun=true`, 실제는 빈 본문 서명)와 모든 바인딩(`isBinding`: 전역 `on`/`off`, 범위 `inherit`/`force_on`/`force_off`, id 검사 그대로)을 받는다. 서명은 자기 dry run 의 `needsPresence` 일 때만(그대로). 문장: 끄기 셋, 삭제, inherit 넷 — inherit 은 `GET /lado/setup/items?projectId=|taskId=` 로 그 항목의 `global`·`project` 를 읽어 "켜짐/꺼짐"을 정한다(못 읽거나 목록에 없으면 묻지 않고 `LADO_PRESENCE_ANSWER_UNREADABLE`). 확인 창의 이유 문장은 "이 항목은 … 모든 변경을 여기서 확인합니다"로. 렌더러 `ladoSetupPresenceRequest` 가 delete 를 `{method: "DELETE", path, body: {}}` 로, 공유 타입 method 에 `DELETE`.
  - **F2**: `restoreSubject` 가 `preconditions` 의 sessions·processes 를 읽어 하나라도 있으면 묻지 않고 `LADO_SETUP_TOOL_RUNNING`(details 그대로). 채택 카드는 행을 `AdoptionRow` 로 나눠 복원 가능한 행마다 `useLadoSetupAdoption(id)` — 실행 중이면 caution 색 한 줄("Restore waits until these close: …")·"Check again"(refetch)·복원 비활성.
  - **F3**: 창 첫 줄 `Adopted: {{when}}` — 메인이 앱 로캘로 `Intl.DateTimeFormat(locale, {dateStyle: "medium", timeStyle: "short"})`. 항목 수는 `items.length`, 0 이면 백업의 `action: move` 항목 수(중단된 채택).
  - **F4**: `changeSubject` 가 `after.text` 를 `excerpt`(제어·양방향 문자 → 공백, 공백 묶음 하나로, 코드 포인트 300자 넘으면 "…")로, `conflictsAdded` 의 blocking 수를 센다(`conflictsAdded` 가 배열이 아니면 읽지 못함). 창에 "Content: …" 와 "Blocking conflicts this change adds: n …" 문단.
  - **F5**: 메인의 "읽지 못함" 실패 14곳 모두 `LADO_PRESENCE_ANSWER_UNREADABLE`(번역 `lado.error.presenceAnswerUnreadable`). `useLadoSetupChange` 에 `onError` 무효화.
  - **F6**: `useScanLadoSetup` 이 인벤토리를 넣은 뒤 `["lado","setup","conflicts"]`·`["lado","setup","items"]`·`["lado","effective"]` 무효화(인벤토리는 다시 읽지 않음).
  - **F7**: 충돌 카드는 목록이 비면 섹션만 숨기고 편집기·변경 감지 검토·영향 대화상자는 늘 렌더.
  - **F8**: `aria-label` — "Turn off {{name}}", "Accept this warning for {{name}}", "Revoke the acceptance for {{name}}", "Restore the adoption of {{time}}".
  - **F9**: "New item" 은 모든 도구가 미검증일 때 비활성, `aria-describedby` 로 OS 줄. OS 줄·`LADO_SETUP_OS_UNVERIFIED` 문장을 "채택과 새 항목" 으로 고침.
  - **수락 취소**: 수락된 warning 행에 "Revoke acceptance" → `{op: "revoke", acceptanceId}`(dry run 뒤, 확인 없음).
  - **새 보고서·코드**: 보고서 `skipped_deleted`("Deleted in LADO, not put back")·`note` `kept_in_lado`(상태 뒤 " · Its later versions stay in LADO", `ladoSetupRestoreNoteText`); `putBackFailed` 가 있으면 `lado.error.setupAdoptionPutBackFailed`(경로 나열 + 채택 기록에서 복원); 사유 `root_is_link`·`provider_login_env`; 충돌 코드 `LADO_SETUP_PATH_USED_IN_PLACE`; 오류 코드 넷 + `LADO_PRESENCE_ANSWER_UNREADABLE`. 편집기는 `LADO_SETUP_DRIFT_PENDING` 에 폼 오류 대신 안내(status)와 "Review change" — 항목 목록·충돌 카드가 `onReviewDrift` 로 변경 감지 검토를 연다(충돌 카드에 `LadoSetupDriftDialog` 를 더함). 채택 `pending` 은 "In progress", 복원 없음.
  - 8개 로캘: 새 키 30개(메인 10 + 렌더러 20), 바뀐 값 4개(`setupChangeDetail`·`setupRestoreDetail`·`section.osUnverified`·`error.setupOsUnverified`). 트레이 KEYS 10개 + `node scripts/sync-tray-catalog.mjs`.
  - 문서: TDD §9(채널 셋 문단 고침 + 리뷰 뒤 홈·고정 bullet), §13(items·bindings 의 P 조건), §15 항목 7(리뷰 수정 bullet); HARNESS §9.1(로그인 env 규칙·`provider_login_env`·`root_is_link`·Codex 비밀), §9.2(증명 먼저·`pending`·interrupted·`putBackFailed`·`PATH_USED_IN_PLACE`·`otelHeadersHelper`·AGENTS 짝·미검증 OS 만들기), §9.4(나머지 연결·구하기·기준 바뀜·고정 빈 실행; "Not delivered" 의 낡은 문장 고침), §9.5(Presence·"Left for the owner" 를 소유자 결정 2026-10-27 로 바꿈), §9.7(`skipped_deleted`·`kept_in_lado`·조회 `preconditions`·pending), §10(표에 PATH_USED_IN_PLACE, revoke 화면, 채택을 멈추는 것); ADR-0005 Consequences 한 bullet; `DESIGN.md` 결정 기록 한 줄(`LADO fork: local setup review fixes`).
- 엔트리와 다르게 한 것과 이유:
  1. 백엔드 HTTP DTO 를 고쳤다(`lado_setup_dto.go` `Note`(enum `kept_in_lado`)·status enum `skipped_deleted`, `lado_setup.go` 매핑 한 줄) + `npm run api`(openapi.yaml +9−1, schema.ts +7−2). 로직이 아니라 S18 이 낸 값을 컨트롤러가 버리던 전송 틈이고, 없으면 필수 테스트의 보고서 표시가 거짓이 된다.
  2. task 범위 inherit 문장은 "Follow Global" 이 아니라 "Follow the project for task …" — 태스크는 프로젝트를 따르고, 프로젝트가 강제하면 Global 과 다르다(진실한 상태).
  3. F4 의 발췌·blocking 수는 만들기·편집만이 아니라 `after.text` 가 있는 모든 변경(drift keep 포함)과 blocking 이 더해지는 모든 변경에 보인다.
  4. F5 코드는 보낸 뒤뿐 아니라 보내기 전 "읽지 못함"(미리보기·조회)에도 같은 코드 — 둘 다 "데몬 답을 읽지 못함"이고 번역 문장이 둘 다에 맞다. 봉투 없는 HTTP 오류(502 등)는 그대로 코드 없음.
  5. S19 메모대로 메인 `adoptSetup` 과 채택 대화상자가 `LADO_SETUP_PATH_USED_IN_PLACE` 를 HOOK_PATH_MOVED 처럼 멈추는 충돌로 다룬다(데몬 `confirm` 이 둘 다 409).
  6. 채택 `pending` 표시("In progress", 복원 숨김)를 더함(S18 메모).
  7. 충돌 카드의 편집기에도 변경 감지 검토로 가는 버튼을 주려고 충돌 카드에 `LadoSetupDriftDialog` 를 더함.
- 테스트 순서: 메인·데이터 층은 코드를 먼저 고치고 바로 테스트를 고쳤다(새 사례 몇은 처음 실행에서 테스트 쪽 실수 — 미리보기 항목 id·문단 순서 — 로 실패한 뒤 통과). 화면의 F7 테스트는 이전 코드(early return)에서 실패하는 사례다. 엄격한 red-first 는 아니었다.
- 확인한 명령과 결과:
  - `npx vitest run src/main/lado-presence.test.ts src/main/tray-catalog.test.ts` 70 통과(메인 63), `src/renderer/lib/lado-setup.test.ts` 15, `lado-errors.test.ts` 13, `LadoSetupAdoptionsCard` 7, `LadoSetupConflictsCard` 10, `LadoSetupItemEditor` 7, `LocalSetupSection` 11.
  - 넓게: `npx vitest run src/main/lado-presence.test.ts src/renderer/components/lado/setup/ src/renderer/lib/ src/renderer/components/settings/LocalSetupSection.test.tsx` 115 파일 1488 통과; `src/renderer/i18n/ src/main/` 73 파일 1484 통과(1 skip).
  - `npx tsc --noEmit`(frontend) 통과, 8분 13초.
  - `go build ./... && go vet ./internal/httpd/...`, 격리 HOME `go test ./internal/httpd/controllers/ ./internal/httpd/apispec/...` ok.
  - 게이트(`--only` 로 나눠, 코드·문서 커밋 `dfa1af691` 위): G0·G1·G2·G3·G4·G6·G7·G8 MET. G5(typecheck)는 위 수동 실행으로 확인했고, 엔트리 커밋 뒤 전체 게이트를 다시 돌려 증거를 그 HEAD 에 남긴다.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 전부(F1~F9, 수락 취소, 새 보고서·코드, 문서 as built, DESIGN.md 줄), 필수 테스트 파일 8개 모두 고침·추가. 엔트리 밖은 "다르게 한 것" 1·5·6·7.
- 남은 틈:
  - 실제 데스크톱 앱에서 확인 창(발췌·blocking·inherit 문장·복원 시각)과 복원 전 실행 중 줄을 띄워 보지 않았다 — 메인 단위 테스트와 렌더러 테스트뿐. 다크·좁은 폭·한국어 화면도 미확인.
  - inherit 확인은 데몬 조회를 한 번 더 한다(dry run 과 조회 사이 바뀌면 문장은 조회 때 것). 시각은 메인의 `Intl` — 앱의 `formatResetTime` 과 꼴이 조금 다를 수 있다.
  - 복원 가능한 채택마다 조회가 하나씩 나간다(데몬이 그때마다 프로세스 목록을 읽음). 채택이 많으면 비용이 커진다.
  - 충돌 카드는 여전히 Global 범위뿐이고, 수락 취소도 Global 카드에서만.
  - 미검증 OS 에서 "새 항목"은 모든 도구가 미검증일 때만 끈다(도구별 검증이 갈리는 OS 는 아직 없음; 그때는 데몬 409 가 대화상자에 번역되어 나옴).
  - `LADO_SETUP_PAIR_REQUIRED` 는 채택 대화상자에서 미리보기 오류 문장으로만 안내한다(짝을 자동으로 고르지 않음).
- 메인 세션이 할 것: 전체 CI 미러(Go 전체·race·lint, 프런트 typecheck·vitest 전체, api·sqlc drift, gitleaks)를 브랜치 전체에 다시; 랩 실기 확인(격리 데몬 + 임시 HOME, lado-lab-recipe — 채택 → 편집·삭제·끄기·inherit 확인 창 → 복원 전 실행 중 줄 → 복원); 푸시·PR(`--repo LJY981008/agent-orchestrator-fork`).
