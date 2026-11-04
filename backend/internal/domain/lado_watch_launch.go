package domain

// LADO fork: the launch values of the worker watch (issue #42). They are
// shared by the service that decides them (service/ladolaunch), the session
// manager that puts them on a launch and the Codex adapters that turn them
// into Codex config.

const (
	// LadoClaudeCompactWindowEnv is Claude Code's own variable for the token
	// count it compacts at. It wins over autoCompactWindow in any settings
	// file, the project's .claude/settings.json included.
	LadoClaudeCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	// LadoCodexCompactLimitKey is Codex's config key for the token count it
	// compacts at (config.toml, -c, and the app-server thread config). Seen
	// in codex-cli 0.146.0's app-server schema and 0.166.0's binary.
	LadoCodexCompactLimitKey = "model_auto_compact_token_limit"
	// LadoCodexCompactLimitEnv carries LADO's Codex compact limit from the
	// session manager to the Codex Chat driver, which moves it into the
	// thread config and keeps it off the app-server's environment. It is
	// AO's own name: Codex reads no such variable.
	LadoCodexCompactLimitEnv = "AO_LADO_CODEX_AUTO_COMPACT_LIMIT"
)
