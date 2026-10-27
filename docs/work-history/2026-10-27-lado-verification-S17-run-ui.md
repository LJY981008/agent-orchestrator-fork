# 2026-10-27 LADO 검증·통합 승인 — S17 화면: 실행 패널 검증·작업 창 하단·보드 줄

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 b·l·m·t, §17) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

PRD §6 "Review and evidence": 개발자가 한 화면에서 PR head 의 검증 항목, 검사 명령 로그, 인수 기준별 근거를 보고, 통합 대기면 데스크톱 확인으로 승인하고, 승인됐으면 병합한다. 작업 창 하단 상태 줄과 보드 세션 카드에도 새 상태가 보여, 사람 차례(통합 대기)를 놓치지 않는다.

## 범위

- 만든다: `frontend/src/renderer/components/lado/LadoVerificationSection.tsx`(+ `.test.tsx`).
- 바꾼다: `LadoExecutionPanel.tsx`(섹션 붙이기), `LadoTaskDialog.tsx`(`RunStatus`), `LadoSessionLine.tsx`, 각 테스트, 8개 로캘, `DESIGN.md`(결정 기록 한 행).
- 하지 않는다: 프로젝트 설정·규칙 화면(S18).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. **`DESIGN.md` 를 먼저 읽는다**(인스펙터 섹션, 점 색 의미, 조용한 버튼, 접는 목록, 고정폭 SHA, 문장형 대소문자). 비슷한 기존 화면: `LadoExecutionPanel.tsx`(InspectorSection, `<dl>`), `LadoTaskDialog.tsx` `RunStatus`, 7단계의 setup 목록. `components/ui/*` 의 shadcn 기본 요소를 쓴다. `<button>` 글꼴은 안쪽 `<span>` 에.
2. `LadoVerificationSection`(실행 패널, 실행 스냅숏 아래): 
   - 머리: "Verification" 과 head 7자(고정폭), 전체 결과 한 줄(점 + 상태 문구 + 사유 문구).
   - 항목 8줄: 점(pass = ready, wait = working, fail = attention, confirm = needs-you, info = idle), 항목 이름, 조용한 세부 한 줄(예: "3 of 3 required checks passed", "2 paths outside the plan", "Not protected: rulesets missing lado/integration"). `not_required` 줄은 흐리게.
   - 검사 명령: 이름·상태·걸린 시간, 접힌 로그(`fetchLadoCheckLog`, 고정폭, 높이 제한·스크롤), 조용한 "Run again"(`runLadoChecks`).
   - 근거: 접힌 목록 — 기준 번호·기준 글(계획의 `acceptanceCriteria`)·상태(연결됨·주장만·추론·오래됨)·ref.
   - 동작: Integration pending 이면 기본 버튼 "Approve integration…"(`approveLadoIntegration` — 데스크톱이 묻는다; 취소면 조용히), Integrating 이면 "Merge"(`mergeLadoPR` — 실패는 데몬 문구를 섹션 아래에). 진행 중 비활성.
3. `RunStatus`(작업 창 하단): 새 상태의 점·이름·사유, 같은 두 동작, `status_not_posted` 면 caution 색 한 줄.
4. `LadoSessionLine`: "LADO · plan r<n> · rules <7 hex>" 뒤에 " · " + 상태 문구, Validating·Integration pending·Integrating·승인 없는 Completed 일 때만(태스크 목록의 상태로). 줄은 여전히 하나, muted; Integration pending 만 needs-you 점.
5. 문구는 8개 로캘(용어집), coverage 테스트 통과. `DESIGN.md` 결정 기록에 한 행: "2026-10-27 | LADO fork: verification section, run status and session line …"(무엇을 어떻게 보이는지 한 단락).
6. 커밋 메시지 예: `feat(lado): show verification and integration on the run and task screens`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S17-run-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `LadoVerificationSection.test.tsx` — 항목 8줄과 점, `not_required` 흐림, 명령 로그 펼치기·다시 돌리기 호출, 근거 접힘·기준 상태, Integration pending 에서만 승인 버튼(브리지 호출, 취소 처리), Integrating 에서 병합 버튼(오류 표시), 기록 없음이면 "Checking the head…"
- `LadoTaskDialog.test.tsx` — 새 상태의 하단 줄과 두 동작, `status_not_posted` 줄
- `LadoSessionLine.test.tsx` — 상태 문구가 붙는 상태와 안 붙는 상태
- `LadoExecutionPanel.test.tsx` — 실행 패널에 섹션이 보이고, PR 없는 실행엔 없음
- `renderer-coverage.test.ts` — 하드코딩된 영어 없음

## 수용 게이트

- [ ] S17-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S17-G1: 화면 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh vitest src/renderer/components/lado/LadoVerificationSection.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoSessionLine.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S17-G2: 프런트엔드 typecheck(e2e 포함)
  CHECK: bash .claude/gate/lado-verification.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S17-G3: DESIGN.md 에 결정 기록 행
  CHECK: bash .claude/gate/lado-verification.sh has DESIGN.md "LADO fork: verification section"
  EXPECT: HAS_OK
- [ ] S17-G4: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 랩에서 볼 것(다크·한국어·좁은 폭), 컴포넌트 이름)
