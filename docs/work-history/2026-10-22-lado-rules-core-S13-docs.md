# 2026-10-22 LADO 규칙 코어 — S13 문서(TDD·HARNESS·PRD·LADO.md)

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§1 Decisions, §10) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

5단계에서 실제로 만든 것과 그 결정을 스펙 문서에 기록한다. 이전 단계들(4a·4b)도 끝에서 TDD·ADR 을 "as built" 로 고쳤다. 스펙과 코드가 어긋나면 다음 단계(6 큐·디스패처)가 틀린 전제에서 출발한다.

## 범위

- 고친다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, 필요할 때만 `docs/LJY-PRD/PRD.md`, `LADO.md`, `LADO.ko.md`.
- **코드는 고치지 않는다.** 문서가 코드와 다르면 코드를 근거로 문서를 쓴다(필요하면 `git log`·`git show` 로 S01~S12 커밋을 읽는다). 코드에서 버그를 발견하면 고치지 말고 회고에 적는다.
- 문서 언어는 각 문서의 기존 언어(영어 스펙, `LADO.ko.md` 는 한국어)와 문체를 따른다. 경로 표기 규칙(TDD 머리말: Go 코드는 `backend/internal/` 생략)도 그대로.

## 절차 (빠짐없이)

1. TDD §3.1 Rule snapshot 행: 승인(`approval_id`)과 함께 고정, `lado_policy_snapshot_rules` 가 쓴 규칙 버전을 적는다는 것. 규칙 테이블(`lado_rules`·`lado_rule_versions`·`lado_rule_bindings`)도 표에 한 행.
2. TDD §3.2: Review required(승인 스냅숏 sha ≠ 지금 해석한 sha — 5단계는 읽을 때 파생, 6단계 큐가 같은 비교) / Blocked(승인 스냅숏의 blocking 문제) — 5단계의 실제 파생 규칙으로.
3. TDD §5.2 "As built in step 4b" 아래에 "As built in step 5" 문단: 승인 트랜잭션의 스냅숏, 본문 `{sha256, policySha256}`(policySha256 은 스키마상 선택·서비스가 요구), main 확인창의 규칙 요약, `LADO_RULES_CHANGED`, review_required·blocked 의 재승인. "Steps 5 and 6 add …" 문장은 "Step 5 added the rule snapshot; step 6 adds the queue entry" 로.
4. TDD §6.6 끝: 결정(2026-10-20) — 권한 상한 기본 `auto`, Codex 는 auto 에서 workspace-write + on-request + 네트워크 없음이라 commit·push·네트워크마다 승인, 소유자가 presence 확인으로 상한을 올린다(`PATCH /lado/rules/agent.permission.ceiling`). 실행에 적용하는 것은 6단계.
5. TDD §9: 해석·스냅숏·프롬프트 컴파일이 5단계에 있고, 워커 프롬프트 주입·상한 적용은 6단계라는 것.
6. TDD §13 Rules 행: `GET, POST /lado/rules`; `GET, PATCH, DELETE /lado/rules/{ruleId}`(PATCH 는 상한 값을 바꿀 때 P); `PUT /lado/bindings`; `GET /lado/effective?projectId=|taskId=`. 변경 라우트의 `dryRun`.
7. TDD §14: 게이트 3 → `service/ladopolicy` `TestResolveBindings`·`TestResolveCapabilities`·`TestResolveConflicts`(HARNESS §15 1·2·3·7·8); 게이트 4 → 5단계는 `TestDisablingKeepsHistoryAndSnapshots`·`TestTaskStateFollowsRuleChanges`, 큐 진입 쪽은 6단계.
8. TDD §15: 5번 항목에 "Built 2026-10-22 …" 요약(무엇을 만들었고 무엇을 6·7·8 단계로 넘겼는지 — 플랜 §1 Out). 4b 항목의 "step 6 adds the rule snapshot and the queue entry to that transaction" 을 "step 5 added the rule snapshot to that transaction; step 6 adds the queue entry" 로.
9. HARNESS §4 끝: 트리거 — 5단계 규칙은 그 범위의 모든 LADO 실행에 적용되고, 트리거 판정은 "모든 실행" 이 아닌 첫 트리거와 함께 온다. §5: 저장된 스키마(name, category, instruction, enforcement, config, conflictsWith; 버전·내장·보호). §8: 활성 버전 = 최고 번호, 바인딩은 활성 버전을 따른다, AI 초안(HAR-006)은 이후. §12: 실제 화면 구성(설정 → 규칙, 프로젝트 설정 → 규칙, 태스크 뷰 규칙 탭, 보드 상태). §14: 단계별 등록(5: `plan.approval.required`·`agent.permission.ceiling`, 선택 내장 `git.commit.message.korean` 은 프롬프트만 — 검증기는 8; 7: `setup.change.requires_presence`; 8: 나머지 넷). §15: 각 항목을 덮는 테스트 이름(1·2·3·4·7·8) 또는 담당 단계(5·6·9~15).
10. PRD 는 요구 행의 상태·설명이 실제와 달라졌을 때만 고친다(예: HAR-001~005 의 LADO change 칸에 구현 위치).
11. `LADO.md`·`LADO.ko.md` 상태표에 5단계 행(In progress / 진행 중 — 머지는 메인 세션이 PR 뒤에 바꾼다).

## 마무리 (모든 단계 공통)

1. 커밋(영어 conventional commit, 예 `docs(lado): record step 5` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S13-docs.md` 로 전건 MET 를 확인한다.

## 수용 게이트

- [ ] S13-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S13-G1: TDD 가 5단계를 기록했다
  CHECK: bash .claude/gate/lado-rules-core.sh has docs/LJY-PRD/TDD.md 'policySha256' 'lado_policy_snapshot_rules' '/lado/effective' 'PUT /lado/bindings' 'step 5 added the rule snapshot' 'LADO_RULES_CHANGED' 'TestResolveBindings'
  EXPECT: HAS_OK
- [ ] S13-G2: HARNESS 가 5단계를 기록했다
  CHECK: bash .claude/gate/lado-rules-core.sh has docs/LJY-PRD/HARNESS.md 'conflictsWith' 'agent.permission.ceiling' 'TestResolveCapabilities' 'TestDisablingKeepsHistoryAndSnapshots'
  EXPECT: HAS_OK
- [ ] S13-G3: LADO 소개 문서 상태표
  CHECK: bash .claude/gate/lado-rules-core.sh has LADO.md 'Rules core' && bash .claude/gate/lado-rules-core.sh has LADO.ko.md '규칙 코어'
  EXPECT: HAS_OK
- [ ] S13-G4: 최종 회귀 — 백엔드 LADO 패키지·저장소 LADO 테스트
  CHECK: cd backend && go test -count=1 ./internal/domain/ ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoproject/ ./internal/service/ladogate/ ./internal/service/ladopresence/ ./internal/httpd/... && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ ./internal/daemon/ && echo S13_BACKEND_OK
  EXPECT: S13_BACKEND_OK
- [ ] S13-G5: 최종 회귀 — 생성물 드리프트 없음
  CHECK: bash .claude/gate/lado-rules-core.sh sqlc-clean && bash .claude/gate/lado-rules-core.sh api-clean
  EXPECT: API_CLEAN
- [ ] S13-G6: 최종 회귀 — 빌드·vet·타입체크
  CHECK: bash .claude/gate/lado-rules-core.sh go-build && bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S13-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
