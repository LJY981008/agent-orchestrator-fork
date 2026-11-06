package ladotask

import (
	"context"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// partFilesCacheSize bounds the revisions whose part file counts LADO keeps.
// A revision and its base commit never change, so a count stays right; the
// cache is emptied when it would grow past this.
const partFilesCacheSize = 64

// partFilesCache keeps, per revision, the number of files under each part's
// paths at the revision's base commit (#69). A revision LADO could not count
// is kept with a nil map, so that a slow or broken repository costs one git
// call per revision, not one per view.
type partFilesCache struct {
	mu     sync.Mutex
	counts map[domain.LadoPlanRevisionID]map[string]int
}

func (c *partFilesCache) get(id domain.LadoPlanRevisionID) (map[string]int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts, ok := c.counts[id]
	return counts, ok
}

func (c *partFilesCache) put(id domain.LadoPlanRevisionID, counts map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil || len(c.counts) >= partFilesCacheSize {
		c.counts = map[domain.LadoPlanRevisionID]map[string]int{}
	}
	c.counts[id] = counts
}

// partFiles returns the number of files under each part's paths at the
// revision's base commit, nil when the plan has no parts, the revision has no
// base commit or LADO could not list the commit's files. A failure is logged
// at debug level only: it drops the file warnings, not the view.
func (s *Service) partFiles(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision, plan domain.LadoPlan) map[string]int {
	if len(plan.Parts) == 0 || rev.BaseSHA == "" {
		return nil
	}
	if counts, ok := s.partFilesCache.get(rev.ID); ok {
		return counts
	}
	counts, err := s.countRevisionFiles(ctx, task, rev, plan)
	if err != nil {
		s.deps.Log.Debug("LADO could not count the files of a plan's parts", "revision", rev.ID, "err", err)
		if ctx.Err() != nil {
			// The viewer went away; the next view tries again.
			return nil
		}
		counts = nil
	}
	s.partFilesCache.put(rev.ID, counts)
	return counts
}

func (s *Service) countRevisionFiles(ctx context.Context, task domain.LadoTask, rev domain.LadoPlanRevision, plan domain.LadoPlan) (map[string]int, error) {
	layout, err := s.repoLayout(ctx, task)
	if err != nil {
		return nil, err
	}
	files, err := s.deps.Git.ListFiles(ctx, layout.dir, rev.BaseSHA)
	if err != nil {
		return nil, err
	}
	return countPartFiles(plan, files, layout.relative), nil
}

// countPartFiles counts, for each part, the files that match at least one of
// its affected_paths, each file once. files are relative to the repository's
// root; in a workspace, relative is the repository's folder in the project,
// which the plan's paths start with. Every part gets an entry.
func countPartFiles(plan domain.LadoPlan, files []string, relative string) map[string]int {
	prefix := strings.Trim(relative, "/")
	if prefix != "" {
		prefix += "/"
	}
	counts := make(map[string]int, len(plan.Parts))
	for _, part := range plan.Parts {
		globs := make([]partGlob, 0, len(part.AffectedPaths))
		for _, pattern := range part.AffectedPaths {
			globs = append(globs, partGlob{pattern: pattern, fixed: globFixedStart(pattern)})
		}
		n := 0
		for _, file := range files {
			p := prefix + file
			for _, g := range globs {
				if strings.HasPrefix(p, g.fixed) && ladoverify.MatchPath(g.pattern, p) {
					n++
					break
				}
			}
		}
		counts[part.ID] = n
	}
	return counts
}

// partGlob is one of a part's paths with the start every path it matches
// has, which skips most files without matching them.
type partGlob struct {
	pattern string
	fixed   string
}

// globFixedStart returns the part of pattern before its first wildcard or
// escape, without empty and "." segments, as ladoverify.MatchPath reads it. A
// path the pattern matches starts with it.
func globFixedStart(pattern string) string {
	var segments []string
	for segment := range strings.SplitSeq(pattern, "/") {
		if segment != "" && segment != "." {
			segments = append(segments, segment)
		}
	}
	clean := strings.Join(segments, "/")
	if i := strings.IndexAny(clean, `*?[\`); i >= 0 {
		return clean[:i]
	}
	return clean
}
