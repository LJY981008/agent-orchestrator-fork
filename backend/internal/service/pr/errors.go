package pr

import (
	"errors"
	"strings"
)

// Sentinel errors returned by the PR action service.
var (
	ErrInvalidPR        = errors.New("pr: invalid identity")
	ErrPRNotFound       = errors.New("pr: not found")
	ErrPRNotMergeable   = errors.New("pr: not mergeable")
	ErrPRHeadChanged    = errors.New("pr: head changed")
	ErrPRPreconditions  = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve = errors.New("pr: nothing to resolve")
)

// MergeBlockedError is the refusal of a merge whose live read found
// blockers (LADO fork, #21): the names of domain.MergeBlocker*. It matches
// ErrPRPreconditions, and the merge route returns Blockers in its details.
type MergeBlockedError struct {
	Blockers []string
}

func (e *MergeBlockedError) Error() string {
	return ErrPRPreconditions.Error() + ": " + strings.Join(e.Blockers, ", ")
}

func (e *MergeBlockedError) Unwrap() error { return ErrPRPreconditions }
