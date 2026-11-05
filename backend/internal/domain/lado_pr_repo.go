package domain

import (
	"slices"
	"strings"
)

// LadoPRRepo returns the repository a GitHub pull request or GitLab merge
// request web URL belongs to (#59), in the form LadoTrackerRepoFromOrigin
// gives a git origin: no port, a lowercase host, an empty host for
// gitlab.com, and GitHub on github.com only. A workspace project's child
// repository whose origin is the PR's compares equal with LadoSameRepo. It
// reads the URL only and never reaches the host.
func LadoPRRepo(prURL string) (TrackerRepo, error) {
	ref, err := ParseChangeRequestURL(prURL)
	if err != nil {
		return TrackerRepo{}, err
	}
	scheme, _, _ := strings.Cut(ref.URL, "://")
	return LadoTrackerRepoFromOrigin(scheme + "://" + ref.Host + "/" + ref.Repository)
}

// LadoMatchWorkspaceRepo returns the child repository of a workspace project
// that a PR's repository (LadoPRRepo) is open in (#64). A child whose origin
// is the same repository (LadoSameRepo) wins. Without one, a GitLab instance
// served under a relative URL root names its merge requests with that root
// in front of the project's path (https://host/gitlab/group/repo/-/...)
// while its git origins do not (git@host:group/repo.git): then the one
// GitLab child on the same host whose path ends the PR's path, segment by
// segment and without case, is the match. Two such children, or a child
// whose origin LADO cannot read, give no match from the suffix rule; ok is
// false without a match.
func LadoMatchWorkspaceRepo(prRepo TrackerRepo, children []WorkspaceRepoRecord) (WorkspaceRepoRecord, bool) {
	type candidate struct {
		child WorkspaceRepoRecord
		repo  TrackerRepo
	}
	candidates := make([]candidate, 0, len(children))
	for _, child := range children {
		repo, err := LadoTrackerRepoFromOrigin(child.RepoOriginURL)
		if err != nil {
			continue
		}
		if LadoSameRepo(repo, prRepo) {
			return child, true
		}
		candidates = append(candidates, candidate{child: child, repo: repo})
	}
	if prRepo.Provider != TrackerProviderGitLab {
		return WorkspaceRepoRecord{}, false
	}
	prPath := strings.Split(strings.ToLower(prRepo.Native), "/")
	var match WorkspaceRepoRecord
	found := 0
	for _, c := range candidates {
		if c.repo.Provider != TrackerProviderGitLab || !strings.EqualFold(c.repo.Host, prRepo.Host) {
			continue
		}
		childPath := strings.Split(strings.ToLower(c.repo.Native), "/")
		if len(childPath) >= len(prPath) || !slices.Equal(childPath, prPath[len(prPath)-len(childPath):]) {
			continue
		}
		match = c.child
		found++
	}
	if found != 1 {
		return WorkspaceRepoRecord{}, false
	}
	return match, true
}
