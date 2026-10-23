# docs/chains — 단계 체인 정의

`.claude/bin/chain-run.sh` 가 읽는 단계 목록이다. 다단계 플랜이 승인되면 메인 세션이 단계마다 work-history 엔트리(수용 게이트 포함)와 체인 TSV 를 쓰고, 러너가 단계마다 **새 `claude -p` 세션**을 띄워 구현한다. 세션을 단계마다 새로 열어 컨텍스트가 0 에서 시작하게 하는 것이 목적이다.

러너와 게이트 러너는 소유자의 다른 저장소 하네스(tbbe-re, 2026-10-15 판)에서 들여왔다. 이 저장소에서 바꾼 것은 러너의 완료 조건 한 줄(커밋 관례 = `AGENTS.md` 의 영어 conventional commit)뿐이다.

## 파일

| 경로 | 역할 |
|---|---|
| `.claude/bin/chain-run.sh` | 러너. TSV 를 읽고 단계마다 프롬프트를 조립해 `claude -p --permission-mode bypassPermissions --max-budget-usd <예산>` 을 띄운다 |
| `.claude/hooks/lib/gate-check.sh` | 수용 게이트 러너. 엔트리의 `## 수용 게이트` 를 파싱해 CHECK 를 돌리고 증거를 원장에 쓴다 |
| `.claude/hooks/pre-bash-guard.sh` | `CHAIN_RUN=1` 일 때만 `git push`·`gh`·publish·loopback 밖 HTTP 쓰기를 거부한다(`.claude/settings.json` PreToolUse 에 등록) |
| `.claude/gate/<체인>.sh` | 체인별 게이트 도우미(한 줄 CHECK 로 못 쓰는 판정) |
| `docs/chains/<체인>.tsv` | 단계 목록 |
| `docs/work-history/<날짜>-<작업>-S<nn>-….md` | 단계 엔트리(작업 지시서 + 수용 게이트 + Before/After/회고) |
| `.claude/hooks/.state/` | 원장·락·마지막 결과(커밋하지 않는다) |

## TSV

한 줄 = 한 단계, **탭 구분** 3필드. `#` 줄과 빈 줄은 무시한다.

```
단계ID <TAB> work-history 엔트리 파일명 <TAB> 한 줄 지시
```

엔트리 경로는 파일명만 쓰면 `docs/work-history/` 아래로 푼다.

## 엔트리와 수용 게이트

엔트리에는 `## 수용 게이트` 절이 **반드시** 있어야 한다(없으면 러너가 조립 단계에서 거부한다).

```
## 수용 게이트

- [ ] S01-G1: <관측 가능한 결과>
  CHECK: <한 줄 셸 명령>
  EXPECT: <출력에 들어 있어야 하는 성공 토큰>
```

- 충족 = CHECK 의 rc 가 0 이고 출력(stdout+stderr)에 EXPECT 가 고정 문자열로 들어 있다.
- CHECK 는 **한 줄**이다(heredoc·여러 줄은 첫 줄만 돈다). 복잡한 판정은 `.claude/gate/<체인>.sh` 의 하위 명령으로 빼고 CHECK 는 호출만 한다.
- 증거(`.claude/hooks/.state/gate-runs.jsonl`)는 HEAD·CHECK·EXPECT 의 sha 에 결속된다. 커밋하면 증거가 죽으므로 **커밋 후 게이트를 다시 돌린다**(러너가 단계 끝에 한 번 더 돌린다).
- 수동 게이트(CHECK 없는 항목)는 없다. 명령으로 못 재는 것은 산문으로 쓴다.

## 판정 3종

다음 단계로 가려면 셋 다 통과해야 한다. 하나라도 실패하면 러너가 멈추고 `재개:` 줄을 출력한다.

1. `claude -p` 결과 JSON 이 `is_error=false` 이고 권한 거부가 없다(작업과 게이트·커밋이 끝났으면 예산 소진은 경고로만 남긴다)
2. `gate-check.sh --run <엔트리>` 전건 통과
3. 커밋이 최소 1건 늘었다

## 실행 (이 저장소)

`go` 는 PATH 에 없고 프런트는 Node 24 가 필요하다. 자식 세션과 게이트는 러너의 환경을 물려받으므로 기동 명령에서 한 번에 준다.

```bash
cd <워크트리>
umask 022
setsid nohup env \
  PATH=<go 툴체인>/bin:$HOME/.nvm/versions/node/v24.21.0/bin:$PATH \
  TZ=UTC LANG=C.UTF-8 LC_ALL=C.UTF-8 \
  GATE_TIMEOUT=1800 CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS=5400000 \
  bash .claude/bin/chain-run.sh docs/chains/<체인>.tsv --plan <승인 플랜> --budget <USD> \
  > .claude/hooks/.state/chain-<체인>-<날짜>.log 2>&1 < /dev/null &
```

- **dry-run 먼저**: `bash .claude/bin/chain-run.sh docs/chains/<체인>.tsv --plan <플랜> --dry-run` 은 프롬프트만 조립한다(claude 기동 0). `--verify-prompt` 는 재료 5종 확인.
- **분리 기동(setsid)**: 하네스의 백그라운드 Bash 로 띄우면 부모 세션이 끝날 때 러너와 자식이 같이 죽는다. 대가로 완료 알림이 없으니 로그·`git log`·`.claude/hooks/.state/chain-runs.jsonl` 로 본다.
- **`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS` 는 유한한 큰 값**: `claude -p` 는 백그라운드 작업을 기본 600 초만 기다리고 죽인다. `0` 은 무기한이 아니라 **즉시 종료**다. 단계 지시에도 "서브에이전트는 전경(run_in_background: false)" 을 적는다.
- **워크트리 메모리**: 자식 세션의 Claude 프로젝트 키는 워크트리 경로에서 나온다. 메인 체크아웃의 메모리를 보게 하려면 `~/.claude/projects/<워크트리 키>/memory` 를 메인 메모리로 심링크한다.
- **예산**: `--budget` 은 단계당 상한이다(러너 기본 30). 모자라면 단계가 `error_max_budget_usd` 로 잘린다 — 게이트·커밋이 끝났으면 경고, 아니면 실패이고 `--from <단계>` 로 예산을 올려 재개한다.
- **재개**: 실패 출력의 `재개:` 줄을 bash 에서 그대로 실행한다. 단계 자식이 마무리 직전에 끊겼으면 같은 세션을 `claude -p --resume <세션 id>` 로 잇는다(세션 id 는 로그에 있다).
- **도는 중에는 러너 파일을 덮지 않는다**(bash 가 실행 중 파일을 조금씩 읽는다).
- **푸시·PR 은 체인에 넣지 않는다.** 가드가 막고, 메인 세션이 사용자 승인 뒤에 한다.

## 중단

러너 PID 만 죽이면 진행 중 단계는 끝까지 돈다. 전부 멈추려면 setsid 로 띄운 러너의 **세션째** 죽인다.

```bash
fuser -v .claude/hooks/.state/chain-run.lock      # 락 보유자 PID
ps -o pid=,sid= -p <PID>                          # 두 값이 같을 때만(세션 리더) 다음 줄
pkill -TERM -s <SID>
```

원장: `.claude/hooks/.state/chain-runs.jsonl` · 게이트 원장: `.claude/hooks/.state/gate-runs.jsonl`
