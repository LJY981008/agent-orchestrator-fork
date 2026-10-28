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

- 시작 HEAD `a813b0e34`(`docs(lado): base the S19 docs-only gate on the S18 lane merge`), 브랜치 `feat/lado-verification`, 트리 깨끗. 메인 병합 기점 `1112300fc`(#14). Ubuntu, 문서만이라 Go·Node 를 돌리지 않음.
- 출발점(문서): TDD §1.1 어댑터 행이 "commit status file in `adapters/scm/github/`"(실제는 `adapters/tracker/github/lado_integration.go`), §1.2 `service/pr/action_service.go` 행만 있고 `prs.go`·`review/launcher.go` 행 없음, §3.1 승인·검사 실행·근거 행이 설계 그대로이고 `lado_verifications`·`lado_execution_reviews` 없음, §3.2 "Until step 8, Completed means that the PR merged", §7·§8 에 step 8 단락 없음(§7.3 끝 "come with step 8"), §8.2 "Job Object", §13 통합 승인 한 줄과 "Checks and evidence join in step 8", §14 관문 6~10 이 설계 문장, §15 항목 8 한 줄. HARNESS §4 "Validators come with step 8", §5·§10 "Not built … step 8", §8 에 step 8 단락 없음, §9.4 "reviewer … (step 8)", §14 네 게이트 "Step 8", §15 항목 5 "Step 8, with the validator". PRD 의 해당 7 행에 Step 8 문장 없음. ADR-0003 상태 줄에 step 8 없음. LADO.md·LADO.ko.md 8행 "Planned"·"예정", 소개의 "(coming)"·"(예정)".
- 코드 실측(기점 대비, 생성물 빼고): 업스트림 훅 `service/pr/action_service.go` +5, `httpd/controllers/prs.go` +5, `review/launcher.go` +6/−4, `httpd/api.go` +3/−2, `httpd/apispec/specgen/build.go` +1, `daemon/daemon.go` +8/−2, `frontend/src/preload.ts` +12/−2, 브리지 `lib/bridge.ts` +2·`test/setup.ts` +2·`e2e/support/fake-bridge.ts` +4, `sync-tray-catalog.mjs` +31, `DESIGN.md` 2행. 관문 테스트 이름 19개를 `grep` 으로 모두 확인(`TestFailingRequiredCheckKeepsValidatingDespiteReport` … `TestReviewerSystemPromptCarriesTheLadoSection`), `taskState` 순서는 `domain/lado_task.go:249-282` 에서 읽음.
- 앞 단계 메모 중 이 단계 몫: S06(짧은 제목 = 라틴 단어 3개 미만, 와일드카드 없는 패턴 = 폴더, 고정부 없는 글롭·글롭마다 이름, 대소문자 틈 → TDD §8·HARNESS §5·§14), S13(503 코드, 병합·닫힌 PR, 게이트의 PR URL 확인·기록 없는 세션, GitLab 게시 없음, dry run 의 저장된 head 비교 → TDD §7·§13), S14(HARNESS §10 "Not built" 고침, 결정 s), S15(§13 라우트·본문·코드, 다르게 한 것 1·3·5·6), S16(main 의 미리 거절, 확인 본문 줄, 명령·글롭의 main 쪽 검사 → ADR-0002·TDD §7.1·§13), S17(다르게 한 것 1~3·7·8, 항목 점 색 → TDD §3.2·§8, LADO.md), S18(화면 이름, 다르게 한 것 1~7 → TDD §7.1·§8.2, HARNESS §14, LADO.md), 그 밖 S01(sqlc 열 목록·트리거와 Down), S03(승인 트랜잭션의 기록 확인), S04(업그레이드·검사기 kind), S05(reflog 판정 모서리), S07(Windows 틈), S08(`integration_id`·GitHub Free 실측 없음), S09(`lado/integration` 행 제외·리뷰 판정), S10(근거 틈), S11(수정 요청·재게시), S12(`SetOnLadoModeOn` 늦은 연결) 의 "다르게 한 것"·남은 틈.

## After — 수행 후 실측

- 커밋: `746145f27` docs(lado): record step 8 as built(6 파일, 코드 없음) · 이 엔트리 커밋.
- 바꾼 것:
  - TDD: §1.1 어댑터 행 고침 + "As built in step 8" 파일 목록(ladoverify 19 파일과 그 곁), §1.2 훅 행(`api.go`, `specgen/build.go`, `daemon.go`, `action_service.go`, 새 행 `prs.go`·`review/launcher.go`, `preload.ts`, 브리지, `sync-tray-catalog.mjs`)에 줄 수(기점 `1112300fc` 대비 numstat)와 그 뜻 + 단락, §3.1(실행 행, 승인·검사 실행·근거 행을 지은 대로, 새 `lado_verifications`·`lado_execution_reviews` 행), §3.2(문장 고침 + `taskState` 순서 7개, 기록 = head 별 증거, 최신 PR 정의), §3.3(보드 줄·작업 창 줄·톤), §4.2(설정 열, 0008 표, Down·트리거 제약, sqlc 열 목록 교훈), §4.3(트리거 6개와 안 내는 것), §4.5(blob 에 로그·근거, 근거 파일 행), §7.1~§7.4·§8 머리·§8.1~§8.6 의 "As built in step 8"(+ 화면 단락), §9(리뷰어 행 나눔, 검사기·프롬프트·리뷰어 단락), §13(라우트 행 4 추가·4 고침, AO 병합 행, "Step 8's codes"), §14 관문 6~10, §15 항목 8(Built, 소유자 결정 넷, 결정 a–t 요지, 벗어난 점, Moved on, 소유자가 정할 것).
  - HARNESS: §4 Compiled, §5(한국어 검사기 단락 + validator 필드 문장), §8(`lado-policy/3` 단락), §9.4 "Not delivered"(리뷰어), §10(표에 `LADO_SETUP_ALLOWS_PROTECTED` 행, "Not built" 고침, 단락), §12(8단계 화면 4묶음), §14(네 게이트 Registered 칸, 한국어 규칙 행, 내장 8개·업그레이드 단락), §15 항목 5.
  - PRD: EXEC-004·MERGE-001·EVD-001·EVD-002·VER-001·VER-002·VER-003 의 "Step 8:" 문장, §5 상태 목록에 지은 대로.
  - ADR-0003: 상태 줄 + "Amended 2026-10-27 (step 8)"(상태 게시 클라이언트, 보호 안 된 저장소는 승인마다 확인, 병합 게이트 위치, 항목 3·7 의 확인 코드, 안 지은 것).
  - LADO.md·LADO.ko.md: 8행 "In progress"·"진행 중"(내용 칸도 넓힘), 소개 줄, 써 보기 7·8 과 프로젝트 화면 단락, 저장 위치, 알려진 한계 4줄. 한국어 화면 이름은 `ko.json` 값(검증, 통합 승인…, 병합, 다시 실행, 검사, 브랜치 보호, 다시 확인, 보호 경로)과 맞춤.
- 엔트리와 다르게 한 것과 이유:
  1. 엔트리 목록 밖의 절도 고쳤다 — 지은 것과 어긋난 문장이 있어서: TDD §1.1 어댑터 행(상태 파일 위치), §1.2 `specgen/build.go`(S15 의 한 줄), §3.1 실행 행, §4.2 설정 열 문장("build/test/lint commands"), §4.5, §7.4 한 줄, §8 머리의 루프 단락과 화면 단락(S17 메모), HARNESS §5·§9.4·§14 단락. PRD PRJ-003 행("build/test/lint commands")은 엔트리 목록 밖이라 두었다(회고).
  2. ADR-0002 는 고치지 않았다: S16 메모는 데스크톱 main 의 미리 거절을 ADR-0002 에 적으라 했지만 이 엔트리의 범위는 ADR-0003 뿐이다. 같은 내용을 TDD §7.1 "The desktop app"·§13 "Step 8's codes" 에 적었다.
  3. "내 결정 a–t" 는 항목별 나열 대신 §15 항목 8 의 한 단락 요지로, 세부는 §7·§8 각 "As built" 에 두었다(앞 단계 항목 7 의 분량에 맞춤).
  4. 사실 확인 서브에이전트(sonnet, 전경)가 배경 하위 에이전트 5개를 띄우고 결과 없이 돌아왔다. 예산(그 시점 남은 약 $11)을 지키려고 5개를 모두 중지했고(부분 기록에 모순 보고 없음, 약 $3.5 씀), 대신 각 단계 After·회고와 직접 실측(관문 테스트 이름 19개 grep, `taskState`, 루프·알림·재게시 상수, `record.go` 의 기준 브랜치 폴백, 내장 규칙 문구, `ko.json` 문구, 기점 대비 numstat)으로 문장을 확인했다.
- 확인한 명령과 결과:
  - `lado-verification.sh has` 6건(G1~G4 문자열) → 모두 `HAS_OK`; G5 명령 → `DOCS_ONLY`.
  - 바꾼 문서 5개의 표 열 수 검사(python, 머리 행과 같은지) → 어긋남 없음.
  - `grep '^func Test'` 로 인용한 테스트 함수 전부 존재 확인(`TestBaseItemCases` 하위 이름, `TestMergeGateNeedsAnApprovalForTheHead` 하위 이름, `TestIntegrationApprovalNeedsPresence` 주석까지 읽고 문장 고침).
- 게이트: 엔트리 커밋 뒤 `gate-check.sh --run` 으로 G0~G6 를 그 HEAD 에 기록한다.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 0~7 전부. 코드·테스트·생성물은 건드리지 않았다(G5). 체인 정의는 손대지 않았다.
- 남은 틈 — 메인 세션이 할 것:
  - **리뷰**: 이 단계의 문서 사실 확인은 서브에이전트 중지로 끝나지 않았다. TDD §7·§8·§13 과 HARNESS §8·§10·§14 의 숫자·코드·순서를 코드와 한 번 더 대조할 것(특히 §7.1 거절 순서, §8.2 워크트리 조건, §13 코드 목록).
  - **CI 미러**(전체 Go·프런트엔드·gitleaks), **랩**(다크 테마·좁은 폭·한국어, 실제 Electron 의 통합·검사 명령·보호 경로 확인 창, 검증 섹션의 긴 줄), **실제 GitHub 확인**(규칙셋 있는 저장소에서 `lado/integration` 게시와 병합 차단, GitHub Free 비공개 저장소의 규칙 응답 — 외부 쓰기라 소유자 승인 뒤), **푸시·PR**(`--repo LJY981008/agent-orchestrator-fork`).
  - 병합 뒤: LADO.md·LADO.ko.md 8행 "Merged"·"병합됨", 소개 줄의 "(step 8, in progress)"·"(8단계, 진행 중)" 지우기; 리뷰 수정이 생기면 TDD §15 항목 8 에 "Review fixes" 줄.
  - 문서 후속: ADR-0002 에 데스크톱 main 의 미리 거절과 통합 확인 본문(S16 메모), PRD PRJ-003 행의 "build/test/lint commands" → 검사 명령.
  - 소유자가 정할 것(TDD §15 항목 8 에 기록): 보호 글롭의 대소문자 무시, 맞는 보호 글롭 하나만 이름 대도 되는지, `integration_id` 가 붙은 `lado/integration` 규칙을 보호 안 됨으로 볼지.
- 다음 단계 메모: 체인의 마지막 단계 — 없음.
