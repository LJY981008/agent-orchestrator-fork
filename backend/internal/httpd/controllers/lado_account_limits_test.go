package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeLadoAccountLimitService struct {
	limits  []domain.LadoAccountLimit
	limit   domain.LadoAccountLimit
	waiting int
	err     error
	gotKey  string
	gotNote string
	calls   int
}

func (f *fakeLadoAccountLimitService) AccountLimits(context.Context) ([]domain.LadoAccountLimit, error) {
	f.calls++
	return f.limits, f.err
}

func (f *fakeLadoAccountLimitService) ReleaseAccountLimit(_ context.Context, key, note string) (domain.LadoAccountLimit, int, error) {
	f.calls++
	f.gotKey, f.gotNote = key, note
	return f.limit, f.waiting, f.err
}

func TestLadoAPI_ListAccountLimits(t *testing.T) {
	detected := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	active := domain.LadoAccountLimit{
		AccountKey: "claude:device-claude", SessionID: "mer-7", Identity: "sha256:abc",
		ResetAt: detected.Add(5 * time.Hour), DetectedAt: detected, UpdatedAt: detected,
	}
	released := domain.LadoAccountLimit{
		AccountKey: "codex:work", ResetAt: detected.Add(5 * time.Hour), ResetGuessed: true,
		DetectedAt: detected, UpdatedAt: detected.Add(time.Minute),
		ReleasedAt: detected.Add(time.Minute), ReleaseReason: domain.LadoLimitReleaseAccountChanged, ReleasedWaiting: 3,
	}
	svc := &fakeLadoAccountLimitService{limits: []domain.LadoAccountLimit{active, released}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoAccountLimits: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/account-limits", "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	if strings.Contains(string(body), "sha256:") || strings.Contains(string(body), `"identity"`) {
		t.Fatalf("the response leaks the identity: %s", body)
	}
	got := decodeLadoJSON[controllers.LadoAccountLimitListResponse](t, body)
	releasedAt := detected.Add(time.Minute)
	want := controllers.LadoAccountLimitListResponse{Limits: []controllers.LadoAccountLimitResponse{
		{
			AccountKey: "claude:device-claude", Provider: "claude", AccountID: "device-claude", SessionID: "mer-7",
			IdentityKnown: true, Active: true, ResetAt: active.ResetAt, DetectedAt: detected, UpdatedAt: detected,
		},
		{
			AccountKey: "codex:work", Provider: "codex", AccountID: "work", Active: false,
			ResetAt: released.ResetAt, ResetGuessed: true, DetectedAt: detected, UpdatedAt: releasedAt,
			ReleasedAt: &releasedAt, ReleaseReason: "account_changed", ReleasedWaiting: 3,
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %+v\nwant %+v", got, want)
	}

	// An empty list is [] for the app, not null.
	svc.limits = nil
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/account-limits", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"limits":[]`) {
		t.Fatalf("empty GET = %d %s", status, body)
	}
}

func TestLadoAPI_ReleaseAccountLimit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	svc := &fakeLadoAccountLimitService{
		limit: domain.LadoAccountLimit{
			AccountKey: "claude:device-claude", Identity: "sha256:abc",
			ResetAt: now.Add(3 * time.Hour), DetectedAt: now.Add(-time.Hour), UpdatedAt: now,
			ReleasedAt: now, ReleaseReason: domain.LadoLimitReleaseManual, ReleasedWaiting: 2,
		},
		waiting: 2,
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoAccountLimits: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/account-limits/claude%3Adevice-claude/release",
		`{"reason":"  서브 계정으로 다시 로그인했어요  "}`)
	if status != http.StatusOK {
		t.Fatalf("POST = %d %s", status, body)
	}
	if svc.gotKey != "claude:device-claude" || svc.gotNote != "서브 계정으로 다시 로그인했어요" {
		t.Fatalf("service got key %q note %q", svc.gotKey, svc.gotNote)
	}
	got := decodeLadoJSON[controllers.ReleaseLadoAccountLimitResponse](t, body)
	if !got.Released || got.Waiting != 2 || got.Limit.Active || got.Limit.ReleaseReason != "manual" ||
		got.Limit.ReleasedAt == nil || !got.Limit.ReleasedAt.Equal(now) || !got.Limit.IdentityKnown {
		t.Fatalf("response = %+v", got)
	}

	// The ':' may also come as it is.
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/account-limits/codex:work/release", `{"reason":"switched"}`); status != http.StatusOK || svc.gotKey != "codex:work" {
		t.Fatalf("unescaped POST = %d %s, key %q", status, body, svc.gotKey)
	}
}

