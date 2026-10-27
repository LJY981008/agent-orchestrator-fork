package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladosetup"
)

var ladoSetupTime = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// fakeLadoSetupService answers with fixed results and keeps what it was
// asked. Like the real service, it asks presence only for a change that
// needs it, an adoption that is not a preview, and every restore.
type fakeLadoSetupService struct {
	overview      ladosetup.Overview
	items         []ladopolicy.SetupItemState
	conflicts     []ladosetup.Conflict
	adopt         ladosetup.AdoptResult
	adoption      ladosetup.AdoptionView
	restore       ladosetup.RestoreResult
	item          ladosetup.ItemView
	change        ladosetup.ChangeResult
	needsPresence bool
	err           error
	calls         int

	gotRescan    []bool
	gotScope     [2]string
	gotConflicts ladosetup.ScopeConflictsInput
	gotAdopt     ladosetup.AdoptRequest
	gotID        string
	gotChange    ladosetup.Change
	gotProof     *domain.LadoPresenceProof
}

func (f *fakeLadoSetupService) Overview(_ context.Context, rescan bool) (ladosetup.Overview, error) {
	f.calls++
	f.gotRescan = append(f.gotRescan, rescan)
	return f.overview, f.err
}

func (f *fakeLadoSetupService) ScopeItems(_ context.Context, projectID domain.ProjectID, taskID domain.LadoTaskID) ([]ladopolicy.SetupItemState, error) {
	f.calls++
	f.gotScope = [2]string{string(projectID), string(taskID)}
	return f.items, f.err
}

func (f *fakeLadoSetupService) ScopeConflicts(_ context.Context, in ladosetup.ScopeConflictsInput) ([]ladosetup.Conflict, error) {
	f.calls++
	f.gotConflicts = in
	return f.conflicts, f.err
}

func (f *fakeLadoSetupService) Adopt(_ context.Context, req ladosetup.AdoptRequest) (ladosetup.AdoptResult, error) {
	f.calls++
	f.gotAdopt = req
	if !req.DryRun {
		if err := f.confirm(req.Presence, domain.LadoPresenceSetupAdopt, req.PlanSHA256); err != nil {
			return ladosetup.AdoptResult{}, err
		}
	}
	return f.adopt, f.err
}

func (f *fakeLadoSetupService) Adoption(_ context.Context, id domain.LadoSetupAdoptionID) (ladosetup.AdoptionView, error) {
	f.calls++
	f.gotID = string(id)
	return f.adoption, f.err
}

func (f *fakeLadoSetupService) Restore(_ context.Context, id domain.LadoSetupAdoptionID, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ladosetup.RestoreResult, error) {
	f.calls++
	f.gotID = string(id)
	if err := f.confirm(presence, domain.LadoPresenceSetupRestore, string(id)); err != nil {
		return ladosetup.RestoreResult{}, err
	}
	return f.restore, f.err
}

func (f *fakeLadoSetupService) Item(_ context.Context, id domain.LadoSetupItemID) (ladosetup.ItemView, error) {
	f.calls++
	f.gotID = string(id)
	return f.item, f.err
}

func (f *fakeLadoSetupService) Apply(_ context.Context, c ladosetup.Change, presence func(action, subject string) (domain.LadoPresenceProof, error)) (ladosetup.ChangeResult, error) {
	f.calls++
	f.gotChange = c
	if f.needsPresence && !c.DryRun {
		if err := f.confirm(presence, domain.LadoPresenceSetupChange, f.change.ChangeSHA256); err != nil {
			return ladosetup.ChangeResult{}, err
		}
	}
	return f.change, f.err
}

func (f *fakeLadoSetupService) confirm(presence func(action, subject string) (domain.LadoPresenceProof, error), action, subject string) error {
	if presence == nil {
		return apierr.Forbidden("LADO_PRESENCE_REQUIRED", "Confirm this in the AO desktop app.")
	}
	proof, err := presence(action, subject)
	if err != nil {
		return err
	}
	f.gotProof = &proof
	return nil
}

func setupSum(c string) string { return strings.Repeat(c, 64) }

func sampleSetupOverview() ladosetup.Overview {
	claude, codex := domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex
	return ladosetup.Overview{
		Inventory: ladosetup.Inventory{
			Entries: []domain.LadoSetupEntry{
				{Tool: claude, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", Location: domain.LadoSetupLocationManaged,
					Path: "/state/lado/harness/claude/CLAUDE.md", Size: 5, SHA256: setupSum("a"), Status: domain.LadoSetupStatusManaged, ItemID: "item-1"},
				{Tool: claude, Kind: domain.LadoSetupKindSetting, Name: "env", Location: domain.LadoSetupLocationClaudeDir,
					Path: "/home/me/.claude/settings.json#env", Size: 20, SHA256: setupSum("b"), Status: domain.LadoSetupStatusAdoptable,
					DefaultAction: domain.LadoSetupActionMove, KeptInPlace: []string{"ANTHROPIC_API_KEY"}},
				{Tool: claude, Name: ".credentials.json", Location: domain.LadoSetupLocationClaudeDir, Path: "/home/me/.claude/.credentials.json",
					Status: domain.LadoSetupStatusNever, Reason: domain.LadoSetupReasonCredentials},
				{Tool: codex, Kind: domain.LadoSetupKindMCPServer, Name: "notion", Location: domain.LadoSetupLocationCodexHome,
					Path: "/home/me/.codex/config.toml#mcp_servers.notion", Status: domain.LadoSetupStatusNotAdoptable, Reason: domain.LadoSetupReasonLiteralCredentials},
				{Tool: claude, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", Location: domain.LadoSetupLocationProject, Path: "/repo/CLAUDE.md",
					Size: 9, SHA256: setupSum("c"), Tracked: true, Status: domain.LadoSetupStatusRepoTracked, Reason: domain.LadoSetupReasonRepoTracked, ProjectID: "mer"},
			},
			Roots: []ladosetup.Root{
				{Tool: claude, Location: domain.LadoSetupLocationClaudeDir, Path: "/home/me/.claude", Found: true},
				{Tool: codex, Location: domain.LadoSetupLocationCodexHome, Path: "/home/me/.codex", Error: "permission denied"},
			},
			Projects: []ladosetup.ProjectScan{{ID: "mer", Name: "Mercury", Path: "/repo"}},
			Adoptions: []domain.LadoSetupAdoption{{
				ID: "adoption-1", CreatedAt: ladoSetupTime, Actor: "human", OS: "linux", PlanSHA256: setupSum("d"), ManifestSHA256: setupSum("e"),
				State: domain.LadoSetupAdoptionCompleted, RestoredAt: ladoSetupTime.Add(time.Hour),
			}},
			Drift: []domain.LadoSetupDrift{{ItemID: "item-1", Kind: domain.LadoSetupDriftEdited, Tool: claude, ItemKind: domain.LadoSetupKindInstructions,
				Name: "CLAUDE.md", Path: "/state/lado/harness/claude/CLAUDE.md", SHA256: setupSum("f")}},
			OS:        "windows",
			Verified:  map[domain.LadoSetupTool]bool{claude: false, codex: false},
			ScannedAt: ladoSetupTime,
		},
		Items: []ladopolicy.SetupItemState{{
			ItemID: "item-1", Tool: claude, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md", VersionID: "version-1", Version: 1, SHA256: setupSum("a"),
			Source: ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff},
			Global: domain.LadoBindingOff, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit,
		}},
		AdoptionItems: map[domain.LadoSetupAdoptionID]int{"adoption-1": 3},
	}
}

