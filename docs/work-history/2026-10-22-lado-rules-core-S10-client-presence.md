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

- 시작 HEAD: `df3f785365b0d831bd3da5e9d655d79ad0296aa8`(`docs(lado): record S09`), 트리 깨끗.
- 렌더러: `lib/lado.ts` 에 규칙 타입·함수·쿼리 키 없음. `approveLadoPlan(revisionId, sha256)` 은 브라우저 경로에서 `{sha256}` 만 보낸다 — S08 이후 데몬은 `policySha256` 없는 승인을 400 `LADO_POLICY_SHA_REQUIRED` 로 거절하므로 브라우저(dev presence) 승인은 지금 늘 실패한다. `lado-errors.ts` 에 규칙 코드·정책 문제 문장 없음(`lado-errors.test.ts` 파일 자체가 없다). `event-transport.ts` 는 `lado_task` 만 안다 — `lado_rules` CDC 는 `ladoTaskOnly` 로 걸리지 않아 사이드바 전체(workspaces) 무효화로 떨어진다.
- main: `approvePlan` 은 플랜만 읽고 본문 `{"sha256":…}` 을 서명한다 → 데스크톱 승인도 지금 400 `LADO_POLICY_SHA_REQUIRED`. 권한 상한 변경 채널 없음. 트레이 카탈로그 KEYS 의 LADO 키는 4개(`approveTitle`·`approveDetail`·`approve`·`cancel`).
- S09 메모 반영 대상: `LADO_PRESENCE_REQUIRED` 문구가 승인 전용("Approve this in the AO desktop app.")인데 상한 변경에도 나간다. 데몬의 상한 PATCH 는 경로 `r.URL.EscapedPath()` 와 본문 바이트 그대로를 검증한다.

## After — 수행 후 실측

