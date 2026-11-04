package cli

// LADO fork: the push guard of a LADO execution worker (issue #44). A
// worker's change reaches its base branch only through a pull request (merge
// request) and the developer's integration approval in LADO. AO's Claude Code
// PreToolUse and PermissionRequest hooks read each Bash command before it
// runs and refuse a push to a base branch, a push of every branch, a push
// option that merges, and gh pr merge or glab mr merge. The command string is
// read before git runs, so --no-verify does not get around it, and nothing in
// the repository's configuration changes. A command the guard allows gets no
// output: the session's own permission flow then goes on as before.
//
// The guard stops mistakes and misread rules. A push inside a script file or
// a git alias does not show in the command and is not seen; the server's
// branch protection is the last line.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ladoExecutionWorkerSession reports whether the hook runs for a LADO
// execution worker. A plan worker has its own, stricter decision.
func ladoExecutionWorkerSession() bool {
	return os.Getenv(domain.LadoWorkerEnv) == "1" && !ladoPlanWorkerSession()
}

// ladoPushGuardTools are the tools whose command the guard reads.
var ladoPushGuardTools = []string{"Bash", "PowerShell"}

// answerLadoPushGuard answers a LADO execution worker's PreToolUse or
// PermissionRequest hook when its command pushes to a base branch or merges.
// It writes nothing for any other call. It reports whether the hook is done:
// a permission prompt it refused reports no activity, since nothing waits.
func answerLadoPushGuard(out io.Writer, event string, payload []byte) (bool, error) {
	if event != "pre-tool-use" && event != "permission-request" {
		return false, nil
	}
	reason := ladoPushGuardPayloadDecision(payload, execLadoGit{})
	if reason == "" {
		return false, nil
	}
	if event == "pre-tool-use" {
		var res claudePreToolUseHookOutput
		res.HookSpecificOutput.HookEventName = "PreToolUse"
		res.HookSpecificOutput.PermissionDecision = "deny"
		res.HookSpecificOutput.PermissionDecisionReason = reason
		return false, json.NewEncoder(out).Encode(res)
	}
	var res claudePermissionHookOutput
	res.HookSpecificOutput.HookEventName = "PermissionRequest"
	res.HookSpecificOutput.Decision.Behavior = "deny"
	res.HookSpecificOutput.Decision.Message = reason
	return true, json.NewEncoder(out).Encode(res)
}

