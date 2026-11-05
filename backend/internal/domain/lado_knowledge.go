package domain

// LADO fork: the project knowledge store (issue #24, docs/LJY-PRD/adr/0010-project-knowledge-store.md).
// A project's knowledge is a folder of Markdown notes under AO's state
// directory. Each note is YAML front matter and a body; LADO renders the
// front matter on every write, so only title, kind and sources come from the
// writer. The revision a note has is the one LADO records beside the notes,
// not the one its front matter shows.

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// LadoKnowledgeKind says what a note records.
type LadoKnowledgeKind string

// Note kinds.
const (
	// LadoKnowledgeMap says where things are in the code.
	LadoKnowledgeMap LadoKnowledgeKind = "map"
	// LadoKnowledgeDecision is a decision and its reasons.
	LadoKnowledgeDecision LadoKnowledgeKind = "decision"
	// LadoKnowledgePitfall is a trap or a fact of the environment.
	LadoKnowledgePitfall LadoKnowledgeKind = "pitfall"
	// LadoKnowledgeResearch is an investigation with file:line evidence.
	LadoKnowledgeResearch LadoKnowledgeKind = "research"
	// LadoKnowledgeConvention is a rule the project follows.
	LadoKnowledgeConvention LadoKnowledgeKind = "convention"
)

// LadoKnowledgeKinds are the kinds in the order the index lists them.
var LadoKnowledgeKinds = []LadoKnowledgeKind{
	LadoKnowledgeMap, LadoKnowledgeDecision, LadoKnowledgePitfall, LadoKnowledgeConvention, LadoKnowledgeResearch,
}

// Valid reports whether k is one of the note kinds.
func (k LadoKnowledgeKind) Valid() bool { return slices.Contains(LadoKnowledgeKinds, k) }

// LadoKnowledgeState is how a note's sources compare with the project's base
// branch.
type LadoKnowledgeState string

// Note states.
const (
	// LadoKnowledgeFresh: every source's blob is the base branch's.
	LadoKnowledgeFresh LadoKnowledgeState = "fresh"
	// LadoKnowledgeStale: a source changed on the base branch or is not on
	// it; the note needs checking before use.
	LadoKnowledgeStale LadoKnowledgeState = "stale"
	// LadoKnowledgeUnchecked: the note has nothing to compare, or LADO
	// could not compare it.
	LadoKnowledgeUnchecked LadoKnowledgeState = "unchecked"
)

// Limits of the store.
const (
	// LadoKnowledgeNoteMaxBytes caps one note file, front matter included.
	LadoKnowledgeNoteMaxBytes = 64 << 10
	// LadoKnowledgeMaxNotes caps the notes of one project.
	LadoKnowledgeMaxNotes = 500
	// LadoKnowledgePromptIndexMaxBytes caps the index a session's standing
	// instructions carry.
	LadoKnowledgePromptIndexMaxBytes = 16 << 10
	// LadoKnowledgeTitleMaxRunes caps a note's title.
	LadoKnowledgeTitleMaxRunes = 200
	// LadoKnowledgeMaxSources caps the sources of one note.
	LadoKnowledgeMaxSources = 64
	// LadoKnowledgeStaleMark ends an index line whose note needs checking.
	LadoKnowledgeStaleMark = " — 확인 필요"
)

// Authors LADO records for notes no session wrote.
const (
	// LadoKnowledgeAuthorExternal: a change made in the folder, outside LADO.
	LadoKnowledgeAuthorExternal = "external"
	// LadoKnowledgeAuthorClaudeMemory: imported from Claude Code's memory.
	LadoKnowledgeAuthorClaudeMemory = "import:claude_memory"
	// LadoKnowledgeAuthorProjectPrefix starts the author of a note imported
	// from another project's knowledge, followed by that project's id.
	LadoKnowledgeAuthorProjectPrefix = "import:project:"
)

// ladoKnowledgeSlug is a note's name and file name (<slug>.md). It holds
// only characters every file system takes, Windows' included.
var ladoKnowledgeSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidLadoKnowledgeSlug reports whether s can name a note.
func ValidLadoKnowledgeSlug(s string) bool { return ladoKnowledgeSlug.MatchString(s) }

// LadoKnowledgeSource is a file a note relies on: a slash-separated path
// relative to the repository root and the blob it had when the note was
// written (empty when LADO has none).
type LadoKnowledgeSource struct {
	Path string `yaml:"path" json:"path"`
	Blob string `yaml:"blob,omitempty" json:"blob,omitempty"`
}

