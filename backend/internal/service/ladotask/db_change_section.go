package ladotask

// LADO fork: the production database changes section of a LADO session's
// standing instructions (#97,
// docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). In a
// project where a repository has its database change settings turned on, the
// integration approval applies the pre-merge SQL and then merges, so an
// orchestrator neither asks about nor applies that SQL after a push, a worker
// adds SQL as new files and never applies it, and a plan worker plans the SQL
// files with db_schema. A project with the settings off gets no section, and
// its instructions stay as they were.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// dbChangeHeading heads the database changes section.
const dbChangeHeading = "## Production database changes (LADO)"

// dbChangeRole is what a session does with the project's SQL files.
type dbChangeRole int

const (
	dbChangeOrchestrator dbChangeRole = iota
	dbChangeWorker
	dbChangeDesigner
	dbChangeCritic
)

// dbChangeIntro opens the section for every role, before the list of paths.
const dbChangeIntro = "This project applies SQL files to its production database through LADO. " +
	"A PR/MR that adds SQL files under the paths below carries them in its LADO verification, so the developer's integration approval in the AO desktop app signs those files. " +
	"Once approved, LADO checks that the PR/MR can merge, applies its pre-merge SQL files one at a time in byte order of their paths, and merges only when every file applied. " +
	"A file whose first line is `" + domain.LadoDBChangeAfterDeployMarker + "` waits until after the deploy, for the developer's button in the app. " +
	"LADO records each file it applied and never runs a file again."

// dbChangeBullets are what the role does with the project's SQL files.
func dbChangeBullets(role dbChangeRole) []string {
	switch role {
	case dbChangeOrchestrator:
		return []string{
			"After a worker pushes SQL files under these paths, do not ask the developer whether to apply them to the production database, and do not apply them yourself: " +
				"the integration approval applies the SQL and merges the PR/MR together.",
			"If the developer asks you to apply one of these files, point them to the database changes of the task in the AO desktop app instead: " +
				"a file applied outside LADO is not recorded and would run again.",
			"Relay LADO's database change notices (a SQL file that failed, a run held because the PR/MR cannot merge, SQL waiting after the deploy, " +
				"a PR/MR merged outside LADO with its SQL not applied) and tell the developer to act on them in the app.",
		}
	case dbChangeDesigner:
		return []string{
			"When the work changes the production database, plan the SQL as new files under these paths: name them in affected_paths and add db_schema to change_kinds.",
			"Plan no step that applies SQL to a production database or asks someone to: the integration approval does it.",
			"Never plan to edit or delete a SQL file that is already on the base branch; plan a new file that corrects it.",
			"Plan SQL that cannot be undone, such as dropping a column the old code still reads, as a separate file whose first line is `" +
				domain.LadoDBChangeAfterDeployMarker + "`.",
		}
	case dbChangeCritic:
		return []string{
			"Check that a plan whose work changes the production database names its SQL files under these paths in affected_paths and db_schema in change_kinds.",
			"Flag a step that applies SQL to a production database or asks someone to, a step that edits or deletes a SQL file already on the base branch, " +
				"and SQL that cannot be undone without the `" + domain.LadoDBChangeAfterDeployMarker + "` first line.",
		}
	default:
		return []string{
			"Add each database change as a new SQL file under these paths. Do not apply it to any production database and do not ask anyone to: " +
				"LADO applies it after the developer's integration approval.",
			"Never edit or delete a SQL file that is already on the base branch: LADO may have applied it and does not run it again. Put a correction in a new file.",
			"Name the files so that they sort in the order they must run, such as `003_add_column.sql` after `002_create_table.sql`.",
			"Start a file that cannot be undone and must wait until the new code is deployed, such as dropping a column the old code still reads, with the line `" +
				domain.LadoDBChangeAfterDeployMarker + "` as its first line.",
		}
	}
}

// renderDBChangeSection renders the section for a role from the settings
// that are turned on.
func renderDBChangeSection(role dbChangeRole, settings []domain.LadoDBChangeSettings) string {
	var b strings.Builder
	b.WriteString(dbChangeHeading + "\n\n")
	b.WriteString(dbChangeIntro + "\n\n")
	b.WriteString("The SQL paths, as globs relative to the repository's root:\n")
	for _, s := range settings {
		repo := "the project's repository"
		if s.Repo != "" {
			repo = "repository " + dbChangeCode(s.Repo)
		}
		globs := make([]string, len(s.Paths))
		for i, glob := range s.Paths {
			globs[i] = dbChangeCode(glob)
		}
		b.WriteString("- " + repo + ": " + strings.Join(globs, ", ") + "\n")
	}
	b.WriteString("\n")
	for _, bullet := range dbChangeBullets(role) {
		b.WriteString("- " + bullet + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// dbChangeCode shows a path as inline code, or in double quotes when it holds
// a backtick, which would end the code.
func dbChangeCode(text string) string {
	if strings.Contains(text, "`") {
		return `"` + text + `"`
	}
	return "`" + text + "`"
}

// DBChangeSection returns the production database changes section of a
// session: "" without a settings source, outside LADO mode, for a session
// that is not an orchestrator or a worker, and when no repository of the
// project has its settings turned on. The section is guidance next to LADO's
// gates, which hold without it, so a failure to read the settings is logged
// and leaves the section out instead of failing the session's start.
func (p PromptSource) DBChangeSection(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID, sessionID domain.SessionID) string {
	if p.DBChanges == nil || projectID == "" || (kind != domain.KindOrchestrator && kind != domain.KindWorker) {
		return ""
	}
	log := p.Log
	if log == nil {
		log = slog.Default()
	}
	warn := func(err error) string {
		log.Warn("LADO left the production database changes out of a session's instructions", "project", projectID, "session", sessionID, "err", err)
		return ""
	}
	settings, err := p.Settings.LadoProjectSettings(ctx, projectID)
	if err != nil {
		return warn(err)
	}
	if !settings.LadoMode {
		return ""
	}
	all, err := p.DBChanges.LadoDBChangeSettings(ctx, projectID)
	if err != nil {
		return warn(err)
	}
	var enabled []domain.LadoDBChangeSettings
	for _, s := range all {
		if s.Enabled && len(s.Paths) > 0 {
			enabled = append(enabled, s)
		}
	}
	if len(enabled) == 0 {
		return ""
	}
	role := dbChangeOrchestrator
	if kind == domain.KindWorker {
		role = dbChangeWorker
		rec, found, err := p.Sessions.GetSession(ctx, sessionID)
		if err != nil {
			return warn(err)
		}
		if _, planRole, _, ok := domain.ParseLadoPlanWorkerClientRequestID(rec.ClientRequestID); found && ok {
			role = dbChangeDesigner
			if planRole == domain.LadoPlanRoleCritic {
				role = dbChangeCritic
			}
		}
	}
	return renderDBChangeSection(role, enabled)
}