// ladoPushGuardPayloadDecision reads a hook payload and returns why its
// command is refused, or "" to let it through.
func ladoPushGuardPayloadDecision(payload []byte, git ladoGitReader) string {
	var p struct {
		ToolName  string `json:"tool_name"`
		Cwd       string `json:"cwd"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil || !slices.Contains(ladoPushGuardTools, p.ToolName) {
		return ""
	}
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	guard := newLadoPushGuard(git, domain.SplitLadoBaseBranches(os.Getenv(domain.LadoBaseBranchesEnv)))
	return guard.decide(p.ToolInput.Command, cwd)
}

// ladoGitReader reads what the guard needs from the repository at dir. A
// value git cannot give is empty.
type ladoGitReader interface {
	// CurrentBranch is the short name of the branch HEAD names; "" when
	// HEAD is detached or dir is no repository.
	CurrentBranch(dir string) string
	// Config is the configuration git reads in dir, as git config --list
	// names its keys: section and name in lower case, a subsection as
	// written.
	Config(dir string) map[string][]string
	// RemoteHead is the branch refs/remotes/<remote>/HEAD names: the
	// remote's default branch as last fetched.
	RemoteHead(dir, remote string) string
}

// ladoGitTimeout bounds each git call the guard makes.
const ladoGitTimeout = 3 * time.Second

// execLadoGit reads from the repository with the git on PATH.
type execLadoGit struct{}

func (execLadoGit) run(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), ladoGitTimeout)
	defer cancel()
	// The program is git; dir and the arguments go to it as arguments, with
	// no shell between, and every call only reads.
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed program, read-only arguments, no shell.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if cmd.Run() != nil {
		return "", false
	}
	return stdout.String(), true
}

func (g execLadoGit) CurrentBranch(dir string) string {
	out, _ := g.run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	return strings.TrimSpace(out)
}

func (g execLadoGit) Config(dir string) map[string][]string {
	out, ok := g.run(dir, "config", "-z", "--list")
	if !ok {
		return nil
	}
	config := map[string][]string{}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		config[key] = append(config[key], value)
	}
	return config
}

func (g execLadoGit) RemoteHead(dir, remote string) string {
	if !ladoRemoteName(remote) {
		return ""
	}
	out, _ := g.run(dir, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	return strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/"+remote+"/")
}

// ladoRemoteName reports whether s can name a configured remote rather than a
// URL or a path.
func ladoRemoteName(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, `:/\ `) && s != "." && s != ".."
}

// ladoPushGuard decides one command. It reads git lazily, once per folder.
type ladoPushGuard struct {
	git      ladoGitReader
	base     []string
	branches map[string]string
	configs  map[string]map[string][]string
	heads    map[string]string
	firstDir string
}

func newLadoPushGuard(git ladoGitReader, base []string) *ladoPushGuard {
	return &ladoPushGuard{
		git:      git,
		base:     base,
		branches: map[string]string{},
		configs:  map[string]map[string][]string{},
		heads:    map[string]string{},
	}
}

// ladoMaxCommandDepth bounds how deep the guard reads commands inside
// commands: substitutions, bash -c and eval.
const ladoMaxCommandDepth = 6

// decide returns why command is refused, or "".
func (g *ladoPushGuard) decide(command, cwd string) string {
	g.firstDir = cwd
	why := g.check(command, cwd, 0)
	if why == "" {
		return ""
	}
	return g.reason(why)
}

// reason is what a refused worker reads: why, and what to do instead.
func (g *ladoPushGuard) reason(why string) string {
	branch := g.currentBranch(g.firstDir)
	if branch == "" || g.protected(g.firstDir, "origin", branch) {
		branch = "<your-work-branch>"
	}
	return "LADO execution worker: " + why + ". A LADO run reaches its base branch only through a pull request " +
		"(GitLab: merge request) and the developer's integration approval in LADO, so never push to a base branch " +
		"and never merge yourself; --no-verify does not change this. Push your work branch instead: " +
		"git push origin HEAD:refs/heads/" + branch + " (on GitLab you may add -o merge_request.create), " +
		"then open the pull request or merge request and leave the merge to the developer."
}

func (g *ladoPushGuard) check(command, cwd string, depth int) string {
	if depth > ladoMaxCommandDepth {
		if ladoMentionsPushOrMerge(command) {
			return "the command nests too deep to read safely and mentions a push or a merge; write it as one plain command"
		}
		return ""
	}
	parsed, ok := ladoShellSplit(command)
	if !ok {
		if ladoMentionsPushOrMerge(command) {
			return "the command could not be read safely (an unfinished quote or substitution) and mentions a push or a merge; write it as one plain command"
		}
		return ""
	}
	dir := cwd
	for _, words := range parsed.commands {
		words = ladoStripCommandPrefix(words)
		if len(words) == 0 {
			continue
		}
		args := words[1:]
		switch ladoProgram(words[0]) {
		case "cd", "pushd":
			if len(args) > 0 && args[0] != "-" && !strings.ContainsAny(args[0], "$~") {
				dir = ladoJoinDir(dir, args[0])
			}
		case "git":
			if why := g.checkGit(args, dir); why != "" {
				return why
			}
		case "gh":
			if ladoSubcommand(args, []string{"-R", "--repo"}, "pr", "merge") {
				return "gh pr merge merges a pull request"
			}
			if ladoAPIMerge(args, "/pulls/", "/merge") {
				return "gh api merges a pull request"
			}
		case "glab":
			if ladoSubcommand(args, []string{"-R", "--repo"}, "mr", "merge") {
				return "glab mr merge merges a merge request"
			}
			if ladoAPIMerge(args, "/merge_requests/", "/merge") {
				return "glab api merges a merge request"
			}
		case "bash", "sh", "zsh", "dash", "ksh":
			if script, ok := ladoShellScriptArg(args); ok {
				if why := g.check(script, dir, depth+1); why != "" {
					return why
				}
			}
		case "eval":
			if why := g.check(strings.Join(args, " "), dir, depth+1); why != "" {
				return why
			}
		}
	}
	for _, nested := range parsed.nested {
		if why := g.check(nested, cwd, depth+1); why != "" {
			return why
		}
	}
	return ""
}

// ladoMentionsPushOrMerge reports whether a command the guard cannot read
// may push or merge.
func ladoMentionsPushOrMerge(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "git") && strings.Contains(lower, "push") ||
		strings.Contains(lower, "merge") && (strings.Contains(lower, "gh ") || strings.Contains(lower, "glab "))
}

// ladoProgram is the name of the program a command word runs.
func ladoProgram(word string) string {
	name := strings.ToLower(path.Base(filepath.ToSlash(word)))
	return strings.TrimSuffix(name, ".exe")
}

func ladoJoinDir(dir, to string) string {
	if filepath.IsAbs(to) || dir == "" {
		return filepath.Clean(to)
	}
	return filepath.Join(dir, to)
}

// ladoStripCommandPrefix drops what runs before the program: variable
// assignments, shell keywords, and the wrappers env, command, builtin, exec,
// nohup, time, nice, timeout and sudo with their options. command -v only
// looks a program up, so it leaves nothing.
func ladoStripCommandPrefix(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case ladoAssignment(w):
			words = words[1:]
		case slices.Contains([]string{"{", "}", "!", "if", "then", "else", "elif", "do", "while", "until", "nohup", "builtin"}, w):
			words = words[1:]
		case w == "env":
			words = ladoSkipOptions(words[1:], []string{"-u", "--unset", "-C", "--chdir", "-S", "--split-string"})
		case w == "command":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				if strings.ContainsAny(words[0], "vV") {
					return nil
				}
				words = words[1:]
			}
		case w == "exec":
			words = ladoSkipOptions(words[1:], []string{"-a"})
		case w == "time":
			words = ladoSkipOptions(words[1:], []string{"-f", "--format", "-o", "--output"})
		case w == "nice":
			words = ladoSkipOptions(words[1:], []string{"-n", "--adjustment"})
		case w == "sudo":
			words = ladoSkipOptions(words[1:], []string{"-u", "--user", "-g", "--group", "-h", "--host", "-C", "-D", "--chdir", "-p", "--prompt", "-r", "--role", "-t", "--type", "-U", "--other-user"})
		case w == "timeout":
			words = ladoSkipOptions(words[1:], []string{"-s", "--signal", "-k", "--kill-after"})
			if len(words) > 0 {
				words = words[1:] // the duration
			}
		default:
			return words
		}
	}
	return words
}

// ladoAssignment reports whether w is a NAME=value word.
func ladoAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := i > 0 && r >= '0' && r <= '9'
		if !letter && !digit {
			return false
		}
	}
	return true
}

// ladoSkipOptions drops leading options and assignments; withValue lists the
// options whose value is the next word.
func ladoSkipOptions(words, withValue []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case w == "--":
			return words[1:]
		case slices.Contains(withValue, w):
			words = words[min(2, len(words)):]
		case strings.HasPrefix(w, "-") && w != "-" || ladoAssignment(w):
			words = words[1:]
		default:
			return words
		}
	}
	return words
}

// ladoSubcommand reports whether args, past options, start with the words
// group and verb. withValue lists the options whose value is the next word.
func ladoSubcommand(args, withValue []string, group, verb string) bool {
	var plain []string
	for i := 0; i < len(args) && len(plain) < 2; i++ {
		switch {
		case slices.Contains(withValue, args[i]):
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			plain = append(plain, args[i])
		}
	}
	return len(plain) == 2 && plain[0] == group && plain[1] == verb
}

// ladoAPIMerge reports whether args of gh api or glab api name a merge
// endpoint: a path with part, then a number, then suffix.
func ladoAPIMerge(args []string, part, suffix string) bool {
	if len(args) == 0 || args[0] != "api" {
		return false
	}
	for _, arg := range args[1:] {
		_, rest, ok := strings.Cut(arg, part)
		if !ok {
			continue
		}
		number, tail, _ := strings.Cut(rest, "/")
		if number != "" && strings.Trim(number, "0123456789") == "" && strings.HasPrefix("/"+tail, suffix) {
			return true
		}
	}
	return false
}

// ladoShellScriptArg returns the script of a shell's -c option.
func ladoShellScriptArg(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "--" || !strings.HasPrefix(arg, "-"):
			// A script file, or the end of the options.
			return "", false
		case strings.HasPrefix(arg, "--"):
			// A long option such as --login.
		case strings.Contains(arg[1:], "c") && i+1 < len(args):
			return args[i+1], true
		}
	}
	return "", false
}

// checkGit decides a git command run in dir.
func (g *ladoPushGuard) checkGit(args []string, dir string) string {
	overrides := map[string][]string{}
	i := 0
loop:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			dir = ladoJoinDir(dir, args[i+1])
			i += 2
		case a == "-c" && i+1 < len(args):
			key, value, _ := strings.Cut(args[i+1], "=")
			if key = ladoConfigKey(key); key != "" {
				overrides[key] = []string{value}
			}
			i += 2
		case slices.Contains([]string{"--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix", "--list-cmds"}, a):
			i += 2
		case strings.HasPrefix(a, "-"):
			i++
		default:
			break loop
		}
	}
	if i >= len(args) || args[i] != "push" {
		return ""
	}
	return g.checkPush(args[i+1:], dir, overrides)
}

// ladoConfigKey writes a config key as git config --list names it.
func ladoConfigKey(key string) string {
	first := strings.Index(key, ".")
	last := strings.LastIndex(key, ".")
	if first <= 0 || last == len(key)-1 {
		return ""
	}
	return strings.ToLower(key[:first]) + key[first:last] + strings.ToLower(key[last:])
}

// ladoMergePushOptions are the GitLab push options that merge the merge
// request the push opens or updates.
var ladoMergePushOptions = []string{"merge_request.merge_when_pipeline_succeeds", "merge_request.auto_merge"}

// checkPush decides git push with args, run in dir.
func (g *ladoPushGuard) checkPush(args []string, dir string, overrides map[string][]string) string {
	var positional []string
	remoteOpt := ""
	deleting, tags := false, false
	pushOption := func(value string) string {
		key, _, _ := strings.Cut(value, "=")
		if slices.Contains(ladoMergePushOptions, strings.TrimSpace(key)) {
			return "the push option " + key + " merges the merge request"
		}
		return ""
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case a == "--all" || a == "--branches":
			return "git push " + a + " pushes every branch, the base branch too"
		case a == "--mirror":
			return "git push --mirror pushes every ref, the base branch too"
		case a == "--delete":
			deleting = true
		case a == "--tags":
			tags = true
		case a == "--push-option" || a == "--repo" || a == "--receive-pack" || a == "--exec":
			if i+1 < len(args) {
				i++
				switch a {
				case "--push-option":
					if why := pushOption(args[i]); why != "" {
						return why
					}
				case "--repo":
					remoteOpt = args[i]
				}
			}
		case strings.HasPrefix(a, "--push-option="):
			if why := pushOption(strings.TrimPrefix(a, "--push-option=")); why != "" {
				return why
			}
		case strings.HasPrefix(a, "--repo="):
			remoteOpt = strings.TrimPrefix(a, "--repo=")
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				switch a[j] {
				case 'd':
					deleting = true
				case 'o':
					value := a[j+1:]
					if value == "" && i+1 < len(args) {
						i++
						value = args[i]
					}
					if why := pushOption(value); why != "" {
						return why
					}
					j = len(a)
				}
			}
		default:
			positional = append(positional, a)
		}
	}
	remote := remoteOpt
	refspecs := positional
	if len(positional) > 0 {
		remote, refspecs = positional[0], positional[1:]
	}
	config := g.config(dir, overrides)
	current := g.currentBranch(dir)
	if remote == "" {
		remote = ladoPushRemote(config, current)
	}
	if len(refspecs) == 0 {
		if deleting || tags {
			return ""
		}
		return g.checkImplicitPush(dir, remote, current, config)
	}
	for _, refspec := range refspecs {
		if why := g.checkRefspec(dir, remote, current, config, refspec, deleting); why != "" {
			return why
		}
	}
	return ""
}

// ladoPushRemote is the remote a push without one goes to.
func ladoPushRemote(config map[string][]string, current string) string {
	if current != "" {
		if v := ladoConfigValue(config, "branch."+current+".pushremote"); v != "" {
			return v
		}
	}
	if v := ladoConfigValue(config, "remote.pushdefault"); v != "" {
		return v
	}
	if current != "" {
		if v := ladoConfigValue(config, "branch."+current+".remote"); v != "" {
			return v
		}
	}
	return "origin"
}

// ladoConfigValue is the last value of key, as git reads it.
func ladoConfigValue(config map[string][]string, key string) string {
	values := config[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[len(values)-1])
}

// checkImplicitPush decides a push that names no refspec: git pushes what
// remote.<remote>.push or push.default says.
func (g *ladoPushGuard) checkImplicitPush(dir, remote, current string, config map[string][]string) string {
	if configured := config["remote."+remote+".push"]; len(configured) > 0 {
		for _, refspec := range configured {
			if why := g.checkRefspec(dir, remote, current, config, refspec, false); why != "" {
				return why + " (remote." + remote + ".push)"
			}
		}
		return ""
	}
	mode := strings.ToLower(ladoConfigValue(config, "push.default"))
	switch mode {
	case "nothing":
		return ""
	case "matching":
		return "git push with push.default=matching pushes every branch the remote has, the base branch too"
	}
	if current == "" {
		return ""
	}
	if g.protected(dir, remote, current) {
		return "git push without a refspec pushes the current branch " + current + ", a base branch"
	}
	if mode == "upstream" || mode == "tracking" {
		if up := ladoUpstream(config, current); up != "" && g.protected(dir, remote, up) {
			return "git push without a refspec pushes " + current + " to its upstream " + up + ", a base branch (push.default=" + mode + ")"
		}
	}
	return ""
}

// ladoUpstream is the remote branch branch.<b>.merge names.
func ladoUpstream(config map[string][]string, branch string) string {
	return ladoBranchName(ladoConfigValue(config, "branch."+branch+".merge"))
}

// checkRefspec decides one refspec of a push to remote.
func (g *ladoPushGuard) checkRefspec(dir, remote, current string, config map[string][]string, refspec string, deleting bool) string {
	refspec = g.resolveCurrentBranchIdioms(refspec, current)
	refspec = strings.TrimPrefix(refspec, "+")
	if refspec == "" || strings.HasPrefix(refspec, "^") {
		return ""
	}
	if deleting {
		if name := g.protectedMatch(dir, remote, ladoBranchName(refspec)); name != "" {
			return "git push --delete removes the base branch " + name
		}
		return ""
	}
	src, dst, hasColon := strings.Cut(refspec, ":")
	if hasColon && src == "" && dst == "" {
		return "git push with the refspec \":\" pushes every branch the remote has, the base branch too"
	}
	if hasColon && src == "" {
		if name := g.protectedMatch(dir, remote, ladoBranchName(dst)); name != "" {
			return "git push " + remote + " :" + dst + " removes the base branch " + name
		}
		return ""
	}
	var targets []string
	switch {
	case hasColon && dst != "":
		targets = []string{dst}
	case src == "HEAD" || src == "@":
		targets = []string{current}
	default:
		targets = []string{src}
		short := ladoBranchName(src)
		mode := strings.ToLower(ladoConfigValue(config, "push.default"))
		if mode == "upstream" || mode == "tracking" {
			targets = append(targets, ladoUpstream(config, short))
		}
		for _, mapping := range config["remote."+remote+".push"] {
			from, to, ok := strings.Cut(strings.TrimPrefix(mapping, "+"), ":")
			if ok && (from == src || ladoBranchName(from) == short) {
				targets = append(targets, to)
			}
		}
	}
	for _, target := range targets {
		if name := g.protectedMatch(dir, remote, ladoBranchName(target)); name != "" {
			return "git push to the base branch " + name
		}
	}
	return ""
}

// ladoCurrentBranchIdioms are the substitutions a worker writes for its
// current branch; the guard reads them as that branch.
var ladoCurrentBranchIdioms = []string{
	"$(git branch --show-current)",
	"$(git rev-parse --abbrev-ref HEAD)",
	"$(git symbolic-ref --short HEAD)",
	"$(git symbolic-ref --short -q HEAD)",
	"$(git symbolic-ref -q --short HEAD)",
}

func (g *ladoPushGuard) resolveCurrentBranchIdioms(word, current string) string {
	if current == "" || !strings.Contains(word, "$(") {
		return word
	}
	for _, idiom := range ladoCurrentBranchIdioms {
		word = strings.ReplaceAll(word, idiom, current)
	}
	return word
}

// ladoBranchName is the branch a ref names: refs/heads/ dropped; "" for a
// ref outside refs/heads/ such as a tag.
func ladoBranchName(ref string) string {
	ref = strings.TrimSpace(ref)
	if rest, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return rest
	}
	if strings.HasPrefix(ref, "refs/") {
		return ""
	}
	return ref
}

// protected reports whether branch is a base branch of a push to remote: one
// the project names, main, master, or the remote's default branch.
func (g *ladoPushGuard) protected(dir, remote, branch string) bool {
	if branch == "" || strings.Contains(branch, "$") {
		return false
	}
	if branch == "main" || branch == "master" || slices.Contains(g.base, branch) {
		return true
	}
	return branch == g.remoteHead(dir, remote)
}

// protectedMatch returns the base branch a branch name, which may hold a
// glob, names; "" when it names none.
func (g *ladoPushGuard) protectedMatch(dir, remote, pattern string) string {
	if !strings.Contains(pattern, "*") {
		if g.protected(dir, remote, pattern) {
			return pattern
		}
		return ""
	}
	candidates := append([]string{"main", "master"}, g.base...)
	if head := g.remoteHead(dir, remote); head != "" {
		candidates = append(candidates, head)
	}
	for _, branch := range candidates {
		if ok, _ := path.Match(pattern, branch); ok {
			return branch
		}
	}
	return ""
}

func (g *ladoPushGuard) currentBranch(dir string) string {
	if dir == "" {
		return ""
	}
	if branch, ok := g.branches[dir]; ok {
		return branch
	}
	branch := g.git.CurrentBranch(dir)
	g.branches[dir] = branch
	return branch
}

func (g *ladoPushGuard) config(dir string, overrides map[string][]string) map[string][]string {
	config, ok := g.configs[dir]
	if !ok && dir != "" {
		config = g.git.Config(dir)
		g.configs[dir] = config
	}
	if len(overrides) == 0 {
		return config
	}
	merged := make(map[string][]string, len(config)+len(overrides))
	for k, v := range config {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

func (g *ladoPushGuard) remoteHead(dir, remote string) string {
	if dir == "" || !ladoRemoteName(remote) {
		return ""
	}
	key := dir + "\x00" + remote
	if head, ok := g.heads[key]; ok {
		return head
	}
	head := g.git.RemoteHead(dir, remote)
	g.heads[key] = head
	return head
}

// ladoShellParse is a command split into its simple commands' words, and the
// commands inside substitutions, to be read on their own.
type ladoShellParse struct {
	commands [][]string
	nested   []string
}

// ladoShellSplit splits a shell command into simple commands. It ends a
// command at ; & && || | ( ) and a line break, drops redirections with their
// targets, here-document bodies and comments, and reads quotes and escapes
// as the shell does. A substitution, $(...) or a backquoted one, is kept in
// its word as $(...) and its command goes to nested. Variables stay as
// written. It reports false for an unfinished quote or substitution.
func ladoShellSplit(command string) (ladoShellParse, bool) {
	var out ladoShellParse
	runes := []rune(command)
	n := len(runes)
	var words []string
	var word strings.Builder
	inWord, skipTarget := false, false
	var pending []ladoHeredoc
	flushWord := func() {
		if !inWord {
			return
		}
		if skipTarget {
			skipTarget = false
		} else {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	endCommand := func() {
		flushWord()
		if len(words) > 0 {
			out.commands = append(out.commands, words)
		}
		words = nil
	}
	substitution := func(inner string) {
		out.nested = append(out.nested, inner)
		word.WriteString("$(" + strings.TrimSpace(inner) + ")")
		inWord = true
	}
	for i := 0; i < n; i++ {
		r := runes[i]
		next := rune(0)
		if i+1 < n {
			next = runes[i+1]
		}
		switch {
		case r == '\\':
			if next == '\n' {
				i++
			} else if i+1 < n {
				word.WriteRune(next)
				inWord = true
				i++
			}
		case r == '\'':
			j := i + 1
			for j < n && runes[j] != '\'' {
				j++
			}
			if j >= n {
				return out, false
			}
			word.WriteString(string(runes[i+1 : j]))
			inWord = true
			i = j
		case r == '"':
			j := i + 1
			for ; j < n && runes[j] != '"'; j++ {
				switch {
				case runes[j] == '\\' && j+1 < n:
					if strings.ContainsRune("$`\"\\\n", runes[j+1]) {
						if runes[j+1] != '\n' {
							word.WriteRune(runes[j+1])
						}
					} else {
						word.WriteRune('\\')
						word.WriteRune(runes[j+1])
					}
					j++
				case runes[j] == '$' && j+1 < n && runes[j+1] == '(':
					end, ok := ladoMatchParen(runes, j+2)
					if !ok {
						return out, false
					}
					substitution(string(runes[j+2 : end]))
					j = end
				case runes[j] == '`':
					end, ok := ladoMatchBackquote(runes, j+1)
					if !ok {
						return out, false
					}
					substitution(string(runes[j+1 : end]))
					j = end
				default:
					word.WriteRune(runes[j])
				}
			}
			if j >= n {
				return out, false
			}
			inWord = true
			i = j
		case r == '$' && next == '(':
			end, ok := ladoMatchParen(runes, i+2)
			if !ok {
				return out, false
			}
			substitution(string(runes[i+2 : end]))
			i = end
		case r == '`':
			end, ok := ladoMatchBackquote(runes, i+1)
			if !ok {
				return out, false
			}
			substitution(string(runes[i+1 : end]))
			i = end
		case r == ' ' || r == '\t' || r == '\r':
			flushWord()
		case r == '\n':
			endCommand()
			for _, h := range pending {
				i = ladoSkipHeredoc(runes, i+1, h.delim, h.strip)
			}
			pending = nil
		case r == ';' || r == '|' || r == '(' || r == ')':
			endCommand()
		case r == '&' && next == '>':
			flushWord()
			i++
			if i+1 < n && runes[i+1] == '>' {
				i++
			}
			skipTarget = true
		case r == '&':
			endCommand()
		case r == '<' || r == '>':
			if inWord && strings.Trim(word.String(), "0123456789") == "" && !skipTarget {
				// A file descriptor number before the redirection.
				word.Reset()
				inWord = false
			} else {
				flushWord()
			}
			if r == '<' && next == '<' && i+2 < n && runes[i+2] == '<' {
				i += 2
				skipTarget = true
				continue
			}
			if r == '<' && next == '<' {
				var h ladoHeredoc
				h, i = ladoReadHeredoc(runes, i+2)
				pending = append(pending, h)
				i--
				continue
			}
			for i+1 < n && strings.ContainsRune(">&|", runes[i+1]) {
				i++
			}
			skipTarget = true
		case r == '#' && !inWord:
			for i+1 < n && runes[i+1] != '\n' {
				i++
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endCommand()
	return out, true
}

