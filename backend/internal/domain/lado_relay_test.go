package domain_test

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoRelayMessageTextPlain(t *testing.T) {
	got := domain.LadoRelayMessageText(domain.LadoRelay{
		ID:          "lado-relay-1",
		Instruction: "  후속 2건을 이어서 하세요.\n",
		Evidence:    "그 워커에게 후속 2건 해\n\n남은 묶음도",
	})
	want := "LADO 전달: 개발자 지시 (lado-relay-1)\n" +
		"개발자가 오케스트레이터에게 내린 지시를 LADO 가 전달합니다.\n\n" +
		"지시:\n후속 2건을 이어서 하세요.\n\n" +
		"개발자 원문:\n> 그 워커에게 후속 2건 해\n>\n> 남은 묶음도\n\n" +
		"승인된 계획의 규칙(권한 상한, 보호 경로, 통합 승인)은 그대로입니다."
	if got != want {
		t.Fatalf("message =\n%s\nwant\n%s", got, want)
	}
	for _, mark := range []string{domain.LadoRelayBeyondEvidenceMark, domain.LadoRelayOutsidePlanMark} {
		if strings.Contains(got, mark) {
			t.Fatalf("plain relay carries %q", mark)
		}
	}
}

func TestLadoRelayMessageTextMarks(t *testing.T) {
	got := domain.LadoRelayMessageText(domain.LadoRelay{
		ID:                 "lado-relay-2",
		Instruction:        "docs/x.md 도 고치세요",
		Evidence:           "이어서 해",
		BeyondEvidence:     true,
		OutsidePlan:        true,
		OutsidePlanReasons: []string{domain.LadoRelayFlaggedReason, domain.LadoRelayPathReason("docs/x.md")},
	})
	for _, want := range []string{
		domain.LadoRelayBeyondEvidenceMark + " 이 지시는 개발자 원문과 같지 않습니다. 개발자 원문을 기준으로 판단하고",
		"> 이어서 해",
		domain.LadoRelayOutsidePlanMark + " 이 지시는 승인된 계획 범위를 벗어날 수 있습니다: 오케스트레이터가 계획 밖으로 표시; 계획 밖 경로: docs/x.md. 바뀐 경로는 검증에서 '계획 밖 경로'로 확인됩니다.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("message lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "승인된 계획의 규칙(권한 상한, 보호 경로, 통합 승인)은 그대로입니다.") {
		t.Fatalf("message does not end with the rules line:\n%s", got)
	}
	// The beyond-evidence mark comes before the outside-plan mark.
	if strings.Index(got, domain.LadoRelayBeyondEvidenceMark) > strings.Index(got, domain.LadoRelayOutsidePlanMark) {
		t.Fatalf("marks out of order:\n%s", got)
	}

	// Outside the plan without a reason has no colon.
	bare := domain.LadoRelayMessageText(domain.LadoRelay{Instruction: "a", Evidence: "b", OutsidePlan: true})
	if !strings.Contains(bare, "벗어날 수 있습니다. 바뀐 경로는") {
		t.Fatalf("outside plan without reasons:\n%s", bare)
	}
	if !strings.HasPrefix(bare, domain.LadoRelayHeader+"\n") {
		t.Fatalf("a relay without an id has a bare header:\n%s", bare)
	}
}

func TestLadoRelayNormalize(t *testing.T) {
	if got := domain.LadoRelayNormalize("  그 워커에게\n\t후속  2건 해 "); got != "그 워커에게 후속 2건 해" {
		t.Fatalf("normalize = %q", got)
	}
	if got := domain.LadoRelayTextLen(" 가나\n다 "); got != 4 {
		t.Fatalf("text length = %d, want 4 (가나 다)", got)
	}
}

func TestLadoRelayLimits(t *testing.T) {
	if domain.LadoRelayMaxInstructionChars != 8000 || domain.LadoRelayMinEvidenceChars != 4 ||
		domain.LadoRelayMaxEvidenceChars != 2000 || domain.LadoRelayEvidenceMaxAge.Hours() != 24 {
		t.Fatal("relay limits changed; update the plan and ADR 0011")
	}
	if domain.LadoRelaySenderLabel != "LADO 전달 · 개발자 지시" {
		t.Fatalf("sender label = %q", domain.LadoRelaySenderLabel)
	}
}
