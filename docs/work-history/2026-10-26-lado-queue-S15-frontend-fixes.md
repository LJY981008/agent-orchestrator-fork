# 2026-10-26 LADO 대기열·디스패처 — S15 리뷰 반영: 프런트·문서

> 체인 `docs/chains/lado-queue-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 g 는 S14·이 엔트리가 고친다) · 브랜치 `feat/lado-queue-dispatcher` · 소유자 승인 2026-10-26("체인 2단계", 시간 상한은 "PR 이 열리면 멈춤")

## 의도/목적 — 왜 이 작업을 하나

6단계 프런트 리뷰(블로커 0, 메이저 1 — 원인은 백엔드라 S14 가 고침, 마이너 4, 니트)를 반영하고, S14 의 새 동작(대기 이유 `run_live`, 시간 상한 규칙, 새 오류 문장, `enqueuedAt`)을 화면과 문서에 맞춘다.

## 범위 (지적 번호 = 리뷰 번호)

- **F1 (마이너 m1, 멈춘 상대 시각)** — 재시도 줄과 실행 패널의 마감을 상대 시각("2분 뒤")이 아니라 시계 시각("14:32에 다시 시도", "18:05까지")으로. 앱의 시간 형식 도우미가 있으면 그것을 쓴다(찾아서). 날짜가 다르면 날짜도.
- **F2 (마이너 m2, 실패 이유)** — failed 태스크의 태스크 뷰 상태 줄 아래에 `task.queue.error`(데몬이 정리한 문장)를 muted 한 줄로(잘리면 `title` 로 전체). `base_ref_mismatch` 안내는 "수정 요청"으로 계획을 고치라고 말한다.
- **F3 (마이너 m3, 대기열 순서)** — Queued 레인은 `queue.enqueuedAt` 오름차순(먼저 승인된 것이 위 — 디스패처가 잡는 순서). 안 쓰는 `useLadoQueue`·`fetchLadoQueue` 는 지운다(API 경로는 둔다).
- **F4 (마이너 m4, 복구된 세션)** — `lib/lado-events.ts`: 세션 갱신 이벤트에 `isTerminated` 가 있으면(true·false 모두) 그 세션이 보드가 아는 실행이면 태스크·실행 쿼리를 무효화한다(복구로 다시 살아난 실행이 Failed 로 남지 않게).
- **S14 반영** — 대기 이유 `run_live` 문구("이 작업의 이전 실행이 아직 돌고 있음"), PR 이 열려 시간 상한이 멈춘 실행의 패널 문구("PR 이 열려 시간 상한을 적용하지 않습니다"), `LADO_ISSUE_NOT_FOUND`·`LADO_EXECUTION_FIXED`(복구 거부) 오류 문구.
- **니트** — en `Project id:` → `Project ID:`, ko `프로젝트 id:` → `프로젝트 ID:`(그 뒤 `node scripts/sync-tray-catalog.mjs`); ko `머지되었습니다` → `병합되었습니다`; 태스크 뷰의 `role="status"` 는 버튼·링크가 아닌 라벨 부분에만; 브라우저에서 모드를 끌 때 `LADO_PRESENCE_UNAVAILABLE` 이 승인 문구로 나오지 않게 모드 끄기 전용 문구; 모드 끄기 확인 대화상자(메인 프로세스, `lado.presence.modeOffDetail` 등)에 결과 한 문장 추가 — "그 뒤로 이 프로젝트의 워커는 승인한 계획 없이도 시작할 수 있습니다."(8개 로캘, 트레이 카탈로그 재생성).
- **문서** — `docs/LJY-PRD/TDD.md` §5.3 "As built in step 6" 에 리뷰 반영(S14 의 B1~B8: 상한 찬 프로젝트는 잡지 않음, 리스는 잡을 때 시각, `lado:` 예약과 연결 전 확인, 태스크당 워커 하나와 정리, 복구는 kill 실패 시 리스 유지, 시간 상한은 PR 이 생기면 멈춤, 멈춘 실행 복구 거부, 이슈 읽기 실패의 늘어나는 대기와 삭제된 이슈) — 기존 "known gap" 문장 중 고쳐진 것은 지운다. "once the startup reconcile has settled" 를 실제(시작 안전 정리 뒤, 배경·지속 호스트 정리는 그 뒤에 돈다)로. 플랜 결정 g 의 기본 8시간 문장에 "PR 이 열리면 멈춤" 추가는 TDD 에만(플랜 문서는 고치지 않는다 — 엔트리가 우선). `LADO.md`·`LADO.ko.md`: 6단계 설명에 Failed 원인 "PR 없이 세션이 끝남", 모드 켜기는 트래커 intake 가 켜져 있으면 거부, 시간 상한은 PR 이 열리면 멈춤.
- 하지 않는다: 백엔드 코드(S14 끝), `RunsSection` 의 동시 변경 덮어쓰기(한 사람 데스크톱이라 둔다 — 회고에 적는다).

## 절차

0. 시작 전에 S01~S14 엔트리의 `## 비교/회고` 와 "다음 단계 메모"(특히 S14)를 읽는다. **`DESIGN.md` 를 먼저 읽는다.**
1. 지적마다 실패하는 테스트 → 고침 → 통과. 새 문구는 8개 로캘에 실제 번역(한국어 용어집, 조사 앞 변수 금지).
2. 메인 프로세스 문구를 바꾸면 `node scripts/sync-tray-catalog.mjs` 로 `tray-catalog.generated.ts` 를 다시 만든다.
3. 커밋 예: `fix(lado): review fixes for the queue and run screens`, `docs(lado): record the step 6 review fixes`.

