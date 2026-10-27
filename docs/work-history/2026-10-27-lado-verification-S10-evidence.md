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

- 시작 HEAD `526dc7493`(브랜치 `lado8/S10`, 트리 깨끗). 이 레인에는 S01~S05·S08·S14 가 병합돼 있고 S06·S07·S09 는 아직 없다(병렬 레인).
- 환경: Ubuntu, go1.27.1(PATH 의 스크래치 툴체인), umask·TZ 는 러너 기본.
- 출발점 실측: `go build ./...` 초록(4.8 s); `go-pkg ./internal/service/ladoqueue/` → `GO_PKG_OK`; `go-pkg-isolated ./internal/service/ladoverify/` → `GO_PKG_OK`; `defs-unchanged` → `DEFS_UNCHANGED`. `service/ladoverify` 에는 `git.go`·`base.go`·`protection.go`(+ 테스트)만 있다.
- 출발점 코드: `ladotask.Artifacts{Versions(ctx, sessionID, path), Read(ctx, version)}`, `ladotask.Blobs{Put(data) (string, error), Get(sum, limit) ([]byte, error)}`(= `ladoblob.Store`). `workerPrompt(task, in)` 은 리드 두 줄 + `## Approved plan` + 계획 원문으로 끝난다. 디스패처 `Store` 에는 프로젝트 검사 명령 읽기가 없다(실제 store 에는 S03 의 `LadoProjectChecks` 가 있다).
- 앞 단계 메모 중 이 단계 몫: S02 — 세부 `LadoEvidenceDetails{FileVersion, FileSHA256, FileHead, Criteria[{Index, State, Refs[{Kind, Key, SHA}]}]}`, 상태 `LadoEvidenceLinked|Stale|Claimed|Inferred|File`, ref `LadoEvidenceRefPRCheck|ArtifactVersion|CheckRun|Commit|Claim|None`, `NewLadoVerifyItem`. S03 — `ReplaceLadoEvidenceLinks(ctx, session, head, links)` 는 링크의 ID·`CreatedAt` 을 호출자에게서 받고(ID 는 표 전체에서 유일), 세션·head 는 인자로 쓴다. S04 — 스냅숏 `Validators`(`ValidatorCommitMessageLocale`·`ValidatorProtectedPaths`)로 커밋 메시지·보호 경로 검사가 켜졌는지 안다(`lado-policy/2` 는 없음). S08 — `ladoverify` 안 이름 충돌 주의(병렬 레인 S06·S07·S09 의 이름: `EvaluateCommits`·`EvaluatePaths`·`RunCommand`·`EvaluateRequiredChecks`·`Reviews` 등) → 이 단계의 이름은 엔트리가 정한 `Artifacts`·`Blobs` 말고는 모두 `Evidence`/`evidence` 로 시작한다.

## After — 수행 후 실측

