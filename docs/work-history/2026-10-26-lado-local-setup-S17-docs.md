# 2026-10-26 LADO 로컬 설정 채택 — S17 문서: TDD·HARNESS·PRD·ADR-0005·LADO 페이지

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(전체, 특히 소유자 결정 1-4, 결정 a-p, "Facts measured") · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

스펙 문서를 만든 그대로(as built) 맞춘다. 7단계는 스펙에서 벗어난 결정이 셋(plugins 제자리, 프로젝트 미추적 파일 채택 안 함, 저장소 추적 파일은 스냅숏 해시에 안 넣음) 있고, 자격 증명 처리처럼 다음 사람이 다시 재지 않아야 할 실측이 있다. 9단계(Windows·macOS 실측)가 이어받을 수 있게 미검증 상태와 실측 명령도 남긴다.

## 범위

- 바꾼다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, `docs/LJY-PRD/PRD.md`, `docs/LJY-PRD/adr/0005-local-harness-adoption.md`, `LADO.md`, `LADO.ko.md`.
- 하지 않는다: 코드(이 단계는 문서만 커밋한다).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). 특히 각 단계 After 의 "엔트리와 다르게 한 것"을 모아 문서에 반영한다(엔트리가 아니라 코드가 기준).
1. 문체는 기존 "As built in step 5/6" 문단을 따른다(영어, 짧은 문장, 파일 경로 인용). 각 문서에 "As built in step 7" 문단을 해당 절에 더한다:
   - TDD §1.1(표의 `service/ladosetup` 파일들), §1.2(훅 줄 표에 이 단계의 업스트림 파일 — `session_manager` 의 홈 훅과 Chat MCP 목록, `agent_switching.go` 의 대화 기록 확인, codex 어댑터의 덧씌우기, `agentcreds`, `httpd/api.go`, `daemon.go`, 프런트 설정 목록·브리지·preload·main), §3.1(Local setup 행), §4.2(마이그레이션 `0007_lado_setup.sql` 의 표), §4.3(`lado_setup` 종류), §4.5(`<state>/lado/harness/`, `trees/`, `homes/`, `backups/`, `setup-baseline.json`), §9(전달: Claude 홈과 `CLAUDE_SECURESTORAGE_CONFIG_DIR`, Codex 덧씌우기, 실행은 고정 버전), §13(라우트 표를 실제 라우트로, 오류 코드), §14(gate 14 의 테스트 이름), §15 항목 7(만든 것, 소유자 결정, 다음으로 넘긴 것: AI 지침 비교, AO 리뷰어, Windows·macOS 실측).
   - HARNESS §9.1(표에 실제 채택 대상과 사유 — plugins·Codex 폴더·프로젝트 미추적 파일), §9.2(흐름의 실제 단계와 실패 처리), §9.3(관리 폴더의 실제 모양), §9.4(전달 — "Facts measured" 의 자격 증명 실측 포함), §9.5(편집·확인 규칙, 저장소 파일 "편집 = 새 태스크", 스냅숏 해시에 안 넣는 이유), §9.6(변경 감지 — 디스패치 직전 검사가 없는 이유), §9.7(복원 — 미검증 OS 에서도 됨), §10(충돌 표에 실제 코드와 심각도, 결정적이 아닌 것은 경고로 낮춘 이유), §12(화면), §13(표), §14(`setup.change.requires_presence` 등록), §15 항목 9-15(테스트 이름).
   - PRD HAR-005, HAR-010..015 의 마지막 칸에 "Step 7: …" 한 문장씩.
   - ADR-0005: Status 를 "Linux 에서 2026-10-2x 실측(claude 2.1.283, codex 0.163.0, claude-agent-acp 0.70.0); Windows·macOS 는 9단계 실측 전까지 채택 거절" 꼴로(날짜·버전은 S01 After 에서), Decision 의 전달 항목을 실제 방식(Claude 세션 홈 + `CLAUDE_SECURESTORAGE_CONFIG_DIR`, Codex 덧씌우기)으로, Consequences 에 벗어난 결정 셋.
   - `LADO.md`·`LADO.ko.md`: 7행을 "In progress"·"진행 중"(머지 뒤 메인 세션이 Merged 로 바꿈), 쓰는 법 한두 줄(설정 → 로컬 설정, Linux 만, 채택 전 Claude Code·Codex 를 닫을 것, 복원).
