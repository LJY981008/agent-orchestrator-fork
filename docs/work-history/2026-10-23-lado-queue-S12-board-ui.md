# 2026-10-23 LADO 대기열·디스패처 — S12 레인·태스크 뷰·보드 카드·세션 LADO 패널

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 k, §10 레인·태스크 뷰·보드 카드·세션 뷰) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

대기열과 실행을 보이게 한다: 레인이 새 상태와 이유를 보여 주고, 태스크 뷰는 디스패치 뒤 세션으로 이어지며, 실행 세션의 보드 카드에 LADO 한 줄, 세션 화면 오른쪽 레일에 그 실행의 스냅숏(HARNESS §12 "모든 실행 화면에 활성 스냅숏")을 띄운다.

## 범위

- 고친다: `frontend/src/renderer/components/lado/LadoLanes.tsx`(배치·상태 줄·이유 줄), `LadoTaskDialog.tsx`(디스패치 뒤 상태 줄과 세션 링크, failed 재승인), `components/SessionsBoardAdapters.tsx`(실행 세션 카드의 LADO 줄 — `footer` 또는 `artifacts` 슬롯; product-ui 는 고치지 않는 것이 목표), `components/SessionInspectorRail.tsx`(LADO 패널 자리 — 업스트림 파일이므로 몇 줄 훅), 8개 로캘 파일.
- 만든다: `components/lado/LadoExecutionPanel.tsx`(+ 테스트), 필요하면 `components/lado/LadoSessionLine.tsx`(+ 테스트).
- 하지 않는다: 설정 화면(S11), 백엔드.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. **`DESIGN.md` 를 먼저 읽는다**(상태 점·톤, 카드 상태 줄 규칙, 인스펙터 레일 구성). S11 의 결정 로그 행 아래에 이 화면들의 행을 하나 더한다(`| 2026-10-23 | LADO fork: queue states, run line and run panel | ... |`).
2. 레인: Plan 레인 = draft·planned·review_required·blocked·failed(failed 는 attention 톤, 이유 문장), Queued 레인 = queued·dispatching(dispatching 은 진행 톤). 카드 둘째 줄에 이유: 대기 — 빈 자리(`slot_global`·`slot_project`), 메모리, 이슈 읽기, 재시도(다음 시도 시각 상대 표시), LADO 모드 꺼짐; "시도 n/3"(시도가 1 이상일 때). running·completed·closed_unmerged 태스크는 레인에서 빠진다(취소된 것처럼 서버가 걸러내지 않으므로 렌더러가 거른다).
3. 태스크 뷰: 실행이 있으면 바닥줄 위에 상태 줄("실행 중 · 세션 열기" 링크 — 세션 라우트로 이동, PR 이 있으면 PR 링크), failed 면 이유와 함께 "계획 승인"이 다시 켜진다(review_required 와 같은 길 — `canApprove` 규칙 갱신). queued·dispatching 이면 승인 버튼 대신 상태 표시.
4. 보드 카드: 보드가 이미 불러오는 `useLadoTasks(projectId)` 에서 `execution.sessionId → 태스크` 맵을 만들어, 그 세션 카드에 "LADO · 계획 r{n} · 규칙 {sha 앞 7자}" 한 줄(muted, mono 해시). LADO 모드가 아닌 프로젝트는 아무것도 하지 않는다.
5. 세션 LADO 패널: `GET /lado/executions/{sessionId}`(404 면 패널 없음). 에이전트와 모드, 권한 상한, 규칙 이름(내장 규칙은 `ladoRuleName` 으로 번역), 워커에게 간 규칙 문구(접힘), 계획 리비전·승인 시각, "베이스가 n커밋 앞섬"(0 이거나 모르면 숨김), 마감(남은 시간 또는 "시간 초과로 종료됨"). 인스펙터 레일의 기존 섹션 꼴을 따르고, 레일 파일에는 패널을 끼우는 몇 줄만.
6. 문구 8개 로캘 실제 번역(한국어 용어집, 조사 앞 변수 금지).
7. 커밋 메시지 예: `feat(lado): queue states on the board, run line on session cards and the run panel`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S12-board-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/components/lado/LadoLanes.test.tsx` — 새 상태의 레인 배치, 이유 줄(대기 이유별·시도 n/3), 실행된 태스크는 레인에 없음
- `src/renderer/components/lado/LadoTaskDialog.test.tsx` — 실행 중 상태 줄과 세션 링크, failed 재승인 가능, queued 는 승인 버튼 없음
- `src/renderer/components/lado/LadoExecutionPanel.test.tsx` — 패널 필드, 404 면 렌더 없음, 시간 초과 표시, 내장 규칙 이름 번역
- 보드 카드 LADO 줄 테스트(`LadoSessionLine.test.tsx` 또는 `SessionsBoardAdapters` 테스트) — 실행 세션에만 줄이 붙는다

## 수용 게이트

- [ ] S12-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S12-G1: 레인·태스크 뷰·패널 테스트 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx
  EXPECT: VITEST_OK
- [ ] S12-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S12-G3: DESIGN.md 결정 로그 행
  CHECK: bash .claude/gate/lado-queue.sh has DESIGN.md 'LADO fork: queue states, run line and run panel'
  EXPECT: HAS_OK
- [ ] S12-G4: 업스트림 화면 파일 훅이 작다
  CHECK: bash .claude/gate/lado-queue.sh diff-budget 40 frontend/src/renderer/components/SessionInspectorRail.tsx frontend/src/renderer/components/SessionsBoardAdapters.tsx
  EXPECT: BUDGET_OK
- [ ] S12-G5: 렌더러 전체 테스트
  CHECK: cd frontend && npx vitest run && echo S12_VITEST_ALL_OK
  EXPECT: S12_VITEST_ALL_OK
- [ ] S12-G6: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S12-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- HEAD `1601bb8f4`(S11 기록), 트리 깨끗. `LadoLanes.test.tsx`·`LadoTaskDialog.test.tsx` 24개 통과, `LadoExecutionPanel.test.tsx` 없음.
- 레인은 `state === "approved"` 만 Queued 에 두었다. 데몬은 6단계부터 `approved` 를 보내지 않으므로 queued·dispatching·running·failed·completed 태스크가 모두 Plan 레인에 들어가 상태 줄의 default 분기("초안")로 보였다. 이유 줄은 없었다.
- 태스크 뷰: 재승인은 review_required 만, 실행 상태 줄·세션 링크 없음, review_required 는 이유와 상관없이 "규칙이 바뀌었습니다" 안내. 보드 카드·인스펙터에 LADO 표시 없음.
- 업스트림 훅 diff(태그 이후): `SessionInspectorRail.tsx`·`SessionsBoardAdapters.tsx` 0/40. 로캘 en 3572 키(LADO 422).
- 구조: `SessionInspectorRail.tsx` 는 크기 조절 틀이고 children 이 탭 인스펙터 전체다. 섹션은 `SessionInspector.tsx` 요약 탭이 product-ui `SessionInspectorSummaryView` 슬롯으로 채우며, 그중 `context` 슬롯("실행 맥락", workers 뒤·브랜치 앞)은 업스트림에서 비어 있다.

## After — 수행 후 실측

- 코드 커밋 `2cfe042e6` `feat(lado): queue states on the board, run line on session cards and the run panel` — 21 파일, +1240 −86. 리뷰 반영 `e0cfd401f` `fix(lado): count run times in whole hours from the first hour` — 3 파일, +48 −4.
  - `components/lado/lado-task-status.ts`(새): `ladoTaskStatus`·`ladoTaskReason`·점 클래스(LadoLanes 에서 옮김 — 대화상자도 써서 순환 import 를 피함). 톤 muted·attention·ready·working·merged.
  - `LadoLanes.tsx`: Queued = queued·dispatching(·옛 approved), Plan = 그 밖. running·completed 와 실행이 있는 closed_unmerged 는 레인에서 뺀다. 카드 상태 줄 아래 muted 둘째 줄(라벨 위치에서 시작, 한 줄 말줄임 + title): 대기 이유(빈 자리 전체·프로젝트, 메모리, 이슈 읽기, LADO 모드 꺼짐), 재시도는 "{{time}} 다시 시도"(retry 일 때만 시각), 시도 1 이상이면 "시도 n/3"; review_required(rules_changed 제외)·blocked·failed 는 이유 문구; 새 계획 요청 중이면 없음. failed 는 needs-you 점, dispatching 은 working 점.
  - `LadoTaskDialog.tsx`: 태스크는 `useLadoTasks(projectId)` 목록의 것을 먼저 쓴다. queued·dispatching 은 승인 버튼 자리에 점·상태·이유(`lado-queue-status`). failed 는 재승인 가능(review_required 와 같은 길). 실행 세션이 살아 있거나 running·completed 면 승인 불가 + "워커가 아직 실행 중…" 안내(`lado.error.taskRunning`). 바닥줄 첫 행(`lado-run-status`)에 실행 줄: running·completed·closed_unmerged(실행 있음)·failed — 점, 상태, 실패 이유, "세션 열기"(대화상자를 닫고 세션 라우트로), PR 이 있으면 mono "PR #n" 링크. Plan 탭 안내는 이유별(아래 결정 3).
  - `components/lado/LadoSessionLine.tsx`(새): 보드 카드 줄 "LADO · plan r{n} · rules {sha 7}"(muted, 해시 mono). LADO 모드인 로컬 프로젝트의 워커만 `useLadoTasks` 를 읽고, `ladoTaskForSession`(작업 목록마다 WeakMap 한 장)으로 세션을 찾는다. 리비전·해시가 생략되면 그 조각만 뺀다. `ladoSessionProjectId` 를 패널도 쓴다.
  - `components/lado/LadoExecutionPanel.tsx`(새): `useLadoExecution` → product-ui `InspectorSection` "LADO 실행": 에이전트(+모델 mono), 모드, 권한 상한, 계획(리비전 · 승인 시각), 규칙(내장은 `ladoRuleName` 번역, 스냅숏 해시 mono), 기준 브랜치(앞선 커밋 수 — 0·모름이면 숨김), 마감("{{time}} 종료" 또는 시간 초과면 `lado.reason.timed_out`; 세션이 스스로 끝났거나 상한이 없으면 숨김), 워커 규칙 문구(접힘).
  - `lib/lado.ts`: `LADO_MAX_ATTEMPTS`, `ladoTimeUntil`(앱 언어 `Intl.RelativeTimeFormat` short·numeric auto, 초→분→시간, 지나면 "지금"), `ladoTaskForSession`.
  - 업스트림 훅: `SessionsBoardAdapters.tsx` +6 −1(`artifacts` 슬롯에 줄), `SessionInspector.tsx` +3(요약 탭 `context` 슬롯), `SessionInspectorRail.tsx` 0.
  - 로캘 8개 × 19키(es·fr·pt-BR 은 `lado.run.baseAhead_many` 까지 20). 한국어: 다시 승인 필요, 병합 없이 닫힘(AO 표시 상태와 같은 말), "{{time}} 다시 시도", "시도 {{attempt}}/{{max}}", 세션 열기, 계획 r{{number}}, 규칙, LADO 실행, 에이전트, 모드, "리비전 {{number}} · {{time}} 승인", 기준 브랜치, "계획보다 {{count}}커밋 앞섬", 마감, "{{time}} 종료" — 자리표시자 바로 뒤 조사 없음.
  - `DESIGN.md` 결정 로그 끝에 `| 2026-10-23 | LADO fork: queue states, run line and run panel … |` 한 행.
  - 테스트: `LadoLanes.test.tsx` 15(11→15), `LadoTaskDialog.test.tsx` 22(13→22), `LadoExecutionPanel.test.tsx` 7(새), `LadoSessionLine.test.tsx` 4(새 — 실제 `BoardSessionCardAdapter` 두 장 중 실행 세션에만 줄), `lib/lado.test.ts` +2(시간 경계).
- 엔트리와 다르게 한 것·엔트리가 정하지 않은 것:
  1. **패널 자리는 `SessionInspectorRail.tsx` 가 아니라 `SessionInspector.tsx` 요약 탭의 `context` 슬롯이다.** 레일은 탭 인스펙터 전체를 감싸는 크기 조절 틀이라, 거기 끼우면 탭 머리 위에 붙어 브라우저·파일 탭에서도 자리를 먹고 세션 id 도 없다. `context` 는 product-ui 가 실행 맥락용으로 둔 빈 슬롯이고 섹션 꼴(`InspectorSection`)이 그대로 맞는다. 레일 파일 diff 는 0(G4 7/40).
  2. closed_unmerged: 실행 뒤의 것만 레인에서 뺀다. 실행 없이 이슈가 닫힌 태스크는 "이슈 닫힘" 카드로 남는다(2026-10-19 DESIGN 행의 카드·취소 동작; 필수 테스트 "실행된 태스크는 레인에 없음"과 같은 뜻).
  3. review_required 의 이유가 rules_changed 가 아니면(issue_edited·approved_before_queue) 라벨을 "다시 승인 필요"로 나눴다 — "규칙 확인 필요"는 거짓이 된다. 대화상자 안내도 이유별: 규칙 → 기존 문구, 이슈 → 뷰의 기존 이슈 변경 안내, 그 밖 → 이유 문구. blocked 도 base_ref_mismatch·agent_unsupported 는 이유 문구(기존 문구는 규칙 탭을 가리킨다).
  4. 실행 줄은 바닥줄 안 첫 행(basis-full)이다 — 띠를 하나 더 두지 않았다. failed 는 실행이 없어도(attempts_exhausted) 이유 때문에 보이고, 새 계획 요청 중이어도 실행의 상태("실패")로 쓴다.
  5. 시각 문구는 새 키 없이 `Intl.RelativeTimeFormat` 으로 8개 언어. 재시도 시각은 reason=retry 일 때만(slot_project·issue_unreadable 의 다음 시각은 재시도가 아님 — S08 메모). 렌더 때 계산하고 흐르지 않는다(다음 claim·실행 이벤트가 다시 그린다).
  6. 패널: 404(null)·로딩·데몬에 닿지 못함이면 아무것도 안 보인다(세션 대부분은 LADO 실행이 아니다). 데몬이 코드 있는 오류로 답하면 섹션에 오류 한 줄. 응답에 snapshot 이 없으면 실행이 아닌 것으로 본다 — 업스트림 `SessionInspector.test.tsx` 의 넓은 GET mock 이 다른 본문을 줘 크래시가 났다(업스트림 테스트는 고치지 않음).
  7. 마감의 시간 초과 문구는 엔트리 예시("시간 초과로 종료됨") 대신 카드·대화상자와 같은 `lado.reason.timed_out`("최대 실행 시간에 도달해 중지됨")을 쓴다 — 같은 사실을 같은 말로.
  8. 보드 줄은 `footer` 가 아니라 `artifacts` 슬롯(상태 줄 위, 산출물 링크 앞) — `footer` 는 상태 줄 아래라 DESIGN 의 카드 정보 순서(근거 → 상태 줄 → 메타)와 어긋난다. 실행이 아니면 null 이라 다른 카드는 그대로다. 줄은 한 텍스트 흐름(" · " 를 글자로 — 읽는 문자열이 "LADO·plan" 으로 붙지 않게). 대화상자의 구분자도 공백 텍스트 노드를 둔다.
  9. 목록 밖 파일: `lib/lado.ts`(헬퍼 3개)·`lib/lado.test.ts`, `SessionInspector.tsx`(위 1).
- 확인:
  - 필수 4파일 48개 통과(리뷰 반영 뒤 49), `lib/lado.test.ts` 21, i18n `instance`·`renderer-coverage`, `SessionInspector`(·remote)·`SessionsBoard`(·remote)·`SessionInspectorRail` 통과.
  - 프런트엔드 전체 `npx vitest run`: 520 파일, 8621 통과·7 skip(1분 37초). `npm run typecheck` 6분 46초·`typecheck:e2e` 통과. 백엔드 변경 없음.
  - 변이 12개 모두 테스트가 잡음: running 이 레인에 남음, dispatching 을 ready 톤, 시도를 2부터, 모든 대기에 시각, failed 재승인 불가, 살아 있는 세션 무시, 뷰의 태스크만 사용, 기준 브랜치 행 항상, 시간 초과 무시, 규칙 이름 원문, LADO 모드 무시, 태스크 id 로 매핑.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker 없음. should-fix 1 — `ladoTimeUntil` 이 60–89분을 분으로, 90분부터 반올림해 "2시간"으로 보여 "1시간"이 나오지 않음 → 시간 단위를 60분부터, 일 단위는 뺌(LADO 시각은 하루 안 — 최장 상한 24시간; 일 단위면 numeric auto 가 "내일"이 된다), 경계 테스트 2개(`e0cfd401f`). nit 2 — 목록 태스크를 뷰보다 앞세워 이벤트 전 잠깐 낡을 수 있음(그대로: 둘 다 같은 `lado_task` 이벤트로 무효화되고 세션·PR 이벤트는 목록만 갱신 — S10 메모), 실행 뒤 closed_unmerged 대화상자 테스트 없음 → 더함. 패널 자리 판단은 타당하다고 봤다.
  - 화면(랩): dev-web 미리보기(`VITE_NO_ELECTRON=1`, 데몬 없음) + Playwright `page.route` 로 LADO 응답만 스텁(설정·태스크 8개·저장소·실행 1개, 미리보기 데모 세션 `demo-working` 에 실행을 붙임). 보드: Plan 레인 실패·차단됨·다시 승인 필요·초안, Queued 레인 대기 중 2("Waiting for a free run slot · Attempt 1 of 3", "Trying again in 2 min. · Attempt 1 of 3")·시작하는 중(파란 점·"Attempt 2 of 3"), 실행 중 태스크는 레인에 없고 그 세션 카드 상태 줄 위에 "LADO · plan r3 · rules 1a2b3c4". 세션 화면 인스펙터 요약 탭 맨 위 "LADO run" 섹션이 Branch·Activity 와 같은 꼴, 규칙 문구 펼침 확인. 콘솔 오류는 스텁 밖 데몬 요청 실패뿐. 대화상자는 화면으로 보지 않았다(테스트 몫), 스크린숏은 세션 스크래치에만 두었다.
- 게이트(HEAD `e0cfd401f`, `GATE_TIMEOUT=1800` 으로 게이트별 실행): 8건 모두 MET(G4 7/40, G6 7분 9초). 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 상태 모듈 분리, review_required 라벨 분리와 이유별 안내(After 결정 3), 살아 있는 세션의 승인 막기, 패널 응답 가드, 시간 헬퍼와 그 경계 테스트. 엔트리와 다르게 한 것: 패널 자리(After 결정 1), 실행 없는 closed_unmerged 를 남김(2), 시간 초과 문구(7), 보드 줄 슬롯(8).
- 남은 틈(기록만):
  - 보드 줄은 LADO 모드인 프로젝트에서만 보인다 — 모드를 끈 뒤에도 도는 실행의 카드에는 줄이 없다(인스펙터 패널은 있다). 같은 태스크의 옛 실행 세션 카드에도 없다(목록은 최신 실행만).
  - 실패했는데 세션이 살아 있는 태스크(마감 kill 실패)는 Plan 레인 카드와 세션 카드 두 곳에 보인다.
  - `lado_mode_off` 이유는 레인에 닿지 않는다 — 모드가 꺼지면 보드가 레인을 숨기고, 대화상자도 레인에서 열린다. 문구는 맵에 있다(대기열 API·다음 화면용).
  - 대기열 항목 `error`(`CODE: message`)는 보이지 않는다 — 실패 이유 문구(`attempts_exhausted` 등)로 갈음.
- 다음 단계 메모(S13):
  - TDD §3.3·HARNESS §12·LADO.md·LADO.ko.md 에 적을 것: 레인 배치(실행 뒤 레인에서 빠짐, 실행 없는 closed_unmerged 는 남음), 카드 둘째 줄 규칙(대기 이유·retry 시각·시도 n/3·이유 문구), review_required 라벨 분리, 태스크 뷰의 대기 상태·실행 줄·재승인 규칙(failed 허용, 세션이 살아 있으면·completed 면 막음), 보드 줄(LADO 모드 로컬 프로젝트, 상태 줄 위), 인스펙터 패널(요약 탭 `context` 슬롯, 404·오류·가드 규칙, 시간 초과 문구).
  - 플랜 §10·이 엔트리는 패널 자리를 `SessionInspectorRail.tsx` 로 적었지만 실제 자리는 `SessionInspector.tsx` 요약 탭의 `context` 슬롯이다 — 문서에는 실제 자리로 적는다.
