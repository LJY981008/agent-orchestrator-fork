package ladoknowledge

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Import sources.
const (
	SourceClaudeMemory = "claude_memory"
	SourceProject      = "project"
)

// Reasons an import skips something.
const (
	SkipClaudeMemoryNotFound = "claude_memory_not_found"
	SkipExists               = "exists"
	SkipLink                 = "link"
	SkipNotAFile             = "not_a_file"
	SkipTooLarge             = "too_large"
	SkipInvalidName          = "invalid_name"
	SkipInvalidNote          = "invalid_note"
	SkipSecret               = "secret"
	SkipLimit                = "limit"
	SkipUnreadable           = "unreadable"
)

// ImportResult says what an import made into notes and what it left.
type ImportResult struct {
	// Imported are the slugs of the new notes.
	Imported []string
	Skipped  []ImportSkip
}

// ImportSkip is something an import did not take: a file name, a slug or,
// for SkipClaudeMemoryNotFound, the encoded folder name it looked for.
type ImportSkip struct {
	Name   string
	Reason string
}

// pending is a note an import is about to write; name is what it came from.
type pending struct {
	name string
	note domain.LadoKnowledgeNote
}

// ClaudeProjectDirName is the folder name Claude Code gives a project path
// under ~/.claude/projects: every UTF-16 code unit that is not ASCII
// [A-Za-z0-9] becomes '-', as its path.replace(/[^a-zA-Z0-9]/g, "-") does.
// Claude Code shortens very long names and adds a hash; those are not
// supported.
func ClaudeProjectDirName(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		n := utf16.RuneLen(r)
		if n < 1 {
			n = 1
		}
		b.WriteString(strings.Repeat("-", n))
	}
	return b.String()
}

// ImportClaudeMemory copies the project's Claude Code memory files into
// notes. It reads <ClaudeDir>/projects/<ClaudeProjectDirName(path)>/memory:
// the regular .md files other than MEMORY.md, no links, at most a note's
// size. The memory's type (top-level or metadata.type) becomes the kind:
// project → decision, reference → research, feedback and user →
// convention, anything else research. Imported notes have no commit or
// sources, so they are unchecked. A slug the project has is skipped. It
// never changes the memory files.
//
// path picks the project path whose memory to read; empty tries the
// project's path as registered and with links resolved. When no memory
// folder exists, the result names each folder it tried as
// SkipClaudeMemoryNotFound.
func (s *Service) ImportClaudeMemory(ctx context.Context, projectID domain.ProjectID, path string) (ImportResult, error) {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return ImportResult{}, err
	}
	var paths []string
	if path = strings.TrimSpace(path); path != "" {
		if !filepath.IsAbs(path) {
			return ImportResult{}, apierr.Invalid(CodeInvalidImport, "The path of Claude Code memory to import is an absolute folder path.", map[string]any{"field": "path"})
		}
		paths = []string{filepath.Clean(path)}
	} else {
		paths = []string{filepath.Clean(p.Path)}
		if resolved, err := filepath.EvalSymlinks(p.Path); err == nil && resolved != paths[0] {
			paths = append(paths, resolved)
		}
	}
	var names []string
	for _, candidate := range paths {
		if name := ClaudeProjectDirName(candidate); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	var result ImportResult
	var notes []pending
	found := false
	for _, name := range names {
		dir := ""
		if s.claudeDir != "" {
			dir = filepath.Join(s.claudeDir, "projects", name, "memory")
		}
		info, err := os.Stat(dir)
		if dir == "" || err != nil || !info.IsDir() {
			continue
		}
		found = true
		got, skipped := s.readClaudeMemory(dir)
		notes = append(notes, got...)
		result.Skipped = append(result.Skipped, skipped...)
	}
	if !found {
		for _, name := range names {
			result.Skipped = append(result.Skipped, ImportSkip{Name: name, Reason: SkipClaudeMemoryNotFound})
		}
		s.audit(ctx, s.event(projectID, "api", AuditImported, map[string]any{"source": SourceClaudeMemory, "folders": names, "imported": []string{}, "skipped": result.Skipped}))
		return result, nil
	}
	return s.importNotes(ctx, p, SourceClaudeMemory, map[string]any{"folders": names}, notes, result)
}

