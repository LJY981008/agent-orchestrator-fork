package ladologwatch

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// logsTestFrame is one multiplexed frame.
func logsTestFrame(kind byte, payload string) []byte {
	out := make([]byte, 8, 8+len(payload))
	out[0] = kind
	binary.BigEndian.PutUint32(out[4:], uint32(len(payload)))
	return append(out, payload...)
}

type logsTestLine struct {
	Stream string
	TS     time.Time
	Text   string
}

// logsTestRead feeds data through an io.Pipe in pieces of the given sizes
// (the rest in one piece), so that headers, frames and lines cross reads.
func logsTestRead(t *testing.T, data []byte, contentType string, tty bool, pieces ...int) ([]logsTestLine, error) {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		rest := data
		for _, n := range pieces {
			if n > len(rest) {
				n = len(rest)
			}
			if _, err := pw.Write(rest[:n]); err != nil {
				return
			}
			rest = rest[n:]
		}
		for len(rest) > 0 {
			n := min(len(rest), 7)
			if _, err := pw.Write(rest[:n]); err != nil {
				return
			}
			rest = rest[n:]
		}
		_ = pw.Close()
	}()
	var lines []logsTestLine
	err := logsRead(pr, contentType, tty, func(stream string, ts time.Time, text string) {
		lines = append(lines, logsTestLine{Stream: stream, TS: ts, Text: text})
	})
	return lines, err
}

