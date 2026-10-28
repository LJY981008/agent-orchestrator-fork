# 2026-10-27 LADO 검증·통합 승인 — S18 화면: 프로젝트 검사·보호 상태·보호 규칙과 보호 경로

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 e·i·o, §17) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

프로젝트 설정 → LADO 에서 검사 명령(데스크톱 확인으로 저장)과 시간 제한을 정하고, 저장소 기준 브랜치가 원격에서 보호되는지("보호 안 됨: 감지만" 이면 무엇을 켜야 하는지)를 본다. 설정 → 규칙의 보호 규칙 카드에 8단계 게이트 넷이 보이고, 보호 경로는 데스크톱 확인으로만 바뀐다.

## 범위

- 만든다: `components/lado/LadoProjectChecksCard.tsx`, `LadoProjectProtectionCard.tsx`, `components/lado/LadoProtectedPathsDialog.tsx`(+ 각 테스트).
- 바꾼다: `LadoProjectSection.tsx`(카드 둘 붙이기), `components/settings/RulesSection.tsx`(보호 카드 행·편집), `lib/lado-rules.ts`(내장 규칙 표 4개), 각 테스트, 8개 로캘, `DESIGN.md`(결정 기록 한 행).
- 하지 않는다: 실행 화면(S17).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. **`DESIGN.md` 를 먼저 읽는다**(설정 모달 구성, 카드, 공유 select·switch, 행 편집, 조용한 동작, caution 색, 문장형 대소문자). 비슷한 기존 화면: `LadoProjectSection.tsx`, `RulesSection.tsx` 의 보호 카드(천장 select), 7단계의 setup 편집기·확인 흐름. `<fieldset>` 은 스크롤되지 않으니 감싸는 div 로 스크롤(메모리 노트).
2. `LadoProjectChecksCard`: 제목 "Checks", 한 줄 설명(데몬이 실행 워크트리에서 PR head 로 돌림, 에이전트 샌드박스 밖이라 저장 때 데스크톱이 묻는다); 명령 행(이름 입력 + 명령 입력, 지우기), "Add command"(10개까지), 저장("Save commands" — 바뀐 게 있을 때만 활성, `updateLadoProjectChecks` → 브리지; 취소면 편집 유지; 400 세부는 그 행 아래 문구); "Time limit" 공유 select(5·10·15·30·60·90·120분, 바로 저장, 확인 없음).
3. `LadoProjectProtectionCard`: 저장소마다 한 줄 — "<repo> · <branch>": Protected(ready 점) 또는 Not protected(caution 점) + 빠진 것("Pull requests required", "lado/integration required", "Force pushes blocked", "Deletion blocked"), 읽기 오류면 그 한 줄, GitLab 이면 "GitLab: LADO only detects"; 아래에 muted 안내(GitHub → Settings → Rules → Rulesets 에서 기준 브랜치에 PR 필수·상태 검사 `lado/integration`·강제 푸시와 삭제 차단); 확인 시각; 조용한 "Check again"(`fetchLadoProtection(…, true)`).
4. `RulesSection` 보호 카드: 새 게이트 넷(이름·설명·"Always on"); `paths.protected` 행은 경로 수("9 paths")와 조용한 "Edit" → `LadoProtectedPathsDialog`(한 줄에 경로 하나인 textarea, 잘못된 줄 표시, 저장 → 브리지 `changeRule` 의 경로 변경 → 데스크톱이 묻는다).
5. `lib/lado-rules.ts`: 상수 넷과 `builtinRules` 에 `seedName`(S04 의 영어 이름 그대로: "Integration approval required", "No direct writes to the base branch", "Tests must pass", "Protected paths")과 `lado.rules.builtin.{integrationApproval,noDirectWrite,testsMustPass,protectedPaths}.{name,description}` 8개 로캘. 한국어 이름 예: 통합 승인 필수, 기준 브랜치 직접 쓰기 금지, 테스트 통과 필수, 보호 경로.
6. 문구 8개 로캘, coverage 통과. `DESIGN.md` 결정 기록 한 행: "2026-10-27 | LADO fork: project checks, protection and protected paths …".
7. 커밋 메시지 예: `feat(lado): project checks, protection status and protected paths in settings`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S18-project-ui.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `LadoProjectChecksCard.test.tsx` — 행 편집·추가·지우기·10개 상한, 저장이 브리지로 가고 취소면 편집 유지, 400 세부가 행 아래, 시간 제한은 바로 저장
- `LadoProjectProtectionCard.test.tsx` — 보호됨·안 됨(빠진 것)·오류·GitLab 줄, 안내 글, 다시 확인이 refresh 로
- `LadoProtectedPathsDialog.test.tsx` — 줄 나누기·빈 줄 무시·잘못된 줄 표시, 저장이 브리지로
- `RulesSection.test.tsx` — 새 게이트 넷이 보호 카드에, 보호 경로 편집 열기
- `lado-rules.test.ts` — 새 이름·설명이 한국어로, 사용자가 이름을 바꾸면 그대로
- `LadoProjectSection.test.tsx` — 카드 둘이 보임
- `renderer-coverage.test.ts`

