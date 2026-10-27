package review

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestReviewerSystemPromptCarriesTheLadoSection is the reviewer hook of step 8
// plan decision s: with a source installed, LADO's section ends the
// reviewer's system.md, on a launch and on an idle restore; without one the
// file is AO's text as before; and a source error leaves the section out and
// the launch goes on.
func TestReviewerSystemPromptCarriesTheLadoSection(t *testing.T) {
	t.Cleanup(func() { SetLadoSystemSection(nil) })
	const section = "## User instructions (managed by LADO)\n\nAnswer in Korean."
	var asked []string
	withSection := func(_ context.Context, worker domain.SessionID, harness domain.AgentHarness) (string, error) {
		asked = append(asked, string(worker)+" "+string(harness))
		return "\n" + section + "\n\n", nil
	}
	failing := func(context.Context, domain.SessionID, domain.AgentHarness) (string, error) {
		return "", errors.New("the store is closed")
	}
	paths := []struct {
		name string
		// aoEnd is how AO's own text on the path ends.
		aoEnd  string
		launch func(Launcher, LaunchSpec) (LaunchResult, error)
	}{
		{"launch", "follow it completely.\n", func(l Launcher, spec LaunchSpec) (LaunchResult, error) {
			return l.Spawn(context.Background(), spec)
		}},
		{"idle restore", "before reviewing or submitting results.\n", func(l Launcher, spec LaunchSpec) (LaunchResult, error) {
			return l.RestoreTerminal(context.Background(), spec)
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			system := func(source LadoSystemSection) string {
				t.Helper()
				SetLadoSystemSection(source)
				reviewer := &fakeReviewer{}
				l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, t.TempDir())
				if _, err := path.launch(l, launchSpec()); err != nil {
					t.Fatalf("%s: %v", path.name, err)
				}
				data, err := os.ReadFile(reviewer.gotInv.SystemPromptFile)
				if err != nil {
					t.Fatalf("read the system prompt: %v", err)
				}
				return string(data)
			}
			ao := system(nil)
			if !strings.HasSuffix(ao, path.aoEnd) || strings.Contains(ao, "managed by LADO") {
				t.Fatalf("system prompt without a source = %q, want AO's text ending %q", ao, path.aoEnd)
			}
			asked = nil
			if got, want := system(withSection), ao+"\n"+section+"\n"; got != want {
				t.Fatalf("system prompt with a source =\n%q\nwant AO's text and the section\n%q", got, want)
			}
			if !slices.Equal(asked, []string{"mer-1 claude-code"}) {
				t.Fatalf("the source was asked %q, want once for worker mer-1 and its claude-code reviewer", asked)
			}
			if got := system(failing); got != ao {
				t.Fatalf("system prompt when the source fails = %q, want AO's text %q", got, ao)
			}
			if got := system(func(context.Context, domain.SessionID, domain.AgentHarness) (string, error) { return " \n", nil }); got != ao {
				t.Fatalf("system prompt with a blank section = %q, want AO's text %q", got, ao)
			}
		})
	}
}
