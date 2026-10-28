# 2026-10-27 LADO 검증·통합 승인 — S16 데스크톱 확인 채널·렌더러 데이터 층

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 e·m·o, §16, §17 데이터) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

ADR-0002: 통합 승인·검사 명령 변경·보호 경로 변경은 Electron main 이 대상을 데몬에서 직접 읽고 네이티브 확인 창으로 묻고 서명해야만 된다 — 렌더러는 무엇을 바꿀지 이름만 댄다. 이 단계는 main 의 채널 두 개(+ 기존 규칙 채널 확장), preload·브리지, main 카탈로그 문구, 렌더러의 데이터 함수·훅·오류 문구·상태 문구를 만든다. S15 의 새 enum 으로 빨개진 렌더러 typecheck 를 여기서 초록으로 돌린다.

## 범위

- 바꾼다: `frontend/src/main/lado-presence.ts`(+ `.test.ts`), `frontend/src/shared/lado-presence.ts`, `frontend/src/preload.ts`(작게), `frontend/src/renderer/lib/bridge.ts`, `frontend/src/renderer/test/setup.ts`, `frontend/e2e/support/fake-bridge.ts`, `frontend/scripts/sync-tray-catalog.mjs`(KEYS) 와 생성된 카탈로그(`npm` 스크립트로), `frontend/src/renderer/lib/lado.ts`, `hooks/useLado.ts`, `lib/lado-errors.ts`, `components/lado/lado-task-status.ts`, 8개 로캘.
- 하지 않는다: 화면 컴포넌트(S17·S18).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다). S15 회고의 typecheck 오류 목록부터.
1. 참고: `lado-presence.ts` 의 `approvePlan`(신뢰하는 sender 확인 → id 형식 → 대상 GET → 미리 거절 → 확인 → 서명 POST), `changeSetup`(dry run 을 main 이 보냄), `confirm`·`signed`·`callDaemon`·`oneLine`·`excerpt`(줄마다 정리 후 `\n` 으로 잇기), `sync-tray-catalog.mjs` 와 `tray-catalog.test.ts`.
2. 채널(플랜 §16):
   - `lado:presence:approveIntegration(sessionId)`: 세션 id 형식 확인 → `GET /lado/executions/{id}` → dry run `POST …/integration-approve {dryRun:true, prUrl, headSha}`(실행 응답의 PR·head) → outcome wait·fail 이면 묻지 않고 실패(사유 코드), 이미 승인이면 실패 → 확인 창: 제목 "Approve the integration of PR #<n> into <branch>?", 본문 줄: 태스크 제목, head 12자, 항목마다 한 줄(통과 수, confirm 코드마다 그 수와 예시 셋까지 — 계획 밖 경로, 애매한 커밋, 기준 브랜치 움직임 항목, 리뷰 결론 없음, 테스트 없음), 다음에 일어날 일(GitHub 상태 게시, 또는 "This repository is not protected: LADO only detects"). 확인 → `{prUrl, headSha, verificationSha256, acknowledge}`(dry run 의 값) 서명 POST.
   - `lado:presence:changeProjectChecks(projectId, commands)`: id 확인 → 설정 GET → 더해지는·바뀌는·지워지는 명령 줄(각 300자, 정리) → 확인 → `PUT …/settings {checkCommands}` 서명.
   - `lado:presence:changeRule` 확장: `paths.protected` 와 `globs` 를 받으면 규칙 GET → 더해지고 빠지는 경로 → 확인 → `PATCH /lado/rules/paths.protected {baseVersion, paths:{globs}}` 서명.
   - 문구는 main 카탈로그 키로(`lado.presence.integration.*`, `lado.presence.checks.*`, `lado.presence.paths.*`) 8개 로캘에, KEYS 에 등록, 생성물 다시 만들기.
