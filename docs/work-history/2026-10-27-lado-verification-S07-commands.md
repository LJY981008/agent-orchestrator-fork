# 2026-10-27 LADO 검증·통합 승인 — S07 검사 명령: 실행기·프로젝트 설정

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 e, §7) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

TDD §8.2: CI 가 없거나 모자란 저장소를 위해 데몬이 프로젝트의 검사 명령(build/test/lint)을 실행 워크트리에서 head 그대로 돌려 실제 종료 코드와 로그를 남긴다(VER-001). 에이전트가 아니라 데몬이 돌리고, 에이전트의 샌드박스 밖이라 명령 목록을 바꾸려면 데스크톱 확인이 필요하다. 시간 초과면 프로세스 트리를 통째로 끝낸다.

## 범위

- 만든다: `service/ladoverify/runner.go` + 테스트.
- 바꾼다: `service/ladoproject/service.go`(검사 명령·시간 제한 입력, 존재 증명, 감사) + 테스트.
- 하지 않는다: 언제 돌릴지(S11), HTTP(S15), 화면(S18).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `session_manager/workspace_cleanup.go` `runWorkspaceStep`(셸 선택, `WaitDelay`), `backend/internal/process`(`ConfigureTreeCancellation`, `CommandContext`), `agentlaunch.MergeEnv`/`RedactValues`.
2. `runner.go`:
   ```go
   type CommandSpec struct {
       Dir, Command string
       Env     []string
       Timeout time.Duration
   }
   type CommandResult struct {
       Status   domain.LadoCheckRunStatus // passed, failed, timed_out, cancelled, error
       ExitCode *int
       Output   []byte // 합친 stdout·stderr 의 마지막 1 MiB
       Duration time.Duration
       Err      error
   }
   func RunCommand(ctx context.Context, spec CommandSpec) CommandResult
   func CommandEnv(base []string, project map[string]string) []string
   func WorktreeAtHead(ctx context.Context, git Git, dir, head string) string // "" 이면 준비됨, 아니면 코드
   ```
   - 셸: Unix `sh -c`, Windows `cmd /c`. `aoprocess.CommandContext` + `aoprocess.ConfigureTreeCancellation(cmd)` 를 Start 전에. 시간 제한은 `context.WithTimeout`; 초과 → `timed_out`(트리가 끝났는지 테스트로 확인), 부모 ctx 취소 → `cancelled`, 시작 실패 → `error`, 0 → `passed`, 그 밖 → `failed` + 종료 코드.
   - 출력: 고정 크기 링 버퍼(1 MiB)로 마지막 부분만 — 메모리를 키우지 않는다.
   - `CommandEnv`: `base`(보통 `os.Environ()`)에서 `AO_` 로 시작하는 것과 `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`, `GL_TOKEN`, `AO_GITHUB_TOKEN`, `AO_GITLAB_TOKEN`, `AO_GITLAB_HOST_TOKENS` 를 빼고 프로젝트 env 를 덮어쓴다(같은 이름은 프로젝트가 이긴다; 프로젝트 env 의 `AO_*` 도 뺀다).
   - `WorktreeAtHead`: 폴더 없음 → `worktree_missing`; `StatusClean` false 이거나 `RevParse(dir, "HEAD")` ≠ head → `worktree_not_at_head`.
   - 한 번에 하나: 실행 칸(slot)은 루프(S11)가 가진다 — 이 파일에는 `RunCommand` 만.
3. `ladoproject`: `UpdateInput` 에 `CheckCommands *[]domain.LadoCheckCommand`, `CheckTimeoutMinutes *int`. 명령: `domain.ValidateLadoCheckCommands` 실패 → `apierr.Invalid("LADO_CHECK_COMMANDS_INVALID", …, {index, field, reason})`; 저장된 것과 같으면 아무것도 안 쓴다; 다르면 존재 증명 `domain.LadoPresenceCheckCommands`(대상 = 프로젝트 id; `Presence` 콜백 — 모드 끄기와 같은 방식) 뒤 `SetLadoProjectCheckCommands`(감사 `project.check_commands_changed`, actor `human`/`human:dev-presence`, `{from, to}` — 명령 글 전체). 시간 제한: 5-120 밖이면 같은 코드(`field: "checkTimeoutMinutes"`, `index: -1`), 존재 증명 없이 `SetLadoProjectCheckTimeout`(감사 `project.check_timeout_changed`, actor `api`). 한 요청에 모드·cap·명령·시간이 함께 오면: 모드 → cap → 시간 → 명령 순, 앞에서 거부되면 뒤도 안 쓴다. 설정 읽기는 둘을 함께 돌려준다.
4. 커밋 메시지 예: `feat(lado): run the project's check commands in the worktree`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S07-commands.md` 로 전건 MET 를 확인한다.

## 필수 테스트

실행기 테스트는 Windows 에서 skip(`runtime.GOOS == "windows"`), 임시 폴더에서:

- `TestRunCommandPassesFailsAndTimesOut` — `exit 0` → passed, `exit 3` → failed·3, `sleep 30` 을 1 s 제한 → timed_out, 부모 취소 → cancelled, 없는 폴더 → error
- `TestRunCommandStopsTheWholeTree` — `sleep 60 & echo $! > pid; wait` 를 1 s 제한으로 → 끝난 뒤 그 손자 PID 가 살아 있지 않다
- `TestRunCommandEnvDropsTokens` — `env` 출력에 `GITHUB_TOKEN`·`AO_DATA_DIR` 없음, 프로젝트 변수 있음
- `TestRunCommandKeepsTheOutputTail` — 3 MiB 출력 → 마지막 1 MiB 만, 끝부분 그대로
- `TestWorktreeAtHead` — 깨끗하고 head 면 "", 더러우면·다른 HEAD 면 `worktree_not_at_head`, 없으면 `worktree_missing`
- `TestCheckCommandsChangeNeedsPresence`(`service/ladoproject`) — 증명 없으면 거부하고 안 씀; 증명 있으면 저장+감사 `human`; 같은 목록은 아무것도 안 씀; 잘못된 목록 400(index·field); 시간 제한은 증명 없이 저장+감사 `api`; 범위 밖 400

## 수용 게이트

- [ ] S07-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S07-G1: 실행기 테스트 5개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestRunCommandPassesFailsAndTimesOut TestRunCommandStopsTheWholeTree TestRunCommandEnvDropsTokens TestRunCommandKeepsTheOutputTail TestWorktreeAtHead
  EXPECT: GO_TESTS_OK 5/5
- [ ] S07-G2: 프로젝트 설정 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladoproject/ TestCheckCommandsChangeNeedsPresence
  EXPECT: GO_TESTS_OK 1/1
- [ ] S07-G3: 프로젝트 설정 패키지 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/ladoproject/
  EXPECT: GO_PKG_OK
- [ ] S07-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S07-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — Windows 에서 확인할 것(Job Object 대신 taskkill /T), 오류 코드 세부)
