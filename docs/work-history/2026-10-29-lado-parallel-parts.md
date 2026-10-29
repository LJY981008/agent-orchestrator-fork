# 2026-10-29 LADO 병렬 파트 — 작업 하나를 여러 워커가 나눠 동시에 한다

> 이슈 #32 · LADO 작업 `lado-task-c14c6be2-d106-48d5-8b91-73cbf8ee5daf` · 승인된 플랜 revision 3 (sha256 0033738b18ad) · 워커 세션 `agent-orchestrator-fork-8` · 세션 브랜치 `ao/parallel-parts/root` · PR #38

## 의도/목적 — 왜 이 작업을 하나

지금까지는 승인된 계획 하나를 워커 하나가 처음부터 끝까지 차례로 했다. #15(8단계)처럼 큰 계획은 하루 가까이 걸렸는데, 단계 대부분은 서로 다른 파일을 건드려 나란히 할 수 있었다. 작업 여러 개는 이미 동시에 돌았지만(상한 4), 작업 하나를 나눠 하지는 못했다. 소유자는 #32 리비전 2 에서 다음을 원했다.

- 계획을 처음부터 병렬로 짠다. A·B·C 는 동시에, D 는 그 뒤.
- 작업 여러 개가 동시에 돈다. 작업 1 은 워커 A·B·C·D, 작업 2 는 E·F.
- 워커는 늘 새 세션으로 시작한다.
- 같은 파일을 건드리면 줄을 세우고, 아니면 병렬로.

결정의 근거와 대안은 [ADR 0008](../LJY-PRD/adr/0008-parallel-parts.md), 동작은 TDD §5.4 에 있다.

## 방법

