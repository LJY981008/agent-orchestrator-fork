package cli

// LADO fork: the hook decision of a LADO plan worker (docs/LJY-PRD/TDD.md
// §5.1). A plan worker is read-only: AO's PreToolUse and PermissionRequest
// hooks answer each of its Claude Code tool calls in place of a person, so a
// write is refused without asking and nothing waits for an approval. Only
// reading tools, a few read-only git and gh commands, and the submission in
// the exact form LADO gives pass.

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ladoPlanWorkerDenyMessage is what a refused plan worker reads.
const ladoPlanWorkerDenyMessage = "LADO plan worker: you are read-only and nobody can approve a prompt. " +
	"Allowed: Read, Grep, Glob; git log, show, diff, status, ls-files and ls-tree; gh issue view, gh pr view and gh pr diff, " +
	"optionally piped into head, tail, wc or grep; and the ao lado plan submit command in the exact form LADO gave you, " +
	"as one command with a quoted here-document. Everything else is refused."

// ladoPlanWorkerTools are the tools a plan worker uses without a check: they
// read, or keep its own to-do list.
var ladoPlanWorkerTools = []string{"Read", "Grep", "Glob", "LS", "NotebookRead", "TodoWrite"}

// ladoPlanReadCommands are the read-only commands a plan worker runs: a git
// subcommand, or a gh command of two words.
var (
	ladoPlanGitSubcommands = []string{"log", "show", "diff", "status", "ls-files", "ls-tree"}
	ladoPlanGhCommands     = []string{"issue view", "pr view", "pr diff"}
	ladoPlanFilters        = []string{"head", "tail", "wc", "grep"}
)

// ladoPlanSubmitHead is the first line of a submission with a here-document:
// the command, then a single-quoted delimiter, so the body is never expanded.
var ladoPlanSubmitHead = regexp.MustCompile(`^(ao lado plan (?:critique )?submit(?: --(?:request [A-Za-z0-9_-]+|draft))+) <<'([A-Za-z_][A-Za-z0-9_]*)'$`)

// ladoPlanSubmitPipe is a submission piped from printf '%s' and one
// single-quoted literal, in which a quote is written as the four characters
// quote, backslash, quote, quote.
var ladoPlanSubmitPipe = regexp.MustCompile(`^printf '%s' '[^']*(?:'\\''[^']*)*' \| (ao lado plan (?:critique )?submit(?: --(?:request [A-Za-z0-9_-]+|draft))+)$`)

// ladoPlanWorkerSession reports whether the hook runs for a LADO plan worker.
func ladoPlanWorkerSession() bool {
	return os.Getenv(domain.LadoPlanWorkerEnv) == "1"
}

// ladoPlanWorkerToolDecision decides one tool call of a plan worker. It
// reports whether to allow it.
func ladoPlanWorkerToolDecision(payload []byte) bool {
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return false
	}
	if slices.Contains(ladoPlanWorkerTools, p.ToolName) {
		return true
	}
	return p.ToolName == "Bash" && ladoPlanWorkerCommand(p.ToolInput.Command)
}

// ladoPlanWorkerCommand reports whether a Bash command is one a plan worker
// may run: a submission in LADO's form, or a read-only command.
func ladoPlanWorkerCommand(command string) bool {
	command = strings.TrimSuffix(strings.TrimSpace(command), "\n")
	if ladoPlanSubmission(command) {
		return true
	}
	segments, ok := shellPipeline(command)
	if !ok || len(segments) == 0 {
		return false
	}
	if !ladoPlanReadCommand(segments[0]) {
		return false
	}
	for _, filter := range segments[1:] {
		if len(filter) == 0 || !slices.Contains(ladoPlanFilters, filter[0]) {
			return false
		}
	}
	return true
}

// ladoPlanSubmission reports whether command submits a plan or findings in
// one of the forms LADO gives: a quoted here-document whose delimiter line
// comes once, last, or a printf of one single-quoted literal.
func ladoPlanSubmission(command string) bool {
	if strings.ContainsRune(command, '\r') {
		return false
	}
	if ladoPlanSubmitPipe.MatchString(command) {
		return true
	}
	lines := strings.Split(command, "\n")
	if len(lines) < 2 {
		return false
	}
	head := ladoPlanSubmitHead.FindStringSubmatch(lines[0])
	if head == nil || lines[len(lines)-1] != head[2] {
		return false
	}
	// The shell ends the here-document at the first line equal to its
	// delimiter; anything after it would run as a command.
	return !slices.Contains(lines[1:len(lines)-1], head[2])
}

