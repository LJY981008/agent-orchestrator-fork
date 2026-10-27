package ladopolicy_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

// fullInput resolves task-a with a bit of everything: the built-ins with the
// Korean commit rule on, a rule proj-a forces on, a rule that is off, Claude
// Code, AO instructions for workers and for the orchestrator, and local setup
// (fullSetup).
func fullInput(t *testing.T) ladopolicy.Input {
	t.Helper()
	rules := seeded()
	ruleIn(t, rules, domain.LadoRuleKoreanCommits).Global = domain.LadoBindingOn
	style := promptRule("team.style", "Small commits", "Keep each commit to one change.", 1)
	style.Overrides = []domain.LadoRuleBinding{projectBinding(projectA, domain.LadoBindingForceOn), projectBinding(projectB, domain.LadoBindingForceOff)}
	idle := promptRule("team.idle", "Draft rule", "Not used yet.", 2)
	return ladopolicy.Input{
		Rules:     append(rules, style, idle),
		ProjectID: projectA,
		TaskID:    taskA,
		Agent:     claudeCode,
		AORules: []ladopolicy.AORule{
			{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: hexSHA256("Use pnpm."), Chars: 9},
			{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/agents.md", SHA256: hexSHA256("Run make test."), Chars: 14},
			{Kind: ladopolicy.AORuleOrchestratorInstructions, SHA256: hexSHA256("Plan first."), Chars: 11},
		},
		AOBuild: "1.0.0",
		Setup:   fullSetup(),
	}
}

// fullSetup is local setup of every shape for a Claude Code task-a: on items
// from each scope, an off item, an enabledPlugins setting with its installed
// plugins, two conflicts and an accepted warning.
func fullSetup() ladopolicy.SetupResolution {
	claude := domain.LadoSetupToolClaudeCode
	global := ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}
	return ladopolicy.SetupResolution{
		Items: []ladopolicy.SetupItemState{
			{ItemID: "setup-claude-md", Tool: claude, Kind: domain.LadoSetupKindInstructions, Name: "CLAUDE.md",
				VersionID: "setup-claude-md-2", Version: 2, SHA256: hexSHA256("Be brief."), On: true, Source: global,
				Global: domain.LadoBindingOn, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit},
			{ItemID: "setup-hooks", Tool: claude, Kind: domain.LadoSetupKindSetting, Name: "hooks",
				VersionID: "setup-hooks-1", Version: 1, SHA256: hexSHA256(`{"Stop":[]}`), On: true,
				Source: ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: string(projectA), State: domain.LadoBindingForceOn},
				Global: domain.LadoBindingOff, Project: domain.LadoBindingForceOn, Task: domain.LadoBindingInherit},
			{ItemID: "setup-permissions", Tool: claude, Kind: domain.LadoSetupKindSetting, Name: "permissions",
				VersionID: "setup-permissions-1", Version: 1, SHA256: hexSHA256(`{"deny":["Bash(git push:*)"]}`), On: true, Source: global,
				Global: domain.LadoBindingOn, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit},
			{ItemID: "setup-plugins", Tool: claude, Kind: domain.LadoSetupKindSetting, Name: "enabledPlugins",
				VersionID: "setup-plugins-1", Version: 1, SHA256: hexSHA256(`{"review@market":true}`), On: true,
				Source: ladopolicy.Source{Scope: domain.LadoScopeTask, ScopeID: string(taskA), State: domain.LadoBindingForceOn},
				Global: domain.LadoBindingOff, Project: domain.LadoBindingInherit, Task: domain.LadoBindingForceOn,
				InstalledSHA256: hexSHA256("review 1.2.0")},
			{ItemID: "setup-review", Tool: claude, Kind: domain.LadoSetupKindSkill, Name: "review",
				VersionID: "setup-review-1", Version: 1, SHA256: hexSHA256("review skill"),
				Source: ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: string(projectA), State: domain.LadoBindingForceOff},
				Global: domain.LadoBindingOn, Project: domain.LadoBindingForceOff, Task: domain.LadoBindingInherit},
		},
		Problems: []domain.LadoPolicyProblem{
			{Code: "LADO_SETUP_HOOK_SHARES_AO_EVENT", Severity: info, Details: map[string]string{"item": "setup-hooks", "event": "Stop"}},
			{Code: "LADO_SETUP_HOOK_SHARES_AO_EVENT", Severity: info, Details: map[string]string{"item": "setup-hooks", "event": "SessionStart"}},
		},
		Accepted: []ladopolicy.AcceptedConflict{
			{Code: "LADO_SETUP_DENIES_GIT", ItemID: "setup-permissions", Detail: "Bash(git push:*)", AcceptanceID: "lado-acceptance-1"},
		},
		AOSystemSHA256: hexSHA256("ao system"),
	}
}

