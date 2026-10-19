package ladoconnect

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestExecGitChecksARemoteAndSetsTheSSHCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "-c", "user.email=lado@example.com", "-c", "user.name=LADO", "commit", "-q", "--allow-empty", "-m", "init")
	ctx := context.Background()
	git := execGit{}

	if err := git.LsRemote(ctx, fileURL(repo), sshCommand("", true)); err != nil {
		t.Fatalf("LsRemote of a reachable repository: %v", err)
	}
	err := git.LsRemote(ctx, fileURL(filepath.Join(repo, "missing")), sshCommand("", true))
	if err == nil {
		t.Fatal("LsRemote of a missing repository succeeded")
	}
	if strings.Contains(err.Error(), "Please make sure") || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("LsRemote error = %q, want the line naming the failure", err)
	}

	command := sshCommand("/keys/id_lado", false)
	if err := git.SetSSHCommand(ctx, repo, command); err != nil {
		t.Fatalf("SetSSHCommand: %v", err)
	}
	if got := gitIn(t, repo, "config", "--local", "core.sshCommand"); got != command {
		t.Fatalf("core.sshCommand = %q, want %q", got, command)
	}
}

func TestSSHCommandQuotesTheKeyPath(t *testing.T) {
	if got := sshCommand("/home/me/it's key", true); got != `ssh -i '/home/me/it'\''s key' -o IdentitiesOnly=yes -o BatchMode=yes` {
		t.Fatalf("sshCommand = %s", got)
	}
	if got := sshCommand("", false); got != "ssh" {
		t.Fatalf("sshCommand without a key = %q, want plain ssh", got)
	}
}

func TestFailureLineSkipsWarnings(t *testing.T) {
	output := "Warning: Permanently added 'github.com' to the list of known hosts.\ngit@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	if got := failureLine(output); got != "git@github.com: Permission denied (publickey)." {
		t.Fatalf("failureLine = %q", got)
	}
}