2. 9단계를 위한 메모(TDD §15 항목 9 근처 또는 ADR-0005): Windows·macOS 에서 돌릴 실측(`AO_LADO_SETUP_PROBE=1` 과 S01 의 세 테스트), 그 OS 를 검증으로 바꾸는 곳(`domain.LadoSetupDeliveryVerified`).
3. 랩 변수 `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1` 을 개발 안내가 있는 곳(TDD §5.2 의 dev presence 문단 옆)에 한 줄.
4. 커밋 메시지 예: `docs(lado): record step 7 as built`.

## 마무리 (모든 단계 공통)

1. 문서 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S17-docs.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- 없음(문서 단계). 게이트가 문서의 핵심 문구와 gitleaks 를 본다.

## 수용 게이트

- [ ] S17-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S17-G1: TDD 에 7단계 as built 와 핵심 사실
  CHECK: bash .claude/gate/lado-local-setup.sh has docs/LJY-PRD/TDD.md 'As built in step 7' 'CLAUDE_SECURESTORAGE_CONFIG_DIR' '0007_lado_setup.sql' 'lado_setup' 'lado-policy/2' 'AO_LADO_DEV_SETUP_IGNORE_PROCESSES'
  EXPECT: HAS_OK
- [ ] S17-G2: HARNESS 에 7단계 as built 와 벗어난 결정
  CHECK: bash .claude/gate/lado-local-setup.sh has docs/LJY-PRD/HARNESS.md 'As built in step 7' 'setup.change.requires_presence' 'enabledPlugins' 'CLAUDE.local.md' 'LADO_SETUP_MCP_NAME_TAKEN'
  EXPECT: HAS_OK
- [ ] S17-G3: ADR-0005 의 상태가 실측을 말한다
  CHECK: bash .claude/gate/lado-local-setup.sh has docs/LJY-PRD/adr/0005-local-harness-adoption.md '2.1.283' '0.163.0' 'CLAUDE_SECURESTORAGE_CONFIG_DIR'
  EXPECT: HAS_OK
- [ ] S17-G4: PRD 와 LADO 페이지
  CHECK: bash .claude/gate/lado-local-setup.sh has docs/LJY-PRD/PRD.md 'Step 7' && bash .claude/gate/lado-local-setup.sh has LADO.md '| 7 |' 'In progress' && bash .claude/gate/lado-local-setup.sh has LADO.ko.md '| 7 |' '진행 중'
  EXPECT: HAS_OK
- [ ] S17-G5: 비밀 값 없음(CI 와 같은 gitleaks, main..HEAD)
  CHECK: bash .claude/gate/lado-local-setup.sh gitleaks
  EXPECT: GITLEAKS_OK
