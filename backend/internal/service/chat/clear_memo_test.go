package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/projectmemo"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// memoResponder plays the provider for one dispatched message.
type memoResponder func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string)

// memoDriver hands out fake conversations that answer through a responder:
// "thread-1" first, then a fresh thread per later Start, and a new instance
// of the named thread per Resume.
type memoDriver struct {
	mu       sync.Mutex
	caps     ports.ChatCapabilities
	respond  memoResponder
	starts   int
	startErr error
	convs    []*fakeConversation
	// wrap, when set, decorates every conversation the driver hands out.
	wrap func(*fakeConversation) ports.ChatConversation
}

func (d *memoDriver) hand(conv *fakeConversation) ports.ChatConversation {
	if d.wrap != nil {
		return d.wrap(conv)
	}
	return conv
}

func (d *memoDriver) newConversation(providerConversationID string, turnSeq int) *fakeConversation {
	conv := newFakeConversation()
	conv.providerConversationID = providerConversationID
	conv.turnSeq = turnSeq
	conv.setCapabilities(d.caps)
	conv.onSend = func(providerTurnID string) {
		sent := conv.sentMessages()
		d.mu.Lock()
		respond := d.respond
		d.mu.Unlock()
		if respond != nil {
			respond(conv, sent[len(sent)-1], providerTurnID)
		}
	}
	d.convs = append(d.convs, conv)
	return conv
}

// latest is the newest conversation the driver made for a provider thread.
func (d *memoDriver) latest(providerConversationID string) *fakeConversation {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.convs) - 1; i >= 0; i-- {
		if d.convs[i].providerConversationID == providerConversationID {
			return d.convs[i]
		}
	}
	return nil
}

func (d *memoDriver) driver() fakeDriver {
	return fakeDriver{
		caps: d.caps,
		start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.starts++
			if d.starts == 1 {
				return d.hand(d.newConversation("thread-1", 0)), nil
			}
			if d.startErr != nil {
				return nil, d.startErr
			}
			return d.hand(d.newConversation(fmt.Sprintf("thread-fresh-%d", d.starts), d.starts*100)), nil
		},
		resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			return d.hand(d.newConversation(cfg.ProviderConversationID, 900+len(d.convs)*10)), nil
		},
	}
}

// answerTurn streams a whole provider turn into the fake's buffered event
// channel. The controller projects it after SendTurn returns.
func answerTurn(conv *fakeConversation, providerTurnID, text string, state domain.TurnState) {
	events := []ports.ChatEvent{{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurnID}}
	if text != "" {
		events = append(events, ports.ChatEvent{
			Kind: ports.ChatEventMessageCompleted, ProviderTurnID: providerTurnID,
			ProviderItemID: "msg-" + providerTurnID, Text: text,
		})
	}
	events = append(events, ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: providerTurnID, TurnState: state,
	})
	conv.emit(events...)
}

func isMemoRequest(msg ports.ChatUserMessage) bool {
	return msg.Origin == domain.MessageOriginDaemon && strings.Contains(msg.Text, "<ao-project-memo>")
}

// replyWithMemo answers memo requests with memo blocks, in turn, and every
// other message with "reply to <text>".
func replyWithMemo(memos ...string) memoResponder {
	var mu sync.Mutex
	next := 0
	return func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
		if !isMemoRequest(msg) {
			answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
			return
		}
		mu.Lock()
		reply := memos[min(next, len(memos)-1)]
		next++
		mu.Unlock()
		answerTurn(conv, providerTurnID, reply, domain.TurnStateCompleted)
	}
}

type memoHarness struct {
	*harness
	dataDir string
	memos   *projectmemo.Store
	drv     *memoDriver
	cfg     chatsvc.StartConfig
}

type memoHarnessOptions struct {
	kind           domain.SessionKind
	noEmbeddedCtx  bool
	respond        memoResponder
	budget         time.Duration
	interruptWait  time.Duration
	interruptStops bool
	// fenceOwner makes the controller env step check the session owner the
	// way Session Manager does before it rotates the browser capability.
	fenceOwner bool
	// sourceCloseFails makes the first provider conversation refuse to close
	// while its stream stays live.
	sourceCloseFails bool
}