## 수용 게이트

- [ ] S18-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S18-G1: 화면 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh vitest src/renderer/components/lado/LadoProjectChecksCard.test.tsx src/renderer/components/lado/LadoProjectProtectionCard.test.tsx src/renderer/components/lado/LadoProtectedPathsDialog.test.tsx src/renderer/components/settings/RulesSection.test.tsx src/renderer/lib/lado-rules.test.ts src/renderer/components/lado/LadoProjectSection.test.tsx src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S18-G2: 새 규칙 이름 키가 8개 로캘에
  CHECK: bash .claude/gate/lado-verification.sh locales lado.rules.builtin.integrationApproval.name lado.rules.builtin.noDirectWrite.name lado.rules.builtin.testsMustPass.name lado.rules.builtin.protectedPaths.name
  EXPECT: LOCALES_OK
- [ ] S18-G3: 프런트엔드 typecheck(e2e 포함)
  CHECK: bash .claude/gate/lado-verification.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S18-G4: DESIGN.md 에 결정 기록 행
  CHECK: bash .claude/gate/lado-verification.sh has DESIGN.md "LADO fork: project checks, protection and protected paths"
  EXPECT: HAS_OK
- [ ] S18-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `521ae6e17`(브랜치 `lado8/S18`, 체인 lane-S18; S16 엔트리 커밋 뒤, S17 은 병렬 레인), 트리 깨끗. Ubuntu, node v24.21.0, TZ=UTC, 체인 무인 세션. G0 `DEFS_UNCHANGED`.
- 출발점 실측:
  - `LadoProjectSection.tsx` 는 머리 없는 카드 하나(LADO 모드 스위치, 프로젝트 동시 실행 select)뿐 — 검사·보호 카드 없음.
  - Settings → Rules 의 보호 카드(`LadoProtectedRuleRow`): 천장만 공유 select, 나머지는 muted "Always on"; 보호 경로 편집 없음. `lib/lado-rules.ts` 내장 표는 4행(계획 승인, 천장, 한국어 커밋, setup 확인) — 새 게이트 4개는 seed 영어 이름 그대로, 설명 없이 보였을 것.
  - S16 데이터 계층은 있음: `useUpdateLadoProjectChecks`·`useUpdateLadoCheckTimeout`·`useLadoProtection`·`useRefreshLadoProtection`·`useChangeLadoProtectedPaths`, `ladoCheckCommandProblemText`·`ladoProtectedPathProblemText`, `LADO_PROTECTED_PATHS_RULE_ID`, 문구 `lado.checkCommand.problem.*`·`lado.protectedPaths.problem.*`.
  - typecheck 는 시작 때 재지 않음(한 번 약 10분).
