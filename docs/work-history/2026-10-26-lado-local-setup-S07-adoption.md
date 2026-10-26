# 2026-10-26 LADO 로컬 설정 채택 — S07 채택: 미리보기·전제 조건·백업·복사·제거·기록

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 e·f·g·h, §7) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-011 의 본체: 고른 항목을 백업하고, 관리 폴더와 blob 으로 옮기고, 원래 자리에서 지운다. 사용자의 실제 파일을 지우는 유일한 코드이므로 순서가 전부다 — 백업과 매니페스트를 다시 읽어 검증하기 전에는 아무것도 지우지 않고, 자격 증명과 앱 상태는 절대 건드리지 않는다(PRD gate 14 의 앞 절반, HARNESS §15 항목 9).

## 범위

- 만든다: `service/ladosetup/adopt.go`(미리보기·적용 흐름), `backup.go`(백업·매니페스트·검증), `managed.go`(관리 폴더 쓰기와 blob·트리 저장), `jsonedit.go`(순서를 지키는 JSON 객체 키 빼기·넣기), `tomledit.go`(`[mcp_servers.<이름>]` 블록 줄 단위 빼기·넣기), `rewrite.go`(hook 경로 다시 쓰기 — S06 의 참조 함수 사용), 테스트.
- 하지 않는다: 복원·변경 감지(S08), 세션 전달(S09·S10), 데스크톱 확인 검증(S13 컨트롤러 — 이 단계의 서비스는 호출자가 넘긴 증명 결과 `Presence func(action, subject) (proof, error)` 를 부른다, `ladopolicy` 의 꼴).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 요청: `AdoptRequest{Entries []{Tool, Kind, Name, Action ("move"|"keep")}, PlanSHA256 string, DryRun bool, Presence}`. `keep` 은 미리보기에만 보이고 아무것도 하지 않는다.
2. 미리보기(플랜 §7.1): 다시 검사(S05) → 항목마다 adoptable 인지(아니면 400 `LADO_SETUP_NOT_ADOPTABLE` + details `{tool, kind, name, reason}`) → 관리 경로, 전달 방식(`home`|`overlay`), hook 다시 쓰기 목록(전·후 명령), 추가될 충돌(S06), 직접 실행 경고 문구 코드, 전제 조건 결과(OS·AO 세션·바깥 프로세스 목록), `planSha256`(정규 JSON — 플랜 §7.1 의 모양, 항목은 tool·kind·name 순). 미리보기는 아무것도 쓰지 않는다.
3. 적용(플랜 §7.2-7.7) 순서를 코드 구조로 지킨다 — 단계마다 함수 하나, 앞 단계의 성공 결과만 다음 단계에 넘긴다:
   1. 다시 검사하고 `planSha256` 재계산 → 다르면 409 `LADO_SETUP_CHANGED`. 전제 조건 → 409 `LADO_SETUP_OS_UNVERIFIED` / `LADO_SETUP_TOOL_RUNNING`(details: 세션 id·프로세스 PID 와 이름). 데스크톱 확인(`Presence(setup.adopt, planSha256)`).
   2. 백업(`backup.go`): `<state>/lado/backups/<adoption-id>/files/<root>/<상대 경로>` 에 원본 바이트·mode·mtime 을 그대로, 키를 뺄 파일(`settings.json`, `~/.claude.json`, `config.toml`)은 통째로. `manifest.json`(플랜 §7.3 의 필드). 파일 fsync, 폴더 fsync. 그다음 백업 전체를 다시 읽어 sha256 을 원본·매니페스트와 대조 — 하나라도 다르면 오류로 끝(아무것도 지우지 않음). 권한 폴더 0700·파일 0600.
   3. 복사(`managed.go`): blob(`ladoblob`) — 파일 바이트, JSON 값(정규 JSON), 트리(파일마다 blob + 매니페스트 blob). 관리 폴더 `<state>/lado/harness/claude/` 와 `codex/` 를 그 버전들로 다시 쓴다(`settings.json` = 채택 키만, `mcp.json` = `{"mcpServers":{...}}`, `config.toml` = 채택 표만, 파일·폴더는 그대로; 실행 비트는 매니페스트의 `exec`). 쓴 뒤 해시 대조.
   4. 제거: 파일·폴더 항목 삭제(그릇 폴더는 남김 — 결정 f), 키 파일은 `jsonedit`/`tomledit` 로 채택 키만 빼고 같은 폴더의 임시 파일에 쓴 뒤 rename(원래 mode). `settings.json` 의 `env` 항목은 로그인 env 만 남긴다. 실패하면 그 자리에서 멈춘다.
   5. 기록: store `RecordLadoSetupAdoption`(채택 `completed`, 항목, 첫 버전 — actor 는 증명의 actor, 감사 `setup.adopted` payload: 항목 수, 도구별 수, 다시 쓰기 수, `processesIgnored`). 제거 중 실패면 그때까지 옮긴 항목으로 `partial` 과 오류 문장을 기록한다(복원 가능). 변경 감지 기준선(원래 위치에 남은 것의 해시)을 `<state>/lado/setup-baseline.json` 에 쓴다.
   6. 검증(구조): 임시 폴더에 자기 로그인용 홈을 S09 이전 최소 형태로 만들 수 없으므로, 이 단계에서는 관리 폴더의 모든 채택 항목이 기록된 해시와 같은지와 Codex 서버마다 TOML 인라인 값이 파싱되는지만 확인해 응답에 넣는다(S09 가 홈 검증을 더한다 — 다음 단계 메모).
