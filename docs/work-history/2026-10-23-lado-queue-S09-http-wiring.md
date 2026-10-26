# 2026-10-23 LADO 대기열·디스패처 — S09 HTTP 경로·DTO·specgen·데몬 연결

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§8) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

디스패처와 대기열을 API 로 열고 데몬에서 실제로 돌린다: 대기열 목록, 실행 상세(세션 화면의 LADO 패널이 쓴다), 디스패치 설정, 프로젝트 설정의 상한, 태스크 DTO 의 대기열·실행 사실. 데몬은 디스패처를 시작·정지하고, 태스크 서비스의 취소가 디스패처의 Killer 를 쓴다.

## 범위

- 만든다: `backend/internal/httpd/controllers/lado_queue.go`·`lado_queue_dto.go`(GET `/api/v1/lado/queue`, GET `/api/v1/lado/executions/{sessionId}`, GET·PUT `/api/v1/lado/dispatch-settings`), `ladoqueue` 의 읽기 메서드(`Queue(ctx, projectID *domain.ProjectID)`, `Execution(ctx, sessionID)`, `Settings(ctx)`, `SetSettings(ctx, settings)` — 감사 actor `api`).
- 고친다: `lado_tasks_dto.go`(태스크에 `queue`·`execution`, state enum 에 `queued`·`dispatching`·`running`·`failed`·`completed`, 이유 문자열), `lado_projects.go`·`lado_projects_dto.go`(`ladoMode`·`maxConcurrent` 둘 다 선택 + 응답에 `maxConcurrent`, 설명에서 `AO_LADO_DEV` 삭제), `LadoController`(대기열 서비스 의존성), `apispec/specgen/lado_operations.go`(새 operation 과 schema 이름), `backend/internal/httpd/api.go`·`daemon/daemon.go`(훅 몇 줄), `daemon/lado_wiring.go`(디스패처 생성·시작·정지, 태스크 서비스 Killer 연결). 생성물: `npm run api` → `openapi.yaml`·`frontend/src/api/schema.ts`.
- 테스트: 컨트롤러 테스트, 데몬 연결 테스트.
- 하지 않는다: 렌더러(S10~S12).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 기존 LADO 컨트롤러 꼴을 따른다(`lado_projects.go:25-65` 의 `LadoController` 와 `Register`, nil 서비스면 `apispec.NotImplemented`, 경로 상수, DTO 태그). 오류는 `apierr` 그대로.
2. 대기열 항목 DTO: `id`, `taskId`, `issueRef`, `title`, `state`(도메인 `LadoQueueState`), `reason`, `attempts`, `nextAttemptAt`, `error`, `enqueuedAt`. 실행 상세 DTO: 플랜 §8 필드 전부 — 스냅숏 요약은 저장된 스냅숏 JSON 을 `ladopolicy.SnapshotContent` 로 읽어 `agent`·`permissionMode`·`permissionCeiling`·규칙 이름(내장 규칙은 seed 이름 — 표시 번역은 렌더러)·`prompt`. 실행이 아닌 세션은 404 `LADO_EXECUTION_NOT_FOUND`.
3. 디스패치 설정 PUT 은 세 필드 필수(`{maxConcurrent, maxRunMinutes, holdOnMemoryTight}`), 검증 실패 400 `LADO_DISPATCH_SETTINGS_INVALID`. presence 없음.
4. 프로젝트 설정 PUT: 둘 중 하나는 있어야 함(S06 의 서비스 규칙), 모드 끄기는 presence(기존 그대로).
5. specgen: `ladoOperations()` 에 네 operation, `ladoSchemaNames` 에 새 DTO. `npm run api` 후 `TestRouteSpecParity`·`TestBuild_MatchesEmbedded` 가 통과해야 한다.
6. 데몬: `wireLadoWork` 에서 `ladoqueue.New(Deps{Store: store, Tasks: ladoTasks, Policy: ladoRules, Sessions: <*sessionsvc.Service>, Memory: <memoryReader>, Projects: ..., Git: ladoqueue 의 시스템 git, ...})`. 세션 서비스가 `wireLadoWork` 뒤에 만들어지면 디스패처 생성·시작을 그 뒤로 옮기는 가장 작은 훅을 고른다(자동화가 `sessions` 를 받는 자리 `daemon.go:881` 근처 참고). 시작은 태스크 서비스 시작 뒤, 정지 대기는 `ladoTasksDone` 옆. 태스크 서비스의 `Killer` 에 디스패처를 넣는다(생성 순서상 setter 가 필요하면 `SetKiller`).
7. 커밋 메시지 예: `feat(lado): queue, execution and dispatch settings routes, and the dispatcher in the daemon`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지). 생성물(`openapi.yaml`, `schema.ts`)도 같이.
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S09-http-wiring.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoAPI_QueueListsEntries` — 프로젝트 필터와 전체, 상태·이유·시도 수
- `TestLadoAPI_ExecutionDetail` — 스냅숏 요약·베이스·마감·PR; 실행 아닌 세션 404
- `TestLadoAPI_DispatchSettings` — GET 기본값, PUT 저장, 잘못된 값 400
- `TestLadoAPI_ProjectSettingsMaxConcurrent` — 상한만 바꾸기, 모드만 바꾸기, 둘 다 없으면 400, 끄기는 presence
- `TestLadoAPI_TaskCarriesQueueAndExecution` — 태스크 DTO 의 queue·execution 과 새 상태
- daemon `TestWireLadoWorkStartsTheDispatcher` — 디스패처가 시작되고 ctx 취소로 멈춘다; 태스크 서비스에 Killer 가 연결됨

## 수용 게이트

- [ ] S09-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S09-G1: 컨트롤러 테스트 5개
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_QueueListsEntries TestLadoAPI_ExecutionDetail TestLadoAPI_DispatchSettings TestLadoAPI_ProjectSettingsMaxConcurrent TestLadoAPI_TaskCarriesQueueAndExecution
  EXPECT: GO_TESTS_OK 5/5
- [ ] S09-G2: 데몬 연결 테스트
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/daemon/ TestWireLadoWorkStartsTheDispatcher
  EXPECT: GO_TESTS_OK 1/1
- [ ] S09-G3: 스펙 동등성
  CHECK: bash .claude/gate/lado-queue.sh go-tests ./internal/httpd/apispec/ TestRouteSpecParity && bash .claude/gate/lado-queue.sh go-tests ./internal/httpd/apispec/specgen/ TestBuild_MatchesEmbedded
  EXPECT: GO_TESTS_OK 1/1
- [ ] S09-G4: API 생성물이 커밋과 같다
  CHECK: bash .claude/gate/lado-queue.sh api-clean
  EXPECT: API_CLEAN
- [ ] S09-G5: LADO 컨트롤러·서비스·데몬 패키지 전체
  CHECK: bash .claude/gate/lado-queue.sh go-pkg ./internal/httpd/controllers/ ./internal/service/ladoqueue/ ./internal/service/ladotask/ ./internal/service/ladoproject/ ./internal/daemon/
  EXPECT: GO_PKG_OK
- [ ] S09-G6: 업스트림 훅이 작다(api.go·daemon.go)
  CHECK: bash .claude/gate/lado-queue.sh diff-budget 30 backend/internal/httpd/api.go backend/internal/daemon/daemon.go
  EXPECT: BUDGET_OK
- [ ] S09-G7: 개발 플래그 설명이 API 에서 사라졌다
  CHECK: bash .claude/gate/lado-queue.sh none backend/internal/httpd/apispec/openapi.yaml 'AO_LADO_DEV=on' && bash .claude/gate/lado-queue.sh none frontend/src/api/schema.ts 'AO_LADO_DEV=on'
  EXPECT: NONE_OK
- [ ] S09-G8: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S09-G9: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `fb9e6686a`(`docs(lado): record S08`), 트리 깨끗. go1.27.1, Node 24.21.0. `defs-unchanged` → `DEFS_UNCHANGED`.
- 출발점: `/api/v1/lado/queue`·`/lado/executions/{sessionId}`·`/lado/dispatch-settings` 라우트 없음, `LadoController` 는 Projects·Connections·Tasks·Rules·Presence. 태스크 DTO state enum 에 새 상태 5개·이유·queue·execution 없음. 프로젝트 설정 DTO 에 `maxConcurrent` 없음, `ladoMode` 설명에 `AO_LADO_DEV=on`(openapi.yaml·schema.ts 각 1곳). 데몬에 디스패처 없음, `wireLadoWork` 는 (connections, tasks, rules), 태스크 서비스 Killer 미연결. 태그 이후 `api.go`·`daemon.go` diff 2줄(S06 의 `wireLado` 인자).
- controllers·ladoqueue·ladotask·ladoproject·daemon·apispec·specgen `go test -count=1` 모두 ok.
- 앞 단계 메모 중 이 단계 몫: S02(state enum 5개, 이유는 `StateReason()`), S04(`ListLadoQueue` 는 열린 항목만이라 대기열 `error` 는 거의 늘 빈 값, `LadoExecution` 이 세션 종료·PR 을 채움, `SetLadoDispatchSettings` 는 저장값을 돌려줌), S06(Killer 는 setter 로, `UpdateInput.MaxConcurrent`·`LadoProjectSettings.MaxConcurrent` 는 있으니 DTO 만, `AO_LADO_DEV=on` 삭제 후 `npm run api`, 오류 `LADO_SETTINGS_EMPTY`·`LADO_DISPATCH_SETTINGS_INVALID`(details `{min, max}`)), S07(`Branches` 를 빠뜨리면 모든 항목이 기다림 — 세션 매니저에 넘기는 그 워크스페이스), S08(`Memory` = `*usage.MemoryReader`, `Git` 은 비우면 시스템 git, Killer = `*ladoqueue.Service`, `Start` 는 곧바로 Tick, 실행 상세의 base·감사 값).
- AO 쪽(코드로 확인):
  - 세션 서비스(`sessionSvc`)와 `memoryReader` 는 `wireLadoWork` 보다 앞(`daemon.go:582·592`)에서 만들어진다 — 디스패처 생성을 옮길 필요 없음.
  - 워크스페이스 라우터는 `startSession` 안에서만 만들어지고 밖으로 나오지 않는다(`lifecycle_wiring.go:293`). `gitworktree.New` 는 만들 때 버린 worktree 를 쓸어 담는다(`sweepDiscarded`) — 두 번째 인스턴스를 만들면 안 된다.
  - `sessMgr.ReconcileStartupSafety`(`daemon.go:871`)가 시작 안전 경계이고, 세션을 띄우는 자동화는 그 뒤에 시작한다.
  - specgen(jsonschema-go v0.3.79): `omitempty` 없는 포인터는 required + nullable, `nullable:"false"` 태그로 null 을 뺀다. 정수 enum 은 JSON 배열 태그로 숫자가 된다.

## After — 수행 후 실측

- 코드 커밋 `af5af8cad` `feat(lado): queue, execution and dispatch settings routes, and the dispatcher in the daemon` — 24 파일, +2642 −132(생성물 openapi.yaml +584 −9, schema.ts +427 −5 포함).
  - `controllers/lado_queue.go`(새): `LadoQueueService`(Queue·Execution·Settings·SetSettings), 핸들러 4개(서비스가 nil 이면 501), 도우미 `ladoQueueReason`(상태 뒤의 열: waiting → `waiting_reason`, review_required → `review_required_reason`, blocked → `blocked_reason`, 그 밖 ""), `ladoNextAttempt`(아직 앞일 때만), 태스크의 queue·execution, 실행 상세, 설정 응답 변환. PUT 은 세 필드 중 하나라도 없거나 null 이면 400 `LADO_DISPATCH_SETTINGS_INVALID`.
  - `controllers/lado_queue_dto.go`(새): `ListLadoQueueQuery`, `LadoQueueItemResponse`(엔트리 필드 + `projectId`), `LadoQueueResponse`, `LadoTaskQueueResponse`, `LadoTaskExecutionResponse`, `LadoExecutionPRResponse`, `LadoSessionIDParam`, 실행 상세 `LadoExecutionResponse` 와 하위(task·plan·snapshot·base·rule), `LadoDispatchSettingsResponse`(`updatedAt` 은 저장 전 생략), `UpdateLadoDispatchSettingsRequest`(포인터 + `nullable:"false"` → required·null 불가). 상태·이유·분 값은 enum.
  - 태스크 DTO: state enum 에 queued·dispatching·running·failed·completed(approved 는 "더는 보내지 않음"으로 남김), `stateReason`(enum 16개), `queue`, `execution`.
  - 프로젝트 설정: 요청 `ladoMode`·`maxConcurrent` 모두 `omitempty`(둘 다 없으면 서비스가 400), 응답 `maxConcurrent`, `AO_LADO_DEV` 문장 삭제, 컨트롤러가 `MaxConcurrent` 를 서비스에 넘김.
  - `ladoqueue/views.go`(새): `Queue`, `Execution` → `ExecutionView{Execution, Task, Revision, Approval, SnapshotSHA256, Snapshot ladopolicy.SnapshotContent}`(실행 아님 → 404 `LADO_EXECUTION_NOT_FOUND`, 태스크·리비전·승인·스냅숏이 없거나 해독 실패 → 500 `LADO_EXECUTION_LOAD_FAILED`), `Settings`, `SetSettings`(검증 → `InvalidSettings`(details `{min, max, runMinutes}`), 지금 값과 같으면 쓰지 않음, 감사 actor `api`·action `dispatch.settings_changed`·subject `dispatch_settings`/`global`·payload `{from, to}`). `Store` 인터페이스에 읽기 6개(`ListLadoQueue`·`LadoExecution`·`LadoPlanRevision`·`ListLadoPlanApprovals`·`LadoPolicySnapshot`·`SetLadoDispatchSettings`)와 가짜 store 의 같은 메서드.
  - `ladotask.Service.SetKiller`.
  - specgen: operation 4개(`listLadoQueue`·`getLadoExecution`·`getLadoDispatchSettings`·`updateLadoDispatchSettings`), schema 이름 15개, 프로젝트 설정 PUT 요약 갱신. `npm run api` 로 재생성(다시 돌려도 md5 같음).
  - `session_manager/lado_gate.go`: `Manager.ResolveDefaultBranch` — 워크스페이스가 `ports.WorkspaceDefaultBranchRefresher` 면 그것에 맡기고, 아니면 `ErrWorkspaceDefaultBranchUnresolved`. `lifecycle_wiring.go` 의 `sessionLifecycle` 에 한 줄(+주석 갱신).
  - 데몬: `ladoWorkDeps` 에 `sessions`(세션 서비스)·`branches`(세션 매니저)·`memory`(usage 메모리 리더). `wireLadoWork` 가 `ladoqueue.New(Deps{Store, Tasks, Policy, Sessions, Projects: store, Branches, Memory, Log})` 를 넷째 값으로 돌려주고, sessions 가 있을 때만 `tasks.SetKiller(queue)`. `daemon.go`: 인자·반환 1줄, `startAutomations` 바로 뒤 `ladoQueueDone := ladoQueue.Start(ctx)`(+주석 2줄), `LadoQueue: ladoQueue`, `<-ladoQueueDone // LADO fork`. `api.go`: 필드·컨트롤러 인자·주석. 태그 이후 두 파일 합계 16줄(한도 30).
  - 테스트: 필수 6개 + `TestLadoAPI_QueueRoutesWithoutAServiceAnswerNotImplemented`, ladoqueue `TestQueueListsTheOpenEntries`·`TestExecutionShowsWhatTheWorkerRuns`·`TestSetSettingsAuditsTheChangeAsTheAPI`, session_manager `TestResolveDefaultBranchAsksTheWorkspace`. 컨트롤러 테스트는 실제 store 와 실제 서비스(ladoqueue·ladoproject·ladotask), presence verifier 로 HTTP 끝까지 돈다(대기·디스패치 중·모드 꺼짐, 실행 중·시간 초과·머지·blocked·재승인 태스크를 store primitive 로 만든다). 데몬 테스트는 실제 store·ladotask·ladoqueue 에 가짜 세션 서비스만 두고: 시작 → 첫 틱의 복구가 커밋된 워커를 실행으로 연결 → ctx 취소로 멈춤 → 그 뒤 태스크 취소가 Killer 로 그 워커를 멈춤. `TestWireLadoGivesADispatchedWorkerItsRules` 의 시드는 `seedLadoApprovedTask` 로 빼서 함께 쓴다(LADO 모드 켬 추가).
