# 2026-10-26 LADO 로컬 설정 채택 — S12 변경: 만들기·편집·삭제·켜기/끄기·변경 감지·수락

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(소유자 결정 3, §1 결정 i·j, §12) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-012: 관리 항목을 앱에서 바꾸는 모든 길을 한 서비스로 모은다. 모든 변경은 먼저 dry run(차이·충돌·영향·`changeSha256`)을 보여 주고, 코드를 실행하거나 권한을 넓히는 변경은 데스크톱 확인(`setup.change`, subject `changeSha256`)을 받은 뒤에만 쓴다. 텍스트 항목만 편집하고 폴더 항목은 켜기/끄기·삭제와 변경 감지로만 바꾼다(소유자 결정 3).

## 범위

- 만든다: `service/ladosetup/changes.go`, 테스트.
- 하지 않는다: HTTP(S13), 화면(S15·S16).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 요청 하나의 모양: `Change{Op ("create"|"edit"|"delete"|"binding"|"drift_keep"|"drift_revert"|"accept"|"revoke"), ItemID, Tool, Kind, Name, Content (text 또는 JSON 값), BaseVersion, Binding{Scope, ScopeID, State ("on"|"off"|"inherit"|"force_on"|"force_off")}, Acceptance{Code, Detail, Scope, ScopeID}, AcceptanceID, DriftSHA256, DryRun bool}`. `changeSha256` = `DryRun` 을 뺀 정규 JSON 의 sha256.
2. 공통 흐름(`Apply(ctx, change, presence)`):
   1. 검증: 종류별 내용 검사 — `instructions`·`agent`·`command`·`output_style`·한 파일 `skill`(SKILL.md) 은 UTF-8 텍스트(최대 1 MiB, 스킬은 front matter `name`·`description`), `setting` 은 JSON 값(로그인 키·`env` 의 로그인 변수 거부 `LADO_SETUP_LOGIN_KEY`), `mcp_server` 는 Claude 면 `{"type":…, …}` JSON, Codex 면 S05 의 adoptable 조건(이름·비밀 값 없음). 폴더 항목 편집은 `LADO_SETUP_FOLDER_ITEM`. 만들기에서 이름이 겹치면 409 `LADO_SETUP_ITEM_EXISTS`.
   2. dry run 결과: 옛·새 텍스트(차이는 화면이 그림), 더하거나 없애는 충돌(S06), 영향(S11 `ImpactOfSetupChange`), `needsPresence`, `changeSha256`. 아무것도 쓰지 않는다.
   3. 실제 적용: `needsPresence` 면 `presence(setup.change, changeSha256)` — 없으면 403 `LADO_PRESENCE_REQUIRED`(ladopresence 의 오류). 쓰기는 S04 메서드로 감사와 한 트랜잭션, 버전 내용은 먼저 blob 에. 커밋 뒤 관리 폴더 파일을 그 버전으로 다시 쓰고(S07 의 `managed.go`), 인벤토리 캐시를 무효화.
