package domain

import (
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// LadoIssueComment is a comment on an issue as the tracker reports it.
type LadoIssueComment struct {
	ID     string
	Author string
	// AuthorTrusted says the author can work on the repository: on GitHub the
	// repository's owner, a member of its organization or a collaborator; on
	// GitLab a member with at least the Developer role. Anyone else can
	// comment on a public repository, so only trusted comments reach an
	// orchestrator or a worker.
	AuthorTrusted bool
	CreatedAt     time.Time
	Body          string
	URL           string
}

// LadoCommentMarkerPrefix starts the hidden marker in every comment LADO
// writes to an issue (PRD TASK-006). LADO never reads those comments back
// into a snapshot: they are copies of LADO's own records.
const LadoCommentMarkerPrefix = "<!-- lado-comment:"

// ByLado reports whether LADO wrote the comment.
func (c LadoIssueComment) ByLado() bool {
	return strings.Contains(c.Body, LadoCommentMarkerPrefix)
}

// LadoSnapshotComment is a comment as an issue snapshot pins it.
type LadoSnapshotComment struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	// CreatedAt is RFC 3339 in UTC, to the second.
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
}

// Limits on the comments a snapshot pins. When an issue has more, the oldest
// go first, since a plan written in a comment is usually among the newest.
const (
	LadoSnapshotMaxComments  = 100
	LadoSnapshotCommentBytes = 128 << 10
)

// LadoCommentSelection counts the comments a snapshot left out, by reason.
type LadoCommentSelection struct {
	// Untrusted comments came from authors who cannot work on the repository.
	Untrusted int
	// ByLado comments are LADO's own copies.
	ByLado int
	// Omitted trusted comments were over the snapshot's limits.
	Omitted int
}

// NewLadoIssueSnapshot pins an issue for a plan request: its canonical id,
// title and body, plus the trusted comments, oldest first
// (docs/LJY-PRD/TDD.md §5.1). An issue often describes the work in its body
// while the plan itself sits in a comment, so the orchestrator sees both.
// The same issue and comments always give the same snapshot, so a later read
// that hashes differently means the issue or a pinned comment changed.
func NewLadoIssueSnapshot(ref IssueID, title, body string, comments []LadoIssueComment) (LadoIssueSnapshot, LadoCommentSelection) {
	snapshot := LadoIssueSnapshot{ID: ref, Title: title, Body: body}
	var selection LadoCommentSelection
	kept := make([]LadoIssueComment, 0, len(comments))
	for _, comment := range comments {
		switch {
		case comment.ByLado():
			selection.ByLado++
		case !comment.AuthorTrusted:
			selection.Untrusted++
		default:
			kept = append(kept, comment)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i].CreatedAt.Unix(), kept[j].CreatedAt.Unix()
		if a != b {
			return a < b
		}
		return lessLadoCommentID(kept[i].ID, kept[j].ID)
	})
	if extra := len(kept) - LadoSnapshotMaxComments; extra > 0 {
		selection.Omitted += extra
		kept = kept[extra:]
	}
	size := 0
	for _, comment := range kept {
		size += len(comment.Body)
	}
	for len(kept) > 0 && size > LadoSnapshotCommentBytes {
		size -= len(kept[0].Body)
		kept = kept[1:]
		selection.Omitted++
	}
	for _, comment := range kept {
		snapshot.Comments = append(snapshot.Comments, LadoSnapshotComment{
			ID:        comment.ID,
			Author:    comment.Author,
			CreatedAt: comment.CreatedAt.UTC().Format(time.RFC3339),
			Body:      comment.Body,
		})
	}
	snapshot.OmittedComments = selection.Omitted
	return snapshot, selection
}

// lessLadoCommentID orders numeric ids by value and others as strings.
func lessLadoCommentID(a, b string) bool {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	if errA == nil && errB == nil {
		return x < y
	}
	return a < b
}

// HasComment reports whether the snapshot pinned the comment.
func (s LadoIssueSnapshot) HasComment(id string) bool {
	for _, comment := range s.Comments {
		if comment.ID == id {
			return true
		}
	}
	return false
}

