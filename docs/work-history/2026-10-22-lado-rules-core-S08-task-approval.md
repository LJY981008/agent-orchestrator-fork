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

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
