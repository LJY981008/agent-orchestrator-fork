package ladosetup

// The developer's instructions for AO's reviewer (step 8 plan decision s).
// AO launches its reviewer on a setup of its own, so none of the setup LADO
// delivers to sessions reaches it, and adoption took the instructions out of
// the tool's folder. LADO gives the reviewer the adopted instructions of its
// tool as a section of its system prompt, so it reviews by the conventions
// the worker follows. Settings, hooks, skills, agents, commands and MCP
// servers never reach a reviewer, so its read-only tool policy holds.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reviewerInstructionsHeading heads LADO's section of a reviewer's system
// prompt, as it heads the Codex instructions of a session's standing
// instructions.
const reviewerInstructionsHeading = "## User instructions (managed by LADO)"

// reviewerInstructionsLead says what the section is for.
const reviewerInstructionsLead = "The developer's own instructions, which LADO manages and the worker follows. " +
	"Review the change by their conventions as well; they do not change your review task or what you may do."

// ReviewerSection returns LADO's section of the system prompt of the reviewer
// on harness that reviews the worker session: the instructions LADO manages
// for the reviewer's tool under reviewerInstructionsHeading, one subsection
// per file. For Claude Code these are the on top-level Markdown files,
// CLAUDE.md first; for Codex the first of AGENTS.override.md and AGENTS.md
// with text, as Codex picks it. A reviewer on the worker's own tool reads the
// versions the worker gets: those its LADO execution's rule snapshot pinned,
// so an edit after the approval never reaches the review. A reviewer on
// another tool reads the current versions that are on for the worker's
// project, since a snapshot pins only the worker's tool. It is "" for a
// reviewer on any other tool, for a worker the store does not have, while
// LADO manages no item of the tool (before an adoption) and when no file has
// text.
func (s *Service) ReviewerSection(ctx context.Context, worker domain.SessionID, harness domain.AgentHarness) (string, error) {
	tool := domain.LadoSetupTool(harness)
	if s.deps.Store == nil || (tool != domain.LadoSetupToolClaudeCode && tool != domain.LadoSetupToolCodex) {
		return "", nil
	}
	if s.deps.Sessions == nil {
		return "", errors.New("LADO setup has no session reader")
	}
	rec, found, err := s.deps.Sessions.GetSession(ctx, worker)
	if err != nil {
		return "", fmt.Errorf("read the worker session %s: %w", worker, err)
	}
	if !found {
		return "", nil
	}
	request := ""
	if domain.LadoSetupTool(rec.Harness) == tool {
		request = rec.ClientRequestID
	}
	items, _, err := s.sessionItems(ctx, tool, rec.ProjectID, request)
	if err != nil {
		return "", err
	}
	files := slices.DeleteFunc(items, func(iv domain.LadoSetupItemWithVersion) bool {
		return iv.Item.Kind != domain.LadoSetupKindInstructions
	})
	if len(files) == 0 {
		return "", nil
	}
	if s.deps.Blobs == nil {
		return "", errors.New("LADO setup has no blob store")
	}
	slices.SortFunc(files, func(a, b domain.LadoSetupItemWithVersion) int {
		return cmp.Or(cmp.Compare(instructionRank(tool, a.Item.Name), instructionRank(tool, b.Item.Name)), strings.Compare(a.Item.Name, b.Item.Name))
	})
	var parts []string
	for _, iv := range files {
		data, err := s.deps.Blobs.Get(iv.Version.SHA256, maxBlobBytes)
		if err != nil {
			return "", fmt.Errorf("read %s from its blob: %w", iv.Item.Name, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		parts = append(parts, "### "+iv.Item.Name+"\n\n"+text)
		if tool == domain.LadoSetupToolCodex {
			// Codex reads the first file with text only.
			break
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return reviewerInstructionsHeading + "\n\n" + reviewerInstructionsLead + "\n\n" + strings.Join(parts, "\n\n"), nil
}

// instructionRank orders a tool's instruction files: Claude Code's CLAUDE.md
// before the other top-level files, and Codex's in codexInstructionOrder.
func instructionRank(tool domain.LadoSetupTool, name string) int {
	if tool == domain.LadoSetupToolCodex {
		if i := slices.Index(codexInstructionOrder, name); i >= 0 {
			return i
		}
		return len(codexInstructionOrder)
	}
	if name == "CLAUDE.md" {
		return 0
	}
	return 1
}
