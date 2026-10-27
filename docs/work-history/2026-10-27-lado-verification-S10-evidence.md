# 2026-10-27 LADO 검증·통합 승인 — S10 근거: 워커 근거 파일·고정·대조

> 체인 `docs/chains/lado-verification.tsv` · 승인 플랜 `docs/work-history/2026-10-27-lado-verification-plan.md`(§1 결정 k, 소유자 결정 3, §10) · 브랜치 `feat/lado-verification`

## 의도/목적 — 왜 이 작업을 하나

EVD-001·EVD-002: 계획의 인수 기준마다 무엇이 그것을 보여 주는지 연결하고, 근거가 없으면 "추론"으로 드러낸다. 워커가 산출물 폴더에 `lado/evidence.md` 를 쓰고(소유자 결정 3), LADO 는 그 주장을 실제 기록(PR 검사, LADO 검사 실행, 커밋)과 대조한다. 다른 head 를 설명하는 근거는 오래된 것으로 표시한다. 근거는 통합을 막지 않는다(결정적 검사가 정한다) — 승인 확인 창에 개수로 보인다.

## 범위

- 만든다: `service/ladoverify/evidence.go` + 테스트.
- 바꾼다: `service/ladoqueue/prompt.go`(워커 프롬프트 리드에 문단) + 기존 프롬프트 테스트.
- 하지 않는다: 루프에서 부르기(S11), 화면(S17).

## 절차

0. 시작 전에 앞 단계 엔트리들(같은 체인의 S01~직전)의 `## 비교/회고` 와 그 안의 "다음 단계 메모"를 읽고, 이 단계에 해당하는 것을 따른다(러너는 직전 커밋 요약만 넘겨 준다).
1. 참고: `service/ladotask/planwatch.go`(`Artifacts.Versions`·`Read`, sha256 재확인, blob 고정), `domain/lado_plan.go`(YAML front matter 읽기, `gopkg.in/yaml.v3`).
2. 워커 프롬프트(`workerPrompt`): 계획 본문 뒤에 영어 문단 하나 — LADO 가 PR head 에서 확인하는 것(필수 CI 검사, 프로젝트 검사 명령, 커밋 메시지 규칙이 켜졌으면 그것, 변경 경로와 보호 경로), 실패하면 LADO 가 메시지를 보낸다는 것, 그리고 `ao report --done` 전에 세션 산출물 폴더에 `lado/evidence.md` 를 이 형식으로 쓰라는 것:
   ```markdown
   ---
   head: <the full SHA of the commit this evidence describes>
   criteria:
     - criterion: 1            # the 1-based number of the acceptance criterion in the approved plan
       evidence:
         - check: <a CI check name on the pull request>
         - command: <a LADO check command name>
         - commit: <a commit SHA in this branch>
       note: <one line>
   ---
   ```
   기존 프롬프트 박제 테스트(`TestDispatchSpawnsAnApprovedTask` 등)를 새 글에 맞춘다.