4. 응답: 채택 id, 항목(새 item id), 다시 쓰기, 검증 결과, 경고. 도중 실패의 응답은 apierr(코드 `LADO_SETUP_ADOPTION_FAILED`, details `{stage, path}` — 경로는 사용자 폴더 안 상대 경로만).
5. `jsonedit`: 최상위 객체의 키 순서를 지키며 키를 빼고 넣는다(값의 원래 바이트는 `json.Indent` 로 2칸 들여쓰기), 마지막 줄바꿈 유지. `tomledit`: `[mcp_servers.<이름>]` 머리 줄부터 다음 표 머리 줄 직전까지와 `[mcp_servers.<이름>.<하위>]` 블록을 뺀다; 넣기는 파일 끝에 백업한 블록 텍스트를 붙인다. 둘 다 표 테스트.
6. Windows 를 위한 것: 경로는 `filepath`, 잠긴 파일 삭제 실패는 4 의 멈춤으로(테스트는 지우기 함수를 가짜로 바꿔 실패를 낸다).
7. 커밋 메시지 예: `feat(lado): adopt local setup with a verified backup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S07-adoption.md` 로 전건 MET 를 확인한다.

## 필수 테스트

모두 `t.TempDir()` 의 가짜 홈과 실제 store(`sqlitetest.MustOpen`) 또는 가짜 store 로:

- `TestAdoptionPreviewHashesThePlan` — 같은 선택 → 같은 해시, 파일 하나 바뀜 → 다른 해시, adoptable 아님 → 400, 미리보기는 아무것도 쓰지 않음(홈·state 폴더 목록 그대로)
- `TestAdoptionBacksUpBeforeRemoving` — 제거 함수가 불릴 때 매니페스트 검증이 이미 끝남(순서 기록), 백업 검증을 깨뜨리면(바이트 하나 바꿈) 원본은 그대로·기록 없음
- `TestAdoptionNeverTouchesCredentials` — `.credentials.json`, `auth.json`, `projects/`, `history.jsonl`, `~/.claude.json` 의 다른 키가 바이트·mtime 그대로
- `TestAdoptionRemovesOnlyAdoptedKeys` — `settings.json` 의 나머지 키 순서·값, 로그인 키·로그인 env 유지, `~/.claude.json` 의 다른 서버·키 유지, `config.toml` 의 주석·다른 표 유지
- `TestAdoptionKeepsContainers` — 모든 스킬을 채택해도 `skills/` 폴더는 남음(빈 폴더)
- `TestAdoptionRewritesHookPaths` — `~/.claude/hooks/x.sh` 가 `"$CLAUDE_CONFIG_DIR"/hooks/x.sh` 로, 미리보기에 전·후, 다시 쓰지 못한 참조는 blocking 으로 거부
- `TestAdoptionRefusesWhilePreconditionsFail` — 미검증 OS, 살아 있는 AO 세션, 바깥 프로세스 각각 409 와 목록, dev 무시면 프로세스만 통과하고 감사에 표시
- `TestAdoptionPartialFailureLeavesRestore` — 제거 셋째 항목에서 실패 → `partial`·오류 문장, 앞 두 항목만 관리됨, 셋째 원본은 그대로
- `TestAdoptionPlanMismatchWritesNothing` — 미리보기 뒤 파일이 바뀌면 409 `LADO_SETUP_CHANGED`, 백업 폴더도 안 생김
- `TestJSONEditKeepsOrder`, `TestTOMLEditRemovesOnlyTheTable` — 위 5 의 표 테스트

