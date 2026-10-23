#!/usr/bin/env bash
# chain-run.sh — 헤드리스 단계 체이닝 러너 (egm · 2026-09-16)
# agent-orchestrator-fork 사본(2026-10-22 · tbbe-re 판 그대로): 바꾼 것은 완료 조건의 커밋 관례 한 줄뿐이다(아래 build_prompt).
#
# 왜: 플랜 1개가 S1·S2·S3·F1·F2 로 쪼개지는데 한 세션에서 끝까지 가면 컨텍스트가 900k 까지 자란다
#     (실측: 비용의 84%가 메인 세션 200k+ 구간, 900k 턴당 $1.53 = 120k 대비 4배).
#     단계마다 `claude -p` 로 **새 프로세스 = 새 세션**을 띄우면 컨텍스트가 매번 0에서 시작한다.
#
# 새 세션은 대화 기록을 잃는다. 그래서 러너가 매 단계 프롬프트를 조립해 주입한다 — 재료는 전부 디스크에 있다:
#   하네스 규약 = CLAUDE.md·session-start 훅이 자동 로드(주입 불필요) / 설계 의도 = 플랜 파일 /
#   이번 할 일 = TSV 지시 + work-history 엔트리 / 완료 조건 = 그 엔트리의 `## 수용 게이트` / 직전 결과 = 커밋 SHA·stat
#
# 판정 3종을 전부 통과해야 다음 단계로 간다(하나라도 실패 = 즉시 중단):
#   ① claude JSON: is_error=false 이고 permission_denials 비어 있음
#   ② gate-check.sh --run <엔트리> 전건 통과
#   ③ 레포 커밋이 최소 1건 늘었다 (back/front 각각 실행 전후 HEAD 비교)
#
# ⚠️ 권한은 bypassPermissions 로 돈다(무인이라 ask 에 답할 사람이 없다). 대신 CHAIN_RUN=1 을 export 해서
#    pre-bash-guard 가 운영 DB 쓰기 등 ask 대상을 exit 2 로 **승격**한다 — 훅의 exit 2 는 권한 모드와 무관하게 유효하다.
#
# 프로젝트 = CLAUDE_PROJECT_DIR > 이 러너가 든 저장소(<저장소>/.claude/bin/chain-run.sh) — 어느 cwd 에서 띄워도 같다.
#   TSV·--plan·엔트리의 상대 경로는 전부 그 저장소 기준이다. 다른 저장소를 돌릴 땐 env 와 **그 저장소의 러너**를 같이 준다
#   (`CLAUDE_PROJECT_DIR=<clone> bash <clone>/.claude/bin/chain-run.sh …` — env 는 export 해 두지 말고 명령 앞에 인라인으로).
#   TSV 소속 = 파일 동일성 `-ef`(사이에 .git 이 있으면 안쪽 저장소 것). env 없이 다른 저장소의 TSV 를 주거나, env 를 줬는데
#   러너도 TSV 도 그 프로젝트 밖이면(낡은 env) 시작 전 거부한다 — env 와 그 프로젝트의 러너를 같이 주면(명시 동의) 밖 TSV 도 받는다.
#   env 를 줬는데 러너가 프로젝트와 다른 저장소 것이거나 러너 위치를 모르면(stdin·<저장소>/.claude/bin 밖 사본) run 은
#   --allow-foreign-runner 가 있어야 시작한다(dry·verify 는 경고만 · 재개 줄이 싣는다 · 위 낡은 env 거부는 옵트인으로 안 풀린다).
#   --plan·--budget·--from 의 값은 `-` 로 시작할 수 없고 예산은 숫자다(값을 빠뜨린 `--budget --dry-run` 이 run 이 되던 것을 막는다).
#   중단 = 러너 **세션째**(docs/chains/README.md §중단) — 러너 PID 만 죽이면 진행 중 단계는 끝까지 돈다.

# ⚠️ 본문 전체가 `{ … }` 한 복합 명령이다(2026-10-14 리뷰 MED-1) — bash 는 스크립트를 실행하면서 증분으로 읽는다.
#   체인이 도는 중에 이 파일을 같은 inode 로 덮으면(cp·`>` = truncate+write) 옛 프로세스가 옛 바이트 오프셋부터 새 파일을
#   읽어, 끝난 체인을 문법 오류·엉뚱한 문장 실행으로 거짓 실패시킨다. 복합 명령은 닫는 `}` 까지 다 파싱된 뒤 실행되므로
#   덮어쓰기가 무해하다. 맨 끝 `exit` 은 브레이스 **안**에 둔다 — 밖이면 그 줄을 새 파일의 옛 오프셋에서 읽는다.
{
set -uo pipefail

# 러너 자신의 절대경로 — 프로젝트 폴백·재개 안내·--help 가 쓴다. 파일이 아니면(`bash -s < chain-run.sh`·프로세스 치환) 비운다.
SELF=""
if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SELF="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
fi

# ⚠️ 기본 예산 = 단계당 $30(2026-09-23 사용자 지시 — 15 로는 매번 모자랐다). 모자라면 단계가
#    error_max_budget_usd 로 잘리고, 게이트·커밋이 이미 끝났으면 "마무리만 잘린" 상태로 남는다.
#    ⚠️ 값은 여기 한 곳에만 산다 — 문서·훅 안내문은 플래그를 적지 않고 이 기본값을 따른다(3중 드리프트 재발 차단).
TSV=""; PLAN=""; BUDGET="30"; FROM=""; MODE="run"; ALLOW_FOREIGN_RUNNER=0
while (( $# )); do
  case "$1" in
    # 값 없는 옵션(마지막 인자)을 받으면 `shift 2` 가 실패해 같은 인자를 영원히 다시 읽는다 — 무한 루프(2026-10-15 발견)
    # 값이 `-` 로 시작하면 값을 빠뜨린 것이다(5차 리뷰 INFO-a) — 받아 주면 `--budget --dry-run` 이 `--dry-run` 을 예산 값으로 먹어
    #   **run 모드**가 됐다(사람은 dry 를 원했는데 claude 가 떴다 · 실측). 옵트인도 같은 식으로 먹혀 "붙여라" 로 거부당했다.
    --plan|--budget|--from)
      (( $# >= 2 )) || { echo "$1 에 값이 없다" >&2; exit 2; }
      [[ "$2" == -* ]] && { echo "$1 의 값이 옵션처럼 보인다: $2 — 값을 빠뜨렸는지 확인하라" >&2; exit 2; }
      case "$1" in --plan) PLAN="$2" ;; --budget) BUDGET="$2" ;; *) FROM="$2" ;; esac
      shift 2 ;;
    --dry-run)        MODE="dry"; shift ;;
    --verify-prompt)  MODE="verify"; shift ;;
    --allow-foreign-runner) ALLOW_FOREIGN_RUNNER=1; shift ;;   # 다른 저장소의 러너로 run 한다는 옵트인(아래 낡은 env 판정)
    -h|--help)        [[ -n "$SELF" ]] && awk 'NR > 1 && /^#/ { print; next } NR > 1 { exit }' "$SELF"; exit 0 ;;
    -*)               echo "알 수 없는 옵션: $1" >&2; exit 2 ;;
    *)                TSV="$1"; shift ;;
  esac
