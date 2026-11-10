package cli

// LADO fork: `ao lado limits`, which lists the account usage limits LADO
// holds new workers for, and `ao lado limits release`, which releases one by
// hand after the developer logged in to another account (issue #92). An
// orchestrator runs these instead of touching LADO's database.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxLadoLimitReason is the daemon's limit of a release reason, in
// characters.
const maxLadoLimitReason = 500

// ladoAccountLimit mirrors controllers.LadoAccountLimitResponse.
type ladoAccountLimit struct {
	AccountKey      string     `json:"accountKey"`
	SessionID       string     `json:"sessionId"`
	IdentityKnown   bool       `json:"identityKnown"`
	Active          bool       `json:"active"`
	ResetAt         time.Time  `json:"resetAt"`
	ResetGuessed    bool       `json:"resetGuessed"`
	DetectedAt      time.Time  `json:"detectedAt"`
	ReleasedAt      *time.Time `json:"releasedAt"`
	ReleaseReason   string     `json:"releaseReason"`
	ReleasedWaiting int        `json:"releasedWaiting"`
}

// ladoAccountLimitList mirrors controllers.LadoAccountLimitListResponse.
type ladoAccountLimitList struct {
	Limits []ladoAccountLimit `json:"limits"`
}

// releaseLadoAccountLimitRequest mirrors
// controllers.ReleaseLadoAccountLimitRequest.
type releaseLadoAccountLimitRequest struct {
	Reason string `json:"reason"`
}

// releaseLadoAccountLimitResponse mirrors
// controllers.ReleaseLadoAccountLimitResponse.
type releaseLadoAccountLimitResponse struct {
	Limit    ladoAccountLimit `json:"limit"`
	Released bool             `json:"released"`
	Waiting  int              `json:"waiting"`
}

func newLadoLimitsCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "limits",
		Short: "List the account usage limits LADO holds new workers for, or release one",
		Long: "List the account usage limits LADO recorded. While a limit is active, LADO holds every new worker on that " +
			"account (plan workers and queued runs wait as account_limited) until the reset time. When the developer " +
			"logged in to another account, release the limit with `ao lado limits release <account-key> --reason <text>`; " +
			"never edit LADO's database for it.",
		Args: ladoNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var raw json.RawMessage
			if err := ctx.getJSON(cmd.Context(), "lado/account-limits", &raw); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), raw)
			}
			var res ladoAccountLimitList
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("decode the account limits: %w", err)
			}
			return writeLadoAccountLimits(cmd.OutOrStdout(), res.Limits)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the daemon's answer as JSON")
	cmd.AddCommand(newLadoLimitsReleaseCommand(ctx))
	return cmd
}

func newLadoLimitsReleaseCommand(ctx *commandContext) *cobra.Command {
	var reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "release <account-key>",
		Short: "Release an active account usage limit, such as after logging in to another account",
		Long: "Release the active usage limit of an account key from `ao lado limits` (such as claude:device-claude) " +
			"after the developer logged in to another account. LADO records the reason in its audit log, checks the " +
			"account_limited waits again so they start, and resumes the workers the limit stopped.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError{errors.New("usage: ao lado limits release <account-key> --reason <text>")}
			}
			key := strings.TrimSpace(args[0])
			if _, _, ok := domain.ParseLadoAccountKey(key); !ok {
				return usageError{fmt.Errorf("usage: %q is not an account key: pass one from `ao lado limits`, such as claude:device-claude", key)}
			}
			reason = strings.TrimSpace(reason)
			if reason == "" {
				return usageError{errors.New("usage: --reason is required: say why the limit no longer holds, such as the developer logging in to another account")}
			}
			if utf8.RuneCountInString(reason) > maxLadoLimitReason {
				return usageError{fmt.Errorf("usage: --reason must be at most %d characters", maxLadoLimitReason)}
			}
			var raw json.RawMessage
			path := "lado/account-limits/" + url.PathEscape(key) + "/release"
			if err := ctx.postJSON(cmd.Context(), path, releaseLadoAccountLimitRequest{Reason: reason}, &raw); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), raw)
			}
			var res releaseLadoAccountLimitResponse
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("decode the release: %w", err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"LADO released the usage limit of %s. It checks %d account_limited waits again and resumes the workers the limit stopped.\n",
				key, res.Waiting)
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why the limit no longer holds, 1 to 500 characters (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the daemon's answer as JSON")
	return cmd
}

// ladoNoArgs refuses arguments as a usage error (exit 2).
func ladoNoArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageError{fmt.Errorf("usage: %s takes no arguments, got %q", cmd.CommandPath(), args[0])}
	}
	return nil
}

func writeLadoAccountLimits(out io.Writer, limits []ladoAccountLimit) error {
	if len(limits) == 0 {
		_, err := fmt.Fprintln(out, "No account usage limits recorded.")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ACCOUNT\tSTATE\tRESET\tDETECTED\tRELEASED\tREASON\tWAITING"); err != nil {
		return err
	}
	active := false
	for _, l := range limits {
		state := "released"
		if l.Active {
			state, active = "active", true
		} else if l.ReleasedAt == nil {
			state = "expired"
		}
		reset := ladoLimitTime(l.ResetAt)
		if l.ResetGuessed {
			reset += " (guessed)"
		}
		released, reason, waiting := "-", "-", "-"
		if l.ReleasedAt != nil {
			released, reason, waiting = ladoLimitTime(*l.ReleasedAt), ladoOrDash(l.ReleaseReason), fmt.Sprint(l.ReleasedWaiting)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.AccountKey, state, reset,
			ladoLimitTime(l.DetectedAt), released, reason, waiting); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !active {
		return nil
	}
	_, err := fmt.Fprintln(out, "\nNew workers on an active account wait until its reset. If the developer logged in to another account, "+
		"run: ao lado limits release <account> --reason <text>")
	return err
}

// ladoLimitTime shows a time in the machine's zone, as the developer reads it.
func ladoLimitTime(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04 MST")
}