func TestSnapshotIsDeterministic(t *testing.T) {
	first := ladopolicy.Resolve(fullInput(t))
	second := ladopolicy.Resolve(fullInput(t))
	if !bytes.Equal(first.JSON, second.JSON) || first.SHA256 != second.SHA256 {
		t.Fatalf("two resolutions of one input differ:\n%s\n%s", first.JSON, second.JSON)
	}
	if sum := sha256.Sum256(first.JSON); first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 %s is not the sha256 of JSON", first.SHA256)
	}

	// The order rules, bindings and AO instructions arrive in does not matter.
	shuffled := fullInput(t)
	slices.Reverse(shuffled.Rules)
	slices.Reverse(ruleIn(t, shuffled.Rules, "team.style").Overrides)
	slices.Reverse(shuffled.AORules)
	slices.Reverse(shuffled.Setup.Items)
	slices.Reverse(shuffled.Setup.Problems)
	shuffled.Setup.Accepted = append(shuffled.Setup.Accepted,
		ladopolicy.AcceptedConflict{Code: "LADO_SETUP_DENIES_GIT", ItemID: "setup-permissions", Detail: "Bash(gh:*)", AcceptanceID: "lado-acceptance-2"})
	slices.Reverse(shuffled.Setup.Accepted)
	sorted := fullInput(t)
	sorted.Setup.Accepted = append(sorted.Setup.Accepted, shuffled.Setup.Accepted[0])
	if got, want := ladopolicy.Resolve(shuffled), ladopolicy.Resolve(sorted); !bytes.Equal(got.JSON, want.JSON) {
		t.Errorf("shuffled setup gives other bytes:\n%s\nwant\n%s", got.JSON, want.JSON)
	}
	shuffled.Setup.Accepted = shuffled.Setup.Accepted[1:]
	if got := ladopolicy.Resolve(shuffled); !bytes.Equal(got.JSON, first.JSON) {
		t.Errorf("shuffled input gives other bytes:\n%s\nwant\n%s", got.JSON, first.JSON)
	}

	// Empty lists encode as [], never as null.
	empty := ladopolicy.Resolve(ladopolicy.Input{ProjectID: projectA})
	for _, field := range []string{`"rules":[]`, `"aoRules":[]`, `"setup":[]`, `"aoSystem":{"sha256":""}`, `"acceptedConflicts":[]`, `"workflowGates":[]`, `"capabilities":[]`} {
		if !bytes.Contains(empty.JSON, []byte(field)) {
			t.Errorf("no %s in %s", field, empty.JSON)
		}
	}
	quiet := ladopolicy.Resolve(ladopolicy.Input{Rules: seeded(), ProjectID: projectA})
	if !bytes.Contains(quiet.JSON, []byte(`"problems":[]`)) {
		t.Errorf(`no "problems":[] in %s`, quiet.JSON)
	}
	for _, r := range []ladopolicy.Resolution{first, empty, quiet} {
		if bytes.Contains(r.JSON, []byte("null")) {
			t.Errorf("null in %s", r.JSON)
		}
	}

	// One line without a trailing newline, with HTML characters as they are,
	// that decodes back into the content.
	markup := promptRule("team.markup", "Markup", "Write <b> & </b> as they are.", 3)
	markup.Global = domain.LadoBindingOn
	r := ladopolicy.Resolve(taskInput(append(seeded(), markup), claudeCode))
	if bytes.IndexByte(r.JSON, '\n') >= 0 {
		t.Errorf("a newline in %q", r.JSON)
	}
	if !bytes.Contains(r.JSON, []byte("Write <b> & </b> as they are.")) {
		t.Errorf("HTML characters were escaped: %s", r.JSON)
	}
	var decoded ladopolicy.SnapshotContent
	if err := json.Unmarshal(r.JSON, &decoded); err != nil || !reflect.DeepEqual(decoded, r.Content) {
		t.Errorf("JSON decodes to %+v (err %v), want %+v", decoded, err, r.Content)
	}
}

