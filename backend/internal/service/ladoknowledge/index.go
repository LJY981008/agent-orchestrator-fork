package ladoknowledge

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// freshnessTimeout bounds the git calls one freshness check makes, so a
// slow repository never holds up a session's start.
const freshnessTimeout = 5 * time.Second

// Index is a project's knowledge as the app and a session's standing
// instructions read it.
type Index struct {
	ProjectID domain.ProjectID
	// Dir is the project's knowledge folder; it may not exist yet.
	Dir string
	// Foreign is set when the folder belongs to another repository; Owner
	// then says which, and Notes and Prompt are empty.
	Foreign bool
	Owner   *Owner
	// Notes are sorted by slug.
	Notes []domain.LadoKnowledgeEntry
	// Prompt is the index a session's standing instructions carry
	// (domain.RenderLadoKnowledgePromptIndex), empty without notes;
	// Truncated says it left notes out.
	Prompt    string
	Truncated bool
}

// Index lists the project's notes with their states and renders the prompt
// index. Edits made in the folder are taken first. A note is unchecked when
// it has no source with a blob (git is not called for it), fresh when every
// source's blob is the base branch's and stale otherwise; see states.
func (s *Service) Index(ctx context.Context, projectID domain.ProjectID) (Index, error) {
	p, err := s.project(ctx, projectID)
	if err != nil {
		return Index{}, err
	}
	f := s.folder(projectID)
	out := Index{ProjectID: projectID, Dir: string(f)}
	owner, err := readOwner(f)
	if err != nil {
		return Index{}, apierr.Internal(CodeFailed, "LADO could not read the knowledge folder's owner")
	}
	out.Owner = owner
	if owner != nil && foreign(owner, s.identity(ctx, p)) {
		out.Foreign = true
		return out, nil
	}

	unlock := s.lock(projectID)
	snap, events, err := s.syncLocked(projectID, f)
	unlock()
	s.audit(ctx, events...)
	if err != nil {
		return Index{}, apierr.Internal(CodeFailed, "LADO could not read the knowledge folder")
	}

	sources := map[string][]domain.LadoKnowledgeSource{}
	for slug, data := range snap.files {
		n := readNote(slug, data)
		out.Notes = append(out.Notes, entryOf(n, snap.records[slug]))
		sources[slug] = n.Sources
	}
	slices.SortFunc(out.Notes, func(a, b domain.LadoKnowledgeEntry) int { return strings.Compare(a.Slug, b.Slug) })
	if states, ok := s.states(ctx, p, sources); ok {
		for i := range out.Notes {
			out.Notes[i].State = states[out.Notes[i].Slug]
		}
	}
	out.Prompt, out.Truncated = domain.RenderLadoKnowledgePromptIndex(out.Notes)
	return out, nil
}

// checkable reports whether a note has something to compare: sources, each
// with a blob.
func checkable(sources []domain.LadoKnowledgeSource) bool {
	if len(sources) == 0 {
		return false
	}
	for _, src := range sources {
		if src.Blob == "" {
			return false
		}
	}
	return true
}

// freshCache is the last base tree lookup of a project: the blobs of paths
// at tip.
type freshCache struct {
	tip   string
	paths string
	blobs map[string]string
}

// states compares each note's sources with the project's base ref, which
// is origin/<default branch> (origin/HEAD when the project resolves it
// automatically) or, without it, HEAD. One git ls-tree reads every source;
// the result is kept per project and base tip. ok is false when nothing was
// compared: then every note stays unchecked, as it does in a workspace
// project, on a git failure and past freshnessTimeout.
func (s *Service) states(ctx context.Context, p domain.ProjectRecord, notes map[string][]domain.LadoKnowledgeSource) (map[string]domain.LadoKnowledgeState, bool) {
	var paths []string
	for _, sources := range notes {
		if checkable(sources) {
			for _, src := range sources {
				paths = append(paths, src.Path)
			}
		}
	}
	if len(paths) == 0 || p.Kind.WithDefault() == domain.ProjectKindWorkspace || p.Path == "" {
		return nil, false
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	ctx, cancel := context.WithTimeout(ctx, freshnessTimeout)
	defer cancel()
	blobs, err := s.baseBlobs(ctx, p, paths)
	if err != nil {
		s.log.Debug("LADO could not compare knowledge with the base branch", "project", p.ID, "err", err)
		return nil, false
	}
	out := make(map[string]domain.LadoKnowledgeState, len(notes))
	for slug, sources := range notes {
		state := domain.LadoKnowledgeUnchecked
		if checkable(sources) {
			state = domain.LadoKnowledgeFresh
			for _, src := range sources {
				if blobs[src.Path] != src.Blob {
					state = domain.LadoKnowledgeStale
					break
				}
			}
		}
		out[slug] = state
	}
	return out, true
}

// baseBlobs returns the blob each path has at the project's base ref; a
// path the tree does not hold is missing from the map.
func (s *Service) baseBlobs(ctx context.Context, p domain.ProjectRecord, paths []string) (map[string]string, error) {
	ref := "origin/HEAD"
	if branch := p.Config.WorktreeBaseBranch(); branch != "" {
		ref = "origin/" + branch
	}
	var tip string
	var err error
	for _, candidate := range []string{ref, "HEAD"} {
		var out []byte
		if out, err = s.git.Run(ctx, p.Path, "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}"); err == nil {
			tip = strings.TrimSpace(string(out))
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if tip == "" {
		return nil, err
	}
	key := strings.Join(paths, "\x00")
	projectID := domain.ProjectID(p.ID)
	s.cacheMu.Lock()
	c, ok := s.cache[projectID]
	s.cacheMu.Unlock()
	if ok && c.tip == tip && c.paths == key {
		return c.blobs, nil
	}
	args := append([]string{"ls-tree", "-r", "-z", "--full-tree", tip, "--"}, paths...)
	out, err := s.git.Run(ctx, p.Path, args...)
	if err != nil {
		return nil, err
	}
	blobs := map[string]string{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		// <mode> SP <type> SP <object> TAB <path>
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			continue
		}
		fields := strings.Fields(string(meta))
		if len(fields) == 3 && fields[1] == "blob" {
			blobs[string(path)] = fields[2]
		}
	}
	s.cacheMu.Lock()
	s.cache[projectID] = freshCache{tip: tip, paths: key, blobs: blobs}
	s.cacheMu.Unlock()
	return blobs, nil
}
