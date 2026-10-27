package ladosetup

// Setup conflict checks (plan §6, docs/LJY-PRD/HARNESS.md §10): deterministic
// checks of the setup a scope's sessions load against what AO adds to every
// session (its hooks, its ao tool server, the using-ao skill, the variables
// it sets) and against LADO's permission ceiling. A blocking result blocks
// the task. A warning can be accepted for one item version, in a scope and
// the narrower ones. Info is only shown.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// Conflict codes. They are stable strings, sent to the renderer, which shows
// them in the developer's language.
const (
	// ConflictMCPNameTaken is an MCP server named ao, in any case: AO's own
	// tool server has that name.
	ConflictMCPNameTaken = "LADO_SETUP_MCP_NAME_TAKEN"
	// ConflictSkillNameTaken is a skill or a command named using-ao, the
	// skill AO installs.
	ConflictSkillNameTaken = "LADO_SETUP_SKILL_NAME_TAKEN"
	// ConflictEnvReserved is an env setting that sets a variable AO sets for
	// its sessions: AO_*, CLAUDE_CONFIG_DIR, CLAUDE_SECURESTORAGE_CONFIG_DIR
	// or CODEX_HOME.
	ConflictEnvReserved = "LADO_SETUP_ENV_RESERVED"
	// ConflictDeniesAO is a permissions.deny rule that covers an ao command
	// or a tool of AO's tool server.
	ConflictDeniesAO = "LADO_SETUP_DENIES_AO"
	// ConflictHookPathMoved is a hook or status line command that still
	// names an entry adoption moved out of the Claude folder (plan decision
	// g).
	ConflictHookPathMoved = "LADO_SETUP_HOOK_PATH_MOVED"
	// ConflictPathUsedInPlace is any other value that names an entry
	// adoption moves out of the Claude folder: a settings value that is no
	// command LADO rewrites (a login key such as apiKeyHelper, an env value,
	// a key that stays in place) or an MCP server's command, args, env or
	// url, which no shell reads. The file must stay in place.
	ConflictPathUsedInPlace = "LADO_SETUP_PATH_USED_IN_PLACE"
	// ConflictDeniesGit is a permissions.deny rule that covers git or gh
	// commands.
	ConflictDeniesGit = "LADO_SETUP_DENIES_GIT"
	// ConflictAllowsProtected is a permissions.allow rule that covers a
	// command a protected gate stops (protectedGitCommands). LADO enforces
	// the gate elsewhere, so it is a warning (HARNESS §10).
	ConflictAllowsProtected = "LADO_SETUP_ALLOWS_PROTECTED"
	// ConflictHookMayBlockTools is a PreToolUse hook whose matcher covers
	// Bash or AO's tool server.
	ConflictHookMayBlockTools = "LADO_SETUP_HOOK_MAY_BLOCK_TOOLS"
	// ConflictHookAddsPromptText is a UserPromptSubmit or SessionStart
	// command hook, whose output joins the agent's context.
	ConflictHookAddsPromptText = "LADO_SETUP_HOOK_ADDS_PROMPT_TEXT"
	// ConflictDefaultModeAboveCeiling is a permissions.defaultMode above the
	// permission ceiling.
	ConflictDefaultModeAboveCeiling = "LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING"
	// ConflictHookSharesAOEvent is a hook on an event AO installs its own
	// Claude Code hook on.
	ConflictHookSharesAOEvent = "LADO_SETUP_HOOK_SHARES_AO_EVENT"
	// ConflictProjectOverrides is a setting whose key the project's
	// repository .claude/settings.json sets as well; Claude Code lets the
	// project's value win.
	ConflictProjectOverrides = "LADO_SETUP_PROJECT_OVERRIDES"
	// ConflictPluginHooksUnreadable is an enabled plugin whose hooks could
	// not be read, so they were not checked.
	ConflictPluginHooksUnreadable = "LADO_SETUP_PLUGIN_HOOKS_UNREADABLE"
)

// CodeConflictNotAcceptable answers a request to accept a result that is not
// a warning (ErrConflictNotAcceptable).
const CodeConflictNotAcceptable = "LADO_SETUP_CONFLICT_NOT_ACCEPTABLE"

// ErrConflictNotAcceptable means an acceptance was asked for a blocking or
// info result, or for a code the checks do not have. Only a warning can be
// accepted.
var ErrConflictNotAcceptable = errors.New("only a setup conflict warning can be accepted")

