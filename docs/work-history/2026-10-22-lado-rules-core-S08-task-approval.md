# 2026-10-22 LADO 규칙 코어 — S08 ladotask: 승인 스냅숏·정책 해시·상태 보강

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§6, §7 마지막 행) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

계획 승인이 규칙 스냅숏을 같은 트랜잭션에 고정하게 하고(TDD §5.2 — 스냅숏은 큐 진입 = 승인 때 고정), 승인 요청이 사람이 본 규칙 스냅숏 sha256 을 함께 싣게 한다. 승인 뒤 규칙이 바뀌면 태스크가 Review required, 승인 스냅숏에 막는 문제가 있으면 Blocked 로 보이게 태스크 응답을 보강한다(HAR-005 "조용한 교체 없음").

## 범위

- 고친다: `backend/internal/service/ladotask/service.go`(`Policy` 인터페이스 — 플랜 §6 그대로, `Deps.Policy`), `plans.go`(`ApprovePlan(ctx, revisionID, sha256, policySHA256 string, proof)`), List·Get·View 와 approve/cancel/update 가 돌려주는 태스크에 `Plan.PolicySHA256` 보강.
- 고친다: `backend/internal/httpd/controllers/lado_plans_dto.go`(`ApproveLadoPlanRequest` 에 `PolicySHA256 string \`json:"policySha256,omitempty"\`` — 설명 태그: 승인 때 본 규칙 스냅숏, 서비스가 요구), `lado_plans.go`(핸들러가 넘긴다), `LadoTaskService` 인터페이스 시그니처, 컨트롤러 테스트의 가짜.
- 재생성: `npm run api`(openapi.yaml·schema.ts). 태스크 상태 문자열에 enum 태그가 있으면 `review_required`·`blocked` 를 더한다.
- 하지 않는다: 데몬 와이어링(S09 — 그 전까지 운영 경로의 Policy 는 nil 이라 승인이 `LADO_RULES_UNAVAILABLE` 로 거부된다. 의도된 중간 상태), 렌더러·main(S10·S12).

## 절차

1. `ApprovePlan`: 4b 검사(sha·최신·문제·blob·열린 태스크) 뒤 — `policySHA256 == ""` → 400 `LADO_POLICY_SHA_REQUIRED`; `Policy == nil` → 500 `LADO_RULES_UNAVAILABLE`; `Policy.Snapshot(task, rev)` 의 sha ≠ `policySHA256` → 409 `LADO_RULES_CHANGED`(메시지: 승인하는 동안 규칙이 바뀌었다, 다시 확인 후 승인); 같으면 `snapshot.ApprovalID = approval.ID` 로 store 에 넘긴다. 감사 payload 에 `policySnapshot`(sha)·`blocking` 추가. 스냅숏이 blocking 이어도 승인은 된다(TDD §5.2 — 상태가 Blocked 로 보인다).
2. 보강: `ApprovalValid()` 인 태스크만 `Policy.CurrentSHA256` 을 불러 `Plan.PolicySHA256` 에 넣는다. 오류는 debug 로그, 필드는 빈 값. 목록은 태스크마다 부른다(승인된 태스크는 적다).
3. 4b 테스트(`TestApprovePlanBindsTheExactRevision` 등)는 가짜 Policy 로 새 시그니처에 맞춘다 — 의미는 그대로.
4. 렌더러는 아직 `policySha256` 을 보내지 않는다(S10·S12). 필드가 omitempty 라 `npm --prefix frontend run typecheck` 는 그대로 통과해야 한다.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): fix the rule snapshot in plan approvals` · Co-Authored-By 유지). openapi.yaml·schema.ts 는 Go 변경과 같은 커밋.
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S08-task-approval.md` 로 전건 MET 를 확인한다.

## 필수 테스트

`backend/internal/service/ladotask/`:
- `TestApprovePlanStoresTheRuleSnapshot` — 승인 뒤 태스크 읽기의 `Plan.Snapshot.SHA256` 이 가짜 Policy 의 sha, 감사 payload 에 `policySnapshot`
- `TestApprovePlanRefusesChangedRules` — 요청의 policySHA256 ≠ 계산값 → 409 `LADO_RULES_CHANGED`, 승인·nonce 0
- `TestApprovePlanNeedsThePolicySHA` — 빈 policySHA256 → 400 `LADO_POLICY_SHA_REQUIRED`
- `TestTaskStateFollowsRuleChanges` — 승인 뒤 가짜 Policy 의 현재 sha 를 바꾸면 List·Get 의 상태가 `review_required`, 되돌리면 `approved`, blocking 스냅숏은 `blocked`
- `TestReviewRequiredTaskCanBeApprovedAgain` — review_required 태스크가 같은 리비전을 새 sha 로 다시 승인하면 `approved`

`backend/internal/httpd/controllers/`:
- `TestLadoAPI_ApprovePlanPassesThePolicySHA` — 본문의 `policySha256` 이 서비스로 그대로 간다(presence 검증 경로는 4b 테스트 그대로)

