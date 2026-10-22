package ladopolicy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// maxAORulesFileBytes is the 4 MiB cap on agentRulesFile from the step 5
// plan.
const maxAORulesFileBytes = 4 << 20

func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeFile writes content to the slash-separated rel under dir and returns
// the file's path.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func projectAt(dir string, cfg domain.ProjectConfig) domain.ProjectRecord {
	return domain.ProjectRecord{ID: "proj-1", Path: dir, Config: cfg}
}

func TestReadAORulesHashesTrimmedText(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/rules.md", "\n\n  Use tabs.\n\n")

	got := ladopolicy.ReadAORules(projectAt(dir, domain.ProjectConfig{
		AgentRules:        "  커밋은 한국어로  \n",
		AgentRulesFile:    " docs/rules.md ",
		OrchestratorRules: "\tPlan first.\n",
	}))
	want := []ladopolicy.AORule{
		// Chars counts runes: the Korean text is 8 runes and 22 bytes.
		{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: hexSHA256("커밋은 한국어로"), Chars: 8},
		{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/rules.md", SHA256: hexSHA256("Use tabs."), Chars: 9},
		{Kind: ladopolicy.AORuleOrchestratorInstructions, SHA256: hexSHA256("Plan first."), Chars: 11},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadAORules() =\n%+v\nwant\n%+v", got, want)
	}

	// Text that differs only in surrounding whitespace hashes the same.
	plain := ladopolicy.ReadAORules(projectAt(dir, domain.ProjectConfig{AgentRules: "커밋은 한국어로"}))
	if len(plain) != 1 || plain[0] != got[0] {
		t.Errorf("untrimmed and trimmed text differ: %+v and %+v", plain, got[0])
	}

	// Settings that are empty or only whitespace make no entry.
	empty := ladopolicy.ReadAORules(projectAt(dir, domain.ProjectConfig{AgentRules: " \n", AgentRulesFile: "  ", OrchestratorRules: "\t"}))
	if len(empty) != 0 {
		t.Errorf("blank settings gave entries: %+v", empty)
	}

	// A configured file with only whitespace keeps its entry, with the empty
	// text's hash.
	writeFile(t, dir, "docs/blank.md", " \n\t\n")
	blank := ladopolicy.ReadAORules(projectAt(dir, domain.ProjectConfig{AgentRulesFile: "docs/blank.md"}))
	wantBlank := []ladopolicy.AORule{{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/blank.md", SHA256: hexSHA256("")}}
	if !reflect.DeepEqual(blank, wantBlank) {
		t.Errorf("blank file gave %+v, want %+v", blank, wantBlank)
	}
}

func TestReadAORulesRefusesPathsOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	writeFile(t, dir, "docs/rules.md", "inside")
	outside := writeFile(t, root, "x.md", "outside rules")
	// Joined under the project, the absolute path would name this file.
	writeFile(t, dir, filepath.ToSlash(strings.TrimPrefix(outside, filepath.VolumeName(outside))), "joined")

	cases := []struct {
		name    string
		project domain.ProjectRecord
	}{
		{"parent", projectAt(dir, domain.ProjectConfig{AgentRulesFile: "../x.md"})},
		{"parent after a folder", projectAt(dir, domain.ProjectConfig{AgentRulesFile: "docs/../../x.md"})},
		{"absolute path", projectAt(dir, domain.ProjectConfig{AgentRulesFile: outside})},
		{"project root's parent", projectAt(dir, domain.ProjectConfig{AgentRulesFile: ".."})},
		{"project without a path", projectAt("", domain.ProjectConfig{AgentRulesFile: "docs/rules.md"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ladopolicy.ReadAORules(c.project)
			if len(got) != 1 {
				t.Fatalf("ReadAORules() = %+v, want one entry", got)
			}
			rule := got[0]
			if rule.Kind != ladopolicy.AORuleWorkerInstructionsFile || rule.Path != c.project.Config.AgentRulesFile {
				t.Errorf("entry %+v, want the file kind with path %q", rule, c.project.Config.AgentRulesFile)
			}
			// A refused path is never read, so the entry has no hash.
			if rule.ReadError == "" || rule.SHA256 != "" || rule.Chars != 0 {
				t.Errorf("entry %+v, want a read error and no text", rule)
			}
		})
	}
}

func TestReadAORulesReportsUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/readme.md", "docs")
	sized := func(rel string, size int64) {
		if err := os.Truncate(writeFile(t, dir, rel, ""), size); err != nil {
			t.Fatal(err)
		}
	}
	sized("big/at-limit.md", maxAORulesFileBytes)
	sized("big/over-limit.md", maxAORulesFileBytes+1)

	cases := []struct {
		name string
		rel  string
		// readable is the file entry when the file can be read.
		readable *ladopolicy.AORule
	}{
		{name: "missing file", rel: "docs/missing.md"},
		{name: "folder", rel: "docs"},
		{name: "over 4 MiB", rel: "big/over-limit.md"},
		{name: "exactly 4 MiB", rel: "big/at-limit.md", readable: &ladopolicy.AORule{
			Kind:   ladopolicy.AORuleWorkerInstructionsFile,
			Path:   "big/at-limit.md",
			SHA256: hexSHA256(strings.Repeat("\x00", maxAORulesFileBytes)),
			Chars:  maxAORulesFileBytes,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ladopolicy.ReadAORules(projectAt(dir, domain.ProjectConfig{
				AgentRules:        "Worker rules.",
				AgentRulesFile:    c.rel,
				OrchestratorRules: "Orchestrator rules.",
			}))
			if len(got) != 3 {
				t.Fatalf("ReadAORules() = %+v, want three entries", got)
			}
			// The other entries come out whatever happens to the file.
			worker := ladopolicy.AORule{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: hexSHA256("Worker rules."), Chars: 13}
			orchestrator := ladopolicy.AORule{Kind: ladopolicy.AORuleOrchestratorInstructions, SHA256: hexSHA256("Orchestrator rules."), Chars: 19}
			if got[0] != worker || got[2] != orchestrator {
				t.Errorf("other entries %+v and %+v, want %+v and %+v", got[0], got[2], worker, orchestrator)
			}
			file := got[1]
			if c.readable != nil {
				if file != *c.readable {
					t.Errorf("file entry %+v, want %+v", file, *c.readable)
				}
				return
			}
			if file.Kind != ladopolicy.AORuleWorkerInstructionsFile || file.Path != c.rel {
				t.Errorf("file entry %+v, want the file kind with path %q", file, c.rel)
			}
			if file.ReadError == "" || file.SHA256 != "" || file.Chars != 0 {
				t.Errorf("file entry %+v, want a read error and no text", file)
			}
			// The reason goes into rule snapshots, which must not depend on
			// where the project lives.
			if strings.Contains(file.ReadError, dir) {
				t.Errorf("read error %q names the project folder", file.ReadError)
			}
		})
	}
}