3. `evidence.go`:
   ```go
   type EvidenceEntry struct{ Check, Command, Commit string }
   type EvidenceCriterion struct{ Criterion int; Evidence []EvidenceEntry; Note string }
   type EvidenceFile struct{ Head string; Criteria []EvidenceCriterion }
   type EvidenceFileRef struct{ Session domain.SessionID; Path string; Version int; SHA256 string }
   func ParseEvidence(data []byte) (EvidenceFile, error)
   type Artifacts interface { /* ladotask 의 Artifacts 와 같은 모양: Versions(ctx, session, path), Read(ctx, version) */ }
   type Blobs interface { /* ladoblob.Store 의 쓰기·읽기 메서드 이름 그대로 */ }
   func PinEvidence(ctx context.Context, artifacts Artifacts, blobs Blobs, session domain.SessionID) (*EvidenceFileRef, []byte, error) // 없으면 nil, nil, nil
   type EvidenceInput struct {
       File          *EvidenceFile
       FileRef       *EvidenceFileRef
       CriteriaCount int
       HeadSHA       string
       Checks        []domain.PullRequestCheck // 이 head
       Runs          []domain.LadoCheckRun     // 이 head
       Commits       []string                  // merge-base..head
   }
   func ResolveEvidence(in EvidenceInput) ([]domain.LadoEvidenceLink, domain.LadoVerifyItem, error)
   ```
   - 파싱: front matter 만 읽는다(본문 무시), 모르는 키 무시, 형식이 깨지면 오류(호출자는 모든 기준을 `inferred` 로).
   - 고정: 최신 버전의 바이트를 읽어 AO 기록의 sha256 과 대조(다르면 거부·로그), blob 에 넣는다(1 MiB 넘으면 무시).
   - 대조(플랜 §1 k): `check` → 그 이름(대소문자 무시)의 head 검사 중 `passed` 가 있으면 `pr_check`(key = 검사 이름, sha = head); `command` → 그 이름의 head 실행 중 `passed` 면 `lado_check_run`(key = 실행 id); `commit` → 범위 안 커밋(앞부분 7자 이상 일치)이면 `commit`(sha = 전체); 아니면 `claim`(key = 원문). 기준 상태: 해결된 것이 하나라도 → `linked`(파일 `head` ≠ PR head 면 `stale`), 항목은 있으나 해결 없음 → `claimed`, 목록에 없음 → `inferred`(ref `none`). 범위 밖 기준 번호는 무시하고 세부에 적지 않는다. 파일 자체는 기준 0, 상태 `file`, ref `artifact_version`(key `<session>/<path>@<version>`, sha = 파일 sha256).
   - 항목: 늘 info, 코드 없음, 세부 `LadoEvidenceDetails`(파일 버전·sha·head, 기준별 상태와 refs). 파일이 없으면 모든 기준 `inferred`.
4. 커밋 메시지 예: `feat(lado): link acceptance criteria to evidence the worker names`.

## 마무리 (모든 단계 공통)

1. 코드 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-27-lado-verification-S10-evidence.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `TestEvidenceParses` — 올바른 파일, 모르는 키, 깨진 YAML 오류, front matter 없음 오류
- `TestEvidenceResolvesAgainstRecords` — 통과한 검사 → linked `pr_check`; 실패한 검사 → claimed; 통과한 명령 → linked `lado_check_run`; 범위 안 커밋(짧은 SHA) → linked `commit`; 범위 밖 커밋 → claimed; 목록에 없는 기준 → inferred; 파일 head ≠ PR head → stale; 파일 행(기준 0)
- `TestEvidencePinChecksTheHash` — 가짜 산출물: sha256 불일치 거부, 일치하면 blob 에 들어가고 ref 가 버전·sha 를 가짐, 파일 없으면 nil
- `TestWorkerPromptAsksForEvidence`(`service/ladoqueue`) — 프롬프트에 `lado/evidence.md`, `criteria:`, `head:` 와 LADO 가 확인하는 것들, 계획 본문은 그대로

## 수용 게이트

- [ ] S10-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-verification.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S10-G1: 근거 테스트 3개가 격리된 홈에서 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests-isolated ./internal/service/ladoverify/ TestEvidenceParses TestEvidenceResolvesAgainstRecords TestEvidencePinChecksTheHash
  EXPECT: GO_TESTS_OK 3/3
- [ ] S10-G2: 워커 프롬프트 테스트 통과
  CHECK: bash .claude/gate/lado-verification.sh go-tests ./internal/service/ladoqueue/ TestWorkerPromptAsksForEvidence
  EXPECT: GO_TESTS_OK 1/1
- [ ] S10-G3: 디스패처 패키지 전체 통과
  CHECK: bash .claude/gate/lado-verification.sh go-pkg ./internal/service/ladoqueue/
  EXPECT: GO_PKG_OK
- [ ] S10-G4: 전체 빌드·vet
  CHECK: bash .claude/gate/lado-verification.sh go-build
  EXPECT: GO_BUILD_OK
- [ ] S10-G5: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-verification.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(단계 시작 때 채운다: 시작 HEAD, 환경, 출발점 실측, 앞 단계 메모 중 이 단계 몫)

## After — 수행 후 실측

(커밋, 바꾼 것, 엔트리와 다르게 한 것과 이유, 확인한 명령과 결과, 게이트 결과)

## 비교/회고

(범위 대비 결과, 남은 틈, 다음 단계 메모 — Artifacts·Blobs 인터페이스의 실제 메서드 이름, 프롬프트 문단 원문)