// The inventory and the scan answer with the same shape: each entry's
// status and reason, the OS and whether adoption is verified there, the
// managed items' Global state and each adoption's size.
func TestLadoAPI_SetupInventory(t *testing.T) {
	svc := &fakeLadoSetupService{overview: sampleSetupOverview()}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/setup/inventory", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoSetupInventoryResponse](t, body)
	if resp.OS != "windows" || !reflect.DeepEqual(resp.Verified, map[string]bool{"claude-code": false, "codex": false}) || !resp.ScannedAt.Equal(ladoSetupTime) {
		t.Fatalf("OS %q verified %v scanned %s; want windows, unverified for both tools", resp.OS, resp.Verified, resp.ScannedAt)
	}
	wantEntries := []controllers.LadoSetupEntryResponse{
		{Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", Location: "managed", Path: "/state/lado/harness/claude/CLAUDE.md", Size: 5,
			SHA256: setupSum("a"), Status: "managed", KeptInPlace: []string{}, ItemID: "item-1"},
		{Tool: "claude-code", Kind: "setting", Name: "env", Location: "claude_dir", Path: "/home/me/.claude/settings.json#env", Size: 20,
			SHA256: setupSum("b"), Status: "adoptable", DefaultAction: "move", KeptInPlace: []string{"ANTHROPIC_API_KEY"}},
		{Tool: "claude-code", Name: ".credentials.json", Location: "claude_dir", Path: "/home/me/.claude/.credentials.json", Status: "never",
			Reason: "credentials", KeptInPlace: []string{}},
		{Tool: "codex", Kind: "mcp_server", Name: "notion", Location: "codex_home", Path: "/home/me/.codex/config.toml#mcp_servers.notion",
			Status: "not_adoptable", Reason: "literal_credentials", KeptInPlace: []string{}},
		{Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", Location: "project", Path: "/repo/CLAUDE.md", Size: 9, SHA256: setupSum("c"),
			Tracked: true, Status: "repo_tracked", Reason: "repo_tracked", KeptInPlace: []string{}, ProjectID: "mer"},
	}
	if !reflect.DeepEqual(resp.Entries, wantEntries) {
		t.Fatalf("entries = %+v\nwant %+v", resp.Entries, wantEntries)
	}
	wantRoots := []controllers.LadoSetupRootResponse{
		{Tool: "claude-code", Location: "claude_dir", Path: "/home/me/.claude", Found: true},
		{Tool: "codex", Location: "codex_home", Path: "/home/me/.codex", Error: "permission denied"},
	}
	wantItems := []controllers.LadoSetupItemStateResponse{{
		ItemID: "item-1", Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", VersionID: "version-1", Version: 1, SHA256: setupSum("a"),
		Source: controllers.LadoRuleSourceResponse{Scope: "global", State: "off"}, Global: "off", Project: "inherit", Task: "inherit",
	}}
	wantDrift := []controllers.LadoSetupDriftResponse{{ItemID: "item-1", Kind: "edited", Tool: "claude-code", ItemKind: "instructions",
		Name: "CLAUDE.md", Path: "/state/lado/harness/claude/CLAUDE.md", SHA256: setupSum("f")}}
	if !reflect.DeepEqual(resp.Roots, wantRoots) || !reflect.DeepEqual(resp.Items, wantItems) || !reflect.DeepEqual(resp.Drift, wantDrift) ||
		!reflect.DeepEqual(resp.Projects, []controllers.LadoSetupProjectScanResponse{{ID: "mer", Name: "Mercury", Path: "/repo"}}) {
		t.Fatalf("roots %+v, items %+v, drift %+v, projects %+v", resp.Roots, resp.Items, resp.Drift, resp.Projects)
	}
	if len(resp.Adoptions) != 1 {
		t.Fatalf("adoptions = %+v", resp.Adoptions)
	}
	if a := resp.Adoptions[0]; a.ID != "adoption-1" || a.ItemCount != 3 || a.State != "completed" || a.RestoredAt == nil ||
		!a.RestoredAt.Equal(ladoSetupTime.Add(time.Hour)) || a.PlanSHA256 != setupSum("d") || a.ManifestSHA256 != setupSum("e") || a.OS != "linux" {
		t.Fatalf("adoption = %+v", a)
	}

	// The scan reads the setup again and answers the same way.
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/setup/scan", "")
	if status != http.StatusOK || !reflect.DeepEqual(svc.gotRescan, []bool{false, true}) {
		t.Fatalf("scan: status = %d, body %s, rescans %v", status, body, svc.gotRescan)
	}
	if scanned := decodeLadoJSON[controllers.LadoSetupInventoryResponse](t, body); !reflect.DeepEqual(scanned, resp) {
		t.Fatalf("scan = %+v, want the inventory's shape %+v", scanned, resp)
	}

	// An empty inventory still lists [] everywhere.
	svc.overview = ladosetup.Overview{}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/inventory", "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"entries", "roots", "projects", "drift", "adoptions", "items"} {
		if string(raw[key]) != "[]" {
			t.Fatalf("%s = %s in %s, want []", key, raw[key], body)
		}
	}
	if string(raw["verified"]) != "{}" {
		t.Fatalf("verified = %s, want {}", raw["verified"])
	}

	svc.err = apierr.Unavailable(ladosetup.CodeUnavailable, "LADO's local setup is not available in this daemon.")
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/inventory", "")
	wantLadoErrorCode(t, body, status, http.StatusServiceUnavailable, "LADO_SETUP_UNAVAILABLE")
}

