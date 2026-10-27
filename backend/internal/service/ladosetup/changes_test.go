package ladosetup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The recording store keeps the audit events of the changes too.
func (r *recordingStore) CreateLadoSetupItem(ctx context.Context, item domain.LadoSetupItem, version domain.LadoSetupItemVersion, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	err := r.Store.CreateLadoSetupItem(ctx, item, version, event, proof)
	return r.record(event, err)
}

func (r *recordingStore) DeleteLadoSetupItem(ctx context.Context, id domain.LadoSetupItemID, at time.Time, event domain.LadoAuditEvent) error {
	return r.record(event, r.Store.DeleteLadoSetupItem(ctx, id, at, event))
}

func (r *recordingStore) SetLadoSetupBinding(ctx context.Context, b domain.LadoSetupBinding, inherit bool, event domain.LadoAuditEvent, proof *domain.LadoPresenceProof) error {
	return r.record(event, r.Store.SetLadoSetupBinding(ctx, b, inherit, event, proof))
}

func (r *recordingStore) AcceptLadoConflict(ctx context.Context, a domain.LadoConflictAcceptance, event domain.LadoAuditEvent) error {
	return r.record(event, r.Store.AcceptLadoConflict(ctx, a, event))
}

func (r *recordingStore) RevokeLadoConflictAcceptance(ctx context.Context, id domain.LadoConflictAcceptanceID, at time.Time, event domain.LadoAuditEvent) error {
	return r.record(event, r.Store.RevokeLadoConflictAcceptance(ctx, id, at, event))
}

func (r *recordingStore) record(event domain.LadoAuditEvent, err error) error {
	if err == nil {
		r.events = append(r.events, event)
	}
	return err
}

// changeFixture is the adoption fixture with its database in a folder of its
// own, so a test can read every LADO table, and with a project and a task
// that bindings and acceptances can name.
type changeFixture struct {
	*adoptionFixture
	raw *sql.DB
	svc *Service
}

const (
	testProject = "demo"
	testTask    = "task-1"
)

func newChangeFixture(t *testing.T) *changeFixture {
	t.Helper()
	f := newAdoption(t)
	dir := t.TempDir()
	f.store = &recordingStore{Store: sqlitetest.MustOpenAt(t, dir)}
	f.deps.Store = f.store
	f.deps.Projects = fakeProjects{{ID: testProject, DisplayName: "Demo", Path: f.mkdir(filepath.Join(f.root, "demo"))}}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := f.store.CreateLadoTask(context.Background(), domain.LadoTask{
		ID: testTask, ProjectID: testProject, Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"},
		IssueRef: "github:acme/demo#12", Agent: "claude-code", CreatedAt: scanAt, UpdatedAt: scanAt,
	}); err != nil {
		t.Fatal(err)
	}
	return &changeFixture{adoptionFixture: f, raw: raw, svc: f.service()}
}

// adopt adopts entries with the fixture's service and maps the items by name.
func (f *changeFixture) adopt(entries ...AdoptEntry) map[string]domain.LadoSetupItemID {
	f.t.Helper()
	return itemIDs(f.mustAdopt(f.svc, entries...))
}

// mustApply makes a change, with a fresh confirmation when confirm is set.
func (f *changeFixture) mustApply(c Change, confirm bool) ChangeResult {
	f.t.Helper()
	var presence func(action, subject string) (domain.LadoPresenceProof, error)
	if confirm {
		presence = f.presence()
	}
	res, err := f.svc.Apply(context.Background(), c, presence)
	if err != nil {
		f.t.Fatalf("%s: %v", c.Op, err)
	}
	return res
}

// dryRun shows a change twice and checks that both runs agree.
func (f *changeFixture) dryRun(c Change) ChangeResult {
	f.t.Helper()
	c.DryRun = true
	first, err := f.svc.Apply(context.Background(), c, nil)
	if err != nil {
		f.t.Fatalf("dry run of %s: %v", c.Op, err)
	}
	again, err := f.svc.Apply(context.Background(), c, nil)
	if err != nil {
		f.t.Fatalf("second dry run of %s: %v", c.Op, err)
	}
	if len(first.ChangeSHA256) != 64 || again.ChangeSHA256 != first.ChangeSHA256 {
		f.t.Fatalf("dry runs of %s hash to %q and %q", c.Op, first.ChangeSHA256, again.ChangeSHA256)
	}
	if first.Applied || first.Actor != "" {
		f.t.Fatalf("dry run of %s applied %v as %q", c.Op, first.Applied, first.Actor)
	}
	return first
}

// dbState dumps every LADO table and the change log. Equal dumps mean the
// store wrote nothing.
func (f *changeFixture) dbState() map[string][]string {
	f.t.Helper()
	var tables []string
	for _, row := range f.query(`SELECT name FROM sqlite_master WHERE type = 'table' AND (name LIKE 'lado%' OR name = 'change_log') ORDER BY name`) {
		tables = append(tables, fmt.Sprint(row[0]))
	}
	if len(tables) < 10 {
		f.t.Fatalf("tables %v, want the LADO tables and the change log", tables)
	}
	state := map[string][]string{}
	for _, table := range tables {
		dump := []string{}
		for _, row := range f.query("SELECT * FROM " + table) {
			dump = append(dump, fmt.Sprintf("%v", row))
		}
		state[table] = dump
	}
	return state
}