done
[[ -n "$TSV" ]] || { echo "사용법: chain-run.sh <체인TSV> [--plan F] [--budget USD(기본 $BUDGET)] [--from 단계ID] [--dry-run|--verify-prompt] [--allow-foreign-runner]" >&2; exit 2; }
# 예산은 숫자(USD)만 받는다(5차 리뷰 INFO-a) — 위 `-` 검사의 짝이다. 아무 문자열이나 `--max-budget-usd` 로 넘기면 유료 세션의 상한이 claude 의 해석에 달린다.
[[ "$BUDGET" =~ ^[0-9]+([.][0-9]+)?$ ]] || { echo "--budget 값은 숫자(USD)여야 한다: $BUDGET" >&2; exit 2; }

# ── 프로젝트 ──
# 종전 폴백은 egm 하드코딩이었다(egm 러너를 들여온 흔적). Claude Code Bash 도구 셸엔 CLAUDE_PROJECT_DIR 이 없어서
#   (2026-10-15 실측 unset) tbbe-re 에서 env 없이 띄우면 남의 프로젝트에서 `claude -p`(bypassPermissions)가 떴다.
# 러너 위치는 구조(`*/.claude/bin/*`)가 맞을 때만 믿는다 — 아니면 추측하지 않고 거부한다(stdin 실행이 `/` 로 떨어졌다).
# 논리 경로(`pwd`)로 둔다 — `-P`·readlink 로 풀면 `.claude` 를 심링크로 공유하는 저장소가 원본 저장소로 바뀐다.
if [[ -n "${CLAUDE_PROJECT_DIR:-}" ]]; then PROJECT_GIVEN="$CLAUDE_PROJECT_DIR"; PROJECT_SRC="env"
elif [[ "$SELF" == */.claude/bin/* ]]; then PROJECT_GIVEN="${SELF%/.claude/bin/*}"; PROJECT_SRC="러너 위치"
else PROJECT_GIVEN=""; PROJECT_SRC="없음"; fi
PROJECT=""
[[ -n "$PROJECT_GIVEN" ]] && PROJECT="$(CDPATH= cd -- "$PROJECT_GIVEN" 2>/dev/null && pwd)"
# 루트는 파일 동일성으로 본다 — `pwd` 는 `//` 를 `//` 로 남겨 문자열 `== /` 를 빠져나갔다(3차 리뷰 N4 실측)
if [[ -z "$PROJECT" || "$PROJECT" -ef / ]]; then
  # 받은 값을 그대로 보인다 — "출처: env" 만으로는 무엇이 틀렸는지 모른다(리뷰 N1)
  src_desc="$PROJECT_SRC"; [[ -n "$PROJECT_GIVEN" ]] && src_desc+="='$PROJECT_GIVEN'"
  echo "프로젝트 루트를 못 정했다(출처: $src_desc) — CLAUDE_PROJECT_DIR 을 주거나 러너를 <저장소>/.claude/bin/ 아래 파일로 실행하라" >&2
  exit 2
fi
# gate-check·자식 세션이 러너와 같은 프로젝트를 보게 싣는다 — 안 실으면 gate-check 가 cwd 를 프로젝트로 잡아
#   CHECK 를 엉뚱한 곳에서 돌리고 증거를 엉뚱한 원장에 쓴다.
export CLAUDE_PROJECT_DIR="$PROJECT"
echo "▶ 프로젝트 = $PROJECT (출처: $PROJECT_SRC)" >&2
# 러너가 든 저장소 — 러너 위치 출처면 PROJECT 와 같다. env 가 다른 저장소를 가리키면 둘이 갈린다(foreign_runner=1).
#   비교는 파일 동일성 — 문자열이면 같은 저장소의 다른 철자(심링크 경로 vs 물리 경로)를 다른 저장소로 본다(3차 리뷰 L1).
#   아래 경고(N2)·낡은 env 거부(I1)·run 옵트인·재개 줄(4차 리뷰 LOW-1)이 이 한 값을 쓴다 — 곳마다 술어를 다시 쓰면 한 곳의 회귀는
#   그곳 전용 케이스가 있어야만 잡힌다(4차 리뷰 LOW-2 실측: I1 쪽 `-ef` 만 문자열로 바꿔도 체인 회귀가 전부 통과했다).
own_repo=""; foreign_runner=0; runner_unknown=0
[[ "$SELF" == */.claude/bin/* ]] && own_repo="$(CDPATH= cd -- "${SELF%/.claude/bin/*}" 2>/dev/null && pwd)"
[[ "$PROJECT_SRC" == env && -n "$own_repo" ]] && ! [[ "$own_repo" -ef "$PROJECT" ]] && foreign_runner=1
# 러너가 든 저장소를 모르면(stdin·프로세스 치환·<저장소>/.claude/bin 밖 사본·PATH 의 심링크) env 와 대조할 길이 없다 — 낡은 env 를
#   줘도 경고 0줄로 env 쪽에서 유료 세션·커밋이 났다(5차 리뷰 LOW-B 실측 · 3차 실험 사고도 이 꼴). 대조 불가 = 같은 위험이라 아래
#   run 옵트인을 같이 건다. I1(밖 TSV)은 러너 저장소가 있을 때의 판정이라 여기엔 걸지 않는다.
[[ "$PROJECT_SRC" == env && -z "$own_repo" ]] && runner_unknown=1
# env 가 러너 저장소가 아닌 곳을 가리키거나 러너 위치를 모르면 한 줄 알린다(리뷰 N2) — dry·verify 는 이 경고뿐이고, run 은 아래 옵트인이 가른다.
if (( foreign_runner )); then
  echo "⚠ 러너 저장소($own_repo) ≠ 프로젝트 — CLAUDE_PROJECT_DIR 이 다른 저장소를 가리킨다(의도한 것인지 확인)" >&2
elif (( runner_unknown )); then
  echo "⚠ 러너 위치를 모른다(${SELF:-stdin·프로세스 치환} — <저장소>/.claude/bin/ 아래 파일이 아니다) — 프로젝트($PROJECT)의 러너인지 대조할 수 없다(의도한 것인지 확인)" >&2
fi

STATE_DIR="$PROJECT/.claude/hooks/.state"
LEDGER="$STATE_DIR/chain-runs.jsonl"
GATE_CHECK="$PROJECT/.claude/hooks/lib/gate-check.sh"
WH_DIR="$PROJECT/docs/work-history"
# 판정 대상 레포 결정 — CHAIN_REPOS(셀프테스트·수동 지정) > 프로젝트 루트가 git 이면 자기 자신(단일 레포)
# > egm 2레포(루트가 비-git 인 유일한 구조라 폴백으로 남긴다)
# ⚠️ `.git` 은 `-e` 로 본다(이 파일 전체) — git worktree 의 `.git` 은 디렉터리가 아니라 파일이다.
#    파일이 가리키는 곳이 실재하는지는 preflight 가 `git rev-parse` 로 확인한다(깨진 gitdir 파일도 `-e` 는 통과한다).
if [[ -n "${CHAIN_REPOS:-}" ]]; then read -r -a REPOS <<< "$CHAIN_REPOS"
elif [[ -e "$PROJECT/.git" ]];  then REPOS=(".")
elif [[ -e "$PROJECT/tbbe-naver-back/.git" && -e "$PROJECT/tbbe-naver-front/.git" ]]; then
  REPOS=(tbbe-naver-back tbbe-naver-front)   # egm — 루트가 비-git 이고 하위에 2레포가 있는 유일한 구조
else
  REPOS=(".")                                # 비-git 이고 하위 레포도 없다 → preflight 가 거부한다
fi

[[ "$TSV" = /* ]] || TSV="$PROJECT/$TSV"
[[ -f "$TSV" ]] || { echo "체인 파일 없음: $TSV" >&2; exit 2; }
# 논리 경로로 정규화한다(`.`·`..` — PROJECT 와 같은 규칙). 아래 소속 검사와 재개 안내가 이 값을 쓴다.
tsv_given="$TSV"
tsv_dir="$(CDPATH= cd -- "$(dirname -- "$TSV")" 2>/dev/null && pwd)" || { echo "체인 파일 디렉터리에 들어갈 수 없다: $TSV" >&2; exit 2; }
TSV="${tsv_dir%/}/$(basename -- "$TSV")"
# 위 `-f` 가 본 파일(커널은 `..` 를 물리로 푼다) ≠ 이제 읽을 파일(논리 정규화)이면 거부한다(3차 리뷰 L2) — 심링크 바로 뒤 `..`
#   (`<P>/lnk/../docs/chains/c.tsv`)에서 둘이 갈려 다른 체인을 조용히 조립했다(env 출처도 같다). 정규화를 물리(`cd -P`)로 바꾸면
#   모호는 사라지지만 프로젝트 안 심링크 디렉터리 아래 TSV 를 "프로젝트 밖" 으로 새로 거부한다 — 논리 경로 결정과 반대라 택하지 않았다.
[[ "$TSV" -ef "$tsv_given" ]] || {
  echo "체인 파일 경로가 모호하다(심링크 뒤 '..'): 준 경로는 $(realpath -- "$tsv_given" 2>/dev/null || printf '%s' "$tsv_given") 를, 정규화한 경로는 $TSV 를 가리킨다 — '..' 없는 경로로 줘라" >&2
  exit 2; }
# 소속 = TSV 디렉터리에서 위로 올라가며 프로젝트와 **같은 디렉터리(-ef)** 를 만나는가(3차 리뷰 L1) — 문자열 접두 비교는 같은 저장소도
#   철자가 다르면(심링크 경로 vs 물리 경로 · PWD 없이 띄운 러너의 물리 SELF) "프로젝트 밖" 으로 거짓 거부했다. 프로젝트에 닿기 전에
#   `.git`(디렉터리·worktree 파일)을 만나면 그 안쪽 저장소(중첩 clone·worktree) 것이다.
tsv_foreign="프로젝트 밖"; d="$tsv_dir"; nested=""
while [[ -n "$d" ]]; do
  [[ "$d" -ef "$PROJECT" ]] && { tsv_foreign="${nested:+프로젝트 안 다른 저장소 $nested}"; break; }
  [[ -z "$nested" && -e "$d/.git" ]] && nested="$d"
  d="${d%/*}"
done
# 거부 문구의 권고는 env 와 그 저장소 러너를 **같이** 준다 — 러너만 바꾸라고 하면 옛 판 러너 사본(egm 폴백)은 env 없이 egm 으로 간다(3차 리뷰 L4)
how_other="그 저장소에서 돌리려면 CLAUDE_PROJECT_DIR=<그 저장소> bash <그 저장소>/.claude/bin/chain-run.sh … 로 띄워라(env 를 같이 주면 옛 판 러너 사본도 같은 곳을 본다)"
# 러너 위치로 정한 프로젝트면 TSV 도 그 저장소 것이어야 한다(리뷰 MED-1) — env 없이 `bash <main>/.claude/bin/chain-run.sh
#   <clone>/docs/chains/X.tsv` 로 띄우면 PROJECT=main 이 되어 엔트리·claude cwd·커밋·게이트·락이 전부 main 으로 간다(격리 의도와
#   정반대 · 리뷰 실측 커밋 main 3/clone 1). --plan 은 검사하지 않는다(~/.claude/plans 실사용).
if [[ -n "$tsv_foreign" && "$PROJECT_SRC" == "러너 위치" ]]; then
  echo "체인 파일이 러너 저장소 것이 아니다($tsv_foreign): $TSV — 프로젝트 = $PROJECT(출처: 러너 위치). $how_other" >&2
  exit 2
fi
# env 출처는 사람이 프로젝트를 명시한 것이라 다른 곳의 TSV 도 받는다(러너 = 그 프로젝트 것일 때 — 명시 동의). 거울상은 거부한다(3차 리뷰 I1):
#   러너도 TSV 도 그 프로젝트 밖이면 env 가 낡은 것이다 — 낡은 `CLAUDE_PROJECT_DIR=main` + clone 러너 + clone TSV 가 경고 1줄만 내고
#   main 엔트리로 조립됐다(실측). 전 모드에 건다(정적 판정 — 아래 옵트인과 무관).
if [[ -n "$tsv_foreign" ]] && (( foreign_runner )); then
  echo "체인 파일이 프로젝트 것이 아니다($tsv_foreign): $TSV — 프로젝트 = $PROJECT(출처: env)인데 러너도 다른 저장소($own_repo) 것이다. 낡은 CLAUDE_PROJECT_DIR 인지 확인하라 — $how_other" >&2
  exit 2
fi
# 다른 저장소의 러너로 run 하려면 옵트인을 요구한다(4차 리뷰 LOW-1 · 2026-10-15 사용자 결정). 위 I1 은 TSV 가 프로젝트 밖일 때만
#   잡는다 — export 로 남은 낡은 `CLAUDE_PROJECT_DIR=<main>` + clone cwd + clone 러너 + **상대** TSV 는 TSV 가 `<main>/docs/chains/…`
#   로 풀려 I1 을 지나고, 경고 1줄 뒤 main 에서 유료 세션·커밋을 냈다(실측). 정상 사용(main 러너 + `CLAUDE_PROJECT_DIR=<clone>`)과
#   입력이 대칭이라 러너가 둘을 가를 수 없다 — 사람이 옵트인으로 가른다. dry·verify 는 읽기 전용이라 위 경고만 낸다.
#   러너 위치를 모를 때도 같다(5차 리뷰 LOW-B — 대조할 수 없으면 같은 위험). 옵트인은 위 I1 을 풀지 않는다(I1 이 먼저 끝낸다).
if [[ "$MODE" == run ]] && (( (foreign_runner || runner_unknown) && ! ALLOW_FOREIGN_RUNNER )); then
  prj_runner="$PROJECT/.claude/bin/chain-run.sh"
  {
    if (( foreign_runner )); then
      echo "러너 저장소($own_repo) ≠ 프로젝트($PROJECT · 출처: env) — 다른 저장소의 러너로는 옵트인 없이 run 하지 않는다(낡은 CLAUDE_PROJECT_DIR 이면 엉뚱한 저장소에서 유료 세션·커밋이 난다)."
      # 처방은 두 방향이다(5차 리뷰 LOW-A) — 이 검사의 계기는 "셸에 남은 export 가 낡았고 사람은 러너 저장소를 돌리려 했다" 인데,
      #   종전 문구는 프로젝트(= 그 낡은 env) 쪽 명령 하나만 '권장' 으로 보여 그 줄을 따르면 막으려던 사고가 그대로 났다(실측).
      printf '  %s 를 돌리려던 것이면(셸에 남은 export 가 낡았다): env -u CLAUDE_PROJECT_DIR bash %q … — 그 뒤 unset CLAUDE_PROJECT_DIR\n' "$own_repo" "$SELF"
    else
      echo "러너 위치를 모른다(${SELF:-stdin·프로세스 치환}) — 프로젝트($PROJECT · 출처: env)의 러너인지 대조할 수 없어 옵트인 없이 run 하지 않는다(낡은 CLAUDE_PROJECT_DIR 이면 엉뚱한 저장소에서 유료 세션·커밋이 난다)."
    fi
    # 프로젝트 쪽 처방 — 그 러너가 없으면 없는 파일을 권하지 않는다(5차 보강 LOW-1). 있는데 이 러너와 판이 다르면 알린다 — 옛 판이면
    #   락·MODE 가드·이 옵트인이 없다(형제 clone 의 러너가 cb6b8bdf 판인 것이 실측됐다 · 5차 LOW-A). stdin 러너는 대조할 파일이 없다.
    if [[ -f "$prj_runner" ]]; then
      printf '  %s 를 돌리려던 것이면: 그 프로젝트의 러너로 CLAUDE_PROJECT_DIR=%q bash %q … · 이 러너로 돌리는 것이 맞으면 --allow-foreign-runner 를 붙여라\n' "$PROJECT" "$PROJECT" "$prj_runner"
      [[ -n "$SELF" ]] && ! cmp -s -- "$SELF" "$prj_runner" && echo "  ⚠ 그 프로젝트의 러너는 이 러너와 판이 다르다(옛 판이면 락·MODE 가드·이 옵트인이 없다)"
    else
      echo "  $PROJECT 를 돌리려던 것이면: 그 프로젝트에는 러너가 없다 — 이 러너로 돌리려면 --allow-foreign-runner 를 붙여라"
    fi
    echo "  CLAUDE_PROJECT_DIR 은 export 해 두지 말고 명령 앞에 인라인으로 준다"
  } >&2
  exit 2
fi
[[ -n "$PLAN" && "$PLAN" != /* ]] && PLAN="$PROJECT/$PLAN"
# 없는 플랜을 조용히 빼면 dry-run 은 GATE_OK 이고 run 은 플랜 없이 유료 세션을 띄운다(리뷰 LOW-5)
[[ -z "$PLAN" || -f "$PLAN" ]] || { echo "플랜 파일 없음: $PLAN" >&2; exit 2; }

CHAIN_NAME="$(basename "$TSV" .tsv)"

# ── TSV 읽기 (단계ID \t work-history엔트리 \t 지시) ──
IDS=(); ENTRIES=(); INSTRS=()
while IFS=$'\t' read -r id entry instr; do
  [[ -z "${id// }" || "$id" == \#* ]] && continue
  IDS+=("$id"); ENTRIES+=("$entry"); INSTRS+=("$instr")
done < "$TSV"
N=${#IDS[@]}
(( N )) || { echo "체인에 단계가 없다: $TSV" >&2; exit 2; }

# ── 엔트리 경로 — 프롬프트 조립과 사후 게이트가 이 해석 하나를 쓴다 ──
# cwd 가 아니라 $PROJECT 에 고정한다(TSV·--plan 과 같은 규칙). 게이트만 cwd 상대로 풀던 때, 다른 레포를
# cwd 로 띄운 러너가 그 레포에 없는 엔트리를 "파싱 실패: 파일 없음" 으로 읽어 게이트 6/6 을 끝낸 단계를
# "수용 게이트 미통과" 로 판정하고 체인을 멈췄다(2026-10-14 격리 clone 체인 S4b-1).
entry_file() {
  local e="$1"
  if [[ "$e" == /* ]]; then printf '%s' "$e"
  elif [[ "$e" == */* ]]; then printf '%s/%s' "$PROJECT" "$e"
  else printf '%s/%s' "$WH_DIR" "$e"; fi
}

