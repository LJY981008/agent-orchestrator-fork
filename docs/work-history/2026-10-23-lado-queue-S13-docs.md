# 2026-10-23 LADO 대기열·디스패처 — S13 문서: TDD·HARNESS·PRD·LADO 페이지

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 a~n 전부) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

6단계를 만든 대로 스펙에 적고, LADO 페이지에서 "개발자 설정으로만 켤 수 있다"는 안내를 앱 안의 스위치와 실행 설정으로 바꾼다. 다음 단계(7·8)가 읽을 기준 문서다.

## 범위

- 고친다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, `docs/LJY-PRD/PRD.md`, `LADO.md`, `LADO.ko.md`. 코드는 건드리지 않는다.
- 각 절에는 "As built in step 6" 문단을 더하는 꼴로(5단계 문서가 쓴 방식 — `TDD.md` §5.2 의 "As built in step 5" 참고), 기존 설계 문장은 틀린 것만 고친다.

## 절차

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

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
