package ladosetup

// LADO fork: the project's skills, agents and instruction files a LADO
// session's instructions list as the project's experts (#29,
// docs/LJY-PRD/HARNESS.md §9.4). They are read from the project's main
// checkout each time a session's instructions are built, like AO's
// agentRulesFile, so a skill the work changed reaches the next session. Only
// files the repository tracks are read: they are the ones every worktree has.

import (
	"context"
	"errors"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// referencesGitTimeout bounds the git call a session's instructions wait
// for. A slower repository starts the session without the list.
const referencesGitTimeout = 5 * time.Second

// referenceReadLimit is the most of a SKILL.md or an agent file read for its
// front matter. A larger file is listed without a description.
const referenceReadLimit = 64 << 10

// Lengths, in characters, of a reference's name and description.
const (
	referenceNameMax        = 64
	referenceDescriptionMax = 240
)

// ProjectReferences lists the skills, agents and instruction files the
// project's repository tracks, sorted by path, for a LADO session's
// instructions. It is nil for a project that is unknown, archived, a scratch
// or a workspace project. A git call that fails or gives no answer is an
// error; a file that cannot be read is listed by name alone.
func (s *Service) ProjectReferences(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoProjectReference, error) {
	if s.deps.Projects == nil || projectID == "" {
		return nil, nil
	}
	records, err := s.deps.Projects.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var project domain.ProjectRecord
	found := false
	for _, p := range records {
		if domain.ProjectID(p.ID) == projectID {
			project, found = p, true
			break
		}
	}
	if !found || !project.ArchivedAt.IsZero() {
		return nil, nil
	}
	if kind := project.Kind.WithDefault(); kind == domain.ProjectKindScratch || kind == domain.ProjectKindWorkspace {
		return nil, nil
	}
	if project.Path == "" {
		return nil, errors.New("the project has no folder")
	}
	tracked, err := trackedSetupFiles(ctx, project.Path, referencesGitTimeout)
	if err != nil {
		return nil, err
	}
	rulesFile := normalizeRepoPath(project.Config.AgentRulesFile)
	var refs []domain.LadoProjectReference
	for _, rel := range tracked {
		if !plainRepoPath(rel) {
			continue
		}
		ref, ok := referenceKind(rel)
		if !ok {
			continue
		}
		if ref.Kind == domain.LadoSetupKindInstructions {
			if rulesFile != "" && rel == rulesFile {
				// AO already puts the agent rules file's text into the
				// instructions.
				continue
			}
			ref.Name = rel
			refs = append(refs, ref)
			continue
		}
		ref.Name, ref.Description = readReference(filepath.Join(project.Path, filepath.FromSlash(rel)), ref.Name)
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return refs, nil
}

// referenceKind tells whether a tracked path is a skill, an agent or an
// instruction file a session can use, and which tool reads it by itself. Its
// Name is the fallback name: the skill's folder or the agent file's name.
func referenceKind(rel string) (domain.LadoProjectReference, bool) {
	ref := domain.LadoProjectReference{Path: rel}
	segments := strings.Split(rel, "/")
	switch {
	case len(segments) == 4 && segments[0] == ".claude" && segments[1] == "skills" && segments[3] == "SKILL.md" && segments[2] != "":
		ref.Kind, ref.Loader, ref.Name = domain.LadoSetupKindSkill, domain.LadoSetupToolClaudeCode, segments[2]
	case len(segments) == 4 && segments[0] == ".agents" && segments[1] == "skills" && segments[3] == "SKILL.md" && segments[2] != "":
		ref.Kind, ref.Loader, ref.Name = domain.LadoSetupKindSkill, domain.LadoSetupToolCodex, segments[2]
	case len(segments) == 3 && segments[0] == ".claude" && segments[1] == "agents" && strings.HasSuffix(segments[2], ".md") && segments[2] != ".md":
		ref.Kind, ref.Loader, ref.Name = domain.LadoSetupKindAgent, domain.LadoSetupToolClaudeCode, strings.TrimSuffix(segments[2], ".md")
	case path.Base(rel) == "CLAUDE.md":
		ref.Kind, ref.Loader = domain.LadoSetupKindInstructions, domain.LadoSetupToolClaudeCode
	case path.Base(rel) == "AGENTS.md":
		ref.Kind, ref.Loader = domain.LadoSetupKindInstructions, domain.LadoSetupToolCodex
	default:
		return ref, false
	}
	return ref, true
}

// plainRepoPath reports whether a path git listed can go into instructions
// as it is: valid UTF-8 without control characters such as a line break.
func plainRepoPath(rel string) bool {
	if !utf8.ValidString(rel) {
		return false
	}
	return strings.IndexFunc(rel, unicode.IsControl) < 0
}

// normalizeRepoPath is a project's agentRulesFile as git writes paths:
// slashes, Windows separators included, without a leading "./".
func normalizeRepoPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return path.Clean(strings.TrimPrefix(filepath.ToSlash(strings.ReplaceAll(v, `\`, `/`)), "./"))
}

// readReference reads a skill's or agent's name and description from its
// front matter. It does not follow a link and reads at most
// referenceReadLimit; a file it cannot read keeps fallback as its name and
// has no description.
func readReference(file, fallback string) (name, description string) {
	name = oneLine(fallback, referenceNameMax)
	f, err := openRegular(file, false)
	if err != nil {
		return name, ""
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, referenceReadLimit+1))
	if err != nil || len(content) > referenceReadLimit {
		return name, ""
	}
	front, _, ok := domain.SplitLadoFrontMatter(string(content))
	if !ok {
		return name, ""
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		return name, ""
	}
	if v, ok := fields["name"].(string); ok {
		if line := oneLine(v, referenceNameMax); line != "" {
			name = line
		}
	}
	text, _ := fields["description"].(string)
	if when, ok := fields["when_to_use"].(string); ok && strings.TrimSpace(when) != "" {
		text = strings.TrimSpace(text) + " Use when: " + when
	}
	return name, oneLine(text, referenceDescriptionMax)
}

// oneLine puts text on one line: every run of spaces, line breaks and
// control characters becomes one space, and text longer than limit
// characters is cut and ends with "…".
func oneLine(text string, limit int) string {
	text = strings.ToValidUTF8(text, " ")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimRight(string(runes[:limit-1]), " ") + "…"
}