// readClaudeMemory reads one memory folder into notes.
func (s *Service) readClaudeMemory(dir string) ([]pending, []ImportSkip) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []ImportSkip{{Name: filepath.Base(filepath.Dir(dir)), Reason: SkipUnreadable}}
	}
	var notes []pending
	var skipped []ImportSkip
	now := s.now().UTC()
	for _, e := range entries {
		name := e.Name()
		stem, ok := strings.CutSuffix(name, ".md")
		if !ok || name == "MEMORY.md" {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case err != nil:
			skipped = append(skipped, ImportSkip{name, SkipUnreadable})
			continue
		case info.Mode()&fs.ModeSymlink != 0:
			skipped = append(skipped, ImportSkip{name, SkipLink})
			continue
		case !info.Mode().IsRegular():
			skipped = append(skipped, ImportSkip{name, SkipNotAFile})
			continue
		case info.Size() > domain.LadoKnowledgeNoteMaxBytes:
			skipped = append(skipped, ImportSkip{name, SkipTooLarge})
			continue
		}
		slug := slugify(stem)
		if !domain.ValidLadoKnowledgeSlug(slug) {
			skipped = append(skipped, ImportSkip{name, SkipInvalidName})
			continue
		}
		data, err := readRegular(filepath.Join(dir, name), domain.LadoKnowledgeNoteMaxBytes)
		if err != nil {
			skipped = append(skipped, ImportSkip{name, SkipUnreadable})
			continue
		}
		note := memoryNote(slug, stem, data)
		note.Revision, note.Updated, note.Author = 1, now, domain.LadoKnowledgeAuthorClaudeMemory
		if reason := checkImported(note); reason != "" {
			skipped = append(skipped, ImportSkip{name, reason})
			continue
		}
		notes = append(notes, pending{name: name, note: note})
	}
	return notes, skipped
}

// memoryNote turns a Claude Code memory file (front matter name,
// description, type or metadata.type, then the fact) into a note.
func memoryNote(slug, stem string, data []byte) domain.LadoKnowledgeNote {
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")
	body := text
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Type        string `yaml:"type"`
		Metadata    struct {
			Type string `yaml:"type"`
		} `yaml:"metadata"`
	}
	if front, rest, ok := domain.SplitLadoFrontMatter(text); ok {
		body = rest
		_ = yamlUnmarshal([]byte(front), &meta)
	}
	title := oneLine(meta.Description)
	if title == "" {
		title = oneLine(meta.Name)
	}
	if title == "" {
		title = stem
	}
	if r := []rune(title); len(r) > domain.LadoKnowledgeTitleMaxRunes {
		title = string(r[:domain.LadoKnowledgeTitleMaxRunes-1]) + "…"
	}
	kind := meta.Type
	if kind == "" {
		kind = meta.Metadata.Type
	}
	return domain.LadoKnowledgeNote{Slug: slug, Title: title, Kind: memoryKind(kind), Body: body}
}

func memoryKind(t string) domain.LadoKnowledgeKind {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "project":
		return domain.LadoKnowledgeDecision
	case "reference":
		return domain.LadoKnowledgeResearch
	case "feedback", "user":
		return domain.LadoKnowledgeConvention
	}
	return domain.LadoKnowledgeResearch
}

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

// slugify makes a file name into a slug: ASCII letters and digits lower
// case, anything else '-', runs of '-' joined and trimmed, at most 64.
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 64 {
		slug = strings.TrimRight(slug[:64], "-")
	}
	return slug
}

// checkImported checks a note an import made: valid, within the size once
// rendered, without a credential. It returns the skip reason or "".
func checkImported(n domain.LadoKnowledgeNote) string {
	if n.Validate() != nil {
		return SkipInvalidNote
	}
	data := domain.RenderLadoKnowledgeNote(n)
	if len(data) > domain.LadoKnowledgeNoteMaxBytes {
		return SkipTooLarge
	}
	if domain.LadoKnowledgeSecret(data) != "" {
		return SkipSecret
	}
	return ""
}

