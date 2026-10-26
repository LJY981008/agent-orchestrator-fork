# 2026-10-23 LADO 대기열·디스패처 — S13 문서: TDD·HARNESS·PRD·LADO 페이지

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 a~n 전부) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

6단계를 만든 대로 스펙에 적고, LADO 페이지에서 "개발자 설정으로만 켤 수 있다"는 안내를 앱 안의 스위치와 실행 설정으로 바꾼다. 다음 단계(7·8)가 읽을 기준 문서다.

## 범위

- 고친다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, `docs/LJY-PRD/PRD.md`, `LADO.md`, `LADO.ko.md`. 코드는 건드리지 않는다.
- 각 절에는 "As built in step 6" 문단을 더하는 꼴로(5단계 문서가 쓴 방식 — `TDD.md` §5.2 의 "As built in step 5" 참고), 기존 설계 문장은 틀린 것만 고친다.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 먼저 S01~S12 엔트리의 After·회고를 읽어 계획과 달라진 점을 모은다. 문서는 **실제로 만든 것**을 적는다(플랜과 다르면 엔트리를 따른다).
2. `TDD.md`:
   - §3.2: "As built in step 6" — 새 상태(queued, dispatching, running, failed, completed, 실행 뒤 closed_unmerged)와 순서, 이유 문자열, 6단계 전 승인은 `approved_before_queue` 로 Review required, `approved` 는 더 나오지 않음, completed 는 PR 머지(통합 승인은 8단계).
   - §4.2·§4.3: 세 테이블과 `lado_project_settings.max_concurrent`, CDC 종류 `lado_task`(대기열 행)·`lado_execution`.
   - §5.2: 승인 트랜잭션이 대기열 항목을 쓰고 이전 열린 항목을 superseded 로.
   - §5.3: "As built in step 6" — 검사 순서와 각 결과, 시도마다 새 client request id `lado:<entry>/<attempt>` 와 해시, 3번 시도·30 s·2 min, 이슈 읽기 실패·상한·메모리 대기는 시도가 아님, 전체·프로젝트 상한과 tight 임계값, 최대 실행 시간(기본 8시간), 베이스는 기록만(커밋 수)·`base_ref` 불일치는 Blocked, Chat 모드, 프롬프트 첫 문장, IssueContext 는 고정 스냅숏, 재시작 복구.
   - §6.2: 디스패치 허가(리스·시도·프로젝트·해시) as built. §6.4·§6.5: 턴별 상한(`default` 는 상한 위), config 옵션 거절, 실행 고정(`LADO_EXECUTION_FIXED`), 명시적 모드.
   - §9: 규칙 문구 주입 위치 — 실제로 들어간 자리(추가 섹션, 기밀 안내 뒤)와 client request id 로 찾기 때문에 복구·재개가 저장된 스냅숏을 쓴다는 점. "after AO's ## Project Rules" 문장을 실제에 맞게 고친다.
   - §10: 게이트 13(재시작) as built.
   - §13: 새 경로 세 개와 프로젝트 설정의 `maxConcurrent`, `AO_LADO_DEV` 문구 삭제(모드 변경은 앱에서, 끄기는 presence).
   - §14: 게이트 1·13·15·17·18 의 실제 테스트 이름.
   - §15 항목 6: "Built 2026-10-2x on `feat/lado-queue-dispatcher`" 와 소유자 결정(범위 전부, 베이스 기록만, 상한은 화면, 8시간), 다음 단계로 넘긴 것(도구 목록, 겹침 검사, PR 검증 상태).
3. `HARNESS.md`: §8 의 "fixed at queue admission" 을 실제(승인 때 고정, 대기열 항목이 가리키고 디스패치가 다시 비교)로, §12 의 "Show the active snapshot on every execution page" as built(세션 레일 LADO 패널·보드 카드 줄), §14 의 권한 상한 행(6단계가 실행에 적용).
4. `PRD.md`: EXEC-001~004, HAR-005 의 비고에 "Step 6: ..." 한 줄씩(무엇을 했고 무엇을 남겼는지).
5. `LADO.md`·`LADO.ko.md`:
   - 상태 표 6행 "Planned"·"예정" → "In progress"·"진행 중"(머지 뒤 별도 커밋이 Merged 로 바꾼다).
   - 소개의 규칙 항목: 워커가 규칙을 받는다(현재형).
   - "Until step 6 ... developer setting" 문단과 그 아래 개발자 설정·curl 안내를 앱 안내로 교체: 프로젝트 설정 → LADO 에서 켜고 끈다(끌 때 데스크톱 확인), 설정 → 실행에서 동시 실행·최대 실행 시간·메모리 대기. `AO_LADO_DEV` 언급을 지운다(`AO_LADO_DEV_PRESENCE` 개발 안내가 있으면 남긴다).
