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

- 시작 HEAD `526dc7493`(브랜치 `lado8/S06`, 트리 깨끗; S01~S05·S08·S14 레인이 병합된 상태). Ubuntu, go1.27.1(스크래치 툴체인), git 2.43.0, `LANG=C.UTF-8`, `CHAIN_RUN=1`. 실제 `~/.ao`·`~/.claude`·`~/.codex` 는 건드리지 않음(임시 저장소 테스트만). `defs-unchanged` DEFS_UNCHANGED, `go-pkg-isolated ./internal/service/ladoverify/` GO_PKG_OK.
- 출발점(코드로 다시 읽음): `service/ladoverify` 에 `git.go`(`Git`·`ExecGit`·`Commit{SHA, Message}`; `Commits` 는 merge 뺀 오래된 것부터 `%B` 전문, `ChangedPaths` 는 이름 바꿈 양쪽·중복 없음), `base.go`, `protection.go`, `testrepo_test.go`. 도메인에 `LadoCommitMessagesDetails{Commits, PRTitle}`·`LadoVerifyCommit{SHA, Subject, Result}`·`LadoVerifyText{Text, Result}`·`LadoPathsDetails{Protected, OutsidePlan, OutsideCount}`·`LadoCommitLocaleConfig{Language, AllowPrefixes}`·`DefaultLadoProtectedPaths()`(9개)·코드 `LadoCodeCommitMessagesFailed|Unclear`·`LadoCodeProtectedPath|OutsidePlan` 이 이미 있다. `validLadoAffectedPath`(계획 검증)는 `./`·끝 `/`·`//` 를 막지 않는다. 규칙 config 는 API 로 바뀌지 않는다(`ladopolicy.Update` 는 천장 모드와 보호 경로만).
- 앞 단계 메모 중 이 단계 몫: S05 — `Commits(mergeBase, head)` 의 subject = `Message` 첫 줄, `ChangedPaths` 는 저장소 루트 기준이고 작업 공간 접두는 호출자 몫, 테스트 도우미 `newTestRepo`·`r.git`·`r.write`·`r.commit`·`t0`, 체인 가드가 막는 글자(임시 저장소는 fetch 로 채움). S04 — 검사기 `commit_message_locale`(config `domain.LadoCommitLocaleConfig`, 버전 config 가 `{}` 면 기본값)·`protected_paths`(`domain.LadoProtectedPathsConfig`); lado-policy/2 면 `commit_messages` 는 `not_required`, `paths` 는 `outside_plan` 만 → 이 단계는 `cfg == nil`·`ProtectedOn=false` 로 받는다. S02 — 결과 문자열(`ok`·`not_korean`·`unclear`·`body_unclear`)은 도메인 상수가 아니다. S08 — 패키지 안 이름 충돌 주의.

## After — 수행 후 실측

- 커밋: `9e8214650` feat(lado): commit message and changed path validators · `6b07ef1e2` fix(lado): unanchored protected globs need a plan entry inside them(자기 검토에서 찾은 구멍, 아래 9) · 이 엔트리 커밋.
- 바꾼 것(새 파일만, 기존 파일 변경 없음):
  - `commits.go`: 결과 상수 `MessageOK`·`MessageNotKorean`·`MessageUnclear`·`MessageBodyUnclear`, `JudgeSubject`, `JudgeBody`, `EvaluateCommits`; 내부 `stripPrefix`, `scanProse`(→ `prose{hangul, words, longWords}`), `trimPunct`, `isCode`, `latinRuns`, `isHangulSyllable`, 정규식 `conventionalPrefix`·`backtickSpan`·`trailerLine`, 상수 `subjectWordsMin`(3)·`subjectLongWordsMin`(2)·`bodyWordsMin`(5).
  - `glob.go`: `MatchPath`, `fixedPrefix`, `patternSegments`, `wildcards`.
  - `paths.go`: `PathsInput{Changed, Affected, Protected, ProtectedOn}`, `PrefixPaths`, `EvaluatePaths`, `protectedPathNamed`, `entryNames`, `outsidePlanMax`(50).
  - 테스트: `commits_test.go`(`TestKoreanCommitValidator` — 제목 19·본문 6 표와 항목 하위 7, `TestNoVerifyCommitIsJudged`, 도우미 `checkCommits`), `glob_test.go`(`TestGlobMatching` — `MatchPath` 35·`fixedPrefix` 9), `paths_test.go`(`TestPathsProtectedNeedThePlanToNameThem` 표 6 + 하위 2(표 6·4), `TestPathsOutsidePlanNeedConfirming`, `TestPathsRenameCountsBothSides`, 도우미 `checkPaths`).