// TestSnapshotFormatIsLadoPolicy2 pins the lado-policy/2 bytes: a change to a
// field, its name or its place needs a new format name.
func TestSnapshotFormatIsLadoPolicy2(t *testing.T) {
	in := fullInput(t)
	shas := []string{}
	for _, rule := range in.Rules {
		shas = append(shas, "<"+string(rule.ID)+">", rule.Version.SHA256)
	}
	for _, item := range in.Setup.Items {
		shas = append(shas, "<"+string(item.ItemID)+">", item.SHA256)
	}
	want := strings.NewReplacer(append(shas, "<Use pnpm.>", hexSHA256("Use pnpm."), "<Run make test.>", hexSHA256("Run make test."),
		"<korean>", koreanCommitsInstruction, "<installed>", hexSHA256("review 1.2.0"), "<ao system>", hexSHA256("ao system"))...).Replace(`{
		"compiler": "lado-policy/2",
		"aoBuild": "1.0.0",
		"projectId": "proj-a",
		"taskId": "task-a",
		"agent": {"harness": "claude-code", "model": "opus", "permissionMode": "auto"},
		"permissionCeiling": "auto",
		"rules": [
			{"ruleId": "agent.permission.ceiling", "versionId": "ver-agent.permission.ceiling-1", "version": 1, "sha256": "<agent.permission.ceiling>",
			 "name": "Permission ceiling", "protected": true, "enforcement": ["tool_policy"], "strength": "prevents",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}},
			{"ruleId": "git.commit.message.korean", "versionId": "ver-git.commit.message.korean-1", "version": 1, "sha256": "<git.commit.message.korean>",
			 "name": "Commit messages in Korean", "protected": false, "enforcement": ["prompt"], "strength": "advisory",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}},
			{"ruleId": "plan.approval.required", "versionId": "ver-plan.approval.required-1", "version": 1, "sha256": "<plan.approval.required>",
			 "name": "Plan approval required", "protected": true, "enforcement": ["workflow_gate"], "strength": "prevents",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}},
			{"ruleId": "setup.change.requires_presence", "versionId": "ver-setup.change.requires_presence-1", "version": 1, "sha256": "<setup.change.requires_presence>",
			 "name": "Local setup changes need the desktop app's confirmation", "protected": true, "enforcement": ["workflow_gate"], "strength": "prevents",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}},
			{"ruleId": "team.style", "versionId": "ver-team.style-1", "version": 1, "sha256": "<team.style>",
			 "name": "Small commits", "protected": false, "enforcement": ["prompt"], "strength": "advisory",
			 "source": {"scope": "project", "scopeId": "proj-a", "state": "force_on"}}
		],
		"aoRules": [
			{"kind": "worker_instructions", "path": "", "sha256": "<Use pnpm.>"},
			{"kind": "worker_instructions_file", "path": "docs/agents.md", "sha256": "<Run make test.>"}
		],
		"setup": [
			{"itemId": "setup-claude-md", "tool": "claude-code", "kind": "instructions", "name": "CLAUDE.md",
			 "versionId": "setup-claude-md-2", "version": 2, "sha256": "<setup-claude-md>",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}},
			{"itemId": "setup-plugins", "tool": "claude-code", "kind": "setting", "name": "enabledPlugins",
			 "versionId": "setup-plugins-1", "version": 1, "sha256": "<setup-plugins>",
			 "source": {"scope": "task", "scopeId": "task-a", "state": "force_on"}, "installedSha256": "<installed>"},
			{"itemId": "setup-hooks", "tool": "claude-code", "kind": "setting", "name": "hooks",
			 "versionId": "setup-hooks-1", "version": 1, "sha256": "<setup-hooks>",
			 "source": {"scope": "project", "scopeId": "proj-a", "state": "force_on"}},
			{"itemId": "setup-permissions", "tool": "claude-code", "kind": "setting", "name": "permissions",
			 "versionId": "setup-permissions-1", "version": 1, "sha256": "<setup-permissions>",
			 "source": {"scope": "global", "scopeId": "", "state": "on"}}
		],
		"aoSystem": {"sha256": "<ao system>"},
		"acceptedConflicts": [
			{"code": "LADO_SETUP_DENIES_GIT", "itemId": "setup-permissions", "detail": "Bash(git push:*)", "acceptanceId": "lado-acceptance-1"}
		],
		"prompt": "## LADO rules\n\nThe developer approved these rules for this run. Follow them together with the project rules above.\n\n### Commit messages in Korean\n\n<korean>\n\n### Small commits\n\nKeep each commit to one change.",
		"workflowGates": ["plan.approval.required", "setup.change.requires_presence"],
		"capabilities": [{"class": "prompt", "status": "supported"}, {"class": "permission_mode", "status": "supported"}],
		"problems": [
			{"code": "LADO_SETUP_HOOK_SHARES_AO_EVENT", "severity": "info", "details": {"event": "SessionStart", "item": "setup-hooks"}},
			{"code": "LADO_SETUP_HOOK_SHARES_AO_EVENT", "severity": "info", "details": {"event": "Stop", "item": "setup-hooks"}}
		]
	}`)
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	r := ladopolicy.Resolve(in)
	if !bytes.Equal(r.JSON, compact.Bytes()) {
		t.Errorf("snapshot\n%s\nwant\n%s", r.JSON, compact.Bytes())
	}
	if ladopolicy.Compiler != "lado-policy/2" {
		t.Errorf("Compiler = %q", ladopolicy.Compiler)
	}
	wantRules := []domain.LadoPolicySnapshotRule{
		{RuleID: domain.LadoRulePermissionCeiling, VersionID: "ver-agent.permission.ceiling-1"},
		{RuleID: domain.LadoRuleKoreanCommits, VersionID: "ver-git.commit.message.korean-1"},
		{RuleID: domain.LadoRulePlanApproval, VersionID: "ver-plan.approval.required-1"},
		{RuleID: domain.LadoRuleSetupPresence, VersionID: "ver-setup.change.requires_presence-1"},
		{RuleID: "team.style", VersionID: "ver-team.style-1"},
	}
	if got := r.SnapshotRules(); !reflect.DeepEqual(got, wantRules) {
		t.Errorf("SnapshotRules() = %+v, want %+v", got, wantRules)
	}
	wantSetup := []domain.LadoSetupItemVersionID{"setup-claude-md-2", "setup-plugins-1", "setup-hooks-1", "setup-permissions-1"}
	if got := r.SnapshotSetup(); !reflect.DeepEqual(got, wantSetup) {
		t.Errorf("SnapshotSetup() = %v, want the on items' versions %v", got, wantSetup)
	}
	if r.Blocking() {
		t.Errorf("Blocking() with problems %+v", r.Problems)
	}
	if !reflect.DeepEqual(r.Setup, in.Setup) {
		t.Errorf("Resolution.Setup = %+v, want the input's %+v", r.Setup, in.Setup)
	}
}