func newMemoHarness(t *testing.T, opts memoHarnessOptions) *memoHarness {
	t.Helper()
	st := openStore(t)
	caps := productionCaps()
	if !opts.noEmbeddedCtx {
		caps[ports.ChatCapabilityEmbeddedContext] = true
	}
	drv := &memoDriver{caps: caps, respond: opts.respond}
	if opts.interruptStops {
		drv.wrap = func(conv *fakeConversation) ports.ChatConversation {
			return &interruptingConversation{fakeConversation: conv}
		}
	}
	if opts.sourceCloseFails {
		drv.wrap = func(conv *fakeConversation) ports.ChatConversation {
			if conv.providerConversationID == "thread-1" && len(drv.convs) == 1 {
				return &stuckConversation{fakeConversation: conv, closeErr: errors.New("provider close failed")}
			}
			return conv
		}
	}
	h := &memoHarness{
		harness: &harness{st: st, activity: &recordingActivity{}, clock: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)},
		dataDir: t.TempDir(),
		drv:     drv,
	}
	h.memos = projectmemo.New(h.dataDir)
	kind := opts.kind
	if kind == "" {
		kind = domain.KindOrchestrator
	}
	h.cfg = chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: kind,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), SystemPrompt: "orchestrator prompt",
	}
	if opts.fenceOwner {
		h.cfg.PrepareControllerEnv = func(ctx context.Context, expected domain.SessionControllerOwner) (map[string]string, error) {
			if expected.ProviderConversationID == "" {
				return nil, nil // the first start has no owner yet
			}
			applied, err := st.UpdateBrowserCapabilityVerifier(ctx, testSession, expected, "verifier")
			if err != nil {
				return nil, err
			}
			if !applied {
				return nil, errors.New("session controller ownership changed before browser capability rotation")
			}
			return nil, nil
		}
	}
	h.svc = h.newService(t)
	if opts.budget > 0 {
		h.svc.SetClearMemoTimings(opts.budget, opts.interruptWait)
	}
	ctrl, err := h.svc.Start(context.Background(), h.cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.ctrl = ctrl
	h.conv = drv.latest("thread-1")
	// Tests have no ControllerReady callback; record the owner it would.
	record, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "thread-1"
	record.Metadata.ControllerGeneration = ctrl.Generation()
	if err := st.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	t.Cleanup(func() { _ = h.svc.Stop(context.Background(), testSession) })
	return h
}

// newService builds a chat service over the harness store and data dir. A
// second one stands in for a restarted daemon.
func (h *memoHarness) newService(t *testing.T) *chatsvc.Service {
	t.Helper()
	var mu sync.Mutex
	counter := 0
	prefix := fmt.Sprintf("svc%d", time.Now().UnixNano()%100000)
	return chatsvc.New(chatsvc.Options{
		Store: h.st, Sessions: h.st,
		Reader: fullSnapshotReader(h.st), PageReader: boundedSnapshotReader(h.st),
		Drivers:  fakeRegistry{driver: h.drv.driver()},
		Activity: h.activity,
		Log:      slog.New(slog.DiscardHandler),
		NewID: func() string {
			mu.Lock()
			defer mu.Unlock()
			counter++
			return fmt.Sprintf("%s-id-%03d", prefix, counter)
		},
		Now:     h.now,
		DataDir: h.dataDir,
	})
}

func (h *memoHarness) send(t *testing.T, text string, origin domain.MessageOrigin) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: text, Origin: origin})
	if err != nil {
		t.Fatalf("Send %q: %v", text, err)
	}
	return turn
}

// awaitTurnState waits until a turn reaches state.
func (h *memoHarness) awaitTurnState(t *testing.T, turnID string, state domain.TurnState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := h.st.TurnByID(context.Background(), turnID)
		if err == nil && turn.State == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	turn, _ := h.st.TurnByID(context.Background(), turnID)
	t.Fatalf("turn %s state = %q, want %q", turnID, turn.State, state)
}

func (h *memoHarness) saveMemo(t *testing.T, body string, source projectmemo.Source) projectmemo.Memo {
	t.Helper()
	memo, err := h.memos.Save(testProject, projectmemo.SaveInput{Body: body, Source: source})
	if err != nil {
		t.Fatalf("save memo: %v", err)
	}
	return memo
}

// awaitSent waits until conv has received n messages and returns them.
func awaitSent(t *testing.T, conv *fakeConversation, n int) []ports.ChatUserMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sent := conv.sentMessages(); len(sent) >= n {
			return sent
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider got %d messages, want %d: %v", len(conv.sentMessages()), n, conv.sentTexts())
	return nil
}

func memoResource(msg ports.ChatUserMessage) (ports.ChatContent, bool) {
	for _, content := range msg.Content {
		if content.Type == "resource" && content.URI == chatsvc.ProjectMemoResourceURI {
			return content, true
		}
	}
	return ports.ChatContent{}, false
}

func requireMemoResource(t *testing.T, msg ports.ChatUserMessage, body string) ports.ChatContent {
	t.Helper()
	resource, ok := memoResource(msg)
	if !ok {
		t.Fatalf("message %q has no project memo resource: %+v", msg.Text, msg.Content)
	}
	if !resource.Internal || resource.MIMEType != "text/markdown" || !strings.Contains(resource.Text, body) ||
		msg.Content[0].URI != chatsvc.ProjectMemoResourceURI {
		t.Fatalf("project memo resource = %+v, want first, internal markdown containing %q", resource, body)
	}
	return resource
}

func requireNoMemoResource(t *testing.T, msg ports.ChatUserMessage) {
	t.Helper()
	if resource, ok := memoResource(msg); ok {
		t.Fatalf("message %q carries the project memo: %+v", msg.Text, resource)
	}
}