- 코드 커밋 `2df79a49e` `feat(lado): rules client and confirmed protected changes`: 27 파일, +1997 −160.
  - main `lado-presence.ts` 승인: 플랜 읽기 → latest·valid 확인(기존) → `GET /api/v1/lado/effective?taskId=<plan.task.id>` → `rulesSubject`(응답 `taskId` 일치, `snapshotSha256` hex 64, `permissionCeiling` 세 모드, `rules`·`problems` 배열) → 확인창 detail = `approveDetail` + 빈 줄 + `approveRules`(ON 수·상한 라벨·규칙 sha 앞 12자) + blocking 이 있으면 `approveRulesBlocking` → 본문 `{"sha256":…,"policySha256":…}` 서명. effective 가 실패·형식 불량·다른 태스크면 확인창 없이 오류(데몬 봉투의 code·details 는 그대로). `planSubject` 는 `task.id` 와 `revision.taskId` 일치도 본다.
  - 새 채널 `lado:presence:changeRule`: 신뢰 발신자 → `ruleId === "agent.permission.ceiling"` 이고 mode 가 세 값 중 하나(아니면 `LADO_RULE_CEILING_INVALID`, fetch 0) → `GET /api/v1/lado/rules/<id>` → 데몬 답의 `rule.id`·`protected === true` 가 아니면 `LADO_RULE_CEILING_INVALID` → `version` 양의 정수 → 같은 모드면 확인·PATCH 없이 `{status:"changed", rule}` → 확인창(`ruleChangeTitle`, `ceilingChangeDetail` 의 from → to 는 `mode.*` 라벨, 버튼 `cancel`·`change`) → `JSON.stringify({baseVersion, ceiling:{mode}})` 그대로 서명한 PATCH → 응답의 `rule` 로 `{status:"changed", rule}`. 모드가 없거나 틀린 상한(해석이 `LADO_CEILING_MISSING` 을 내는 상태)은 from 을 "—" 로 보이고 고칠 수 있게 했다.
  - 두 흐름은 `signed()`(nonce·만료·헤더)·`translator()`(요청마다 로캘)·`confirm(t, …)` 을 공유한다. 기존 고정 서명 벡터 테스트는 그대로다.
  - shared: `LADO_PRESENCE_CHANGE_RULE_CHANNEL`, `LadoCeilingMode`(= schema `LadoRuleCeiling.mode`), `LADO_CEILING_MODES`, `LadoPresenceRuleChangeRequest`·`LadoPresenceRuleChangeResult`, 공통 `LadoPresenceFailure`. preload `lado.presence.changeRule(ruleId, mode)`; `bridge.ts`·`test/setup.ts`·`e2e/support/fake-bridge.ts`(2곳) 스텁 `changeRule: async () => null`.
  - 트레이 KEYS 에 8개(`approveRules`·`approveRulesBlocking`·`ruleChangeTitle`·`ceilingChangeDetail`·`change`·`mode.{acceptEdits,auto,bypassPermissions}`) → `node scripts/sync-tray-catalog.mjs` 로 재생성.
  - `lib/lado.ts`: 규칙 타입 14개, 쿼리 키 `["lado","rules",archived]`·`["lado","rule",id]`·`["lado","effective",projectId,taskId]`(+ 루트·프로젝트 접두), `invalidateLadoRuleQueries`(rules·rule·effective·tasks·task), `fetchLadoRules`·`fetchLadoRule`·`createLadoRule`·`updateLadoRule`·`deleteLadoRule`·`setLadoBinding`·`fetchLadoEffective(projectId, taskId?)`(둘 중 하나만 보낸다), `changeLadoCeiling(ruleId, mode)`(브리지 → 없으면 활성 버전을 읽어 PATCH), `approveLadoPlan(revisionId, sha256, policySha256)`.
  - `hooks/useLado.ts`: `useLadoRules`, `useLadoRule`, `useLadoEffective`(`refetchOnMount: "always"` — AO 프로젝트 설정 변경은 CDC 가 없다), `useDeleteLadoRule`; `useApproveLadoPlan` 입력에 `policySha256?`(없으면 `""`), 성공 시 그 태스크의 effective 도 무효화.
  - `lib/lado-rules.ts`(신규): 내장 규칙 id 상수, `ladoRuleName`·`ladoRuleDescription`(seed 이름일 때만 번역 — 보호되지 않은 내장 규칙 `git.commit.message.korean` 의 개명을 존중), `ladoStrengthLabel`, `ladoProblemText`(effective 규칙 이름을 붙인 문제 문장), `useLadoRuleChange()` → `{change, pending, confirm, cancel, isPending, error, reset}`. 요청은 `create`·`update`·`binding`·`ceiling`; dry run 의 `impact.tasks` 가 있으면 `{status:"confirm", impact, rule}` 로 멈춰 `pending` 에 두고, 없으면 바로 적용. 적용 성공만 무효화한다. 실패는 `LadoApiError` 로 reject 하고 `error` 에도 남는다.
  - `lib/lado-errors.ts`: 규칙 코드 27개(+ `INVALID_QUERY` → internal), detail `rule`, `ladoPolicyProblemText`(문제 코드 9개, capability·모드 라벨, 규칙·에이전트 라벨 함수 선택). 모르는 코드는 코드 그대로.
  - `lib/event-transport.ts`: `kind === "lado_rules"` → rules·rule 루트, 프로젝트가 있으면 task 뷰 루트, 그리고 프로젝트마다 effective 접두·tasks. `ladoTaskOnly` → `ladoOnly` 로 바꿔 workspaces 무효화에서 뺀다.
  - i18n 8 로캘 × 57 키: 신규 53(error 25·policy problem 9·capability 2·builtin 6·strength 3·presence 8), 문구 변경 4(`presenceRequired`·`presenceInvalid`·`presenceExpired`·`presenceReplayed` — "다시 승인" → "다시 시도", 상한 변경에도 맞게). 변수 집합은 로캘 간 같다(스크립트에서 단언).
  - `LadoTaskDialog.test.tsx` 한 줄: 훅이 세 번째 인자 `""` 를 넘기므로 기대값을 맞췄다(태스크 뷰 연결은 S12).