## 수용 게이트

- [ ] S08-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S08-G1: ladotask 필수 테스트 5종이 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladotask/ TestApprovePlanStoresTheRuleSnapshot TestApprovePlanRefusesChangedRules TestApprovePlanNeedsThePolicySHA TestTaskStateFollowsRuleChanges TestReviewRequiredTaskCanBeApprovedAgain
  EXPECT: GO_TESTS_OK 5/5
- [ ] S08-G2: 컨트롤러 필수 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_ApprovePlanPassesThePolicySHA
  EXPECT: GO_TESTS_OK 1/1
- [ ] S08-G3: ladotask·컨트롤러·apispec 패키지 전체 통과(4b 테스트 포함)
  CHECK: bash .claude/gate/lado-rules-core.sh go-pkg ./internal/service/ladotask/ ./internal/httpd/...
  EXPECT: GO_PKG_OK
- [ ] S08-G4: 생성 API 파일이 커밋과 같다
  CHECK: bash .claude/gate/lado-rules-core.sh api-clean
  EXPECT: API_CLEAN
- [ ] S08-G5: 승인 본문 필드가 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/api/schema.ts 'policySha256'
  EXPECT: HAS_OK
- [ ] S08-G6: 프런트 타입체크 그대로 통과
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S08-G7: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S08-G8: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `962009f39634a7bd03bfc6d26d1b1c08e4d3730a`(`docs(lado): record S07`), 트리 깨끗.
- `ladotask.ApprovePlan(ctx, revisionID, sha256, proof)` 는 4b 검사(리비전·열린 태스크·sha·최신·문제·blob) 뒤 store 에 `snapshot = nil` 로 승인했다(주석 "no rule snapshot until the task service reads the rules"). store 의 `ApproveLadoPlan(…, snapshot)`(S04)·태스크 읽기의 `Plan.Snapshot`(S04)·도메인 `PolicySHA256`·`RulesChanged`·`review_required`·`blocked`(S02)·정책 서비스의 `Snapshot`·`CurrentSHA256`(S07)은 이미 있었다. 승인 DTO 는 `{sha256}` 만, 태스크 `state` enum 은 `draft,planned,approved,cancelled,closed_unmerged`.
- 전제 확인: 렌더러의 `ladoTaskStatus` 는 `default` 가 있는 switch 라 enum 이 늘어도 타입체크가 깨지지 않는다. presence `Verifier` 는 nonce 를 메모리에 두지 않는다(재사용 거부는 store) — 컨트롤러 테스트가 같은 서명 nonce 로 두 번 보낼 수 있다. 스냅숏 규칙 행은 `lado_rules`·`lado_rule_versions` FK 를 가지므로 가짜 Policy 는 `Rules` 를 비운 스냅숏을 낸다.
- 테스트는 구현 뒤에 썼다. 첫 PASS 를 믿지 않고 변이 검사로 확인했다(After).

## After — 수행 후 실측

- 코드 커밋 `41fe3a7a6` `feat(lado): fix the rule snapshot in plan approvals`: 12 파일, +475 −47(openapi.yaml·schema.ts 같은 커밋).
  - `service.go`: `Policy` 인터페이스(플랜 §6 그대로), `Deps.Policy`, `withPolicy(ctx, task)` — `Policy != nil && ApprovalValid()` 일 때만 `CurrentSHA256` → `Plan.PolicySHA256`, 오류는 debug 로그·빈 값. `List`(태스크마다)·`Get`(두 반환 경로)·`UpdatePreferences`·`Cancel` 이 거친다.
  - `plans.go`: `ApprovePlan(ctx, revisionID, sha256, policySHA256, proof)`. 4b 검사 뒤 `ruleSnapshot`: 빈 해시 400 `LADO_POLICY_SHA_REQUIRED` → Policy nil 500 `LADO_RULES_UNAVAILABLE` → `Policy.Snapshot(task, rev)` 오류는 apierr 면 그대로(정책 서비스의 `LADO_PLAN_BLOB_MISSING` 409·`LADO_RULES_LOAD_FAILED` 500 등), 아니면 500 `LADO_RULES_LOAD_FAILED` → sha 불일치 409 `LADO_RULES_CHANGED`(details 없음: 현재 해시를 돌려주면 클라이언트가 보지 않고 재시도할 수 있다). 같으면 `snapshot.ApprovalID = approval.ID` 로 `&snapshot` 을 store 에 넘기고 감사 payload 에 `policySnapshot`·`blocking`. blocking 스냅숏도 승인된다. `Plan`(PlanView.Task)·`View`(TaskView.Task)도 `withPolicy`.
  - 컨트롤러: `ApproveLadoPlanRequest.PolicySHA256` `json:"policySha256,omitempty"`(설명 태그), 핸들러가 그대로 넘김, `LadoTaskService.ApprovePlan` 시그니처, 가짜의 `gotPolicySHA`. 태스크 `state` enum `draft,planned,review_required,blocked,approved,cancelled,closed_unmerged`(도메인 순서, 설명에 두 상태 뜻).
  - 테스트: 필수 5종 + `TestLadoAPI_ApprovePlanPassesThePolicySHA`(본문의 두 해시가 서비스로, 필드가 없으면 빈 값이 가고 서비스의 400 이 그대로 나감). 4b 테스트 5곳은 `policyA` 를 더했을 뿐 단언은 그대로. 픽스처: `taskFixture.dataDir`(`sqlitetest.MustOpenAt`) + raw 연결(행 수·감사 payload, S07 과 같은 방식), `fakePolicy{sha, blocking, currentErr}`(규칙 = 한 해시; `set` 으로 바꾸면 Snapshot·CurrentSHA256 이 함께 바뀐다), `planned`·`count`·`approvalEvents`·`states`/`wantState`(List·Get·View·Plan·UpdatePreferences 다섯 경로의 상태를 한 번에 단언), `apiKind`.
