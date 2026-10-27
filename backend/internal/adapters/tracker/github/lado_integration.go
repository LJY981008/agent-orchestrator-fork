package github

// LADO fork: the base branch's rules and the lado/integration commit status
// (docs/LJY-PRD/TDD.md §7.1). The rules endpoint returns the active rules
// that apply to a branch from every ruleset level and needs only Metadata
// read; a status needs Commit statuses write. Refusals keep their HTTP status
// as a ports.LadoHTTPError.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/httpkit"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// ladoRulePageSize is the largest page GitHub serves.
	ladoRulePageSize = 100
	// ladoRuleMaxPages caps the pages of rules one read follows.
	ladoRuleMaxPages = 10
)

// ladoCommitSHA is a full commit id, SHA-1 or SHA-256.
var ladoCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ghLadoBranchRule is the part of a branch rule LADO reads. Parameters
// differ by type, so they are decoded only for required_status_checks.
type ghLadoBranchRule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}

type ghLadoStatusChecks struct {
	RequiredStatusChecks []struct {
		Context       string `json:"context"`
		IntegrationID *int64 `json:"integration_id"`
	} `json:"required_status_checks"`
}

// BranchRules reads the active rules that apply to branch on repo, following
// the pages to the end. The branch is escaped as CheckAccess escapes it, so a
// slash stays a slash.
func (w *LadoIssues) BranchRules(ctx context.Context, repo domain.TrackerRepo, branch string) (ports.LadoBranchRules, error) {
	owner, name, err := ladoRepoParts(repo)
	if err != nil {
		return ports.LadoBranchRules{}, err
	}
	if strings.TrimSpace(branch) == "" {
		return ports.LadoBranchRules{}, errors.New("github tracker: no branch to read the rules of")
	}
	path := fmt.Sprintf("/repos/%s/%s/rules/branches/%s?per_page=%d", owner, name, (&url.URL{Path: branch}).EscapedPath(), ladoRulePageSize)
	var rules ports.LadoBranchRules
	for page := 1; ; page++ {
		answer, err := w.send(ctx, http.MethodGet, path, nil)
		if err != nil {
			return ports.LadoBranchRules{}, err
		}
		if !answer.ok() {
			return ports.LadoBranchRules{}, fmt.Errorf("github tracker: read the rules of %s: %w", branch, answer.httpError())
		}
		var raw []ghLadoBranchRule
		if err := json.Unmarshal(answer.body, &raw); err != nil {
			return ports.LadoBranchRules{}, fmt.Errorf("github tracker: decode branch rules: %w", err)
		}
		for _, r := range raw {
			rule := ports.LadoBranchRule{Type: r.Type}
			if r.Type == ports.LadoBranchRuleRequiredStatusChecks && len(r.Parameters) > 0 {
				var params ghLadoStatusChecks
				if err := json.Unmarshal(r.Parameters, &params); err != nil {
					return ports.LadoBranchRules{}, fmt.Errorf("github tracker: decode required status checks: %w", err)
				}
				rule.RequiredChecks = make([]ports.LadoRequiredCheck, 0, len(params.RequiredStatusChecks))
				for _, check := range params.RequiredStatusChecks {
					rule.RequiredChecks = append(rule.RequiredChecks, ports.LadoRequiredCheck{Context: check.Context, IntegrationID: check.IntegrationID})
				}
			}
			rules.Rules = append(rules.Rules, rule)
		}
		next := httpkit.ParseLinkNext(answer.header.Get("Link"), w.t.baseURL)
		if next == "" {
			return rules, nil
		}
		if page == ladoRuleMaxPages {
			// A partial list could miss a rule, so it is no answer.
			return ports.LadoBranchRules{}, fmt.Errorf("github tracker: the rules of %s run past %d pages", branch, ladoRuleMaxPages)
		}
		path = next
	}
}

// ghLadoStatus is the body of a commit status.
type ghLadoStatus struct {
	State       string `json:"state"`
	TargetURL   string `json:"target_url,omitempty"`
	Description string `json:"description"`
	Context     string `json:"context"`
}

// PostCommitStatus posts status on the commit sha. Only 201 Created is
// success.
func (w *LadoIssues) PostCommitStatus(ctx context.Context, repo domain.TrackerRepo, sha string, status ports.LadoCommitStatus) error {
	owner, name, err := ladoRepoParts(repo)
	if err != nil {
		return err
	}
	if !ladoCommitSHA.MatchString(sha) {
		return fmt.Errorf("github tracker: %q is not a commit SHA", sha)
	}
	body := ghLadoStatus{State: status.State, TargetURL: status.TargetURL, Description: status.Description, Context: status.Context}
	answer, err := w.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/statuses/%s", owner, name, sha), body)
	if err != nil {
		return err
	}
	if answer.status != http.StatusCreated {
		return fmt.Errorf("github tracker: post the %s status: %w", status.Context, answer.httpError())
	}
	return nil
}

// httpError is the answer as a ports.LadoHTTPError, with the token cut out of
// GitHub's message in case something on the way echoed it.
func (a ladoAnswer) httpError() *ports.LadoHTTPError {
	message := httpkit.Message(a.body)
	if a.token != "" {
		message = strings.ReplaceAll(message, a.token, "[redacted]")
	}
	return &ports.LadoHTTPError{Status: a.status, Message: message}
}
