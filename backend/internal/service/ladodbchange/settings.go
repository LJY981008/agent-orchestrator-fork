package ladodbchange

// Reading and changing a project's database change settings. A change needs
// the desktop app's presence (project.db_changes, subject
// "<project>:<repo>") and the hash of the settings it started from. Turning
// the settings on, or changing the paths while they are on, records the
// default branch's matching files as applied (the baseline) in the same
// transaction as the settings; without that baseline LADO would run SQL that
// is in the database already, so a change whose baseline LADO cannot record
// is refused.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// baselineRemote is the remote whose default branch the baseline reads.
const baselineRemote = "origin"

// repoPlace is a repository of a project that may have settings: "" and the
// project's folder for a single-repository project, else each child
// repository of the workspace by its relative path.
type repoPlace struct {
	repo, name, dir string
	// configured is the base branch the project configures for it, if any.
	configured string
}

// project reads a project that is not archived.
func (s *Service) project(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, error) {
	project, ok, err := s.deps.Store.GetProject(ctx, string(id))
	if err != nil {
		s.deps.Log.Error("LADO could not read a project for its database change settings", "project", id, "err", err)
		return domain.ProjectRecord{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return project, nil
}

// places lists the repositories of a project that may have settings, in the
// order the workspace lists them.
func (s *Service) places(ctx context.Context, project domain.ProjectRecord) ([]repoPlace, error) {
	configured := project.Config.DefaultBranch
	if configured == domain.DefaultBranchAuto {
		configured = ""
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		name := project.DisplayName
		if name == "" {
			name = filepath.Base(project.Path)
		}
		return []repoPlace{{repo: "", name: name, dir: project.Path, configured: configured}}, nil
	}
	children, err := s.deps.Store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("read the workspace repositories of project %s: %w", project.ID, err)
	}
	places := make([]repoPlace, 0, len(children))
	for _, child := range children {
		branch := child.DefaultBranch
		if branch == "" || branch == domain.DefaultBranchAuto {
			branch = configured
		}
		repo := filepath.ToSlash(child.RelativePath)
		name := child.Name
		if name == "" {
			name = repo
		}
		places = append(places, repoPlace{
			repo: repo, name: name, dir: filepath.Join(project.Path, filepath.FromSlash(child.RelativePath)), configured: branch,
		})
	}
	return places, nil
}

// RepoDir returns the local folder of a project's repository: the project's
// folder for "", else the workspace's child repository at that relative
// path.
func (s *Service) RepoDir(ctx context.Context, projectID domain.ProjectID, repo string) (string, error) {
	project, ok, err := s.deps.Store.GetProject(ctx, string(projectID))
	switch {
	case err != nil:
		return "", fmt.Errorf("read project %s: %w", projectID, err)
	case !ok:
		return "", fmt.Errorf("project %s not found", projectID)
	}
	places, err := s.places(ctx, project)
	if err != nil {
		return "", err
	}
	for _, place := range places {
		if place.repo == repo {
			return place.dir, nil
		}
	}
	return "", fmt.Errorf("project %s has no repository %q", projectID, repo)
}

// DBChangeSettings returns a project's settings: one entry per repository
// that may have them, with the names of its stored secrets, and the entries'
// hash. Stored settings of a repository the workspace no longer lists come
// last, so they can still be turned off.
func (s *Service) DBChangeSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoDBChangeSettingsView, error) {
	project, err := s.project(ctx, projectID)
	if err != nil {
		return domain.LadoDBChangeSettingsView{}, err
	}
	return s.view(ctx, project)
}