- 앞 단계 메모 중 이 단계 몫: S04(내장 영어 이름 4개 = `seedName`, 데스크톱 `changeRule` 이 `PATCH {"baseVersion": n, "paths": {"globs": [...]}}` 에 서명), S07(400 `LADO_CHECK_COMMANDS_INVALID {index, field, reason}` → 행 아래 문구, `checkTimeoutMinutes` 는 시간 선택 아래; 시간 제한은 증명 없이 저장), S08·S15(보호 상태 `protected|unprotected|unknown|unsupported`, `missing` 값 `pull_request`·`required_status_checks:lado/integration`·`non_fast_forward`·`deletion`, `checkedAt`, `error`), S16(훅 이름, 취소면 null, 문제 문구 함수). 메모리: `<fieldset>` 은 스크롤 안 됨(div 로), 버튼 글꼴은 안쪽 span, 대화상자 autofocus.

## After — 수행 후 실측

- 커밋: `69e7ee2c2` feat(lado): project checks, protection status and protected paths in settings · `28e715da7` style(lado): size the protected paths Edit like the row's other text · 이 엔트리 커밋.
- 바꾼 것:
  - 새 `components/lado/LadoProjectChecksCard.tsx`(Project settings → LADO → "Checks"): 머리 아래 muted 한 줄(toolbar), 카드 안 폼 블록(`settings-content-row`)에 열 이름 Name·Command(aria-hidden, 입력마다 aria-label "Name n"·"Command n"), 행마다 이름 입력(w-40)·명령 입력(감싼 div 의 font-mono)·지우기 아이콘("Remove <이름>" 또는 "Remove command n"), 행 아래 문제 문구(role=alert, 입력 `aria-invalid`·`aria-describedby`). 조용한 "Add command"(10행이면 비활성 + muted "LADO runs at most 10 commands.", 새 행에 포커스, 행을 지우면 Add command 로 포커스), primary "Save commands"(바뀐 것이 있을 때만). 저장 → `useUpdateLadoProjectChecks` → 브리지(없으면 PUT); null(취소) → 편집 그대로, 성공 → 저장된 목록. 오류: `LADO_CHECK_COMMANDS_INVALID` 의 index·field 가 보낸 행을 가리키면 그 행 아래 `ladoCheckCommandProblemText`, 그 밖은 목록 아래 `ladoErrorText`, 브라우저의 `LADO_PRESENCE_UNAVAILABLE` 는 `lado.checks.unavailable`. "Time limit" 행: 공유 `SettingsOptionMenu` 5·10·15·30·60·90·120분(저장값이 목록 밖이면 끼워 넣음), 바로 `useUpdateLadoCheckTimeout`, 저장 중 고른 값, 실패는 행 설명 아래. 저장된 명령이 다른 곳에서 바뀌면 편집이 없을 때만 행이 따라감.
  - 새 `LadoProjectProtectionCard.tsx`("Branch protection"): 머리 action 에 가장 오래된 `checkedAt` 의 시각("Last checked 11:38 AM", `formatResetTime`, title 은 전체 시각)과 조용한 "Check again"(RefreshCw, 진행 중 회전·"Checking…", `useRefreshLadoProtection` = `fetchLadoProtection(id, true)`). 저장소마다 `SettingsRow`(`data-state`): 라벨 `<repo> · <branch>`(각각 mono, `github:`·`@host` 뗌), 오른쪽 점 + 상태 — protected = ready 점 "Protected"; unprotected = caution 점 "Not protected" + muted "Missing: Pull requests required, lado/integration required, Force pushes blocked, Deletion blocked"(모르는 값은 그대로); unknown = caution 점 "Could not check" + "Could not read the rules: <오류>"; unsupported = caution 점 "Not protected" + "GitLab: LADO only detects". 카드 아래 muted 안내(unprotected·unknown 이 있을 때만, `Trans` 로 `lado/integration` mono), 다시 읽기 실패는 카드 아래 alert(마지막 읽기는 남음), 저장소 없음 "No repositories to check.", 로딩·읽기 오류.
  - 새 `LadoProtectedPathsDialog.tsx`("Edit protected paths"): 설정 대화상자 틀(규칙 편집기와 같은 머리·바닥), 스크롤은 div(fieldset 아님). 라벨 "Paths and patterns" + 개수("12 paths"), muted 문법 한 줄, mono textarea(12줄). 줄마다 trim, 빈 줄 무시. 클라이언트 검사 = 데몬 `validLadoAffectedPath` 규칙(앞 `/`, 역슬래시, 제어 문자, 드라이브 문자, `..` 마디, Go `path.Match` 문자 클래스 모양) → "Line n: …" 목록, 0개 → "Enter a path or a pattern.", 50 초과 → tooMany; 문제·변경 없음·진행 중이면 "Save paths" 비활성. 저장 → `useChangeLadoProtectedPaths` → 브리지 `changeRule("paths.protected", {globs})`; null 이면 열린 채 편집 유지, 규칙이 오면 닫힘. 거절 `{index, reason}` 은 보낸 줄의 번호로 "Line n: …", `{rule}` 등은 `ladoErrorText`, 브라우저의 `LADO_PRESENCE_UNAVAILABLE` 는 `lado.protectedPaths.unavailable`. 열 때 textarea 포커스, 닫을 때 포커스 복원.
  - `LadoProjectSection.tsx`: 기존 카드 아래 두 카드(세 섹션을 section gap 의 열로).
  - `LadoRuleRows.tsx` `LadoProtectedRuleRow`: 필수 prop `onEditPaths`; `paths.protected` 행은 "Always on" 대신 muted 개수("9 paths", `rule.paths.globs`)와 조용한 "Edit"(aria-label "Edit protected paths", 글자 14px).
  - `RulesSection.tsx`: 보호 경로 편집기 상태(열 때의 글롭 고정, 열 때마다 key 로 다시 마운트), 머리 주석.
  - `lib/lado-rules.ts`: 상수 `LADO_RULE_INTEGRATION_APPROVAL`·`LADO_RULE_NO_DIRECT_WRITE`·`LADO_RULE_TESTS_MUST_PASS`·`LADO_RULE_PROTECTED_PATHS`(= `LADO_PROTECTED_PATHS_RULE_ID`), `builtinRules` 4행(seedName = S04 영어 이름).
  - 로캘 8개: 키 +52(`lado.checks.*` 20, `lado.protection.*` 16, `lado.rules.builtin.{integrationApproval,noDirectWrite,testsMustPass,protectedPaths}.{name,description}` 8, `lado.rules.protectedPaths.count_{one,other}` 2, `lado.protectedPaths.{title,label,hint,line,save,unavailable}` 6) + es·fr·pt-BR 의 `_many` 2개씩. 한국어 이름: 통합 승인 필수, 기준 브랜치 직접 쓰기 금지, 테스트 통과 필수, 보호 경로.
  - `DESIGN.md` 결정 기록 한 행("2026-10-27 | LADO fork: project checks, protection and protected paths …").
  - 테스트: 새 3파일(검사 5, 보호 5, 대화상자 4), `RulesSection.test.tsx` +2(게이트 넷, 보호 경로 편집), `lado-rules.test.ts` +1(한국어 이름·설명, 이름 바꾸면 그대로), `LadoProjectSection.test.tsx` +1(카드 둘) + `fetchLadoProtection` 모킹 + 옛 응답 fixture 1곳에 `checkCommands`·`checkTimeoutMinutes`.
