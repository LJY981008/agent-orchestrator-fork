// Package ladoknowledge owns LADO's project knowledge store (issue #24,
// docs/LJY-PRD/adr/0010-project-knowledge-store.md): one folder of Markdown
// notes per project under <state>/lado/knowledge/<project-id>/.
//
//	notes/<slug>.md     the notes, plain Markdown an editor such as Obsidian opens
//	index.md            the list LADO renders after every change
//	.lado/notes.json    slug → {revision, sha256, author, updated}; the revision
//	                    a note has, whatever its front matter shows
//	.lado/owner.json    the project that first wrote: its path and root commit
//
// Agents write through the daemon only (Write), which serializes each
// project's changes, checks the base revision and renders the front matter.
// A file changed in the folder by hand is taken as a new revision, so a
// writer that started from the old one gets LADO_KNOWLEDGE_CHANGED and
// merges instead of erasing the edit. Nothing here ever writes outside the
// knowledge folder; imports only read their sources.
package ladoknowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Audit log actions.
const (
	AuditExternalEdit = "knowledge.external_edit"
	AuditClaimed      = "knowledge.claimed"
	AuditNoteWritten  = "knowledge.note_written"
	AuditImported     = "knowledge.imported"
)

// Error codes the service returns as apierr errors.
const (
	CodeInvalidNote     = "LADO_KNOWLEDGE_INVALID_NOTE"      // 400
	CodeSecret          = "LADO_KNOWLEDGE_SECRET"            // 400
	CodeInvalidImport   = "LADO_KNOWLEDGE_INVALID_IMPORT"    // 400: bad source, fromProjectId or path
	CodeProjectNotFound = "LADO_KNOWLEDGE_PROJECT_NOT_FOUND" // 404
	CodeSessionNotFound = "LADO_KNOWLEDGE_SESSION_NOT_FOUND" // 404
	CodeImportNotFound  = "LADO_KNOWLEDGE_IMPORT_NOT_FOUND"  // 404: no such knowledge folder
	CodeExists          = "LADO_KNOWLEDGE_EXISTS"            // 409
	CodeChanged         = "LADO_KNOWLEDGE_CHANGED"           // 409, details.currentRevision
	CodeSessionEnded    = "LADO_KNOWLEDGE_SESSION_ENDED"     // 409
	CodeNotLadoProject  = "LADO_KNOWLEDGE_NOT_LADO_PROJECT"  // 409
	CodeReadOnly        = "LADO_KNOWLEDGE_READ_ONLY"         // 409: plan critic sessions
	CodeForeign         = "LADO_KNOWLEDGE_FOREIGN"           // 409, details.ownerPath
	CodeFailed          = "LADO_KNOWLEDGE_FAILED"            // 500
)

// maxReadBytes caps a note file LADO reads from the folder. A note LADO
// writes is at most domain.LadoKnowledgeNoteMaxBytes; a larger file someone
// put there is left out of the index.
const maxReadBytes = 4 * domain.LadoKnowledgeNoteMaxBytes

// Store is what the service reads from AO's and LADO's records.
type Store interface {
	// GetProject returns a project, active or archived.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	// ListProjects returns the active projects.
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error)
	AppendLadoAuditEvent(ctx context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error)
}

// Options builds a Service.
type Options struct {
	// StateDir is AO's state directory; knowledge lives in
	// <StateDir>/lado/knowledge.
	StateDir string
	Store    Store
	// ClaudeDir is the computer's Claude Code folder (~/.claude), whose
	// projects/<encoded path>/memory ImportClaudeMemory reads. Empty turns
	// that import into a claude_memory_not_found report.
	ClaudeDir string
	// Git runs git; nil uses the system git.
	Git Git
	Log *slog.Logger
	Now func() time.Time
}

