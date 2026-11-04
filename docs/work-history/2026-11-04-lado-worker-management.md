# 2026-11-04 워커 관리: 컨텍스트 상한·사용량 알림·멈춘 워커 재개 (#42)

- 이슈: [#42](https://github.com/LJY981008/agent-orchestrator-fork/issues/42) (#41 을 합침)
- 계획: 리비전 2(sha256 84e68be70c40), 2026-11-04 승인. 결정은 [adr/0009](../LJY-PRD/adr/0009-worker-watch.md), 설계는 [TDD §5.7](../LJY-PRD/TDD.md).
- 실행: LADO 가 파트 6개를 따로 워커로 돌리고(store → launch·usage·wake 동시 → alerts → ui) 통합 브랜치 `lado/96427611/87e3cbcc/integration` 에 병합했다. 통합 워커가 파트 사이 검수, daemon 배선(19단계), 실제 확인(20단계), 문서(21단계), 전체 검사와 PR 을 맡았다. 기준은 origin/main `0cc546cd8`(#45 포함).

## 파트별 결과

| 파트 | 단계 | 커밋 | 내용 |
| --- | --- | --- | --- |
| store | 1–3 | `bf0dfebbc` | 마이그레이션 0012(감시 설정, 세션 감시, 계정 한도, 알림), CDC 트리거, 쿼리와 gen, 저장소, 도메인 타입 |
| launch | 4–8 | `f9d9f3149` `d0383dfbb` `5c771c13b` | ladolaunch(compact 기준, CompactInfo, 감사), 세션 관리자·리뷰어 훅, Codex -c 와 app-server thread config, 고정된 계획 파일과 읽기 폴더, 실행 파일 다시 찾기와 PreflightLaunch |
| usage | 9–11 | `c2f1d640e` | ladousage(작업 합계, 세션별 내역, 걸린 시간 끝 정하기, compact 세기), 사용량 API 2개 |
| wake | 12–15 | `135d2e82b` `7c4d3be1d` `b65da6cfc` `2798006f8` | ladowatch(한도·멈춤 감지, 깨우기), 큐·파트·계획 워커의 한도와 실행 파일 보류 |
| alerts | 16–17 | `07b59af76` | ladoalert(비용·compact 기준 알림, 설정·알림 API 서비스), ladonotify 알림 outbox, 감시 설정·알림 API, 효과 보기 compact 정보 |
| ui | 18 | `03ce73170` | 사용량 탭, 칸반 비용 줄과 알림 배지, 감시 설정 카드, 데스크톱 알림 |

## 파트가 확인한 사실

- **Codex compact 키:** `model_auto_compact_token_limit`. 설치된 codex 가 없어 npm `@openai/codex@0.166.0-linux-x64` 바이너리를 실행하지 않고 `strings` 로 봤다. config.toml 키 목록과 thread Config 키 목록 양쪽에 있고, 저장소의 `codexproto/protocol.gen.go`(codex-cli 0.146.0 app-server 스키마) `Config.ModelAutoCompactTokenLimit` 와 같다. Chat 은 세션 관리자가 실행 환경에 둔 `AO_LADO_CODEX_AUTO_COMPACT_LIMIT` 을 driver 가 thread/start·resume·fork 의 config 로 옮기고 자식 환경에서 지운다. 실제 Codex 실행 확인은 이 컴퓨터에 codex 로그인이 없어 하지 못했다.
- **auto-compact 를 끄는 설정:** Claude Code 2.1.317 바이너리 문구 "autoCompactEnabled setting + DISABLE_AUTO_COMPACT / DISABLE_COMPACT env".
- **한도 문구와 저장 위치:** Chat 의 한도 멈춤은 실패한 턴의 `conversation_turns.error_message` 에 남는다(실제 DB 27건 확인, 10-29 의 모든 실제 사례가 거기 있었고 어시스턴트 메시지는 같은 문구를 되풀이할 뿐). Claude: `You've hit your session|5-hour|weekly|Opus|Sonnet|Fable limit · resets 8:20pm (Asia/Seoul)` 또는 `resets Oct 31, 11am (...)`. Codex 0.166: `usage limit … try again at 8:20 PM` 또는 `Oct 31st, 2026 11:00 AM`(로컬 시간), `try again later.`(60분으로 봄). Codex 실제 사례는 없다. 월 지출 한도 같은 문구는 창이 리셋돼도 풀리지 않으므로 한도로 보지 않는다.
- **Codex compact 기록:** Codex 0.108 rollout 은 compact 마다 최상위 `type: compacted` 한 줄(과 `event_msg` 의 `context_compacted`, 세지 않음)을 쓴다. Claude 는 `system`/`compact_boundary`(사이드체인 제외).
- **upstream:** upstream `a1fe965e7` 은 캐시한 실행 파일 문제를 아직 고치지 않았다.
- **readiness 의 실행 확인:** `EnsureAgentReadiness` 는 경로만 보고 실행하지 않는다. 그래서 `launch_preflight.go` 가 Claude·Codex 에만 `--version`(20초)을 더한다. 로그인 상태로는 막지 않는다.
- **10-30 사고의 모양:** npm 의 `@anthropic-ai/claude-code/bin/claude.exe` 가 postinstall 전 스텁(`echo "Error: claude native binary not installed."`)으로 남는 경우가 있다(2.1.317 패키지). 그런 shim, 끊긴 링크, 대상 없는 Windows `claude.cmd` 는 없는 것으로 보고 다음 후보를 찾는다.
- **같은 캐시를 가진 다른 하네스(이번엔 고치지 않음):** kilocode, kiro, openhands, crush, droid, autohand, goose, devin, vibe, gemini, auggie, muse, deepseekharness, pi, copilot, continueagent, unrealagent, kimchi, opencode, cline, omp, fx, grok, primeagent, cursor, aider, codewhale, mimocode, kimi, agy, amp, commandcode, qwen. LADO 는 Claude Code 와 Codex 로만 실행하므로 영향이 없다.

## 통합 검수

파트들을 서로, 그리고 계획 전체와 맞춰 봤다. 같은 것을 두 번 만든 곳이나 같은 패키지의 중복 도우미는 없었다. 이름과 API(`AccountLimits`, `Preflight`, `CompactCounter`, CDC payload `{kind, alertId, op}`, `lado-alert:<id>`)는 파트 사이에 맞았다. 고친 어긋남은 둘이다.

1. **대기 사유 enum 누락**(wake ↔ API ↔ ui). wake 파트가 큐 항목, 파트, 계획 요청에 쓰는 `account_limited`, `agent_unavailable` 이 큐 항목 `reason`, 작업 `stateReason`, 파트 `waitingReason`, 계획 워커 `waitingReason` 의 OpenAPI enum 에 없었다. 그대로면 생성된 schema.ts 타입이 틀리고 렌더러가 문구 대신 코드 그대로 보인다. DTO 다섯 파일의 enum 을 더하고 `npm run api` 로 다시 만들고, `reasonKeys`·`planWaitingKeys` 와 8개 언어 문구, vitest 를 더했다(`c5fca2777`).
2. **비용 기준 하한**(store ↔ 계획 20단계). store 파트가 비용 기준 하한을 $1 로 두어, 계획이 정한 lab 확인($0.01 기준)을 API 가 400 으로 거절했다. 계획의 lab 확인을 따르도록 하한을 1센트로 낮추고 도메인 테스트와 DTO `minimum` 을 맞췄다(같은 커밋).

그 밖에 확인한 것:

- 문서와 코드: 렌더러는 `compactWarnings` 를 보여 주지 않는다(계획 18단계가 요구하지 않음). LADO.md 와 TDD 는 API 와 감사 기록만 말하도록 썼다. 화면 표시는 후속 일감이다.
- 비용 알림은 비용이 처음 읽힐 때 이미 기준의 2배를 넘었으면 2단계만 넣는다. ladoalert 가 일부러 그렇게 했고 주석에 적었다. 계획의 "기준마다 한 번, 2배 넘으면 한 번 더"와 어긋나지 않는다(같은 기준으로 다시 생기지 않음).
- 알림 id: 이미 있는 알림을 틱마다 다시 넣으려다 `ON CONFLICT DO NOTHING` 으로 끝나는데, 그때도 SQLite 가 rowid 를 하나씩 쓴다(lab 에서 3 다음이 43). 행은 늘지 않고 int64 라 문제는 없다.

### 파트 경로 밖 변경

| 경로 | 누가 | 이유 |
| --- | --- | --- |
| `backend/internal/session_manager/manager.go` | launch | Manager 구조체에 훅 필드 2개 |
| `backend/internal/adapters/agent/claudecode/auth_test.go` | launch | `/fixture/claude` 3곳을 임시 실행 파일로. 없는 절대 경로는 이제 다시 찾으므로 Claude 가 없는 CI 에서 실패했을 것 |
| `frontend/src/main/lado-presence.test.ts` | ui | 테스트 fixture 3줄(새 필드) |
| `backend/internal/daemon/daemon.go` | 통합 | 감시 루프 시작과 종료 대기, APIDeps 3개, ladoWorkDeps 2개. 다른 LADO 배선과 같은 자리 |
| `backend/internal/httpd/controllers/lado_{parts,plan_workers,plans,queue,tasks}_dto.go` | 통합 | 위 검수 1 의 enum |

보호 경로는 바꾸지 않았다.

## daemon 배선 (19단계)

`daemon/lado_watch_wiring.go`(`b416612d4`):

- `wireLadoWatch` 가 ladolaunch, ladousage(`usage.SummaryReader`), ladowatch(Store·Conversations = store, Sessions = store 의 세션 기록, Accounts = agentaccounts, Messenger = 세션 관리자 Send, CompactCounter = ladousage), ladoalert(Usage = ladousage)를 만든다.
- 훅은 `wireLadoSetup` 바로 뒤, 무엇이든 띄우기 전에 넣는다: `SetLadoLaunchValues`, `SetLadoAdditionalDirectories`(`ladotask.PlanDirResolver`), `review.SetLadoReviewerEnv`, Codex `SetLadoCompactOverrides`. 시그니처가 어긋나면 빌드가 실패하게 컴파일 시간 단언을 뒀다.
- ladoqueue 와 ladotask Deps 에 `AccountLimits = ladowatch`, `Preflight = agent.Service`.
- `startLadoWatch` 가 1분마다 `ladowatch.Tick`(맨 앞에서 `CountCompactions`) 다음 `ladoalert.Tick` 을 돈다. 디스패처와 검증 루프 뒤에 시작하고, 종료 때 기다린다.
- APIDeps: `LadoUsage`, `LadoWatch`(ladoalert), `LadoCompact`(ladolaunch).
- 배선 테스트: `TestWireLadoWatchInstallsTheLaunchHooks`(LADO 모드 프로젝트 200000, 일반 프로젝트 없음, 리뷰어, Codex -c), `TestLadoWatchLoopCountsCompactionsWithoutTheUsageTab`(사용량 탭 없이 루프가 대화 기록의 compact 2번을 세고, 다시 돌아도 두 번 세지 않음).

## 실제 확인 (20단계)

### compact 100,000: 실제 바이너리

- Claude Code 2.1.317, 모델 Haiku 5.5(컨텍스트 창 1,000,000), 임시 폴더 `/tmp/lado42-compact` 에 프로젝트 `.claude/settings.json` 의 `{"autoCompactWindow": 900000}` 을 두고 600줄짜리 파일 10개를 차례로 읽게 했다(`claude -p --allowedTools Read`).
- `CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000`: 자동 compact 6번, `compactMetadata.preTokens` 65,913 / 66,793 / 78,685 / 67,698 / 79,274 / 68,169. 기준 10만보다 조금 앞에서 요약했고, 설정 파일의 900000 을 이겼다. 비용 $0.10.
- 대조(환경변수 없음, 같은 설정 파일): compact 0번, 컨텍스트가 364,356 토큰까지 찼다. 비용 $0.46.

### compact 100,000 과 계획 파일: lab 데몬의 Chat(ACP) 세션

- 구성: 이 브랜치로 빌드한 격리 데몬(포트 3917, `AO_DATA_DIR=/tmp/lado42/lab/data`, tmux 소켓 `ao-lab-42`, `AO_LADO_DEV_PRESENCE=1`, `AO_ACP_RUNTIME_DIR` 는 설치된 데스크톱 릴리스의 것). 워커가 실제 Claude 로그인을 쓰도록 HOME 은 실제 HOME, 환경은 `env -i` 로 새로 만들었다. GitHub 토큰은 이슈 읽기용으로 데몬 환경에만 줬다. 실사용 데몬(3001)과 `~/.ao/data` 는 쓰지 않았다.
- 장난감 저장소 `/tmp/lado42/lab/toy`(origin 은 로컬 bare), 프로젝트 `lab42` LADO 모드 켬, 전역 감시 설정 compact 100000·비용 기준 1센트·compact 1번. 작업(#42 를 가리킴), 계획 요청, 리비전, blob 을 DB 에 넣고 dev presence 로 실제 승인 라우트를 썼다. 계획: 계획 파일을 먼저 다시 읽고, `data/part1~10.txt` 를 읽고, NOTES.md 에 한 줄을 더해 origin 에 push(gh·PR 금지). 모델 `claude-haiku-5-5`.
- 효과 보기 응답: `compactWindow: 100000`, `compactApplies: true`, `compactWarnings: []`.
- 디스패처가 Chat 워커 `lab42-1` 을 띄웠다. chat-host, ACP 어댑터(node), `claude` 프로세스 셋의 환경 모두에 `CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000` 이 있었다. `claude` 명령줄에 `--add-dir /tmp/lado42/lab/data/lado/runs/lado-plan-lab42` 이 있었다.
- `runs/lado-plan-lab42/plan.md` 는 0400 이고 sha256 이 승인한 계획(`c2488497…`)과 같았다.
- 워커는 첫 도구 호출로 `Read …/runs/lado-plan-lab42/plan.md` 를 했고, 대화 활동에 권한 요청은 한 건도 없었다(kind 는 command 6, file_change 1, mcp_tool 22, 모두 completed).
- 대화 기록에 자동 compact 가 생겼다(첫 번째 `preTokens` 120,881 → 88,227). 열 개 파일을 한 턴에 몰아 읽어 한 번에 10만을 넘긴 뒤 다음 확인에서 요약했다. 기준은 Chat 경로에도 적용되고, 도구 결과가 한꺼번에 크게 들어오면 기준을 조금 넘긴 뒤 요약한다(알려진 한계에 적음).
- 1분 루프가 compact 2번을 세었고(`lado_session_watch.compactions = 2`), 같은 틱의 ladoalert 가 알림 3개(작업 비용 2단계, 세션 비용 2단계, 세션 compact)를 넣었다. change_log 에 각 알림의 INSERT 이벤트가 한 번씩 있었다. 알림 하나를 seen 으로 두 번 표시하니 UPDATE 이벤트는 한 번이었고 미확인 목록에서 빠졌다.
- 사용량 API 와 timings API 의 세션 역할이 같았다(`lab42-1` worker). 병합 전이라 timings `totalMs` 는 0, 사용량은 접수부터 지금까지(`in_progress`)다.

### 알림 한 번: 데스크톱 앱

- lab 데몬을 멈추고, 같은 lab 데이터로 이 브랜치의 Electron 개발 앱을 띄웠다(`electron-forge start -- --no-sandbox`, `AO_DEV_DAEMON_BINARY` 는 lab 빌드, 프로필 `~/.ao/dev/lado42/electron`, 데몬 3002). 이 컴퓨터의 AppArmor 가 포장 안 된 Electron 의 샌드박스를 막아 `--no-sandbox` 를 썼다. 앱 창은 "Orchestrator.inc (dev)".
- 데스크톱 알림은 D-Bus `org.freedesktop.Notifications.Notify` 호출을 `dbus-monitor` 로 세어 확인했다. 다른 앱의 알림은 세기만 하고 내용은 적지 않았고, 확인 뒤 로그를 지웠다.
- 앱을 띄운 직후: 이미 있던 알림 3개(띄우기 전에 생김)는 알림을 띄우지 않았다.
- `wmctrl` 로 데스크톱 창에 포커스를 옮겨 앱 창 포커스를 뺀 상태에서 두 번째 짧은 실제 실행(#44 를 가리키는 작업, `OTHER.md` 한 줄)을 돌렸다. 비용이 1센트를 넘도록 워커에 파일 네 개를 더 읽으라는 메시지를 한 번 보냈다. 알림 3개(43 작업 비용, 44 세션 비용, 45 compact)가 생겼고, 앱이 알림마다 정확히 한 번 Notify 를 보냈다(제목 "LADO 알림 · LJY981008/agent-orchestrator-fork#44", 본문 예 "작업 비용 추정 US$0.03, 기준 US$0.01의 2배 넘음", "세션 compact 1번(기준 1번) — 파트를 다시 쪼갤지 확인하세요"). 같은 내용이 데스크톱 쪽 중계에서 한 번 더 찍혔는데, 다른 앱 알림도 똑같이 두 번 찍혀 앱이 보낸 것은 하나다.
- 알림 44 를 seen 으로 표시(UPDATE)하고, 앱을 끄고 같은 프로필로 다시 띄운 뒤, 포커스를 뺀 채 2분 30초 기다렸다: 새 Notify 없음. 앱이 보낸 알림은 모두 3개, 알림마다 하나였다.
- 확인하지 못한 것(병합 뒤 소유자 앱에서 볼 항목): 알림을 **클릭**하면 그 작업 창이 열리는지(vitest 로만 확인), 두 번째 창을 연 상태에서 다시 띄우지 않는지(localStorage 공유, vitest 로만 확인), 포장된 앱(샌드박스 켬)에서도 같은지.
- lab 은 모두 멈췄다. 남은 것: `/tmp/lado42`(lab 데이터와 빌드), `/tmp/lado42-compact`, `~/.ao/dev/lado42/electron`(개발 앱 프로필). 비용은 두 lab 실행과 바이너리 확인을 합쳐 API 환산 약 $0.65.

## 리뷰 반영 (head 68d95fe5b 의 변경 요청)

LADO 의 코드 리뷰가 9건을 고치라고 했고 모두 고쳤다.

1. **Codex 하위 에이전트 rollout:** 수집기는 Codex 하위 에이전트 스레드의 rollout 을 부모 binding 아래 `subagent_id` 를 붙여 등록한다. compact 세기의 대화 파일 쿼리에 `us.subagent_id = ''` 를 더해 부모 세션의 compact 로 세지 않는다(store 테스트에 하위 에이전트 rollout 을 섞음).
2. **Windows `claude.cmd`:** npm 패키지 폴더가 shim 옆에 있을 때만 그 안의 `claude.exe`·`cli.js` 로 판정하고, 없으면(pnpm, Yarn) 쓸 수 있는 것으로 본다.
3. **`PreflightLaunch`:** 설치 관찰과 실행 파일 경로만 지우고 모델 목록 캐시는 건드리지 않는다. 워커마다, 대기 중 1분마다 부르므로 모든 프로젝트의 모델 탐색이 쌓이던 문제다(테스트: 세 번 확인해도 목록 읽기 0번). `--version` 은 시간이 넘으면 프로세스 트리를 멈추고 출력 대기도 2초로 끊는다(선택 제안).
4. **계획 파일:** 내용이 어긋난 plan.md 는 임시 파일과 rename 으로 다시 쓰고(Windows 는 읽기 전용을 먼저 풂), 쓰기가 실패하면 경고만 남기고 경로 없이 실행한다. 프롬프트가 계획 전문을 담으므로 파일은 보조 사본이다. 계획의 결정 3("있으면 sha256 만 비교하고 다르면 오류")과 다른 점이다.
5. **`account_limited` 큐 항목:** 리셋 시각이 아니라 1분마다 다시 확인한다(리셋 시각이 더 이르면 그때). 한도가 일찍 풀리거나 기본 계정을 바꾸면 다음 pass 에 뜬다.
6. **반복 멈춤 알림:** 세션 감시 행에 `limit_stops`, `stalls` 를 더하고(0012 는 이 PR 에서 처음 생기는 마이그레이션이라 고쳤다) 한도 알림과 멈춤 알림의 level 로 쓴다. 깨우기를 끈 프로젝트의 두 번째 한도 멈춤, 재개 상한 뒤의 멈춤, 재촉 뒤의 멈춤마다 새 알림이 생긴다.
7. **소유자가 멈춘 워커:** 마지막 턴이 interrupted 나 cancelled 면 재촉도 한도 재개도 보내지 않고 멈춤 알림만 남긴다.
8. **실행 중 작업의 배지:** 세션 카드의 LADO 줄 배지가 `requestLadoTaskOpen` 으로 그 작업을 사용량 탭으로 연다(보드의 LADO 레인이 요청을 받음). 배지 클릭이 세션 카드까지 열지 않게 했다.
9. **보드의 안 본 알림 수:** 새 API `GET /lado/projects/{id}/alerts/unseen-counts` 가 작업별 안 본 알림을 모두 센다. 보드는 목록(최대 100개) 대신 이 수를 쓴다.

문서도 맞췄다: Codex AO 리뷰어는 Chat 일 때만 compact 값을 받는다(터미널 리뷰어는 세션 기록이 없어 `-c` 를 못 받음). 한도 재개는 PR 이 열려 있어도 보내고 재촉만 PR 이 열리면 보내지 않는다(ADR 0009 를 코드에 맞춤).

남긴 선택 제안: compact 오프셋의 파일 identity, `ListLadoPlanWorkerSessions` 의 인덱스 없는 조인과 `ProjectUsage` 의 N+1, 계획 critic 보류 중 요청 시간 초과, shown-ring 의 DB 초기화 대응, 받지 않은 열기 요청, 카드 배지 색과 DESIGN.md. 후속 일감으로 둔다.

## 계획과 다른 점

- 실행 파일 확인을 통과한 직후 업데이트로 파일이 잠깐 사라져 Spawn 이 `ErrAgentBinaryNotFound` 로 실패하면, 지금처럼 시도 하나를 쓴다(결정 4). 이슈는 "시작 재시도 횟수를 쓰지 않는다"였다.
- 비용 기준 하한을 $1 에서 1센트로 낮췄다(통합 검수 2).
- 렌더러는 `compactWarnings` 를 보여 주지 않는다(API 와 감사 기록만).
- 계획 파일이 어긋나면 오류 대신 다시 쓰고, 쓰지 못하면 경로 없이 실행한다(리뷰 반영 4).
- `PreflightLaunch` 는 `InvalidateAgentInstallation` 을 그대로 부르지 않고 모델 목록 캐시를 뺀 무효화를 쓴다(리뷰 반영 3).

## 검사

전체 결과는 PR 설명에 적는다.
