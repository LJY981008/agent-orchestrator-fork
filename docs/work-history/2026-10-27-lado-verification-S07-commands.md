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

- 시작 HEAD: `526dc7493` chore(lado): merge the S14 lane(`feat/lado-verification`, 통합 워크트리에서 이 레인을 돈다). 같은 단계 묶음의 S06·S09·S10 은 각자 워크트리(`lado8-S06`, `lado8-S09`, `lado8-S10`)에서 병렬 — S06·S09·S10 도 `service/ladoverify` 에 파일을 더한다.
- 환경: go1.27.1 linux/amd64, git 2.43.0, umask 0022, TZ=UTC, LANG=C.UTF-8.
- 출발점 실측:
  - `go-pkg-isolated ./internal/service/ladoverify/ ./internal/service/ladoproject/` → `GO_PKG_OK`.
  - `ladoverify` 에는 `git.go`·`base.go`·`protection.go`(+테스트)만, 실행기 없음. `ladoproject.UpdateInput` 은 `LadoMode`·`MaxConcurrent`·`Presence` 뿐, `Store` 에 검사 명령 쓰기 없음.
  - store(S03)에 `SetLadoProjectCheckCommands`·`SetLadoProjectCheckTimeout`·`LadoProjectChecks` 가 있고, `LadoProjectSettings`(행 없으면 `[]`·30)와 `SetLadoMode*` 의 반환이 이미 두 값을 채운다 → "설정 읽기는 둘을 함께 돌려준다"는 서비스가 store 읽기를 그대로 돌려주면 된다. domain(S02)에 `ValidateLadoCheckCommands`·`LadoCheckCommandProblem`·`LadoPresenceCheckCommands`·`LadoCodeWorktreeMissing|NotAtHead`·`LadoCheckRun*` 상태가 있다.
  - 데몬은 `wireLado` 에서 `*sqlite.Store` 를 `ladoproject.Store` 로 넘긴다(새 메서드가 이미 있어 배선 변경 없음). 컨트롤러는 `UpdateInput` 의 두 필드만 채운다(S15 몫).
- 앞 단계 메모 중 이 단계 몫: S05 — 워크트리 확인은 `StatusClean(worktree)` + `RevParse(worktree, "HEAD")`, 임시 저장소 도우미 `newTestRepo`·`r.worktree`·`r.commit`·`t0`, 게이트는 `go-tests-isolated`. S03 — 위 store 시그니처(증명 nil 가능, 재사용 → `domain.ErrLadoPresenceReplayed`; 변경 시각 = 감사 시각). S02 — 400 `LADO_CHECK_COMMANDS_INVALID {index, field, reason}`. S08 — 같은 패키지 레인 병합 때 이름 충돌 주의 → 새 이름은 `Command*`·`command*`·`outputTail`·`*Variable` 로, 커밋 전 세 레인 워크트리를 grep 해 충돌 없음 확인.

## After — 수행 후 실측

- 커밋: `12dbf2785` feat(lado): run the project's check commands in the worktree · 이 엔트리 커밋.
- 바꾼 것:
  - 새 `service/ladoverify/runner.go`: `CommandSpec{Dir, Command, Env, Timeout}`, `CommandResult{Status, ExitCode, Output, Duration, Err}`, `RunCommand`, `CommandEnv`, `WorktreeAtHead`, `MaxCommandOutput`(1 MiB); 내부 `commandEnd`(끝 판정), `outputTail`(고정 크기 링, `newOutputTail`), `tokenVariables`(엔트리의 7개), `envName`, `aoVariable`, `withheldVariable`, `sameVariable`, `commandWaitDelay`(1 s).
  - 새 `runner_test.go`: 필수 테스트 5개(`TestRunCommandKeepsTheOutputTail` 에 링 자체의 표 케이스 포함), 도우미 `skipCommandsOnWindows`, `requireCommandEnd`, `exitedWith`, `processGone`(없거나 좀비면 끝남 — `/proc/<pid>/stat` 상태 `Z`), `requireProcessGone`(10 s 까지 기다림).
  - `ladoproject/service.go`: `Store` 에 두 메서드, `UpdateInput.CheckCommands *[]domain.LadoCheckCommand`·`CheckTimeoutMinutes *int`; `UpdateSettings` = 빈 요청 거부 → `validate`(cap → 시간 → 명령) → 프로젝트 → 현재 설정 → (명령이 바뀌면) 존재 증명 → 모드 → cap → 시간 → 명령 → 쓴 것이 있으면 한 번 다시 읽기. 도우미 `validate`, `checkCommandsInvalid`, `checkCommandsPresence`, `setCheckTimeout`, `apiEvent`, `setCheckCommands`; `setMaxConcurrent` 는 오류만 돌려준다.
  - `ladoproject/service_test.go`: 가짜 store 의 기본값(명령 `[]`, 시간 30)과 두 메서드, `TestCheckCommandsChangeNeedsPresence`, 도우미 `requireInvalidField`·`auditPayload`.
