package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func ladoKoreanCommitsContent() domain.LadoRuleContent {
	return domain.LadoRuleContent{
		Name:        "Commit messages in Korean",
		Category:    "git",
		Instruction: "Write Git commit subjects and bodies primarily in Korean.",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
		Config:      json.RawMessage(`{}`),
		Conflicts:   []domain.LadoRuleID{"rule.b", "rule.a"},
	}
}

func TestLadoRuleContentIsCanonicalJSON(t *testing.T) {
	content := domain.LadoRuleContent{
		Name:        "  Keep <b>tags</b> & more \n",
		Category:    " git ",
		Instruction: "\tUse <kbd>Enter</kbd>.\n",
		Conflicts:   []domain.LadoRuleID{"rule.b", "rule.a", "rule.b"},
	}
	want := `{"name":"Keep <b>tags</b> & more","category":"git","instruction":"Use <kbd>Enter</kbd>.","enforcement":[],"config":{},"conflictsWith":["rule.a","rule.b"]}`
	if got := string(content.CanonicalJSON()); got != want {
		t.Fatalf("CanonicalJSON = %s\nwant %s", got, want)
	}
	sum := sha256.Sum256([]byte(want))
	if got := content.SHA256(); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("SHA256 = %s, want the sha256 of the canonical JSON", got)
	}
	if !slices.Equal(content.Conflicts, []domain.LadoRuleID{"rule.b", "rule.a", "rule.b"}) {
		t.Fatalf("normalizing changed the caller's conflicts: %v", content.Conflicts)
	}
	normalized := content.Normalize()
	if normalized.Enforcement == nil || normalized.Conflicts == nil {
		t.Fatalf("Normalize left a nil slice: %+v", normalized)
	}
	if normalized.SHA256() != content.SHA256() {
		t.Fatal("normalizing twice changed the hash")
	}
}

func TestLadoRuleContentHashIgnoresConflictOrder(t *testing.T) {
	content := ladoKoreanCommitsContent()
	reordered := content
	reordered.Conflicts = []domain.LadoRuleID{"rule.a", "rule.b", "rule.a", "rule.b"}
	if reordered.SHA256() != content.SHA256() {
		t.Fatalf("conflicts in another order with duplicates hash %s, want %s", reordered.SHA256(), content.SHA256())
	}
	renamed := content
	renamed.Name = "Commit messages in korean"
	if renamed.SHA256() == content.SHA256() {
		t.Fatal("a name one letter apart kept the hash")
	}
	fewer := content
	fewer.Conflicts = []domain.LadoRuleID{"rule.a"}
	if fewer.SHA256() == content.SHA256() {
		t.Fatal("dropping a conflict kept the hash")
	}
}

func TestLadoRuleContentHashCompactsConfig(t *testing.T) {
	ceiling := func(config json.RawMessage) domain.LadoRuleContent {
		return domain.LadoRuleContent{
			Name:        "Permission ceiling",
			Category:    "tools",
			Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementToolPolicy},
			Config:      config,
		}
	}
	compact := ceiling(json.RawMessage(`{"mode":"auto"}`)).SHA256()
	for _, spaced := range []string{`{"mode": "auto"}`, "{\n  \"mode\" : \"auto\"\n}\n"} {
		if got := ceiling(json.RawMessage(spaced)).SHA256(); got != compact {
			t.Errorf("config %q hashes %s, want the hash of {\"mode\":\"auto\"} %s", spaced, got, compact)
		}
	}
	empty := ceiling(json.RawMessage(`{}`)).SHA256()
	for name, config := range map[string]json.RawMessage{
		"nil":   nil,
		"empty": json.RawMessage(""),
		"blank": json.RawMessage(" \n"),
		"null":  json.RawMessage("null"),
	} {
		if got := ceiling(config).SHA256(); got != empty {
			t.Errorf("%s config hashes %s, want the hash of {} %s", name, got, empty)
		}
	}
	if ceiling(json.RawMessage(`{"mode":"accept-edits"}`)).SHA256() == compact {
		t.Fatal("another ceiling mode kept the hash")
	}
}

func TestLadoRuleContentStrength(t *testing.T) {
	const (
		prompt       = domain.LadoEnforcementPrompt
		validator    = domain.LadoEnforcementValidator
		toolPolicy   = domain.LadoEnforcementToolPolicy
		workflowGate = domain.LadoEnforcementWorkflowGate
	)
	for _, tc := range []struct {
		name        string
		enforcement []domain.LadoEnforcement
		want        domain.LadoStrength
	}{
		{"workflow gate", []domain.LadoEnforcement{workflowGate}, domain.LadoStrengthPrevents},
		{"tool policy", []domain.LadoEnforcement{toolPolicy}, domain.LadoStrengthPrevents},
		{"prompt and tool policy", []domain.LadoEnforcement{prompt, toolPolicy}, domain.LadoStrengthPrevents},
		{"validator and workflow gate", []domain.LadoEnforcement{validator, workflowGate}, domain.LadoStrengthPrevents},
		{"validator", []domain.LadoEnforcement{validator}, domain.LadoStrengthDetects},
		{"prompt and validator", []domain.LadoEnforcement{prompt, validator}, domain.LadoStrengthDetects},
		{"prompt only", []domain.LadoEnforcement{prompt}, domain.LadoStrengthAdvisory},
		{"no enforcement", nil, domain.LadoStrengthAdvisory},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := domain.LadoRuleContent{Name: "Rule", Enforcement: tc.enforcement}
			if got := content.Strength(); got != tc.want {
				t.Fatalf("Strength = %q, want %q", got, tc.want)
			}
			for _, e := range []domain.LadoEnforcement{prompt, validator, toolPolicy, workflowGate} {
				if got, want := content.Has(e), slices.Contains(tc.enforcement, e); got != want {
					t.Fatalf("Has(%q) = %v, want %v", e, got, want)
				}
			}
		})
	}
}

func TestLadoPermissionRank(t *testing.T) {
	acceptEdits := domain.LadoPermissionRank(domain.PermissionModeAcceptEdits)
	auto := domain.LadoPermissionRank(domain.PermissionModeAuto)
	bypass := domain.LadoPermissionRank(domain.PermissionModeBypassPermissions)
	if acceptEdits != 1 || auto != 2 || bypass != 3 {
		t.Fatalf("ranks accept-edits=%d auto=%d bypass-permissions=%d, want 1 < 2 < 3", acceptEdits, auto, bypass)
	}
	for i, mode := range domain.LadoCeilingModes {
		if got := domain.LadoPermissionRank(mode); got != i+1 {
			t.Fatalf("LadoCeilingModes[%d] = %q ranks %d, want the list lowest first", i, mode, got)
		}
	}
	for _, mode := range []domain.PermissionMode{domain.PermissionModeDefault, "", "plan", "Auto"} {
		if got := domain.LadoPermissionRank(mode); got != 0 {
			t.Errorf("LadoPermissionRank(%q) = %d, want 0: it is never a ceiling", mode, got)
		}
	}
}
