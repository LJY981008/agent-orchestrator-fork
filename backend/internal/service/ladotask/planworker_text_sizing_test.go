package ladotask

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sizingDraft is a draft plan of the given number of steps, split into the
// given parts, each written as "id:first-last" over one path of its own.
func sizingDraft(steps int, parts ...string) []byte {
	var b strings.Builder
	b.WriteString("---\ntask: lado-task-1\nissue: github:acme/demo#1\nbase_ref: main\nsteps:\n")
	for i := 1; i <= steps; i++ {
		fmt.Fprintf(&b, "  - Step %d\n", i)
	}
	b.WriteString("acceptance_criteria:\n  - It works\naffected_paths:\n  - backend/**\n" +
		"test_commands: []\nrisk: low\npermission_ceiling: auto\nreview:\n  required: true\nplan_source: issue\n")
	if len(parts) > 0 {
		b.WriteString("parts:\n")
	}
	for _, part := range parts {
		id, span, _ := strings.Cut(part, ":")
		from, to, _ := strings.Cut(span, "-")
		first, _ := strconv.Atoi(from)
		last, _ := strconv.Atoi(to)
		numbers := []string{}
		for n := first; n <= last; n++ {
			numbers = append(numbers, fmt.Sprint(n))
		}
		fmt.Fprintf(&b, "  - id: %s\n    title: The %s part\n    steps: [%s]\n    affected_paths:\n      - backend/%s/**\n    depends_on: []\n",
			id, id, strings.Join(numbers, ", "), id)
	}
	b.WriteString("---\n\nPlan.\n")
	return []byte(b.String())
}

// The critic hears the sizing limits in every review and is told to report
// them as major findings.
func TestCriticPromptStatesThePartSizingLimits(t *testing.T) {
	prompt := criticPrompt(domain.LadoPlanRequest{ID: "req-1"}, sizingDraft(8, "store:1-3", "api:4-6", "ui:7-8"))
	for _, want := range []string{
		"## Part sizing", "at most 4 steps", "about 5 files", "2 to 3 steps in one package or one area",
		"more than 4 steps or more than 8 affected_paths", "more than 200 files", "git -C <repository folder> ls-files",
		"more than 10000 files", "major finding", "compacts again and again", "costs more than the work",
		"a small plan split into parts", "a part of too many steps", "paths or files are too wide", "several one-step parts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("critic prompt lacks %q:\n%s", want, prompt)
		}
	}
	if sizing, draft := strings.Index(prompt, "## Part sizing"), strings.Index(prompt, "## Draft"); sizing > draft {
		t.Fatalf("the sizing section comes after the draft:\n%s", prompt)
	}
	if strings.Contains(prompt, "size warnings:") {
		t.Fatalf("a draft within the limits lists warnings:\n%s", prompt)
	}
}

// A small plan split into parts is named in the critic's sizing section.
func TestCriticPromptListsASmallSplitPlan(t *testing.T) {
	prompt := criticPrompt(domain.LadoPlanRequest{ID: "req-1"}, sizingDraft(3, "backend:1-2", "frontend:3-3"))
	section := prompt[strings.Index(prompt, "## Part sizing"):strings.Index(prompt, "## Draft")]
	for _, want := range []string{"size warnings:", domain.LadoPlanWarningSmallSplit + ": the plan has 3 steps, at most 4, and names 2 parts"} {
		if !strings.Contains(section, want) {
			t.Fatalf("sizing section lacks %q:\n%s", want, section)
		}
	}
}

// A part of too many steps is named, with its count and the limit.
func TestCriticPromptListsAPartOfManySteps(t *testing.T) {
	prompt := criticPrompt(domain.LadoPlanRequest{ID: "req-1"}, sizingDraft(8, "core:1-6", "ui:7-8"))
	section := prompt[strings.Index(prompt, "## Part sizing"):strings.Index(prompt, "## Draft")]
	want := "part core: " + domain.LadoPlanPartWarningSteps + ", 6 over the limit of 4"
	if !strings.Contains(section, want) {
		t.Fatalf("sizing section lacks %q:\n%s", want, section)
	}
	if strings.Contains(section, "part ui:") || strings.Contains(section, domain.LadoPlanWarningSmallSplit) {
		t.Fatalf("sizing section warns of more than the core part:\n%s", section)
	}
}
