package agentaccounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountFingerprint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	saved := domain.AgentAccount{ID: "acct-1", Provider: domain.AgentAccountProviderClaude, Home: root + "/claude/acct-1", Email: "sub@example.com"}
	claude := &fakeClaude{
		signedInAs: map[string]string{"": "Main@Example.com", saved.Home: "sub@example.com"},
		apiKey:     map[string]bool{},
		failing:    map[string]bool{},
	}
	store := &memStore{accounts: []domain.AgentAccount{saved}, defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	device := domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)

	want := func(email string) string {
		sum := sha256.Sum256([]byte("claude\x00" + email))
		return "sha256:" + hex.EncodeToString(sum[:])
	}

	got, err := svc.AccountFingerprint(ctx, device)
	if err != nil || got != want("main@example.com") {
		t.Fatalf("device fingerprint = %q, %v; want the hash of the lower-cased email", got, err)
	}
	if strings.Contains(got, "example") {
		t.Fatalf("fingerprint %q holds the email", got)
	}
	sub, err := svc.AccountFingerprint(ctx, saved.ID)
	if err != nil || sub != want("sub@example.com") || sub == got {
		t.Fatalf("saved account fingerprint = %q, %v", sub, err)
	}

	// Another login on the device: another fingerprint, read past the cache.
	claude.signedInAs[""] = "other@example.com"
	if again, err := svc.AccountFingerprint(ctx, device); err != nil || again != want("other@example.com") {
		t.Fatalf("after the switch fingerprint = %q, %v", again, err)
	}

	// No email (a token login) and signed out are unknown, not an error.
	claude.apiKey[""] = true
	if fp, err := svc.AccountFingerprint(ctx, device); err != nil || fp != "" {
		t.Fatalf("token login fingerprint = %q, %v; want empty", fp, err)
	}
	delete(claude.apiKey, "")
	delete(claude.signedInAs, "")
	if fp, err := svc.AccountFingerprint(ctx, device); err != nil || fp != "" {
		t.Fatalf("signed out fingerprint = %q, %v; want empty", fp, err)
	}

	// A failed check is an error; an unknown account too.
	claude.failing[saved.Home] = true
	if fp, err := svc.AccountFingerprint(ctx, saved.ID); err == nil || fp != "" {
		t.Fatalf("failed check fingerprint = %q, %v; want an error", fp, err)
	}
	if _, err := svc.AccountFingerprint(ctx, "missing"); !errors.Is(err, ports.ErrAgentAccountUnknown) {
		t.Fatalf("unknown account err = %v", err)
	}
	if _, err := svc.AccountFingerprint(ctx, domain.DeviceAgentAccountID(domain.AgentAccountProviderCodex)); err == nil {
		t.Fatal("an account without its provider's CLI must fail")
	}
}
