package controllers

// LADO fork: DTOs for plans and approvals (docs/LJY-PRD/TDD.md §5.1, §5.2 and
// §13).

import "time"

// LadoPlanIDParam names a plan revision in /api/v1/lado/plans routes.
type LadoPlanIDParam struct {
	RevisionID string `path:"revisionId" description:"LADO plan revision id."`
}

// LadoPresenceHeaderParam carries a human-presence proof. The AO desktop app
// adds it after the developer confirms; no other caller can make one
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
type LadoPresenceHeaderParam struct {
	Proof string `header:"X-LADO-Presence" description:"Human-presence proof v1.<nonce>.<expires>.<mac>, signed by the AO desktop app over the method, path and body."`
}

// LadoPlanRevisionSummaryResponse is the newest revision as the board shows
// it.
type LadoPlanRevisionSummaryResponse struct {
	ID        string    `json:"id"`
	Number    int       `json:"number" description:"The task's revisions count from 1."`
	SHA256    string    `json:"sha256" description:"Hash of the pinned plan file. An approval binds to it."`
	Source    string    `json:"source" enum:"agent,user" description:"Who wrote the version: the orchestrator, or a person editing it in the artifact viewer."`
	Valid     bool      `json:"valid" description:"False when the plan has problems; such a plan cannot be approved."`
	CreatedAt time.Time `json:"createdAt"`
}

// LadoTaskPlanResponse is what the board needs to know about a task's plans.
type LadoTaskPlanResponse struct {
	RequestPending bool       `json:"requestPending" description:"A plan request waits for the orchestrator's plan."`
	RequestedAt    *time.Time `json:"requestedAt,omitempty" description:"When the newest plan request was made."`
	RequestError   string     `json:"requestError,omitempty" description:"Why LADO stopped delivering the newest request, when it did."`
	// Latest is the newest revision.
	Latest *LadoPlanRevisionSummaryResponse `json:"latest,omitempty"`
	// ApprovedRevisionID is set while a valid approval covers the latest
	// revision.
	ApprovedRevisionID string `json:"approvedRevisionId,omitempty" description:"The latest revision's id while a valid approval covers it."`
}

// LadoPlanProblemResponse is one thing wrong with a plan file.
type LadoPlanProblemResponse struct {
	Code   string `json:"code" enum:"too_large,not_text,missing_front_matter,invalid_front_matter,missing_field,invalid_field,wrong_task,wrong_issue,unknown_source_comment,wrong_source,unknown_base_ref" description:"Stable problem code the app translates."`
	Field  string `json:"field,omitempty" description:"The front matter key the problem is about."`
	Detail string `json:"detail,omitempty" description:"The offending value, or the parser's message."`
}

// LadoPlanRevisionResponse is one pinned plan revision.
type LadoPlanRevisionResponse struct {
	ID                  string                    `json:"id"`
	TaskID              string                    `json:"taskId"`
	RequestID           string                    `json:"requestId"`
	Number              int                       `json:"number"`
	Source              string                    `json:"source" enum:"agent,user"`
	SHA256              string                    `json:"sha256"`
	BaseRef             string                    `json:"baseRef,omitempty"`
	BaseSHA             string                    `json:"baseSha,omitempty" description:"The commit base_ref named when LADO pinned the revision."`
	IssueSnapshotSHA256 string                    `json:"issueSnapshotSha256" description:"The pinned issue snapshot the plan was made from."`
	Valid               bool                      `json:"valid"`
	Problems            []LadoPlanProblemResponse `json:"problems"`
	CreatedAt           time.Time                 `json:"createdAt"`
}

// LadoPlanRequestResponse is one request to the orchestrator for a plan.
type LadoPlanRequestResponse struct {
	ID               string     `json:"id"`
	TaskID           string     `json:"taskId"`
	SessionID        string     `json:"sessionId" description:"The orchestrator session asked for the plan."`
	SourceCommentID  string     `json:"sourceCommentId,omitempty" description:"The issue comment the developer named as the plan."`
	Note             string     `json:"note,omitempty" description:"What the developer asked to change."`
	BasedOn          string     `json:"basedOn,omitempty" description:"The revision a change request starts from."`
	RequestedAt      time.Time  `json:"requestedAt"`
	DeliveredAt      *time.Time `json:"deliveredAt,omitempty"`
	DeliveryAttempts int        `json:"deliveryAttempts"`
	DeliveryError    string     `json:"deliveryError,omitempty"`
	FailedAt         *time.Time `json:"failedAt,omitempty" description:"When LADO stopped trying to deliver the request."`
}

