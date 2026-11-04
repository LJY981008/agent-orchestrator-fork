package domain

import "testing"

// TestCanonicalRepositoryValidationIgnoresSSHPort covers self-managed GitLab
// checkouts cloned over an SSH port that differs from the web port: the SSH
// port is not part of the web identity, so only host names are compared.
func TestCanonicalRepositoryValidationIgnoresSSHPort(t *testing.T) {
	for _, tc := range []struct {
		name, origin, canonical string
		valid                   bool
	}{
		{"ssh port vs https host", "ssh://git@host:30022/alice/repo.git", "https://host/group/repo", true},
		{"ssh port vs https web port", "ssh://git@host:30022/alice/repo.git", "https://host:8443/group/repo", true},
		{"ssh port case insensitive", "ssh://git@Host:30022/alice/repo.git", "https://host/group/repo", true},
		{"scp vs https web port", "git@host:alice/repo.git", "https://host:8443/group/repo", true},
		{"ssh port other host", "ssh://git@other:30022/alice/repo.git", "https://host/group/repo", false},
		{"scp other host", "git@other:alice/repo.git", "https://host/group/repo", false},
		{"ssh port cross provider", "ssh://git@github.com:2222/alice/repo.git", "https://gitlab.com/group/repo", false},
		{"https checkout port still compared", "https://host:8443/alice/repo.git", "https://host/group/repo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProjectConfig{CanonicalRepoURL: tc.canonical}
			if err := cfg.ValidateCanonicalRepository(tc.origin); (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid=%v", err, tc.valid)
			}
		})
	}
}
