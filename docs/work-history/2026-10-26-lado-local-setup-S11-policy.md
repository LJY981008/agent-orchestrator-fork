# 2026-10-26 LADO 로컬 설정 채택 — S11 정책: setup 해석·스냅숏 `lado-policy/2`·고정·미리보기·보호 규칙

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 c·i·j·m, §11) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-005 의 setup 쪽: 태스크가 승인될 때 그 에이전트 도구의 켜진 setup 항목(버전·해시·출처 바인딩), AO 시스템 층, 수락한 충돌이 스냅숏에 고정되고, 승인 뒤 setup 이 바뀌면 해시가 달라져 Review required 가 된다(디스패처 검사 3 이 그대로 잡는다). 미리보기와 영향(dry run)도 setup 을 보여 준다. 보호 규칙 `setup.change.requires_presence` 를 등록한다.

## 범위

- 만든다: `service/ladosetup/effective.go`(`SetupSource` 구현), `ladopolicy` 의 `SetupSource` 포트와 해석·스냅숏·미리보기·영향 확장, 보호 규칙, 테스트.
- 바꾼다: `service/ladopolicy/snapshot.go`(컴파일러 이름과 필드), `snapshot_test.go`(형식 테스트를 `TestSnapshotFormatIsLadoPolicy2` 로), `service/ladotask` 의 승인이 스냅숏의 setup 버전 id 를 store 에 넘기는 곳(S04 의 `SetupVersionIDs`).
- 하지 않는다: 변경 API(S12), HTTP DTO(S13 — 서비스의 `EffectiveView` 필드까지만).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 포트(`ladopolicy` 안의 작은 인터페이스): `SetupSource { EffectiveSetup(ctx, in SetupScope) (SetupResolution, error) }`, `SetupScope{ProjectID, TaskID (빈 값 가능), Harness, Ceiling domain.PermissionMode}`, `SetupResolution{Items []SetupItemState{ItemID, Tool, Kind, Name, VersionID, Version, SHA256, Source{Scope, ScopeID, State}}, Problems []domain.LadoPolicyProblem, Accepted []AcceptedConflict{Code, ItemID, Detail, AcceptanceID}, AOSystemSHA256 string}`. `Deps.Setup` 이 nil 이면 setup 없음(항목·문제 없음, `AOSystemSHA256` 빈 값).
2. `ladosetup.EffectiveSetup`: harness 의 도구(claude-code → Claude 항목, codex → Codex 항목, 그 밖 → 없음)의 살아 있는 항목 중 켜진 것(전역 행 없음 = 켜짐 / off, 프로젝트 force, 태스크 force — 가장 가까운 non-inherit 가 이김), 최신 버전; 충돌(S06)을 그 범위로 돌려 문제로(수락된 경고는 `Accepted` 로 옮기고 문제에서 뺌); `aoSystem`(S06).
3. 해석(`resolve.go`): `Input` 에 `Setup SetupResolution` 을 더하고 `projectScope`/`projectInput`/`taskInput` 이 채운다(에이전트가 정해진 뒤 — harness 가 필요). setup 문제는 `problems` 에 합친다(정렬 규칙 그대로). blocking 이면 스냅숏 blocking.
4. 스냅숏(`snapshot.go`): 컴파일러 `lado-policy/2`. `aoRules` 다음에 `setup`(tool·kind·name 순 정렬), `aoSystem` {"sha256"}, `acceptedConflicts`(code·itemId·detail 순). 그 뒤 순서는 그대로. `SnapshotSetup()` 이 버전 id 목록을 준다 → `ladotask` 승인 경로가 `snapshot.SetupVersionIDs` 에 넣어 store 로(S04 가 고정). `TestSnapshotFormatIsLadoPolicy1` 은 `TestSnapshotFormatIsLadoPolicy2` 로 바꾸고 바이트를 고정한다.
5. 미리보기(`EffectiveView`): `Setup`(항목과 출처), `AOSystemSHA256`, 충돌과 수락, 승인된 태스크면 승인 뒤 바뀐 setup(추가·제거·변경 — `diffRules` 옆 `diffSetup`).
6. 영향(`impact.go`): setup 변경을 dry run 할 수 있게 `ImpactOfSetupChange(ctx, change SetupChange)` — 바뀌는 LADO 프로젝트와 해시가 바뀌는 승인된 태스크(규칙 영향과 같은 기계). `SetupChange` 는 "이 항목을 이 버전/바인딩으로 바꾼다고 치면"을 표현하는 최소 모양(S12 가 씀).
7. 보호 규칙: `EnsureBuiltins` 에 `setup.change.requires_presence`(이름 "Local setup changes need the desktop app's confirmation", 범주 비움, 강제 `workflow_gate`, 지시문은 무엇이 확인을 요구하는지 한 문장, 보호, 늘 켜짐). 기존 보호 규칙과 같은 거부(바인딩·편집·보관·삭제).
8. 디스패처는 코드를 바꾸지 않는다(`CurrentSHA256` 이 setup 을 포함하므로 검사 3 이 `rules_changed` 로 잡음). 이것을 테스트로 묶는다.
9. 커밋 메시지 예: `feat(lado): local setup in the rule snapshot (lado-policy/2)`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S11-policy.md` 로 전건 MET 를 확인한다. 한 번에 10분을 넘기면 `--only <ID>` 로 나눠 돌리고 `--status` 로 확인한다.

## 필수 테스트

`service/ladopolicy`:

- `TestSnapshotFormatIsLadoPolicy2` — 고정 바이트(setup·aoSystem·acceptedConflicts 포함, 필드 순서)
- `TestResolveIncludesSetupOfTheAgentTool` — Claude 태스크는 Claude 항목만, Codex 태스크는 Codex 항목만, 에이전트 없음은 setup 없음
- `TestSetupBindingsResolveLikeRules` — 전역 기본 켜짐, 전역 off, 프로젝트 force_off, 태스크 force_on 이 다시 켬, 가장 가까운 것이 이김
- `TestSetupConflictsBecomeProblems` — blocking 충돌은 스냅숏 blocking, 수락된 경고는 `acceptedConflicts` 에 있고 문제에서 빠짐
- `TestEffectiveShowsSetupDiffSinceApproval` — 승인 뒤 항목 추가·제거·새 버전이 차이로 보임
- `TestSetupImpactListsApprovedTasks` — 항목 변경의 영향에 그 프로젝트의 승인된 태스크가 나옴, 다른 도구의 태스크는 안 나옴
- `TestProtectedSetupPresenceRule` — `EnsureBuiltins` 가 규칙을 만들고, 보호 거부가 적용됨

`service/ladotask`:

- `TestSetupChangeAfterApprovalNeedsReview` — HARNESS §15 항목 12: 실제 store·policy·setup 으로 승인 → 항목 새 버전 → 태스크 Review required(`rules_changed`), 되돌리면 다시 승인 상태 해시, 승인이 setup 버전을 고정함

## 수용 게이트

- [ ] S11-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S11-G1: ladopolicy 필수 테스트 7개 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/service/ladopolicy/ TestSnapshotFormatIsLadoPolicy2 TestResolveIncludesSetupOfTheAgentTool TestSetupBindingsResolveLikeRules TestSetupConflictsBecomeProblems TestEffectiveShowsSetupDiffSinceApproval TestSetupImpactListsApprovedTasks TestProtectedSetupPresenceRule
  EXPECT: GO_TESTS_OK 7/7
- [ ] S11-G2: ladotask 필수 테스트 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/service/ladotask/ TestSetupChangeAfterApprovalNeedsReview
  EXPECT: GO_TESTS_OK 1/1
- [ ] S11-G3: 5·6단계의 규칙·대기열 테스트가 그대로 통과(패키지 전체)
  CHECK: bash .claude/gate/lado-local-setup.sh go-pkg ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoqueue/ ./internal/service/ladogate/
  EXPECT: GO_PKG_OK
- [ ] S11-G4: 정책 패키지 race 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race ./internal/service/ladopolicy/ ./internal/service/ladoqueue/
  EXPECT: GO_RACE_OK
- [ ] S11-G5: 컴파일러 이름이 바뀌었고 옛 형식 테스트는 없다
  CHECK: bash .claude/gate/lado-local-setup.sh has backend/internal/service/ladopolicy/snapshot.go '"lado-policy/2"' && bash .claude/gate/lado-local-setup.sh none backend/internal/service/ladopolicy 'TestSnapshotFormatIsLadoPolicy1'
  EXPECT: NONE_OK
- [ ] S11-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S11-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `c630edbf4`(docs(lado): record S10), 브랜치 `feat/lado-local-setup`, 트리 깨끗. Linux, go1.27.1.
- 출발점: `ladopolicy` 는 `lado-policy/1`(setup 없음), `Deps` 에 setup 포트 없음, 내장 규칙 3개(보호 2). `ladosetup` 은 충돌 검사(`Conflicts`)·AO 층(`CurrentAOSystem`)·세션 전달까지 있고 범위 해석은 없다(`offForProject` 는 Global→Project 만, 태스크 범위 없음). store 는 승인 트랜잭션이 `snapshot.SetupVersionIDs` 를 이미 고정한다(S04). `ladotask` 승인은 `Policy.Snapshot` 결과를 그대로 store 에 넘긴다.
- 앞 단계 메모 중 이 단계 몫:
  - S04: `ladotask` 승인이 `SetupVersionIDs` 를 채우면 `ApproveLadoPlan` 이 고정, 끝난 항목·없는 버전은 `ErrLadoRulesChanged`; 승인 뒤 비교는 `LadoPolicySnapshot(approvalID).SetupVersionIDs`.
  - S06: 실효 setup → `SetupContent{ItemID, Tool, Kind, Name, SHA256, Content(settings·서버는 블롭)}`, `Moved` = 관리 Claude 파일·폴더 항목 경로, `Ceiling` = 해석의 ceiling, `ProjectSettings`, `Acceptances` = live 수락; 문제 = 수락 안 된 결과의 `Problem()`, `acceptedConflicts` = 수락된 결과; `aoSystem` = `CurrentAOSystem()` 둘째 값(오류면 해석 실패). 원래 위치에 남은 설정을 넣을지는 S11 결정. 남은 틈: 플러그인 업데이트는 "결정 2 의 설치된 것의 해시(S11 스냅숏)"가 다시 보기를 맡는다.
  - S08: 해석은 버전만(`TestDriftNeverChangesResolution`).
  - S09·S10: 바인딩 해석은 `offForProject` 와 같은 순서(Global → Project → Task), 행 없음 = 켜짐. 실행 홈·덧씌우기는 스냅숏 고정 버전을 쓴다.

## After — 수행 후 실측

- 커밋: `100883796` feat(lado): local setup in the rule snapshot (lado-policy/2) — 코드·테스트. 이 엔트리는 다음 커밋.
- 바꾼 것:
  - `service/ladopolicy/setup.go`(새): 포트 `SetupSource{EffectiveSetup(ctx, SetupScope)}`, `SetupScope{ProjectID, TaskID, Harness, Ceiling, Change}`, `SetupResolution{Items, Problems, Accepted, AOSystemSHA256}`, `SetupItemState{ItemID, Tool, Kind, Name, VersionID, Version, SHA256, On, Source, Global, Project, Task, InstalledSHA256}`, `AcceptedConflict{Code, ItemID, Detail, AcceptanceID}`, `SetupChange`, `ImpactOfSetupChange`, `Resolution.SnapshotSetup()`, `diffSetup`, 오류 `LADO_SETUP_LOAD_FAILED`(500, 원인은 로그).
  - `resolve.go`: `Input.Setup`, `Resolution.Setup`; setup 문제를 `Problems` 에 합친다. 정렬에 details 를 마지막 키로 더했다(같은 코드의 충돌 둘이 들어온 순서와 상관없이 같은 바이트).
  - `snapshot.go`: `Compiler = "lado-policy/2"`; `aoRules` 다음 `setup`(tool·kind·name, 동점이면 itemId) · `aoSystem{"sha256"}` · `acceptedConflicts`(code·itemId·detail, 동점이면 acceptanceId). setup 항목은 켜진 것만.
  - `service.go`: `Deps.Setup`; `projectInput`·`taskInput` 이 규칙과 setup 까지 채운다(규칙의 ceiling 을 `rulesCeiling` 으로 먼저 구해 넘김); `Snapshot` 이 `SetupVersionIDs` 를 채운다; `ApprovedView.SetupAdded/SetupRemoved/SetupChanged`.
  - `impact.go`: 규칙 영향과 setup 영향이 같은 `impactOf(ctx, rules, after)` 를 쓴다.
  - `builtins.go`·`domain/lado_rule.go`: `setup.change.requires_presence`(`domain.LadoRuleSetupPresence`) — 보호, Global on, 범주 없음, `workflow_gate` 만이라 지시문은 작업자 프롬프트에 안 들어간다. 거부는 기존 보호 규칙 경로 그대로(바인딩·편집·보관·ceiling 모드 = `LADO_RULE_PROTECTED`, 삭제 = `LADO_RULE_BUILTIN`).
  - `service/ladosetup/effective.go`(새): `EffectiveSetup`(포트 구현), `ScopeConflicts`(앱 목록용, 아래 2), `installedSum`(아래 1). `conflicts.go` 의 `installedPlugins` 가 설치 경로 대신 `pluginInstall{InstallPath, Version, GitCommitSha}` 를 준다. `Store` 포트에 `ListLiveLadoConflictAcceptances`.
  - 테스트: 필수 8개 + `TestEffectiveSetupHashesInstalledPlugins`·`TestScopeConflictsShowsWhatSnapshotsLeaveOut`(ladosetup). 내장 규칙이 4개가 되어 고친 기존 테스트: `TestBuiltinsAreTheStepFiveCatalog`, `TestDryRunWritesNothing`, `TestEnsureBuiltinsSeedsOnce`, `TestSnapshotMatchesCurrentSHA`, `TestResolveBindings`(ladopolicy), `TestWireLadoWorkSeedsBuiltinRules`(daemon). `fullInput` 은 Codex → Claude Code 로 바꾸고 setup 을 실었다(Codex 쪽에는 경고·수락이 생길 수 없어서). `TestSnapshotHashFollowsWhatApplies` 에 setup 사례 9개.
- 엔트리와 다르게 한 것과 이유:
  1. **설치된 플러그인 해시**(엔트리에 없음): 켜진 Claude `enabledPlugins` 항목은 `installedSha256` 을 싣는다 — 그 값이 켜는 플러그인마다 `installed_plugins.json` 의 user 설치(installPath·version·gitCommitSha)를 정규 JSON 으로 해시, 파일을 못 읽으면 그 사실도 해시에 든다. 플랜 결정 2("The snapshot records a hash of what is installed")와 S06 회고가 이 단계 스냅숏에 맡긴 몫. `lastUpdated` 같은 시각과 꺼진 플러그인은 넣지 않아 자동 업데이트 확인만으로는 해시가 안 바뀐다. 스냅숏 필드는 `omitempty` 라 다른 항목 모양은 엔트리 그대로.
  2. **저장소 `.claude/settings.json` 은 스냅숏에 안 넣음**: `LADO_SETUP_PROJECT_OVERRIDES` 는 본 체크아웃 작업 트리 파일을 읽어야 나오는데, 결정 l(저장소 추적 파일은 스냅숏에 해시하지 않고 기준 브랜치로 간다)과 S05 회고("표시용")에 맞춰 `EffectiveSetup` 은 `ProjectSettings` 를 비운다. 앱 목록(`GET /lado/setup/conflicts`)을 위해 `ScopeConflicts` 가 두 도구 항목 + 저장소 파일로 결과(수락 표시 포함)를 준다.
  3. **원래 위치에 남은 설정은 넣지 않음**(S06 이 S11 에 맡긴 결정): 해석은 관리 항목의 저장된 버전만 본다(결정 n·S08). 넣으면 항목 id 가 없어 수락할 수 없는 경고가 늘 남고, Claude 가 직접 쓴 키 하나로 승인된 태스크가 다시 보기로 간다.
  4. **`SetupItemState` 에 꺼진 항목도**: `Items` 는 도구의 live 항목 전부(`On`, `Global/Project/Task`)이고 스냅숏은 켜진 것만 싣는다. S16 의 태스크 탭·프로젝트 규칙 행(상속/켜기/끄기와 출처)이 꺼진 항목도 보여야 해서 규칙의 `Entry` 꼴을 따랐다.
  5. **`SetupScope.Change`**: 영향 dry run 을 위해 포트가 "변경을 적용했다고 치고" 해석한다(같은 메서드, 필드 하나). 규칙 변경의 영향은 setup 을 다시 구하지 않는다 — ceiling 이 바뀌는 규칙 변경은 어차피 해시가 바뀐다.
  6. **`ladotask` 코드는 안 바꿈**: 승인은 `Policy.Snapshot` 이 준 스냅숏을 그대로 store 로 넘기므로 `Snapshot` 이 `SetupVersionIDs` 를 채우는 것으로 끝났다(`TestSetupChangeAfterApprovalNeedsReview` 가 고정 확인).
  7. "되돌리면 다시 승인 상태 해시": 버전은 되돌릴 수 없어(새 버전 = 새 id) 테스트는 프로젝트 `force_off` → `inherit` 로 되돌림을 보이고, 새 버전은 Review required 로 끝난다.
- 확인한 명령과 결과:
  - `go test -count=1 ./internal/service/{ladopolicy,ladotask,ladoqueue,ladogate,ladosetup}/ ./internal/domain/` ok; `go test -count=1 ./internal/daemon/ ./internal/httpd/controllers/` ok; `go test -count=1 -run Lado ./internal/storage/sqlite/store/` ok.
  - `bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestEffectiveSetupHashesInstalledPlugins TestScopeConflictsShowsWhatSnapshotsLeaveOut` → `GO_TESTS_OK 2/2`; `no-home-lookups backend/internal/service/ladosetup` → `NO_HOME_LOOKUPS`.
  - golangci-lint v2.13.2(오프라인 빌드) `run --path-mode=abs` ladopolicy·ladosetup·ladotask·daemon·domain → 0 issues(처음 2건 nilerr·prealloc 고침).
  - 변이 확인: `state()` 에서 태스크 바인딩이 프로젝트 바인딩을 못 이기게 바꾸면 `TestSetupBindingsResolveLikeRules/task` 가 실패(되돌림).
- 게이트(코드 커밋 `100883796` 에서): `gate-check.sh --run` → `GATES=8 MET=8 UNMET=0`(44 s). 엔트리 커밋 뒤 다시 돌려 HEAD 에 재기록.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 포트와 `ladosetup` 구현, 해석·스냅숏 `lado-policy/2`·승인 고정·미리보기 차이·setup 영향·보호 규칙, 필수 테스트 8개. 변경 API(S12)·HTTP DTO(S13)는 없다. 엔트리 밖은 After 의 1·2(플러그인 해시, `ScopeConflicts`).
- 남은 틈:
  - `lado-policy/1` 로 한 승인은 모두 한 번 Review required 가 된다(결정 m, 컴파일러 이름이 해시에 든다). 새 내장 규칙도 모든 스냅숏의 `rules`·`workflowGates` 에 든다.
  - 해석마다 store 를 몇 번 읽고(항목·바인딩 3종·수락), 켜진 settings·서버의 블롭과 `enabledPlugins` 가 켜졌으면 `installed_plugins.json`·플러그인 `hooks.json` 을 읽는다. 태스크 목록(`CurrentSHA256s`)은 태스크마다 그렇다 — 항목이 수십 개라 문제없고, 느려지면 호출당 캐시.
  - 플러그인 해시는 user 설치만 본다(project·local scope 설치, 저장소 settings 의 `enabledPlugins` 는 S06 처럼 안 봄). 플러그인 폴더 내용(스킬·명령)은 해시하지 않고 설치 기록(경로·버전·커밋)만 — 같은 버전을 덮어쓴 설치는 못 잡는다.
  - 프로젝트 범위 해석은 그 프로젝트 작업자 에이전트의 도구 항목만 준다: 작업자 에이전트가 없는 프로젝트의 미리보기에는 setup 항목이 없다. S16 프로젝트 규칙 페이지가 두 도구 항목을 다 보여야 하면 인벤토리 managed 줄 + 바인딩 목록이 필요하다(아래 S13 메모).
  - `setupFailed` 는 `LADO_SETUP_LOAD_FAILED` 하나로 감싼다(블롭이 없거나 상함, store 오류). 규칙 변경의 영향 계산도 프로젝트 단위 setup 실패면 실패한다(태스크 단위는 로그 후 제외).
- 다음 단계 메모:
  - **S12(변경)** — 영향 함수: `policy.ImpactOfSetupChange(ctx, ladopolicy.SetupChange{…}) (ladopolicy.Impact, error)`(규칙 영향과 같은 `Impact{Projects, Tasks}`). 변경별 모양:
    - create·edit·drift keep: `Item: &domain.LadoSetupItemWithVersion{Item, Version}`(새 항목은 아직 저장 안 된 id, 편집은 같은 id + 새 버전 id·번호·sha), `Content`: settings·MCP 서버면 그 버전의 정규 JSON 바이트(충돌 검사가 읽음 — 없으면 블롭에서 찾다가 실패), 파일 종류는 nil 이어도 된다.
    - delete: `Remove: itemID`. binding: `Binding: &domain.LadoSetupBinding{ItemID, Scope, ScopeID, State}` — `inherit` 은 그 범위 행 삭제(Global inherit = 켜짐). accept: `Accept: &domain.LadoConflictAcceptance{ID(임시), Code, ItemID, Detail, ContentSHA256, Scope, ScopeID}`; revoke: `Revoke: id`. drift revert 는 버전이 안 바뀌니 영향 없음(`noImpact` 꼴로 빈 목록).
    - 서비스 연결: ladosetup 이 ladopolicy 를 import 하므로 반대 방향은 안 된다 — S12 의 `changes.go` 는 `ImpactOfSetupChange` 를 가진 작은 인터페이스(예: `Deps.Impact`)를 `ladosetup` 쪽에 두고 데몬(S13)이 정책 서비스를 넣는다.
    - presence 규칙의 id 는 `domain.LadoRuleSetupPresence`(감사 payload 에 쓸 수 있음). 판정은 그대로 `domain.LadoSetupNeedsPresence`.
  - **S13(DTO·배선)** — `EffectiveView` 에서 옮길 필드: `view.Setup.Items[]`(`itemId, tool, kind, name, versionId, version, sha256, on, source{scope, scopeId, state}, global, project, task, installedSha256`), `view.Setup.AOSystemSHA256`, setup 충돌 = `view.Setup.Problems`(문제 꼴, `details.item`; 이미 `view.Problems` 에도 있음), 수락 = `view.Setup.Accepted[]{code, itemId, detail, acceptanceId}`, 승인 차이 = `view.Approved.SetupAdded/SetupRemoved/SetupChanged`(item id; 이름은 `view.Approved.Content.Setup` 과 지금 `Items` 에서), 승인 스냅숏의 setup = `view.Approved.Content.Setup`·`AOSystem`·`AcceptedConflicts`. 실행 패널 스냅숏 DTO 의 setup 이름은 스냅숏 JSON 의 `setup[].name`. `GET /lado/setup/conflicts?projectId=&taskId=` 는 `setupSvc.ScopeConflicts(ctx, ladosetup.ScopeConflictsInput{ProjectID, TaskID, Ceiling})` — Ceiling 은 정책의 해석 ceiling(`policy.Effective(…).Ceiling`, Global 이면 아무 프로젝트 없이 규칙 ceiling 이 필요하니 비워도 된다: 기본 모드 검사만 빠짐). 결과는 S06 의 `Conflict` 꼴(수락된 것은 `AcceptanceID`). 배선: `ladopolicy.Deps{…, Setup: setupSvc}`.
  - **S14(번역)**: 새 코드 `LADO_SETUP_LOAD_FAILED`. 렌더러 `lib/lado-rules.ts` 의 내장 규칙 표에 `setup.change.requires_presence`(seed 이름 "Local setup changes need the desktop app's confirmation", 이름·설명 키 8개 로캘) — 없으면 영어 seed 이름이 그대로 보인다. review 이유는 그대로 `rules_changed`(문구는 S16 "규칙이나 로컬 설정이 바뀜").
  - **S16(화면)**: 태스크 규칙 탭의 setup 행은 `Items`(꺼진 것 포함)와 `Task` 상태로 상속/켜기/끄기, 출처는 `source`. 차이는 `SetupAdded/Removed/Changed`. `installedSha256` 이 바뀐 `enabledPlugins` 는 "설치된 플러그인이 바뀜"으로 보여 줄 수 있다.
  - **S17(문서)**: HARNESS §8 "As built in step 7" — `lado-policy/2` 필드와 순서, 켜진 항목만, `installedSha256`(결정 2의 실제 모양), 저장소 settings 와 원래 위치 설정은 스냅숏 밖(위 2·3), `SetupItemState` 의 꺼진 항목, `setup.change.requires_presence` 의 지시문은 프롬프트에 안 감; §14 표의 Registered 칸 "Step 7, built-in protected rule".
