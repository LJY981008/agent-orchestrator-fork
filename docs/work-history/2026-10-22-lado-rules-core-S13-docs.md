# 2026-10-22 LADO 규칙 코어 — S13 문서(TDD·HARNESS·PRD·LADO.md)

> 체인 `docs/chains/lado-rules-core.tsv` · 승인 플랜 `docs/work-history/2026-10-22-lado-rules-core-plan.md`(§1 Decisions, §10) · 브랜치 `feat/lado-rules-core`

## 의도/목적 — 왜 이 작업을 하나

5단계에서 실제로 만든 것과 그 결정을 스펙 문서에 기록한다. 이전 단계들(4a·4b)도 끝에서 TDD·ADR 을 "as built" 로 고쳤다. 스펙과 코드가 어긋나면 다음 단계(6 큐·디스패처)가 틀린 전제에서 출발한다.

## 범위

- 고친다: `docs/LJY-PRD/TDD.md`, `docs/LJY-PRD/HARNESS.md`, 필요할 때만 `docs/LJY-PRD/PRD.md`, `LADO.md`, `LADO.ko.md`.
- **코드는 고치지 않는다.** 문서가 코드와 다르면 코드를 근거로 문서를 쓴다(필요하면 `git log`·`git show` 로 S01~S12 커밋을 읽는다). 코드에서 버그를 발견하면 고치지 말고 회고에 적는다.
- 문서 언어는 각 문서의 기존 언어(영어 스펙, `LADO.ko.md` 는 한국어)와 문체를 따른다. 경로 표기 규칙(TDD 머리말: Go 코드는 `backend/internal/` 생략)도 그대로.

## 절차 (빠짐없이)

1. TDD §3.1 Rule snapshot 행: 승인(`approval_id`)과 함께 고정, `lado_policy_snapshot_rules` 가 쓴 규칙 버전을 적는다는 것. 규칙 테이블(`lado_rules`·`lado_rule_versions`·`lado_rule_bindings`)도 표에 한 행.
2. TDD §3.2: Review required(승인 스냅숏 sha ≠ 지금 해석한 sha — 5단계는 읽을 때 파생, 6단계 큐가 같은 비교) / Blocked(승인 스냅숏의 blocking 문제) — 5단계의 실제 파생 규칙으로.
3. TDD §5.2 "As built in step 4b" 아래에 "As built in step 5" 문단: 승인 트랜잭션의 스냅숏, 본문 `{sha256, policySha256}`(policySha256 은 스키마상 선택·서비스가 요구), main 확인창의 규칙 요약, `LADO_RULES_CHANGED`, review_required·blocked 의 재승인. "Steps 5 and 6 add …" 문장은 "Step 5 added the rule snapshot; step 6 adds the queue entry" 로.
4. TDD §6.6 끝: 결정(2026-10-20) — 권한 상한 기본 `auto`, Codex 는 auto 에서 workspace-write + on-request + 네트워크 없음이라 commit·push·네트워크마다 승인, 소유자가 presence 확인으로 상한을 올린다(`PATCH /lado/rules/agent.permission.ceiling`). 실행에 적용하는 것은 6단계.
5. TDD §9: 해석·스냅숏·프롬프트 컴파일이 5단계에 있고, 워커 프롬프트 주입·상한 적용은 6단계라는 것.
6. TDD §13 Rules 행: `GET, POST /lado/rules`; `GET, PATCH, DELETE /lado/rules/{ruleId}`(PATCH 는 상한 값을 바꿀 때 P); `PUT /lado/bindings`; `GET /lado/effective?projectId=|taskId=`. 변경 라우트의 `dryRun`.
7. TDD §14: 게이트 3 → `service/ladopolicy` `TestResolveBindings`·`TestResolveCapabilities`·`TestResolveConflicts`(HARNESS §15 1·2·3·7·8); 게이트 4 → 5단계는 `TestDisablingKeepsHistoryAndSnapshots`·`TestTaskStateFollowsRuleChanges`, 큐 진입 쪽은 6단계.
8. TDD §15: 5번 항목에 "Built 2026-10-22 …" 요약(무엇을 만들었고 무엇을 6·7·8 단계로 넘겼는지 — 플랜 §1 Out). 4b 항목의 "step 6 adds the rule snapshot and the queue entry to that transaction" 을 "step 5 added the rule snapshot to that transaction; step 6 adds the queue entry" 로.
9. HARNESS §4 끝: 트리거 — 5단계 규칙은 그 범위의 모든 LADO 실행에 적용되고, 트리거 판정은 "모든 실행" 이 아닌 첫 트리거와 함께 온다. §5: 저장된 스키마(name, category, instruction, enforcement, config, conflictsWith; 버전·내장·보호). §8: 활성 버전 = 최고 번호, 바인딩은 활성 버전을 따른다, AI 초안(HAR-006)은 이후. §12: 실제 화면 구성(설정 → 규칙, 프로젝트 설정 → 규칙, 태스크 뷰 규칙 탭, 보드 상태). §14: 단계별 등록(5: `plan.approval.required`·`agent.permission.ceiling`, 선택 내장 `git.commit.message.korean` 은 프롬프트만 — 검증기는 8; 7: `setup.change.requires_presence`; 8: 나머지 넷). §15: 각 항목을 덮는 테스트 이름(1·2·3·4·7·8) 또는 담당 단계(5·6·9~15).
10. PRD 는 요구 행의 상태·설명이 실제와 달라졌을 때만 고친다(예: HAR-001~005 의 LADO change 칸에 구현 위치).
11. `LADO.md`·`LADO.ko.md` 상태표에 5단계 행(In progress / 진행 중 — 머지는 메인 세션이 PR 뒤에 바꾼다).

