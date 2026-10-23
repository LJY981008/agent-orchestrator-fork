package domain

import (
	"errors"
	"time"
)

// LadoPolicySnapshotID identifies a rule snapshot.
type LadoPolicySnapshotID string

// ErrLadoRulesChanged reports that a rule version an approval's snapshot
// used was deleted after the rules were resolved for the approval.
var ErrLadoRulesChanged = errors.New("a rule version the snapshot used no longer exists")

// LadoProblemSeverity says what a policy problem does to a task.
type LadoProblemSeverity string

// Problem severities. A blocking problem blocks the task; a warning and info
// are only shown.
const (
	LadoSeverityBlocking LadoProblemSeverity = "blocking"
	LadoSeverityWarning  LadoProblemSeverity = "warning"
	LadoSeverityInfo     LadoProblemSeverity = "info"
)

// LadoPolicyProblem is one thing rule resolution found, such as an agent
// that cannot honor a rule or two rules that exclude each other
// (docs/LJY-PRD/HARNESS.md §7). Code is stable, so the app can show it in
// the developer's language; Rules names the rules involved and Details
// carries the values.
type LadoPolicyProblem struct {
	Code     string              `json:"code"`
	Severity LadoProblemSeverity `json:"severity"`
	Rules    []LadoRuleID        `json:"rules,omitempty"`
	Details  map[string]string   `json:"details,omitempty"`
}

// LadoPolicySnapshotRule is one rule version a snapshot used. A rule with a
// used version is never deleted.
type LadoPolicySnapshotRule struct {
	RuleID    LadoRuleID
	VersionID LadoRuleVersionID
}

// LadoPolicySnapshot is the rules that applied to one plan approval, fixed in
// the approval's transaction (docs/LJY-PRD/HARNESS.md §8,
// docs/LJY-PRD/TDD.md §5.2). Content is the canonical JSON, and the approval
// signs its sha256 together with the plan's.
type LadoPolicySnapshot struct {
	ID         LadoPolicySnapshotID
	ApprovalID LadoPlanApprovalID
	SHA256     string
	Content    []byte
	// Blocking reports whether the snapshot holds a blocking problem. A task
	// approved with one is Blocked.
	Blocking  bool
	Rules     []LadoPolicySnapshotRule
	CreatedAt time.Time
}

// LadoPolicySnapshotRef is what a task's state needs to know about the rule
// snapshot of its newest approval.
type LadoPolicySnapshotRef struct {
	ID       LadoPolicySnapshotID
	SHA256   string
	Blocking bool
}