3. preload·브리지: `aoBridge.lado.presence.approveIntegration(sessionId)`, `changeProjectChecks(projectId, commands)`(그리고 `changeRule` 의 새 인자), 세 스텁 파일.
4. 렌더러 데이터(`lib/lado.ts`): 스키마 타입(`schema.ts`), `approveLadoIntegration(sessionId)`(브리지 없으면 dev presence 직접 호출 — 기존 패턴), `runLadoChecks(sessionId)`, `fetchLadoCheckLog(sessionId, runId)`, `fetchLadoProtection(projectId, refresh)`, `updateLadoProjectChecks(projectId, commands)`(브리지), `updateLadoCheckTimeout(projectId, minutes)`, `mergeLadoPR(pr)`(AO 의 `POST /api/v1/prs/{id}/merge` 를 `{prUrl, expectedHeadSha}` 로 — 기존 병합 호출이 있으면 그것), 보호 경로 변경. 훅(`useLado.ts`): `useLadoProtection`, 실행 쿼리는 기존 것을 넓힘, 변경 뒤 무효화 키.
5. 문구: `lado-errors.ts` 에 새 코드(플랜 §15)와 상태 사유, 설정 충돌 `LADO_SETUP_ALLOWS_PROTECTED`; `lado-task-status.ts` 에 새 상태의 이름·색(Validating = working, Integration pending = needs-you, Integrating = ready, Completed + `merged_without_approval` = attention). 한국어는 용어집, 자리표시자 바로 뒤 조사 금지.
6. `preload.ts` 편집은 작게(G6).
7. 커밋 메시지 예: `feat(lado): desktop confirmations for integration, check commands and protected paths`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S16-client-presence.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `frontend/src/main/lado-presence.test.ts` — "approving an integration"(대상을 main 이 읽음, wait·fail 이면 묻지 않음, 확인 본문 줄, 서명 본문이 dry run 값, 취소면 아무것도 안 보냄), "changing check commands"(바뀐 명령 줄, 서명 PUT), "changing protected paths"(더한·뺀 경로, 서명 PATCH)
- `frontend/src/renderer/lib/lado.test.ts` 또는 새 `lado-verify.test.ts` — 데이터 함수가 브리지·라우트를 맞게 부름(dev 경로 포함)
- `frontend/src/renderer/lib/lado-errors.test.ts` — 새 코드·사유가 한국어·영어로
- `frontend/src/renderer/components/lado/lado-task-status.test.ts` — 새 상태의 이름·색
- `frontend/src/main/tray-catalog.test.ts` — 카탈로그와 KEYS 가 맞음

## 수용 게이트

- [ ] S16-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S16-G1: main 확인 채널 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh vitest src/main/lado-presence.test.ts src/main/tray-catalog.test.ts
  EXPECT: VITEST_OK
- [ ] S16-G2: 렌더러 데이터·문구 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh vitest src/renderer/lib/lado-errors.test.ts src/renderer/components/lado/lado-task-status.test.ts
  EXPECT: VITEST_OK
- [ ] S16-G3: 새 확인 문구 키가 8개 로캘에
  CHECK: bash .claude/gate/lado-verification.sh locales lado.presence.integration.title lado.presence.checks.title lado.presence.paths.title
  EXPECT: LOCALES_OK
- [ ] S16-G4: 프런트엔드 typecheck(e2e 포함)
  CHECK: bash .claude/gate/lado-verification.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S16-G5: i18n coverage 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh vitest src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S16-G6: preload.ts 편집은 작다
  CHECK: bash .claude/gate/lado-verification.sh diff-budget 20 frontend/src/preload.ts
  EXPECT: BUDGET_OK
