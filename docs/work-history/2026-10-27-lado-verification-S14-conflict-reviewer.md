# 2026-10-27 LADO 검증·통합 승인 — S14 설정 충돌(보호 규칙 허용)·리뷰어 지침

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 q·s, §14) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

7단계가 8단계로 넘긴 두 가지. (1) 채택된 `permissions.allow` 가 보호 규칙이 막는 git 동작(`git push`, `gh pr merge` 등)을 허용하면 경고한다(HARNESS §10 "Allow rules against protected gates" — 게이트는 다른 곳에서 강제되므로 경고, 수락 가능). (2) AO 리뷰어는 자기 환경을 따로 만들어 채택 설정을 하나도 받지 못한다 — 개발자의 지침(CLAUDE.md·AGENTS.md)만 리뷰어 시스템 프롬프트에 붙여 같은 관례로 리뷰하게 한다. 설정·hook·스킬·MCP 는 주지 않아 리뷰어의 읽기 전용 도구 정책이 유지된다.

## 범위

- 바꾼다: `service/ladosetup/conflicts.go`(새 코드) + 테스트, `domain/lado_setup.go`(코드 상수가 거기 있으면).
- 만든다: `service/ladosetup/reviewer.go`(리뷰어 지침 절) + 테스트.
- 바꾼다(업스트림 훅, 작게): `review/launcher.go`(늦게 붙는 setter 하나, `prepareInvocation`·`prepareIdleInvocation` 에서 절 붙이기) + 테스트; `daemon/lado_verify_wiring.go` 또는 `daemon/lado_setup_wiring.go`(설치).
- 하지 않는다: 리뷰어에게 설정 홈·`-c` 옵션 주기(결정 s).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 충돌(`conflicts.go`): 기존 `LADO_SETUP_DENIES_GIT` 이 `bashRuleCovers` 로 deny 를 읽는 방식 그대로 `permissions.allow` 를 읽어, `git push`, `git update-ref`, `git merge`, `git reset`, `gh pr merge` 중 하나를 덮는 규칙(`Bash(git push:*)`, `Bash(git:*)`, `Bash(*)` 같은 접두 규칙)이면 경고 `LADO_SETUP_ALLOWS_PROTECTED`(세부: 규칙 원문과 덮는 명령). 수락 가능(다른 경고와 같은 경로). 보호 규칙은 늘 켜져 있으므로 조건 없이 검사한다. 렌더러 문구는 S16 이 더한다(코드는 API enum 에 들어가므로 S15 가 생성물을 다시 만든다).
2. 리뷰어 절(`reviewer.go`): `func (s *Service) ReviewerSection(ctx context.Context, worker domain.SessionID, harness domain.AgentHarness) (string, error)` — 리뷰어 도구(Claude Code 면 켜진 최상위 `*.md` 지침 항목들, Codex 면 `AGENTS.override.md`·`AGENTS.md` 중 글이 있는 첫 것)의 내용을, 워커가 LADO 실행이고 그 스냅숏의 도구가 리뷰어 도구와 같으면 스냅숏이 고정한 버전으로(7단계의 `LadoSetupVersionsForClientRequest` 경로), 아니면 그 프로젝트에 켜진 현재 버전으로. 결과는 `## User instructions (managed by LADO)` 절 하나(항목 없으면 빈 문자열). 관리 항목이 하나도 없으면(채택 전) 빈 문자열.
3. 런처 훅(`review/launcher.go`): 패키지에 늦게 설치되는 원천 하나 — 예: `func SetLadoSystemSection(f func(ctx context.Context, worker domain.SessionID, harness domain.AgentHarness) (string, error))`(동시성 안전, nil 이면 지금과 같음) 또는 런처 값에 대한 setter. `prepareInvocation` 과 `prepareIdleInvocation` 이 `system.md` 를 쓰기 전에 절을 받아 기존 글 뒤에 붙인다. 원천의 오류는 경고 로그를 남기고 절 없이 진행(리뷰가 LADO 때문에 막히지 않는다). 리뷰어 harness 는 `spec` 에서.
4. 데몬: LADO 설정 서비스가 만들어진 뒤 setter 에 `setup.ReviewerSection` 을 넘긴다.
5. 커밋 메시지 예: `feat(lado): warn on allow rules that cover protected git actions and give the reviewer the adopted instructions`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S14-conflict-reviewer.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestConflictAllowsProtectedGate`(`service/ladosetup`, 격리 홈) — `Bash(git push:*)`·`Bash(gh pr merge:*)`·`Bash(git:*)` → 경고(수락 가능), `Bash(git status:*)`·`Bash(npm test:*)` → 없음, deny 쪽 검사는 그대로
- `TestReviewerSectionUsesPinnedInstructions`(`service/ladosetup`, 격리 홈) — Claude 실행의 리뷰어(Claude)는 고정 버전 CLAUDE.md, 승인 뒤 고친 현재 버전이 아님; Claude 실행의 Codex 리뷰어는 현재 AGENTS.md; 관리 항목 없음 → 빈 문자열
- `TestReviewerSystemPromptCarriesTheLadoSection`(`review`) — 원천이 있으면 `system.md` 끝에 절, 없으면 지금 글 그대로, 원천 오류면 절 없이 계속(유휴 복원 경로도)
- 기존 `ladosetup` 충돌 테스트 전체가 그대로 통과

## 수용 게이트

- [ ] S14-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S14-G1: 설정 테스트 2개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladosetup/ TestConflictAllowsProtectedGate TestReviewerSectionUsesPinnedInstructions
  EXPECT: GO_TESTS_OK 2/2
- [ ] S14-G2: 설정 패키지 전체가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg-isolated ./internal/service/ladosetup/
  EXPECT: GO_PKG_OK
- [ ] S14-G3: 리뷰 런처 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/review/ TestReviewerSystemPromptCarriesTheLadoSection
  EXPECT: GO_TESTS_OK 1/1
- [ ] S14-G4: 리뷰 패키지 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/review/
  EXPECT: GO_PKG_OK
- [ ] S14-G5: 런처 편집은 작다
  CHECK: bash .claude/gate/lado-verification.sh diff-budget 30 backend/internal/review/launcher.go
  EXPECT: BUDGET_OK
- [ ] S14-G6: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S14-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 새 충돌 코드(S15·S16), setter 이름, 런처 줄 수)
