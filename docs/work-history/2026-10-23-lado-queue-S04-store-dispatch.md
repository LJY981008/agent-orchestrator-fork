# 2026-10-23 LADO 대기열·디스패처 — S04 store: 디스패치 primitive·설정·대기열 목록

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 d·f·g·h·m, §2, §4 "Dispatcher primitives") · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

디스패처(S07·S08)가 쓸 저장 primitive 를 만든다: 리스 잡기·놓기·보류·표시·실행 연결·동시 실행 집계·마감 지난 실행·게이트 허가 읽기·설정. 재시작 안전성(게이트 13)은 이 primitive 의 원자성에 달려 있다.

## 범위

- 고친다/만든다: `backend/internal/storage/sqlite/store/lado_queue_store.go` — 플랜 §4 의 메서드 전부(이름·시그니처 그대로): `ClaimLadoQueueEntries`, `ReleaseLadoQueueEntry`, `HoldLadoQueueEntry`, `MarkLadoQueueEntryReviewRequired`, `MarkLadoQueueEntryBlocked`, `FailLadoQueueEntry`, `LinkLadoExecution`, `LeasedLadoQueueEntries`, `CountLiveLadoExecutions`, `LadoExecutionsPastDeadline`, `MarkLadoExecutionTimedOut`, `LadoExecution`, `LadoExecutionByClientRequest`, `LadoDispatchGrant`, `LadoDispatchSettings`, `SetLadoDispatchSettings`, `SetLadoProjectMaxConcurrent`, `LadoProjectMaxConcurrent`, `ListLadoQueue`. 필요한 쿼리는 `queries/lado_queue.sql` 에 더한다(ASCII, `npm run sqlc`, gen 직접 읽기).
- 테스트: `store/lado_queue_store_test.go` 에 아래 필수 테스트.
- 하지 않는다: 디스패처 로직(S07·S08), 게이트(S05), HTTP(S09).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 쓰기 메서드는 모두 `s.writeMu.LockContext(ctx)` + 행이 둘 이상이면 `s.inTx`(LADO store 꼴 — `lado_plan_store.go` 참조). 토큰을 받는 메서드는 `id AND lease_token = ?` 로만 쓰고, 0행이면 `false`(또는 `domain.ErrLadoLeaseLost`).
2. `ClaimLadoQueueEntries(ctx, now, leaseUntil, limit, newToken)`: 한 트랜잭션에서 claimable 목록을 읽고 하나씩 `ClaimLadoQueueEntry`(조건 재확인 CAS) — 0행이면 건너뛴다. 잡은 항목을 다시 읽어 돌려준다(attempt_count 는 +1 된 값). LADO 모드가 꺼진 프로젝트의 항목은 잡지 않는다.
3. `HoldLadoQueueEntry`: 리스를 비우고 attempt_count 를 1 줄인다(보류는 시도가 아니다 — 플랜 §1 h), waiting_reason·next_attempt_at 을 쓴다. `ReleaseLadoQueueEntry` 는 attempt_count 를 그대로 둔다.
4. `LinkLadoExecution(ctx, entryID, token, exec)`: 한 트랜잭션에서 토큰이 아직 유효한지 확인 → `InsertLadoExecution` → `SetLadoQueueEntryDispatched`(dispatched_session_id, 리스 비움). 토큰이 안 맞으면 아무것도 쓰지 않고 `domain.ErrLadoLeaseLost`. 같은 세션 id 로 두 번 부르면 두 번째는 기존 실행을 그대로 두고 nil(복구 경로가 다시 부를 수 있다 — 멱등).
5. `CountLiveLadoExecutions(ctx, now)`: 살아 있는 세션의 실행 + 지금 유효한 리스를 프로젝트별·전체로 센다(리스 중인 항목도 슬롯을 차지한다).
6. `LadoDispatchGrant(ctx, entryID, attempt, now)`: 항목의 project_id, `LadoDispatchHash(최신 리비전 sha256 이 아니라 **항목의 승인이 가리키는 리비전의 sha256**, 항목의 스냅숏 sha256)`, `Active = lease_token 있음 && lease_expires_at > now && attempt_count == attempt && dispatched_session_id 없음`. 항목이 없으면 `Active=false` 와 nil.
7. `LadoExecutionByClientRequest(ctx, id)`: `domain.ParseLadoClientRequestID` → 항목의 실행. 형식이 아니면 `found=false`.
8. 설정: 행이 없으면 `domain.DefaultLadoDispatchSettings()`. `SetLadoDispatchSettings(ctx, settings, event)` 는 upsert 와 감사 이벤트(`dispatch.settings_changed`, actor `api`)를 한 트랜잭션에. `SetLadoProjectMaxConcurrent(ctx, project, n, event)` 도 upsert + 감사(`project.max_concurrent_changed`). `LadoProjectMaxConcurrent` 는 행이 없으면 1.
9. `ListLadoQueue(ctx, projectID *domain.ProjectID)`: 항목과 태스크 제목·이슈 ref 를 함께(대기열 화면용 — S09 DTO 가 쓴다).
10. S02 메모: 태스크를 돌려주는 store 메서드는 모두 `Queue`·`Execution` 을 채워야 한다(안 채우면 승인된 태스크가 `approved_before_queue` 로 보인다). S03 이 빠뜨린 메서드(예: `SetLadoTaskPreferences`·`CancelLadoTask` 가 돌려주는 태스크)가 있으면 여기서 채우고 `TestLadoTaskReadsCarryQueueAndExecution` 에 사례를 더한다.
11. 커밋 메시지 예: `feat(lado): dispatch primitives and settings in the store`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S04-store-dispatch.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestClaimLadoQueueEntriesLeasesOldestOpen` — 오래된 순, next_attempt_at 미래는 제외, LADO 모드 꺼진 프로젝트 제외, 만료된 리스는 다시 잡힘, attempt_count +1, 토큰 설정, limit 준수, 같은 항목을 두 번 잡지 않음
- `TestLadoQueueReleaseHoldAndMarks` — release 는 시도 수 유지, hold 는 시도 수 되돌림, review/blocked/failed 표시, 틀린 토큰은 false 이고 아무것도 안 바뀜
- `TestLinkLadoExecutionAtomically` — 실행 insert + dispatched + 리스 비움이 함께; 토큰을 잃으면 `ErrLadoLeaseLost` 이고 실행 행 없음; 같은 세션으로 다시 불러도 행 하나
- `TestCountLiveLadoExecutions` — 끝난 세션 제외, 리스 중인 항목 포함, 프로젝트별·전체
- `TestLadoDispatchGrant` — 리스 중 + 같은 시도면 Active, 만료·다른 시도·dispatched·없는 항목은 아님, 해시가 `domain.LadoDispatchHash` 와 같다
- `TestLadoExecutionsPastDeadline` — 마감 지남 + 세션 살아 있음 + timed_out 없음만; 마감 없음(NULL)은 제외
- `TestLadoDispatchSettingsDefaultsAndUpdate` — 행 없음 → 기본값; upsert 와 감사 이벤트가 함께; 프로젝트 상한 기본 1 과 upsert
- `TestListLadoQueue` — 프로젝트 필터와 전체, 태스크 제목·이슈 ref 포함

## 수용 게이트

- [ ] S04-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S04-G1: 필수 테스트 8개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/storage/sqlite/store/ TestClaimLadoQueueEntriesLeasesOldestOpen TestLadoQueueReleaseHoldAndMarks TestLinkLadoExecutionAtomically TestCountLiveLadoExecutions TestLadoDispatchGrant TestLadoExecutionsPastDeadline TestLadoDispatchSettingsDefaultsAndUpdate TestListLadoQueue
  EXPECT: GO_TESTS_OK 8/8
- [ ] S04-G2: LADO store 테스트가 race 로도 통과
  CHECK: cd backend && go test -race -count=1 -run 'Lado' ./internal/storage/sqlite/store/ && echo S04_LADO_RACE_OK
  EXPECT: S04_LADO_RACE_OK
- [ ] S04-G3: sqlc 재생성이 커밋과 같다
  CHECK: bash .claude/gate/lado-queue.sh sqlc-clean
  EXPECT: SQLC_CLEAN
- [ ] S04-G4: LADO 쿼리 파일 ASCII
  CHECK: bash .claude/gate/lado-queue.sh queries-ascii
  EXPECT: QUERIES_ASCII
- [ ] S04-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S04-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
