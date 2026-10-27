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

- 시작 HEAD `521ae6e17`(S16 엔트리 기록), 트리 깨끗, 브랜치 `feat/lado-verification`. 환경: Ubuntu, vitest v4.1.8. `tsc --noEmit` 한 번이 9~10분이라 첫 증분 실행은 590 s `timeout` 에 끊겼다(출력 없음) → 이후 `setsid nohup` 로 띄우고 `timeout 580 tail --pid=<pid> -f /dev/null` 로 전경에서 기다림.
- 출발점: 실행 패널은 스냅숏 섹션("LADO run") 하나뿐, PR 이 있어도 검증 표시 없음. 작업 창 `RunStatus` 는 `runLine` 조건이 running·completed·closed_unmerged·failed 만이라 새 상태(validating·integration_pending·integrating)에선 줄이 없었다. `runHolds` 도 새 상태를 빼서, 세션이 끝난 검증 중 태스크에서 Approve plan 이 켜졌다(데몬은 `ladotask/plans.go approvable` 에서 `LADO_TASK_RUNNING` 으로 거절). 보드 줄은 "LADO · plan r<n> · rules <hex>" 만. `lado.verify.*` 문구 없음.
- 앞 단계 메모 중 이 단계 몫: S16(훅 `useApproveLadoIntegration`·`useMergeLadoPR`·`useRunLadoChecks`·`useLadoCheckLog`, 취소면 null, confirm·pass·info 코드 문구는 S17 이 더함, 상태 이름·톤은 `ladoTaskStatus`/`ladoTaskReason`), S15(세부는 typed union, `verification` 은 현재 head 기록만, `integration` 은 현재 head 승인), S09(안 돈 명령 = `checkRunId`·`status` 빈 값, reviewer note `unknown reviewer <x>; AO chose` 를 접두로 번역, `required` 플래그), S10(ref 모양: `lado_check_run` key = 실행 id, `commit` sha = 전체, `claim` key = `check: …`), S06(커밋 결과 값 4개, `outsideCount` 로 "외 N개"), S12(`domain/lado_task.go taskState` 의 유도 순서).

## After — 수행 후 실측

- 커밋: `a5b6fdcd4` feat(lado): show verification and integration on the run and task screens — 18 files, +1953/−36(새 컴포넌트 568줄, 그 테스트 424줄, 로캘 8개 각 +82~91키).
- 바꾼 것:
  - 새 `components/lado/LadoVerificationSection.tsx`: 스냅숏 아래 별도 `InspectorSection` "Verification". 첫 줄 = 점 + 태스크 상태 이름 + 사유(`ladoReasonText`), 끝에 "Head <7자>"(mono, title 에 전체 SHA); PR 이 열렸고 현재 head 기록이 없으면 working 점 + "Checking the head…". 항목 8줄(데몬 순서 그대로): 점(pass ready, wait working, fail `--color-status-exited`, confirm needs-you, info idle) + 이름 + 세부 한 줄(truncate, title 에 전체). `not_required` 줄은 이름·세부 `text-passive`. 검사 명령 줄 아래 실행 목록(이름·상태·시간 `formatDuration`; "Output" 접힘 → `useLadoCheckLog` → `<pre>` mono `max-h-48 overflow-auto`, 빈 출력은 "No output") + 조용한 ghost "Run again"(PR 열린 동안만, 오류는 그 아래). 근거 줄 아래 "Evidence by criterion" 접힘 → 열 때만 `useLadoPlan(revisionId)` 로 `acceptanceCriteria` 글, 상태(Linked·Claimed only·Inferred·Stale), ref(CI check·Check command(실행 id → 명령 이름)·Commit 7자는 mono, Claim 은 워커 글 그대로). 동작 `LadoIntegrationAction`(export): Integration pending → "Approve integration…", Integrating → "Merge"(`integration.headSha`); 진행 중 비활성·"Approving…"/"Merging…", 오류는 버튼 아래 `ladoErrorText`.
  - `lado-task-status.ts`: `LadoRunState`·`ladoRunStatus(state, reason, t)`; `ladoTaskStatus` 가 PR 상태 다섯(실행 뒤 closed_unmerged 포함)을 이것으로 낸다(라벨·톤 그대로).
  - `LadoExecutionPanel.tsx`: 스냅숏 섹션 뒤에 섹션(프래그먼트), `projectId` 한 번 계산.
  - `LadoTaskDialog.tsx`: `runStates`(running·validating·integration_pending·integrating·completed)로 `runHolds`·`runLine`. `RunStatus` 의 사유는 `ladoTaskReason`, 줄 끝(`ml-auto`)에 footer-primary "Approve integration…" 또는 "Merge"(`pr.headSha`), `status_not_posted` 는 사유 대신 caution 색(`--color-status-in-review`) 한 줄 `lado-run-not-posted`.
  - `LadoSessionLine.tsx`: 태스크 목록 상태로 " · " + 단어(Validating·Integration pending·Integrating; 승인 없는 Completed 는 "Merged without an integration approval"), Integration pending 만 needs-you 점(`lado-session-line-dot`). 줄은 하나, muted, truncate 그대로.
  - 로캘 8개: `lado.verify.*` 82키(es·fr·pt-BR 는 `_many` 까지 91). `DESIGN.md` 결정 기록 한 행("2026-10-27 | LADO fork: verification section, run status and session line …").
