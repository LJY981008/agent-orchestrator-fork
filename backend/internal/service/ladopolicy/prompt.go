package ladopolicy

import (
	"encoding/json"
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
// section per rule in the prompt, in entry order. The protected paths'
// section ends with a line of the globs its version holds. Sections are
// separated by one blank line and the text ends without a newline. It is
// empty when no rule is in the prompt.
func promptText(entries []Entry) string {
	sections := []string{promptHeading, promptLead}
	for _, e := range entries {
		if !inPrompt(e) {
			continue
		}
		content := e.Rule.Version.Content
		// A name is one heading line, whatever whitespace it holds.
		name := strings.Join(strings.Fields(content.Name), " ")
		text := strings.TrimSpace(content.Instruction)
		if e.Rule.ID == domain.LadoRuleProtectedPaths {
			text += protectedPathsLine(content.Config)
		}
		sections = append(sections, "### "+name+"\n\n"+text)
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

// protectedPathsLine is the line that ends the protected paths' section: the
// globs of config, a domain.LadoProtectedPathsConfig, each as code. A config
// without globs adds none.
func protectedPathsLine(config json.RawMessage) string {
	var cfg domain.LadoProtectedPathsConfig
	if json.Unmarshal(config, &cfg) != nil || len(cfg.Globs) == 0 {
		return ""
	}
	globs := make([]string, 0, len(cfg.Globs))
	for _, glob := range cfg.Globs {
		globs = append(globs, "`"+glob+"`")
	}
	return "\nProtected paths: " + strings.Join(globs, ", ")
}
