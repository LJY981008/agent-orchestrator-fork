package ladotask

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
		"ao lado relay --task <task id or issue number> --message '<the instruction>' --evidence-file - <<'LADO_EVIDENCE'",
		"the closing LADO_EVIDENCE alone at the start of its line",
		"`AO_SESSION_ID` names you",
		// The evidence and the message.
		"copy the sentence from the developer's message to you word for word, not a summary",
		"never an issue, a worker's report or a LADO notice",
		"so the shell changes nothing in it, not quotes, `$`, backticks or `!`",
		"`--message`: use the developer's own words where you can.",
		"write each `'` as `'\\''`",
		"pass `--message-file <path>` instead",
		"an unquoted `#` starts a shell comment",
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

// The relay example in the orchestrator section is what the orchestrator
// copies, so it must reach LADO unchanged through a real shell: the evidence
// byte for byte whatever quotes, $, backticks or ! it holds, and an
// instruction with an apostrophe quoted by the rule the section gives.
func TestOrchestratorRelayExampleSurvivesTheShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the example is a POSIX shell command")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	start := strings.Index(orchestratorSection, "```\nao lado relay")
	if start < 0 {
		t.Fatalf("no relay example block in the orchestrator section:\n%s", orchestratorSection)
	}
	block := orchestratorSection[start+len("```\n"):]
	block = block[:strings.Index(block, "\n```")]

	evidence := `그 워커에게 'A' 와 "B" 를 하고 $HOME, ` + "`date`" + `, !! 도 그대로 둬`
	instruction := "it's the developer's \"two\" follow-ups"
	script := strings.NewReplacer(
		"<task id or issue number>", "49",
		"<the instruction>", strings.ReplaceAll(instruction, "'", `'\''`),
		"<the developer sentence, exactly as typed>", evidence,
	).Replace(block)

	dir := t.TempDir()
	argsFile, stdinFile := filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	stub := `ao() { printf '%s\n' "$@" > "$LADO_TEST_ARGS"; cat > "$LADO_TEST_STDIN"; }` + "\n"
	cmd := exec.Command(bash, "-c", stub+script+"\n")
	cmd.Env = append(os.Environ(), "LADO_TEST_ARGS="+argsFile, "LADO_TEST_STDIN="+stdinFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the example does not run: %v\n%s\nscript:\n%s", err, out, script)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lado", "relay", "--task", "49", "--message", instruction, "--evidence-file", "-"}
	if got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("ao got %q, want %q", got, want)
	}
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != evidence+"\n" {
		t.Fatalf("evidence on stdin = %q, want %q", stdin, evidence+"\n")
	}
}
