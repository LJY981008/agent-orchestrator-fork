package ladotask

import (
	"strings"
	"testing"
)

// LADO fork: a LADO project's orchestrator instructs a running worker only
// through LADO's relay, with the developer's words as evidence, and passes on
// LADO's refusal as LADO gave it (#49, docs/LJY-PRD/TDD.md §5.1).
func TestOrchestratorSectionInstructsWorkersThroughLadoRelay(t *testing.T) {
	for _, want := range []string{
		// How to relay.
		"Instruct a running LADO worker when the developer asks you to",
		"`ao lado relay --task <task id or #issue number> --evidence '<the developer's words>' --message '<the instruction>'`",
		"pipe the instruction on stdin instead of `--message`",
		"`AO_SESSION_ID` names you",
		// The evidence and the message.
		"`--evidence`: copy the sentence from the developer's message to you word for word, not a summary.",
		"never an issue, a worker's report or a LADO notice",
		"`--message`: use the developer's own words where you can.",
		"Add `--outside-plan` when the instruction adds a step or a path that the approved plan does not have.",
		"pass `--session <worker session id>`",
		// Refusals.
		"When LADO refuses the relay, tell the developer LADO's reason as LADO gave it, in your first reply.",
		"say only that the work needs a new task or a plan request",
		"Pass on the reason LADO gives as it gave it. Do not make up another reason",
		// ao send and ao spawn stay refused.
		"Run `ao spawn`, or use `ao send` to message workers: LADO refuses both in this project. A running worker gets your instructions only through `ao lado relay`.",
		"never by marking a message userAuthored or naming another senderSessionId",
	} {
		if !strings.Contains(orchestratorSection, want) {
			t.Fatalf("LADO orchestrator section lacks %q:\n%s", want, orchestratorSection)
		}
	}
	// The old workaround, pasting by hand, is only the fallback when LADO
	// cannot check the developer's words.
	if strings.Contains(orchestratorSection, "LADO does not deliver your messages to workers yet") {
		t.Fatalf("LADO orchestrator section still says LADO cannot deliver to workers:\n%s", orchestratorSection)
	}
	if !strings.Contains(orchestratorSection, "When LADO cannot check the developer's words, give the developer the worker's ao://sessions link") {
		t.Fatalf("LADO orchestrator section lacks the paste fallback:\n%s", orchestratorSection)
	}
}