// LadoPlanRequestID identifies a plan request.
type LadoPlanRequestID string

// LadoPlanRevisionID identifies a plan revision.
type LadoPlanRevisionID string

// LadoPlanApprovalID identifies a plan approval.
type LadoPlanApprovalID string

// LadoPlanPath is where the orchestrator writes a task's plan, relative to
// its artifact directory.
func LadoPlanPath(task LadoTaskID) string {
	return "lado/" + string(task) + "/plan.md"
}

// LadoIssueFilePath is where LADO puts the pinned issue snapshot for the
// orchestrator to read, relative to its artifact directory. The file is a
// copy; LADO's blob store keeps the pinned original.
func LadoIssueFilePath(task LadoTaskID) string {
	return "lado/" + string(task) + "/issue.md"
}

// LadoPlanRequest is one request for a plan or a changed plan
// (docs/LJY-PRD/TDD.md §5.1): to a plan worker, or, before plan workers, to
// the project's orchestrator.
type LadoPlanRequest struct {
	ID     LadoPlanRequestID
	TaskID LadoTaskID
	// Kind says who writes the plan.
	Kind LadoPlanRequestKind
	// SessionID is the session whose artifact directory receives the plan:
	// the orchestrator asked, or the plan worker's designer. A plan worker
	// request holds LadoPlanRequestPlaceholder until its designer starts.
	SessionID SessionID
	// IssueSnapshotSHA256 is the issue snapshot pinned for this request.
	// Every revision written for the request records it.
	IssueSnapshotSHA256 string
	// SourceCommentID is the comment the developer named as the plan, if any.
	SourceCommentID string
	// Note is what the developer asked to change, for a changed plan.
	Note string
	// BasedOn is the revision a changed plan starts from.
	BasedOn LadoPlanRevisionID
	// RequestedAt is when the developer asked.
	RequestedAt time.Time
	// DeliveredAt is when the orchestrator took the message. A new
	// orchestrator may still be starting, so delivery is retried.
	DeliveredAt      time.Time
	DeliveryAttempts int
	DeliveryError    string
	// FailedAt is when LADO stopped trying to deliver the request, or when a
	// plan worker request failed; DeliveryError then holds the details.
	FailedAt time.Time

	// The plan worker facts (plan_worker.go); zero for a request to an
	// orchestrator. WaitingReason says why the designer has not started yet.
	WaitingReason string
	// WorkerAttempts counts the designer's spawns.
	WorkerAttempts  int
	WorkerStartedAt time.Time
	// DraftSHA256 is the draft's blob; the draft is never a revision.
	DraftSHA256      string
	DraftSubmittedAt time.Time
	// FeedbackSentAt is when LADO sent the designer the critique's findings,
	// the reason it skipped the review, or that the review failed.
	FeedbackSentAt time.Time
	// FinalSHA256 is the plan the designer last submitted, which LADO wrote
	// into its artifact directory and pins.
	FinalSHA256 string
	SubmittedAt time.Time
	// FixRequestedAt is when LADO sent a pinned revision's problems back,
	// once per request.
	FixRequestedAt time.Time
	// RemindedAt is when LADO last reminded the designer to submit after its
	// turn ended without the submission.
	RemindedAt time.Time
	// CleanedAt is when LADO stopped the request's sessions after pinning.
	CleanedAt   time.Time
	FailureCode string
}

// LadoPlanRevision is one pinned version of a task's plan file
// (docs/LJY-PRD/TDD.md §3.1). LADO copies the bytes into its own blob store,
// so AO pruning or deleting artifact versions cannot remove an approved plan.
type LadoPlanRevision struct {
	ID        LadoPlanRevisionID
	TaskID    LadoTaskID
	RequestID LadoPlanRequestID
	// Number counts the task's revisions from 1.
	Number int
	// SessionID, Path and ArtifactVersion name the AO artifact version the
	// revision was pinned from.
	SessionID       SessionID
	Path            string
	ArtifactVersion int
	// Source is who wrote that version: "agent" or "user" (an edit in the
	// artifact viewer).
	Source string
	// SHA256 is the pinned blob's hash. An approval binds to it.
	SHA256  string
	BaseRef string
	// BaseSHA is the commit BaseRef named when LADO pinned the revision.
	BaseSHA string
	// IssueSnapshotSHA256 is the issue snapshot the plan was made from.
	IssueSnapshotSHA256 string
	// Problems lists what is wrong with the plan. A plan with problems
	// cannot be approved.
	Problems  []LadoPlanProblem
	CreatedAt time.Time
}