- 엔트리·플랜이 정하지 않았거나 다르게 적은 것:
  1. **디스패처는 `startAutomations` 바로 뒤에 시작한다**(`ReconcileStartupSafety` 뒤). 엔트리의 "태스크 서비스 시작 뒤"·"정지 대기는 `ladoTasksDone` 옆" 은 그대로 지킨다. 태스크 서비스 바로 뒤(line 687)면 디스패처의 복구 kill·마감 kill·스폰이 AO 의 시작 안전 정리(중단된 사가, setup 중 끊긴 client 요청 세션)와 같은 세션 행에서 겹칠 수 있다. 같은 종류의 일을 하는 자동화가 그 경계 뒤에 시작한다. 경계 전에 `Run` 이 실패로 돌아가면 디스패처는 시작되지 않는다.
  2. **`Branches` 는 세션 매니저**. 워크스페이스 라우터는 `startSession` 밖으로 나오지 않고(꺼내려면 upstream 시그니처와 return 4곳을 바꿔야 함), 두 번째 `gitworktree` 는 만들 때 버린 worktree 를 쓸어 담는 부작용이 있다. 그래서 LADO 파일 `session_manager/lado_gate.go` 에 위임 메서드를 두고 `sessionLifecycle` 에 한 줄 더했다(S05 가 같은 인터페이스에 LADO 줄 2개를 더한 선례). 엔트리의 파일 목록에 없던 upstream 파일 1곳(lifecycle_wiring.go, +3 −2).
  3. 디스패처는 sessions 가 없어도 만든다(읽기 라우트용): API 에 typed nil 이 들어가면 핸들러가 nil 포인터로 죽는다. sessions 가 없으면 Killer 는 걸지 않는다(취소가 nil 세션 서비스를 부르지 않게). `Start` 는 데몬만 부른다.
  4. **태스크 DTO `execution` 에 `planRevisionId`·`planRevisionNumber`·`policySha256`·`sessionEnded` 를 더했다**(플랜 §8 의 5개 위에). S12 보드 줄("계획 r{n} · 규칙 {sha}")과 S06 메모(실행 세션이 살아 있으면 재승인 거절 — DTO 의 세션 종료 여부로 막을 것)가 태스크 목록만으로 되게. 번호와 해시는 태스크가 읽어 오는 계획 사실(최신 리비전, 최신 승인의 스냅숏)에서 오므로, 실행 중 새 리비전이 고정되면 번호를, 옛 승인의 실행이면 해시를 생략한다(설명에 적음) — 실행 상세가 늘 정확한 값을 준다. store 쿼리를 늘리지 않은 것은 S03/S04 의 실행 DeepEqual 테스트 여럿을 흔들기 때문.
  5. `nextAttemptAt` 은 아직 앞일 때만 보낸다(claim 은 지난 시각을 남긴다 — S01). `reason` 은 상태 뒤의 열만(failed 는 `error` 가 말한다).
  6. 디스패치 설정 PUT: 세 필드 required(스키마), 빠지거나 null → `LADO_DISPATCH_SETTINGS_INVALID`(빠진 `maxRunMinutes` 가 "제한 없음" 0 으로 읽히지 않게), 모르는 필드·깨진 JSON → `INVALID_JSON`. 지금 값과 같으면 쓰지 않는다(S06 프로젝트 상한과 같은 규칙) — `updatedAt` 도 그대로.
  7. 대기열 항목에 `projectId` 를 더했다(전체 목록에서 프로젝트를 가르게). 알 수 없는 `projectId` 는 404 가 아니라 빈 목록.
