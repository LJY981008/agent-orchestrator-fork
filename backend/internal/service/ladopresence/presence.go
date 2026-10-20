// Package ladopresence checks the human-presence proofs that LADO's approval
// routes require (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
//
// The desktop app's main process shows a native confirmation naming the
// subject, then signs the request itself with a key both it and the daemon
// derive in memory from the per-launch desktop token. Any local process can
// call the loopback API, but only the app's main process holds the key, so an
// agent's mistaken `ao` or curl call cannot approve anything.
//
// Proof header:
//
//	X-LADO-Presence: v1.<nonce>.<expires>.<mac>
//
// nonce is 16 to 64 base64url characters, expires is Unix seconds at most
// 120 s ahead, and mac is the base64url (no padding) HMAC-SHA256, under
// Key(token), of
//
//	"lado-presence-v1\n" + method + "\n" + escaped path + "\n" +
//	hex(sha256(body)) + "\n" + nonce + "\n" + expires
//
// The body names the subject's hash, such as a plan's sha256, so a proof for
// one subject cannot approve another. The caller stores the nonce with the
// action it allowed, once, so a proof cannot be replayed.
package ladopresence

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Header carries the proof.
const Header = "X-LADO-Presence"

// MaxLifetime is how far ahead a proof may expire.
const MaxLifetime = 120 * time.Second

// clockSkew tolerates the app's and the daemon's clocks reading differently.
const clockSkew = 5 * time.Second

// keyLabel separates the presence key from the desktop token itself.
const keyLabel = "lado-presence-v1"

// Key derives the presence key from the desktop token.
func Key(token string) []byte {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(keyLabel))
	return mac.Sum(nil)
}

// Sign returns the proof header value for a request. The desktop app's main
// process computes the same value (frontend/src/main/lado-presence.ts).
func Sign(key []byte, method, path string, body []byte, nonce string, expires time.Time) string {
	unix := strconv.FormatInt(expires.Unix(), 10)
	return "v1." + nonce + "." + unix + "." + base64.RawURLEncoding.EncodeToString(signature(key, method, path, body, nonce, unix))
}

func signature(key []byte, method, path string, body []byte, nonce, expires string) []byte {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(keyLabel + "\n" + method + "\n" + path + "\n" + hex.EncodeToString(sum[:]) + "\n" + nonce + "\n" + expires))
	return mac.Sum(nil)
}

// Verifier checks proofs against the daemon's desktop token.
type Verifier struct {
	key []byte
	dev bool
	now func() time.Time
}

// New returns a verifier. fromApp says whether the daemon received its
// desktop token from the app: a daemon that made up its own token shares it
// with nobody, so no proof could be right, and approvals fail closed with a
// request to open the app. dev allows approvals without a proof, marked as
// such, for the renderer running in a browser during development
// (AO_LADO_DEV_PRESENCE=1).
func New(token string, fromApp, dev bool, now func() time.Time) *Verifier {
	if now == nil {
		now = time.Now
	}
	v := &Verifier{dev: dev, now: now}
	if fromApp && token != "" {
		v.key = Key(token)
	}
	return v
}

// Verify checks the proof for a request and returns it, ready to be stored
// with the action it allows. path is the request's escaped path, as the app
// signs it.
func (v *Verifier) Verify(header, method, path string, body []byte, action, subject string) (domain.LadoPresenceProof, error) {
	now := v.now().UTC()
	header = strings.TrimSpace(header)
	if header == "" {
		if v.dev {
			return devProof(action, subject, now), nil
		}
		if v.key == nil {
			return domain.LadoPresenceProof{}, unavailable()
		}
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Approve this in the AO desktop app. It asks you to confirm, then sends the approval itself.")
	}
	if v.key == nil {
		return domain.LadoPresenceProof{}, unavailable()
	}
	parts := strings.Split(header, ".")
	if len(parts) != 4 || parts[0] != "v1" || !validNonce(parts[1]) {
		return domain.LadoPresenceProof{}, invalid()
	}
	nonce, expiresText, macText := parts[1], parts[2], parts[3]
	unix, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || strconv.FormatInt(unix, 10) != expiresText {
		return domain.LadoPresenceProof{}, invalid()
	}
	expires := time.Unix(unix, 0).UTC()
	if !expires.After(now) || expires.After(now.Add(MaxLifetime+clockSkew)) {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_EXPIRED",
			"The confirmation expired. Approve again in the AO desktop app.")
	}
	got, err := base64.RawURLEncoding.DecodeString(macText)
	if err != nil || !hmac.Equal(got, signature(v.key, method, path, body, nonce, expiresText)) {
		return domain.LadoPresenceProof{}, invalid()
	}
	return domain.LadoPresenceProof{
		Nonce:     nonce,
		Action:    action,
		SubjectID: subject,
		ExpiresAt: expires,
		UsedAt:    now,
	}, nil
}

// Replayed is the error for a proof whose nonce was used before.
func Replayed() error {
	return apierr.Forbidden("LADO_PRESENCE_REPLAYED", "This confirmation was already used. Approve again in the AO desktop app.")
}

func unavailable() error {
	return apierr.Forbidden("LADO_PRESENCE_UNAVAILABLE",
		"Approvals need the AO desktop app. Open the app, which restarts the daemon with its own token, then approve there.")
}

func invalid() error {
	return apierr.Forbidden("LADO_PRESENCE_INVALID", "The confirmation could not be verified. Approve again in the AO desktop app.")
}

func validNonce(nonce string) bool {
	if len(nonce) < 16 || len(nonce) > 64 {
		return false
	}
	for _, r := range nonce {
		if !isBase64URLChar(r) {
			return false
		}
	}
	return true
}

func isBase64URLChar(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// devProof stands in for a proof when development presence is on. Its nonce
// is random, so storing it still works, and Dev marks the action.
func devProof(action, subject string, now time.Time) domain.LadoPresenceProof {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return domain.LadoPresenceProof{
		Nonce:     "dev-" + base64.RawURLEncoding.EncodeToString(b[:]),
		Action:    action,
		SubjectID: subject,
		Dev:       true,
		ExpiresAt: now,
		UsedAt:    now,
	}
}
