package domain

import (
	"errors"
	"time"
)

// LadoPresenceProof is a human-presence proof as LADO records it once used
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). Each nonce is
// stored once, so a proof cannot be replayed, also not after a restart.
type LadoPresenceProof struct {
	Nonce string
	// Action and SubjectID say what the proof was used for, such as
	// LadoPresencePlanApprove and a revision id.
	Action    string
	SubjectID string
	// Dev marks an action taken without presence because the daemon runs with
	// AO_LADO_DEV_PRESENCE=1, for the renderer in a browser during
	// development.
	Dev       bool
	ExpiresAt time.Time
	UsedAt    time.Time
}

// Actions that need human presence in step 4b.
const (
	LadoPresencePlanApprove = "plan.approve"
	LadoPresenceModeOff     = "project.lado_mode_off"
)

// ErrLadoPresenceReplayed means the proof's nonce was already used.
var ErrLadoPresenceReplayed = errors.New("the presence proof was already used")

// ErrLadoPlanSuperseded means a newer revision replaced the one being
// approved, or its bytes changed.
var ErrLadoPlanSuperseded = errors.New("the plan revision is no longer the task's latest")