func memoFailureRows(t *testing.T, activities []domain.ConversationActivity) []map[string]string {
	t.Helper()
	var rows []map[string]string
	for _, activity := range activities {
		if activity.Kind != domain.ActivityKindSystem || len(activity.Detail) == 0 {
			continue
		}
		var detail map[string]string
		if json.Unmarshal(activity.Detail, &detail) == nil && detail["event"] == chatsvc.ClearMemoFailedEvent {
			if activity.Status != domain.ActivityStatusFailed {
				t.Fatalf("memo failure row status = %q", activity.Status)
			}
			rows = append(rows, detail)
		}
	}
	return rows
}

func daemonTurnIDs(messages []domain.ConversationMessage) []string {
	var ids []string
	for _, message := range messages {
		if message.Role == domain.MessageRoleUser && message.Origin == domain.MessageOriginDaemon {
			ids = append(ids, message.TurnID)
		}
	}
	return ids
}

func TestClearContextSavesOrchestratorMemoBeforeClearing(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{
		respond: replyWithMemo("<ao-project-memo>\nScope: issue 50 only. Merge is the developer's job.\n</ao-project-memo>"),
	})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
	before, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.svc.ClearContext(ctx, testSession)
	if err != nil {
		t.Fatalf("ClearContext: %v", err)
	}
	if !result.Cleared || result.ActiveBranchID == before.ActiveBranchID {
		t.Fatalf("result = %+v", result)
	}
	if result.Memo.Status != projectmemo.ClearSaved || result.Memo.Revision == "" || result.Memo.UpdatedAt.IsZero() {
		t.Fatalf("memo outcome = %+v, want saved", result.Memo)
	}
	saved, err := h.memos.Get(testProject)
	if err != nil {
		t.Fatalf("get memo: %v", err)
	}
	if saved.Body != "Scope: issue 50 only. Merge is the developer's job." || saved.Source != projectmemo.SourceClear ||
		saved.Revision != result.Memo.Revision {
		t.Fatalf("saved memo = %+v", saved)
	}

	// The memo request went to the old conversation, straight from AO and
	// without the memo resource, and stays above the /clear divider.
	sent := h.conv.sentMessages()
	if len(sent) != 2 || !isMemoRequest(sent[1]) || !strings.Contains(sent[1].Text, "There is no project memo yet.") {
		t.Fatalf("source provider messages = %v", h.conv.sentTexts())
	}
	requireNoMemoResource(t, sent[1])
	snapshot := h.awaitSnapshot(t, func(s sqlite.ConversationSnapshot) bool { return len(clearBoundaries(t, s.Activities)) == 1 })
	boundary := clearBoundaries(t, snapshot.Activities)[0]
	requests := daemonTurnIDs(snapshot.Messages)
	if len(requests) != 1 {
		t.Fatalf("memo request rows = %v", requests)
	}
	for _, message := range snapshot.Messages {
		if message.TurnID == requests[0] && message.Sequence >= boundary.Sequence {
			t.Fatalf("memo turn row %+v is below the divider at %d", message, boundary.Sequence)
		}
	}

	// The new conversation's first turn reads the memo; the second does not.
	h.send(t, "B", domain.MessageOriginHuman)
	fresh := h.drv.latest("thread-fresh-2")
	sentFresh := awaitSent(t, fresh, 1)
	requireMemoResource(t, sentFresh[0], "Scope: issue 50 only.")
	h.awaitSnapshot(t, func(s sqlite.ConversationSnapshot) bool { return len(s.Messages) == 6 })
	h.send(t, "C", domain.MessageOriginHuman)
	sentFresh = awaitSent(t, fresh, 2)
	requireNoMemoResource(t, sentFresh[1])

	// The resource never reaches AO's rows, so the timeline shows no attachment.
	snapshot = h.awaitSnapshot(t, func(s sqlite.ConversationSnapshot) bool { return len(s.Messages) == 8 })
	for _, message := range snapshot.Messages {
		if strings.Contains(message.DeliveryContentJSON, chatsvc.ProjectMemoResourceURI) {
			t.Fatalf("message %q stores the memo resource: %s", message.Text, message.DeliveryContentJSON)
		}
	}
}

func TestClearContextMemoRequestIncludesCurrentMemoAndShrinksOnce(t *testing.T) {
	long := strings.Repeat("가", projectmemo.MaxRunes+1)
	h := newMemoHarness(t, memoHarnessOptions{
		respond: replyWithMemo("<ao-project-memo>"+long+"</ao-project-memo>", "<ao-project-memo>short</ao-project-memo>"),
	})
	h.saveMemo(t, "earlier memo", projectmemo.SourceUser)
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	result, err := h.svc.ClearContext(context.Background(), testSession)
	if err != nil || !result.Cleared || result.Memo.Status != projectmemo.ClearSaved {
		t.Fatalf("ClearContext = %+v, %v", result, err)
	}
	sent := h.conv.sentMessages()
	if len(sent) != 3 || !strings.Contains(sent[1].Text, "<current-project-memo>\nearlier memo\n</current-project-memo>") ||
		!strings.Contains(sent[2].Text, fmt.Sprintf("That memo is %d characters", projectmemo.MaxRunes+1)) {
		t.Fatalf("memo requests = %v", h.conv.sentTexts())
	}
	saved, err := h.memos.Get(testProject)
	if err != nil || saved.Body != "short" {
		t.Fatalf("saved memo = %+v, %v", saved, err)
	}
}

