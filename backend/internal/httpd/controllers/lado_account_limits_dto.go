package controllers

// LADO fork: DTOs of the account usage limits LADO holds new workers for, and
// of releasing one by hand (issue #92). Times are RFC 3339. No DTO carries the
// account's identity: only whether LADO knows it.

import "time"

// LadoAccountKeyParam names an account limit by its key.
type LadoAccountKeyParam struct {
	AccountKey string `path:"accountKey" description:"The account key, <provider>:<account id> such as claude:device-claude (the ':' may be sent as %3A)."`
}

// LadoAccountLimitResponse is one account limit LADO recorded, held or
// released.
type LadoAccountLimitResponse struct {
	AccountKey      string     `json:"accountKey" description:"<provider>:<account id>; device-claude and device-codex are the device's own login."`
	Provider        string     `json:"provider" enum:"claude,codex"`
	AccountID       string     `json:"accountId"`
	SessionID       string     `json:"sessionId,omitempty" description:"The session LADO saw the limit in."`
	IdentityKnown   bool       `json:"identityKnown" description:"LADO recorded which login hit the limit, so it can tell when the key is logged in to another account."`
	Active          bool       `json:"active" description:"The limit still holds: it was not released and its reset has not come. New workers on the account wait while it holds."`
	ResetAt         time.Time  `json:"resetAt"`
	ResetGuessed    bool       `json:"resetGuessed" description:"LADO guessed the reset time; the limit message did not say it."`
	DetectedAt      time.Time  `json:"detectedAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ReleasedAt      *time.Time `json:"releasedAt,omitempty" description:"When LADO released the limit; omitted while it holds."`
	ReleaseReason   string     `json:"releaseReason,omitempty" enum:"reset,account_changed,manual,turn_completed" description:"Why LADO released the limit."`
	ReleasedWaiting int        `json:"releasedWaiting" description:"How many account_limited waits there were when LADO released the limit."`
}

// LadoAccountLimitListResponse is every account limit LADO recorded.
type LadoAccountLimitListResponse struct {
	Limits []LadoAccountLimitResponse `json:"limits"`
}

// ReleaseLadoAccountLimitRequest releases an account limit by hand.
type ReleaseLadoAccountLimitRequest struct {
	Reason string `json:"reason" description:"Why the limit no longer holds, such as the developer logging in to another account; 1 to 500 characters, kept in the audit log."`
}

// ReleaseLadoAccountLimitResponse is the released limit.
type ReleaseLadoAccountLimitResponse struct {
	Limit    LadoAccountLimitResponse `json:"limit"`
	Released bool                     `json:"released" description:"LADO released the limit."`
	Waiting  int                      `json:"waiting" description:"How many account_limited waits LADO checks again now."`
}
