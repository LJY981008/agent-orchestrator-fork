# 2026-10-27 LADO 검증·통합 승인 — S05 기준 브랜치: git 도우미·디스패치 기록·base_branch 항목

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 h, §5) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

PRD 관문 8: 워크트리에서 로컬 기준 브랜치를 옮기는 실수(`git update-ref refs/heads/main HEAD`)를 감지하고 통합을 막는다(사람이 확인해야 통과). 디스패치 때 기준 브랜치의 이름·원격·로컬 커밋을 기록하고, reflog 로 그 뒤의 움직임을 읽는다. 이 단계는 새 패키지 `service/ladoverify` 의 git 도우미로 시작한다 — 이후 검사기들이 같은 도우미를 쓴다.

## 범위

- 만든다: `backend/internal/service/ladoverify/`(새 패키지) — `git.go`(패키지 주석, `Git` 인터페이스, `ExecGit`), `base.go`, 테스트(`git_test.go`, `base_test.go`, 임시 저장소 도우미 `testrepo_test.go`).
- 바꾼다: `service/ladoqueue/dispatch.go`(기준 브랜치 기록) + 테스트.
- 하지 않는다: 검사기(S06), 루프(S11).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `git.go`: `service/ladoqueue/git.go` 의 방식(`aoprocess.CommandContext("git", "-C", dir, …)`, `GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=Never`, `--end-of-options`, 커밋 id 정규식 확인)을 따라
   ```go
   type Commit struct{ SHA, Message string }
   type ReflogEntry struct{ Commit, Selector, Subject string; At time.Time }
   type Git interface {
       MergeBase(ctx context.Context, dir, a, b string) (string, error)
       Commits(ctx context.Context, dir, from, to string) ([]Commit, error)      // rev-list --no-merges from..to, 오래된 것부터, 메시지 포함
       ChangedPaths(ctx context.Context, dir, from, to string) ([]string, error) // diff --name-status -M -z, 이름 바꿈·복사는 양쪽
       Reflog(ctx context.Context, dir, ref string) ([]ReflogEntry, error)       // 최신부터; --date=unix 의 selector 에서 시각
       IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error)
       RevParse(ctx context.Context, dir, ref string) (string, error)            // 없는 ref 는 ErrRefNotFound
       StatusClean(ctx context.Context, dir string) (bool, error)
       Fetch(ctx context.Context, dir, remote, branch string) error            // 30 s
   }
   ```
   기본 10 s 시간 제한. 출력 파싱은 NUL·`%x1f`·`%x1e` 구분자로(메시지 안의 줄바꿈 안전). 오류에는 git 의 stderr 한 줄만(토큰·URL 자격은 잘라낸다 — `ladoqueue` 의 `problem` 처럼).
2. 디스패치 기록(`ladoqueue/dispatch.go`): 4번 검사 뒤 `baseMove` 옆에서 `exec.BaseBranch = base.Branch`, `exec.BaseRemote = base.Remote`, `exec.DispatchLocalBaseSHA` = `RevParse(dir, "refs/heads/"+base.Branch)`(없거나 실패하면 빈 값, 경고 로그만 — 디스패치를 막지 않는다). 복구 경로로 이어 붙은 실행은 빈 값 그대로(검사는 `started_at` 만으로도 된다).
3. `base.go`:
   ```go
   type BaseInput struct {
       Dir, Branch, Remote, HeadSHA string // Dir = 이슈를 가진 저장소(작업 공간이면 그 자식)
       StartedAt time.Time
   }
   func EvaluateBase(ctx context.Context, git Git, in BaseInput) (domain.LadoVerifyItem, error)
   ```
   플랜 §1 h: 로컬 `refs/heads/<branch>` 가 없으면 pass `no_local_branch`; reflog 가 비면 confirm `reflog_off`; `StartedAt` 이후 항목마다 그 커밋이 `refs/remotes/<remote>/<branch>` 에서 닿는지(`IsAncestor`), 아니면 `Moved` 에 담기; 로컬 끝 커밋도 같은 검사; `HeadSHA` 가 원격 기준에서 이미 닿으면 `AlreadyOnBase`. 코드 우선순위: `already_on_base` > `base_moved` > `reflog_off` > pass. 원격 추적 ref(`refs/remotes/<remote>/<branch>`)가 없으면 비교할 기준이 없으므로 움직임 판정을 건너뛰고 pass 한다(원격 없는 저장소는 LADO 에 연결될 수 없어 실제로는 생기지 않는다; `Moved` 는 빈 목록). 루프는 fetch 하지 않는다; 승인 쪽(S13)이 먼저 `Fetch` 한다.
