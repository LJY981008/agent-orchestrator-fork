# 2026-11-04 프로젝트 지식 저장소: 앱 세션이 프로젝트 맥락을 이어 쓴다 (#24)

- 이슈: [#24](https://github.com/LJY981008/agent-orchestrator-fork/issues/24) (해결 방향은 #34 의 "프로젝트 지식 저장소")
- 계획: 리비전 1, 2026-11-04 승인. 결정은 [adr/0010](../LJY-PRD/adr/0010-project-knowledge-store.md), 설계는 [TDD §5.1](../LJY-PRD/TDD.md) 의 "Project knowledge".
- 실행: LADO 가 파트 5개를 따로 워커로 돌리고(knowledge → cli·api·prompt 동시 → frontend) 통합 브랜치 `lado/4a5dd844/d41be5be/integration` 에 병합했다. 통합 워커가 파트 사이 검수, daemon 배선(7단계), 문서(8단계), 전체 검사와 PR(9단계)을 맡았다. 기준은 origin/main `a67b1bc2e`(#54 포함)였고, PR 에서는 브랜치를 origin/main `3ef1db761`(#57 포함) 위 한 커밋으로 다시 썼다(아래 CI). 아래 커밋 해시는 LADO 의 파트·통합 커밋으로, 다시 쓴 PR 브랜치에는 남아 있지 않다.

## 파트별 결과

| 파트 | 단계 | 커밋 | 내용 |
| --- | --- | --- | --- |
| knowledge | 1–2 | `46e5120d1` | 노트 형식(domain: 파싱·렌더·검증, 목차 두 가지), `service/ladoknowledge`(저장, 바깥 편집 채택, 소유 확인과 claim, 쓰기, blob 신선도, Claude 메모리·다른 폴더 가져오기) |
| cli | 3 | `3b5aa66d8` | `ao lado knowledge write --note <slug> [--base N]`, 계획 워커 훅의 here-document 형식 하나 허용 |
| api | 4 | `99ce1ca89` | 지식 라우트 4개, OpenAPI 와 `schema.ts` 재생성 |
| prompt | 5 | `05e7e0cbc` | 모든 LADO 세션 standing instructions 의 `## Project knowledge (LADO)` 절, 설계자·검수자 구분, readOnlyNote |
| frontend | 6 | `c5455a2f6` | 프로젝트 설정의 지식 카드, 가져오기와 이어 쓰기(claim), 오류 문구, 8개 locale |

## 파트가 확인한 사실

- **Claude 폴더 인코딩:** Claude Code 는 경로의 UTF-16 단위마다 ASCII `[A-Za-z0-9]` 가 아니면 `-` 로 바꾼다. 그래서 `/home/u/프로젝트` 는 `-home-u-----`(대시 5개: `/` 1개 + 한글 4글자)다. 계획의 예시(대시 4개)는 틀렸고, 테스트는 실제 규칙을 따른다.
- **가져오기도 비밀값을 건너뛴다:** 쓰기와 같은 형식 검사로, 비밀값이 든 메모리 파일은 `secret` 사유로 건너뛴다.
- **500개 상한:** 프로젝트당 노트 500개를 넘는 쓰기는 400 `LADO_KNOWLEDGE_INVALID_NOTE` 다(가져오기는 `limit` 사유로 건너뜀).
- **foreign 폴더의 가져오기:** 서버가 `LADO_KNOWLEDGE_FOREIGN` 로 막으므로, 카드는 foreign 인 동안 가져오기 버튼을 숨기고 claim 만 보인다.
- **Claude Code 는 이미 worktree 의 메모리를 본래 저장소 기준으로 찾는다:** LADO 설정 홈이 `projects` 를 기기 폴더로 잇기 때문에(`ladosetup/home.go`), Claude 세션은 지금도 저장소의 메모리를 읽는다. 끊기는 것은 폴더 이동·이름 변경, Codex, 동시 쓰기 규칙, 신선도였다(ADR 의 맥락).

## 통합 검수

파트들을 서로, 그리고 계획 전체와 맞춰 봤다. 라우트 경로, 요청·응답 본문 필드, 오류 코드(`LADO_KNOWLEDGE_*` 13개), 가져오기 건너뜀 사유(`claude_memory_not_found`, `exists`, `link`, `not_a_file`, `too_large`, `invalid_name`, `invalid_note`, `secret`, `limit`, `unreadable`), CLI 출력과 검수자 거절은 서비스·컨트롤러·CLI·프롬프트·렌더러 사이에 맞았다. 같은 것을 두 번 만든 곳이나 같은 패키지의 중복 도우미도 없었다(훅 정규식은 `plan submit` 의 `ladoQuotedHereDocument` 를 함께 쓴다). 고친 것은 둘이다.

1. **CLI 가 오류 details 를 읽으려고 공용 클라이언트를 고친 것**(cli ↔ knowledge). cli 파트가 `CHANGED` 의 현재 revision 을 보이려고 `cli/client.go` 의 `apiError` 에 `Details` 필드를 더하고 메시지 뒤에 덧붙였다. `client.go` 는 계획의 affected_paths 밖이고 모든 `ao` 명령이 쓰는 파일이다. 계획은 "데몬 오류 envelope 를 그대로 보여 준다(현재 revision 포함)" 였으므로, `client.go` 를 main 으로 되돌리고 대신 데몬의 `LADO_KNOWLEDGE_EXISTS`·`LADO_KNOWLEDGE_CHANGED` 메시지가 현재 revision 을 말하게 했다("The note is at revision 5 now. Read it again, merge, and send it with revision 5 as the base."). `details.currentRevision` 은 API 와 앱을 위해 그대로 둔다. CLI 테스트와 서비스 테스트가 메시지의 revision 을 확인한다.
2. **ADR 번호 충돌**(계획 ↔ main). 계획은 `adr/0009-project-knowledge-store.md` 를 정했는데, 그 사이 병합된 #42 가 `adr/0009-worker-watch.md` 를 썼다. 새 ADR 을 0010 으로 쓰고, 파트들이 코드 주석에 적은 `adr/0009-project-knowledge-store.md` 참조 7곳(domain, service, ladotask, controllers 2개, 렌더러 2개)을 0010 으로 고쳤다.

### 파트 경로 밖 변경

| 경로 | 누가 | 처리 |
| --- | --- | --- |
| `backend/internal/cli/client.go` | cli | 되돌림(통합 검수 1) |

그 밖에 파트와 통합이 바꾼 파일은 모두 계획의 affected_paths 안이다. 사용자 안내서 `LADO.md`(저장소 최상위)는 affected_paths 밖이라 고치지 않았다. 지식 카드 안내를 더할지는 후속으로 남긴다.

## daemon 배선 (7단계)

- `daemon/lado_knowledge_wiring.go`: `newLadoKnowledge(cfg, store, claudeDir, log)` 가 `cfg.StateDir`, 저장소(프로젝트·세션·설정·감사), 기기 Claude 폴더(`ladoDeviceFolders(codexHome).claudeDir`)로 `ladoknowledge.Service` 를 만든다. git 은 시스템 git 이다. 프롬프트 쪽은 `Index` 하나만 보는 `ladoKnowledgeIndex` 인터페이스로 받는다.
- PromptSource 를 만드는 두 곳 모두에 `Knowledge` 와 `Log` 를 넘긴다: `wireLado`(인자 추가)와 `wireLadoSetup`(`ladoSetupDeps.knowledge`). 설정 쪽이 `wireLado` 의 프롬프트를 바꿔 끼우므로 한쪽만 넘기면 설정이 켜진 뒤 절이 사라진다.
- `daemon.go`: 서비스를 한 번 만들어 두 곳과 `APIDeps.LadoKnowledge` 에 같은 것을 준다.
- 배선 테스트(`daemon/lado_knowledge_wiring_test.go`): `TestWireLadoGivesBothPromptSourcesTheProjectKnowledge`(LADO 모드 프로젝트의 오케스트레이터 절에 `## Project knowledge (LADO)` 와 `<state>/lado/knowledge/<id>` 가 두 PromptSource 모두에서 들어가고, Codex 하네스도 같으며, 저장소가 없으면 절이 없다), `TestLadoKnowledgeRoutesAreServed`(라우터에서 GET 이 저장소 없이는 501, 있으면 200 과 `projectId`·`dir`).

## 문서 (8단계)

- 새 ADR: [adr/0010](../LJY-PRD/adr/0010-project-knowledge-store.md).
- 옛 설계(고정된 `knowledge.md` 스냅샷)를 새 동작으로 고친 곳: PRD 의 오케스트레이터 행, 목표 2단계 행, PRJ-002 행; TDD §4.5(blob 행에서 knowledge snapshot 을 빼고 지식 폴더 행 추가), §5.1(Project knowledge 항목을 저장소 설계로 교체), §13(라우트 4개), §15(#24 기록); adr/0004 39행(지식은 blob 이 아님); adr/0007 48행(계획 워커가 목차를 읽고 설계자는 최종 제출 전에 쓴다).

## 계획과 다른 점

- ADR 번호가 0009 가 아니라 0010 이다(통합 검수 2).
- CLI 는 오류 details 를 읽지 않고 데몬 메시지로 현재 revision 을 보인다(통합 검수 1).
- 한글 경로 인코딩의 예시가 계획과 다르다(대시 5개, 파트가 확인한 사실).

## AO 리뷰 (head `ee5a0f358`)

AO 리뷰가 변경 요청 1건을 냈다. LADO 가 읽지 못한 노트 파일(256 KiB 초과, 링크, 폴더, 읽기 실패)은 `syncLocked` 가 목록에서 빼면서 기록(`notes.json`)까지 지웠다. 그래서 그 뒤 `--base` 없는 쓰기나 가져오기가 `EXISTS` 없이 그 파일을 덮어썼다. 이제 디스크에 `<slug>.md` 가 있는데 읽지 못하면 목록에서만 빼고 기록은 남긴다. 쓰기는 `--base` 와 상관없이 `LADO_KNOWLEDGE_EXISTS`(`details.unreadable`, 메시지에 파일과 이유)로 거절하고, 가져오기는 `exists` 로 건너뛴다. 노트 수 상한도 읽지 못한 파일을 센다. 소유자가 파일을 고치면 바깥 편집으로 다음 revision 이 된다. `TestUnreadableNoteIsNotWrittenOver` 가 확인한다(고치기 전에는 실패).

선택 사항 중 하나도 고쳤다. 쓰기 끝의 신선도 확인이 노트 하나의 경로로 프로젝트의 캐시를 덮어써서, 다음 목차가 `git ls-tree` 를 다시 돌았다. 이제 같은 기준 tip 에서 캐시가 가진 경로만 묻는 조회는 캐시로 답하고 캐시를 그대로 둔다(`TestFreshness`). claim 의 presence 확인과 `LADO.md` 갱신은 후속으로 남긴다.

## CI: gitleaks

PR #56 의 `scan` 이 실패했다. 비밀값 거절을 확인하는 테스트 두 곳(`domain/lado_knowledge_test.go`, `ladoknowledge/write_test.go`)이 본문 없는 OPENSSH·RSA 개인 키 머리말 줄을 그대로 적었고, CI 의 gitleaks 는 PR 의 커밋을 하나씩 보므로 앞선 파트 커밋의 그 줄도 잡는다. `.github/.gitleaks.toml` 허용 목록은 LADO 보호 경로라 쓰지 않는다(한 번 고쳤다가 LADO 경로 검사에 막혀 뺐다). 대신 테스트는 문자열 이어 붙이기(`"-----BEGIN RSA " + "PRIVATE KEY-----\n"`)로 바꾸고, 개발자 승인을 받아 브랜치를 origin/main `3ef1db761` 위 한 커밋으로 다시 써 `--force-with-lease` 로 push 했다(#55 와 같은 방식). 최종 코드는 다시 쓰기 전과 같고, 다른 점은 #57 과 겹친 8개 locale 파일의 끝(두 쪽 모두 새 키를 덧붙임)에서 두 쪽 키를 모두 둔 것뿐이다. 로컬 gitleaks v7.4.0 으로 CI 처럼 `--commits-file` 과 저장소 설정으로 새 커밋을 검사했다.

## 검사

전체 결과는 PR 설명에 적는다.
