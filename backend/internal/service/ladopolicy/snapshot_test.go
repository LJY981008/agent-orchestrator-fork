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
// Korean commit rule on, a rule proj-a forces on, a rule that is off, Codex,
// and AO instructions for workers and for the orchestrator.
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
		Agent:     ladopolicy.AgentChoice{Harness: domain.HarnessCodex, Model: "gpt-5.1-codex", Source: "task"},
		AORules: []ladopolicy.AORule{
			{Kind: ladopolicy.AORuleWorkerInstructions, SHA256: hexSHA256("Use pnpm."), Chars: 9},
			{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: "docs/agents.md", SHA256: hexSHA256("Run make test."), Chars: 14},
			{Kind: ladopolicy.AORuleOrchestratorInstructions, SHA256: hexSHA256("Plan first."), Chars: 11},
		},
		AOBuild: "1.0.0",
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
	if got := ladopolicy.Resolve(shuffled); !bytes.Equal(got.JSON, first.JSON) {
		t.Errorf("shuffled input gives other bytes:\n%s\nwant\n%s", got.JSON, first.JSON)
	}

	// Empty lists encode as [], never as null.
	empty := ladopolicy.Resolve(ladopolicy.Input{ProjectID: projectA})
	for _, field := range []string{`"rules":[]`, `"aoRules":[]`, `"workflowGates":[]`, `"capabilities":[]`} {
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

// TestSnapshotFormatIsLadoPolicy1 pins the lado-policy/1 bytes: a change to a
// field, its name or its place needs a new format name.
func TestSnapshotFormatIsLadoPolicy1(t *testing.T) {
	in := fullInput(t)
	shas := []string{}
	for _, rule := range in.Rules {
		shas = append(shas, "<"+string(rule.ID)+">", rule.Version.SHA256)
	}
	want := strings.NewReplacer(append(shas, "<Use pnpm.>", hexSHA256("Use pnpm."), "<Run make test.>", hexSHA256("Run make test."), "<korean>", koreanCommitsInstruction)...).Replace(`{
		"compiler": "lado-policy/1",
		"aoBuild": "1.0.0",
		"projectId": "proj-a",
		"taskId": "task-a",
		"agent": {"harness": "codex", "model": "gpt-5.1-codex", "permissionMode": "auto"},
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
			{"ruleId": "team.style", "versionId": "ver-team.style-1", "version": 1, "sha256": "<team.style>",
			 "name": "Small commits", "protected": false, "enforcement": ["prompt"], "strength": "advisory",
			 "source": {"scope": "project", "scopeId": "proj-a", "state": "force_on"}}
		],
		"aoRules": [
			{"kind": "worker_instructions", "path": "", "sha256": "<Use pnpm.>"},
			{"kind": "worker_instructions_file", "path": "docs/agents.md", "sha256": "<Run make test.>"}
		],
		"prompt": "## LADO rules\n\nThe developer approved these rules for this run. Follow them together with the project rules above.\n\n### Commit messages in Korean\n\n<korean>\n\n### Small commits\n\nKeep each commit to one change.",
		"workflowGates": ["plan.approval.required"],
		"capabilities": [{"class": "prompt", "status": "supported"}, {"class": "permission_mode", "status": "supported"}],
		"problems": [{"code": "LADO_CODEX_ASKS_APPROVAL", "severity": "info", "details": {"mode": "auto"}}]
	}`)
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	r := ladopolicy.Resolve(in)
	if !bytes.Equal(r.JSON, compact.Bytes()) {
		t.Errorf("snapshot\n%s\nwant\n%s", r.JSON, compact.Bytes())
	}
	if ladopolicy.Compiler != "lado-policy/1" {
		t.Errorf("Compiler = %q", ladopolicy.Compiler)
	}
	wantRules := []domain.LadoPolicySnapshotRule{
		{RuleID: domain.LadoRulePermissionCeiling, VersionID: "ver-agent.permission.ceiling-1"},
		{RuleID: domain.LadoRuleKoreanCommits, VersionID: "ver-git.commit.message.korean-1"},
		{RuleID: domain.LadoRulePlanApproval, VersionID: "ver-plan.approval.required-1"},
		{RuleID: "team.style", VersionID: "ver-team.style-1"},
	}
	if got := r.SnapshotRules(); !reflect.DeepEqual(got, wantRules) {
		t.Errorf("SnapshotRules() = %+v, want %+v", got, wantRules)
	}
	if r.Blocking() {
		t.Errorf("Blocking() with problems %+v", r.Problems)
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
			change: func(t *testing.T, in *ladopolicy.Input) { in.Agent.Harness = domain.HarnessClaudeCode },
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
