package ladoqueue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// The worker learns what LADO verifies on its pull request and how to write
// the evidence file (decision k of the step 8 plan), after the approved plan,
// which stays as it was pinned.
func TestWorkerPromptAsksForEvidence(t *testing.T) {
	common := []string{
		"LADO verifies your pull request's head",
		"The CI checks its base branch requires must pass",
		"A changed path outside the plan's affected_paths needs the developer's confirmation",
		"When a check fails, you get a message: push a fix, and LADO checks the new head.",
		"Before you run `ao report --done`, write `lado/evidence.md` in your session artifact directory",
		"\nhead: <the full SHA of the commit this evidence describes>\n",
		"\ncriteria:\n  - criterion: 1 ",
		"      - check: <a CI check name on the pull request>\n",
		"      - command: <a LADO check command name>\n",
		"      - commit: <a commit SHA in this branch>\n",
	}
	for _, c := range []struct {
		name       string
		checks     []domain.LadoCheckCommand
		validators []ladopolicy.ValidatorKind
		want       []string
		absent     []string
	}{
		{
			name:   "the project's commands and the rules' validators",
			checks: []domain.LadoCheckCommand{{Name: "lint", Command: "npm run lint"}, {Name: "unit tests", Command: "go test ./..."}},
			validators: []ladopolicy.ValidatorKind{
				ladopolicy.ValidatorChecksMustPass, ladopolicy.ValidatorProtectedPaths, ladopolicy.ValidatorCommitMessageLocale,
			},
			want: []string{
				"The project's check commands `lint` and `unit tests` must pass; LADO runs them in this worktree",
				"Your commit messages and the pull request title must follow the commit message rule.",
				"a changed protected path fails unless the plan names it.",
			},
			absent: []string{"npm run lint", "go test"},
		},
		{
			name:   "one command",
			checks: []domain.LadoCheckCommand{{Name: "check", Command: "make check"}},
			want:   []string{"The project's check command `check` must pass; LADO runs it in this worktree"},
		},
		{
			name:   "no commands and a snapshot without validators",
			absent: []string{"check command `", "check commands `", "commit message rule", "protected path"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.checks = c.checks
			f.tasks.input.Snapshot = snapshotWithValidators(t, c.validators...)
			f.store.add(testEntry("entry-1", t0.Add(-10*time.Minute)))

			f.tick(t, t0)

			spawned := f.sessions.spawned()
			if len(spawned) != 1 {
				t.Fatalf("spawned %d workers, want 1", len(spawned))
			}
			prompt := spawned[0].Prompt
			plan := "## Approved plan (revision 2, sha256 a1b2c3d4e5f6)\n\n" + testPlanText + "\n## Verification and evidence\n\n"
			if !strings.Contains(prompt, plan) {
				t.Fatalf("prompt does not hold the approved plan unchanged before the verification section:\n%s", prompt)
			}
			for _, want := range append(common, c.want...) {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt lacks %q:\n%s", want, prompt)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(prompt, absent) {
					t.Errorf("prompt holds %q:\n%s", absent, prompt)
				}
			}
		})
	}
}

// snapshotWithValidators is the fixture's snapshot with the validators of
// the given kinds.
func snapshotWithValidators(t *testing.T, kinds ...ladopolicy.ValidatorKind) domain.LadoPolicySnapshot {
	t.Helper()
	snapshot := testSnapshot(claudeAgent(), false)
	var content ladopolicy.SnapshotContent
	if err := json.Unmarshal(snapshot.Content, &content); err != nil {
		t.Fatalf("decode the fixture's snapshot: %v", err)
	}
	for _, kind := range kinds {
		content.Validators = append(content.Validators, ladopolicy.SnapshotValidator{Kind: kind, Config: json.RawMessage("{}")})
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("encode the snapshot: %v", err)
	}
	snapshot.Content = data
	return snapshot
}