- [ ] S16-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `6334176de`(브랜치 `feat/lado-verification`, S15 엔트리 커밋 뒤), 트리 깨끗. Ubuntu, node v24.21.0, 체인 무인 세션(lane-S16).
- 출발점 실측:
  - presence 채널 6개(`approvePlan`, 천장 전용 `changeRule`, `setLadoMode`, `adoptSetup`, `restoreSetup`, `changeSetup`). `changeRule` 은 `agent.permission.ceiling` 외 규칙을 `LADO_RULE_CEILING_INVALID` 로 거부. main 카탈로그 KEYS 61개, `lado.presence.integration|checks|paths.*` 없음.
  - 렌더러: `lib/lado.ts` 에 검증·통합·보호·검사 함수 없음, `useLado.ts` 에 해당 훅 없음, `lado-task-status.ts` 에 새 세 상태 없음(기본 분기로 Draft 처럼 보임), `lado-task-status.test.ts` 없음, `lado-errors.ts` 에 step 8 코드·사유·`LADO_SETUP_ALLOWS_PROTECTED` 없음.
  - typecheck: 시작 때 다시 재지 않았다(이 머신에서 `tsc --noEmit` 한 번이 약 9분 — 처음 띄운 실행은 편집이 시작된 뒤라 멈춤). S15 회고의 목록(8개: `lado-errors.ts(424)` 사유 `Record` + 설정 fixture 7곳, e2e 0개)을 출발점으로 썼고, 고친 줄이 그 목록과 같았다.
  - diff-budget: `preload.ts` 0/20.
- 앞 단계 메모 중 이 단계 몫: S02(`lado.error.taskRunning` 문구), S04(`changeRule` 이 `PATCH {"baseVersion": n, "paths": {"globs": [...]}}` 에 서명, `LADO_PROTECTED_PATHS_INVALID` 문구; 내장 이름 표는 S18), S06(커밋 결과 값, `outsideCount` 로 "외 N개"), S07(검사 명령 오류 세부 `{index, field, reason}`), S13·S15(dry run 의 `verification.prUrl`·`headSha`·`verificationSha256`·`confirm` 으로 서명, 라우트별 오류 코드, 새 스키마 이름, typecheck 목록), S14(`LADO_SETUP_ALLOWS_PROTECTED` 문구·`commands` 세부). S11 의 수정 요청 문구는 S17 몫이라 두었다.

## After — 수행 후 실측