- [ ] S17-G6: 전체 빌드·vet(코드가 그대로)
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S17-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `cf682cff6`(docs(lado): record S16), 트리 깨끗, 브랜치 `feat/lado-local-setup`, 태그 `lado-step7-chain-def` = `0e7bf9ee8`. Linux 7.0.0-31-generic, go1.27.1, Node v24.21.0.
- 출발점: 스펙 문서는 7단계를 계획으로만 적고 있었다 — TDD §1.2 의 `share.go`·"account env 옆 launch-home 훅" 행, §3.1 의 표 둘, §4.5 의 `harness/`·`backups/` 두 줄, §9 의 계획 문장, §13 의 setup 두 행, §14 gate 14 의 일반 문장, §15 항목 7 한 줄; HARNESS §9 은 "plugins 한 덩어리 채택", 프로젝트 미추적 파일 채택, `projects/<id>/`, 저장소 파일 해시, 계정 링커 수정, "Hooks that block tools AO needs = blocking" 그대로; ADR-0005 상태는 "검증 전 제안"; LADO 페이지 7행은 "Planned".
- 앞 단계들(S01~S16)의 "엔트리와 다르게 한 것"·"다음 단계 메모" 가운데 문서에 옮길 것(코드로 다시 확인):
  - S01: 실측 결과(로그인은 `CLAUDE_SECURESTORAGE_CONFIG_DIR` 로만, 없으면 로그아웃; print·ACP 둘 다 홈을 읽음; 대화 기록은 링크로 base 쪽; 홈에 자격 증명 없음; `-c` 는 `sessionFlags`), `.claude.json` 없이도 로그인됨, 9단계 실측 명령과 Windows 확인 거리.
  - S02·S04: 쿼리 2개 추가, 승인 고정은 항목이 live 일 때만, 바인딩·수락은 live 항목만, 수락 CDC 도 범위별, 트리거 8개 이름.
  - S03·S05: 타입 id, 사유 `symlink`·`unreadable`, 위치 `managed`, `KeptInPlace`, 로그인 env 만 남은 `env` 는 never, scratch 프로젝트 건너뜀, 추적된 개인 파일은 repo_tracked, 조직 관리 설정은 미검사.
  - S06: 플러그인 hook 은 user scope 만, 못 읽으면 info, `=`·`:` 뒤 `~` 도 참조로 셈, POSIX 따옴표만.
  - S07: 기록 실패면 백업에서 되돌림(`putBack`), 첫 항목부터 제거 실패면 기록 없음, 폴더는 rename 뒤 삭제, 제거 직전 재해시, `WithoutCancel`, 관리 키 파일은 그 키만, 다시 못 쓴 hook 경로만 채택을 막고 그 밖의 blocking 은 안 막음, `already_managed`.
  - S08: 기록된 항목만 복원, 없는 키 파일엔 채택한 키만, 채택이 남긴 그대로인 키 파일은 통째로, 멱등, 기준선은 복원한 경로만 더함, presence 종류 keep 은 증명 필수.
  - S09: Chat 은 `PrepareControllerEnv` 콜백 세 곳에서도 홈, `.claude.json` 은 처음만 복사 뒤 서버만 맞춤, 10분 유예, 실행 경로 표, 백그라운드 작업·리뷰어는 홈 없음, `agentcreds` 수정, 링크 안 하는 base 항목(소유자 확인 사항).
  - S10: `CodexConfig` 로 Chat 에 표 전체, 어댑터 훅은 Plugin 필드, harness 는 ctx 값, 프로젝트 없는 세션도 LADO 절, 지침 짝 채택 주의.
  - S11: `installedSha256`, 저장소 settings·원래 위치 설정은 스냅숏 밖, 꺼진 항목도 `Items`, `SetupScope.Change`.
  - S12: inherit 의 확인 규칙(모든 범위), hook 스크립트 만들기 거부, 엄격한 요청 모양, 새 코드들, 결정 i 의 틈(소유자 확인 사항).
  - S13: 라우트 15개(`GET /lado/setup/items` 추가, `{itemId}` 꼴, DELETE 는 쿼리 dryRun·200), Global 충돌 목록의 천장은 정책에서, `startLadoSetup`, `deviceClaudeFolder` 공유.
  - S14: 메인 채널 셋과 묻지 않고 거부하는 경우, 120 s 제한 시간, 증명 수명 60 s 틈, `main.ts` 는 안 바뀜.
  - S15·S16: Local setup 은 Rules 다음 별도 섹션, 채택 대화상자는 다시 못 쓴 hook 경로만 막음, partial 은 결과 화면에서 복원 안내, 미검증 OS 에서만 OS 줄, 편집기·이력·변경 감지·새 항목, 프로젝트·태스크·실행 화면, `requestNewTask(…, prompt?)`·`initialPrompt`·`MarkdownEditor autoFocus` 업스트림 줄, revoke 화면 없음, 실기 랩 미확인.

## After — 수행 후 실측