// interruptingConversation stops a turn when asked, as a provider does.
type interruptingConversation struct {
	*fakeConversation
}

func (c *interruptingConversation) Interrupt(_ context.Context, providerTurnID string) error {
	answerTurn(c.fakeConversation, providerTurnID, "", domain.TurnStateInterrupted)
	return nil
}

func TestClearContextClearsWhenMemoStepFails(t *testing.T) {
	long := "<ao-project-memo>" + strings.Repeat("x", projectmemo.MaxRunes+1) + "</ao-project-memo>"
	silent := func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
		if !isMemoRequest(msg) {
			answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
			return
		}
		// Acknowledge the turn and never finish it.
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurnID})
	}
	cases := []struct {
		name            string
		respond         memoResponder
		interruptStops  bool
		code            string
		memoTurnState   domain.TurnState
		wantMemoRequest int
	}{
		{
			name: "turn fails",
			respond: func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
				state := domain.TurnStateCompleted
				if isMemoRequest(msg) {
					state = domain.TurnStateFailed
				}
				answerTurn(conv, providerTurnID, "reply to "+msg.Text, state)
			},
			code: chatsvc.ClearMemoCodeTurnFailed, memoTurnState: domain.TurnStateFailed, wantMemoRequest: 1,
		},
		{
			name: "budget runs out and the interrupt stops the turn", respond: silent, interruptStops: true,
			code: chatsvc.ClearMemoCodeTimeout, memoTurnState: domain.TurnStateInterrupted, wantMemoRequest: 1,
		},
		{
			name: "budget runs out and the turn ignores the interrupt", respond: silent,
			code: chatsvc.ClearMemoCodeTimeout, memoTurnState: domain.TurnStateInterrupted, wantMemoRequest: 1,
		},
		{
			name: "reply has no memo block", respond: replyWithMemo("I would rather not."),
			code: chatsvc.ClearMemoCodeMissing, memoTurnState: domain.TurnStateCompleted, wantMemoRequest: 1,
		},
		{
			name: "memo stays too long", respond: replyWithMemo(long),
			code: chatsvc.ClearMemoCodeTooLong, memoTurnState: domain.TurnStateCompleted, wantMemoRequest: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMemoHarness(t, memoHarnessOptions{
				respond: tc.respond, budget: 300 * time.Millisecond, interruptWait: 300 * time.Millisecond,
				interruptStops: tc.interruptStops,
			})
			earlier := h.saveMemo(t, "earlier memo", projectmemo.SourceUser)
			first := h.send(t, "A", domain.MessageOriginHuman)
			h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

			result, err := h.svc.ClearContext(context.Background(), testSession)
			if err != nil {
				t.Fatalf("ClearContext: %v", err)
			}
			if !result.Cleared || result.Memo.Status != projectmemo.ClearFailed || result.Memo.Code != tc.code ||
				result.Memo.Message == "" {
				t.Fatalf("result = %+v, want cleared with failed memo %s", result, tc.code)
			}
			if memo, err := h.memos.Get(testProject); err != nil || memo.Revision != earlier.Revision {
				t.Fatalf("memo after failure = %+v, %v; want the earlier one kept", memo, err)
			}
			requests := 0
			for _, msg := range h.conv.sentMessages() {
				if isMemoRequest(msg) {
					requests++
				}
			}
			if requests != tc.wantMemoRequest {
				t.Fatalf("memo requests = %d, want %d", requests, tc.wantMemoRequest)
			}
			snapshot := h.awaitSnapshot(t, func(s sqlite.ConversationSnapshot) bool {
				return len(clearBoundaries(t, s.Activities)) == 1 && len(memoFailureRows(t, s.Activities)) == 1
			})
			if row := memoFailureRows(t, snapshot.Activities)[0]; row["code"] != tc.code {
				t.Fatalf("failure row = %+v", row)
			}
			memoTurns := daemonTurnIDs(snapshot.Messages)
			if len(memoTurns) == 0 {
				t.Fatal("memo request row missing")
			}
			h.awaitTurnState(t, memoTurns[len(memoTurns)-1], tc.memoTurnState)

			// The session goes on in the cleared conversation.
			h.send(t, "after", domain.MessageOriginHuman)
			fresh := h.drv.latest("thread-fresh-2")
			sent := awaitSent(t, fresh, 1)
			if sent[0].Text != "after" {
				t.Fatalf("fresh provider got %v", fresh.sentTexts())
			}
			requireMemoResource(t, sent[0], "earlier memo")
		})
	}
}

