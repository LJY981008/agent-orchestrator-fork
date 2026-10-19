package domain

import (
	"encoding/json"
	"time"
)

// LadoAuditEvent is one entry of the LADO audit log (docs/LJY-PRD/TDD.md §4.4).
// The log is append-only and never pruned. PrevSHA256 chains each entry to the
// one before it, so an edit to any entry with a successor is detectable.
type LadoAuditEvent struct {
	ID          int64           `json:"id"`
	At          time.Time       `json:"at"`
	Actor       string          `json:"actor"`
	Action      string          `json:"action"`
	SubjectKind string          `json:"subjectKind"`
	SubjectID   string          `json:"subjectId"`
	Payload     json.RawMessage `json:"payload"`
	PrevSHA256  string          `json:"prevSha256"`
}