// view builds a project's settings view.
func (s *Service) view(ctx context.Context, project domain.ProjectRecord) (domain.LadoDBChangeSettingsView, error) {
	projectID := domain.ProjectID(project.ID)
	places, err := s.places(ctx, project)
	if err != nil {
		s.deps.Log.Error("LADO could not list a project's repositories", "project", projectID, "err", err)
		return domain.LadoDBChangeSettingsView{}, loadFailed()
	}
	stored, err := s.deps.Store.LadoDBChangeSettings(ctx, projectID)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change settings", "project", projectID, "err", err)
		return domain.LadoDBChangeSettingsView{}, loadFailed()
	}
	byRepo := map[string]domain.LadoDBChangeSettings{}
	for _, settings := range stored {
		byRepo[settings.Repo] = settings
	}
	view := domain.LadoDBChangeSettingsView{Repositories: []domain.LadoDBChangeSettingsRepo{}}
	add := func(repo, name string) error {
		settings, ok := byRepo[repo]
		if !ok {
			settings = domain.LadoDBChangeSettings{ProjectID: projectID, Repo: repo}
		}
		delete(byRepo, repo)
		names, err := s.storedSecretNames(projectID, repo)
		if err != nil {
			return err
		}
		view.Repositories = append(view.Repositories, domain.LadoDBChangeSettingsRepo{
			LadoDBChangeSettings: settings, Name: name, StoredSecretNames: names,
		})
		return nil
	}
	for _, place := range places {
		if err := add(place.repo, place.name); err != nil {
			s.deps.Log.Error("LADO could not read database change secrets", "project", projectID, "repo", place.repo, "err", err)
			return domain.LadoDBChangeSettingsView{}, loadFailed()
		}
	}
	for _, settings := range stored {
		if _, left := byRepo[settings.Repo]; left {
			if err := add(settings.Repo, settings.Repo); err != nil {
				s.deps.Log.Error("LADO could not read database change secrets", "project", projectID, "repo", settings.Repo, "err", err)
				return domain.LadoDBChangeSettingsView{}, loadFailed()
			}
		}
	}
	list := make([]domain.LadoDBChangeSettings, len(view.Repositories))
	for i, repo := range view.Repositories {
		list[i] = repo.LadoDBChangeSettings
	}
	view.SHA256 = domain.LadoDBChangeSettingsListSHA256(list)
	return view, nil
}

// Enabled returns a repository's settings when they are turned on with at
// least one path, false otherwise.
func (s *Service) Enabled(ctx context.Context, projectID domain.ProjectID, repo string) (domain.LadoDBChangeSettings, bool, error) {
	settings, ok, err := s.deps.Store.LadoDBChangeSetting(ctx, projectID, repo)
	if err != nil {
		return domain.LadoDBChangeSettings{}, false, fmt.Errorf("read the database change settings of %s %q: %w", projectID, repo, err)
	}
	if !ok || !settings.Enabled || len(settings.Paths) == 0 {
		return domain.LadoDBChangeSettings{}, false, nil
	}
	return settings, true, nil
}