- 커밋: `1fda22d51` feat(lado): link acceptance criteria to evidence the worker names (코드), 이 엔트리는 뒤 커밋.
- 바꾼 것:
  1. `service/ladoverify/evidence.go`(새): `EvidenceEntry`, `EvidenceCriterion`, `EvidenceFile`, `EvidenceFileRef`, `EvidenceInput`, `Artifacts`, `Blobs`, `ParseEvidence`, `PinEvidence`, `ResolveEvidence`, `EvidenceMaxBytes`(1 MiB), `ErrEvidenceHashMismatch`. 패키지 안 도우미는 모두 `evidence*`(`evidenceRefs`, `evidenceCheckRef`, `evidenceCommandRef`, `evidenceCommitRef`, `evidenceSameCommit`, `evidenceClaim`, `evidenceText`, 상수 `evidenceMaxRefs`·`evidenceMaxTextRunes`·`evidenceMinSHAChars`).
  2. `domain/lado_verify.go`: 상수 `LadoEvidencePath = "lado/evidence.md"` 하나.
  3. `service/ladoqueue/prompt.go`: `workerPrompt(task, in, checks)` — 계획 원문 뒤에 `## Verification and evidence` 절(영어 문단 하나 + 형식 블록 `evidenceForm`). 문단은 `verificationParagraph` 가 스냅숏 validators(`snapshotValidators`)와 검사 명령 이름으로 만든다.
  4. `service/ladoqueue/dispatch.go`·`service.go`: 디스패처 `Store` 에 `LadoProjectChecks` 를 더하고, 스폰 직전에 읽어 `spawnConfig(…, checks)` 로 넘긴다. 읽기 실패 → 경고 로그 + `LadoWaitingRetry` 로 보류(시도 횟수 안 씀), ctx 취소면 되돌림.
  5. 테스트: `ladoverify/evidence_test.go`(필수 3), `ladoqueue/prompt_test.go`(`TestWorkerPromptAsksForEvidence`, 디스패치를 거쳐 스폰된 프롬프트를 본다), `dispatch_test.go`(`wantPrompt` 박제 갱신, 보류 표에 "check commands that cannot be read" 행), `fakes_test.go`(`checks`·`checksErr`·`LadoProjectChecks`).
- 엔트리와 다르게 한 것과 이유:
  - 범위 밖 파일 셋: `dispatch.go`·`service.go` — 검사 명령 이름은 프로젝트 설정에만 있어 디스패처가 읽어 넘겨야 한다(플랜 §10 "the project's check command names"). `domain/lado_verify.go` — 경로 상수를 ladoqueue·ladoverify 가 함께 쓰도록 domain 에(`LadoPlanPath` 와 같은 자리). ladoqueue 가 ladoverify 를 import 하면 S11 이 ladoverify 에서 ladoqueue 를 쓸 때 순환이 생긴다.
  - 문단 위치는 절차 2 대로 "계획 본문 뒤"(범위 줄의 "리드" 가 아님). 계획과 섞이지 않게 `##` 머리말을 달았다. 계획 원문이 줄바꿈으로 끝나지 않으면 줄바꿈 하나를 더한다(원문은 그대로).
  - `command` 이름도 `check` 처럼 대소문자 무시. 같은 이름의 통과 실행이 여럿이면 가장 늦게 시작한 실행의 id. 입력에 다른 head 의 검사·실행이 섞여도 무시한다(`CommitHash`·`HeadSHA` 대조, 빈 `CommitHash` 는 허용).
  - 파일 `head` 와 PR head 비교도 커밋과 같은 규칙(16진 7자 이상 접두어, 대소문자 무시). `head` 가 없으면 해결된 기준은 `stale`.
  - 목록에 있지만 항목이 없는(note 만) 기준 → `claimed` + ref `none`(도메인 주석 "none is a criterion without entries").
  - 주장 key 는 종류를 남겨 `check: lint`·`command: …`·`commit: …`(공백 정리, 200자 컷 + `…`). commit ref 는 key = sha = 전체 SHA, 짧은 SHA 가 범위 안 커밋 둘에 맞으면 주장. `lado_check_run` ref 의 sha = head. 기준 하나에 ref 는 20개까지, 같은 ref 는 한 번.
  - 링크의 `ID`·`CreatedAt`·`ExecutionSessionID`·`HeadSHA` 는 비워 둔다 — 호출자가 ID·시각을 채우고 store 가 인자로 실행·head 를 쓴다(S03).
  - 고정: 크기 초과(AO 기록의 `Size` 또는 읽은 바이트) → nil, nil, nil(무시). 해시 불일치 → `ErrEvidenceHashMismatch` 로 감싼 오류(로그는 호출자 몫 — 시그니처에 로거가 없다), blob 에 넣지 않는다.
  - 파싱: 빈 front matter·매핑이 아닌 front matter·타입이 틀린 값(`criteria: 글`, 평범한 문자열 항목)·UTF-8 아님도 오류. 아는 키가 하나도 없는 항목은 버린다. `commit: 1234567` 같은 숫자 값도 글로 읽힌다(yaml.v3, 테스트로 확인).
