package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var ladoLogAt = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// openLadoLogWatchStore opens a store with project mer registered and a raw
// connection to read change_log.
func openLadoLogWatchStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	s, raw := openLadoQueueStore(t)
	seedProject(t, s, "mer")
	return s, raw
}

// ladoLogWatchFixture is project mer's settings with two sources, with
// documentation addresses only.
func ladoLogWatchFixture() domain.LadoLogWatchSettings {
	s := domain.DefaultLadoLogWatchSettings("mer")
	s.Enabled = true
	s.Repository = "github:acme/demo"
	s.Include = []string{`(?i)timeout`}
	s.HourlyIssueCap = 7
	s.AutoPlanMinCount = 2
	s.UpdatedAt = ladoLogAt
	s.Sources = []domain.LadoLogSource{
		{
			ID: "src-1", Kind: domain.LadoLogSourceKindDockerTCP, DisplayName: "prod1",
			Endpoint: "tcp://192.0.2.10:2375", CertKey: "key-1",
			Targets: []domain.LadoLogTarget{
				{ID: "tgt-1", Kind: domain.LadoLogTargetComposeProject, Value: "grp"},
				{ID: "tgt-2", Kind: domain.LadoLogTargetComposeService, Value: "api", ComposeProject: "grp", Service: "grp/api", Paused: true},
			},
		},
		{
			ID: "src-2", DisplayName: "prod2", Endpoint: "tcp://docker.example.test:2376", TLS: true, Paused: true,
			Targets: []domain.LadoLogTarget{{ID: "tgt-3", Kind: domain.LadoLogTargetName, Value: "worker"}},
		},
	}
	return s
}

// wantLadoLogWatchStored is how settings read back: the default kind, the
// source of each target, UTC times.
func wantLadoLogWatchStored(s domain.LadoLogWatchSettings) domain.LadoLogWatchSettings {
	s.UpdatedAt = s.UpdatedAt.UTC()
	out := make([]domain.LadoLogSource, 0, len(s.Sources))
	for _, src := range s.Sources {
		if src.Kind == "" {
			src.Kind = domain.LadoLogSourceKindDockerTCP
		}
		targets := make([]domain.LadoLogTarget, 0, len(src.Targets))
		for _, target := range src.Targets {
			target.SourceID = src.ID
			targets = append(targets, target)
		}
		src.Targets = targets
		out = append(out, src)
	}
	s.Sources = out
	return s
}

func mustReadLadoLogWatch(t *testing.T, s *sqlite.Store, project domain.ProjectID) domain.LadoLogWatchSettings {
	t.Helper()
	got, ok, err := s.LadoLogWatchSettings(context.Background(), project)
	if err != nil || !ok {
		t.Fatalf("LadoLogWatchSettings %s = (%v, %v)", project, ok, err)
	}
	return got
}