// query reads every row of a query on the raw connection.
func (f *changeFixture) query(query string) [][]any {
	f.t.Helper()
	rows, err := f.raw.Query(query)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		f.t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func wantSameDB(t *testing.T, what string, before, after map[string][]string) {
	t.Helper()
	for table, rows := range before {
		if !slices.Equal(rows, after[table]) {
			t.Fatalf("%s changed %s:\nbefore %v\nafter  %v", what, table, rows, after[table])
		}
	}
}

func jsonText(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

const freshSkill = "---\nname: fresh\ndescription: Does fresh things\n---\nUse it well.\n"

// impactPolicy stands in for the rule policy: it answers every change with
// one project and keeps the changes it saw.
type impactPolicy struct{ seen []ladopolicy.SetupChange }

func (p *impactPolicy) ImpactOfSetupChange(_ context.Context, change ladopolicy.SetupChange) (ladopolicy.Impact, error) {
	p.seen = append(p.seen, change)
	return ladopolicy.Impact{Projects: []ladopolicy.ImpactProject{{ID: testProject, Name: "Demo"}}, Tasks: []ladopolicy.ImpactTask{}}, nil
}

func (p *impactPolicy) Ceiling(context.Context) (domain.PermissionMode, error) {
	return domain.PermissionModeAuto, nil
}

// Plan §12: a dry run of every op writes nothing, not to the store, the
// blobs, the managed folder or the tool folders, and hashes the change the
// same way each time. The hash is the subject the real change's
// confirmation signs.
func TestChangeDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindSkill, "review"),
	)
	md := filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md")
	f.writeMode(md, "# me, edited by hand\n", 0o600)
	// The PreToolUse hook on Bash is a warning, accepted for the task.
	block := ChangeAcceptance{Code: ConflictHookMayBlockTools, Detail: "Bash", Scope: domain.LadoScopeTask, ScopeID: testTask}
	accepted := f.mustApply(Change{Op: OpAccept, ItemID: ids["hooks"], Acceptance: block}, false)
	policy := &impactPolicy{}
	f.svc.SetPolicy(policy)

	db, lado, home := f.dbState(), treeState(t, f.state), f.homeState()
	// The edited CLAUDE.md waits for keep or revert before an edit.
	_, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: 1, Content: jsonText("# me, edited in the app\n"), DryRun: true}, nil)
	wantAPIError(t, err, CodeDriftPending)
	edit := f.dryRun(Change{Op: OpEdit, ItemID: ids["model"], BaseVersion: 1, Content: json.RawMessage(`"sonnet"`)})
	if edit.Before == nil || edit.Before.Text != `"opus"` || edit.After == nil || edit.After.Text != `"sonnet"` ||
		edit.Version == nil || edit.Version.ID != "" || edit.Version.Version != 2 || edit.NeedsPresence {
		t.Fatalf("edit dry run %+v", edit)
	}
	if len(edit.Impact.Projects) != 1 || len(policy.seen) != 2 || policy.seen[0].Item == nil || policy.seen[0].Item.Version.SHA256 != sumOf([]byte(`"sonnet"`)) {
		t.Fatalf("impact %+v from the changes %+v", edit.Impact, policy.seen)
	}
	create := f.dryRun(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "fresh", Content: jsonText(freshSkill)})
	if create.Item.ID != "" || create.Version.ItemID != "" || create.Before != nil || create.After.Text != freshSkill ||
		len(create.After.Files) != 1 || create.After.Files[0].Path != skillFile || create.NeedsPresence {
		t.Fatalf("create dry run %+v", create)
	}
	server := f.dryRun(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindMCPServer, Name: "ao",
		Content: json.RawMessage(`{"command": "ao-mcp"}`)})
	if !server.NeedsPresence || len(withCode(server.ConflictsAdded, ConflictMCPNameTaken)) != 1 || server.ConflictsAdded[0].ItemID != "" {
		t.Fatalf("a server named ao: %+v", server)
	}
	del := f.dryRun(Change{Op: OpDelete, ItemID: ids["hooks"]})
	if del.Before == nil || del.After != nil || len(withCode(del.ConflictsRemoved, ConflictHookMayBlockTools)) != 1 {
		t.Fatalf("delete dry run %+v", del)
	}
	f.dryRun(Change{Op: OpDelete, ItemID: ids["model"]})
	on := f.dryRun(Change{Op: OpBinding, ItemID: ids["check.sh"], Binding: ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: testProject, State: domain.LadoBindingForceOn}})
	if !on.NeedsPresence || on.Binding == nil || on.Binding.State != domain.LadoBindingForceOn {
		t.Fatalf("binding dry run %+v", on)
	}
	f.dryRun(Change{Op: OpBinding, ItemID: ids["CLAUDE.md"], Binding: ChangeBinding{Scope: domain.LadoScopeTask, ScopeID: testTask, State: domain.LadoBindingForceOff}})
	f.dryRun(Change{Op: OpBinding, ItemID: ids["hooks"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}})
	keep := f.dryRun(Change{Op: OpDriftKeep, ItemID: ids["CLAUDE.md"], DriftSHA256: sumOf([]byte("# me, edited by hand\n"))})
	if keep.Before.Text != "# me\n" || keep.After.Text != "# me, edited by hand\n" || keep.Version.Version != 2 || keep.Version.ID != "" {
		t.Fatalf("drift keep dry run %+v", keep)
	}
	revert := f.dryRun(Change{Op: OpDriftRevert, ItemID: ids["CLAUDE.md"]})
	if revert.Before.Text != "# me, edited by hand\n" || revert.After.Text != "# me\n" {
		t.Fatalf("drift revert dry run %+v", revert)
	}
	global := block
	global.Scope, global.ScopeID = domain.LadoScopeGlobal, ""
	accept := f.dryRun(Change{Op: OpAccept, ItemID: ids["hooks"], Acceptance: global})
	if accept.Acceptance == nil || accept.Acceptance.ID != "" || len(withCode(accept.ConflictsRemoved, ConflictHookMayBlockTools)) != 1 {
		t.Fatalf("accept dry run %+v", accept)
	}
	revoke := f.dryRun(Change{Op: OpRevoke, AcceptanceID: accepted.Acceptance.ID})
	if len(withCode(revoke.ConflictsAdded, ConflictHookMayBlockTools)) != 1 {
		t.Fatalf("revoke dry run %+v", revoke)
	}

	wantSameDB(t, "a dry run", db, f.dbState())
	if diffs := diffStates(lado, treeState(t, f.state)); len(diffs) > 0 {
		t.Fatalf("dry runs changed the state dir:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := diffStates(home, f.homeState()); len(diffs) > 0 {
		t.Fatalf("dry runs changed the tool folders:\n%s", strings.Join(diffs, "\n"))
	}

	// The real change asks the confirmation for the dry run's hash.
	var asked []string
	presence := f.presence()
	res, err := f.svc.Apply(ctx, Change{Op: OpBinding, ItemID: ids["check.sh"], Binding: ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: testProject, State: domain.LadoBindingForceOn}},
		func(action, subject string) (domain.LadoPresenceProof, error) {
			asked = append(asked, action+" "+subject)
			return presence(action, subject)
		})
	if err != nil || !res.Applied || res.ChangeSHA256 != on.ChangeSHA256 || !slices.Equal(asked, []string{domain.LadoPresenceSetupChange + " " + on.ChangeSHA256}) {
		t.Fatalf("binding = %+v (%v), asked %v, want the dry run's hash %s", res, err, asked, on.ChangeSHA256)
	}
	// Another change hashes differently.
	if off := f.dryRun(Change{Op: OpBinding, ItemID: ids["check.sh"], Binding: ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: testProject, State: domain.LadoBindingForceOff}}); off.ChangeSHA256 == on.ChangeSHA256 {
		t.Fatal("force_off hashes like force_on")
	}
}

// presenceCase is one row of decision i: a change and whether it needs the
// desktop app's confirmation.
type presenceCase struct {
	name  string
	needs bool
	// change sets up what the change needs in a fresh fixture and returns
	// it.
	change func(f *changeFixture, ids map[string]domain.LadoSetupItemID) Change
}

func createCase(name string, needs bool, kind domain.LadoSetupKind, item string, content json.RawMessage) presenceCase {
	return createToolCase(name, needs, domain.LadoSetupToolClaudeCode, kind, item, content)
}

func createToolCase(name string, needs bool, tool domain.LadoSetupTool, kind domain.LadoSetupKind, item string, content json.RawMessage) presenceCase {
	return presenceCase{name, needs, func(*changeFixture, map[string]domain.LadoSetupItemID) Change {
		return Change{Op: OpCreate, Tool: tool, Kind: kind, Name: item, Content: content}
	}}
}

func editCase(name string, needs bool, item string, content json.RawMessage) presenceCase {
	return presenceCase{name, needs, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
		return Change{Op: OpEdit, ItemID: ids[item], BaseVersion: 1, Content: content}
	}}
}

func bindingCase(name string, needs bool, item string, b ChangeBinding) presenceCase {
	return presenceCase{name, needs, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
		return Change{Op: OpBinding, ItemID: ids[item], Binding: b}
	}}
}

// driftCase edits the managed file of item by hand, then keeps or reverts
// it.
func driftCase(name string, needs bool, op ChangeOp, item, file, content string) presenceCase {
	return presenceCase{name, needs, func(f *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
		mode := os.FileMode(0o600)
		if strings.HasPrefix(file, "hooks/") {
			mode = 0o700
		}
		f.writeMode(filepath.Join(f.state, "lado", "harness", "claude", file), content, mode)
		if op == OpDriftRevert {
			return Change{Op: op, ItemID: ids[item]}
		}
		return Change{Op: op, ItemID: ids[item], DriftSHA256: sumOf([]byte(content))}
	}}
}