- 확인한 명령과 결과:
  - `go-tests-isolated ./internal/service/ladoverify/ TestEvidenceParses TestEvidenceResolvesAgainstRecords TestEvidencePinChecksTheHash` → `GO_TESTS_OK 3/3`
  - `go-tests ./internal/service/ladoqueue/ TestWorkerPromptAsksForEvidence` → `GO_TESTS_OK 1/1`; `go-pkg ./internal/service/ladoqueue/` → `GO_PKG_OK`
  - `go-pkg-isolated ./internal/service/ladoverify/ ./internal/domain/` → `GO_PKG_OK`; `go-run-isolated ./internal/daemon/ Lado` → `GO_RUN_OK 17`
  - `go-lint ./internal/service/ladoverify/... ./internal/service/ladoqueue/... ./internal/domain/...` → `GO_LINT_OK`; `go-build` → `GO_BUILD_OK`; `defs-unchanged` → `DEFS_UNCHANGED`
  - 변이 4건(검사 `passed` 조건 제거, stale 판정 제거, 해시 대조 제거, 실행 head 거르기 제거) → 모두 `TestEvidence*` 실패, 원복 뒤 통과.
- 게이트 결과: 코드 커밋 뒤 G0~G4 를 하나씩 돌려 모두 기대값. G5 와 전건은 이 엔트리를 커밋한 뒤 `gate-check.sh --run` 으로 다시 확인한다.

## 비교/회고

- 범위 대비 결과: 절차 1~4 전부, 필수 테스트 4 + 보조 1(디스패치 보류 표의 검사 명령 읽기 실패 행). 루프에서 부르기(S11)·화면(S17)·store·HTTP 는 손대지 않았다. 범위 밖 변경은 After 의 셋(디스패처가 검사 명령을 읽음, domain 경로 상수).
- 남은 틈:
  - `note` 는 파싱하지만 세부·링크 어디에도 저장되지 않는다(`LadoEvidenceCriterion` 에 자리 없음). 화면이 보이려면 고정된 blob(sha = 세부 `fileSha256`)을 다시 읽어 `ParseEvidence` 하면 된다.
  - `PinEvidence` 는 부를 때마다 최신 버전을 읽고 해시한다(최대 1 MiB). 매 패스·실행마다 부르면 그만큼 읽는다 — 같은 head 의 기록이 같은 파일 버전·sha 를 이미 가지면 건너뛰는 일은 S11 몫(`Artifacts.Versions` 만 먼저 보면 된다).
  - 1 MiB 넘는 파일은 조용히 "없음" 이다 — 개발자는 모든 기준이 `inferred` 인 것만 본다.
  - 프롬프트는 디스패치 때의 검사 명령 이름을 담는다. 그 뒤 명령을 바꾸면 워커 글은 옛 이름이다(근거의 `command:` 는 그때 이름으로 대조된다).
  - 리뷰(계획이 요구할 때)는 문단에 넣지 않았다(엔트리 목록에 없음). 워커는 리뷰 결과를 수정 요청 메시지로 받는다(S11).
