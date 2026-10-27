package codex

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodexAdapterAppendsLadoOverrides(t *testing.T) {
	ctx, plugin := context.Background(), &Plugin{resolvedBinary: "codex"}
	launch := ports.LaunchConfig{SessionID: "s-1", Prompt: "go"}
	restore := ports.RestoreConfig{Session: ports.SessionRef{ID: "s-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "thread-1"}}}
	commands := func() ([]string, []string, error) {
		launched, err := plugin.GetLaunchCommand(ctx, launch)
		restored, _, restoreErr := plugin.GetRestoreCommand(ctx, restore)
		return launched, restored, errors.Join(err, restoreErr)
	}
	upstreamLaunch, upstreamRestore, err := commands()
	if err != nil {
		t.Fatal(err)
	}

	pair := []string{"-c", `mcp_servers.docs={url = "https://docs"}`}
	var asked []string
	plugin.SetLadoOverrides(func(_ context.Context, id string) ([]string, error) { asked = append(asked, id); return pair, nil })
	// After AO's options, before the prompt and the thread to resume.
	wantLaunch := slices.Insert(slices.Clone(upstreamLaunch), len(upstreamLaunch)-2, pair...)
	wantRestore := slices.Insert(slices.Clone(upstreamRestore), len(upstreamRestore)-1, pair...)
	if launched, restored, err := commands(); err != nil || !reflect.DeepEqual(launched, wantLaunch) || !reflect.DeepEqual(restored, wantRestore) || !reflect.DeepEqual(asked, []string{"s-1", "s-1"}) {
		t.Fatalf("with a hook: launch %q, restore %q, %v, asked for %q; want %q and %q for s-1", launched, restored, err, asked, wantLaunch, wantRestore)
	}

	failed := errors.New("LADO_SETUP_CODEX_FAILED")
	plugin.SetLadoOverrides(func(context.Context, string) ([]string, error) { return nil, failed })
	if launched, restored, err := commands(); !errors.Is(err, failed) || launched != nil || restored != nil {
		t.Fatalf("a failing hook: launch %q, restore %q, %v; want its error and no command", launched, restored, err)
	}

	plugin.SetLadoOverrides(nil)
	if launched, restored, err := commands(); err != nil || !reflect.DeepEqual(launched, upstreamLaunch) || !reflect.DeepEqual(restored, upstreamRestore) {
		t.Fatalf("without a hook: launch %q, restore %q, %v; want upstream's", launched, restored, err)
	}
}
