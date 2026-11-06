package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestResolvedWorkspaceAssets(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantDef  bool
		want     []string
		wantKeep bool
	}{
		{name: "no workspaceAssets", json: `{}`, wantDef: true},
		{name: "empty object", json: `{"workspaceAssets":{}}`, wantDef: true},
		{name: "null exclude", json: `{"workspaceAssets":{"exclude":null}}`, wantDef: true},
		{name: "keep without exclude", json: `{"workspaceAssets":{"absoluteLinks":"keep"}}`, wantDef: true, wantKeep: true},
		{name: "skip without exclude", json: `{"workspaceAssets":{"absoluteLinks":"skip"}}`, wantDef: true},
		{name: "empty exclude copies everything", json: `{"workspaceAssets":{"exclude":[]}}`, want: []string{}},
		{name: "custom list replaces defaults", json: `{"workspaceAssets":{"exclude":["dist","tools/cache"],"absoluteLinks":"keep"}}`, want: []string{"dist", "tools/cache"}, wantKeep: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg ProjectConfig
			if err := json.Unmarshal([]byte(tc.json), &cfg); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			exclude, keep := cfg.ResolvedWorkspaceAssets()
			want := tc.want
			if tc.wantDef {
				want = DefaultWorkspaceAssetExcludes
			}
			if exclude == nil {
				t.Fatal("exclude is nil, want a non-nil list")
			}
			if !reflect.DeepEqual(exclude, want) {
				t.Fatalf("exclude = %#v, want %#v", exclude, want)
			}
			if keep != tc.wantKeep {
				t.Fatalf("keep = %v, want %v", keep, tc.wantKeep)
			}
		})
	}
}

func TestResolvedWorkspaceAssetsReturnsCopy(t *testing.T) {
	exclude, _ := ProjectConfig{}.ResolvedWorkspaceAssets()
	exclude[0] = "changed"
	if DefaultWorkspaceAssetExcludes[0] != ".venv" {
		t.Fatalf("defaults mutated: %v", DefaultWorkspaceAssetExcludes)
	}
	own := []string{"a"}
	cfg := ProjectConfig{WorkspaceAssets: &WorkspaceAssetsConfig{Exclude: own}}
	got, _ := cfg.ResolvedWorkspaceAssets()
	got[0] = "b"
	if own[0] != "a" {
		t.Fatal("configured list mutated")
	}
}

func TestWorkspaceAssetsEmptyExcludeRoundTrips(t *testing.T) {
	cfg := ProjectConfig{WorkspaceAssets: &WorkspaceAssetsConfig{Exclude: []string{}}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"workspaceAssets":{"exclude":[]}`) {
		t.Fatalf("marshal = %s, want exclude: []", raw)
	}
	var back ProjectConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.WorkspaceAssets == nil || back.WorkspaceAssets.Exclude == nil || len(back.WorkspaceAssets.Exclude) != 0 {
		t.Fatalf("round trip = %#v", back.WorkspaceAssets)
	}
	if raw, _ := json.Marshal(ProjectConfig{}); strings.Contains(string(raw), "workspaceAssets") {
		t.Fatalf("zero config marshals workspaceAssets: %s", raw)
	}
}

func TestWorkspaceAssetExcluded(t *testing.T) {
	cases := []struct {
		rel      string
		patterns []string
		want     bool
	}{
		{".venv", DefaultWorkspaceAssetExcludes, true},
		{".venv/bin/python", DefaultWorkspaceAssetExcludes, true},
		{"web/node_modules", DefaultWorkspaceAssetExcludes, true},
		{"tools/a/b/node_modules/x/index.js", DefaultWorkspaceAssetExcludes, true},
		{`pkg\__pycache__\m.pyc`, DefaultWorkspaceAssetExcludes, true},
		{"data", DefaultWorkspaceAssetExcludes, false},
		{"data/venv.txt", DefaultWorkspaceAssetExcludes, false},
		{"anything", nil, false},
		{"anything", []string{}, false},
		{"", DefaultWorkspaceAssetExcludes, false},
		{"logs/app.log", []string{"*.log"}, true},
		{"build-1/out", []string{"build-*"}, true},
		{"tools/cache", []string{"tools/cache"}, true},
		{"tools/cache/file", []string{"tools/cache"}, true},
		{"tools/cache/file", []string{"tools/cache/"}, true},
		{"other/tools/cache", []string{"tools/cache"}, false},
		{"tools", []string{"tools/cache"}, false},
		{"tools/cachex", []string{"tools/cache"}, false},
		{"a/x/c/d", []string{"a/*/c"}, true},
		{"./data/tmp", []string{"data/tmp"}, true},
		{"data", []string{"["}, false},
	}
	for _, tc := range cases {
		if got := WorkspaceAssetExcluded(tc.rel, tc.patterns); got != tc.want {
			t.Errorf("WorkspaceAssetExcluded(%q, %v) = %v, want %v", tc.rel, tc.patterns, got, tc.want)
		}
	}
}

func TestValidateWorkspaceAssetsAccepts(t *testing.T) {
	ok := []*WorkspaceAssetsConfig{
		nil,
		{},
		{Exclude: []string{}},
		{Exclude: []string{".venv", "tools/cache/", "*.log", "a/[bc]/d"}, AbsoluteLinks: "keep"},
		{Exclude: []string{strings.Repeat("a", WorkspaceAssetExcludeMaxLen)}, AbsoluteLinks: "skip"},
		{Exclude: make100("x")},
	}
	for _, c := range ok {
		if err := (ProjectConfig{WorkspaceAssets: c}).Validate(); err != nil {
			t.Errorf("Validate(%#v) = %v, want nil", c, err)
		}
	}
}

func TestValidateWorkspaceAssetsRejects(t *testing.T) {
	bad := map[string]*WorkspaceAssetsConfig{
		"unknown links":   {AbsoluteLinks: "follow"},
		"empty item":      {Exclude: []string{""}},
		"blank item":      {Exclude: []string{"  "}},
		"padded item":     {Exclude: []string{" .venv"}},
		"nul":             {Exclude: []string{"a\x00b"}},
		"absolute":        {Exclude: []string{"/tmp/x"}},
		"backslash abs":   {Exclude: []string{`\x`}},
		"drive":           {Exclude: []string{`C:\x`}},
		"dotdot":          {Exclude: []string{"../x"}},
		"inner dotdot":    {Exclude: []string{"a/../b"}},
		"dot segment":     {Exclude: []string{"./a"}},
		"double slash":    {Exclude: []string{"a//b"}},
		"bad glob":        {Exclude: []string{"["}},
		"bad glob nested": {Exclude: []string{"a/[b"}},
		"too long":        {Exclude: []string{strings.Repeat("a", WorkspaceAssetExcludeMaxLen+1)}},
		"too many":        {Exclude: append(make100("x"), "y")},
	}
	for name, c := range bad {
		err := (ProjectConfig{WorkspaceAssets: c}).Validate()
		if err == nil {
			t.Errorf("%s: Validate = nil, want error", name)
			continue
		}
		if !strings.Contains(err.Error(), "workspaceAssets") {
			t.Errorf("%s: error %q does not name workspaceAssets", name, err)
		}
	}
}

func make100(prefix string) []string {
	out := make([]string, WorkspaceAssetExcludeMaxItems)
	for i := range out {
		out[i] = prefix
	}
	return out
}
