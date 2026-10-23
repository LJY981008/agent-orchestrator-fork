#!/usr/bin/env bash
# pre-bash-guard.sh — 체인 러너 무인 세션의 Bash 가드 (agent-orchestrator-fork · 2026-10-22)
#
# 왜: chain-run.sh 의 자식 세션은 bypassPermissions 로 돈다(무인이라 ask 에 답할 사람이 없다). 전역 훅
#   (~/.claude/hooks/block-destructive.sh)은 파괴적 명령만 막고, 밖으로 나가는 쓰기(git push · PR · 원격 API)는
#   통과시킨다. 이 저장소에서 푸시·PR·외부 서비스 쓰기는 메인 세션이 사용자 승인 뒤에만 한다.
# 언제: CHAIN_RUN=1(러너가 export)일 때만 판정한다. 대화형 세션에는 아무 영향이 없다(즉시 exit 0).
# 막는 것(exit 2 = 도구 호출 거부, 사유는 stderr 로 모델에게 간다):
#   - 명령 위치의 git push (git 전역 옵션 -C·-c·--x=y 가 끼어도)
#   - 명령 위치의 gh (어떤 하위 명령이든 — 체인 단계에는 필요 없다)
#   - 명령 위치의 npm·pnpm·yarn publish
#   - curl·wget 의 쓰기 요청(-X/--request POST·PUT·PATCH·DELETE, -d/--data*, -F/--form, --post-*) 중 loopback 밖으로 가는 것
# "명령 위치" = 줄 맨 앞, 또는 ; & | ( ` 뒤 — env·command·exec·nohup·timeout N 접두와 VAR=값 대입을 건너뛴다.
#   커밋 메시지·인자 속의 글자("push", "gh")는 막지 않는다. heredoc 본문 줄이 그 꼴로 시작하면 막힐 수 있다 —
#   그런 글자를 파일에 써야 하면 Write/Edit 도구로 쓴다(거부 문구가 같은 안내를 준다).
set -uo pipefail

[[ "${CHAIN_RUN:-}" == "1" ]] || exit 0

input="$(cat)"
cmd="$(jq -r '.tool_input.command // empty' <<<"$input" 2>/dev/null)" || exit 0
[[ -n "$cmd" ]] || exit 0

deny() {
  printf '[pre-bash-guard] 체인 무인 세션(CHAIN_RUN=1)에서는 %s 를 실행하지 않는다 — 푸시·PR·원격 쓰기는 메인 세션이 사용자 승인 뒤에 한다. 이 단계 범위의 로컬 작업만 하라(그런 글자를 파일에 써야 하면 Write/Edit 도구를 쓴다).\n' "$1" >&2
  exit 2
}

# 명령 위치 접두: 줄 시작 또는 구분자 뒤, 흔한 래퍼와 VAR=값 대입은 건너뛴다.
POS='(^|[;&|(`])[[:space:]]*((env|command|exec|nohup|time|timeout[[:space:]]+[0-9]+[smh]?)[[:space:]]+)*([A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)*'
# git 전역 옵션: -C <경로> · -c <키=값> · --옵션[=값] · 짧은 묶음(-p 등)
GITOPTS='(([[:space:]]+-[Cc][[:space:]]+[^[:space:]]+)|([[:space:]]+--[a-z-]+(=[^[:space:]]+)?)|([[:space:]]+-[a-zA-Z]+))*'

if grep -qE "${POS}git${GITOPTS}[[:space:]]+push([[:space:]]|$)" <<<"$cmd"; then
  deny "git push"
fi
if grep -qE "${POS}gh([[:space:]]|$)" <<<"$cmd"; then
  deny "gh"
fi
if grep -qE "${POS}(npm|pnpm|yarn)([[:space:]]+[^[:space:];&|]+)*[[:space:]]+publish([[:space:]]|$)" <<<"$cmd"; then
  deny "패키지 publish"
fi
if grep -qE "${POS}(curl|wget)([[:space:]]|$)" <<<"$cmd" \
  && grep -qE '(-X|--request)[[:space:]=]*(POST|PUT|PATCH|DELETE)|(^|[[:space:]])(-d|--data[a-z-]*|-F|--form|--post-data|--post-file)([[:space:]=]|$)' <<<"$cmd" \
  && ! grep -qE '(https?://)?(127\.0\.0\.1|localhost|\[::1\])([:/]|[[:space:]]|$)' <<<"$cmd"; then
  deny "loopback 밖으로 가는 HTTP 쓰기"
fi
exit 0
