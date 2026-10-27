package agentcreds

// LADO fork: a Claude Code session on a LADO config home keeps its login in
// the folder CLAUDE_SECURESTORAGE_CONFIG_DIR names, not in its config
// directory (docs/LJY-PRD/HARNESS.md §9). The credential reader of
// resolve.go asks claudeCredentialsDir for that folder.

import (
	"os"
	"strings"
)

// claudeCredentialsDir is the folder of Claude Code's login file. LADO fork:
// CLAUDE_SECURESTORAGE_CONFIG_DIR, when set, moves the login away from the
// config directory (an empty value means ~/.claude), as Claude Code itself
// reads it; a session on a LADO config home keeps its login there.
func claudeCredentialsDir(opts ResolveOptions) (string, error) {
	dir, set := opts.lookupEnv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if !set {
		return claudeConfigDir(opts)
	}
	opts.ConfigDir = dir
	if dir == "" {
		env := opts.env
		opts.Env = func(name string) string {
			if name == "CLAUDE_CONFIG_DIR" {
				return ""
			}
			return env(name)
		}
	}
	return claudeConfigDir(opts)
}

// lookupEnv is env that tells an unset variable from an empty one where it
// can: the launch environment first, which a WithClaudeSettings lookup also
// reads first, then the process environment. An injected lookup reports an
// empty value as unset.
func (o ResolveOptions) lookupEnv(name string) (string, bool) {
	if value, ok := o.CommandEnv[name]; ok {
		return strings.TrimSpace(value), true
	}
	if o.Env != nil {
		value := o.env(name)
		return value, value != ""
	}
	value, ok := os.LookupEnv(name)
	return strings.TrimSpace(value), ok
}
