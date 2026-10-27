package ladosetup

// The project part of the inventory (plan decisions k and l). The setup
// files a project's repository tracks reach every worker through its
// worktree, so LADO shows them read-only and never adopts them. The personal
// files most repositories leave untracked never reach a worktree, so LADO
// shows them with that reason and leaves them alone.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// gitTimeout bounds the git call that lists one project's tracked files.
const gitTimeout = 10 * time.Second

// The personal Claude files of a project's main checkout.
const (
	claudeLocalMD       = "CLAUDE.local.md"
	claudeLocalSettings = ".claude/settings.local.json"
)

// repoSetupPathspecs are the setup files Claude Code and Codex load from a
// checkout, and the two personal files, which are listed so a repository
// that tracks them shows them as tracked.
var repoSetupPathspecs = []string{
	"*CLAUDE.md", "AGENTS.md", "*/AGENTS.md", ".claude/settings.json", ".mcp.json",
	".claude/skills/*", ".claude/agents/*", ".claude/commands/*", ".codex/*",
	claudeLocalMD, claudeLocalSettings,
}

// projects reads the main checkout of every registered project. A project
// that cannot be read keeps its error and adds no entries; the others are
// read as usual. Scratch projects have no repository and are not read.
func (sc *scanner) projects(ctx context.Context) ([]ProjectScan, error) {
	if sc.deps.Projects == nil {
		return nil, nil
	}
	records, err := sc.deps.Projects.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the projects: %w", err)
	}
	var scans []ProjectScan
	for _, p := range records {
		if p.Kind.WithDefault() == domain.ProjectKindScratch || !p.ArchivedAt.IsZero() {
			continue
		}
		scan := ProjectScan{ID: domain.ProjectID(p.ID), Name: projectName(p), Path: p.Path}
		if err := sc.project(ctx, scan.ID, p.Path); err != nil {
			scan.Error = err.Error()
		}
		scans = append(scans, scan)
	}
	return scans, nil
}

func projectName(p domain.ProjectRecord) string {
	switch {
	case strings.TrimSpace(p.DisplayName) != "":
		return p.DisplayName
	case p.Path != "":
		return filepath.Base(p.Path)
	default:
		return p.ID
	}
}

// project lists one project's tracked setup files and its untracked
// personal files. Entries are added only when git answered.
func (sc *scanner) project(ctx context.Context, id domain.ProjectID, dir string) error {
	if dir == "" {
		return errors.New("the project has no folder")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("read the project folder: %w", err)
	}
	tracked, err := trackedSetupFiles(ctx, dir)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(tracked))
	for _, rel := range tracked {
		seen[rel] = true
		tool, kind, ok := repoFileKind(rel)
		if !ok {
			continue
		}
		e := domain.LadoSetupEntry{
			Tool: tool, Kind: kind, Name: rel, Location: domain.LadoSetupLocationProject,
			Path: filepath.Join(dir, filepath.FromSlash(rel)), Tracked: true, ProjectID: id,
		}
		// A file deleted in the main checkout is still tracked, and the
		// worktrees still get it; it is listed without its content.
		if info, err := os.Lstat(e.Path); err == nil && info.Mode().IsRegular() {
			if size, sum, err := fileSum(e.Path); err == nil {
				e.Size, e.SHA256 = size, sum
			}
		}
		sc.add(e, domain.LadoSetupStatusRepoTracked, domain.LadoSetupReasonRepoTracked)
	}
	for _, rel := range []string{claudeLocalMD, claudeLocalSettings} {
		if seen[rel] {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		_, kind, _ := repoFileKind(rel)
		sc.add(domain.LadoSetupEntry{
			Tool: domain.LadoSetupToolClaudeCode, Kind: kind, Name: rel, Location: domain.LadoSetupLocationProject, Path: p, ProjectID: id,
		}, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonWorktreesDoNotLoad)
	}
	return nil
}

// repoFileKind tells which tool loads a tracked file and as what. It refuses
// what a pathspec matched by its suffix alone, such as NOTCLAUDE.md.
func repoFileKind(rel string) (domain.LadoSetupTool, domain.LadoSetupKind, bool) {
	claude, codex := domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex
	switch {
	case rel == claudeLocalMD || path.Base(rel) == "CLAUDE.md":
		return claude, domain.LadoSetupKindInstructions, true
	case rel == claudeLocalSettings || rel == ".claude/settings.json":
		return claude, domain.LadoSetupKindSetting, true
	case rel == ".mcp.json":
		return claude, domain.LadoSetupKindMCPServer, true
	case strings.HasPrefix(rel, ".claude/skills/"):
		return claude, domain.LadoSetupKindSkill, true
	case strings.HasPrefix(rel, ".claude/agents/"):
		return claude, domain.LadoSetupKindAgent, true
	case strings.HasPrefix(rel, ".claude/commands/"):
		return claude, domain.LadoSetupKindCommand, true
	case path.Base(rel) == "AGENTS.md":
		return codex, domain.LadoSetupKindInstructions, true
	case strings.HasPrefix(rel, ".codex/"):
		return codex, "", true
	default:
		return "", "", false
	}
}

// trackedSetupFiles runs git ls-files in dir for repoSetupPathspecs, with
// every prompt off, and returns the paths relative to dir with slashes.
func trackedSetupFiles(ctx context.Context, dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	args := append([]string{"-C", dir, "ls-files", "-z", "--"}, repoSetupPathspecs...)
	cmd := aoprocess.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("git ls-files gave no answer within %s", gitTimeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
			line, _, _ := strings.Cut(strings.TrimSpace(string(exitErr.Stderr)), "\n")
			return nil, fmt.Errorf("git ls-files: %s", line)
		}
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, rel := range bytes.Split(out, []byte{0}) {
		if len(rel) > 0 {
			files = append(files, string(rel))
		}
	}
	return files, nil
}