## 수용 게이트

- [ ] S07-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S07-G1: 필수 테스트 11개가 격리된 홈에서 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestAdoptionPreviewHashesThePlan TestAdoptionBacksUpBeforeRemoving TestAdoptionNeverTouchesCredentials TestAdoptionRemovesOnlyAdoptedKeys TestAdoptionKeepsContainers TestAdoptionRewritesHookPaths TestAdoptionRefusesWhilePreconditionsFail TestAdoptionPartialFailureLeavesRestore TestAdoptionPlanMismatchWritesNothing TestJSONEditKeepsOrder TestTOMLEditRemovesOnlyTheTable
  EXPECT: GO_TESTS_OK 11/11
- [ ] S07-G2: 패키지 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S07-G3: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S07-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S07-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `ed487f0a5`(docs(lado): record S06), 브랜치 `feat/lado-local-setup`, 트리 깨끗.
- 환경: Linux(Ubuntu), go1.27.1(scratchpad 툴체인), golangci-lint v2.13.2(오프라인 빌드본, `GOPROXY=off`). 실제 `~/.claude`·`~/.claude.json`·`~/.codex` 는 읽지도 쓰지도 않았다 — 테스트는 `t.TempDir()` 가짜 홈, 게이트는 격리 홈.
- 출발점: `service/ladosetup` 에 인벤토리·충돌·AO 시스템 층·실행 중 도구만 있었다. `Store` 인터페이스는 읽기 둘(`ListLiveLadoSetupItems`, `ListLadoSetupAdoptions`), `managedPath` 는 `inventory.go` 에, 백업·관리 폴더 쓰기·키 편집은 없었다.
- 앞 단계 메모 중 이 단계 몫:
  - S03: 동작 `LadoSetupActionMove`/`Keep`, presence `setup.adopt`(subject = planSha256), 버전 sha256 = blob 이름(파일 바이트, 정규 JSON, 트리 `CanonicalJSON()`), 파일 실행 비트는 `Executable`, 트리는 항목별 `Mode`.
  - S04: store 가 items 의 `AdoptionID`·versions 의 `Version` 을 덮어쓰고 versions 의 `ItemID` 는 채워 보낸다. 제거 중 실패는 처음부터 `partial` 로 감사와 한 트랜잭션에 넣는다.
  - S05: managed 가 아닌 adoptable 줄을 tool·kind·name 으로 찾기, 계획 해시의 `originPath`·`sha256` 은 줄의 `Path`·`SHA256`, `codexServerHeader` 와 같은 머리 줄 규칙, 로그인 변수가 안 남는 env 는 키 통째로 삭제, `RunningTools` 를 도구별로 거르고 `ProcessesIgnored` 를 감사로, 원본 읽기는 `fileSum`/`openRegular`, 관리 경로는 `managedPath`.
  - S06: `Conflicts(ConflictInput{Setup, Moved})`, `Moved` 는 Claude 폴더 기준 슬래시 경로, 다시 쓰기는 끝에서부터 `cmd[:Start]+Replacement+cmd[End:]`, 다시 쓴 값으로 검사하면 남는 `LADO_SETUP_HOOK_PATH_MOVED` 는 다시 못 쓴 참조뿐.