- 엔트리와 다르게 한 것과 이유:
  1. **짧은 제목**: 엔트리 규칙("3글자 이상 라틴 단어 둘 이상, 한글 없음 → not_korean")대로면 필수 테스트의 `chore: bump deps`(bump·deps 모두 4글자)가 not_korean 이 되어 같은 엔트리의 기대 "unclear(짧음)" 과 어긋난다. "라틴 단어 3개 이상, 그중 3글자 이상 2개 이상" 으로 했다(`subjectWordsMin`·`subjectLongWordsMin`). `Add login page`, 관문 9 의 `Add the feature` 는 그대로 not_korean.
  2. **접두어**: 정규식은 플랜 §1 f 모양 `^(type)(\([^)]*\))?!?:\s*` 에 type 글자를 `[^\s()!:]+` 로 두고, 잡힌 type 이 설정 목록에 있을 때만(대소문자 무시 — Conventional Commits 명세가 대소문자를 가리지 않는다) 하나만 벗긴다. `wip:` 처럼 목록에 없는 type 은 그대로 낱말이 된다.
  3. **낱말 다듬기**: 판정 전에 낱말 앞의 여는 괄호·따옴표, 뒤의 닫는 괄호·따옴표·문장 부호(`.,;:!?…`)를 뗀다 — 문장 끝 점(`typo.`)이 파일 이름으로 잡히지 않게. 맨 앞 `.`(`.gitignore`)은 남아 파일 이름이 된다. `\` 도 경로로 본다(Windows 경로). URL 은 `/` 가 들어 있어 경로 규칙으로 빠진다.
  4. **순서 = 경로 → 한글 → 식별자**: 식별자 규칙(숫자·`_`·글자 바로 뒤 대문자 — `JSON`·`fooBar`·`S20`·`#12`·`ABC-12`·숫자 모두 여기)은 한글 음절이 없는 낱말에만 쓴다 — `API를`·`login을` 같은 조사 붙은 낱말은 한글로 남는다. 경로 규칙은 엔트리 순서대로 한글보다 먼저라 `README.md를` 은 지워진다. 이슈 참조를 따로 지우는 정규식은 두지 않았다(숫자 규칙이 덮음).
  5. **본문**: trailer 는 위치와 상관없이 `Key: value` 꼴 줄을 모두 뺀다(엔트리 글 그대로 — `Note: …` 같은 산문 줄도 빠진다). 본문에도 제목과 같은 코드 제거를 쓴다(백틱 정규식이 펜스 블록도 짝으로 지움); 라틴 단어 수는 글자 수와 상관없이 5개(플랜 "at least five Latin words"). 본문 판정은 제목이 ok 일 때만 결과를 바꾼다(`body_unclear`).
  6. **커밋 없음** → pass(엔트리 "커밋이 없으면 pass" 를 앞선 규칙으로 읽음 — 들어갈 커밋이 없으니 제목을 받을 커밋도 없다). 세부의 PR 제목 판정은 그대로 남긴다(`not_korean` 이어도 pass).
  7. **언어**: `cfg.Language` 가 `""`·`"ko"` 가 아니면 오류 — 지금은 config 를 바꿀 길이 없어 생기지 않지만 다른 언어를 한국어로 조용히 판정하지 않게.
  8. **`fixedPrefix`**: 와일드카드 없는 패턴은 그 폴더 전체(`dir/**`)이므로 끝에 `/` 를 붙인다 — 글자 그대로면 계획 `.github` 가 `.github/**` 를 이름 대지 못한다(7 < 8). 빈 조각·`.` 조각은 빼고 잰다(`././././**` 로 길이를 부풀려 보호 경로를 통과하는 구멍); `MatchPath` 도 같은 정규화라 `./docs`·`docs/` 도 폴더 패턴.
  9. **고정부 없는 보호 글롭**(`6b07ef1e2`): 경로에 맞는 계획 항목의 고정부는 늘 빈 고정부 이상이라, 엔트리 규칙 그대로면 계획 `**` 가 모든 `CLAUDE.md`·`AGENTS.md` 를 이름 댄 것이 된다(소유자 결정 2 와 어긋남). 고정부가 빈 글롭은 "글롭이 계획 항목 자체에 맞을 것"(`MatchPath(glob, entry)` 또는 같은 글롭)으로 판정: `docs/CLAUDE.md`·`**/CLAUDE.md`·`docs/**/CLAUDE.md` 는 이름 댐, `**`·`docs/**`·`docs/*.md` 는 아님. 고정부가 있는 글롭(기본값의 나머지 7개)은 엔트리 규칙 그대로.
  10. **보호 글롭 여럿**: 한 경로에 맞는 보호 글롭마다 이름을 대야 한다 — `.claude/CLAUDE.md` 는 `.claude/**` 만으로도, `**/CLAUDE.md` 만으로도 안 되고, 둘 다 있거나 파일 자체를 대야 한다(안전 쪽).
  11. `MatchPath` 는 조각 단위 DP(패턴 조각 × 경로 조각) — `**` 가 많은 패턴에서 되부름이 폭발하지 않게. 깨진 패턴은 먼저 모든 조각을 `path.Match(seg, "")` 로 검사해 false. Go `path.Match` 의 부정 클래스는 `[^ab]`(`[!ab]` 아님).
  12. 세부의 커밋 subject 는 첫 줄의 앞뒤 공백을 뗀 것(CRLF 의 `\r` 도); PR 제목은 받은 그대로.
