package domain

import "strings"

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