func TestClearContextHoldsMessagesUntilTheNewBranch(t *testing.T) {
	release := make(chan struct{})
	h := newMemoHarness(t, memoHarnessOptions{
		respond: func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
			if !isMemoRequest(msg) {
				answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
				return
			}
			go func() {
				<-release
				answerTurn(conv, providerTurnID, "<ao-project-memo>held memo</ao-project-memo>", domain.TurnStateCompleted)
			}()
		},
	})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	type clearResult struct {
		result chatsvc.ClearContextResult
		err    error
	}
	done := make(chan clearResult, 1)
	go func() {
		result, err := h.svc.ClearContext(ctx, testSession)
		done <- clearResult{result, err}
	}()
	awaitSent(t, h.conv, 2) // A, then the memo request

	// A person and the daemon write while the memo turn runs. Both are
	// accepted at once and wait.
	human := h.send(t, "while memo", domain.MessageOriginHuman)
	daemon := h.send(t, "daemon notice", domain.MessageOriginDaemon)
	if human.State != domain.TurnStateQueued || daemon.State != domain.TurnStateQueued {
		t.Fatalf("sends during the memo step = %+v, %+v; want queued", human, daemon)
	}
	time.Sleep(100 * time.Millisecond)
	if got := h.conv.sentTexts(); len(got) != 2 {
		t.Fatalf("source dispatched during the memo hold: %v", got)
	}

	close(release)
	var outcome clearResult
	select {
	case outcome = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ClearContext did not finish")
	}
	if outcome.err != nil || !outcome.result.Cleared || outcome.result.Memo.Status != projectmemo.ClearSaved {
		t.Fatalf("ClearContext = %+v, %v", outcome.result, outcome.err)
	}
	if got := h.conv.sentTexts(); len(got) != 2 {
		t.Fatalf("source got held messages: %v", got)
	}

	// The held messages run in order on the new conversation; the first one
	// carries the memo.
	fresh := h.drv.latest("thread-fresh-2")
	sent := awaitSent(t, fresh, 2)
	if sent[0].Text != "while memo" || sent[1].Text != "daemon notice" {
		t.Fatalf("fresh provider order = %v", fresh.sentTexts())
	}
	requireMemoResource(t, sent[0], "held memo")
	requireNoMemoResource(t, sent[1])

	snapshot := h.awaitSnapshot(t, func(s sqlite.ConversationSnapshot) bool {
		return len(clearBoundaries(t, s.Activities)) == 1 && len(s.Messages) == 8
	})
	boundary := clearBoundaries(t, snapshot.Activities)[0]
	for _, text := range []string{"while memo", "daemon notice"} {
		if seq := messageSequence(t, snapshot, text); seq <= boundary.Sequence {
			t.Fatalf("%q at %d is not after the divider at %d", text, seq, boundary.Sequence)
		}
	}
	for _, id := range []string{human.ID, daemon.ID} {
		turn, err := h.st.TurnByID(ctx, id)
		if err != nil || turn.State != domain.TurnStateCancelled {
			t.Fatalf("held turn %s = %+v, %v; want moved (cancelled) to the new branch", id, turn, err)
		}
	}
}

// Each /clear activates a new provider conversation, which also moves the
// session's native identity. The second /clear must still pass the owner
// fence that guards the browser capability rotation.
func TestClearContextTwiceKeepsTheControllerOwnerCurrent(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{
		fenceOwner: true,
		respond:    replyWithMemo("<ao-project-memo>first</ao-project-memo>", "<ao-project-memo>second</ao-project-memo>"),
	})
	ctx := context.Background()
	for round, text := range []string{"A", "B"} {
		turn := h.send(t, text, domain.MessageOriginHuman)
		h.awaitTurnState(t, turn.ID, domain.TurnStateCompleted)
		result, err := h.svc.ClearContext(ctx, testSession)
		if err != nil {
			t.Fatalf("ClearContext %d: %v", round+1, err)
		}
		if !result.Cleared || result.Memo.Status != projectmemo.ClearSaved {
			t.Fatalf("ClearContext %d = %+v", round+1, result)
		}
	}
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if record.Metadata.BrowserCapabilityVerifier != "verifier" {
		t.Fatalf("browser capability verifier = %q, want rotated", record.Metadata.BrowserCapabilityVerifier)
	}
	// The session still answers after the second clear.
	after := h.send(t, "C", domain.MessageOriginHuman)
	h.awaitTurnState(t, after.ID, domain.TurnStateCompleted)
}

