package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMReviewPoster = (*Provider)(nil)

// mrVersion is one entry of GET /merge_requests/:iid/versions.
type mrVersion struct {
	HeadCommitSHA  string `json:"head_commit_sha"`
	BaseCommitSHA  string `json:"base_commit_sha"`
	StartCommitSHA string `json:"start_commit_sha"`
}

// discussionPosition anchors a discussion on a line of the new file version.
type discussionPosition struct {
	PositionType string `json:"position_type"`
	BaseSHA      string `json:"base_sha"`
	StartSHA     string `json:"start_sha"`
	HeadSHA      string `json:"head_sha"`
	OldPath      string `json:"old_path"`
	NewPath      string `json:"new_path"`
	NewLine      int    `json:"new_line"`
}

type discussionRequest struct {
	Body     string              `json:"body"`
	Position *discussionPosition `json:"position,omitempty"`
}

type noteRequest struct {
	Body string `json:"body"`
}

// PostReview posts an AO review on a GitLab merge request. The summary becomes
// a plain note for people to read. Each comment becomes a resolvable
// discussion, so the SCM observer forwards it to the worker as an unresolved
// review thread.
//
// A comment is anchored on the merge request version whose head is the
// reviewed commit. When no version matches (the branch moved on during the
// review) or GitLab rejects the position, the comment is posted without a
// position and its "path:line" goes in front of the text, so it is never lost.
// Every comment is attempted; the returned error joins the failures.
func (p *Provider) PostReview(ctx context.Context, post ports.SCMReviewPost) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("gitlab scm: review poster is not configured")
	}
	repo := post.PR.Repo
	if post.PR.Number <= 0 || strings.TrimSpace(repo.Owner) == "" || strings.TrimSpace(repo.Name) == "" {
		return fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	client, err := p.clientForRepoErr(repo)
	if err != nil {
		return err
	}
	mrPath := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(repo.Owner, repo.Name), post.PR.Number)

	var errs []error
	if summary := strings.TrimSpace(post.Summary); summary != "" {
		if resp, err := client.doPOSTJSON(ctx, mrPath+"/notes", noteRequest{Body: summary}); err != nil {
			if resp.StatusCode == http.StatusNotFound {
				return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
			}
			errs = append(errs, fmt.Errorf("gitlab scm: post review summary: %w", err))
		}
	}
	if len(post.Comments) == 0 {
		return errors.Join(errs...)
	}

	version, found, err := reviewedVersion(ctx, client, mrPath, post.TargetSHA)
	if err != nil {
		if errors.Is(err, ports.ErrSCMNotFound) {
			return errors.Join(append(errs, err)...)
		}
		// The versions list is only for positions; the comments still go up
		// without them.
		found = false
	}
	for i, comment := range post.Comments {
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		if found && strings.TrimSpace(comment.Path) != "" && comment.Line > 0 {
			path := strings.TrimSpace(comment.Path)
			resp, err := client.doPOSTJSON(ctx, mrPath+"/discussions", discussionRequest{
				Body: body,
				Position: &discussionPosition{
					PositionType: "text",
					BaseSHA:      version.BaseCommitSHA,
					StartSHA:     version.StartCommitSHA,
					HeadSHA:      version.HeadCommitSHA,
					OldPath:      path,
					NewPath:      path,
					NewLine:      comment.Line,
				},
			})
			if err == nil {
				continue
			}
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
				errs = append(errs, fmt.Errorf("gitlab scm: post review comment %d: %w", i+1, err))
				continue
			}
			// GitLab rejected the position (the line is not in the diff, a
			// renamed file, ...): fall through and post it without one.
		}
		if _, err := client.doPOSTJSON(ctx, mrPath+"/discussions", discussionRequest{Body: unpositionedBody(comment.Path, comment.Line, body)}); err != nil {
			errs = append(errs, fmt.Errorf("gitlab scm: post review comment %d: %w", i+1, err))
		}
	}
	return errors.Join(errs...)
}

// reviewedVersion finds the merge request version whose head is targetSHA.
func reviewedVersion(ctx context.Context, client *Client, mrPath, targetSHA string) (mrVersion, bool, error) {
	targetSHA = strings.TrimSpace(targetSHA)
	if targetSHA == "" {
		return mrVersion{}, false, nil
	}
	resp, err := client.doGET(ctx, mrPath+"/versions", nil)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return mrVersion{}, false, fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
		}
		return mrVersion{}, false, err
	}
	var versions []mrVersion
	if err := json.Unmarshal(resp.Body, &versions); err != nil {
		return mrVersion{}, false, fmt.Errorf("gitlab scm: decode merge request versions: %w", err)
	}
	for _, v := range versions {
		if strings.EqualFold(strings.TrimSpace(v.HeadCommitSHA), targetSHA) && v.BaseCommitSHA != "" && v.StartCommitSHA != "" {
			return v, true, nil
		}
	}
	return mrVersion{}, false, nil
}

// unpositionedBody keeps a comment's location readable when it cannot be
// anchored on the diff.
func unpositionedBody(path string, line int, body string) string {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return body
	case line > 0:
		return fmt.Sprintf("`%s:%d`\n\n%s", path, line, body)
	default:
		return fmt.Sprintf("`%s`\n\n%s", path, body)
	}
}

// doPOSTJSON performs a POST with a JSON body. Like doMERGE it bypasses the
// ETag cache and returns the status code for caller-side interpretation.
func (c *Client) doPOSTJSON(ctx context.Context, path string, body any) (RESTResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: encode %s body: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.restURL(path, nil), bytes.NewReader(b))
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := readResponseBody(resp, path)
	if err != nil {
		return RESTResponse{StatusCode: resp.StatusCode}, err
	}
	if resp.StatusCode >= 400 {
		return RESTResponse{StatusCode: resp.StatusCode, Body: rb}, classifyError(resp, rb)
	}
	return RESTResponse{StatusCode: resp.StatusCode, Body: rb}, nil
}