3. 데스크톱 확인 판정(`needsPresence`, 결정 i): 먼저 S03 회고가 짚은 키 셋을 `domain.LadoSetupNeedsPresence` 의 settings 목록에 더한다 — `otelHeadersHelper`(스크립트 실행), `enableAllProjectMcpServers`·`enabledMcpjsonServers`(프로젝트 MCP 서버 승인) — 그리고 `TestLadoSetupNeedsPresence` 표에 세 줄을 더한다(플랜 결정 i 는 갱신됨). 만들기·편집·변경 감지 유지 — `domain.LadoSetupNeedsPresence(kind, name)`; 바인딩 — 그 항목이 presence 종류이고 새 상태가 `on`·`force_on`(전역 행을 지워 켜지는 inherit 포함). 끄기·`force_off`·삭제·되돌리기·수락·취소 — 필요 없음. 결과에 actor `human`/`human:dev-presence`(증명) 또는 `api`.
4. 삭제: soft delete(버전·스냅숏 고정은 그대로 — 항목이 쓰인 적이 있으면 그 사실을 응답에). 관리 폴더의 파일은 지운다(blob 은 남김).
5. 수락: S06 규칙(경고만, 지금 버전의 sha256, 범위). 취소는 `revoked_at`.
6. 감사 동작 이름은 플랜 §12 그대로.
6a. S08 회고가 짚은 S07 의 틈을 고친다: `removeOriginal` 의 keys 분기에서 `atomicWrite` 가 rename 뒤 폴더 fsync 실패로 오류를 내면 지금은 실패로 보고 그 키가 기록·관리 사본 없이 원래 파일에서 빠진다. 오류 뒤 원래 파일을 다시 해시해 쓰려던 내용(`out`)과 같으면 성공으로 이어 간다(fsync 실패는 경고 로그). 테스트는 `ops` 대역으로 rename 뒤 오류를 낸다.
7. 커밋 메시지 예: `feat(lado): change managed setup items with dry runs and confirmation`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S12-changes.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestChangeDryRunWritesNothing` — 모든 Op 의 dry run 이 store·blob·관리 폴더를 바꾸지 않고 `changeSha256` 이 안정적
- `TestChangePresenceRules` — 결정 i 의 표: 필요한 변경은 증명 없이 403, 증명이 있으면 쓰고 actor `human`, 필요 없는 변경은 증명 없이 actor `api`
- `TestEditNeedsTheLatestVersion` — 기준 버전 틀림 409, 맞으면 새 버전·관리 파일 갱신
- `TestFolderItemsRefuseEdits` — 여러 파일 스킬·hook 폴더 편집 거부, 켜기/끄기·삭제는 됨
- `TestCreateSkillWithOneFile` — SKILL.md 하나로 만든 스킬이 관리 폴더에 생기고 front matter 검사
- `TestDeleteKeepsVersionsAndPins` — 삭제 뒤 버전 읽힘, 고정된 스냅숏은 그대로 읽힘
- `TestBindingTurningOnACodeItemNeedsPresence` — hook 항목의 전역 on·프로젝트 force_on 은 확인 필요, off·force_off 는 불필요
- `TestLoginKeysCannotBeAdded` — settings 에 로그인 키·로그인 env 를 넣는 만들기·편집 거부
- `TestAdoptionTreatsAFlushErrorAfterRenameAsDone` — 위 6a: rename 은 됐고 fsync 만 실패하면 그 키는 관리 항목으로 기록되고 복원할 수 있다

## 수용 게이트

- [ ] S12-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S12-G1: 필수 테스트 9개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestChangeDryRunWritesNothing TestChangePresenceRules TestEditNeedsTheLatestVersion TestFolderItemsRefuseEdits TestCreateSkillWithOneFile TestDeleteKeepsVersionsAndPins TestBindingTurningOnACodeItemNeedsPresence TestLoginKeysCannotBeAdded TestAdoptionTreatsAFlushErrorAfterRenameAsDone
  EXPECT: GO_TESTS_OK 9/9
- [ ] S12-G2: 패키지 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S12-G3: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S12-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S12-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `0b3215250`(docs(lado): record S11), 브랜치 `feat/lado-local-setup`, 트리 깨끗. Linux, go1.27.1. `go test -count=1 ./internal/service/ladosetup/` 통과(2.1 s).
- 출발점: `ladosetup` 에 변경 서비스가 없다. 있는 것 — `KeepDrift`/`keepDrift`·`RevertDrift`(S08, 스스로 `lockWrite`), `writeManaged`·`removeManaged`(S07), 실효 해석의 `readScope`·`apply(ladopolicy.SetupChange)`(S11), `checkAcceptable`(S06). store 는 `CreateLadoSetupItem`·`AddLadoSetupItemVersion`·`DeleteLadoSetupItem`·`SetLadoSetupBinding`·`AcceptLadoConflict`·`RevokeLadoConflictAcceptance` 를 갖지만 `ladosetup.Store` 인터페이스에는 버전 추가만 있다. 고정 수 쿼리 `CountLadoPolicySnapshotSetupForItem` 은 gen 에만 있고 store 메서드가 없다. `domain.LadoSetupNeedsPresence` 의 settings 목록은 6개(플랜 결정 i 의 세 키 없음). `removeOriginal` keys 분기는 `atomicWrite` 오류를 그대로 실패로 본다.
- 앞 단계 메모 중 이 단계 몫:
  - S03·S08: presence 판정 `domain.LadoSetupNeedsPresence`, 동작 `domain.LadoPresenceSetupChange`(subject = changeSha256); 목록을 넓히면 `KeepDrift` 의 가드도 따라 넓어진다(S08 남은 틈 — HTTP 전에 닫힘).
  - S04: `SetLadoSetupBinding` 은 inherit=true 면 행 삭제(전역 inherit = 켜짐), 수락 범위 조합은 서비스가 확인.
  - S06: 수락은 `checkAcceptable(code)` → `CodeConflictNotAcceptable`, `Detail`·`ContentSHA256` 은 지금 결과에서, 항목 없는 결과는 수락 불가.
  - S07: 쓰기 `writeManaged(iv)`, 지우기 `removeManaged(item)`, 같은 이름 생성과 채택이 겹치지 않게 `lockWrite`, presence 꼴은 `confirm`(nil → 403 `LADO_PRESENCE_REQUIRED`, Dev → `human:dev-presence`, 재사용 → `ladopresence.Replayed()`).
  - S08: drift keep 은 `keepDrift(ctx, id, sha, approvalOf(proof))`, revert 는 `RevertDrift` — 둘 다 스스로 잠그므로 잡은 채로 부르지 말 것. S07 의 flush 틈(6a)은 이 단계가 고친다.
  - S09: 바인딩·버전이 바뀌면 다음 실행에 홈이 맞춰진다(홈 쪽 할 일 없음).
  - S11: 영향 `ImpactOfSetupChange(ctx, ladopolicy.SetupChange{Item·Content | Remove | Binding | Accept | Revoke})`, revert 는 영향 없음. ladosetup 이 ladopolicy 를 import 하므로 정책 서비스는 ladosetup 쪽 작은 인터페이스로 받는다(S13 이 넣는다).

## After — 수행 후 실측

- 커밋 `0b0bab84e` feat(lado): change managed setup items with dry runs and confirmation (코드 9개 파일).
- 바꾼 것:
  1. `service/ladosetup/changes.go`(새) — `Apply(ctx, Change, presence)` 하나로 8개 op(`OpCreate`·`OpEdit`·`OpDelete`·`OpBinding`·`OpDriftKeep`·`OpDriftRevert`·`OpAccept`·`OpRevoke`). 결과 `ChangeResult{Op, ChangeSHA256, NeedsPresence, Applied, Actor, Item, Version, Before, After (ChangeContent{ContentKind, SHA256, Size, Executable, Text, Files}), ConflictsAdded, ConflictsRemoved, Impact, Pins, Binding, Acceptance, Warnings}`. dry run 이 만든 새 항목·버전·수락 id 는 결과에서 비운다.
  2. `changeSha256` = 요청 필드(JSON 이름 `op, itemId, tool, kind, name, content, baseVersion, binding{scope,scopeId,state}, acceptance{code,detail,scope,scopeId}, acceptanceId, driftSha256`, 빈 것은 뺌, content 는 정규 JSON)의 정규 JSON sha256. 형식 표시는 넣지 않았다(엔트리 문구 그대로).
  3. 요청 모양: op 마다 받는 필드가 정해져 있고 빠지면 `missing`, 받지 않는 것이 있으면 `not_taken`(400 `LADO_SETUP_CHANGE_INVALID` {op, field, reason}) — 같은 변경은 해시가 하나다.
  4. 이름(만들기): Codex 는 instructions(`AGENTS.md`·`AGENTS.override.md`)와 MCP 서버만, MCP 이름은 두 도구 모두 `[A-Za-z0-9_-]`(`name_unsupported`), skill 은 한 단계, agent·command 는 하위 폴더 가능 `.md`, instructions·output style 은 `.md`, 점으로 시작하는 단계·`..`·역슬래시·NUL 거부, 로그인 키 이름은 `LADO_SETUP_LOGIN_KEY`. 같은 tool·kind 의 live 항목과 이름이 같거나(대소문자 무시) 한쪽이 다른 쪽 안이면 409 `LADO_SETUP_ITEM_EXISTS`.
  5. 내용: 텍스트 = JSON 문자열(UTF-8, NUL 없음, ≤ 1 MiB; `null` 거부), 스킬 SKILL.md 는 front matter `name`·`description`(`front_matter` + field), setting = JSON 값(로그인 키·env 의 로그인 변수 — 대소문자 무시 — 는 `LADO_SETUP_LOGIN_KEY` {key}, env 는 객체), MCP 서버 = 객체, `type` 은 있으면 문자열, `command` 나 `url`(`server_shape`), Codex 는 더해 비밀 값 없음(`literal_credentials`)·`-c` 왕복(`unreadable`). 오류 400 `LADO_SETUP_CONTENT_INVALID` {reason, …}. 편집은 최신 버전의 꼴을 따른다(파일은 실행 비트 유지, 한 파일 스킬은 SKILL.md 의 mode 유지).
  6. 폴더 항목(트리 버전 중 SKILL.md 하나뿐인 스킬이 아닌 것) 편집은 409 `LADO_SETUP_FOLDER_ITEM` — 기준 버전 검사보다 먼저. 기준 버전이 최신이 아니면 409 `LADO_SETUP_ITEM_CHANGED` {item, version}. 같은 내용은 no-op.
  7. 데스크톱 확인(`needsPresence`): 만들기·편집·변경 감지 유지 = `domain.LadoSetupNeedsPresence(kind, name)`; 바인딩 = 그 종류이고 새 상태가 on·force_on, 또는 inherit 이 그 범위에서 꺼짐 → 켜짐으로 만들 때; 나머지는 불필요. 증명 함수가 nil 이면 403 `LADO_PRESENCE_REQUIRED`, 재사용 증명은 `LADO_PRESENCE_REPLAYED`. actor `human`/`human:dev-presence`/`api`, 감사 payload 에 `change`(해시)와 증명이면 `proof`·`dev`.
  8. 쓰기(dry run 아닐 때): `lockWrite` 안에서 검사·쓰기. blob 먼저 → store(감사·증명 한 트랜잭션: `CreateLadoSetupItem`·`AddLadoSetupItemVersion`·`DeleteLadoSetupItem`·`SetLadoSetupBinding`·`AcceptLadoConflict`·`RevokeLadoConflictAcceptance`) → 관리 폴더(`writeManaged`/`removeManaged`, 실패는 경고 `LADO_SETUP_MANAGED_WRITE_FAILED`) → `rescan`(인벤토리 캐시). 감사 동작은 플랜 §12 이름 그대로(`setup.item_created/updated/deleted`, `setup.binding_set`, `setup.conflict_accepted/revoked` — 수락은 subject `setup_conflict_acceptance`). drift keep·revert 는 S08 의 `keepDrift`·`RevertDrift` 를 잠금 밖에서 부른다.
  9. 범위: 규칙 바인딩의 코드를 그대로 쓴다 — `LADO_BINDING_SCOPE_INVALID`·`LADO_BINDING_STATE_INVALID`(400), `LADO_SCOPE_NOT_FOUND`(404; 프로젝트는 `Deps.Projects` 의 보관 안 된 것, 태스크는 `Store.LadoTask` 의 이슈 있는 것), `LADO_TASK_CANCELLED`(409; 취소된 태스크는 inherit 만). Global 은 on·off·inherit(행 삭제 = 켜짐).
  10. 수락: 경고 코드만(400 `LADO_SETUP_CONFLICT_NOT_ACCEPTABLE` {code}), 항목의 최신 버전이 그 code·detail 경고를 스스로 내야 한다(기본 모드 검사는 가장 낮은 천장으로 — 어떤 천장에서든 날 경고는 받는다; 없으면 404 `LADO_SETUP_CONFLICT_NOT_FOUND` {item, code, detail}), `ContentSHA256` = 최신 버전 sha, 같은 live 수락은 no-op. 취소는 live 수락만(404 `LADO_SETUP_NOT_FOUND`).
  11. 충돌 증감: 항목 변경은 Global, 바인딩·수락·취소는 그 범위(태스크는 그 프로젝트와 함께)에서 S11 의 `readScope`+`apply` 로 전후를 돌려 수락 안 된 결과를 code·item·path·detail 로 비교(내용만 바뀐 같은 경고는 증감 없음, 수락이 풀리면 추가). 영향은 `Policy.ImpactOfSetupChange`(S11 의 `SetupChange` 꼴). 둘 다 정보이고 막지 않는다: 읽지 못하면(blob 없음 등) 비우고 `LADO_SETUP_PREVIEW_INCOMPLETE` — 깨진 항목도 지우거나 끌 수 있다.
  12. `service.go`: `Store` 인터페이스에 위 쓰기 6개·`CountLadoSetupItemPins`·`LadoTask`, `Policy` 인터페이스와 `SetPolicy`(없으면 빈 영향).
  13. store `CountLadoSetupItemPins`(gen 의 `CountLadoPolicySnapshotSetupForItem`) + `TestApproveLadoPlanPinsSetupVersions` 에 단언(지운 항목도 셈). domain `ladoSetupPresenceSettings` 에 `otelHeadersHelper`·`enableAllProjectMcpServers`·`enabledMcpjsonServers`, `TestLadoSetupNeedsPresence` 표 3줄. `domain.SplitLadoFrontMatter` 공개(스킬 front matter 가 플랜 파일과 같은 꼴).
  14. 6a: `adopt.go` `removeOriginal` keys 분기 — `atomicWrite` 오류 뒤 원래 파일이 `out` 바이트면 성공으로 이어 가고 경고 로그. 이 수정 없이 새 테스트를 돌리면 "The adoption stopped before anything was removed" 로 실패하면서 settings.json 에서는 키가 이미 빠져 있다(틈 재현 확인).
- 엔트리와 다르게 한 것과 이유:
  1. inherit 의 확인을 Global 만이 아니라 모든 범위에 같은 원칙(그 범위에서 꺼짐 → 켜짐)으로 적용 — 결정 i 의 "어느 범위에서든 켜기"에 맞고, 프로젝트 force_off 를 inherit 으로 지워 다시 켜는 길을 막는다. 같은 상태 반복은 no-op(쓰기·확인 없음). 행 없는 Global 에 on 은 행을 쓰므로 확인을 받는다(실효 상태는 그대로).
  2. hook_script 만들기는 거부(`not_created`) — 플랜 §12 의 만들기 목록에 없다. hook 스크립트는 채택으로만 들어오고 앱에서는 텍스트 편집·켜기/끄기·삭제.
  3. 받지 않는 필드를 거부하는 엄격한 요청 모양, 이름 겹침(대소문자·안팎) 거부, 미리보기 best-effort — 셋 다 리뷰(서브에이전트)가 짚은 것과 해시 유일성 때문.
  4. 새 오류·경고 코드: `LADO_SETUP_CHANGE_INVALID`, `LADO_SETUP_CONTENT_INVALID`, `LADO_SETUP_LOGIN_KEY`, `LADO_SETUP_ITEM_EXISTS`, `LADO_SETUP_FOLDER_ITEM`, `LADO_SETUP_CONFLICT_NOT_FOUND`; 경고 `LADO_SETUP_MANAGED_WRITE_FAILED`, `LADO_SETUP_PREVIEW_INCOMPLETE`.
  5. 고정 테스트는 승인 체인 대신 원시 연결(FK 끔)로 `lado_policy_snapshots`·`lado_policy_snapshot_setup` 행을 넣는다 — 지우기가 읽는 것은 고정 행뿐이다.
- 확인한 명령과 결과: `go test -count=1 ./internal/service/ladosetup/` ok(≈4.5 s), 이웃 패키지 `domain`·`store`·`ladopolicy`·`ladotask` ok, golangci-lint v2.13.2(오프라인 빌드)로 `ladosetup`·`domain`·`store` 0 issues, 6a 변이 확인(위 14). 리뷰 서브에이전트 1회 — 반영한 것: 이름 겹침, 미리보기 best-effort, 테스트 보강(거부 시 무기록, 취소된 태스크, 한 파일 hook 폴더, 잃은 blob), 문서 주석, `bytes.Equal`.
- 게이트(코드 커밋 `0b0bab84e` 에서): G0 `DEFS_UNCHANGED`, G1 `GO_TESTS_OK 9/9`, G2 `GO_RACE_OK`(≈58 s), G3 `NO_HOME_LOOKUPS`, G4 `GO_BUILD_OK`. G5 와 전체는 엔트리 커밋 뒤 `gate-check.sh --run` 으로.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 변경 서비스(8개 op, dry run, changeSha256, 데스크톱 확인, 감사, blob → store → 관리 폴더 → 캐시), 결정 i 의 settings 키 셋, 6a, 필수 테스트 9개. HTTP·화면은 없다. 엔트리 밖은 After 의 "다르게 한 것" 1~5 와 store 읽기 하나.
- 남은 틈:
  - **결정 i 의 틈(리뷰가 짚음, 소유자 결정 사항)**: 삭제·끄기는 확인 없이 api 다 — `permissions` 의 deny 를 담은 항목을 지우거나 끄면 접근이 넓어지고, `disableAllHooks` 같은 키를 끄는 것도 같다. SKILL.md·agent·command 의 front matter(`hooks`, `allowed-tools`)는 확인 없이 만들어진다(최근 Claude Code 가 front matter hook 을 읽는지는 실측 안 함). presence settings 목록은 허용 목록이 아니라 거부 목록이라 `sandbox` 같은 키가 빠진다. 넓히려면 `ladoSetupPresenceSettings`, `prepareDelete`·`prepareBinding` 의 판정, 텍스트 종류의 front matter 검사만 바꾸면 된다. S17 이 문서에 소유자 확인 사항으로 적는다.
  - 충돌 증감에 권한 천장 검사(`DEFAULT_MODE_ABOVE_CEILING`)가 없다 — ladosetup 은 규칙을 읽지 않는다. 천장이 필요하면 `scopeProblems` 의 `ConflictInput.Ceiling` 에 넣는다(정책이 천장을 내주는 길이 생기면).
  - drift keep 은 기준 버전 없이 `DriftSHA256` 만 본다: 검토와 유지 사이 다른 변경이 버전을 더하면 그 위에 얹힌다(관리 사본 해시가 같을 때만).
  - Windows 예약 이름(`CON` 등)·끝이 점이나 공백인 이름은 거부하지 않는다(Windows 채택은 9단계까지 거절).
  - 스킬 front matter 의 `name` 을 폴더 이름과 대조하지 않는다 — `LADO_SETUP_SKILL_NAME_TAKEN` 은 항목 이름(폴더)만 본다.
  - 실제 만들기·편집·지우기는 쓰기 잠금 안에서 전체 검사(`Scan`)를 한 번 더 돈다(채택과 같은 꼴).
  - `changeFailed` 의 `ErrLadoSetupItemExists` 는 새 항목 id 를 details 에 싣는다 — 쓰기 잠금 때문에 지금은 일어나지 않는다.
- 다음 단계 메모:
  - **S13(라우트 → Op)**: 컨트롤러는 그 op 가 받는 필드만 채운다(그 밖은 `not_taken`). `content` 는 요청의 JSON 값 그대로(`json.RawMessage`). 모든 변경 라우트에 `c.presenceCheck(r, body)` 를 넘기면 서비스가 필요할 때만 부른다(헤더가 없으면 그 함수가 403).
    - `POST /lado/setup/items` {tool, kind, name, content, dryRun} → `OpCreate`
    - `PATCH /lado/setup/items/{id}` {baseVersion, content, dryRun} → `OpEdit`(ItemID = 경로)
    - `DELETE /lado/setup/items/{id}`(dryRun 은 쿼리나 본문 — S13 이 정함) → `OpDelete`
    - `PUT /lado/setup/bindings` {itemId, scope, scopeId, state, dryRun} → `OpBinding`(itemId → ItemID, 나머지 → Binding; state 는 on·off·inherit·force_on·force_off)
    - `POST /lado/setup/items/{id}/drift` {action keep|revert, sha256, dryRun} → `OpDriftKeep`(DriftSHA256) / `OpDriftRevert`
    - `POST /lado/setup/conflicts/acceptances` {itemId, code, detail, scope, scopeId, dryRun} → `OpAccept`; `DELETE /lado/setup/conflicts/acceptances/{id}` → `OpRevoke`
    - 배선: 정책 서비스를 만든 뒤 `setupSvc.SetPolicy(policySvc)`.
    - 오류 status: `CHANGE_INVALID`·`CONTENT_INVALID`·`LOGIN_KEY`·`CONFLICT_NOT_ACCEPTABLE`·`LADO_BINDING_*` 400, `NOT_FOUND`·`CONFLICT_NOT_FOUND`·`LADO_SCOPE_NOT_FOUND` 404, `ITEM_EXISTS`·`FOLDER_ITEM`·`ITEM_CHANGED`·`CHANGED`·`DRIFT_UNREADABLE`·`LADO_TASK_CANCELLED` 409, `NOT_ADOPTABLE`(Codex 서버 keep) 400, `LADO_PRESENCE_*` 403, `UNAVAILABLE` 503. details 키: op, field, reason, key, limit, tool, kind, name, item, version, code, detail, scope, scopeId, state, task, sha256, error.
    - 응답 DTO: `ChangeResult` 그대로 — `before`/`after` 는 `{contentKind, sha256, size, executable, text, files[{path, mode, size, sha256}]}`, 충돌은 S06 `Conflict` 꼴, 영향은 규칙 영향 DTO 재사용.
  - **S14(dry run → 확인 → 적용)**: 같은 요청을 `dryRun:true` 로 보내 `changeSha256`·`needsPresence`·항목(이름·종류)을 받고, `needsPresence` 면 확인 창 뒤 `dryRun` 을 뺀 같은 요청에 서명(subject = `changeSha256`), 아니면 확인 없이 같은 요청. 번역할 reason: missing, not_taken, unknown, invalid, not_for_tool, not_created, not_text, too_large, not_json, not_object, front_matter(field name·description), server_shape(field type·command), name_unsupported, literal_credentials, unreadable; 경고 2개.
  - **S16(화면)**: 차이는 `before.text`·`after.text`, 폴더는 `files`. dry run 의 새 항목·버전·수락은 id 가 비어 있다. `LADO_SETUP_PREVIEW_INCOMPLETE` 면 "일부를 보여 줄 수 없음". 만들기 종류에 hook 스크립트는 없다.
  - **S17(문서)**: 결정 i 의 실제 판정(모든 범위의 inherit 규칙, 행 없는 Global on), 위 남은 틈 첫 항목을 소유자 확인 사항으로, hook 스크립트는 만들 수 없음.
