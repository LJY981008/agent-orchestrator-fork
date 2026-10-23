# 2026-10-23 LADO 대기열·디스패처 — S10 렌더러 데이터 층·오류·이벤트, 데스크톱 모드 끄기 확인

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 l, §9, §10 마지막 두 항목) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

화면 단계(S11·S12)가 쓸 클라이언트 함수·훅·쿼리 키·오류 문구·이벤트 무효화를 만들고, LADO 모드 끄기를 데스크톱 앱 메인 프로세스의 네이티브 확인으로 서명하게 한다(승인·상한 변경과 같은 길 — ADR-0002).

## 범위

- 고친다: `frontend/src/renderer/lib/lado.ts`(클라이언트: `fetchLadoQueue`, `fetchLadoExecution`, `fetchLadoDispatchSettings`, `updateLadoDispatchSettings`, `updateLadoProjectSettings`(상한), `setLadoMode(projectId, on)` — 켜기는 직접 PUT, 끄기는 브리지 presence 먼저, 브라우저에서는 직접 PUT(개발 presence 일 때만 데몬이 받음); 쿼리 키; 타입 별칭), `hooks/useLado.ts`(훅), `lib/lado-errors.ts`(새 코드: `LADO_APPROVAL_ABOVE_CEILING`, `LADO_EXECUTION_FIXED`, `LADO_TASK_ALREADY_QUEUED`, `LADO_DISPATCH_SETTINGS_INVALID`, `LADO_SETTINGS_EMPTY`, `LADO_EXECUTION_NOT_FOUND` 와 이유 문자열 → 문구 키), `lib/lado-events.ts`(`lado_execution` → 그 실행과 프로젝트 태스크 무효화; 업스트림 `event-transport.ts` 는 필요하면 훅 한두 줄), 8개 로캘 파일(새 키 — 실제 번역), `frontend/src/shared/lado-presence.ts`·`frontend/src/main/lado-presence.ts`(새 채널 `lado:presence:setLadoMode`), `frontend/src/preload.ts`, 브리지 세 곳(`renderer/lib/bridge.ts`, `renderer/test/setup.ts`, `e2e/support/fake-bridge.ts`), 트레이 카탈로그(`node scripts/sync-tray-catalog.mjs` 로 재생성 — 메인 프로세스 확인 문구).
- S09 가 재생성한 `schema.ts` 의 새 상태 값 때문에 깨지는 타입이 있으면 여기서 고친다(화면 동작은 S12 — 여기서는 컴파일만).
- 하지 않는다: 설정·보드 화면(S11·S12).

## 절차

1. 기존 `approveLadoPlan`·`changeLadoCeiling` 의 브리지 우선 패턴을 따른다. `setLadoMode(projectId, false)`: `aoBridge.lado?.presence?.setLadoMode(projectId, false)` → `cancelled` 면 null(아무것도 안 바뀜), `error` 면 `LadoApiError`, 브리지가 없으면 직접 PUT.
2. 메인 프로세스(`lado-presence.ts`): `changeRule` 채널과 같은 구조로 — 데몬에서 프로젝트 설정을 직접 읽고(렌더러가 말하는 이름을 믿지 않는다), 앱 언어로 네이티브 확인("Turn off LADO mode for <project>? Queued tasks wait until it is on again; running workers keep running." 와 각 로캘 번역은 트레이 카탈로그로), 확인하면 `PUT /api/v1/lado/projects/{id}/settings {"ladoMode": false}` 를 presence 헤더로 서명. `on = true` 는 이 채널에서 거절(렌더러 직접 호출).
3. 새 문구 키는 `lado.` 접두로 기존 LADO 키 뒤에 추가: 오류 문구, 대기열 이유(`slot_global` 등), 실패 이유(`attempts_exhausted`·`timed_out`·`ended_without_pr`), 상태 이름(queued·dispatching·running·failed·completed). 한국어 용어: 대기열, 실행, 대기 중, 실행 중, 실패, 완료.
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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
