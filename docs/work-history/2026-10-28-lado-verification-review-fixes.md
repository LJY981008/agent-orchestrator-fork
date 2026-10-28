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
| S1 | Windows 검사 명령을 `cmd /d /s /c "<명령>"`(SysProcAttr.CmdLine)로 실행. 포크 CI `windows-workspace` 에 단계 추가 | 고침 | 31d1ee0f0 | `TestRunCommandPassesQuotedArguments`(테스트 바이너리를 프로그램으로 띄워 `"A\|B"`·`"two words"` 가 그대로 도착하는지 봄), 로컬은 `GOOS=windows go vet` |
| S2 데몬 | 제어 문자(Cc: LF, CR, 탭, NUL, C1)와 방향 제어 문자가 든 명령을 400 `LADO_CHECK_COMMANDS_INVALID`(`control_character`·`bidi_control`, `position`, `character`)로 거부 | 고침 | 31d1ee0f0 | `TestValidateLadoCheckCommands`(6건), `TestCheckCommandsChangeNeedsPresence`, `TestLadoAPI_CheckCommandsNeedPresence` |
| S3·Q1 | 보호 글롭 판정과 "계획이 이름을 댐" 판정을 소문자로 비교. 계획 밖 판정은 그대로 | 고침 | 31d1ee0f0 | `TestPathsProtectedIgnoreCase` |
| Q2 | 경로 하나에 보호 글롭이 여럿 맞으면 전부 이름을 대야 통과(현행 유지) | 고정 | 31d1ee0f0 | `TestPathsProtectedNeedThePlanToNameThem/every protected glob that matches needs naming` |
| Q3 | `integration_id` 가 걸린 `lado/integration` 요구만 있으면 부족분 `required_status_checks:lado/integration:any_source` | 고침 | 31d1ee0f0 | `TestProtectionItem`("lado/integration from one app only", "…from any source as well") |
| S4·Q4 | 검사 명령 env 에서 `AO_*`, `GCM_*`, `GH_*`, `GITHUB_*`, `GITLAB_*`, `*ASKPASS`, `*_TOKEN`, `*_API_KEY`, `SSH_AUTH_SOCK`, `GIT_SSH(_COMMAND)`, `GIT_CONFIG_*` 를 지움. 빈 `credential.helper`·`core.askPass`(GIT_CONFIG_COUNT), `GIT_TERMINAL_PROMPT=0`, SSH 거부 `GIT_SSH_COMMAND`, 실행마다 빈 `GH_CONFIG_DIR`. 프로젝트 env 는 그 뒤에 더해짐 | 고침 | 31d1ee0f0 | `TestRunCommandEnvWithholdsCredentials`(실제 `git credential fill` 이 store·URL helper 의 비밀번호를 못 읽음, SSH ls-remote 거부, 빈 GH 폴더가 실행 뒤 사라짐) |
| S5·S6 | 승인 키를 (세션, PR URL, head, 대상 브랜치)로. 게이트는 지금 대상과 다른 승인만 있으면 409 `LADO_INTEGRATION_TARGET_CHANGED`, 작업은 통합 대기로 | 고침 | 31d1ee0f0 | `TestMergeGateNeedsAnApprovalForTheHead/another target branch`, `TestApprovalHoldsForItsPR`, `TestLadoExecutionReadsHoldApprovalsToTheirPRAndTarget`, `TestApproveLadoIntegrationRecordsProofAndAuditTogether` |
| S7 | PR URL 을 가리키는 LADO 기록(검증·승인)이 있으면 PR 행의 세션과 관계없이 그 head 의 승인 요구 | 고침 | 31d1ee0f0 | `TestMergeGateNeedsAnApprovalForTheHead/a PR claimed into another session` |
| S8 | `POST /lado/executions/{sessionId}/integration-status`(presence 없음): 승인된 현재 head 의 시도 횟수를 0 으로 되돌리고 다시 게시 | 고침 | 31d1ee0f0 | `TestRepostIntegrationStatusStartsOver`, `TestLadoAPI_RepostIntegrationStatus` |
| S10 | gate-6 테스트를 루프 경로로: `ao report --done` 뒤 세션이 끝나고 PR 이 열린 실행 | 고침 | 31d1ee0f0 | `TestFailingRequiredCheckKeepsValidatingDespiteReport` |
| U7 데몬 | 설정 응답에 `checkCommandsSha256`, 변경 PUT 에 `checkCommandsBase` 필수. 다르면 409 `LADO_CHECK_COMMANDS_CHANGED`, 저장소도 트랜잭션 안에서 비교 | 고침 | 31d1ee0f0 | `TestLadoCheckCommandsSHA256`, `TestCheckCommandsChangeNeedsPresence`, `TestLadoProjectCheckSettings`, `TestLadoAPI_CheckCommandsNeedPresence` |
| 데몬 상태 | 실행 응답에 `state`·`stateReason`(`LadoExecution.State`) | 고침 | 31d1ee0f0 | 단계 5 의 렌더러 테스트 |
| S2 main | main 이 제어 문자(Cc, U+2028/2029)·방향 제어 문자가 든 명령을 묻기 전에 거부. 확인 창은 명령을 자르지 않고 공백을 접지 않음. 데몬도 U+2028/2029 를 제어 문자로 거부 | 고침 | 4ca6877cd | `lado-presence.test.ts`("names a new order, and shows a long command whole", "refuses commands the daemon would refuse without asking"), `TestValidateLadoCheckCommands`(line separator) |
| U1 | 보호 부족분을 공용 `ladoProtectionPartText` 로 번역(실행 패널, 카드, 데스크톱 확인 창). main 카탈로그에 `lado.protection.part.*` 5개 | 고침 | 4ca6877cd | `LadoVerificationSection.test.tsx`(실제 값 fixture), `lado-presence.test.ts`("words protection and the base branch as they stand") |
| U2 | Output 은 끝난 실행에만. 로그 query key 에 상태, `lado_execution` 이벤트에도 로그 무효화 | 고침 | 4ca6877cd | "offers a command's output only once it ended, and reads it again for each state" |
| U3 | 보호 경로 저장이 dry run → 영향받는 승인된 작업 목록 → 데스크톱 확인 | 고침 | 4ca6877cd | `LadoProtectedPathsDialog.test.tsx`("lists the approved tasks…"), `RulesSection.test.tsx` |
| U4 | `nextUnprotected` 는 `unprotected`·`unprotected_gitlab` 에만 | 고침 | 4ca6877cd | "words protection and the base branch as they stand" |
| U5 | 항목마다 sr-only 결과 단어 | 고침 | 4ca6877cd | "lists the eight items…"(`Check commands, failed`) |
| U6 | 병합 성공 뒤 PR 이 merged 로 보일 때까지 버튼 끔, AO PR 쿼리 무효화, 거절 뒤에도 다시 읽기 | 고침 | 4ca6877cd | "keeps Merge off once the merge went through…" |
| U7 main | main 이 읽은 설정의 `checkCommandsSha256` 을 서명 본문 `checkCommandsBase` 로. 브라우저 경로는 카드가 보인 목록의 해시 | 고침 | 4ca6877cd | `lado-presence.test.ts`(서명 본문), `LadoProjectChecksCard.test.tsx` |
| U8 | 검증 중을 `--color-status-validating` 으로 | 고침 | 4ca6877cd | `lado-task-status.test.ts`, `LadoTaskDialog.test.tsx`, `LadoVerificationSection.test.tsx` |
| U9 | DESIGN.md S17 행(Run again 위치, 나올 수 없는 예시)을 코드와 맞춤, 리뷰 수정 행 추가 | 고침 | 4ca6877cd | — |
| U10 | ko "헤드: {{head}}" → "head: {{head}}" | 고침 | 4ca6877cd | 카탈로그 생성 |
| 새 코드 | `worktree_changed`, `head_unavailable`, `base_deleted`, `LADO_INTEGRATION_TARGET_CHANGED`, `LADO_INTEGRATION_NOT_APPROVED`, `LADO_CHECK_COMMANDS_CHANGED`, `control_character`·`bidi_control`, 부족분 `any_source` 와 안내 | 고침 | 4ca6877cd | `lado-errors.test.ts`, "names a deleted local base branch and a check taken from one app only", `LadoProjectProtectionCard.test.tsx` |
| 데몬 상태 | 실행 패널이 `execution.state`/`stateReason` 을 그대로 씀 | 고침 | 4ca6877cd | "shows the state and reason the daemon sends, not one of its own" |
| 다시 게시 | 상태 게시 실패 시 "상태 다시 게시"(실행 패널, 작업 창) | 고침 | 4ca6877cd | "posts a status that could not be posted again" |
| 문서 23건 | 문서 대 코드 리뷰의 불일치 23건(TDD, HARNESS, PRD, ADR-0003)과 리뷰가 찾은 미기재 동작: 기준 브랜치 항목은 코드 하나(`already_on_base` → `base_deleted` → `base_moved` → `reflog_off`), 끝난 세션의 worktree 가 사라지면 `worktree_missing` 이 계속되고 수정 요청 없음, 거절된 승인도 다시 계산한 기록은 저장, 고전 브랜치 보호는 `unprotected` | 고침 | f022834da | 옛 문구 grep 0건 |
| ADR·PRD | ADR-0002 에 main 의 이른 거절과 통합 확인 문구, ADR-0003 에 토큰 사슬·확인 코드 집합·게이트 S5·S7, PRD PRJ-003·EXEC-004·MERGE-001·EVD-002·VER-002·VER-003 | 고침 | f022834da | — |
| 플랜·TDD 기록 | 8단계 플랜 §20 변경 기록(a, e·§16, h, l, Q1–Q4, 이 플랜의 결정)과 §0 용어집 head, TDD §15 항목 8 "Review fixes", TDD §8.2·HARNESS T2 한계, TDD §1.2 `go.yml` 행 | 고침 | f022834da | — |
| 랩: 레인 | PR 이 열린(검증 중·통합 대기·통합 중) 작업이 계획 레인에도 남아 세션 카드와 겹침 → 워커가 시작한 작업은 이 상태들에서도 레인에서 뺌 | 고침 | 21c7d42a6 | `LadoLanes.test.tsx`("a task whose worker started leaves the lanes…") |
| 랩: 명령 충돌 | `LADO_CHECK_COMMANDS_CHANGED` 뒤에도 옛 기준으로 같은 거절이 되풀이되고 "지금 목록"을 볼 수 없음 → 설정을 다시 읽고 카드가 지금 목록을 보임 | 고침 | 21c7d42a6 | `LadoProjectChecksCard.test.tsx`("shows the current list when the commands changed…") |
| 랩: 병합 뒤 오류 | 병합되거나 닫힌 PR 에도 상태 게시 오류가 남음 → 열린 PR 에만 | 고침 | 21c7d42a6 | "posts a status that could not be posted again"(병합 PR 사례) |
| 랩: any source 줄 | 보호 카드에서 "빠진 것: …"과 Q3 안내가 마침표 없이 한 줄로 이어짐 → 안내를 따로 한 줄로 | 고침 | 4c6d61f88 | `LadoProjectProtectionCard.test.tsx`("says to take lado/integration from any source") |

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