// Owner decision 2026-10-27 (decision i as amended): every change of an
// item that can run code or widen access needs the desktop app's
// confirmation, reverting its drift aside: creating, editing, deleting it,
// turning it on or off at any scope, inherit, and keeping its drift. Such
// items are hook scripts, MCP servers, settings outside the safe keys, and
// commands, skills and agents whose content gives them tools or hooks or
// runs a shell command. Without a confirmation the change is refused and
// nothing is written; with it the change is the developer's. Every other
// change needs none and is the API's.
func TestEveryChangeToCodeRunningItemsNeedsPresence(t *testing.T) {
	global := func(state domain.LadoBindingState) ChangeBinding {
		return ChangeBinding{Scope: domain.LadoScopeGlobal, State: state}
	}
	project := func(state domain.LadoBindingState) ChangeBinding {
		return ChangeBinding{Scope: domain.LadoScopeProject, ScopeID: testProject, State: state}
	}
	task := func(state domain.LadoBindingState) ChangeBinding {
		return ChangeBinding{Scope: domain.LadoScopeTask, ScopeID: testTask, State: state}
	}
	setting := func(key, value string) presenceCase {
		return createCase("create setting "+key, true, domain.LadoSetupKindSetting, key, json.RawMessage(value))
	}
	cases := []presenceCase{
		createCase("create Claude MCP server", true, domain.LadoSetupKindMCPServer, "notes", json.RawMessage(`{"type":"http","url":"https://notes"}`)),
		createToolCase("create Codex MCP server", true, domain.LadoSetupToolCodex, domain.LadoSetupKindMCPServer, "linear", json.RawMessage(`{"url":"https://linear"}`)),
		setting("permissions", `{"allow":["Bash(ls)"]}`),
		setting("env", `{"DEBUG":"1"}`),
		setting("statusLine", `{"type":"command","command":"echo hi"}`),
		setting("enabledPlugins", `{"review@market":true}`),
		setting("extraKnownMarketplaces", `{"market":{"source":{"source":"github","repo":"acme/market"}}}`),
		setting("otelHeadersHelper", `"/usr/local/bin/headers.sh"`),
		setting("enableAllProjectMcpServers", `true`),
		setting("enabledMcpjsonServers", `["docs"]`),
		editCase("edit hook script", true, "check.sh", jsonText("#!/bin/sh\nexit 2\n")),
		editCase("edit hooks setting", true, "hooks", json.RawMessage(`{"Stop":[]}`)),
		editCase("edit Claude MCP server", true, "docs", json.RawMessage(`{"command":"docs-mcp","args":["--port","8"]}`)),
		editCase("edit Codex MCP server", true, "figma", json.RawMessage(`{"url":"https://figma.example"}`)),
		bindingCase("hook script on at Global", true, "check.sh", global(domain.LadoBindingOn)),
		bindingCase("hooks setting forced on for a project", true, "hooks", project(domain.LadoBindingForceOn)),
		bindingCase("MCP server forced on for a task", true, "docs", task(domain.LadoBindingForceOn)),
		driftCase("keep a hook script's drift", true, OpDriftKeep, "check.sh", "hooks/check.sh", "#!/bin/sh\nexit 3\n"),
		{"delete a hook script", true, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			return Change{Op: OpDelete, ItemID: ids["check.sh"]}
		}},
		{"delete the hooks setting", true, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			return Change{Op: OpDelete, ItemID: ids["hooks"]}
		}},
		{"delete an MCP server", true, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			return Change{Op: OpDelete, ItemID: ids["figma"]}
		}},
		bindingCase("hook script off at Global", true, "check.sh", global(domain.LadoBindingOff)),
		bindingCase("hooks setting forced off for a project", true, "hooks", project(domain.LadoBindingForceOff)),
		bindingCase("MCP server forced off for a task", true, "docs", task(domain.LadoBindingForceOff)),
		{"hooks setting back to inherit for a project", true, func(f *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			f.mustApply(Change{Op: OpBinding, ItemID: ids["hooks"], Binding: project(domain.LadoBindingForceOff)}, true)
			return Change{Op: OpBinding, ItemID: ids["hooks"], Binding: project(domain.LadoBindingInherit)}
		}},
		setting("disableAllHooks", `true`),
		setting("sandbox", `{"enabled":false}`),
		createCase("create a skill with allowed-tools", true, domain.LadoSetupKindSkill, "tooled",
			jsonText("---\nname: tooled\ndescription: Runs git.\nallowed-tools: Bash(git:*)\n---\nRun git.\n")),
		createCase("create a command that runs a shell command", true, domain.LadoSetupKindCommand, "st.md", jsonText("Status: !`git status`\n")),
		createCase("create an agent with hooks", true, domain.LadoSetupKindAgent, "guard.md", jsonText("---\nname: guard\nhooks:\n  Stop: []\n---\nGuard.\n")),
		{"edit a plain command to give it tools", true, func(f *changeFixture, _ map[string]domain.LadoSetupItemID) Change {
			res := f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindCommand, Name: "p.md", Content: jsonText("plain\n")}, false)
			return Change{Op: OpEdit, ItemID: res.Item.ID, BaseVersion: 1, Content: jsonText("---\nallowed-tools: Bash\n---\nplain\n")}
		}},
		{"edit a tooled command back to plain", true, func(f *changeFixture, _ map[string]domain.LadoSetupItemID) Change {
			res := f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindCommand, Name: "t.md", Content: jsonText("!`date`\n")}, true)
			return Change{Op: OpEdit, ItemID: res.Item.ID, BaseVersion: 1, Content: jsonText("plain\n")}
		}},
		{"turn a tooled command off", true, func(f *changeFixture, _ map[string]domain.LadoSetupItemID) Change {
			res := f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindCommand, Name: "t.md", Content: jsonText("!`date`\n")}, true)
			return Change{Op: OpBinding, ItemID: res.Item.ID, Binding: global(domain.LadoBindingOff)}
		}},

		createCase("create instructions", false, domain.LadoSetupKindInstructions, "RULES.md", jsonText("# rules\n")),
		createCase("create a plain setting", false, domain.LadoSetupKindSetting, "theme", json.RawMessage(`"light"`)),
		createCase("create an agent", false, domain.LadoSetupKindAgent, "team/c.md", jsonText("agent c\n")),
		createCase("create a command", false, domain.LadoSetupKindCommand, "git/push.md", jsonText("push\n")),
		createCase("create an output style", false, domain.LadoSetupKindOutputStyle, "short.md", jsonText("short\n")),
		createCase("create a skill", false, domain.LadoSetupKindSkill, "fresh", jsonText(freshSkill)),
		createToolCase("create Codex instructions", false, domain.LadoSetupToolCodex, domain.LadoSetupKindInstructions, "AGENTS.override.md", jsonText("override\n")),
		editCase("edit instructions", false, "CLAUDE.md", jsonText("# edited\n")),
		editCase("edit a plain setting", false, "model", json.RawMessage(`"haiku"`)),
		{"delete a plain setting", false, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			return Change{Op: OpDelete, ItemID: ids["model"]}
		}},
		bindingCase("plain setting off at Global", false, "model", global(domain.LadoBindingOff)),
		{"turn a plain command off", false, func(f *changeFixture, _ map[string]domain.LadoSetupItemID) Change {
			res := f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindCommand, Name: "p.md", Content: jsonText("plain\n")}, false)
			return Change{Op: OpBinding, ItemID: res.Item.ID, Binding: global(domain.LadoBindingOff)}
		}},
		driftCase("revert the hooks setting's drift", false, OpDriftRevert, "hooks", "settings.json", `{"hooks":{"Stop":[]}}`),
		bindingCase("instructions on at Global", false, "CLAUDE.md", global(domain.LadoBindingOn)),
		bindingCase("instructions forced on for a task", false, "CLAUDE.md", task(domain.LadoBindingForceOn)),
		driftCase("keep instructions' drift", false, OpDriftKeep, "CLAUDE.md", "CLAUDE.md", "# by hand\n"),
		driftCase("revert a hook script's drift", false, OpDriftRevert, "check.sh", "hooks/check.sh", "#!/bin/sh\nexit 4\n"),
		{"accept a warning", false, func(_ *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			return Change{Op: OpAccept, ItemID: ids["hooks"], Acceptance: ChangeAcceptance{Code: ConflictHookMayBlockTools, Detail: "Bash", Scope: domain.LadoScopeGlobal}}
		}},
		{"revoke an acceptance", false, func(f *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			res := f.mustApply(Change{Op: OpAccept, ItemID: ids["hooks"], Acceptance: ChangeAcceptance{Code: ConflictHookMayBlockTools, Detail: "Bash", Scope: domain.LadoScopeGlobal}}, false)
			return Change{Op: OpRevoke, AcceptanceID: res.Acceptance.ID}
		}},
	}
	// Every safe key is made, changed and ended without a confirmation.
	for _, key := range []string{"theme", "verbose", "outputStyle", "includeCoAuthoredBy", "showClearContextOnPlanAccept",
		"inputNeededNotifEnabled", "agentPushNotifEnabled", "preferredNotifChannel", "spinnerTipsEnabled", "cleanupPeriodDays", "language"} {
		cases = append(cases, createCase("create safe setting "+key, false, domain.LadoSetupKindSetting, key, json.RawMessage(`"x"`)))
	}
	for _, key := range []string{"model", "modelSettings"} {
		cases = append(cases, presenceCase{"create, edit and delete safe setting " + key, false, func(f *changeFixture, ids map[string]domain.LadoSetupItemID) Change {
			id := ids[key]
			if id == "" {
				id = f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: key, Content: json.RawMessage(`{"a":1}`)}, false).Item.ID
			}
			f.mustApply(Change{Op: OpEdit, ItemID: id, BaseVersion: 1, Content: json.RawMessage(`{"a":2}`)}, false)
			f.mustApply(Change{Op: OpBinding, ItemID: id, Binding: project(domain.LadoBindingForceOff)}, false)
			return Change{Op: OpDelete, ItemID: id}
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newChangeFixture(t)
			ids := f.adopt(
				claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
				claudeMove(domain.LadoSetupKindSetting, "hooks"),
				claudeMove(domain.LadoSetupKindSetting, "model"),
				claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
				claudeMove(domain.LadoSetupKindMCPServer, "docs"),
				codexMove(domain.LadoSetupKindMCPServer, "figma"),
			)
			c := tc.change(f, ids)
			preview := f.dryRun(c)
			if preview.NeedsPresence != tc.needs {
				t.Fatalf("the dry run says needsPresence=%v, want %v", preview.NeedsPresence, tc.needs)
			}
			events := len(f.store.events)
			if tc.needs {
				db := f.dbState()
				_, err := f.svc.Apply(ctx, c, nil)
				wantAPIError(t, err, "LADO_PRESENCE_REQUIRED")
				wantSameDB(t, "a refused change", db, f.dbState())
			}
			var asked []string
			presence := f.presence()
			res, err := f.svc.Apply(ctx, c, func(action, subject string) (domain.LadoPresenceProof, error) {
				asked = append(asked, action+" "+subject)
				return presence(action, subject)
			})
			if err != nil || !res.Applied {
				t.Fatalf("Apply = %+v (%v)", res, err)
			}
			want, wantAsked := actorAPI, []string(nil)
			if tc.needs {
				want, wantAsked = actorHuman, []string{domain.LadoPresenceSetupChange + " " + preview.ChangeSHA256}
			}
			if res.Actor != want || !slices.Equal(asked, wantAsked) {
				t.Fatalf("applied as %q after asking %v, want %q after %v", res.Actor, asked, want, wantAsked)
			}
			if len(f.store.events) != events+1 {
				t.Fatalf("%d audit events recorded, want one", len(f.store.events)-events)
			}
			ev, payload := f.store.lastEvent(t)
			if ev.Actor != want || (tc.needs && payload["proof"] == nil) || (!tc.needs && payload["proof"] != nil && payload["proof"] != "") {
				t.Fatalf("audit event %+v with payload %v, want actor %s", ev, payload, want)
			}
			if res.Version != nil && res.Version.ID != "" && res.Version.Actor != want && c.Op != OpDriftRevert {
				t.Fatalf("version %+v, want made by %s", res.Version, want)
			}
		})
	}

	// A lab's dev presence is recorded as such.
	f := newChangeFixture(t)
	// The hooks setting names check.sh, so the two move together.
	ids := f.adopt(claudeMove(domain.LadoSetupKindHookScript, "check.sh"), claudeMove(domain.LadoSetupKindSetting, "hooks"))
	res, err := f.svc.Apply(context.Background(), Change{Op: OpEdit, ItemID: ids["check.sh"], BaseVersion: 1, Content: jsonText("#!/bin/sh\n")},
		func(action, subject string) (domain.LadoPresenceProof, error) {
			return domain.LadoPresenceProof{Nonce: "dev-abcdefghijklmnop", Action: action, SubjectID: subject, Dev: true, ExpiresAt: scanAt, UsedAt: scanAt}, nil
		})
	if err != nil || res.Actor != actorDevPresence || res.Version.Actor != actorDevPresence {
		t.Fatalf("dev presence edit = %+v (%v)", res, err)
	}
	// A proof is used once.
	_, err = f.svc.Apply(context.Background(), Change{Op: OpEdit, ItemID: ids["check.sh"], BaseVersion: 2, Content: jsonText("#!/bin/sh\nexit 0\n")},
		func(action, subject string) (domain.LadoPresenceProof, error) {
			return domain.LadoPresenceProof{Nonce: "dev-abcdefghijklmnop", Action: action, SubjectID: subject, Dev: true, ExpiresAt: scanAt, UsedAt: scanAt}, nil
		})
	wantAPIError(t, err, "LADO_PRESENCE_REPLAYED")
}