## 마무리 (모든 단계 공통)

1. 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-queue-S15-frontend-fixes.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/components/lado/LadoLanes.test.tsx` — 재시도 줄이 시계 시각, Queued 레인이 enqueuedAt 오름차순, `run_live` 이유 줄
- `src/renderer/components/lado/LadoTaskDialog.test.tsx` — failed 의 데몬 오류 줄, base_ref_mismatch 안내, status 역할이 라벨에만
- `src/renderer/components/lado/LadoExecutionPanel.test.tsx` — 마감이 시계 시각, PR 이 있으면 "시간 상한 적용 안 함"
- `src/renderer/lib/event-transport.test.ts` — 실행 세션의 `isTerminated: false` 갱신도 태스크·실행 쿼리를 무효화
- `src/main/lado-presence.test.ts` — 모드 끄기 대화상자에 결과 문장
- `src/renderer/lib/lado-errors.test.ts` — 새 오류 코드와 `run_live` 가 모든 로캘에서 번역됨

## 수용 게이트

- [ ] S15-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S15-G1: 필수 테스트 파일 6개 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx src/renderer/lib/event-transport.test.ts src/main/lado-presence.test.ts src/renderer/lib/lado-errors.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G3: 용어 니트와 대화상자 문장
  CHECK: bash .claude/gate/lado-queue.sh none frontend/src/renderer/i18n/ko.json '프로젝트 id:' '머지되었습니다' && bash .claude/gate/lado-queue.sh none frontend/src/renderer/i18n/en.json 'Project id:' && bash .claude/gate/lado-queue.sh has frontend/src/renderer/i18n/en.json 'without an approved plan'
  EXPECT: HAS_OK
- [ ] S15-G4: 안 쓰는 대기열 훅 삭제
  CHECK: bash .claude/gate/lado-queue.sh none frontend/src/renderer/hooks/useLado.ts 'useLadoQueue' && bash .claude/gate/lado-queue.sh none frontend/src/renderer/lib/lado.ts 'fetchLadoQueue'
  EXPECT: NONE_OK
- [ ] S15-G5: 문서가 리뷰 반영을 적는다
  CHECK: bash .claude/gate/lado-queue.sh has docs/LJY-PRD/TDD.md 'run_live' 'LADO_ISSUE_NOT_FOUND' && bash .claude/gate/lado-queue.sh none docs/LJY-PRD/TDD.md 'once the startup reconcile has settled'
  EXPECT: NONE_OK
- [ ] S15-G6: 렌더러 전체 테스트
  CHECK: cd frontend && npx vitest run && echo S15_VITEST_ALL_OK
  EXPECT: S15_VITEST_ALL_OK
- [ ] S15-G7: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S15-G8: gitleaks 가 브랜치 커밋 전부에서 깨끗하다
  CHECK: bash .claude/gate/lado-queue.sh gitleaks
  EXPECT: GITLEAKS_OK
- [ ] S15-G9: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `13e456910`(`docs(lado): record S14`), 트리 깨끗. umask 0022, Node 24.21.0, TZ=UTC, LANG=C.UTF-8, GATE_TIMEOUT=1800. 체인 태그 `lado-step6-chain-def` = `adc84a959`.
- 게이트 문자열(HEAD 의 `git show`): ko `프로젝트 id:` 1·`머지되었습니다` 1, en `Project id:` 1·`without an approved plan` 0, `useLadoQueue` 1(`useLado.ts`), `fetchLadoQueue` 1(`lado.ts`), TDD `run_live` 0·`LADO_ISSUE_NOT_FOUND` 0·`once the startup reconcile has settled` 2(§1.2 daemon 행, §5.3 Daemon).
- S14 After 그대로: 렌더러 타입체크 14건 실패(`reasonKeys` 에 `run_live` 없음, `LadoLanes.test.tsx`·`LadoTaskDialog.test.tsx` 의 queue 고정값에 `enqueuedAt` 없음).
- 코드로 확인한 출발점: 재시도 줄·패널 마감은 `ladoTimeUntil`("in 2 min." 상대 시각); Queued 레인은 `createdAt` 내림차순; failed 태스크 뷰에 `queue.error` 없음; `RunStatus` 의 `role="status"` 가 Open session 버튼과 PR 링크까지 감쌈; `lado-events` 는 `isTerminated === true` 만 봄(AO 가 복구한 실행은 Failed 로 남고, LADO 와 무관한 세션 종료에도 태스크를 다시 읽음); `run_live`·`LADO_ISSUE_NOT_FOUND`·복구 거부(`LADO_EXECUTION_FIXED` + `reason`)·PR 뒤 시간 상한 문구 없음; 브라우저에서 모드를 끌 때 `LADO_PRESENCE_UNAVAILABLE` 은 "승인하려면 데스크톱 앱이 필요" 문구; `useLadoQueue`·`fetchLadoQueue`·`ladoQueueQueryKey/Root` 는 쓰는 곳이 없는데 `lado_task` 이벤트와 모드 변경마다 queue 키를 무효화.
- 새 단언이 먼저 실패함을 확인(빨강, 이유 확인): LadoLanes 3(시계 시각, enqueuedAt 순서, `run_live` 줄), LadoExecutionPanel 3, LadoTaskDialog 4(status 역할, 오류 줄 2, base 안내), lado-errors 1(복구 문구 — 맵을 잠시 빼고 확인), event-transport 3, lado-presence 2, LadoProjectSection 1.

## After — 수행 후 실측

- 커밋 2개 + 이 엔트리:
  - `f0c379dd6` `fix(lado): review fixes for the queue and run screens` — 렌더러·메인·8개 로캘·트레이 카탈로그·테스트 27 파일.
  - `9638b25ce` `docs(lado): record the step 6 review fixes` — TDD, LADO.md, LADO.ko.md, DESIGN.md.
- F1: 앱 도우미 `formatResetTime`(설정의 Codex 계정 화면이 쓰는 것: 오늘이면 시:분, 아니면 날짜와 시:분)을 재시도 줄(`lado-task-status.ts`)과 패널 마감(`LadoExecutionPanel.tsx`)에 씀. en "Trying again at 02:32 PM", "Stops at 06:05 PM", 날짜가 다르면 "Stops at Oct 27, 08:00 AM". 키는 그대로(`lado.lane.retryAt`, `lado.run.stops`)이고 en·es·fr·de·pt-BR 문장만 바꿈(ko·ja·zh-CN 은 시계 시각에도 맞음). ko 는 조사 없이 "{{time}} 다시 시도", "{{time}} 종료". 쓰는 곳이 없어진 `ladoTimeUntil` 과 그 테스트, `ladoTaskReason` 의 `now` 인자 삭제.
- 패널: 세션에 PR 이 있으면(상태 무관) 마감 대신 "A PR was opened, so the time limit no longer applies"(`lado.run.prOpen`, ko "PR이 열려 시간 상한을 적용하지 않습니다"). 시간 초과가 먼저이고, 세션이 끝났거나 마감이 없으면 줄이 없다.
- F2: `RunStatus` 는 failed 이고 `queue.error` 가 있으면 상태 줄 아래에 `truncate pl-3.5 text-settings-muted` 한 줄(`title` 전체, `data-testid="lado-run-error"`). `ladoQueueErrorText` — 앞의 코드가 `LADO_ISSUE_NOT_FOUND` 면 번역(`lado.error.issueNotFound`), 그 밖은 데몬 문장 그대로(코드에 따라 details 가 필요한 문구가 있어 일반 번역은 하지 않음). blocked `base_ref_mismatch` 의 태스크 뷰 안내는 `lado.view.baseRefMismatch`("… Use Request changes to have the plan fixed.", ko "… 수정 요청으로 계획을 고치세요."); 카드 둘째 줄은 짧은 이유 그대로.
- F3: `orderQueue` — `Date.parse(queue.enqueuedAt)` 오름차순. 데몬은 소수 초 끝의 0 을 지우므로 문자열 비교는 틀린다(`10:00:00.5Z` < `10:00:00Z`) — 테스트가 이 쌍으로 고정. `useLadoQueue`·`fetchLadoQueue`·`ladoQueueQueryKey`·`ladoQueueQueryRoot` 와 그 무효화(모드 변경, `lado_task` 이벤트), 관련 테스트 2개 삭제. API 경로와 `LadoQueueItem`·`LadoQueueReason` 타입은 둠.
- F4: `createLadoEventBatch(queryClient)`(event-transport.ts 한 줄). `session_updated` 의 `isTerminated` 가 보드 태스크 목록(`ladoTaskForSession`) 또는 그 세션의 실행 패널 캐시가 보이는 `sessionEnded` 와 다르면 그 프로젝트의 태스크와 그 세션의 실행을 무효화. 끝난 실행(true)과 AO 가 복구한 실행(false)은 갱신, 살아 있는 실행의 활동과 어느 쪽도 실행으로 보이지 않는 세션은 갱신 없음.
- S14 문구: `lado.reason.run_live`("An earlier run of this task is still going"/"이 작업의 이전 실행이 아직 돌고 있음"), `lado.error.issueNotFound`, `LADO_EXECUTION_FIXED` 의 `details.reason` → `lado.error.restoreTimedOut`·`lado.error.restoreCancelled`(이유가 없거나 모르는 이유면 기존 `executionFixed`), `lado.run.prOpen`.
- 니트: `role="status"` 는 점·상태·이유를 감싼 span 에만(버튼·링크는 그 옆 형제). 모드 끄기 대화상자 `Project ID`·`프로젝트 ID`(소문자였던 것은 en·ko 둘) + 결과 문장 "After that, this project's workers can start without an approved plan."/"그 뒤로 이 프로젝트의 워커는 승인한 계획 없이도 시작할 수 있습니다."(8개 로캘) → `node scripts/sync-tray-catalog.mjs`. ko `lado.error.taskRunning` 의 "머지" → "병합". 브라우저에서 끌 때의 `LADO_PRESENCE_UNAVAILABLE` 은 `lado.project.modeOffUnavailable`("Turning LADO mode off needs the AO desktop app. Open the app, then turn it off there.") — 프로젝트 LADO 페이지에서만(`capErrorText` 와 같은 꼴).
- 문구: 새 키 7개(`lado.reason.run_live`, `lado.run.prOpen`, `lado.view.baseRefMismatch`, `lado.error.issueNotFound`, `lado.error.restoreTimedOut`, `lado.error.restoreCancelled`, `lado.project.modeOffUnavailable`) × 8개 로캘, 바꾼 키 4개(`lado.lane.retryAt`, `lado.run.stops`, `lado.presence.modeOffDetail`, ko `lado.error.taskRunning`). 로캘 파일은 줄 단위로만 고쳐(스크립트) 순서·들여쓰기 그대로.
- 테스트: 필수 6개 파일에 단언·케이스 추가(시계 시각은 `vi.useFakeTimers({ toFake: ["Date"] })` + 로컬 시각으로 만들어 시간대와 무관), `LadoProjectSection.test.tsx` 1개, `lado.test.ts`·`event-transport.test.ts` 에서 삭제한 함수·동작의 테스트 정리.
- 문서: TDD §5.3 As built — Pass(설정 읽기 실패 때, Sweep 위치), Admission(B1 json_each 로 상한 찬 프로젝트 제외·`slot_project` 한 번 쓰기·상한 못 읽은 프로젝트 제외, B2 잡을 때부터 리스), 검사 1(`run_live`), 검사 2(1·2·5·15·60분, 메모리 계수, `LADO_ISSUE_NOT_FOUND`), Spawn(표시 이름 100자), Link·Recovery(B3 남의 세션 미채택, B5 리스 유지), 새 Sweep(`execution.stopped`)·Restore 항목, 최대 실행 시간(PR 이 있으면 적용 안 함, 소유자 결정 2026-10-26 — "merged or closed PR does not lift the deadline" 문장 삭제), Daemon(시작 안전 정리 뒤; 영속 채팅 호스트·배경 세션·런타임 정리는 리스너가 뜬 뒤). 알려진 틈: 고친 3개를 지우고 S14 회고의 남은 틈 4개를 더함. 같은 사실이 걸린 곳도 고침: §1.2(daemon 행의 같은 문장, event-transport 행), §4.3(렌더러 갱신 규칙, queue 갱신 삭제), §6.2(`lado:` 예약 한 문장, 표의 복원 행), §13(`enqueuedAt`, 복구 거부·`LADO_ISSUE_NOT_FOUND`·`run_live`), §15(결정 g 에 "PR 이 열리면 멈춤", 리뷰 반영 한 줄). 플랜 문서는 고치지 않음. LADO.md·LADO.ko.md 3곳씩. DESIGN.md 결정 로그 1행.
- 확인: 필수 6개·i18n 2개·LADO 렌더러 테스트 통과, 전체 vitest 520 파일 8632 통과·7 건너뜀(97 s), `npm run typecheck` 깨끗(6분 52초), `typecheck:e2e` 깨끗(8 s).
- 독립 리뷰(Sonnet 서브에이전트, 약 2.5달러): 블로커·메이저 0. 문서의 백엔드 주장(시작 순서, `execution.stopped` payload, PR 이 있으면 마감 건너뜀, 1·2·5·15·60분, `AuthorizeRestore` 훅 위치)을 코드와 대조해 맞음. 마이너 1 — `enqueuedAt` 이 없는 옛 데몬의 `approved` 태스크는 정렬 값 0(맨 앞). 옛 데몬은 queue 를 보내지 않고 새 데몬은 `approved` 를 보내지 않아 둘이 섞이지 않으며, 모두 0 이면 안정 정렬로 받은 순서가 된다 — 고치지 않음.
- 게이트(HEAD `9638b25ce`, 깨끗한 트리, `--only` 로 나눠 실행): `GATES=10 MET=10 UNMET=0`. 이 엔트리 커밋 뒤 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: `ladoTimeUntil` 삭제(F1 뒤 쓰는 곳 없음), `ladoQueueQueryKey/Root` 와 그 무효화 삭제(F3 의 훅과 한 몸), `ladoQueueErrorText`, `LadoProjectSection` 테스트 1개, TDD §1.2·§4.3·§6.2·§13·§15 의 같은 사실, DESIGN.md 결정 로그 1행(2026-10-23 행의 "Trying again in 2 min." 예시를 대신함).
- 엔트리와 다르게 한 것:
  - F4 조건: "`isTerminated` 가 있고 보드가 아는 실행이면" 에 "보이는 종료 상태와 다를 때만" 을 더했고, 세션의 실행 패널 캐시도 "아는 실행" 으로 봤다. 모든 세션 갱신이 `isTerminated` 를 싣고 오므로(활동이 바뀔 때마다), 엔트리 문장 그대로면 살아 있는 실행의 턴마다 태스크 목록과 실행을 다시 읽는다. 패널을 넣은 것은 보드가 최신 실행만 알기 때문(옛 실행의 패널도 끝남·복구를 따라감). 그 결과 LADO 가 모르는 세션의 종료는 이제 아무것도 다시 읽지 않는다(전에는 늘 다시 읽음).
  - ko 시계 문구는 조사를 붙이지 않았다("{{time}} 다시 시도", "{{time}} 종료") — 플랜 §0 "자리표시자 바로 뒤 조사 금지" 규칙을 엔트리의 예시("14:32에", "18:05까지")보다 앞세웠다.
  - 시계 시각 형식은 `formatResetTime` 그대로라 en 이 "02:32 PM"(시 두 자리)이다. 앱의 다른 화면(Codex 계정의 초기화 시각)과 같게 두었다.
- 하지 않은 것(엔트리대로·기록):
  - `RunsSection` 의 동시 변경 덮어쓰기: 설정이 세 값을 함께 PUT 하므로, 두 창에서 거의 동시에 바꾸면 나중 저장이 다른 창의 변경을 자기 화면의 값으로 덮는다. 한 사람의 데스크톱이라 둔다.
  - 랩(실제 렌더러) 확인은 하지 않았다 — jsdom 단위 테스트만. 바뀐 배치는 `RunStatus`(한 줄 → 줄 + 그 아래 오류 줄)뿐이고 새 버튼 글자는 없다. 실제 화면에서 오류 줄의 잘림과 들여쓰기(`pl-3.5`)를 볼 일이 남았다.
  - `daemon.go` 의 주석 "after the startup reconcile above has settled the sessions" 는 백엔드 코드라 두었다(문서는 고침).
- 남은 틈(기록만):
  - 대기 중인 항목의 다음 시각은 보이지 않는다(`retry` 만 시각). `run_live`·`issue_unreadable` 은 이유만 보인다.
  - 태스크 뷰의 오류 줄은 LADO 가 직접 쓰는 `LADO_ISSUE_NOT_FOUND` 만 번역한다. 그 밖의 코드(스폰·RunInput 의 AO 오류)와 고정 영어 문장("another session holds…", "the dispatch stopped before the worker started")은 데몬 문장 그대로다.
  - F4 는 렌더러가 캐시한 상태와 비교하므로, 보드도 패널도 그 실행을 캐시하지 않았으면(프로젝트 화면을 연 적 없음) 아무것도 갱신하지 않는다 — 열 때 새로 읽으니 해는 없다.
  - S14 회고의 백엔드 틈(복구 오류가 모든 프로젝트의 claim 을 막음, 감사 쓰기 실패 뒤 재기록 없음, 재승인 뒤 옛 실행 복구는 정리가 끔, 삭제된 이슈의 `stateReason` 은 `attempts_exhausted`)은 TDD §5.3 알려진 틈에 옮겨 적었다.
- 비용: 독립 리뷰가 약 2.5달러(19만 토큰, 10분)로 이 단계의 가장 큰 단일 항목이었다. 범위를 diff 와 대조할 백엔드 파일로 좁혀 맡긴 것은 S13(6달러)보다 나았다.
- 다음 단계 메모(체인 끝 — 6단계 PR 전):
  - 체인 `lado-queue-fixes` 는 이 단계로 끝난다. PR(대상 `LJY981008/agent-orchestrator-fork`, `--repo` 필수)은 소유자가 연다. AGENTS.md 대로 CI 작업을 로컬에서 먼저 돌릴 것 — 렌더러 타입체크는 이 기계에서 약 7분, 전체 vitest 약 1.6분.
  - 머지 뒤 별도 커밋이 `LADO.md`·`LADO.ko.md` 6행을 Merged·병합됨으로 바꾼다(S13 메모).
  - 실제 데스크톱 앱에서 볼 것: 모드 끄기 대화상자의 새 문장(트레이 카탈로그), 실패 태스크 뷰의 오류 줄, 패널의 "PR 이 열려 시간 상한을 적용하지 않습니다".
