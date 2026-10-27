package agentcreds

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// LADO fork: a session on a LADO config home keeps its login where
// CLAUDE_SECURESTORAGE_CONFIG_DIR names it; an empty value means ~/.claude.
func TestCredentialsFileFollowsSecureStorageDir(t *testing.T) {
	home, login, user := t.TempDir(), t.TempDir(), t.TempDir()
	for dir, token := range map[string]string{home: "from-home", login: "from-login", filepath.Join(user, ".claude"): "from-default"} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"accessToken":"`+token+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for env, want := range map[[2]string]string{
		{"CLAUDE_SECURESTORAGE_CONFIG_DIR", login}: "from-login",
		{"CLAUDE_SECURESTORAGE_CONFIG_DIR", ""}:    "from-default",
		{"UNSET", ""}:                              "from-home",
	} {
		launch := map[string]string{"CLAUDE_CONFIG_DIR": home, "HOME": user, env[0]: env[1]}
		opts := (ResolveOptions{Env: envFrom(nil), CommandEnv: launch, GOOS: "linux"}).WithClaudeSettings(context.Background())
		if cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts); !ok || cred.Secret != want {
			t.Fatalf("%v: credential %q (%v), want %q", env, cred.Secret, ok, want)
		}
	}
}
