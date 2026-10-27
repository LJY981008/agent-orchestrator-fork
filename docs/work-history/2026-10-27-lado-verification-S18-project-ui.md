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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 랩에서 볼 것, S19 문서에 적을 화면 이름)
