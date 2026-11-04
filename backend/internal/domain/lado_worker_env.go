package domain

// LADO fork: the marks on a LADO execution worker's launch environment
// (issue #44). AO's Claude Code hook command reads them and refuses a push to
// a base branch, or a merge, before the command runs: the change has to reach
// the base branch through a pull request and the developer's integration
// approval.

import "strings"

const (
	// LadoWorkerEnv marks the launch environment of a LADO execution worker:
	// a single worker, a part, a merge or the integration worker. A plan
	// worker is marked with LadoPlanWorkerEnv instead.
	LadoWorkerEnv = "AO_LADO_WORKER"
	// LadoBaseBranchesEnv lists the base branches of the worker's project,
	// comma-separated (JoinLadoBaseBranches). The hook adds main, master and
	// the remote's default branch itself.
	LadoBaseBranchesEnv = "AO_LADO_BASE_BRANCHES"
)

// IsLadoRunClientRequestID reports whether s names a LADO execution worker
// (ParseLadoRunClientRequestID). A plan worker's id is none.
func IsLadoRunClientRequestID(s string) bool {
	_, ok := ParseLadoRunClientRequestID(s)
	return ok
}

// JoinLadoBaseBranches writes branches as a LadoBaseBranchesEnv value: in
// order, without blanks, "auto" or repeats. No branch name has a space, so
// SplitLadoBaseBranches reads it back.
func JoinLadoBaseBranches(branches []string) string {
	return strings.Join(normalizeLadoBaseBranches(branches), ",")
}

// SplitLadoBaseBranches reads a LadoBaseBranchesEnv value. Commas and white
// space both separate branches.
func SplitLadoBaseBranches(value string) []string {
	return normalizeLadoBaseBranches(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}))
}

func normalizeLadoBaseBranches(branches []string) []string {
	out := make([]string, 0, len(branches))
	seen := make(map[string]bool, len(branches))
	for _, branch := range branches {
		branch = strings.TrimPrefix(strings.TrimSpace(branch), "refs/heads/")
		if branch == "" || branch == DefaultBranchAuto || seen[branch] {
			continue
		}
		seen[branch] = true
		out = append(out, branch)
	}
	return out
}
