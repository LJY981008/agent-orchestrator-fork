# 2026-10-26 LADO 대기열·디스패처 — S15 리뷰 반영: 프런트·문서

> 체인 `docs/chains/lado-queue-fixes.tsv` · 승인 플랜 `docs/work-history/2026-10-23-lado-queue-plan.md`(§1 결정 g 는 S14·이 엔트리가 고친다) · 브랜치 `feat/lado-queue-dispatcher` · 소유자 승인 2026-10-26("체인 2단계", 시간 상한은 "PR 이 열리면 멈춤")

## 의도/목적 — 왜 이 작업을 하나

6단계 프런트 리뷰(블로커 0, 메이저 1 — 원인은 백엔드라 S14 가 고침, 마이너 4, 니트)를 반영하고, S14 의 새 동작(대기 이유 `run_live`, 시간 상한 규칙, 새 오류 문장, `enqueuedAt`)을 화면과 문서에 맞춘다.

## 범위 (지적 번호 = 리뷰 번호)

- **F1 (마이너 m1, 멈춘 상대 시각)** — 재시도 줄과 실행 패널의 마감을 상대 시각("2분 뒤")이 아니라 시계 시각("14:32에 다시 시도", "18:05까지")으로. 앱의 시간 형식 도우미가 있으면 그것을 쓴다(찾아서). 날짜가 다르면 날짜도.
- **F2 (마이너 m2, 실패 이유)** — failed 태스크의 태스크 뷰 상태 줄 아래에 `task.queue.error`(데몬이 정리한 문장)를 muted 한 줄로(잘리면 `title` 로 전체). `base_ref_mismatch` 안내는 "수정 요청"으로 계획을 고치라고 말한다.
- **F3 (마이너 m3, 대기열 순서)** — Queued 레인은 `queue.enqueuedAt` 오름차순(먼저 승인된 것이 위 — 디스패처가 잡는 순서). 안 쓰는 `useLadoQueue`·`fetchLadoQueue` 는 지운다(API 경로는 둔다).
- **F4 (마이너 m4, 복구된 세션)** — `lib/lado-events.ts`: 세션 갱신 이벤트에 `isTerminated` 가 있으면(true·false 모두) 그 세션이 보드가 아는 실행이면 태스크·실행 쿼리를 무효화한다(복구로 다시 살아난 실행이 Failed 로 남지 않게).
- **S14 반영** — 대기 이유 `run_live` 문구("이 작업의 이전 실행이 아직 돌고 있음"), PR 이 열려 시간 상한이 멈춘 실행의 패널 문구("PR 이 열려 시간 상한을 적용하지 않습니다"), `LADO_ISSUE_NOT_FOUND`·`LADO_EXECUTION_FIXED`(복구 거부) 오류 문구.
- **니트** — en `Project id:` → `Project ID:`, ko `프로젝트 id:` → `프로젝트 ID:`(그 뒤 `node scripts/sync-tray-catalog.mjs`); ko `머지되었습니다` → `병합되었습니다`; 태스크 뷰의 `role="status"` 는 버튼·링크가 아닌 라벨 부분에만; 브라우저에서 모드를 끌 때 `LADO_PRESENCE_UNAVAILABLE` 이 승인 문구로 나오지 않게 모드 끄기 전용 문구; 모드 끄기 확인 대화상자(메인 프로세스, `lado.presence.modeOffDetail` 등)에 결과 한 문장 추가 — "그 뒤로 이 프로젝트의 워커는 승인한 계획 없이도 시작할 수 있습니다."(8개 로캘, 트레이 카탈로그 재생성).
- **문서** — `docs/LJY-PRD/TDD.md` §5.3 "As built in step 6" 에 리뷰 반영(S14 의 B1~B8: 상한 찬 프로젝트는 잡지 않음, 리스는 잡을 때 시각, `lado:` 예약과 연결 전 확인, 태스크당 워커 하나와 정리, 복구는 kill 실패 시 리스 유지, 시간 상한은 PR 이 생기면 멈춤, 멈춘 실행 복구 거부, 이슈 읽기 실패의 늘어나는 대기와 삭제된 이슈) — 기존 "known gap" 문장 중 고쳐진 것은 지운다. "once the startup reconcile has settled" 를 실제(시작 안전 정리 뒤, 배경·지속 호스트 정리는 그 뒤에 돈다)로. 플랜 결정 g 의 기본 8시간 문장에 "PR 이 열리면 멈춤" 추가는 TDD 에만(플랜 문서는 고치지 않는다 — 엔트리가 우선). `LADO.md`·`LADO.ko.md`: 6단계 설명에 Failed 원인 "PR 없이 세션이 끝남", 모드 켜기는 트래커 intake 가 켜져 있으면 거부, 시간 상한은 PR 이 열리면 멈춤.
- 하지 않는다: 백엔드 코드(S14 끝), `RunsSection` 의 동시 변경 덮어쓰기(한 사람 데스크톱이라 둔다 — 회고에 적는다).

## 절차

