# 2026-10-23 LADO 대기열·디스패처 — S08 디스패처: 상한·메모리·재시작 복구·시간 상한·베이스 기록

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 e·f·g·h·l, §7) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

디스패처를 실제 사용에 안전하게 만든다: 동시 실행 상한(전체·프로젝트)과 메모리 보류, 데몬 재시작 뒤 잡혀 있던 항목의 복구(게이트 13 — 세션은 하나, 재시작 뒤 연결), 최대 실행 시간 초과 시 종료, 베이스 이동 기록, LADO 모드가 꺼진 프로젝트의 대기 표시. 태스크 취소가 쓸 `Killer` 도 여기서 구현한다.

## 범위

- 고친다: `backend/internal/service/ladoqueue/`(`Tick` 의 복구·마감·상한 자리 채우기, 베이스 기록, Killer), store(`SetLadoQueueWaiting` 와 감사 추가에 필요한 것만 — 아래 3·6), 필요하면 쿼리(ASCII, `npm run sqlc`, gen 직접 읽기).
- 만든다: `ladoqueue/admission.go`(상한·메모리 판정, tight 임계값), `ladoqueue/recovery.go`, `ladoqueue/deadline.go`, `ladoqueue/git.go`(`RevParse`·`CountCommits` — `ladotask/git.go` 의 꼴, 시스템 git, 프롬프트 끔), 테스트.
- 하지 않는다: HTTP·데몬 연결(S09), 렌더러.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `Tick(ctx, now)` 최종 순서: `recover` → `stopOverdue` → 설정 읽기(`LadoDispatchSettings`) → LADO 모드 꺼진 프로젝트의 열린 항목에 대기 표시 `lado_mode_off` → 메모리 판정(보류 켜짐 + tight → 열린 항목 전부 `memory_tight` 표시하고 이번 틱은 잡지 않음) → 전체 여유(`settings.MaxConcurrent - total`) 가 0 이면 열린 항목에 `slot_global` 표시하고 잡지 않음 → 하나씩 잡기: 잡은 항목의 프로젝트가 상한(`LadoProjectMaxConcurrent`)이면 `HoldLadoQueueEntry(slot_project, now+Interval)` 하고 다음 항목을 잡는다(이번 틱에 띄운 것도 센다), 여유가 다 차거나 잡을 것이 없을 때까지(안전 상한 반복 50).
2. tight 판정(`admission.go`, product UI 와 같은 표): `psi` → Raw > 20, `memorystatus` → Raw >= 4, `available_pct` → Raw > 90. `ErrUnsupported`·오류 → tight 아님(로그 한 번).
3. store `SetLadoQueueWaiting(ctx, reason string, projectID *domain.ProjectID) (int64, error)`: 열린 + 리스 없는 항목의 `waiting_reason` 만 쓴다(시도 수·next_attempt_at 그대로; 이미 같은 이유면 쓰지 않아 CDC 가 매 틱 울리지 않게). 잡기(`ClaimLadoQueueEntry`)는 `waiting_reason` 을 비운다(S01 쿼리 그대로).
4. 복구(`recovery.go`, 게이트 13): `LeasedLadoQueueEntries` 의 각 항목에 대해 `GetSessionByClientRequestID(lado:<entry>/<attempt>)`(store 의 기존 메서드 — Deps 인터페이스에 더한다):
   - committed 세션이 있음 → `LinkLadoExecution`(시작 시각 = 세션 생성 시각, 마감 = 그 시각 + 설정 분) — 리스가 만료됐어도 같은 토큰으로 연결(store 가 토큰만 본다).
   - 세션 없음 + 리스 만료 → `ReleaseLadoQueueEntry(retry, now)`.
   - uncommitted 세션 + 리스 만료 → release(AO 의 startup health 가 그 세션을 정리한다; 다음 시도는 새 id 라 막히지 않는다).
   - 리스 유효 → 그대로(다른 dispatch 가 진행 중일 수 있다 — 같은 프로세스 안에서는 dispatch 가 끝난 뒤에 Tick 이 돈다).