const setupAdoptPath = "/api/v1/lado/setup/adoptions"

// An adoption preview needs no confirmation. The adoption itself does: the
// desktop app's proof over the exact body, for the plan's hash.
func TestLadoAPI_AdoptNeedsPresence(t *testing.T) {
	plan := setupSum("a")
	svc := &fakeLadoSetupService{adopt: ladosetup.AdoptResult{
		Preview: ladosetup.AdoptionPreview{
			PlanSHA256: plan, OS: "linux",
			Entries: []ladosetup.PreviewEntry{{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "hooks",
				Action: domain.LadoSetupActionMove, OriginPath: "/home/me/.claude/settings.json#hooks", SHA256: setupSum("b"), Size: 40,
				ManagedPath: "/state/lado/harness/claude/settings.json#hooks", Delivery: ladosetup.DeliveryHome}},
			Rewrites: []ladosetup.HookRewrite{{Setting: "hooks", Event: "PreToolUse", Matcher: "Bash", Before: "~/.claude/hooks/check.sh",
				After: `"$CLAUDE_CONFIG_DIR"/hooks/check.sh`, Paths: []string{"hooks/check.sh"}}},
			Preconditions: ladosetup.Preconditions{OS: "linux", Processes: []ladosetup.ToolProcess{{
				Process: procmem.Process{PID: 4242, Command: "/usr/local/bin/claude --api-key sk-secret-value"}, Tool: domain.LadoSetupToolClaudeCode,
			}}},
			Warnings: []string{ladosetup.WarningDirectUse},
		},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc, LadoPresence: presenceVerifier()})
	entries := `"entries":[{"tool":"claude-code","kind":"setting","name":"hooks","action":"move"}]`
	wantEntries := []ladosetup.AdoptEntry{{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "hooks", Action: domain.LadoSetupActionMove}}

	t.Run("a preview without a proof", func(t *testing.T) {
		body, status, _ := doRequest(t, srv, "POST", setupAdoptPath, "{"+entries+`,"dryRun":true}`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		if got := svc.gotAdopt; !got.DryRun || got.Presence != nil || !reflect.DeepEqual(got.Entries, wantEntries) || svc.gotProof != nil {
			t.Fatalf("service got %+v (presence set: %v), proof %+v", got, got.Presence != nil, svc.gotProof)
		}
		resp := decodeLadoJSON[controllers.LadoSetupAdoptResponse](t, body)
		pre := resp.Preview.Preconditions
		if resp.Applied || resp.Preview.PlanSHA256 != plan || len(resp.Preview.Entries) != 1 || resp.Preview.Entries[0].Delivery != "home" ||
			len(resp.Preview.Rewrites) != 1 || resp.Preview.Rewrites[0].After != `"$CLAUDE_CONFIG_DIR"/hooks/check.sh` ||
			pre.OK || len(pre.Processes) != 1 || pre.Processes[0] != (controllers.LadoSetupRunningProcessResponse{PID: 4242, Name: "claude", Tool: "claude-code"}) ||
			!reflect.DeepEqual(resp.Preview.Warnings, []string{"LADO_SETUP_DIRECT_USE_STOPS"}) {
			t.Fatalf("preview = %+v", resp)
		}
		// A program shows by its name, never by a command line that can
		// carry secrets.
		if strings.Contains(string(body), "sk-secret-value") {
			t.Fatalf("the preview carries a command line: %s", body)
		}
	})
	apply := "{" + entries + `, "planSha256": "` + plan + `" }`
	t.Run("an adoption without a proof", func(t *testing.T) {
		body, status, _ := doRequest(t, srv, "POST", setupAdoptPath, apply)
		wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REQUIRED")
		if svc.gotProof != nil {
			t.Fatalf("the service got a proof: %+v", svc.gotProof)
		}
	})
	t.Run("a proof over another body", func(t *testing.T) {
		other := "{" + entries + `,"planSha256":"` + setupSum("9") + `"}`
		body, status := signedRequest(t, srv, "POST", setupAdoptPath, other, apply)
		wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
		if svc.gotProof != nil {
			t.Fatalf("the service got a proof: %+v", svc.gotProof)
		}
	})
	t.Run("a proof over the exact body", func(t *testing.T) {
		svc.adopt.Applied, svc.adopt.AdoptionID, svc.adopt.State = true, "adoption-1", domain.LadoSetupAdoptionCompleted
		body, status := signedRequest(t, srv, "POST", setupAdoptPath, apply, apply)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		got := svc.gotAdopt
		if got.DryRun || got.PlanSHA256 != plan || !reflect.DeepEqual(got.Entries, wantEntries) {
			t.Fatalf("service got %+v", got)
		}
		if p := svc.gotProof; p == nil || p.Action != domain.LadoPresenceSetupAdopt || p.SubjectID != plan || p.Nonce != "AAAAAAAAAAAAAAAAAAAAAA" {
			t.Fatalf("proof = %+v, want the app's for setup.adopt of the plan", p)
		}
		if resp := decodeLadoJSON[controllers.LadoSetupAdoptResponse](t, body); !resp.Applied || resp.AdoptionID != "adoption-1" || resp.State != "completed" {
			t.Fatalf("response = %+v", resp)
		}
	})
	t.Run("a body with an unknown field", func(t *testing.T) {
		before := svc.calls
		body, status, _ := doRequest(t, srv, "POST", setupAdoptPath, "{"+entries+`,"force":true}`)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
		if svc.calls != before {
			t.Fatal("the service was called for a body with an unknown field")
		}
	})
}

// A restore needs the desktop app's proof over the exact body, which is
// empty or an empty object, for the adoption's id.
func TestLadoAPI_RestoreNeedsPresence(t *testing.T) {
	svc := &fakeLadoSetupService{restore: ladosetup.RestoreResult{
		AdoptionID: "adoption-1", Applied: true, RestoredAt: ladoSetupTime,
		Report: ladosetup.RestoreReport{Entries: []ladosetup.RestoredEntry{
			{ItemID: "item-1", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md",
				Root: domain.LadoSetupLocationClaudeDir, Path: "CLAUDE.md", Status: ladosetup.RestoreBeside, Beside: "CLAUDE.md.lado-restored"},
		}},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc, LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/setup/adoptions/adoption-1/restore"

	body, status, _ := doRequest(t, srv, "POST", path, "")
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REQUIRED")
	body, status = signedRequest(t, srv, "POST", path, `{"x":1}`, "")
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
	if svc.gotProof != nil {
		t.Fatalf("the service got a proof: %+v", svc.gotProof)
	}

	for _, sent := range []string{"", "{}"} {
		svc.gotProof = nil
		body, status = signedRequest(t, srv, "POST", path, sent, sent)
		if status != http.StatusOK {
			t.Fatalf("restore with body %q: status = %d, body %s", sent, status, body)
		}
		if p := svc.gotProof; svc.gotID != "adoption-1" || p == nil || p.Action != domain.LadoPresenceSetupRestore || p.SubjectID != "adoption-1" {
			t.Fatalf("service got %q with proof %+v", svc.gotID, p)
		}
	}
	resp := decodeLadoJSON[controllers.LadoSetupRestoreResponse](t, body)
	wantReport := controllers.LadoSetupRestoreReportResponse{Entries: []controllers.LadoSetupRestoredEntryResponse{{
		ItemID: "item-1", Tool: "claude-code", Kind: "instructions", Name: "CLAUDE.md", Root: "claude_dir", Path: "CLAUDE.md",
		Status: "restored_beside", Beside: "CLAUDE.md.lado-restored",
	}}}
	if !resp.Applied || resp.AdoptionID != "adoption-1" || !resp.RestoredAt.Equal(ladoSetupTime) || !reflect.DeepEqual(resp.Report, wantReport) || resp.Warnings == nil {
		t.Fatalf("response = %+v", resp)
	}

	before := svc.calls
	body, status = signedRequest(t, srv, "POST", path, `{"force":true}`, `{"force":true}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.calls != before {
		t.Fatal("the service was called for a body with a field")
	}

	// The service's refusals keep their codes and details.
	svc.err = apierr.Conflict(ladosetup.CodeToolRunning, "Close the sessions listed.",
		map[string]any{"sessions": []map[string]any{{"id": "mer-1", "tool": "codex"}}, "processes": []map[string]any{}})
	body, status = signedRequest(t, srv, "POST", path, "", "")
	wantLadoErrorCode(t, body, status, http.StatusConflict, "LADO_SETUP_TOOL_RUNNING")
	if apiErr := decodeLadoJSON[envelope.APIError](t, body); !strings.Contains(string(body), `"sessions":[{"id":"mer-1","tool":"codex"}]`) || apiErr.Details["processes"] == nil {
		t.Fatalf("envelope = %s, want the running sessions and processes", body)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
		want   map[string]any
	}{
		{apierr.Conflict(ladosetup.ConflictPathUsedInPlace, "Keep that file in place.", map[string]any{"key": "apiKeyHelper", "path": "hooks/key.sh", "origin": "/home/me/.claude/settings.json#apiKeyHelper"}),
			http.StatusConflict, "LADO_SETUP_PATH_USED_IN_PLACE", map[string]any{"key": "apiKeyHelper", "path": "hooks/key.sh"}},
		{apierr.Invalid(ladosetup.CodePairRequired, "Adopt both of them together.", map[string]any{"name": "AGENTS.md", "pair": "AGENTS.override.md"}),
			http.StatusBadRequest, "LADO_SETUP_PAIR_REQUIRED", map[string]any{"name": "AGENTS.md", "pair": "AGENTS.override.md"}},
	} {
		svc.err = tc.err
		body, status = signedRequest(t, srv, "POST", path, "", "")
		wantLadoErrorCode(t, body, status, tc.status, tc.code)
		details := decodeLadoJSON[envelope.APIError](t, body).Details
		for k, v := range tc.want {
			if details[k] != v {
				t.Fatalf("%s: details %v, want %s=%v", tc.code, details, k, v)
			}
		}
	}
}

// Every change route reads the raw body, hands the service the change and a
// presence check over exactly those bytes, and answers its errors in the
// envelope with their codes and details.
func TestLadoAPI_SetupChangesPassTheExactBody(t *testing.T) {
	presence := &recordingPresence{}
	change := ladosetup.ChangeResult{
		Op: ladosetup.OpEdit, ChangeSHA256: setupSum("1"), NeedsPresence: true, Applied: true, Actor: "human",
		Item: domain.LadoSetupItem{ID: "item-1", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "hooks",
			OriginPath: "/home/me/.claude/settings.json#hooks", AdoptionID: "adoption-1", CreatedAt: ladoSetupTime},
		Version: &domain.LadoSetupItemVersion{ID: "version-2", ItemID: "item-1", Version: 2, ContentKind: domain.LadoSetupContentJSON,
			SHA256: setupSum("2"), Size: 12, Actor: "human", CreatedAt: ladoSetupTime},
		Before: &ladosetup.ChangeContent{ContentKind: domain.LadoSetupContentJSON, SHA256: setupSum("3"), Size: 2, Text: "{}"},
		After:  &ladosetup.ChangeContent{ContentKind: domain.LadoSetupContentJSON, SHA256: setupSum("2"), Size: 12, Text: "{\n  \"x\": 1\n}"},
		ConflictsAdded: []ladosetup.Conflict{{Code: ladosetup.ConflictHookMayBlockTools, Severity: domain.LadoSeverityWarning, ItemID: "item-1",
			Detail: "PreToolUse", ContentSHA256: setupSum("2"), Details: map[string]string{"item": "item-1", "event": "PreToolUse", "matcher": "Bash"}}},
		Impact: ladopolicy.Impact{Projects: []ladopolicy.ImpactProject{{ID: "mer", Name: "Mercury"}}, Tasks: []ladopolicy.ImpactTask{}},
	}
	svc := &fakeLadoSetupService{change: change, needsPresence: true}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc, LadoPresence: presence})
	header := map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"}
	drift := setupSum("4")

	for _, tc := range []struct {
		name, method, path, body string
		want                     ladosetup.Change
	}{
		{"create", "POST", "/api/v1/lado/setup/items",
			"{ \"tool\":\"claude-code\", \"kind\":\"mcp_server\", \"name\":\"search\",\n \"content\": {\"url\":  \"https://search\"} }",
			ladosetup.Change{Op: ladosetup.OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindMCPServer, Name: "search",
				Content: json.RawMessage(`{"url":  "https://search"}`)}},
		{"edit", "PATCH", "/api/v1/lado/setup/items/item-1",
			"{\"content\": \"# me\\n\",  \"baseVersion\": 2}",
			ladosetup.Change{Op: ladosetup.OpEdit, ItemID: "item-1", Content: json.RawMessage(`"# me\n"`), BaseVersion: 2}},
		{"binding", "PUT", "/api/v1/lado/setup/bindings",
			"{\"itemId\":\"item-1\", \"scope\":\"project\", \"scopeId\":\"mer\", \"state\":\"force_on\"}",
			ladosetup.Change{Op: ladosetup.OpBinding, ItemID: "item-1",
				Binding: ladosetup.ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingForceOn}}},
		{"drift keep", "POST", "/api/v1/lado/setup/items/item-1/drift",
			"{ \"action\": \"keep\", \"sha256\": \"" + drift + "\" }",
			ladosetup.Change{Op: ladosetup.OpDriftKeep, ItemID: "item-1", DriftSHA256: drift}},
		{"drift revert", "POST", "/api/v1/lado/setup/items/item-1/drift",
			`{"action":"revert"}`,
			ladosetup.Change{Op: ladosetup.OpDriftRevert, ItemID: "item-1"}},
		{"accept", "POST", "/api/v1/lado/setup/conflicts/acceptances",
			`{"itemId":"item-1","code":"LADO_SETUP_DENIES_GIT","detail":"Bash(git push:*)","scope":"task","scopeId":"lado-task-1"}`,
			ladosetup.Change{Op: ladosetup.OpAccept, ItemID: "item-1", Acceptance: ladosetup.ChangeAcceptance{
				Code: "LADO_SETUP_DENIES_GIT", Detail: "Bash(git push:*)", Scope: domain.LadoScopeTask, ScopeID: "lado-task-1"}}},
		{"delete", "DELETE", "/api/v1/lado/setup/items/item-1", "",
			ladosetup.Change{Op: ladosetup.OpDelete, ItemID: "item-1"}},
		{"revoke", "DELETE", "/api/v1/lado/setup/conflicts/acceptances/acceptance-1", "",
			ladosetup.Change{Op: ladosetup.OpRevoke, AcceptanceID: "acceptance-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*presence = recordingPresence{}
			svc.gotProof = nil
			body, status := ladoHeaderRequest(t, srv, tc.method, tc.path, tc.body, header)
			if status != http.StatusOK {
				t.Fatalf("status = %d, body %s", status, body)
			}
			if !reflect.DeepEqual(svc.gotChange, tc.want) {
				t.Fatalf("service got %+v (content %s)\nwant %+v (content %s)", svc.gotChange, svc.gotChange.Content, tc.want, tc.want.Content)
			}
			if presence.method != tc.method || presence.path != tc.path || string(presence.body) != tc.body || presence.header != "v1.proof-from-the-app" ||
				presence.action != domain.LadoPresenceSetupChange || presence.subject != setupSum("1") {
				t.Fatalf("verifier got %s %s body %q header %q for %s %s", presence.method, presence.path, presence.body, presence.header, presence.action, presence.subject)
			}
			if svc.gotProof == nil || svc.gotProof.Nonce != "BBBBBBBBBBBBBBBBBBBBBB" {
				t.Fatalf("the service got proof %+v", svc.gotProof)
			}
		})
	}

	// A dry run reaches the service as one and asks for no confirmation.
	*presence = recordingPresence{}
	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/lado/setup/items/item-1?dryRun=true", "")
	if status != http.StatusOK || !reflect.DeepEqual(svc.gotChange, ladosetup.Change{Op: ladosetup.OpDelete, ItemID: "item-1", DryRun: true}) || presence.action != "" {
		t.Fatalf("delete dry run: status %d body %s, service got %+v, verifier asked for %q", status, body, svc.gotChange, presence.action)
	}
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/lado/setup/items/item-1", `{"baseVersion":2,"content":"x","dryRun":true}`)
	if status != http.StatusOK || !svc.gotChange.DryRun || presence.action != "" {
		t.Fatalf("edit dry run: status %d body %s, service got %+v, verifier asked for %q", status, body, svc.gotChange, presence.action)
	}

	// The change as the app shows it.
	resp := decodeLadoJSON[controllers.LadoSetupChangeResponse](t, body)
	wantItem := controllers.LadoSetupItemResponse{ID: "item-1", Tool: "claude-code", Kind: "setting", Name: "hooks",
		OriginPath: "/home/me/.claude/settings.json#hooks", AdoptionID: "adoption-1", Live: true, CreatedAt: ladoSetupTime}
	wantAdded := []controllers.LadoSetupConflictResponse{{Code: "LADO_SETUP_HOOK_MAY_BLOCK_TOOLS", Severity: "warning", ItemID: "item-1",
		Detail: "PreToolUse", ContentSHA256: setupSum("2"), Details: map[string]string{"item": "item-1", "event": "PreToolUse", "matcher": "Bash"}}}
	if resp.Op != "edit" || resp.ChangeSHA256 != setupSum("1") || !resp.NeedsPresence || !resp.Applied || resp.Actor != "human" ||
		resp.Item != wantItem || resp.Version == nil || resp.Version.ID != "version-2" || resp.Version.Version != 2 ||
		resp.Before == nil || resp.Before.Text != "{}" || resp.After == nil || resp.After.Text != "{\n  \"x\": 1\n}" || resp.After.Files == nil ||
		!reflect.DeepEqual(resp.ConflictsAdded, wantAdded) || resp.ConflictsRemoved == nil ||
		!reflect.DeepEqual(resp.Impact, controllers.LadoRuleImpactResponse{Projects: []controllers.LadoImpactProjectResponse{{ID: "mer", Name: "Mercury"}}, Tasks: []controllers.LadoImpactTaskResponse{}}) ||
		resp.Binding != nil || resp.Acceptance != nil || resp.Warnings == nil {
		t.Fatalf("change response = %+v", resp)
	}

	// The service's refusals keep their status, code and details.
	for _, tc := range []struct {
		err    error
		status int
		code   string
		key    string
		value  any
	}{
		{apierr.Conflict(ladosetup.CodeItemChanged, "The item got another version meanwhile.", map[string]any{"item": "item-1"}), http.StatusConflict, "LADO_SETUP_ITEM_CHANGED", "item", "item-1"},
		{apierr.Invalid(ladosetup.CodeContentInvalid, "This content does not fit the item.", map[string]any{"reason": "front_matter", "field": "name"}), http.StatusBadRequest, "LADO_SETUP_CONTENT_INVALID", "reason", "front_matter"},
		{apierr.Forbidden("LADO_PRESENCE_REQUIRED", "Confirm this in the AO desktop app."), http.StatusForbidden, "LADO_PRESENCE_REQUIRED", "", nil},
		{apierr.Conflict(ladosetup.CodeOSUnverified, "Creating setup items is not available on this OS yet.", map[string]any{"os": "darwin", "tools": []string{"codex"}}), http.StatusConflict, "LADO_SETUP_OS_UNVERIFIED", "os", "darwin"},
		{apierr.Invalid(ladosetup.CodeLoginKey, "Logins stay where Claude Code keeps them.", map[string]any{"key": "AWS_REGION"}), http.StatusBadRequest, "LADO_SETUP_LOGIN_KEY", "key", "AWS_REGION"},
		{apierr.NotFound(ladosetup.CodeNotFound, "LADO does not manage this setup item."), http.StatusNotFound, "LADO_SETUP_NOT_FOUND", "", nil},
		{apierr.Unavailable(ladosetup.CodeUnavailable, "LADO's local setup is not available in this daemon."), http.StatusServiceUnavailable, "LADO_SETUP_UNAVAILABLE", "", nil},
	} {
		svc.err = tc.err
		body, status := ladoHeaderRequest(t, srv, "PATCH", "/api/v1/lado/setup/items/item-1", `{"baseVersion":1,"content":"x"}`, header)
		wantLadoErrorCode(t, body, status, tc.status, tc.code)
		if apiErr := decodeLadoJSON[envelope.APIError](t, body); tc.key != "" && apiErr.Details[tc.key] != tc.value {
			t.Fatalf("envelope = %s, want details %s = %v", body, tc.key, tc.value)
		}
	}
	svc.err = nil

	// The controller refuses what it cannot read before the service sees it.
	before := svc.calls
	for _, tc := range []struct{ method, path, body, code string }{
		{"PATCH", "/api/v1/lado/setup/items/item-1", `{"baseVersion":1,"content":"x","name":"y"}`, "INVALID_JSON"},
		{"PUT", "/api/v1/lado/setup/bindings", `{"itemId":"item-1","scope":"global","state":"on"`, "INVALID_JSON"},
		{"POST", "/api/v1/lado/setup/items/item-1/drift", `{"action":"merge"}`, "LADO_SETUP_CHANGE_INVALID"},
		{"DELETE", "/api/v1/lado/setup/items/item-1?dryRun=maybe", "", "INVALID_QUERY"},
		{"DELETE", "/api/v1/lado/setup/conflicts/acceptances/acceptance-1?dryRun=2", "", "INVALID_QUERY"},
	} {
		body, status, _ := doRequest(t, srv, tc.method, tc.path, tc.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, tc.code)
	}
	if svc.calls != before {
		t.Fatal("the service was called for a request the controller refuses")
	}
}

// Without a service every setup route answers 501 with its operation from
// the spec, so each route has one.
func TestLadoAPI_SetupRoutesWithoutService(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/setup/inventory", ""},
		{"POST", "/api/v1/lado/setup/scan", ""},
		{"GET", "/api/v1/lado/setup/items?projectId=mer", ""},
		{"POST", "/api/v1/lado/setup/items", `{"tool":"claude-code","kind":"instructions","name":"A.md","content":"a"}`},
		{"GET", "/api/v1/lado/setup/items/item-1", ""},
		{"PATCH", "/api/v1/lado/setup/items/item-1", `{"baseVersion":1,"content":"a"}`},
		{"DELETE", "/api/v1/lado/setup/items/item-1", ""},
		{"POST", "/api/v1/lado/setup/items/item-1/drift", `{"action":"revert"}`},
		{"PUT", "/api/v1/lado/setup/bindings", `{"itemId":"item-1","scope":"global","state":"off"}`},
		{"GET", "/api/v1/lado/setup/conflicts", ""},
		{"POST", "/api/v1/lado/setup/conflicts/acceptances", `{"itemId":"item-1","code":"LADO_SETUP_DENIES_GIT","scope":"global"}`},
		{"DELETE", "/api/v1/lado/setup/conflicts/acceptances/acceptance-1", ""},
		{"POST", "/api/v1/lado/setup/adoptions", `{"entries":[],"dryRun":true}`},
		{"GET", "/api/v1/lado/setup/adoptions/adoption-1", ""},
		{"POST", "/api/v1/lado/setup/adoptions/adoption-1/restore", ""},
	} {
		body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
		var resp struct {
			Code string          `json:"code"`
			Spec json.RawMessage `json:"spec"`
		}
		if status != http.StatusNotImplemented || json.Unmarshal(body, &resp) != nil || resp.Code != "NOT_IMPLEMENTED" || len(resp.Spec) == 0 {
			t.Errorf("%s %s status = %d, body %s; want 501 with its operation", route.method, route.path, status, body)
		}
	}
}

// The scope views, one item with its versions and one adoption with its
// backup.
func TestLadoAPI_SetupViews(t *testing.T) {
	svc := &fakeLadoSetupService{
		items: sampleSetupOverview().Items,
		conflicts: []ladosetup.Conflict{{Code: ladosetup.ConflictMCPNameTaken, Severity: domain.LadoSeverityBlocking, ItemID: "item-2",
			ContentSHA256: setupSum("5"), Details: map[string]string{"item": "item-2"}}},
		item: ladosetup.ItemView{
			Item:        domain.LadoSetupItem{ID: "item-1", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "review", CreatedAt: ladoSetupTime, DeletedAt: ladoSetupTime.Add(time.Hour)},
			ManagedPath: "",
			Versions: []ladosetup.VersionView{
				{Version: domain.LadoSetupItemVersion{ID: "version-1", ItemID: "item-1", Version: 1, ContentKind: domain.LadoSetupContentTree, SHA256: setupSum("6"), Size: 10, Actor: "human", CreatedAt: ladoSetupTime},
					Content: &ladosetup.ChangeContent{ContentKind: domain.LadoSetupContentTree, SHA256: setupSum("6"), Size: 10,
						Files: []domain.LadoSetupTreeEntry{{Path: "SKILL.md", Mode: domain.LadoSetupModeFile, Size: 7, SHA256: setupSum("7")}, {Path: "run.sh", Mode: domain.LadoSetupModeExec, Size: 3, SHA256: setupSum("8")}}}},
				{Version: domain.LadoSetupItemVersion{ID: "version-2", ItemID: "item-1", Version: 2, ContentKind: domain.LadoSetupContentTree, SHA256: setupSum("9"), Size: 11, Actor: "api", CreatedAt: ladoSetupTime}},
			},
			Warnings: []string{ladosetup.WarningPreviewIncomplete},
		},
		adoption: ladosetup.AdoptionView{
			Adoption: domain.LadoSetupAdoption{ID: "adoption-1", CreatedAt: ladoSetupTime, Actor: "human", OS: "linux", State: domain.LadoSetupAdoptionPartial, ErrorMessage: "remove hooks/check.sh: busy"},
			Items:    []domain.LadoSetupItem{{ID: "item-1", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "review", AdoptionID: "adoption-1", CreatedAt: ladoSetupTime}},
		},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/setup/items?taskId=lado-task-1", "")
	if status != http.StatusOK || svc.gotScope != [2]string{"", "lado-task-1"} || len(decodeLadoJSON[controllers.LadoSetupItemListResponse](t, body).Items) != 1 {
		t.Fatalf("items: status %d body %s scope %v", status, body, svc.gotScope)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/conflicts?projectId=mer", "")
	conflicts := decodeLadoJSON[controllers.LadoSetupConflictListResponse](t, body).Conflicts
	if status != http.StatusOK || svc.gotConflicts != (ladosetup.ScopeConflictsInput{ProjectID: "mer"}) || len(conflicts) != 1 ||
		conflicts[0].Code != "LADO_SETUP_MCP_NAME_TAKEN" || conflicts[0].Severity != "blocking" || conflicts[0].Details["item"] != "item-2" {
		t.Fatalf("conflicts: status %d body %s input %+v", status, body, svc.gotConflicts)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/items/item-1", "")
	item := decodeLadoJSON[controllers.LadoSetupItemDetailResponse](t, body)
	if status != http.StatusOK || svc.gotID != "item-1" || item.Item.Live || item.Item.DeletedAt == nil || item.ManagedPath != "" || len(item.Versions) != 2 ||
		item.Versions[0].Content == nil || len(item.Versions[0].Content.Files) != 2 || item.Versions[0].Content.Files[1].Mode != "exec" ||
		item.Versions[1].Content != nil || item.Versions[1].Version.Actor != "api" || !reflect.DeepEqual(item.Warnings, []string{"LADO_SETUP_PREVIEW_INCOMPLETE"}) {
		t.Fatalf("item: status %d body %s", status, body)
	}

	// A backup LADO cannot read leaves the adoption readable.
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/adoptions/adoption-1", "")
	adoption := decodeLadoJSON[controllers.LadoSetupAdoptionDetailResponse](t, body)
	if status != http.StatusOK || svc.gotID != "adoption-1" || adoption.Adoption.State != "partial" || adoption.Adoption.ItemCount != 1 ||
		adoption.BackupReadable || adoption.Backup == nil || len(adoption.Items) != 1 || adoption.Report != nil {
		t.Fatalf("adoption: status %d body %s", status, body)
	}
	svc.adoption.Backup = []ladosetup.BackupEntry{{Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "review",
		Action: domain.LadoSetupActionMove, Root: domain.LadoSetupLocationClaudeDir, Path: "skills/review", Type: "dir", Size: 10, SHA256: setupSum("6")}}
	svc.adoption.Report = &ladosetup.RestoreReport{Entries: []ladosetup.RestoredEntry{{ItemID: "item-1", Status: ladosetup.RestoreRestored, Root: domain.LadoSetupLocationClaudeDir, Path: "skills/review"}}}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/adoptions/adoption-1", "")
	adoption = decodeLadoJSON[controllers.LadoSetupAdoptionDetailResponse](t, body)
	if !adoption.BackupReadable || len(adoption.Backup) != 1 || adoption.Backup[0].Path != "skills/review" || adoption.Backup[0].Type != "dir" ||
		adoption.Report == nil || len(adoption.Report.Entries) != 1 || adoption.Report.Entries[0].Status != "restored" {
		t.Fatalf("adoption with its backup: %s", body)
	}
}

// The effective preview carries the agent tool's setup and, for an approved
// task, the setup fixed then next to now; an execution names its setup.
func TestLadoAPI_EffectiveShowsTheSetup(t *testing.T) {
	item := sampleSetupOverview().Items[0]
	item.On, item.Source, item.Global = true, ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}, domain.LadoBindingOn
	view := ladopolicy.EffectiveView{
		Resolution: ladopolicy.Resolution{
			Ceiling: domain.PermissionModeAuto, Mode: domain.PermissionModeAuto,
			Setup: ladopolicy.SetupResolution{
				Items:          []ladopolicy.SetupItemState{item},
				Problems:       []domain.LadoPolicyProblem{{Code: ladosetup.ConflictDeniesAO, Severity: domain.LadoSeverityBlocking, Details: map[string]string{"item": "item-2", "rule": "Bash(ao:*)"}}},
				Accepted:       []ladopolicy.AcceptedConflict{{Code: ladosetup.ConflictDeniesGit, ItemID: "item-2", Detail: "Bash(git push:*)", AcceptanceID: "acceptance-1"}},
				AOSystemSHA256: setupSum("a"),
			},
			Content: ladopolicy.SnapshotContent{ProjectID: "mer", TaskID: "lado-task-1"},
		},
		Approved: &ladopolicy.ApprovedView{
			Content: ladopolicy.SnapshotContent{
				Setup: []ladopolicy.SnapshotSetupItem{{ItemID: "item-3", Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "enabledPlugins",
					VersionID: "version-3", Version: 1, SHA256: setupSum("b"), Source: ladopolicy.SnapshotSource{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn},
					InstalledSHA256: setupSum("c")}},
				AOSystem:          ladopolicy.SnapshotAOSystem{SHA256: setupSum("d")},
				AcceptedConflicts: []ladopolicy.SnapshotAcceptedConflict{},
			},
			SetupAdded: []domain.LadoSetupItemID{"item-1"}, SetupRemoved: []domain.LadoSetupItemID{"item-3"},
		},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: &fakeLadoRuleService{effective: view}})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/effective?taskId=lado-task-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body)
	setup := resp.Setup
	if len(setup.Items) != 1 || !setup.Items[0].On || setup.Items[0].Global != "on" || setup.AOSystemSHA256 != setupSum("a") ||
		len(setup.Conflicts) != 1 || setup.Conflicts[0].Code != "LADO_SETUP_DENIES_AO" || setup.Conflicts[0].Details["rule"] != "Bash(ao:*)" ||
		!reflect.DeepEqual(setup.Accepted, []controllers.LadoSetupAcceptedConflictResponse{{Code: "LADO_SETUP_DENIES_GIT", ItemID: "item-2", Detail: "Bash(git push:*)", AcceptanceID: "acceptance-1"}}) {
		t.Fatalf("setup = %+v", setup)
	}
	approved := resp.Approved
	wantSnapshot := []controllers.LadoSnapshotSetupItemResponse{{ItemID: "item-3", Tool: "claude-code", Kind: "setting", Name: "enabledPlugins",
		VersionID: "version-3", Version: 1, SHA256: setupSum("b"), Source: controllers.LadoRuleSourceResponse{Scope: "global", State: "on"}, InstalledSHA256: setupSum("c")}}
	if approved == nil || !reflect.DeepEqual(approved.SetupAdded, []string{"item-1"}) || !reflect.DeepEqual(approved.SetupRemoved, []string{"item-3"}) ||
		len(approved.SetupChanged) != 0 || approved.SetupChanged == nil || !reflect.DeepEqual(approved.Setup, wantSnapshot) || approved.AOSystemSHA256 != setupSum("d") || approved.Accepted == nil {
		t.Fatalf("approved = %+v", approved)
	}
}

// The adoption detail carries what holds a restore back, in the adoption
// preview's shape, so the app can show it before the confirmation.
func TestLadoAPI_AdoptionShowsRestorePreconditions(t *testing.T) {
	svc := &fakeLadoSetupService{adoption: ladosetup.AdoptionView{
		Adoption: domain.LadoSetupAdoption{ID: "adoption-1", CreatedAt: ladoSetupTime, Actor: "human", OS: "linux", State: domain.LadoSetupAdoptionCompleted},
		Preconditions: &ladosetup.Preconditions{
			OS:        "darwin",
			Sessions:  []ladosetup.LiveSession{{ID: "s-1", ProjectID: "mer", Tool: domain.LadoSetupToolClaudeCode}},
			Processes: []ladosetup.ToolProcess{{Process: procmem.Process{PID: 42, Command: "/usr/bin/codex --secret-flag"}, Tool: domain.LadoSetupToolCodex}},
		},
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoSetup: svc})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/setup/adoptions/adoption-1", "")
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoSetupAdoptionDetailResponse](t, body).Preconditions
	want := &controllers.LadoSetupPreconditionsResponse{
		OS: "darwin", Unverified: []string{},
		Sessions:  []controllers.LadoSetupRunningSessionResponse{{ID: "s-1", Tool: "claude-code"}},
		Processes: []controllers.LadoSetupRunningProcessResponse{{PID: 42, Name: "codex", Tool: "codex"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preconditions %+v, want %+v", got, want)
	}
	if strings.Contains(string(body), "secret-flag") {
		t.Fatalf("the detail shows a command line: %s", body)
	}
	// A restored adoption has none.
	svc.adoption.Preconditions = nil
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/setup/adoptions/adoption-1", "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["preconditions"]; ok {
		t.Fatalf("preconditions without any: %s", body)
	}
}
