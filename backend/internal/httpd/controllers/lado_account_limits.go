package controllers

// LADO fork: the account usage limits LADO holds new workers for, and the
// route a person releases one through after logging in to another account
// (issue #92). Like the rest of /api/v1/lado, they serve the loopback listener
// only. The daemon's ladowatch service records the release, its audit event
// and the orchestrator notices.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// LadoAccountLimitService lists the account limits and releases one by hand.
// *ladowatch.Service implements it. ReleaseAccountLimit returns
// domain.ErrLadoAccountLimitNotFound for a key LADO has no limit of and
// domain.ErrLadoAccountLimitNotActive for one already released or past its
// reset, and the number of account_limited waits LADO checks again.
type LadoAccountLimitService interface {
	AccountLimits(ctx context.Context) ([]domain.LadoAccountLimit, error)
	ReleaseAccountLimit(ctx context.Context, key, note string) (domain.LadoAccountLimit, int, error)
}

const (
	ladoAccountLimitsPath       = "/api/v1/lado/account-limits"
	ladoAccountLimitReleasePath = "/api/v1/lado/account-limits/{accountKey}/release"
	// maxLadoAccountLimitReason is the longest release reason, in characters.
	maxLadoAccountLimitReason = 500
)

func (c *LadoController) registerAccountLimits(r chi.Router) {
	r.Get("/lado/account-limits", c.listAccountLimits)
	r.Post("/lado/account-limits/{accountKey}/release", c.releaseAccountLimit)
}

func (c *LadoController) listAccountLimits(w http.ResponseWriter, r *http.Request) {
	if c.AccountLimits == nil {
		apispec.NotImplemented(w, r, "GET", ladoAccountLimitsPath)
		return
	}
	limits, err := c.AccountLimits.AccountLimits(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	now := time.Now()
	resp := LadoAccountLimitListResponse{Limits: make([]LadoAccountLimitResponse, 0, len(limits))}
	for _, limit := range limits {
		resp.Limits = append(resp.Limits, ladoAccountLimitResponse(limit, now))
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *LadoController) releaseAccountLimit(w http.ResponseWriter, r *http.Request) {
	if c.AccountLimits == nil {
		apispec.NotImplemented(w, r, "POST", ladoAccountLimitReleasePath)
		return
	}
	// The key holds a ':', which clients send as %3A; chi hands the raw
	// segment over.
	key, err := url.PathUnescape(chi.URLParam(r, "accountKey"))
	if err == nil {
		if _, _, ok := domain.ParseLadoAccountKey(key); !ok {
			err = errors.New("not an account key")
		}
	}
	if err != nil {
		writeLadoInvalidRequest(w, r, "accountKey", "accountKey must be <provider>:<account id>, such as claude:device-claude")
		return
	}
	var req ReleaseLadoAccountLimitRequest
	if !decodeLadoBody(w, r, maxLadoSmallBody, &req) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxLadoAccountLimitReason {
		writeLadoInvalidRequest(w, r, "reason", "reason must be 1 to 500 characters")
		return
	}
	limit, waiting, err := c.AccountLimits.ReleaseAccountLimit(r.Context(), key, reason)
	switch {
	case errors.Is(err, domain.ErrLadoAccountLimitNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LADO_ACCOUNT_LIMIT_NOT_FOUND", "LADO has no usage limit of this account", nil)
		return
	case errors.Is(err, domain.ErrLadoAccountLimitNotActive):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "LADO_ACCOUNT_LIMIT_NOT_ACTIVE", "The account's usage limit was already released or its reset passed", nil)
		return
	case err != nil:
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseLadoAccountLimitResponse{
		Limit:    ladoAccountLimitResponse(limit, time.Now()),
		Released: true,
		Waiting:  waiting,
	})
}

func writeLadoInvalidRequest(w http.ResponseWriter, r *http.Request, field, message string) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_REQUEST", message, map[string]any{"field": field})
}

func ladoAccountLimitResponse(l domain.LadoAccountLimit, now time.Time) LadoAccountLimitResponse {
	provider, accountID, _ := domain.ParseLadoAccountKey(l.AccountKey)
	return LadoAccountLimitResponse{
		AccountKey:      l.AccountKey,
		Provider:        string(provider),
		AccountID:       accountID,
		SessionID:       string(l.SessionID),
		IdentityKnown:   l.Identity != "",
		Active:          l.Active(now),
		ResetAt:         l.ResetAt.UTC(),
		ResetGuessed:    l.ResetGuessed,
		DetectedAt:      l.DetectedAt.UTC(),
		UpdatedAt:       l.UpdatedAt.UTC(),
		ReleasedAt:      ladoOptionalUTC(l.ReleasedAt),
		ReleaseReason:   string(l.ReleaseReason),
		ReleasedWaiting: l.ReleasedWaiting,
	}
}