// ladoHeredoc is a here-document whose body starts on the next line.
type ladoHeredoc struct {
	delim string
	strip bool
}

// ladoReadHeredoc reads the delimiter of a here-document whose << ends just
// before start, quotes dropped, and returns the index after it.
func ladoReadHeredoc(runes []rune, start int) (ladoHeredoc, int) {
	i, n := start, len(runes)
	var h ladoHeredoc
	if i < n && runes[i] == '-' {
		h.strip = true
		i++
	}
	for i < n && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	var delim strings.Builder
	for i < n && !strings.ContainsRune(" \t\n;|&<>()", runes[i]) {
		if !strings.ContainsRune(`'"\`, runes[i]) {
			delim.WriteRune(runes[i])
		}
		i++
	}
	h.delim = delim.String()
	return h, i
}

// ladoMatchParen returns the index of the parenthesis that closes a
// substitution whose text starts at start. A here-document's body inside
// it is skipped, so a quote or a parenthesis in its text does not count.
func ladoMatchParen(runes []rune, start int) (int, bool) {
	depth := 1
	var pending []ladoHeredoc
	for j := start; j < len(runes); j++ {
		switch runes[j] {
		case '<':
			if j+1 < len(runes) && runes[j+1] == '<' {
				if j+2 < len(runes) && runes[j+2] == '<' {
					j += 2
					continue
				}
				var h ladoHeredoc
				h, j = ladoReadHeredoc(runes, j+2)
				pending = append(pending, h)
				j--
			}
		case '\n':
			for _, h := range pending {
				j = ladoSkipHeredoc(runes, j+1, h.delim, h.strip)
			}
			pending = nil
		case '\\':
			j++
		case '\'':
			j++
			for j < len(runes) && runes[j] != '\'' {
				j++
			}
		case '"':
			for j++; j < len(runes) && runes[j] != '"'; j++ {
				if runes[j] == '\\' {
					j++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j, true
			}
		}
	}
	return 0, false
}

// ladoMatchBackquote returns the index of the backquote that ends a
// substitution whose text starts at start.
func ladoMatchBackquote(runes []rune, start int) (int, bool) {
	for j := start; j < len(runes); j++ {
		switch runes[j] {
		case '\\':
			j++
		case '`':
			return j, true
		}
	}
	return 0, false
}

// ladoSkipHeredoc returns the index of the line break that ends a
// here-document body starting at start, or the end of the command.
func ladoSkipHeredoc(runes []rune, start int, delim string, strip bool) int {
	i := start
	for i < len(runes) {
		end := i
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		line := string(runes[i:end])
		if strip {
			line = strings.TrimLeft(line, "\t")
		}
		if line == delim {
			return end
		}
		i = end + 1
	}
	return len(runes)
}
