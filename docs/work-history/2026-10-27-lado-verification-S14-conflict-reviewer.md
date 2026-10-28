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

- 시작 HEAD `31668c4b3`(브랜치 `lado8/S14`, S02 레인 병합 뒤), 트리 깨끗. Ubuntu, go1.27.1(PATH 에 있음), 체인 무인 세션(`CHAIN_RUN=1`: Bash 명령 글에 "git push" 가 있으면 가드가 거부 — 그런 글은 Write/Edit 로 썼다).
- 출발점 실측:
  - `service/ladosetup/conflicts.go`: 코드 13개. `permissions` 는 `deny` 만 `bashRuleCovers` 로 읽고(`LADO_SETUP_DENIES_AO`, `LADO_SETUP_DENIES_GIT`), `allow` 는 읽지 않음(HARNESS §10 "Not built"). 코드 상수는 `domain/lado_setup.go` 가 아니라 `conflicts.go` 에 있음. `TestConflictChecksTable` 은 `conflictSeverity` 의 모든 코드에 맞는 행을 요구.
  - API: `LadoSetupConflictResponse.Code` 는 enum 없는 string(설명뿐), `Details` 설명에 키 목록.
  - `review/launcher.go`(905줄, 태그 이후 변경 0): `prepareInvocation` 과 `prepareIdleInvocation(spec)`(ctx 없음)이 `system.md` 를 AO 글만으로 씀. review 패키지에 로거·`t.Parallel` 없음. `ReviewerHarness` 값은 claude-code·codex 에서 `AgentHarness` 와 같음.
  - `ladosetup`: 고정/현재 버전 해석은 `sessionItems(tool, project, clientRequestID)`(실행이면 스냅숏 고정본, 고정 없으면 스냅숏 뒤 만든 항목, 아니면 프로젝트에 켜진 현재본). 스냅숏은 워커 도구의 항목만 고정(`ladopolicy.SetupScope.Harness`). `Sessions` 인터페이스는 `ListAllSessions` 만.
  - 데몬: 리뷰 런처는 `startSession` 안에서 LADO 보다 먼저 만들어짐; LADO 설정 훅은 `wireLadoSetup` 이 `deps.sessions != nil` 일 때 설치.
- 앞 단계 메모 중 이 단계 몫: S01·S02 회고에 S14 몫 없음(새 이름 목록뿐). S02 의 "렌더러 문구는 S16" 원칙만 따름.

## After — 수행 후 실측