- 시작: `origin/main` 97e9a7050(#15 의 `0008_lado_verify.sql`, #27 의 `0009_lado_plan_workers.sql` 이 있음)에서 세션 브랜치 `ao/parallel-parts/root` 로 했다. 디스크 여유 56 GB. 이 작업의 마이그레이션은 `0010_lado_parts.sql` 로 origin/main 의 마지막 번호 바로 다음이다.
- 체인 러너 없이 이 워커가 플랜 순서대로 직접 했다. 커밋은 국면마다 나눴고, 첫 push 에서 draft PR #38 을 열었다.

## 진행표

| 플랜 단계 | 내용 | 커밋 |
|---|---|---|
| 준비 | 0008·0009 확인, Go 1.27.1·Node 24, npm 의존성, 디스크 56 GB | — |
| 설계 확인 | 아래 "설계 확인 결과" | — |
| 계획 형식·저장소·겹침 판정·디스패처·통합 | `parts` 파서와 검사, 0010 마이그레이션(실행 표 재구성, 파트 실행·파트 표, 설정 열), 쿼리·sqlc·store, 겹침 함수, 파트 패스·병합·병합 워커·통합 워커, 작업·워커 상한, 디스크, 작업 사이 겹침 대기, 게이트 허가 | 9de6a6dfb |
| 계획 안내·HTTP | 계획 요청 문구와 계획 워커 프롬프트의 병렬 기본 안내, 파트 문제 문장, 작업·큐·실행·계획·설정 DTO, `GET /lado/tasks/{id}/parts`, specgen·`npm run api` | 4f99c8441 |
| CDC·프런트엔드·문서·랩 | 파트 CDC 트리거, 계획 화면 파트·국면, 작업 창 파트 진행, 카드 파트 n/m(대기 레인), 설정 → 실행·프로젝트 겹침 카드, 8개 로캘, TDD §5.4·§4.2·§4.3·§13·§14, HARNESS, PRD(EXEC-005, 관문 20, D15), ADR 0008, LADO.md·LADO.ko.md, DESIGN.md 결정 기록, 이 문서. 랩에서 찾은 결함 셋(아래)과 lint 지적 | (이 커밋) |

## 설계 확인 결과 (플랜 2단계)

1. **파트 세션도 실행이다: `lado_executions` 재구성.** `queue_entry_id` 가 UNIQUE 라 한 큐 항목에 실행이 하나뿐이었다. 8단계 표 다섯이 이 표를 참조해 SQLite 의 테이블 재구성(새로 만들고 복사·삭제·이름 바꾸기)이 막혔다. `PRAGMA defer_foreign_keys = ON` 으로 트랜잭션 안에서 FK 검사를 미루고, `lado_executions_next` 로 복사 → 옛 표 삭제(자식 행이 위반으로 세어짐) → 같은 이름으로 새로 만들고 같은 세션을 다시 넣어(위반이 풀림) 커밋한다. Python sqlite3 로 단계별 실험을 먼저 했다. 새 표는 `role`·`part_id`·`branch`·`start_sha` 를 갖고, PR 을 여는 실행(`role IN ('', 'integration')`)은 항목당 하나라는 부분 UNIQUE 인덱스를 둔다. 덕분에 게이트·권한 상한·규칙 스냅숏·로컬 설정·실행 시간 제한·sweep·복구가 파트 워커에 그대로 적용된다.
2. **큐 항목은 `parts_started_at` 으로 큐를 떠난다.** 파트 실행을 시작하면 항목의 임대가 풀리고 다시 claim 되지 않는다(열린 조건 6곳과 열린 인덱스에 `parts_started_at IS NULL`). 항목 상태 `running_parts`, 작업은 Running. 통합 워커가 뜨면 `dispatched_session_id` 가 그 세션이 되어 그 뒤는 지금 흐름 그대로다.
3. **게이트 허가: 항목의 `part_spawn_request`.** client request id 를 `lado:<entry>/<part>/<n>`·`…/merge-<part>/<n>`·`…/integration/<n>` 로 했다. 기존 파서(`ParseLadoClientRequestID`)는 단일 워커 꼴만 읽으므로 그대로 두고 둘 다 읽는 `ParseLadoRunClientRequestID` 를 더했다. 계획 워커 꼴(`lado:plan/…`, 네 칸)은 읽지 않는다. 띄우기 직전에 항목에 그 요청과 만료(임대 길이)를 기록하고, 게이트는 정확히 그 요청만 허가한다. 정책·로컬 설정 읽기도 새 꼴을 받는다. 복구는 만료된 요청을 임대처럼 정산한다.
4. **병합: 분리 워크트리 대신 `git merge-tree` + `commit-tree`(플랜과 다름).** 플랜은 `~/.ao` 아래 분리 워크트리에서 detach 로 꺼내 `git merge --no-ff` 하는 방법이었다. git 2.38+ 의 `merge-tree --write-tree` 는 같은 ort 병합을 객체 저장소에서 하고 충돌이면 1 로 끝난다. 그래서 체크아웃·워크트리·저장소 훅 없이 `commit-tree -p head -p tip` 으로 병합 커밋을 만들고 `update-ref <ref> <new> <old>` 로 옮긴다. 큰 저장소를 한 번 더 체크아웃하지 않아 디스크를 쓰지 않고, 데몬이 프로젝트 훅을 돌리지 않는다. 이 기계의 git 은 2.43 이다.
5. **완료 신호: 세션의 가장 새 AO 보고가 `done`.** `reports` 를 세션별로 읽는다. done 이면 세션을 멈춰(워커 자리를 비우고 그 뒤 커밋을 막음) 브랜치 끝을 읽는다. done 없이 세션이 끝나면 실패한 시도다.
6. **작업 상태:** 작업의 `Execution` 은 PR 을 여는 실행(단일·통합)만 고른다. 파트 실행 중에는 항목 상태로 Running 이고, 파트 실행이 실패하면 항목 `failed_at` 으로 Failed(`attempts_exhausted`)다. 8단계 검증 루프(`ListLadoExecutionsToVerify`)는 `role IN ('', 'integration')` 만 본다.
7. **취소:** 작업 취소는 기존 sweep 이 살아 있는 실행(파트 포함)을 모두 멈추고, 파트 패스가 실행을 cancelled 로 끝낸다. 새 승인이면 superseded.
8. **상한:** 작업 수는 살아 있는 실행·임대·진행 중 파트 실행을 가진 작업을 한 번씩 센다(`ListLiveLadoRunTasks`). 워커 수는 살아 있는 실행 세션·임대·진행 중 파트 띄우기를 센다(`CountLiveLadoWorkers`). 입장 루프는 claim 마다 부하를 다시 읽는다.

## 결정과 이탈

- 병합 방법(위 4): 분리 워크트리 대신 `merge-tree`. 플랜의 목적(통합 브랜치를 체크아웃하지 않고 병합, main 불변)은 그대로이고 더 가볍다. ADR 0008 대안에 적었다.
- 병합 커밋 메시지는 한국어 Conventional 제목이다(`chore(lado): 파트 <id>(<제목>)를 통합 브랜치에 병합`). 8단계의 한국어 커밋 검사가 PR 의 모든 커밋을 보기 때문이다. 작성자는 `LADO <lado@localhost>`, 서명은 끈다.
- 병합 워커가 쓴 메모는 파트 요약 뒤에 붙여 통합 워커 프롬프트에 함께 간다.
- 통합 워커의 시작 횟수를 세려고 `lado_part_runs.integration_attempts` 를 더했다. AO 는 실패한 client request id 를 다시 쓰지 못하게 하므로, 시도마다 새 id 가 필요하다.
- 디스크 기준은 끄는 스위치(`holdOnDiskLow`)와 GB 값(1–1000) 둘로 했다. 화면의 선택지는 5·10·20·50·100 GB 다.
- 작업 사이 겹침 대기는 다시 확인을 다음 패스(15초)로 둔다. 상대 실행이 끝났는지 따로 구독하지 않는다.
- `0010` 의 down 단계는 `lado_cdc_queue_entries_update` 를 먼저 지운다. 그 트리거가 down 이 지우는 열을 읽기 때문이다(0009 와 같은 방식). 다음 열기 때 다시 만들어진다.
- 기존 마이그레이션 테스트 셋이 "열이 표 끝에 있다"를 보던 것을 "그 순서대로 이어서 있다"로 바꿨다. 0010 이 `lado_executions`·`lado_dispatch_settings` 끝에 열을 더하기 때문이다.
- 화면 문구: 기존 "동시 실행"을 "동시에 도는 작업"으로 바꿨다(전역·프로젝트). 영어는 "Concurrent tasks".
- 랩에서 찾은 결함 1: 파트 워커를 띄우지 못하면(첫 랩 데몬에 ACP 런타임이 없었다) 바로 다시 시도해 두 번의 시도를 15초 안에 다 쓰고 작업이 실패했다. 띄우기 실패는 시도를 쓰되 30초, 2분을 기다린 뒤 다시 하고, 세 번째 실패에 실행이 실패하게 했다(큐 항목과 같은 규칙, 대기 이유 `retry`). `TestAPartWhoseWorkerDoesNotStartWaitsBeforeItTriesAgain`.
- 랩에서 찾은 결함 2: 병합 워커가 충돌을 푸는 동안 다른 파트(c)가 병합되어 통합 브랜치가 움직이자, 병합 워커의 브랜치가 fast-forward 되지 않아 시도가 하나 버려지고 두 번째 병합 워커가 같은 일을 다시 했다. 병합 워커의 브랜치가 파트를 담고 있으면 움직인 통합 브랜치 위에 `merge-tree` 로 병합하게 했다. 그때 충돌이 나야만 시도를 쓴다. `TestAMergeWorkersBranchIsMergedOntoAMovedIntegrationBranch`.
- 랩에서 찾은 결함 3: 파트가 도는 작업은 통합 워커가 뜨기 전까지 자기 세션 카드가 없어 두 LADO 레인 어디에도 없었다. 그래서 카드의 "파트 n/m"이 보일 곳이 없었다. 통합 워커가 뜰 때까지 대기 레인에 남게 했다(`LadoLanes.test.tsx`).
- 남는 것: 취소되거나 대체된 파트 실행은 파트의 마지막 상태를 그대로 둔다. sweep 이 멈춘 파트도 작업 창에서 "실행 중"으로 보인다(랩의 작업 3 의 c). 작업 자체는 취소됨으로 보인다.
- 변경 경로는 모두 플랜의 `affected_paths` 안이다. upstream 공유 파일은 건드리지 않았다(TDD §1.2 변경 없음).

## AO 리뷰

- 1회차(`f5119b79c`, changes_requested). 필수 셋을 모두 고쳤다.
  1. 같은 작업을 다시 승인하면 브랜치 이름(`lado/<task>/…`)이 이전 실행과 겹쳐 새 실행이 실패하거나 이전 워커의 커밋에서 시작할 수 있었다. 이름에 큐 항목 짧은 id 를 넣어 `lado/<task>/<entry>/…` 로 실행마다 따로 두었다(`TestATaskApprovedAgainRunsItsPartsOnNewBranches`).
  2. 병합 워커 결과로 통합 브랜치를 옮긴 뒤 기록 전에 멈추면 다음 패스가 영원히 실패하거나 같은 병합을 매번 다시 만들 수 있었다. 브랜치 끝이 기록된 head 와 다르면 워커 끝(fast-forward)이거나 [기록된 head, 워커 끝]을 부모로 한 병합일 때만 기록하고, 아니면 "LADO 밖에서 움직임"으로 실패시켰다(`TestAMergeWorkersMoveIsRecordedAfterARestart`, 두 경우).
  3. 재시도 브랜치 접미사 `-<n>` 이 유효한 파트 id(`api-2`)와 겹쳤다. 파트 id 에 없는 `.` 을 써 `api.2` 로 했다(`TestLadoPartBranches`).
- 선택 둘도 반영했다. 통합 워커 띄우기 실패도 30초·2분을 기다린다(`lado_part_runs.integration_retry_at`, `TestAnIntegrationWorkerThatDoesNotStartWaitsBeforeItTriesAgain`). 디스패치에서 front matter 나 parts 를 읽지 못한 승인된 계획은 단일 워커로 돌지 않고 시도를 쓴다.

- 2회차(`4587011f1`, approved). 필수 세 가지와 선택 두 가지가 모두 반영됐다고 확인했다. 참고 한 건(고치지 않음): `partsUnreadable` 은 매번 같은 결과가 나오는 실패인데도 시도 세 번(약 2분 30초)을 거친 뒤에 실패한다. 승인된 계획이 이 상태가 되는 일은 사실상 없어 지금 동작으로 둔다.

## 통합 검수

이 PR 은 이 워커 하나가 차례로 만들었으므로 파트 사이 검수 대상은 없다. 랩에서 통합 워커가 한 검수는 아래 "랩 확인"에 적는다.

## 테스트

- Go: domain(파서·겹침·id·브랜치·상태), store(파트 실행·링크·부하·검증 제외·실패와 종료), 마이그레이션(0010 재구성·왕복), ladoqueue(실제 store·임시 git 저장소로 전체 흐름, 재시도와 실패, 취소, 워커 상한, 재시작 뒤 이중 적용 방지, 작업 두 개 동시, 작업 상한, 작업 사이 겹침, 디스크, git 헬퍼), ladogate(파트 허가), ladoproject(겹침 설정), ladotask(계획 안내), controllers(파트 라우트, 설정).
- 렌더러: `LadoParts.test.tsx`, `LadoProjectOverlapCard.test.tsx`, `RunsSection.test.tsx`, `lado-task-status.test.ts`, 로캘 복수형 검사.
- 실행 결과는 PR 본문과 아래 "로컬 확인"에 적는다.

## 로컬 확인

- `npm run sqlc`·`npm run api` 뒤 생성 파일 drift 없음. specgen 이름표에 새 DTO 넷(파트 응답·파트·요약·계획 파트)을 올려 `Controllers` 접두어 없는 스키마 이름이 되게 했다.
- backend: `gofmt` 깨끗, `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./internal/service/ladoqueue/...` 통과. golangci-lint v2.13.2 0 issues(처음 돌린 결과 6건: errorlint 3, rowserrcheck·sqlclosecheck 각 1, unparam 1을 고침).
- race(격리 HOME): ladoqueue·ladotask·ladoproject·ladoverify·ladogate·session_manager·domain·httpd·daemon `GO_RACE_OK`, sqlite·store는 아래.
- frontend: `typecheck`·`typecheck:e2e` 통과(처음엔 스키마 이름과 옛 테스트 고정값의 새 필드 누락으로 실패해 고침). vitest 537 파일 중 1건(`browser-history-store.test.ts`)이 race 테스트와 함께 돌 때 20초 시간 초과로 실패했고, 단독 재실행은 통과했다. 렌더러 smoke 96건 중 채팅 스펙 7건이 부하에서 실패했고, 그 스펙만 다시 돌리면 8건 모두 통과했다(플랜이 예상한 불안정).
- gitleaks v7.4.0(CI 와 같은 방식, `origin/main..HEAD` 커밋): 누출 없음. 게이트 스크립트의 기본 바이너리 경로에는 바이너리가 없어 `/tmp/lrf-tools/gitleaks` 를 썼다.
- `test:browser-postcondition-integration` 은 `xvfb-run` 이 없어 로컬에서 돌리지 못했다(빈칸).
- `go.work.sum` 변경 없음.

## 랩 확인 (2026-10-29)

- 구성: 이 브랜치로 빌드한 격리 데몬(포트 3917, `AO_DATA_DIR=/tmp/pp/lab/data2`, `AO_RUN_FILE=/tmp/pp/lab/running.json`, tmux 소켓 `ao-lab-pp`, `AO_LADO_DEV_PRESENCE=1`, `AO_ACP_RUNTIME_DIR` 는 설치된 데스크톱 릴리스의 `acp-runtime`)과 dev-web(5199). 워커가 실제 Claude 로그인을 쓰도록 HOME 은 실제 HOME 이고 `CLAUDE_CONFIG_DIR` 은 지웠다. GitHub 토큰은 `gh auth token` 값을 데몬 환경 변수로만 줬다(이슈 읽기만). 실사용 데몬(3001)과 `~/.ao/data` 는 쓰지 않았다. 산출물(상태, 실행 목록, 데몬 로그 발췌, 통합 브랜치 그래프, 스크린숏 4장)은 세션 산출물 폴더 `lab/` 에 있다.
- 장난감 저장소: `/tmp/pp/lab/toy`(main `5b569c819b15`), origin 은 로컬 bare 저장소. 승인이 GitHub 저장소를 요구해 DB 의 프로젝트 `repo_origin_url` 만 포크 주소로 바꿨다. 실제 git remote 는 로컬 bare 그대로다. 작업 두 개는 이슈 #30·#26(댓글 없음)을 가리키게 행을 넣었다. 이슈 스냅숏, 계획 리비전, blob 을 직접 넣었고, 승인은 dev presence 로 실제 승인 라우트를 썼다. 계획 본문에 "gh·PR·네트워크 금지, 통합 브랜치는 origin 에 push"를 적었다.
- 작업 1(#30): 파트 a(svc, retry_limit=3), b(web, retry_limit=5, 일부러 낸 모순), c(docs, a 뒤). a·b 둘 다 NOTES.md 에 한 줄을 덧붙여 텍스트 충돌을 낸다. 작업 2(#26): 파트 e(api), f(cli). 모델은 claude-sonnet-5.
- 결과:
  1. 워커 상한 3: a(`ao-demo-1`)·b(`ao-demo-2`)·e(`ao-demo-3`)가 함께 뜨고 f 는 `slot_workers` 로 기다렸다. 상한을 4 로 올리자 f(`ao-demo-6`)가 떴다. 작업 상한 2 안에서 두 작업이 동시에 돌았다.
  2. c 는 a 가 통합된 뒤(`afea57e`) 새 세션 `ao-demo-5` 로 그 커밋에서 시작했다.
  3. b 의 병합이 NOTES.md 에서 충돌해 병합 워커 `ao-demo-4`(브랜치 `merge-b`, 시작 `afea57e`)가 떴다. 그 사이 c 가 통합되어(`c117f30`) fast-forward 가 안 됐고(결함 2), 두 번째 병합 워커 `ao-demo-8`(`merge-b-2`, 시작 `c117f30`)이 두 줄을 모두 살려 `7d311c4` 로 통합됐다. 두 병합 워커 모두 done 요약에 retry_limit 불일치를 통합 단계에 넘긴다고 적었다.
  4. 통합 워커 `ao-demo-9`(작업 1, 시작 `7d311c4`)가 web 의 retry_limit=5 가 계획의 "서비스 값이 기준"과 모순됨을 찾아 3 으로 고쳐 `5449e84 fix: 웹 설정의 retry_limit을 서비스 값(3)으로 맞춘다` 를 커밋하고, done 보고에 "통합 검수 완료"로 적은 뒤 `lado/33333333/integration` 을 origin 에 push 했다. 통합 워커 `ao-demo-7`(작업 2)은 모순 없음을 확인하고 `lado/44444444/integration`(`af53104`)을 push 했다.
  5. 모든 병합 커밋은 체크아웃 없이 만들어졌고 제목이 한국어다(`chore(lado): 파트 a(Service settings)를 통합 브랜치에 병합` 등). main 은 `5b569c8` 그대로다(로컬·origin 모두).
  6. 작업 3(#29, 작업 1 과 같은 경로): 작업 1 의 통합 워커가 살아 있는 동안 `paths_overlap`(상대 #30)으로 기다렸고, 대기 레인 카드에 "Waiting for #30, which changes the same files"가 보였다. 통합 워커를 멈추자 디스패치되어, 워커 상한 1 에서 a 가 돌고 b 는 `slot_workers`, c 는 `part_dependency` 로 기다렸다(작업 창 스크린숏). 작업을 취소하자 살아 있던 파트 세션이 모두 멈췄다.
  7. PR 단계는 GitHub 가 없어 단위 테스트(`TestLadoPartRunsInTheStore`, `TestAPartsRunMerges…`)에 맡겼다. 통합 워커를 멈춘 작업 1·2 는 PR 없이 끝나 Failed(`ended_without_pr`)로 보인다. 랩이라 생긴 결과다.
- 세션: 파트 `ao-demo-1,2,3,5,6,10,11,12`, 병합 `ao-demo-4,8`, 통합 `ao-demo-7,9`. 모두 새 세션이고 client request id 가 서로 다르다.
- 비용을 줄이려고 파트 일은 한두 줄짜리였다. 첫 데몬(`data`)은 ACP 런타임이 없어 워커를 띄우지 못했다(결함 1을 찾음). 두 번째 데몬(`data2`)으로 위 결과를 얻었다.
