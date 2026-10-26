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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S13 이 라우트로 옮길 Op 와 요청 모양, S14 의 dry run → 확인 → 적용 흐름)