// Settings read back as saved; a later save removes the sources and targets
// it no longer has, with their cursors, and keeps what the server set.
func TestLadoLogWatchSettingsInTheStore(t *testing.T) {
	s, _ := openLadoLogWatchStore(t)
	ctx := context.Background()
	if _, ok, err := s.LadoLogWatchSettings(ctx, "mer"); ok || err != nil {
		t.Fatalf("settings before a save = (%v, %v), want none", ok, err)
	}
	first := ladoLogWatchFixture()
	if removed, err := s.PutLadoLogWatchSettings(ctx, first); err != nil || len(removed) != 0 {
		t.Fatalf("PutLadoLogWatchSettings = (%v, %v)", removed, err)
	}
	if got, want := mustReadLadoLogWatch(t, s, "mer"), wantLadoLogWatchStored(first); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v\nwant       %+v", got, want)
	}
	enabled, err := s.ListEnabledLadoLogWatchSettings(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ProjectID != "mer" || len(enabled[0].Sources) != 2 {
		t.Fatalf("ListEnabledLadoLogWatchSettings = (%+v, %v)", enabled, err)
	}

	down := ladoLogAt.Add(time.Minute)
	if err := s.SetLadoLogSourceDown(ctx, "src-1", down, down.Add(10*time.Minute)); err != nil {
		t.Fatalf("SetLadoLogSourceDown: %v", err)
	}
	if err := s.SetLadoLogBurstNotified(ctx, "mer", down); err != nil {
		t.Fatalf("SetLadoLogBurstNotified: %v", err)
	}
	if err := s.SetLadoLogSourceDown(ctx, "src-missing", down, time.Time{}); !errors.Is(err, domain.ErrLadoLogSourceNotFound) {
		t.Fatalf("SetLadoLogSourceDown of an unknown source = %v", err)
	}
	if err := s.SetLadoLogSourceCertKey(ctx, "src-2", "key-2"); err != nil {
		t.Fatalf("SetLadoLogSourceCertKey: %v", err)
	}
	if project, ok, err := s.LadoLogSourceProject(ctx, "src-2"); project != "mer" || !ok || err != nil {
		t.Fatalf("LadoLogSourceProject = (%q, %v, %v)", project, ok, err)
	}
	for _, target := range []domain.LadoLogTargetID{"tgt-1", "tgt-2"} {
		if err := s.PutLadoLogCursor(ctx, domain.LadoLogCursor{TargetID: target, Instance: "grp-api-1", Since: ladoLogAt}); err != nil {
			t.Fatalf("PutLadoLogCursor %s: %v", target, err)
		}
	}

	// The second save drops src-2 and tgt-2 (which has a cursor), moves
	// tgt-3 to src-1 and adds src-3; the server's values stay.
	second := ladoLogWatchFixture()
	second.Enabled = false
	second.Exclude = []string{"healthcheck"}
	second.UpdatedAt = ladoLogAt.Add(time.Hour)
	second.Sources[0].DisplayName = "prod-1"
	second.Sources[0].CertKey = "ignored"
	second.Sources[0].Targets = []domain.LadoLogTarget{
		{ID: "tgt-3", Kind: domain.LadoLogTargetLabel, Value: "com.example.watch=1"},
		{ID: "tgt-1", Kind: domain.LadoLogTargetComposeProject, Value: "grp2"},
	}
	second.Sources[1] = domain.LadoLogSource{ID: "src-3", DisplayName: "prod3", Endpoint: "tcp://198.51.100.7:2375", CertKey: "key-3"}
	removed, err := s.PutLadoLogWatchSettings(ctx, second)
	if err != nil || !slices.Equal(removed, []domain.LadoLogSourceID{"src-2"}) {
		t.Fatalf("second PutLadoLogWatchSettings = (%v, %v), want src-2 removed", removed, err)
	}
	want := wantLadoLogWatchStored(second)
	want.BurstNotifiedAt = down
	want.Sources[0].CertKey = "key-1"
	want.Sources[0].DownSince, want.Sources[0].DownNotifiedAt = down, down.Add(10*time.Minute)
	want.Sources[1].Targets = []domain.LadoLogTarget{}
	if got := mustReadLadoLogWatch(t, s, "mer"); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings after the second save = %+v\nwant %+v", got, want)
	}
	if _, ok, err := s.LadoLogCursor(ctx, "tgt-2", "grp-api-1"); ok || err != nil {
		t.Fatalf("cursor of the removed target = (%v, %v), want gone", ok, err)
	}
	cursor, ok, err := s.LadoLogCursor(ctx, "tgt-1", "grp-api-1")
	if err != nil || !ok || !cursor.Since.Equal(ladoLogAt) {
		t.Fatalf("cursor of the kept target = (%+v, %v, %v)", cursor, ok, err)
	}
	if err := s.PutLadoLogCursor(ctx, domain.LadoLogCursor{TargetID: "tgt-2", Instance: "grp-api-1", Since: ladoLogAt}); err != nil {
		t.Fatalf("cursor of a removed target = %v, want dropped without an error", err)
	}
	if _, ok, _ := s.LadoLogCursor(ctx, "tgt-2", "grp-api-1"); ok {
		t.Fatal("a cursor of a removed target was stored")
	}
	if enabled, err := s.ListEnabledLadoLogWatchSettings(ctx); err != nil || len(enabled) != 0 {
		t.Fatalf("enabled settings after turning it off = (%+v, %v)", enabled, err)
	}

	// Another project cannot take a source or a target id; nothing it
	// tried stays.
	seedProject(t, s, "other")
	other := domain.DefaultLadoLogWatchSettings("other")
	other.Sources = []domain.LadoLogSource{{ID: "src-1", DisplayName: "x", Endpoint: "tcp://192.0.2.99:2375"}}
	if _, err := s.PutLadoLogWatchSettings(ctx, other); err == nil {
		t.Fatal("another project took source src-1")
	}
	other.Sources = []domain.LadoLogSource{{ID: "src-9", DisplayName: "x", Endpoint: "tcp://192.0.2.99:2375",
		Targets: []domain.LadoLogTarget{{ID: "tgt-1", Kind: domain.LadoLogTargetName, Value: "x"}}}}
	if _, err := s.PutLadoLogWatchSettings(ctx, other); err == nil {
		t.Fatal("another project took target tgt-1")
	}
	if _, ok, err := s.LadoLogWatchSettings(ctx, "other"); ok || err != nil {
		t.Fatalf("settings of the refused project = (%v, %v), want none", ok, err)
	}
	if got := mustReadLadoLogWatch(t, s, "mer"); !reflect.DeepEqual(got, want) {
		t.Fatalf("mer's settings changed by another project's save: %+v", got)
	}
}