// Valid reports whether the revision can be approved.
func (r LadoPlanRevision) Valid() bool {
	return len(r.Problems) == 0
}

// LadoPlanApproval is the human's approval of one exact plan revision
// (docs/LJY-PRD/TDD.md §5.2).
type LadoPlanApproval struct {
	ID         LadoPlanApprovalID
	RevisionID LadoPlanRevisionID
	TaskID     LadoTaskID
	// SHA256 is the revision's hash at approval.
	SHA256 string
	// ProofNonce is the human-presence proof the approval used
	// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
	ProofNonce string
	ApprovedAt time.Time
}

// LadoPlanProblem is one thing wrong with a plan file. Code is stable, so the
// app can show it in the developer's language; Field names the front matter
// key and Detail carries the offending value or the parser's message.
type LadoPlanProblem struct {
	Code   string `json:"code"`
	Field  string `json:"field,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Plan problem codes.
const (
	LadoPlanTooLarge             = "too_large"
	LadoPlanNotText              = "not_text"
	LadoPlanMissingFrontMatter   = "missing_front_matter"
	LadoPlanInvalidFrontMatter   = "invalid_front_matter"
	LadoPlanMissingField         = "missing_field"
	LadoPlanInvalidField         = "invalid_field"
	LadoPlanWrongTask            = "wrong_task"
	LadoPlanWrongIssue           = "wrong_issue"
	LadoPlanUnknownSourceComment = "unknown_source_comment"
	LadoPlanWrongSource          = "wrong_source"
	LadoPlanUnknownBaseRef       = "unknown_base_ref"
)

// LadoPlanMaxBytes caps a plan file LADO pins.
const LadoPlanMaxBytes = 1 << 20

// LadoPlanRisk is a plan's risk level.
type LadoPlanRisk string

// Plan risk levels.
const (
	LadoPlanRiskLow    LadoPlanRisk = "low"
	LadoPlanRiskMedium LadoPlanRisk = "medium"
	LadoPlanRiskHigh   LadoPlanRisk = "high"
)

// LadoPlanSourceIssue says the orchestrator wrote the plan from the issue.
// A plan converted from a comment names it as "comment:<id>".
const LadoPlanSourceIssue = "issue"

// LadoPlanSourceComment returns the plan_source value for a comment.
func LadoPlanSourceComment(id string) string {
	return "comment:" + id
}

// LadoPlan is a plan file: Markdown with YAML front matter
// (docs/LJY-PRD/TDD.md §5.1).
type LadoPlan struct {
	Task               string
	Issue              string
	BaseRef            string
	Steps              []string
	AcceptanceCriteria []string
	AffectedPaths      []string
	TestCommands       []string
	Risk               LadoPlanRisk
	PermissionCeiling  PermissionMode
	ReviewRequired     bool
	Reviewer           string
	// PlanSource is "issue", or "comment:<id>" for a plan converted from an
	// issue comment.
	PlanSource string
	// ChangeKinds is the optional change_kinds list: which of
	// LadoChangeKinds the work makes. It decides, with the paths, whether a
	// plan worker's draft is reviewed.
	ChangeKinds []string
	// Body is the Markdown after the front matter.
	Body string
}

// LadoPlanExpectations is what a plan must agree with: the task and issue it
// was requested for, the comments its snapshot pinned, and the comment the
// developer named as the plan.
type LadoPlanExpectations struct {
	Task          LadoTaskID
	Issue         IssueID
	Comments      map[string]bool
	SourceComment string
}

// ParseLadoPlan reads a plan file and lists its problems. It never fails: a
// file it cannot read is a revision with problems, which the developer sees
// and the orchestrator can fix.
func ParseLadoPlan(content []byte, want LadoPlanExpectations) (LadoPlan, []LadoPlanProblem) {
	if len(content) > LadoPlanMaxBytes {
		return LadoPlan{}, []LadoPlanProblem{{Code: LadoPlanTooLarge, Detail: strconv.Itoa(LadoPlanMaxBytes)}}
	}
	if !utf8.Valid(content) {
		return LadoPlan{}, []LadoPlanProblem{{Code: LadoPlanNotText}}
	}
	front, body, ok := SplitLadoFrontMatter(string(content))
	if !ok {
		return LadoPlan{}, []LadoPlanProblem{{Code: LadoPlanMissingFrontMatter}}
	}
	var fields map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		return LadoPlan{}, []LadoPlanProblem{{Code: LadoPlanInvalidFrontMatter, Detail: err.Error()}}
	}
	if fields == nil {
		return LadoPlan{}, []LadoPlanProblem{{Code: LadoPlanMissingFrontMatter}}
	}
	r := planReader{fields: fields}
	plan := LadoPlan{Body: strings.TrimSpace(body)}
	plan.Task = r.text("task")
	plan.Issue = r.text("issue")
	plan.BaseRef = r.text("base_ref")
	plan.Steps = r.list("steps", true)
	plan.AcceptanceCriteria = r.list("acceptance_criteria", true)
	plan.AffectedPaths = r.list("affected_paths", true)
	plan.TestCommands = r.list("test_commands", false)
	plan.Risk = LadoPlanRisk(r.text("risk"))
	plan.PermissionCeiling = PermissionMode(r.text("permission_ceiling"))
	plan.ReviewRequired, plan.Reviewer = r.review()
	plan.PlanSource = r.text("plan_source")
	plan.ChangeKinds = r.changeKinds()

	if plan.Task != "" && plan.Task != string(want.Task) {
		r.add(LadoPlanWrongTask, "task", plan.Task)
	}
	if plan.Issue != "" && plan.Issue != string(want.Issue) {
		r.add(LadoPlanWrongIssue, "issue", plan.Issue)
	}
	if plan.BaseRef != "" && !validLadoBaseRef(plan.BaseRef) {
		r.add(LadoPlanInvalidField, "base_ref", plan.BaseRef)
	}
	for _, glob := range plan.AffectedPaths {
		if !validLadoAffectedPath(glob) {
			r.add(LadoPlanInvalidField, "affected_paths", glob)
		}
	}
	switch plan.Risk {
	case "", LadoPlanRiskLow, LadoPlanRiskMedium, LadoPlanRiskHigh:
	default:
		r.add(LadoPlanInvalidField, "risk", string(plan.Risk))
	}
	switch plan.PermissionCeiling {
	case "", PermissionModeDefault, PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions:
	default:
		r.add(LadoPlanInvalidField, "permission_ceiling", string(plan.PermissionCeiling))
	}
	if source := plan.PlanSource; source != "" {
		comment, isComment := strings.CutPrefix(source, "comment:")
		switch {
		case source == LadoPlanSourceIssue:
		case isComment && want.Comments[comment]:
		case isComment:
			r.add(LadoPlanUnknownSourceComment, "plan_source", comment)
		default:
			r.add(LadoPlanInvalidField, "plan_source", source)
		}
		if want.SourceComment != "" && source != LadoPlanSourceComment(want.SourceComment) {
			r.add(LadoPlanWrongSource, "plan_source", LadoPlanSourceComment(want.SourceComment))
		}
	}
	return plan, r.problems
}

// SplitLadoFrontMatter splits "---\n<yaml>\n---\n<body>", the form of a plan
// file and of a skill's SKILL.md. A byte order mark and CRLF line ends are
// accepted.
func SplitLadoFrontMatter(content string) (front, body string, ok bool) {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	rest, found := strings.CutPrefix(content, "---\n")
	if !found {
		return "", "", false
	}
	if strings.HasPrefix(rest, "---\n") || rest == "---" {
		return "", strings.TrimPrefix(rest, "---"), true
	}
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if !strings.HasSuffix(rest, "\n---") {
			return "", "", false
		}
		return strings.TrimSuffix(rest, "\n---"), "", true
	}
	return rest[:end], rest[end+len("\n---\n"):], true
}

// planReader decodes front matter fields one at a time, so each bad field
// becomes its own problem.
type planReader struct {
	fields   map[string]yaml.Node
	problems []LadoPlanProblem
}

func (r *planReader) add(code, field, detail string) {
	r.problems = append(r.problems, LadoPlanProblem{Code: code, Field: field, Detail: detail})
}

func (r *planReader) node(key string) (*yaml.Node, bool) {
	node, ok := r.fields[key]
	if !ok || node.Tag == "!!null" {
		r.add(LadoPlanMissingField, key, "")
		return nil, false
	}
	return &node, true
}

// text reads a required, non-blank scalar.
func (r *planReader) text(key string) string {
	node, ok := r.node(key)
	if !ok {
		return ""
	}
	var value string
	if node.Kind != yaml.ScalarNode || node.Decode(&value) != nil {
		r.add(LadoPlanInvalidField, key, "")
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		r.add(LadoPlanMissingField, key, "")
	}
	return value
}

// list reads a required list of non-blank strings. nonEmpty says whether the
// list needs at least one entry.
func (r *planReader) list(key string, nonEmpty bool) []string {
	node, ok := r.node(key)
	if !ok {
		return nil
	}
	var values []string
	if node.Kind != yaml.SequenceNode || node.Decode(&values) != nil {
		r.add(LadoPlanInvalidField, key, "")
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			r.add(LadoPlanInvalidField, key, "")
			return nil
		}
		out = append(out, value)
	}
	if nonEmpty && len(out) == 0 {
		r.add(LadoPlanMissingField, key, "")
	}
	return out
}

// review reads the required review mapping: required (a bool) and an
// optional reviewer.
func (r *planReader) review() (bool, string) {
	node, ok := r.node("review")
	if !ok {
		return false, ""
	}
	var review struct {
		Required *bool  `yaml:"required"`
		Reviewer string `yaml:"reviewer"`
	}
	if node.Kind != yaml.MappingNode || node.Decode(&review) != nil {
		r.add(LadoPlanInvalidField, "review", "")
		return false, ""
	}
	if review.Required == nil {
		r.add(LadoPlanMissingField, "review.required", "")
		return false, ""
	}
	return *review.Required, strings.TrimSpace(review.Reviewer)
}

// changeKinds reads the optional change_kinds list: missing or null is none,
// and each entry must be one of LadoChangeKinds.
func (r *planReader) changeKinds() []string {
	node, ok := r.fields["change_kinds"]
	if !ok || node.Tag == "!!null" {
		return nil
	}
	var values []string
	if node.Kind != yaml.SequenceNode || node.Decode(&values) != nil {
		r.add(LadoPlanInvalidField, "change_kinds", "")
		return nil
	}
	kinds := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !slices.Contains(LadoChangeKinds, value) {
			r.add(LadoPlanInvalidField, "change_kinds", value)
			continue
		}
		if !slices.Contains(kinds, value) {
			kinds = append(kinds, value)
		}
	}
	return kinds
}

// validLadoBaseRef accepts a branch or tag name the way a plan names its
// base: no spaces, control characters, "..", or leading "-".
func validLadoBaseRef(ref string) bool {
	if strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") || strings.HasSuffix(ref, "/") {
		return false
	}
	for _, r := range ref {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("~^:?*[\\", r) {
			return false
		}
	}
	return true
}

// validLadoAffectedPath accepts a glob relative to the repository root that
// stays inside it.
func validLadoAffectedPath(glob string) bool {
	if strings.HasPrefix(glob, "/") || strings.Contains(glob, "\\") || strings.ContainsRune(glob, 0) {
		return false
	}
	if len(glob) >= 2 && glob[1] == ':' {
		return false
	}
	for _, segment := range strings.Split(glob, "/") {
		if segment == ".." {
			return false
		}
	}
	if _, err := path.Match(glob, ""); err != nil {
		return false
	}
	return true
}
