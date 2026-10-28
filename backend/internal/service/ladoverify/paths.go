package ladoverify

// The paths item (decision g of the step 8 plan; owner decision 2): a change
// to a protected path blocks the integration unless the approved plan names
// that path, and a change outside the plan's affected_paths is listed for
// the developer, who confirms it when approving the integration. Protected
// paths compare without case (Q1 of #15): on Windows and, by default, macOS
// the tools read .Claude/settings.json or Agents.md as the protected file.
// Paths outside the plan compare as git gives them.

import (
	"path"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// outsidePlanMax caps how many changed paths outside the plan the details
// list.
const outsidePlanMax = 50

// PathsInput is what the paths item reads.
type PathsInput struct {
	// Changed are the changed paths, both sides of a rename or a copy,
	// relative to the project root: in a workspace project, PrefixPaths puts
	// the repository's folder before the paths git gives.
	Changed []string
	// Affected are the approved plan's affected_paths, relative to the
	// project root.
	Affected []string
	// Protected are the globs of the snapshot's protected_paths validator,
	// and ProtectedOn whether the snapshot has that validator: without it,
	// as in a lado-policy/2 snapshot, only the plan is checked.
	Protected   []string
	ProtectedOn bool
	// Repository is the repository's folder in a workspace project, the
	// prefix PrefixPaths put before the changed paths; empty in a project of
	// one repository. A protected glob matches a changed path from the
	// project root and from the root of its repository as well, since the
	// defaults (".github/**", ".mcp.json", ...) name places in a repository.
	Repository string
}

// PrefixPaths puts the slash-separated folder prefix before each path.
func PrefixPaths(prefix string, paths []string) []string {
	prefixed := make([]string, len(paths))
	for i, p := range paths {
		prefixed[i] = path.Join(prefix, p)
	}
	return prefixed
}

// EvaluatePaths computes the paths item. A changed path that a protected glob
// matches fails it as protected_path unless the plan names the path (see
// protectedPathNamed). A changed path that no affected_paths entry matches
// needs confirming as outside_plan; the details list the first
// outsidePlanMax of those and count them all. Paths are sorted and listed
// once.
func EvaluatePaths(in PathsInput) (domain.LadoVerifyItem, error) {
	changed := slices.Clone(in.Changed)
	slices.Sort(changed)
	changed = slices.Compact(changed)
	var details domain.LadoPathsDetails
	for _, p := range changed {
		if in.ProtectedOn && !protectedPathNamed(in, p) {
			details.Protected = append(details.Protected, p)
		}
		if !slices.ContainsFunc(in.Affected, func(entry string) bool { return MatchPath(entry, p) }) {
			if details.OutsideCount < outsidePlanMax {
				details.OutsidePlan = append(details.OutsidePlan, p)
			}
			details.OutsideCount++
		}
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case len(details.Protected) > 0:
		outcome, code = domain.LadoVerifyFail, domain.LadoCodeProtectedPath
	case details.OutsideCount > 0:
		outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeOutsidePlan
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyPaths, outcome, code, details)
}

// protectedPathNamed reports whether the changed path p may change: for each
// protected glob that matches p, an affected_paths entry that matches p names
// it (see entryNames). Every glob that matches needs naming (Q2 of #15). The
// path, the globs and the entries compare without case.
//
// In a workspace project the globs match the path from its repository's root
// too, where the plan's entries count without the repository's folder: an
// entry without that folder counts there only when it matches the path from
// the project root as well, so the workspace's own ".github/**" does not name
// a repository's ".github".
func protectedPathNamed(in PathsInput, p string) bool {
	p = strings.ToLower(p)
	entries := make([]string, len(in.Affected))
	for i, entry := range in.Affected {
		entries[i] = strings.ToLower(entry)
	}
	if !globsNamed(in.Protected, entries, p) {
		return false
	}
	folder := strings.ToLower(strings.Trim(in.Repository, "/"))
	inRepository, ok := strings.CutPrefix(p, folder+"/")
	if folder == "" || !ok {
		return true
	}
	var fromRepository []string
	for _, entry := range entries {
		if rest, ok := strings.CutPrefix(entry, folder+"/"); ok {
			fromRepository = append(fromRepository, rest)
		} else if MatchPath(entry, p) {
			fromRepository = append(fromRepository, entry)
		}
	}
	return globsNamed(in.Protected, fromRepository, inRepository)
}

// globsNamed reports whether each protected glob that matches the lowercased
// path p is named by an entry that matches p.
func globsNamed(globs, entries []string, p string) bool {
	var matching []string
	for _, entry := range entries {
		if MatchPath(entry, p) {
			matching = append(matching, entry)
		}
	}
	for _, glob := range globs {
		glob = strings.ToLower(glob)
		if MatchPath(glob, p) && !slices.ContainsFunc(matching, func(entry string) bool { return entryNames(entry, glob) }) {
			return false
		}
	}
	return true
}

// entryNames reports whether an affected_paths entry that matches a path is
// as precise as the protected glob that matches it: its fixed part is at
// least as long as the glob's, so ".github/workflows/*" and ".github" name
// paths under ".github/**" and "**" does not. Every entry would pass that way
// for a glob without a fixed part, as "**/CLAUDE.md", so there the glob must
// match the entry itself: "docs/CLAUDE.md" and "**/CLAUDE.md" name it, "**"
// and "docs/**" do not.
func entryNames(entry, glob string) bool {
	if need := fixedPrefix(glob); need != "" {
		return len(fixedPrefix(entry)) >= len(need)
	}
	entry = strings.Join(patternSegments(entry), "/")
	return entry == strings.Join(patternSegments(glob), "/") || MatchPath(glob, entry)
}