- 엔트리와 다르게 한 것과 이유:
  1. 보호 카드 제목은 "Branch protection"(플랜 §17 의 "Protection") — 프로젝트 설정 안에서 보호 규칙·보호 경로와 헷갈리지 않게.
  2. 확인 시각은 저장소마다가 아니라 카드 머리에 하나(가장 오래된 읽기 = 카드 전체가 그만큼은 새로움), 문구 "Last checked {{time}}".
  3. unknown 은 "Could not check" + 오류 줄, GitLab 은 "Not protected" + "GitLab: LADO only detects", 둘 다 caution 점. GitHub 안내는 unprotected·unknown 이 있을 때만(전부 보호됨이거나 GitLab 뿐이면 숨김 — 결정에 쓸모없는 글은 빼는 DESIGN 규칙).
  4. 이름·명령이 모두 빈 행은 보내지 않고, 이름·명령의 앞뒤 공백을 뗀다(main·데몬은 trim 하지 않음 — 앞 공백은 이름 정규식이 거절, 뒤 공백은 다른 이름이 됨). 그래서 "Add command" 만으로는 저장이 켜지지 않는다.
  5. 브라우저(dev)의 `LADO_PRESENCE_UNAVAILABLE` 는 "approve" 문구 대신 `lado.checks.unavailable`·`lado.protectedPaths.unavailable`(모드 끄기의 `modeOffUnavailable` 선례).
  6. 보호 경로는 잘못된 줄을 보내기 전에 줄 번호로 보이고(데몬과 같은 규칙의 클라이언트 검사), 거절 index 도 보낸 줄의 번호로 바꿔 보인다. 개수는 라벨 옆. 저장 전 영향(승인된 작업) 목록은 없음 — S16 의 `useChangeLadoProtectedPaths` 가 곧장 브리지로 가고, 데스크톱 확인 본문이 다시 승인이 필요함을 말한다.
  7. 보호 경로 행은 "Always on" 대신 개수 + Edit(천장 행이 select 를 두는 자리).
  8. 명령 목록은 행마다 `settings-row-bar` 가 아니라 카드 안 폼 블록 하나(열 이름 + 입력 행, 행 사이 구분선 없음); 시간 제한은 그 아래 일반 행.
  9. 플레이스홀더 예시 `lint`·`npm run lint` 는 8개 로캘 모두 같은 값(이름은 ASCII 만 허용).
  10. es·fr·pt-BR `_many` 복수형 — `i18n/instance.test.ts` 의 CLDR 검사가 넓은 실행에서 잡았다.
  11. 랩에서 "Edit" 가 13px 로 이웃("9 paths"·"Always on" 14px)보다 작아 별도 커밋으로 맞춤.