- 다음 단계 메모(이름 그대로):
  - **인터페이스(S11 `Deps`)**: `ladoverify.Artifacts` = `Versions(ctx context.Context, sessionID domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)` + `Read(ctx context.Context, version domain.SessionArtifactVersion) ([]byte, error)` — `ladotask.Artifacts` 와 같은 모양이라 데몬이 계획 감시에 주는 어댑터를 그대로 넘기면 된다. `ladoverify.Blobs` = `Put(data []byte) (string, error)` + `Get(sum string, limit int64) ([]byte, error)` = `*ladoblob.Store`(테스트가 `var _ Blobs = (*ladoblob.Store)(nil)` 로 고정).
  - **S11 순서**: `ref, data, err := PinEvidence(ctx, artifacts, blobs, session)` — `errors.Is(err, ErrEvidenceHashMismatch)` 면 경고 로그 + 파일 없음으로, 다른 오류는 로그(파일 없음으로 둘지 항목을 미룰지는 S11 이 정한다); `data != nil` 이면 `file, perr := ParseEvidence(data)`(perr 면 `File` nil, `FileRef` 는 그대로 넘겨 기준 0 행을 남긴다) → `ResolveEvidence(EvidenceInput{File, FileRef: ref, CriteriaCount: len(plan.AcceptanceCriteria), HeadSHA: head, Checks: ListLadoCommitChecks(prURL, head), Runs: ListLadoCheckRuns(session, head), Commits: Commits(mergeBase, head) 의 SHA 들})` → 링크마다 `ID`(표 전체에서 유일 — 예 `NewID()`)와 `CreatedAt` 을 채워 `ReplaceLadoEvidenceLinks(ctx, session, head, links)`; 항목은 기록의 마지막(evidence) 항목. 기준 수는 승인된 개정의 계획 blob 을 `domain.ParseLadoPlan` 으로 읽어 `len(AcceptanceCriteria)`.
  - **세부 JSON 예**: `{"fileVersion":3,"fileSha256":"…","fileHead":"…","criteria":[{"index":1,"state":"linked","refs":[{"kind":"pr_check","key":"Build","sha":"<head>"}]},{"index":2,"state":"inferred","refs":[]}]}` — `inferred`·항목 없는 `claimed` 는 세부에서 `refs: []`, 링크 표에는 ref `none` 한 행. 파일 행은 링크에만(기준 0, `file`, `artifact_version`, key `<session>/lado/evidence.md@<version>`, sha = 파일 sha256).
  - **S15·S17 표시**: ref `pr_check` key = 기록된 검사 이름·sha = head, `lado_check_run` key = 실행 id·sha = head, `commit` key = sha = 전체 SHA, `claim` key = `check: <이름>`·`command: <이름>`·`commit: <글>`(200자 컷). 경로 상수 `domain.LadoEvidencePath`.
  - **ladoqueue**: 디스패처 `Store` 에 `LadoProjectChecks(ctx, projectID) ([]domain.LadoCheckCommand, int, error)` 가 더해졌다 — 다른 가짜 Store 를 만드는 단계는 이 메서드가 필요하다. ladoqueue 는 ladoverify 를 import 하지 않는다(그 방향을 열어 두었다).
  - **프롬프트 문단 원문**(검사 명령 `lint`·`unit tests`, 한국어 커밋 규칙·보호 경로 validator 가 모두 있을 때; 명령이 하나면 "check command `x` … runs it", 없으면 그 문장이 빠지고, validator 가 없으면 커밋 문장과 ", and a changed protected path …" 가 빠진다). 계획 원문 바로 뒤:

    ```text
    ## Verification and evidence

    Before the developer approves the integration, LADO verifies your pull request's head. The CI checks its base branch requires must pass, or every check reported on the head when it requires none. The project's check commands `lint` and `unit tests` must pass; LADO runs them in this worktree, so leave it clean at the commit you pushed. Your commit messages and the pull request title must follow the commit message rule. A changed path outside the plan's affected_paths needs the developer's confirmation, and a changed protected path fails unless the plan names it. When a check fails, you get a message: push a fix, and LADO checks the new head. Before you run `ao report --done`, write `lado/evidence.md` in your session artifact directory, not in the repository, in the form below, listing for each acceptance criterion of the approved plan the CI checks, LADO check commands or commits that show it is met. LADO matches every entry against its records, shows the developer the entries it cannot match as claims, and marks evidence written for another head as stale, so write the file again whenever you push again.
    ```

    그 뒤에 엔트리 절차 2 의 형식 블록(```` ```markdown ```` 울타리, 주석 포함 그대로).
