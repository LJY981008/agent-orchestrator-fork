package ladorelay

import (
	"slices"
	"testing"
)

func TestMentionedPaths(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"backend/internal/service/ladorelay/plan.go 를 고치세요", []string{"backend/internal/service/ladorelay/plan.go"}},
		{"`plan_text.go`의 orchestratorSection 과 README.md 를 보세요.", []string{"plan_text.go", "README.md"}},
		{"./frontend/src/App.tsx:42 에서", []string{"frontend/src/App.tsx"}},
		{`backend\internal\cli\lado.go 도`, []string{"backend/internal/cli/lado.go"}},
		{"docs/LJY-PRD/ 아래 문서를", []string{"docs/LJY-PRD"}},
		{"backend/internal/service 를 정리", []string{"backend/internal/service"}},
		{"같은 경로 a/b.go 와 a/b.go", []string{"a/b.go"}},
		{"https://github.com/acme/app/pull/3 와 www.example.com/x.html", nil},
		{"origin/main 에 rebase 하고 and/or e.g. v1.2 를 보고 --outside-plan 으로", nil},
		{"테스트를 다시 돌리세요", nil},
	}
	for _, tc := range cases {
		if got := mentionedPaths(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("mentionedPaths(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestCovered(t *testing.T) {
	patterns := []string{"backend/internal/service/ladorelay/**", "backend/internal/domain/lado_relay*.go", "docs/LJY-PRD/TDD.md"}
	cases := []struct {
		path string
		want bool
	}{
		{"backend/internal/service/ladorelay/plan.go", true},
		{"backend/internal/domain/lado_relay_test.go", true},
		{"docs/LJY-PRD/TDD.md", true},
		{"ladorelay/plan.go", true},
		{"service/ladorelay/plan.go", true},
		{"lado_relay.go", true},
		{"plan.go", true},
		{"frontend/src/App.tsx", false},
		{"backend/internal/domain/lado_task.go", false},
		{"domain/lado_task.go", false},
		{"docs/LJY-PRD/PRD.md", false},
	}
	for _, tc := range cases {
		if got := covered(patterns, tc.path); got != tc.want {
			t.Errorf("covered(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	if covered([]string{"docs/LJY-PRD/TDD.md"}, "App.tsx") {
		t.Error("a bare name no pattern names is covered")
	}
}
