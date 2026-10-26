# 2026-10-26 LADO 로컬 설정 채택 — S03 도메인: 항목·버전·트리·바인딩·인벤토리 용어

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 e·i, §3) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

뒤 단계 전부가 같은 이름과 규칙을 쓰도록 도메인 타입과 순수 함수를 먼저 고정한다: 도구·종류·내용 종류, 폴더 항목의 정규 매니페스트(해시가 버전이 된다), 바인딩 규칙, 인벤토리의 상태·사유 코드, OS 검증표, 데스크톱 확인이 필요한 변경의 판정.

## 범위

- 만든다: `backend/internal/domain/lado_setup.go`, `backend/internal/domain/lado_setup_test.go`; `domain/lado_presence.go` 에 세 동작.
- 하지 않는다: 저장(S04), 검사·채택 로직(S05 이후).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. `domain/lado_rule.go`·`domain/lado_queue.go` 의 꼴(문자열 타입, 상수, `Validate`, `CanonicalJSON`/`SHA256`)을 따른다.
2. 타입(플랜 §3):
   - `LadoSetupTool`(`claude-code`, `codex` — `domain.AgentHarness` 값과 같은 문자열), `LadoSetupKind`(`instructions`, `setting`, `skill`, `agent`, `command`, `output_style`, `hook_script`, `mcp_server`), `LadoSetupContentKind`(`file`, `json`, `tree`).
   - `LadoSetupItem`(id, tool, kind, name, origin path, adoption id, created/deleted/restored 시각)과 `Live()`; `LadoSetupItemVersion`(id, item id, version, content kind, sha256, size, executable, actor, created at); `LadoSetupAdoption`(플랜 §2 열; state `completed`/`partial`); `LadoSetupBinding`(item id, scope `global`/`project`/`task`, scope id, state)와 `Validate()`(global 은 scope id 비고 on/off, project·task 는 scope id 필수와 force_on/force_off); `LadoConflictAcceptance`.
   - `LadoSetupTree{Entries []LadoSetupTreeEntry{Path, Mode ("file"|"exec"), Size, SHA256}}` — `Normalize()`(경로 순 정렬, 슬래시 구분, 상대 경로만: 빈 경로·`..`·절대·역슬래시·중복 거부), `CanonicalJSON()`, `SHA256()`.
   - 인벤토리: `LadoSetupEntry`(Tool, Kind, Name, Location(`claude_dir`|`claude_json`|`codex_home`|`project`), Path, Size, SHA256, Tracked, Status, Reason, DefaultAction(`move`|`keep`), ItemID, ProjectID)와 상태 상수(`managed`, `adoptable`, `not_adoptable`, `never`, `not_managed`, `repo_tracked`), 사유 상수(플랜 §3 목록 그대로), 기본 동작 규칙 `LadoSetupDefaultAction(tool, kind, name)`(결정 e: settings 키는 목록 안이면 move, 밖이면 keep; 나머지 move).
   - 변경 감지: `LadoSetupDrift{ItemID, Kind ("edited"|"new_local"), Tool, ItemKind, Name, Path, SHA256}`.
   - `LadoSetupDeliveryVerified(goos string, tool LadoSetupTool) bool` — `linux` 는 두 도구 다 true, 나머지 OS 는 false(플랜 결정 4; S01 결과가 다르면 그 결과를 따르고 After 에 적는다).
   - `LadoSetupNeedsPresence(kind LadoSetupKind, name string) bool` — 결정 i: `hook_script`, `mcp_server`, 그리고 `setting` 의 `hooks`, `permissions`, `env`, `statusLine`, `enabledPlugins`, `extraKnownMarketplaces`.
   - 로그인 키: `adapters/agentaccount/claude.go:32-41` 과 같은 값을 도메인 상수 `LadoSetupClaudeLoginKeys`(최상위 키)·`LadoSetupClaudeLoginEnv`(env 이름)로 둔다(domain 은 adapters 를 import 하지 않는다). 두 목록이 같은지는 S05 가 agentaccount 쪽 값과 비교하는 테스트로 묶는다.
3. `domain/lado_presence.go`: `LadoPresenceSetupAdopt = "setup.adopt"`, `LadoPresenceSetupRestore = "setup.restore"`, `LadoPresenceSetupChange = "setup.change"`(기존 상수 이름 꼴을 따른다).
4. 커밋 메시지 예: `feat(lado): domain types for adopted local setup`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S03-domain.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestLadoSetupTreeIsCanonical` — 순서가 달라도 같은 JSON·해시, 내용 하나가 바뀌면 다른 해시, 잘못된 경로 6가지 거부
- `TestLadoSetupBindingValidate` — 허용 4조합, 거부 조합(global force_on, project on, 빈 scope id 등)
- `TestLadoSetupItemLive` — 지움·복원 시각이 있으면 live 아님
- `TestLadoSetupDeliveryVerified` — linux 두 도구 true, windows·darwin·freebsd false
- `TestLadoSetupNeedsPresence` — 결정 i 의 표 전부(필요/불필요 양쪽)
- `TestLadoSetupDefaultAction` — 결정 e 의 move 목록과 keep(예 `theme`, `verbose`), 나머지 종류는 move
- `TestLadoSetupCodesAreStable` — 상태·사유·종류·동작 문자열을 그대로 고정(렌더러가 쓰는 값)

## 수용 게이트

- [ ] S03-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S03-G1: 필수 테스트 7개가 실제로 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests ./internal/domain/ TestLadoSetupTreeIsCanonical TestLadoSetupBindingValidate TestLadoSetupItemLive TestLadoSetupDeliveryVerified TestLadoSetupNeedsPresence TestLadoSetupDefaultAction TestLadoSetupCodesAreStable
  EXPECT: GO_TESTS_OK 7/7
- [ ] S03-G2: 데스크톱 확인 동작 세 개가 도메인에 있다
  CHECK: bash .claude/gate/lado-local-setup.sh has backend/internal/domain/lado_presence.go '"setup.adopt"' '"setup.restore"' '"setup.change"'
  EXPECT: HAS_OK
- [ ] S03-G3: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S03-G4: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — 타입·함수 이름, 로그인 키 목록)