// conflictSeverity is the severity of each code.
var conflictSeverity = map[string]domain.LadoProblemSeverity{
	ConflictMCPNameTaken:            domain.LadoSeverityBlocking,
	ConflictSkillNameTaken:          domain.LadoSeverityBlocking,
	ConflictEnvReserved:             domain.LadoSeverityBlocking,
	ConflictDeniesAO:                domain.LadoSeverityBlocking,
	ConflictHookPathMoved:           domain.LadoSeverityBlocking,
	ConflictPathUsedInPlace:         domain.LadoSeverityBlocking,
	ConflictDeniesGit:               domain.LadoSeverityWarning,
	ConflictAllowsProtected:         domain.LadoSeverityWarning,
	ConflictHookMayBlockTools:       domain.LadoSeverityWarning,
	ConflictHookAddsPromptText:      domain.LadoSeverityWarning,
	ConflictDefaultModeAboveCeiling: domain.LadoSeverityWarning,
	ConflictHookSharesAOEvent:       domain.LadoSeverityInfo,
	ConflictProjectOverrides:        domain.LadoSeverityInfo,
	ConflictPluginHooksUnreadable:   domain.LadoSeverityInfo,
}

// checkAcceptable refuses an acceptance of a code whose results are not
// warnings.
func checkAcceptable(code string) error {
	if conflictSeverity[code] != domain.LadoSeverityWarning {
		return fmt.Errorf("%w: %s", ErrConflictNotAcceptable, code)
	}
	return nil
}

// SetupContent is one piece of setup the conflict checks read.
type SetupContent struct {
	// ItemID is the managed item. It is empty for setup at an original
	// location, which the tools load as well, and for an entry an adoption
	// preview would adopt.
	ItemID domain.LadoSetupItemID
	Tool   domain.LadoSetupTool
	Kind   domain.LadoSetupKind
	Name   string
	// Path is where setup without an item is: the inventory entry's Path.
	Path string
	// SHA256 is the content's sha256, the item version's. An acceptance
	// holds for that content only.
	SHA256 string
	// Content is a setting's or an MCP server's JSON value. The other kinds
	// are checked by name, and their content is not read.
	Content []byte
}

// ConflictInput is what the conflict checks read for one scope.
type ConflictInput struct {
	// ProjectID and TaskID name the scope: neither for Global, the project
	// for a project, and the task with its project for a task. An acceptance
	// holds in its scope and in the narrower ones.
	ProjectID domain.ProjectID
	TaskID    domain.LadoTaskID
	// Setup is what the scope's sessions load from the developer's setup: the
	// managed items that are on in the scope with their latest versions (or
	// the versions a LADO execution pinned), and the settings keys and MCP
	// servers that stay at their original locations.
	Setup []SetupContent
	// Moved are the entries adoption moved out of the Claude folder, as
	// slash paths under it ("CLAUDE.md", "hooks/check.sh", "skills/review").
	Moved []string
	// Ceiling is the scope's permission ceiling, the mode of the permission
	// ceiling rule. Empty skips the default mode check.
	Ceiling domain.PermissionMode
	// ProjectSettings is the project's repository .claude/settings.json, nil
	// when the project has none.
	ProjectSettings []byte
	// Acceptances are the live conflict acceptances.
	Acceptances []domain.LadoConflictAcceptance
}

// Conflict is one result of the conflict checks.
type Conflict struct {
	Code     string
	Severity domain.LadoProblemSeverity
	// ItemID is the managed item the result is about. It is empty for setup
	// without an item, which Path then names.
	ItemID domain.LadoSetupItemID
	Path   string
	// Detail tells apart the results of one code on one item, such as the
	// hook event or the permission rule; it is empty when there is only one.
	// An acceptance names it.
	Detail string
	// ContentSHA256 is the sha256 of the content the result was found in.
	ContentSHA256 string
	// Details are the values the app shows: "item" (or "origin", the path of
	// setup without an item) and, by code, "event", "matcher", "rule",
	// "commands", "key", "mode", "ceiling", "plugin" and "path".
	Details map[string]string
	// AcceptanceID is the live acceptance that covers a warning; it is empty
	// when none does.
	AcceptanceID domain.LadoConflictAcceptanceID
}

// Accepted reports whether a live acceptance covers the result. An accepted
// warning leaves the list of what needs attention.
func (c Conflict) Accepted() bool { return c.AcceptanceID != "" }

// Problem returns the result in the shape of a rule problem.
func (c Conflict) Problem() domain.LadoPolicyProblem {
	return domain.LadoPolicyProblem{Code: c.Code, Severity: c.Severity, Details: maps.Clone(c.Details)}
}