func TestLadoAPI_ReleaseAccountLimitErrors(t *testing.T) {
	long := strings.Repeat("가", 501)
	for name, tc := range map[string]struct {
		path, body string
		err        error
		status     int
		code       string
		calls      int
	}{
		"not found":          {"claude%3Adevice-claude", `{"reason":"x"}`, domain.ErrLadoAccountLimitNotFound, http.StatusNotFound, "LADO_ACCOUNT_LIMIT_NOT_FOUND", 1},
		"wrapped not found":  {"claude%3Adevice-claude", `{"reason":"x"}`, fmt.Errorf("release: %w", domain.ErrLadoAccountLimitNotFound), http.StatusNotFound, "LADO_ACCOUNT_LIMIT_NOT_FOUND", 1},
		"not active":         {"claude%3Adevice-claude", `{"reason":"x"}`, domain.ErrLadoAccountLimitNotActive, http.StatusConflict, "LADO_ACCOUNT_LIMIT_NOT_ACTIVE", 1},
		"other failure":      {"claude%3Adevice-claude", `{"reason":"x"}`, errors.New("disk"), http.StatusInternalServerError, "INTERNAL_ERROR", 1},
		"empty reason":       {"claude%3Adevice-claude", `{"reason":""}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"blank reason":       {"claude%3Adevice-claude", `{"reason":"   "}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"no reason":          {"claude%3Adevice-claude", `{}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"long reason":        {"claude%3Adevice-claude", `{"reason":"` + long + `"}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"unknown field":      {"claude%3Adevice-claude", `{"reason":"x","force":true}`, nil, http.StatusBadRequest, "INVALID_JSON", 0},
		"unknown provider":   {"gemini%3Adevice", `{"reason":"x"}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"no account":         {"claude%3A", `{"reason":"x"}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"slash in account":   {"claude%3Aa%2Fb", `{"reason":"x"}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
		"no provider prefix": {"device-claude", `{"reason":"x"}`, nil, http.StatusBadRequest, "INVALID_REQUEST", 0},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeLadoAccountLimitService{err: tc.err}
			srv := newLadoTestServer(t, httpd.APIDeps{LadoAccountLimits: svc})
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/account-limits/"+tc.path+"/release", tc.body)
			if status != tc.status || !strings.Contains(string(body), `"code":"`+tc.code+`"`) {
				t.Fatalf("POST = %d %s, want %d %s", status, body, tc.status, tc.code)
			}
			if svc.calls != tc.calls {
				t.Fatalf("service calls = %d, want %d", svc.calls, tc.calls)
			}
		})
	}

	// A 500 char reason is the longest taken.
	svc := &fakeLadoAccountLimitService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoAccountLimits: svc})
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/account-limits/claude%3Adevice-claude/release",
		`{"reason":"`+strings.Repeat("가", 500)+`"}`); status != http.StatusOK {
		t.Fatalf("500 chars = %d %s", status, body)
	}
}

func TestLadoAPI_AccountLimitsNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, req := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/account-limits", ""},
		{"POST", "/api/v1/lado/account-limits/claude%3Adevice-claude/release", `{"reason":"x"}`},
	} {
		if body, status, _ := doRequest(t, srv, req.method, req.path, req.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d %s", req.method, req.path, status, body)
		}
	}
}