- 테스트 순서: 필수 테스트가 새 컴포넌트를 렌더링해 구현 뒤에 썼다(실패부터 보지 못함).
- 확인한 명령과 결과:
  - 필수 7파일 46/46(G1 세트). 넓게 `src/renderer/components/lado src/renderer/components/settings src/renderer/lib src/renderer/i18n SettingsDialog.test.tsx` 167파일 중 1 실패(es `lado.checks.minutes_many` 없음) → 고친 뒤 i18n 21/21; 전체 `npx vitest run` 534파일 8794 통과·7 skip(2m01s).
  - `tsc --noEmit` 증분 0(9m49s), `tsc -p tsconfig.e2e.json` 0(9 s). `_many` 추가 뒤 증분은 590 s `timeout` 에 걸려(rc=124) G3 전체 실행으로 확인. 프런트엔드 lint·format 도구는 이 저장소에 없음.
  - 랩(렌더러만, 데몬 없이): dev-web(5199) + Playwright 가 `127.0.0.1:3917` 의 LADO 경로만 채움(프로젝트 설정·보호·규칙), 나머지는 중단. 프로젝트 설정 → LADO(두 카드, 네 상태, 안내), 설정 → 규칙(게이트 넷, "9 paths"·Edit), "Edit protected paths"(mono textarea, 잘못된 줄 3개, 저장 비활성)을 라이트 테마 1400px 스크린숏으로 봄. 콘솔 오류는 모두 중단한 데몬 요청. 스크린숏은 scratchpad 로 옮기고 `.playwright-mcp/` 지움.