## After — 수행 후 실측

- 커밋: `2f49fd6e1` feat(lado): adopt local setup with a verified backup · `f4775f198` refactor(lado): check setup preconditions by tool · 이 엔트리 커밋.
- 바꾼 것(모두 `backend/internal/service/ladosetup/`):
  - `adopt.go` — `Adopt(ctx, AdoptRequest)`; `DryRun` 이면 미리보기만. 단계 함수가 앞 단계의 성공 결과만 받는다: `plan`(다시 검사·adoptable 확인·키 내용 읽기·hook 다시 쓰기·충돌·전제 조건·`planSha256`) → `confirm`(해시·이동 0개·다시 못 쓴 참조·OS·실행 중·presence) → `backup` → `copyManaged` → `removeOriginals` → `record` → `finish`(기준선·구조 검증). 채택은 쓰기 잠금(`lockWrite`)으로 한 번에 하나. 코드 상수 `CodeAdoptionInvalid`·`CodeNotAdoptable`·`CodeChanged`·`CodeOSUnverified`·`CodeToolRunning`·`CodeAdoptionFailed`·`CodeUnavailable`, 경고 `LADO_SETUP_DIRECT_USE_STOPS`·`LADO_SETUP_BASELINE_FAILED`, 전달 `home`/`overlay`, 검증 검사 `managed_copy`/`codex_override`. 기준선 `<state>/lado/setup-baseline.json`(`baselinePath`).
  - `backup.go` — 매니페스트 `lado-setup-backup/1`, 복사본 0600·폴더 0700·원래 mtime, 파일·폴더 fsync, `verifyBackup`(manifest.json 바이트·복사본 해시·계획 해시 대조), `restoreCopy`(빈 자리에 원래 mode·mtime 으로), `atomicWrite`·`writeNew`·`syncTree`.
  - `managed.go` — `writeManaged`(버전 blob → 관리 폴더; 파일·트리는 그 경로, 키는 그 키만), `removeManaged`, `managedSum`/`checkManaged`, `keyValue`(원래·관리 파일 공용 키 내용 읽기), `managedKeyFile`/`managedRoot`, TOML 렌더 `codexServerBlock`·`codexServerOverride`·`checkCodexOverride`·`tomlInline`.
  - `jsonedit.go`(`jsonWithout`·`jsonWith`·`jsonMemberValue`·`jsonWithoutMCPServer`·`jsonWithMCPServer`), `tomledit.go`(`tomlWithoutServer`·`tomlWithBlock`, 둘 다 파싱 대조), `rewrite.go`(`rewriteHookPaths`, `HookRewrite`).
  - `service.go` — `Store` 에 `RecordLadoSetupAdoption`, `Service` 에 `writing`·`ops`(removeAll·rename, 테스트 대역)·`onStage`·`afterBackup`. `inventory.go` — `managedPath` 가 `managedKeyFile`·`managedRoot` 를 쓴다(경로 그대로).
  - 테스트: 필수 11개 + `TestAdoptionPutsBackWhenRecordFails`(실제 store `sqlitetest.MustOpen`, 실제 `ladoblob`, 가짜 홈).
