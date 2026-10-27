# 2026-10-27 LADO 검증·통합 승인 — S06 검사기: 커밋 메시지·변경 경로

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 f·g, 소유자 결정 2, §6) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

PRD 관문 9 와 HARNESS §15 항목 5: `--no-verify` 로 hook 을 건너뛴 영어 커밋도 `base..head` 검사기가 잡아 통합을 막는다. 한국어 판정은 휴리스틱이라 애매한 경우는 개발자에게 넘긴다(confirm). 변경 경로는 보호 경로(계획이 이름을 대지 않았으면 실패)와 계획 밖 경로(확인 필요)를 가른다 — 소유자 결정 2.

## 범위

- 만든다: `service/ladoverify/commits.go`, `glob.go`, `paths.go`, 테스트.
- 하지 않는다: git 실행 위치 결정·루프(S11), 수정 요청 메시지(S11).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `commits.go`(플랜 §1 f):
   ```go
   func JudgeSubject(cfg domain.LadoCommitLocaleConfig, subject string) string // "ok", "not_korean", "unclear"
   func JudgeBody(body string) string                                          // "ok", "unclear"
   func EvaluateCommits(cfg *domain.LadoCommitLocaleConfig, commits []Commit, prTitle string) (domain.LadoVerifyItem, error)
   ```
   `cfg == nil` → info `not_required`. 제목: 허용 접두어(`type(scope)!: `) 하나만 벗기고, 이슈 참조(`#12`, `ABC-12`), 백틱 구간, URL, 경로·파일 이름(`/` 나 `.` 이 든 토큰), `_`·내부 대문자·숫자가 든 식별자, 숫자를 지운 뒤 판정: 한글 음절(U+AC00–U+D7A3)이 남으면 ok; 3글자 이상 라틴 단어가 둘 이상이고 한글이 없으면 not_korean; 그 밖 unclear. 본문: 둘째 줄부터, trailer(`Key: value` 형식 줄, `Co-Authored-By:`, `Signed-off-by:`)를 빼고, 한글이 없고 라틴 단어 5개 이상이면 unclear. PR 제목은 제목처럼. 세부: 커밋마다 `{sha, subject, result}`(본문 unclear 는 result `body_unclear`), `prTitle`. 결과: not_korean 하나라도 → fail `commit_messages_failed`; unclear·body_unclear → confirm `commit_messages_unclear`; 아니면 pass. 커밋이 없으면 pass.
2. `glob.go`: `MatchPath(pattern, p string) bool` — `/` 로 나눈 조각마다 `path.Match`, `**` 조각은 0개 이상 조각; 와일드카드(`*?[`) 없는 패턴은 그 경로 자체와 그 아래 모두; `fixedPrefix(pattern) string`(첫 와일드카드 앞까지).
3. `paths.go`(플랜 §1 g):
   ```go
   type PathsInput struct {
       Changed  []string // 저장소 기준, 필요하면 PrefixPaths 로 프로젝트 기준
       Affected []string // 계획의 affected_paths(프로젝트 기준)
       Protected []string
       ProtectedOn bool  // 스냅숏에 protected_paths 검사기가 있을 때만
   }
   func PrefixPaths(prefix string, paths []string) []string
   func EvaluatePaths(in PathsInput) (domain.LadoVerifyItem, error)
   ```
   보호 경로에 걸린 변경은, 그 경로에 맞고 `fixedPrefix` 가 보호 패턴의 것 이상인 계획 항목이 없으면 `Protected` 에 담고 fail `protected_path`. 계획 항목 어느 것에도 맞지 않는 변경은 `OutsidePlan`(앞의 50개, `OutsideCount` = 전체) → confirm `outside_plan`. 둘 다 없으면 pass. 경로 정렬·중복 제거.
4. 커밋 메시지 예: `feat(lado): commit message and changed path validators`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S06-validators.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestKoreanCommitValidator` — HARNESS §15 항목 5: 표로 — `feat: 로그인 추가`·`fix(api): 토큰 만료 처리` ok, `Add login page` not_korean, `fix: typo` unclear, `chore: bump deps` unclear(짧음), `` refactor: `foo_bar` 정리 `` ok, 영어 본문 5단어 이상 body_unclear, trailer 만 있는 본문 ok, 설정에 없는 접두어(`wip:`)는 벗기지 않음; PR 제목이 영어면 fail
- `TestNoVerifyCommitIsJudged` — 관문 9: 임시 저장소에 영어를 거부하는 `commit-msg` hook 을 두고 `git commit --no-verify -m "Add the feature"` 로 만든 커밋이 `Commits` 로 읽혀 `EvaluateCommits` 가 fail
- `TestGlobMatching` — `**`(0개·여러 조각), `*`(한 조각), 와일드카드 없는 폴더 접두, `[ab]`, 깨진 패턴 false
- `TestPathsProtectedNeedThePlanToNameThem` — `.github/workflows/ci.yml` 변경: 계획 `**` → fail, 계획 `.github/workflows/ci.yml` → 통과, 계획 `.github/workflows/*` → 통과, `ProtectedOn=false` → 보호 검사 없음
- `TestPathsOutsidePlanNeedConfirming` — 60개 밖 경로 → 50개 목록과 60, confirm; 작업 공간 접두(`back/`) 적용
- `TestPathsRenameCountsBothSides` — 보호 경로에서 밖으로 옮긴 파일도 보호 위반

## 수용 게이트

- [ ] S06-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S06-G1: 필수 테스트 6개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestKoreanCommitValidator TestNoVerifyCommitIsJudged TestGlobMatching TestPathsProtectedNeedThePlanToNameThem TestPathsOutsidePlanNeedConfirming TestPathsRenameCountsBothSides
  EXPECT: GO_TESTS_OK 6/6
- [ ] S06-G2: 패키지 전체가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg-isolated ./internal/service/ladoverify/
  EXPECT: GO_PKG_OK
- [ ] S06-G3: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S06-G4: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 휴리스틱이 애매하게 본 예, 함수 이름)
