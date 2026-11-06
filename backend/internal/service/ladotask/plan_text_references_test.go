package ladotask

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
)

// fakeReferences answers ProjectReferences like ladosetup.Service.
type fakeReferences struct {
	refs  []domain.LadoProjectReference
	err   error
	asked int
}

func (f *fakeReferences) ProjectReferences(context.Context, domain.ProjectID) ([]domain.LadoProjectReference, error) {
	f.asked++
	return f.refs, f.err
}

// referenceSessions are the demo project's sessions of every role on one
// harness.
type referenceSessions struct {
	orchestrator, worker, designer, critic domain.SessionID
}

func recordReferenceSessions(t *testing.T, pf *planFixture, harness domain.AgentHarness) referenceSessions {
	t.Helper()
	ctx := context.Background()
	record := func(kind domain.SessionKind, clientRequestID string) domain.SessionID {
		rec, err := pf.store.CreateSession(ctx, domain.SessionRecord{ProjectID: "demo", Kind: kind, Harness: harness,
			ClientRequestID: clientRequestID, ClientRequestHash: strings.Repeat("e", 64), CreatedAt: pf.now, UpdatedAt: pf.now})
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	n := len(harness)
	return referenceSessions{
		orchestrator: record(domain.KindOrchestrator, ""),
		worker:       record(domain.KindWorker, fmt.Sprintf("lado:lado-queue-%d/1", n)),
		designer:     record(domain.KindWorker, domain.LadoPlanWorkerClientRequestID(domain.LadoPlanRequestID(fmt.Sprintf("req-%d", n)), domain.LadoPlanRoleDesigner, 1)),
		critic:       record(domain.KindWorker, domain.LadoPlanWorkerClientRequestID(domain.LadoPlanRequestID(fmt.Sprintf("req-%d", n)), domain.LadoPlanRoleCritic, 1)),
	}
}

// demoReferences are both tools' skills, with one name in both folders, an
// agent and both instruction files.
var demoReferences = []domain.LadoProjectReference{
	{Kind: domain.LadoSetupKindSkill, Loader: domain.LadoSetupToolCodex, Path: ".agents/skills/codex-only/SKILL.md", Name: "codex-only", Description: "Codex skill text."},
	{Kind: domain.LadoSetupKindSkill, Loader: domain.LadoSetupToolCodex, Path: ".agents/skills/shared/SKILL.md", Name: "shared-codex", Description: "Shared, Codex copy."},
	{Kind: domain.LadoSetupKindAgent, Loader: domain.LadoSetupToolClaudeCode, Path: ".claude/agents/sync-job-debugger.md", Name: "sync-job-debugger", Description: "Finds why a sync job failed."},
	{Kind: domain.LadoSetupKindSkill, Loader: domain.LadoSetupToolClaudeCode, Path: ".claude/skills/bundle/SKILL.md", Name: "bundle-pipeline", Description: "How bundles move."},
	{Kind: domain.LadoSetupKindSkill, Loader: domain.LadoSetupToolClaudeCode, Path: ".claude/skills/shared/SKILL.md", Name: "shared-claude", Description: "Shared, Claude copy."},
	{Kind: domain.LadoSetupKindInstructions, Loader: domain.LadoSetupToolCodex, Path: "AGENTS.md", Name: "AGENTS.md"},
	{Kind: domain.LadoSetupKindInstructions, Loader: domain.LadoSetupToolClaudeCode, Path: "CLAUDE.md", Name: "CLAUDE.md"},
}

func referencesOf(t *testing.T, source PromptSource, kind domain.SessionKind, id domain.SessionID, harness domain.AgentHarness) (section, references string) {
	t.Helper()
	section, err := source.Section(context.Background(), kind, "demo", id, harness)
	if err != nil {
		t.Fatalf("Section: %v", err)
	}
	if i := strings.Index(section, referencesHeading); i >= 0 {
		references = section[i:]
	}
	return section, references
}

// listOf is the list under title in a section, up to the next blank line.
func listOf(section, title string) string {
	_, list, ok := strings.Cut(section, "\n"+title+"\n")
	if !ok {
		return ""
	}
	list, _, _ = strings.Cut(list, "\n\n")
	return list
}

// Every LADO role gets the section after the knowledge section, with its own
// sentences and none of the other roles'; only a Claude Code worker and
// orchestrator may ask the project's agents, and an orchestrator's section
// does not change whether LADO replaces AO's orchestrator role.
func TestReferencesSectionGivesEachRoleItsSentences(t *testing.T) {
	pf := newPlanFixture(t)
	claude := recordReferenceSessions(t, pf, domain.HarnessClaudeCode)
	codex := recordReferenceSessions(t, pf, domain.HarnessCodex)
	// A note, so the critic, which only reads knowledge, gets the section too.
	knowledge := &fakeKnowledge{index: ladoknowledge.Index{ProjectID: "demo", Dir: "/state/lado/knowledge/demo",
		Notes:  []domain.LadoKnowledgeEntry{{Slug: "a", Title: "A", Kind: domain.LadoKnowledgeMap, Revision: 1}},
		Prompt: "### 코드 지도 (map)\n- [[a]] — A (rev 1)\n"}}
	base := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, Knowledge: knowledge}
	source := base
	source.References = &fakeReferences{refs: demoReferences}

	const (
		designer     = "(스킬: <name>)"
		affected     = "name the file in affected_paths"
		critic       = "out-of-date skill"
		implementer  = "Before you explore the code for a step"
		orchestrator = "Use them when you investigate for the developer"
		exception    = "As the one exception to the rule above against the runtime's built-in subagents"
		stale        = "스킬 갱신 필요"
	)
	all := []string{designer, affected, critic, implementer, orchestrator, exception, stale}
	for _, tc := range []struct {
		name    string
		kind    domain.SessionKind
		id      domain.SessionID
		harness domain.AgentHarness
		want    []string
	}{
		{"claude designer", domain.KindWorker, claude.designer, domain.HarnessClaudeCode, []string{designer, affected}},
		{"codex designer", domain.KindWorker, codex.designer, domain.HarnessCodex, []string{designer, affected}},
		{"claude critic", domain.KindWorker, claude.critic, domain.HarnessClaudeCode, []string{critic}},
		{"claude implementer", domain.KindWorker, claude.worker, domain.HarnessClaudeCode, []string{implementer, exception, stale}},
		{"codex implementer", domain.KindWorker, codex.worker, domain.HarnessCodex, []string{implementer, stale}},
		{"claude orchestrator", domain.KindOrchestrator, claude.orchestrator, domain.HarnessClaudeCode, []string{orchestrator, exception}},
		{"codex orchestrator", domain.KindOrchestrator, codex.orchestrator, domain.HarnessCodex, []string{orchestrator}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section, references := referencesOf(t, source, tc.kind, tc.id, tc.harness)
			if references == "" {
				t.Fatalf("no project skills and agents section:\n%s", section)
			}
			if k := strings.Index(section, knowledgeHeading); k < 0 || k > strings.Index(section, referencesHeading) {
				t.Fatalf("the section does not follow the knowledge section:\n%s", section)
			}
			if !strings.Contains(references, referencesIntro) {
				t.Fatalf("the section lacks its introduction:\n%s", references)
			}
			for _, sentence := range all {
				want := false
				for _, w := range tc.want {
					want = want || w == sentence
				}
				if got := strings.Contains(references, sentence); got != want {
					t.Fatalf("%q present = %v, want %v:\n%s", sentence, got, want, references)
				}
			}
			if tc.kind == domain.KindOrchestrator {
				before, err := base.Standing(context.Background(), tc.kind, "demo", tc.id, tc.harness)
				if err != nil {
					t.Fatal(err)
				}
				after, err := source.Standing(context.Background(), tc.kind, "demo", tc.id, tc.harness)
				if err != nil {
					t.Fatal(err)
				}
				if before.ReplacesOrchestratorRole != after.ReplacesOrchestratorRole || !after.ReplacesOrchestratorRole {
					t.Fatalf("ReplacesOrchestratorRole %v before the section, %v after", before.ReplacesOrchestratorRole, after.ReplacesOrchestratorRole)
				}
			}
		})
	}
}