- 엔트리와 다르게 한 것과 이유:
  1. **기록 실패면 되돌린다**(`putBack`): 제거 뒤 store 가 거부하면(저장 오류, 재사용된 presence nonce — nonce 는 기록 트랜잭션에서야 확인된다) 기록 없이 원본이 사라져 복원 API 로 못 돌린다. 지운 파일·폴더는 백업에서 원래 mode·mtime 으로, 다시 쓴 키 파일은 LADO 가 쓴 그대로일 때만 백업 바이트로 되돌리고 관리 폴더를 비운다.
  2. 첫 항목부터 제거에 실패하면 partial 을 기록하지 않는다(옮긴 것이 없어 복원할 것이 없다) — 관리 폴더를 비우고 `LADO_SETUP_ADOPTION_FAILED`(stage remove).
  3. 폴더는 같은 폴더 안 `.<이름>.lado-removed-<id>` 로 rename 한 뒤 지운다: rename 이 실패하면 원본이 온전하고, 지우기 실패는 경고 로그(찌꺼기는 인벤토리에 unknown 으로 보인다).
  4. 지우기·다시 쓰기 직전에 원본을 다시 해시해 백업과 같은지 본다(키 파일은 LADO 가 마지막으로 쓴 해시와) — 백업에 없는 바이트는 지우지 않는다. 다르면 그 자리에서 멈춘다(partial).
  5. 제거부터는 `context.WithoutCancel`: 호출자가 끊겨도 제거와 기록 사이에서 멈추지 않는다.
  6. 관리 키 파일(settings.json·mcp.json·config.toml)은 통째로 다시 만들지 않고 그 키만 넣고 뺀다(값은 버전 blob) — 손으로 고친 다른 키(S08 변경 감지 대상)를 덮지 않으려고.
  7. partial 은 오류(details `stage, root, path, adoption, state:"partial"`)와 함께 결과(`Applied`, `State`, 옮긴 항목)도 돌려준다.
  8. 다시 못 쓴 참조의 거부는 충돌 코드 그대로 409 `LADO_SETUP_HOOK_PATH_MOVED`(details 는 그 충돌의 details). 다른 blocking 충돌(`ao` 이름 등)은 채택을 막지 않는다 — 제자리에 둬도 같은 충돌이고, 미리보기와 확인 창에 보인다.
  9. `LADO_SETUP_NOT_ADOPTABLE` 사유에 인벤토리 사유 외 `not_found`, `already_managed`(같은 tool·kind·name 의 live 항목 — store 가 기록 단계에서 `ErrLadoSetupItemExists` 로 거부하기 전에 미리보기에서 막는다)를 더했다.
  10. 미리보기 충돌은 옮길 줄에 관한 결과와 모든 `HOOK_PATH_MOVED` 만 남긴다(전역 범위, 천장 없음 — 범위별 전체 충돌은 S11).
  11. 중첩 객체 편집은 mcpServers 전용 `jsonWithoutMCPServer`/`jsonWithMCPServer`(일반 함수는 lint unparam).
