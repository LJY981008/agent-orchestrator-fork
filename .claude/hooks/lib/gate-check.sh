#!/usr/bin/env bash
# lib/gate-check.sh — 수용 게이트 러너 + 파서 (tbbe-re · 2026-08-25)
#
# 왜: 하네스의 게이트가 전부 **횡단 고정 검사**(컴파일·spotless·archTest·리뷰 흔적·WH 동반)라
#   "**이번 작업이 무엇을 만족해야 완료인가**"를 기계가 읽을 수 있게 박제하는 층이 없었다.
#   work-history 는 산문이라 훅이 내용을 판정하지 못한다 → "일부만 보고 전체를 추측한 완료 보고"가
#   커밋 seam 을 그냥 통과했다.
# 개념 출처: Leonxlnx/unlazy(MIT) — 수용원장 + CHECK/EXPECT 이중 조건 + 정직한 게이트 작성 규칙.
#   ⚠️ 그 저장소의 효과 수치는 저자가 스스로 철회했다(research/validation-protocol.md — 재현 불가).
#   따라서 **개념만** 차용하고 성능 주장은 인용하지 않는다. Depth Tree·병렬 dispatch·자체 Stop 훅
#   (6회 후 자동 해제 = fail-open)·승인 디렉토리는 우리 규율과 충돌해 채택하지 않았다.
#
# 데이터 모델 — **정의는 WH(모델 산출물), 증거는 원장(기계 산출물)**
#   정의: work-history 엔트리의 `## 수용 게이트` 섹션
#     - [ ] G1: <관측 가능한 결과>
#       CHECK: <셸 명령>
#       EXPECT: <출력에 반드시 포함될 성공 토큰>
#       CWD: <선택·저장소 상대>
#   증거: .claude/hooks/.state/gate-runs.jsonl (이 스크립트만 쓴다)
#   ⚠️ WH 본문에 `EVIDENCE:` 칸을 **두지 않는다** — 모델이 손으로 적을 수 있는 칸을 만들지 않는 것이
#     지문으로 방어하는 것보다 싸다(리뷰 마커·검수 섹션 같은 모델 산출물 게이트는 2026-08-19 하루에 3중으로 뚫렸다).
#
# 충족 = `rc == 0` ∧ 합친 출력(stdout+stderr)이 EXPECT 를 **고정 문자열**로 포함.
#   ⚠️ exit 0 만으로는 부족하다 — 파이프라인은 마지막 명령의 rc 만 보므로 조용히 성공한다.
#
# 위조 차단 3결속(원장 행이 유효하려면 전부 일치):
#   head      == 현재 HEAD   → 이전 커밋에서 통과시킨 증거 재사용 차단(crg 리뷰 마커와 동일 규율)
#   cmd_sha   == 현재 CHECK  → 통과 후 CHECK 를 몰래 바꾸기 차단
#   expect_sha== 현재 EXPECT → 통과 후 EXPECT 를 느슨하게 바꾸기 차단
#
# 이 파일은 **실행 가능 ∧ source 가능**이다. commit-review-gate 가 source 해서 같은 파서를 쓴다
#   (파서가 두 벌이면 갈라진다 — lint-patterns.sh 가 같은 이유로 SSOT 다).
#
# 사용:
#   gate-check.sh --status <wh.md...> [--only G3]   # 파싱 + 현재 충족 상태. CHECK 실행 0
#   gate-check.sh --run    <wh.md...> [--only G3]   # 실행 + 원장 기록
#   엔트리 경로 = 절대 또는 **저장소(GATE_PROJECT) 상대** — 호출 셸 cwd 와 무관하다
# 종료: 0=전건 met · 1=미충족/파싱에러 · 2=사용법

set -uo pipefail

GATE_TIMEOUT="${GATE_TIMEOUT:-300}"
GATE_PROJECT="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
GATE_RUNS="$GATE_PROJECT/.claude/hooks/.state/gate-runs.jsonl"
GATE_RUNS_MAX_BYTES="${GATE_RUNS_MAX_BYTES:-5242880}"

# 파서 산출물(전역 배열 — source 하는 쪽이 그대로 읽는다)
GATE_ID=(); GATE_DESC=(); GATE_CHECK=(); GATE_EXPECT=(); GATE_CWD=(); GATE_FILE=()
GATE_ERR=""