// LadoPlanApprovalResponse is one approval of an exact plan revision.
type LadoPlanApprovalResponse struct {
	ID         string    `json:"id"`
	RevisionID string    `json:"revisionId"`
	TaskID     string    `json:"taskId"`
	SHA256     string    `json:"sha256"`
	ApprovedAt time.Time `json:"approvedAt"`
}

// LadoPlanHistoryResponse is a task's plan requests, revisions and
// approvals, oldest first.
type LadoPlanHistoryResponse struct {
	Requests  []LadoPlanRequestResponse  `json:"requests"`
	Revisions []LadoPlanRevisionResponse `json:"revisions"`
	Approvals []LadoPlanApprovalResponse `json:"approvals"`
}

// LadoPlanContentResponse is a plan file's front matter, as far as it could
// be read.
type LadoPlanContentResponse struct {
	BaseRef            string   `json:"baseRef,omitempty"`
	Steps              []string `json:"steps"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	AffectedPaths      []string `json:"affectedPaths"`
	TestCommands       []string `json:"testCommands"`
	Risk               string   `json:"risk,omitempty"`
	PermissionCeiling  string   `json:"permissionCeiling,omitempty"`
	ReviewRequired     bool     `json:"reviewRequired"`
	Reviewer           string   `json:"reviewer,omitempty"`
	PlanSource         string   `json:"planSource,omitempty" description:"issue, or comment:<id> for a plan converted from an issue comment."`
	Body               string   `json:"body" description:"The Markdown after the front matter."`
}

// LadoPlanResponse is one revision with its pinned content.
type LadoPlanResponse struct {
	Task     LadoTaskResponse         `json:"task"`
	Revision LadoPlanRevisionResponse `json:"revision"`
	Content  string                   `json:"content" description:"The pinned plan file; empty when the pin is missing."`
	Plan     LadoPlanContentResponse  `json:"plan"`
	Latest   bool                     `json:"latest" description:"This is the task's newest revision."`
	Approved bool                     `json:"approved" description:"A valid approval covers this revision."`
}

// CreateLadoPlanRequestRequest asks the orchestrator for a plan, or for
// changes to the latest one.
type CreateLadoPlanRequestRequest struct {
	SourceCommentID string `json:"sourceCommentId,omitempty" description:"An issue comment that holds the plan; the orchestrator converts it faithfully."`
	Note            string `json:"note,omitempty" description:"What to change in the latest revision; makes this a change request."`
}

// ApproveLadoPlanRequest approves one revision. It needs a human-presence
// proof in X-LADO-Presence, which the desktop app adds after the developer
// confirms.
type ApproveLadoPlanRequest struct {
	SHA256 string `json:"sha256" description:"The revision's sha256 the developer reviewed."`
}

// LadoIssueTextResponse is a task's issue as read for the task view.
type LadoIssueTextResponse struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	State string `json:"state" enum:"open,in_progress,review,done,cancelled"`
	URL   string `json:"url"`
}

// LadoIssueCommentResponse is one comment on a task's issue.
type LadoIssueCommentResponse struct {
	ID            string    `json:"id"`
	Author        string    `json:"author"`
	AuthorTrusted bool      `json:"authorTrusted" description:"The author can work on the repository."`
	ByLado        bool      `json:"byLado" description:"LADO wrote this comment; it is never read back into a plan."`
	Included      bool      `json:"included" description:"A plan request pins this comment for the orchestrator."`
	CreatedAt     time.Time `json:"createdAt"`
	Body          string    `json:"body"`
	URL           string    `json:"url,omitempty"`
}

// LadoTaskViewResponse is the task view: the task, its issue and comments as
// they are now, and its plan history.
type LadoTaskViewResponse struct {
	Task           LadoTaskResponse           `json:"task"`
	Issue          *LadoIssueTextResponse     `json:"issue,omitempty" description:"Missing when the tracker could not be read."`
	Comments       []LadoIssueCommentResponse `json:"comments"`
	IssueReadError string                     `json:"issueReadError,omitempty"`
	// IssueChangedSincePlan says the issue or a pinned comment changed since
	// the latest revision's snapshot.
	IssueChangedSincePlan bool                    `json:"issueChangedSincePlan" description:"The issue or a comment a plan may use changed since the latest revision was planned."`
	History               LadoPlanHistoryResponse `json:"history"`
}