- 엔트리와 다르게 한 것과 이유:
  1. Unix 에서는 셸이 끝난 뒤(정상 종료 포함) 그 프로세스 그룹에 남은 것도 끝낸다(`Wait` 뒤 `cmd.Cancel()` = `ConfigureTreeCancellation` 의 그룹 SIGKILL) — 통과한 명령이 백그라운드에 남긴 자식이 실행 뒤에도 워크트리를 바꾸거나 포트를 쥐지 않게. Windows 는 하지 않는다: `Wait` 뒤에는 셸의 PID 가 다른 프로세스 것일 수 있어 `taskkill /T` 가 남의 트리를 칠 수 있다. `TestRunCommandStopsTheWholeTree` 에 그 케이스를 더했다.
  2. 끝 판정은 Unix 에서 `ProcessState` 가 먼저: 셸이 스스로 끝났으면(신호 아님) ctx 가 그 뒤 출력 배수(WaitDelay 최대 1 s) 동안 끝나도 종료 코드가 답이다 — 제한 직전에 통과한 실행이 `timed_out` 으로 보이지 않게. 신호로 끝났으면 ctx 가 가른다(부모 → `cancelled`, 제한 → `timed_out`, 둘 다 아니면 `failed`·종료 코드 없음·`signal: killed` 오류). Windows 는 신호가 없고 taskkill 된 트리가 1 로 끝나므로 ctx 를 먼저 본다.
  3. `Duration` 은 시간 제한이 시작되는 순간(Start 전)부터 잰다 — 처음 돌렸을 때 1 s 제한의 실행이 999.99 ms 로 재여 테스트가 잡았다.
  4. 입력 방어: 빈 `Dir`(데몬의 cwd 에서 돌게 됨)·빈 `Command`·0 이하 `Timeout` → `error`. `Env` nil → `CommandEnv(os.Environ(), nil)`(데몬 env 그대로는 절대 아님). 시작 전에 ctx 가 이미 끝났으면 `cancelled`.
  5. `Err` 는 `passed` 와 종료 코드가 있는 `failed` 에서 nil(코드가 이유), 나머지(`timed_out`, `cancelled`, `error`, 신호로 끝난 `failed`)에서 값.
  6. `CommandEnv`: `AO_` 접두와 토큰 이름은 대소문자 무시(`MergeEnv` 의 ToUpper 처럼; 소문자 `github_token` 도 뺌), 그 밖의 이름 비교는 Windows 에서만 대소문자 무시. 프로젝트 변수는 이름 순으로 붙이고 빈 이름은 버린다. 프로젝트 env 의 토큰 이름 변수는 남긴다(엔트리대로 `AO_*` 만 뺌 — 프로젝트 env 는 워커도 받는 값이라 새로 드러나는 것이 없고, 막는 것은 데몬 쪽 토큰).
  7. `WorktreeAtHead`: `RevParse` 를 `StatusClean` 보다 먼저(틀린 HEAD 가 먼저 답하고 status 는 큰 저장소에서 느림), head 는 대소문자 무시 비교. 빈 경로·없는 경로·파일 → `worktree_missing`; 없음이 아닌 stat 오류와 git 오류(저장소 아님 등) → `worktree_not_at_head`(wait, 다음 패스에 다시).
  8. `ladoproject`: 명령 변경의 존재 증명을 값 검증 뒤·어떤 쓰기보다 먼저 묻는다 — 증명 없는 {cap, 시간, 명령} 요청은 아무것도 쓰지 않는다(쓰기 순서 모드 → cap → 시간 → 명령은 엔트리대로). 명령 감사 payload 는 `{from, to, proof, dev}`(모드 끄기·보호 경로처럼 증명 nonce·dev 를 더함). 시간 제한 400 의 reason 은 domain 상수 `invalid`, 세부는 정확히 `{index: -1, field: "checkTimeoutMinutes", reason}`. 명령 400 의 메시지는 "Check command <n>: the <field> <이유>." (too_many 는 "A project has at most 10 check commands."), `LADO_SETTINGS_EMPTY` 문구에 네 필드.
  9. `TestWorktreeAtHead` 는 Windows 에서 skip 하지 않는다(git 만 쓴다 — S05 의 git 테스트와 같음). sh 를 쓰는 `TestRunCommand*` 4개만 skip.