// When the source refuses to close but keeps running, /clear fails and the
// source takes the held messages back. The memo hold must end with it, or the
// source would queue every later message and refuse the next /clear.
func TestClearContextFailedCloseReleasesTheMemoHold(t *testing.T) {
	release := make(chan struct{})
	h := newMemoHarness(t, memoHarnessOptions{
		sourceCloseFails: true,
		respond: func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
			if !isMemoRequest(msg) {
				answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
				return
			}
			go func() {
				<-release
				answerTurn(conv, providerTurnID, "<ao-project-memo>memo</ao-project-memo>", domain.TurnStateCompleted)
			}()
		},
	})
	h.svc.SetClearHandoffLimit(200 * time.Millisecond)
	// The stuck stream must end before the harness stops the session.
	t.Cleanup(func() { h.conv.closeOnce.Do(func() { close(h.conv.events) }) })
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	done := make(chan error, 1)
	go func() {
		_, err := h.svc.ClearContext(ctx, testSession)
		done <- err
	}()
	awaitSent(t, h.conv, 2) // A, then the memo request
	h.send(t, "while memo", domain.MessageOriginHuman)
	close(release)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "close source writer before clear") {
			t.Fatalf("ClearContext error = %v, want the close failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ClearContext did not finish")
	}

	// The held message runs on the source, which still answers.
	sent := awaitSent(t, h.conv, 3)
	if sent[2].Text != "while memo" {
		t.Fatalf("source got %v", h.conv.sentTexts())
	}
	later := h.send(t, "after the failed clear", domain.MessageOriginHuman)
	h.awaitTurnState(t, later.ID, domain.TurnStateCompleted)

	// The next /clear is not refused by a stale hold: it asks for a memo again.
	go func() {
		_, err := h.svc.ClearContext(ctx, testSession)
		done <- err
	}()
	select {
	case err := <-done:
		if errors.Is(err, chatsvc.ErrControllerHandoff) {
			t.Fatalf("second ClearContext refused by the old hold: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second ClearContext did not finish")
	}
	if got := h.conv.sentTexts(); len(got) != 5 || !isMemoRequest(h.conv.sentMessages()[4]) {
		t.Fatalf("source sends after the second clear = %v, want a second memo request", got)
	}
}

// ao send --steer during the memo step neither steers the memo turn nor starts
// a turn beside it: the message waits and moves to the new branch.
func TestClearContextHoldsSteerOrSendUntilTheNewBranch(t *testing.T) {
	release := make(chan struct{})
	h := newMemoHarness(t, memoHarnessOptions{
		respond: func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
			if !isMemoRequest(msg) {
				answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
				return
			}
			go func() {
				<-release
				answerTurn(conv, providerTurnID, "<ao-project-memo>steer memo</ao-project-memo>", domain.TurnStateCompleted)
			}()
		},
	})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	type clearResult struct {
		result chatsvc.ClearContextResult
		err    error
	}
	done := make(chan clearResult, 1)
	go func() {
		result, err := h.svc.ClearContext(ctx, testSession)
		done <- clearResult{result, err}
	}()
	awaitSent(t, h.conv, 2)

	steered, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "worker update", ClientMessageID: "steer-during-memo", Origin: domain.MessageOriginDaemon,
	}, false)
	if err != nil {
		t.Fatalf("SteerOrSend: %v", err)
	}
	if steered.Steered || steered.Turn.State != domain.TurnStateQueued {
		t.Fatalf("SteerOrSend during the memo step = %+v, want queued", steered)
	}
	time.Sleep(100 * time.Millisecond)
	if got := h.conv.sentTexts(); len(got) != 2 {
		t.Fatalf("source dispatched during the memo hold: %v", got)
	}

	close(release)
	var outcome clearResult
	select {
	case outcome = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ClearContext did not finish")
	}
	if outcome.err != nil || !outcome.result.Cleared || outcome.result.Memo.Status != projectmemo.ClearSaved {
		t.Fatalf("ClearContext = %+v, %v", outcome.result, outcome.err)
	}
	fresh := h.drv.latest("thread-fresh-2")
	sent := awaitSent(t, fresh, 1)
	if sent[0].Text != "worker update" {
		t.Fatalf("fresh provider got %v", fresh.sentTexts())
	}
	requireMemoResource(t, sent[0], "steer memo")
}

func TestClearContextLeavesWorkerReportsForTheFirstTurnAfter(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("<ao-project-memo>memo</ao-project-memo>")})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	now := time.Now().UTC()
	worker, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.CreateReport(ctx, domain.ReportRecord{
		ID: "pending-report", SessionID: worker.ID, ProjectID: testProject,
		State: domain.ReportCheckpoint, Note: "pending-report",
		CreatedAt: now, AvailableAt: now.Add(time.Hour),
		DeliveryState: domain.ReportPending, RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	h.svc.SetReportCoordinator(reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{
		Store: h.st, Now: func() time.Time { return now }, NewToken: func() string { return "report-claim" },
	}))

	if result, err := h.svc.ClearContext(ctx, testSession); err != nil || result.Memo.Status != projectmemo.ClearSaved {
		t.Fatalf("ClearContext = %+v, %v", result, err)
	}
	sent := h.conv.sentMessages()
	if strings.Contains(sent[len(sent)-1].Text, "<ao-worker-reports>") {
		t.Fatalf("memo request carried worker reports: %q", sent[len(sent)-1].Text)
	}
	report, found, err := h.st.GetReport(ctx, "pending-report")
	if err != nil || !found || report.DeliveryState != domain.ReportPending {
		t.Fatalf("report after clear = %+v, found=%v, err=%v; want still pending", report, found, err)
	}

	h.send(t, "B", domain.MessageOriginHuman)
	fresh := awaitSent(t, h.drv.latest("thread-fresh-2"), 1)
	if !strings.Contains(fresh[0].Text, "<ao-worker-reports>") || !strings.Contains(fresh[0].Text, "pending-report") {
		t.Fatalf("first turn after clear = %q, want the pending report", fresh[0].Text)
	}
	report, _, _ = h.st.GetReport(ctx, "pending-report")
	if report.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("report after first turn = %+v", report)
	}
}