- 플랜에 없어 이 단계에서 정한 것:
  - 보강 범위에 `Plan`(PlanView)의 태스크를 더했다: `LadoPlanResponse.task.state` 가 목록·상세와 다르게 보이지 않게. 승인 응답은 태스크가 없어 보강할 것이 없다. `Create`·`Import` 의 새 태스크는 승인이 없어 건너뛴다.
  - 검사 순서: 4b 오류가 먼저다(다른 plan sha + 빈 정책 해시 → `LADO_PLAN_CHANGED`, 테스트로 고정). 정책 해시 거부는 store 호출 전이라 승인·proof·스냅숏·감사 행이 0 이다(테스트가 네 테이블을 센다).
  - `CurrentSHA256` 실패 = 빈 해시 = `approved` 로 보인다(스펙대로, 테스트로 고정). 렌더러의 Rules 탭은 S12 에서 effective 로 실제 차이를 보인다.
- 변이 검사(3회, 적용 → 필수 테스트 → 원복): 보강 제거 → 3개 실패, 정책 해시 비교 제거 → `RefusesChangedRules` 실패, store 에 스냅숏 대신 nil → 3개 실패. 생존 0.
- 추가 실행: `gofmt -l` 출력 없음. golangci-lint(저장소 `backend/.golangci.yml`, 앞 단계 세션이 빌드한 바이너리, `GOPROXY=off`) `./internal/service/ladotask/... ./internal/httpd/controllers/...` → `0 issues`.
- 게이트(HEAD `41fe3a7a6`, 코드 커밋 직후 깨끗한 트리): `GATES=9 MET=9 UNMET=0`(G1 `GO_TESTS_OK 5/5`, G2 `1/1`). 이 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. S02·S04·S07 이 도메인 상태·store 의 스냅숏 tx·`Snapshot`/`CurrentSHA256` 을 미리 깔아 두어서, 이 단계는 연결과 검사 순서만 정하면 됐다.
- 가짜 Policy 를 "스냅숏 해시 + 현재 해시" 두 값이 아니라 "규칙 = 한 해시" 로 둔 것이 테스트를 단순하게 했다: 실제 정책 서비스처럼 규칙이 바뀌면 다음 승인 스냅숏과 현재 해시가 함께 바뀌므로, "검토한 해시 ≠ 지금" 을 `set` 한 번으로 만든다.
- 상태 단언을 다섯 경로(`states`)로 묶어 "어느 반환 경로가 보강을 빠뜨렸나" 를 한 테스트가 잡는다. 경로를 하나만 보면 `View`·`Plan` 누락이 남는다.
- 다음 단계 메모:
  - S09: `wireLadoWork` 가 정책 서비스를 만들어 `ladotask.Deps.Policy` 에 넣기 전까지 운영 경로의 승인은 500 `LADO_RULES_UNAVAILABLE` 이다(의도된 중간 상태). `*ladopolicy.Service` 는 `ladotask.Policy` 를 그대로 만족한다.
  - S10: `lib/lado-errors.ts` 에 `LADO_POLICY_SHA_REQUIRED`(400)·`LADO_RULES_UNAVAILABLE`(500)·`LADO_RULES_CHANGED`(409)를 더한다. main 의 승인 본문은 `{"sha256":…,"policySha256":…}` — 서명 본문과 보낸 본문이 같아야 한다(presence 가 본문 해시를 묶는다).
  - S12: 태스크 상태 `review_required`·`blocked` 가 이제 목록·상세·플랜 응답에 나온다. 레인의 `ladoTaskStatus` 는 지금 `default` 로 Draft 처럼 보이고 `LadoLanes` 의 `approved` 필터 때문에 Plan 레인에 남는다 — S12 에서 라벨·톤을 붙인다. 목록은 승인된 태스크마다 `CurrentSHA256`(규칙·프로젝트·AO 지시 파일 읽기)을 부르므로, 느려지면 S07 메모대로 한 번 읽은 규칙으로 여러 태스크를 해석하는 도우미를 더한다.
