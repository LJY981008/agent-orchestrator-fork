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

- 시작 HEAD `d89ae9da2`(메인 세션의 `docs(lado): name the S03 test failures S06 fixes` — S05·S06 엔트리만, S04 파일은 안 건드림; 그 앞이 `0af21261e` `docs(lado): record S03`), 트리 깨끗. 환경 umask 0022·TZ=UTC·LANG=C.UTF-8, go1.27.1. `defs-unchanged` → `DEFS_UNCHANGED`.
- store: `lado_queue_store.go` 에는 S03 의 `queueLadoApprovalTx`·`fillLadoQueueFacts`·변환 둘뿐 — 디스패치 primitive·설정·대기열 목록 없음. 도메인에 대기열 목록 행 타입·프로젝트 상한 기본값 상수 없음.
- 쿼리: S01 의 29개가 다 있다. `GetLadoExecution`·`GetLadoExecutionByEntry` 는 실행 열만 주고(세션 종료 여부 없음) 호출부가 없다. 허가 해시에 필요한 "항목 → 승인의 리비전 sha256·스냅숏 sha256" 과 태스크 제목·이슈 ref 를 함께 주는 대기열 목록 쿼리는 없다.
- 기준선: `go test -count=1 -run 'Lado' ./internal/storage/sqlite/store/` ok(1.05s). 다른 패키지의 알려진 실패는 S02·S03 기록과 같은 6개(ladotask 4·ladopolicy 2, S06 몫).
- 테스트 순서: 필수 테스트를 코드 뒤에 썼다(RED 먼저가 아님). 대신 After 의 변이 27개로 테스트가 실제로 잡는지 확인했다.

## After — 수행 후 실측

- 코드 커밋 `95808686e` `feat(lado): dispatch primitives and settings in the store` — 6 파일, +1596 −61.
  - `store/lado_queue_store.go`: 플랜 §4 의 메서드 19개 전부와 도우미 `writeLadoRow`(쓰기 잠금 + 한 문장 + 바뀐 행 여부 — 토큰 쓰기 5개와 time-out 이 같이 씀), `fillLadoExecutionPRs`(실행 묶음의 최신 PR 을 한 번에 — S03 의 `fillLadoQueueFacts` 도 이것을 쓰게 고침, 동작 같음), `ladoExecutionWithPR`, `ladoDispatchSettingsFromGen`. 여러 행을 쓰는 것(claim·link·설정 둘)은 `inTx`, 나머지 쓰기는 `writeMu` 아래 한 문장.
  - `store/lado_plan_store.go`: `unwrapLadoSentinel` 에 `domain.ErrLadoLeaseLost`(트랜잭션 문구 없이 `errors.Is` 로 맞춘다).
  - `queries/lado_queue.sql`: `GetLadoExecution`·`GetLadoExecutionByEntry` 를 `sqlc.embed(e)` + `NOT EXISTS (살아 있는 sessions 행) AS session_terminated` 로(호출부 없던 것); 새 쿼리 `GetLadoQueueEntryPlan`(항목 + 승인이 가리키는 리비전의 id·sha256·base_sha + 항목 스냅숏의 sha256 — 허가와 link 가 한 번 읽기로 씀), `ListOpenLadoQueueEntriesWithIssues`(대기열 화면). gen 을 직접 읽어 확인: JOIN 셋, `(e.project_id = ?1 OR ?1 IS NULL)`(인자 `sql.NullString`), `ORDER BY e.enqueued_at, e.id`, `NOT EXISTS (...)` 모두 온전. ASCII 만.
  - `domain/lado_queue.go`: `LadoQueueItem{Entry, IssueRef, Title}`(대기열 목록 행), `LadoDefaultProjectMaxConcurrent = 1`.
  - 테스트(`store/lado_queue_store_test.go`): 필수 8개 + `TestLadoExecutionByClientRequest`(목록에 없던 메서드와 단건 읽기의 세션 종료·PR 채움), claim 테스트 안에 `LeasedLadoQueueEntries` 확인. 도우미 `mustQueuedLadoTask`(태스크·요청·리비전·승인을 store 로 심음), `mustLadoMode`, `ladoTokens`, `mustClaimLadoQueue`(잡힌 순서까지 단언), `storedLadoQueueEntry`, `unleased`, `countLadoExecutions`. 새 테스트는 store primitive 만 쓴다(원자성 확인용 트리거 하나만 raw).
