# 2026-10-26 LADO 로컬 설정 채택 — S08 복원·변경 감지

> 체인 `docs/chains/lado-local-setup.tsv` · 승인 플랜 `docs/work-history/2026-10-26-lado-local-setup-plan.md`(§1 결정 n·o, §8) · 브랜치 `feat/lado-local-setup`

## 의도/목적 — 왜 이 작업을 하나

HAR-015 와 HAR-014. 채택은 되돌릴 수 있어야 하고(백업을 바이트 그대로, 새것은 덮지 않음), 채택 뒤 원래 자리에 다시 생긴 것과 관리 폴더를 바깥에서 고친 것은 보여 주되 저절로 옮기거나 반영하지 않는다. 복원은 채택과 함께 나가야 하는 짝이다(HARNESS §15 항목 9 의 뒷절반, 항목 15).

## 범위

- 만든다: `service/ladosetup/restore.go`, `service/ladosetup/drift.go`, 테스트.
- 하지 않는다: 유지(keep)가 새 버전을 쓸 때의 데스크톱 확인 규칙과 HTTP(S12·S13) — 이 단계의 유지·되돌리기 함수는 S12 가 감싼다.

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 복원(`Restore(ctx, adoptionID, presence)`): 전제 조건은 채택과 같다(S07 함수 재사용; OS 미검증이어도 복원은 허용 — 되돌리는 길은 늘 열려 있어야 함, 이 예외를 After 에 적는다). 데스크톱 확인 `setup.restore`(subject 채택 id). 이미 복원 → 409 `LADO_SETUP_ALREADY_RESTORED`.
2. 매니페스트 항목마다:
   - 파일·폴더: 원래 경로가 비었으면 백업 바이트·mode·mtime 그대로 쓴다(폴더는 트리 전체). 무언가 있으면 그대로 두고 백업을 `<이름>.lado-restored` 로 옆에 쓴다(이미 있으면 `.lado-restored-2` …). 쓴 파일마다 매니페스트 sha256 과 대조.
   - 키(`settings.json`·`~/.claude.json`·`config.toml`): 백업 파일에서 그 키의 값을 읽어 지금 파일에 그 키가 없을 때만 넣는다(S07 의 `jsonedit`/`tomledit`). 있으면 보고서에 "kept" 로. 파일 자체가 없으면 백업 파일 전체를 쓴다.
3. 보고서(JSON): 항목마다 `restored`|`restored_beside`(옆 경로)|`key_kept`|`failed`(이유). store `RestoreLadoSetupAdoption`(감사 `setup.restored`, payload 에 개수). 하나라도 `failed` 면 store 를 쓰지 않고 오류(`LADO_SETUP_RESTORE_FAILED`, details 의 첫 실패) — 이미 쓴 파일은 그대로 두되 다시 복원하면 같은 규칙으로 마저 한다(멱등).
4. 복원 뒤: 그 채택의 항목은 `restored_at` 이라 더는 해석·전달되지 않는다. 관리 폴더의 그 파일은 지우지 않는다(blob·백업도 남음). 인벤토리를 다시 검사하면 원래 위치의 항목이 `adoptable` 로 보인다.
5. 변경 감지(`Drift(ctx)`, 플랜 §8):
   - `edited`: 관리 폴더의 파일·폴더가 그 항목 최신 버전의 해시와 다름(폴더는 트리 매니페스트로 비교).
   - `new_local`: 원래 위치에 adoptable 항목이 있는데 기준선(S07 의 `setup-baseline.json`)에 없거나 해시가 다름 — 예: Claude 를 직접 써서 `settings.json` 에 키가 새로 생김, 새 스킬. 저절로 옮기지 않는다.
   - 검사 시점: 데몬 시작(S13 배선), 요청 시, LADO 가 관리 폴더나 원래 위치를 쓴 뒤. 결과는 인벤토리 캐시의 `Drift`.