- 테스트 순서: 새 파일이라 구현 뒤에 테스트를 썼다. 대신 변이 확인: `ConfigureTreeCancellation` 과 `Wait` 뒤 정리를 빼면 `TestRunCommandStopsTheWholeTree` 가 "process … still runs after the run ended" 로 실패(12 s) — 되돌린 뒤 통과.
- 확인한 명령과 결과: `go test -count=3 -run 'TestRunCommand|TestWorktreeAtHead' ./internal/service/ladoverify/` ok(6.9 s); 같은 것 `-race` ok; `go test -run Lado ./internal/httpd/controllers/ ./internal/daemon/` ok(빈 PUT 은 여전히 `LADO_SETTINGS_EMPTY`); `go-lint ./internal/service/ladoverify/... ./internal/service/ladoproject/...` → `GO_LINT_OK`; `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`; gofmt 깨끗.
- 게이트: 코드 커밋 `12dbf2785` 에서 G0~G4 를 하나씩 돌려 `DEFS_UNCHANGED`, `GO_TESTS_OK 5/5`, `GO_TESTS_OK 1/1`, `GO_PKG_OK`, `GO_BUILD_OK`. 엔트리 커밋 뒤 `gate-check.sh --run` 으로 G0~G5 전건을 그 HEAD 에 다시 기록.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 1~4 전부, 필수 테스트 6개. 언제 돌릴지·실행 칸·로그 blob·실행 기록(S11), HTTP DTO·명세(S15), 화면(S18)은 손대지 않았다. store·domain·데몬 배선 변경 없음.
- 남은 틈:
  - Windows 실측 없음(이 머신은 Ubuntu). 확인할 것: `cmd /c` 에 넘기는 명령의 따옴표 — Go 는 인자를 `EscapeArg` 로 감싸고 cmd.exe 는 자기 규칙으로 읽어 `"` 가 든 명령이 달리 읽힐 수 있다(AO PostCreate 와 같은 방식; 고치려면 Windows 전용 파일에서 `SysProcAttr.CmdLine`); 트리 종료는 Job Object 가 아니라 `taskkill /PID <pid> /T /F` — 부모가 이미 끝난 고아 자식은 `/T` 가 찾지 못한다; 정상 종료 뒤 남은 자식은 정리하지 않는다; ctx 를 먼저 보므로 제한 직전 통과가 WaitDelay(1 s) 사이에 `timed_out` 으로 보일 수 있다. Job Object 측정은 step 9(플랜 §1 Out).
  - Unix 에서도 자식이 `setsid`·`setpgid` 로 자기 그룹을 만들면 트리 종료에서 빠진다(AO 공통 한계).
  - 모드 끄기와 명령 변경을 한 요청에 실제(dev 아닌) 증명 하나로 보내면, 모드 끄기가 nonce 를 쓰고 명령 쓰기는 `LADO_PRESENCE_REPLAYED`(403)로 거부된다 — 모드·cap·시간은 이미 쓰인 뒤. 데스크톱은 명령만 담은 본문에 서명하므로(플랜 §16) 앱 경로에는 없다.
  - 출력 끝 1 MiB 는 UTF-8 글자 중간에서 시작할 수 있다(바이트 그대로 둠).