// UpdateDBChangeSettings changes one repository's settings and secrets. It
// checks the change, refuses it once the settings moved past u.Base, asks
// for presence, records the baseline when the settings turn on or their
// paths change while on, saves the settings with the proof, the audit and
// the baseline, and then writes the secrets file. A change that changes
// nothing returns the view without asking for presence.
func (s *Service) UpdateDBChangeSettings(ctx context.Context, projectID domain.ProjectID, u domain.LadoDBChangeSettingsUpdate, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeSettingsView, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	project, err := s.project(ctx, projectID)
	if err != nil {
		return domain.LadoDBChangeSettingsView{}, err
	}
	places, err := s.places(ctx, project)
	if err != nil {
		s.deps.Log.Error("LADO could not list a project's repositories", "project", projectID, "err", err)
		return domain.LadoDBChangeSettingsView{}, loadFailed()
	}
	current, exists, err := s.deps.Store.LadoDBChangeSetting(ctx, projectID, u.Repo)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change settings", "project", projectID, "repo", u.Repo, "err", err)
		return domain.LadoDBChangeSettingsView{}, loadFailed()
	}
	i := slices.IndexFunc(places, func(p repoPlace) bool { return p.repo == u.Repo })
	if i < 0 && !exists {
		return domain.LadoDBChangeSettingsView{}, invalid([]domain.LadoDBChangeProblem{{Field: "repo", Reason: domain.LadoDBChangeProblemInvalid}})
	}
	var place repoPlace
	if i >= 0 {
		place = places[i]
	}

	next := u.Settings(projectID)
	next.Command = strings.TrimSpace(next.Command)
	next.Paths, next.SecretNames = orEmpty(next.Paths), orEmpty(next.SecretNames)
	problems := domain.ValidateLadoDBChangeSettings(next)
	if i < 0 && next.Enabled {
		// A repository the workspace no longer lists can only be turned off.
		problems = append(problems, domain.LadoDBChangeProblem{Field: "repo", Reason: domain.LadoDBChangeProblemInvalid})
	}
	stored, err := s.Secrets(projectID, u.Repo)
	if err != nil {
		s.deps.Log.Error("LADO could not read database change secrets", "project", projectID, "repo", u.Repo, "err", err)
		return domain.LadoDBChangeSettingsView{}, loadFailed()
	}
	secrets, secretProblems := planSecrets(stored, next.SecretNames, u.Secrets)
	if problems = append(problems, secretProblems...); len(problems) > 0 {
		return domain.LadoDBChangeSettingsView{}, invalid(problems)
	}

	base := ""
	if exists {
		base = current.SHA256()
	}
	if !strings.EqualFold(strings.TrimSpace(u.Base), base) {
		return domain.LadoDBChangeSettingsView{}, apierr.Conflict("LADO_DB_CHANGE_SETTINGS_CHANGED",
			"The repository's database change settings changed since you opened them. Review them, then change them again.",
			map[string]any{"repo": u.Repo, "sha256": base})
	}
	next.BaselineCommit, next.EnabledAt = current.BaselineCommit, current.EnabledAt
	rebaseline := next.Enabled && (!exists || !current.Enabled || !slices.Equal(current.Paths, next.Paths))
	if exists && !rebaseline && next.SHA256() == current.SHA256() && !secrets.changed() {
		return s.view(ctx, project)
	}

	proof, err := s.presence(ctx, presence, domain.LadoPresenceDBChanges, domain.LadoDBChangeSettingsSubject(projectID, u.Repo),
		"Change the database change settings in the AO desktop app. It asks you to confirm, then sends the change itself.")
	if err != nil {
		return domain.LadoDBChangeSettingsView{}, err
	}
	now := s.now()
	var baseline []domain.LadoDBChangeApplication
	if rebaseline {
		commit, rows, err := s.baseline(ctx, place, next, now)
		if err != nil {
			reason := RedactLine(err.Error(), nil)
			s.deps.Log.Warn("LADO could not record the database change baseline", "project", projectID, "repo", u.Repo, "err", reason)
			return domain.LadoDBChangeSettingsView{}, apierr.Conflict("LADO_DB_CHANGE_BASELINE_FAILED",
				"LADO could not read the default branch to record its SQL files as applied, so the settings were not saved: "+reason,
				map[string]any{"repo": u.Repo, "reason": reason})
		}
		next.BaselineCommit, baseline = commit, rows
	}
	if next.Enabled && (!exists || !current.Enabled) {
		next.EnabledAt = now
	}
	next.UpdatedAt = now

	audit := s.settingsAudit(proof, current, exists, next, secrets, rebaseline, baseline, now)
	switch err := s.deps.Store.SetLadoDBChangeSettings(ctx, next, base, baseline, &proof, audit); {
	case errors.Is(err, domain.ErrLadoDBChangeSettingsChanged):
		latest, _, _ := s.deps.Store.LadoDBChangeSetting(ctx, projectID, u.Repo)
		return domain.LadoDBChangeSettingsView{}, apierr.Conflict("LADO_DB_CHANGE_SETTINGS_CHANGED",
			"The repository's database change settings changed since you opened them. Review them, then change them again.",
			map[string]any{"repo": u.Repo, "sha256": latest.SHA256()})
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return domain.LadoDBChangeSettingsView{}, ladopresence.Replayed()
	case err != nil:
		s.deps.Log.Error("LADO could not save database change settings", "project", projectID, "repo", u.Repo, "err", err)
		return domain.LadoDBChangeSettingsView{}, apierr.Internal("LADO_DB_CHANGE_UPDATE_FAILED", "Failed to save the database change settings")
	}
	if secrets.changed() {
		s.secretsMu.Lock()
		err := s.writeSecrets(projectID, u.Repo, secrets.next)
		s.secretsMu.Unlock()
		if err != nil {
			s.deps.Log.Error("LADO saved database change settings but could not write their secrets", "project", projectID, "repo", u.Repo, "err", err)
			return domain.LadoDBChangeSettingsView{}, apierr.Internal("LADO_DB_CHANGE_SECRETS_FAILED",
				"The settings were saved, but LADO could not store the secrets. Enter them again.")
		}
	}
	return s.view(ctx, project)
}