// LadoKnowledgeNote is one note.
type LadoKnowledgeNote struct {
	Slug    string
	Title   string
	Kind    LadoKnowledgeKind
	Sources []LadoKnowledgeSource
	// Commit is the writer's HEAD when it wrote, for information only.
	Commit string
	// Revision as the front matter shows it; LADO overwrites it on render.
	Revision int
	Updated  time.Time
	Author   string
	// Body is the Markdown after the front matter, without the blank lines
	// that start it.
	Body string
}

// LadoKnowledgeNoteError is a note LADO cannot take. Field names the front
// matter field at fault, empty for the note as a whole.
type LadoKnowledgeNoteError struct {
	Field  string
	Reason string
}

func (e *LadoKnowledgeNoteError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return e.Field + ": " + e.Reason
}

func noteError(field, format string, args ...any) error {
	return &LadoKnowledgeNoteError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// rawKnowledgeSource accepts a source as {path, blob} or as a bare path.
type rawKnowledgeSource LadoKnowledgeSource

func (s *rawKnowledgeSource) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&s.Path)
	}
	var full struct {
		Path string `yaml:"path"`
		Blob string `yaml:"blob"`
	}
	if err := node.Decode(&full); err != nil {
		return err
	}
	s.Path, s.Blob = full.Path, full.Blob
	return nil
}

type rawKnowledgeFront struct {
	Title    string               `yaml:"title"`
	Kind     string               `yaml:"kind"`
	Sources  []rawKnowledgeSource `yaml:"sources"`
	Commit   string               `yaml:"commit"`
	Revision int                  `yaml:"revision"`
	Updated  string               `yaml:"updated"`
	Author   string               `yaml:"author"`
}

// ParseLadoKnowledgeNote reads a note as a writer sends it or as it is on
// disk, and checks it with Validate. Fields LADO renders (commit, revision,
// updated, author) are read but not checked; unknown fields are dropped.
// Source paths come back normalized, as NormalizeLadoKnowledgeSourcePath
// makes them.
func ParseLadoKnowledgeNote(slug string, content []byte) (LadoKnowledgeNote, error) {
	if !ValidLadoKnowledgeSlug(slug) {
		return LadoKnowledgeNote{}, noteError("slug", "a slug is 1 to 64 of a-z, 0-9 and '-', starting with a letter or digit")
	}
	if len(content) > LadoKnowledgeNoteMaxBytes {
		return LadoKnowledgeNote{}, noteError("", "a note is at most %d bytes", LadoKnowledgeNoteMaxBytes)
	}
	if !utf8.Valid(content) {
		return LadoKnowledgeNote{}, noteError("", "a note is UTF-8 text")
	}
	front, body, ok := SplitLadoFrontMatter(string(content))
	if !ok {
		return LadoKnowledgeNote{}, noteError("", "a note starts with YAML front matter between '---' lines")
	}
	var raw rawKnowledgeFront
	if err := yaml.Unmarshal([]byte(front), &raw); err != nil {
		return LadoKnowledgeNote{}, noteError("", "the front matter is not valid YAML: %v", err)
	}
	note := LadoKnowledgeNote{
		Slug:     slug,
		Title:    strings.TrimSpace(raw.Title),
		Kind:     LadoKnowledgeKind(strings.TrimSpace(raw.Kind)),
		Commit:   strings.TrimSpace(raw.Commit),
		Revision: raw.Revision,
		Author:   strings.TrimSpace(raw.Author),
		Body:     strings.TrimLeft(body, "\n"),
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw.Updated)); err == nil {
		note.Updated = t
	}
	for i, s := range raw.Sources {
		p, err := NormalizeLadoKnowledgeSourcePath(s.Path)
		if err != nil {
			return LadoKnowledgeNote{}, noteError("sources["+strconv.Itoa(i)+"].path", "%v", err)
		}
		note.Sources = append(note.Sources, LadoKnowledgeSource{Path: p, Blob: strings.TrimSpace(s.Blob)})
	}
	if err := note.Validate(); err != nil {
		return LadoKnowledgeNote{}, err
	}
	return note, nil
}

// Validate checks what a writer controls: slug, title, kind and sources.
func (n LadoKnowledgeNote) Validate() error {
	if !ValidLadoKnowledgeSlug(n.Slug) {
		return noteError("slug", "a slug is 1 to 64 of a-z, 0-9 and '-', starting with a letter or digit")
	}
	switch {
	case n.Title == "":
		return noteError("title", "a note needs a title")
	case utf8.RuneCountInString(n.Title) > LadoKnowledgeTitleMaxRunes:
		return noteError("title", "a title is at most %d characters", LadoKnowledgeTitleMaxRunes)
	case strings.IndexFunc(n.Title, unicode.IsControl) >= 0:
		return noteError("title", "a title is one line")
	}
	if !n.Kind.Valid() {
		return noteError("kind", "kind is one of map, decision, pitfall, research, convention")
	}
	if len(n.Sources) > LadoKnowledgeMaxSources {
		return noteError("sources", "a note has at most %d sources", LadoKnowledgeMaxSources)
	}
	seen := map[string]bool{}
	for i, s := range n.Sources {
		p, err := NormalizeLadoKnowledgeSourcePath(s.Path)
		if err != nil || p != s.Path {
			return noteError("sources["+strconv.Itoa(i)+"].path", "not a normalized repository path")
		}
		if seen[p] {
			return noteError("sources["+strconv.Itoa(i)+"].path", "%s is listed twice", p)
		}
		seen[p] = true
	}
	return nil
}

