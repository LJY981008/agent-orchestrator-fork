# 2026-11-06 워크스페이스 세션 준비 실패: 절대 심볼릭 링크 자산과 git-lfs (#22)

- 이슈: [#22](https://github.com/LJY981008/agent-orchestrator-fork/issues/22)
- 계획: 리비전 1(sha256 f8f0d1c16857), 2026-11-06 승인.
- 실행: LADO 가 파트 5개를 따로 워커로 돌리고(basics·verify 동시 → workspace·startup 동시 → frontend) 통합 브랜치 `lado/aba11901/39771310/integration` 에 병합했다. 통합 워커가 파트 사이 검수, 문서(14단계), 전체 검사와 PR 을 맡았다.

## 파트별 결과

| 파트 | 단계 | 커밋 | 내용 |
| --- | --- | --- | --- |
| basics | 1–2 | `80de96c88b` | `domain.WorkspaceAssetsConfig`(exclude·absoluteLinks), 기본 제외 목록, `ResolvedWorkspaceAssets`, `WorkspaceAssetExcluded`, 검증. ports 의 `AssetExclude`·`KeepAbsoluteAssetLinks`·`SkippedAssetLinks`, `ErrGitLFSMissing`, `WorkspaceSetupError`, `WorkspaceSetupSummary`. 새 패키지 `internal/gitlfs`(`UsesLFS`, `Installed`) |
| verify | 3–4 | `6f2ac6f361` | `worktree_dirty` 코드와 세부 `dirty`·`dirtyCount`, `WorktreeCheck`, 선택 인터페이스 `DirtyPaths`, 2분 뒤 한 번 보내는 워커 알림(`requestClean`), DTO enum |
| workspace | 5–7 | `3311ca6101` | `ladoCopyWorkspaceAssets`(제외 목록, 루트 밖 링크 skip/keep, `.git`/`.ao`·관리 루트 대상 차단), `ladoRequireGitLFS`(새 워크트리만), `ladoWrapWorktreeAddError`, `ladoGuardHooksDir` |
| startup | 8–10 | `0fad59d20e` | 세션 시작이 설정을 해석해 넘기고 건너뛴 링크를 Warn 로그로, `provisionError`·`WORKSPACE_CREATE_FAILED` 메시지가 요약으로 시작, 가져오기 검증의 `gitLfsMissing` |
| frontend | 11–13 | `13c92b8686` `7e0c48c800` `ef891bca3b` | `npm run api`, 검증 카드의 worktree_dirty 문구·파일 목록, `WorkspaceAssetsSettings`, 가져오기 화면의 git-lfs 안내, 8개 로캘 |

## 통합 검수

파트들을 서로, 그리고 계획 전체와 맞춰 봤다. 코드를 고쳐야 할 어긋남은 없었다. 확인한 것:

- **설정 이름과 해석**: JSON `workspaceAssets`/`exclude`/`absoluteLinks` 가 domain, OpenAPI(`DomainWorkspaceAssetsConfig`), schema.ts, 화면에서 같다. `exclude` 없음·`null` = 기본값, `[]` = 모두 복사의 해석이 domain(`ResolvedWorkspaceAssets`), session_manager(해석해서 넘김), gitworktree(nil = 아무것도 제외 안 함, 해석은 호출자 몫), 화면(편집 안 하면 키를 보내지 않음, 읽을 때 null = 기본값)에서 맞물린다.
- **기본 제외 목록**: 프런트 `DEFAULT_WORKSPACE_ASSET_EXCLUDES` 와 `domain.DefaultWorkspaceAssetExcludes` 12개 항목이 순서까지 같다. 양쪽 주석이 서로를 가리킨다.
- **오류 요약 형식**: gitworktree 가 만드는 `*ports.WorkspaceSetupError` 는 `fail()`·`errors.Join`(롤백)·`wrapSpawnStage` 의 `%w` 를 거쳐도 `errors.As` 로 찾힌다. `provisionError` 와 `toSpawnAPIError` 가 같은 `WorkspaceSetupSummary` 를 쓴다. 자산 복사 실패의 조치 문구 "Project settings → Scripts → Workspace folders" 는 화면의 영어 이름(`settings.project.workspaceAssets` = "Workspace folders")과 위치와 맞다.
- **worktree_dirty**: domain 상수, DTO 4곳(item code·verification reason·queue·tasks stateReason), schema.ts, `lado-errors` 사유 표, 검증 카드, 8개 로캘이 같은 이름이다. 워커 메시지와 카드 문구 모두 커밋·삭제·`.gitignore` 만 권하고 `.git/info/exclude` 는 말하지 않는다.
- **`dirtySince` 키(계획과 다른 점, 유지)**: 계획 4단계는 키를 세션 id 로 정했지만 verify 파트는 기존 `passKey` 를 썼다. 단일 PR 실행에서는 세션 id 그대로라 계획과 같고, 다중 저장소 실행(PR 여럿)에서만 세션+PR URL 이다. 세션 id 하나로 두면 한 세션의 두 PR head 가 패스마다 서로 덮어써 2분이 끝내 차지 않으므로, 파트의 선택이 계획의 의도(head 마다 항목이 쌓이지 않게)를 지키면서 다중 PR 을 깨지 않는 쪽이다. 항목 수는 실행이 추적하는 PR 수를 넘지 않는다.
- **CLI 미러**: `internal/cli/project.go` 의 `projectConfig` 에 `workspaceAssets` 가 없어 `ao project set-config` 가 이 설정을 지운다. `preRemove`·`effort` 도 이미 같은 처지이고 계획 경로 밖이라 고치지 않고, projects.mdx 에 적었다(후속 후보).

### 문서 (14단계와 검수)

- `frontend/src/docs/content/configuration/projects.mdx`: 설정 표에 `workspaceAssets`, 새 절 "Workspace folders"(패턴 규칙, 기본값, `[]`, skip/keep, symlinks 로 원본 쓰기, CLI 가 지운다는 점), 자주 겪는 문제에 git-lfs 없음·hooksPath 훅·자산 복사 실패.
- `docs/LJY-PRD/TDD.md`(계획 경로 밖): §검증 "When" 문단이 "첫 실행 전 status 가 비지 않으면 기다린다(`worktree_not_at_head`)" 로 읽혀 새 동작과 어긋났다. `worktree_dirty`, 파일 목록, 2분 뒤 한 번 알림을 한 문장으로 더했다.

### 파트 경로 밖 변경

| 경로 | 누가 | 이유 |
| --- | --- | --- |
| `docs/LJY-PRD/TDD.md` | 통합 | 위 문서 검수: 검증 명령 대기 설명을 코드와 맞춤 |

보호 경로는 바꾸지 않았다.

## 검사

- `go test` 계획 패키지 10묶음(domain, ports, gitlfs, gitworktree, session_manager, service/session, service/project, service/importer, ladoverify, httpd/...): 통과(umask 022).
- `go vet ./...`: 통과.
- golangci-lint v2.13.2: 0 issues.
- `npm run api` 뒤 openapi.yaml·schema.ts 차이: 없음.
- vitest 6개 파일(LadoVerificationSection, lado-errors, WorkspaceAssetsSettings, ProjectScriptsSettings, CreateProjectFlow, renderer-coverage): 154개 통과.
- `npm run frontend:typecheck`: 결과는 PR 설명에 적는다.
- `npm run lint` 는 이 컴퓨터에서 환경 탓 5개 패키지 실패로 golangci-lint 전에 멈추므로 CI 에서 본다.

## 남은 질문 (계획에서 이어짐)

- 건너뛴 링크를 세션 화면에도 남길지(세션 열 추가 = DB 스키마 변경).
- git-lfs 가 없을 때 막지 않고 포인터 파일로 시작하는 프로젝트별 선택지.
- CLI `projectConfig` 미러에 `workspaceAssets`(와 `preRemove`)를 더할지.
- 실제 앱 미리보기로 설정 카드·가져오기 안내를 눈으로 확인하지는 않았다(단위 테스트로만 확인).