- 커밋: `078a15cd0 docs(lado): record step 7 as built`(6 파일, +334 −62), 그리고 이 엔트리 커밋. 코드는 건드리지 않았다.
- 문서별로 바꾼 절:
  - **TDD**: §1.1 표 뒤 "As built in step 7"(ladosetup 파일 묶음과 곁 파일들). §1.2 표 — 행 고침(`httpd/api.go`, `daemon/daemon.go`, `daemon/lifecycle_wiring.go`, `session_manager/manager.go`(계획한 account env 옆 훅 대신 `prepareWorkspace` 맨 앞), `agent_switching.go`, settings 목록·ui-store, `TaskComposer`·`NewTaskDialog`, `main.ts`(바뀐 줄 없음), preload, 브리지, tray 카탈로그), 새 행(`chat_spawn.go`·`agent_switching_chat.go`, codex 어댑터, 접근자 넷 파일, `ports/chat.go`·codexappserver, `pkg/agentcreds`, `MarkdownEditor`), `share.go` 행은 "Not edited" 와 이유, 표 뒤 한 문단. §3.1 스냅숏 행(`lado-policy/2`, `lado_policy_snapshot_setup`)·Local setup 행·Conflict acceptance 새 행. §3.2 끝에 setup 변경 → `rules_changed`(문구 "Rules or local setup changed since approval"). §4.2 `0007_lado_setup.sql` 표. §4.3 `lado_setup` 트리거와 렌더러 무효화. §4.5 `harness/claude|codex`, `trees/`, `homes/`+`placed.json`, `backups/`(매니페스트 형식), `setup-baseline.json`, `verify-home-*`, 권한·blob. §5.2 dev presence 옆 `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1`. §9 "Adopted local setup" 문장을 실제로, "As built in step 7"(시작 전엔 그대로, Claude 홈 모양, 로그인 위치와 자격 증명 실측, 버전 고르기, 실행 경로 표, Codex 덧씌우기와 로그인 실측, 스냅숏, 데스크톱 확인 채널 셋과 120 s·60 s, 데몬 배선). §10 Windows 줄(Linux 만 채택, 9단계, 잠긴 파일, 심링크, 프로세스 목록 한계). §13 setup 라우트 7행(15 라우트)과 "Step 7's codes"(상태별). §14 gate 4·10·14 에 7단계 테스트 이름. §15 항목 7(만든 것, 소유자 결정, 실측, 9단계 메모, 넘긴 것, 소유자에게 남긴 것)과 항목 9 에 한 문장.
  - **HARNESS**: §2·§4·§7·§8 에 As built 문단(AO 시스템 층 해시, setup 해석 규칙, capability 없음, `lado-policy/2` 필드·`installedSha256`·스냅숏 밖의 것). §9 머리에 벗어난 결정 셋 요약. §9.1(벗어난 결정 셋과 실제 표 — 상태·사유 코드, 기본 동작 목록, 발견 방식), §9.2(실제 7단계와 실패 처리), §9.3(실제 관리 폴더 모양), §9.4(설계 A, 자격 증명 실측, Codex 전달 표, 지침 짝 주의, 전달 안 되는 곳), §9.5(편집 규칙, dry run·`changeSha256`·감사 동작, presence 실제 판정, 저장소 파일 "편집 = 새 태스크"와 스냅숏에 안 넣는 이유, 소유자 확인 사항), §9.6(시점과 디스패치 직전 검사가 없는 이유, edited·new_local, 기준선), §9.7(미검증 OS 에서도 복원, 실제 규칙과 보고서), §10(실제 코드·심각도 표, 결정적인 것만 blocking 인 이유, 수락, 플러그인, 검사 위치, 안 만든 것), §12(설정 → 로컬 설정, 채택 대화상자, 편집기, 프로젝트·태스크·실행 화면, 아직 안 한 것), §13(6 표), §14(`setup.change.requires_presence` 등록), §15 항목 9-15(테스트 이름).
  - **PRD**: HAR-005, HAR-010..015 마지막 칸에 "Step 7: …" 한 문장씩(앞 문장 끝에 마침표를 더함), §6 Rules manager 행에 Local setup 섹션 한 구절.
  - **ADR-0005**: Status(2026-10-26 Linux 실측 claude 2.1.283·codex 0.163.0·claude-agent-acp 0.70.0, Windows·macOS 는 9단계까지 채택 거절, 복원은 어디서나), Decision 의 채택 대상·전달 항목을 실제로(Claude 세션 홈 + `CLAUDE_SECURESTORAGE_CONFIG_DIR`, Codex 덧씌우기, 링커 그대로, 버전), Consequences 에 훅 위치, 벗어난 결정 셋, 실측, 9단계 실측 명령과 `domain.LadoSetupDeliveryVerified`.
  - **LADO.md·LADO.ko.md**: 7행 "In progress"·"진행 중", 쓰는 법 한 문단(설정 → 로컬 설정, 채택 전 Claude Code·Codex 닫기, Linux 만, 편집·범위, 복원), dev 변수 한 문장, 저장 위치(`harness`·`backups`·`homes`, 도구 폴더는 확인 때만 바뀜), 알려진 한계 한 줄.
- 엔트리와 다르게 한 것과 이유:
  1. 엔트리 목록 밖 절도 고쳤다: TDD §3.2(setup 변경이 Review required 가 되는 길), §10(Windows 의 local setup 줄), §15 항목 9(9단계 메모 가리킴); HARNESS §2·§4·§7·§8·§9 머리·§13(S11 메모가 §8 을 맡겼고, 나머지는 같은 사실이 그 절에서 틀린 채 남기 때문); PRD §6 Rules manager 행(Local setup 이 별도 섹션). 모두 범위 안 파일이다.
  2. ADR-0002 는 고치지 않았다. S14 메모가 "ADR-0002 의 채널 표"를 말했지만 ADR-0002 에는 채널 표가 없고, Decision 이 이미 "setup changes that can run code or widen access, local setup adoption and restore" 를 presence 동작으로 적고 있다. 채널 셋·묻지 않는 거부·120 s·60 s 틈은 TDD §9 에 적었다.
  3. 9단계 메모는 TDD §15 항목 7(자세히)과 ADR-0005 Consequences(실측 명령) 두 곳에 두고, 항목 9 에는 가리키는 한 문장만.
  4. 렌더러 실기 확인·revoke 화면·리뷰어 등 "아직"인 것은 각 As built 문단 안에 적었다(따로 절을 만들지 않음).
