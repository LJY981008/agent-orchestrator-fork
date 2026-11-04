package ladousage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	claudeUser      = `{"type":"user","sessionId":"x","uuid":"u1","message":{"role":"user","content":"why is compact_boundary in this text"}}`
	claudeCompact1  = `{"parentUuid":null,"isSidechain":false,"type":"system","subtype":"compact_boundary","content":"Conversation compacted","timestamp":"2026-11-01T10:00:00.000Z","compactMetadata":{"trigger":"auto","preTokens":312543}}`
	claudeCompact2  = `{"parentUuid":null,"isSidechain":false,"type":"system","subtype":"compact_boundary","content":"Conversation compacted","timestamp":"2026-11-01T12:30:00.000Z","compactMetadata":{"trigger":"auto","preTokens":198000}}`
	claudeSidechain = `{"isSidechain":true,"type":"system","subtype":"compact_boundary","timestamp":"2026-11-01T13:00:00.000Z"}`
	codexCompacted  = `{"timestamp":"2026-11-01T11:00:00.000Z","type":"compacted","payload":{"message":"","replacement_history":[]}}`
	codexEvent      = `{"timestamp":"2026-11-01T11:00:00.001Z","type":"event_msg","payload":{"type":"context_compacted"}}`
	codexItem       = `{"timestamp":"2026-11-01T11:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the word \"compacted\" alone"}]}}`
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

func line(s string) string { return s + "\n" }

// compactFixture is a live task with one Claude session and one Codex
// session.
func compactFixture(t *testing.T) (*fakeStore, *Service, string, string) {
	t.Helper()
	dir := t.TempDir()
	claude, codex := filepath.Join(dir, "claude.jsonl"), filepath.Join(dir, "rollout.jsonl")
	f := newFakeStore()
	f.tasks[taskID] = domain.LadoTask{ID: taskID, ProjectID: project, IssueRef: "github:o/r#7", CreatedAt: t0}
	f.facts[taskID] = domain.LadoTaskTimingFacts{
		TaskID: taskID, ProjectID: project, IssueRef: "github:o/r#7", CreatedAt: t0,
		PlanSessions: []domain.LadoTimingSession{{SessionID: "design-1", Role: domain.LadoSessionDesigner}},
		Executions: []domain.LadoTimingExecution{
			{SessionID: "worker-1", Role: domain.LadoRoleSingle, StartedAt: t0.Add(time.Hour)},
		},
	}
	f.sources["worker-1"] = []domain.LadoTranscriptSource{{Kind: domain.UsageSourceClaudeMain, Path: claude}}
	f.sources["design-1"] = []domain.LadoTranscriptSource{{Kind: domain.UsageSourceCodexRollout, Path: codex}}
	svc := New(Deps{Store: f, Usage: fakeUsage{}, Clock: func() time.Time { return t0.Add(2 * time.Hour) }, Log: quietLog()})
	return f, svc, claude, codex
}

func TestCountCompactionsReadsOnWithoutCountingTwice(t *testing.T) {
	f, svc, claude, codex := compactFixture(t)
	ctx := context.Background()
	writeLines(t, claude, line(claudeUser), line(claudeCompact1), line(claudeSidechain))
	writeLines(t, codex, line(codexCompacted), line(codexEvent), line(codexItem))

	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatalf("CountCompactions: %v", err)
	}
	w := f.watches["worker-1"]
	if w.Compactions != 1 || !w.CompactedAt.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("claude watch = %+v, want 1 compaction at 10:00", w.LadoWatchCompactions)
	}
	if info, _ := os.Stat(claude); w.ReadOffsets[claude] != info.Size() {
		t.Errorf("claude offset = %d, want the file's size %d", w.ReadOffsets[claude], info.Size())
	}
	if w.TaskID != taskID {
		t.Errorf("watch task = %q", w.TaskID)
	}
	if c := f.watches["design-1"]; c.Compactions != 1 {
		t.Fatalf("codex watch = %+v, want 1 compaction", c.LadoWatchCompactions)
	}
	writes := f.compactWrites

	// A pass with nothing new reads nothing and writes nothing.
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.watches["worker-1"].Compactions != 1 || f.compactWrites != writes {
		t.Fatalf("second pass: compactions %d, writes %d→%d", f.watches["worker-1"].Compactions, writes, f.compactWrites)
	}

	// A line still being written is left for the next pass.
	half := len(claudeCompact2) / 2
	writeLines(t, claude, claudeCompact2[:half])
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.watches["worker-1"].Compactions != 1 || f.compactWrites != writes {
		t.Fatalf("partial line: compactions %d, writes %d→%d", f.watches["worker-1"].Compactions, writes, f.compactWrites)
	}
	writeLines(t, claude, line(claudeCompact2[half:]))
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	w = f.watches["worker-1"]
	if w.Compactions != 2 || !w.CompactedAt.Equal(time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC)) {
		t.Fatalf("after the line completed = %+v, want 2 compactions at 12:30", w.LadoWatchCompactions)
	}
	if f.watches["design-1"].Compactions != 1 {
		t.Errorf("codex count moved to %d", f.watches["design-1"].Compactions)
	}
}

