package ladoknowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// WriteInput is one note a session writes.
type WriteInput struct {
	SessionID domain.SessionID
	Slug      string
	// BaseRevision is the revision the writer read; nil writes a new note.
	BaseRevision *int
	// Content is the whole note: front matter (title, kind, sources) and
	// body. LADO renders commit, revision, updated, author and the sources'
	// blobs itself.
	Content []byte
}

// Write stores a note for a session of a LADO mode project, in that
// session's project. The project comes from the session, never from the
// request.
//
// A new note (no base) must not exist yet (LADO_KNOWLEDGE_EXISTS); an
// update's base must be the note's revision after edits made outside LADO
// were taken (LADO_KNOWLEDGE_CHANGED with currentRevision). A note file LADO
// cannot read is never written over (LADO_KNOWLEDGE_EXISTS with
// details.unreadable): the owner fixes or removes it first. A plan critic
// only reads knowledge (LADO_KNOWLEDGE_READ_ONLY). Each source's blob is
// what git hash-object gives for the file in the session's folder, changes
// not yet committed included; a missing file refuses the note.
func (s *Service) Write(ctx context.Context, in WriteInput) (domain.LadoKnowledgeEntry, error) {
	if !domain.ValidLadoKnowledgeSlug(in.Slug) {
		return domain.LadoKnowledgeEntry{}, apierr.Invalid(CodeInvalidNote,
			"A note's slug is 1 to 64 of a-z, 0-9 and '-', starting with a letter or digit.", map[string]any{"field": "slug"})
	}
	if in.BaseRevision != nil && *in.BaseRevision < 1 {
		return domain.LadoKnowledgeEntry{}, apierr.Invalid(CodeInvalidNote, "baseRevision is a revision, 1 or more; leave it out for a new note.", map[string]any{"field": "baseRevision"})
	}
	if len(in.Content) > domain.LadoKnowledgeNoteMaxBytes {
		return domain.LadoKnowledgeEntry{}, apierr.Invalid(CodeInvalidNote, "A note is at most 64 KiB.", map[string]any{"field": "content"})
	}
	rec, p, err := s.writer(ctx, in.SessionID)
	if err != nil {
		return domain.LadoKnowledgeEntry{}, err
	}
	note, err := domain.ParseLadoKnowledgeNote(in.Slug, in.Content)
	if err != nil {
		return domain.LadoKnowledgeEntry{}, noteInvalid(err)
	}
	if kind := domain.LadoKnowledgeSecret(in.Content); kind != "" {
		return domain.LadoKnowledgeEntry{}, apierr.Invalid(CodeSecret,
			"The note holds what looks like a credential. Notes go into later sessions' instructions; leave secrets out.", map[string]any{"kind": kind})
	}

	// Git runs before the lock.
	workdir := rec.Metadata.WorkspacePath
	if workdir == "" {
		workdir = p.Path
	}
	if err := s.hashSources(ctx, workdir, note.Sources); err != nil {
		return domain.LadoKnowledgeEntry{}, err
	}
	commit := ""
	if out, err := s.git.Run(ctx, workdir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	cur := s.identity(ctx, p)
	projectID := domain.ProjectID(p.ID)

	f := s.folder(projectID)
	unlock := s.lock(projectID)
	entry, events, err := s.writeLocked(f, cur, note, in, commit)
	unlock()
	s.audit(ctx, events...)
	if err != nil {
		return domain.LadoKnowledgeEntry{}, err
	}
	s.audit(ctx, s.event(projectID, "session:"+string(in.SessionID), AuditNoteWritten, map[string]any{
		"slug": in.Slug, "revision": entry.Revision, "sessionId": string(in.SessionID), "kind": string(note.Kind),
	}))
	if states, ok := s.states(ctx, p, map[string][]domain.LadoKnowledgeSource{in.Slug: note.Sources}); ok {
		entry.State = states[in.Slug]
	}
	return entry, nil
}

func (s *Service) writeLocked(f folder, cur Owner, note domain.LadoKnowledgeNote, in WriteInput, commit string) (domain.LadoKnowledgeEntry, []domain.LadoAuditEvent, error) {
	if err := f.ensure(); err != nil {
		return domain.LadoKnowledgeEntry{}, nil, apierr.Internal(CodeFailed, "LADO could not create the knowledge folder")
	}
	if err := s.ownLocked(f, cur); err != nil {
		return domain.LadoKnowledgeEntry{}, nil, err
	}
	snap, events, err := s.syncLocked(cur.ProjectID, f)
	if err != nil {
		return domain.LadoKnowledgeEntry{}, events, apierr.Internal(CodeFailed, "LADO could not read the knowledge folder")
	}
	prev, exists := snap.records[in.Slug]
	if why, ok := snap.unreadable[in.Slug]; ok {
		return domain.LadoKnowledgeEntry{}, events, apierr.Conflict(CodeExists,
			fmt.Sprintf("notes/%s.md is in the knowledge folder, but LADO cannot read it: %s. Ask the owner to fix or remove that file, or write the note under another slug.", in.Slug, why),
			map[string]any{"unreadable": true})
	}
	switch {
	case in.BaseRevision == nil && exists:
		return domain.LadoKnowledgeEntry{}, events, apierr.Conflict(CodeExists,
			fmt.Sprintf("The note exists at revision %d. Read it, then update it with that revision as the base.", prev.Revision),
			map[string]any{"currentRevision": prev.Revision})
	case in.BaseRevision != nil && (!exists || *in.BaseRevision != prev.Revision):
		return domain.LadoKnowledgeEntry{}, events, apierr.Conflict(CodeChanged,
			fmt.Sprintf("The note is at revision %d now. Read it again, merge, and send it with revision %d as the base.", prev.Revision, prev.Revision),
			map[string]any{"currentRevision": prev.Revision})
	case !exists && snap.count() >= domain.LadoKnowledgeMaxNotes:
		return domain.LadoKnowledgeEntry{}, events, apierr.Invalid(CodeInvalidNote,
			"The project has the most notes LADO keeps. Update or merge existing notes instead.", map[string]any{"limit": domain.LadoKnowledgeMaxNotes})
	}
	note.Revision = prev.Revision + 1
	note.Updated = s.now().UTC()
	note.Author = string(in.SessionID)
	note.Commit = commit
	data := domain.RenderLadoKnowledgeNote(note)
	if len(data) > domain.LadoKnowledgeNoteMaxBytes {
		return domain.LadoKnowledgeEntry{}, events, apierr.Invalid(CodeInvalidNote, "The note with LADO's front matter is larger than 64 KiB.", map[string]any{"field": "content"})
	}
	if err := writeAtomic(f.notePath(in.Slug), data); err != nil {
		return domain.LadoKnowledgeEntry{}, events, apierr.Internal(CodeFailed, "LADO could not write the note")
	}
	r := noteRecord{Revision: note.Revision, SHA256: sha256Hex(data), Author: note.Author, Updated: note.Updated}
	snap.records[in.Slug], snap.files[in.Slug] = r, data
	if err := s.saveLocked(f, snap); err != nil {
		s.log.Warn("LADO wrote a note but could not save its record", "project", cur.ProjectID, "note", in.Slug, "err", err)
		return domain.LadoKnowledgeEntry{}, events, apierr.Internal(CodeFailed, "LADO could not record the note")
	}
	return entryOf(note, r), events, nil
}

// writer resolves the session that writes and its project, and refuses a
// session that may not.
func (s *Service) writer(ctx context.Context, id domain.SessionID) (domain.SessionRecord, domain.ProjectRecord, error) {
	if id == "" {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Invalid(CodeInvalidNote, "sessionId is required.", map[string]any{"field": "sessionId"})
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Internal(CodeFailed, "LADO could not read the session")
	}
	if !ok {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.NotFound(CodeSessionNotFound, "Session not found")
	}
	if rec.IsTerminated {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Conflict(CodeSessionEnded, "The session has ended; it can no longer write knowledge.", nil)
	}
	if rec.ProjectID == "" {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Conflict(CodeNotLadoProject, "Only sessions of a LADO mode project write knowledge.", nil)
	}
	p, err := s.project(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, domain.ProjectRecord{}, err
	}
	settings, err := s.store.LadoProjectSettings(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Internal(CodeFailed, "LADO could not read the project's settings")
	}
	if !settings.LadoMode {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Conflict(CodeNotLadoProject, "Only sessions of a LADO mode project write knowledge.", nil)
	}
	if _, role, _, ok := domain.ParseLadoPlanWorkerClientRequestID(rec.ClientRequestID); ok && role == domain.LadoPlanRoleCritic {
		return domain.SessionRecord{}, domain.ProjectRecord{}, apierr.Conflict(CodeReadOnly, "A plan critic only reads project knowledge.", nil)
	}
	return rec, p, nil
}

// hashSources fills each source's blob with git hash-object in the
// session's folder, one call for all. A source that is not a regular file
// there refuses the note; when git cannot hash, the blobs stay empty and
// the note is unchecked.
func (s *Service) hashSources(ctx context.Context, workdir string, sources []domain.LadoKnowledgeSource) error {
	if len(sources) == 0 {
		return nil
	}
	args := []string{"hash-object", "--"}
	for i, src := range sources {
		sources[i].Blob = ""
		info, err := os.Lstat(filepath.Join(workdir, filepath.FromSlash(src.Path)))
		if err != nil || !info.Mode().IsRegular() {
			reason := "is not a file in the session's folder"
			if errors.Is(err, os.ErrNotExist) {
				reason = "does not exist in the session's folder"
			}
			return apierr.Invalid(CodeInvalidNote, "Source "+src.Path+" "+reason+".", map[string]any{"field": "sources", "path": src.Path})
		}
		args = append(args, src.Path)
	}
	out, err := s.git.Run(ctx, workdir, args...)
	if err != nil {
		s.log.Warn("LADO could not hash a note's sources; the note stays unchecked", "dir", workdir, "err", err)
		return nil
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(sources) {
		return nil
	}
	for i := range sources {
		sources[i].Blob = lines[i]
	}
	return nil
}

func noteInvalid(err error) error {
	var ne *domain.LadoKnowledgeNoteError
	details := map[string]any{}
	if errors.As(err, &ne) && ne.Field != "" {
		details["field"] = ne.Field
	}
	return apierr.Invalid(CodeInvalidNote, "The note is not valid: "+err.Error(), details)
}