// Plan §12: an edit names the version it started from. Another version in
// between refuses it; the latest one gives the next version, its blob and
// the managed file, and the inventory shows it.
func TestEditNeedsTheLatestVersion(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindSetting, "model"))
	md := filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md")
	edit := func(base int, content string) (ChangeResult, error) {
		return f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: base, Content: jsonText(content)}, nil)
	}

	db := f.dbState()
	_, err := edit(2, "# too new\n")
	if details := wantAPIError(t, err, CodeItemChanged); details["version"] != 1 || details["item"] != string(ids["CLAUDE.md"]) {
		t.Fatalf("details %v", details)
	}
	wantSameDB(t, "a refused edit", db, f.dbState())
	if got := readFile(t, md); got != "# me\n" {
		t.Fatalf("managed CLAUDE.md is %q after a refused edit", got)
	}

	res, err := edit(1, "# second\n")
	if err != nil || !res.Applied || res.Version.Version != 2 || res.Version.SHA256 != sumOf([]byte("# second\n")) || res.Before.Text != "# me\n" {
		t.Fatalf("edit = %+v (%v)", res, err)
	}
	wantFile(t, md, "# second\n", 0o600)
	if data, err := f.deps.Blobs.Get(res.Version.SHA256, maxBlobBytes); err != nil || string(data) != "# second\n" {
		t.Fatalf("the version's blob is %q (%v)", data, err)
	}
	_, versions, _, err := f.store.LadoSetupItem(ctx, ids["CLAUDE.md"])
	if err != nil || len(versions) != 2 || versions[1].ID != res.Version.ID {
		t.Fatalf("versions %+v (%v)", versions, err)
	}
	ev, payload := f.store.lastEvent(t)
	if ev.Action != actionItemUpdated || ev.SubjectID != string(ids["CLAUDE.md"]) || payload["version"] != float64(2) ||
		payload["previousSha256"] != sumOf([]byte("# me\n")) || payload["change"] != res.ChangeSHA256 {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}
	// The cached inventory was renewed.
	inv, err := f.svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := entryAt(t, inv, domain.LadoSetupLocationManaged, domain.LadoSetupKindInstructions, "CLAUDE.md"); e.SHA256 != res.Version.SHA256 {
		t.Fatalf("managed entry %+v, want version 2", e)
	}
	if len(inv.Drift) != 0 {
		t.Fatalf("drift %+v after an edit", inv.Drift)
	}

	// The edit started from version 1 is stale now.
	_, err = edit(1, "# third\n")
	wantAPIError(t, err, CodeItemChanged)
	// The same content again changes nothing.
	same, err := edit(2, "# second\n")
	if err != nil || !same.Applied || same.Version.ID != res.Version.ID {
		t.Fatalf("same content = %+v (%v), want version 2", same, err)
	}
	third, err := edit(2, "# third\n")
	if err != nil || third.Version.Version != 3 {
		t.Fatalf("edit from version 2 = %+v (%v)", third, err)
	}
	wantFile(t, md, "# third\n", 0o600)

	// A setting's value: only its key changes in the managed settings.json.
	model, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["model"], BaseVersion: 1, Content: json.RawMessage(` "haiku" `)}, nil)
	if err != nil || model.Version.ContentKind != domain.LadoSetupContentJSON || model.Version.SHA256 != sumOf([]byte(`"haiku"`)) ||
		model.Before.Text != `"opus"` || model.After.Text != `"haiku"` {
		t.Fatalf("model edit = %+v (%v)", model, err)
	}
	settings := filepath.Join(f.state, "lado", "harness", "claude", "settings.json")
	if value, _, err := jsonMemberValue([]byte(readFile(t, settings)), "model"); err != nil || string(value) != `"haiku"` {
		t.Fatalf("managed model is %s (%v)", value, err)
	}
	// Edits of an item that is gone are refused.
	f.mustApply(Change{Op: OpDelete, ItemID: ids["model"]}, false)
	_, err = f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["model"], BaseVersion: 2, Content: json.RawMessage(`"opus"`)}, nil)
	wantAPIError(t, err, CodeNotFound)
}

