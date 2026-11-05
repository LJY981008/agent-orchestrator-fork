package controllers

// LADO fork: project knowledge DTOs (issue #24,
// docs/LJY-PRD/adr/0010-project-knowledge-store.md).

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoknowledge"
)

// LadoKnowledgeSlugParam names a knowledge note.
type LadoKnowledgeSlugParam struct {
	Slug string `path:"slug" description:"The note's slug: 1 to 64 of a-z, 0-9 and '-', starting with a letter or digit. It is the file name notes/<slug>.md."`
}

// LadoKnowledgeNoteResponse is one note as the knowledge index lists it.
type LadoKnowledgeNoteResponse struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Kind is map, decision, pitfall, research or convention; a note edited
	// outside LADO may carry another value.
	Kind string `json:"kind"`
	// Revision is the note's revision as LADO records it; an update sends it
	// as baseRevision.
	Revision  int       `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Author is the session id that wrote the revision, external for an edit
	// made in the folder, or import:<source> for an imported note.
	Author string `json:"author"`
	// State compares the note's source files with the project's base
	// branch: fresh when every blob matches, stale when one differs or is
	// gone, unchecked when the note has no sources or git could not tell.
	State string `json:"state" enum:"fresh,stale,unchecked"`
}

// LadoKnowledgeOwnerResponse is the project its .lado/owner.json records:
// the one that first wrote the folder or last claimed it.
type LadoKnowledgeOwnerResponse struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	// RootCommit is the repository's first commit; empty for a workspace
	// project or a folder git could not read.
	RootCommit string    `json:"rootCommit,omitempty"`
	ClaimedAt  time.Time `json:"claimedAt"`
}

// LadoKnowledgeImportableResponse is another knowledge folder the project can
// import notes from.
type LadoKnowledgeImportableResponse struct {
	ProjectID string `json:"projectId"`
	Notes     int    `json:"notes"`
	// OwnerPath is the path the folder's owner.json records, empty without
	// one.
	OwnerPath string `json:"ownerPath,omitempty"`
	// Registered says an active project has the id; false for an archived
	// project's folder or one whose project is gone.
	Registered bool `json:"registered"`
}

// LadoKnowledgeResponse is a project's knowledge folder for the app.
type LadoKnowledgeResponse struct {
	ProjectID string `json:"projectId"`
	// Dir is the knowledge folder; it may not exist before the first note.
	Dir string `json:"dir"`
	// Foreign is set when the folder belongs to another repository than the
	// project. Notes are then left out (and out of sessions' instructions)
	// until the project claims the folder; Owner says whose it is.
	Foreign bool                        `json:"foreign"`
	Owner   *LadoKnowledgeOwnerResponse `json:"owner,omitempty"`
	// Notes are sorted by slug.
	Notes []LadoKnowledgeNoteResponse `json:"notes"`
	// Truncated says the index sessions get leaves notes out to stay in its
	// size cap.
	Truncated bool `json:"truncated"`
	// Importable are the other knowledge folders with notes, archived
	// projects' and orphaned folders included.
	Importable []LadoKnowledgeImportableResponse `json:"importable"`
}

// WriteLadoKnowledgeNoteRequest writes a note for a session. The note goes to
// the session's project; the session must be running in a LADO mode project
// and not be a plan critic.
type WriteLadoKnowledgeNoteRequest struct {
	SessionID string `json:"sessionId"`
	// Content is the whole note: YAML front matter (title, kind, sources)
	// and a Markdown body, at most 64 KiB. LADO fills in commit, revision,
	// updated, author and the sources' blobs.
	Content string `json:"content"`
	// BaseRevision is the revision an update starts from; leave it out for
	// a new note.
	BaseRevision *int `json:"baseRevision,omitempty" nullable:"false"`
}

// ImportLadoKnowledgeRequest imports notes into the project's knowledge.
type ImportLadoKnowledgeRequest struct {
	// Source is claude_memory (Claude Code's memory of a project path) or
	// project (another knowledge folder).
	Source string `json:"source" enum:"claude_memory,project"`
	// Path picks the absolute project path whose Claude Code memory to read;
	// empty tries the project's path. Only for claude_memory.
	Path string `json:"path,omitempty"`
	// FromProjectID is the knowledge folder to import from. Required for
	// project.
	FromProjectID string `json:"fromProjectId,omitempty"`
}

// LadoKnowledgeImportSkipResponse is something an import left out.
type LadoKnowledgeImportSkipResponse struct {
	// Name is a file name, a slug or, for claude_memory_not_found, the
	// encoded folder name LADO looked for.
	Name   string `json:"name"`
	Reason string `json:"reason" enum:"claude_memory_not_found,exists,link,not_a_file,too_large,invalid_name,invalid_note,secret,limit,unreadable"`
}

// LadoKnowledgeImportResponse says what an import made into notes.
type LadoKnowledgeImportResponse struct {
	// Imported are the slugs of the new notes.
	Imported []string                          `json:"imported"`
	Skipped  []LadoKnowledgeImportSkipResponse `json:"skipped"`
}

func ladoKnowledgeNoteResponse(e domain.LadoKnowledgeEntry) LadoKnowledgeNoteResponse {
	state := string(e.State)
	if state == "" {
		state = string(domain.LadoKnowledgeUnchecked)
	}
	return LadoKnowledgeNoteResponse{
		Slug: e.Slug, Title: e.Title, Kind: string(e.Kind), Revision: e.Revision,
		UpdatedAt: e.UpdatedAt, Author: e.Author, State: state,
	}
}

func ladoKnowledgeResponse(idx ladoknowledge.Index, importable []ladoknowledge.Importable) LadoKnowledgeResponse {
	resp := LadoKnowledgeResponse{
		ProjectID:  string(idx.ProjectID),
		Dir:        idx.Dir,
		Foreign:    idx.Foreign,
		Notes:      make([]LadoKnowledgeNoteResponse, 0, len(idx.Notes)),
		Truncated:  idx.Truncated,
		Importable: make([]LadoKnowledgeImportableResponse, 0, len(importable)),
	}
	if idx.Owner != nil {
		resp.Owner = &LadoKnowledgeOwnerResponse{
			ProjectID: string(idx.Owner.ProjectID), Path: idx.Owner.Path,
			RootCommit: idx.Owner.RootCommit, ClaimedAt: idx.Owner.ClaimedAt,
		}
	}
	for _, n := range idx.Notes {
		resp.Notes = append(resp.Notes, ladoKnowledgeNoteResponse(n))
	}
	for _, item := range importable {
		resp.Importable = append(resp.Importable, LadoKnowledgeImportableResponse{
			ProjectID: string(item.ProjectID), Notes: item.Notes, OwnerPath: item.OwnerPath, Registered: item.Registered,
		})
	}
	return resp
}

func ladoKnowledgeImportResponse(result ladoknowledge.ImportResult) LadoKnowledgeImportResponse {
	resp := LadoKnowledgeImportResponse{
		Imported: append([]string{}, result.Imported...),
		Skipped:  make([]LadoKnowledgeImportSkipResponse, 0, len(result.Skipped)),
	}
	for _, s := range result.Skipped {
		resp.Skipped = append(resp.Skipped, LadoKnowledgeImportSkipResponse{Name: s.Name, Reason: s.Reason})
	}
	return resp
}
