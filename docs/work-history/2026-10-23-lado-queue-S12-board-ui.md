# 2026-10-23 LADO 대기열·디스패처 — S12 레인·태스크 뷰·보드 카드·세션 LADO 패널

> 체인 `docs/chains/lado-queue.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 k, §10 레인·태스크 뷰·보드 카드·세션 뷰) · 브랜치 `feat/lado-queue-dispatcher`

## 의도/목적 — 왜 이 작업을 하나

대기열과 실행을 보이게 한다: 레인이 새 상태와 이유를 보여 주고, 태스크 뷰는 디스패치 뒤 세션으로 이어지며, 실행 세션의 보드 카드에 LADO 한 줄, 세션 화면 오른쪽 레일에 그 실행의 스냅숏(HARNESS §12 "모든 실행 화면에 활성 스냅숏")을 띄운다.

## 범위

- 고친다: `frontend/src/renderer/components/lado/LadoLanes.tsx`(배치·상태 줄·이유 줄), `LadoTaskDialog.tsx`(디스패치 뒤 상태 줄과 세션 링크, failed 재승인), `components/SessionsBoardAdapters.tsx`(실행 세션 카드의 LADO 줄 — `footer` 또는 `artifacts` 슬롯; product-ui 는 고치지 않는 것이 목표), `components/SessionInspectorRail.tsx`(LADO 패널 자리 — 업스트림 파일이므로 몇 줄 훅), 8개 로캘 파일.
- 만든다: `components/lado/LadoExecutionPanel.tsx`(+ 테스트), 필요하면 `components/lado/LadoSessionLine.tsx`(+ 테스트).
- 하지 않는다: 설정 화면(S11), 백엔드.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. **`DESIGN.md` 를 먼저 읽는다**(상태 점·톤, 카드 상태 줄 규칙, 인스펙터 레일 구성). S11 의 결정 로그 행 아래에 이 화면들의 행을 하나 더한다(`| 2026-10-23 | LADO fork: queue states, run line and run panel | ... |`).
2. 레인: Plan 레인 = draft·planned·review_required·blocked·failed(failed 는 attention 톤, 이유 문장), Queued 레인 = queued·dispatching(dispatching 은 진행 톤). 카드 둘째 줄에 이유: 대기 — 빈 자리(`slot_global`·`slot_project`), 메모리, 이슈 읽기, 재시도(다음 시도 시각 상대 표시), LADO 모드 꺼짐; "시도 n/3"(시도가 1 이상일 때). running·completed·closed_unmerged 태스크는 레인에서 빠진다(취소된 것처럼 서버가 걸러내지 않으므로 렌더러가 거른다).
3. 태스크 뷰: 실행이 있으면 바닥줄 위에 상태 줄("실행 중 · 세션 열기" 링크 — 세션 라우트로 이동, PR 이 있으면 PR 링크), failed 면 이유와 함께 "계획 승인"이 다시 켜진다(review_required 와 같은 길 — `canApprove` 규칙 갱신). queued·dispatching 이면 승인 버튼 대신 상태 표시.
4. 보드 카드: 보드가 이미 불러오는 `useLadoTasks(projectId)` 에서 `execution.sessionId → 태스크` 맵을 만들어, 그 세션 카드에 "LADO · 계획 r{n} · 규칙 {sha 앞 7자}" 한 줄(muted, mono 해시). LADO 모드가 아닌 프로젝트는 아무것도 하지 않는다.
5. 세션 LADO 패널: `GET /lado/executions/{sessionId}`(404 면 패널 없음). 에이전트와 모드, 권한 상한, 규칙 이름(내장 규칙은 `ladoRuleName` 으로 번역), 워커에게 간 규칙 문구(접힘), 계획 리비전·승인 시각, "베이스가 n커밋 앞섬"(0 이거나 모르면 숨김), 마감(남은 시간 또는 "시간 초과로 종료됨"). 인스펙터 레일의 기존 섹션 꼴을 따르고, 레일 파일에는 패널을 끼우는 몇 줄만.
6. 문구 8개 로캘 실제 번역(한국어 용어집, 조사 앞 변수 금지).
7. 커밋 메시지 예: `feat(lado): queue states on the board, run line on session cards and the run panel`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-23-lado-queue-S12-board-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/components/lado/LadoLanes.test.tsx` — 새 상태의 레인 배치, 이유 줄(대기 이유별·시도 n/3), 실행된 태스크는 레인에 없음
- `src/renderer/components/lado/LadoTaskDialog.test.tsx` — 실행 중 상태 줄과 세션 링크, failed 재승인 가능, queued 는 승인 버튼 없음
- `src/renderer/components/lado/LadoExecutionPanel.test.tsx` — 패널 필드, 404 면 렌더 없음, 시간 초과 표시, 내장 규칙 이름 번역
- 보드 카드 LADO 줄 테스트(`LadoSessionLine.test.tsx` 또는 `SessionsBoardAdapters` 테스트) — 실행 세션에만 줄이 붙는다

## 수용 게이트

- [ ] S12-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S12-G1: 레인·태스크 뷰·패널 테스트 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx
  EXPECT: VITEST_OK
- [ ] S12-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S12-G3: DESIGN.md 결정 로그 행
  CHECK: bash .claude/gate/lado-queue.sh has DESIGN.md 'LADO fork: queue states, run line and run panel'
  EXPECT: HAS_OK
- [ ] S12-G4: 업스트림 화면 파일 훅이 작다
  CHECK: bash .claude/gate/lado-queue.sh diff-budget 40 frontend/src/renderer/components/SessionInspectorRail.tsx frontend/src/renderer/components/SessionsBoardAdapters.tsx
  EXPECT: BUDGET_OK
- [ ] S12-G5: 렌더러 전체 테스트
  CHECK: cd frontend && npx vitest run && echo S12_VITEST_ALL_OK
  EXPECT: S12_VITEST_ALL_OK
- [ ] S12-G6: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S12-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
