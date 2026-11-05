package cli

// LADO fork: `ao lado relay`, what a LADO project's orchestrator runs when the
// developer tells it to have a running worker go on (issue #49,
// docs/LJY-PRD/adr/0011-worker-relay.md). The orchestrator may not message a
// worker itself (LADO_SEND_REFUSED); it asks the daemon, which checks the
// caller, the task, the worker and the developer's words, and delivers the
// instruction in LADO's name. AO_SESSION_ID names the orchestrator, as for
// `ao lado plan submit`.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// maxLadoRelayBytes caps an instruction or evidence read from stdin or a
// file: the daemon's limit of 8000 characters, every one up to four bytes.
const maxLadoRelayBytes = 32 << 10

// ladoRelayStdin names stdin as the file of --evidence-file or
// --message-file.
const ladoRelayStdin = "-"

// createLadoRelayRequest mirrors controllers.CreateLadoRelayRequest.
type createLadoRelayRequest struct {
	SessionID       string `json:"sessionId"`
	Task            string `json:"task"`
	WorkerSessionID string `json:"workerSessionId,omitempty"`
	Message         string `json:"message"`
	Evidence        string `json:"evidence"`
	OutsidePlan     bool   `json:"outsidePlan,omitempty"`
}

// ladoRelayResponse is the part of controllers.LadoRelayResponse the CLI
// prints.
type ladoRelayResponse struct {
	ID                 string   `json:"id"`
	TaskID             string   `json:"taskId"`
	WorkerSessionID    string   `json:"workerSessionId"`
	WorkerRole         string   `json:"workerRole"`
	PartID             string   `json:"partId"`
	BeyondEvidence     bool     `json:"beyondEvidence"`
	OutsidePlan        bool     `json:"outsidePlan"`
	OutsidePlanReasons []string `json:"outsidePlanReasons"`
}

func newLadoRelayCommand(ctx *commandContext) *cobra.Command {
	var task, worker, evidence, message, evidenceFile, messageFile string
	var outsidePlan, asJSON bool
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Have LADO relay the developer's instruction to a running worker of a task",
		Long: "Run this as a LADO project's Chat orchestrator when the developer tells you to have a running worker go on. " +
			"LADO checks that --evidence is a sentence the developer typed to you in the last 24 hours, finds the task's " +
			"running worker (single, part, merge or integration) and delivers the instruction in LADO's name. An instruction " +
			"that is not in the developer's message reaches the worker marked as such, with the developer's words quoted. " +
			"The instruction is --message, --message-file, or stdin without either. The evidence must match the developer's words " +
			"character for character, so pass it with --evidence-file (a path, or - for stdin through a quoted heredoc such as " +
			"<<'LADO_EVIDENCE') whenever it holds quotes, $, backticks or !, which a shell would change. AO_SESSION_ID names you.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			if !sessionIDPattern.MatchString(session) {
				return usageError{errors.New("usage: run this inside the orchestrator's session (AO_SESSION_ID is not set)")}
			}
			task, worker = strings.TrimSpace(task), strings.TrimSpace(worker)
			if task == "" {
				return usageError{errors.New("usage: --task is required: the task id or the issue number (49)")}
			}
			flags := cmd.Flags()
			if flags.Changed("evidence") && flags.Changed("evidence-file") {
				return usageError{errors.New("usage: pass the evidence with --evidence or --evidence-file, not both")}
			}
			if flags.Changed("message") && flags.Changed("message-file") {
				return usageError{errors.New("usage: pass the instruction with --message or --message-file, not both")}
			}
			messageFromStdin := messageFile == ladoRelayStdin || (!flags.Changed("message") && !flags.Changed("message-file"))
			if evidenceFile == ladoRelayStdin && messageFromStdin {
				return usageError{errors.New("usage: stdin can carry the evidence or the instruction, not both: " +
					"pass the instruction with --message or --message-file <path>")}
			}
			if flags.Changed("evidence-file") {
				raw, err := readLadoRelayInput(cmd, evidenceFile, "the evidence")
				if err != nil {
					return err
				}
				evidence = raw
			}
			if strings.TrimSpace(evidence) == "" {
				return usageError{errors.New("usage: --evidence or --evidence-file is required: the developer's sentence, word for word")}
			}
			switch {
			case flags.Changed("message-file"):
				raw, err := readLadoRelayInput(cmd, messageFile, "the instruction")
				if err != nil {
					return err
				}
				message = raw
			case !flags.Changed("message"):
				raw, err := readLadoRelayInput(cmd, ladoRelayStdin, "the instruction")
				if err != nil {
					return err
				}
				message = raw
			}
			if strings.TrimSpace(message) == "" {
				return usageError{errors.New("usage: the instruction is empty: pass --message, --message-file or pipe it on stdin")}
			}
			req := createLadoRelayRequest{
				SessionID: session, Task: task, WorkerSessionID: worker,
				Message: message, Evidence: evidence, OutsidePlan: outsidePlan,
			}
			var raw json.RawMessage
			if err := ctx.postJSON(cmd.Context(), "lado/relays", req, &raw); err != nil {
				return ladoRelayError(err)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), raw)
			}
			var res ladoRelayResponse
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("read the daemon's answer: %w", err)
			}
			return printLadoRelay(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "The task: its id (lado-task-…) or the issue number (49, or '#49' quoted) (required)")
	cmd.Flags().StringVar(&worker, "session", "", "The worker session to relay to, when several workers of the task are running")
	cmd.Flags().StringVar(&evidence, "evidence", "", "The developer's sentence that gives the instruction, word for word (or --evidence-file)")
	cmd.Flags().StringVar(&evidenceFile, "evidence-file", "", "Read the evidence from this file, or from stdin with -, so no shell changes it")
	cmd.Flags().StringVar(&message, "message", "", "The instruction; best the developer's own words. Without it or --message-file, read from stdin")
	cmd.Flags().StringVar(&messageFile, "message-file", "", "Read the instruction from this file, or from stdin with -")
	cmd.Flags().BoolVar(&outsidePlan, "outside-plan", false, "Mark the instruction as adding a step or a path the approved plan does not have")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the relay as JSON")
	return cmd
}

