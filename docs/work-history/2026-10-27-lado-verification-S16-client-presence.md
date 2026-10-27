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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 데이터 함수·훅 이름, 문구 키, 실제 테스트 파일 이름(G2 가 다르면 After 에 적는다))