## 마무리 (모든 단계 공통)

1. 커밋(영어 conventional commit, 예 `docs(lado): record step 5` · Co-Authored-By 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-22-lado-rules-core-S13-docs.md` 로 전건 MET 를 확인한다.

## 수용 게이트

- [ ] S13-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-rules-core.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S13-G1: TDD 가 5단계를 기록했다
  CHECK: bash .claude/gate/lado-rules-core.sh has docs/LJY-PRD/TDD.md 'policySha256' 'lado_policy_snapshot_rules' '/lado/effective' 'PUT /lado/bindings' 'step 5 added the rule snapshot' 'LADO_RULES_CHANGED' 'TestResolveBindings'
  EXPECT: HAS_OK
- [ ] S13-G2: HARNESS 가 5단계를 기록했다
  CHECK: bash .claude/gate/lado-rules-core.sh has docs/LJY-PRD/HARNESS.md 'conflictsWith' 'agent.permission.ceiling' 'TestResolveCapabilities' 'TestDisablingKeepsHistoryAndSnapshots'
  EXPECT: HAS_OK
- [ ] S13-G3: LADO 소개 문서 상태표
  CHECK: bash .claude/gate/lado-rules-core.sh has LADO.md 'Rules core' && bash .claude/gate/lado-rules-core.sh has LADO.ko.md '규칙 코어'
  EXPECT: HAS_OK
- [ ] S13-G4: 최종 회귀 — 백엔드 LADO 패키지·저장소 LADO 테스트
  CHECK: cd backend && go test -count=1 ./internal/domain/ ./internal/service/ladopolicy/ ./internal/service/ladotask/ ./internal/service/ladoproject/ ./internal/service/ladogate/ ./internal/service/ladopresence/ ./internal/httpd/... && go test -count=1 -run 'Lado' ./internal/storage/sqlite/ ./internal/storage/sqlite/store/ ./internal/daemon/ && echo S13_BACKEND_OK
  EXPECT: S13_BACKEND_OK
- [ ] S13-G5: 최종 회귀 — 생성물 드리프트 없음
  CHECK: bash .claude/gate/lado-rules-core.sh sqlc-clean && bash .claude/gate/lado-rules-core.sh api-clean
  EXPECT: API_CLEAN
- [ ] S13-G6: 최종 회귀 — 빌드·vet·타입체크
  CHECK: bash .claude/gate/lado-rules-core.sh go-build && bash .claude/gate/lado-rules-core.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S13-G7: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-rules-core.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

- 시작 HEAD: `0a5ffab4904d3e22fce360dc1f1baa1074628484`(`docs(lado): record S12`), 트리 깨끗.
- TDD: §3.1 에 규칙 테이블 행이 없고 Rule snapshot 행은 "Canonical JSON and its sha256" 뿐. §3.2 Review required·Blocked 는 큐 엔트리의 `review_required_reason`·`blocked_reason` 으로만 정의. §5.2 4b 문단 "Steps 5 and 6 add the rule snapshot and the queue entry to it", 5단계 문단 없음. §6.6 은 Codex 상한을 "rules core 에서 정한다"로 미뤄 둠. §9 "Resolution at queue admission". §13 Rules 행은 `GET, POST /lado/rules; PATCH /lado/rules/{id}; PUT /lado/bindings; GET /lado/effective` 한 줄. §14 게이트 3 "table tests (HARNESS §15)", 게이트 4 는 `service/ladoqueue` 만. §15 5번 항목에 결과 없음, 4b 항목 "step 6 adds the rule snapshot and the queue entry to that transaction".
- 절차 밖에서 코드와 어긋난 곳: TDD §1.2("the complete list of upstream files")에 5단계가 고친 `SettingsDialog.tsx`·`GlobalSettingsForm.test.tsx` 가 없음. §4.3 은 규칙 변경도 `{"kind":"lado_task"}` 로 적음(코드는 `lado_rules`). §6.1·§6.4 는 규칙 스냅숏을 6단계가 가져온다고 적음.
- HARNESS: §4·§5·§8·§12·§14·§15 는 설계 문장만(트리거, `owner`·`default_enabled`·`validator`, 버전 정책, `RuleChangeProposal`, 큐 진입 때 고정). §13 에 `lado_policy_snapshot_rules` 없음. PRD HAR-001~005 의 LADO change 칸에 구현 위치 없음. `LADO.md`·`LADO.ko.md` 5단계 행 "Planned"·"예정".

## After — 수행 후 실측

- 문서 커밋 `90cc0ff3c` `docs(lado): record step 5 in the specs`: 5 파일, +121 −57. 코드 변경 없음.
  - TDD §1.2: `SettingsDialog.tsx` 행 추가(로컬 LADO 프로젝트의 Rules 페이지), settingsCatalog·ui-store 행에 프로젝트 섹션 id `rules` 와 `GlobalSettingsForm.test.tsx` 의 mock, event-transport 행에 `lado_rules`, preload 행에 상한 변경 호출.
  - §3.1: Rule 행(`lado_rules`·`lado_rule_versions`·`lado_rule_bindings`, scope_id 는 FK 없음, 행 없음 = INHERIT), Rule snapshot 행(`approval_id` 로 승인과 함께 고정, `lado-policy/1`, blocking, `lado_policy_snapshot_rules`).
  - §3.2: Review required = 유효 승인의 스냅숏 sha ≠ 지금 해석한 sha(`LadoTaskPlan.RulesChanged`, 5단계는 읽을 때 파생, 6단계 큐가 같은 비교 + 다른 사유), Blocked = 승인 스냅숏의 blocking 문제(종류 나열), Approved 행 보강. 표 아래 "as built in step 5" 문단: 어느 읽기 경로가 현재 해시를 채우는지, 우선순위(취소·닫힘 > Review required > Blocked), 규칙 이전 승인 → Review required, 해시 계산 실패 → 스냅숏대로, 재승인.
  - §4.3: 규칙 테이블 트리거의 `{"kind":"lado_rules","projectId":…}` 와 누구에게 가는지.
  - §5.2: "Step 5 added the rule snapshot; step 6 adds the queue entry." + "As built in step 5" 다섯 항목 — 본문 `{sha256, policySha256}`(스키마상 선택·서비스가 400 `LADO_POLICY_SHA_REQUIRED`, 500 `LADO_RULES_UNAVAILABLE`), main 이 effective 를 직접 읽어 확인창에 규칙 요약(켜진 수·상한·sha 앞 12자·blocking 수)을 싣고 그 해시로 서명, 브라우저 경로는 렌더러가 승인 직전 새로 읽은 해시, 409 `LADO_RULES_CHANGED`(아무것도 안 씀), 같은 tx 의 스냅숏·규칙 행·감사 `policySnapshot`·`blocking`, blocking 스냅숏도 승인, review_required·blocked 재승인.
  - §6.1·§6.4: 규칙 스냅숏은 5단계부터 있다.
  - §6.6: "Decision (2026-10-20, rules core)" — 기본 `auto`, Codex 는 workspace-write + on-request + 네트워크 없음이라 commit·push·네트워크마다 승인(`LADO_CODEX_ASKS_APPROVAL` info), `bypass-permissions` 만 danger-full-access, 소유자가 presence 확인으로 `PATCH /lado/rules/agent.permission.ceiling`, 상한 모드 셋·`default` 불가, 플랜 상한은 낮추기만, 실행 적용은 6단계.
  - §9: 해석(승인 때·승인된 태스크 읽기마다·effective), 프롬프트 컴파일은 5단계·주입은 6단계, AO 프로젝트 지시(읽기 전용·worker 두 종류 해시·못 읽거나 4 MiB 초과면 blocking), AO 빌드(`config.Telemetry.AppVersion`), 도구 정책(5단계 해석·6단계 적용), capability 표(Claude Code·Codex 만 둘 다 지원, 나머지는 닫힌 쪽으로 막힘).
  - §13: 승인 행에 `policySha256`, Rules 행 = `GET, POST /lado/rules`(`includeArchived`); `GET, PATCH, DELETE /lado/rules/{ruleId}`(PATCH 는 상한 모드를 바꿀 때 P); `PUT /lado/bindings`; `GET /lado/effective?projectId=` 또는 `?taskId=`, 그리고 `dryRun`·`baseVersion`·보호·보관·삭제 거부·effective 응답 내용.
  - §14: 게이트 3 → `TestResolveBindings`(HARNESS §15 1·2·3)·`TestResolveCapabilities`(7)·`TestResolveConflicts`(8)·`TestProtectedRulesRefuseBindingsArchiveAndDelete`, 게이트 4 → 5단계 `TestDisablingKeepsHistoryAndSnapshots`·`TestTaskStateFollowsRuleChanges`, 큐 진입은 6단계. 게이트 10 에 상한 presence 테스트, 게이트 15 에 5단계 진단 테스트를 더했다.
  - §15: 4b 항목 "step 5 added the rule snapshot to that transaction; step 6 adds the queue entry", 5번 항목 "Built 2026-10-22" — 만든 것, 설계와 함께 승인된 결정(d·e·c·f·g), 6·7·8 단계와 이후로 넘긴 것(플랜 §1 Out), 쓰는 곳이 생길 때까지 두지 않는 것(트리거·버전 고정 바인딩·프로젝트별 상한).
  - HARNESS §4 끝 "As built in step 5": 해석 순서, 트리거 없음(그 범위의 모든 LADO 실행에 적용, 트리거 판정은 첫 트리거와 함께), 상한·실행 모드, 에이전트·capability·선언된 충돌만, 컴파일 대상, 문제 심각도. §5 "As stored in step 5": 규칙 행·버전 필드(name·category·instruction·enforcement·config·conflictsWith, 한도)·바인딩, 정규 JSON 예(한국어 커밋 규칙), 아직 저장하지 않는 필드. §7: capability as built. §8 "As built in step 5": 승인 tx 에서 고정, `lado-policy/1` 필드, Review required 를 만드는 변경들과 재승인, 활성 버전 = 최고 번호·바인딩은 활성 버전을 따름, 변경마다 한 tx + 감사·`baseVersion`·actor `api`/presence, dry run 과 impact, AI 초안(HAR-006)은 이후, 삭제·보관 규칙. §12: 실제 화면(설정 → 규칙, 프로젝트 설정 → 규칙, 태스크 뷰 Rules 탭, 보드 상태, dry run·영향 다이얼로그, 아직 없는 것). §13: `lado_policy_snapshot_rules` 와 만든 단계. §14: "Registered" 열(5: `plan.approval.required`·`agent.permission.ceiling`, 선택 내장 `git.commit.message.korean` 은 프롬프트만·검증기는 8; 7: `setup.change.requires_presence`; 8: 나머지 넷)과 시드 규칙. §15: 항목마다 테스트 이름(1·2·3·4·7·8) 또는 담당 단계(5 → 8, 6 → HAR-006, 9~15 → 7).
  - PRD HAR-001~005: LADO change 칸에 5단계 구현 위치(상태 칸은 그대로). `LADO.md`·`LADO.ko.md`: 5단계 행 "Rules core … | In progress" / "규칙 코어 … | 진행 중".
- 플랜 §1 결정 a~h 의 기록 위치: a TDD §5.2, b TDD §3.2, c TDD §9·HARNESS §7, d HARNESS §8·TDD §13·§15, e HARNESS §8·TDD §13·§15, f TDD §9, g TDD §9·HARNESS §8, h TDD §6.6.
- 문장마다 코드로 확인: `domain/lado_task.go` `State()`·`RulesChanged`, `service/ladopolicy` `capabilities.go` 표·`builtins.go`·`service.go` 한도(120·40·8000·20)·id 형식·삭제 거부 문구, `httpd/controllers/lado_projects.go` 라우트 7개·`lado_rules_dto.go` `dryRun`·`includeArchived`, `openapi.yaml` `ApproveLadoPlanRequest.required: [sha256]`, `daemon/lado_wiring.go` `AOBuild: cfg.Telemetry.AppVersion`, `config/config.go` `AO_TELEMETRY_APP_VERSION`, `frontend/src/main.ts` 의 `app.getVersion()`, `main/lado-presence.ts` 의 effective 읽기·`slice(0, 12)`, `renderer/lib/lado.ts` `approveLadoPlan`, en.json 화면 문구, 테스트 이름(`grep '^func Test'`)과 본문(`TestResolveBindings` 행 이름의 "HARNESS 15.x", `TestDisablingKeepsHistoryAndSnapshots`, `TestTaskStateFollowsRuleChanges`, `TestEffectiveBlocksAnUnsupportedAgent`), `lado_migrate.go` 트리거 7개, `git diff c9ed1e754 HEAD` 의 업스트림 파일 목록.
- 표 검사(스크립트, 코드 스팬 제외): 다섯 파일 모든 표 행의 열 수가 머리 행과 같다.
- 게이트(HEAD `90cc0ff3c`, 트리 깨끗, 한 번에 전부): `GATES=8 MET=8 UNMET=0`, 446초. G4 가 몇 초 만에 끝나 같은 명령을 따로 다시 돌렸다: 14 패키지 ok(테스트 파일 없는 `httpd/apierr`·`httpd/requestscope` 제외), 9초 — 패키지가 병렬로 돈다. 이 엔트리를 커밋한 뒤 전 게이트를 다시 돌려 증거를 새 HEAD 에 묶는다.
- 하지 않음: 코드 변경, DESIGN.md(S11 의 결정 행이 이미 있다), 스크린숏, `LADO.md` 의 다른 문단(머지 뒤 메인 세션 몫).

## 비교/회고

- 계획대로 끝났다. 막힌 곳 없음. 절차 11개 밖에서 고친 것은 모두 "문서가 코드와 다르면 코드를 근거로" 에 따른 작은 수정이다: TDD §1.2 의 업스트림 파일 목록(이 표는 "complete list" 라고 스스로 말한다), §4.3 의 kind, §6.1·§6.4 의 "규칙 스냅숏은 6단계", §14 게이트 10·15, HARNESS §7·§13.
- 문서는 코드에서 확인한 것만 적었다. 6단계 일은 "From step 6" / "Step 6" 으로만 적고 방식은 정하지 않았다(예: 큐 엔트리의 `review_required_reason` 은 §5.3 설계 그대로).
- 코드에서 본 것(고치지 않음, 다음 단계 메모):
  - `aoBuild` 는 데스크톱 앱이 `AO_TELEMETRY_APP_VERSION`(= `app.getVersion()`)을 넘길 때만 채워진다. 앱 없이 띄운 데몬(`ao start`, 개발 루프)은 빈 값이라, 앱이 띄운 데몬에서 승인한 태스크가 다른 방식으로 띄운 데몬에서는 Review required 로 보인다(반대도). 닫힌 쪽 실패라 버그는 아니지만 개발 중 재승인이 잦을 수 있다. 6단계 전에 그대로 둘지 정할 것.
  - `EnsureBuiltins` 는 없는 내장 규칙만 심는다. 다음 빌드가 내장 규칙의 내용(이름·config·enforcement)을 바꿔도 저장된 규칙은 그대로다 — 8단계가 `git.commit.message.korean` 에 검증기를 더하려면 새 버전을 쓰는 이관이 따로 필요하다.
  - 태스크 읽기마다 승인된 태스크의 `CurrentSHA256`(규칙·프로젝트·AO 지시 파일 읽기)을 부른다(S07·S08 메모). 6단계 디스패처도 같은 해석을 쓰므로, 느려지면 그때 한 번 읽은 규칙으로 여러 태스크를 해석하는 도우미를 더한다.
- 관찰: 처음에 `setsid nohup … &` 의 `$!` 를 기다렸더니 곧바로 돌아왔다 — `$!` 는 금방 끝나는 setsid 프로세스였고 실제 `bash -c` 는 다른 PID 로 계속 돌았다. `ps` 로 그 PID 를 찾아 `tail --pid` 로 기다렸다. 게이트 원장은 각 게이트를 실행할 때의 HEAD 에 묶이므로, 게이트가 도는 동안에는 커밋하지 않았다.
- 체인 마지막 단계라 다음 단계는 없다. 메인 세션 몫(플랜 밖): 전체 CI 미러, 랩 스크린숏, gitleaks, 커밋 정리, 소유자 승인 뒤 push·PR, 머지 뒤 `LADO.md`·`LADO.ko.md` 5단계 행을 Merged/병합됨으로.
