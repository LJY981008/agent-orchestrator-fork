# 2026-10-28 LADO 8단계 — 리뷰 수정, 확인, PR

> 이슈 #15 · LADO 작업 `lado-task-7d4b432f` · 승인된 플랜 revision 2 (sha256 600f4f8d2006) · 워커 세션 `agent-orchestrator-fork-5` · 브랜치 `ao/lado-review-fixes/root` · PR #31

## 의도/목적 — 왜 이 작업을 하나

8단계 체인 결과물(`feat/lado-verification`, 4719d76fd)에 리뷰 4개(백엔드 코어, 저장소·HTTP·배선, 프런트엔드, 문서 대 코드)가 찾은 결함과 문서 불일치를 고친다. 그다음 로컬, PR CI, 랩, 실제 GitHub ruleset 으로 확인하고 포크에 PR 로 올린다. 병합은 소유자 몫이다.

## 방법

- 체인 러너 없이 이 워커가 S20(백엔드 검증 정확성) → S22(백엔드 보안·통합) → S21+S23(프런트엔드) → S24(문서) 순서로 직접 한다(플랜 결정 1).
- 시작: `origin/main`(1112300fc)에서 `git merge --ff-only feat/lado-verification` → 4719d76fd. gitleaks(main..HEAD) 깨끗, push 후 draft PR #31 을 처음에 열었다(결정 3). AO 세션과 LADO 실행 모두 PR #31 을 연결했다.
- 항목마다 결함을 재현하는 테스트를 더한다. 이 문서가 진행표다.

## 진행표

