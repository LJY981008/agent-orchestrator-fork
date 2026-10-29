package store

// LADO fork: per-project LADO settings (docs/LJY-PRD/TDD.md §4.2).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LadoProjectSettings returns the project's LADO settings. A project without a
// stored row has LADO mode off, the default cap, no check commands and the
// default check time limit.
func (s *Store) LadoProjectSettings(ctx context.Context, projectID domain.ProjectID) (domain.LadoProjectSettings, error) {
	row, err := s.qr.GetLadoProjectSettings(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoProjectSettings{
			ProjectID:           projectID,
			MaxConcurrent:       domain.LadoDefaultProjectMaxConcurrent,
			CheckCommands:       []domain.LadoCheckCommand{},
			CheckTimeoutMinutes: domain.LadoCheckTimeoutDefault,
			OverlapWait:         true,
		}, nil
	}
	if err != nil {
		return domain.LadoProjectSettings{}, fmt.Errorf("read LADO project settings: %w", err)
	}
	return ladoProjectSettingsFromGen(row)
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
	return ladoProjectSettingsFromGen(row)
}

// LadoProjectChecks returns a project's check commands and their time limit
// in minutes: none and domain.LadoCheckTimeoutDefault until the project sets
// them.
func (s *Store) LadoProjectChecks(ctx context.Context, projectID domain.ProjectID) ([]domain.LadoCheckCommand, int, error) {
	row, err := s.qr.GetLadoProjectChecks(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return []domain.LadoCheckCommand{}, domain.LadoCheckTimeoutDefault, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read LADO project checks: %w", err)
	}
	commands, err := ladoCheckCommandsFromJSON(row.CheckCommands)
	if err != nil {
		return nil, 0, err
	}
	return commands, int(row.CheckTimeoutMinutes), nil
}

// SetLadoProjectCheckCommands stores a project's check commands with their
// audit event, and the presence proof that allowed the change when one is
// given, in one transaction; the change takes the event's time. The stored
// commands must still hash to base (domain.LadoCheckCommandsSHA256), else
// the change is refused with domain.ErrLadoCheckCommandsChanged and nothing
// is kept. A proof used before is refused with domain.ErrLadoPresenceReplayed.
// A project without LADO settings gets them with LADO mode off. The caller
// validates the commands (domain.ValidateLadoCheckCommands).
func (s *Store) SetLadoProjectCheckCommands(ctx context.Context, projectID domain.ProjectID, base string, cmds []domain.LadoCheckCommand, proof *domain.LadoPresenceProof, audit domain.LadoAuditEvent) error {
	// A nil list stores as none.
	commands, err := json.Marshal(append([]domain.LadoCheckCommand{}, cmds...))
	if err != nil {
		return fmt.Errorf("set LADO project check commands: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "set LADO project check commands", func(q *gen.Queries) error {
		stored, err := ladoStoredCheckCommands(ctx, q, projectID)
		if err != nil {
			return err
		}
		if !strings.EqualFold(base, domain.LadoCheckCommandsSHA256(stored)) {
			return domain.ErrLadoCheckCommandsChanged
		}
		if proof != nil {
			if err := insertLadoPresenceProof(ctx, q, *proof); err != nil {
				return err
			}
		}
		if err := q.UpsertLadoProjectCheckCommands(ctx, gen.UpsertLadoProjectCheckCommandsParams{
			ProjectID:     string(projectID),
			CheckCommands: string(commands),
			UpdatedAt:     audit.At.UTC(),
		}); err != nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, audit)
		return err
	})
	return unwrapLadoSentinel(err)
}

// SetLadoProjectCheckTimeout stores a project's check time limit in minutes
// with its audit event in one transaction; the change takes the event's time.
// A project without LADO settings gets them with LADO mode off. The table
// refuses a limit outside domain.LadoCheckTimeoutMin to
// domain.LadoCheckTimeoutMax.
func (s *Store) SetLadoProjectCheckTimeout(ctx context.Context, projectID domain.ProjectID, minutes int, audit domain.LadoAuditEvent) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set LADO project check timeout", func(q *gen.Queries) error {
		if err := q.UpsertLadoProjectCheckTimeout(ctx, gen.UpsertLadoProjectCheckTimeoutParams{
			ProjectID:           string(projectID),
			CheckTimeoutMinutes: int64(minutes),
			UpdatedAt:           audit.At.UTC(),
		}); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, audit)
		return err
	})
}

func ladoProjectSettingsFromGen(row gen.LadoProjectSetting) (domain.LadoProjectSettings, error) {
	commands, err := ladoCheckCommandsFromJSON(row.CheckCommands)
	if err != nil {
		return domain.LadoProjectSettings{}, err
	}
	var ignore []string
	if row.OverlapIgnoreJson.Valid {
		ignore = []string{}
		if err := json.Unmarshal([]byte(row.OverlapIgnoreJson.String), &ignore); err != nil {
			return domain.LadoProjectSettings{}, fmt.Errorf("decode LADO overlap ignore globs: %w", err)
		}
	}
	return domain.LadoProjectSettings{
		ProjectID:           domain.ProjectID(row.ProjectID),
		LadoMode:            row.LadoMode == 1,
		MaxConcurrent:       int(row.MaxConcurrent),
		CheckCommands:       commands,
		CheckTimeoutMinutes: int(row.CheckTimeoutMinutes),
		OverlapWait:         row.OverlapWait == 1,
		OverlapIgnore:       ignore,
		UpdatedAt:           row.UpdatedAt,
	}, nil
}

// SetLadoProjectOverlap stores whether a project holds tasks whose paths
// overlap another task's live run, and the globs whose overlap does not count
// (nil for the defaults), with the audit event, in one transaction; the
// change takes the event's time. A project without LADO settings gets them
// with LADO mode off.
func (s *Store) SetLadoProjectOverlap(ctx context.Context, projectID domain.ProjectID, wait bool, ignore []string, event domain.LadoAuditEvent) error {
	var stored sql.NullString
	if ignore != nil {
		data, err := json.Marshal(ignore)
		if err != nil {
			return fmt.Errorf("encode LADO overlap ignore globs: %w", err)
		}
		stored = sql.NullString{String: string(data), Valid: true}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "set LADO project overlap", func(q *gen.Queries) error {
		if err := q.UpsertLadoProjectOverlap(ctx, gen.UpsertLadoProjectOverlapParams{
			ProjectID:         string(projectID),
			OverlapWait:       boolInt(wait),
			OverlapIgnoreJson: stored,
			UpdatedAt:         event.At.UTC(),
		}); err != nil {
			return err
		}
		_, err := appendLadoAuditEventTx(ctx, q, event)
		return err
	})
}

// ladoCheckCommandsFromJSON decodes a project's stored check commands; none
// decode as an empty list.
func ladoCheckCommandsFromJSON(raw string) ([]domain.LadoCheckCommand, error) {
	commands := []domain.LadoCheckCommand{}
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		return nil, fmt.Errorf("decode LADO project check commands: %w", err)
	}
	return commands, nil
}

// ladoStoredCheckCommands reads a project's check commands inside a
// transaction: none without a settings row.
func ladoStoredCheckCommands(ctx context.Context, q *gen.Queries, projectID domain.ProjectID) ([]domain.LadoCheckCommand, error) {
	row, err := q.GetLadoProjectChecks(ctx, string(projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return []domain.LadoCheckCommand{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ladoCheckCommandsFromJSON(row.CheckCommands)
}