// Conflicts runs the conflict checks over one scope's setup. The hooks of
// the plugins the setup enables are read from the Claude folder.
func (s *Service) Conflicts(in ConflictInput) []Conflict {
	cc := &conflictChecker{in: in, claudeDir: s.deps.ClaudeDir, aoEvents: claudecode.ManagedHookEvents(), seen: map[string]bool{}}
	var project map[string]json.RawMessage
	if len(in.ProjectSettings) > 0 {
		// Claude Code reads nothing from a file that does not parse.
		project, _ = jsonObject(in.ProjectSettings)
	}
	for _, c := range in.Setup {
		cc.check(c, project)
	}
	cc.accept()
	slices.SortFunc(cc.out, compareConflicts)
	return cc.out
}

type conflictChecker struct {
	in        ConflictInput
	claudeDir string
	// aoEvents are the Claude Code hook events AO installs.
	aoEvents []string
	out      []Conflict
	seen     map[string]bool
	// plugins are the user installs of the plugins, read once.
	plugins    map[string]pluginInstall
	pluginsErr error
	pluginsSet bool
}

func (cc *conflictChecker) check(c SetupContent, project map[string]json.RawMessage) {
	cc.pathsUsed(c)
	switch c.Kind {
	case domain.LadoSetupKindMCPServer:
		if strings.EqualFold(c.Name, aoMCPServerName) {
			cc.add(c, ConflictMCPNameTaken, nil)
		}
	case domain.LadoSetupKindSkill, domain.LadoSetupKindCommand:
		// commands/using-ao.md is /using-ao; a command in a subfolder has the
		// folder in its name.
		name := c.Name
		if isMarkdown(name) {
			name = name[:len(name)-len(".md")]
		}
		if c.Tool == domain.LadoSetupToolClaudeCode && strings.EqualFold(name, skillassets.SkillName) {
			cc.add(c, ConflictSkillNameTaken, nil)
		}
	case domain.LadoSetupKindSetting:
		if c.Tool != domain.LadoSetupToolClaudeCode {
			return
		}
		switch c.Name {
		case claudeEnvKey:
			cc.env(c)
		case "permissions":
			cc.permissions(c)
		case "hooks":
			cc.hooks(c, "", claudeHooks(c.Content), true)
		case "statusLine":
			cc.statusLine(c)
		case "enabledPlugins":
			cc.enabledPlugins(c)
		}
		if _, ok := project[c.Name]; ok {
			cc.add(c, ConflictProjectOverrides, map[string]string{"key": c.Name})
		}
	}
}

// add records a result about c. parts tell it apart from the other results
// of its code on c; the empty ones are left out.
func (cc *conflictChecker) add(c SetupContent, code string, details map[string]string, parts ...string) {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	detail := strings.Join(kept, " ")
	key := strings.Join([]string{code, string(c.ItemID), c.Path, detail}, "\x00")
	if cc.seen[key] {
		return
	}
	cc.seen[key] = true
	d := maps.Clone(details)
	if d == nil {
		d = map[string]string{}
	}
	if c.ItemID != "" {
		d["item"] = string(c.ItemID)
	} else if c.Path != "" {
		d["origin"] = c.Path
	}
	cc.out = append(cc.out, Conflict{
		Code: code, Severity: conflictSeverity[code], ItemID: c.ItemID, Path: c.Path,
		Detail: detail, ContentSHA256: c.SHA256, Details: d,
	})
}

// reservedEnv are the variables AO sets for its sessions, besides AO_*.
var reservedEnv = []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CODEX_HOME"}

// env finds the variables AO sets. Names are compared in upper case, as
// Windows does.
func (cc *conflictChecker) env(c SetupContent) {
	vars, err := jsonObject(c.Content)
	if err != nil {
		return
	}
	for _, name := range sortedKeys(vars) {
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "AO_") || slices.Contains(reservedEnv, upper) {
			cc.add(c, ConflictEnvReserved, map[string]string{"key": name}, name)
		}
	}
}

// claudeModes are Claude Code's permissions.defaultMode values as AO's
// permission modes. plan and default are under every ceiling.
var claudeModes = map[string]domain.PermissionMode{
	"acceptEdits":       domain.PermissionModeAcceptEdits,
	"auto":              domain.PermissionModeAuto,
	"bypassPermissions": domain.PermissionModeBypassPermissions,
}