// ladoSourceLines is the ':<line>' or ':<line>-<line>' a writer may put after
// a source path.
var ladoSourceLines = regexp.MustCompile(`:\d+(-\d+)?$`)

// NormalizeLadoKnowledgeSourcePath takes a source path as a writer gives it
// and returns the slash-separated repository path LADO stores. It drops a
// trailing ':<line>' or ':<line>-<line>' and refuses an empty path, an
// absolute one, a backslash, a '..' segment, a leading ':' (git pathspec
// magic) and any other ':' or control character.
func NormalizeLadoKnowledgeSourcePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return "", errors.New("a source path is empty")
	case strings.HasPrefix(p, ":"):
		return "", errors.New("a source path cannot start with ':'")
	case strings.HasPrefix(p, "/"):
		return "", errors.New("a source path is relative to the repository root")
	case strings.Contains(p, `\`):
		return "", errors.New("a source path uses '/' between folders")
	}
	p = ladoSourceLines.ReplaceAllString(p, "")
	if strings.Contains(p, ":") {
		return "", errors.New("a source path cannot hold ':'")
	}
	if strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", errors.New("a source path cannot hold control characters")
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return "", errors.New("a source path cannot hold '..'")
	}
	clean := path.Clean(p)
	if clean == "." || clean == "" {
		return "", errors.New("a source path names no file")
	}
	return clean, nil
}

// RenderLadoKnowledgeNote renders the note as LADO stores it. Empty commit,
// author and sources are left out.
func RenderLadoKnowledgeNote(n LadoKnowledgeNote) []byte {
	front := struct {
		Title    string                `yaml:"title"`
		Kind     string                `yaml:"kind"`
		Sources  []LadoKnowledgeSource `yaml:"sources,omitempty"`
		Commit   string                `yaml:"commit,omitempty"`
		Revision int                   `yaml:"revision"`
		Updated  string                `yaml:"updated,omitempty"`
		Author   string                `yaml:"author,omitempty"`
	}{Title: n.Title, Kind: string(n.Kind), Sources: n.Sources, Commit: n.Commit, Revision: n.Revision, Author: n.Author}
	if !n.Updated.IsZero() {
		front.Updated = n.Updated.UTC().Format(time.RFC3339)
	}
	data, err := yaml.Marshal(front)
	if err != nil {
		// The struct holds strings and ints only.
		panic(fmt.Sprintf("render knowledge note: %v", err))
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(data)
	b.WriteString("---\n\n")
	body := strings.TrimLeft(strings.ReplaceAll(n.Body, "\r\n", "\n"), "\n")
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// ladoSecretPatterns are common credential forms a note must not hold.
var ladoSecretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"github_token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"github_pat", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"gitlab_pat", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`)},
	{"anthropic_key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"aws_access_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"private_key", regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-{5}`)},
}

// LadoKnowledgeSecret names the first common credential form in content, or
// returns "" when it finds none. It catches common forms only.
func LadoKnowledgeSecret(content []byte) string {
	for _, p := range ladoSecretPatterns {
		if p.re.Match(content) {
			return p.name
		}
	}
	return ""
}

// LadoKnowledgeEntry is one note as the index lists it.
type LadoKnowledgeEntry struct {
	Slug      string             `json:"slug"`
	Title     string             `json:"title"`
	Kind      LadoKnowledgeKind  `json:"kind"`
	Revision  int                `json:"revision"`
	UpdatedAt time.Time          `json:"updatedAt"`
	Author    string             `json:"author"`
	State     LadoKnowledgeState `json:"state"`
}

// ladoKnowledgeKindHeadings head each group of the index; notes of no known
// kind (a file edited outside LADO) go last.
var ladoKnowledgeKindHeadings = map[LadoKnowledgeKind]string{
	LadoKnowledgeMap:        "코드 지도 (map)",
	LadoKnowledgeDecision:   "결정 (decision)",
	LadoKnowledgePitfall:    "함정·환경 (pitfall)",
	LadoKnowledgeConvention: "규칙 (convention)",
	LadoKnowledgeResearch:   "조사 (research)",
}

const ladoKnowledgeOtherHeading = "기타"

// ladoKnowledgeGroups orders entries by kind, then slug.
func ladoKnowledgeGroups(entries []LadoKnowledgeEntry) [][]LadoKnowledgeEntry {
	byKind := map[LadoKnowledgeKind][]LadoKnowledgeEntry{}
	var other []LadoKnowledgeEntry
	for _, e := range entries {
		if e.Kind.Valid() {
			byKind[e.Kind] = append(byKind[e.Kind], e)
		} else {
			other = append(other, e)
		}
	}
	var groups [][]LadoKnowledgeEntry
	for _, k := range LadoKnowledgeKinds {
		if len(byKind[k]) > 0 {
			groups = append(groups, byKind[k])
		}
	}
	if len(other) > 0 {
		groups = append(groups, other)
	}
	for _, g := range groups {
		slices.SortFunc(g, func(a, b LadoKnowledgeEntry) int { return strings.Compare(a.Slug, b.Slug) })
	}
	return groups
}

func ladoKnowledgeHeading(k LadoKnowledgeKind) string {
	if h, ok := ladoKnowledgeKindHeadings[k]; ok {
		return h
	}
	return ladoKnowledgeOtherHeading
}

// ladoKnowledgeLine is "- [[slug]] — title (rev N)". A title from a file
// edited outside LADO is put on one line and cut.
func ladoKnowledgeLine(e LadoKnowledgeEntry) string {
	title := strings.Join(strings.FieldsFunc(e.Title, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
	if r := []rune(title); len(r) > LadoKnowledgeTitleMaxRunes {
		title = string(r[:LadoKnowledgeTitleMaxRunes]) + "…"
	}
	if title == "" {
		title = e.Slug
	}
	return "- [[" + e.Slug + "]] — " + title + " (rev " + strconv.Itoa(e.Revision) + ")"
}

// RenderLadoKnowledgePromptIndex renders the index a session's standing
// instructions carry: notes grouped by kind, one line each, stale ones
// marked LadoKnowledgeStaleMark. It stops before
// LadoKnowledgePromptIndexMaxBytes, says how many notes it left out, and
// reports whether it did.
func RenderLadoKnowledgePromptIndex(entries []LadoKnowledgeEntry) (string, bool) {
	var b strings.Builder
	left := len(entries)
	truncated := false
	for _, g := range ladoKnowledgeGroups(entries) {
		heading := "### " + ladoKnowledgeHeading(g[0].Kind) + "\n"
		headed := false
		for _, e := range g {
			line := ladoKnowledgeLine(e)
			if e.State == LadoKnowledgeStale {
				line += LadoKnowledgeStaleMark
			}
			line += "\n"
			add := len(line)
			if !headed {
				add += len(heading)
				if b.Len() > 0 {
					add++
				}
			}
			// Keep room for the truncation line.
			if b.Len()+add > LadoKnowledgePromptIndexMaxBytes-256 {
				truncated = true
				break
			}
			if !headed {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(heading)
				headed = true
			}
			b.WriteString(line)
			left--
		}
		if truncated {
			break
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n- … 목차가 %d KiB 를 넘어 노트 %d개를 뺐다. 폴더의 index.md 와 notes/ 에서 전체를 본다.\n", LadoKnowledgePromptIndexMaxBytes>>10, left)
	}
	return b.String(), truncated
}

// LadoKnowledgeDiskIndexPreamble heads the index.md LADO keeps in the folder.
const LadoKnowledgeDiskIndexPreamble = "# 프로젝트 지식 목차\n\n" +
	"> LADO 가 만드는 파일이다. 고쳐도 다음 쓰기에서 다시 만들어진다.\n" +
	"> 신선도는 앱의 지식 카드와 세션 목차에서 본다.\n"

// RenderLadoKnowledgeDiskIndex renders the folder's index.md: the same list
// as the prompt index without freshness and without a cap, since the state
// it would show is only right when it is written.
func RenderLadoKnowledgeDiskIndex(entries []LadoKnowledgeEntry) []byte {
	var b strings.Builder
	b.WriteString(LadoKnowledgeDiskIndexPreamble)
	groups := ladoKnowledgeGroups(entries)
	if len(groups) == 0 {
		b.WriteString("\n노트가 아직 없다.\n")
	}
	for _, g := range groups {
		b.WriteString("\n## " + ladoKnowledgeHeading(g[0].Kind) + "\n\n")
		for _, e := range g {
			b.WriteString(ladoKnowledgeLine(e) + "\n")
		}
	}
	return []byte(b.String())
}