- 다음 단계 메모(이름 그대로):
  - **S11(루프)**: `RunCommand(ctx, ladoverify.CommandSpec{Dir: 워크트리, Command: c.Command, Env: CommandEnv(Env(), 프로젝트 env), Timeout: time.Duration(분) * time.Minute})` 는 끝날 때까지 막는다(고루틴에서). 결과 → `FinishLadoCheckRun(ctx, id, r.Status, r.ExitCode, <r.Output 의 blob sha256>, <r.Err 가 nil 이 아니면 Err.Error()>, at)`. `Output` 은 최대 `MaxCommandOutput`; ctx 취소(데몬 정지) → `cancelled`, 시작 실패(없는 폴더 등) → `error`. 한 번에 하나는 루프의 칸이 지킨다. `WorktreeAtHead(ctx, git, dir, head)` → `""` / `domain.LadoCodeWorktreeMissing` / `domain.LadoCodeWorktreeNotAtHead`(S09 `CommandsInput.WorktreeCode`). 주의: `.gitignore` 에 없는 파일을 남기는 명령(빌드 산출물 등)이 있으면 첫 명령 뒤 워크트리가 더러워져, 명령마다 준비를 다시 보면 같은 head 의 다음 명령이 `worktree_not_at_head` 에 영영 머문다 — head 의 첫 명령 전에만 보거나, 그 head 의 실행이 이미 있으면 준비 검사를 건너뛸 것.
  - **S15(HTTP)**: 컨트롤러가 `UpdateInput.CheckCommands`·`CheckTimeoutMinutes` 를 채우고 응답에 `settings.CheckCommands`(store 에서 늘 nil 아님)·`CheckTimeoutMinutes` 를 낸다. 오류: 400 `LADO_CHECK_COMMANDS_INVALID` details `{index, field, reason}` — index 는 0부터, field `name`|`command`, reason `required`|`too_long`|`invalid`|`duplicate`; 너무 많으면 `{index: 10, field: "", reason: "too_many"}`; 시간 제한 범위 밖은 `{index: -1, field: "checkTimeoutMinutes", reason: "invalid"}`. 403 `LADO_PRESENCE_REQUIRED`("Change the check commands in the AO desktop app. It asks you to confirm, then sends the change itself."), `LADO_PRESENCE_REPLAYED`, 증명 실패 코드는 `ladopresence` 그대로. presence 동작 `project.check_commands`, subject = 프로젝트 id. 같은 목록·같은 시간이면 증명도 쓰기도 없다.
  - **감사**: `project.check_commands_changed` actor `human`|`human:dev-presence`, payload `{from: [{name, command}], to: [...], proof, dev}`(명령 글 전체); `project.check_timeout_changed` actor `api`, `{from, to}`(분, 숫자).
  - **S16·S18**: 위 reason 값과 field 로 행 아래 문구를 고른다(`checkTimeoutMinutes` 는 시간 선택 아래). 시간 제한은 증명 없이 바로 저장된다.
  - **패키지 안 이름**(레인 병합 때 충돌 주의): `MaxCommandOutput`, `commandWaitDelay`, `tokenVariables`, `CommandSpec`, `CommandResult`, `RunCommand`, `commandEnd`, `CommandEnv`, `envName`, `aoVariable`, `withheldVariable`, `sameVariable`, `WorktreeAtHead`, `outputTail`, `newOutputTail`; 테스트 `skipCommandsOnWindows`, `requireCommandEnd`, `exitedWith`, `processGone`, `requireProcessGone`.