6. 커밋 메시지 예: `docs(lado): record step 6 in the specs and the LADO pages`.

## 마무리 (모든 단계 공통)

1. 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S13-docs.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- 없음(문서). 대신 아래 게이트가 문장을 확인하고, 링크 앵커(`#15-build-order-agreed-2026-10-19-by-dependency`)가 그대로인지 본다.

## 수용 게이트

- [ ] S13-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S13-G1: TDD 에 6단계 as built 문단과 새 경로·테스트
  CHECK: bash .claude/gate/lado-queue.sh has docs/LJY-PRD/TDD.md 'As built in step 6' '/lado/dispatch-settings' 'lado:<entry>/<attempt>' 'LADO_EXECUTION_FIXED' 'TestRecoveryLinksASpawnedSession' 'feat/lado-queue-dispatcher'
  EXPECT: HAS_OK
- [ ] S13-G2: HARNESS·PRD 갱신
  CHECK: bash .claude/gate/lado-queue.sh has docs/LJY-PRD/HARNESS.md 'As built in step 6' && bash .claude/gate/lado-queue.sh has docs/LJY-PRD/PRD.md 'Step 6:'
  EXPECT: HAS_OK
- [ ] S13-G3: LADO 페이지 6행이 진행 중, 개발 플래그 안내 삭제
  CHECK: bash .claude/gate/lado-queue.sh has LADO.md '| In progress |' && bash .claude/gate/lado-queue.sh has LADO.ko.md '| 진행 중 |' && bash .claude/gate/lado-queue.sh none LADO.md 'AO_LADO_DEV=on' 'There is no switch in the app yet' && bash .claude/gate/lado-queue.sh none LADO.ko.md 'AO_LADO_DEV=on'
  EXPECT: NONE_OK
- [ ] S13-G4: 상태 표 링크 앵커가 그대로
  CHECK: bash .claude/gate/lado-queue.sh has LADO.md '#15-build-order-agreed-2026-10-19-by-dependency' && bash .claude/gate/lado-queue.sh has docs/LJY-PRD/TDD.md '## 15. Build order (agreed 2026-10-19, by dependency)'
  EXPECT: HAS_OK
- [ ] S13-G5: 코드는 그대로(빌드·vet)
  CHECK: bash .claude/gate/lado-queue.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S13-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `2178d2fbc`(`docs(lado): record S12`), 트리 깨끗, `defs-unchanged` → `DEFS_UNCHANGED`.
- 게이트를 먼저 돌림(RED 확인): G1 `missing in docs/LJY-PRD/TDD.md: As built in step 6`, G2 `missing in docs/LJY-PRD/HARNESS.md: As built in step 6`·`missing in docs/LJY-PRD/PRD.md: Step 6:`, G3 `missing in LADO.md: | In progress |`·`AO_LADO_DEV=on` 이 `LADO.md:37`·`LADO.ko.md:37` 에 남음, G4 `HAS_OK`(앵커 그대로).
- 출발점: TDD 는 6단계를 설계 문장으로만 적었고 틀린 곳이 있었다 — §5.3 검사 4 "베이스가 움직이면 Review required"(실제는 기록만, `base_ref` 불일치는 Blocked), client request id `lado:<queue-entry-id>`(실제는 시도마다 `lado:<entry>/<attempt>`), 프롬프트 "계획 본문"(실제는 머리말 + 계획 파일 전체), IssueContext 를 AO 의 이슈 렌더 방식으로(실제는 LADO `issue.md` 와 같은 렌더), §9 "after AO's ## Project Rules"(실제는 기밀 안내 뒤의 추가 섹션), §9 도구 목록 적용(실제는 적용 안 함), §5.2 "fatal conflict 면 blocked_reason 으로 항목 생성"(실제는 항목은 그대로, 태스크가 스냅숏으로 Blocked), §6.1 인터페이스 메서드 1개(실제 3개), §3.2 Approved 상태(더는 안 나옴), §13 `AO_LADO_DEV=on` 문장. §1.2 의 "upstream 훅 전체 목록" 에 6단계가 손댄 upstream 파일(`agent_switching.go`·`interface_transition.go`·`SessionInspector.tsx`·`SessionsBoardAdapters.tsx`)이 없고, 턴 설정 clamp 를 `conversations.go` 에 두었다(실제는 chat `service.go`). HARNESS §8 "fixed at queue admission", PRD §5 의 Approved, LADO 페이지의 개발 플래그·curl 안내.
- 앞 단계 메모 중 이 단계 몫: S06(AO_LADO_DEV 흔적, 살아 있는 세션이면 승인 거절을 §5.2/§5.3 에), S07(베이스 비교 다섯 형태, 1분 대기, link 뒤 재확인, 오류 문장 규칙), S08(Tick 순서, 복구·마감·베이스 기록·메모리 표·모드 꺼짐 표시), S09(§13 경로·오류 코드, 설정 감사, 디스패처 시작 위치, `Branches` = 세션 매니저), S10(모드 끄기 채널, 렌더러 이벤트 규칙 → §4.3; ADR-0002 는 아래 회고), S11(설정 → 실행, 프로젝트 설정 → LADO), S12(§3.3·HARNESS §12, 패널 실제 자리는 `SessionInspector.tsx` 요약 탭 `context` 슬롯).

