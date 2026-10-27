package ladoverify

import "testing"

func TestGlobMatching(t *testing.T) {
	t.Run("MatchPath", func(t *testing.T) {
		for _, tc := range []struct {
			pattern, path string
			want          bool
		}{
			// ** stands for zero or more segments.
			{"**/CLAUDE.md", "CLAUDE.md", true},
			{"**/CLAUDE.md", "docs/team/CLAUDE.md", true},
			{"**/CLAUDE.md", "docs/CLAUDE.md.bak", false},
			{".github/**", ".github/workflows/ci.yml", true},
			{".github/**", ".githubx/ci.yml", false},
			{"a/**/b", "a/b", true},
			{"a/**/b", "a/x/y/b", true},
			{"a/**/b", "a/x/y/c", false},
			{"**", "src/app.go", true},
			{"**/**/x", "a/x", true},
			// * stands for one segment, or part of one.
			{".github/workflows/*", ".github/workflows/ci.yml", true},
			{".github/workflows/*", ".github/workflows/sub/ci.yml", false},
			{"*.md", "README.md", true},
			{"*.md", "docs/README.md", false},
			{"src/*/app.go", "src/web/app.go", true},
			{"src/a?.go", "src/ab.go", true},
			// A pattern without a wildcard is that path and everything
			// below it.
			{"docs", "docs", true},
			{"docs", "docs/guide.md", true},
			{"docs", "docs/team/notes.md", true},
			{"docs", "docs2/guide.md", false},
			{"docs", "doc", false},
			{"docs/", "docs/guide.md", true},
			{"./docs", "docs/guide.md", true},
			{".gitlab-ci.yml", ".gitlab-ci.yml", true},
			{".gitlab-ci.yml", "ci/.gitlab-ci.yml", false},
			// Character classes.
			{"src/[ab].go", "src/a.go", true},
			{"src/[ab].go", "src/b.go", true},
			{"src/[ab].go", "src/c.go", false},
			{"src/[^ab].go", "src/c.go", true},
			// A malformed pattern matches nothing.
			{"src/[", "src/[", false},
			{"src/[ab", "src/a", false},
			{"x/[/**", "y/z", false},
			{"**/[", "a/[", false},
			{"", "docs", false},
			{".", "docs", false},
		} {
			if got := MatchPath(tc.pattern, tc.path); got != tc.want {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		}
	})

	t.Run("fixedPrefix", func(t *testing.T) {
		for _, tc := range []struct{ pattern, want string }{
			{".github/**", ".github/"},
			{".github/workflows/*", ".github/workflows/"},
			{"**/CLAUDE.md", ""},
			{"src/a?.go", "src/a"},
			{"src/[ab].go", "src/"},
			// A pattern without a wildcard covers its folder, as "dir/**"
			// does.
			{".github", ".github/"},
			{".github/workflows/ci.yml", ".github/workflows/ci.yml/"},
			// Empty and "." segments do not lengthen it.
			{"./././.github/**", ".github/"},
			{"././**", ""},
		} {
			if got := fixedPrefix(tc.pattern); got != tc.want {
				t.Errorf("fixedPrefix(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
		}
	})
}