- 커밋: `3b4da3c83` feat(lado): desktop confirmations for integration, check commands and protected paths · 이 엔트리 커밋.
- 바꾼 것:
  - `shared/lado-presence.ts`: 채널 `lado:presence:approveIntegration`·`lado:presence:changeProjectChecks`, 타입 `LadoPresenceIntegrationRequest{sessionId}`, `LadoPresenceIntegration`(= `LadoIntegrationApproveResponse`), `LadoPresenceIntegrationResult`(`approved` + `integration`), `LadoPresenceCheckCommand`, `LadoPresenceChecksRequest{projectId, commands}`, `LadoPresenceChecksResult`(= 모드 결과, `settings`), `LadoPresenceRulePaths`; `LadoPresenceRuleChangeRequest` 는 `ceiling` 또는 `paths` 의 합집합.
  - `main/lado-presence.ts`(+약 480줄): `approveIntegration`(신뢰 sender → 세션 id → `GET /lado/executions/{id}` → 미리 거절 → dry run `POST …/integration-approve {prUrl, headSha, dryRun:true}`(서명 없음, 60 s) → wait·fail 이면 묻지 않고 `LADO_INTEGRATION_NOT_READY {reasons}` → 확인 → `{prUrl, headSha, verificationSha256, acknowledge}` 서명 POST), `changeProjectChecks`(id → 명령 검사 → 설정 GET → 같으면 묻지 않음 → 줄 → 확인 → `PUT …/settings {checkCommands}` 서명), `changeRule` 의 `paths.protected` 분기 `changeProtectedPaths`(글롭 검사·trim → 규칙 GET → 같으면 묻지 않음 → 더한·뺀 줄 → 확인 → `PATCH /lado/rules/paths.protected {baseVersion, paths:{globs}}` 서명). `excerpt(value, max)` 로 넓힘.
  - 카탈로그: `sync-tray-catalog.mjs` KEYS +29(`lado.presence.integration.*` 19, `checks.*` 6, `paths.*` 4), `tray-catalog.generated.ts` 다시 생성.
  - `preload.ts` +12/−2(14/20): `approveIntegration(sessionId)`, `changeProjectChecks(projectId, commands)`, `changeRule(ruleId, change: LadoCeilingMode | {globs})`. 스텁: `lib/bridge.ts`, `test/setup.ts`, `e2e/support/fake-bridge.ts`(2곳).
  - `lib/lado.ts`: 타입 `LadoVerification`, `LadoVerifyItem`, `LadoVerifyItemDetails`, `LadoVerifyKind`, `LadoVerifyOutcome`, `LadoVerifyCode`, `LadoVerifyCommandRun`, `LadoEvidenceCriterion`, `LadoIntegrationApproval`, `LadoIntegrationResult`, `LadoRunChecksResult`, `LadoProjectProtection`, `LadoBranchProtection`, `LadoCheckCommand`; 상수 `LADO_PROTECTED_PATHS_RULE_ID`; 키 `ladoExecutionQueryRoot`, `ladoCheckLogQueryKey`, `ladoProtectionQueryKey`; 함수 `approveLadoIntegration`(브리지, 없으면 실행 GET → dry run → 그 값으로 POST), `runLadoChecks`, `fetchLadoCheckLog`(`parseAs: "text"`), `fetchLadoProtection(projectId, refresh)`, `updateLadoProjectChecks`(브리지, 없으면 PUT), `updateLadoCheckTimeout`, `mergeLadoPR({number, url, headSha})`(`POST /api/v1/prs/{id}/merge {prUrl, expectedHeadSha}`), `changeLadoProtectedPaths(globs)`(브리지, 없으면 규칙 GET → PATCH).
  - `hooks/useLado.ts`: `useUpdateLadoProjectChecks`, `useUpdateLadoCheckTimeout`, `useLadoProtection`, `useRefreshLadoProtection`, `useLadoCheckLog`, `useApproveLadoIntegration`, `useRunLadoChecks`, `useMergeLadoPR`, `useChangeLadoProtectedPaths`. 무효화: 승인·병합·다시 돌리기 → 실행·프로젝트 태스크·태스크 뷰 루트; 명령 변경 → 설정 캐시 갱신 + 실행 루트·태스크; 보호 다시 읽기 → 보호 캐시 + 실행 루트; 보호 경로 → `invalidateLadoRuleQueries`.
  - `lib/lado-errors.ts`: 코드 14개(아래 회고), 사유 +14(typecheck 오류 해소), 충돌 `LADO_SETUP_ALLOWS_PROTECTED`(`commands` 세부), `ladoCheckCommandProblemText`, `ladoProtectedPathProblemText`; `errorKey` 반환형을 `MessageKey` 로.
  - `lado-task-status.ts`: Validating = working, Integration pending = attention(needs-you 색), Integrating = ready, Completed + `merged_without_approval` = attention; 둘째 줄은 Validating·Integrating 의 사유와 승인 없는 병합.
  - 로캘 8개: 키 +72(main 29 + 렌더러 43), `lado.error.taskRunning` 문구를 새 상태까지로.
  - 기존 테스트: 설정 fixture 7곳에 `checkCommands: []`, `checkTimeoutMinutes: 30`(S15 목록), `LadoTaskDialog.test.tsx` 의 `taskRunning` 기대 문구 1곳.