// ladoPlanReadCommand reports whether words are a read-only git or gh command.
func ladoPlanReadCommand(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "git":
		rest := words[1:]
		// Global options that only choose what to read.
		for len(rest) > 0 {
			switch {
			case rest[0] == "--no-pager" || rest[0] == "-P":
				rest = rest[1:]
				continue
			case rest[0] == "-C" && len(rest) > 1:
				rest = rest[2:]
				continue
			}
			break
		}
		if len(rest) == 0 || !slices.Contains(ladoPlanGitSubcommands, rest[0]) {
			return false
		}
		for _, arg := range rest[1:] {
			// Options that write a file or run a program.
			if strings.HasPrefix(arg, "--output") || strings.HasPrefix(arg, "--ext-diff") || strings.HasPrefix(arg, "--exec") {
				return false
			}
		}
		return true
	case "gh":
		return len(words) >= 3 && slices.Contains(ladoPlanGhCommands, words[1]+" "+words[2])
	}
	return false
}

// shellPipeline splits a command into its pipeline's commands and their
// words. It refuses anything else the shell would do: a list (; & && ||), a
// redirection, an expansion ($, `, a glob, a brace, a leading ~), a subshell
// or a group, a comment, an escape, a line break and an unfinished quote.
// Single quotes keep their text; double quotes too, as long as it holds no
// expansion or escape.
func shellPipeline(command string) ([][]string, bool) {
	var segments [][]string
	var words []string
	var word strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t':
			flushWord()
		case r == '|':
			flushWord()
			if len(words) == 0 || (i+1 < len(runes) && (runes[i+1] == '|' || runes[i+1] == '&')) {
				return nil, false
			}
			segments = append(segments, words)
			words = nil
		case r == '\'' || r == '"':
			j := i + 1
			for ; j < len(runes) && runes[j] != r; j++ {
				if r == '"' && strings.ContainsRune("$`\\", runes[j]) {
					return nil, false
				}
			}
			if j >= len(runes) {
				return nil, false
			}
			inWord = true
			word.WriteString(string(runes[i+1 : j]))
			i = j
		case strings.ContainsRune(";&<>$`(){}\\\n\r*?[!", r):
			return nil, false
		case !inWord && (r == '~' || r == '#'):
			// A tilde expands and a hash starts a comment at a word's start.
			return nil, false
		default:
			inWord = true
			word.WriteRune(r)
		}
	}
	flushWord()
	if len(words) == 0 {
		return nil, false
	}
	return append(segments, words), true
}

// answerLadoPlanWorker answers a plan worker's PreToolUse or PermissionRequest
// hook with LadoPlanWorkerToolDecision. It reports whether the hook is done:
// a permission prompt it answered reports no activity, since nothing waits.
func answerLadoPlanWorker(out io.Writer, event string, payload []byte) (bool, error) {
	allow := ladoPlanWorkerToolDecision(payload)
	switch event {
	case "pre-tool-use":
		var res claudePreToolUseHookOutput
		res.HookSpecificOutput.HookEventName = "PreToolUse"
		res.HookSpecificOutput.PermissionDecision = "deny"
		res.HookSpecificOutput.PermissionDecisionReason = ladoPlanWorkerDenyMessage
		if allow {
			res.HookSpecificOutput.PermissionDecision = "allow"
			res.HookSpecificOutput.PermissionDecisionReason = "LADO plan worker: read-only call"
		}
		return false, json.NewEncoder(out).Encode(res)
	case "permission-request":
		var res claudePermissionHookOutput
		res.HookSpecificOutput.HookEventName = "PermissionRequest"
		res.HookSpecificOutput.Decision.Behavior = "deny"
		res.HookSpecificOutput.Decision.Message = ladoPlanWorkerDenyMessage
		if allow {
			res.HookSpecificOutput.Decision.Behavior = "allow"
			res.HookSpecificOutput.Decision.Message = ""
		}
		return true, json.NewEncoder(out).Encode(res)
	}
	return false, nil
}

// claudePreToolUseHookOutput is Claude Code's PreToolUse decision: allow runs
// the tool without a prompt, deny refuses it with the reason.
type claudePreToolUseHookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	} `json:"hookSpecificOutput"`
}