5. 시간 상한(`deadline.go`): `LadoExecutionsPastDeadline(now)` 각각 `Sessions.RequestKill(ctx, sessionID)` → `MarkLadoExecutionTimedOut(sessionID, now)` + 감사 `execution.timed_out`(actor `lado`, payload task·session·deadline). kill 오류면 표시하지 않고 다음 틱에 다시.
6. 베이스 기록: dispatch 검사 4 직후 프로젝트 저장소 경로에서 `RevParse(dir, base_ref)` → `dispatch_base_sha`, `CountCommits(dir, plan base_sha, dispatch sha)` → `base_commits_ahead`(실패 → 빈 값/NULL, 막지 않음). `LinkLadoExecution` 의 실행 값에 넣는다. 저장소 경로는 이슈 저장소(워크스페이스면 그 자식 저장소)의 로컬 경로 — 4b 의 `base_sha` 를 구할 때 쓴 경로와 같은 방법(`ladotask` 의 리비전 핀 코드를 읽고 따른다).
7. Killer: `(s *Service) Stop(ctx, sessionID) error` → `Sessions.RequestKill`. 태스크 서비스가 쓴다(S06 의 Deps.Killer — 연결은 S09).
8. 재시작 결함 테스트(게이트 13)는 **실제 store**(`sqlitetest.MustOpen`)로: 태스크·승인·스냅숏·항목을 store 메서드로 심고, 가짜 Sessions 의 Spawn 이 client request 세션을 만들고 커밋(store 의 기존 메서드)한 뒤 성공을 돌려주게 하고, 첫 디스패처의 `LinkLadoExecution` 을 한 번 실패시키는 store 래퍼로 "연결 직전 크래시"를 만든다. 새 디스패처 인스턴스로 `Tick` → 세션 하나, 실행이 연결됨, Spawn 호출 1번.
9. 커밋 메시지 예: `feat(lado): admission, restart recovery and run time limits in the dispatcher`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S08-dispatcher-recovery.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestAdmissionHoldsAtTheCaps` — 전체 상한(대기 표시 slot_global, 잡지 않음), 프로젝트 상한(hold slot_project, 다른 프로젝트 항목은 잡힘), 보류는 시도 수를 쓰지 않는다
- `TestMemoryTightHoldsWhenEnabled` — tight + 켜짐 → memory_tight 표시·잡지 않음; 꺼짐 → 무시; 지원 안 됨 → 무시
- `TestMemoryTightThresholds` — psi·memorystatus·available_pct 경계값 표
- `TestRecoveryLinksASpawnedSession` — 게이트 13: 실제 store, 연결 직전 크래시 → 재시작 뒤 세션 하나·실행 연결·Spawn 1번
- `TestRecoveryReleasesAnExpiredClaim` — 세션 없는 만료 리스 → retry 로 놓기, 유효한 리스는 그대로
- `TestOverdueRunsAreStopped` — 마감 지난 실행 → RequestKill·timed_out·감사; kill 실패 → 표시 없음
- `TestDispatchRecordsTheBaseMove` — 계획 base_sha·실행 시점 SHA·앞선 커밋 수가 실행에 기록; git 실패 → 빈 값이고 막지 않음
- `TestModeOffProjectsWait` — LADO 모드 꺼진 프로젝트 항목은 잡히지 않고 lado_mode_off 표시, 다시 켜면 잡힌다

## 수용 게이트

- [ ] S08-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S08-G1: 필수 테스트 8개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/service/ladoqueue/ TestAdmissionHoldsAtTheCaps TestMemoryTightHoldsWhenEnabled TestMemoryTightThresholds TestRecoveryLinksASpawnedSession TestRecoveryReleasesAnExpiredClaim TestOverdueRunsAreStopped TestDispatchRecordsTheBaseMove TestModeOffProjectsWait
  EXPECT: GO_TESTS_OK 8/8
- [ ] S08-G2: 디스패처 패키지 전체가 race 로 통과(S07 테스트 포함)
  CHECK: bash .claude/gate/lado-queue.sh go-race ./internal/service/ladoqueue/
  EXPECT: GO_RACE_OK
- [ ] S08-G3: LADO store 테스트 통과
  CHECK: cd backend && go test -count=1 -run 'Lado' ./internal/storage/sqlite/store/ && echo S08_LADO_STORE_OK
  EXPECT: S08_LADO_STORE_OK
- [ ] S08-G4: sqlc 재생성이 커밋과 같고 쿼리 ASCII
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean && bash .claude/gate/lado-queue.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S08-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S08-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `dc0c7d9ec`(`docs(lado): record S07`), 트리 깨끗. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1, Node 24.21.0. `defs-unchanged` → `DEFS_UNCHANGED`.
- 출발점: `Tick` 은 설정 → `CountLiveLadoExecutions`(합계만) → `MaxConcurrent − taken` 만큼 한 번에 claim. 복구·마감·프로젝트 상한·메모리·모드 꺼짐 표시 없음(자리 주석 2곳). store 에 `SetLadoQueueWaiting` 없음, `MarkLadoExecutionTimedOut(ctx, id, at)` 은 감사 없이 한 행만 쓴다. `Deps` 에 Memory·Git 없음, `Stop` 없음.
- 앞 단계 메모 중 이 단계 몫: S01(claim 은 `waiting_reason` 을 비움, hold 는 attempt −1, 시각은 UTC), S04(`LeasedLadoQueueEntries` 는 만료·유효 리스를 `claimed_at` 순으로, 복구 link 는 지금 토큰으로 — 같은 세션이면 nil, `MarkLadoExecutionTimedOut` 의 false 로 감사 중복 방지, 마감 목록은 PR 이 머지된 실행도 준다 — 끌지 S08 이 정함, `SetLadoQueueWaiting` 은 같은 이유면 쓰지 말 것), S06(Killer = `Stop(ctx, sessionID) error`, 마감 kill 이 세션 종료까지 가게), S07(자리 주석, 하나씩 잡기, `hold`·`handBack`, `workerBase` 가 dir 도 돌려주게, `RevParse(dir, base.BaseRef)`, 스폰 오류 뒤 `GetSessionByClientRequestID`, Killer 는 `RequestKill` 하나).
- AO 쪽(코드로 확인):
  - `CreateClientRequestSession` 이 uncommitted 행을 만들고, 동기 스폰은 런타임 시작·프롬프트 전달 뒤, Chat 비동기 스폰은 `beginAsyncChatSpawn` 이 돌아온 직후 `CommitClientRequestSession` 한다(`manager.go:1284·1401·1576`). committed = AO 가 시작을 받아들였다.
  - startup health(`startup_health.go`)는 워크스페이스 없는 시드 행과 setup 중 끊긴 client 요청만 되돌린다. Ready 상태로 commit 직전에 데몬이 멈춘 uncommitted 세션은 그대로 둔다(런타임이 살아 있으면 유지) — 엔트리 4 의 "startup health 가 정리한다" 는 일부만 맞다(After 결정 3).
  - `RequestKill` 은 종료 의도를 저장한 뒤 돌아온다(`manager.go:2556`) — kill 이 성공하면 세션은 끝난 것으로 세고 슬롯이 그 틱에 빈다.
  - PR 머지 때 AO 는 `TerminateOnPRMerge` 세션을 에이전트가 쉬면 끝낸다(`lifecycle/reactions.go:86`).
  - `usage.MemoryReader.Pressure` → `procmem.Pressure{Raw, Source}`, 지원 안 되면 `procmem.ErrUnsupported`. product UI 표(`resource-presentation.ts` `pressureStateFromRaw`): psi > 20, memorystatus ≥ 4, 그 밖의 소스는 available_pct 표(> 90).

## After — 수행 후 실측

- 코드 커밋 `35ebf3c1d` `feat(lado): admission, restart recovery and run time limits in the dispatcher` — 20 파일, +1877 −139.
  - `service.go`: Store 에 `LadoProjectMaxConcurrent`·`SetLadoQueueWaiting`·`LeasedLadoQueueEntries`·`GetSessionByClientRequestID`·`LadoExecutionsPastDeadline`·`MarkLadoExecutionTimedOut`; 새 의존 `Memory`(`Pressure` — `*usage.MemoryReader`)·`Git`(nil → 시스템 git). `Tick` = 설정 → `recover` → `stopOverdue` → (복구가 깨끗할 때만) `admit`. `Stop(ctx, sessionID)` = `RequestKill`(`ladotask.Killer` 컴파일 단언).
  - `admission.go`: `admit` — `SetLadoQueueWaiting(lado_mode_off, nil)` → 보류 켜짐 + tight → `memory_tight` 표시, 잡지 않음 → `MaxConcurrent − total` ≤ 0 → `slot_global` 표시, 잡지 않음 → 한 번에 하나씩 claim(최대 50): 프로젝트 상한(틱마다 한 번 읽어 둠)이면 `hold(slot_project, now+Interval)`, 아니면 `dispatch`; 워커가 시작되면 여유 −1·그 프로젝트 +1. `tightPressure` 는 UI 표 그대로(알 수 없는 소스도 UI 처럼 available_pct 표). 읽기 실패·지원 안 됨·Memory 없음 → tight 아님, 로그 한 번(`atomic.Bool`; ErrUnsupported 는 Info, 나머지 Warn). 설정이 꺼져 있으면 읽지도 않는다.
  - `recovery.go`: 리스 항목마다 `GetSessionByClientRequestID(lado:<entry>/<attempt>)` — committed → `relink`(StartedAt = 세션 생성 시각, 마감 = + 설정 분, 베이스 이동은 모름 → 빈 값·NULL, 뒤에 `stopIfUnwanted`), 리스 유효·만료 무관; 리스 유효 → 그대로; 만료 → (uncommitted 이고 끝나지 않았으면 `RequestKill`, best effort) → `abandon`: 시도 < 3 → `Release(retry, now)`, 시도 3 → `Fail("the dispatch stopped before the worker started")`.
  - `deadline.go`: 마감 지난 실행마다 `RequestKill` → 성공 → `MarkLadoExecutionTimedOut(id, event)`(시각 = kill 뒤 시계, actor `lado`, action `execution.timed_out`, subject `execution`/세션 id, payload `{task, session, deadline}`); 실패 → 로그만(표시 없음, 다음 틱에 다시).
  - `dispatch.go`: `dispatch` 가 `(started bool, error)` — 시작된 워커가 슬롯을 차지한다. 검사 4 직후 `baseMove`: `RevParse(dir, base.BaseRef, 없으면 base_ref)` → `CountCommits(dir, rev.BaseSHA, sha)`; 실패·계획 base 없음 → 빈 값·nil, 막지 않음. 검사 5 자리는 "admit 에서 끝남" 주석. 스폰 오류 → 같은 id 의 세션이 committed 면 그 세션을 link. `link` 는 실행 값(세션·시작·베이스)에 항목 값과 마감을 채운다; 성공 로그·`stopIfUnwanted` 는 호출자 몫.
  - `git.go`: `execGit` — `rev-parse --verify --quiet --end-of-options <ref>^{commit}`, `rev-list --count --end-of-options A..B --`(A·B 는 40/64 hex 만), 호출마다 10 s, `GIT_TERMINAL_PROMPT=0`·`GCM_INTERACTIVE=Never`.
  - store: 쿼리 `SetLadoQueueWaiting`·`SetLadoQueueWaitingModeOff`(:execrows; 열린 + `lease_token IS NULL` + `waiting_reason <> reason` + 프로젝트 필터 + 모드 켜짐 `IN`/꺼짐 `NOT IN`). `Store.SetLadoQueueWaiting(ctx, reason, projectID)` — `lado_mode_off` 는 모드 꺼진 프로젝트, 다른 이유는 켜진 프로젝트, 빈 이유 거부. `MarkLadoExecutionTimedOut(ctx, sessionID, event)` 한 트랜잭션(행이 바뀔 때만 감사). `npm run sqlc` 재생성 후 gen 을 읽음(ASCII). UPDATE 안의 서브쿼리 때문에 sqlc 가 "column reference project_id is ambiguous" 를 내서 `lado_queue_entries.project_id` 로 한정했다.
  - 테스트: 필수 8개 + `TestDispatchLinksTheSessionOfAFailedSpawn`·`TestStopStopsTheSession`·`TestGitCountsOnlyBetweenCommitIDs`, store `TestSetLadoQueueWaiting`, `TestLadoExecutionsPastDeadline` 에 감사 단언, `TestDispatchKeepsTokensOut` 에 git 사례. 실제 store 하네스 `storefixture_test.go`(프로젝트·LADO 모드·태스크·plan request·리비전·승인+스냅숏+항목을 store 메서드로 심고, 스폰은 client request 세션을 만들어 커밋; `crashBeforeLink` 래퍼). `TestDispatchRecordsTheBaseMove` 는 실제 store + 실제 git 저장소. S07 테스트 2개 조정: `TestDispatchSpawnsAnApprovedTask`(실행에 베이스 값), `TestTickClaimsWithinTheGlobalCap`(하나씩 → limits `[1 1 1 1]`, 프로젝트 상한 8, `slot_global` 단언).
- 엔트리·플랜과 다르게 하거나, 문장이 정하지 않아 정한 것:
  1. 설정을 맨 먼저 읽는다: 복구 link 가 마감에 `MaxRunMinutes` 를 쓴다(엔트리 순서는 recover → stopOverdue → 설정).
  2. 복구에 문제가 하나라도 있으면 그 틱은 아무것도 잡지 않는다. claim 쿼리는 만료된 리스를 잡으므로 정리 못 한 리스가 워커를 숨기고 있을 수 있다. 저장소가 계속 실패하면 대기열이 서지만 그때는 디스패처도 일할 수 없다. 마감 처리는 그래도 돈다.
  3. 만료 + uncommitted + 끝나지 않은 세션은 `RequestKill`(best effort) 뒤 놓는다. 엔트리는 startup health 에 맡겼지만 Before 대로 Ready 세션은 정리되지 않는다(자동화 스케줄러도 같은 경우 kill). kill 실패는 로그만 — 대기열을 세우지 않는다.
  4. 만료 + committed 세션 없음의 마지막 시도(3)는 놓지 않고 실패시킨다. 놓으면 claim 이 4번째 시도를 센다(플랜 §1 h "세 번 뒤 Failed").
  5. 리스가 유효해도 committed 세션이면 link 한다(엔트리 4 의 "리스가 만료됐어도" + 게이트 13 의 연결 직전 크래시 뒤 곧 재시작). 같은 프로세스에서는 dispatch 가 끝난 뒤 Tick 이 돌아 진행 중인 스폰과 겹치지 않는다.
  6. `SetLadoQueueWaiting` 의 모드 구분: `lado_mode_off` 는 모드 꺼진 프로젝트의 항목만, 다른 이유는 켜진 프로젝트의 항목만. 안 그러면 `memory_tight`·`slot_global` 과 `lado_mode_off` 가 틱마다 서로 덮어 CDC 가 매 틱 울린다. 기한(`next_attempt_at`)은 거르지 않는다(엔트리 3) — `retry`·`issue_unreadable` 로 기다리던 항목도 `slot_global`·`memory_tight` 로 바뀐다(다음 시각은 그대로).
  7. `MarkLadoExecutionTimedOut(ctx, id, at)` → `(ctx, id, event)`: 표시와 감사가 한 트랜잭션(플랜 §4 "두 행 이상이면 한 트랜잭션", 다른 감사 쓰기와 같은 꼴, 시각은 이벤트의 것). 호출자 테스트 3곳(ladotask 2·ladopolicy 1) 수정.
  8. 마감은 PR 이 머지·닫힌 실행도 세션이 살아 있으면 끈다(S04 메모의 결정): 상한은 세션 수명을 묶고, 살아 있는 세션은 슬롯을 차지한다. 태스크 상태는 그대로다(`taskState` 에서 머지·닫힘이 timed_out 보다 앞).
  9. dispatch SHA 는 `base_ref` 가 아니라 AO 가 해석한 canonical `BaseRef`(예 `refs/remotes/origin/main`)로 읽는다(S07 메모) — 워커가 실제로 시작하는 ref. 없으면 `base_ref`. 설정된 브랜치는 AO 스폰이 이 뒤에 fetch 하므로 기록이 한 fetch 늦을 수 있다(기록일 뿐).
  10. S07 의 둘째 틈(스폰 오류인데 AO 가 세션을 committed 로 남김)을 막았다: 스폰 오류 뒤 같은 id 의 세션을 찾아 committed 면 그 세션을 link.
  11. 프로젝트 상한을 읽지 못하면 그 항목을 되돌리고(시도 반환) 그 틱의 claim 을 멈춘다.
- 확인:
  - 필수 8개 PASS, 패키지 race PASS(20.7 s), `go test -run 'Lado' ./internal/storage/sqlite/store/` PASS, ladotask·ladopolicy·ladoproject·ladogate PASS. golangci-lint v2.13.2(오프라인 빌드 재사용) ladoqueue·store·ladotask·ladopolicy 0 issues, gofmt 없음, `go build ./... && go vet ./...` 통과.
  - 변이 25개 중 24개를 테스트가 잡음(복구의 committed link·유효 리스·마지막 시도·미완 세션 kill·release 시각·StartedAt, 복구 문제 때 claim 막기, 프로젝트 상한·이번 틱 카운트·hold 의 시도 반환, 메모리 설정·로그 한 번·임계값 3개, 모드 꺼짐 표시, `slot_global` 표시, 마감 kill 실패·payload, 베이스 값, 스폰 오류 link 2개, git 토큰, commit id 검사). 놓친 1개(계획 base SHA 가 없을 때 count 생략)는 결과가 같고 경고 로그만 달라 경고 단언을 더해 잡게 했다.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker·should-fix 없음. nit 2 — (a) 리스 만료 뒤 AO 가 늦게 commit 하면 옛 시도의 세션을 못 본다: 같은 프로세스에서는 스폰이 끝나야 다음 Tick 이 돌고, 크래시면 AO 스폰도 함께 죽어 늦은 commit 이 없다(그대로 둠). (b) 가짜 store 의 link 는 같은 세션 재연결의 멱등을 흉내 내지 않는다 — 그런 테스트가 없다(그대로 둠).
- 게이트(HEAD `35ebf3c1d`): `GATES=7 MET=7 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 복구 문제 때 claim 막기(After 2), 미완 세션 kill(3), 마지막 시도 실패(4), 스폰 오류 뒤 세션 찾기(10), store 테스트 1개와 보조 테스트 3개. 엔트리와 다르게 한 것: 설정 읽기 순서(1), `MarkLadoExecutionTimedOut` 의 인자(7), dispatch SHA 의 ref(9).
- 남은 틈(기록만):
  - 상한이 찬 프로젝트의 항목은 전체 여유가 있는 동안 매 틱 claim 되고 hold 된다(엔트리 절차 1 그대로). 항목마다 틱당 쓰기 2번(리스·시도·이유) → `lado_task` CDC 이벤트 2개. 기다리는 항목이 많으면 15 초마다 이벤트가 몰린다. 줄이려면 상한이 찬 프로젝트를 빼는 claim 쿼리가 필요하다(이번 범위 밖).
  - `slot_global`·`memory_tight` 표시는 기한이 안 된 항목(`retry`·`issue_unreadable`)의 이유도 덮는다(다음 시각은 남음).
  - 만료 리스의 uncommitted 세션 kill 이 실패하고 AO startup health 도 정리하지 않는 경우(Ready) 그 세션이 남아 다음 시도의 워커와 둘이 될 수 있다(로그로만 남김).