func TestCountCompactionsSkipsMergedAndCancelledTasks(t *testing.T) {
	f, svc, claude, _ := compactFixture(t)
	writeLines(t, claude, line(claudeCompact1))
	facts := f.facts[taskID]
	facts.Executions[0].SettledState, facts.Executions[0].SettledAt = string(domain.PRStateMerged), t0.Add(3*time.Hour)
	f.facts[taskID] = facts
	if err := svc.CountCompactions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.watches) != 0 {
		t.Fatalf("merged task got watch rows %+v", f.watches)
	}

	facts.Executions[0].SettledState, facts.Executions[0].SettledAt = "", time.Time{}
	f.facts[taskID] = facts
	task := f.tasks[taskID]
	task.CancelledAt = t0.Add(time.Hour)
	f.tasks[taskID] = task
	if err := svc.CountCompactions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.watches) != 0 {
		t.Fatalf("cancelled task got watch rows %+v", f.watches)
	}
}

func TestCountCompactionsKeepsTheOtherWatchColumns(t *testing.T) {
	f, svc, claude, _ := compactFixture(t)
	reset := t0.Add(5 * time.Hour)
	f.watches["worker-1"] = domain.LadoSessionWatch{
		SessionID: "worker-1", TaskID: taskID,
		LadoWatchCompactions: domain.LadoWatchCompactions{ReadOffsets: map[string]int64{}},
		LadoWatchLimit:       domain.LadoWatchLimit{LimitState: domain.LadoLimitLimited, LimitResetAt: reset, Resumes: 2},
		LadoWatchStall:       domain.LadoWatchStall{StallState: domain.LadoStallStalled, Nudges: 1},
	}
	writeLines(t, claude, line(claudeCompact1))
	if err := svc.CountCompactions(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := f.watches["worker-1"]
	if w.Compactions != 1 || w.LimitState != domain.LadoLimitLimited || !w.LimitResetAt.Equal(reset) || w.Resumes != 2 ||
		w.StallState != domain.LadoStallStalled || w.Nudges != 1 {
		t.Fatalf("watch = %+v", w)
	}
}

func TestCountCompactionsMissingAndReplacedFiles(t *testing.T) {
	f, svc, claude, _ := compactFixture(t)
	ctx := context.Background()
	// Neither file exists yet: rows are made, nothing is counted.
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	if w := f.watches["worker-1"]; w.Compactions != 0 || len(w.ReadOffsets) != 0 {
		t.Fatalf("watch before the file = %+v", w.LadoWatchCompactions)
	}
	writeLines(t, claude, line(claudeCompact1), line(claudeCompact2))
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.watches["worker-1"].Compactions != 2 {
		t.Fatalf("compactions = %d, want 2", f.watches["worker-1"].Compactions)
	}
	// A shorter file replaced it: the old compactions are not counted again.
	if err := os.WriteFile(claude, []byte(line(claudeCompact1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.CountCompactions(ctx); err != nil {
		t.Fatal(err)
	}
	w := f.watches["worker-1"]
	if info, _ := os.Stat(claude); w.Compactions != 2 || w.ReadOffsets[claude] != info.Size() {
		t.Fatalf("after replace = %+v, want 2 and the new size", w.LadoWatchCompactions)
	}
}

func TestReadCompactionsReadsAPassAtATime(t *testing.T) {
	old := maxReadPerPass
	t.Cleanup(func() { maxReadPerPass = old })
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeLines(t, path, line(claudeCompact1), line(claudeCompact2))
	maxReadPerPass = int64(len(claudeCompact1)) + 10
	first, err := readCompactions(context.Background(), path, 0, claudeCompactions)
	if err != nil || first.count != 1 || first.offset != int64(len(claudeCompact1))+1 {
		t.Fatalf("first pass = %+v, %v", first, err)
	}
	second, err := readCompactions(context.Background(), path, first.offset, claudeCompactions)
	if err != nil || second.count != 1 {
		t.Fatalf("second pass = %+v, %v", second, err)
	}

	// One line longer than a pass is skipped instead of stopping the
	// counter.
	long := filepath.Join(t.TempDir(), "long.jsonl")
	writeLines(t, long, line(`{"type":"user","text":"`+strings.Repeat("x", 3*len(claudeCompact1))+`"}`), line(claudeCompact1))
	maxReadPerPass = int64(len(claudeCompact1)) + 10
	offset, count := int64(0), 0
	for range 10 {
		res, err := readCompactions(context.Background(), long, offset, claudeCompactions)
		if err != nil {
			t.Fatal(err)
		}
		offset, count = res.offset, count+res.count
	}
	if info, _ := os.Stat(long); count != 1 || offset != info.Size() {
		t.Fatalf("long line: count %d offset %d", count, offset)
	}
}
