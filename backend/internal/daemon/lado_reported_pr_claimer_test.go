package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// reportedClaimRecorder answers a claim with err and records what it was
// asked.
type reportedClaimRecorder struct {
	err     error
	session domain.SessionID
	ref     string
	opts    sessionsvc.ClaimPROptions
	calls   int
}

func (r *reportedClaimRecorder) ClaimPR(_ context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	r.session, r.ref, r.opts = id, ref, opts
	r.calls++
	return sessionsvc.ClaimPRResult{}, r.err
}

// The reported PR claimer (#71) claims through the session service without
// ever taking a PR over, and turns each claim error into a refusal with its
// reason or an error that may pass later.
func TestLadoReportedPRClaimerClaimsWithoutTakeoverAndSortsErrors(t *testing.T) {
	const url = "https://github.com/acme/demo/pull/7"
	cases := []struct {
		name   string
		err    error
		reason string // "" for an error that may pass later
	}{
		{"owned by an active session", ports.PRClaimedByActiveSessionError{Owner: "ao-2"}, domain.LadoReportedPROwnedElsewhere},
		{"owned, wrapped", fmt.Errorf("claim: %w", ports.PRClaimedByActiveSessionError{Owner: "ao-2"}), domain.LadoReportedPROwnedElsewhere},
		{"outside the project", sessionsvc.ErrProjectMismatch, domain.LadoReportedPROutsideProject},
		{"not open", sessionsvc.ErrPRNotOpen, domain.LadoReportedPRNotOpen},
		{"not a PR", sessionsvc.ErrInvalidPRRef, domain.LadoReportedPRInvalid},
		{"session not claimable", sessionsvc.ErrSessionNotClaimable, domain.LadoReportedPRSessionNotClaimable},
		{"session without a workspace", sessionsvc.ErrSessionNoWorkspace, domain.LadoReportedPRSessionNotClaimable},
		{"session terminated", apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil), domain.LadoReportedPRSessionNotClaimable},
		{"SCM unavailable", sessionsvc.ErrSCMUnavailable, ""},
		{"SCM unavailable around a host error", fmt.Errorf("%w: %w", sessionsvc.ErrSCMUnavailable, sessionsvc.ErrPRNotOpen), ""},
		{"PR not found", sessionsvc.ErrPRNotFound, ""},
		{"project not resolvable", apierr.Invalid("PROJECT_NOT_RESOLVABLE", "Project is not registered", nil), ""},
		{"invalid project config", apierr.Invalid("INVALID_PROJECT_CONFIG", "bad canonical URL", nil), ""},
		{"session not found", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), ""},
		{"anything else", errors.New("database is locked"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessions := &reportedClaimRecorder{err: tc.err}
			err := ladoReportedPRClaimer{sessions: sessions}.ClaimReportedPR(context.Background(), "ao-1", url)
			if sessions.calls != 1 || sessions.session != "ao-1" || sessions.ref != url {
				t.Fatalf("claim = %d calls, %s %s; want one for ao-1 and the PR", sessions.calls, sessions.session, sessions.ref)
			}
			if sessions.opts.AllowTakeover {
				t.Fatal("a reported PR claim must never take a PR over")
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.err)
			}
			var refusal *ladoverify.ReportedPRRefusal
			refused := errors.As(err, &refusal)
			switch {
			case tc.reason == "" && refused:
				t.Errorf("err = refusal %q, want an error that may pass later", refusal.Reason)
			case tc.reason != "" && (!refused || refusal.Reason != tc.reason):
				t.Errorf("err = %v, want a refusal %q", err, tc.reason)
			}
		})
	}

	sessions := &reportedClaimRecorder{}
	if err := (ladoReportedPRClaimer{sessions: sessions}).ClaimReportedPR(context.Background(), "ao-1", url); err != nil || sessions.opts.AllowTakeover {
		t.Errorf("a claim that passes = %v, takeover %v; want nil without takeover", err, sessions.opts.AllowTakeover)
	}
}