func TestSnapshotHashFollowsWhatApplies(t *testing.T) {
	base := ladopolicy.Resolve(fullInput(t)).SHA256
	newVersion := func(t *testing.T, in *ladopolicy.Input, id domain.LadoRuleID, instruction string) {
		t.Helper()
		rule := ruleIn(t, in.Rules, id)
		content := rule.Version.Content
		content.Instruction = instruction
		*rule = withContent(*rule, content)
	}
	cases := []struct {
		name   string
		change func(t *testing.T, in *ladopolicy.Input)
		same   bool
	}{
		{
			name:   "a new version of an off rule leaves it",
			change: func(t *testing.T, in *ladopolicy.Input) { newVersion(t, in, "team.idle", "Now it says more.") },
			same:   true,
		},
		{
			name: "archiving an off rule leaves it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				ruleIn(t, in.Rules, "team.idle").ArchivedAt = seededAt.Add(1)
			},
			same: true,
		},
		{
			name:   "orchestrator instructions are not part of it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.AORules[2].SHA256 = hexSHA256("Plan twice.") },
			same:   true,
		},
		{
			name:   "where the agent choice came from is not part of it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Agent.Source = "project" },
			same:   true,
		},
		{
			name:   "a new instruction of an on rule changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { newVersion(t, in, "team.style", "Keep commits small.") },
		},
		{
			name: "the binding an on rule comes from changes it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				korean := ruleIn(t, in.Rules, domain.LadoRuleKoreanCommits)
				korean.Global = domain.LadoBindingOff
				korean.Overrides = []domain.LadoRuleBinding{taskBinding(taskA, domain.LadoBindingForceOn)}
			},
		},
		{
			name:   "another agent changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Agent.Harness = domain.HarnessCodex },
		},
		{
			name:   "another model changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Agent.Model = "gpt-5.2-codex" },
		},
		{
			name:   "changed AO worker instructions change it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.AORules[0].SHA256 = hexSHA256("Use npm.") },
		},
		{
			name:   "another AO build changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.AOBuild = "1.0.1" },
		},
		{
			name:   "a lower plan ceiling changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.PlanCeiling = domain.PermissionModeAcceptEdits },
		},
		{
			name: "a new version of an off setup item leaves it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				in.Setup.Items[4].VersionID, in.Setup.Items[4].Version, in.Setup.Items[4].SHA256 = "setup-review-2", 2, hexSHA256("review skill 2")
			},
			same: true,
		},
		{
			name:   "where an item's Global state comes from leaves it while a nearer binding decides",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Setup.Items[1].Global = domain.LadoBindingOn },
			same:   true,
		},
		{
			name: "a new version of an on setup item changes it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				in.Setup.Items[0].VersionID, in.Setup.Items[0].Version, in.Setup.Items[0].SHA256 = "setup-claude-md-3", 3, hexSHA256("Be briefer.")
			},
		},
		{
			name: "the binding an on setup item comes from changes it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				in.Setup.Items[0].Source = ladopolicy.Source{Scope: domain.LadoScopeProject, ScopeID: string(projectA), State: domain.LadoBindingForceOn}
			},
		},
		{
			name:   "turning a setup item off changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Setup.Items[0].On = false },
		},
		{
			name: "other installed plugins change it",
			change: func(t *testing.T, in *ladopolicy.Input) {
				in.Setup.Items[3].InstalledSHA256 = hexSHA256("review 1.3.0")
			},
		},
		{
			name:   "another AO system layer changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Setup.AOSystemSHA256 = hexSHA256("ao system 2") },
		},
		{
			name:   "an accepted warning changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Setup.Accepted = nil },
		},
		{
			name:   "a setup conflict changes it",
			change: func(t *testing.T, in *ladopolicy.Input) { in.Setup.Problems = in.Setup.Problems[:1] },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fullInput(t)
			c.change(t, &in)
			got := ladopolicy.Resolve(in).SHA256
			if (got == base) != c.same {
				t.Errorf("sha256 %s, base %s; want same = %v", got, base, c.same)
			}
		})
	}
}

