# 2026-10-22 LADO 규칙 코어 — S10 렌더러 데이터 계층·Electron main presence

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§8, §9 첫 세 불릿) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

화면(S11·S12)이 쓸 데이터 계층을 만들고, 사람만 할 수 있는 두 동작을 Electron main 이 맡게 한다: 계획 승인이 **규칙 스냅숏 sha256 까지 서명**하고 확인창에 규칙 요약을 보이는 것, 권한 상한 변경을 네이티브 확인 뒤 서명해 보내는 것(ADR-0002 — 렌더러는 키를 갖지 않고 대상을 잘못 말할 수 없다).

## 범위

- 고친다(렌더러): `frontend/src/renderer/lib/lado.ts`(타입·함수·쿼리 키, `approveLadoPlan(revisionId, sha256, policySha256)`), `hooks/useLado.ts`(규칙·유효 규칙 훅, 변경 후 무효화 — 플랜 §9), `lib/lado-errors.ts`(새 오류 코드 전부: 플랜 §5·§6 목록 + `LADO_EFFECTIVE_SCOPE_REQUIRED`, `LADO_POLICY_SHA_REQUIRED`, `LADO_RULES_UNAVAILABLE`, `LADO_RULES_CHANGED`, 그리고 문제 코드 `LADO_AGENT_*`·`LADO_RULES_CONFLICT`·`LADO_AO_RULES_FILE_UNREADABLE`·`LADO_CEILING_MISSING`·`LADO_PLAN_CEILING_CLAMPED`·`LADO_CODEX_ASKS_APPROVAL` 의 문장), `lib/event-transport.ts`(`lado_rules`).
- 만든다: `frontend/src/renderer/lib/lado-rules.ts`(`useLadoRuleChange`, 내장 규칙 이름·설명 키, 강도 라벨, 문제 문장) + 테스트 `lado-rules.test.ts`, `lado-errors.test.ts`(없으면 만든다).
- 고친다(main·공유·브리지): `frontend/src/main/lado-presence.ts`, `frontend/src/shared/lado-presence.ts`(`LADO_PRESENCE_CHANGE_RULE_CHANNEL`, 요청·결과 타입), `frontend/src/preload.ts`(`lado.presence.changeRule`), `frontend/src/renderer/lib/bridge.ts`·`frontend/src/renderer/test/setup.ts`·`frontend/e2e/support/fake-bridge.ts` 의 `lado` 스텁, `frontend/scripts/sync-tray-catalog.mjs` KEYS(플랜 §8 의 새 `lado.presence.*` 키) 후 `node scripts/sync-tray-catalog.mjs`(frontend/ 에서)로 `tray-catalog.generated.ts` 재생성.
- i18n: 새 키(`lado.error.*`, `lado.policy.problem.*`, `lado.presence.*`, `lado.rules.builtin.*`, 강도 라벨)를 8개 로캘 파일 끝에 실제 번역으로. 화면 문자열은 S11·S12 가 더한다.
- 하지 않는다: 화면 컴포넌트(S11·S12). 태스크 뷰가 실제로 `policySha256` 을 넘기는 연결은 S12(여기서는 함수·훅 시그니처까지).

## 절차

