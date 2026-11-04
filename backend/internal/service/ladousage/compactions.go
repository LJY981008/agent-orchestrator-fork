package ladousage

// The compaction counter (plan step 10, decision 10). The watch loop calls
// CountCompactions once a minute, so a task's compaction count rises, and its
// alert fires, without anyone opening the usage view. Each session's
// conversation files are read on from where the last pass stopped; only whole
// lines move the offset, so a line Claude or Codex is still writing is read
// the next time.
//
// What counts as one compaction:
//   - Claude Code: a transcript record {"type":"system","subtype":"compact_boundary"}
//     outside a sidechain, the record chatdriver/claudeacp/checkpoint.go reads.
//   - Codex: a rollout record {"type":"compacted", ...}. Codex writes it once
//     per compaction, next to an event_msg of payload type
//     "context_compacted", which is not counted again (checked against real
//     rollouts of codex-cli 0.108).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxReadPerPass bounds how much of one file one pass reads; the rest is read
// on the next passes.
var maxReadPerPass int64 = 64 << 20

// CountCompactions counts, for each session of every task that is neither
// merged nor cancelled, the compactions its conversation files recorded since
// the last pass, and stores the offsets and the count in the session's watch
// row through the compaction columns' own update. A session that fails is
// logged and left for the next pass; the error is the context's or a
// failure to list the tasks.
func (s *Service) CountCompactions(ctx context.Context) error {
	tasks, err := s.deps.Store.ListLadoTasksToObserve(ctx)
	if err != nil {
		return fmt.Errorf("count LADO compactions: %w", err)
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.countTaskCompactions(ctx, task.ID); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			s.deps.Log.Warn("LADO could not count a task's compactions", "task", task.ID, "err", err)
		}
	}
	return nil
}

func (s *Service) countTaskCompactions(ctx context.Context, id domain.LadoTaskID) error {
	facts, ok, err := s.deps.Store.LadoTaskTimingFacts(ctx, id)
	if err != nil || !ok {
		return err
	}
	if _, _, _, reason := elapsed(facts, time.Time{}, time.Time{}); reason == EndMerged {
		return nil
	}
	sessions, err := s.taskSessions(ctx, facts)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if err := s.countSessionCompactions(ctx, id, session.id); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			s.deps.Log.Warn("LADO could not count a session's compactions", "task", id, "session", session.id, "err", err)
		}
	}
	return nil
}

func (s *Service) countSessionCompactions(ctx context.Context, task domain.LadoTaskID, session domain.SessionID) error {
	sources, err := s.deps.Store.ListLadoSessionTranscriptSources(ctx, session)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil // nothing to read yet, or a deleted session
	}
	now := s.deps.Clock().UTC()
	if _, err := s.deps.Store.EnsureLadoSessionWatch(ctx, session, task, now); err != nil {
		return err
	}
	watch, ok, err := s.deps.Store.LadoSessionWatch(ctx, session)
	if err != nil || !ok {
		return err
	}
	next := domain.LadoWatchCompactions{
		ReadOffsets: maps.Clone(watch.ReadOffsets),
		Compactions: watch.Compactions,
		CompactedAt: watch.CompactedAt,
	}
	if next.ReadOffsets == nil {
		next.ReadOffsets = map[string]int64{}
	}
	for _, source := range sources {
		kind, ok := compactionKindOf(source.Kind)
		if !ok {
			continue
		}
		res, err := readCompactions(ctx, source.Path, next.ReadOffsets[source.Path], kind)
		if errors.Is(err, os.ErrNotExist) {
			continue // not written yet, or removed
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", source.Path, err)
		}
		next.ReadOffsets[source.Path] = res.offset
		next.Compactions += res.count
		if res.count > 0 {
			at := res.lastAt
			if at.IsZero() {
				at = now
			}
			if at.After(next.CompactedAt) {
				next.CompactedAt = at.UTC()
			}
		}
	}
	if next.Compactions == watch.Compactions && maps.Equal(next.ReadOffsets, watch.ReadOffsets) {
		return nil
	}
	_, err = s.deps.Store.SetLadoSessionWatchCompactions(ctx, session, next, now)
	return err
}

type compactionKind int

const (
	claudeCompactions compactionKind = iota + 1
	codexCompactions
)

func compactionKindOf(kind domain.UsageSourceKind) (compactionKind, bool) {
	switch kind {
	case domain.UsageSourceClaudeMain:
		return claudeCompactions, true
	case domain.UsageSourceCodexRollout:
		return codexCompactions, true
	}
	return 0, false
}

type compactionRead struct {
	offset int64
	count  int
	lastAt time.Time
}

// readCompactions reads path from offset to its last whole line, at most
// maxReadPerPass bytes, and counts the compaction records. A file shorter
// than offset was replaced: the counter starts at its end and counts nothing,
// rather than count the old compactions again.
func readCompactions(ctx context.Context, path string, offset int64, kind compactionKind) (compactionRead, error) {
	f, err := os.Open(path)
	if err != nil {
		return compactionRead{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return compactionRead{}, err
	}
	size := info.Size()
	if size < offset {
		return compactionRead{offset: size}, nil
	}
	if size == offset {
		return compactionRead{offset: offset}, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return compactionRead{}, err
	}
	out := compactionRead{offset: offset}
	r := bufio.NewReaderSize(io.LimitReader(f, maxReadPerPass), 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return compactionRead{}, err
		}
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// A partial last line, or the pass's limit; read it next time.
			// A single line longer than the limit would stop the counter
			// for good, so it is skipped: its tail then fails to decode.
			if out.offset == offset && int64(len(line)) >= maxReadPerPass {
				out.offset += int64(len(line))
			}
			return out, nil
		}
		if err != nil {
			return compactionRead{}, err
		}
		out.offset += int64(len(line))
		if at, ok := compactionRecord(line, kind); ok {
			out.count++
			if at.After(out.lastAt) {
				out.lastAt = at
			}
		}
	}
}

var (
	claudeMarker = []byte(`compact_boundary`)
	codexMarker  = []byte(`"compacted"`)
)

// compactionRecord reports whether line is one compaction record and when it
// was written, zero when the record has no readable time.
func compactionRecord(line []byte, kind compactionKind) (time.Time, bool) {
	marker := claudeMarker
	if kind == codexCompactions {
		marker = codexMarker
	}
	if !bytes.Contains(line, marker) {
		return time.Time{}, false
	}
	var record struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		Sidechain bool   `json:"isSidechain"`
		Timestamp string `json:"timestamp"`
	}
	if json.Unmarshal(line, &record) != nil {
		return time.Time{}, false
	}
	switch kind {
	case claudeCompactions:
		if record.Type != "system" || record.Subtype != "compact_boundary" || record.Sidechain {
			return time.Time{}, false
		}
	case codexCompactions:
		if record.Type != "compacted" {
			return time.Time{}, false
		}
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return time.Time{}, true
	}
	return at.UTC(), true
}