- 다음 단계 메모:
  - S09: `ladoqueue.New(Deps{..., Memory: <*usage.MemoryReader>, ...})` — `Git` 은 비워 두면 시스템 git. `Memory` 를 빠뜨리면 메모리 보류가 꺼진 것과 같고 "LADO cannot read memory pressure" 를 한 번 남긴다. 태스크 서비스의 `Killer` 는 `*ladoqueue.Service`(`Stop`). `Start` 는 곧바로 Tick 하므로 데몬 시작 직후 복구가 돈다(AO startup health 와 서로 막지 않음). 실행 상세 DTO 의 base 는 `PlanBaseSHA`·`DispatchBaseSHA`·`BaseCommitsAhead`(nil = 모름; 복구로 연결된 실행은 늘 모름). 대기열 DTO 의 `error` 는 실패한 항목에만 있다(복구가 실패시킨 항목은 `the dispatch stopped before the worker started`). 감사 `execution.timed_out`: actor `lado`, subject `execution`/세션 id, payload `{task, session, deadline}`.
  - S10·S12: 대기 이유는 `slot_global`·`slot_project`·`memory_tight`·`lado_mode_off`·`retry`·`issue_unreadable`. `slot_project` 항목의 `next_attempt_at` 은 다음 틱(15 s)이라 "retry at" 시각으로 보이면 안 된다 — 이유로만 보일 것. 상한이 찬 프로젝트에서는 `lado_task` 이벤트가 15 초마다 올 수 있다 — 무효화를 묶을 것(debounce).
  - S13: TDD §5.3 에 Tick 순서, 복구 규칙(After 2·3·4·5), 마감 규칙(8), 베이스 기록(9), 메모리 표, 모드 꺼짐 표시 규칙(6)을 적는다.
