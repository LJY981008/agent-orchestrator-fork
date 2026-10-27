package ladosetup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// B4: the variables that pick Claude Code's login, provider or proxy stay
// in the original settings.json when env is adopted, an env that turns a
// cloud provider on is not adopted at all, and no change can add them.
func TestProviderLoginEnvStaysInPlace(t *testing.T) {
	ctx := context.Background()
	settings := `{
  "env": {
    "AWS_REGION": "us-east-1",
    "AWS_PROFILE": "work",
    "CLOUD_ML_REGION": "us-east5",
    "VERTEX_REGION_CLAUDE_3_7_SONNET": "europe-west1",
    "GOOGLE_APPLICATION_CREDENTIALS": "/keys/gcp.json",
    "AZURE_API_KEY": "az",
    "https_proxy": "http://proxy:3128",
    "NO_PROXY": "localhost",
    "ANTHROPIC_MODEL": "opus",
    "DEBUG": "1"
  },
  "model": "opus"
}
`
	kept := []string{"ANTHROPIC_MODEL", "AWS_PROFILE", "AWS_REGION", "AZURE_API_KEY", "CLOUD_ML_REGION", "GOOGLE_APPLICATION_CREDENTIALS",
		"NO_PROXY", "VERTEX_REGION_CLAUDE_3_7_SONNET", "https_proxy"}
	f := newChangeFixture(t)
	path := filepath.Join(f.claude, "settings.json")
	f.write(path, settings)
	env := entryAt(t, mustScan(t, f.deps), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env")
	wantStatus(t, env, domain.LadoSetupStatusAdoptable, "")
	if !slices.Equal(env.KeptInPlace, kept) {
		t.Fatalf("env keeps %v in place, want %v", env.KeptInPlace, kept)
	}
	ids := f.adopt(claudeMove(domain.LadoSetupKindSetting, "env"))
	var left struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &left); err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(left.Env); !slices.Equal(got, kept) {
		t.Fatalf("settings.json keeps env %v, want %v", got, kept)
	}
	_, versions, _, err := f.store.LadoSetupItem(ctx, ids["env"])
	if err != nil || len(versions) != 1 {
		t.Fatalf("env versions %+v (%v)", versions, err)
	}
	adopted, err := f.deps.Blobs.Get(versions[0].SHA256, maxBlobBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(adopted) != `{"DEBUG":"1"}` {
		t.Fatalf("env adopted as %s", adopted)
	}

	// An edit or a new env item cannot bring them back.
	for _, name := range []string{"AWS_REGION", "aws_secret_access_key", "CLAUDE_CODE_USE_VERTEX", "VERTEX_REGION_X", "GOOGLE_CLOUD_PROJECT", "AZURE_OPENAI_KEY", "http_proxy", "HTTPS_PROXY"} {
		_, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["env"], BaseVersion: 1, Content: json.RawMessage(`{"DEBUG":"1","` + name + `":"x"}`)}, f.presence())
		if details := wantAPIError(t, err, CodeLoginKey); details["key"] != name {
			t.Fatalf("%s: details %v", name, details)
		}
	}

	// A provider switch that is on keeps the whole env in place.
	for _, on := range []string{`"1"`, `"true"`, `1`} {
		h := newFakeHome(t)
		h.write(filepath.Join(h.claude, "settings.json"), `{"env":{"CLAUDE_CODE_USE_BEDROCK":`+on+`,"AWS_REGION":"us-east-1","DEBUG":"1"}}`)
		wantStatus(t, entryAt(t, mustScan(t, h.deps()), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env"),
			domain.LadoSetupStatusNotAdoptable, domain.LadoSetupReasonProviderLoginEnv)
	}
	h := newFakeHome(t)
	h.write(filepath.Join(h.claude, "settings.json"), `{"env":{"CLAUDE_CODE_USE_FOUNDRY":"0","DEBUG":"1"}}`)
	off := entryAt(t, mustScan(t, h.deps()), domain.LadoSetupLocationClaudeDir, domain.LadoSetupKindSetting, "env")
	wantStatus(t, off, domain.LadoSetupStatusAdoptable, "")
	if !slices.Equal(off.KeptInPlace, []string{"CLAUDE_CODE_USE_FOUNDRY"}) {
		t.Fatalf("a switch that is off is kept as %v", off.KeptInPlace)
	}
	f.mustApply(Change{Op: OpDelete, ItemID: ids["env"]}, true)
	f.write(path, `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","DEBUG":"2"}}`)
	_, err = f.svc.Adopt(ctx, AdoptRequest{Entries: []AdoptEntry{claudeMove(domain.LadoSetupKindSetting, "env")}, DryRun: true})
	if details := wantAPIError(t, err, CodeNotAdoptable); details["reason"] != string(domain.LadoSetupReasonProviderLoginEnv) {
		t.Fatalf("details %v", details)
	}
}