- 엔트리와 다르게 한 것과 이유:
  1. 섹션의 상태는 태스크 목록이 아니라 실행 응답(`pr`·`verification`·`integration`)에서 `taskState` 와 같은 순서로 유도한다(`ladoRunState`): 한 응답이라야 상태·항목·병합 head 가 같은 head 를 말한다. 작업 창·보드 줄은 엔트리대로 태스크 목록 상태.
  2. fail 점은 엔트리의 "attention"(이 코드의 attention 톤 = needs-you 색)이 아니라 `--color-status-exited`(DESIGN §5 Failure): 같은 색이면 fail 과 confirm 을 가를 수 없고, fail 은 워커가 고칠 일(LADO 가 수정 요청), confirm 만 사람 차례다.
  3. 승인 없는 Completed 의 보드 줄 단어는 "Completed" 가 아니라 사유 문구: 카드 자체 상태가 이미 merged 를 보이므로 "Completed" 는 새 정보가 없다.
  4. "Run again" 은 명령 줄 머리 끝이 아니라 실행 목록 아래(조용한 ghost): 좁은 인스펙터에서 오류 문구가 항목 이름을 밀지 않게.
  5. `runHolds` 에 새 상태 셋 추가(`RunStatus` 곁의 한 줄): 데몬이 그 상태에서 계획 승인을 거절하는데 화면은 세션이 끝난 경우 버튼을 켰다.
  6. 근거의 계획 글은 목록을 열 때만 읽는다(`useLadoPlan(open ? id : null)`).
  7. 섹션은 merged·closed PR 에도 남는다(Completed / Closed without merge, 동작·Run again 없음) — 엔트리는 "PR 없는 실행엔 없음"만 정한다.
  8. 승인 head 의 상태 게시가 `failed` 면 섹션 첫 줄 아래 호스트 오류(`statusError`) 한 줄(muted, truncate, title).