// Owner decision 3: a folder item, a skill of more than one file or a hook
// folder, is not edited in the app. It can still be turned on or off and
// deleted. A skill of one SKILL.md is edited as text.
func TestFolderItemsRefuseEdits(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	f.writeMode(filepath.Join(f.claude, "hooks", "lint", "run.sh"), "#!/bin/sh\n", 0o755)
	f.write(filepath.Join(f.claude, "hooks", "lint", "rules.txt"), "rules\n")
	f.writeMode(filepath.Join(f.claude, "hooks", "one", "run.sh"), "#!/bin/sh\n", 0o755)
	f.write(filepath.Join(f.claude, "skills", "solo", "SKILL.md"), "---\nname: solo\ndescription: One file\n---\nSolo.\n")
	ids := f.adopt(
		claudeMove(domain.LadoSetupKindSkill, "review"),
		claudeMove(domain.LadoSetupKindHookScript, "lint"),
		claudeMove(domain.LadoSetupKindHookScript, "one"),
		claudeMove(domain.LadoSetupKindSkill, "solo"),
	)
	managed := filepath.Join(f.state, "lado", "harness", "claude")
	db, lado := f.dbState(), treeState(t, f.state)
	// A hook folder of one file is a folder too: only a skill's lone
	// SKILL.md is text.
	for _, name := range []string{"review", "lint", "one"} {
		for _, dryRun := range []bool{true, false} {
			_, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids[name], BaseVersion: 1, Content: jsonText("text\n"), DryRun: dryRun}, f.presence())
			if details := wantAPIError(t, err, CodeFolderItem); details["name"] != name {
				t.Fatalf("details %v", details)
			}
		}
	}
	wantSameDB(t, "a refused folder edit", db, f.dbState())
	if diffs := diffStates(lado, treeState(t, f.state)); len(diffs) > 0 {
		t.Fatalf("a refused folder edit changed the state dir:\n%s", strings.Join(diffs, "\n"))
	}

	// Off and on again, and gone.
	f.mustApply(Change{Op: OpBinding, ItemID: ids["review"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}}, false)
	f.mustApply(Change{Op: OpBinding, ItemID: ids["lint"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff}}, true)
	on := f.mustApply(Change{Op: OpBinding, ItemID: ids["lint"], Binding: ChangeBinding{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}}, true)
	if on.Actor != actorHuman {
		t.Fatalf("turning a hook folder on = %+v, want the developer's", on)
	}
	global, err := f.store.ListLadoSetupGlobalBindings(ctx)
	if err != nil || len(global) != 2 {
		t.Fatalf("Global bindings %+v (%v)", global, err)
	}
	for _, name := range []string{"review", "lint"} {
		// A hook folder runs code; the skill's SKILL.md is plain.
		res := f.mustApply(Change{Op: OpDelete, ItemID: ids[name]}, name == "lint")
		if res.Item.DeletedAt.IsZero() || res.Before == nil || len(res.Before.Files) != 2 || res.Before.Text != "" {
			t.Fatalf("delete %s = %+v", name, res)
		}
	}
	wantMissing(t, filepath.Join(managed, "skills", "review"))
	wantMissing(t, filepath.Join(managed, "hooks", "lint"))

	// A skill of one file is text.
	solo := f.mustApply(Change{Op: OpEdit, ItemID: ids["solo"], BaseVersion: 1, Content: jsonText("---\nname: solo\ndescription: Still one file\n---\nSolo.\n")}, false)
	if solo.Version.ContentKind != domain.LadoSetupContentTree || solo.Before.Text == "" || len(solo.After.Files) != 1 {
		t.Fatalf("one-file skill edit %+v", solo)
	}
	wantFile(t, filepath.Join(managed, "skills", "solo", "SKILL.md"), "---\nname: solo\ndescription: Still one file\n---\nSolo.\n", 0o600)
}

