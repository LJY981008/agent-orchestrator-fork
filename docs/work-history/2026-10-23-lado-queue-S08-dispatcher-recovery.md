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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
