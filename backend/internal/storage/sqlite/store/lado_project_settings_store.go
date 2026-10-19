package store

// LADO fork: per-project LADO settings (docs/LJY-PRD/TDD.md §4.2).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoProjectSettings returns the project's LADO settings. A project without a
// stored row has LADO mode off.
func (s *Store) LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	row, err := s.qr.GetLadoProjectSettings(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoProjectSettings{ProjectID: projectID}, nil
	}
	if err != nil {
		return domain.LadoProjectSettings{}, fmt.Errorf("read LADO project settings: %w", err)
	}
	return ladoProjectSettingsFromGen(row), nil
}

// SetLadoMode stores the project's LADO mode.
func (s *Store) SetLadoMode(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time) (domain.LadoProjectSettings, error) {
	mode := int64(0)
	if on {
		mode = 1
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoProjectSettings{}, err
	}
	defer s.writeMu.Unlock()
	row, err := s.qw.UpsertLadoProjectSettings(ctx, gen.UpsertLadoProjectSettingsParams{
		ProjectID: string(projectID),
		LadoMode:  mode,
		UpdatedAt: at.UTC(),
	})
	if err != nil {
		return domain.LadoProjectSettings{}, fmt.Errorf("set LADO mode: %w", err)
	}
	return ladoProjectSettingsFromGen(row), nil
}

func ladoProjectSettingsFromGen(row gen.LadoProjectSetting) domain.LadoProjectSettings {
	return domain.LadoProjectSettings{
		ProjectID: domain.ProjectID(row.ProjectID),
		LadoMode:  row.LadoMode == 1,
		UpdatedAt: row.UpdatedAt,
	}
}