| ID | 내용 | 상태 | 커밋 | 테스트 |
|---|---|---|---|---|
| V1 | 검사 실행을 이름과 명령 내용으로 맞춤, 명령 sha256 을 기록·해시에 넣음, 근거 `{command}` 도 같은 규칙 | 고침 | 09b4ad7d6 | `TestCommandsItem`(옛 내용 두 경우), `TestEvidenceCommandRunsMatchTheCommandText`, `TestTickRunsAChangedCommandAgain` |
| V2 | 첫 실행 뒤에는 마지막 실행이 끝난 뒤의 `git status --porcelain` 해시와 같아야 다음 명령이 돈다(`worktree_changed` 대기) | 고침 | 09b4ad7d6 | `TestWorktreeReady`(실제 저장소), `TestTickWaitsForAWorktreeChangedAfterARun`, `TestTickRunsCommandsOncePerHead`(빌드 결과물을 실행 중에 남기도록 고침) |
| V3·a | head 기록 없는 실행 먼저, 다음은 `computed_at` 오래된 순. 검증 못 한 실행은 5분 쉬고, 쉬는 수만큼 더 읽음. head 를 모르는 열린 PR 은 목록에서 뺌 | 고침 | 09b4ad7d6 | `TestListLadoExecutionsToVerifyTakesTurns`, `TestListLadoExecutionsToVerify`(순서), `TestTickRestsAnExecutionItCannotVerify` |
| V4 | 0008 이 최신 PR 이 merged·closed 인 실행을 그 PR 의 마지막 갱신 시각으로 정산. `integration_gated` 열(기존 실행 0, 디스패처가 넣는 실행 1). 승인 없는 병합 이유·감사는 게이트된 실행만 | 고침 | 09b4ad7d6 | `TestLadoMigrate0008SettlesEarlierExecutions`(7까지 올린 DB → 0008), `TestTickSettlesPerPRStateAndGate`, 도메인 "merged, dispatched before integration approval" |
| V5·h | head 가 로컬에 없으면 PR head ref(`refs/pull/<n>/head`, `refs/merge-requests/<iid>/head`)를 ref 를 쓰지 않고 가져옴. 루프는 head 마다 5분에 한 번, 승인·dry run 은 매번. 못 가져오면 `head_unavailable`. worktree 가 호스트 head 보다 뒤처지면 pull 하라는 메시지 한 번 | 고침 | 09b4ad7d6 | `TestFetchPullHeadBringsAHeadMadeOnTheHost`(임시 원격), `TestTickFetchesAHeadMadeOnTheHost`, `TestIntegrationApprovalFetchesTheHead`, `TestTickAsksTheWorkerToPullAHeadMadeOnTheHost` |
| V6 | 정산을 PR URL·상태 단위로(`settled_pr_url`, `settled_pr_state`) | 고침 | 09b4ad7d6 | `TestSettleLadoExecutionPerPRAndState`, `TestTickSettlesPerPRStateAndGate` |
| V7 | 디스패치 때 로컬 브랜치가 있었는데 없으면 `base_deleted`(confirm), 로컬 tip 은 디스패치 SHA 와 다를 때만 셈 | 고침 | 09b4ad7d6 | `TestBaseUsesTheDispatchCommit`(실제 저장소) |
| V8 | 보호 읽기 오류는 분류(`http_<n>`, `rate_limited`, `transport`, `other`)만 해시에, 문구는 기록의 `protection_error`(해시 밖) | 고침 | 09b4ad7d6 | `TestProtectionItem`("the hash holds the error's class…", "error classes") |
| V9·l | `timed_out` 은 PR 이 없을 때만 Failed. 열린 PR 은 PR 상태를 따름 | 고침 | 09b4ad7d6 | 도메인 "timed out while its PR opened" 외 3건, `TestLadoExecutionsPastDeadline` |
| S1 | Windows 검사 명령을 `cmd /d /s /c "<명령>"`(SysProcAttr.CmdLine)로 실행. 포크 CI `windows-workspace` 에 단계 추가 | 고침 | S22 | `TestRunCommandPassesQuotedArguments`(테스트 바이너리를 프로그램으로 띄워 `"A\|B"`·`"two words"` 가 그대로 도착하는지 봄), 로컬은 `GOOS=windows go vet` |
| S2 데몬 | 제어 문자(Cc: LF, CR, 탭, NUL, C1)와 방향 제어 문자가 든 명령을 400 `LADO_CHECK_COMMANDS_INVALID`(`control_character`·`bidi_control`, `position`, `character`)로 거부 | 고침 | S22 | `TestValidateLadoCheckCommands`(6건), `TestCheckCommandsChangeNeedsPresence`, `TestLadoAPI_CheckCommandsNeedPresence` |
| S3·Q1 | 보호 글롭 판정과 "계획이 이름을 댐" 판정을 소문자로 비교. 계획 밖 판정은 그대로 | 고침 | S22 | `TestPathsProtectedIgnoreCase` |
| Q2 | 경로 하나에 보호 글롭이 여럿 맞으면 전부 이름을 대야 통과(현행 유지) | 고정 | S22 | `TestPathsProtectedNeedThePlanToNameThem/every protected glob that matches needs naming` |
| Q3 | `integration_id` 가 걸린 `lado/integration` 요구만 있으면 부족분 `required_status_checks:lado/integration:any_source` | 고침 | S22 | `TestProtectionItem`("lado/integration from one app only", "…from any source as well") |
| S4·Q4 | 검사 명령 env 에서 `AO_*`, `GCM_*`, `GH_*`, `GITHUB_*`, `GITLAB_*`, `*ASKPASS`, `*_TOKEN`, `*_API_KEY`, `SSH_AUTH_SOCK`, `GIT_SSH(_COMMAND)`, `GIT_CONFIG_*` 를 지움. 빈 `credential.helper`·`core.askPass`(GIT_CONFIG_COUNT), `GIT_TERMINAL_PROMPT=0`, SSH 거부 `GIT_SSH_COMMAND`, 실행마다 빈 `GH_CONFIG_DIR`. 프로젝트 env 는 그 뒤에 더해짐 | 고침 | S22 | `TestRunCommandEnvWithholdsCredentials`(실제 `git credential fill` 이 store·URL helper 의 비밀번호를 못 읽음, SSH ls-remote 거부, 빈 GH 폴더가 실행 뒤 사라짐) |
| S5·S6 | 승인 키를 (세션, PR URL, head, 대상 브랜치)로. 게이트는 지금 대상과 다른 승인만 있으면 409 `LADO_INTEGRATION_TARGET_CHANGED`, 작업은 통합 대기로 | 고침 | S22 | `TestMergeGateNeedsAnApprovalForTheHead/another target branch`, `TestApprovalHoldsForItsPR`, `TestLadoExecutionReadsHoldApprovalsToTheirPRAndTarget`, `TestApproveLadoIntegrationRecordsProofAndAuditTogether` |
| S7 | PR URL 을 가리키는 LADO 기록(검증·승인)이 있으면 PR 행의 세션과 관계없이 그 head 의 승인 요구 | 고침 | S22 | `TestMergeGateNeedsAnApprovalForTheHead/a PR claimed into another session` |
| S8 | `POST /lado/executions/{sessionId}/integration-status`(presence 없음): 승인된 현재 head 의 시도 횟수를 0 으로 되돌리고 다시 게시 | 고침 | S22 | `TestRepostIntegrationStatusStartsOver`, `TestLadoAPI_RepostIntegrationStatus` |
| S10 | gate-6 테스트를 루프 경로로: `ao report --done` 뒤 세션이 끝나고 PR 이 열린 실행 | 고침 | S22 | `TestFailingRequiredCheckKeepsValidatingDespiteReport` |
| U7 데몬 | 설정 응답에 `checkCommandsSha256`, 변경 PUT 에 `checkCommandsBase` 필수. 다르면 409 `LADO_CHECK_COMMANDS_CHANGED`, 저장소도 트랜잭션 안에서 비교 | 고침 | S22 | `TestLadoCheckCommandsSHA256`, `TestCheckCommandsChangeNeedsPresence`, `TestLadoProjectCheckSettings`, `TestLadoAPI_CheckCommandsNeedPresence` |
| 데몬 상태 | 실행 응답에 `state`·`stateReason`(`LadoExecution.State`) | 고침 | S22 | 단계 5 의 렌더러 테스트 |