- 확인한 명령과 결과: `go vet ./internal/service/ladosetup/` 통과; golangci-lint v2.13.2 `run ./internal/service/ladosetup/...` → 0 issues; `go test -v -run '^TestAdoption|^TestJSONEdit|^TestTOMLEdit'` → 12 PASS. 변이 둘을 넣어 테스트가 잡는지 봤다(원복함): 백업 검증 건너뛰기 → `TestAdoptionBacksUpBeforeRemoving` 실패(복사 단계가 blob 해시로 한 번 더 막아 stage copy 로 멈춤), env 의 로그인 변수 버리기 → `TestAdoptionRemovesOnlyAdoptedKeys`·`TestAdoptionNeverTouchesCredentials` 실패.
- 게이트(직접 실행, 커밋 뒤 gate-check 로 다시 기록): G0 `DEFS_UNCHANGED` · G1 `GO_TESTS_OK 11/11` · G2 `GO_RACE_OK`(23 s) · G3 `NO_HOME_LOOKUPS` · G4 `GO_BUILD_OK` · G5 엔트리 커밋 뒤 `TREE_CLEAN`.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 그대로 — 미리보기·planSha256·전제 조건·백업과 다시 읽기·blob 과 관리 폴더 복사·제거(그릇 폴더 유지)·키 빼기·hook 경로 다시 쓰기·기록(partial 포함)·기준선·구조 검증, 필수 테스트 11개. 복원·변경 감지·세션 전달·HTTP 는 없다. 엔트리 밖으로 더한 것은 After 의 1~11.
- 남은 틈:
  - 구조 검증은 관리 폴더 해시와 Codex `-c` 값 파싱뿐이다 — 자기 로그인용 홈 검증은 S09.
  - 미리보기 충돌에 권한 천장이 없다(`ConflictInput.Ceiling` 비움) → `DEFAULT_MODE_ABOVE_CEILING` 은 채택 미리보기에 안 나온다(S11 범위 검사가 낸다).
  - `ClaudeDirFromEnv` 일 때의 `~`·`$HOME` 과잉(S06 그대로): 다시 쓰기도 같은 참조 함수를 써서 `~/.claude/…` 를 그 폴더로 보고 바꾼다. 데몬이 홈을 `Deps` 로 넘기면(S13) 고칠 자리는 `movedPathReferences` 를 부르는 두 곳(`conflicts.go` hooks·statusLine, `rewrite.go`).
  - 큰 폴더 항목(스킬 안 `node_modules`)도 파일마다 메모리로 읽어 blob 에 넣는다. blob 읽기 상한은 256 MiB(`maxBlobBytes`), 채택 크기 상한은 없다.
  - JSON 편집은 LF·2칸으로 쓴다: CRLF 파일은 LF 가 되고, 키는 다시 인코딩되며(원래 escape 안 지킴), 사람이 한 줄로 쓴 값은 펼쳐진다. Claude 가 쓴 파일(2칸 `JSON.stringify`)은 남는 값의 바이트가 그대로다(테스트로 고정).
  - TOML: 다음 표 머리 줄 바로 위의 주석은 그 표 몫으로 남긴다 — 잘라낸 서버 머리 줄 바로 위 주석은 고아로 남는다. 여러 줄 문자열 안의 가짜 머리 줄은 파싱 대조가 잡아 쓰지 않고 실패한다(partial).
  - Windows: 폴더 fsync 를 건너뛴다(열 수 없음). 잠긴 파일은 제거 실패(partial)로 간다. 실측은 9단계.
  - 기준선은 채택마다 그때 상태로 덮어쓴다: 채택 사이에 생긴 new_local 은 다음 채택의 기준선에 흡수된다.
  - live 이름 검사와 기록 사이의 경쟁은 쓰기 잠금으로만 막는다 — 같은 이름을 만들 수 있는 S12 변경도 `lockWrite` 를 잡아야 한다.