1. `lado.ts`: `approveLadoPlan` 은 브리지 결과가 있으면 그대로(main 이 effective 를 직접 읽어 서명한다), 브라우저 경로는 `{sha256, policySha256}` 를 POST. `changeLadoCeiling(ruleId, mode)` 도 같은 꼴(브리지 `presence.changeRule` → 없으면 PATCH `{baseVersion, ceiling:{mode}}` 직접 — 개발 presence 에서만 통과).
2. main `approvePlan`: 플랜 읽기 → `GET /api/v1/lado/effective?taskId=<plan.task.id>` → `snapshotSha256` 검증(hex 64), ON 규칙 수·`permissionCeiling`·blocking 문제 수 → 확인창 detail 에 규칙 줄(+ blocking 줄) → 확인 뒤 본문 `{"sha256":…,"policySha256":…}` 서명·전송. effective 읽기 실패면 확인창 없이 오류.
3. main `changeRule`(새 채널): 신뢰 발신자만, mode 는 세 값만, `GET /api/v1/lado/rules/<id>` 의 `rule.protected === true` 이고 id 가 `agent.permission.ceiling` 일 때만, 확인창 문구는 main 카탈로그(`lado.presence.ruleChangeTitle`·`ceilingChangeDetail`·`mode.*`·`change`·`cancel`), 본문 `{"baseVersion":<n>,"ceiling":{"mode":"<mode>"}}` 정확히 이 바이트로 서명해 PATCH. UNSAFE_TEXT 정리·타임아웃·오류 봉투 처리는 기존 `approvePlan` 과 같게.
4. 기존 `lado-presence.test.ts` 의 고정 서명 벡터는 바꾸지 않는다(백엔드 `TestSignMatchesTheDesktopApp` 와 짝). 새 테스트는 벡터 함수 `signLadoPresence` 로 기대값을 만든다.
5. `useLadoRuleChange`: dry run 요청 → `impact.tasks` 가 있으면 확인 상태(다이얼로그 열림 — 컴포넌트는 S11 이 만들고 여기서는 상태·콜백만) → 확인 시 적용 / 없으면 바로 적용. 실패는 `LadoApiError` 그대로.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, 예 `feat(lado): rules client and confirmed protected changes` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S10-client-presence.md` 로 전건 MET 를 확인한다.

## 필수 테스트 (제목 문자열 그대로 — 게이트가 찾는다)

- `frontend/src/main/lado-presence.test.ts`: `signs the plan and rule snapshot hashes together`, `refuses to approve when the effective rules cannot be read`, `changes the permission ceiling after confirmation`, `refuses a rule change from an untrusted sender`, `refuses to change a rule that is not the permission ceiling`
- `frontend/src/renderer/lib/lado.test.ts`: `sends the rule snapshot hash when approving in a browser`
- `frontend/src/renderer/lib/lado-rules.test.ts`: `asks before a change that affects approved tasks`, `applies a change without approved tasks at once`
- `frontend/src/renderer/lib/event-transport.test.ts`: `refreshes rules and tasks on lado_rules events`
- `frontend/src/renderer/lib/lado-errors.test.ts`: `translates rule error codes`

## 수용 게이트

- [ ] S10-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S10-G1: 필수 테스트 제목이 있다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/main/lado-presence.test.ts 'signs the plan and rule snapshot hashes together' 'refuses to approve when the effective rules cannot be read' 'changes the permission ceiling after confirmation' 'refuses a rule change from an untrusted sender' 'refuses to change a rule that is not the permission ceiling' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/lado.test.ts 'sends the rule snapshot hash when approving in a browser' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/lado-rules.test.ts 'asks before a change that affects approved tasks' 'applies a change without approved tasks at once' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/event-transport.test.ts 'refreshes rules and tasks on lado_rules events' && bash .claude/gate/lado-rules-core.sh has frontend/src/renderer/lib/lado-errors.test.ts 'translates rule error codes'
  EXPECT: HAS_OK
- [ ] S10-G2: 관련 테스트 파일 전부 통과(트레이 카탈로그·i18n 포함)
  CHECK: bash .claude/gate/lado-rules-core.sh vitest src/main/lado-presence.test.ts src/main/tray-catalog.test.ts src/renderer/lib/lado.test.ts src/renderer/lib/lado-rules.test.ts src/renderer/lib/event-transport.test.ts src/renderer/lib/lado-errors.test.ts src/renderer/i18n/renderer-coverage.test.ts src/renderer/i18n/instance.test.ts
  EXPECT: VITEST_OK
- [ ] S10-G3: 채널·브리지·트레이 키가 이어졌다
  CHECK: bash .claude/gate/lado-rules-core.sh has frontend/src/shared/lado-presence.ts 'lado:presence:changeRule' && bash .claude/gate/lado-rules-core.sh has frontend/src/preload.ts 'changeRule' && bash .claude/gate/lado-rules-core.sh has frontend/scripts/sync-tray-catalog.mjs 'lado.presence.ceilingChangeDetail' 'lado.presence.approveRules' && bash .claude/gate/lado-rules-core.sh has frontend/src/main/tray-catalog.generated.ts 'lado.presence.ceilingChangeDetail'
  EXPECT: HAS_OK
- [ ] S10-G4: 타입체크(렌더러·main·e2e 가짜 브리지)
  CHECK: bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S10-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: (단계 세션이 적는다)

## After — 수행 후 실측

(단계 세션이 채운다)

## 비교/회고

(단계 세션이 채운다)
