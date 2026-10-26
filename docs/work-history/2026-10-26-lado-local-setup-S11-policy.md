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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S12 가 쓸 영향 함수 모양, S13 이 DTO 로 옮길 `EffectiveView` 필드)