// presence asks for the desktop app's presence and refuses a proof used
// before, so LADO reads and fetches nothing for a replayed one.
func (s *Service) presence(ctx context.Context, presence func(action, subject string) (domain.LadoPresenceProof, error), action, subject, hint string) (domain.LadoPresenceProof, error) {
	if presence == nil {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED", hint)
	}
	proof, err := presence(action, subject)
	if err != nil {
		return domain.LadoPresenceProof{}, err
	}
	used, err := s.deps.Store.LadoPresenceProofUsed(ctx, proof.Nonce)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a presence proof", "err", err)
		return domain.LadoPresenceProof{}, apierr.Internal("LADO_DB_CHANGE_UPDATE_FAILED", "Failed to check the confirmation")
	case used:
		return domain.LadoPresenceProof{}, ladopresence.Replayed()
	}
	return proof, nil
}

// baseline fetches the repository's default branch and returns its commit
// and a baseline row for each matching file without an applied or baseline
// row yet. The default branch is the one refs/remotes/origin/HEAD names,
// else the one the project configures for the repository.
func (s *Service) baseline(ctx context.Context, place repoPlace, settings domain.LadoDBChangeSettings, now time.Time) (string, []domain.LadoDBChangeApplication, error) {
	if place.dir == "" {
		return "", nil, errors.New("the repository has no local folder")
	}
	branch, err := s.deps.Git.RemoteHead(ctx, place.dir, baselineRemote)
	switch {
	case errors.Is(err, ErrRefNotFound):
		branch = place.configured
	case err != nil:
		return "", nil, err
	}
	if branch == "" {
		return "", nil, errors.New("the default branch is unknown: refs/remotes/origin/HEAD is missing and the project configures no base branch")
	}
	if err := s.deps.Git.Fetch(ctx, place.dir, baselineRemote, branch); err != nil {
		return "", nil, err
	}
	commit, err := s.deps.Git.RevParse(ctx, place.dir, "refs/remotes/"+baselineRemote+"/"+branch)
	if err != nil {
		return "", nil, err
	}
	entries, err := s.deps.Git.Tree(ctx, place.dir, commit)
	if err != nil {
		return "", nil, err
	}
	var matched []TreeEntry
	for _, entry := range entries {
		if regularFile(entry.Mode) && matchesAny(settings.Paths, entry.Path) {
			matched = append(matched, entry)
		}
	}
	slices.SortFunc(matched, func(a, b TreeEntry) int { return strings.Compare(a.Path, b.Path) })
	paths := make([]string, len(matched))
	for i, entry := range matched {
		paths[i] = entry.Path
	}
	applied := map[string]domain.LadoDBChangeApplication{}
	if len(paths) > 0 {
		if applied, err = s.deps.Store.LadoDBChangeAppliedPaths(ctx, settings.ProjectID, settings.Repo, paths); err != nil {
			return "", nil, fmt.Errorf("read the applied files: %w", err)
		}
	}
	var rows []domain.LadoDBChangeApplication
	for _, entry := range matched {
		if _, ok := applied[entry.Path]; ok {
			continue
		}
		content, _, err := s.deps.Git.Blob(ctx, place.dir, entry.Blob, BlobReadMax)
		if err != nil {
			return "", nil, err
		}
		finished := now
		rows = append(rows, domain.LadoDBChangeApplication{
			ID: s.deps.NewID(), ProjectID: settings.ProjectID, Repo: settings.Repo, Path: entry.Path, BlobSHA: entry.Blob,
			Phase: domain.LadoDBChangePhaseOf(string(content)), Result: domain.LadoDBChangeResultBaseline,
			HeadSHA: commit, Actor: "lado", StartedAt: now, FinishedAt: &finished,
		})
	}
	return commit, rows, nil
}

