package cli

// LADO fork: `ao lado plan submit` and `ao lado plan critique submit`, which a
// LADO plan worker runs to hand LADO its plan or its findings
// (docs/LJY-PRD/TDD.md §5.1). A plan worker writes no files: it pipes the text
// in, and the daemon checks that the calling session is the request's
// designer or running critic.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// maxLadoSubmitBytes caps what a submission reads from stdin: a plan's limit
// with room for JSON.
const maxLadoSubmitBytes = 2 << 20

// submitLadoPlanRequest mirrors controllers.SubmitLadoPlanRequest.
type submitLadoPlanRequest struct {
	SessionID string `json:"sessionId"`
	Content   string `json:"content"`
	Draft     bool   `json:"draft"`
}

// submitLadoCritiqueRequest mirrors controllers.SubmitLadoCritiqueRequest.
type submitLadoCritiqueRequest struct {
	SessionID string             `json:"sessionId"`
	Findings  []ladoFindingInput `json:"findings"`
}

// ladoFindingInput mirrors controllers.LadoPlanFindingDTO.
type ladoFindingInput struct {
	ID              string `json:"id"`
	Severity        string `json:"severity"`
	Title           string `json:"title"`
	FailureScenario string `json:"failure_scenario,omitempty"`
	Where           string `json:"where,omitempty"`
	Suggestion      string `json:"suggestion,omitempty"`
}

// ladoPlanSubmitResponse is the part of the daemon's answer the CLI prints.
type ladoPlanSubmitResponse struct {
	ID    string `json:"id"`
	Phase string `json:"phase"`
}

func newLadoCommand(ctx *commandContext) *cobra.Command {
	lado := &cobra.Command{
		Use:   "lado",
		Short: "Commands LADO's plan workers run",
	}
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Hand LADO a plan or a review of one",
	}
	plan.AddCommand(newLadoPlanSubmitCommand(ctx), newLadoCritiqueCommand(ctx))
	lado.AddCommand(plan)
	return lado
}

func newLadoPlanSubmitCommand(ctx *commandContext) *cobra.Command {
	var request string
	var draft bool
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit the plan file, read from stdin, as this plan worker's draft or final plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, err := ladoSubmitter(request)
			if err != nil {
				return err
			}
			content, err := readLadoSubmission(cmd)
			if err != nil {
				return err
			}
			var res ladoPlanSubmitResponse
			path := "lado/plan-requests/" + url.PathEscape(strings.TrimSpace(request)) + "/submit"
			if err := ctx.postJSON(cmd.Context(), path, submitLadoPlanRequest{SessionID: session, Content: string(content), Draft: draft}, &res); err != nil {
				return err
			}
			if draft {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "LADO took the draft of %s. Wait for its message about the review.\n", res.ID)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "LADO took the plan of %s and pins it as a revision.\n", res.ID)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&request, "request", "", "The plan request id LADO gave you (required)")
	cmd.Flags().BoolVar(&draft, "draft", false, "Submit the draft LADO reviews, not the final plan")
	return cmd
}

func newLadoCritiqueCommand(ctx *commandContext) *cobra.Command {
	critique := &cobra.Command{
		Use:   "critique",
		Short: "Hand LADO a critic's findings",
	}
	var request string
	submit := &cobra.Command{
		Use:   "submit",
		Short: `Submit this critic's findings, read from stdin as {"findings":[...]}`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, err := ladoSubmitter(request)
			if err != nil {
				return err
			}
			raw, err := readLadoSubmission(cmd)
			if err != nil {
				return err
			}
			var body struct {
				Findings []ladoFindingInput `json:"findings"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				return usageError{fmt.Errorf(`read the findings: want JSON {"findings":[...]}: %w`, err)}
			}
			if body.Findings == nil {
				body.Findings = []ladoFindingInput{}
			}
			path := "lado/plan-requests/" + url.PathEscape(strings.TrimSpace(request)) + "/critique"
			if err := ctx.postJSON(cmd.Context(), path, submitLadoCritiqueRequest{SessionID: session, Findings: body.Findings}, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "LADO took %d findings. Your review is done.\n", len(body.Findings))
			return err
		},
	}
	submit.Flags().StringVar(&request, "request", "", "The plan request id LADO gave you (required)")
	critique.AddCommand(submit)
	return critique
}

// ladoSubmitter returns the session that submits: the plan worker the command
// runs in.
func ladoSubmitter(request string) (string, error) {
	if strings.TrimSpace(request) == "" {
		return "", usageError{errors.New("usage: --request is required")}
	}
	session := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if !sessionIDPattern.MatchString(session) {
		return "", usageError{errors.New("usage: run this inside the LADO plan worker's session (AO_SESSION_ID is not set)")}
	}
	return session, nil
}

// readLadoSubmission reads what a plan worker pipes in.
func readLadoSubmission(cmd *cobra.Command) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxLadoSubmitBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(raw) > maxLadoSubmitBytes {
		return nil, usageError{fmt.Errorf("the submission is over %d bytes", maxLadoSubmitBytes)}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, usageError{errors.New("usage: pipe the text into the command; stdin is empty")}
	}
	return raw, nil
}