func (cc *conflictChecker) permissions(c SetupContent) {
	keys, err := jsonObject(c.Content)
	if err != nil {
		return
	}
	for _, rule := range jsonStrings(keys["deny"]) {
		if bashRuleCovers(rule, "ao") || mcpRuleCovers(rule, aoMCPServerName) {
			cc.add(c, ConflictDeniesAO, map[string]string{"rule": rule}, rule)
		}
		if bashRuleCovers(rule, "git") || bashRuleCovers(rule, "gh") {
			cc.add(c, ConflictDeniesGit, map[string]string{"rule": rule}, rule)
		}
	}
	// The protected gates are always on, so every allow rule is checked.
	for _, rule := range jsonStrings(keys["allow"]) {
		var covered []string
		for _, command := range protectedGitCommands {
			if bashRuleCovers(rule, command) {
				covered = append(covered, command)
			}
		}
		if len(covered) > 0 {
			cc.add(c, ConflictAllowsProtected, map[string]string{"rule": rule, "commands": strings.Join(covered, ", ")}, rule)
		}
	}
	ceiling := domain.LadoPermissionRank(cc.in.Ceiling)
	mode := jsonString(keys["defaultMode"])
	if ceiling > 0 && domain.LadoPermissionRank(claudeModes[mode]) > ceiling {
		cc.add(c, ConflictDefaultModeAboveCeiling, map[string]string{"mode": mode, "ceiling": string(cc.in.Ceiling)})
	}
}

// protectedGitCommands are the commands the protected gates
// git.protected_branch.no_direct_write and integration.approval.required
// stop. An allow rule that covers one lets a worker run it without asking.
var protectedGitCommands = []string{"git push", "git update-ref", "git merge", "git reset", "gh pr merge"}

// bashRuleCovers reports whether a Claude Code permission rule applies to a
// command of program, read by prefix: "Bash" and "Bash(*)" cover every
// command, "Bash(ao)" the program alone and "Bash(ao status)" that command,
// and a pattern with "*" the commands that start with what comes before it,
// so "Bash(ao:*)", "Bash(ao *)", "Bash(ao status:*)" and "Bash(a*)" cover ao.
// program may name a subcommand too: "Bash(git:*)" covers "git push".
func bashRuleCovers(rule, program string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "Bash" || rule == "*" {
		return true
	}
	pattern, ok := strings.CutPrefix(rule, "Bash(")
	if !ok {
		return false
	}
	if pattern, ok = strings.CutSuffix(pattern, ")"); !ok {
		return false
	}
	pattern = strings.TrimSpace(pattern)
	star := strings.IndexByte(pattern, '*')
	if star < 0 {
		return pattern == program || strings.HasPrefix(pattern, program+" ")
	}
	// "ao:*" is the older way to write "ao *".
	prefix := strings.TrimSuffix(pattern[:star], ":")
	return strings.HasPrefix(program+" ", prefix) || strings.HasPrefix(prefix, program+" ")
}

// mcpRuleCovers reports whether a Claude Code permission rule applies to a
// tool of the MCP server, read by prefix: "mcp__ao" and "mcp__ao__<tool>"
// name it, and a pattern with "*" covers the tools whose names start with
// what comes before it, as "mcp__ao__*" and "mcp__*" do.
func mcpRuleCovers(rule, server string) bool {
	rule = strings.TrimSpace(rule)
	tools := "mcp__" + server + "__"
	if star := strings.IndexByte(rule, '*'); star >= 0 {
		prefix := rule[:star]
		return strings.HasPrefix(tools, prefix) || strings.HasPrefix(prefix, tools)
	}
	return rule == "mcp__"+server || strings.HasPrefix(rule, tools)
}

// claudeHook is one hook of a Claude Code hooks value.
type claudeHook struct {
	event, matcher string
	// typ is "command" for a shell command; Claude Code has other kinds,
	// such as prompt hooks.
	typ, command string
}

// runsCommand reports whether the hook runs a shell command.
func (h claudeHook) runsCommand() bool { return h.typ == "" || h.typ == "command" }

// claudeHooks reads a hooks value ({event: [{matcher, hooks: [{type,
// command}]}]}) by event, skipping what does not parse.
func claudeHooks(value []byte) []claudeHook {
	events, err := jsonObject(value)
	if err != nil {
		return nil
	}
	var hooks []claudeHook
	for _, event := range sortedKeys(events) {
		var groups []json.RawMessage
		if json.Unmarshal(events[event], &groups) != nil {
			continue
		}
		for _, raw := range groups {
			var group struct {
				Matcher string            `json:"matcher"`
				Hooks   []json.RawMessage `json:"hooks"`
			}
			if json.Unmarshal(raw, &group) != nil {
				continue
			}
			for _, rawHook := range group.Hooks {
				var hook struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				}
				if json.Unmarshal(rawHook, &hook) == nil {
					hooks = append(hooks, claudeHook{event: event, matcher: group.Matcher, typ: hook.Type, command: hook.Command})
				}
			}
		}
	}
	return hooks
}