## After — 수행 후 실측

- 문서 커밋 `d1c535430` `docs(lado): record step 6 in the specs and the LADO pages` — 5 파일, +188 −93. 코드 변경 없음.
  - `TDD.md`: §1.1 렌더러 행(`RunsSection.tsx`), §1.2 훅 표(데몬 디스패처 시작·정지, `ResolveDefaultBranch`, `SwitchAgent`·`StartInterfaceTransition`, chat `SetTurnSettings`·`SetConfigOption`, `conversations.go` 행을 "아직 안 만듦"으로, 설정 카탈로그·대화상자·이벤트 전송·인스펙터·보드 카드·preload), §3.2 표의 틀린 행(Review required·Blocked 이유, Approved, Failed) + "As built in step 6"(`stateAt` 9단계 순서, `StateReason`, `plan_superseded` 는 항목에만, Completed = PR 머지), §3.3 레인(Plan 에 Failed, Queued 에서 Approved 삭제) + as built(레인에서 빠지는 조건, 둘째 줄, 태스크 뷰, 보드 카드 줄), §4.2 세 테이블·`max_concurrent`·sqlc "ambiguous"·`CAST AS TIMESTAMP` 함정, §4.3 `lado_task`(대기열)·`lado_execution` 트리거와 세션 FK, 렌더러의 세션 종료·PR 이벤트, §5.2 첫 문단(blocking 스냅숏) + as built(항목 쓰기·supersede, 재승인 규칙 `LADO_TASK_ALREADY_QUEUED`·`LADO_TASK_RUNNING`, 취소 killer, 앱 모드 스위치와 main 의 끄기 확인, 상한·설정 감사 `api`), §5.3 열 목록·검사 4·스폰 문단 수정 + as built(Pass·Admission·Waiting reasons·Checks 1-6·Spawn(프롬프트 첫 문장 그대로)·Link·Errors·Recovery·Maximum run time·Cancel·Daemon) + 알려진 틈 4개, §6.1 인터페이스 5메서드, §6.2 `lado:<entry>/<attempt>`·허가 조건, §6.4 as built(clamp·`LADO_APPROVAL_ABOVE_CEILING`·`LADO_EXECUTION_FIXED`·못 막는 경로), §6.5·§6.6, §7.3(기록하는 SHA 는 하나), §9 규칙 문구 자리(추가 섹션: 작업 공간 섹션 다음, `ao` CLI 안내 앞 — 기밀 안내 뒤)와 client request id 로 찾는 출처, 도구 목록 미적용, §10 Chat·재시작, §13 세 경로(`/lado/queue`·`/lado/executions/{sessionId}`·`/lado/dispatch-settings`)와 태스크 DTO·프로젝트 설정 `maxConcurrent`·오류 코드(`AO_LADO_DEV` 문장 삭제), §14 게이트 1·4·13·15·17·18 실제 테스트 이름, §15 3항(변수 삭제)·6항(Built 2026-10-23 on `feat/lado-queue-dispatcher`, 만든 것, 소유자 결정 4개, 넘긴 것).
  - `HARNESS.md`: §7 as built(디스패치 직전 재확인), §8 "fixed with the plan approval"(항목이 가리키고 디스패치가 다시 비교) + as built, §12 as built(세션 인스펙터 요약 탭의 LADO 실행 섹션, 보드 줄, 설정 → 실행, 프로젝트 설정 → LADO), §14 두 행(`plan.approval.required`·`agent.permission.ceiling`), §15 7·8항 테스트.
  - `PRD.md`: EXEC-001~004·HAR-005 비고에 "Step 6: …", §5 상태 목록(Approved 삭제, 시작 못 한 Failed).
  - `LADO.md`·`LADO.ko.md`: 6행 In progress·진행 중, 규칙 소개 현재형, "Until step 6 …"·`AO_LADO_DEV=on`·curl 안내 삭제 → 저장소 연결 → 프로젝트 설정 LADO 페이지에서 켜기(끌 때 데스크톱 확인) → … → 6번 디스패치 설명, 설정 → 실행 문단, `AO_LADO_DEV_PRESENCE` 안내는 남김(모드 끄기도 허용한다고 갱신), 알려진 한계 2개(Claude Code·Codex 만·Chat 고정, 베이스 이동은 기록만).
