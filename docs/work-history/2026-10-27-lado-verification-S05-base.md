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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — `Git` 메서드 목록, 임시 저장소 도우미 이름, reflog 파싱의 함정)