- 테스트: 필수 10개 + 추가 — main: blocking 줄, 취소·같은 모드, 모드 없는 상한 복구, PATCH 거절 봉투, ko 문구(7 케이스 표로 effective 읽기 실패); renderer: `changeLadoCeiling` 3, `fetchLadoEffective` 1, 상한 dry run → 데스크톱 취소, 오류 reject, 이름·설명·강도, 충돌 문장; errors: 8 로캘 × 규칙 코드 28·문제 코드 9 가 키 그대로나 데몬 문장으로 새지 않음.
- 변이 검사 17개(파일 사본 → 변이 → 해당 테스트만 → 복원): 승인 본문에서 policySha256 제거, effective 실패 무시, sha hex 검사 제거, 다른 태스크 effective 허용, changeRule 발신자 검사 제거, 아무 규칙 id 허용, 비보호 답 허용, PATCH 키 순서 바꿈, PATCH 를 POST 로 서명, 브라우저 승인에서 해시 제거, 확인 안 묻기, 늘 묻기, 적용 후 무효화 제거, `lado_rules` 무시, `lado_rules` 가 workspaces 무효화, 규칙 코드 매핑 제거, detail `rule` 제거 — 17개 모두 죽었다. 생존 0.
- 추가 실행: 전체 vitest 512 파일 8521 통과(7 skip, 1분 43초). `npm run typecheck` 6분 31초 통과, `typecheck:e2e` 8초 통과. 첫 typecheck 는 내 테스트 도우미의 암묵 any 하나로 실패해 고쳤다. 백엔드 변경 없음.
- 게이트(HEAD `2df79a49e`, 이 엔트리만 미커밋): `GATES=6 MET=5 UNMET=1` — G5 만 이 엔트리 파일 때문에 UNMET(6분 34초). 엔트리를 커밋한 뒤 같은 명령을 다시 돌려 전건 MET 와 증거를 새 HEAD 에 묶는다.

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 플랜에 없어 이 단계에서 정한 것:
  - `changeLadoCeiling(ruleId, mode)` 의 브라우저 경로는 baseVersion 을 인자로 받지 않고 활성 버전을 읽어 PATCH 한다 — main 과 같은 꼴이고 엔트리 시그니처를 지킨다. dry run 은 화면이 본 버전(`baseVersion`)으로 하므로 그 사이의 변경은 dry run 의 409 `LADO_RULE_CHANGED` 가 잡는다.
  - 같은 모드 요청은 확인·PATCH 없이 현재 규칙으로 `changed`(데몬도 내용 sha 가 같으면 presence 없이 통과시킨다).
  - main 은 렌더러가 보낸 id 가 아니라 데몬 답으로 보호된 상한인지 판단한다. 규칙 id·모드가 틀리면 데몬과 같은 코드 `LADO_RULE_CEILING_INVALID`.
  - 문제 문장 표(`ladoPolicyProblemText`)는 오류·계획 문제 표와 같은 `lado-errors.ts` 에, 규칙 이름을 붙이는 `ladoProblemText` 는 `lado-rules.ts` 에 두었다(엔트리가 두 파일에 "문제 문장" 을 적어 나눴다).
  - 모드 라벨: main 은 새 `lado.presence.mode.*`(트레이 카탈로그 전용 경로), 렌더러 문제 문장은 AO 의 `settings.project.permission*` — 8 로캘 모두 같은 문자열로 맞췄다.
  - `useApproveLadoPlan` 의 `policySha256` 는 선택. 데스크톱 승인은 main 이 effective 를 직접 읽으므로 이 단계부터 다시 동작한다(Before 의 400 `LADO_POLICY_SHA_REQUIRED` 경로가 닫힘). 브라우저(dev presence) 승인은 태스크 뷰가 해시를 넘기는 S12 까지 400 그대로다.
  - `approveDetail` 은 바꾸지 않고 규칙 줄을 뒤에 붙였다 — 기존 키 8 로캘을 건드리지 않고, 앞 세 줄(제목 위조 방지 테스트)도 그대로다.
- 다음 단계 메모:
  - S11: `useLadoRuleChange()` 를 쓴다. `pending` 이 있으면 `LadoRuleImpactDialog` 를 `pending.impact`(·`pending.rule`)로 열고 `confirm()`/`cancel()`. 상한 Select 는 `{kind:"ceiling", ruleId, baseVersion: rule.version, mode}` — dry run 뒤 데스크톱이 네이티브 확인을 띄운다(`cancelled` 가 올 수 있다). `change()`·`confirm()` 은 실패 시 reject 하므로 `.catch(() => undefined)` 뒤 `error` 를 `ladoErrorText` 로 그린다. 이름 `ladoRuleName(t, id, name)`, 설명 `ladoRuleDescription`, 강도 `ladoStrengthLabel`, 삭제 `useDeleteLadoRule`. 내장 카테고리(`workflow`·`tools`·`git`)의 번역은 화면 문자열로 S11 이 더한다.
  - S12: 태스크 뷰 승인 직전에 `fetchLadoEffective(projectId, taskId)` 의 `snapshotSha256` 을 `policySha256` 으로 넘기고 `LadoTaskDialog.test.tsx` 의 `""` 기대값을 바꾼다. 문제 목록은 `ladoProblemText(t, problem, effective.rules)`. ko `lado.view.permission` 은 아직 "최대 권한"인데 용어집·새 문자열은 "권한 상한" — S12 가 태스크 뷰에서 맞춘다.
  - 게이트의 typecheck 는 여전히 6분 30초 남짓이다.