// Each tool's section lists what the tool reads by itself by name and path,
// with descriptions only for plan workers, and what it does not read with
// descriptions; a skill in both folders is listed once, from the tool's own,
// and the tool's own instruction files are not listed.
func TestReferencesSectionListsByTool(t *testing.T) {
	pf := newPlanFixture(t)
	claude := recordReferenceSessions(t, pf, domain.HarnessClaudeCode)
	codex := recordReferenceSessions(t, pf, domain.HarnessCodex)
	source := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, References: &fakeReferences{refs: demoReferences}}

	const (
		claudeLoaded  = "Loaded by Claude Code itself:"
		claudeOther   = "Not loaded by Claude Code; read them with your file reading tool:"
		codexLoaded   = "Loaded by Codex itself:"
		codexOther    = "Not loaded by Codex; read them with your file reading tool:"
		bundle        = "- skill bundle-pipeline (.claude/skills/bundle/SKILL.md)"
		bundleFull    = "- skill bundle-pipeline — How bundles move. (.claude/skills/bundle/SKILL.md)"
		agent         = "- agent sync-job-debugger (.claude/agents/sync-job-debugger.md)"
		agentFull     = "- agent sync-job-debugger — Finds why a sync job failed. (.claude/agents/sync-job-debugger.md)"
		sharedClaude  = "- skill shared-claude (.claude/skills/shared/SKILL.md)"
		codexOnly     = "- skill codex-only (.agents/skills/codex-only/SKILL.md)"
		codexOnlyFull = "- skill codex-only — Codex skill text. (.agents/skills/codex-only/SKILL.md)"
		sharedCodex   = "- skill shared-codex (.agents/skills/shared/SKILL.md)"
	)
	type want struct {
		loaded, other string
		in, notIn     []string
	}
	for _, tc := range []struct {
		name    string
		id      domain.SessionID
		harness domain.AgentHarness
		want    want
	}{
		{"claude implementer", claude.worker, domain.HarnessClaudeCode, want{claudeLoaded, claudeOther,
			[]string{bundle, agent, sharedClaude, codexOnlyFull, "- instructions AGENTS.md"},
			[]string{bundleFull, agentFull, "shared-codex", "CLAUDE.md", "How bundles move."}}},
		{"claude designer", claude.designer, domain.HarnessClaudeCode, want{claudeLoaded, claudeOther,
			[]string{bundleFull, agentFull, codexOnlyFull, "- instructions AGENTS.md"},
			[]string{"shared-codex", "CLAUDE.md"}}},
		{"codex implementer", codex.worker, domain.HarnessCodex, want{codexLoaded, codexOther,
			[]string{codexOnly, sharedCodex, bundleFull, agentFull, "- instructions CLAUDE.md"},
			[]string{codexOnlyFull, "shared-claude", "AGENTS.md"}}},
		{"codex designer", codex.designer, domain.HarnessCodex, want{codexLoaded, codexOther,
			[]string{codexOnlyFull, bundleFull, agentFull, "- instructions CLAUDE.md"},
			[]string{"shared-claude", "AGENTS.md"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, references := referencesOf(t, source, domain.KindWorker, tc.id, tc.harness)
			loaded, other := listOf(references, tc.want.loaded), listOf(references, tc.want.other)
			if loaded == "" || other == "" {
				t.Fatalf("the section lacks a list:\n%s", references)
			}
			for _, line := range tc.want.in {
				inLoaded, inOther := strings.Contains(loaded, line), strings.Contains(other, line)
				// What the tool reads by itself is in Loaded, the rest in Not loaded.
				ownTool := strings.Contains(line, ".claude/") == (tc.harness == domain.HarnessClaudeCode) && !strings.HasPrefix(line, "- instructions")
				if inLoaded != ownTool || inOther == ownTool {
					t.Fatalf("%q is in Loaded = %v and Not loaded = %v:\n%s", line, inLoaded, inOther, references)
				}
			}
			for _, text := range tc.want.notIn {
				if strings.Contains(references, text) {
					t.Fatalf("the section holds %q:\n%s", text, references)
				}
			}
		})
	}

	// harness "" is the one the session's row names.
	_, references := referencesOf(t, source, domain.KindWorker, codex.worker, "")
	if !strings.Contains(references, codexLoaded) || strings.Contains(references, claudeLoaded) {
		t.Fatalf("a Codex session without a harness got:\n%s", references)
	}
	_, references = referencesOf(t, source, domain.KindOrchestrator, claude.orchestrator, "")
	if !strings.Contains(references, claudeLoaded) {
		t.Fatalf("a Claude Code orchestrator without a harness got:\n%s", references)
	}
}

