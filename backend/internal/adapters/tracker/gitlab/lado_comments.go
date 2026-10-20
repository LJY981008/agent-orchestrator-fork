package gitlab

// LADO fork: issue comments for plan requests (docs/LJY-PRD/TDD.md §5.1). An
// issue may describe the work in its description while the plan sits in a
// comment, so a plan request pins the issue's trusted comments too. Anyone
// can comment on a public project, so each comment says whether its author is
// a member with at least the Developer role.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/httpkit"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// ladoNotePageSize is the largest page GitLab serves.
	ladoNotePageSize = 100
	// ladoNoteMaxPages caps the pages of notes one call reads.
	ladoNoteMaxPages = 10
)

// glLadoNote is the part of an issue note LADO reads. GitLab writes system
// notes itself, for changes such as a new label; people write the others.
type glLadoNote struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	System    bool      `json:"system"`
	CreatedAt time.Time `json:"created_at"`
	Author    struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"author"`
}

// IssueComments lists the people's comments on an issue, oldest first, with
// whether each author is a member of the project with at least the Developer
// role. System notes are not comments and are left out. It reads the newest
// ladoNoteMaxPages pages of notes, since a plan written in a comment is
// usually among the newest.
func (w *LadoIssues) IssueComments(ctx context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error) {
	projectPath, host, err := w.target(domain.LadoIssueRepo(id))
	if err != nil {
		return nil, err
	}
	iid := domain.LadoIssueNumber(id)
	if iid <= 0 {
		return nil, fmt.Errorf("%w: no iid in %q", ErrBadID, id.Native)
	}
	notes, err := w.issueNotes(ctx, host, projectPath, iid)
	if err != nil {
		return nil, err
	}
	slices.Reverse(notes)
	// The notes API gives no web address, so each comment links to its note
	// on the issue's page.
	issueURL := fmt.Sprintf("%s/%s/-/issues/%d", ladoWebBase(host.baseURL), projectPath, iid)
	developers := map[int64]bool{}
	comments := make([]domain.LadoIssueComment, 0, len(notes))
	for _, note := range notes {
		trusted, known := developers[note.Author.ID]
		if !known {
			if trusted, err = w.developer(ctx, host, projectPath, note.Author.ID); err != nil {
				return nil, err
			}
			developers[note.Author.ID] = trusted
		}
		noteID := strconv.FormatInt(note.ID, 10)
		comments = append(comments, domain.LadoIssueComment{
			ID:            noteID,
			Author:        note.Author.Username,
			AuthorTrusted: trusted,
			CreatedAt:     note.CreatedAt.UTC(),
			Body:          note.Body,
			URL:           issueURL + "#note_" + noteID,
		})
	}
	return comments, nil
}

// issueNotes reads an issue's notes newest first, leaving out system notes. A
// note that slides onto the next page while LADO reads, because someone
// commented meanwhile, is kept once.
func (w *LadoIssues) issueNotes(ctx context.Context, host hostEntry, projectPath string, iid int) ([]glLadoNote, error) {
	first := fmt.Sprintf("%s/issues/%d/notes?sort=desc&order_by=created_at&per_page=%d", ladoProjectPath(projectPath), iid, ladoNotePageSize)
	var notes []glLadoNote
	seen := map[int64]bool{}
	path := first
	for pages := 0; path != "" && pages < ladoNoteMaxPages; pages++ {
		answer, err := w.send(ctx, host, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if !answer.ok() {
			return nil, answer.err()
		}
		var page []glLadoNote
		if err := json.Unmarshal(answer.body, &page); err != nil {
			return nil, fmt.Errorf("gitlab tracker: decode comments: %w", err)
		}
		for _, note := range page {
			if !note.System && !seen[note.ID] {
				seen[note.ID] = true
				notes = append(notes, note)
			}
		}
		path = ladoNextPage(answer.header, host.baseURL, first)
	}
	return notes, nil
}

// ladoNextPage returns the path of the page after an answer, or "" after the
// last page. GitLab names it in the Link header and, by number, in
// X-Next-Page, which LADO reads when Link is missing. The path is relative to
// the host's API base and send puts that base back in front, so a Link naming
// another host cannot move the next request there.
func ladoNextPage(header http.Header, apiBase, first string) string {
	if link := header.Get("Link"); link != "" {
		return httpkit.ParseLinkNext(link, apiBase)
	}
	if page, err := strconv.Atoi(header.Get("X-Next-Page")); err == nil && page > 0 {
		return first + "&page=" + strconv.Itoa(page)
	}
	return ""
}

// developer reports whether a user is a member of the project, directly or
// through its groups, with at least the Developer role. A note without an
// author is never trusted.
func (w *LadoIssues) developer(ctx context.Context, host hostEntry, projectPath string, userID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	answer, err := w.send(ctx, host, http.MethodGet, fmt.Sprintf("%s/members/all/%d", ladoProjectPath(projectPath), userID), nil)
	if err != nil {
		return false, err
	}
	if answer.status == http.StatusNotFound {
		return false, nil
	}
	if !answer.ok() {
		return false, answer.err()
	}
	var member struct {
		AccessLevel int `json:"access_level"`
	}
	if err := json.Unmarshal(answer.body, &member); err != nil {
		return false, fmt.Errorf("gitlab tracker: decode member: %w", err)
	}
	return member.AccessLevel >= ladoDeveloperAccess, nil
}

// ladoWebBase returns the web address of the GitLab whose API base is
// apiBase, such as https://gitlab.com for https://gitlab.com/api/v4.
func ladoWebBase(apiBase string) string {
	return strings.TrimSuffix(strings.TrimRight(apiBase, "/"), "/api/v4")
}