- 확인한 명령과 결과:
  - 문서의 사실은 코드에서 다시 확인했다: `git diff --stat main...HEAD`(업스트림 파일 목록, `share.go`·`main.ts` 안 바뀜), 각 업스트림 diff, `scan_claude.go`·`scan_codex.go`·`scan_projects.go`(상태·사유), `conflicts.go`(코드·심각도), `domain/lado_setup.go`(presence 목록·이동 기본 목록·OS 표), `home.go`(경로·`homeBase`·유예), `managed.go`·`inventory.go`(관리 폴더 경로), `adopt.go`·`restore.go`(형식 문자열·`processesIgnored`), `lado_migrate.go`(트리거 이름), `lado-events.ts`, `lado-presence.ts`(120 s·60 s), `lado_setup.go` 컨트롤러(라우트), 테스트 이름 23개의 존재와 doc 주석.
  - 사실 확인 서브에이전트(general-purpose, sonnet, 전경, 읽기 전용): §13 라우트·코드, HARNESS §9.1 표·기본 동작, §10 표·§9.5 presence, §9.6·§9.7·§9.2 실패, TDD §9 홈·경로·Codex·배선, §1.2 행 — 틀린 주장 없음.
  - 게이트(코드 커밋 `078a15cd0` 뒤 직접): G1~G4 `HAS_OK`, G0 `DEFS_UNCHANGED`, G5 `GITLEAKS_OK`, G6 `GO_BUILD_OK`, 트리 깨끗. 이 엔트리 커밋 뒤 `gate-check.sh --run` 전건으로 다시 기록한다.

## 비교/회고

- 범위 대비 결과: 엔트리의 문서 여섯 개와 절 목록 전부, 9단계 메모(TDD §15 항목 7, ADR-0005), 랩 변수(TDD §5.2). 코드 변경 없음. 엔트리 밖은 After 의 "다르게 한 것" 1 뿐(같은 파일 안의 낡은 문장).
- 남은 틈 — 메인 세션이 할 것:
  - 전체 CI 미러(Go 전체·race·lint, 프런트 typecheck·vitest, api·sqlc drift, gitleaks)를 브랜치 전체에 한 번. 이 단계는 문서만이라 go build·vet 와 gitleaks 만 돌렸다.
  - 랩 확인(격리 데몬 + 임시 HOME, lado-lab-recipe): 채택 → 세션이 받는지 → 편집·유지·되돌리기 → 복원을 실제 앱으로 끝까지, 다크·좁은 폭·한국어 화면도. S13~S16 이 모두 남긴 틈이다.
  - 머지 뒤 LADO.md·LADO.ko.md 7행을 "Merged"·"병합됨"으로, TDD §15 항목 7 은 그대로("Built … on `feat/lado-local-setup`" 꼴이 5·6단계와 같다).
  - 소유자 결정 거리(TDD §15 항목 7 "Left for the owner", HARNESS §9.4·§9.5 에 적음): 홈이 링크하지 않는 base 항목(`keybindings.json`, `rules/` 등), 지우기·끄기의 확인 없음, front matter 의 hook·allowed-tools, presence 목록이 거부 목록인 점. 그리고 S14 의 증명 수명 60 s 틈.
  - S10 이 남긴 lint 1건이 아직 있다: `session_manager/lado_setup_home_test.go:176` 변수 `real`(revive redefines-builtin-id). 이 체인 안에서 고친 단계가 없어 CI lint 가 실패할 것이므로 리뷰 수정 때 이름을 바꾼다(문서 단계라 손대지 않음).
- 다음 단계 메모: 체인의 마지막 단계다. 문서에서 9단계가 이어받을 곳은 TDD §15 항목 7 의 "For step 9" 와 ADR-0005 Consequences 의 "Still to verify" — `AO_LADO_SETUP_PROBE=1` 로 S01 의 세 테스트를 Windows·macOS 에서 돌리고, 통과한 OS 를 `domain.LadoSetupDeliveryVerified` 와 그 테스트에서 검증으로 바꾼다.