- 커밋: `b0d57ed1d` feat(lado): warn on allow rules that cover protected git actions and give the reviewer the adopted instructions · 이 엔트리 커밋.
- 바꾼 것:
  - `ladosetup/conflicts.go`: `ConflictAllowsProtected = "LADO_SETUP_ALLOWS_PROTECTED"`(warning, 수락 가능), `protectedGitCommands`(git push, git update-ref, git merge, git reset, gh pr merge), `permissions()` 가 `allow` 의 규칙마다 `bashRuleCovers(rule, command)` 로 다섯 명령을 검사해 덮는 것이 있으면 결과 하나(Detail = 규칙 원문, details `rule`·`commands`). 조건 없이 검사(보호 규칙은 늘 켜짐). `bashRuleCovers` 주석에 하위 명령 예.
  - 새 `ladosetup/reviewer.go`: `(*Service).ReviewerSection(ctx, worker domain.SessionID, harness domain.AgentHarness) (string, error)`. 워커 행을 `Sessions.GetSession` 으로 읽고, 워커 harness 의 도구가 리뷰어 도구와 같으면 `sessionItems(tool, project, rec.ClientRequestID)`(워커가 받는 것 = 스냅숏 고정본), 다르면 `sessionItems(tool, project, "")`(프로젝트에 켜진 현재본). `instructions` 항목만, 글 trim, 빈 파일 건너뜀. 결과 `## User instructions (managed by LADO)` + 안내 한 줄(`reviewerInstructionsLead`) + 파일마다 `### <이름>`; Claude 는 CLAUDE.md 먼저·나머지 이름순, Codex 는 `codexInstructionOrder` 순 첫 글 있는 파일 하나. 다른 도구·없는 워커·관리 항목 없음·글 없음 → "".
  - `ladosetup/service.go`: `Sessions` 에 `GetSession` 추가(`*sqlite.Store` 가 이미 구현), 테스트 fake `fakeSessions.GetSession`.
  - 새 `review/lado_section.go`: `type LadoSystemSection func(ctx, worker domain.SessionID, harness domain.AgentHarness) (string, error)`, `SetLadoSystemSection(LadoSystemSection)`(`atomic.Pointer`, nil 이면 제거), `ladoSystemText(ctx, spec)`(빈 줄 + 절 + 줄바꿈; 오류는 `slog.Default().Warn` 후 ""; 공백뿐인 절도 "").
  - `review/launcher.go`(+6/−4 = 10줄): `prepareInvocation`·`prepareIdleInvocation` 의 `systemPrompt` 끝에 `+ ladoSystemText(ctx, spec)`, `prepareIdleInvocation` 에 ctx 인자(호출 1곳), 주석 `// LADO fork:` 2줄. Chat 리뷰어도 같은 `system.md` 를 읽으므로 함께 받음.
  - `daemon/lado_setup_wiring.go`: `ladoSetupDeps.reviewers func(review.LadoSystemSection)`(nil → `review.SetLadoSystemSection`), `wireLadoSetup` 이 세션 훅과 함께(`deps.sessions != nil`) `setup.ReviewerSection` 을 설치. 파일 머리 주석.
  - 테스트: `TestConflictAllowsProtectedGate`(새), `TestConflictChecksTable` 행 5개(새 코드 hit 2·miss 2, allow 규칙은 `LADO_SETUP_DENIES_GIT` 이 아님 1; `allow` 도우미), `TestReviewerSectionUsesPinnedInstructions`(새, `reviewer_test.go`), `TestReviewerSystemPromptCarriesTheLadoSection`(새, `review/lado_section_test.go`), `TestWireLadoInstallsTheSetupHooks` 에 리뷰어 절 설치·실제 store 로 Codex·Claude 리뷰어 절 확인.
- 엔트리와 다르게 한 것과 이유:
  1. setter 와 헬퍼는 `launcher.go` 가 아니라 같은 패키지 새 LADO 파일 `review/lado_section.go` — 업스트림 파일에는 호출만 남겨 diff 를 줄였다(TDD §1.2; 10줄/예산 30).
  2. 데몬 설치는 `daemon.go` 나 `lado_verify_wiring.go` 가 아니라 `lado_setup_wiring.go` `wireLadoSetup` 안. `daemon.go` 686-687 의 `ladoWorkDeps{…}` 줄은 S12 레인이 고칠 자리라 병합 충돌을 피했고, 설치가 세션 훅과 같은 조건이라 다른 부분의 테스트는 전역 원천을 설치하지 않는다. `reviewers` 필드는 테스트가 원천을 캡처하려고 둠.
  3. "스냅숏의 도구"는 워커 세션 harness 의 도구로 판단(스냅숏은 워커 도구의 항목만 고정). 그래서 같은 도구면 워커가 받은 것과 똑같다 — 설정을 하나도 고정하지 않은 실행(채택 전 승인)은 스냅숏 뒤 만든 항목.
  4. LADO 실행이 아닌 워커의 리뷰어도 현재본 절을 받는다(엔트리 "아니면 그 프로젝트에 켜진 현재 버전"; 채택 뒤 일반 세션도 관리 항목을 받으므로 같은 기준).
  5. 형식에 안내 한 줄과 파일별 `###` 소제목을 더함 — Claude 의 최상위 `*.md` 가 여럿일 수 있고, 리뷰어에게 이 글이 무엇인지(관례이지 작업 지시가 아님) 알려 주려고.
  6. 경고 로그는 `slog.Default()`(review 패키지에 로거가 없음; 코드베이스에 같은 쓰임 66곳).
  7. 세부 키 `commands` 는 덮는 명령을 ", " 로 이은 하나의 문자열(렌더러 자리표시 하나로 쓰게).