0. 시작 전에 S01~S14 엔트리의 `## 비교/회고` 와 "다음 단계 메모"(특히 S14)를 읽는다. **`DESIGN.md` 를 먼저 읽는다.**
1. 지적마다 실패하는 테스트 → 고침 → 통과. 새 문구는 8개 로캘에 실제 번역(한국어 용어집, 조사 앞 변수 금지).
2. 메인 프로세스 문구를 바꾸면 `node scripts/sync-tray-catalog.mjs` 로 `tray-catalog.generated.ts` 를 다시 만든다.
3. 커밋 예: `fix(lado): review fixes for the queue and run screens`, `docs(lado): record the step 6 review fixes`.

## 마무리 (모든 단계 공통)

1. 커밋(영어 conventional commit, Co-Authored-By 줄 유지).
2. 이 엔트리의 Before·After·회고를 채우고 **엔트리도 커밋**한다. 미커밋 변경이 남으면 마지막 게이트가 거부한다.
3. 마지막 커밋 뒤 `bash .claude/hooks/lib/gate-check.sh --run docs/work-history/2026-10-26-lado-queue-S15-frontend-fixes.md` 로 전건 MET 를 확인한다.

## 필수 테스트

- `src/renderer/components/lado/LadoLanes.test.tsx` — 재시도 줄이 시계 시각, Queued 레인이 enqueuedAt 오름차순, `run_live` 이유 줄
- `src/renderer/components/lado/LadoTaskDialog.test.tsx` — failed 의 데몬 오류 줄, base_ref_mismatch 안내, status 역할이 라벨에만
- `src/renderer/components/lado/LadoExecutionPanel.test.tsx` — 마감이 시계 시각, PR 이 있으면 "시간 상한 적용 안 함"
- `src/renderer/lib/event-transport.test.ts` — 실행 세션의 `isTerminated: false` 갱신도 태스크·실행 쿼리를 무효화
- `src/main/lado-presence.test.ts` — 모드 끄기 대화상자에 결과 문장
- `src/renderer/lib/lado-errors.test.ts` — 새 오류 코드와 `run_live` 가 모든 로캘에서 번역됨

## 수용 게이트

- [ ] S15-G0: 체인 정의·하네스가 태그 이후 바뀌지 않았다
  CHECK: bash .claude/gate/lado-queue.sh defs-unchanged
  EXPECT: DEFS_UNCHANGED
- [ ] S15-G1: 필수 테스트 파일 6개 통과
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/components/lado/LadoLanes.test.tsx src/renderer/components/lado/LadoTaskDialog.test.tsx src/renderer/components/lado/LadoExecutionPanel.test.tsx src/renderer/lib/event-transport.test.ts src/main/lado-presence.test.ts src/renderer/lib/lado-errors.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G2: i18n 정합(변수 정렬·렌더러 커버리지)
  CHECK: bash .claude/gate/lado-queue.sh vitest src/renderer/i18n/instance.test.ts src/renderer/i18n/renderer-coverage.test.ts
  EXPECT: VITEST_OK
- [ ] S15-G3: 용어 니트와 대화상자 문장
  CHECK: bash .claude/gate/lado-queue.sh none frontend/src/renderer/i18n/ko.json '프로젝트 id:' '머지되었습니다' && bash .claude/gate/lado-queue.sh none frontend/src/renderer/i18n/en.json 'Project id:' && bash .claude/gate/lado-queue.sh has frontend/src/renderer/i18n/en.json 'without an approved plan'
  EXPECT: HAS_OK
- [ ] S15-G4: 안 쓰는 대기열 훅 삭제
  CHECK: bash .claude/gate/lado-queue.sh none frontend/src/renderer/hooks/useLado.ts 'useLadoQueue' && bash .claude/gate/lado-queue.sh none frontend/src/renderer/lib/lado.ts 'fetchLadoQueue'
  EXPECT: NONE_OK
- [ ] S15-G5: 문서가 리뷰 반영을 적는다
  CHECK: bash .claude/gate/lado-queue.sh has docs/LJY-PRD/TDD.md 'run_live' 'LADO_ISSUE_NOT_FOUND' && bash .claude/gate/lado-queue.sh none docs/LJY-PRD/TDD.md 'once the startup reconcile has settled'
  EXPECT: NONE_OK
- [ ] S15-G6: 렌더러 전체 테스트
  CHECK: cd frontend && npx vitest run && echo S15_VITEST_ALL_OK
  EXPECT: S15_VITEST_ALL_OK
- [ ] S15-G7: 타입체크(렌더러·e2e)
  CHECK: bash .claude/gate/lado-queue.sh typecheck
  EXPECT: TYPECHECK_OK
- [ ] S15-G8: gitleaks 가 브랜치 커밋 전부에서 깨끗하다
  CHECK: bash .claude/gate/lado-queue.sh gitleaks
  EXPECT: GITLEAKS_OK
- [ ] S15-G9: 미커밋 변경 없음
  CHECK: bash .claude/gate/lado-queue.sh tree-clean
  EXPECT: TREE_CLEAN

## Before — 수행 전 실측

(이 단계의 세션이 채운다)

## After — 수행 후 실측

(이 단계의 세션이 채운다)

## 비교/회고

(이 단계의 세션이 채운다)