# ── 프롬프트 조립 ──
# 재료 5종을 모두 넣는다. 하나라도 빠지면 새 세션이 맥락 없이 헤맨다.
build_prompt() {
  local i="$1" prev_block="$2"
  local id="${IDS[$i]}" entry="${ENTRIES[$i]}" instr="${INSTRS[$i]}"
  local ef; ef="$(entry_file "$entry")"
  [[ -f "$ef" ]] || { echo "work-history 엔트리 없음: $ef" >&2; return 1; }
  # 완료 조건을 기계로 판정하려면 엔트리에 수용 게이트 절이 반드시 있어야 한다.
  grep -qE '^##[[:space:]]*수용[[:space:]]*게이트' "$ef" || { echo "수용 게이트 절 없음: $entry" >&2; return 1; }

  printf '[체인 %s · 단계 %d/%d · %s]\n\n%s\n\n' "$CHAIN_NAME" "$((i+1))" "$N" "$id" "$instr"
  printf '## 이번 단계의 작업 지시서 (%s)\n\n' "${ef#"$PROJECT"/}"
  cat "$ef"
  if [[ -n "$PLAN" && -f "$PLAN" ]]; then
    printf '\n\n## 승인된 플랜 (전체 설계 의도 — 이번 단계 범위만 구현한다)\n\n'
    cat "$PLAN"
  fi
  printf '\n\n## 직전 단계 결과\n\n%s\n' "$prev_block"
  # 이 블록이 없으면 새 세션이 플랜 전문을 보고 끝까지 다 해버린다 — 그러면 세션을 쪼갠 의미가 사라진다.
  printf '\n## 완료 조건 (이것만 만족하면 끝)\n'
  printf -- '- 이 엔트리의 `## 수용 게이트` 전건 통과 (`bash .claude/hooks/lib/gate-check.sh --run %s`)\n' "${ef#"$PROJECT"/}"
  # agent-orchestrator-fork: 커밋 관례는 이 저장소의 AGENTS.md 를 따른다(영어 conventional commit · Claude Code 의 Co-Authored-By 줄 유지).
  printf -- '- 커밋 1건 이상 — 이 저장소 관례(AGENTS.md): 영어 conventional commit(예 `feat(lado): …`) · Claude Code 가 붙이는 Co-Authored-By 줄은 그대로 둔다\n'
  printf -- '- 이 엔트리의 After/회고 채우기\n'
  # 게이트 증거는 HEAD 에 결속돼 있어 커밋이 나면 죽는다 — 커밋 후 한 번 더 돌려 현재 HEAD 에 재기록시킨다.
  printf -- '- 커밋 **후에** 게이트를 한 번 더 실행해 증거를 현재 HEAD 에 재기록하라(증거는 HEAD 결속이라 커밋으로 죽는다)\n'
  printf -- '- **다음 단계는 별도 세션이 맡는다. 착수하지 마라.**\n'
  # 실전(2026-09-16)에서 세션이 체인 정의를 "정리"하다 단계 ID 를 바꿔 --from 재개가 0단계로 조용히 끝났다.
  printf -- '- **체인 정의 파일(docs/chains/*.tsv)을 수정·정리하지 마라.** 러너가 단계 ID 로 재개한다\n'
}

