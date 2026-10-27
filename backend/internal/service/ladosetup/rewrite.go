package ladosetup

// Hook path rewrites (plan decision g). A hooks, statusLine or
// otelHeadersHelper command that names an entry adoption moves out of the
// Claude folder ("~/.claude/hooks/x.sh") would run nothing once the entry is
// gone, so the adopted value names it in the session's config folder
// instead ("$CLAUDE_CONFIG_DIR"/hooks/x.sh),
// where LADO's session homes hold the managed copy. The S06 reference finder
// decides what a command names; a reference adoption cannot rewrite, such as
// one in a setting that stays in place, is a blocking conflict.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// HookRewrite is one command an adoption rewrites.
type HookRewrite struct {
	// Setting is the settings key the command is in: hooks, statusLine or
	// otelHeadersHelper.
	Setting string
	// Event and Matcher place a hooks command; both are empty for the
	// others.
	Event, Matcher string
	Before, After  string
	// Paths are the moved entries the command names, slash paths under the
	// Claude folder.
	Paths []string
}

// rewritable are the Claude settings whose commands adoption rewrites: the
// shell runs them, so "$CLAUDE_CONFIG_DIR" expands.
var rewritable = []string{"hooks", "statusLine", "otelHeadersHelper"}

// rewriteHookPaths rewrites the commands of a hooks, statusLine or
// otelHeadersHelper value (canonical JSON) that name moved entries of the
// Claude folder, and returns the value in canonical JSON with the rewrites
// in order. A value without such commands comes back as it was.
func rewriteHookPaths(setting string, value []byte, claudeDir string, moved []string) ([]byte, []HookRewrite, error) {
	if !slices.Contains(rewritable, setting) || len(moved) == 0 {
		return value, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", setting, err)
	}
	var rewrites []HookRewrite
	// rewriteCommand rewrites one command, or returns "" when it names no
	// moved entry.
	rewriteCommand := func(command, event, matcher string) string {
		refs := movedPathReferences(command, claudeDir, moved)
		if len(refs) == 0 {
			return ""
		}
		after := command
		var paths []string
		for i := len(refs) - 1; i >= 0; i-- {
			r := refs[i]
			after = after[:r.Start] + r.Replacement + after[r.End:]
		}
		for _, r := range refs {
			if !slices.Contains(paths, r.Name) {
				paths = append(paths, r.Name)
			}
		}
		rewrites = append(rewrites, HookRewrite{Setting: setting, Event: event, Matcher: matcher, Before: command, After: after, Paths: paths})
		return after
	}
	rewrite := func(hook map[string]any, event, matcher string) {
		command, _ := hook["command"].(string)
		typ, _ := hook["type"].(string)
		if command == "" || (typ != "" && typ != "command") {
			return
		}
		if after := rewriteCommand(command, event, matcher); after != "" {
			hook["command"] = after
		}
	}
	switch setting {
	case "otelHeadersHelper":
		if command, ok := v.(string); ok && command != "" {
			if after := rewriteCommand(command, "", ""); after != "" {
				v = after
			}
		}
	case "statusLine":
		if line, ok := v.(map[string]any); ok {
			rewrite(line, "", "")
		}
	case "hooks":
		events, _ := v.(map[string]any)
		for _, event := range sortedKeys(events) {
			groups, _ := events[event].([]any)
			for _, g := range groups {
				group, _ := g.(map[string]any)
				matcher, _ := group["matcher"].(string)
				hooks, _ := group["hooks"].([]any)
				for _, h := range hooks {
					if hook, ok := h.(map[string]any); ok {
						rewrite(hook, event, matcher)
					}
				}
			}
		}
	}
	if len(rewrites) == 0 {
		return value, nil, nil
	}
	out, err := marshalCanonical(v)
	if err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", setting, err)
	}
	return out, rewrites, nil
}
