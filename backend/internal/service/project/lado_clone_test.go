package project_test

import (
	"context"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// LADO fork: a connection with a key file clones with that key and keeps it in
// the repository's config (docs/LJY-PRD/TDD.md §5.1).
func TestManager_CloneSetsGitConfigBeforeTheFirstFetch(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	source := gitRepo(t)
	command := "ssh -i '/keys/id_lado' -o IdentitiesOnly=yes"

	cloned, err := m.Clone(ctx, project.CloneInput{
		RemoteURL:         (&url.URL{Scheme: "file", Path: source}).String(),
		DestinationParent: t.TempDir(),
		GitConfig:         []string{"core.sshCommand=" + command},
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	out, err := exec.Command("git", "-C", cloned.Path, "config", "--local", "core.sshCommand").CombinedOutput()
	if err != nil {
		t.Fatalf("read core.sshCommand: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != command {
		t.Fatalf("core.sshCommand = %q, want %q", got, command)
	}
}