6. 유지·되돌리기 함수(S12 가 감쌈): `KeepDrift(ctx, itemID, sha256)` → 관리 내용으로 새 버전(blob·store, 감사 `setup.drift_kept`); `RevertDrift(ctx, itemID)` → 최신 버전으로 관리 파일 다시 씀(감사 `setup.drift_reverted`). `sha256` 이 지금 관리 내용과 다르면 409 `LADO_SETUP_CHANGED`.
7. 해석·홈은 버전만 읽는다는 것을 테스트로 고정한다: `edited` 상태여도 유효 항목의 버전 id·sha256 은 그대로.
8. 커밋 메시지 예: `feat(lado): restore adoptions and detect setup drift`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-local-setup-S08-restore-drift.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestRestoreIsByteIdentical` — 채택 → 복원 → 파일·폴더 바이트, mode, mtime 이 채택 전과 같음(PRD gate 14)
- `TestRestoreKeepsBothWhenOccupied` — 그 자리에 새 파일이 있으면 그대로, 백업은 `.lado-restored`, 보고서에 옆 경로
- `TestRestorePutsKeysBackOnlyWhereAbsent` — 없는 키만 넣음, 있는 키는 `key_kept`, 다른 키·순서 유지
- `TestRestoreStopsDelivery` — 항목 `restored_at`, 유효 항목 목록에서 빠짐, 버전은 읽힘, 두 번째 복원 409
- `TestRestoreIsAllowedOnAnUnverifiedOS` — 미검증 OS 에서도 복원 가능, 채택은 거부
- `TestDriftShowsNewLocalItems` — HARNESS §15 항목 15: 채택 뒤 `settings.json` 에 새 키 → `new_local`, 원래 파일·관리 폴더 그대로
- `TestDriftEditedManagedFile` — 관리 파일 수정 → `edited`; 유지 → 새 버전(감사), 되돌리기 → 파일이 최신 버전 바이트
- `TestDriftNeverChangesResolution` — `edited` 상태에서 유효 항목의 버전·해시 그대로

## 수용 게이트

- [ ] S08-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-local-setup.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S08-G1: 필수 테스트 8개가 격리된 홈에서 돌고 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-tests-isolated ./internal/service/ladosetup/ TestRestoreIsByteIdentical TestRestoreKeepsBothWhenOccupied TestRestorePutsKeysBackOnlyWhereAbsent TestRestoreStopsDelivery TestRestoreIsAllowedOnAnUnverifiedOS TestDriftShowsNewLocalItems TestDriftEditedManagedFile TestDriftNeverChangesResolution
  EXPECT: GO_TESTS_OK 8/8
- [ ] S08-G2: 패키지 전체가 격리된 홈에서 race 없이 통과
  CHECK: bash .claude/gate/lado-local-setup.sh go-race-isolated ./internal/service/ladosetup/
  EXPECT: GO_RACE_OK
- [ ] S08-G3: 패키지가 홈·도구 폴더를 스스로 찾지 않는다
  CHECK: bash .claude/gate/lado-local-setup.sh no-home-lookups backend/internal/service/ladosetup
  EXPECT: NO_HOME_LOOKUPS
- [ ] S08-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-local-setup.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S08-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-local-setup.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD `a15912843`(docs(lado): record S07), 브랜치 `feat/lado-local-setup`, 트리 깨끗. 단계 중 메인 세션이 `fca7d47a4`(S14·S15 엔트리 문구, 이 엔트리는 안 건드림)를 커밋했다.
- 환경: Linux(Ubuntu), go1.27.1(scratchpad 툴체인), golangci-lint v2.13.2(rules-core 스크래치 빌드, `GOPROXY=off`), sqlc v1.31.1(모듈 캐시, `npm run sqlc`). 실제 `~/.claude`·`~/.claude.json`·`~/.codex` 는 읽지도 쓰지도 않았다 — 테스트는 `t.TempDir()` 가짜 홈, 게이트는 격리 홈.
- 출발점: `service/ladosetup` 에 채택(S07)까지 — 복원·변경 감지·유지·되돌리기 없음, `Inventory` 에 `Drift` 없음. store 에 채택별 항목 목록 읽기가 없었다(live 목록뿐이라 partial 채택이 실제로 지운 항목, S12 가 지울 항목을 알 수 없음).
- 앞 단계 메모 중 이 단계 몫: S03(`LadoSetupDrift` 꼴, presence `setup.restore` subject = 채택 id), S04(복원은 지운 항목까지 같은 `restored_at`, 유지는 `AddLadoSetupItemVersion(최신 번호)`), S05(new_local 은 기준선과 비교, 캐시 갱신은 `s.Scan`), S07(백업·매니페스트 꼴과 `ManifestSHA256` 대조, 키 항목 key/member, env 의 `keptInPlace` 합치기, `restoreCopy`, `preconditions`/`runningDetails`, `lockWrite`, 기준선 꼴, `managedSum`/`checkManaged`/`writeManaged`).

## After — 수행 후 실측

- 커밋: `810cff105` feat(lado): restore adoptions and detect setup drift · `c25d9efe9` fix(lado): review fixes for setup restore and drift · 이 엔트리 커밋.
- 바꾼 것(`backend/internal/service/ladosetup/` 와 store):
  - `restore.go` — `Restore(ctx, adoptionID, presence) (RestoreResult, error)`. 순서: `available` → `lockWrite` → `restoreTarget`(없음 404 `LADO_SETUP_NOT_FOUND`, 이미 복원 409 `LADO_SETUP_ALREADY_RESTORED`, manifest.json sha256 = 채택 `ManifestSHA256`, store 가 기록한 항목만 매니페스트 순서로) → `confirmRestore`(S07 `preconditions` — `Unverified` 는 무시, 세션·프로세스는 409 `LADO_SETUP_TOOL_RUNNING`, presence 없으면 403, `setup.restore`/채택 id) → `WithoutCancel` → `putBackOriginals` → 하나라도 실패면 기록 없이 500 `LADO_SETUP_RESTORE_FAILED`(details `stage: put_back` 와 첫 실패의 tool·kind·name·root·path·error) → `recordRestore`(store `RestoreLadoSetupAdoption`, 보고서 JSON, 감사 `setup.restored` payload items·restored·restoredBeside·keyKept·processesIgnored·proof·dev) → `addToBaseline`.
  - 파일·폴더: 백업 복사본을 매니페스트와 다시 대조 → 원래 경로가 원본을 이미 가지면 restored(쓰기 없음), 비었으면 형제 임시 이름에 원래 mode·mtime 으로 쓰고 매니페스트와 대조한 뒤 rename(그 사이 누가 차지하면 쓰지 않음), 차 있으면 예전 복원이 옆에 쓴 원본 사본을 찾고 없으면 첫 빈 이름 `<이름>.lado-restored`, `-2` … `-99`.
  - 키: 파일 하나씩. 파일이 채택이 남긴 그대로(백업에 `withoutAdoptedKey` 를 백업 순서로 다시 적용한 바이트와 같음)면 백업 파일을 통째로(원래 mode·mtime) — 바이트까지 되돌아온다. 아니면 키마다: 없으면 백업의 원래 값(쓴 그대로)을 백업에서 이웃했던 키 옆에(`insertMember`), 있고 같으면 restored, 다르면 key_kept. env 는 채택이 가져간 변수만 없는 이름에 합친다(로그인 변수는 안 되살림, 다른 값의 변수가 있으면 key_kept). Codex 는 백업에서 `tomlWithoutServer` 로 블록을 잘라 `tomlWithBlock`. 고쳐 쓰기 직전 파일이 읽은 그대로인지 다시 본다(`replaceFile`). 쓴 뒤 다시 읽어 키마다 매니페스트 sha256(env 는 변수별)과 대조.
  - 보고서 `RestoreReport{format "lado-setup-restore/1", adoptionId, entries[{itemId, tool, kind, name, root, path(루트 기준 슬래시, 키는 "#<키>"), status restored|restored_beside|key_kept|failed, beside, error}]}` — 절대 경로 없음.
  - `drift.go` — `Drift(ctx)`(다시 검사, 결과 = `Inventory.Drift`), `editedDrift`(live 항목마다 `managedSum`+`compareManaged`, 못 읽으면 SHA256 ""), `newLocalDrift`(기준선 없으면 없음; 원래 위치의 adoptable 줄이 기준선에 없거나 해시가 다름; 같은 이름 live 항목 id 를 붙임), `setBaseline`(스캔 잠금 안에서 쓰고 캐시의 new_local 을 새 기준선으로 다시 계산), `KeepDrift(ctx, itemID, sha256)`/`keepDrift(…, approval)`, `RevertDrift(ctx, itemID)`, `liveItem`, `readManaged`, `checkKeepable`, `managedKeyFileParses`, `rewriteManagedKeyFile`, `rescan`. 코드 `LADO_SETUP_NOT_FOUND`·`LADO_SETUP_ITEM_CHANGED`·`LADO_SETUP_DRIFT_UNREADABLE`, 감사 `setup.drift_kept`(버전과 한 트랜잭션)·`setup.drift_reverted`(`AppendLadoAuditEvent`), actor `api`, subject kind `setup_item`.
  - S07 파일: `adopt.go`(`available`·`approvalOf` 로 묶음, `saveBaseline` → `setBaseline`), `backup.go`(`checkBackupCopy` 를 `verifyBackup` 에서 분리 — 복원이 다시 씀), `managed.go`(`compareManaged` 분리), `inventory.go`(scan 이 Drift 계산), `service.go`(Store 인터페이스 6개 더함, `Inventory.Drift`), `jsonedit.go`(`memberKeys`·`insertMember`·`jsonInsert`).
  - store: `queries/lado_setup.sql` `ListLadoSetupItemsForAdoption`(ASCII), `npm run sqlc`(gen 추가만), `ListLadoSetupItemsForAdoption(ctx, id)`, `TestLadoSetupItemsForAdoption`.
  - 테스트: 필수 8개 + `TestRestoreFinishesWhatAFailedRestoreLeft`, `TestRestoreOfAPartialAdoption`, `TestRestoreBringsBackOnlyAdoptedKeysIntoAGoneFile`.
- 엔트리와 다르게 한 것과 이유:
  1. store 에 읽기 하나(`ListLadoSetupItemsForAdoption`): live 목록만으로는 partial 채택이 지운 앞쪽 항목과 나중에 지워진 항목을 알 수 없다. 기록된 항목만 복원하고, 채택이 지우지 않은 원본은 건드리지 않는다(보고서에도 없음).
  2. 키 파일 자체가 없으면 "백업 파일 전체"가 아니라 채택한 키만 새 파일(원래 mode)에 쓴다: 백업 전체를 쓰면 개발자가 파일과 함께 지운 로그인 키(`apiKeyHelper`)·로그인 변수·`~/.claude.json` 의 앱 상태(`oauthAccount` 등)가 되살아난다 — "자격 증명과 앱 상태는 건드리지 않는다"와 어긋난다(리뷰 지적). 대신 파일이 채택이 남긴 그대로면 백업을 통째로 써서 바이트·mtime 까지 되돌린다(엔트리 밖 추가 — 남는 내용이 같으므로 안전).
  3. 원래 자리(또는 옆 사본)에 같은 내용이 이미 있으면 restored(쓰기 없음): 다시 돌린 복원이 사본을 또 만들지 않는다(멱등).
  4. 실패가 있어도 나머지 항목은 계속 복원하고 보고서에 모두 싣는다(오류 details 는 첫 실패).
  5. 복원 뒤 기준선은 통째로가 아니라 복원이 되돌린 경로만 더한다(restored·restored_beside 와 통째로 쓴 키 파일의 모든 키). key_kept 는 개발자 도구가 쓴 키라 new_local 로 남는다. 채택은 플랜 §7.6 대로 통째로 다시 저장한다(S07 그대로).
  6. 유지의 확인 규칙(S12 몫)은 만들지 않았지만, presence 종류를 증명 없이 유지하면 403 으로 막는다 — 보호 규칙을 우회하는 공개 함수가 생기지 않게. 증명이 있는 유지는 `keepDrift(ctx, id, sum, approvalOf(proof))`.
  7. 유지 거부를 더했다: 못 읽는 사본·형태가 바뀐 사본(파일 항목 자리에 폴더)은 409 `LADO_SETUP_DRIFT_UNREADABLE`(되돌리기만 됨), 비밀 값이 든 Codex 서버는 400 `LADO_SETUP_NOT_ADOPTABLE`(reason `literal_credentials`). 내용이 이미 최신 버전이면 아무것도 안 쓰고 그 버전을 돌려준다.
  8. 되돌리기: 사본이 이미 최신이면 쓰기·감사 없음. 관리 키 파일이 파싱되지 않으면 live 항목들의 버전으로 그 파일을 다시 만든다(파싱되는 파일의 다른 편집은 버리지 않고 오류).
  9. S07 의 틈: 채택 뒤 캐시의 drift 가 옛 기준선으로 계산돼 있었다 — `setBaseline` 이 스캔 잠금 안에서 캐시의 new_local 을 다시 계산한다.
- 확인한 명령과 결과: golangci-lint v2.13.2 `run ./internal/service/ladosetup/... ./internal/storage/sqlite/store/...` 0 issues(처음 goimports 1·unparam 1 고침); `gofmt -l` 출력 없음; `GOOS=windows`·`GOOS=darwin go vet ./internal/service/ladosetup/` ok; `go test ./internal/storage/sqlite/ ./internal/storage/sqlite/store/` ok(42 s); `queries-ascii` → QUERIES_ASCII. 변이 16개(mtime 을 지금으로, key_kept 를 restored 로, 통째 쓰기 끔, 해시 무시, presence 가드 제거, OS 로 복원 막기, 기록 안 된 항목 포함, 원본 알아보기 끔, edited 끔, 기준선 갱신 끔, env 합치기 끔, 위치 무시, 되돌리기 쓰기 끔, 형태 검사 끔, key_kept 를 기준선에, 없는 파일 통째로)를 넣어 모두 테스트가 잡는 것을 봤다(원복 확인).
- 리뷰(foreground code-reviewer, 수정 없이 보고만): 중요 5·낮음 3·플랜 2 → staged 대조 뒤 rename·옆 사본 재사용·없는 키 파일·key_kept 기준선·유지 형태 검사·되돌리기 재작성 조건·`WithoutCancel` 반영(`c25d9efe9`), 비원자 rename 은 일부(키 파일 재확인), 나머지는 남은 틈.
- 게이트(직접 실행, 커밋 뒤 gate-check 로 다시 기록): G0 `DEFS_UNCHANGED` · G1 `GO_TESTS_OK 8/8` · G2 `GO_RACE_OK`(29 s) · G3 `NO_HOME_LOOKUPS` · G4 `GO_BUILD_OK` · G5 엔트리 커밋 뒤 `TREE_CLEAN`.

## 비교/회고

- 범위 대비 결과: 엔트리 범위 — 복원(바이트 그대로·옆에 `.lado-restored`·키는 없을 때만·미검증 OS 에서도·멱등)과 변경 감지(edited·new_local, 유지·되돌리기, 해석은 버전만), 필수 테스트 8개. 데스크톱 확인 흐름과 HTTP 는 없다. 엔트리 밖: store 읽기 하나, 없는 키 파일 처리, After 의 1~9.
- 남은 틈:
  - "비었음 확인 → rename" 은 원자적이지 않다: Linux rename 과 Windows `MoveFileEx(REPLACE_EXISTING)` 는 그 사이 생긴 파일을 덮는다. 키 파일도 다시 읽기와 rename 사이가 남는다. 전제 조건(도구 미실행)이 막는 창이고 S07 제거와 같은 꼴. 막으려면 파일은 `os.Link`(덮지 않음) 뒤 지우기, 폴더는 `renameat2(RENAME_NOREPLACE)`.
  - 폴더 해시는 실행 비트를 담는다: Windows 는 실행 비트가 없어 Linux 에서 만든 백업·버전의 실행 파일이 든 폴더를 원본으로 알아보지 못한다(옆 사본이 생기고 되돌리기 검증이 실패). 상태를 OS 사이로 옮길 때만 — 9단계 실측 때.
  - 복원이 중간에 실패하면 원래 자리로 돌아온 항목은 기준선에 없어 다음 성공 복원 전까지 new_local 로 보인다.
  - presence 목록이 S12 에서 넓어지기 전에는 `KeepDrift` 가 `otelHeadersHelper`·`enableAllProjectMcpServers`·`enabledMcpjsonServers` 를 확인 없이 유지한다 — HTTP(S13)는 S12 뒤라 노출 전에 닫힌다.
  - S07 쪽(리뷰가 짚음, 고치지 않음): `atomicWrite` 가 rename 성공 뒤 폴더 fsync 실패로 오류를 내면 `removeOriginal` 이 실패로 보고, 그 키는 기록·관리 사본 없이 원래 파일에서 빠진다(백업에만 남고 복원 대상이 아님). 고칠 자리는 `removeOriginal` keys 분기: 오류 뒤 파일이 `out` 해시면 성공으로.
  - 되돌리기는 파싱은 되지만 줄 편집이 안 되는 관리 config.toml(서버를 인라인 표로 바꿈)에서 오류로 끝난다 — 손으로 고쳐야 한다.
- 다음 단계 메모:
  - **S09(홈)**: 복원된 채택의 항목은 `ListLiveLadoSetupItems` 에서 빠진다 — 홈은 live 목록과 고정 버전만 쓰면 된다. 관리 폴더 사본은 복원 뒤에도 남으니 홈을 관리 폴더에서 만들지 말 것(결정 c 그대로).
  - **S11**: 해석은 버전만 — `TestDriftNeverChangesResolution` 이 편집 중에도 live 목록과 인벤토리 managed 줄이 버전 해시인지 고정한다.
  - **S12(감쌀 함수)**: drift_keep → `s.keepDrift(ctx, itemID, DriftSHA256, approvalOf(proof))`(presence 종류는 증명 필수 — 없으면 이 함수가 403), 확인 없는 종류는 `KeepDrift`(actor api)와 같다. drift_revert → `RevertDrift(ctx, itemID)`. 둘 다 `lockWrite` 를 스스로 잡는다 — 채널 잠금이라 S12 가 잡은 채로 부르면 교착이니 잡지 말고 부르거나 잠금 없는 내부 함수로 나눌 것. 코드: `LADO_SETUP_CHANGED`(details item·sha256), `LADO_SETUP_DRIFT_UNREADABLE`(item·error), `LADO_SETUP_ITEM_CHANGED`(item), `LADO_SETUP_NOT_FOUND`, `LADO_SETUP_NOT_ADOPTABLE`(reason literal_credentials·unreadable), 403 `LADO_PRESENCE_REQUIRED`. 반환은 새(또는 그대로인) 버전. 감사 payload: kept `{version, sha256, previousSha256, proof, dev}`, reverted `{version, sha256, previousSha256}`. presence 목록을 넓히면 유지 가드도 따라 넓어진다.
  - **S13(HTTP·데몬 시작 검사)**: 데몬 시작에 `Scan(ctx)` 한 번이면 Drift 까지 계산된다(`wireLadoWork` 의 서비스 생성 직후, 세션 스폰 전 — 실패는 로그만). `GET /lado/setup/inventory` 의 drift = `Inventory.Drift`, `POST /lado/setup/scan` = `Scan`. 복원 라우트는 `Restore(ctx, id, presence 클로저)` — 실패면 결과(`Applied=false`, 보고서)와 오류를 함께 돌려준다: 500 `LADO_SETUP_RESTORE_FAILED`(details stage manifest|put_back|record), 404 `LADO_SETUP_NOT_FOUND`, 409 `LADO_SETUP_ALREADY_RESTORED`(adoption)·`LADO_SETUP_TOOL_RUNNING`, 403. `GET /lado/setup/adoptions/{id}` 의 보고서는 `adoption.RestoreReport`(RestoreReport JSON). Store 는 `LadoSetupAdoption`·`ListLadoSetupItemsForAdoption`·`RestoreLadoSetupAdoption`·`LadoSetupItem`·`AddLadoSetupItemVersion`·`AppendLadoAuditEvent` 까지 필요(`*sqlite.Store` 는 다 가짐).
  - **S14(번역)**: 코드 `LADO_SETUP_NOT_FOUND`·`LADO_SETUP_ALREADY_RESTORED`·`LADO_SETUP_RESTORE_FAILED`·`LADO_SETUP_DRIFT_UNREADABLE`·`LADO_SETUP_ITEM_CHANGED`, 상태 `restored`·`restored_beside`·`key_kept`·`failed`, details 키 `adoption`·`stage`(manifest|put_back|record)·`item`·`sha256`·`error`.
  - **S15·S16**: 보고서 `beside`·`path` 는 루트 기준 상대 경로 — 화면은 인벤토리 `Roots` 로 앞을 붙인다. new_local 에 ItemID 가 있으면 같은 이름의 live 항목이 있어 그대로 채택은 `already_managed` 로 거부된다(그 항목을 먼저 지우거나 유지·되돌리기).