// Occurrences create a fingerprint and add up per instance; a task that
// never got its issue can still be deleted, and its fingerprint loses it.
func TestLadoLogFingerprintOccurrencesAndPendingTask(t *testing.T) {
	s, _ := openLadoLogWatchStore(t)
	ctx := context.Background()
	delta := domain.LadoLogOccurrenceDelta{
		Project: "mer", Fingerprint: "fp-1", Service: "grp/api", Kind: "java.lang.IllegalStateException",
		Location: "com.example.Api.run", Instance: "prod1/grp-api-1", Image: "app:1.2", Delta: 2,
		FirstSeen: ladoLogAt, LastSeen: ladoLogAt.Add(time.Second),
	}
	if _, err := s.AddLadoLogOccurrences(ctx, delta); err != nil {
		t.Fatalf("AddLadoLogOccurrences: %v", err)
	}
	second := delta
	second.Instance, second.Image, second.Delta = "prod2/grp-api-1", "app:1.3", 1
	second.FirstSeen, second.LastSeen = ladoLogAt.Add(-time.Minute), ladoLogAt.Add(time.Minute)
	if _, err := s.AddLadoLogOccurrences(ctx, second); err != nil {
		t.Fatalf("AddLadoLogOccurrences second: %v", err)
	}
	// An older batch of the first instance adds its count, keeping the
	// newer image and time.
	older := delta
	older.Delta, older.Image, older.FirstSeen, older.LastSeen = 1, "app:1.0", time.Time{}, ladoLogAt
	f, err := s.AddLadoLogOccurrences(ctx, older)
	if err != nil {
		t.Fatalf("AddLadoLogOccurrences older: %v", err)
	}
	if f.ID == 0 || f.Count != 4 || !f.FirstSeenAt.Equal(ladoLogAt.Add(-time.Minute)) || !f.LastSeenAt.Equal(ladoLogAt.Add(time.Minute)) ||
		f.Service != "grp/api" || f.ExceptionKind != delta.Kind || f.Location != delta.Location || f.TaskID != "" {
		t.Fatalf("fingerprint = %+v", f)
	}
	wantInstances := []domain.LadoLogInstanceCount{
		{Instance: "prod1/grp-api-1", Image: "app:1.2", Count: 3, LastSeenAt: ladoLogAt.Add(time.Second)},
		{Instance: "prod2/grp-api-1", Image: "app:1.3", Count: 1, LastSeenAt: ladoLogAt.Add(time.Minute)},
	}
	if !reflect.DeepEqual(f.Instances, wantInstances) {
		t.Fatalf("instances = %+v, want %+v", f.Instances, wantInstances)
	}
	if got, ok, err := s.LadoLogFingerprint(ctx, "mer", "fp-1"); !ok || err != nil || !reflect.DeepEqual(got, f) {
		t.Fatalf("LadoLogFingerprint = (%+v, %v, %v)", got, ok, err)
	}
	if _, ok, err := s.LadoLogFingerprint(ctx, "other", "fp-1"); ok || err != nil {
		t.Fatalf("fingerprint of another project = (%v, %v), want none", ok, err)
	}
	if _, err := s.AddLadoLogOccurrences(ctx, domain.LadoLogOccurrenceDelta{Project: "mer", Fingerprint: "fp-x", Delta: -1, LastSeen: ladoLogAt}); err == nil {
		t.Fatal("a negative delta was accepted")
	}

	task := pendingLadoTask("lado-task-log")
	task.Source = domain.LadoTaskSourceLog
	mustCreateLadoTask(t, s, task)
	if err := s.LinkLadoLogFingerprintTask(ctx, f.ID, task.ID, "", "", ladoLogAt); err != nil {
		t.Fatalf("LinkLadoLogFingerprintTask: %v", err)
	}
	if err := s.DeletePendingLadoTask(ctx, task.ID); err != nil {
		t.Fatalf("DeletePendingLadoTask of a task a fingerprint names: %v", err)
	}
	if _, ok, err := s.LadoTask(ctx, task.ID); ok || err != nil {
		t.Fatalf("task after the delete = (%v, %v), want gone", ok, err)
	}
	got, ok, err := s.LadoLogFingerprintByID(ctx, f.ID)
	if err != nil || !ok || got.TaskID != "" || got.IssueRef != "" || got.Count != 4 || len(got.Instances) != 2 {
		t.Fatalf("fingerprint after the delete = (%+v, %v, %v), want it without a task", got, ok, err)
	}
	if err := s.LinkLadoLogFingerprintTask(ctx, 999, task.ID, "", "", ladoLogAt); !errors.Is(err, domain.ErrLadoLogFingerprintNotFound) {
		t.Fatalf("link of an unknown fingerprint = %v", err)
	}
}