func TestClearContextOnWorkerSkipsTheMemo(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{kind: domain.KindWorker, respond: replyWithMemo("<ao-project-memo>x</ao-project-memo>")})
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)

	result, err := h.svc.ClearContext(context.Background(), testSession)
	if err != nil || !result.Cleared || result.Memo.Status != projectmemo.ClearSkipped {
		t.Fatalf("ClearContext = %+v, %v", result, err)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("worker clear sent extra turns: %v", got)
	}
	if _, err := h.memos.Get(testProject); !errors.Is(err, projectmemo.ErrNotFound) {
		t.Fatalf("worker clear wrote a memo: %v", err)
	}
	h.send(t, "B", domain.MessageOriginHuman)
	requireNoMemoResource(t, awaitSent(t, h.drv.latest("thread-fresh-2"), 1)[0])
}

func TestClearContextOnEmptyOrchestratorSkipsTheMemo(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("<ao-project-memo>x</ao-project-memo>")})
	result, err := h.svc.ClearContext(context.Background(), testSession)
	if err != nil || result.Cleared || result.Memo.Status != projectmemo.ClearSkipped {
		t.Fatalf("ClearContext = %+v, %v", result, err)
	}
	if got := h.conv.sentTexts(); len(got) != 0 {
		t.Fatalf("empty clear sent turns: %v", got)
	}
}

func TestClearContextRefusesBusyOrchestratorWithoutMemoTurn(t *testing.T) {
	h := newMemoHarness(t, memoHarnessOptions{}) // nothing answers: A keeps running
	h.send(t, "A", domain.MessageOriginHuman)
	if _, err := h.svc.ClearContext(context.Background(), testSession); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatalf("ClearContext busy = %v, want ErrTurnRunning", err)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("busy clear sent a memo turn: %v", got)
	}
	// The refused clear left no hold: the next message queues behind A and
	// dispatches when A ends.
	h.send(t, "B", domain.MessageOriginHuman)
	sent := h.conv.sentMessages()
	answerTurn(h.conv, "provider-turn-1", "done", domain.TurnStateCompleted)
	if got := awaitSent(t, h.conv, len(sent)+1); got[len(got)-1].Text != "B" {
		t.Fatalf("queued message after refused clear = %v", h.conv.sentTexts())
	}
}

func TestClearContextFailedStartReturnsHeldMessagesToTheSource(t *testing.T) {
	release := make(chan struct{})
	h := newMemoHarness(t, memoHarnessOptions{
		respond: func(conv *fakeConversation, msg ports.ChatUserMessage, providerTurnID string) {
			if !isMemoRequest(msg) {
				answerTurn(conv, providerTurnID, "reply to "+msg.Text, domain.TurnStateCompleted)
				return
			}
			go func() {
				<-release
				answerTurn(conv, providerTurnID, "<ao-project-memo>memo</ao-project-memo>", domain.TurnStateCompleted)
			}()
		},
	})
	ctx := context.Background()
	first := h.send(t, "A", domain.MessageOriginHuman)
	h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
	h.drv.mu.Lock()
	h.drv.startErr = errors.New("provider unavailable")
	h.drv.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := h.svc.ClearContext(ctx, testSession)
		done <- err
	}()
	awaitSent(t, h.conv, 2)
	h.send(t, "while memo", domain.MessageOriginHuman)
	close(release)
	if err := <-done; err == nil {
		t.Fatal("ClearContext succeeded after Start failure")
	}
	// The source thread is resumed, and the held message runs there.
	restored := h.drv.latest("thread-1")
	if restored == h.conv {
		t.Fatal("source was not restored")
	}
	sent := awaitSent(t, restored, 1)
	if sent[0].Text != "while memo" {
		t.Fatalf("restored source got %v", restored.sentTexts())
	}
	// Same provider conversation, memo unchanged since it last read it? It
	// never read this revision (the memo step saved a new one), so it gets it.
	requireMemoResource(t, sent[0], "memo")
}