// Plan §12: a skill made in the app is one SKILL.md whose front matter names
// it and says what it does. It lands in the managed folder as a folder with
// that file.
func TestCreateSkillWithOneFile(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	create := func(name, content string) (ChangeResult, error) {
		return f.svc.Apply(ctx, Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: name, Content: jsonText(content)}, nil)
	}
	db := f.dbState()
	for content, field := range map[string]string{
		"Use it well.\n": "name",
		"---\ndescription: Does fresh things\n---\nUse it well.\n": "name",
		"---\nname: fresh\n---\nUse it well.\n":                    "description",
		"---\nname: fresh\ndescription: \"  \"\n---\n":             "description",
		"---\nname: [fresh\n---\n":                                 "name",
	} {
		_, err := create("fresh", content)
		if details := wantAPIError(t, err, CodeContentInvalid); details["reason"] != reasonFrontMatter || details["field"] != field {
			t.Fatalf("%q: details %v, want the front matter's %s", content, details, field)
		}
	}
	for _, content := range []string{`{"SKILL.md":"x"}`, `null`, `"a\u0000b"`} {
		_, err := f.svc.Apply(ctx, Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSkill, Name: "fresh", Content: json.RawMessage(content)}, nil)
		if details := wantAPIError(t, err, CodeContentInvalid); details["reason"] != reasonNotText {
			t.Fatalf("%s: details %v", content, details)
		}
	}
	for _, name := range []string{"a/b", ".hidden", "..", "x\\y"} {
		_, err := create(name, freshSkill)
		if details := wantAPIError(t, err, CodeChangeInvalid); details["field"] != "name" {
			t.Fatalf("%q: details %v", name, details)
		}
	}
	wantSameDB(t, "a refused skill", db, f.dbState())

	res, err := create("fresh", freshSkill)
	if err != nil || !res.Applied || res.Item.ID == "" || res.Version.ContentKind != domain.LadoSetupContentTree || res.Version.Version != 1 ||
		res.Version.Size != int64(len(freshSkill)) || res.Actor != actorAPI {
		t.Fatalf("create = %+v (%v)", res, err)
	}
	folder := filepath.Join(f.state, "lado", "harness", "claude", "skills", "fresh")
	wantFile(t, filepath.Join(folder, skillFile), freshSkill, 0o600)
	tree, err := treeOf(ctx, folder)
	if err != nil || len(tree.Entries) != 1 {
		t.Fatalf("managed skill tree %+v (%v)", tree, err)
	}
	if sum, err := tree.SHA256(); err != nil || sum != res.Version.SHA256 {
		t.Fatalf("the managed folder hashes to %s (%v), want the version's %s", sum, err, res.Version.SHA256)
	}
	stored, err := f.svc.treeOfVersion(*res.Version)
	if err != nil || len(stored.Entries) != 1 || stored.Entries[0].Path != skillFile || stored.Entries[0].Mode != domain.LadoSetupModeFile {
		t.Fatalf("stored tree %+v (%v)", stored, err)
	}
	item, versions, ok, err := f.store.LadoSetupItem(ctx, res.Item.ID)
	if err != nil || !ok || item.AdoptionID != "" || item.OriginPath != "" || len(versions) != 1 || versions[0].Actor != actorAPI {
		t.Fatalf("stored item %+v %+v (%v %v)", item, versions, ok, err)
	}
	ev, payload := f.store.lastEvent(t)
	if ev.Action != actionItemCreated || ev.SubjectID != string(res.Item.ID) || payload["name"] != "fresh" || payload["kind"] != "skill" {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}
	inv, err := f.svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e := entryAt(t, inv, domain.LadoSetupLocationManaged, domain.LadoSetupKindSkill, "fresh"); e.ItemID != res.Item.ID || e.SHA256 != res.Version.SHA256 {
		t.Fatalf("managed entry %+v", e)
	}
	// One live skill of a name, in any case: Windows and macOS would put
	// both in one folder.
	for _, name := range []string{"fresh", "FRESH"} {
		_, err = create(name, freshSkill)
		if details := wantAPIError(t, err, CodeItemExists); details["item"] != string(res.Item.ID) {
			t.Fatalf("%s: details %v", name, details)
		}
	}
	// Nor can an agent sit where another one's file or folder is.
	agent := func(name string) (ChangeResult, error) {
		return f.svc.Apply(ctx, Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindAgent, Name: name, Content: jsonText("agent\n")}, nil)
	}
	if _, err := agent("team/lead.md"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"team/lead.md/deputy.md", "TEAM/Lead.md"} {
		_, err := agent(name)
		wantAPIError(t, err, CodeItemExists)
	}
	if _, err := agent("team/deputy.md"); err != nil {
		t.Fatalf("a second agent in the folder: %v", err)
	}
}

// Plan §12 and §2: deleting ends an item and takes it out of the managed
// folder. Its versions and their blobs stay, and so does a rule snapshot that
// pinned one, which the response counts.
func TestDeleteKeepsVersionsAndPins(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"), claudeMove(domain.LadoSetupKindSetting, "model"))
	edited := f.mustApply(Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: 1, Content: jsonText("# second\n")}, false)
	_, versions, _, err := f.store.LadoSetupItem(ctx, ids["CLAUDE.md"])
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions %+v (%v)", versions, err)
	}
	// An approval's snapshot pinned version 1. The snapshot row stands in
	// for an approval's: the delete reads only the pin.
	if _, err := f.raw.Exec(`INSERT INTO lado_policy_snapshots (id, approval_id, sha256, snapshot_json, blocking, created_at) VALUES (?, ?, ?, '{}', 0, ?)`,
		"snapshot-1", "approval-1", strings.Repeat("a", 64), scanAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.raw.Exec(`INSERT INTO lado_policy_snapshot_setup (snapshot_id, item_version_id) VALUES (?, ?)`, "snapshot-1", string(versions[0].ID)); err != nil {
		t.Fatal(err)
	}

	preview := f.dryRun(Change{Op: OpDelete, ItemID: ids["CLAUDE.md"]})
	if preview.Pins != 1 || preview.Before.Text != "# second\n" || !preview.Item.DeletedAt.IsZero() {
		t.Fatalf("delete dry run %+v", preview)
	}
	res := f.mustApply(Change{Op: OpDelete, ItemID: ids["CLAUDE.md"]}, false)
	if !res.Applied || res.Pins != 1 || res.Item.DeletedAt.IsZero() || res.Actor != actorAPI {
		t.Fatalf("delete = %+v", res)
	}
	ev, payload := f.store.lastEvent(t)
	if ev.Action != actionItemDeleted || ev.SubjectID != string(ids["CLAUDE.md"]) || payload["pins"] != float64(1) || payload["name"] != "CLAUDE.md" {
		t.Fatalf("audit event %+v with payload %v", ev, payload)
	}
	wantMissing(t, filepath.Join(f.state, "lado", "harness", "claude", "CLAUDE.md"))

	item, after, ok, err := f.store.LadoSetupItem(ctx, ids["CLAUDE.md"])
	if err != nil || !ok || item.Live() || len(after) != 2 {
		t.Fatalf("deleted item %+v with versions %+v (%v %v)", item, after, ok, err)
	}
	for _, v := range after {
		if data, err := f.deps.Blobs.Get(v.SHA256, maxBlobBytes); err != nil || len(data) == 0 {
			t.Fatalf("version %d's blob: %v", v.Version, err)
		}
	}
	snapshot, ok, err := f.store.LadoPolicySnapshot(ctx, "approval-1")
	if err != nil || !ok || !slices.Equal(snapshot.SetupVersionIDs, []domain.LadoSetupItemVersionID{versions[0].ID}) {
		t.Fatalf("snapshot after the delete = %+v (%v %v), want its pin", snapshot, ok, err)
	}
	if v, ok, err := f.store.LadoSetupItemVersion(ctx, versions[0].ID); err != nil || !ok || v.SHA256 != sumOf([]byte("# me\n")) {
		t.Fatalf("pinned version %+v (%v %v)", v, ok, err)
	}
	if got := liveItemNames(t, f.store); !slices.Equal(got, []string{"setting:model"}) {
		t.Fatalf("live items %v", got)
	}
	inv, err := f.svc.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inv.Entries {
		if e.Location == domain.LadoSetupLocationManaged && e.Name == "CLAUDE.md" {
			t.Fatalf("the inventory still lists %+v", e)
		}
	}
	// An item without pins, whose blob is gone: what LADO cannot show does
	// not hold the delete back.
	if err := os.Remove(filepath.Join(f.state, "lado", "blobs", sumOf([]byte(`"opus"`))[:2], sumOf([]byte(`"opus"`)))); err != nil {
		t.Fatal(err)
	}
	if model := f.mustApply(Change{Op: OpDelete, ItemID: ids["model"]}, false); model.Pins != 0 || model.Before != nil ||
		!slices.Equal(model.Warnings, []string{WarningPreviewIncomplete}) {
		t.Fatalf("model delete = %+v", model)
	}
	// An item that is gone.
	_, err = f.svc.Apply(ctx, Change{Op: OpDelete, ItemID: ids["CLAUDE.md"]}, nil)
	wantAPIError(t, err, CodeNotFound)
	_, err = f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: ids["CLAUDE.md"], BaseVersion: edited.Version.Version, Content: jsonText("# again\n")}, nil)
	wantAPIError(t, err, CodeNotFound)
}

