package ladorelay

// Whether a relayed instruction may leave the approved plan (plan decision
// 6). LADO marks it, and never refuses it: the orchestrator's own mark, and
// every path the instruction names that no affected path of the worker's
// plan covers (a part or merge worker's part's paths). A new step is not
// something a machine can tell, so that is the orchestrator's mark alone.
// The paths a worker then changes are checked by verification as before.

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// maxPathReasons caps the outside-plan paths a relay records.
const maxPathReasons = 20

// outsidePlan lists why the instruction may leave the plan of worker. A plan
// LADO cannot read (no pinned blob, a plan that does not parse, no affected
// paths) does not stop the relay: outside plan is a mark, so the failure is
// logged and recorded as the reason that the paths went unchecked.
func (s *Service) outsidePlan(ctx context.Context, worker domain.LadoExecution, instruction string, flagged bool) []string {
	var reasons []string
	if flagged {
		reasons = append(reasons, domain.LadoRelayFlaggedReason)
	}
	paths := mentionedPaths(instruction)
	if len(paths) == 0 || s.deps.Plans == nil {
		return reasons
	}
	plan, err := s.deps.Plans.ApprovedPlan(ctx, worker.PlanRevisionID)
	if err != nil {
		s.deps.Log.Warn("LADO could not read the approved plan to check a relay's paths",
			"worker", worker.SessionID, "revision", worker.PlanRevisionID, "err", err)
		return append(reasons, domain.LadoRelayPlanUnreadReason)
	}
	patterns := planPaths(plan, worker)
	for _, p := range paths {
		if len(reasons) >= maxPathReasons {
			break
		}
		if !covered(patterns, p) {
			reasons = append(reasons, domain.LadoRelayPathReason(p))
		}
	}
	return reasons
}

// planPaths are the affected paths that bind worker: its part's for a part
// or merge worker of a part, the plan's otherwise.
func planPaths(plan domain.LadoPlan, worker domain.LadoExecution) []string {
	if worker.PartID != "" && (worker.Role == domain.LadoRolePart || worker.Role == domain.LadoRoleMerge) {
		for _, part := range plan.Parts {
			if part.ID == worker.PartID && len(part.AffectedPaths) > 0 {
				return part.AffectedPaths
			}
		}
	}
	return plan.AffectedPaths
}

// covered reports whether some pattern covers p. A path with a directory is
// matched as a whole, or as the tail of a path the pattern matches when its
// first directory is a segment of the pattern ("ladotask/plan_text.go" under
// "backend/internal/service/ladotask/**"). A bare file name is covered by a
// pattern whose last segment matches it or is "**".
func covered(patterns []string, p string) bool {
	segments := strings.Split(p, "/")
	for _, pattern := range patterns {
		pattern = strings.Trim(strings.TrimPrefix(pattern, "./"), "/")
		if pattern == "" {
			continue
		}
		parts := strings.Split(pattern, "/")
		if len(segments) == 1 {
			last := parts[len(parts)-1]
			if ok, _ := path.Match(last, p); ok || last == "**" {
				return true
			}
			continue
		}
		if ladoverify.MatchPath(pattern, p) {
			return true
		}
		for k := 1; k < len(parts); k++ {
			if parts[k] == "**" {
				continue
			}
			if ok, _ := path.Match(parts[k], segments[0]); ok && ladoverify.MatchPath(strings.Join(parts[k:], "/"), p) {
				return true
			}
		}
	}
	return false
}

var (
	// urlPattern finds URLs, which name no file of the repository.
	urlPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+|\bwww\.\S+`)
	// pathRun finds runs of the characters a path is written with.
	pathRun = regexp.MustCompile(`[A-Za-z0-9_.\-/\\*@+~]+`)
	// extension is a file extension: letters and digits, at least one letter.
	extension = regexp.MustCompile(`^[A-Za-z0-9]{1,10}$`)
)

// mentionedPaths lists the paths an instruction names, in order and once
// each: a word with a file extension ("plan_text.go"), or with slashes that
// ends in one or in a slash, or has two or more ("backend/internal/"). A URL
// is no path, nor is a word like "origin/main", "e.g" or "v1.2".
func mentionedPaths(text string) []string {
	text = urlPattern.ReplaceAllString(text, " ")
	var paths []string
	for _, run := range pathRun.FindAllString(text, -1) {
		p := strings.ReplaceAll(run, `\`, "/")
		p = strings.TrimRight(p, ".-")
		p = strings.TrimLeft(p, "-")
		for strings.HasPrefix(p, "./") {
			p = p[2:]
		}
		if !looksLikePath(p) {
			continue
		}
		p = strings.Trim(p, "/")
		if p != "" && !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths
}

func looksLikePath(p string) bool {
	if p == "" || strings.Contains(p, "//") {
		return false
	}
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return false
	}
	name := trimmed[strings.LastIndexByte(trimmed, '/')+1:]
	slashes := strings.Count(trimmed, "/")
	if slashes == 0 {
		// A bare name needs a file extension after a name of two or more
		// characters, so "e.g" and "1.5" are not paths.
		return hasExtension(name, 2)
	}
	return strings.HasSuffix(p, "/") || slashes >= 2 || hasExtension(name, 1)
}

// hasExtension reports whether name is a base of at least minBase
// characters, a dot and an extension with a letter.
func hasExtension(name string, minBase int) bool {
	dot := strings.LastIndexByte(name, '.')
	if dot < minBase || dot == len(name)-1 {
		return false
	}
	ext := name[dot+1:]
	return extension.MatchString(ext) && strings.IndexFunc(ext, isLetter) >= 0
}

func isLetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