## 결정과 이탈

- V3: 플랜은 정렬만 말했다. 검증 자체가 실패하는 실행(작업·프로젝트가 사라진 경우)은 기록이 생기지 않아 늘 맨 앞에 서므로, 그런 실행이 50개를 넘으면 다시 기아가 생긴다. 그래서 실패한 실행은 5분 쉬게 하고, 한 번에 읽는 수를 쉬는 수만큼 늘렸다.
- V4: 게이트는 전용 열 `integration_gated` 로 갈랐다(디스패치 base 사실보다 뜻이 분명하다). 백필 시각은 PR 행의 `updated_at` 이다.
- V5: 가져온 객체는 ref 없이 둔다(`--no-write-fetch-head`). 개발자 저장소의 ref 와 FETCH_HEAD 를 건드리지 않는다. 객체는 gc 의 prune 기한(기본 2주)까지 남고, 그 안에 검증과 승인이 끝난다.
- V2: 명령이 이미 바꾼 추적 파일을 워커가 다시 고치면 porcelain 출력이 같아 알아채지 못한다. 드문 경우라 해시를 넓히지 않았다.
- V8: 기록 내용에서 `error` 를 `errorClass` 로 바꿨다. API 의 protection 항목 `details.error` 는 기록 옆 문구로 계속 채운다.
- 형식 이름 `lado-verify/1` 은 그대로 두고 내용만 바꿨다(0008 이 아직 배포되지 않음, 플랜 결정 6).
- S5·S6: 플랜은 승인 키를 (세션, PR URL, head)로 적었다. 대상 브랜치까지 키에 넣었다. 대상만 바뀐 PR 을 다시 승인할 수 있어야 하는데, (세션, PR URL, head)만으로는 두 번째 승인이 `ALREADY_APPROVED` 로 막히기 때문이다.
- S4: 플랜의 테스트 문구 "`git config --get-all credential.helper` 가 비어 있다"는 지킬 수 없다. 목록을 비우는 빈 값도 `--get-all` 에 한 줄로 나온다. 그래서 실제 `git credential fill` 이 저장된 비밀번호를 읽지 못하는지로 확인했다. 더 강한 확인이다.
- S2: "행 번호"는 목록의 행(`index`)으로 이미 있다. 명령 안의 위치는 `position`(1부터 센 글자 위치)과 `character`(U+XXXX)로 더했다. 줄바꿈 자체가 제어 문자라 명령 안의 줄 번호는 늘 1 이 되어 쓸모가 없다.
- S8: 다시 게시는 `posted`·`not_applicable` 승인에는 아무것도 하지 않고 그대로 답한다.
