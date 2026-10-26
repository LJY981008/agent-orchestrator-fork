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

(단계 시작 때 채운다: 시작 HEAD, 환경, 앞 단계들의 "엔트리와 다르게 한 것" 목록)

## After — 수행 후 실측

(커밋, 문서별로 바꾼 절, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈 — 메인 세션이 할 것: 전체 CI 미러, 랩 확인, 머지 뒤 LADO 페이지 Merged)
