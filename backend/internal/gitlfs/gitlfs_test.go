package gitlfs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// newRepo makes an isolated repository. Only .gitattributes files are
// committed, so a git-lfs install on this computer cannot turn anything into
// a pointer file.
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func TestUsesLFS(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{name: "root attributes", files: map[string]string{".gitattributes": "*.bin filter=lfs diff=lfs merge=lfs -text\n"}, want: true},
		{name: "nested attributes", files: map[string]string{"README": "x\n", "assets/models/.gitattributes": "*.onnx filter=lfs -text\n"}, want: true},
		{name: "attributes without lfs", files: map[string]string{".gitattributes": "*.sh text eol=lf\n"}},
		{name: "lfs text outside attributes", files: map[string]string{"notes.txt": "filter=lfs\n"}},
		{name: "empty commit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t, tc.files)
			got, err := UsesLFS(ctx, "", repo, "HEAD")
			if err != nil {
				t.Fatalf("UsesLFS: %v", err)
			}
			if got != tc.want {
				t.Fatalf("UsesLFS = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUsesLFSReadsTheGivenRef(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{".gitattributes": "*.sh text\n"})
	git(t, repo, "checkout", "-q", "-b", "lfs")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.bin filter=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-am", "lfs")
	git(t, repo, "checkout", "-q", "main")

	if got, err := UsesLFS(ctx, "git", repo, "lfs"); err != nil || !got {
		t.Fatalf("UsesLFS(lfs) = %v, %v; want true", got, err)
	}
	if got, err := UsesLFS(ctx, "git", repo, "main"); err != nil || got {
		t.Fatalf("UsesLFS(main) = %v, %v; want false", got, err)
	}
	if got, err := UsesLFS(ctx, "git", repo, ""); err != nil || got {
		t.Fatalf("UsesLFS(\"\") = %v, %v; want HEAD (main) false", got, err)
	}
}

func TestUsesLFSUnknownRef(t *testing.T) {
	repo := newRepo(t, map[string]string{".gitattributes": "*.bin filter=lfs\n"})
	if _, err := UsesLFS(context.Background(), "", repo, "refs/heads/missing"); err == nil {
		t.Fatal("UsesLFS on a missing ref = nil error, want error")
	}
	if _, err := UsesLFS(context.Background(), "", t.TempDir(), "HEAD"); err == nil {
		t.Fatal("UsesLFS outside a repository = nil error, want error")
	}
}

func TestInstalled(t *testing.T) {
	ctx := context.Background()
	if Installed(ctx, filepath.Join(t.TempDir(), "no-such-git")) {
		t.Fatal("Installed with a missing binary = true")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fake git scripts need a POSIX shell")
	}
	dir := t.TempDir()
	ok := filepath.Join(dir, "git-ok")
	fail := filepath.Join(dir, "git-fail")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\n[ \"$1 $2\" = \"lfs version\" ] && exit 0\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, []byte("#!/bin/sh\necho \"git: 'lfs' is not a git command\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Installed(ctx, ok) {
		t.Fatal("Installed with a git that runs lfs = false")
	}
	if Installed(ctx, fail) {
		t.Fatal("Installed with a git without lfs = true")
	}
}
