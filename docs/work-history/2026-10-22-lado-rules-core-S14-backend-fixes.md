# 2026-10-22 LADO 규칙 코어 — S14 리뷰 반영: 백엔드

> 체인 `docs/chains/lado-rules-core-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md` · 브랜치 `feat/lado-rules-core` · 소유자 승인 2026-10-22("체인 2단계")

## 의도/목적 — 왜 이 작업을 하나

S01~S13 이 끝난 뒤 백엔드 리뷰(블로커·메이저 0, 마이너 8)에서 나온 지적 중 소유자가 고치기로 한 것을 고친다. 각 지적은 실제로 재현된 것이다. 심링크 지적(B3)은 고치지 않는다 — AO 자신도 같은 `agentRulesFile` 을 심링크를 따라 읽어 워커 프롬프트에 넣으므로, 스냅숏 해시는 AO 가 실제로 읽는 바이트와 같아야 한다(S15 가 이 결정을 TDD 에 적는다).

## 범위 (지적 번호 = 리뷰 번호)

- **B1 — 삭제된 규칙이 다른 규칙의 충돌 목록에 남으면 그 규칙의 충돌 편집이 막힌다.** 재현: A 가 B 와 충돌, B 삭제(스냅숏이 안 썼으면 허용), A 에 C 를 더하면 편집기는 `[B, C]` 를 보내고 `checkContent`(`service/ladopolicy/service.go` 의 충돌 검증)가 400 `LADO_RULE_CONFLICT_UNKNOWN`. 고침: **수정(Update)** 에서 활성 버전에 이미 있던 충돌 id 는 규칙이 없어도 받아들이고, 새로 더한 id 만 존재·자기 자신·보호 규칙 검사를 한다. 만들기(Create)는 그대로 전부 검사. 해석기는 이미 없는 id 를 무시한다(그대로 둔다).
- **B2 — 승인 도중 규칙이 삭제되면 409 가 아니라 500.** 재현: 승인 전에 해석한 스냅숏이 쓴 규칙 버전을, 트랜잭션 전에 다른 요청이 지우면 `lado_policy_snapshot_rules` 삽입이 FOREIGN KEY 로 실패해 `LADO_PLAN_APPROVE_FAILED`. 고침: `ApproveLadoPlan`(store) 트랜잭션 안에서 스냅숏의 각 규칙 버전이 아직 있는지 확인하고 없으면 새 도메인 sentinel `ErrLadoRulesChanged`(`domain/lado_policy.go`, `unwrapLadoSentinel` 에 추가)를 돌려준다 — 아무것도 쓰지 않는다(nonce 도). `ladotask.ApprovePlan` 이 그것을 409 `LADO_RULES_CHANGED` 로 바꾼다.
- **B4 — 태스크 목록이 승인된 태스크마다 규칙을 다시 읽는다**(태스크당 `ListLadoRules` 3쿼리 + `GetProject` + 지시 파일 읽기; 보드는 변경 이벤트마다 다시 읽는다). 고침: `ladopolicy.Service.CurrentSHA256s(ctx, tasks []domain.LadoTask) (map[domain.LadoTaskID]string, error)` — 규칙은 한 번, 프로젝트·AO 지시는 프로젝트당 한 번, 플랜 상한은 기존 캐시. `ladotask.Policy` 인터페이스에 더하고 `List` 가 쓴다(단건 경로는 `CurrentSHA256` 그대로, 내부는 같은 코드). 결과는 태스크별 `CurrentSHA256` 과 같아야 한다.
- **B5 — 권한 상한 PATCH 의 HTTP 테스트가 모두 통과시키는 가짜 검증기를 쓴다.** 고침: `lado_plans_test.go` 의 `signedRequest`/`presenceVerifier()` 꼴로 **실제** `ladopresence.Verifier` + **실제** `ladopolicy.Service`(`sqlitetest.MustOpen`) 를 묶은 컨트롤러 테스트: 헤더 없음 → 403 `LADO_PRESENCE_REQUIRED`, 서명 뒤 본문 변조 → 403 `LADO_PRESENCE_INVALID`, 올바른 증명 → 200·버전 2, 같은 증명 재사용 → 403 `LADO_PRESENCE_REPLAYED`. 각 경우 뒤 store 의 활성 버전 번호를 단언한다.
- **B7 — I/O 함수에 ctx 가 없다**(AGENTS.md "Use `context.Context` as the first argument for functions that do I/O"). 고침: `ReadAORules(ctx context.Context, project domain.ProjectRecord) []AORule`, 플랜 상한을 읽는 함수도 ctx 첫 인자. 호출부·테스트 갱신.
- **B8 — 쓰이지 않는 쿼리.** `queries/lado_rules.sql` 의 `ListLadoPolicySnapshotsByTask` 를 지우고 `npm run sqlc`.
- **L1 — 로컬 CI 미러의 golangci-lint v2.13.2 실패 2건(`unconvert`)**: `backend/internal/storage/sqlite/store/lado_rule_store_test.go:596` 과 `:599` 의 `string(ev.ProjectID)`(이미 string). 변환을 지운다. 이 단계 끝에 lint 전체가 0건이어야 한다.
- 하지 않는다: 프런트·문서(S15), B3·B6(설계 그대로).