// settingsAudit returns the audit events of a settings change: the change,
// with the secrets by name and the command by its hash, and the baseline
// when one was recorded.
func (s *Service) settingsAudit(proof domain.LadoPresenceProof, current domain.LadoDBChangeSettings, existed bool, next domain.LadoDBChangeSettings, secrets secretsChange, rebaselined bool, baseline []domain.LadoDBChangeApplication, now time.Time) []domain.LadoAuditEvent {
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	subject := domain.LadoDBChangeSettingsSubject(next.ProjectID, next.Repo)
	previous := ""
	if existed {
		previous = current.SHA256()
	}
	changed, _ := json.Marshal(map[string]any{
		"repo": next.Repo, "enabled": next.Enabled, "paths": next.Paths, "commandSha256": sha256Hex(next.Command),
		"timeoutMinutes": next.TimeoutMinutes, "secretNames": next.SecretNames,
		"secretsSet": orEmpty(secrets.set), "secretsRemoved": orEmpty(secrets.removed),
		"baselineCommit": next.BaselineCommit, "previousSha256": previous, "sha256": next.SHA256(),
		"proof": proof.Nonce, "dev": proof.Dev,
	})
	events := []domain.LadoAuditEvent{{
		At: now, Actor: actor, Action: domain.LadoAuditDBChangesChanged, SubjectKind: "project",
		SubjectID: string(next.ProjectID), Payload: changed,
	}}
	if rebaselined {
		files := make([]map[string]string, len(baseline))
		for i, row := range baseline {
			files[i] = map[string]string{"path": row.Path, "blobSha": row.BlobSHA}
		}
		payload, _ := json.Marshal(map[string]any{
			"repo": next.Repo, "subject": subject, "commit": next.BaselineCommit, "count": len(baseline), "files": files,
		})
		events = append(events, domain.LadoAuditEvent{
			At: now, Actor: "lado", Action: domain.LadoAuditDBChangeBaseline, SubjectKind: "project",
			SubjectID: string(next.ProjectID), Payload: payload,
		})
	}
	return events
}

// matchesAny reports whether a repository path matches one of the globs.
func matchesAny(globs []string, path string) bool {
	return slices.ContainsFunc(globs, func(glob string) bool { return ladoverify.MatchPath(glob, path) })
}

// regularFile reports whether a tree mode is a regular file's: a symbolic
// link or a submodule is no SQL file.
func regularFile(mode string) bool { return mode == "100644" || mode == "100755" }

// invalid is the 400 of a refused setting, with every problem.
func invalid(problems []domain.LadoDBChangeProblem) error {
	list := make([]map[string]string, len(problems))
	for i, p := range problems {
		list[i] = map[string]string{"field": p.Field, "reason": p.Reason}
	}
	return apierr.Invalid("LADO_DB_CHANGE_INVALID", (&domain.LadoDBChangeInvalidError{Problems: problems}).Error(),
		map[string]any{"problems": list})
}

func loadFailed() error {
	return apierr.Internal("LADO_DB_CHANGE_LOAD_FAILED", "Failed to load the database change settings")
}

// orEmpty returns list, or an empty list for nil.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// sha256Hex is the hex sha256 of text.
func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