- 시그니처는 플랜 §4 그대로이고, 정하지 않은 타입·반환값은 이렇게: 항목·세션 id 는 `domain.LadoQueueEntryID`·`domain.SessionID`(플랜의 `entryID, token string` 을 Go 문법 그대로 읽으면 string 이지만, store 의 다른 id 인자가 도메인 타입이고 S05·S07 이 `ParseLadoClientRequestID`·`entry.ID` 의 값을 그대로 넘긴다), 토큰은 `string`. Release·Hold·Mark 둘·Fail·`MarkLadoExecutionTimedOut` → `(bool, error)`, `LinkLadoExecution` → `error`, `SetLadoDispatchSettings` → `(domain.LadoDispatchSettings, error)`(저장된 값), `SetLadoProjectMaxConcurrent` → `error`, `LadoProjectMaxConcurrent` → `(int, error)`, `ListLadoQueue` → `([]domain.LadoQueueItem, error)`.
- 엔트리가 정하지 않은 것을 다음처럼 정했다(모두 테스트로 고정).
  1. `LinkLadoExecution` 의 실행은 항목의 태스크·항목·승인·스냅숏·시도 수와 승인이 가리키는 리비전·그 `base_sha` 를 store 가 채운다. 호출자 값은 세션·`DispatchBaseSHA`·`BaseCommitsAhead`·`StartedAt`·`DeadlineAt` 만 쓴다(S03 `ApproveLadoPlan` 의 항목과 같은 꼴 — 실행이 늘 제 항목과 맞는다). 토큰만 보고 리스 만료는 보지 않는다(인자에 `now` 가 없다): S08 복구가 "만료됐지만 아무도 다시 잡지 않은" 리스의 세션을 연결한다. 멱등: 같은 세션·같은 항목의 실행이 이미 있으면 토큰 검사 **전에** nil(첫 link 가 리스를 비웠으므로); 같은 세션이 다른 항목을 돌리면 오류(`ErrLadoLeaseLost` 아님). 리스 중에 대체·취소된 항목도 연결한다(S03 메모 — 세션은 이미 있다). 세션 id·시작 시각이 비면 거절.
  2. `LadoDispatchGrant.Active` = `entry.State(now) == dispatching && attempt_count == attempt`. 엔트리 식(리스 있음·만료 전·같은 시도·dispatched 아님)보다 엄격하다: 리스 중에 대체·취소된 항목도 거절한다(review·blocked·failed 는 리스를 비우므로 원래 거절). 게이트는 스폰 직전의 마지막 검사라, 디스패처의 검사 1 뒤에 새 승인이나 취소가 들어와도 옛 계획의 워커가 뜨지 않는다. 엔트리 식 그대로인 변이(M12)는 테스트가 잡는다. 해시는 최신 리비전이 아니라 승인이 가리키는 리비전의 sha256(승인 뒤 리비전 2 를 고정해 확인).
  3. `LadoExecutionByClientRequest` 는 실행의 시도 수가 id 의 시도와 같을 때만 찾는다 — 실패한 앞 시도의 세션(`lado:<entry>/1`)은 실행이 아니다. S05 의 `LadoEntryPolicyByClientRequest` 는 실행 없이 항목으로 찾는 별개 읽기다.
  4. `ListLadoQueue` 는 열린 항목만(queued·waiting·dispatching, LADO 모드 꺼진 프로젝트의 것 포함 — 플랜 §7 "the queue view shows them waiting with `lado_mode_off`"), 디스패처가 잡는 순서(`enqueued_at, id`). 멈춘 항목(review·blocked·failed)·dispatched·superseded·cancelled 는 뺐다: supersede 는 열린 항목만 바꾸므로 failed 항목은 다시 승인하거나 태스크가 끝나도 failed 로 영원히 남는다 — 넣으면 낡은 행이 계속 보인다. 현재 항목의 상태·이유·오류는 태스크 DTO 의 `queue`(S09)가 보인다.
  5. 설정·프로젝트 상한의 `updated_at` 은 감사 이벤트의 시각(시그니처에 `at` 이 없다). store 는 값을 검증하지 않고(서비스 몫 — S06·S09), 테이블 CHECK 가 1-8 밖을 거절하면 감사 이벤트도 같이 롤백(테스트). 프로젝트 상한은 S01 의 `UpsertLadoProjectMaxConcurrent`(행이 없으면 모드 0 으로 만듦)를 쓴다 — `SetLadoProjectMaxConcurrent :execrows`(갱신만) 쿼리는 호출부가 없다.
  6. 조용히 다른 일을 하게 되는 입력을 거절: claim 은 `leaseUntil <= now`(0 이면 NULL 만료 → 영원히 못 잡히는 항목)와 빈 토큰, review·blocked 표시는 빈 이유, fail·time-out 은 0 시각(그대로면 fail 은 release 가 되고 time-out 은 아무것도 안 바꾸고 true).
  7. `CountLiveLadoExecutions` 는 읽기 트랜잭션 없이 두 쿼리를 리스 → 실행 순으로 읽는다. 사이에 link 가 끼면 그 항목은 두 번 세어진다(안전 쪽 — 한 틱 더 기다림); 반대 순서면 빠져서 상한을 넘길 수 있다.
