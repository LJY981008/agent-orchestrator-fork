package domain

import "testing"

func TestLadoPRRepo(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want TrackerRepo
	}{
		{"https://github.com/acme/web/pull/7", TrackerRepo{Provider: TrackerProviderGitHub, Native: "acme/web"}},
		{"https://www.github.com/Acme/Web/pull/7", TrackerRepo{Provider: TrackerProviderGitHub, Native: "acme/web"}},
		{"http://github.com/acme/web/pull/7", TrackerRepo{Provider: TrackerProviderGitHub, Native: "acme/web"}},
		{"https://gitlab.com/group/sub/repo/-/merge_requests/9", TrackerRepo{Provider: TrackerProviderGitLab, Native: "group/sub/repo"}},
		{"https://GitLab.Example.com/team/apps/front/-/merge_requests/9", TrackerRepo{Provider: TrackerProviderGitLab, Native: "team/apps/front", Host: "gitlab.example.com"}},
		{"http://gitlab.internal/team/apps/front/-/merge_requests/3", TrackerRepo{Provider: TrackerProviderGitLab, Native: "team/apps/front", Host: "gitlab.internal"}},
		{"https://gitlab.example.com:8443/team/front/-/merge_requests/2", TrackerRepo{Provider: TrackerProviderGitLab, Native: "team/front", Host: "gitlab.example.com"}},
	} {
		got, err := LadoPRRepo(tc.url)
		if err != nil || got != tc.want {
			t.Errorf("LadoPRRepo(%q) = %+v, %v; want %+v", tc.url, got, err, tc.want)
		}
	}

	// The PR's repository compares equal with a child repository's origin,
	// SSH port and all.
	pr, err := LadoPRRepo("https://gitlab.example.com/team/apps/front/-/merge_requests/9")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := LadoTrackerRepoFromOrigin("ssh://git@gitlab.example.com:2222/team/apps/front.git")
	if err != nil || !LadoSameRepo(pr, origin) {
		t.Errorf("PR repository %+v and origin %+v (%v) differ", pr, origin, err)
	}

	for _, url := range []string{
		"https://acme.ghe.io/acme/web/pull/7",
		"https://github.com/acme/web/issues/7",
		"https://gitlab.com/group/repo/merge_requests/9",
		"https://example.com/acme/web/pull/7",
		"ssh://git@github.com/acme/web.git",
		"",
	} {
		if got, err := LadoPRRepo(url); err == nil {
			t.Errorf("LadoPRRepo(%q) = %+v, want an error", url, got)
		}
	}
}