func TestPromptText(t *testing.T) {
	const golden = `## LADO rules

The developer approved these rules for this run. Follow them together with the project rules above.

### Commit messages in Korean

Write Git commit subjects and bodies primarily in Korean. Conventional Commit prefixes such as feat, fix, docs, refactor, test and chore may stay in English.

### Tests before commits

Run the unit tests before each commit.
Fix what fails before you commit.`

	rules := seeded()
	ruleIn(t, rules, domain.LadoRuleKoreanCommits).Global = domain.LadoBindingOn
	tests := promptRule("team.tests", "Tests before commits", "Run the unit tests before each commit.\nFix what fails before you commit.", 1)
	tests.Global = domain.LadoBindingOn
	// Newest first: the text follows creation order, not input order.
	r := ladopolicy.Resolve(taskInput(append([]domain.LadoRule{tests}, rules...), claudeCode))
	if r.Prompt != golden {
		t.Errorf("prompt\n%q\nwant\n%q", r.Prompt, golden)
	}
	if r.Content.Prompt != r.Prompt {
		t.Errorf("snapshot prompt %q, want %q", r.Content.Prompt, r.Prompt)
	}

	if got := ladopolicy.Resolve(taskInput(seeded(), claudeCode)).Prompt; got != "" {
		t.Errorf("prompt %q without prompt rules, want empty", got)
	}

	// Off rules, rules without prompt enforcement and rules without an
	// instruction give no section.
	off := promptRule("team.off", "Off", "Off rule.", 1)
	validator := withContent(domain.LadoRule{ID: "team.validator", Global: domain.LadoBindingOn, CreatedAt: seededAt}, domain.LadoRuleContent{
		Name: "Checked", Instruction: "Checked after the run.", Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementValidator},
	})
	blank := promptRule("team.blank", "Blank", "   ", 2)
	blank.Global = domain.LadoBindingOn
	if got := ladopolicy.Resolve(taskInput(append(seeded(), off, validator, blank), claudeCode)).Prompt; got != "" {
		t.Errorf("prompt %q, want empty", got)
	}

	// A rule name is one heading line.
	split := promptRule("team.split", "Tests\nbefore  commits", "Run them.", 1)
	split.Global = domain.LadoBindingOn
	if got := ladopolicy.Resolve(taskInput([]domain.LadoRule{split}, claudeCode)).Prompt; !strings.HasSuffix(got, "\n\n### Tests before commits\n\nRun them.") {
		t.Errorf("prompt %q, want one heading line", got)
	}
}
