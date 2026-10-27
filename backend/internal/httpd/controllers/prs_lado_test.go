package controllers_test

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

// LADO fork, PRD gate 7: the merge route renders the merge gate's refusal
// with its status, code and details, and maps AO's own errors as before.
func TestPRMergeRendersLadoRefusal(t *testing.T) {
	details := map[string]any{
		"prUrl":     "https://github.com/acme/widgets/pull/42",
		"headSha":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sessionId": "lado-1",
	}
	refusal := apierr.Conflict("LADO_INTEGRATION_APPROVAL_REQUIRED", "Approve the integration of this head in the AO desktop app first.", details)
	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		details map[string]any
	}{
		{"the gate's refusal", refusal, http.StatusConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED", details},
		{"the refusal wrapped", fmt.Errorf("merge: %w", refusal), http.StatusConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED", details},
		{"a gate that cannot read", apierr.Internal("LADO_MERGE_GATE_FAILED", "LADO could not check this pull request's integration approval."), http.StatusInternalServerError, "LADO_MERGE_GATE_FAILED", nil},
		{"a moved head", prsvc.ErrPRHeadChanged, http.StatusConflict, "PR_HEAD_CHANGED", nil},
		{"preconditions", fmt.Errorf("%w: pull request head is unknown", prsvc.ErrPRPreconditions), http.StatusUnprocessableEntity, "PR_PRECONDITIONS_UNMET", nil},
		{"anything else", errors.New("provider exploded"), http.StatusInternalServerError, "PR_OPERATION_FAILED", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newPRTestServer(t, &fakePRService{mergeErr: c.err})
			body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", validMergeBody)
			assertJSON(t, headers)
			assertErrorCode(t, body, status, c.status, c.code)
			var got errorBody
			mustJSON(t, body, &got)
			if !reflect.DeepEqual(got.Details, c.details) {
				t.Errorf("details = %#v, want %#v", got.Details, c.details)
			}
		})
	}
}
