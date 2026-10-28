# 2026-10-29 LADO 계획 워커 — 계획은 계획 워커가 쓰고, 오케스트레이터는 알림만

> 이슈 #27(#33 통합) · LADO 작업 `lado-task-ddda62ff-e2c1-4236-94ac-2dc00983eeec` · 승인된 플랜 revision 4 (sha256 7d42031009ab) · 워커 세션 `agent-orchestrator-fork-7` · 브랜치 `ao/plan-worker-2/plan-workers`

## 의도/목적 — 왜 이 작업을 하나

지금까지는 프로젝트 오케스트레이터가 모든 계획을 썼다. 오케스트레이터 대화에 모든 작업의 이슈·계획·수정 요청이 쌓여 컨텍스트가 커지고, 계획을 쓰는 동안 다른 일을 못 했다. 계획 검수는 각 저장소의 플랜 서브에이전트에 맡겨져 LADO 가 강제하거나 측정할 수 없었다. 소유자는 #27 과 #33 에서 구조를 바꿨다.

- 계획 요청(수정 요청 포함)마다 LADO 가 읽기 전용 계획 워커를 띄운다. 오케스트레이터는 거치지 않는다.
- 설계 → 적대적 검수 → 반영을 기본으로 한다. 작은 계획은 검수를 건너뛴다. 역할 지시문은 LADO 의 전역 규칙이다.
- 오케스트레이터는 한 줄 알림만 받아 사용자에게 전한다.

결정의 근거와 대안은 [ADR 0007](../LJY-PRD/adr/0007-plan-workers.md)에 있다.

## 방법

