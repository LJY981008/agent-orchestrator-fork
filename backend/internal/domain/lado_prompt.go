package domain

// LadoStandingSection is LADO's part of a session's standing instructions
// (docs/LJY-PRD/TDD.md §5.1). Text is appended to the session's prompt.
// ReplacesOrchestratorRole means Text defines the orchestrator's role in
// place of AO's default orchestrator role and its delegation rules: the
// session manager then leaves AO's role out instead of sending both.
type LadoStandingSection struct {
	Text                     string
	ReplacesOrchestratorRole bool
}
