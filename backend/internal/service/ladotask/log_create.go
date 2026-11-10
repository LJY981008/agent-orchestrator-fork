package ladotask

// LADO fork: tasks the production log watch creates (issue #87). The log
// watch (service/ladologwatch) writes the issue's title and body itself, from
// a redacted, fenced log excerpt, and creates the task through the same path
// as the New Task composer, so the marker, the lost-answer search and the
// pending-task cleanup work alike.

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LogCreateInput is an issue about a production incident, as the log watch
// wrote it.
type LogCreateInput struct {
	ProjectID domain.ProjectID
	// Repository is the canonical id of the repository the log watch settings
	// name. It may be empty when the project has one repository.
	Repository string
	Title      string
	Body       string
}

// CreateFromLog creates the issue and the task of a production incident:
// a task with Source domain.LadoTaskSourceLog. The title is cut to 256
// characters and the body to 65,000, where Create would refuse them, because
// no developer is there to shorten them. It fails like Create, with one
// difference: when the issue may exist while the task stays pending (LADO
// cannot confirm whether the tracker created the issue,
// LADO_ISSUE_CREATE_UNCONFIRMED, or could not save the created issue on the
// task, LADO_TASK_LINK_FAILED), it also returns the pending task, without an
// issue ref, so the log watch can find it again by its id and does not create
// a second issue.
func (s *Service) CreateFromLog(ctx context.Context, in LogCreateInput) (domain.LadoTask, error) {
	title := logCut(strings.Join(strings.Fields(in.Title), " "), maxTitleRunes)
	if title == "" {
		title = "운영 오류"
	}
	return s.create(ctx, newTask{
		ProjectID:  in.ProjectID,
		Repository: in.Repository,
		Title:      title,
		Body:       logCut(strings.TrimSpace(in.Body), maxBodyRunes),
		Source:     domain.LadoTaskSourceLog,
	})
}

// LogRepositories lists the repositories the log watch of a project may
// create its issues in: the project's repositories while it is in LADO mode.
// It fails with LADO_MODE_OFF outside LADO mode, and with
// LADO_REPOSITORY_NOT_CONNECTED when the project has none.
func (s *Service) LogRepositories(ctx context.Context, projectID domain.ProjectID) ([]ProjectRepo, error) {
	return s.ladoRepos(ctx, projectID)
}

// logCut cuts s to at most limit runes.
func logCut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}