- 엔트리와 다르게 한 것과 이유:
  1. 세션 id 형식은 AO 프로젝트 id 알파벳(`SESSION_ID = PROJECT_ID`, `..` 거부)을 쓰고, 어긋나면 main 코드 `LADO_SESSION_ID_INVALID`(렌더러는 `executionNotFound` 문구).
  2. dry run 전에도 묻지 않고 거절한다(데몬도 거절하는 것): PR 없음·병합·닫힘 → `LADO_EXECUTION_NO_PR {sessionId}`, head 를 아직 모름 → `LADO_INTEGRATION_NOT_READY {reasons:["verifying"]}`, 실행의 `integration` 이 그 head → `LADO_INTEGRATION_ALREADY_APPROVED`. 다른 PR·head 에 대한 dry run 답은 읽을 수 없음으로 거절. `reasons` 는 wait·fail 항목 코드(항목 순서, 중복 없음 — 데몬과 같은 모양).
  3. 확인 본문: "Passed: N of M verification items"(M 은 info 가 아닌 항목 수), confirm 코드마다 한 줄 — `outside_plan`(`outsideCount`), `commit_messages_unclear`(unclear·body_unclear 커밋 + 애매한 PR 제목), `base_moved`(커밋 7자 + 제목), `reflog_off`, `already_on_base`, `unprotected`(빠진 부분), `protection_unknown`(오류), `unprotected_gitlab`, `review_inconclusive`(회차), `no_tests`, 모르는 코드는 "Also to confirm: <code>". 예시는 셋까지, 각 120자, 더 있으면 "…". 다음 일: GitHub 면 상태 게시 줄, 보호가 pass 가 아니면 "This repository is not protected: LADO only detects a merge without approval.", GitLab 은 뒤의 줄만. 수는 괄호에 넣어 복수형을 피했다(main 의 `format` 은 복수형이 없음).
  4. 검사 명령: main 이 `ValidateLadoCheckCommands` 를 그대로 따라 읽기 전에 거절한다(세부 `{index, field, reason}` 도 데몬과 같음 — 렌더러가 행 문구를 같은 함수로 고름). 이름은 정확히 같아야 같은 명령(이름 바꾸기 = 삭제 + 추가), 남은 명령의 순서가 바뀌면 "Run order: …" 줄. 버튼은 기존 `lado.presence.change`. 프로젝트 이름은 읽지 않고 본문에 ID.
  5. 보호 경로: main 이 trim 하고 빈 목록·빈 글롭·제어/방향 문자·50개 초과를 데몬의 `{index, reason}` 으로 거절, 데몬의 규칙이 보호 규칙이 아니면 `{rule}`. 순서까지 같으면 묻지 않음. 더한·뺀 줄은 각 300자.
  6. 카탈로그 생성용 `npm` 스크립트가 없어 스크립트 머리말대로 `node scripts/sync-tray-catalog.mjs` 로 만들었다.
  7. Completed + `merged_without_approval` 의 이름은 그대로 "Completed"(톤만 attention) + 둘째 줄 사유.
  8. 데이터 함수 테스트는 브리지 모킹이 달라 새 파일 `src/renderer/lib/lado-verify.test.ts`.
  9. `useLadoExecution` 은 그대로 둠 — 응답 타입이 스키마로 `verification`·`integration`·PR head 를 이미 실어 "넓힘" 이 끝났고, `lado_execution` 변경 이벤트가 이미 무효화한다.