- 다음 단계 메모:
  - **S08(복원·변경 감지)**:
    - 백업: `backupDir(stateDir, id)` = `<state>/lado/backups/<id>`, `manifest.json` = `backupManifest{format "lado-setup-backup/1", adoptionId, createdAt, os, planSha256, roots{claude_dir,claude_json,codex_home → 절대 경로}, entries}`. 항목 `backupEntry{tool, kind, name, action, root, relPath(슬래시), path(원래 절대 경로), type file|dir|keys, sha256, size, mode("0644"), mtime, files[{path, dir, mode, mtime, size, sha256}](폴더, 걷기 순서), removedKeys[{key, member, sha256, keptInPlace}]}`. 복사본 경로 `be.copyPath(dir)`. 채택 행의 `ManifestSHA256` = manifest.json 바이트의 sha256 — 복원 전에 대조할 것.
    - 키 항목: 설정은 `key` = 설정 이름; Claude 서버는 `key "mcpServers"`·`member` = 이름; Codex 는 `key "mcp_servers"`·`member`. 키 파일은 항목마다 같은 복사본을 가리킨다(파일당 한 번 복사). 값은 `keyValue(tool, kind, name, 백업 바이트)`, 넣기는 `jsonWith`/`jsonWithMCPServer`, Codex 블록은 `tomlWithoutServer(백업 바이트, 이름)` 의 둘째 값을 `tomlWithBlock` 으로 붙인다(이미 있으면 오류 → key_kept). **env**: 채택 뒤 원래 settings.json 에 `env` 가 로그인 변수만 담고 남아 있을 수 있다 — "키가 있으면 kept" 로 두면 옮긴 변수가 안 돌아온다. `keptInPlace` 가 있으면 env 객체에 없는 변수만 합쳐 넣을 것(`jsonMembers` 로 순서 유지).
    - 파일·폴더: 빈 자리면 `restoreCopy(dir, be)`(원래 mode·mtime, 폴더는 하위까지), 차 있으면 `<이름>.lado-restored` 로 — `restoreCopy` 는 `be.Path` 에 쓰므로 `be.Path` 를 바꾼 사본을 넘기면 된다.
    - 전제 조건: `s.preconditions(ctx, tools)` 를 부르고 `Unverified` 는 무시(복원은 모든 OS), 거부 details 는 `runningDetails(p)`. 쓰기는 `lockWrite`/`unlockWrite` 안에서, 끝나면 `s.Scan(ctx)` 로 캐시 갱신, presence 는 `confirm` 의 꼴.
    - 변경 감지: 기준선 `setupBaseline{format "lado-setup-baseline/1", savedAt, entries[{tool, kind, name, path, sha256}]}` at `baselinePath(stateDir)` — 원래 위치의 adoptable 줄 전부(keep·미선택 포함). edited 는 `s.managedSum(ctx, item)` 과 최신 버전 sha 비교(`checkManaged` 는 파일 실행 비트도 본다). 되돌리기는 `s.writeManaged(iv)`.
  - **S09(홈)**: 홈 검증 자리는 `verifyAdopted` — `managed_copy`·`codex_override` 다음에 Claude 항목마다 `home` 검사를 더한다(상수 `Check…` 하나 추가). 트리는 `treeOfVersion(v)`(blob 매니페스트 읽기·`Normalize`·sha 대조)로 `<state>/lado/harness/trees/<sha256>/` 를 만들 수 있다. 관리 경로 함수 `managedPath`·`managedRoot`·`managedKeyFile`.
  - **S10(Codex)**: `codexServerOverride(name, content)` 가 `-c` 인자 값 그대로(`mcp_servers.<name>={…}`, 키 정렬, JSON 숫자는 쓴 그대로) — `checkCodexOverride` 가 파싱 대조. 값은 `tomlInline`.
  - **S12(변경)**: 버전을 관리 폴더에 쓰기 `writeManaged(iv)`(키 항목은 그 키만 넣고 뺌 — 다른 키는 그대로), 지우기 `removeManaged(item)`. 같은 이름 생성과 채택이 겹치지 않게 `lockWrite` 를 잡는다. presence 꼴은 `confirm` 과 같다(nil → 403 `LADO_PRESENCE_REQUIRED`, `proof.Dev` → actor `human:dev-presence`, 재사용 → `ladopresence.Replayed()`).
  - **S13(HTTP·배선)**: `Adopt` 의 partial 은 결과와 오류를 함께 돌려준다(오류 kind internal, details 에 `state:"partial"`·`adoption`) — 응답에 둘 다 싣거나 오류만 보낸다. `CodeUnavailable` 은 apierr Unavailable. `AdoptRequest.Presence` 는 ladopolicy 처럼 Verifier.Verify 를 감싼 클로저(subject = planSha256). `Deps.Blobs = ladoblob.New(stateDir)`, `Deps.Store` 는 `RecordLadoSetupAdoption` 까지 필요.
  - **S14(번역)**: 코드 7개(`LADO_SETUP_ADOPTION_INVALID`·`NOT_ADOPTABLE`·`CHANGED`·`OS_UNVERIFIED`·`TOOL_RUNNING`·`ADOPTION_FAILED`·`UNAVAILABLE`) + 거부에 쓰는 `LADO_SETUP_HOOK_PATH_MOVED` + 경고 2개 + 사유 `not_found`·`already_managed`. details 키: `stage(backup|copy|remove|record)`, `root`, `path`, `adoption`, `state`, `planSha256`, `os`, `tools`, `sessions[{id,tool}]`, `processes[{pid,name,tool}]`, `tool`, `kind`, `name`, `reason`.