## 절차

1. 각 지적마다 실패하는 테스트를 먼저 쓰고(재현), 고치고, 통과를 본다.
2. 기존 테스트는 그대로 통과해야 한다. 시그니처가 바뀐 곳(B7·B4 의 Policy 인터페이스)만 맞춘다 — ladotask 의 가짜 Policy 에 `CurrentSHA256s` 를 더한다.
3. 쿼리 파일은 ASCII 만, `npm run sqlc` 뒤 gen 을 읽어 확인.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `fix(lado): review fixes for the rules backend` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S14-backend-fixes.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `backend/internal/service/ladopolicy/`: `TestUpdateKeepsConflictsWithADeletedRule`(B1 — 재현 시나리오 그대로, 새로 더한 모르는 id 는 여전히 400), `TestCurrentSHA256sMatchesOneByOne`(B4), `TestCurrentSHA256sReadsRulesOnce`(B4 — 세는 store 래퍼로 태스크 3개에 `ListLadoRules` 1회)
- `backend/internal/storage/sqlite/store/`: `TestApproveLadoPlanRefusesARuleDeletedMeanwhile`(B2 — `ErrLadoRulesChanged`, 승인·nonce·스냅숏·감사 0행)
- `backend/internal/service/ladotask/`: `TestApprovePlanMapsADeletedRuleToRulesChanged`(B2 — 409 `LADO_RULES_CHANGED`)
- `backend/internal/httpd/controllers/`: `TestLadoAPI_CeilingChangeChecksPresenceEndToEnd`(B5 — 네 서브테스트)

## 수용 게이트

- [ ] S14-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S14-G1: ladopolicy 필수 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladopolicy/ TestUpdateKeepsConflictsWithADeletedRule TestCurrentSHA256sMatchesOneByOne TestCurrentSHA256sReadsRulesOnce
  EXPECT: GO_TESTS_OK 3/3
- [ ] S14-G2: store 필수 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/storage/sqlite/store/ TestApproveLadoPlanRefusesARuleDeletedMeanwhile
  EXPECT: GO_TESTS_OK 1/1
- [ ] S14-G3: ladotask 필수 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/service/ladotask/ TestApprovePlanMapsADeletedRuleToRulesChanged
  EXPECT: GO_TESTS_OK 1/1
- [ ] S14-G4: 컨트롤러 끝-끝 presence 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-tests ./internal/httpd/controllers/ TestLadoAPI_CeilingChangeChecksPresenceEndToEnd
  EXPECT: GO_TESTS_OK 1/1
- [ ] S14-G5: ctx 시그니처와 쓰이지 않는 쿼리 제거
  CHECK: bash .claude/gate/lado-rules-core.sh has backend/internal/service/ladopolicy/aorules.go 'func ReadAORules(ctx context.Context' && ! grep -rq 'ListLadoPolicySnapshotsByTask' backend/internal && echo S14_CLEANUP_OK
  EXPECT: S14_CLEANUP_OK
- [ ] S14-G6: 바뀐 패키지 전체 통과(-race 포함)와 저장소 LADO 테스트
  CHECK: bash .claude/gate/lado-rules-core.sh go-race ./internal/service/ladopolicy/ ./internal/service/ladotask/ && cd backend && go test -count=1 ./internal/httpd/... ./internal/domain/ && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ ./internal/daemon/ && echo S14_PACKAGES_OK
  EXPECT: S14_PACKAGES_OK
- [ ] S14-G7: 생성물 드리프트 없음·쿼리 ASCII
  CHECK: bash .claude/gate/lado-rules-core.sh sqlc-clean && bash .claude/gate/lado-rules-core.sh queries-ascii && bash .claude/gate/lado-rules-core.sh api-clean
  EXPECT: API_CLEAN
- [ ] S14-G8: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-rules-core.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S14-G10: golangci-lint v2.13.2 0건(CI lint 잡과 같은 명령)
  CHECK: cd backend && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --path-mode=abs && echo S14_LINT_OK
  EXPECT: S14_LINT_OK
- [ ] S14-G9: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
