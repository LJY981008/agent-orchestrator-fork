package agentaccounts

// LADO fork (issue #92): the fingerprint of the login an account is signed in
// to, so LADO can tell that a key at its usage limit now runs on another
// login.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// fingerprintPrefix marks the hash a fingerprint is.
const fingerprintPrefix = "sha256:"

// AccountFingerprint reads, fresh and past the cache, who accountID (the
// computer's own account included) is signed in to, and returns
// "sha256:<hex>" of the provider, a NUL and the login's lower-cased email.
// It returns "" when the account is signed out or its login has no email (a
// long-lived token): LADO cannot tell that login from another. The email
// itself is neither kept nor logged.
func (s *Service) AccountFingerprint(ctx context.Context, accountID string) (string, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return "", err
	}
	provider := s.providers[account.Provider]
	if provider == nil {
		return "", fmt.Errorf("no %s CLI for account %s", account.Provider, accountID)
	}
	identity, err := provider.Identity(ctx, account.Home)
	if err != nil {
		return "", fmt.Errorf("read who account %s is signed in to: %w", accountID, err)
	}
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	if (!identity.LoggedIn && !identity.SignedIn) || email == "" {
		return "", nil
	}
	sum := sha256.Sum256([]byte(string(account.Provider) + "\x00" + email))
	return fingerprintPrefix + hex.EncodeToString(sum[:]), nil
}
