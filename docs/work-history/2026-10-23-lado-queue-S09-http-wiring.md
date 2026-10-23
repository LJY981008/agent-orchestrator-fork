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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
