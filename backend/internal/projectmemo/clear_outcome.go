package projectmemo

import "time"

// ClearStatus is what happened to the project memo during a /clear.
type ClearStatus string

const (
	// ClearSaved means the orchestrator wrote a memo and AO saved it.
	ClearSaved ClearStatus = "saved"
	// ClearFailed means the memo step failed; the conversation was still
	// cleared and the earlier memo is unchanged.
	ClearFailed ClearStatus = "failed"
	// ClearSkipped means /clear had no memo step: a worker session, or an
	// orchestrator with nothing to hand off.
	ClearSkipped ClearStatus = "skipped"
)

// ClearOutcome reports the memo step of one /clear. Code and Message are set
// when Status is ClearFailed; UpdatedAt and Revision when it is ClearSaved.
type ClearOutcome struct {
	Status    ClearStatus
	Code      string
	Message   string
	UpdatedAt time.Time
	Revision  string
}

// SavedOutcome reports a memo saved by /clear.
func SavedOutcome(m Memo) ClearOutcome {
	return ClearOutcome{Status: ClearSaved, UpdatedAt: m.UpdatedAt, Revision: m.Revision}
}

// FailedOutcome reports a memo step that failed with code and message.
func FailedOutcome(code, message string) ClearOutcome {
	return ClearOutcome{Status: ClearFailed, Code: code, Message: message}
}

// SkippedOutcome reports a /clear with no memo step.
func SkippedOutcome() ClearOutcome { return ClearOutcome{Status: ClearSkipped} }