- 테스트 순서: 컴포넌트·문구 키를 먼저 만들고 테스트를 썼다(새 키가 typecheck 에 걸려 테스트만 먼저 돌릴 수 없음). 첫 실행에서 `nameList` 가 `count` 를 넘겨 "외 N개"의 `{{rest}}` 가 비는 버그를 테스트가 잡았다 → 고침.
- 확인한 명령과 결과: vitest 필수 5파일 + `lado-task-status.test.ts` + `i18n/instance.test.ts`(로캘 키·변수·복수형) 79 ok; 넓게 `components/lado src/renderer/i18n src/renderer/lib src/renderer/hooks SessionInspector.test.tsx` 204파일 2379 ok, `components/SessionsBoard* components/SessionInspector*` 5파일 244 ok; 마지막 수정(Run again 이동) 뒤 `components/lado src/renderer/i18n` 21파일 189 ok. `tsc --noEmit --incremental`(scratchpad buildinfo) rc 0 두 번(두 번째 3분), `tsc -p tsconfig.e2e.json` rc 0(9 s). Go 변경 없음.
- 게이트: HEAD `a5b6fdcd4` 에서 `--only` 로 하나씩 S17-G0·G1·G3·G4 MET, G2 는 `setsid nohup` + `tail --pid` 대기로 MET(약 10분). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 절차 0~6 전부, 필수 테스트 5파일(새 섹션 12개, 작업 창 +4, 세션 줄 +1, 패널 +1). 프로젝트 설정·규칙 화면(S18)은 손대지 않음.
- 남은 틈:
  - 실제 렌더러(랩)에서 보지 않았다 — 다크·라이트, 한국어의 긴 세부 줄, 좁은 인스펙터 폭, 로그 `<pre>` 스크롤, 작업 창 하단에서 버튼이 줄바꿈되는 모양. 메인 세션 랩 몫.
  - 섹션 상태는 렌더러가 실행 응답에서 유도한다(다르게 한 것 1) — 데몬 `taskState` 의 순서가 바뀌면 `ladoRunState` 도 같이 바꿔야 한다(PR 이 있으면 오지 않는 timed-out 분기는 뺐다).
  - 브라우저(dev) 경로의 승인은 S16 대로 wait·fail 을 미리 거절하지 않고 데몬의 409 를 버튼 아래에 보인다.
  - en 외 7개 로캘은 원어민 검토 없음; ko 는 자리표시자 바로 뒤 조사를 피해 "…: {{branch}}" 꼴로 썼다.
  - "Run again" 은 명령 행이 있을 때만 보인다(명령 없는 프로젝트엔 없음); 실행 중인 명령은 다시 돌지 않는다(S15 동작).
- 다음 단계 메모:
  - **S18(프로젝트·규칙 화면)**: 상태 이름·톤은 `ladoRunStatus`/`ladoTaskStatus`, 점 클래스 `ladoTaskToneDotClass`(attention = needs-you 색); 실패 색은 `bg-[var(--color-status-exited)]`(이 단계 항목 점), caution 글은 `text-[var(--color-status-in-review)]`. DESIGN.md 결정 기록의 S18 행은 이 행 뒤에.
  - **S19(문서)**: 다르게 한 것 1~3·7·8 과 항목 점 색을 TDD §3.2·§8 의 화면 설명, LADO.md·LADO.ko.md 에.
  - **새 이름**(뒤 단계 충돌 주의): 컴포넌트 `LadoVerificationSection`, `LadoIntegrationAction`(export; props `projectId, sessionId, action, pr, headSha?, variant?, className?`); 파일 안 `ladoRunState`, `VerifyItemRow`, `itemDetail`, `nameList`, `RunAgain`, `CommandRuns`, `CommandRun`, `EvidenceList`, `EvidenceRefValue`, `refValue`, 상수 `REVIEW_ROUNDS`·`LISTED`·`kindKeys`·`outcomeDotClass`·`runStatusKeys`·`evidenceStateKeys`·`refKindKeys`; `lado-task-status.ts` `LadoRunState`·`ladoRunStatus`; `LadoTaskDialog.tsx` `runStates`; `LadoSessionLine.tsx` `runWord`. testid `lado-verification`, `lado-verify-state`, `lado-verify-items`, `lado-verify-item`(data-kind·data-outcome), `lado-verify-commands`, `lado-verify-command`, `lado-verify-log`, `lado-verify-evidence`, `lado-verify-criterion`(data-state), `lado-integration-error`, `lado-run-not-posted`, `lado-session-line-dot`.
  - **문구 키**: `lado.verify.{title, head, checking, item.*(8), notRequired, more, failed, checks.*, commands.*, commits.*, paths.*, base.*, protection.*, review.*, evidence.*(state.* 포함), ref.*, run.*, runAgain, runningAgain, approve, approving, merge, merging}`.
  - **typecheck 게이트**: 전체 `tsc --noEmit` 이 약 10분이라 G2 는 `setsid nohup bash -c "bash .claude/hooks/lib/gate-check.sh --run <entry> --only <G> > <log> 2>&1; echo \$? > <rc>" &` 로 띄우고 `timeout 580 tail --pid=<pid> -f /dev/null` 를 끝날 때까지 반복(전경 `sleep` 은 막힘, GATE_TIMEOUT=1800 은 환경에 있음).
  - **랩에서 볼 것**: 다크·한국어·좁은 폭의 Verification 섹션(세부 truncate, Head 위치, 근거 목록), 작업 창 하단 Integration pending 줄의 버튼, 보드 줄의 needs-you 점 정렬.