// Over a group's limit the rest is summed up in one line, and the section
// stays within 8 KiB of valid UTF-8, cut only between lines.
func TestReferencesSectionLimits(t *testing.T) {
	pf := newPlanFixture(t)
	claude := recordReferenceSessions(t, pf, domain.HarnessClaudeCode)
	references := &fakeReferences{}
	source := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, References: references}

	for i := range 41 {
		references.refs = append(references.refs, domain.LadoProjectReference{Kind: domain.LadoSetupKindSkill, Loader: domain.LadoSetupToolCodex,
			Path: fmt.Sprintf(".agents/skills/s%02d/SKILL.md", i), Name: fmt.Sprintf("s%02d", i), Description: "x"})
	}
	_, section := referencesOf(t, source, domain.KindWorker, claude.worker, domain.HarnessClaudeCode)
	other := listOf(section, "Not loaded by Claude Code; read them with your file reading tool:")
	if n := strings.Count(other, "\n- skill ") + 1; n != 40 || !strings.Contains(other, "- … and 1 more under .agents/skills/") ||
		strings.Contains(other, "s40") {
		t.Fatalf("41 skills give %d lines:\n%s", n, other)
	}

	// Long descriptions in Korean, past 8 KiB.
	references.refs = nil
	description := strings.Repeat("설명", 120)
	for i := range 60 {
		references.refs = append(references.refs, domain.LadoProjectReference{Kind: domain.LadoSetupKindAgent, Loader: domain.LadoSetupToolClaudeCode,
			Path: fmt.Sprintf(".claude/agents/a%02d.md", i), Name: fmt.Sprintf("a%02d", i), Description: description})
	}
	_, section = referencesOf(t, source, domain.KindWorker, claude.designer, domain.HarnessClaudeCode)
	if len(section) > 8<<10 || !utf8.ValidString(section) {
		t.Fatalf("the section is %d bytes, valid UTF-8 %v", len(section), utf8.ValidString(section))
	}
	lines := strings.Split(section, "\n")
	last := lines[len(lines)-1]
	kept := strings.Count(section, "\n- agent ")
	if !strings.HasPrefix(last, "- … and ") || last != fmt.Sprintf("- … and %d more", 60-kept) || kept == 0 {
		t.Fatalf("the section keeps %d agents and ends with %q", kept, last)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "- agent ") && !strings.HasSuffix(line, ".md)") {
			t.Fatalf("a line was cut: %q", line)
		}
	}
}