- 테스트 순서: 필수 테스트는 새 API(`ReviewerSection`, `SetLadoSystemSection`, `ConflictAllowsProtected`)를 쓰므로 구현 전에는 컴파일되지 않아 실패부터 보지 못했다. 고정본 확인은 승인 뒤 편집본(`# edited`)과 다른 기대값(`# me`)으로, 오류·빈 절·원천 없음은 각각 AO 글과 바이트 단위 비교로.
- 확인한 명령과 결과: `go build ./... && go vet ./...`(게이트 G6) ok; `go test` `./internal/service/ladosetup/`·`./internal/review/` 전체 ok; 데몬 `-run Lado` 16 테스트 ok(격리 홈); `./internal/service/ladopolicy/`·`./internal/service/ladotask/`(ladosetup.Deps 사용처) ok; golangci-lint v2.13.2(오프라인 빌드본, `GOPROXY=off`) `./internal/review/... ./internal/service/ladosetup/... ./internal/daemon/...` → 0 issues; gofmt 깨끗; `diff-budget` launcher.go 10.
- 게이트: `gate-check.sh --run` 8/8 MET(HEAD `b0d57ed1d`). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 전부(절차 1~4), 필수 테스트 3개 + 기존 충돌 테스트 전체 통과. 리뷰어에게 설정 홈·`-c` 옵션은 주지 않음(결정 s). 렌더러 문구·API 생성물은 손대지 않음.
- 남은 틈:
  - 렌더러가 새 코드를 모름: `lado-errors.ts` 매핑·문구 없음(S16) — 그 전까지 앱은 이 경고를 알 수 없는 코드로 보여 준다.
  - 실제 Claude Code·Codex 리뷰어 바이너리로 `system.md` 가 읽히는지는 보지 않음(단위 테스트만; 런처는 기존대로 `SystemPromptFile` 을 넘김). 큰 지침 파일도 자르지 않고 그대로 붙인다.
  - "복사" 채택처럼 원래 위치에 지침이 남은 경우 Claude 리뷰어는 원본도 직접 읽어 두 번 볼 수 있다(내용이 같으면 무해).
  - 전역 원천이라, 한 프로세스에서 데몬을 여러 번 띄우는 테스트는 마지막 설치가 이긴다(닫힌 store 면 경고 로그 뒤 절 없음 — 리뷰는 계속).
- 다음 단계 메모:
  - **새 충돌 코드(S15·S16)**: `LADO_SETUP_ALLOWS_PROTECTED` = `ladosetup.ConflictAllowsProtected`, 심각도 warning(수락 가능), Detail = 규칙 원문(수락 키), details `item`(또는 `origin`)·`rule`·`commands`(덮는 명령을 ", " 로 이은 문자열, 순서 git push, git update-ref, git merge, git reset, gh pr merge 중 해당하는 것 — 예 `Bash(git:*)` → "git push, git update-ref, git merge, git reset", `Bash(*)` → 다섯 개). S15: API 의 `Code` 는 지금 enum 없는 string 이라 생성물에 바뀔 enum 이 없다 — `LadoSetupConflictResponse.Details` 설명의 키 목록에 `commands` 를 더하면 된다(넣으면 `npm run api`). S16: i18n 키(예 `lado.setup.conflict.allowsProtected`, 8개 locale, `{{rule}}`·`{{commands}}`; 뜻 "이 허용 규칙은 보호 규칙이 막는 명령을 허용한다 — LADO 가 다른 곳에서 막으므로 수락 가능"), `lado-errors.ts` 매핑, `lado-errors.test.ts` 의 코드 목록(373-379 근처).
  - **setter 이름**: `review.SetLadoSystemSection(review.LadoSystemSection)`(패키지 `internal/review`, 데몬에서는 `reviewcore`), 원천 `(*ladosetup.Service).ReviewerSection`, 설치 `wireLadoSetup`(`ladoSetupDeps.reviewers`). 절 머리 `## User instructions (managed by LADO)`(워커 Codex 절과 같은 글).
  - **런처 줄 수**: `review/launcher.go` 태그 대비 +6/−4 = 10(예산 30, 남은 20).
  - S19(docs): HARNESS §10 표의 "Allow rules against protected gates" 가 이제 만들어졌고(§10 "Not built" 문단 고침), 결정 s(리뷰어 지침)도 기록할 것.