func logsTestTS(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func logsTestJoin(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestLogsMultiplexedFrames(t *testing.T) {
	data := logsTestJoin(
		// Two lines in one stdout frame.
		logsTestFrame(1, "2026-11-10T01:02:03.000000001Z first\n2026-11-10T01:02:03.000000002Z second\n"),
		// A stdout line that crosses two frames, with a stderr line between.
		logsTestFrame(1, "2026-11-10T01:02:04.5Z java.lang.Illegal"),
		logsTestFrame(2, "2026-11-10T01:02:05Z ERROR on stderr\r\n"),
		logsTestFrame(1, "StateException: boom\n"),
		// A stdin frame is not a log line.
		logsTestFrame(0, "ignored input\n"),
		// A line without a timestamp takes the last one.
		logsTestFrame(2, "\tat com.example.App.run(App.java:10)\n"),
		// An empty frame.
		logsTestFrame(1, ""),
	)
	for _, pieces := range [][]int{nil, {3, 5, 1, 9, 2}, {8}, {len(data)}} {
		lines, err := logsTestRead(t, data, logsContentMultiplexed, false, pieces...)
		if err == nil || !strings.Contains(err.Error(), "ignored input") {
			t.Fatalf("pieces %v: err = %v, want the stdin frame in the detail", pieces, err)
		}
		want := []logsTestLine{
			{"stdout", logsTestTS(t, "2026-11-10T01:02:03.000000001Z"), "first"},
			{"stdout", logsTestTS(t, "2026-11-10T01:02:03.000000002Z"), "second"},
			{"stderr", logsTestTS(t, "2026-11-10T01:02:05Z"), "ERROR on stderr"},
			{"stdout", logsTestTS(t, "2026-11-10T01:02:04.5Z"), "java.lang.IllegalStateException: boom"},
			{"stderr", logsTestTS(t, "2026-11-10T01:02:04.5Z"), "\tat com.example.App.run(App.java:10)"},
		}
		logsTestCompare(t, lines, want)
	}
}

func TestLogsSystemErrorFrameIsDetail(t *testing.T) {
	data := logsTestJoin(
		logsTestFrame(1, "2026-11-10T01:00:00Z ok\n"),
		logsTestFrame(3, "error from daemon in stream: log driver gone\n"),
	)
	lines, err := logsTestRead(t, data, logsContentMultiplexed, false)
	var se *logsStreamError
	if !errors.As(err, &se) || !strings.Contains(se.Detail, "log driver gone") {
		t.Fatalf("err = %v, want a stream error with the system-error text", err)
	}
	if len(lines) != 1 || lines[0].Text != "ok" {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestLogsFrameOverOneMiB(t *testing.T) {
	big := strings.Repeat("x", (1<<20)+(512<<10))
	prefix := "2026-11-10T02:00:00Z "
	data := logsTestJoin(
		logsTestFrame(2, prefix+big+"\n2026-11-10T02:00:01Z after\n"),
		logsTestFrame(2, "2026-11-10T02:00:02Z last without newline"),
	)
	lines, err := logsTestRead(t, data, logsContentMultiplexed, false, 4, 100, 70000)
	if err != nil {
		t.Fatalf("logsRead: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0].Stream != "stderr" || len(lines[0].Text) != logsMaxLine-len(prefix) || strings.Trim(lines[0].Text, "x") != "" {
		t.Fatalf("long line = %s %d bytes, want %d x", lines[0].Stream, len(lines[0].Text), logsMaxLine-len(prefix))
	}
	if !lines[0].TS.Equal(logsTestTS(t, "2026-11-10T02:00:00Z")) {
		t.Fatalf("long line ts = %v", lines[0].TS)
	}
	if lines[1].Text != "after" || lines[2].Text != "last without newline" {
		t.Fatalf("lines after the cut = %q, %q", lines[1].Text, lines[2].Text)
	}
}

func TestLogsCutLineKeepsWholeRunes(t *testing.T) {
	prefix := "2026-11-10T02:00:00Z "
	// Put a three-byte rune across the cut.
	text := strings.Repeat("a", logsMaxLine-len(prefix)-1) + "가나다\n"
	lines, err := logsTestRead(t, logsTestFrame(1, prefix+text), logsContentMultiplexed, false)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines = %d, err = %v", len(lines), err)
	}
	if got := lines[0].Text; !strings.HasSuffix(got, "a") || len(got) != logsMaxLine-len(prefix)-1 {
		t.Fatalf("cut line ends %q, %d bytes", got[len(got)-3:], len(got))
	}
}

func TestLogsRawStreams(t *testing.T) {
	raw := []byte("2026-11-10T03:00:00.123456789Z \x1b[31mERROR\x1b[0m tty line\r\n2026-11-10T03:00:01Z panic: boom\nno timestamp here\n")
	want := []logsTestLine{
		{"stdout", logsTestTS(t, "2026-11-10T03:00:00.123456789Z"), "\x1b[31mERROR\x1b[0m tty line"},
		{"stdout", logsTestTS(t, "2026-11-10T03:00:01Z"), "panic: boom"},
		{"stdout", logsTestTS(t, "2026-11-10T03:00:01Z"), "no timestamp here"},
	}
	cases := []struct {
		name        string
		contentType string
		tty         bool
	}{
		{"raw-stream content type", logsContentRaw, false},
		{"tty container, no content type", "", true},
		{"tty container, multiplexed would be wrong", logsContentRaw + "; charset=utf-8", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := logsTestRead(t, raw, tc.contentType, tc.tty, 2, 31)
			if err != nil {
				t.Fatalf("logsRead: %v", err)
			}
			logsTestCompare(t, lines, want)
		})
	}
}

func TestLogsOlderEngineRawHeaderOnMultiplexedStream(t *testing.T) {
	// An Engine before API 1.42 says raw-stream for a multiplexed stream; the
	// first byte of a frame header gives it away.
	data := logsTestFrame(2, "2026-11-10T04:00:00Z ERROR old engine\n")
	for _, ct := range []string{logsContentRaw, ""} {
		lines, err := logsTestRead(t, data, ct, false, 1)
		if err != nil {
			t.Fatalf("%q: %v", ct, err)
		}
		logsTestCompare(t, lines, []logsTestLine{{"stderr", logsTestTS(t, "2026-11-10T04:00:00Z"), "ERROR old engine"}})
	}
}

func TestLogsBrokenStreams(t *testing.T) {
	cut := logsTestFrame(1, "2026-11-10T05:00:00Z whole\n")
	cut = append(cut, logsTestFrame(1, "2026-11-10T05:00:01Z half")[:15]...)
	lines, err := logsTestRead(t, cut, logsContentMultiplexed, false)
	var se *logsStreamError
	if !errors.As(err, &se) {
		t.Fatalf("cut frame: err = %v", err)
	}
	if len(lines) < 1 || lines[0].Text != "whole" {
		t.Fatalf("cut frame lines = %+v", lines)
	}
	bad := append(logsTestFrame(1, "2026-11-10T05:00:00Z ok\n"), 9, 0, 0, 0, 0, 0, 0, 1, 'x')
	if _, err := logsTestRead(t, bad, logsContentMultiplexed, false); !errors.As(err, &se) {
		t.Fatalf("bad header: err = %v", err)
	}
	if lines, err := logsTestRead(t, nil, logsContentMultiplexed, false); err != nil || len(lines) != 0 {
		t.Fatalf("empty stream = %+v, %v", lines, err)
	}
	if lines, err := logsTestRead(t, nil, "", false); err != nil || len(lines) != 0 {
		t.Fatalf("empty stream without content type = %+v, %v", lines, err)
	}
}

func TestLogsTimestampPrefix(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		text string
	}{
		{"2026-11-10T01:02:03.000000009Z hello world", true, "hello world"},
		{"2026-11-10T01:02:03+09:00 kst", true, "kst"},
		{"2026-11-10T01:02:03Z", true, ""},
		{"2026-11-10 01:02:03 not rfc3339", false, "2026-11-10 01:02:03 not rfc3339"},
		{"ERROR something", false, "ERROR something"},
		{"", false, ""},
	}
	for _, tc := range cases {
		_, text, ok := logsTimestamp(tc.line)
		if ok != tc.ok || text != tc.text {
			t.Errorf("logsTimestamp(%q) = %q, %v; want %q, %v", tc.line, text, ok, tc.text, tc.ok)
		}
	}
}

func logsTestCompare(t *testing.T, got, want []logsTestLine) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Stream != want[i].Stream || !got[i].TS.Equal(want[i].TS) || got[i].Text != want[i].Text {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// logsTestEngine answers GET /containers/{id}/logs with a body and records
// the queries it got.
type logsTestEngine struct {
	mu          sync.Mutex
	queries     []url.Values
	paths       []string
	methods     []string
	contentType string
	body        []byte
	status      int
	srv         *httptest.Server
}

func newLogsTestEngine(t *testing.T) *logsTestEngine {
	t.Helper()
	e := &logsTestEngine{status: http.StatusOK}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.queries = append(e.queries, r.URL.Query())
		e.paths = append(e.paths, r.URL.Path)
		e.methods = append(e.methods, r.Method)
		ct, body, status := e.contentType, e.body, e.status
		e.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"No such container"}`))
			return
		}
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *logsTestEngine) source(t *testing.T) *DockerSource {
	t.Helper()
	u, _ := url.Parse(e.srv.URL)
	s, err := NewDockerSource(DockerConfig{Endpoint: "tcp://" + u.Host})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDockerFollowRequestsAndReads(t *testing.T) {
	e := newLogsTestEngine(t)
	e.contentType = logsContentMultiplexed
	e.body = logsTestJoin(
		logsTestFrame(1, "2026-11-10T06:00:00Z out\n"),
		logsTestFrame(2, "2026-11-10T06:00:01Z err\n"),
	)
	s := e.source(t)
	inst := Instance{ID: dockerTestID("api"), Name: "grp-api-1"}
	since := time.Date(2026, 11, 10, 5, 59, 59, 7, time.UTC)
	var lines []logsTestLine
	onLine := func(stream string, ts time.Time, text string) {
		lines = append(lines, logsTestLine{stream, ts, text})
	}
	if err := s.Follow(context.Background(), inst, FollowFrom{Since: since}, onLine); err != nil {
		t.Fatalf("Follow since: %v", err)
	}
	if err := s.Follow(context.Background(), inst, FollowFrom{TailZero: true}, onLine); err != nil {
		t.Fatalf("Follow tail: %v", err)
	}
	if len(lines) != 4 || lines[0].Stream != "stdout" || lines[1].Stream != "stderr" || lines[1].Text != "err" {
		t.Fatalf("lines = %+v", lines)
	}
	e.mu.Lock()
	queries, paths, methods := e.queries, e.paths, e.methods
	e.mu.Unlock()
	for i, p := range paths {
		if methods[i] != http.MethodGet || p != "/containers/"+inst.ID+"/logs" {
			t.Fatalf("request %d = %s %s", i, methods[i], p)
		}
	}
	first, second := queries[0], queries[1]
	for _, q := range []url.Values{first, second} {
		for _, key := range []string{"follow", "stdout", "stderr", "timestamps"} {
			if q.Get(key) != "1" {
				t.Errorf("query %s = %q, want 1 (%v)", key, q.Get(key), q)
			}
		}
	}
	if first.Get("since") != "1794290399.000000007" || first.Has("tail") {
		t.Errorf("since query = %v", first)
	}
	if second.Get("tail") != "0" || second.Has("since") {
		t.Errorf("tail query = %v", second)
	}
}

func TestDockerFollowRawAndGone(t *testing.T) {
	e := newLogsTestEngine(t)
	e.contentType = logsContentRaw
	e.body = []byte("2026-11-10T07:00:00Z tty\n")
	s := e.source(t)
	inst := Instance{ID: dockerTestID("tty"), Name: "tty-1", Tty: true}
	var got []string
	if err := s.Follow(context.Background(), inst, FollowFrom{}, func(stream string, _ time.Time, text string) {
		got = append(got, stream+":"+text)
	}); err != nil || len(got) != 1 || got[0] != "stdout:tty" {
		t.Fatalf("raw Follow = %v, %v", got, err)
	}
	e.mu.Lock()
	tail := e.queries[0].Get("tail")
	e.status = http.StatusNotFound
	e.mu.Unlock()
	if tail != "0" {
		t.Fatalf("a follow without since asks tail=%q, want 0", tail)
	}
	err := s.Follow(context.Background(), inst, FollowFrom{TailZero: true}, func(string, time.Time, string) {})
	if !dockerNotFound(err) {
		t.Fatalf("gone container: err = %v, want a 404", err)
	}
	if err := s.Follow(context.Background(), Instance{ID: "../version"}, FollowFrom{}, nil); err == nil {
		t.Fatal("Follow took an id that is not hex")
	}
}