// Owner decision 2026-10-27: every binding of an item that can run code
// needs the desktop app's confirmation at every scope, on, off and inherit
// alike; turning it off can lift a guard as turning it on can add a command.
func TestEveryBindingOfACodeItemNeedsPresence(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(
		claudeMove(domain.LadoSetupKindHookScript, "check.sh"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindInstructions, "CLAUDE.md"),
	)
	hook := ids["check.sh"]
	bind := func(item domain.LadoSetupItemID, scope domain.LadoRuleScope, id string, state domain.LadoBindingState) Change {
		return Change{Op: OpBinding, ItemID: item, Binding: ChangeBinding{Scope: scope, ScopeID: id, State: state}}
	}
	// stored is the item's binding rows, as scope/id=state.
	stored := func(item domain.LadoSetupItemID) []string {
		t.Helper()
		var out []string
		global, err := f.store.ListLadoSetupGlobalBindings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		project, err := f.store.ListLadoSetupBindings(ctx, domain.LadoScopeProject, testProject)
		if err != nil {
			t.Fatal(err)
		}
		task, err := f.store.ListLadoSetupBindings(ctx, domain.LadoScopeTask, testTask)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range slices.Concat(global, project, task) {
			if b.ItemID == item {
				out = append(out, fmt.Sprintf("%s/%s=%s", b.Scope, b.ScopeID, b.State))
			}
		}
		return out
	}
	for _, step := range []struct {
		change Change
		needs  bool
		want   []string
	}{
		// Global on, though the item was on without a row.
		{bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingOn), true, []string{"global/=on"}},
		{bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingOff), true, []string{"global/=off"}},
		// Removing the Global off turns the item on.
		{bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingInherit), true, nil},
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingForceOn), true, []string{"project/demo=force_on"}},
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingForceOff), true, []string{"project/demo=force_off"}},
		// Inherit after force_off falls back to Global, which is on.
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingInherit), true, nil},
		{bind(hook, domain.LadoScopeTask, testTask, domain.LadoBindingForceOff), true, []string{"task/task-1=force_off"}},
		{bind(hook, domain.LadoScopeTask, testTask, domain.LadoBindingForceOn), true, []string{"task/task-1=force_on"}},
		// Inherit after force_on leaves it on: nothing is turned on.
		{bind(hook, domain.LadoScopeTask, testTask, domain.LadoBindingInherit), true, nil},
		{bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingOff), true, []string{"global/=off"}},
		// With Global off, a project inherit turns nothing on.
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingForceOff), true, []string{"global/=off", "project/demo=force_off"}},
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingInherit), true, []string{"global/=off"}},
	} {
		b := step.change.Binding
		name := fmt.Sprintf("%s %s=%s", b.Scope, b.ScopeID, b.State)
		preview := f.dryRun(step.change)
		if preview.NeedsPresence != step.needs {
			t.Fatalf("%s: needsPresence=%v, want %v", name, preview.NeedsPresence, step.needs)
		}
		if step.needs {
			db := f.dbState()
			_, err := f.svc.Apply(ctx, step.change, nil)
			wantAPIError(t, err, "LADO_PRESENCE_REQUIRED")
			wantSameDB(t, name+" without a confirmation", db, f.dbState())
		}
		res := f.mustApply(step.change, step.needs)
		if want := map[bool]string{true: actorHuman, false: actorAPI}[step.needs]; res.Actor != want {
			t.Fatalf("%s: applied as %q, want %q", name, res.Actor, want)
		}
		if got := stored(hook); !slices.Equal(got, step.want) {
			t.Fatalf("%s: bindings %v, want %v", name, got, step.want)
		}
		ev, payload := f.store.lastEvent(t)
		if ev.Action != actionBindingSet || payload["to"] != string(b.State) || payload["scope"] != string(b.Scope) {
			t.Fatalf("%s: audit event %+v with payload %v", name, ev, payload)
		}
	}
	// The same state again changes nothing and asks nothing.
	events := len(f.store.events)
	same := f.mustApply(bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingOff), false)
	if !same.Applied || same.NeedsPresence || len(f.store.events) != events {
		t.Fatalf("the same binding = %+v with %d new events", same, len(f.store.events)-events)
	}
	// Instructions run nothing: turning them on is the API's.
	if res := f.mustApply(bind(ids["CLAUDE.md"], domain.LadoScopeTask, testTask, domain.LadoBindingForceOn), false); res.Actor != actorAPI {
		t.Fatalf("instructions on = %+v", res)
	}
	// A cancelled task takes no forced state, and inherit still clears one.
	if _, _, err := f.store.CancelLadoTask(ctx, testTask, scanAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Apply(ctx, bind(hook, domain.LadoScopeTask, testTask, domain.LadoBindingForceOff), nil)
	wantAPIError(t, err, codeTaskCancelled)
	f.mustApply(bind(ids["CLAUDE.md"], domain.LadoScopeTask, testTask, domain.LadoBindingInherit), false)
	// Scopes LADO does not know, and states a scope does not take.
	for _, refused := range []struct {
		change Change
		code   string
	}{
		{bind(hook, domain.LadoScopeProject, "nope", domain.LadoBindingForceOn), codeScopeNotFound},
		{bind(hook, domain.LadoScopeTask, "nope", domain.LadoBindingForceOff), codeScopeNotFound},
		{bind(hook, domain.LadoScopeGlobal, "", domain.LadoBindingForceOn), codeBindingStateInvalid},
		{bind(hook, domain.LadoScopeProject, testProject, domain.LadoBindingOn), codeBindingStateInvalid},
		{bind(hook, domain.LadoScopeGlobal, testProject, domain.LadoBindingOff), codeBindingScopeInvalid},
		{bind(hook, domain.LadoScopeProject, "", domain.LadoBindingForceOff), codeBindingScopeInvalid},
		{bind(hook, domain.LadoRuleScope("team"), "x", domain.LadoBindingForceOn), codeBindingScopeInvalid},
		{bind("missing", domain.LadoScopeGlobal, "", domain.LadoBindingOff), CodeNotFound},
	} {
		_, err := f.svc.Apply(ctx, refused.change, f.presence())
		wantAPIError(t, err, refused.code)
	}
}