// No section outside LADO mode, without a reference source, with an empty
// list, for another tool or session kind; a failure is logged and the rest
// of the instructions stay.
func TestReferencesSectionLeftOut(t *testing.T) {
	pf := newPlanFixture(t)
	ctx := context.Background()
	claude := recordReferenceSessions(t, pf, domain.HarnessClaudeCode)
	other := recordReferenceSessions(t, pf, domain.AgentHarness("opencode"))
	references := &fakeReferences{refs: demoReferences}
	source := PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store, References: references}
	has := func(t *testing.T, source PromptSource, kind domain.SessionKind, project domain.ProjectID, id domain.SessionID, harness domain.AgentHarness) bool {
		t.Helper()
		section, err := source.Section(ctx, kind, project, id, harness)
		if err != nil {
			t.Fatalf("Section: %v", err)
		}
		return strings.Contains(section, referencesHeading)
	}
	if !has(t, source, domain.KindWorker, "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("a LADO worker got no section")
	}
	references.asked = 0
	if has(t, source, domain.KindOrchestrator, "other", claude.orchestrator, domain.HarnessClaudeCode) || references.asked != 0 {
		t.Fatal("a project outside LADO mode got the section")
	}
	if _, err := pf.store.SetLadoMode(ctx, "demo", false, pf.now); err != nil {
		t.Fatal(err)
	}
	if has(t, source, domain.KindWorker, "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("a project whose LADO mode is off got the section")
	}
	if _, err := pf.store.SetLadoMode(ctx, "demo", true, pf.now); err != nil {
		t.Fatal(err)
	}
	if has(t, PromptSource{Settings: pf.store, Sessions: pf.store, Policies: pf.store}, domain.KindWorker, "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("a source without references added the section")
	}
	if has(t, source, domain.SessionKind("reviewer"), "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("a reviewer got the section")
	}
	if has(t, source, domain.KindWorker, "demo", other.worker, domain.AgentHarness("opencode")) || has(t, source, domain.KindWorker, "demo", other.worker, "") {
		t.Fatal("another tool got the section")
	}
	// A list with only the tool's own instruction files lists nothing.
	references.refs = []domain.LadoProjectReference{{Kind: domain.LadoSetupKindInstructions, Loader: domain.LadoSetupToolClaudeCode, Path: "CLAUDE.md", Name: "CLAUDE.md"}}
	if has(t, source, domain.KindWorker, "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("a list of nothing to show added the section")
	}
	references.refs = nil
	if has(t, source, domain.KindWorker, "demo", claude.worker, domain.HarnessClaudeCode) {
		t.Fatal("an empty list added the section")
	}

	var logged bytes.Buffer
	references.refs, references.err = demoReferences, errors.New("git gave no answer")
	source.Log = slog.New(slog.NewTextHandler(&logged, nil))
	standing, err := source.Standing(ctx, domain.KindOrchestrator, "demo", claude.orchestrator, domain.HarnessClaudeCode)
	if err != nil || strings.Contains(standing.Text, referencesHeading) || !strings.Contains(standing.Text, "## LADO project") || !standing.ReplacesOrchestratorRole {
		t.Fatalf("a failed list = %+v, %v; want the LADO section without the list", standing, err)
	}
	if !strings.Contains(logged.String(), "git gave no answer") {
		t.Fatalf("the failure was not logged: %q", logged.String())
	}
}