repo_path() { case "$1" in /*) printf '%s' "$1" ;; *) printf '%s/%s' "$PROJECT" "$1" ;; esac; }

# 직전 단계 블록
prev_summary() {
  local prev_id="$1"; shift
  if [[ -z "$prev_id" ]]; then echo "없음 — 이 체인의 첫 단계다."; return; fi
  echo "단계 $prev_id 완료."
  local r
  for r in "${REPOS[@]}"; do
    local rp; rp="$(repo_path "$r")"
    [[ -e "$rp/.git" ]] || continue
    echo "· $r — $(git -C "$rp" log -1 --format='%h %s' 2>/dev/null)"
    git -C "$rp" show --stat --format='' HEAD 2>/dev/null | tail -4 | sed 's/^/    /'
  done
}

heads_snapshot() {
  local r out=""
  for r in "${REPOS[@]}"; do
    local rp; rp="$(repo_path "$r")"
    [[ -e "$rp/.git" ]] && out+="$r:$(git -C "$rp" rev-parse HEAD 2>/dev/null) "
  done
  printf '%s' "$out"
}

ledger_row() {
  command -v jq >/dev/null 2>&1 || return 0
  mkdir -p "$STATE_DIR" 2>/dev/null
  jq -cn --arg ts "$(date -Iseconds)" --arg chain "$CHAIN_NAME" --arg step "$1" \
     --arg verdict "$2" --arg reason "$3" --arg sid "$4" \
     --argjson cost "${5:-0}" --argjson turns "${6:-0}" --arg model "${7:--}" \
     '{ts:$ts,chain:$chain,step:$step,verdict:$verdict,reason:$reason,session:$sid,cost_usd:$cost,turns:$turns,model:$model}' \
     >> "$LEDGER" 2>/dev/null || true
}

# ── --verify-prompt: 조립 프롬프트에 재료 5종이 다 있는지 전수 확인 ──
if [[ "$MODE" == "verify" ]]; then
  p="$(build_prompt 0 "$(prev_summary '')")" || exit 1
  need=(  '^\[체인 '  '## 이번 단계의 작업 지시서'  '## 승인된 플랜'  '## 직전 단계 결과'  '## 완료 조건' )
  names=( '단계헤더'   '작업지시서'                  '플랜전문'        '직전결과'           '완료조건' )
  ok=0
  for k in "${!need[@]}"; do
    if grep -qE "${need[$k]}" <<<"$p"; then ok=$((ok+1)); else echo "누락: ${names[$k]}"; fi
  done
  echo "재료 $ok/${#need[@]}"
  (( ok == ${#need[@]} )) && echo GATE_OK
  exit $(( ok == ${#need[@]} ? 0 : 1 ))
fi

# ── preflight: 전제를 실행 **전에** 검사한다 ──
# 단계를 띄우고 나서 판정 불가를 알게 되면 API 비용만 쓰고 아무것도 못 판정한다.
if [[ "$MODE" == "run" ]]; then
  pf_fail=""
  [[ -f "$GATE_CHECK" ]] || pf_fail="게이트 러너 없음: $GATE_CHECK — 완료를 기계로 판정할 수 없다"
  if [[ -z "$pf_fail" ]]; then
    # 판정 대상 레포마다 **git 최상위가 그 디렉터리 자신(-ef)인가** 를 본다(3차 리뷰 L3·N5). 종전 `-e .git` + `rev-parse --git-dir` 는
    #   깨진 `.git` 파일(리뷰 LOW-1)은 걸렀지만 상위 저장소 안의 **빈 `.git` 디렉터리**를 통과시켰다 — git 이 그것을 건너 상위
    #   저장소를 찾아, 커밋이 상위로 들어가고도 판정 ③ 이 통과했다(실측). 최상위 대조 하나가 깨진 `.git` 파일·빈 `.git` 디렉터리·
    #   상위 저장소 안 비-git 디렉터리를 모두 거른다. 실패 사유에 git 이 한 말 1줄을 싣는다.
    for r in "${REPOS[@]}"; do
      rp="$(repo_path "$r")"
      if top="$(git -C "$rp" rev-parse --show-toplevel 2>/dev/null)"; then
        [[ "$top" -ef "$rp" ]] && continue
        why="git 최상위 = $top"
      else
        why="$(git -C "$rp" rev-parse --show-toplevel 2>&1 >/dev/null | head -n 1)"
      fi
      pf_fail="git 레포 아님: $rp (${why:-원인 미상}) — 판정 ③(커밋 증가)이 성립하지 않는다"; break
    done
  fi
  command -v flock >/dev/null 2>&1 || pf_fail="${pf_fail:-flock 없음 — 같은 프로젝트의 체인 이중 기동을 막을 수 없다}"
  if [[ -n "$pf_fail" ]]; then
    echo "[preflight] $pf_fail" >&2
    echo "[preflight] 실행하지 않는다." >&2
    exit 2
  fi
  # 이중 기동 차단 — 이 락이 막는 것은 **같은 워크트리**에 체인 세션이 둘 도는 것이다(반쯤 고친 파일·인덱스 교차 ·
  #   chain-last-result.json 덮어쓰기). 종전엔 egm 폴백이 우연히 막고 있었다(env 없이 띄운 둘째 러너는 egm 으로 갔다 — 리뷰 LOW-6).
  #   ⚠️ 재사용 Testcontainers 오염은 이 락의 범위 밖이다(리뷰 MED-2) — TC 는 Docker 데몬 전역이고 게이트 CHECK 가 쥐는 상대
  #   `gradle-build.lock` 은 clone 마다 다른 파일이라, clone 두 곳의 체인이 동시에 gradle 게이트를 돌리면 그대로 섞인다
  #   (2026-09-29 겹친 회차 15건 RED). clone 병행 절차 = README(clone 의 gradle 락을 main 락으로 심링크).
  #   run 모드만 잡는다(dry·verify 는 읽기 전용). 락은 프로세스가 죽으면 커널이 푼다(stale 없음).
  #   ⚠️ fd 9 는 자식에게 넘기지 않는다(`9>&-`) — 자식 세션·CHECK 가 띄운 장수 프로세스(`nohup ./gradlew bootRun &` 의 클라이언트
  #   JVM·`tail -f &` 등)가 물려받으면 러너가 끝나도 락이 남는다. gradle **데몬**은 JVM 이 fd 를 끊어 안 물려받는다
  #   (scripts/gate/lib/pc-guard.sh 실측). pc-guard 가 반대로 상속시키는 것은 그 락이 gradle 이 끝날 때까지 덮어야 해서고,
  #   이 락은 러너 수명 + **진행 중 단계**만 덮는다 — claude·gate-check 를 각각 기다리는 대기 서브셸(러너의 fork)이 fd 9 를 쥐어,
  #   러너만 죽어도 그 단계가 끝날 때까지 락이 남는다(3차 리뷰 M1 — 종전엔 게이트 단계에서 러너가 죽으면 CHECK 가 락 밖에서 돌았다).
  mkdir -p "$STATE_DIR" 2>/dev/null
  lock_file="$STATE_DIR/chain-run.lock"
  exec 9>"$lock_file" || { echo "[preflight] 락 파일을 못 연다: $lock_file — 실행하지 않는다." >&2; exit 2; }
  if ! flock -n 9; then
    echo "[preflight] 이 프로젝트에서 체인이 이미 돌고 있다($lock_file) — 끝난 뒤 다시 띄워라." >&2
    # 보유자 PID 를 파일에 적지 않는다 — 경쟁자의 `exec 9>` 가 truncate 해서 지운다(리뷰 N3). 확인 명령을 준다.
    #   중단 절차를 같이 가리킨다 — 고아 단계면 fuser 에는 대기 서브셸 bash 만 보이고, 그것만 죽이면 락만 풀린 채 단계가 계속 돈다(3차 리뷰 N2).
    printf '[preflight] 보유자 확인: fuser -v %q · 멈추려면 docs/chains/README.md §중단(보유자가 대기 서브셸 bash 뿐이면 그것만 죽여도 락만 풀리고 단계는 계속 돈다)\n' "$lock_file" >&2
    echo "[preflight] 실행하지 않는다." >&2
    exit 2
  fi
  # 차단은 아니지만 눈에 보이게 한다 — bypassPermissions 로 도는데 승격할 훅이 없다는 뜻이다.
  [[ -f "$PROJECT/.claude/hooks/pre-bash-guard.sh" ]] || \
    echo "[preflight] ⚠ pre-bash-guard.sh 없음 — CHAIN_RUN 승격이 걸리지 않는다(bypassPermissions 무방비)." >&2
fi

# ── 본 루프 ──
export CHAIN_RUN=1          # pre-bash-guard 가 ask 를 exit 2 로 승격하는 신호
started=0; prev_id=""; assembled=0
for i in "${!IDS[@]}"; do
  id="${IDS[$i]}"
  if [[ -n "$FROM" && $started -eq 0 ]]; then
    [[ "$id" == "$FROM" ]] && started=1 || { prev_id="$id"; continue; }
  fi
  PROMPT="$(build_prompt "$i" "$(prev_summary "$prev_id")")" || exit 1
  assembled=$((assembled+1))
  # 프롬프트는 매 단계 새 세션에 통째로 실린다 — 플랜 전문이 크면 단계 수만큼 곱해져 턴당 단가를 밀어올린다.
  # (실전: 플랜 914줄 ~90KB → 발췌 22.6KB 로 4배 축소)
  if (( ${#PROMPT} > 40000 )); then
    echo "⚠ 단계 $id 프롬프트 ${#PROMPT}자 — 플랜 전문이 큽니다. 해당 단계 발췌본을 --plan 으로 주면 턴당 단가가 내려갑니다." >&2
  fi

  if [[ "$MODE" == "dry" ]]; then
    printf '%s\n' "$PROMPT"
    printf '\n----- (단계 %s · %d자) -----\n\n' "$id" "${#PROMPT}"
    prev_id="$id"; continue
  fi

  # dry·verify 가 여기까지 오면 버그다 — 아래는 유료 세션(bypassPermissions)을 띄운다. dry 분기를 깬 사본이 dry-run 꼴 실행에서
  #   claude 를 띄우는 것이 실측됐다(3차 리뷰 L5) — 분기 하나에 기대지 않고 기동 앞에서 한 번 더 막는다.
  [[ "$MODE" == run ]] || { echo "BUG: $MODE 모드가 claude 기동 지점에 왔다 — 실행하지 않는다" >&2; exit 3; }
  echo "▶ 단계 $((i+1))/$N · $id — 새 세션 시작 (예산 \$$BUDGET)"
  before="$(heads_snapshot)"
  # ⚠️ 2>&1 금지 — Stop 훅(post-work-check 등)이 stderr 로 쓰는 경고가 JSON 에 섞이면 jq 파싱이 깨지고
  #    `.is_error // true` 기본값이 잡혀 **성공한 단계가 실패로 오판**된다(2026-09-16 G6 에서 실제로 발생).
  ERRF="$(mktemp)"
  # ⚠️ 이 명령 치환 서브셸(러너의 fork)이 fd 9 를 쥔 채 claude 를 기다린다 — 러너만 SIGTERM 되면 자식 세션은 계속 돌아 커밋까지
  #   하는데(리뷰 LOW-3 실측), 그동안 다른 체인이 끼어들지 못하게 락을 지키는 것이 이 서브셸이다. 끝의 `; exit $?` 가 claude 를
  #   서브셸의 마지막 명령에서 빼 bash 판과 무관하게 서브셸을 남긴다(마지막 단순 명령은 bash 가 exec 로 갈음할 수 있다 —
  #   5.2.21 은 명령 치환 안에선 리디렉션이 붙으면 안 갈음하지만 판 의존이다 · 3차 리뷰 M1 ② · `( … )` 서브셸은 5.2.21 도
  #   갈음한다 — 아래 게이트 단계). `exec claude …` 꼴로 고치면 서브셸이 claude 로 바뀌어 락이 먼저 풀린다
  #   (회귀 `chain: 러너만 죽어도`). 전부 멈추려면 러너 **세션째** 죽인다(README §중단 — trap 으로 그룹·세션을 죽이는 안은
  #   setsid 없이 띄운 러너에선 호출자 셸까지 칠 수 있어 기각).
  OUT="$(cd "$PROJECT" && claude -p "$PROMPT" \
          --output-format json \
          --permission-mode bypassPermissions \
          --max-budget-usd "$BUDGET" 2>"$ERRF" 9>&-; exit $?)"
  rc=$?

  # 진단용 원문 보존 — JSON 이 아니면 파싱이 조용히 기본값(실패)으로 떨어지므로 원문이 있어야 판별된다
  RAWF="$STATE_DIR/chain-last-result.json"; mkdir -p "$STATE_DIR" 2>/dev/null; printf '%s' "$OUT" > "$RAWF"
  sid="$(jq -r '.session_id // "-"' <<<"$OUT" 2>/dev/null || echo -)"
  cost="$(jq -r '.total_cost_usd // 0' <<<"$OUT" 2>/dev/null || echo 0)"
  turns="$(jq -r '.num_turns // 0' <<<"$OUT" 2>/dev/null || echo 0)"
  # 해석된 모델을 단계마다 박제한다 — 러너는 `--model` 을 주지 않으므로 자식 세션이 프로젝트 settings 의
  # 별칭(`model: opus`)을 스스로 푼다. 부모 프로세스 env 에 ANTHROPIC_DEFAULT_<FAMILY>_MODEL 핀이 남아 있으면
  # 그 별칭이 다른 세대로 조용히 붙는다(2026-09-28 실측 — 세션에 잔존한 핀이 opus→claude-opus-5 로 고정시켰다).
  # 무인 체인은 사람이 화면을 안 보므로, 값이 결과 JSON 에만 있고 출력·원장에 없으면 사후에도 확인할 길이 없다.
  model="$(jq -r '(.modelUsage // {}) | keys | join(",")' <<<"$OUT" 2>/dev/null || echo -)"
  [[ -n "$model" ]] || model="-"
  # ⚠️ `.is_error // true` 금지 — jq 의 `//` 는 LHS 가 null 뿐 아니라 **false 일 때도** RHS 를 돌려준다.
  #    즉 정상(is_error:false)을 실패로 뒤집는다(2026-09-16 G6 에서 실제로 두 번 오판). has() 로 존재를 먼저 본다.
  iserr="$(jq -r 'if has("is_error") then (.is_error|tostring) else "true" end' <<<"$OUT" 2>/dev/null || echo true)"
  denials="$(jq -r '(.permission_denials // []) | length' <<<"$OUT" 2>/dev/null || echo 0)"

  subtype="$(jq -r '.subtype // "-"' <<<"$OUT" 2>/dev/null || echo -)"

  # ⚠️ 판정 순서가 중요하다(2026-09-16 S3.5 실전에서 드러남).
  #    먼저 **객관적 증거**(게이트·커밋)를 보고, 프로세스 종료 코드는 그 다음에 해석한다.
  #    rc 를 먼저 보면 "작업은 다 끝났는데 마무리 중 예산 상한에 걸린" 단계가 실패로 뭉개진다 —
  #    S3.5 는 12파일 +847 커밋 + 게이트 6/6 을 끝내고 error_max_budget_usd 로 rc=1 을 냈다.

  # ② 게이트 — --run 은 재실행이라 커밋으로 죽은 증거를 현재 HEAD 에 다시 기록한다(증거 복구 경로)
  #   claude 단계와 같은 락 모델이다: 대기 서브셸 `( … ; exit $? )` 이 fd 9 를 쥐고 gate-check·CHECK 에는 넘기지 않는다 — 러너만
  #   죽어도 진행 중 CHECK 가 끝날 때까지 락이 남는다(3차 리뷰 M1 ① — 종전엔 러너가 gate-check 를 직접 기다려 러너와 함께 락이
  #   풀리고 CHECK 는 락 밖에서 끝까지 돌았다 · 회귀 `chain: 게이트 단계에서`).
  #   ⚠️ 여기선 `; exit $?` 가 락을 쥐는 **실제 장치**다 — 명령 치환과 달리 `( … )` 는 bash 5.2.21 도 마지막 명령을 리디렉션째
  #   exec 로 갈음해, 걷으면 서브셸이 fd 9 를 닫은 gate-check 로 바뀌고 락이 러너와 함께 풀린다(4차 리뷰 NIT-1 실측 · 걷으면 위 회귀 FAIL).
  gate_ok=1
  ( bash "$GATE_CHECK" --run "$(entry_file "${ENTRIES[$i]}")" >/dev/null 2>&1 9>&-; exit $? ) || gate_ok=0
  # ③ 커밋 증가
  commit_ok=1
  [[ "$(heads_snapshot)" == "$before" ]] && commit_ok=0

  fail=""; warn=""
  if (( gate_ok && commit_ok )); then
    # 작업은 객관적으로 완료됐다. 종료 코드는 진단 정보로만 쓴다.
    if [[ "$subtype" == "error_max_budget_usd" ]]; then
      warn="예산 소진(\$$BUDGET) — 작업은 완료됐으나 마무리가 잘렸을 수 있다(After/회고 확인). 다음 단계 예산을 올려라"
    elif (( rc != 0 )); then
      warn="claude rc=$rc · subtype=$subtype — 게이트·커밋은 통과했다"
    elif [[ "$denials" != "0" ]]; then
      warn="권한 거부 ${denials}건 — 게이트·커밋은 통과했다(훅 차단이 있었는지 확인)"
    fi
  else
    (( gate_ok ))   || fail="수용 게이트 미통과"
    (( commit_ok )) || fail="${fail:+$fail · }커밋 없음 — 판정 대상 레포 HEAD 가 그대로다"
    [[ "$subtype" == "error_max_budget_usd" ]] && fail="$fail (예산 \$$BUDGET 소진 — 작업을 못 끝냈다. 예산을 올려 --from 재개)"
    [[ -z "$fail" ]] && fail="claude rc=$rc · subtype=$subtype"
  fi

  if [[ -n "$fail" ]]; then
    ledger_row "$id" block "$fail" "$sid" "$cost" "$turns" "$model"
    {
      echo "✖ 단계 $id 실패 — $fail"
      echo "  세션 $sid · \$${cost} · ${turns}턴 · 모델 ${model}"
      echo "  남은 단계: ${IDS[*]:$i}"
      # 복사해 어느 셸에 붙여도 같은 프로젝트·같은 러너로 뜨게 절대경로 + cwd 고정 + 인용(%q). 종전 안내는 무인용
      #   (공백 경로 rc=125·괄호 경로 문법 오류 — 형제 `/home/code/project/C(H)OLD` 실재)·cwd 상대 러너 경로였다(리뷰 MED-2).
      resume=(env -C "$PROJECT" "CLAUDE_PROJECT_DIR=$PROJECT")
      [[ -n "${GATE_TIMEOUT:-}" ]] && resume+=("GATE_TIMEOUT=$GATE_TIMEOUT")
      [[ -n "${CHAIN_REPOS:-}" ]] && resume+=("CHAIN_REPOS=$CHAIN_REPOS")
      resume+=(bash "${SELF:-$PROJECT/.claude/bin/chain-run.sh}" "$TSV" --from "$id")
      [[ -n "$PLAN" ]] && resume+=(--plan "$PLAN")
      resume+=(--budget "$BUDGET")
      # 다른 저장소의 러너로 돈 체인이면 옵트인을 같이 싣는다 — 재개 줄은 env 를 싣기 때문에 빠뜨리면 그 줄이 옵트인 검사에 막힌다(4차 LOW-1).
      #   위치를 모르는 러너 사본도 같다(5차 LOW-B). stdin 러너(SELF 없음)는 재개 줄이 프로젝트의 러너를 띄우므로 싣지 않는다.
      if (( foreign_runner )) || { (( runner_unknown )) && [[ -n "$SELF" ]]; }; then resume+=(--allow-foreign-runner); fi
      printf '  재개:'; printf ' %q' "${resume[@]}"; printf '\n'
      [[ -s "$ERRF" ]] && { echo "  --- 세션 stderr 말미 ---"; tail -5 "$ERRF" | sed 's/^/  /'; }
      echo "  --- 결과 원문 앞 300자 ($RAWF) ---"; head -c 300 "$RAWF" | sed 's/^/  /'; echo
    } >&2
    rm -f "$ERRF"
    exit 1
  fi

  rm -f "$ERRF"
  ledger_row "$id" allow "${warn:-ok}" "$sid" "$cost" "$turns" "$model"
  [[ -n "$warn" ]] && echo "  ⚠ $warn" >&2
  echo "  ✓ $id 통과 — \$${cost} · ${turns}턴 · 모델 ${model} · 세션 $sid"
  prev_id="$id"
done

if (( assembled == 0 )); then   # dry 포함 전 모드 — 0단계는 어떤 모드에서도 정상이 아니다
  # --from 이 어떤 단계 ID 와도 안 맞으면 루프가 전건 skip 되고 조용히 "완료"가 된다(2026-09-16 실전).
  { echo "실행된 단계 0 — --from '${FROM:-}' 이 어떤 단계 ID 와도 맞지 않는다."
    echo "  이 체인의 단계 ID: ${IDS[*]}"; } >&2
  exit 1
fi
if [[ "$MODE" == "dry" ]]; then
  # --from 을 쓰면 앞 단계는 의도적으로 건너뛴다 — 기대치는 N 이 아니라 "재개 지점 이후 단계 수"다.
  expected="$N"
  if [[ -n "$FROM" ]]; then
    expected=0; seen=0
    for x in "${IDS[@]}"; do [[ "$x" == "$FROM" ]] && seen=1; (( seen )) && expected=$((expected+1)); done
  fi
  echo "조립 $assembled/$expected"
  (( assembled == expected )) && echo GATE_OK
  exit $(( assembled == expected ? 0 : 1 ))
fi
echo "체인 완료 — $CHAIN_NAME · $assembled/$N 단계 실행"
exit 0
}