4. 임시 저장소 도우미(테스트 전용): 맨 저장소(origin 역할)를 만들고 clone, `user.name`/`user.email`/`commit.gpgsign=false` 를 저장소마다 설정, `git worktree add` 로 연결 워크트리. 게이트는 `go-tests-isolated` 로 돌린다(개발자 git 설정 차단).
5. 커밋 메시지 예: `feat(lado): detect local base branch moves after dispatch`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S05-base.md` 로 전건 MET 를 확인한다.

## 필수 테스트

임시 저장소로, 격리 홈에서:

- `TestGitHelpersReadCommitsPathsAndReflog` — merge-base, `--no-merges` 커밋(여러 줄 메시지 보존), 이름 바꿈의 양쪽 경로, reflog 항목의 selector·시각·subject, `IsAncestor`, 없는 ref 의 `ErrRefNotFound`, `StatusClean`
- `TestBaseMovedFromAWorktreeNeedsConfirming` — 관문 8: 디스패치 시각 뒤 연결 워크트리에서 커밋하고 `git update-ref refs/heads/main HEAD` → confirm `base_moved`(selector·커밋 포함); 같은 저장소에서 origin 을 fast-forward pull 만 했으면 pass
- `TestBaseItemCases` — 로컬 브랜치 없음 → pass `no_local_branch`; `core.logAllRefUpdates=false` 이고 reflog 없음 → confirm `reflog_off`; head 가 이미 origin 기준에 있음 → confirm `already_on_base`; 디스패치 전 항목은 무시
- `TestDispatchRecordsTheLocalBase`(`service/ladoqueue`) — 가짜 git 으로: 실행에 기준 브랜치·원격·로컬 SHA 가 실리고, 로컬 브랜치가 없으면 빈 값이며 디스패치는 그대로

## 수용 게이트

- [ ] S05-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S05-G1: 필수 테스트 3개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestGitHelpersReadCommitsPathsAndReflog TestBaseMovedFromAWorktreeNeedsConfirming TestBaseItemCases
  EXPECT: GO_TESTS_OK 3/3
- [ ] S05-G2: 디스패치 기록 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladoqueue/ TestDispatchRecordsTheLocalBase
  EXPECT: GO_TESTS_OK 1/1
- [ ] S05-G3: 디스패처 패키지 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/ladoqueue/
  EXPECT: GO_PKG_OK
- [ ] S05-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S05-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `31668c4b3`(브랜치 `lado8/S05`, 트리 깨끗; S01·S02 레인이 병합된 상태, S03·S04 는 병렬 레인이라 이 브랜치에 없음). Ubuntu, go1.27.1(스크래치 툴체인), git 2.43.0, umask 022, TZ=UTC, `CHAIN_RUN=1`. 실제 `~/.ao`·`~/.claude`·`~/.codex` 는 건드리지 않음(임시 저장소 테스트만).
- 앞 단계 메모 중 이 단계 몫: S01 "S05(디스패치 기록): `InsertLadoExecution` 에 세 base 열을 더하거나 따로 UPDATE" — S03 엔트리 절차 3 이 `LinkLadoExecution` 이 세 열을 쓰게 하므로(병렬 레인) 이 단계는 실행 값에만 싣는다. S02 이름 그대로: `LadoBaseBranchDetails{Branch, Remote, Moved, AlreadyOnBase}`, `LadoReflogEntry{Selector, Commit, Subject}`, `LadoCodeBaseMoved|ReflogOff|AlreadyOnBase|NoLocalBranch`, `NewLadoVerifyItem`, `LadoExecution.BaseBranch|BaseRemote|DispatchLocalBaseSHA`. S03·S04 회고는 비어 있음(병렬).
- 출발점(코드로 다시 읽음): `service/ladoverify` 없음. `ladoqueue/git.go` = `Git{RevParse, CountCommits}`·`execGit`·`runGit`(10 s, 프롬프트 끔, `--end-of-options`, `commitID` 정규식). `dispatch.go` 4번 검사: `workerBase` → `(dir, base)`, `namesBase`, `baseMove` → `DispatchBaseSHA`·`BaseCommitsAhead`; `exec` 에 base 필드 없음. `fakeGit` 은 모든 ref 에 같은 SHA. `TestDispatchSpawnsAnApprovedTask` 가 실행 전체를 `reflect.DeepEqual` 로 비교.
- git 실측(스크래치 저장소): `reflog show --date=unix --format=%gd` 는 `refs/heads/main` 을 줘도 `main@{1793090100}`(짧은 이름); `update-ref`(`-m` 없음)의 `%gs` 는 빈 문자열; `clone -c core.logAllRefUpdates=false` 는 reflog 파일을 만들지 않고 `reflog show` 는 rc 0·빈 출력; 없는 ref 의 reflog 는 rc 128; `rev-list --format` 은 레코드마다 `commit <sha>` 머리 줄을 붙임; `merge-base --is-ancestor` 는 아님 1·없는 객체 128; `rev-parse --verify --quiet` 는 없는 객체 1·저장소 아님 128; `diff-tree -M -z --name-status` 의 이름 바꿈은 `R100 NUL <old> NUL <new> NUL`.

## After — 수행 후 실측

- 커밋: `705138f33` feat(lado): detect local base branch moves after dispatch · 이 엔트리 커밋.
- 바꾼 것:
  - 새 `service/ladoverify/git.go`: 패키지 주석, `Commit`, `ReflogEntry`, `Git`(엔트리의 8 메서드), `ExecGit`, `ErrRefNotFound`; 내부 `runGit`(10 s, fetch 30 s, `GIT_TERMINAL_PROMPT=0`·`GCM_INTERACTIVE=Never`·`GIT_OPTIONAL_LOCKS=0`, 프로세스 트리 취소), `gitError{command, code, line}`, `failureLine`(첫 `fatal:`/`error:` 줄, 없으면 글이 있는 첫 줄; URL 자격 잘라냄, 512 바이트), `parseCommits`, `parseNameStatus`, `reflogTime`, `refName`.
  - 새 `base.go`: `BaseInput`, `EvaluateBase`(+ `commitOf`, `reachability`(커밋마다 한 번만 묻는 캐시), `headOnRemote`, `movedSince`).
  - 테스트: `testrepo_test.go`(`testRepo`, `newTestRepo`, `git`, `write`, `commit`, `publish`, `worktree`, `t0`), `git_test.go`(`TestGitHelpersReadCommitsPathsAndReflog`, `TestGitErrorsCarryOneLineWithoutCredentials`), `base_test.go`(`TestBaseMovedFromAWorktreeNeedsConfirming` 2 하위, `TestBaseItemCases` 6 하위, 도우미 `checkBase`).
  - `ladoqueue/dispatch.go`: `localBase`(로컬 `refs/heads/<branch>` 를 기존 `Git.RevParse` 로; 실패하면 "" 와 경고 "LADO could not read the local base branch of a queued task"), `exec` 에 `BaseBranch`·`BaseRemote`·`DispatchLocalBaseSHA`, 4번 검사·`link` 주석. `fakes_test.go` `fakeGit.refs`(ref 별 답, "" 는 커밋 없음). `dispatch_test.go` `TestDispatchSpawnsAnApprovedTask` 기대에 세 필드. `git_test.go` `TestDispatchRecordsTheLocalBase`(로컬 가지 있음/없음; 원격 `upstream`·가지 `develop` 으로 기본값과 구별).
- 엔트리와 다르게 한 것과 이유:
  1. `ChangedPaths` 는 `git diff` 가 아니라 plumbing `git diff-tree -r --no-commit-id --name-status -M -z` — 출력 형식은 같고 개발자의 git diff 설정(색, 외부 도구, `diff.relative`)을 읽지 않는다.
  2. `Reflog` 에 `--no-show-signature`(개발자의 `log.showSignature=true` 가 서명 출력을 끼워 넣지 않게). `Commits` 는 `rev-list` 라 log 설정을 읽지 않는다; 머리 줄 `commit <sha>` 는 파서가 건너뛴다.
  3. `StatusClean` 은 `--untracked-files=normal`(설정이 untracked 를 숨겨도 셈). 모든 호출에 `GIT_OPTIONAL_LOCKS=0`(작업 중인 워크트리의 index 를 새로 쓰지 않게)과 `aoprocess.ConfigureTreeCancellation`(fetch 가 띄우는 원격 접속 도구 같은 자식까지 종료; `ladoconnect` 처럼 `WaitDelay` 1 s).
  4. `Fetch` 는 refspec `+refs/heads/<branch>:refs/remotes/<remote>/<branch>` 를 명시(`--quiet --no-tags --no-recurse-submodules`). 가지·원격 이름에 refspec·리비전이 특별히 읽는 문자(`:`, `~ ^ ? * [ \`, 공백·제어 문자, `..`, `@{`, 앞 `-`, 앞뒤 `/`, `//`)가 있으면 거부 — URL 원격도 거부(원격 이름만).
  5. 커밋 id 정규식은 `Commits`·`ChangedPaths` 만 요구(범위식 `from..to` 와 diff 인자). `MergeBase`·`IsAncestor`·`RevParse`·`Reflog` 는 ref 를 받아야 하므로 빈 값만 거부하고 인자를 `--end-of-options` 뒤에 둔다.
  6. reflog 시각 비교: `StartedAt` 을 초 단위로 내린 값 이상이면 디스패치 뒤로 본다(reflog 시각은 초 단위 — 같은 초의 움직임을 놓치지 않게, 확인 쪽으로 치우침).
  7. 로컬 끝 커밋이 원격에 없고 `Moved` 에 같은 커밋이 없으면 끝을 덧붙인다: 가장 최신 reflog 항목이 그 커밋이면 그 selector·subject, 아니면 selector = 가지 이름(`main`)과 빈 subject. reflog 가 꺼진 저장소에서도 끝이 움직였으면 `base_moved`(`reflog_off` 보다 위) — 하위 테스트 "no reflog and a moved tip".
  8. `already_on_base` 는 로컬 가지가 없어도 판정(원격 추적 ref 만 필요). head 객체가 저장소에 없으면 원격 추적 가지에도 없으므로 false(`IsAncestor` 오류 대신 `RevParse` 로 먼저 확인).
  9. 원격 추적 ref 가 없거나 `Remote` 가 비면 움직임·`already_on_base` 판정을 건너뛰고 pass(로컬 가지도 없으면 `no_local_branch`). `Dir`·`Branch` 가 비면 오류.
  10. `Moved` 의 subject 는 URL 자격을 잘라 담는다(`clone: from <url>`·`pull <url>` 같은 reflog 메시지에 토큰이 있을 수 있음).
  11. 디스패처의 로컬 SHA 는 `ladoqueue.Git.RevParse` 를 그대로 쓴다(`ladoverify.Git` 을 디스패처에 넣지 않음). 없음과 실패를 가르지 않고 경고 한 줄.
  12. 테스트 도우미는 git 의 업로드 명령(push)을 쓰지 않는다: origin 이 `fetch <upstream> +refs/heads/main:refs/heads/main` 으로 받는다(`publish`) — 체인 가드가 bash 의 그 글자를 막고, 원격 쓰기가 없는 쪽이 단순하다.
  13. 더한 테스트: `TestGitErrorsCarryOneLineWithoutCredentials`; `TestBaseItemCases` 의 "no reflog and a moved tip"·"no remote-tracking branch"; 디스패치 전 항목 케이스는 시작 시각을 앞당기면 같은 항목이 `base_moved` 가 됨도 확인.
- 테스트 순서: 필수 테스트를 실패부터 보지 못했다 — 새 패키지라 구현 전에는 컴파일되지 않고, `TestDispatchRecordsTheLocalBase` 도 구현 뒤에 썼다.
- 확인한 명령과 결과: `go test -count=1 ./internal/service/ladoverify/...`(격리 없이, 개발자 설정) ok; 같은 패키지 `-race` ok; `./internal/daemon/...` ok; `go-lint ./internal/service/ladoverify/... ./internal/service/ladoqueue/...` → 처음 `ExecGit` 메서드 주석 누락 8건(revive) → 주석 달고 `GO_LINT_OK`. gofmt 깨끗.
- 게이트: `gate-check.sh --run` 6/6 MET(HEAD `705138f33`). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 범위(절차 1~4, 필수 테스트 4) 전부. 검사기(S06)·루프(S11)·store 의 base 열 쓰기(S03)는 손대지 않음.
- 남은 틈:
  - 이 레인의 store `LinkLadoExecution` 은 세 base 값을 아직 쓰지 않는다(S03 병렬 레인 몫) — 병합 전에는 디스패치가 값을 실어도 DB 에 남지 않는다. 병합 뒤 실제 store 를 쓰는 `TestDispatchRecordsTheBaseMove` 에 base 값 확인을 더할 수 있다.
  - `EvaluateBase` 는 `Branch` 가 빈 실행(8단계 전·복구 경로)을 오류로 돌린다 — S11 이 PR 의 target 가지와 원격(없으면 `origin`)으로 채워야 한다.
  - reflog 항목의 커밋이 저장소에 없으면(gc) `IsAncestor` 오류 → `EvaluateBase` 오류. Windows 실제 git 확인 없음.
- 다음 단계 메모:
  - **`Git` 메서드**(`ladoverify.Git`, 구현 `ExecGit{}`): `MergeBase(ctx, dir, a, b)`(ref 가능), `Commits(ctx, dir, from, to)`(커밋 id 만; `[]Commit{SHA, Message}` 오래된 것부터, `Message` 는 끝 줄바꿈만 뗀 `%B`), `ChangedPaths(ctx, dir, from, to)`(커밋 id 만; 저장소 루트 기준, 이름 바꿈 양쪽, 중복 없음, 없으면 nil), `Reflog(ctx, dir, ref)`(최신부터 `[]ReflogEntry{Commit, Selector, Subject, At}`; reflog 없으면 빈 목록·nil 오류, 없는 ref 는 오류), `IsAncestor(ctx, dir, ancestor, descendant)`, `RevParse(ctx, dir, ref)`(커밋이 없으면 `errors.Is(err, ErrRefNotFound)` — 저장소에 없는 객체의 SHA 도), `StatusClean(ctx, dir)`, `Fetch(ctx, dir, remote, branch)`(원격 이름만, 30 s). 오류 문구는 `git <명령>: <stderr 한 줄>`, 시간 초과는 `git <명령> gave no answer within 10s`.
  - S06: `MergeBase(dir, "refs/remotes/<remote>/<target>", head)` 뒤 `Commits(mergeBase, head)`(subject = `Message` 첫 줄)·`ChangedPaths(mergeBase, head)`; 작업 공간 자식 폴더 접두는 호출자 몫. S07: `StatusClean(worktree)` + `RevParse(worktree, "HEAD")` 를 head SHA 와 비교. S13: `Fetch(dir, remote, branch)` 뒤 `EvaluateBase`.
  - **`EvaluateBase(ctx, git, BaseInput{Dir, Branch, Remote, HeadSHA, StartedAt})`** → `domain.LadoVerifyItem`(kind `base_branch`). `Dir` 은 디스패처 `workerBase` 처럼 고른다(단일 저장소 = 프로젝트 경로, 작업 공간 = 이슈 저장소의 자식) — 실행에 dir 은 저장되지 않는다. 디스패처가 남기는 `DispatchLocalBaseSHA` 는 이 검사가 읽지 않는다(기록용).
  - **임시 저장소 도우미**(package `ladoverify` 테스트): `newTestRepo(t, at, cloneArgs...)` → `*testRepo{origin(bare), upstream(다른 개발자의 clone), dir(개발자의 clone)}`; `r.git(at, dir, args...)`(`GIT_AUTHOR_DATE`·`GIT_COMMITTER_DATE`=at → 커밋·reflog 시각 고정), `r.write(dir, path, content)`, `r.commit(at, dir, msg)`(`add -A`, `--no-verify --allow-empty`, SHA 반환), `r.publish(at)`(origin 이 upstream 의 main 을 fetch), `r.worktree(at, branch)`(`refs/remotes/origin/main` 에서 새 가지의 연결 워크트리). 시각 기준 `t0` = 2026-10-27 09:00 UTC. 게이트는 `go-tests-isolated`.
  - **reflog 파싱의 함정**: `--date=unix` 의 `%gd` 는 `main@{<unix>}`(ref 를 `refs/heads/main` 으로 줘도 짧은 이름) — 같은 초의 항목은 selector 가 같아 고유 키가 아니다. `update-ref`(`-m` 없음)의 `%gs` 는 빈 문자열. reflog 시각은 커미터 날짜라 테스트는 `GIT_COMMITTER_DATE` 로 고정한다. reflog 없는 가지는 rc 0·빈 출력, 없는 ref 는 rc 128(문구는 로캘에 따라 바뀌니 글자로 가르지 말 것). `core.logAllRefUpdates=false` 는 새 reflog 파일만 만들지 않는다(있는 파일에는 계속 기록 — git 문서). `rev-list --format` 은 `commit <sha>` 머리 줄을 붙인다(`--no-commit-header` 는 git 2.33+ 라 쓰지 않음). 체인 가드는 bash 명령 안의 git 업로드 명령 글자와 원격 접속 도구 이름을 막는다 — 임시 저장소는 `fetch` 로 채우고, 그런 글자가 든 문서는 Edit 도구로 쓴다.