// importNotes writes the notes an import read into the project's folder:
// it makes the project the owner of a folder without one, refuses a
// foreign one, and skips a slug the project has (or the import gave
// already) and notes past the cap. It records knowledge.imported.
func (s *Service) importNotes(ctx context.Context, p domain.ProjectRecord, source string, extra map[string]any, notes []pending, result ImportResult) (ImportResult, error) {
	projectID := domain.ProjectID(p.ID)
	result.Imported = []string{}
	if len(notes) > 0 {
		cur := s.identity(ctx, p)
		f := s.folder(projectID)
		unlock := s.lock(projectID)
		events, err := s.importLocked(f, cur, notes, &result)
		unlock()
		s.audit(ctx, events...)
		if err != nil {
			return ImportResult{}, err
		}
	}
	payload := map[string]any{"source": source, "imported": result.Imported, "skipped": result.Skipped}
	for k, v := range extra {
		payload[k] = v
	}
	s.audit(ctx, s.event(projectID, "api", AuditImported, payload))
	return result, nil
}

func (s *Service) importLocked(f folder, cur Owner, notes []pending, result *ImportResult) ([]domain.LadoAuditEvent, error) {
	if err := f.ensure(); err != nil {
		return nil, apierr.Internal(CodeFailed, "LADO could not create the knowledge folder")
	}
	if err := s.ownLocked(f, cur); err != nil {
		return nil, err
	}
	snap, events, err := s.syncLocked(cur.ProjectID, f)
	if err != nil {
		return events, apierr.Internal(CodeFailed, "LADO could not read the knowledge folder")
	}
	wrote := false
	for _, n := range notes {
		slug := n.note.Slug
		_, recorded := snap.records[slug]
		if _, unreadable := snap.unreadable[slug]; recorded || unreadable {
			result.Skipped = append(result.Skipped, ImportSkip{n.name, SkipExists})
			continue
		}
		if snap.count() >= domain.LadoKnowledgeMaxNotes {
			result.Skipped = append(result.Skipped, ImportSkip{n.name, SkipLimit})
			continue
		}
		data := domain.RenderLadoKnowledgeNote(n.note)
		if err := writeAtomic(f.notePath(slug), data); err != nil {
			s.log.Warn("LADO could not write an imported note", "project", cur.ProjectID, "note", slug, "err", err)
			result.Skipped = append(result.Skipped, ImportSkip{n.name, SkipUnreadable})
			continue
		}
		snap.records[slug] = noteRecord{Revision: 1, SHA256: sha256Hex(data), Author: n.note.Author, Updated: n.note.Updated}
		snap.files[slug] = data
		result.Imported = append(result.Imported, slug)
		wrote = true
	}
	if wrote {
		if err := s.saveLocked(f, snap); err != nil {
			return events, apierr.Internal(CodeFailed, "LADO could not record the imported notes")
		}
	}
	return events, nil
}

// Importable is another knowledge folder a project can import from.
type Importable struct {
	ProjectID domain.ProjectID
	Notes     int
	// OwnerPath is the path its owner.json records, empty without one.
	OwnerPath string
	// Registered says an active project has the id; an archived project's
	// folder, or one whose project is gone, is not.
	Registered bool
}

