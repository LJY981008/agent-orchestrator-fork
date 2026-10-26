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

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — S12 가 감쌀 함수, S13 의 데몬 시작 검사 자리)