gate_sha() { printf '%s' "${1:-}" | sha256sum 2>/dev/null | cut -d' ' -f1; }

gate_head() { git -C "$GATE_PROJECT" rev-parse HEAD 2>/dev/null || echo none; }

# gate_parse <file...> — 섹션을 파싱해 전역 배열을 채운다. 0=성공, 1=파싱 에러(GATE_ERR 에 사유)
#   ⚠️ 코드펜스 안은 건너뛴다 — WH 엔트리는 승인된 플랜 전문을 백업하므로 **게이트 문법 예시가
#     펜스 안에 그대로 들어 있다**(이 기능의 첫 엔트리에서 실제로 발생). 펜스를 안 보면 예시가
#     진짜 게이트로 잡혀 영원히 unmet 이 된다.
#   ⚠️ 수동 게이트(CHECK/EXPECT 없는 항목)는 **미지원**이다 — 판정이 모델 산출물로 되돌아가
#     이 층의 존재 이유가 사라진다. 명령으로 못 재는 것은 게이트가 아니라 산문으로 쓴다.
gate_parse() {
  GATE_ID=(); GATE_DESC=(); GATE_CHECK=(); GATE_EXPECT=(); GATE_CWD=(); GATE_FILE=()
  GATE_ERR=""
  local f line in_sec fence cur_idx=-1 saw_section=0
  for f in "$@"; do
    # 상대 경로는 **GATE_PROJECT 기준**으로 푼다(2026-10-15) — CHECK 실행 cwd·원장·HEAD 가 전부 GATE_PROJECT 에 붙는데
    #   엔트리만 호출 셸 cwd 로 풀면, CLAUDE_PROJECT_DIR=<다른 저장소> 로 부를 때 cwd 쪽 **동명 엔트리를 조용히 파싱**하고
    #   (원장 키가 `../` 로 갈려 crg 조회가 영영 빗나간다) 없으면 "파일 없음" 으로 죽는다(체인 러너 S4b-1 거짓 실패의 뿌리).
    #   cwd 상대 재시도 폴백은 두지 않는다 — 규칙이 둘이면 또 갈린다. 대신 실패 문구가 해석 경로와 기준을 밝힌다.
    local given="$f"
    [[ "$f" == /* ]] || f="$GATE_PROJECT/$f"
    if [[ ! -f "$f" ]]; then
      GATE_ERR="파일 없음: $f"
      [[ "$given" == /* ]] || GATE_ERR+=" (상대 경로는 저장소 기준으로 푼다: $GATE_PROJECT — 호출 셸 cwd 무관)"
      return 1
    fi
    in_sec=0; fence=0
    # 경로는 **항상 저장소 상대로 정규화**한다 — 원장 키의 일부라, 같은 파일을 절대/상대로 부르면
    #   기록과 조회가 갈려 영원히 unmet 이 된다(crg 는 절대경로, 사람·러너는 아무 형태로나 부른다).
    local rel; rel="$(realpath --relative-to="$GATE_PROJECT" "$f" 2>/dev/null || echo "${f#"$GATE_PROJECT"/}")"
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" =~ ^[[:space:]]*(\`\`\`|~~~) ]]; then fence=$((1 - fence)); continue; fi
      (( fence )) && continue
      if [[ "$line" =~ ^##[[:space:]]*수용[[:space:]]*게이트 ]]; then in_sec=1; saw_section=1; continue; fi
      if (( in_sec )) && [[ "$line" =~ ^\#{1,6}[[:space:]] ]]; then in_sec=0; continue; fi
      (( in_sec )) || continue

      if [[ "$line" =~ ^-[[:space:]]*\[[[:space:]xX]\][[:space:]]*([A-Za-z0-9_.:-]+):[[:space:]]*(.*)$ ]]; then
        local id="${BASH_REMATCH[1]}" desc="${BASH_REMATCH[2]}"
        local i
        for i in "${!GATE_ID[@]}"; do
          [[ "${GATE_ID[$i]}" == "$id" ]] && { GATE_ERR="중복 게이트 ID: $id ($rel)"; return 1; }
        done
        GATE_ID+=("$id"); GATE_DESC+=("$desc"); GATE_CHECK+=(""); GATE_EXPECT+=(""); GATE_CWD+=(""); GATE_FILE+=("$rel")
        cur_idx=$(( ${#GATE_ID[@]} - 1 ))
        continue
      fi
      if [[ "$line" =~ ^[[:space:]]+(CHECK|EXPECT|CWD):[[:space:]]*(.*)$ ]]; then
        local key="${BASH_REMATCH[1]}" val="${BASH_REMATCH[2]}"
        (( cur_idx < 0 )) && { GATE_ERR="게이트 항목 밖의 $key 속성 ($rel)"; return 1; }
        case "$key" in
          CHECK)  [[ -n "${GATE_CHECK[$cur_idx]}" ]]  && { GATE_ERR="CHECK 중복: ${GATE_ID[$cur_idx]}"; return 1; }; GATE_CHECK[$cur_idx]="$val" ;;
          EXPECT) [[ -n "${GATE_EXPECT[$cur_idx]}" ]] && { GATE_ERR="EXPECT 중복: ${GATE_ID[$cur_idx]}"; return 1; }; GATE_EXPECT[$cur_idx]="$val" ;;
          CWD)    GATE_CWD[$cur_idx]="$val" ;;
        esac
      fi
    done < "$f"
  done

  (( saw_section )) || { GATE_ERR="수용 게이트 섹션 없음"; return 1; }
  (( ${#GATE_ID[@]} )) || { GATE_ERR="수용 게이트 섹션이 비어 있음(항목 0)"; return 1; }

  local i
  for i in "${!GATE_ID[@]}"; do
    if [[ -z "${GATE_CHECK[$i]}" || -z "${GATE_EXPECT[$i]}" ]]; then
      GATE_ERR="게이트 ${GATE_ID[$i]}: CHECK 와 EXPECT 를 둘 다 적어야 한다(수동 게이트 미지원)"
      return 1
    fi
  done
  return 0
}

# gate_met <idx> [head] — 3결속을 만족하는 matched=true 원장 행이 있는가. 0=met
gate_met() {
  local i="$1" head="${2:-$(gate_head)}"
  [[ -f "$GATE_RUNS" ]] || return 1
  command -v jq >/dev/null 2>&1 || return 1
  jq -e --arg h "$head" --arg f "${GATE_FILE[$i]}" --arg i "${GATE_ID[$i]}" \
        --arg c "$(gate_sha "${GATE_CHECK[$i]}")" --arg e "$(gate_sha "${GATE_EXPECT[$i]}")" \
     'select(.head==$h and .file==$f and .id==$i and .cmd_sha==$c and .expect_sha==$e and .matched==true)' \
     "$GATE_RUNS" >/dev/null 2>&1
}

gate_runs_append() {  # <json>
  {
    mkdir -p "$(dirname "$GATE_RUNS")" || return 0
    if [[ -f "$GATE_RUNS" ]] && [[ "$(stat -c%s "$GATE_RUNS" 2>/dev/null || echo 0)" -gt "$GATE_RUNS_MAX_BYTES" ]]; then
      mv -f "$GATE_RUNS" "$GATE_RUNS.1" || true
    fi
    printf '%s\n' "$1" >> "$GATE_RUNS"
  } 2>/dev/null || true
  return 0
}

# gate_run <idx> — 실행 + 원장 기록. 0=met
gate_run() {
  local i="$1" head; head="$(gate_head)"
  local cwd="$GATE_PROJECT"
  [[ -n "${GATE_CWD[$i]}" ]] && cwd="$GATE_PROJECT/${GATE_CWD[$i]}"
  local out rc
  if [[ ! -d "$cwd" ]]; then
    # ⚠️ cd 실패를 rc≠0 과 뭉뚱그리면 "명령이 실패했다"는 **거짓 사유**를 보고한다
    out="CWD 없음: ${GATE_CWD[$i]}"; rc=126
  else
    out="$(cd "$cwd" && timeout "$GATE_TIMEOUT" bash -c "${GATE_CHECK[$i]}" 2>&1)"; rc=$?
  fi
  local matched=false
  if [[ "$rc" -eq 0 ]] && printf '%s' "$out" | grep -qF -- "${GATE_EXPECT[$i]}"; then matched=true; fi

  if command -v jq >/dev/null 2>&1; then
    gate_runs_append "$(jq -cn \
      --arg ts "$(date -Iseconds)" --arg s "${LEDGER_SESSION:-${CLAUDE_SESSION_ID:--}}" \
      --arg h "$head" --arg f "${GATE_FILE[$i]}" --arg i "${GATE_ID[$i]}" \
      --arg c "$(gate_sha "${GATE_CHECK[$i]}")" --arg e "$(gate_sha "${GATE_EXPECT[$i]}")" \
      --argjson rc "$rc" --argjson m "$matched" \
      --arg o "$(printf '%s' "$out" | sha256sum | cut -d' ' -f1)" --argjson b "$(printf '%s' "$out" | wc -c)" \
      '{ts:$ts,session:$s,head:$h,file:$f,id:$i,cmd_sha:$c,expect_sha:$e,rc:$rc,matched:$m,out_sha:$o,bytes:$b}')"
  fi

  if [[ "$matched" == true ]]; then
    printf 'MET    %-6s %s\n' "${GATE_ID[$i]}" "${GATE_DESC[$i]}"
    return 0
  fi
  {
    printf 'UNMET  %-6s %s\n' "${GATE_ID[$i]}" "${GATE_DESC[$i]}"
    if [[ "$rc" -eq 126 ]]; then
      printf '       사유: CWD 진입 실패 — %s\n' "${GATE_CWD[$i]}"
    elif [[ "$rc" -eq 124 ]]; then
      printf '       사유: 타임아웃 %ss 초과\n' "$GATE_TIMEOUT"
    elif [[ "$rc" -ne 0 ]]; then
      printf '       사유: rc=%s (0 아님)\n' "$rc"
    else
      printf '       사유: rc=0 이지만 EXPECT 미포함 — EXPECT: %s\n' "${GATE_EXPECT[$i]}"
    fi
    printf '       출력(마지막 20줄):\n'
    printf '%s\n' "$out" | tail -20 | sed 's/^/       | /'
  } >&2
  return 1
}

# ── CLI (source 시에는 실행되지 않는다) ──────────────────────────────────────
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  MODE=""; ONLY=""; FILES=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --status|--run) MODE="${1#--}"; shift ;;
      --only) ONLY="${2:-}"; shift 2 ;;
      -h|--help) echo "사용: gate-check.sh --status|--run <work-history.md...> [--only <ID>]" >&2; exit 2 ;;
      *) FILES+=("$1"); shift ;;
    esac
  done
  if [[ -z "$MODE" || ${#FILES[@]} -eq 0 ]]; then
    echo "사용: gate-check.sh --status|--run <work-history.md...> [--only <ID>]" >&2
    exit 2
  fi

  if ! gate_parse "${FILES[@]}"; then
    echo "[수용 게이트 · 파싱 실패] $GATE_ERR" >&2
    exit 1
  fi

  HEAD_NOW="$(gate_head)"
  FAILED=0; RAN=0
  for idx in "${!GATE_ID[@]}"; do
    [[ -n "$ONLY" && "${GATE_ID[$idx]}" != "$ONLY" ]] && continue
    RAN=$((RAN + 1))
    # ⚠️ pre-bash-guard 가 flock 없는 ./gradlew 를 deny 한다 — 게이트가 그 형태면 영원히 rc≠0 이다.
    if [[ "${GATE_CHECK[$idx]}" == *"gradlew"* && "${GATE_CHECK[$idx]}" != *"flock"* ]]; then
      echo "[경고] ${GATE_ID[$idx]}: gradle 게이트는 flock 직렬화가 필요하다 — flock -w 900 .claude/hooks/.state/gradle-build.lock ./gradlew …" >&2
    fi
    if [[ "$MODE" == "status" ]]; then
      if gate_met "$idx" "$HEAD_NOW"; then
        printf 'MET    %-6s %s\n' "${GATE_ID[$idx]}" "${GATE_DESC[$idx]}"
      else
        printf 'UNMET  %-6s %s\n' "${GATE_ID[$idx]}" "${GATE_DESC[$idx]}"
        FAILED=$((FAILED + 1))
      fi
    else
      gate_run "$idx" || FAILED=$((FAILED + 1))
    fi
  done

  if [[ "$RAN" -eq 0 ]]; then
    echo "[수용 게이트] 대상 게이트 0 — --only '$ONLY' 가 어떤 ID 와도 안 맞는다" >&2
    exit 1
  fi
  echo "----"
  echo "GATES=$RAN MET=$((RAN - FAILED)) UNMET=$FAILED"
  [[ "$FAILED" -eq 0 ]]
fi