- 테스트 순서: 필수 테스트가 새 채널·타입을 써서 구현 뒤에 썼다(실패부터 보지 못함). main 테스트는 가짜 데몬으로 요청 순서·서명 여부·정확한 본문 바이트·확인 본문 줄을 본다.
- 확인한 명령과 결과: vitest main 2파일 86 ok, 렌더러 5파일(lado-errors, lado-task-status, lado-verify, lado, renderer-coverage) 51 ok; 넓게 `src/renderer/components/lado src/renderer/lib src/renderer/hooks src/renderer/i18n src/main src/renderer/components/settings` 309파일 중 1 실패(`LadoTaskDialog` 의 옛 `taskRunning` 문구) → 고친 뒤 23/23. `tsc --noEmit` 첫 실행 2개 오류(`recordList` 의 `flatMap` 추론, 상태 테스트 fixture 의 execution 모양) → 고친 뒤 0개(9m03s, 증분), `tsc -p tsconfig.e2e.json` 0개(8 s). 프런트엔드 lint·format 도구는 이 저장소에 없음.
- 게이트: `gate-check.sh --run` 8/8 MET(HEAD `3b4da3c83`; G6 `preload.ts` 14/20, G4 9m27s 중 대부분). 엔트리 커밋 뒤 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 절차 0~7 전부, 필수 테스트 5파일(main 2, 렌더러 3). 화면 컴포넌트(S17·S18)는 손대지 않음 — 바꾼 컴포넌트 테스트는 fixture·기대 문구뿐.
- 남은 틈:
  - 실제 Electron 네이티브 창으로 긴 본문(Windows·macOS 의 줄바꿈·폭)을 보지 않았다(단위 테스트만).
  - 통합 확인 본문은 근거(criteria) 요약을 보이지 않는다(info 라 막지 않음). 다음 일 줄은 GitHub 게시가 성공한다고 가정한다(실패하면 Integrating + `status_not_posted`).
  - 브라우저(dev) 경로의 `approveLadoIntegration` 은 wait·fail 을 미리 거절하지 않고 데몬의 409 를 그대로 낸다.
  - en 외 7개 로캘은 원어민 검토 없음(용어집·기존 문체 — 독어 du·freigeben, 불어 vous·콜론 앞 공백, 중국어 worker — 를 따름).
  - 이 머신에서 `tsc --noEmit` 이 약 9분(병렬 레인 부하로 보임) — 게이트 G4 하나가 10분 도구 한도에 가깝다.
