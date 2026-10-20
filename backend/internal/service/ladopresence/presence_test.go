package ladopresence_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

const (
	token  = "desktop-token-from-the-app"
	nonce  = "AAAAAAAAAAAAAAAAAAAAAA"
	path   = "/api/v1/lado/plans/lado-plan-1/approve"
	action = domain.LadoPresencePlanApprove
)

var (
	now  = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	body = []byte(`{"sha256":"` + strings.Repeat("a", 64) + `"}`)
)

func verifier(fromApp, dev bool) *ladopresence.Verifier {
	return ladopresence.New(token, fromApp, dev, func() time.Time { return now })
}

func code(t *testing.T, err error) string {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not an API error", err)
	}
	if apiErr.Kind != apierr.KindForbidden {
		t.Fatalf("error %v has kind %v, want forbidden", err, apiErr.Kind)
	}
	return apiErr.Code
}

func TestKeyIsTheTokensHMACOfTheLabel(t *testing.T) {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("lado-presence-v1"))
	if !hmac.Equal(ladopresence.Key(token), mac.Sum(nil)) {
		t.Fatal("Key must be HMAC-SHA256(token, \"lado-presence-v1\"), as the desktop app computes it")
	}
}

func TestVerifyAcceptsTheAppsProof(t *testing.T) {
	header := ladopresence.Sign(ladopresence.Key(token), "POST", path, body, nonce, now.Add(60*time.Second))
	proof, err := verifier(true, false).Verify(header, "POST", path, body, action, "lado-plan-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if proof.Nonce != nonce || proof.Action != action || proof.SubjectID != "lado-plan-1" || proof.Dev ||
		!proof.ExpiresAt.Equal(now.Add(60*time.Second)) || !proof.UsedAt.Equal(now) {
		t.Fatalf("proof = %+v", proof)
	}
}

// Gate 10: no proof, a wrong signature, an expired proof, or a daemon whose
// token did not come from the app are all refused.
func TestVerifyRefusesBadProofs(t *testing.T) {
	key := ladopresence.Key(token)
	good := func(expires time.Time) string { return ladopresence.Sign(key, "POST", path, body, nonce, expires) }
	for _, tc := range []struct {
		name   string
		v      *ladopresence.Verifier
		header string
		method string
		path   string
		body   []byte
		code   string
	}{
		{"no proof", verifier(true, false), "", "POST", path, body, "LADO_PRESENCE_REQUIRED"},
		{"curl without the app", verifier(false, false), "", "POST", path, body, "LADO_PRESENCE_UNAVAILABLE"},
		{"a daemon with its own token", verifier(false, false), good(now.Add(time.Minute)), "POST", path, body, "LADO_PRESENCE_UNAVAILABLE"},
		{"wrong key", verifier(true, false), ladopresence.Sign(ladopresence.Key("other"), "POST", path, body, nonce, now.Add(time.Minute)), "POST", path, body, "LADO_PRESENCE_INVALID"},
		{"another subject's body", verifier(true, false), good(now.Add(time.Minute)), "POST", path, []byte(`{"sha256":"` + strings.Repeat("b", 64) + `"}`), "LADO_PRESENCE_INVALID"},
		{"another revision's path", verifier(true, false), good(now.Add(time.Minute)), "POST", "/api/v1/lado/plans/lado-plan-2/approve", body, "LADO_PRESENCE_INVALID"},
		{"another method", verifier(true, false), good(now.Add(time.Minute)), "PUT", path, body, "LADO_PRESENCE_INVALID"},
		{"expired", verifier(true, false), good(now.Add(-time.Second)), "POST", path, body, "LADO_PRESENCE_EXPIRED"},
		{"expiring too far ahead", verifier(true, false), good(now.Add(10 * time.Minute)), "POST", path, body, "LADO_PRESENCE_EXPIRED"},
		{"malformed", verifier(true, false), "v1.short.123.abc", "POST", path, body, "LADO_PRESENCE_INVALID"},
		{"other version", verifier(true, false), strings.Replace(good(now.Add(time.Minute)), "v1.", "v2.", 1), "POST", path, body, "LADO_PRESENCE_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.v.Verify(tc.header, tc.method, tc.path, tc.body, action, "lado-plan-1")
			if err == nil {
				t.Fatal("Verify accepted the proof")
			}
			if got := code(t, err); got != tc.code {
				t.Fatalf("code = %s, want %s", got, tc.code)
			}
		})
	}
}

func TestDevPresenceAllowsApprovalsWithoutAProofAndSaysSo(t *testing.T) {
	proof, err := verifier(false, true).Verify("", "POST", path, body, action, "lado-plan-1")
	if err != nil {
		t.Fatalf("Verify with dev presence: %v", err)
	}
	if !proof.Dev || !strings.HasPrefix(proof.Nonce, "dev-") || proof.SubjectID != "lado-plan-1" {
		t.Fatalf("dev proof = %+v", proof)
	}
	other, _ := verifier(false, true).Verify("", "POST", path, body, action, "lado-plan-1")
	if other.Nonce == proof.Nonce {
		t.Fatal("two dev proofs share a nonce")
	}
	if got := code(t, ladopresence.Replayed()); got != "LADO_PRESENCE_REPLAYED" {
		t.Fatalf("Replayed code = %s", got)
	}
}

// TestSignMatchesTheDesktopApp pins the proof format to a fixed vector. The
// desktop app's test (frontend/src/main/lado-presence.test.ts) checks the
// same vector, so the two signers cannot drift apart.
func TestSignMatchesTheDesktopApp(t *testing.T) {
	body := []byte(`{"sha256":"` + strings.Repeat("a", 64) + `"}`)
	got := ladopresence.Sign(ladopresence.Key("desktop-token"), "POST", "/api/v1/lado/plans/lado-plan-1/approve", body, "AAAAAAAAAAAAAAAAAAAAAA", time.Unix(1792400000, 0))
	const want = "v1.AAAAAAAAAAAAAAAAAAAAAA.1792400000.eh5sJcCnuuZHmMIBJ7sPF9UVbNtH_31e7ODo_8twalk"
	if got != want {
		t.Fatalf("Sign = %s\nwant %s", got, want)
	}
}