// hooks checks the hooks of c, or of the plugin c enables. Only the user's
// own hooks can name moved paths: a plugin's live in the plugin.
func (cc *conflictChecker) hooks(c SetupContent, plugin string, hooks []claudeHook, paths bool) {
	for _, h := range hooks {
		details := func(kv ...string) map[string]string {
			d := map[string]string{"event": h.event}
			if plugin != "" {
				d["plugin"] = plugin
			}
			for i := 0; i+1 < len(kv); i += 2 {
				d[kv[i]] = kv[i+1]
			}
			return d
		}
		if slices.Contains(cc.aoEvents, h.event) {
			cc.add(c, ConflictHookSharesAOEvent, details(), plugin, h.event)
		}
		if h.runsCommand() && (h.event == "UserPromptSubmit" || h.event == "SessionStart") {
			cc.add(c, ConflictHookAddsPromptText, details(), plugin, h.event)
		}
		if h.event == "PreToolUse" && matcherCoversAOTools(h.matcher) {
			cc.add(c, ConflictHookMayBlockTools, details("matcher", h.matcher), plugin, h.matcher)
		}
		if paths && h.runsCommand() {
			for _, ref := range movedPathReferences(h.command, cc.claudeDir, cc.in.Moved) {
				cc.add(c, ConflictHookPathMoved, details("path", ref.Name), h.event, ref.Name)
			}
		}
	}
}

// simpleMatcher is a matcher Claude Code compares to the tool's name as a
// list of exact names. Any other matcher is a regular expression.
var simpleMatcher = regexp.MustCompile(`^[A-Za-z0-9_|]+$`)

// namesAOServer finds AO's tool server in a regular expression.
var namesAOServer = regexp.MustCompile(`mcp__ao(?:__|\b)`)