// The dry run of a change compares the conflicts against the rules'
// permission ceiling.
func TestChangeDryRunChecksTheCeiling(t *testing.T) {
	f := newChangeFixture(t)
	f.svc.SetPolicy(&impactPolicy{})
	create := Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "permissions",
		Content: json.RawMessage(`{"defaultMode":"bypassPermissions"}`)}
	res := f.dryRun(create)
	if !slices.ContainsFunc(res.ConflictsAdded, func(c Conflict) bool {
		return c.Code == ConflictDefaultModeAboveCeiling && c.Details["mode"] == "bypassPermissions" && c.Details["ceiling"] == string(domain.PermissionModeAuto)
	}) {
		t.Fatalf("conflicts added %+v, want the default mode above the auto ceiling", res.ConflictsAdded)
	}
	id := f.mustApply(create, true).Item.ID
	edit := f.dryRun(Change{Op: OpEdit, ItemID: id, BaseVersion: 1, Content: json.RawMessage(`{"defaultMode":"acceptEdits"}`)})
	if !slices.ContainsFunc(edit.ConflictsRemoved, func(c Conflict) bool { return c.Code == ConflictDefaultModeAboveCeiling }) {
		t.Fatalf("conflicts removed %+v, want the default mode above the ceiling", edit.ConflictsRemoved)
	}
}

// Items reach sessions on a verified OS only, so making one elsewhere is
// refused as adoption is, dry run or not, and nothing is written.
func TestCreateRefusedOnUnverifiedOS(t *testing.T) {
	f := newChangeFixture(t)
	for _, goos := range []string{"darwin", "windows"} {
		f.deps.GOOS = goos
		svc := New(f.deps)
		for _, c := range []Change{
			{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "RULES.md", Content: jsonText("# rules\n")},
			{Op: OpCreate, Tool: domain.LadoSetupToolCodex, Kind: domain.LadoSetupKindMCPServer, Name: "docs", Content: json.RawMessage(`{"url":"https://docs"}`)},
		} {
			for _, dryRun := range []bool{true, false} {
				db := f.dbState()
				c.DryRun = dryRun
				_, err := svc.Apply(context.Background(), c, f.presence())
				details := wantAPIError(t, err, CodeOSUnverified)
				if details["os"] != goos || !strings.Contains(strings.Join(anyStrings(details["tools"]), ","), string(c.Tool)) {
					t.Fatalf("%s %s: details %v", goos, c.Tool, details)
				}
				wantSameDB(t, "a refused create", db, f.dbState())
			}
		}
	}
	f.deps.GOOS = "linux"
	f.svc = New(f.deps)
	f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "RULES.md", Content: jsonText("# rules\n")}, false)
}

// anyStrings reads an error detail that holds a list.
func anyStrings(v any) []string {
	var out []string
	switch v := v.(type) {
	case []string:
		out = v
	case []domain.LadoSetupTool:
		for _, s := range v {
			out = append(out, string(s))
		}
	case []any:
		for _, s := range v {
			if s, ok := s.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