- 테스트 순서: 테스트를 먼저 쓰고 컴파일 실패(`undefined: PathsInput`)를 본 뒤 구현. 첫 실행의 실패 하나는 테스트 쪽 오류(`[!ab]` → `[^ab]`). 9 는 커밋 뒤 자기 검토에서 찾아 실패 케이스(`docs/**`·`**` 가 `docs/CLAUDE.md` 를 통과)를 표로 더하고 고쳤다.
- 확인한 명령과 결과: gofmt 깨끗; `go vet`·`go test -count=1 ./internal/service/ladoverify/`(격리 없이) ok; `go-race-isolated ./internal/service/ladoverify/` GO_RACE_OK; `go-lint ./internal/service/ladoverify/...` GO_LINT_OK; `go-build` GO_BUILD_OK; `go.work.sum` 변화 없음. 휴리스틱 실측(지운 임시 테스트): 이 저장소 최근 커밋 제목 400개 → `not_korean` 347·`unclear` 53(모두 `docs(lado): record S20` 꼴 — `S20` 이 식별자로 빠져 한 낱말).
- 게이트: `gate-check.sh --run` 5/5 MET(HEAD `9e8214650`, 그리고 `6b07ef1e2`). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~4 전부, 필수 테스트 6개. git 실행 위치·merge-base 고르기·루프·수정 요청(S11)은 손대지 않음. 기존 파일 변경 없음.
- 남은 틈:
  - 보호 경로·계획 경로 비교는 대소문자를 가린다(`path.Match`, 엔트리대로). 대소문자를 가리지 않는 파일 시스템(Windows·macOS)에서 새 `docs/claude.md` 는 Claude Code 가 CLAUDE.md 로 읽을 수 있는데 `**/CLAUDE.md` 에 걸리지 않는다 — Windows-first 라면 보호 글롭만이라도 대소문자 무시로 맞출지 소유자 결정(S19 문서에 적거나 후속).
  - After 9·10 의 판정은 엔트리 규칙보다 엄하다: 계획이 `.claude/**` 만 대고 워커가 `.claude/CLAUDE.md` 도 고치면 `protected_path` — 수정 요청을 받은 워커는 그 변경을 되돌리면 된다. 소유자가 "어느 글롭 하나만 이름 대면 됨" 을 원하면 `protectedPathNamed` 의 루프를 바꾼다.
  - 세부 크기: `Commits`(커밋마다 한 줄, subject 길이 제한 없음)와 `Protected`(개수 제한 없음)는 자르지 않는다 — 엔트리대로. 커밋이 수천 개거나 보호 폴더에 파일 수천 개를 넣은 PR 이면 기록이 커진다(S11 이 필요하면 자름; 해시 형식이 바뀌므로 그 전에).
  - 휴리스틱이 애매하게 보는 예: `fix: 로그인/로그아웃`·`fix: 1.2버전`(한글이 `/`·`.` 낱말 안에 있어 경로로 지워짐 → unclear), `Update README.md`·`test: add tests`·`docs: update`·`Initial commit`·`WIP`(짧음 → unclear), `fix: CI`(식별자뿐 → unclear), 다른 문자(`修复登录问题` → unclear). git 이 만드는 `Merge branch …`(`--no-merges` 로 빠짐)·`Revert "Add …"`(not_korean)도 있다. 본문의 `Note: …` 줄은 trailer 로 빠진다.
