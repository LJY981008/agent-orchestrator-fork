package ladopolicy

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// promptHeading and promptLead open the rule text. A worker gets it after
// the project rules AO already sends, which the lead refers to.
const (
	promptHeading = "## LADO rules"
	promptLead    = "The developer approved these rules for this run. Follow them together with the project rules above."
)

// promptText is the rule text a worker gets: the heading, the lead, then one
// section per rule in the prompt, in entry order. Sections are separated by
// one blank line and the text ends without a newline. It is empty when no
// rule is in the prompt.
func promptText(entries []Entry) string {
	sections := []string{promptHeading, promptLead}
	for _, e := range entries {
		if !inPrompt(e) {
			continue
		}
		content := e.Rule.Version.Content
		// A name is one heading line, whatever whitespace it holds.
		name := strings.Join(strings.Fields(content.Name), " ")
		sections = append(sections, "### "+name+"\n\n"+strings.TrimSpace(content.Instruction))
	}
	if len(sections) == 2 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}

// inPrompt reports whether the entry's rule reaches the worker as text: it is
// on, has prompt enforcement and has an instruction.
func inPrompt(e Entry) bool {
	content := e.Rule.Version.Content
	return e.On && content.Has(domain.LadoEnforcementPrompt) && strings.TrimSpace(content.Instruction) != ""
}
