package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// ladoTimingsTables are the tables 0011 creates.
var ladoTimingsTables = []string{"lado_execution_timings", "lado_session_metrics"}

// A fresh database has both tables, empty, and their checks hold.
func TestLadoMigrate0011CreatesTimingTables(t *testing.T) {
	db := openLadoDatabase(t, 1)
	for _, table := range ladoTimingsTables {
		if !tableExists(t, db, table) {
			t.Fatalf("table %s is missing", table)
		}
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want || got < 11 {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_execution_timings`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("execution timings on a fresh database = %d, %v; want none", rows, err)
	}
	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO lado_execution_timings (session_id, pr_url, pr_seen_at) VALUES ('session', 'https://github.com/o/r/pull/1', CURRENT_TIMESTAMP)`)
	if _, err := db.Exec(`INSERT INTO lado_execution_timings (session_id) VALUES ('missing')`); err == nil {
		t.Fatal("timings of no execution were accepted")
	}
	if _, err := db.Exec(`UPDATE lado_execution_timings SET verified_estimated = 2`); err == nil {
		t.Fatal("an estimate flag other than 0 or 1 was accepted")
	}
	mustExec(t, db, `INSERT INTO lado_session_metrics (session_id, task_id, role, updated_at) VALUES ('session', 'task', 'worker', CURRENT_TIMESTAMP)`)
	for name, query := range map[string]string{
		"an unknown role":          `INSERT INTO lado_session_metrics (session_id, task_id, role, updated_at) VALUES ('s2', 'task', 'orchestrator', CURRENT_TIMESTAMP)`,
		"an empty session":         `INSERT INTO lado_session_metrics (session_id, task_id, role, updated_at) VALUES ('', 'task', 'worker', CURRENT_TIMESTAMP)`,
		"metrics of no task":       `INSERT INTO lado_session_metrics (session_id, task_id, role, updated_at) VALUES ('s3', 'missing', 'worker', CURRENT_TIMESTAMP)`,
		"a negative investigation": `INSERT INTO lado_session_metrics (session_id, task_id, role, investigate_ms, updated_at) VALUES ('s4', 'task', 'worker', -1, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// An upgraded database estimates the times of executions that already have a
// PR: the PR was seen at the first check run, else at the first verification
// record of any head, else at the first review round, and verified at the
// first passing or confirming record. An execution without a PR gets no row.
// The down migration drops both tables and the round trip estimates again.
func TestLadoMigrate0011EstimatesEarlierExecutionsAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openLadoDatabase(t, 1)
	provider := ladoMigrationProvider(t, db)
	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatalf("down to 0010: %v", err)
	}
	for _, table := range ladoTimingsTables {
		if tableExists(t, db, table) {
			t.Fatalf("table %s survived the 0011 down migration", table)
		}
	}

	insertLadoExecutionFixture(t, db)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/p', CURRENT_TIMESTAMP)`)
	otherHead := strings.Repeat("e", 40)
	hash := strings.Repeat("c", 64)
	execution := func(session string) {
		mustExec(t, db, `INSERT INTO lado_executions (session_id, task_id, queue_entry_id, plan_revision_id, plan_approval_id, policy_snapshot_id, attempt, started_at, role, part_id)
			VALUES (?, 'task', 'entry', 'revision', 'approval', 'snapshot', 1, '2026-10-20 09:00:00+00:00', 'part', ?)`, session, session)
	}
	withPR := func(session string, number int) {
		mustExec(t, db, `INSERT INTO sessions (id, project_id, num, created_at, updated_at, activity_last_at)
			VALUES (?, 'p', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, session, number)
		mustExec(t, db, `INSERT INTO pr (url, session_id, number, pr_state, head_sha, updated_at)
			VALUES (?, ?, ?, 'open', ?, '2026-10-20 12:00:00+00:00')`, "https://github.com/o/r/pull/"+session, session, number, ladoVerifyHead)
	}
	checkRun := func(session, id, at string) {
		mustExec(t, db, `INSERT INTO lado_check_runs (id, execution_session_id, head_sha, name, command, status, started_at)
			VALUES (?, ?, ?, 'test', 'go test', 'passed', ?)`, id, session, ladoVerifyHead, at)
	}
	record := func(session, head, outcome, at string) {
		mustExec(t, db, `INSERT INTO lado_verifications (execution_session_id, head_sha, pr_url, outcome, content_json, sha256, computed_at)
			VALUES (?, ?, ?, ?, '{}', ?, ?)`, session, head, "https://github.com/o/r/pull/record-"+session, outcome, hash, at)
	}
	review := func(session, id, at string) {
		mustExec(t, db, `INSERT INTO lado_execution_reviews (review_run_id, execution_session_id, head_sha, round, created_at)
			VALUES (?, ?, ?, 1, ?)`, id, session, ladoVerifyHead, at)
	}

	// checks: the first check run wins over records and reviews, and the pr
	// row names the PR.
	execution("checks")
	withPR("checks", 1)
	checkRun("checks", "run-2", "2026-10-20 10:20:00+00:00")
	checkRun("checks", "run-1", "2026-10-20 10:10:00+00:00")
	record("checks", ladoVerifyHead, "wait", "2026-10-20 10:05:00+00:00")
	record("checks", otherHead, "pass", "2026-10-20 10:40:00+00:00")
	review("checks", "review-checks", "2026-10-20 10:01:00+00:00")
	// records: no check run, so the first record of any head; it verified at
	// its confirming record. Without a pr row the record names the PR.
	execution("records")
	record("records", ladoVerifyHead, "confirm", "2026-10-20 11:30:00+00:00")
	record("records", otherHead, "wait", "2026-10-20 11:15:00+00:00")
	review("records", "review-records", "2026-10-20 11:00:00+00:00")
	// reviews: a PR with only a review round.
	execution("reviews")
	withPR("reviews", 2)
	review("reviews", "review-reviews", "2026-10-20 12:30:00+00:00")
	// bare: a PR and nothing else; failing records never verify.
	execution("bare")
	withPR("bare", 3)
	// The fixture's own execution has no PR and no record.

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0011: %v", err)
	}
	at := func(clock string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", "2026-10-20 "+clock)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		session, url   string
		seen, verified time.Time
	}{
		{session: "checks", url: "https://github.com/o/r/pull/checks", seen: at("10:10"), verified: at("10:40")},
		{session: "records", url: "https://github.com/o/r/pull/record-records", seen: at("11:15"), verified: at("11:30")},
		{session: "reviews", url: "https://github.com/o/r/pull/reviews", seen: at("12:30")},
		{session: "bare", url: "https://github.com/o/r/pull/bare"},
	} {
		var url string
		var seen, verified sql.NullTime
		var seenEstimated, verifiedEstimated int
		if err := db.QueryRow(`SELECT pr_url, pr_seen_at, pr_seen_estimated, verified_at, verified_estimated FROM lado_execution_timings WHERE session_id = ?`, tc.session).
			Scan(&url, &seen, &seenEstimated, &verified, &verifiedEstimated); err != nil {
			t.Fatalf("%s: read timings: %v", tc.session, err)
		}
		if url != tc.url {
			t.Fatalf("%s: pr_url = %q, want %q", tc.session, url, tc.url)
		}
		checkEstimate := func(name string, got sql.NullTime, estimated int, want time.Time) {
			if want.IsZero() {
				if got.Valid || estimated != 0 {
					t.Fatalf("%s: %s = %v (estimated %d), want none", tc.session, name, got, estimated)
				}
				return
			}
			if !got.Valid || !got.Time.Equal(want) || estimated != 1 {
				t.Fatalf("%s: %s = %v (estimated %d), want %v estimated", tc.session, name, got, estimated, want)
			}
		}
		checkEstimate("pr_seen_at", seen, seenEstimated, tc.seen)
		checkEstimate("verified_at", verified, verifiedEstimated, tc.verified)
	}
	var none int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_execution_timings WHERE session_id = 'session'`).Scan(&none); err != nil || none != 0 {
		t.Fatalf("timings of an execution without a PR = %d, %v; want none", none, err)
	}

	if _, err := provider.DownTo(ctx, 10); err != nil {
		t.Fatalf("down to 0010 again: %v", err)
	}
	for _, table := range ladoTimingsTables {
		if tableExists(t, db, table) {
			t.Fatalf("table %s survived the 0011 down migration", table)
		}
	}
	var executions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_executions`).Scan(&executions); err != nil || executions != 5 {
		t.Fatalf("executions after the down migration = %d, %v; want all 5 kept", executions, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var estimated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lado_execution_timings WHERE pr_seen_estimated = 1`).Scan(&estimated); err != nil || estimated != 3 {
		t.Fatalf("estimated PR times after the round trip = %d, %v; want 3", estimated, err)
	}
	if got, want := appliedVersion(t, db, ladoVersionTable), latestLadoMigration(t); got != want {
		t.Fatalf("%s = %d, want %d", ladoVersionTable, got, want)
	}
}
