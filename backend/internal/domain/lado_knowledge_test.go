package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLadoKnowledgeSlug(t *testing.T) {
	for _, ok := range []string{"a", "0", "build-cache", strings.Repeat("a", 64)} {
		if !domain.ValidLadoKnowledgeSlug(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-a", "A", "a_b", "a.b", "../x", "a/b", `a\b`, "con:", strings.Repeat("a", 65), "노트"} {
		if domain.ValidLadoKnowledgeSlug(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLadoKnowledgeSourcePath(t *testing.T) {
	good := map[string]string{
		"a/b.go":           "a/b.go",
		"a/b.go:12":        "a/b.go",
		"a/b.go:12-30":     "a/b.go",
		" ./a//b.go ":      "a/b.go",
		"docs/x y.md":      "docs/x y.md",
		"backend/go.mod:1": "backend/go.mod",
	}
	for in, want := range good {
		got, err := domain.NormalizeLadoKnowledgeSourcePath(in)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", ":!x", ":(glob)*", "/etc/passwd", `\x`, `a\b`, "../x", "a/../b", "a/..", "C:/x", "a:b", ".", "./", "a/b:12:3", "a\x00b"} {
		if got, err := domain.NormalizeLadoKnowledgeSourcePath(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

const sampleNote = `---
title: 세션 작업 폴더의 기준
kind: decision
sources:
  - path: backend/a.go:10-20
  - backend/b.go
revision: 7
author: someone
updated: 2020-01-01T00:00:00Z
tags: [x]
---
본문 [[other-note]]
`

func TestParseLadoKnowledgeNote(t *testing.T) {
	n, err := domain.ParseLadoKnowledgeNote("worktree-base", []byte(sampleNote))
	if err != nil {
		t.Fatal(err)
	}
	if n.Title != "세션 작업 폴더의 기준" || n.Kind != domain.LadoKnowledgeDecision || n.Revision != 7 || n.Author != "someone" {
		t.Fatalf("note = %+v", n)
	}
	if len(n.Sources) != 2 || n.Sources[0].Path != "backend/a.go" || n.Sources[1].Path != "backend/b.go" {
		t.Fatalf("sources = %+v", n.Sources)
	}
	if !n.Updated.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("updated = %v", n.Updated)
	}
	if n.Body != "본문 [[other-note]]\n" {
		t.Fatalf("body = %q", n.Body)
	}
}

func TestParseLadoKnowledgeNoteRefuses(t *testing.T) {
	cases := map[string]struct {
		slug, content, field string
	}{
		"slug":       {"../x", sampleNote, "slug"},
		"no front":   {"a", "just text\n", ""},
		"no title":   {"a", "---\nkind: map\n---\nx\n", "title"},
		"bad kind":   {"a", "---\ntitle: t\nkind: story\n---\nx\n", "kind"},
		"magic":      {"a", "---\ntitle: t\nkind: map\nsources: [':!x']\n---\n", "sources[0].path"},
		"absolute":   {"a", "---\ntitle: t\nkind: map\nsources:\n  - path: /etc/x\n---\n", "sources[0].path"},
		"twice":      {"a", "---\ntitle: t\nkind: map\nsources: [a.go, 'a.go:3']\n---\n", "sources[1].path"},
		"two lines":  {"a", "---\ntitle: \"a\\nb\"\nkind: map\n---\n", "title"},
		"bad yaml":   {"a", "---\ntitle: [\n---\n", ""},
		"bad rev":    {"a", "---\ntitle: t\nkind: map\nrevision: abc\n---\n", ""},
		"too large":  {"a", "---\ntitle: t\nkind: map\n---\n" + strings.Repeat("x", domain.LadoKnowledgeNoteMaxBytes), ""},
		"not utf8":   {"a", "---\ntitle: t\nkind: map\n---\n\xff\n", ""},
		"long title": {"a", "---\ntitle: " + strings.Repeat("가", domain.LadoKnowledgeTitleMaxRunes+1) + "\nkind: map\n---\n", "title"},
	}
	for name, c := range cases {
		_, err := domain.ParseLadoKnowledgeNote(c.slug, []byte(c.content))
		var ne *domain.LadoKnowledgeNoteError
		if !errors.As(err, &ne) {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if ne.Field != c.field {
			t.Errorf("%s: field = %q (%v), want %q", name, ne.Field, err, c.field)
		}
	}
}

func TestRenderLadoKnowledgeNoteRoundTrip(t *testing.T) {
	n := domain.LadoKnowledgeNote{
		Slug: "a", Title: "제목: 콜론", Kind: domain.LadoKnowledgePitfall,
		Sources: []domain.LadoKnowledgeSource{{Path: "x/y.go", Blob: "abc"}},
		Commit:  "def", Revision: 3, Updated: time.Date(2026, 11, 4, 1, 2, 3, 0, time.UTC), Author: "s-1",
		Body: "\n\n본문\r\n줄",
	}
	data := domain.RenderLadoKnowledgeNote(n)
	if !strings.HasPrefix(string(data), "---\ntitle: '제목: 콜론'\nkind: pitfall\nsources:\n") || !strings.HasSuffix(string(data), "---\n\n본문\n줄\n") {
		t.Fatalf("rendered:\n%s", data)
	}
	back, err := domain.ParseLadoKnowledgeNote("a", data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Title != n.Title || back.Revision != 3 || back.Commit != "def" || back.Author != "s-1" || !back.Updated.Equal(n.Updated) ||
		len(back.Sources) != 1 || back.Sources[0] != n.Sources[0] || back.Body != "본문\n줄\n" {
		t.Fatalf("round trip = %+v", back)
	}
	empty := string(domain.RenderLadoKnowledgeNote(domain.LadoKnowledgeNote{Slug: "b", Title: "t", Kind: domain.LadoKnowledgeMap, Revision: 1}))
	if strings.Contains(empty, "sources") || strings.Contains(empty, "commit") || strings.Contains(empty, "author") || strings.Contains(empty, "updated") {
		t.Fatalf("empty fields rendered:\n%s", empty)
	}
}

func TestLadoKnowledgeSecret(t *testing.T) {
	for want, s := range map[string]string{
		"github_token":   "token ghp_" + strings.Repeat("A1", 18),
		"github_pat":     "github_pat_" + strings.Repeat("b", 30),
		"gitlab_pat":     "glpat-" + strings.Repeat("c", 20),
		"anthropic_key":  "sk-ant-api03-" + strings.Repeat("d", 30),
		"aws_access_key": "key=AKIA" + strings.Repeat("Z", 16),
		"private_key":    "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nxx",
	} {
		if got := domain.LadoKnowledgeSecret([]byte(s)); got != want {
			t.Errorf("%s: got %q", want, got)
		}
	}
	for _, s := range []string{"ghp_ prefix is mentioned", "use AKIA keys", "BEGIN PUBLIC KEY", "glpat- tokens"} {
		if got := domain.LadoKnowledgeSecret([]byte(s)); got != "" {
			t.Errorf("%q flagged as %s", s, got)
		}
	}
}

func knowledgeEntries() []domain.LadoKnowledgeEntry {
	return []domain.LadoKnowledgeEntry{
		{Slug: "z-dec", Title: "결정 Z", Kind: domain.LadoKnowledgeDecision, Revision: 2, State: domain.LadoKnowledgeStale},
		{Slug: "a-dec", Title: "결정 A", Kind: domain.LadoKnowledgeDecision, Revision: 1, State: domain.LadoKnowledgeFresh},
		{Slug: "map", Title: "지도", Kind: domain.LadoKnowledgeMap, Revision: 4, State: domain.LadoKnowledgeUnchecked},
		{Slug: "loose", Title: "밖에서\n만든", Kind: "", Revision: 1, State: domain.LadoKnowledgeUnchecked},
	}
}

func TestRenderLadoKnowledgePromptIndex(t *testing.T) {
	got, truncated := domain.RenderLadoKnowledgePromptIndex(knowledgeEntries())
	want := "### 코드 지도 (map)\n- [[map]] — 지도 (rev 4)\n\n" +
		"### 결정 (decision)\n- [[a-dec]] — 결정 A (rev 1)\n- [[z-dec]] — 결정 Z (rev 2) — 확인 필요\n\n" +
		"### 기타\n- [[loose]] — 밖에서 만든 (rev 1)\n"
	if truncated || got != want {
		t.Fatalf("index (truncated %v):\n%s\nwant:\n%s", truncated, got, want)
	}
	if got, truncated := domain.RenderLadoKnowledgePromptIndex(nil); got != "" || truncated {
		t.Fatalf("empty index = %q %v", got, truncated)
	}
}

func TestRenderLadoKnowledgePromptIndexTruncates(t *testing.T) {
	var entries []domain.LadoKnowledgeEntry
	for i := range domain.LadoKnowledgeMaxNotes {
		entries = append(entries, domain.LadoKnowledgeEntry{
			Slug: fmt.Sprintf("note-%03d", i), Title: strings.Repeat("긴 제목 ", 10), Kind: domain.LadoKnowledgeResearch, Revision: 1,
		})
	}
	got, truncated := domain.RenderLadoKnowledgePromptIndex(entries)
	if !truncated || len(got) > domain.LadoKnowledgePromptIndexMaxBytes {
		t.Fatalf("truncated = %v, %d bytes", truncated, len(got))
	}
	shown := strings.Count(got, "- [[")
	if !strings.Contains(got, fmt.Sprintf("노트 %d개를 뺐다", len(entries)-shown)) {
		t.Fatalf("no truncation line for %d shown:\n%s", shown, got[len(got)-300:])
	}
}

func TestRenderLadoKnowledgeDiskIndex(t *testing.T) {
	got := string(domain.RenderLadoKnowledgeDiskIndex(knowledgeEntries()))
	if !strings.HasPrefix(got, domain.LadoKnowledgeDiskIndexPreamble) || !strings.Contains(got, "신선도는 앱의 지식 카드와 세션 목차에서 본다") {
		t.Fatalf("preamble missing:\n%s", got)
	}
	if strings.Contains(got, "확인 필요") {
		t.Fatalf("disk index shows freshness:\n%s", got)
	}
	if !strings.Contains(got, "## 결정 (decision)\n\n- [[a-dec]] — 결정 A (rev 1)\n- [[z-dec]] — 결정 Z (rev 2)\n") {
		t.Fatalf("decision group:\n%s", got)
	}
	if empty := string(domain.RenderLadoKnowledgeDiskIndex(nil)); !strings.Contains(empty, "노트가 아직 없다") {
		t.Fatalf("empty index:\n%s", empty)
	}
}
