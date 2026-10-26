# 2026-10-23 LADO 대기열·디스패처 — S10 렌더러 데이터 층·오류·이벤트, 데스크톱 모드 끄기 확인

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 l, §9, §10 마지막 두 항목) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

화면 단계(S11·S12)가 쓸 클라이언트 함수·훅·쿼리 키·오류 문구·이벤트 무효화를 만들고, LADO 모드 끄기를 데스크톱 앱 메인 프로세스의 네이티브 확인으로 서명하게 한다(승인·상한 변경과 같은 길 — ADR-0002).

## 범위

- 고친다: `frontend/src/renderer/lib/lado.ts`(클라이언트: `fetchLadoQueue`, `fetchLadoExecution`, `fetchLadoDispatchSettings`, `updateLadoDispatchSettings`, `updateLadoProjectSettings`(상한), `setLadoMode(projectId, on)` — 켜기는 직접 PUT, 끄기는 브리지 presence 먼저, 브라우저에서는 직접 PUT(개발 presence 일 때만 데몬이 받음); 쿼리 키; 타입 별칭), `hooks/useLado.ts`(훅), `lib/lado-errors.ts`(새 코드: `LADO_APPROVAL_ABOVE_CEILING`, `LADO_EXECUTION_FIXED`, `LADO_TASK_ALREADY_QUEUED`, `LADO_DISPATCH_SETTINGS_INVALID`, `LADO_SETTINGS_EMPTY`, `LADO_EXECUTION_NOT_FOUND` 와 이유 문자열 → 문구 키), `lib/lado-events.ts`(`lado_execution` → 그 실행과 프로젝트 태스크 무효화; 업스트림 `event-transport.ts` 는 필요하면 훅 한두 줄), 8개 로캘 파일(새 키 — 실제 번역), `frontend/src/shared/lado-presence.ts`·`frontend/src/main/lado-presence.ts`(새 채널 `lado:presence:setLadoMode`), `frontend/src/preload.ts`, 브리지 세 곳(`renderer/lib/bridge.ts`, `renderer/test/setup.ts`, `e2e/support/fake-bridge.ts`), 트레이 카탈로그(`node scripts/sync-tray-catalog.mjs` 로 재생성 — 메인 프로세스 확인 문구).
- S09 가 재생성한 `schema.ts` 의 새 상태 값 때문에 깨지는 타입이 있으면 여기서 고친다(화면 동작은 S12 — 여기서는 컴파일만).
- 하지 않는다: 설정·보드 화면(S11·S12).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 기존 `approveLadoPlan`·`changeLadoCeiling` 의 브리지 우선 패턴을 따른다. `setLadoMode(projectId, false)`: `aoBridge.lado?.presence?.setLadoMode(projectId, false)` → `cancelled` 면 null(아무것도 안 바뀜), `error` 면 `LadoApiError`, 브리지가 없으면 직접 PUT.
2. 메인 프로세스(`lado-presence.ts`): `changeRule` 채널과 같은 구조로 — 데몬에서 프로젝트 설정을 직접 읽고(렌더러가 말하는 이름을 믿지 않는다), 앱 언어로 네이티브 확인("Turn off LADO mode for <project>? Queued tasks wait until it is on again; running workers keep running." 와 각 로캘 번역은 트레이 카탈로그로), 확인하면 `PUT /api/v1/lado/projects/{id}/settings {"ladoMode": false}` 를 presence 헤더로 서명. `on = true` 는 이 채널에서 거절(렌더러 직접 호출).
3. 새 문구 키는 `lado.` 접두로 기존 LADO 키 뒤에 추가: 오류 문구, 대기열 이유(`slot_global` 등), 실패 이유(`attempts_exhausted`·`timed_out`·`ended_without_pr`), 상태 이름(queued·dispatching·running·failed·completed). 한국어 용어: 대기열, 실행, 대기 중, 실행 중, 실패, 완료. `approved_before_queue` 문구는 "6단계 전 승인"이 아니라 "대기열에 없음 — 다시 승인하면 대기열에 듭니다" 뜻으로(S02 메모: 항목이 취소·대체된 경우에도 쓰인다). 새 오류 코드 `LADO_TASK_RUNNING` 도 문구를 단다.
4. 커밋 메시지 예: `feat(lado): renderer client for the queue and runs, and the desktop mode-off confirmation`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S10-client-presence.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/lib/lado.test.ts` — 새 클라이언트 함수(경로·본문), `setLadoMode` 의 켜기 직접 호출·끄기 브리지 우선·취소 시 null
- `src/renderer/lib/lado-errors.test.ts` — 새 코드와 이유 문자열이 모든 로캘에서 번역된다(키 문자열이 그대로 나오지 않음)
- `src/renderer/lib/event-transport.test.ts` — `lado_execution` 이벤트가 실행·태스크 쿼리를 무효화한다
- `src/main/lado-presence.test.ts` — 끄기: 프로젝트를 데몬에서 읽고 확인 대화상자 → 서명된 PUT; 취소 → 요청 없음; 켜기는 거절

## 수용 게이트

- [ ] S10-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S10-G1: 필수 테스트 파일 4개 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/lib/lado.test.ts src/renderer/lib/lado-errors.test.ts src/renderer/lib/event-transport.test.ts src/main/lado-presence.test.ts
  EXPECT: VITEST_OK
- [ ] S10-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S10-G3: 새 채널이 메인·preload·공유 정의에 있다
  CHECK: bash .claude/gate/lado-queue.sh has frontend/src/shared/lado-presence.ts 'lado:presence:setLadoMode' && bash .claude/gate/lado-queue.sh has frontend/src/preload.ts 'setLadoMode'
  EXPECT: HAS_OK
- [ ] S10-G4: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S10-G5: 백엔드 빌드·vet 유지
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S10-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `f4df87347`(`docs(lado): record S09`), 트리 깨끗. go1.27.1, Node 24.21.0. `gate-check --status` → 7건 UNMET(아직 안 돌림).
- 출발점: `lib/lado.ts` 에 대기열·실행·디스패치 설정 클라이언트가 없고 프로젝트 설정은 읽기(`fetchLadoProjectSettings`)뿐 — 모드·상한을 바꾸는 함수 없음. `lado-errors.ts` 에 6단계 오류 코드가 하나도 없고, 이유 문자열 16개의 문구도 없음. `lado-events.ts` 는 `lado_task`·`lado_rules` 만 알아서 `lado_execution` 은 일반 세션 경로(워크스페이스·대화·에디터 핸드오프 갱신)로만 흐름. 메인 프로세스 presence 채널은 `approvePlan`·`changeRule` 둘, 트레이 카탈로그의 LADO 키 12개.
- `npm run typecheck` 1건 실패 — `LadoProjectRulesSection.test.tsx:295` mock 에 `maxConcurrent` 없음(S09 메모 그대로). `npm run typecheck:e2e` 통과.
- 필수 테스트 4개 파일은 이미 있고 모두 통과: `lado.test.ts` 8, `lado-errors.test.ts` 2, `event-transport.test.ts` 47, `lado-presence.test.ts` 28(i18n 2개·tray-catalog 포함 7파일 114개 통과).
- 앞 단계 메모 중 이 단계 몫: S02(`approved_before_queue` 는 "대기열에 없음 — 다시 승인하면 대기열에 듦" 뜻), S03(`lado_execution` 은 `change_log.session_id` 를 채움), S05(`LADO_APPROVAL_ABOVE_CEILING`·`LADO_EXECUTION_FIXED` 는 409), S06(`LADO_SETTINGS_EMPTY`·`LADO_DISPATCH_SETTINGS_INVALID`(details `{min, max}`)·`LADO_TASK_ALREADY_QUEUED`·`LADO_TASK_RUNNING`(details `{taskId, state}`); 사라진 `LADO_MODE_REQUIRED`·`LADO_MODE_UNAVAILABLE` 는 렌더러 맵에 원래 없음), S08(대기 이유 6개, `lado_task` 이벤트가 15 초마다 올 수 있으니 무효화를 묶을 것), S09(타입체크 1건, 새 타입·오류 코드, enum union 으로 문구 키 빠짐을 타입으로 잡기).
- AO 쪽(코드로 확인):
  - presence 검증은 `r.URL.EscapedPath()` 와 받은 바이트 그대로(`lado_plans.go:121`), 모드 끄기 action 은 `project.lado_mode_off`·subject 는 프로젝트 id(`ladoproject/service.go:130`).
  - 프로젝트 설정 응답에는 이름이 없다(`projectId`·`ladoMode`·`maxConcurrent`). 이름은 `GET /api/v1/projects/{id}` 의 `project.name`(정상·degraded 둘 다 있음).
  - 프로젝트 id 알파벳 `^[A-Za-z0-9][A-Za-z0-9._-]*$`, `..` 거절(`project/service.go:1043`).
  - CDC: `lado_execution` 은 `session_updated` 타입, 봉투의 `sessionId`(세션 행이 있을 때)와 payload `{kind, sessionId, taskId, projectId}`. 세션 종료는 `session_updated` payload `isTerminated: true`, PR 은 `pr_created`·`pr_updated`·`pr_session_changed`(봉투에 그 세션·프로젝트). 세션이 끝나거나 PR 이 머지돼도 LADO 테이블은 바뀌지 않으므로 LADO 이벤트가 없다.

## After — 수행 후 실측

- 코드 커밋 `2df92cedd` `feat(lado): renderer client for the queue and runs, and the desktop mode-off confirmation` — 26 파일, +1268 −55. 리뷰 반영 `7f268a430` `fix(lado): refresh the run a moved PR leaves` — 2 파일, +33 −1.
  - `lib/lado.ts`: 타입 별칭 13개(`LadoTaskState`·`LadoTaskStateReason`·`LadoTaskQueue`·`LadoTaskExecution`·`LadoQueueItem`·`LadoQueueState`·`LadoQueueReason`·`LadoExecution`·`LadoExecutionPR`·`LadoDispatchSettings`·`LadoDispatchSettingsInput`·`LadoRunMinutes` 와 내부용 프로젝트 설정 입력), 쿼리 키(`ladoQueueQueryRoot`, `ladoQueueQueryKey(projectId?)` — 전체는 `""`, `ladoExecutionQueryKey(sessionId)`, `ladoDispatchSettingsQueryKey`), `setLadoMode`(켜기 직접 PUT, 끄기 브리지 먼저: cancelled → null, error → `LadoApiError`, 브리지 null → 직접 PUT), `updateLadoProjectSettings`(본문 `{maxConcurrent}` 만, 둘이 `putLadoProjectSettings` 공유), `fetchLadoQueue`(projectId 없으면 빈 query), `fetchLadoExecution`, `fetchLadoDispatchSettings`, `updateLadoDispatchSettings`(세 값 함께).
  - `hooks/useLado.ts`: `useSetLadoMode`(null 이면 아무것도 안 함; 성공하면 설정 캐시를 바꾸고 그 프로젝트 태스크·대기열 무효화), `useUpdateLadoProjectSettings`(설정 캐시), `useLadoQueue`, `useLadoExecution(sessionId, enabled)`, `useLadoDispatchSettings`, `useUpdateLadoDispatchSettings`(캐시).
  - `lib/lado-errors.ts`: 코드 14개 — 새 문구 8개(`taskAlreadyQueued`·`taskRunning`·`approvalAboveCeiling`·`executionFixed`·`executionNotFound`·`dispatchSettingsInvalid`·`settingsEmpty`·`trackerIntakeEnabled`), 메인의 `LADO_PROJECT_ID_INVALID` → `projectNotFound`, 읽기·저장 실패 5개 → `internal`. 세부값에 `min`·`ceiling`(AO 프로젝트 설정의 모드 이름으로). `ladoReasonText(t, reason)` — 맵 타입이 `Record<LadoTaskStateReason | LadoQueueReason, …>` 라 데몬이 이유를 더하면 타입체크가 깨진다; 없으면 "", 모르는 값은 그대로.
  - `lib/lado-events.ts`: `lado_execution` → 그 실행·프로젝트 태스크·태스크 뷰 무효화(LADO 전용 — 워크스페이스는 안 건드림), `lado_task` → 대기열 루트도. AO 의 세션 종료(`isTerminated: true`)·`pr_created`·`pr_updated`·`pr_session_changed` → 그 프로젝트의 LADO 태스크와 그 세션(PR 이 옮겨 갔으면 `fromSession` 도)의 실행 무효화, 워크스페이스 갱신은 그대로. `event-transport.ts` 는 1줄(`note(payload, decoded)`).
  - 메인 `lado-presence.ts`: 채널 `lado:presence:setLadoMode`. 신뢰 발신자 → 프로젝트 id(AO 알파벳·255자·`..` 거절, `LADO_PROJECT_ID_INVALID`) → `ladoMode === false` 만(`LADO_MODE_CHANGE_INVALID`) → `GET …/lado/projects/{id}/settings`(id·모드·상한 확인) → 이미 꺼져 있으면 대화상자 없이 `changed` → `GET /api/v1/projects/{id}`(id 확인, 이름은 `oneLine`, 비면 id) → 앱 언어 확인 → 서명한 `PUT …/settings {"ladoMode":false}` → 응답 확인. 렌더러가 보낸 다른 필드(이름·헤더)는 버린다.
  - `shared/lado-presence.ts`(채널·요청·결과 타입), `preload.ts`, 브리지 3파일(fake-bridge 는 객체 2개), `scripts/sync-tray-catalog.mjs` 키 +3 → `node scripts/sync-tray-catalog.mjs` 로 재생성.
  - 로캘 8개 × 32키: 오류 8, 이유 16(`lado.reason.<이유>`), 상태 5(`lado.task.state.queued·dispatching·running·failed·completed`), 확인 3(`lado.presence.modeOffTitle·modeOffDetail·turnOff`). 한국어 상태 이름 대기 중·시작하는 중·실행 중·실패·완료.
  - `LadoProjectRulesSection.test.tsx` mock 에 `maxConcurrent: 1`.
- 엔트리가 정하지 않았거나 다르게 적은 것:
  1. **세션 종료·PR 이벤트도 LADO 태스크와 실행을 갱신한다.** 실행 뒤의 상태(running·completed·failed·closed_unmerged)는 세션 종료 여부와 PR 에서 읽는데(결정 k), 그때는 LADO 테이블이 안 바뀌어 LADO 이벤트가 없다. 엔트리 문장대로(`lado_execution` 만)면 레인·보드 카드·실행 패널이 다른 이유로 다시 읽힐 때까지 "실행 중" 에 머문다. 살아 있는 세션의 activity 변화는 건드리지 않고, `invalidateQueries` 는 열린 쿼리만 다시 읽으므로 LADO 화면이 없으면 비용이 없다.
  2. `fetchLadoExecution` 은 404 `LADO_EXECUTION_NOT_FOUND` 를 `null` 로 돌려준다(S12 "404 면 패널 없음" 을 오류 상태 없이). 그 밖의 오류는 던진다. 코드의 문구도 맵에 있다.
  3. `LADO_TRACKER_INTAKE_ENABLED` 문구를 더했다 — S11 스위치가 켜기를 거절당하면 보일 데몬 문구이고, `setLadoMode` 자신의 거절이다.
  4. 메인의 거절 코드: `LADO_PROJECT_ID_INVALID`(`LADO_PLAN_REVISION_ID_INVALID` → planNotFound 선례대로 projectNotFound), `LADO_MODE_CHANGE_INVALID`(켜기·잘못된 값). 뒤의 것은 앱 코드로는 닿지 않아(`lib/lado.ts` 는 false 만 보냄) 문구 없이 영어 메시지 그대로 둔다.
  5. 대화상자: 제목 "Turn off LADO mode for {{project}}?"(이름은 데몬에서), 본문 첫 줄 "Project id: {{id}}" 를 더했다 — 표시 이름은 두 프로젝트가 같을 수 있고 id 는 렌더러가 고른다. 이름은 이슈 제목처럼 줄바꿈·방향 문자를 지운다. 나머지는 플랜 문장 그대로.
  6. 이미 꺼진 프로젝트는 대화상자 없이 `changed`(권한 상한의 같은 모드와 같은 규칙).
  7. 상태 이름은 키만 더했다(레인 매핑은 S12). es·fr·pt-BR 은 기존 "Aprobado·Approuvé·Aprovado" 에 맞춰 남성형.
  8. 이유 문구는 레인 둘째 줄용 짧은 구(마침표 없음). 대기 이유의 "다음 시도 시각"·"시도 n/3" 는 S12 몫이라 넣지 않았다.
- 확인:
  - 필수 4개 파일: `lado.test.ts` 19(8→19), `lado-errors.test.ts` 5(2→5), `event-transport.test.ts` 53(47→53), `lado-presence.test.ts` 35(28→35). i18n `instance`·`renderer-coverage`, `tray-catalog.test.ts` 통과.
  - 프런트엔드 전체 `npx vitest run`: 516 파일, 8581 통과·7 skip(리뷰 반영 전; 반영 뒤 event-transport 53 통과).
  - `npm run typecheck`(7분 7초)·`typecheck:e2e` 통과, `go build ./... && go vet ./...` 통과.
  - 변이 13개 모두 테스트가 잡음: 끄기가 브리지를 건너뜀, 취소가 PUT 으로 흐름, not-found 를 던짐, 메인이 켜기 허용, 메인이 렌더러의 이름을 씀, 이미 꺼짐 지름길 삭제, 다른 본문 서명, `..` 허용, `lado_execution` 무시, 모든 세션 갱신에 반응, 대기열 무효화 삭제, 상한 모드 이름 미번역, 코드 하나 삭제.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker 없음. should-fix 1 — `pr_session_changed` 의 봉투 세션은 새 세션뿐이라 PR 이 떠난 실행의 패널이 낡는다 → `fromSession` 도 무효화(`7f268a430`, 테스트 1개). nit 1 — `LADO_MODE_CHANGE_INVALID` 문구 없음 → 결정 4 대로 둠.
- 게이트(HEAD `2df92cedd`): 7건 모두 MET(G4 7분 7초). 리뷰 반영·이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 세션 종료·PR 이벤트 갱신(After 결정 1), not-found 의 null(2), intake 문구(3), 메인의 거절 코드 2개(4), 대화상자의 프로젝트 id 줄(5). 엔트리와 다르게 한 것은 없다.
- 남은 틈(기록만):
  - S08 메모의 묶기: 한 틱의 claim·hold 이벤트 쌍은 150 ms 창과 키별 중복 제거로 1~2번의 갱신이 된다. 그래도 상한이 찬 프로젝트에서 기다리는 태스크의 뷰를 열어 두면 15 초마다 뷰가 다시 읽히고, 뷰는 이슈를 트래커에서 다시 읽는다(분당 최대 4번). 뷰를 늦추면 대화상자의 상태 줄도 늦어져 여기서는 두었다 — 근본은 상한이 찬 프로젝트를 빼는 claim(S08 의 틈)이다.
  - 세션 종료·PR 이벤트는 세션으로 태스크를 모르므로 태스크 뷰는 무효화하지 않는다(태스크 목록·실행만).
  - 대기열 항목 `error`(`CODE: message` 꼴, S07)의 번역 도우미는 만들지 않았다 — 보일지는 S12 가 정한다.
  - 렌더러 `npm run typecheck` 가 이 기계에서 7분 걸린다(GATE_TIMEOUT 1800 안).
- 다음 단계 메모:
  - S11: `useLadoDispatchSettings()`·`useUpdateLadoDispatchSettings()`(세 값 함께 `mutate`, 성공하면 캐시가 바뀜, 오류는 `ladoErrorText(t, error)`), 분 선택은 `LadoRunMinutes` 타입. `useSetLadoMode(projectId)`: `mutateAsync(false)` 가 null 이면 취소(스위치는 켜진 채, 캐시 그대로), 켜기가 intake 때문에 거절되면 `LADO_TRACKER_INTAKE_ENABLED` 문구. 브라우저(dev-web)는 브리지가 null 이라 직접 PUT — 데몬이 `AO_LADO_DEV_PRESENCE=1` 이 아니면 `LADO_PRESENCE_REQUIRED`/`UNAVAILABLE` 문구. 상한은 `useUpdateLadoProjectSettings(projectId).mutate({ maxConcurrent })`. 셋 다 `useLadoModeState` 와 같은 캐시 키를 바꾸므로 보드가 따라온다. 테스트의 브리지 기본값(`test/setup.ts`)은 `setLadoMode: async () => null`.
  - S12: 이유는 `ladoReasonText(t, task.stateReason)`·항목 `reason`, 상태 이름 키 `lado.task.state.{queued,dispatching,running,failed,completed}`. 패널은 `useLadoExecution(sessionId, enabled)` — data `null` 이면 실행 아님(패널 없음), 오류는 오류; LADO 경로는 이 컴퓨터 데몬뿐이니 로컬 세션에서만 켠다. 실행 쿼리는 `lado_execution`·세션 종료·PR 이벤트로 갱신된다. 대화상자의 실행 상태 줄은 뷰가 아니라 `useLadoTasks(projectId)` 의 태스크에서 읽으면 세션 종료·PR 머지가 바로 보인다. "다음 시도 시각"·"시도 n/3" 문구는 S12 가 더한다.
  - S13: 모드 끄기 채널(검사 순서, 대화상자 문구와 id 줄, 이미 꺼짐 지름길, 켜기 거절, 메인 코드 2개)을 ADR-0002·TDD §9/§13 에, 렌더러 이벤트 규칙(세션 종료·PR → LADO 태스크·실행)을 TDD §4.3 에 적는다.