// matcherCoversAOTools reports whether a PreToolUse matcher can match Bash or
// a tool of AO's tool server. An empty matcher and "*" match every tool, and
// a regular expression is tried on "Bash" and "mcp__ao__". One that Go cannot
// compile (JavaScript's syntax has more) is taken to match.
func matcherCoversAOTools(matcher string) bool {
	switch {
	case matcher == "" || matcher == "*":
		return true
	case simpleMatcher.MatchString(matcher):
		for _, name := range strings.Split(matcher, "|") {
			if name == "Bash" || name == "mcp__"+aoMCPServerName || strings.HasPrefix(name, "mcp__"+aoMCPServerName+"__") {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(matcher)
	if err != nil {
		return true
	}
	return re.MatchString("Bash") || re.MatchString("mcp__ao__") || namesAOServer.MatchString(matcher)
}

func (cc *conflictChecker) statusLine(c SetupContent) {
	keys, err := jsonObject(c.Content)
	if err != nil {
		return
	}
	if typ := jsonString(keys["type"]); typ != "" && typ != "command" {
		return
	}
	for _, ref := range movedPathReferences(jsonString(keys["command"]), cc.claudeDir, cc.in.Moved) {
		cc.add(c, ConflictHookPathMoved, map[string]string{"key": "statusLine", "path": ref.Name}, ref.Name)
	}
}

// pathsUsed finds the values of a setting or an MCP server that name a moved
// entry and are not commands adoption rewrites: every string of a setting
// but hooks and statusLine, which hooks and statusLine check, and an MCP
// server's command, args, env and url. Each result names the value's key
// ("apiKeyHelper", "env.KEY", "args[1]") and the entry.
func (cc *conflictChecker) pathsUsed(c SetupContent) {
	if len(cc.in.Moved) == 0 || len(c.Content) == 0 {
		return
	}
	var values []keyedString
	switch {
	case c.Kind == domain.LadoSetupKindSetting && c.Name != "hooks" && c.Name != "statusLine":
		var v any
		if json.Unmarshal(c.Content, &v) != nil {
			return
		}
		values = stringsIn(c.Name, v, nil)
	case c.Kind == domain.LadoSetupKindMCPServer:
		var server map[string]any
		if json.Unmarshal(c.Content, &server) != nil {
			return
		}
		for _, key := range []string{"command", "args", "env", "url"} {
			if v, ok := server[key]; ok {
				values = stringsIn(key, v, values)
			}
		}
	}
	for _, v := range values {
		for _, ref := range movedPathReferences(v.value, cc.claudeDir, cc.in.Moved) {
			cc.add(c, ConflictPathUsedInPlace, map[string]string{"key": v.key, "path": ref.Name}, v.key, ref.Name)
		}
	}
}

// keyedString is a string in a JSON value with the key path to it.
type keyedString struct {
	key, value string
}

// stringsIn appends every string in v to out, in key order, with its path
// under key: ".name" into an object, "[i]" into an array.
func stringsIn(key string, v any, out []keyedString) []keyedString {
	switch v := v.(type) {
	case string:
		out = append(out, keyedString{key, v})
	case []any:
		for i, item := range v {
			out = stringsIn(fmt.Sprintf("%s[%d]", key, i), item, out)
		}
	case map[string]any:
		for _, name := range sortedKeys(v) {
			out = stringsIn(key+"."+name, v[name], out)
		}
	}
	return out
}

// enabledPlugins checks the hooks of the plugins c turns on: hooks/hooks.json
// of each one's user install, which plugins/installed_plugins.json of the
// Claude folder lists. A plugin whose hooks cannot be read is info.
func (cc *conflictChecker) enabledPlugins(c SetupContent) {
	plugins, err := jsonObject(c.Content)
	if err != nil {
		return
	}
	for _, name := range sortedKeys(plugins) {
		var on bool
		if json.Unmarshal(plugins[name], &on) != nil || !on {
			continue
		}
		hooks, err := cc.pluginHooks(name)
		if err != nil {
			cc.add(c, ConflictPluginHooksUnreadable, map[string]string{"plugin": name}, name)
			continue
		}
		cc.hooks(c, name, hooks, false)
	}
}

// pluginHooks reads the hooks of a plugin's user install. A plugin without a
// hooks file has none.
func (cc *conflictChecker) pluginHooks(name string) ([]claudeHook, error) {
	if !cc.pluginsSet {
		cc.plugins, cc.pluginsErr = installedPlugins(cc.claudeDir)
		cc.pluginsSet = true
	}
	if cc.pluginsErr != nil {
		return nil, cc.pluginsErr
	}
	dir := cc.plugins[name].InstallPath
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("plugin %s has no user install", name)
	}
	data, err := readRegular(filepath.Join(dir, "hooks", "hooks.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file, err := jsonObject(data)
	if err != nil {
		return nil, err
	}
	return claudeHooks(file["hooks"]), nil
}

// pluginInstall is a plugin's user install as Claude Code records it.
type pluginInstall struct {
	InstallPath  string `json:"installPath"`
	Version      string `json:"version"`
	GitCommitSha string `json:"gitCommitSha"`
}

// installedPlugins reads the user installs of the plugins from the Claude
// folder's plugins/installed_plugins.json, by plugin name. Version 2 of the
// file lists each plugin's installs with their scope; version 1 had one
// install per plugin.
func installedPlugins(claudeDir string) (map[string]pluginInstall, error) {
	if claudeDir == "" {
		return nil, errors.New("no Claude folder")
	}
	data, err := readRegular(filepath.Join(claudeDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil, err
	}
	var index struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	type install struct {
		pluginInstall
		Scope string `json:"scope"`
	}
	installs := make(map[string]pluginInstall, len(index.Plugins))
	for name, raw := range index.Plugins {
		var list []install
		if json.Unmarshal(raw, &list) != nil {
			var one install
			if json.Unmarshal(raw, &one) != nil {
				continue
			}
			list = []install{one}
		}
		for _, in := range list {
			if (in.Scope == "" || in.Scope == "user") && in.InstallPath != "" {
				installs[name] = in.pluginInstall
				break
			}
		}
	}
	return installs, nil
}

// accept marks the warnings a live acceptance covers: the same code, item,
// detail and content, given in the input's scope or a wider one.
func (cc *conflictChecker) accept() {
	for i := range cc.out {
		c := &cc.out[i]
		if c.Severity != domain.LadoSeverityWarning || c.ItemID == "" {
			continue
		}
		for _, a := range cc.in.Acceptances {
			if a.RevokedAt.IsZero() && a.Code == c.Code && a.ItemID == c.ItemID && a.Detail == c.Detail &&
				a.ContentSHA256 == c.ContentSHA256 && cc.in.covers(a) {
				c.AcceptanceID = a.ID
				break
			}
		}
	}
}

// covers reports whether an acceptance holds in the input's scope. Global
// covers every project, and a project its tasks.
func (in ConflictInput) covers(a domain.LadoConflictAcceptance) bool {
	switch a.Scope {
	case domain.LadoScopeGlobal:
		return true
	case domain.LadoScopeProject:
		return in.ProjectID != "" && a.ScopeID == string(in.ProjectID)
	case domain.LadoScopeTask:
		return in.TaskID != "" && a.ScopeID == string(in.TaskID)
	default:
		return false
	}
}

// severityOrder puts blocking results first.
var severityOrder = []domain.LadoProblemSeverity{domain.LadoSeverityBlocking, domain.LadoSeverityWarning, domain.LadoSeverityInfo}

func compareConflicts(a, b Conflict) int {
	if d := slices.Index(severityOrder, a.Severity) - slices.Index(severityOrder, b.Severity); d != 0 {
		return d
	}
	for _, pair := range [][2]string{{a.Code, b.Code}, {string(a.ItemID), string(b.ItemID)}, {a.Path, b.Path}, {a.Detail, b.Detail}} {
		if d := strings.Compare(pair[0], pair[1]); d != 0 {
			return d
		}
	}
	return 0
}

// jsonStrings reads the strings of a JSON array and skips its other values.
func jsonStrings(value json.RawMessage) []string {
	var items []json.RawMessage
	if json.Unmarshal(value, &items) != nil {
		return nil
	}
	var out []string
	for _, item := range items {
		var s string
		if json.Unmarshal(item, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// jsonString reads a JSON string, or "" for anything else.
func jsonString(value json.RawMessage) string {
	var s string
	if json.Unmarshal(value, &s) != nil {
		return ""
	}
	return s
}

// pathRef is where a shell command names an entry adoption moves out of the
// Claude folder (plan decision g).
type pathRef struct {
	// Start and End are the byte offsets of the part that names the Claude
	// folder ("~/.claude", "$HOME/.claude", "${HOME}/.claude" or the folder's
	// absolute path), without the slash after it.
	Start, End int
	// Name is the moved entry, a slash path under the folder.
	Name string
	// Replacement is what adoption writes over [Start, End) so the command
	// names the entry in the session's config folder: $CLAUDE_CONFIG_DIR,
	// quoted to fit the quoting where the part starts and ends.
	Replacement string
}

// movedPathReferences finds where command names a moved entry of the Claude
// folder: the folder, a slash and the entry's name, followed by the end of
// the word or by a slash into the entry. Runs of "/" and "./" may stand
// between the parts, as the file system reads them; ".." is not resolved.
// The folder is "~/.claude" (with the "~" unquoted at the start of a word, or
// after "=" or ":"), "$HOME/.claude" or "${HOME}/.claude" (not in single
// quotes) or claudeDir itself (in any quotes). A quoted "~" is a plain
// character to the shell and names nothing. The references come in the order
// they appear.
func movedPathReferences(command, claudeDir string, movedNames []string) []pathRef {
	if len(movedNames) == 0 || command == "" {
		return nil
	}
	chars := shellBytes(command)
	dirs := claudeDirForms(claudeDir)
	var refs []pathRef
	for i := 0; i < len(chars); i++ {
		n := claudeFolderAt(chars, i, dirs)
		if n == 0 || i+n >= len(chars) || chars[i+n].b != '/' {
			continue
		}
		name, end := movedNameAt(chars, i+n+1, movedNames)
		if name == "" {
			continue
		}
		first, last := chars[i], chars[i+n-1]
		refs = append(refs, pathRef{Start: first.at, End: last.at + 1, Name: name, Replacement: configDirQuoted(first.quote, last.quote)})
		i = end - 1
	}
	return refs
}

// shellQuote is the quoting a byte of a command is in.
type shellQuote uint8

const (
	shellUnquoted shellQuote = iota
	shellSingle
	shellDouble
)

// shellByte is one byte of a command as the shell reads it, after the quotes
// and the backslashes that only quote are taken out.
type shellByte struct {
	b     byte
	quote shellQuote
	// escaped marks a byte a backslash quotes.
	escaped bool
	// at is the byte's offset in the command.
	at int
}

// shellBytes reads command as a POSIX shell quotes it: '…' keeps every byte,
// "…" keeps every byte but \$, \`, \", \\ and a backslash-newline, and outside
// quotes a backslash quotes the next byte. A backslash-newline joins two
// lines and is dropped.
func shellBytes(command string) []shellByte {
	out := make([]shellByte, 0, len(command))
	quote := shellUnquoted
	for i := 0; i < len(command); i++ {
		b := command[i]
		switch {
		case quote == shellUnquoted && b == '\'':
			quote = shellSingle
			continue
		case quote == shellUnquoted && b == '"':
			quote = shellDouble
			continue
		case quote == shellSingle && b == '\'', quote == shellDouble && b == '"':
			quote = shellUnquoted
			continue
		case b == '\\' && i+1 < len(command) &&
			(quote == shellUnquoted || (quote == shellDouble && strings.IndexByte("$`\"\\\n", command[i+1]) >= 0)):
			i++
			if command[i] != '\n' {
				out = append(out, shellByte{b: command[i], quote: quote, escaped: true, at: i})
			}
			continue
		}
		out = append(out, shellByte{b: b, quote: quote, at: i})
	}
	return out
}

// claudeDirForms are the ways a command can write the absolute Claude folder.
func claudeDirForms(claudeDir string) []string {
	if claudeDir == "" || !filepath.IsAbs(claudeDir) {
		return nil
	}
	dir := filepath.Clean(claudeDir)
	if dir == filepath.Dir(dir) {
		// A root names everything.
		return nil
	}
	forms := []string{dir}
	if slash := filepath.ToSlash(dir); slash != dir {
		forms = append(forms, slash)
	}
	return forms
}

// claudeFolderAt returns how many bytes from chars[i] name the Claude folder,
// or 0.
func claudeFolderAt(chars []shellByte, i int, dirs []string) int {
	first := chars[i]
	if first.escaped {
		return 0
	}
	const folder = "/.claude"
	switch first.b {
	case '~':
		if first.quote == shellUnquoted && wordStart(chars, i) && hasLiteral(chars, i+1, folder) &&
			chars[i+1].quote == shellUnquoted && !chars[i+1].escaped {
			return 1 + len(folder)
		}
		return 0
	case '$':
		for _, home := range []string{"$HOME", "${HOME}"} {
			if expands(chars, i, home) && hasLiteral(chars, i+len(home), folder) {
				return len(home) + len(folder)
			}
		}
		return 0
	}
	if i > 0 && (chars[i-1].escaped || isPathByte(chars[i-1].b)) {
		// Inside a longer path.
		return 0
	}
	for _, dir := range dirs {
		if hasLiteral(chars, i, dir) {
			return len(dir)
		}
	}
	return 0
}

// wordStart reports whether chars[i] starts a word, or follows "=" or ":",
// where the shell expands "~" in an assignment. Outside an assignment, as in
// --config=~/x, the shell leaves the "~" alone but the program may expand
// it, so it counts as well: the rewritten command works either way.
func wordStart(chars []shellByte, i int) bool {
	if i == 0 {
		return chars[0].at == 0
	}
	prev := chars[i-1]
	return prev.at == chars[i].at-1 && prev.quote == shellUnquoted && !prev.escaped &&
		strings.IndexByte(" \t\n;|&()<>`=:", prev.b) >= 0
}

// expands reports whether chars spell the variable reference v from i,
// unescaped and in one quoting that is not single quotes.
func expands(chars []shellByte, i int, v string) bool {
	if !hasLiteral(chars, i, v) || chars[i].quote == shellSingle {
		return false
	}
	for _, c := range chars[i : i+len(v)] {
		if c.escaped || c.quote != chars[i].quote {
			return false
		}
	}
	return true
}

// hasLiteral reports whether chars spell s from i.
func hasLiteral(chars []shellByte, i int, s string) bool {
	if i < 0 || i+len(s) > len(chars) {
		return false
	}
	for k := range len(s) {
		if chars[i+k].b != s[k] {
			return false
		}
	}
	return true
}

// movedNameAt returns the longest moved name chars spell from j and the
// index after it, or "" and -1.
func movedNameAt(chars []shellByte, j int, names []string) (string, int) {
	best, end := "", -1
	for _, name := range names {
		if len(name) <= len(best) {
			continue
		}
		if k := nameEnd(chars, j, name); k >= 0 {
			best, end = name, k
		}
	}
	return best, end
}

// nameEnd returns the index after the slash path name when chars spell it
// from j and it ends a path segment there, or -1. Runs of "/" and "./" may
// come before the name and between its segments.
func nameEnd(chars []shellByte, j int, name string) int {
	k := skipSeparators(chars, j)
	for n, segment := range strings.Split(name, "/") {
		if n > 0 {
			if k >= len(chars) || chars[k].b != '/' {
				return -1
			}
			k = skipSeparators(chars, k)
		}
		if segment == "" || !hasLiteral(chars, k, segment) {
			return -1
		}
		k += len(segment)
	}
	if k == len(chars) || (!chars[k].escaped && (chars[k].b == '/' || !isPathByte(chars[k].b))) {
		return k
	}
	return -1
}

// skipSeparators returns the index after the slashes and "./" segments at k.
func skipSeparators(chars []shellByte, k int) int {
	for k < len(chars) {
		switch {
		case chars[k].b == '/':
			k++
		case chars[k].b == '.' && k+1 < len(chars) && chars[k+1].b == '/':
			k += 2
		default:
			return k
		}
	}
	return k
}

// isPathByte reports whether b can be part of a file name in a command
// without quoting.
func isPathByte(b byte) bool {
	return b >= 0x80 || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9') ||
		strings.IndexByte("._-+@%~#,/", b) >= 0
}

// configDirQuoted is "$CLAUDE_CONFIG_DIR" written for a part of a command
// that starts in quoting start and ends in quoting end. It leaves the
// quoting at the end as it was, so the rest of the command reads the same.
func configDirQuoted(start, end shellQuote) string {
	const v = "$CLAUDE_CONFIG_DIR"
	var s string
	switch start {
	case shellDouble:
		if end == shellDouble {
			return v
		}
		s = v + `"`
	case shellSingle:
		s = `'"` + v + `"`
	default:
		s = `"` + v + `"`
	}
	switch end {
	case shellSingle:
		s += `'`
	case shellDouble:
		s += `"`
	}
	return s
}