// Plan decision e: logins stay where the tools keep them. A login key, or an
// env setting with a login variable, cannot be made or edited in.
func TestLoginKeysCannotBeAdded(t *testing.T) {
	ctx := context.Background()
	f := newChangeFixture(t)
	ids := f.adopt(claudeMove(domain.LadoSetupKindSetting, "env"))
	original := filepath.Join(f.claude, "settings.json")
	if value, _, err := jsonMemberValue([]byte(readFile(t, original)), "env"); err != nil || !strings.Contains(string(value), "ANTHROPIC_API_KEY") {
		t.Fatalf("the original env is %s (%v), want its login variable kept", value, err)
	}
	db, home := f.dbState(), f.homeState()
	create := func(key, value string) error {
		_, err := f.svc.Apply(ctx, Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: key, Content: json.RawMessage(value)}, f.presence())
		return err
	}
	for _, key := range domain.LadoSetupClaudeLoginKeys {
		if details := wantAPIError(t, create(key, `"x"`), CodeLoginKey); details["key"] != key {
			t.Fatalf("%s: details %v", key, details)
		}
	}
	wantSameDB(t, "a refused login key", db, f.dbState())
	// env is adopted already; a second env item is refused before its name.
	f.mustApply(Change{Op: OpDelete, ItemID: ids["env"]}, true)
	db = f.dbState()
	for _, login := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN", "AWS_REGION", "CLOUD_ML_REGION", "VERTEX_REGION_X", "GOOGLE_CLOUD_PROJECT", "AZURE_API_KEY", "HTTPS_PROXY"} {
		for _, name := range []string{login, strings.ToLower(login)} {
			if details := wantAPIError(t, create("env", fmt.Sprintf(`{"DEBUG":"1",%q:"x"}`, name)), CodeLoginKey); details["key"] != name {
				t.Fatalf("%s: details %v", name, details)
			}
		}
	}
	if details := wantAPIError(t, create("env", `"DEBUG=1"`), CodeContentInvalid); details["reason"] != reasonNotObject {
		t.Fatalf("details %v", details)
	}
	wantSameDB(t, "a refused login", db, f.dbState())

	// An env item without logins, edited with one and without.
	made := f.mustApply(Change{Op: OpCreate, Tool: domain.LadoSetupToolClaudeCode, Kind: domain.LadoSetupKindSetting, Name: "env", Content: json.RawMessage(`{"DEBUG":"1"}`)}, true)
	db = f.dbState()
	_, err := f.svc.Apply(ctx, Change{Op: OpEdit, ItemID: made.Item.ID, BaseVersion: 1, Content: json.RawMessage(`{"DEBUG":"2","CLAUDE_CODE_OAUTH_TOKEN":"x"}`)}, f.presence())
	wantAPIError(t, err, CodeLoginKey)
	wantSameDB(t, "a refused login", db, f.dbState())
	edited := f.mustApply(Change{Op: OpEdit, ItemID: made.Item.ID, BaseVersion: 1, Content: json.RawMessage(`{"DEBUG":"2"}`)}, true)
	if edited.Version.Version != 2 || edited.Actor != actorHuman {
		t.Fatalf("env edit = %+v", edited)
	}
	managed := filepath.Join(f.state, "lado", "harness", "claude", "settings.json")
	value, _, err := jsonMemberValue([]byte(readFile(t, managed)), "env")
	if err == nil {
		value, err = canonicalJSON(value)
	}
	if err != nil || string(value) != `{"DEBUG":"2"}` {
		t.Fatalf("managed env is %s (%v)", value, err)
	}
	// The original keeps its login variable, untouched by every change.
	if diffs := diffStates(home, f.homeState()); len(diffs) > 0 {
		t.Fatalf("changes wrote the tool folders:\n%s", strings.Join(diffs, "\n"))
	}
}

// S07's gap (S08 retrospective): the rename that rewrites an original
// without an adopted key can go through and still fail, as when flushing
// the folder fails. The key is then gone from the file, so the adoption must
// record it as a managed item, which restore puts back.
func TestAdoptionTreatsAFlushErrorAfterRenameAsDone(t *testing.T) {
	ctx := context.Background()
	f := newAdoption(t)
	svc := f.service()
	renames := 0
	svc.ops.rename = func(oldpath, newpath string) error {
		if err := os.Rename(oldpath, newpath); err != nil {
			return err
		}
		renames++
		return errors.New("sync: input/output error")
	}
	res := f.mustAdopt(svc,
		claudeMove(domain.LadoSetupKindSetting, "model"),
		claudeMove(domain.LadoSetupKindSetting, "hooks"),
		claudeMove(domain.LadoSetupKindMCPServer, "docs"),
		codexMove(domain.LadoSetupKindMCPServer, "figma"),
	)
	if res.State != domain.LadoSetupAdoptionCompleted || len(res.Items) != 4 || renames != 4 {
		t.Fatalf("adoption %+v after %d renames, want it completed with every key", res, renames)
	}
	settings := []byte(readFile(t, filepath.Join(f.claude, "settings.json")))
	for _, key := range []string{"model", "hooks"} {
		if _, ok, err := jsonMemberValue(settings, key); err != nil || ok {
			t.Fatalf("settings.json still has %s (%v)", key, err)
		}
	}
	if strings.Contains(readFile(t, f.claudeJSON), `"docs"`) || strings.Contains(readFile(t, filepath.Join(f.codex, "config.toml")), "[mcp_servers.figma]") {
		t.Fatal("an adopted server is still in its original file")
	}
	if got := liveItemNames(t, f.store); len(got) != 4 {
		t.Fatalf("live items %v, want the four keys", got)
	}
	for _, check := range res.Verification {
		if !check.OK {
			t.Fatalf("verification %+v", check)
		}
	}
	// Restore, with the folder flushing again, puts every key back.
	if _, err := f.restore(f.service(), res.AdoptionID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	settings = []byte(readFile(t, filepath.Join(f.claude, "settings.json")))
	for _, key := range []string{"model", "hooks"} {
		if _, ok, err := jsonMemberValue(settings, key); err != nil || !ok {
			t.Fatalf("settings.json lacks %s after the restore (%v)", key, err)
		}
	}
	if !strings.Contains(readFile(t, f.claudeJSON), `"docs"`) || !strings.Contains(readFile(t, filepath.Join(f.codex, "config.toml")), "[mcp_servers.figma]") {
		t.Fatal("a server did not come back")
	}
	if items, err := f.store.ListLiveLadoSetupItems(ctx); err != nil || len(items) != 0 {
		t.Fatalf("live items after the restore %+v (%v)", items, err)
	}

	// A rename that fails before it moves anything is still a failure: the
	// adoption stops before recording the key it could not remove.
	g := newAdoption(t)
	broken := g.service()
	broken.ops.rename = func(string, string) error { return errors.New("rename: permission denied") }
	_, err := g.adopt(broken, claudeMove(domain.LadoSetupKindSetting, "model"))
	if details := wantAPIError(t, err, CodeAdoptionFailed); details["stage"] != stageRemove {
		t.Fatalf("details %v", details)
	}
	if got := liveItemNames(t, g.store); len(got) != 0 {
		t.Fatalf("live items %v after a failed removal", got)
	}
	if _, ok, err := jsonMemberValue([]byte(readFile(t, filepath.Join(g.claude, "settings.json"))), "model"); err != nil || !ok {
		t.Fatalf("model left settings.json after a failed rename (%v)", err)
	}
}