- 확인:
  - 필수 6개 PASS. 변이 11개 모두 테스트가 잡음: 지난 다음 시도 노출, blocked 이유, 상한 미전달, 빠진 필드 검사, Killer 미연결, 리비전 번호 무조건, 실행 상세의 승인 시각, 같은 설정 재기록, 프로젝트 필터 무시, 옛 승인의 스냅숏 해시(처음엔 살아남아 재승인 태스크 사례를 더함), `timedOut` 을 마감 유무로.
  - G5 패키지 `go test -count=1` ok, apispec·specgen ok, `go build ./... && go vet ./...` ok, session_manager `-run 'Lado|ResolveDefault|Spawn'` ok. golangci-lint v2.13.2(오프라인 빌드 재사용) httpd·ladoqueue·ladotask·daemon·session_manager 0 issues, gofmt 없음.
  - 프런트엔드: `npm run typecheck:e2e` 통과. `npm run typecheck` 는 1건 실패 — `src/renderer/components/lado/LadoProjectRulesSection.test.tsx:295` 의 mock 이 `maxConcurrent` 없이 `{projectId, ladoMode}` 를 돌려준다(응답 타입에 required 로 생김). 렌더러는 S10 몫(S10 범위 "S09 가 재생성한 `schema.ts` 로 깨지는 타입은 여기서 고친다"), 플랜 §0 의 typecheck 의무도 S10 부터.
  - 독립 리뷰 서브에이전트(Sonnet) 1회: blocker·should-fix 없음. nit 1 — 시작 위치 주석이 "왜 경계 뒤인가"를 말하지 않음 → 주석을 고쳐 반영. 결정 1~6 과 SetKiller 경쟁(없음 — 배선이 HTTP 시작 전 동기)을 코드로 따라가 확인.
