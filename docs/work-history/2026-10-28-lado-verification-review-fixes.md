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
| V1 | 검사 실행을 이름과 명령 내용으로 맞춤, 명령 sha256 을 기록·해시에 넣음, 근거 `{command}` 도 같은 규칙 | 고침 | S20 | `TestCommandsItem`(옛 내용 두 경우), `TestEvidenceCommandRunsMatchTheCommandText`, `TestTickRunsAChangedCommandAgain` |
| V2 | 첫 실행 뒤에는 마지막 실행이 끝난 뒤의 `git status --porcelain` 해시와 같아야 다음 명령이 돈다(`worktree_changed` 대기) | 고침 | S20 | `TestWorktreeReady`(실제 저장소), `TestTickWaitsForAWorktreeChangedAfterARun`, `TestTickRunsCommandsOncePerHead`(빌드 결과물을 실행 중에 남기도록 고침) |
| V3·a | head 기록 없는 실행 먼저, 다음은 `computed_at` 오래된 순. 검증 못 한 실행은 5분 쉬고, 쉬는 수만큼 더 읽음. head 를 모르는 열린 PR 은 목록에서 뺌 | 고침 | S20 | `TestListLadoExecutionsToVerifyTakesTurns`, `TestListLadoExecutionsToVerify`(순서), `TestTickRestsAnExecutionItCannotVerify` |
| V4 | 0008 이 최신 PR 이 merged·closed 인 실행을 그 PR 의 마지막 갱신 시각으로 정산. `integration_gated` 열(기존 실행 0, 디스패처가 넣는 실행 1). 승인 없는 병합 이유·감사는 게이트된 실행만 | 고침 | S20 | `TestLadoMigrate0008SettlesEarlierExecutions`(7까지 올린 DB → 0008), `TestTickSettlesPerPRStateAndGate`, 도메인 "merged, dispatched before integration approval" |
| V5·h | head 가 로컬에 없으면 PR head ref(`refs/pull/<n>/head`, `refs/merge-requests/<iid>/head`)를 ref 를 쓰지 않고 가져옴. 루프는 head 마다 5분에 한 번, 승인·dry run 은 매번. 못 가져오면 `head_unavailable`. worktree 가 호스트 head 보다 뒤처지면 pull 하라는 메시지 한 번 | 고침 | S20 | `TestFetchPullHeadBringsAHeadMadeOnTheHost`(임시 원격), `TestTickFetchesAHeadMadeOnTheHost`, `TestIntegrationApprovalFetchesTheHead`, `TestTickAsksTheWorkerToPullAHeadMadeOnTheHost` |
| V6 | 정산을 PR URL·상태 단위로(`settled_pr_url`, `settled_pr_state`) | 고침 | S20 | `TestSettleLadoExecutionPerPRAndState`, `TestTickSettlesPerPRStateAndGate` |
| V7 | 디스패치 때 로컬 브랜치가 있었는데 없으면 `base_deleted`(confirm), 로컬 tip 은 디스패치 SHA 와 다를 때만 셈 | 고침 | S20 | `TestBaseUsesTheDispatchCommit`(실제 저장소) |
| V8 | 보호 읽기 오류는 분류(`http_<n>`, `rate_limited`, `transport`, `other`)만 해시에, 문구는 기록의 `protection_error`(해시 밖) | 고침 | S20 | `TestProtectionItem`("the hash holds the error's class…", "error classes") |
| V9·l | `timed_out` 은 PR 이 없을 때만 Failed. 열린 PR 은 PR 상태를 따름 | 고침 | S20 | 도메인 "timed out while its PR opened" 외 3건, `TestLadoExecutionsPastDeadline` |

## 결정과 이탈

- V3: 플랜은 정렬만 말했다. 검증 자체가 실패하는 실행(작업·프로젝트가 사라진 경우)은 기록이 생기지 않아 늘 맨 앞에 서므로, 그런 실행이 50개를 넘으면 다시 기아가 생긴다. 그래서 실패한 실행은 5분 쉬게 하고, 한 번에 읽는 수를 쉬는 수만큼 늘렸다.
- V4: 게이트는 전용 열 `integration_gated` 로 갈랐다(디스패치 base 사실보다 뜻이 분명하다). 백필 시각은 PR 행의 `updated_at` 이다.
- V5: 가져온 객체는 ref 없이 둔다(`--no-write-fetch-head`). 개발자 저장소의 ref 와 FETCH_HEAD 를 건드리지 않는다. 객체는 gc 의 prune 기한(기본 2주)까지 남고, 그 안에 검증과 승인이 끝난다.
- V2: 명령이 이미 바꾼 추적 파일을 워커가 다시 고치면 porcelain 출력이 같아 알아채지 못한다. 드문 경우라 해시를 넓히지 않았다.
- V8: 기록 내용에서 `error` 를 `errorClass` 로 바꿨다. API 의 protection 항목 `details.error` 는 기록 옆 문구로 계속 채운다.
- 형식 이름 `lado-verify/1` 은 그대로 두고 내용만 바꿨다(0008 이 아직 배포되지 않음, 플랜 결정 6).