- 시작: `origin/main` 7863bfe68(#31 병합, `0008_lado_verify.sql` 있음)에서 브랜치를 만들었다. 디스크 여유 62 GB.
- 체인 러너 없이 이 워커가 플랜 순서대로 직접 했다. 커밋은 국면마다 나눴다.
- 세션이 한 번 끊겼다. 대화 요약을 받아 같은 워크트리에서 이어 했다.

## 진행표

| 플랜 단계 | 내용 | 커밋 |
|---|---|---|
| 준비 | 0008 확인, Go 1.27.1·Node 24, npm 의존성, 디스크 | — |
| 설계 확인 | 아래 "설계 확인 결과" | — |
| 저장소 | `0009_lado_plan_workers.sql`(요청의 계획 워커 사실, 검수, 리비전 측정값, 알림 outbox, 실행 설정 열), 쿼리·sqlc·store, CDC 트리거, 테스트 | c995bb1f8 |
| 계획 요청 경로·설계 절차·실패 처리 | `RequestPlan` 이 계획 워커 요청을 만들고, 계획 루프가 설계자 → 검수 결정 → 검수자 → 피드백 → 최종 → 고정 → 한 번 고치기 → 정리를 몬다. 제출 라우트, 측정값, 시간 제한, 실패 코드 | 98312a6ee |
| 읽기 전용·알림 | 게이트의 계획 워커 허가와 auto 상한, Chat 읽기 전용, ACP 허용·거부 목록, `ao hooks` 결정, `ao lado` CLI, 알림 outbox 와 보내는 루프, 오케스트레이터 LADO 절 | aa7cae690 |
| HTTP·화면 | 작업·계획 DTO(계획 워커, 검수, 측정값, change_kinds), 칸반 카드·작업 창 상태와 세션 링크, 계획 화면 검수 결과·측정값, 설정 → 규칙 계획 카드와 버전 되돌리기, 설정 → 실행 계획 워커 카드와 경로 패턴 대화상자, 8개 로캘 | 44fc76e17 |
| 로컬 확인 | lint 지적 수정과 그 과정에서 찾은 쓰기 오류 누락 | 86f544611 |
| 랩 | 랩에서 찾은 결함 세 건과 안내 다듬기(아래 "결정과 이탈") | 02f064c66, af6a53855, 53efadbc3, 5ac873b29 |
| 문서 | TDD §1.1·§1.2·§2·§3.1·§4.2·§4.3·§4.5·§5.1·§5.3·§6.2·§9·§13·§14, PRD(TASK-007, 관문 19, D14), ADR 0007, LADO.md·LADO.ko.md, DESIGN.md 결정 기록, 이 문서 | 6f9413da4 |

## 설계 확인 결과 (플랜 2단계)

1. **띄우는 경로: Chat(ACP) 워커 세션.** client request id 는 `lado:plan/<요청>/design/<시도>`·`lado:plan/<요청>/critique/<시도>` 이고, 해시는 `sha256("lado-plan-worker\n"+id)` 다. 게이트는 디스패처의 임대(lease)가 아니라 계획 요청의 저장된 사실로 허가한다. 그 요청이 지금 그 시도의 설계자를 띄우는 중일 때(설계자 미기록, 시도 수 일치)나, 그 시도의 검수가 running 이고 세션이 아직 없을 때만 통과한다. 실행용 `lado:<entry>/<attempt>` 꼴과는 접두사로 구별한다. 이유: 계획 워커는 큐 항목이 없고, 요청 행이 이미 "지금 띄우는 중"을 말한다.
2. **에이전트·모델:** 프로젝트 설정의 orchestrator agent·agentConfig(없으면 프로젝트 에이전트 설정). 모드는 auto. Claude Code 와 Codex 만 읽기 전용으로 둘 수 있어 다른 에이전트는 `plan_worker_agent_unsupported` 로 실패한다.
3. **읽기 전용: 터미널이 아니라 Chat.** AO 리뷰어 방식(허용·거부 목록 + 프롬프트에 답하는 결정 함수)을 Chat 에 맞춰 세 겹으로 걸었다.
   - 게이트: 계획 워커는 auto 를 넘지 못하고(bypass 없음) 에이전트·인터페이스를 바꾸지 못한다.
   - claude-agent-acp: 세션 `_meta.claudeCode.options` 의 `allowedTools`·`disallowedTools`.
   - AO 훅: Claude Code 의 `pre-tool-use`·`permission-request` 훅이 환경 `AO_LADO_PLAN_WORKER=1` 인 세션의 도구 호출마다 사람 대신 허용·거부를 답한다. 정적으로 읽을 수 없는 셸 명령은 거부한다. 표시는 LADO 의 설정 홈 적용(`applyLadoSetupHome`)과 Chat MCP 목록(`chatMCPServers`)에서 붙여 세션 매니저 훅을 늘리지 않았다.
   - Codex 는 Chat 의 `ReadOnly`(승인 never, 샌드박스 read-only)다. Chat 서비스가 리뷰 대화처럼 계획 워커를 읽기 전용으로 시작한다(게이트의 선택적 메서드 `ReadOnlySession`).
   - 터미널을 버린 이유: 터미널 워커에는 도구마다 LADO 가 답할 자리가 없고, Codex 의 읽기 전용 샌드박스와 ACP 목록은 Chat 에서만 쓸 수 있다.
4. **수정 요청: 새 세션.** 리비전이 고정되면 앞 세션은 정리되어 없다. 새 세션은 첫 요청과 같은 경로에 이전 리비전 파일(`previous-plan.md`, LADO 폴더)과 메모만 더하면 된다. 이전 리비전을 설계자의 산출물 폴더에 쓰지 않으므로 같은 내용의 새 리비전으로 잘못 고정되지 않는다.
5. **세션 정리: `RequestKill`.** 끝난 요청의 설계자·검수자를 멈춘다. 세션은 보드에서 종료된 세션으로 남아 무엇을 했는지 읽을 수 있다. 고정한 계획은 LADO blob 에 복사되어 있다.
6. **알림: 저장된 사실에서 만든다.** 이벤트마다 코드 곳곳에서 알림을 넣지 않고, 계획 루프의 타이머가 요청·리비전·실행 사실을 읽어 키별 outbox 에 한 번씩 넣는다. 다시 읽어도 키가 같아 더해지지 않는다. `daemon.go` 의 시작·대기 줄을 늘리지 않으려고 알림은 계획 루프가 몬다.
7. **오케스트레이터 띄우기 제거.** 계획 요청이 오케스트레이터를 띄우던 경로(`SpawnOrchestrator`)를 지웠다. 업그레이드 전에 저장된 오케스트레이터 요청은 그 요청이 이름 댄 오케스트레이터로 계속 전달되고 고정된다(`TestRequestsMadeBeforePlanWorkersAreStillDelivered`).

## 결정과 이탈

- 플랜은 설계 시간 제한만 60분으로 적었다. 검수 45분(한 번 다시), 반영 30분, 고치기 15분, 전체 2시간 30분은 #33 의 측정값(설계 중앙값 17–23분, 검수 14–24분)으로 정했다.
- 턴이 끝났는데 다음 제출이 없는 설계자는 2분 뒤 한 번 알려 주고, 다시 끝나면 `plan_worker_no_plan` 으로 실패한다. 플랜에 없던 규칙이다. 계획 워커가 제출 명령을 잊고 턴을 끝내는 경우가 있어서다.
- 검수 건너뛰기 판단은 `change_kinds` 선언과 경로 패턴을 합친다. 기본 패턴은 `db_schema`(migrations, SQL, Prisma 스키마), `external_api`(OpenAPI, apispec, proto, routes), `auth`(auth, credential, token)다. `data_deletion` 은 경로로 알기 어려워 기본 패턴이 없고 선언만 본다. 패턴은 Settings → Runs 에서 고친다.
- 계획 검수 절 이름은 플랜대로 `## 계획 검수` 이고, 답은 `반영` 또는 `미반영: <이유>` 다. 검수 결과와 계획 화면은 지적 id 로 짝을 맞춘다.
- 역할 지시문 규칙(`plan.role.designer`, `plan.role.critic`)은 바인딩·보관·범주 변경·충돌을 거부한다(`LADO_RULE_PLAN_ROLE`). 해석(resolution)과 스냅숏에서 빠지므로 바꿔도 승인된 작업이 다시 검토로 가지 않는다.
- 플랜의 "이전 판으로 되돌리기"는 규칙 API 에 없어서, 기록 대화상자의 "이 버전으로 되돌리기…"가 그 버전의 내용을 편집기에 채우고 보통 저장(dry run 포함)으로 새 버전을 만들게 했다. 모든 보호되지 않은 규칙에 같이 쓰인다.
- 변경 경로 중 플랜의 `affected_paths` 밖인 것: `backend/internal/adapters/chatdriver/claudeacp/`(ACP 목록, 업스트림 훅 3줄), `backend/internal/service/chat/`(읽기 전용 시작, 훅 1줄과 LADO 파일), `backend/internal/telemetrymeta/cli.go`(`ao lado` 분류 1줄). 읽기 전용 강제(플랜 5단계)에 필요했다. TDD §1.2 에 줄 수와 함께 적었다.
- 계획 워커의 이슈 스냅숏은 파일(`issue.md`, LADO 폴더)과 스폰의 `IssueContext` 둘 다로 준다. AO 가 이슈를 실시간으로 다시 읽지 않게 하기 위해서다.
- 세션 이름은 사이드바에서 알아보도록 `계획 #<n> <제목>`·`검수 #<n> <제목>` 이다.
- 화면: 계획 워커가 쓰는 중은 working 색, 기다리는 중은 muted, 실패만 needs-you 다. 검수 실패와 P1·major 미반영만 caution 색이다(DESIGN.md 2026-10-29 행).
- 랩에서 찾은 결함: 설계자가 `: ` 가 두 번 든 목록 항목을 따옴표 없이 써서 초안의 YAML 이 깨졌고, 읽지 못한 초안은 영향 경로 0개로 보여 "작은 계획"으로 검수를 건너뛰었다. 읽을 수 없는 초안은 제출 때 `LADO_PLAN_DRAFT_INVALID` 와 문제 목록으로 거절해 설계자가 바로 고치게 하고, 저장된 초안이 읽히지 않으면 건너뛰지 않고 검수하게 했다(`TestAnUnreadableDraftIsReviewed`, 거절 테스트의 두 경우).
- 랩에서 찾은 결함: 계획 화면이 세션 토큰에 캐시 읽기·쓰기를 다시 더해 부풀렸다. AO 의 입력 토큰은 이미 캐시를 포함한다(랩: 입력 3.74M 중 캐시 읽기 3.58M). 입력+출력으로 세고 API 설명에 적었다.
- 랩에서 찾은 결함: 랩에서 검수자 규칙에 "[LAB] 로 시작" 문구를 넣었는데 검수 결과 제목에 나오지 않았다. LADO 프롬프트 소스가 두 곳에서 설치되고, 나중에 설치되는 로컬 설정 쪽 소스에 규칙 읽기가 빠져 설계자·검수자 지시문이 계획 워커에 전혀 가지 않았다. 그 소스도 규칙을 읽게 하고, 데몬 배선 테스트(`TestWireLadoInstallsTheSetupHooks`)가 검수자 세션의 LADO 절에 Plan critic 지시문이 있는지 보게 했다(고치기 전에는 실패).
- 랩에서 다듬은 것: 설계자가 `ls && cat …` 와 `gh … 2>&1 | head` 를 먼저 시도하다 거절당했다(거절 문구를 읽고 Grep·Read 와 리다이렉트 없는 명령으로 바꿔 계속함). 지시문과 거절 문구에 허용·거절되는 형태를 미리 적었다. 계획이 오는 중에는 작업 창의 "계획이 없습니다. 요청하면…" 안내를 숨겼다.
- 테스트 환경: 세션 환경의 `AO_*` 변수(특히 `AO_RUN_FILE`, `AO_DATA_DIR`, `AO_SESSION_ID`)가 Go 테스트에 새어 실사용 데몬을 가리켰다. Go 테스트는 `AO_*` 를 모두 지운 환경에서 돌렸다.

## 랩 확인 (2026-10-29)

- 구성: 이 브랜치로 빌드한 격리 데몬(포트 3917, `AO_DATA_DIR=/tmp/pw2/lab/data`, `AO_RUN_FILE=/tmp/pw2/lab/running.json`, tmux 소켓 `ao-lab-pw2`, `AO_LADO_DEV_PRESENCE=1`)과 dev-web(5199). 계획 워커가 실제 Claude 로그인을 쓰도록 HOME 은 실제 HOME 이고 `CLAUDE_CONFIG_DIR` 은 지웠다. GitHub 토큰은 `gh auth token` 값을 데몬 환경 변수로만 줬다. 실사용 데몬(3001)과 `~/.ao/data` 는 쓰지 않았다(`~/.ao/running.json` 은 계속 3001). 스크린숏은 세션 산출물 폴더 `lab/` 에 있다.
- 랩 프로젝트 `ao-demo`: 포크를 로컬에서 공유 클론한 저장소(7863bfe68, origin 은 포크 https 주소). 오케스트레이터 설정은 claude-code·claude-sonnet-5(비용을 줄이려고), 오케스트레이터 `ao-demo-1` 을 Chat 으로 띄웠다. 이슈 #30·#26 을 실제 가져오기 라우트로 작업으로 만들었다(트래커에 쓰지 않음). 계획 요청은 실제 라우트로 보냈고, 계획을 승인하지는 않았다(실제 워커가 뜨지 않게).
- 상한 1: #30 설계자(`ao-demo-2`, "계획 #30 …", claude-sonnet-5, Chat)가 뜨고 #26 은 `plan_worker_capacity` 로 기다렸다가 #30 이 끝나자 떴다. #30 수정 요청도 #26 뒤에서 같은 이유로 기다렸다.
- 오케스트레이터: 두 요청 접수 알림이 한 메시지로 묶여 갔고, 리비전 알림이 하나씩 갔다. 오케스트레이터는 한두 줄로 전했고 대화에 계획 본문은 없다.
- 읽기 전용: 설계자의 `ls && echo && cat AGENTS.md | head`(목록), `gh issue view … 2>&1 | head`(리다이렉트), `cat > /tmp/lado_plan_draft.md <<'LADO_PLAN_EOF'`(파일 쓰기)가 묻지 않고 거부됐고, 설계자는 거부 문구를 읽고 Read·Grep 과 리다이렉트 없는 명령, `ao lado plan submit` 으로 바꿨다.
- #30(작은 계획): 초안의 YAML 이 깨져(결함 1) 경로 0개로 검수를 건너뛰었다. 최종 리비전 1 은 `invalid_front_matter`·`plan_review_missing` 문제로 고정되어 한 번 고치기 메시지가 갔고, 리비전 2 가 유효하게 고정된 뒤 설계자 세션이 정리됐다. 측정값: 설계 9.4분, 반영 1.3분, 토큰 입력 3.74M(캐시 읽기 3.58M 포함)·출력 52K, $1.91.
- 데몬 재시작: #26 설계 중과 반영 중에 데몬을 새 바이너리로 두 번 바꿔 띄웠다. 설계자 세션과 요청은 그대로 이어졌다.
- #26(검수 경로, 영향 경로 기준을 1 로 낮춤): 초안이 새 초안 검사를 통과하고, 검수자(`ao-demo-4`, "검수 #26 …")가 이슈 없이 초안만 받아 저장소를 읽으며 지적 7건(major 3, minor 4)을 냈다. 설계자는 각 지적을 코드로 확인한 뒤 `## 계획 검수` 절에 C1–C6 반영, C7 미반영과 이유를 적어 최종을 냈고, 리비전 1 이 문제 없이 고정된 뒤 두 세션이 정리됐다. 측정값: 설계 10.3분+반영 4.8분, 검수 10분, 설계자 토큰 4.57M·$2.65, 검수자 3.21M·$1.76, 지적 P1 0·major 3·minor 4, 반영 3·미반영 0, 경로로 찾은 변경 `external_api`.
- 규칙 편집: 검수자 규칙에 "[LAB] 로 시작" 문구를 넣었지만 #26 검수 결과에 나타나지 않아 결함 3 을 찾았다. 고친 바이너리로 설계자 규칙에 "LAB-DESIGNER 줄로 끝내기" 문구를 넣고 #30 수정 요청으로 확인했다(아래).
- #30 수정 요청(고친 바이너리): 새 설계자(`ao-demo-5`)가 메모와 이전 리비전 2 의 경로(LADO 요청 폴더의 `previous-plan.md`)를 받았다. 초안이 경로 2개로 정상적으로 검수를 건너뛰었고("the plan names 2 affected path(s), within the limit of 3 …"), 리비전 3 이 유효하게 고정됐다. 이전 리비전 파일이 따로 고정되지 않았다(리비전은 1·2·3 뿐). 본문 끝에 "LAB-DESIGNER" 줄이 있어, 고친 설계자 규칙이 실제로 설계자에게 갔다.
- 감사 기록: 요청마다 `plan_worker.started`, `draft_submitted`, `critique_skipped` 또는 `critique_started`·`critique_submitted`, `submitted`, `cleaned` 가 순서대로 남았다.
- 정리: 랩 세션 다섯 개 모두 종료, 데몬·dev-web 은 PID 로 멈췄고 남은 프로세스는 없다. 랩 폴더(워크트리 포함)에 토큰 문자열은 0건이다.
- 화면: 칸반 카드(대기·이유, 쓰는 중·단계·세션 링크), 작업 창(계획 워커 줄, 고치는 중, 문제 목록, 계획 검수 측정값과 지적별 답), 설정 → 규칙 계획 카드와 기록의 되돌리기, 설정 → 실행 계획 워커 카드와 경로 패턴 대화상자.

## 확인

- 로컬(HEAD 기준, Go 테스트는 `AO_*`·Claude·Codex 설정 변수를 지우고 임시 HOME 으로): sqlc·api drift 없음, `go.work.sum` 변경 없음, 트리 깨끗함; gofmt·build(`GOOS=windows`·`darwin` 포함)·vet; golangci-lint v2.13.2 0건; race(격리 HOME) `ladotask`·`ladoqueue`·`ladonotify`·`ladosetup`·`ladogate`·`ladoproject`·`ladopolicy`·`session_manager`·`domain`·`httpd`·`daemon`·`cli`, `storage/sqlite/store`(226초)·`storage/sqlite`(1495초, 기본 10분 제한에 걸려 `-timeout 60m` 으로); cloud-client generate·typecheck·test; frontend typecheck·typecheck:e2e·vitest(535 파일, 8834 테스트); 렌더러 smoke 91 통과, 채팅 spec 5개(`chat-agent-switch`, `chat-continue`, `chat-interface-refusal`, `chat-message-overflow`, `chat-queued-attachments`)가 race 와 함께 돌 때 실패했고 단독 재실행에서 6개 모두 통과(LADO 와 무관); gitleaks v7 누출 없음.
- PR CI(#36): 첫 push(53efadbc3)에서 `cloud-build-test` 만 도커 이미지 미러의 429(요청 과다)로 실패했고, 다음 push(1d585b469)에서 통과했다. 나머지는 모두 통과.
- AO 리뷰: (진행하며 채운다.)