- 게이트(HEAD `af5af8cad`): `GATES=10 MET=10 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다. 엔트리에 없던 것: `Manager.ResolveDefaultBranch`(+`sessionLifecycle` 한 줄), 태스크 DTO 실행의 4개 필드, 대기열 항목 `projectId`, 501 테스트·ladoqueue 뷰 테스트 3개·세션 매니저 테스트 1개, 데몬 테스트 시드 도우미. 엔트리와 다르게 한 것은 시작 위치(After 결정 1) 하나이고, 그것도 엔트리 문장 안이다.
- specgen `lado_operations.go` 의 diff(+108 −70)가 큰 것은 새 schema 이름이 맵의 가장 긴 키가 되어 gofmt 가 맵 정렬을 다시 맞춘 공백 변경 때문이다.
- 남은 틈(기록만):
  - 태스크 DTO 의 `planRevisionNumber`·`policySha256` 은 태스크의 계획 사실에서 오는 부분 값이다(After 결정 4). 정확한 값은 실행 상세에서.
  - `daemon.Run` 안의 배선(시작 위치, `branches: sessMgr`, `memory: memoryReader`)은 테스트가 덮지 못한다(`Run` 을 띄우는 테스트 없음). 타입이 nil 이 아닌 값을 강제하는 것으로 갈음.
  - 대기열 응답의 `error` 는 열린 항목만 나오므로 사실상 늘 빈 값이다(S04·S07 메모 그대로).
- 다음 단계 메모:
  - S10: 타입체크 1건 — `LadoProjectRulesSection.test.tsx:295` mock 에 `maxConcurrent` 를 더할 것. 새 타입: `LadoQueueResponse`/`LadoQueueItemResponse`(`GET /lado/queue?projectId=`), `LadoExecutionResponse`(`GET /lado/executions/{sessionId}`; 404 `LADO_EXECUTION_NOT_FOUND` = "패널 없음"), `LadoDispatchSettingsResponse`·`UpdateLadoDispatchSettingsRequest`(PUT 은 세 필드 모두, presence 없음), `UpdateLadoProjectSettingsRequest`(`ladoMode`·`maxConcurrent` 선택, 끄기만 presence). 오류 코드: 400 `LADO_DISPATCH_SETTINGS_INVALID`(디스패치 설정 경로 details `{min, max, runMinutes}`, 프로젝트 상한 경로는 `{min, max}`), 400 `LADO_SETTINGS_EMPTY`, 404 `LADO_EXECUTION_NOT_FOUND`, 500 `LADO_QUEUE_LOAD_FAILED`·`LADO_EXECUTION_LOAD_FAILED`·`LADO_DISPATCH_SETTINGS_LOAD_FAILED`·`LADO_DISPATCH_SETTINGS_UPDATE_FAILED`. 이유 문자열은 태스크 `stateReason` enum 16개, 항목 `reason` enum 12개 — `schema.ts` 의 union 으로 문구 키 맵의 빠짐을 타입으로 잡을 수 있다.
  - S11: 설정 GET 은 저장 전 `updatedAt` 이 없다. PUT 은 지금 값과 같으면 아무것도 쓰지 않는다. `maxRunMinutes` 타입은 `0 | 60 | 120 | 240 | 480 | 720 | 1440`, `maxConcurrent` 는 1-8(스키마 minimum·maximum).
  - S12: 레인은 `state`·`stateReason`·`queue.attempts`·`queue.nextAttemptAt`(아직 앞일 때만 온다; `slot_project` 는 다음 틱이라 이유로만 — S08 메모). 보드 줄은 `execution.sessionId`·`planRevisionNumber`·`policySha256` — 뒤 둘은 생략될 수 있으니 그 조각만 빼거나 실행 상세를 읽는다. `execution.sessionEnded === false` 면 재승인을 데몬이 `LADO_TASK_RUNNING` 으로 거절한다. 실행 패널은 `snapshot.rules[]` 의 `{id, name}` 을 `ladoRuleName(t, id, name)` 에 넘기고, `base.commitsAhead` 가 없으면 모름, `timedOutAt` 이 있으면 시간 초과로 종료.
  - S13: TDD §13 에 네 경로와 오류 코드, 디스패치 설정 감사(`dispatch.settings_changed`, actor `api`, 같은 값은 쓰지 않음), 디스패처 시작 위치(시작 안전 정리 뒤, 자동화와 함께), `Branches` = 세션 매니저 위임을 적는다.