- 확인: 독립 사실 확인 서브에이전트(Sonnet) 1회 — 추가 줄 전부를 코드·S01~S12 기록과 대조, 인용 테스트 16개 존재·설명 일치. 지적 2개: (1) `manager.go:5692` 가 HEAD 와 다름 → TDD 머리말의 "Line numbers are at `55b02b848`" 규칙대로라 그대로 둠, (2) `conversations.go` 행이 만든 것처럼 읽힘 → "Not built yet:" 을 앞으로 옮겨 반영(커밋 전).
- 게이트(HEAD `d1c535430`): `GATES=7 MET=7 UNMET=0`. 이 엔트리 커밋 뒤 같은 명령으로 다시 돌려 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 범위대로 끝났다(다섯 파일, 코드 없음). 엔트리 절차에 없던 것: TDD §1.1·§1.2(훅 표가 "upstream 파일 전체 목록"이라 6단계 훅을 빼면 틀린 문서가 된다), §3.3(S12 메모), §6.1 인터페이스 코드 블록, §7.3 한 문단, PRD §5 상태 목록, HARNESS §7·§15. 모두 "틀린 문장만 고치고 as built 를 더함" 원칙 안이다.
- 엔트리 문장과 다르게 적은 것: 절차 2 §9 의 "기밀 안내 뒤" 는 맞지만 더 정확히 "작업 공간 섹션 다음, `ao` CLI 안내·산출물 섹션 앞의 추가 섹션"으로 적었다. 절차 3 의 "세션 레일 LADO 패널" 은 S12 결정대로 실제 자리(인스펙터 요약 탭 `context` 슬롯)로 적었다.
- 하지 않은 것: S10 메모의 ADR-0002 갱신 — 이 엔트리의 파일 범위 밖이다. ADR-0002 는 이미 "turning LADO mode off" 를 presence 행동으로 적고 있어 틀린 문장은 없다. 채널 세부는 TDD §5.2 에 적었다.
- 남은 틈(기록만): TDD §14 게이트 1 은 "디스패처 스폰을 실제 게이트로 통과시키는 통합 테스트가 없다"고 적었다(허가와 스폰 설정을 따로 고정). 게이트 18 의 워크트리 검사도 없음을 적었다.
- 독립 사실 확인이 약 6달러로 비쌌다(323k 토큰, 14분). 문서 단계의 대조는 범위를 좁혀(바뀐 절별 코드 파일 지정) 맡길 것.
- 다음 단계 메모(7·8단계, 머지 뒤):
  - 머지 뒤 별도 커밋이 `LADO.md`·`LADO.ko.md` 6행을 Merged·병합됨으로 바꾼다(엔트리 절차 5).
  - 7단계: HARNESS §8 as built 의 "Rule changes reach only later runs" 와 TDD §5.3 검사 3 이 셋업 항목·드리프트를 더할 자리다(HAR-005 비고에 "Setup items and drift join with step 7").
  - 8단계: TDD §3.2 의 Completed 는 지금 PR 머지만 본다 — 통합 승인과 Validating·Integration pending·Integrating 은 `domain/lado_task.go` `taskState` 에 들어간다. §7.3 의 로컬 브랜치 SHA·reflog 는 실행의 `dispatch_base_sha`(AO 가 해석한 ref 의 커밋) 옆에 더한다. 실행 상세(`GET /lado/executions/{sessionId}`)에 검사·근거가 붙는다(§13).