func TestProjectMemoInjection(t *testing.T) {
	t.Run("first turn of a provider conversation only", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("")})
		h.saveMemo(t, "standing memo", projectmemo.SourceUser)
		first := h.send(t, "A", domain.MessageOriginHuman)
		h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
		h.send(t, "B", domain.MessageOriginHuman)
		sent := awaitSent(t, h.conv, 2)
		resource := requireMemoResource(t, sent[0], "standing memo")
		if !strings.Contains(resource.Text, "Written by: the developer") || strings.Contains(resource.Text, "this memo is old") {
			t.Fatalf("memo resource text = %q", resource.Text)
		}
		requireNoMemoResource(t, sent[1])
	})

	t.Run("an edited memo reaches the next turn", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("")})
		h.saveMemo(t, "first memo", projectmemo.SourceUser)
		first := h.send(t, "A", domain.MessageOriginHuman)
		h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
		h.saveMemo(t, "edited memo", projectmemo.SourceUser)
		second := h.send(t, "B", domain.MessageOriginHuman)
		h.awaitTurnState(t, second.ID, domain.TurnStateCompleted)
		h.send(t, "C", domain.MessageOriginHuman)
		sent := awaitSent(t, h.conv, 3)
		requireMemoResource(t, sent[1], "edited memo")
		requireNoMemoResource(t, sent[2])
	})

	t.Run("stale memo says so", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("")})
		old := h.now().Add(-8 * 24 * time.Hour)
		if _, err := projectmemo.New(h.dataDir).WithClock(func() time.Time { return old }).Save(testProject,
			projectmemo.SaveInput{Body: "old memo", Source: projectmemo.SourceClear}); err != nil {
			t.Fatal(err)
		}
		h.send(t, "A", domain.MessageOriginHuman)
		resource := requireMemoResource(t, awaitSent(t, h.conv, 1)[0], "old memo")
		if !strings.Contains(resource.Text, "this memo is old (8 days)") ||
			!strings.Contains(resource.Text, "Written by: the orchestrator, before /clear") {
			t.Fatalf("stale memo resource = %q", resource.Text)
		}
	})

	t.Run("provider without embedded context", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{noEmbeddedCtx: true, respond: replyWithMemo("")})
		h.saveMemo(t, "memo", projectmemo.SourceUser)
		h.send(t, "A", domain.MessageOriginHuman)
		requireNoMemoResource(t, awaitSent(t, h.conv, 1)[0])
	})

	t.Run("workers never get it", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{kind: domain.KindWorker, respond: replyWithMemo("")})
		h.saveMemo(t, "memo", projectmemo.SourceUser)
		h.send(t, "A", domain.MessageOriginHuman)
		requireNoMemoResource(t, awaitSent(t, h.conv, 1)[0])
	})

	t.Run("restore after the session ends", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("")})
		h.saveMemo(t, "memo", projectmemo.SourceUser)
		first := h.send(t, "A", domain.MessageOriginHuman)
		h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
		if err := h.svc.Stop(context.Background(), testSession); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		cfg := h.cfg
		cfg.ProviderConversationID = "thread-1"
		ctrl, err := h.svc.Start(context.Background(), cfg)
		if err != nil {
			t.Fatalf("restore Start: %v", err)
		}
		h.ctrl = ctrl
		restored := h.drv.latest("thread-1")
		if restored == h.conv {
			t.Fatal("restore reused the stopped conversation")
		}
		h.send(t, "after restore", domain.MessageOriginHuman)
		requireMemoResource(t, awaitSent(t, restored, 1)[0], "memo")
	})

	t.Run("daemon restart", func(t *testing.T) {
		h := newMemoHarness(t, memoHarnessOptions{respond: replyWithMemo("")})
		h.saveMemo(t, "memo", projectmemo.SourceUser)
		first := h.send(t, "A", domain.MessageOriginHuman)
		h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
		h.svc.StopAll(context.Background())

		restarted := h.newService(t)
		cfg := h.cfg
		cfg.ProviderConversationID = "thread-1"
		if _, err := restarted.Start(context.Background(), cfg); err != nil {
			t.Fatalf("Start after restart: %v", err)
		}
		t.Cleanup(func() { _ = restarted.Stop(context.Background(), testSession) })
		if _, err := restarted.Send(context.Background(), testSession, ports.ChatUserMessage{
			Text: "after restart", Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatalf("Send after restart: %v", err)
		}
		requireMemoResource(t, awaitSent(t, h.drv.latest("thread-1"), 1)[0], "memo")
	})

	t.Run("resuming the same provider conversation", func(t *testing.T) {
		// A failed /clear reopens the source thread with a new controller,
		// the way a wake does. The memo did not change, so it is not resent.
		h := newMemoHarness(t, memoHarnessOptions{
			respond: replyWithMemo("no block"), budget: time.Second, interruptWait: time.Second,
		})
		h.saveMemo(t, "memo", projectmemo.SourceUser)
		first := h.send(t, "A", domain.MessageOriginHuman)
		h.awaitTurnState(t, first.ID, domain.TurnStateCompleted)
		h.drv.mu.Lock()
		h.drv.startErr = errors.New("provider unavailable")
		h.drv.mu.Unlock()
		if _, err := h.svc.ClearContext(context.Background(), testSession); err == nil {
			t.Fatal("ClearContext succeeded after Start failure")
		}
		restored := h.drv.latest("thread-1")
		if restored == h.conv {
			t.Fatal("source was not restored")
		}
		h.send(t, "after resume", domain.MessageOriginHuman)
		requireNoMemoResource(t, awaitSent(t, restored, 1)[0])
		requireMemoResource(t, h.conv.sentMessages()[0], "memo")
	})
}