- 게이트: HEAD `28e715da7` 에서 G0·G1·G2·G4 MET, G3 MET(9m49s — Bash 10분 한도 직전), G5 는 엔트리 커밋 뒤. 엔트리 커밋 뒤 전건 다시 돌려 그 HEAD 에 증거를 남김.

## 비교/회고

- 범위 대비 결과: 엔트리 범위(만든다 3 + 테스트, 바꾼다 `LadoProjectSection`·`RulesSection`·`lado-rules` + 테스트·로캘·DESIGN.md) 전부, 필수 테스트 7파일. 실행 화면(S17)은 손대지 않음. 범위 밖 변경: `LadoRuleRows.tsx`(보호 행이 그려지는 곳), 기존 fixture 1곳.
- 남은 틈:
  - 실제 Electron 창의 데스크톱 확인까지 이어 보지 않았다(브리지 모킹 + dev-web 렌더러만). 다크 테마·좁은 폭은 보지 않음.
  - 클라이언트 경로 검사는 Go `path.Match` 를 UTF-16 단위로 흉내 — 서로게이트 쌍이 든 문자 클래스는 다르게 볼 수 있다(데몬이 다시 거절하므로 저장은 안전).
  - 편집 중 다른 창이 명령을 바꾸면 편집이 있는 동안 새 값을 따라가지 않고, 저장하면 이 목록으로 덮는다(확인 본문이 바뀌는 줄을 모두 보임).
  - 이름 규칙 위반(공백으로 시작 등)은 저장 뒤 main 의 첫 문제 하나로만 보인다. 보호 상태의 `requiredChecks` 는 화면에 없다.
  - typecheck 한 번이 약 9m50s — G3 가 Bash 도구 10분 한도에 닿을락말락(러너의 `GATE_TIMEOUT=1800` 은 넉넉함).
- 다음 단계 메모:
  - S19(docs) 화면 이름: Project settings → LADO 의 "Checks" 카드(Name·Command 행, Add command, Save commands, Time limit)와 "Branch protection" 카드(Protected / Not protected / Could not check, "GitLab: LADO only detects", Last checked, Check again, GitHub 룰셋 안내), Settings → Rules 보호 카드의 게이트 넷과 "Protected paths" 행(개수·Edit) → "Edit protected paths" 대화상자(Paths and patterns, Save paths). 다르게 한 것 1~7 을 TDD §7.1·§8.2, HARNESS §14, LADO.md·LADO.ko.md 에.
  - 랩에서 볼 것(메인 세션): 다크 테마와 좁은 설정 창, 실제 Electron 확인 창의 줄(명령 추가·변경·삭제, 경로 더함·뺌), 실제 GitHub 저장소의 보호 상태(소유자 허락 뒤).
  - 병합: S17 레인도 `DESIGN.md` 결정 기록 표 끝에 행을 더한다 — 병합 충돌이면 두 행 모두 남길 것(S17 → S18 순).
  - 패키지 안 새 이름(충돌 주의): `LadoProjectChecksCard`, `LADO_MAX_CHECK_COMMANDS`, `LadoProjectProtectionCard`, `LadoProtectedPathsDialog`, `LADO_RULE_INTEGRATION_APPROVAL`, `LADO_RULE_NO_DIRECT_WRITE`, `LADO_RULE_TESTS_MUST_PASS`, `LADO_RULE_PROTECTED_PATHS`, `LadoProtectedRuleRow` 의 `onEditPaths`; 테스트 id `lado-project-checks`, `lado-check-commands`, `lado-check-command`, `lado-project-protection`, `lado-protection-repository`, `lado-protection-guide`, `lado-protected-paths`, `lado-protected-path-count`, `lado-protected-path-problems`.