// Service reads and writes project knowledge.
type Service struct {
	root      string
	store     Store
	claudeDir string
	git       Git
	log       *slog.Logger
	now       func() time.Time

	mu    sync.Mutex
	locks map[domain.ProjectID]*sync.Mutex

	cacheMu sync.Mutex
	cache   map[domain.ProjectID]freshCache
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Git == nil {
		opts.Git = ExecGit{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{
		root:      filepath.Join(opts.StateDir, "lado", "knowledge"),
		store:     opts.Store,
		claudeDir: opts.ClaudeDir,
		git:       opts.Git,
		log:       opts.Log,
		now:       opts.Now,
		locks:     map[domain.ProjectID]*sync.Mutex{},
		cache:     map[domain.ProjectID]freshCache{},
	}
}

// Dir is the knowledge folder of a project. It does not check the id.
func (s *Service) Dir(projectID domain.ProjectID) string {
	return filepath.Join(s.root, string(projectID))
}

// lock takes the project's mutex, which every read and write of its folder
// holds. Git never runs under it.
func (s *Service) lock(projectID domain.ProjectID) func() {
	s.mu.Lock()
	m, ok := s.locks[projectID]
	if !ok {
		m = &sync.Mutex{}
		s.locks[projectID] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// folder is one project's knowledge folder.
type folder string

func (f folder) notesDir() string                    { return filepath.Join(string(f), "notes") }
func (f folder) notePath(slug string) string         { return filepath.Join(f.notesDir(), slug+".md") }
func (f folder) indexPath() string                   { return filepath.Join(string(f), "index.md") }
func (f folder) metaDir() string                     { return filepath.Join(string(f), ".lado") }
func (f folder) recordsPath() string                 { return filepath.Join(f.metaDir(), "notes.json") }
func (f folder) ownerPath() string                   { return filepath.Join(f.metaDir(), "owner.json") }
func (s *Service) folder(id domain.ProjectID) folder { return folder(s.Dir(id)) }

// ensure creates the folder's directories, private to the user.
func (f folder) ensure() error {
	for _, dir := range []string{string(f), f.notesDir(), f.metaDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// noteRecord is what LADO keeps about one note: the revision it has, the
// sha256 of the file LADO last wrote or took, who made that revision and
// when.
type noteRecord struct {
	Revision int       `json:"revision"`
	SHA256   string    `json:"sha256"`
	Author   string    `json:"author"`
	Updated  time.Time `json:"updated"`
}

// Owner is the project a knowledge folder belongs to.
type Owner struct {
	ProjectID  domain.ProjectID `json:"projectId"`
	Path       string           `json:"path"`
	RootCommit string           `json:"rootCommit,omitempty"`
	ClaimedAt  time.Time        `json:"claimedAt"`
}

// readJSON reads a file LADO wrote. A missing file is ok=false.
func readJSON(path string, v any) (bool, error) {
	data, err := readRegular(path, 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return true, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// readRegular reads a regular file of at most limit bytes, never through a
// link.
func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// writeAtomic replaces path through a temporary file in the same folder and
// a rename, so a crash leaves the old or the new content, mode 0600.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// snapshot is a folder as one locked read saw it, after external edits
// were taken.
type snapshot struct {
	records map[string]noteRecord
	files   map[string][]byte
}

// syncLocked reads the folder's notes and takes the ones changed outside
// LADO: a file whose sha256 is not the recorded one becomes the next
// revision, a file without a record revision 1, both by
// domain.LadoKnowledgeAuthorExternal; a record whose file is gone is
// dropped. It saves the records and the index when anything changed and
// returns the audit events to append once the lock is released. Links,
// folders and files that are not <slug>.md are not notes. The caller holds
// the project's lock.
func (s *Service) syncLocked(projectID domain.ProjectID, f folder) (snapshot, []domain.LadoAuditEvent, error) {
	snap := snapshot{records: map[string]noteRecord{}, files: map[string][]byte{}}
	if _, err := readJSON(f.recordsPath(), &snap.records); err != nil {
		// A damaged record file is not worth blocking the notes over: every
		// note is taken again, as from outside.
		s.log.Warn("LADO could not read the knowledge records; it takes every note again", "project", projectID, "err", err)
		snap.records = map[string]noteRecord{}
	}
	entries, err := os.ReadDir(f.notesDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return snapshot{}, nil, err
	}
	now := s.now().UTC()
	var events []domain.LadoAuditEvent
	changed := false
	for _, e := range entries {
		slug, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !domain.ValidLadoKnowledgeSlug(slug) || !e.Type().IsRegular() {
			continue
		}
		data, err := readRegular(f.notePath(slug), maxReadBytes)
		if err != nil {
			s.log.Warn("LADO leaves a knowledge note out", "project", projectID, "note", slug, "err", err)
			continue
		}
		snap.files[slug] = data
		sum := sha256Hex(data)
		rec, known := snap.records[slug]
		if known && rec.SHA256 == sum {
			continue
		}
		from := rec.Revision
		rec = noteRecord{Revision: rec.Revision + 1, SHA256: sum, Author: domain.LadoKnowledgeAuthorExternal, Updated: now}
		if info, err := e.Info(); err == nil {
			rec.Updated = info.ModTime().UTC()
		}
		snap.records[slug] = rec
		changed = true
		events = append(events, s.event(projectID, domain.LadoKnowledgeAuthorExternal, AuditExternalEdit,
			map[string]any{"slug": slug, "fromRevision": from, "revision": rec.Revision, "sha256": sum}))
	}
	for slug, rec := range snap.records {
		if _, ok := snap.files[slug]; ok {
			continue
		}
		delete(snap.records, slug)
		changed = true
		events = append(events, s.event(projectID, domain.LadoKnowledgeAuthorExternal, AuditExternalEdit,
			map[string]any{"slug": slug, "fromRevision": rec.Revision, "deleted": true}))
	}
	if changed {
		if err := f.ensure(); err != nil {
			return snapshot{}, nil, err
		}
		if err := s.saveLocked(f, snap); err != nil {
			return snapshot{}, nil, err
		}
	}
	return snap, events, nil
}

// saveLocked writes the records and renders index.md.
func (s *Service) saveLocked(f folder, snap snapshot) error {
	if err := writeJSON(f.recordsPath(), snap.records); err != nil {
		return fmt.Errorf("save knowledge records: %w", err)
	}
	if err := writeAtomic(f.indexPath(), domain.RenderLadoKnowledgeDiskIndex(snap.entries())); err != nil {
		return fmt.Errorf("render knowledge index: %w", err)
	}
	return nil
}

// entries lists the notes as the index shows them, without a state.
func (snap snapshot) entries() []domain.LadoKnowledgeEntry {
	out := make([]domain.LadoKnowledgeEntry, 0, len(snap.files))
	for slug, data := range snap.files {
		out = append(out, entryOf(readNote(slug, data), snap.records[slug]))
	}
	return out
}

func entryOf(n domain.LadoKnowledgeNote, rec noteRecord) domain.LadoKnowledgeEntry {
	return domain.LadoKnowledgeEntry{
		Slug: n.Slug, Title: n.Title, Kind: n.Kind, Revision: rec.Revision,
		UpdatedAt: rec.Updated, Author: rec.Author, State: domain.LadoKnowledgeUnchecked,
	}
}

// readNote reads a note file leniently: one edited outside LADO may not
// parse, and then keeps the title and kind it has, or its slug as title,
// and no sources.
func readNote(slug string, data []byte) domain.LadoKnowledgeNote {
	if n, err := domain.ParseLadoKnowledgeNote(slug, data); err == nil {
		return n
	}
	n := domain.LadoKnowledgeNote{Slug: slug, Title: slug}
	if front, _, ok := domain.SplitLadoFrontMatter(string(data)); ok {
		var loose struct {
			Title any `yaml:"title"`
			Kind  any `yaml:"kind"`
		}
		if yamlUnmarshal([]byte(front), &loose) == nil {
			if t, ok := loose.Title.(string); ok && strings.TrimSpace(t) != "" {
				n.Title = strings.TrimSpace(t)
			}
			if k, ok := loose.Kind.(string); ok {
				n.Kind = domain.LadoKnowledgeKind(strings.TrimSpace(k))
			}
		}
	}
	return n
}

// readOwner reads owner.json; nil when the folder has none.
func readOwner(f folder) (*Owner, error) {
	var o Owner
	ok, err := readJSON(f.ownerPath(), &o)
	if err != nil || !ok {
		return nil, err
	}
	return &o, nil
}

// identity is the project as owner.json records it. The root commit is the
// first of git rev-list --max-parents=0 HEAD in the project's folder, none
// for a workspace project or when git cannot tell.
func (s *Service) identity(ctx context.Context, p domain.ProjectRecord) Owner {
	o := Owner{ProjectID: domain.ProjectID(p.ID), Path: filepath.Clean(p.Path)}
	if p.Kind.WithDefault() == domain.ProjectKindWorkspace || p.Path == "" {
		return o
	}
	out, err := s.git.Run(ctx, p.Path, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		s.log.Debug("LADO could not read the project's root commit", "project", p.ID, "err", err)
		return o
	}
	if first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); first != "" {
		o.RootCommit = strings.TrimSpace(first)
	}
	return o
}

// foreign reports whether the folder's owner is another repository than
// the project: by root commit when both have one, else (a workspace
// project, a folder without git) by path.
func foreign(owner *Owner, cur Owner) bool {
	if owner == nil {
		return false
	}
	if owner.RootCommit != "" && cur.RootCommit != "" {
		return owner.RootCommit != cur.RootCommit
	}
	return !samePath(owner.Path, cur.Path)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// ownLocked checks the folder belongs to the project and records the
// project as its owner when it has none. The caller holds the lock and
// created the folder.
func (s *Service) ownLocked(f folder, cur Owner) error {
	owner, err := readOwner(f)
	if err != nil {
		return apierr.Internal(CodeFailed, "LADO could not read the knowledge folder's owner")
	}
	if foreign(owner, cur) {
		return foreignError(owner)
	}
	if owner == nil {
		cur.ClaimedAt = s.now().UTC()
		if err := writeJSON(f.ownerPath(), cur); err != nil {
			return apierr.Internal(CodeFailed, "LADO could not record the knowledge folder's owner")
		}
	}
	return nil
}

func foreignError(owner *Owner) error {
	return apierr.Conflict(CodeForeign,
		"This knowledge folder belongs to another repository. Confirm in the app that this project continues it.",
		map[string]any{"ownerPath": owner.Path, "ownerProjectId": string(owner.ProjectID)})
}

// event builds an audit event about a project's knowledge.
func (s *Service) event(projectID domain.ProjectID, actor, action string, payload map[string]any) domain.LadoAuditEvent {
	data, _ := json.Marshal(payload)
	return domain.LadoAuditEvent{
		At: s.now().UTC(), Actor: actor, Action: action,
		SubjectKind: "project", SubjectID: string(projectID), Payload: data,
	}
}

// audit appends the events. A failure is logged: the change is on disk.
func (s *Service) audit(ctx context.Context, events ...domain.LadoAuditEvent) {
	for _, ev := range events {
		if _, err := s.store.AppendLadoAuditEvent(context.WithoutCancel(ctx), ev); err != nil {
			s.log.Warn("LADO could not record a knowledge audit event", "action", ev.Action, "project", ev.SubjectID, "err", err)
		}
	}
}

// project reads a project, archived ones included.
func (s *Service) project(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, error) {
	if !validProjectID(string(id)) {
		return domain.ProjectRecord{}, apierr.NotFound(CodeProjectNotFound, "Project not found")
	}
	p, ok, err := s.store.GetProject(ctx, string(id))
	if err != nil {
		return domain.ProjectRecord{}, apierr.Internal(CodeFailed, "LADO could not read the project")
	}
	if !ok {
		return domain.ProjectRecord{}, apierr.NotFound(CodeProjectNotFound, "Project not found")
	}
	return p, nil
}

// validProjectID is AO's project id form (service/project validateProjectID),
// so a project id is always one folder name under the knowledge root.
func validProjectID(id string) bool {
	if id == "" || id == "." || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return false
	}
	for i, r := range id {
		alnum := r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		if !alnum && (i == 0 || (r != '.' && r != '_' && r != '-')) {
			return false
		}
	}
	return true
}

// Claim makes the project the owner of its knowledge folder, as the owner
// confirms in the app when the folder came from another repository.
func (s *Service) Claim(ctx context.Context, projectID domain.ProjectID) error {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return err
	}
	cur := s.identity(ctx, p)
	f := s.folder(projectID)
	unlock := s.lock(projectID)
	if err := f.ensure(); err != nil {
		unlock()
		return apierr.Internal(CodeFailed, "LADO could not create the knowledge folder")
	}
	prev, err := readOwner(f)
	if err != nil {
		s.log.Warn("LADO replaces an unreadable knowledge owner", "project", projectID, "err", err)
	}
	cur.ClaimedAt = s.now().UTC()
	if err := writeJSON(f.ownerPath(), cur); err != nil {
		unlock()
		return apierr.Internal(CodeFailed, "LADO could not record the knowledge folder's owner")
	}
	unlock()
	payload := map[string]any{"path": cur.Path, "rootCommit": cur.RootCommit}
	if prev != nil {
		payload["fromPath"], payload["fromRootCommit"], payload["fromProjectId"] = prev.Path, prev.RootCommit, string(prev.ProjectID)
	}
	s.audit(ctx, s.event(projectID, "api", AuditClaimed, payload))
	return nil
}