- 렌더러에는 보호 경로를 실제 경로에 맞춰 보는 미리보기가 없다(글롭 문법만 검사). 그래서 Q1 의 "렌더러 미리보기도 같은 규칙"은 할 일이 없었다.
- 단계 3·4 를 push 한 5289e3b5c 에서는 프런트 typecheck 가 테스트 fixture 15건(새 필수 필드)으로 실패했다. 백엔드 CI 를 먼저 보려고 그대로 올렸고, 단계 5 에서 고쳤다.

## 랩 확인 (2026-10-28)

- 구성: 이 브랜치를 빌드한 격리 데몬(3917, 임시 HOME·AO_DATA_DIR, `AO_LADO_DEV_PRESENCE=1`, tmux 소켓 따로)과 dev-web(5199, `AO_DEV_API_TARGET=http://127.0.0.1:9`). 실사용 데몬(3001)과 실제 `~/.ao` 는 건드리지 않았다. 스크린숏은 세션 산출물 폴더 `lab/` 에 있다.
- 데이터: 오케스트레이터·트래커·디스패처·PR 관찰자가 만들 행(작업 2개, 전달된 플랜 요청, 플랜 판과 blob, 실행, 세션, PR)은 python `sqlite3` 로 넣고, 플랜 승인·검사 명령·보호 경로·상태 다시 게시는 진짜 화면으로 몰았다. 실행 세션 id 는 미리보기 보드의 `demo-working`·`demo-needs-input` 을 써서 세션 인스펙터에 실행 패널이 뜨게 했다. 에이전트는 설치되지 않은 `cursor` 로 두어 디스패처가 워커를 띄우지 않게 했다.
- 가짜 토큰 단계(합성 저장소): 루프가 실제로 기록을 계산(통합 대기, `protection_unknown` 의 `http_401`). S2: 방향 제어 문자(U+202E)와 줄 구분 문자(U+2028)가 든 명령을 데몬이 위치·문자와 함께 거절하고, 입력란은 뒤집힌 글자와 한 줄처럼 보이는 두 명령을 그대로 보여 위험을 확인했다. U7: 다른 곳에서 목록을 바꾼 뒤 저장하면 충돌 문구와 지금 목록. U2: 실행 중에는 Output 없음, 끝나면 Output 에 로그. U3: 보호 경로에 글롭을 더하면 다시 검토될 승인된 작업(#9002) 목록. S8: 실패한 상태(시도 12회)에 "상태 다시 게시" → 시도가 0 으로 돌아가 한 번 다시 시도(가짜 토큰이라 다시 실패). V9: 시간 초과를 넣어도 열린 PR 의 상태(통합 중)와 "최대 실행 시간에 도달해 중지됨". V7: 로컬 `main` 을 지우자 `base_deleted`("실행 시작 때 있던 로컬 브랜치가 없어짐"). 병합 표시 뒤 완료(병합 색). 영어·다크, 한국어·라이트.
- 실제 GitHub 단계(소유자 사전 승인, PRD 게이트 7 의 수동 절반): 포크에 임시 브랜치 `lado-gate7-base`(main 에서)와 `lado-gate7-head`(`[skip ci]` 커밋 하나), `lado-gate7-base` 에만 거는 임시 규칙셋 `lado-gate7-temp`(PR 필수, `lado/integration` 필수(모든 출처), 강제 푸시·삭제 차단), PR #35. 랩 데몬에는 실제 토큰을 환경 변수로만 줬다(파일에 쓰지 않음, 끝난 뒤 로그·데이터에서 토큰 문자열 0건).
  - 규칙셋 전: LADO 가 `unprotected`, 빠진 것 넷을 번역해 보임(U1).
  - 규칙셋 뒤: LADO `protected`, 실행의 보호 항목 pass. GitHub 는 PR #35 를 `BLOCKED` 로 막음.
  - 랩에서 통합 승인 → head `a25b0d02f` 에 `lado/integration` `success`("Approved in LADO") 게시, 시도 1회 → GitHub `CLEAN`. 병합하지 않았다.
  - 검사를 GitHub Actions 앱(`integration_id` 15368)에서만 받도록 바꾸자 LADO 가 `unprotected`·`required_status_checks:lado/integration:any_source` 와 안내를 보였고, GitHub 는 LADO 의 상태가 있어도 PR 을 다시 `BLOCKED` 로 막았다. Q3 판단(토큰으로는 그 규칙을 채울 수 없음)이 실제로 맞다.
  - 정리: 규칙셋 삭제, PR #35 닫음(병합 안 됨), 두 브랜치 삭제. `GET /repos/LJY981008/agent-orchestrator-fork/rulesets` 가 빈 목록이고 `main` 의 규칙도 0개다. 랩 데몬은 바로 멈췄다.
- 도구: 실제 GitHub 단계 중간에 AO 브라우저가 탭 대상 불일치(`BROWSER_TARGET_MISMATCH`)로 멈춰, 마지막 스크린숏 세 장은 별도 Playwright 브라우저로 같은 dev-web 을 찍었다.

## 다음 단계 메모 (진행 중, 끝나면 지운다)

- 랩과 실제 GitHub 확인은 끝났다. LADO.md·LADO.ko.md 의 8행과 알려진 한계(대소문자, 실제 규칙셋, 검사 명령의 T2)를 고쳤다.
- 남은 것: 로컬 test_commands 전체(sqlite race 는 다른 작업이 없을 때 다시), PR CI, AO 리뷰(최대 3회), PR 본문, ready.
