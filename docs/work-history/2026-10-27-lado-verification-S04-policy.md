# 2026-10-27 LADO 검증·통합 승인 — S04 정책: 보호 규칙 4개·한국어 규칙 v2·lado-policy/3·보호 경로 변경

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 o·p, §14) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

HARNESS §14 의 보호 규칙 4개(통합 승인 필수, 기준 브랜치 직접 쓰기 금지, 테스트 통과, 보호 경로)를 늘 켜진 내장 규칙으로 등록하고, 한국어 커밋 규칙에 검사기를 붙인다. 스냅숏은 어느 검사기가 적용되는지 담도록 `lado-policy/3` 이 된다. 보호 경로 목록은 보호 규칙이라 데스크톱 확인으로만 바뀐다.

## 범위

- 바꾼다: `service/ladopolicy/builtins.go`, `service.go`(`EnsureBuiltins` 의 업그레이드, 보호 경로 변경), `snapshot.go`(`Compiler`, `Validators`), `prompt.go`(보호 경로 목록을 프롬프트에), `resolve.go`(필요하면), 테스트들(`builtins_test.go`, 스냅숏 박제 테스트 이름과 바이트), `daemon/lado_wiring_test.go`(`TestWireLadoWorkSeedsBuiltinRules` 의 기대값).
- 만들 수 있다: 규칙 버전을 `lado` 로 덧붙이는 store 메서드가 없으면 `store/lado_rule_store.go` 에 하나(`AppendLadoBuiltinRuleVersion` 처럼, 감사 이벤트 같은 트랜잭션).
- 하지 않는다: HTTP DTO·명세(S15), 렌더러(S18), 검사기 실행(S06·S11).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `Builtins()` 에 플랜 §1 o 의 네 규칙(보호, Global on, 이름·범주·enforcement·지침 그대로). `paths.protected` 의 config 는 `domain.DefaultLadoProtectedPaths()` 를 `{"globs":[…]}` 로. 영어 이름은 렌더러 표(`lib/lado-rules.ts` `seedName`, S18)가 그대로 쓸 문자열이다: "Integration approval required", "No direct writes to the base branch", "Tests must pass", "Protected paths".
2. 한국어 규칙 v2: 내장 정의가 버전 이력을 갖게 한다(예: `previous []domain.LadoRuleContent`). `EnsureBuiltins` 는 없는 규칙을 심는 것에 더해, 저장된 활성 버전의 sha256 이 이전 내장 버전 중 하나와 같으면 현재 내장 버전을 다음 번호로 `lado` 가 덧붙이고 감사 `rule.builtin_upgraded`(`{from, to}`)를 같은 트랜잭션에 쓴다. 개발자가 고친 규칙(다른 sha256)은 그대로 둔다. 두 번째 시작은 아무것도 안 쓴다. v2 = v1 + `validator` enforcement + config `domain.DefaultLadoCommitLocaleConfig()`.
3. 스냅숏: `Compiler = "lado-policy/3"`, `SnapshotContent` 의 `WorkflowGates` 바로 뒤에 `Validators []SnapshotValidator`(`ruleId`, `kind`, `config` — 원시 JSON). 켜진 규칙 중 `validator` enforcement 가 있는 것마다: `test.command.must_pass` → `checks_must_pass`(`{}`), `paths.protected` → `protected_paths`(그 버전의 config), `git.commit.message.korean` → `commit_message_locale`(그 버전의 config, 없으면 기본). 목록은 nil 아닌 `[]`. 형식 주석을 고친다.
4. 프롬프트: 켜진 규칙의 지침이 들어가는 `## LADO rules` 에서 `paths.protected` 부분 끝에 설정된 경로를 한 줄로 붙인다(`Protected paths: \`.github/**\`, …`). 다른 규칙은 그대로.
5. 보호 경로 변경: `UpdateRule`(또는 지금 천장을 바꾸는 경로)에 `paths.protected` 의 `Paths *domain.LadoProtectedPathsConfig` 입력을 더한다 — 천장과 같은 방식: `baseVersion` 확인(`LADO_RULE_CHANGED`), `domain.ValidateLadoProtectedPaths`(400 `LADO_PROTECTED_PATHS_INVALID` `{index, reason}`), 존재 증명 `rule.protected_change`(대상 규칙 id), 새 버전(config 만 바뀜)과 감사 `rule.protected_changed`(actor `human`·`human:dev-presence`, `{from, to}`). dry run 은 증명 없이 영향만. 다른 보호 규칙의 편집 거부는 그대로.
6. 기존 테스트의 내장 규칙 수(보호 3 → 7, 전체 4 → 8)와 스냅숏 박제를 새 값으로 고친다. 스냅숏 해시를 계산해 비교하는 다른 패키지 테스트(ladotask, ladoqueue)가 깨지면 해시를 하드코딩하지 말고 계산하도록 고친다.
7. 커밋 메시지 예: `feat(lado): the four protected verification gates and lado-policy/3`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S04-policy.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestBuiltinsIncludeTheVerificationGates` — 8개 행: id, 보호, Global, 범주, enforcement, config 기본값(보호 경로 9개, 한국어 v2 config)
- `TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule` — v1 그대로면 v2 가 `lado` 로 붙고 감사 1건; 고친 규칙은 그대로; 다시 시작하면 아무것도 없음; 없는 규칙은 v2 로 바로 심김
- `TestSnapshotFormatIsLadoPolicy3` — 고정 입력의 정규 JSON 바이트 박제(`validators` 위치·내용 포함)
- `TestValidatorsFollowTheOnRules` — 한국어 규칙 꺼짐 → `commit_message_locale` 없음, 켜짐 → 있음(설정 그대로), 보호 검사기 둘은 늘 있음, 고친 한국어 규칙에 `validator` 가 없으면 없음
- `TestProtectedPathsChangeNeedsPresence` — 증명 없음 거부, 증명 있으면 새 버전·감사 `human`, 재사용 증명 거부, 잘못된 경로 400, `baseVersion` 불일치 409, dry run 은 아무것도 안 씀
- `TestProtectedPathsPromptListsGlobs` — 워커 규칙 글에 설정 경로 줄

## 수용 게이트

- [ ] S04-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S04-G1: 필수 테스트 6개 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladopolicy/ TestBuiltinsIncludeTheVerificationGates TestEnsureBuiltinsUpgradesAnUnchangedKoreanRule TestSnapshotFormatIsLadoPolicy3 TestValidatorsFollowTheOnRules TestProtectedPathsChangeNeedsPresence TestProtectedPathsPromptListsGlobs
  EXPECT: GO_TESTS_OK 6/6
- [ ] S04-G2: 규칙·태스크·디스패처 패키지 테스트가 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoqueue/
  EXPECT: GO_PKG_OK
- [ ] S04-G3: 데몬이 새 내장 규칙을 심는다
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/daemon/ TestWireLadoWorkSeedsBuiltinRules
  EXPECT: GO_TESTS_OK 1/1
- [ ] S04-G4: store 의 규칙 테스트가 그대로 통과
  CHECK: bash .claude/gate/lado-verification.sh go-run ./internal/storage/sqlite/store/ 'LadoRule|LadoBuiltin|LadoPolicy'
  EXPECT: GO_RUN_OK
- [ ] S04-G5: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S04-G6: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 검사기 kind 이름, 업그레이드 함수, 변경 입력 필드 이름, S15 가 DTO 에 더할 것)
