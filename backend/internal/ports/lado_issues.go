package ports

// LADO fork: issue writes and token checks on a project's repository
// (docs/LJY-PRD/TDD.md §5.1 and docs/LJY-PRD/adr/0006-issues-as-work-items.md),
// and the base branch's rules and the lado/integration commit status
// (TDD §7.1).

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LadoRepoClient reads, creates and checks issues on one repository host for
// LADO.
type LadoRepoClient interface {
	Tracker
	// CreateIssue creates an issue on repo. An error wrapping
	// ErrLadoIssueNotCreated means the tracker answered without creating one;
	// any other error leaves the outcome unknown.
	CreateIssue(ctx context.Context, repo domain.TrackerRepo, title, body string) (domain.Issue, error)
	// Login returns the account the token belongs to. An error wrapping
	// ErrLadoTokenRejected means the host refused the token.
	Login(ctx context.Context) (string, error)
	// CheckAccess reports whose token this is and what it can do on repo. An
	// error wrapping ErrLadoTokenRejected means the host refused the token.
	CheckAccess(ctx context.Context, repo domain.TrackerRepo) (LadoRepoAccess, error)
	// IssueComments lists the people's comments on an issue, oldest first,
	// with whether each author can work on the repository. GitLab's system
	// notes are not comments and are left out. A plan request pins the
	// trusted ones, since an issue may hold its plan in a comment
	// (docs/LJY-PRD/TDD.md §5.1).
	IssueComments(ctx context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error)
	// BranchRules reads the active rules that apply to branch on repo, from
	// every ruleset level (docs/LJY-PRD/TDD.md §7.1). A refusal is a
	// *LadoHTTPError; a provider without such rules answers
	// ErrLadoUnsupported.
	BranchRules(ctx context.Context, repo domain.TrackerRepo, branch string) (LadoBranchRules, error)
	// PostCommitStatus posts status on the commit sha. A refusal is a
	// *LadoHTTPError; a provider LADO does not post to answers
	// ErrLadoUnsupported.
	PostCommitStatus(ctx context.Context, repo domain.TrackerRepo, sha string, status LadoCommitStatus) error
}

// LadoRepoClients hands out repository clients by host.
type LadoRepoClients interface {
	// ForRepo returns a client for repo's host. With an empty token it uses
	// the host's token chain: AO's environment overrides, then the stored
	// token, then the provider's CLI.
	ForRepo(ctx context.Context, repo domain.TrackerRepo, token string) (LadoRepoClient, error)
}

// LADO repository client errors.
var (
	ErrLadoIssueNotCreated = errors.New("the tracker did not create the issue")
	ErrLadoTokenRejected   = errors.New("the host rejected the token")
	ErrLadoUnsupported     = errors.New("lado: not supported for this provider")
)

// LadoHTTPError is a host's refusal of a LADO request: the HTTP status and
// the host's message. It never holds the token.
type LadoHTTPError struct {
	Status  int
	Message string
}

func (e *LadoHTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the host answered %d", e.Status)
	}
	return fmt.Sprintf("the host answered %d: %s", e.Status, e.Message)
}

// Branch rule types LADO reads, as GitHub names them.
const (
	LadoBranchRulePullRequest          = "pull_request"
	LadoBranchRuleRequiredStatusChecks = "required_status_checks"
	LadoBranchRuleNonFastForward       = "non_fast_forward"
	LadoBranchRuleDeletion             = "deletion"
)

// LadoBranchRules are the rules that apply to one branch.
type LadoBranchRules struct {
	Rules []LadoBranchRule
}

// LadoBranchRule is one rule. A branch may get the same type from several
// rulesets.
type LadoBranchRule struct {
	// Type is the provider's rule type, such as LadoBranchRulePullRequest.
	Type string
	// RequiredChecks are the checks a required_status_checks rule names.
	RequiredChecks []LadoRequiredCheck
}

// LadoRequiredCheck is one check a rule requires.
type LadoRequiredCheck struct {
	Context string
	// IntegrationID is the app the check must come from; nil means any.
	IntegrationID *int64
}

// LadoCommitStatus is a commit status LADO posts. An empty TargetURL is not
// sent.
type LadoCommitStatus struct {
	State       string
	Context     string
	Description string
	TargetURL   string
}

// LadoRepoAccess is what a token can do on a repository.
type LadoRepoAccess struct {
	// Login is the account the token belongs to.
	Login         string
	DefaultBranch string
	Permissions   []LadoPermission
}

// LadoPermissionName names one thing LADO needs a token to do.
type LadoPermissionName string

// The permissions LADO checks. Build step 4 uses repository and issues; the
// others serve integration and verification in later steps.
const (
	LadoPermissionRepository     LadoPermissionName = "repository"
	LadoPermissionIssues         LadoPermissionName = "issues"
	LadoPermissionPullRequests   LadoPermissionName = "pull_requests"
	LadoPermissionBranchRules    LadoPermissionName = "branch_rules"
	LadoPermissionCommitStatuses LadoPermissionName = "commit_statuses"
)

// LadoPermissionStatus is the result of checking one permission.
type LadoPermissionStatus string

// Permission check results. Unverified means the provider cannot say without
// trying the write itself.
const (
	LadoPermissionOK         LadoPermissionStatus = "ok"
	LadoPermissionMissing    LadoPermissionStatus = "missing"
	LadoPermissionUnverified LadoPermissionStatus = "unverified"
)

// LadoPermission is one checked permission.
type LadoPermission struct {
	Name   LadoPermissionName
	Status LadoPermissionStatus
	// Detail says what is missing or why it cannot be verified.
	Detail string
}