- 절차 10(S02 메모): 더할 것이 없었다. S03 이 `SetLadoTaskPreferences`·`CancelLadoTask` 반환값을 이미 채우고 `TestLadoTaskReadsCarryQueueAndExecution`·`TestCancelLadoTaskCancelsOpenEntries` 가 본다. `CreateLadoTask`·`LinkLadoTaskIssue` 는 승인이 있을 수 없고, `ListPendingLadoTasks`·`ListLadoTasksToObserve` 는 계획 사실도 안 채우는 관찰용(`RefreshAll` 은 `State()` 를 쓰지 않는다).
- 확인:
  - 필수 8개 + `TestLadoExecutionByClientRequest` PASS(`-v` 로 실행 확인). 변이 27개 모두 잡힘: limit 무시, claim 전 행 반환, hold 가 시도 유지, link 멱등 없음·토큰 검사 없음·호출자 시도/base 사용·dispatch 오류 무시(트리거로 롤백 확인), 상한 집계에서 리스 빠짐, 해시 순서, 허가가 시도 무시·엔트리 식 그대로, 마감 목록·단건 읽기의 PR 없음·종료 여부 없음, 설정 감사 없음·기본값 0, 프로젝트 상한 기본 0·감사 없음, 목록이 프로젝트 무시, client request 가 시도 무시, 가드 4개 제거, gen SQL 의 failed 필터 제거·최신 리비전 해시. (첫 판의 M9·M19 는 미사용 변수로 컴파일 실패라 동작 변이로 다시 함.)
  - `go test -count=1 ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ ./internal/storage/sqlite/sqlitetest/ ./internal/domain/` ok(32.2s·7.7s·0.5s·0.1s), `go test -race -count=1 -run 'Lado' ./internal/storage/sqlite/store/` ok(32.5s), golangci-lint v2.13.2(오프라인 빌드) `./internal/storage/sqlite/... ./internal/domain/...` 0 issues, gofmt 없음.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: 지적 없음(트랜잭션·토큰·허가·집계 순서·필수 테스트 하위 사례를 대조).
  - 다른 패키지: `ladoproject`·`ladogate` ok, `-run 'Lado'` 의 `daemon`·`httpd/controllers`·`session_manager`·`service/chat` ok. 실패는 S02·S03 기록과 같은 6개뿐(ladotask `TestApprovePlanBindsTheExactRevision`·`TestApprovePlanStoresTheRuleSnapshot`·`TestTaskStateFollowsRuleChanges`·`TestReviewRequiredTaskCanBeApprovedAgain`, ladopolicy `TestDisablingKeepsHistoryAndSnapshots`·`TestImpactListsApprovedTasks`) — S06 몫.
