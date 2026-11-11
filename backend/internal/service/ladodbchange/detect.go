package ladodbchange

// The detection: which SQL files a PR head adds or changes, as the db_changes
// item of its verification record (ladoverify.DBChanges.Item), and which of
// an approved head's pre-merge files are not applied yet.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Item returns the db_changes item of a PR head, false when the repository's
// settings are off (or missing), so the record keeps the items and the hash
// it has without them. The files are those the diff from the merge base of
// in.Base and in.Head to the head adds or changes and the settings' paths
// match, in byte order of their paths, each with its blob and phase. A file
// LADO applied (or recorded at the baseline) that the head changes or
// deletes is listed as modified and is not run again.
//
// The outcome is fail db_change_secrets_missing when the head has files and a
// secret the settings name has no stored value; else confirm
// db_change_applied_modified when it changes applied files; else confirm
// db_change_unplanned when it has files and the plan's change_kinds lacks
// db_schema; else pass, no_db_changes when there is nothing.
func (s *Service) Item(ctx context.Context, in domain.LadoDBChangeInput) (domain.LadoVerifyItem, bool, error) {
	settings, on, err := s.Enabled(ctx, in.ProjectID, in.Repo)
	if err != nil || !on {
		return domain.LadoVerifyItem{}, false, err
	}
	details, err := s.detect(ctx, settings, in)
	if err != nil {
		return domain.LadoVerifyItem{}, false, err
	}
	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case len(details.Files) == 0 && len(details.Modified) == 0:
		code = domain.LadoCodeNoDBChanges
	default:
		missing := []string(nil)
		if len(details.Files) > 0 {
			if missing, err = s.missingSecrets(settings); err != nil {
				return domain.LadoVerifyItem{}, false, err
			}
		}
		switch {
		case len(missing) > 0:
			outcome, code = domain.LadoVerifyFail, domain.LadoCodeDBChangeSecretsMissing
		case len(details.Modified) > 0:
			outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeDBChangeAppliedModified
		case len(details.Files) > 0 && !details.Planned:
			outcome, code = domain.LadoVerifyConfirm, domain.LadoCodeDBChangeUnplanned
		}
	}
	item, err := domain.NewLadoVerifyItem(domain.LadoVerifyDBChanges, outcome, code, details)
	if err != nil {
		return domain.LadoVerifyItem{}, false, err
	}
	return item, true, nil
}

// detect computes the db_changes details of a head under settings.
func (s *Service) detect(ctx context.Context, settings domain.LadoDBChangeSettings, in domain.LadoDBChangeInput) (domain.LadoDBChangesDetails, error) {
	details := domain.LadoDBChangesDetails{
		Repo: in.Repo, Files: []domain.LadoDBChangeFileRef{}, Modified: []domain.LadoDBChangeModified{},
		Planned: slices.Contains(in.Plan.ChangeKinds, domain.LadoChangeDBSchema),
	}
	if in.Dir == "" || in.Base == "" || !IsObjectID(in.Head) {
		return details, fmt.Errorf("look for SQL files: the repository, the base or the head is missing")
	}
	base, err := s.deps.Git.MergeBase(ctx, in.Dir, in.Base, in.Head)
	if err != nil {
		return details, err
	}
	changes, err := s.deps.Git.Diff(ctx, in.Dir, base, in.Head)
	if err != nil {
		return details, err
	}
	var matched []Change
	for _, change := range changes {
		if matchesAny(settings.Paths, change.Path) {
			matched = append(matched, change)
		}
	}
	if len(matched) == 0 {
		return details, nil
	}
	slices.SortFunc(matched, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	paths := make([]string, len(matched))
	for i, change := range matched {
		paths[i] = change.Path
	}
	applied, err := s.deps.Store.LadoDBChangeAppliedPaths(ctx, in.ProjectID, in.Repo, paths)
	if err != nil {
		return details, fmt.Errorf("read the applied SQL files: %w", err)
	}
	for _, change := range matched {
		done, wasApplied := applied[change.Path]
		gone := change.NewBlob == "" || !regularFile(change.NewMode)
		switch {
		case wasApplied && gone:
			details.Modified = append(details.Modified, domain.LadoDBChangeModified{
				Path: change.Path, AppliedBlobSHA: done.BlobSHA, Deleted: true,
			})
		case wasApplied:
			if done.BlobSHA != change.NewBlob {
				details.Modified = append(details.Modified, domain.LadoDBChangeModified{
					Path: change.Path, AppliedBlobSHA: done.BlobSHA, HeadBlobSHA: change.NewBlob,
				})
			}
		case gone:
			// A file never applied that the head deletes runs nowhere.
		default:
			content, _, err := s.deps.Git.Blob(ctx, in.Dir, change.NewBlob, BlobReadMax)
			if err != nil {
				return details, err
			}
			details.Files = append(details.Files, domain.LadoDBChangeFileRef{
				Path: change.Path, BlobSHA: change.NewBlob, Phase: domain.LadoDBChangePhaseOf(string(content)),
			})
		}
	}
	return details, nil
}

// PendingPreMerge returns the pre-merge files of a record's db_changes
// details that have no applied or baseline row, in their order: what LADO
// must apply before the head merges. It returns none while the
// repository's settings are off.
func (s *Service) PendingPreMerge(ctx context.Context, project domain.ProjectID, details domain.LadoDBChangesDetails) ([]domain.LadoDBChangeFileRef, error) {
	files := details.PreMerge()
	if len(files) == 0 {
		return nil, nil
	}
	if _, on, err := s.Enabled(ctx, project, details.Repo); err != nil || !on {
		return nil, err
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	applied, err := s.deps.Store.LadoDBChangeAppliedPaths(ctx, project, details.Repo, paths)
	if err != nil {
		return nil, fmt.Errorf("read the applied SQL files: %w", err)
	}
	var pending []domain.LadoDBChangeFileRef
	for _, file := range files {
		if _, ok := applied[file.Path]; !ok {
			pending = append(pending, file)
		}
	}
	return pending, nil
}

// FileContent reads a SQL file's content at its blob in the repository at
// dir, cut at domain.LadoDBChangeFileContentMax bytes.
func (s *Service) FileContent(ctx context.Context, dir string, file domain.LadoDBChangeFileRef) (domain.LadoDBChangeFileContent, error) {
	if !IsObjectID(file.BlobSHA) {
		return domain.LadoDBChangeFileContent{}, errors.New("read a SQL file: not a blob id")
	}
	content, truncated, err := s.deps.Git.Blob(ctx, dir, file.BlobSHA, domain.LadoDBChangeFileContentMax)
	if err != nil {
		return domain.LadoDBChangeFileContent{}, err
	}
	text := string(content)
	if truncated {
		// The cut may split the last character: leave it out.
		for i := len(text) - 1; i >= 0 && i >= len(text)-utf8.UTFMax; i-- {
			if utf8.RuneStart(text[i]) {
				if !utf8.FullRuneInString(text[i:]) {
					text = text[:i]
				}
				break
			}
		}
	}
	return domain.LadoDBChangeFileContent{
		Path: file.Path, BlobSHA: file.BlobSHA, Content: strings.ToValidUTF8(text, "�"), Truncated: truncated,
	}, nil
}