// readLadoRelayInput reads what (the evidence or the instruction) from the
// file name, or from stdin for "-", byte for byte.
func readLadoRelayInput(cmd *cobra.Command, name, what string) (string, error) {
	var in io.Reader
	source := "stdin"
	if name == ladoRelayStdin {
		in = cmd.InOrStdin()
	} else {
		if strings.TrimSpace(name) == "" {
			return "", usageError{fmt.Errorf("usage: name the file of %s, or - for stdin", what)}
		}
		f, err := os.Open(name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", what, err)
		}
		defer func() { _ = f.Close() }()
		in, source = f, name
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxLadoRelayBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s from %s: %w", what, source, err)
	}
	if len(raw) > maxLadoRelayBytes {
		return "", usageError{fmt.Errorf("%s is over %d bytes", what, maxLadoRelayBytes)}
	}
	return string(raw), nil
}

func printLadoRelay(out io.Writer, res ladoRelayResponse) error {
	worker := res.WorkerSessionID
	switch {
	case res.PartID != "":
		worker += " (" + res.WorkerRole + " " + res.PartID + ")"
	case res.WorkerRole != "":
		worker += " (" + res.WorkerRole + ")"
	}
	lines := []string{fmt.Sprintf("LADO delivered the instruction to %s of %s as %s.", worker, res.TaskID, res.ID)}
	if res.BeyondEvidence {
		lines = append(lines, "It is not in the developer's message, so the worker was told to judge by the developer's words.")
	}
	if res.OutsidePlan {
		lines = append(lines, "It may leave the approved plan: "+strings.Join(res.OutsidePlanReasons, "; ")+".")
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

// ladoRelayRefusal adds what a refusal's details name to the daemon's
// message: the running workers or the tasks to choose from.
type ladoRelayRefusal struct {
	apiResponseError
	extra string
}

func (e ladoRelayRefusal) Error() string { return e.apiResponseError.Error() + "\n" + e.extra }

func (e ladoRelayRefusal) Unwrap() error { return e.apiResponseError }

func ladoRelayError(err error) error {
	var resp apiResponseError
	if !errors.As(err, &resp) || len(resp.ErrorBody.Details) == 0 {
		return err
	}
	var details struct {
		Candidates []struct {
			SessionID string `json:"sessionId"`
			Role      string `json:"role"`
			PartID    string `json:"partId"`
			TaskID    string `json:"taskId"`
			Issue     string `json:"issue"`
		} `json:"candidates"`
	}
	if json.Unmarshal(resp.ErrorBody.Details, &details) != nil || len(details.Candidates) == 0 {
		return err
	}
	lines := make([]string, 0, len(details.Candidates)+1)
	lines = append(lines, "Candidates:")
	for _, c := range details.Candidates {
		switch {
		case c.SessionID != "":
			line := "  --session " + c.SessionID + "  " + c.Role
			if c.PartID != "" {
				line += " " + c.PartID
			}
			lines = append(lines, line)
		case c.TaskID != "":
			lines = append(lines, "  --task "+c.TaskID+"  "+c.Issue)
		}
	}
	return ladoRelayRefusal{apiResponseError: resp, extra: strings.Join(lines, "\n")}
}