- 게이트(HEAD `95808686e`): `GATES=7 MET=7 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: 도메인 `LadoQueueItem`·`LadoDefaultProjectMaxConcurrent`, 쿼리 2개 추가·2개 변경, `TestLadoExecutionByClientRequest`, 허가의 엄격한 Active(After 2).
- S03 메모 "raw 쓰기를 store primitive 로 바꿀 수 있다": S03 테스트의 `insertLadoExecution`·`dispatchLadoQueueEntry` 는 그대로 뒀다. primitive 가 항목에서 가져오는 값(시도 수 2·3, 세션 행 없는 실행)을 직접 정하는 테스트이고, CDC 테스트는 열 하나씩 raw 로 바꿔야 한다. 새 테스트는 primitive 만 쓴다.
- 다음 단계 메모:
  - S05: `LadoDispatchGrant(ctx, entryID domain.LadoQueueEntryID, attempt int, now)` — Active 는 대체·취소된 항목에서도 false. 없는 항목은 zero 허가와 nil(→ `LADO_TASK_REQUIRED`). `LadoEntryPolicyByClientRequest` 는 스냅숏 JSON(`permissionCeiling`·`prompt`)이 필요해 `GetLadoQueueEntryPlan`(sha256 만)으로는 안 된다 — `GetLadoQueueEntry` + `GetLadoPolicySnapshotByApproval(entry.plan_approval_id)` 또는 새 JOIN 쿼리.
  - S07: `ClaimLadoQueueEntries` 는 `leaseUntil > now` 와 빈 문자열이 아닌 토큰을 요구한다. Release 는 `(id, token, nextAttempt, waiting)`, Hold 는 `(id, token, waiting, nextAttempt)` — 인자 순서가 다르다(플랜 그대로). `LinkLadoExecution` 에는 `SessionID`·`StartedAt`(필수)·`DeadlineAt`(0 = 없음)·`DispatchBaseSHA`·`BaseCommitsAhead` 만 의미가 있고 나머지는 항목에서 채워진다. link 는 "스폰 도중 대체·취소됨"을 알려 주지 않으니, 그런 세션을 바로 끌지는 link 뒤 `LadoTask` 를 다시 읽어 정한다. `CancelLadoQueueEntry`(S07 이 더함)는 `writeLadoRow` + `id AND lease_token` 쿼리 꼴로. `CountLiveLadoExecutions` 는 방금 잡은 리스도 센다(같은 틱 안에서 잡은 항목이 슬롯을 차지).
  - S08: `LeasedLadoQueueEntries` 는 만료·유효 리스를 `claimed_at, id` 순으로 준다. 복구 link 는 항목의 지금 `LeaseToken` 으로 부르면 되고 같은 세션이면 몇 번이든 nil. `MarkLadoExecutionTimedOut` 의 false(이미 표시)로 감사 이벤트 중복을 막을 수 있다. `LadoExecutionsPastDeadline` 은 PR 이 머지됐어도 세션이 살아 있으면 돌려준다(실행에 PR 이 채워져 있음) — 그런 세션도 끌지 S08 이 정한다. `SetLadoQueueWaiting` 을 더할 때 `waiting_reason` 변경은 CDC 이벤트를 낸다(같은 이유면 쓰지 말 것 — 엔트리에 있음).
  - S06: `SetLadoProjectMaxConcurrent(ctx, project, n, event)` 전에 n(1-8)을 검증하고, `event.At` 이 저장 시각이 된다. `domain.LadoProjectSettings` 에는 `MaxConcurrent` 가 없다 — 설정 응답에 필요하면 도메인 필드와 `ladoProjectSettingsFromGen` 에 더한다(gen 행에는 S01 부터 있음), 또는 `LadoProjectMaxConcurrent` 를 따로 읽는다.
  - S09: `ListLadoQueue` 는 열린 항목만 준다(After 4). 대기열 DTO 의 `error` 는 지금 쿼리로는 늘 빈 값이다(release 는 `error_message` 를 쓰지 않고 fail 한 항목은 목록에 없다) — S07 이 release 때 오류를 남기기로 하면 채워진다. `LadoExecution(ctx, sessionID)` 이 세션 종료 여부·PR 을 채우므로 실행 상세가 그대로 쓴다. `SetLadoDispatchSettings` 는 저장된 값(`UpdatedAt` = 이벤트 시각)을 돌려준다.