- 다음 단계 메모:
  - **S17(실행 화면)**: 승인 `useApproveLadoIntegration(projectId, sessionId).mutate()`(취소면 결과 null, 실패는 `LadoApiError` → `ladoErrorText`), 병합 `useMergeLadoPR(projectId, sessionId).mutate({number: pr.number, url: pr.url, headSha: integration.headSha})`(거절 `LADO_INTEGRATION_APPROVAL_REQUIRED`·`LADO_MERGE_GATE_FAILED` 문구 있음), 다시 돌리기 `useRunLadoChecks(projectId, sessionId)`, 로그 `useLadoCheckLog(sessionId, checkRunId, open)`(빈 문자열 가능). 실행은 `useLadoExecution` 의 `verification`(`LadoVerification`)·`integration`(`LadoIntegrationApproval`)·`pr.headSha/targetBranch/title`. 항목 kind·코드의 화면 문구(`lado.verify.*` 같은 것)는 아직 없다 — wait·fail 코드는 `ladoReasonText` 가 읽지만 confirm·pass·info 코드(`no_tests`, `outside_plan`, `base_moved`, `reflog_off`, `already_on_base`, `unprotected*`, `protection_unknown`, `review_inconclusive`, `none_reported`, `none_configured`, `no_local_branch`, `not_required`)는 S17 이 키를 더할 것. 상태 이름·톤은 `ladoTaskStatus`/`ladoTaskReason` 이 이미 낸다(보드 줄도 같은 함수로).
  - **S18(프로젝트·규칙 화면)**: 명령 `useUpdateLadoProjectChecks(projectId).mutate(commands)`(취소 null; 오류 세부 `{index, field, reason}` → 행 아래 `ladoCheckCommandProblemText(t, details)`, 목록 전체·시간은 `ladoErrorText` 가 문제만), 시간 `useUpdateLadoCheckTimeout(projectId).mutate(minutes)`, 보호 `useLadoProtection(projectId)`·Check again `useRefreshLadoProtection(projectId)`, 보호 경로 `useChangeLadoProtectedPaths().mutate(globs)`(오류 `{index, reason}` → `ladoProtectedPathProblemText`, `{rule}` → `lado.error.protectedPathsRule`), 규칙 id 상수 `LADO_PROTECTED_PATHS_RULE_ID`. `lib/lado-rules.ts` 의 새 게이트 4개(S04 의 `seedName`)는 아직 없음.
  - **문구 키**: main `lado.presence.integration.{title, task, head, passed, outsidePlan, commitsUnclear, prTitle, baseMoved, reflogOff, alreadyOnBase, unprotected, protectionUnknown, unprotectedGitlab, reviewInconclusive, noTests, otherCode, nextStatus, nextUnprotected, approve}`, `lado.presence.checks.{title, detail, added, changed, removed, order}`, `lado.presence.paths.{title, detail, added, removed}`(KEYS 에 등록, 바꾸면 `node scripts/sync-tray-catalog.mjs`). 렌더러 `lado.task.state.{validating, integrationPending, integrating}`, `lado.reason.*` 14개, `lado.error.{integrationNotReady, integrationHeadChanged, verificationChanged, integrationAlreadyApproved, integrationAckMismatch, verifyUnavailable, executionNoPr, checkRunNotFound, checkCommandsInvalid, protectedPathsInvalid, protectedPathsRule, integrationApprovalRequired, mergeGateFailed}`, `lado.checkCommand.problem.*` 9개, `lado.protectedPaths.problem.{required, tooMany, invalid}`, `lado.setup.conflict.allowsProtected`.
  - **테스트 파일**: G1 `src/main/lado-presence.test.ts`("approving an integration", "changing check commands", "changing protected paths")·`src/main/tray-catalog.test.ts`, G2 `src/renderer/lib/lado-errors.test.ts`("LADO verification errors")·`src/renderer/components/lado/lado-task-status.test.ts`(새), 데이터 함수 `src/renderer/lib/lado-verify.test.ts`(새, G2 밖).
  - **typecheck 반복**: `npx tsc --noEmit --incremental --tsBuildInfoFile <scratchpad>/main.tsbuildinfo`(첫 실행 9분, 이후 빠름), e2e 는 `-p tsconfig.e2e.json` 8초. 게이트 G4 는 증분 없이 약 9분.
  - **패키지 안 새 이름**(뒤 단계 충돌 주의): main `approveIntegration`, `changeProjectChecks`, `changeProtectedPaths`, `pathsSubject`, `protectedGlobs`, `checkCommands`, `checkChangeLines`, `integrationRun`, `verifiedSubject`, `integrationDetail`, `confirmLine`, `examples`, `isStringList`, `stringList`, `recordList`, 타입 `PathsSubject`·`IntegrationRun`·`VerifyItem`·`VerifiedSubject`, 상수 `SESSION_ID`·`PROTECTED_PATHS_RULE_ID`·`MAX_PROTECTED_PATHS`·`MAX_CHECK_COMMANDS`·`MAX_CHECK_NAME`·`MAX_CHECK_COMMAND`·`CHECK_NAME`·`UNSAFE_CHAR`·`INTEGRATION_TIMEOUT_MS`·`MAX_EXAMPLES`·`MAX_EXAMPLE_CHARS`; main 테스트 `HEAD`·`VERIFY_SHA`·`PR_URL`·`PATHS_RULE`·`executionResponse`·`confirmItems`·`confirmCodes`·`integrationPreview`·`integrationApproved`·`pathsRule`, 라우트 `execution`·`integrationPreview`·`integration`; 렌더러 lado-errors `checkCommandProblemKeys`·`wholeCheckProblems`·`checkCommandProblemKey`·`protectedPathProblemKeys`, useLado `useInvalidateLadoRun`.
  - **S19(문서)**: 다르게 한 것 1~5(main 의 미리 거절, 확인 본문 줄 형식, 명령·글롭의 main 쪽 검사)를 ADR-0002·TDD §7.1·§13 에 적을 것.