// Linking, plan requests and the ignore list are recorded and counted, and
// the incident list puts the live tasks' fingerprints first.
func TestLadoLogFingerprintLinksAndIncidents(t *testing.T) {
	s, _ := openLadoLogWatchStore(t)
	ctx := context.Background()
	add := func(fp string, last time.Time) domain.LadoLogFingerprint {
		t.Helper()
		f, err := s.AddLadoLogOccurrences(ctx, domain.LadoLogOccurrenceDelta{
			Project: "mer", Fingerprint: fp, Service: "api", Kind: "Boom", Instance: "prod1/api-1", Delta: 1, LastSeen: last,
		})
		if err != nil {
			t.Fatalf("AddLadoLogOccurrences %s: %v", fp, err)
		}
		return f
	}
	live := add("fp-live", ladoLogAt)
	cancelled := add("fp-cancelled", ladoLogAt.Add(3*time.Minute))
	fresh := add("fp-fresh", ladoLogAt.Add(2*time.Minute))
	old := add("fp-old", ladoLogAt.Add(time.Minute))

	liveTask := pendingLadoTask("lado-task-live")
	liveTask.Source = domain.LadoTaskSourceLog
	mustCreateLadoTask(t, s, liveTask)
	if err := s.LinkLadoLogFingerprintTask(ctx, live.ID, liveTask.ID, "", "", ladoLogAt); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLadoLogAutoPlan(ctx, live.ID, ladoLogAt.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
	// Confirming the issue later keeps when the creation started and the
	// plan request.
	if err := s.LinkLadoLogFingerprintTask(ctx, live.ID, liveTask.ID, "github:acme/demo#12", "github:acme/demo#3", ladoLogAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.LadoLogFingerprintByID(ctx, live.ID)
	if got.TaskID != liveTask.ID || got.IssueRef != "github:acme/demo#12" || got.PreviousIssueRef != "github:acme/demo#3" ||
		!got.IssueCreatedAt.Equal(ladoLogAt) || !got.AutoPlanRequestedAt.Equal(ladoLogAt.Add(time.Second)) {
		t.Fatalf("linked fingerprint = %+v", got)
	}

	cancelledTask := importedLadoTask("lado-task-cancelled", "github:acme/demo#20")
	mustCreateLadoTask(t, s, cancelledTask)
	if _, _, err := s.CancelLadoTask(ctx, cancelledTask.ID, ladoLogAt); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkLadoLogFingerprintTask(ctx, cancelled.ID, cancelledTask.ID, "github:acme/demo#20", "", ladoLogAt.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLadoLogAutoPlan(ctx, cancelled.ID, time.Time{}, domain.LadoLogAutoPlanHeldCap); err != nil {
		t.Fatal(err)
	}
	// A new task for the fingerprint starts over: a new creation time, no
	// plan request yet.
	recurTask := pendingLadoTask("lado-task-recur")
	mustCreateLadoTask(t, s, recurTask)
	if err := s.LinkLadoLogFingerprintTask(ctx, cancelled.ID, recurTask.ID, "", "github:acme/demo#20", ladoLogAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.LadoLogFingerprintByID(ctx, cancelled.ID)
	if got.TaskID != recurTask.ID || got.IssueRef != "" || got.PreviousIssueRef != "github:acme/demo#20" ||
		!got.IssueCreatedAt.Equal(ladoLogAt.Add(time.Minute)) || got.AutoPlanHeld != "" || !got.AutoPlanRequestedAt.IsZero() {
		t.Fatalf("relinked fingerprint = %+v", got)
	}
	if err := s.ClearLadoLogFingerprintTask(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.LadoLogFingerprintByID(ctx, cancelled.ID)
	if got.TaskID != "" || got.IssueRef != "" || got.PreviousIssueRef != "github:acme/demo#20" {
		t.Fatalf("cleared fingerprint = %+v", got)
	}
	if err := s.LinkLadoLogFingerprintTask(ctx, cancelled.ID, cancelledTask.ID, "github:acme/demo#20", "", ladoLogAt.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if n, err := s.CountLadoLogIssuesSince(ctx, "mer", ladoLogAt.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountLadoLogIssuesSince = (%d, %v), want 1", n, err)
	}
	if n, err := s.CountLadoLogIssuesSince(ctx, "mer", ladoLogAt.Add(-3*time.Hour)); err != nil || n != 2 {
		t.Fatalf("CountLadoLogIssuesSince over 3 hours = (%d, %v), want 2", n, err)
	}
	if n, err := s.CountLadoLogAutoPlansSince(ctx, "mer", ladoLogAt); err != nil || n != 1 {
		t.Fatalf("CountLadoLogAutoPlansSince = (%d, %v), want 1", n, err)
	}
	if n, err := s.CountLadoLogAutoPlansSince(ctx, "other", ladoLogAt); err != nil || n != 0 {
		t.Fatalf("CountLadoLogAutoPlansSince of another project = (%d, %v), want 0", n, err)
	}

	ignored, err := s.SetLadoLogFingerprintIgnored(ctx, old.ID, true, ladoLogAt)
	if err != nil || !ignored.Ignored || !ignored.IgnoredAt.Equal(ladoLogAt) {
		t.Fatalf("SetLadoLogFingerprintIgnored = (%+v, %v)", ignored, err)
	}
	if _, err := s.SetLadoLogFingerprintIgnored(ctx, 999, true, ladoLogAt); !errors.Is(err, domain.ErrLadoLogFingerprintNotFound) {
		t.Fatalf("ignore of an unknown fingerprint = %v", err)
	}

	ids := func(fs []domain.LadoLogFingerprint) []domain.LadoLogFingerprintID {
		out := []domain.LadoLogFingerprintID{}
		for _, f := range fs {
			out = append(out, f.ID)
		}
		return out
	}
	incidents, err := s.ListLadoLogIncidents(ctx, "mer", 1)
	if err != nil {
		t.Fatal(err)
	}
	// The live task's fingerprint, then the newest of the rest: the
	// cancelled task's is newer than fp-fresh.
	if got, want := ids(incidents), []domain.LadoLogFingerprintID{live.ID, cancelled.ID}; !slices.Equal(got, want) {
		t.Fatalf("incidents = %v, want %v", got, want)
	}
	if len(incidents[0].Instances) != 1 || incidents[0].Instances[0].Instance != "prod1/api-1" {
		t.Fatalf("incident instances = %+v", incidents[0].Instances)
	}
	all, err := s.ListLadoLogIncidents(ctx, "mer", 200)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), []domain.LadoLogFingerprintID{live.ID, cancelled.ID, fresh.ID, old.ID}; !slices.Equal(got, want) {
		t.Fatalf("all incidents = %v, want %v", got, want)
	}
	if !all[3].Ignored {
		t.Fatal("the ignored fingerprint reads as not ignored")
	}
}

// A task keeps its source; the log watch's change rows name the fingerprint
// and the settings.
func TestLadoLogWatchTaskSourceAndChanges(t *testing.T) {
	s, raw := openLadoLogWatchStore(t)
	ctx := context.Background()
	logTask := importedLadoTask("lado-task-log", "github:acme/demo#5")
	logTask.Source = domain.LadoTaskSourceLog
	created := mustCreateLadoTask(t, s, logTask)
	if created.Source != domain.LadoTaskSourceLog {
		t.Fatalf("created source = %q", created.Source)
	}
	mustCreateLadoTask(t, s, importedLadoTask("lado-task-dev", "github:acme/demo#6"))
	tasks, err := s.ListLadoTasks(ctx, "mer", false)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("ListLadoTasks = (%d tasks, %v)", len(tasks), err)
	}
	for _, task := range tasks {
		if want := map[domain.LadoTaskID]string{"lado-task-log": domain.LadoTaskSourceLog}[task.ID]; task.Source != want {
			t.Fatalf("task %s source = %q, want %q", task.ID, task.Source, want)
		}
	}
	bad := importedLadoTask("lado-task-bad", "github:acme/demo#7")
	bad.Source = "cron"
	if _, err := s.CreateLadoTask(ctx, bad); err == nil {
		t.Fatal("an unknown source was stored")
	}

	var seq int64
	if err := raw.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM change_log`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	changes := func() []string {
		t.Helper()
		rows, err := raw.Query(`SELECT seq, json_extract(payload, '$.op'), COALESCE(json_extract(payload, '$.fingerprintId'), 0)
			FROM change_log WHERE seq > ? AND project_id = 'mer' AND session_id IS NULL
			AND json_extract(payload, '$.kind') = 'lado_log_watch' ORDER BY seq`, seq)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var op string
			var fp int64
			if err := rows.Scan(&seq, &op, &fp); err != nil {
				t.Fatal(err)
			}
			if fp != 0 {
				op += ":fp"
			}
			out = append(out, op)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := s.PutLadoLogWatchSettings(ctx, ladoLogWatchFixture()); err != nil {
		t.Fatal(err)
	}
	if got := changes(); !slices.Equal(got, []string{"settings"}) {
		t.Fatalf("changes of a settings save = %v, want one settings change", got)
	}
	f, err := s.AddLadoLogOccurrences(ctx, domain.LadoLogOccurrenceDelta{Project: "mer", Fingerprint: "fp", Instance: "prod1/a", Delta: 1, LastSeen: ladoLogAt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLadoLogOccurrences(ctx, domain.LadoLogOccurrenceDelta{Project: "mer", Fingerprint: "fp", Instance: "prod1/a", Delta: 3, LastSeen: ladoLogAt}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutLadoLogCursor(ctx, domain.LadoLogCursor{TargetID: "tgt-1", Instance: "a", Since: ladoLogAt}); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkLadoLogFingerprintTask(ctx, f.ID, created.ID, created.IssueRef, "", ladoLogAt); err != nil {
		t.Fatal(err)
	}
	if got := changes(); !slices.Equal(got, []string{"insert:fp", "update:fp", "update:fp"}) {
		t.Fatalf("changes of a fingerprint = %v", got)
	}
	if err := s.SetLadoLogSourceDown(ctx, "src-1", ladoLogAt, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := changes(); !slices.Equal(got, []string{"settings"}) {
		t.Fatalf("changes of a source going down = %v", got)
	}
}