- 다음 단계 메모(이름 그대로):
  - **S11(루프)**: `EvaluateCommits(cfg, commits, prTitle)` — `cfg` 는 스냅숏 `Validators` 의 `commit_message_locale` config 를 `domain.LadoCommitLocaleConfig` 로 푼 것(없으면 nil → info `not_required`; lado-policy/2 도 nil). `commits` = `Git.Commits(dir, mergeBase, head)`(merge-base 는 S05 메모대로 `MergeBase(dir, "refs/remotes/<remote>/<target>", head)`, 실패하면 PR `base_sha` 객체), `prTitle` = PR 의 `Title`. 오류는 언어가 `ko` 가 아닐 때뿐.
  - **S11 경로**: `EvaluatePaths(PathsInput{Changed: PrefixPaths(<작업 공간 안 저장소 폴더, / 구분>, Git.ChangedPaths(dir, mergeBase, head)), Affected: plan.AffectedPaths, Protected: <protected_paths config 의 globs>, ProtectedOn: <스냅숏에 protected_paths 검사기가 있음>})`. 단일 저장소면 접두 `""`(`PrefixPaths` 를 건너뛰어도 같음). Windows 의 `filepath.Rel` 결과는 `filepath.ToSlash` 뒤 넘길 것. 오류는 `NewLadoVerifyItem` 뿐(실제로 없음).
  - **S11 수정 요청**: 실패 세부 — 커밋은 `Result == MessageNotKorean` 인 `Commits[]` 와 `PRTitle`, 경로는 `Protected`. confirm 코드는 `commit_messages_unclear`(결과 `unclear`·`body_unclear`), `outside_plan`.
  - **S16·S17(문구)**: 커밋 결과 값 4개 `ok`·`not_korean`·`unclear`·`body_unclear`(PR 제목은 앞의 셋). `outsideCount` > `outsidePlan` 길이(50)면 "외 N개".
  - **S19(문서)**: After 1(짧은 제목 = 라틴 단어 3개 미만), 8(와일드카드 없는 패턴 = 폴더), 9·10(고정부 없는 글롭, 글롭마다 이름), 대소문자 틈을 TDD §8·HARNESS §5·§14 에 적을 것.
