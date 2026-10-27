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

(단계 시작 때 채운다: 시작 HEAD, 환경, 리뷰 항목마다 코드에서 찾은 지금 위치, S18·S19 가 넘긴 코드·details)

## After — 수행 후 실측

(커밋, 항목마다 고친 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈 — 메인 세션이 할 것: 전체 CI 미러 다시, 랩 실기 확인, 푸시·PR)
