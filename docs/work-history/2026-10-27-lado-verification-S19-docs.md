# 2026-10-27 LADO 검증·통합 승인 — S19 문서: TDD·HARNESS·PRD·ADR-0003·LADO.md

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 전부) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

명세가 지은 것과 맞아야 다음 단계(9단계 릴리스, 업스트림 동기화)가 문서를 믿을 수 있다. 8단계가 실제로 만든 것, 소유자 결정, 내 결정 a–t, 벗어난 점, 남긴 틈을 앞 단계들과 같은 "As built in step 8" 형식으로 적는다. 코드는 건드리지 않는다.

## 범위

- 바꾼다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, `docs/LJY-PRD/PRD.md`, `docs/LJY-PRD/adr/0003-enforcement-points.md`, `LADO.md`, `LADO.ko.md`.
- 하지 않는다: 코드·테스트·생성물.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S01~S18 의 After 에 적힌 "엔트리와 다르게 한 것"을 모두 반영한다.
1. 형식: 기존 "As built in step 7" 단락처럼 영어, 짧은 문장, 파일·함수·코드 이름을 백틱으로, 지은 것만(계획만 한 것은 "Not built"). 이 저장소 문서의 문체(앞 단계가 쓴 단락)를 따른다.
2. TDD:
   - §1.1 표에 `service/ladoverify/` 파일들과 새 어댑터·컨트롤러·렌더러 파일; §1.2 표에 8단계 훅(`service/pr/action_service.go`, `httpd/controllers/prs.go`, `review/launcher.go`, `httpd/api.go`, `daemon/daemon.go`, `frontend/src/preload.ts`, 브리지들, `sync-tray-catalog.mjs`)과 각 줄 수의 의미.
   - §3.1 표의 `lado_integration_approvals`·`lado_check_runs`·`lado_evidence_links` 를 지은 대로, 새 `lado_verifications`·`lado_execution_reviews`; §3.2 에 "As built in step 8"(Validating·Integration pending·Integrating·Completed 와 사유, 기록이 head 별 증거라는 것), §3.3 에 보드 줄.
   - §4.2 에 마이그레이션 `0008_lado_verify.sql`, §4.3 에 새 `lado_execution` 트리거, §4.5 에 검사 로그·근거 파일 blob.
   - §7.1-§7.3, §8.1-§8.6 끝마다 "As built in step 8"(규칙셋 읽기와 캐시, 상태 게시와 재시도, 보호 판정과 확인, 병합 게이트, 기준 브랜치 reflog, 필수 검사 규칙, 검사 명령과 env·트리 종료, 커밋·경로 검사기, 보고는 주장, 리뷰 정책, 근거 파일 형식과 대조, 오래된 근거).
   - §9 에 리뷰어 지침(7단계가 남긴 리뷰어 행을 고친다).
   - §13 라우트 표와 8단계 오류 코드 목록.
   - §14 표: 관문 6·7·8·9·10 에 8단계 테스트 이름.
   - §15 항목 8: Built, 소유자 결정(2026-10-27 넷), 내 결정의 요지, Moved on(플랜 §1 Out 목록), 남은 틈(실행당 PR 하나, 알림 없음, Windows Job Object, 실제 GitHub 확인은 랩에서).
3. HARNESS: §4 Compiled 에 validators, §5 의 한국어 검사기가 지어진 대로, §8 `lado-policy/3`, §10 `LADO_SETUP_ALLOWS_PROTECTED` 를 표에 넣고 "Not built" 에서 지움, §12 8단계 화면, §14 표의 네 게이트 "Registered" 칸을 Step 8 로, §15 항목 5.
4. PRD: MERGE-001·VER-001·VER-002·VER-003·EVD-001·EVD-002·EXEC-004 의 LADO change 칸에 "Step 8: …" 한 문장씩, §5 상태 목록이 지은 것과 맞게.
5. ADR-0003: 끝에 "Amended 2026-10-27 (step 8)" 단락 — 상태는 LADO 의 저장소 클라이언트(저장된 토큰)로 게시, 병합 게이트 위치, 보호 안 된 저장소는 승인마다 확인.
6. `LADO.md`·`LADO.ko.md`: 8행을 "In progress"·"진행 중"으로(병합 뒤 메인 세션이 "Merged" 로 바꾼다).
7. 커밋 메시지 예: `docs(lado): record step 8 as built`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S19-docs.md` 로 전건 MET 를 확인한다.

## 필수 테스트

없음(문서만). 게이트가 문서의 핵심 문장을 확인한다.

## 수용 게이트

- [ ] S19-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S19-G1: TDD 에 8단계 기록
  CHECK: bash .claude/gate/lado-verification.sh has docs/LJY-PRD/TDD.md "As built in step 8" "0008_lado_verify.sql" "LADO_INTEGRATION_APPROVAL_REQUIRED" "lado-verify/1" "integration-approve"
  EXPECT: HAS_OK
- [ ] S19-G2: HARNESS 에 8단계 기록
  CHECK: bash .claude/gate/lado-verification.sh has docs/LJY-PRD/HARNESS.md "lado-policy/3" "LADO_SETUP_ALLOWS_PROTECTED" "As built in step 8"
  EXPECT: HAS_OK
- [ ] S19-G3: PRD·ADR-0003 에 8단계 기록
  CHECK: bash .claude/gate/lado-verification.sh has docs/LJY-PRD/PRD.md "Step 8:" && bash .claude/gate/lado-verification.sh has docs/LJY-PRD/adr/0003-enforcement-points.md "step 8"
  EXPECT: HAS_OK
- [ ] S19-G4: LADO.md·LADO.ko.md 8행 진행 중
  CHECK: bash .claude/gate/lado-verification.sh has LADO.md "In progress" && bash .claude/gate/lado-verification.sh has LADO.ko.md "진행 중"
  EXPECT: HAS_OK
- [ ] S19-G5: 이 단계는 코드를 바꾸지 않았다(백엔드·프런트엔드 소스가 S18 레인 병합 커밋과 같다)
  CHECK: bash -c 'base=$(git log -1 --format=%H --grep="merge the S18 lane"); [ -n "$base" ] && git diff --quiet "$base" HEAD -- backend frontend packages && echo DOCS_ONLY'
  EXPECT: DOCS_ONLY
- [ ] S19-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈 — 메인 세션이 할 것: CI 미러, 리뷰, 랩, 실제 GitHub 확인(외부 쓰기, 소유자 승인), 푸시·PR)
