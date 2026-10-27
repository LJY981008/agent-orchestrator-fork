# 2026-10-27 LADO 로컬 설정 채택 — 랩 확인(수정 체인 뒤)

> 브랜치 `feat/lado-local-setup` · 체인 `docs/chains/lado-local-setup.tsv`(S01~S17)·`docs/chains/lado-local-setup-fixes.tsv`(S18~S20) 뒤 메인 세션 확인 · 확인한 HEAD `7be9dc693`(S20 기록), 고친 커밋 `98a5629d2`

## 랩 구성

- 데몬: 브랜치 HEAD 를 스크래치에 빌드(`go build -o <lab>/ao ./cmd/ao`), `<lab>/ao daemon` 을 `HOME=<lab>/home`, `AO_DATA_DIR=<lab>/data`, `AO_PORT=3917`, `AO_LADO_DEV_PRESENCE=1`, `AO_LADO_DEV_SETUP_IGNORE_PROCESSES=1`, `AO_TMUX_SOCKET_NAME=ao-lab7`, 가짜 GitHub·GitLab 토큰으로. 실제 `~/.claude`·`~/.claude.json`·`~/.codex` 는 건드리지 않았다.
- 가짜 HOME: Claude 쪽 `CLAUDE.md`, `settings.json`(env 에 `LAB_VAR`·`ANTHROPIC_BASE_URL`·`AWS_REGION`, permissions, `~/.claude/hooks/notify.sh` 를 부르는 SessionStart hook, theme, model), 스킬·에이전트·명령·출력 스타일·hook 스크립트 하나씩, 가짜 `.credentials.json`, `keybindings.json`, `rules/`; `~/.claude.json` 의 MCP 서버 `lab-mcp`; Codex 쪽 `AGENTS.md`, `config.toml`(`model` 과 주석 달린 `[mcp_servers.lab_codex]` url 서버), 가짜 `auth.json`. 채택 전 사본을 떠서 복원 뒤 바이트 비교에 썼다.
- 렌더러: 워크트리의 dev-web(`VITE_AO_API_BASE_URL=http://127.0.0.1:3917`, 포트 5199)을 Playwright MCP 로. 데모 저장소(추적 파일 `CLAUDE.md`·`.mcp.json`)를 `ao-demo` 로 등록하고 LADO 모드를 켰다.

## 확인한 것과 결과

| 흐름 | 결과 |
|---|---|
| 인벤토리 | 22줄. 자격 증명·앱 상태 `never`, `keybindings.json`·`rules/` `not_managed`(제자리, 홈이 그대로 잇는다), settings 의 `theme` 만 기본 keep, Codex `model` 은 `ao_sets_per_session`, OS linux 검증 |
| 채택 대화상자 | 항목·동작, hook 다시 쓰기 전후(`~/.claude/hooks/notify.sh` → `"$CLAUDE_CONFIG_DIR"/hooks/notify.sh`), 충돌(경고: 프롬프트에 글을 넣는 hook, 정보: AO 와 같은 이벤트), 랩의 프로세스 검사 끔 문구 |
| 채택 | "13개 채택, 이동 13·제자리 1, 모든 검사 통과". 옮긴 원본이 사라지고 그릇 폴더(`skills/` 등)는 빈 채로 남음. `settings.json` env 에는 공급자 로그인 변수만 남고 hooks·model·permissions 빠짐, theme 그대로. `.claude.json` 의 `lab-mcp`, Codex 의 `[mcp_servers.lab_codex]` 빠짐. 자격 증명 두 파일 바이트 그대로. 관리 폴더의 settings 는 로그인 변수 없는 env 와 다시 쓴 hook, hook 스크립트 0700·설정 0600·백업 폴더 0700. 감사 `setup.adopted` 는 `human:dev-presence` |
| 편집 | CLAUDE.md 를 앱에서 고치면 차이(@pierre/diffs) → 저장, 버전 2(`api`, `setup.item_updated`), 관리 파일 갱신 |
| 끄기 | `hooks` 전역 끄기는 확인을 거친다(감사 actor `human:dev-presence`) — 소유자 결정 2026-10-27 대로. 그 hook 의 충돌 카드도 사라짐 |
| 변경 감지 | 관리 폴더의 에이전트를 손으로 고치고 다시 검사 → "변경 발견"과 검토(경로, "유지하기 전까지 세션은 버전 1", 차이, 되돌리기/유지). 되돌리기 → 원본 바이트, `setup.drift_reverted`(`api`) |
| 한국어·다크 | 설정 → 로컬 설정이 한국어(로컬 설정, 관리됨·켜짐/꺼짐, 채택 가능, 지침·설정·스킬)와 다크 테마에서 정상 |
| 복원 | 옮긴 파일 7개가 원본과 바이트 같음(`notify.sh` 0755 포함). `settings.json`·Codex `config.toml` 은 채택 전과 완전히 같음(주석 자리까지). `.claude.json` 에 `lab-mcp` 돌아옴(다른 키 차이는 랩 데몬이 claude 를 확인 실행하며 Claude 가 쓴 것). 카드에 "항목 13개 · 복원됨" |
| 프로젝트 규칙 | CLAUDE.md 만 다시 채택한 뒤 `ao-demo` 프로젝트 설정 → 규칙: "이 프로젝트의 로컬 설정"(CLAUDE.md · 켜짐 · 전역 설정 · 상속), "저장소에서" 카드(작업 폴더에서 읽는다는 안내, `.mcp.json`·`CLAUDE.md` 크기·편집). 편집은 `ao-demo` 의 새 작업 창을 제목 `.mcp.json 수정` 으로 연다 |

## 고친 것 (`98a5629d2`)

1. 채택 대화상자에서 keep 항목 문구가 "제자리에 두고 LADO 가 사본을 관리"였다 — keep 은 아무것도 하지 않으므로(관리 항목이 생기지 않음을 데이터로 확인) "제자리에 둡니다. LADO가 관리하지 않습니다." 꼴로 8개 로캘.
2. 보호 규칙 `setup.change.requires_presence` 가 모든 언어에서 영어 기본 이름으로 보였다 — 렌더러의 내장 규칙 표(`lib/lado-rules.ts`)에 넣고 이름·설명을 8개 로캘에 더함(ko: "로컬 설정 변경 확인 필수"). `lado-rules.test.ts` 에 사례, 대화상자 테스트 문구 갱신. vitest 5 파일 37개, typecheck 통과, 랩 화면에서 한국어 이름 확인.

## 남은 틈 (기록만)

- Codex 서버 표를 뺄 때 그 바로 위 주석이 고아로 남는다(S07 에 기록된 틈) — 복원하면 제자리로 돌아온다.
- 데스크톱 앱(Electron 메인)의 실제 확인 창은 브라우저 랩에서 볼 수 없다 — 메인 채널 단위 테스트(S14·S20)로만 확인.
- 실제 Claude·Codex 세션이 홈을 받는 것은 이 랩(로그인 없음)이 아니라 S01 실측과 단위 테스트로 확인했다.
- 렌더러 스모크의 chat 스펙 4개가 CI 미러에서 부하로 흔들렸고 따로 돌리면 통과(아래 CI 미러 줄).
