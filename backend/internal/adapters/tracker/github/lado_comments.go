package github

// LADO fork: issue comments for plan requests (docs/LJY-PRD/TDD.md §5.1). An
// issue may describe the work in its body while the plan sits in a comment,
// so a plan request pins the issue's trusted comments too. Anyone can comment
// on a public repository, so each comment says whether its author can work on
// the repository.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/httpkit"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// ladoCommentPageSize is the largest page GitHub serves.
	ladoCommentPageSize = 100
	// ladoCommentMaxPages caps the pages of comments one call keeps.
	ladoCommentMaxPages = 10
)

// ghLadoComment is the part of an issue comment LADO reads. User is null when
// the author deleted their account.
type ghLadoComment struct {
	ID                int64     `json:"id"`
	User              *ghUser   `json:"user"`
	AuthorAssociation string    `json:"author_association"`
	Body              string    `json:"body"`
	CreatedAt         time.Time `json:"created_at"`
	HTMLURL           string    `json:"html_url"`
}

// IssueComments lists the people's comments on an issue, oldest first, with
// whether each author can work on the repository: its owner, a member of its
// organization or a collaborator. GitHub lists comments oldest first and
// cannot sort them otherwise, so when an issue has more than
// ladoCommentMaxPages pages of comments, it reads the last
// ladoCommentMaxPages, since a plan written in a comment is usually among the
// newest.
func (w *LadoIssues) IssueComments(ctx context.Context, id domain.TrackerID) ([]domain.LadoIssueComment, error) {
	owner, name, err := ladoRepoParts(domain.LadoIssueRepo(id))
	if err != nil {
		return nil, err
	}
	number := domain.LadoIssueNumber(id)
	if number <= 0 {
		return nil, fmt.Errorf("%w: no issue number in %q", ErrBadID, id.Native)
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=%d", owner, name, number, ladoCommentPageSize)
	page, link, err := w.commentPage(ctx, path)
	if err != nil {
		return nil, err
	}
	if last := ladoLastPage(link); last > ladoCommentMaxPages {
		page, link, err = w.commentPage(ctx, fmt.Sprintf("%s&page=%d", path, last-ladoCommentMaxPages+1))
		if err != nil {
			return nil, err
		}
	}
	comments := make([]domain.LadoIssueComment, 0, len(page))
	for pages := 1; ; pages++ {
		for _, raw := range page {
			comments = append(comments, ladoCommentFromGH(raw))
		}
		next := httpkit.ParseLinkNext(link, w.t.baseURL)
		if next == "" || pages == ladoCommentMaxPages {
			return comments, nil
		}
		if page, link, err = w.commentPage(ctx, next); err != nil {
			return nil, err
		}
	}
}

// commentPage reads one page of comments and its Link header.
func (w *LadoIssues) commentPage(ctx context.Context, path string) ([]ghLadoComment, string, error) {
	answer, err := w.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, "", err
	}
	if !answer.ok() {
		return nil, "", answer.err()
	}
	var page []ghLadoComment
	if err := json.Unmarshal(answer.body, &page); err != nil {
		return nil, "", fmt.Errorf("github tracker: decode comments: %w", err)
	}
	return page, answer.header.Get("Link"), nil
}

// ladoCommentFromGH maps a GitHub comment onto LADO's. A deleted account
// shows as GitHub's ghost user, whom LADO never trusts.
func ladoCommentFromGH(raw ghLadoComment) domain.LadoIssueComment {
	author, trusted := "ghost", false
	if raw.User != nil {
		author = raw.User.Login
		switch raw.AuthorAssociation {
		case "OWNER", "MEMBER", "COLLABORATOR":
			trusted = true
		}
	}
	return domain.LadoIssueComment{
		ID:            strconv.FormatInt(raw.ID, 10),
		Author:        author,
		AuthorTrusted: trusted,
		CreatedAt:     raw.CreatedAt.UTC(),
		Body:          raw.Body,
		URL:           raw.HTMLURL,
	}
}

// ladoLastPage returns the page number a Link header's rel="last" names, or 0
// when it names none. GitHub sends rel="last" on every page but the last.
func ladoLastPage(link string) int {
	for _, part := range strings.Split(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ">")
		if !ok || !strings.HasPrefix(target, "<") || !ladoLinkRel(params, "last") {
			continue
		}
		u, err := url.Parse(target[1:])
		if err != nil {
			return 0
		}
		page, err := strconv.Atoi(u.Query().Get("page"))
		if err != nil {
			return 0
		}
		return page
	}
	return 0
}

// ladoLinkRel reports whether a Link entry's parameters include rel.
func ladoLinkRel(params, rel string) bool {
	for _, param := range strings.Split(params, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(name, "rel") {
			continue
		}
		for _, v := range strings.Fields(strings.Trim(value, `"`)) {
			if strings.EqualFold(v, rel) {
				return true
			}
		}
	}
	return false
}