// Importable lists the knowledge folders other than the project's own that
// hold notes, by id.
func (s *Service) Importable(ctx context.Context, projectID domain.ProjectID) ([]Importable, error) {
	if _, err := s.project(ctx, projectID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return []Importable{}, nil
	}
	if err != nil {
		return nil, apierr.Internal(CodeFailed, "LADO could not list the knowledge folders")
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, apierr.Internal(CodeFailed, "LADO could not list the projects")
	}
	active := map[string]bool{}
	for _, p := range projects {
		if p.ArchivedAt.IsZero() {
			active[p.ID] = true
		}
	}
	out := []Importable{}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || id == string(projectID) || !validProjectID(id) {
			continue
		}
		f := folder(filepath.Join(s.root, id))
		n := countNotes(f)
		if n == 0 {
			continue
		}
		item := Importable{ProjectID: domain.ProjectID(id), Notes: n, Registered: active[id]}
		if owner, err := readOwner(f); err == nil && owner != nil {
			item.OwnerPath = owner.Path
		}
		out = append(out, item)
	}
	return out, nil
}

func countNotes(f folder) int {
	entries, err := os.ReadDir(f.notesDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if slug, ok := strings.CutSuffix(e.Name(), ".md"); ok && domain.ValidLadoKnowledgeSlug(slug) && e.Type().IsRegular() {
			n++
		}
	}
	return n
}

// ImportProject copies the notes of another knowledge folder, the one of
// project id fromID, which need not be registered any more. The notes keep
// their sources, blobs and commit, start at revision 1 and have author
// import:project:<fromID>. A slug the project has is skipped, and so is a
// file that does not parse as a note. The source folder is only read.
func (s *Service) ImportProject(ctx context.Context, projectID, fromID domain.ProjectID) (ImportResult, error) {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return ImportResult{}, err
	}
	if !validProjectID(string(fromID)) {
		return ImportResult{}, apierr.Invalid(CodeInvalidImport, "fromProjectId is not a project id.", map[string]any{"field": "fromProjectId"})
	}
	if fromID == projectID {
		return ImportResult{}, apierr.Invalid(CodeInvalidImport, "A project cannot import its own knowledge.", map[string]any{"field": "fromProjectId"})
	}
	src := filepath.Join(s.root, string(fromID))
	if rel, err := filepath.Rel(s.root, src); err != nil || rel != string(fromID) {
		return ImportResult{}, apierr.Invalid(CodeInvalidImport, "fromProjectId is not a project id.", map[string]any{"field": "fromProjectId"})
	}
	if info, err := os.Lstat(src); err != nil || !info.IsDir() {
		return ImportResult{}, apierr.NotFound(CodeImportNotFound, "No knowledge folder has that project id.")
	}

	var result ImportResult
	var notes []pending
	author := domain.LadoKnowledgeAuthorProjectPrefix + string(fromID)
	now := s.now().UTC()
	from := folder(src)
	unlock := s.lock(fromID)
	entries, err := os.ReadDir(from.notesDir())
	files := map[string][]byte{}
	for _, e := range entries {
		slug, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !domain.ValidLadoKnowledgeSlug(slug) {
			continue
		}
		if !e.Type().IsRegular() {
			result.Skipped = append(result.Skipped, ImportSkip{e.Name(), SkipLink})
			continue
		}
		data, rerr := readRegular(from.notePath(slug), domain.LadoKnowledgeNoteMaxBytes)
		if rerr != nil {
			result.Skipped = append(result.Skipped, ImportSkip{e.Name(), SkipTooLarge})
			continue
		}
		files[slug] = data
	}
	unlock()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ImportResult{}, apierr.Internal(CodeFailed, "LADO could not read that knowledge folder")
	}
	slugs := make([]string, 0, len(files))
	for slug := range files {
		slugs = append(slugs, slug)
	}
	slices.Sort(slugs)
	for _, slug := range slugs {
		note, err := domain.ParseLadoKnowledgeNote(slug, files[slug])
		if err != nil {
			result.Skipped = append(result.Skipped, ImportSkip{slug + ".md", SkipInvalidNote})
			continue
		}
		note.Revision, note.Updated, note.Author = 1, now, author
		if reason := checkImported(note); reason != "" {
			result.Skipped = append(result.Skipped, ImportSkip{slug + ".md", reason})
			continue
		}
		notes = append(notes, pending{name: slug + ".md", note: note})
	}
	return s.importNotes(ctx, p, SourceProject, map[string]any{"fromProjectId": string(fromID)}, notes, result)
}
