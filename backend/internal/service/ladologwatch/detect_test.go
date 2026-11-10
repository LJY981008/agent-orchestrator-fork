package ladologwatch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var detectT0 = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// detectFeed sends lines on one stream, one second apart, then flushes the
// detector to the end, and returns every incident it gave out.
func detectFeed(d *Detector, stream string, lines ...string) []Incident {
	var out []Incident
	at := detectT0
	for _, line := range lines {
		out = append(out, d.Line(stream, at, line)...)
		at = at.Add(time.Millisecond)
	}
	return append(out, detectFlushAll(d)...)
}

// detectFlushAll flushes twice, DetectIdle apart: the first Flush marks the
// quiet time, the second gives the waiting incidents out.
func detectFlushAll(d *Detector) []Incident {
	now := detectT0.Add(time.Hour)
	out := d.Flush(now)
	return append(out, d.Flush(now.Add(DetectIdle))...)
}

func detectOne(t *testing.T, service string, lines ...string) Incident {
	t.Helper()
	got := detectFeed(NewDetector(service, Rules{}), "stdout", lines...)
	if len(got) != 1 {
		t.Fatalf("incidents = %d, want 1: %#v", len(got), got)
	}
	return got[0]
}

func javaStack(id, at string, line int) []string {
	return []string{
		"2026-11-10 09:00:00.123  INFO 1 --- [nio-8080-exec-1] c.e.a.Controller : request " + id,
		at + " ERROR 1 --- [nio-8080-exec-7] c.e.a.Controller : Request " + id + " failed",
		"java.lang.IllegalStateException: order " + id + " is closed",
		"\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:897)",
		fmt.Sprintf("\tat com.example.app.OrderService.close(OrderService.java:%d)", line),
		"\tat com.example.app.OrderController.close(OrderController.java:31)",
		"\tat java.base/java.lang.Thread.run(Thread.java:833)",
		"Caused by: java.io.IOException: connection reset",
		"\tat com.example.app.Store.write(Store.java:12)",
		"\t... 12 more",
		"2026-11-10 09:00:01.000  INFO 1 --- [nio-8080-exec-2] c.e.a.Controller : next request",
	}
}

func pythonStack(id string) []string {
	return []string{
		"ERROR:app.views:order " + id + " failed",
		"Traceback (most recent call last):",
		`  File "/usr/local/lib/python3.12/site-packages/django/core/handlers/base.py", line 197, in _get_response`,
		"    response = wrapped_callback(request, *callback_args, **callback_kwargs)",
		`  File "/app/app/views.py", line 42, in close_order`,
		"    order = orders[" + id + "]",
		"KeyError: '" + id + "'",
		"",
		"During handling of the above exception, another exception occurred:",
		"",
		"Traceback (most recent call last):",
		`  File "/app/app/views.py", line 44, in close_order`,
		"    raise NotFound(id)",
		`  File "/usr/local/lib/python3.12/site-packages/rest_framework/exceptions.py", line 10, in __init__`,
		"    super().__init__()",
		"app.errors.NotFound: order " + id,
		"INFO:app.views:next request",
	}
}

func nodeStack(id string) []string {
	return []string{
		"TypeError: Cannot read properties of undefined (reading '" + id + "')",
		"    at Layer.handle [as handle_request] (/app/node_modules/express/lib/router/layer.js:95:5)",
		"    at closeOrder (/app/src/routes/orders.js:27:19)",
		"    at process.processTicksAndRejections (node:internal/process/task_queues:95:5)",
		"GET /health 200",
	}
}

func goStack(addr string) []string {
	return []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=" + addr + "]",
		"",
		"goroutine 18 [running]:",
		"runtime.panicmem()",
		"\t/usr/local/go/src/runtime/panic.go:261 +0x48",
		"main.(*Server).closeOrder(0x0, {0x7a1b20, " + addr + "})",
		"\t/app/cmd/server/orders.go:42 +0x1d",
		"net/http.HandlerFunc.ServeHTTP(...)",
		"\t/usr/local/go/src/net/http/server.go:2136",
		"created by net/http.(*Server).Serve in goroutine 1",
		"\t/usr/local/go/src/net/http/server.go:3285 +0x4b4",
		"exit status 2",
		"starting server",
	}
}

func TestDetectStacksGroupIntoOneIncident(t *testing.T) {
	cases := []struct {
		name     string
		lines    []string
		kind     string
		location string
		first    string
		last     string
	}{
		{
			name:     "java",
			lines:    javaStack("4711", "2026-11-10 09:00:00.200", 57),
			kind:     "java.lang.IllegalStateException",
			location: "com.example.app.OrderService.close(OrderService.java:57)",
			first:    "ERROR 1 ---",
			last:     "\t... 12 more",
		},
		{
			name:     "python",
			lines:    pythonStack("4711"),
			kind:     "app.errors.NotFound",
			location: "/app/app/views.py:44 in close_order",
			first:    "ERROR:app.views",
			last:     "app.errors.NotFound: order 4711",
		},
		{
			name:     "node",
			lines:    nodeStack("id"),
			kind:     "TypeError",
			location: "closeOrder (/app/src/routes/orders.js:27:19)",
			first:    "TypeError:",
			last:     "    at process.processTicksAndRejections (node:internal/process/task_queues:95:5)",
		},
		{
			name:     "go",
			lines:    goStack("0xc000123456"),
			kind:     "panic",
			location: "main.(*Server).closeOrder /app/cmd/server/orders.go:42",
			first:    "panic: runtime error",
			last:     "exit status 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectOne(t, "grp/api", tc.lines...)
			if got.ExceptionKind != tc.kind {
				t.Errorf("kind = %q, want %q", got.ExceptionKind, tc.kind)
			}
			if got.Location != tc.location {
				t.Errorf("location = %q, want %q", got.Location, tc.location)
			}
			if !strings.Contains(got.Lines[0], tc.first) {
				t.Errorf("first line = %q, want it to hold %q", got.Lines[0], tc.first)
			}
			if last := got.Lines[len(got.Lines)-1]; last != tc.last {
				t.Errorf("last line = %q, want %q", last, tc.last)
			}
			if len(got.After) != 1 {
				t.Errorf("after = %q, want the one line after the block", got.After)
			}
			if len(got.Fingerprint) != 64 {
				t.Errorf("fingerprint = %q, want hex sha256", got.Fingerprint)
			}
		})
	}
}

func TestDetectJSONLines(t *testing.T) {
	stack := `Error: boom\n    at handler (/app/node_modules/koa/lib/x.js:1:2)\n    at saveOrder (/app/src/orders.js:10:5)`
	got := detectOne(t, "grp/web",
		`{"level":"info","msg":"listening"}`,
		`{"level":50,"msg":"save failed","err":{"type":"Error","message":"boom","stack":"`+stack+`"}}`,
	)
	if got.ExceptionKind != "Error" || got.Location != "saveOrder (/app/src/orders.js:10:5)" {
		t.Fatalf("json incident = %q at %q", got.ExceptionKind, got.Location)
	}
	if len(got.Lines) != 1 || len(got.Before) != 1 {
		t.Fatalf("json lines = %q before = %q", got.Lines, got.Before)
	}

	plain := detectOne(t, "grp/web", `{"severity":"ERROR","message":"payment 991 timed out"}`)
	if plain.ExceptionKind != "ERROR" || plain.Location != "" {
		t.Fatalf("plain json = %q at %q", plain.ExceptionKind, plain.Location)
	}
	other := detectOne(t, "grp/web", `{"severity":"ERROR","message":"payment 12 timed out"}`)
	if plain.Fingerprint != other.Fingerprint {
		t.Fatal("json errors that differ only in a number have different fingerprints")
	}

	if got := detectFeed(NewDetector("grp/web", Rules{}), "stdout",
		`{"level":"warn","msg":"ERROR budget low"}`, `{"level":30,"msg":"ok"}`); len(got) != 0 {
		t.Fatalf("non-error json lines gave %d incidents", len(got))
	}
}

func TestDetectFingerprintIsPerService(t *testing.T) {
	// Two replicas of one service, on two hosts: the ids, times, thread
	// names, addresses and line numbers differ, the error does not.
	replicaA := NewDetector("grp/api", Rules{})
	replicaB := NewDetector("grp/api", Rules{})
	a := detectFeed(replicaA, "stdout", javaStack("4711", "2026-11-10 09:00:00.200", 57)...)
	b := detectFeed(replicaB, "stdout", javaStack("550e8400-e29b-41d4-a716-446655440000", "2026-11-11T22:13:59.999Z", 61)...)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("incidents = %d, %d", len(a), len(b))
	}
	if a[0].Fingerprint != b[0].Fingerprint {
		t.Fatal("the same exception on two replicas has two fingerprints")
	}

	for name, lines := range map[string][][]string{
		"python": {pythonStack("4711"), pythonStack("8812")},
		"node":   {nodeStack("id"), nodeStack("name")},
		"go":     {goStack("0xc000123456"), goStack("0xc000ffee00")},
	} {
		x := detectOne(t, "grp/api", lines[0]...)
		y := detectOne(t, "grp/api", lines[1]...)
		if x.Fingerprint != y.Fingerprint {
			t.Errorf("%s: occurrences that differ in ids have different fingerprints", name)
		}
	}

	other := detectOne(t, "grp/worker", javaStack("4711", "2026-11-10 09:00:00.200", 57)...)
	if other.Fingerprint == a[0].Fingerprint {
		t.Fatal("another service has the same fingerprint")
	}
	moved := javaStack("4711", "2026-11-10 09:00:00.200", 57)
	moved[4] = "\tat com.example.app.OrderService.reopen(OrderService.java:57)"
	if got := detectOne(t, "grp/api", moved...); got.Fingerprint == a[0].Fingerprint {
		t.Fatal("another first application frame has the same fingerprint")
	}
}

func TestDetectSingleLineErrors(t *testing.T) {
	x := detectOne(t, "grp/api", "2026-11-10T09:00:00.120Z ERROR request 550e8400-e29b-41d4-a716-446655440000 failed after 120ms (worker 3, token deadbeef0123)")
	y := detectOne(t, "grp/api", "2026-11-12T23:41:07.002Z ERROR request 6ba7b810-9dad-11d1-80b4-00c04fd430c8 failed after 9ms (worker 11, token 0123abcd4567ef)")
	if x.Fingerprint != y.Fingerprint {
		t.Fatal("error lines that differ in time, UUID, hex and numbers have different fingerprints")
	}
	if x.ExceptionKind != "ERROR" || x.Location != "" {
		t.Fatalf("single line = %q at %q", x.ExceptionKind, x.Location)
	}
	z := detectOne(t, "grp/api", "2026-11-10T09:00:00.120Z ERROR cache refill failed")
	if z.Fingerprint == x.Fingerprint {
		t.Fatal("another message has the same fingerprint")
	}

	for _, line := range []string{
		"2026/11/10 09:00:00 [error] 12#12: *1 connect() failed",
		`time=2026-11-10T09:00:00Z level=error msg="db down"`,
		"FATAL: password authentication failed",
		"\x1b[31mCRITICAL\x1b[0m disk full",
	} {
		got := detectOne(t, "grp/api", "INFO ok", line)
		if strings.Contains(got.Lines[0], "\x1b") {
			t.Errorf("%q: colour codes left in %q", line, got.Lines[0])
		}
	}

	if got := detectFeed(NewDetector("grp/api", Rules{}), "stdout",
		"INFO 0 errors", "WARN retrying", "DEBUG ERRORS_TOTAL=0", "ERROR(s) none"); len(got) != 0 {
		t.Fatalf("non-error lines gave %d incidents: %#v", len(got), got)
	}
}

func TestDetectContext(t *testing.T) {
	var lines []string
	for i := range 12 {
		lines = append(lines, fmt.Sprintf("INFO before %d", i))
	}
	lines = append(lines, "ERROR it broke")
	for i := range 7 {
		lines = append(lines, fmt.Sprintf("INFO after %d", i))
	}
	d := NewDetector("grp/api", Rules{})
	var got []Incident
	for i, line := range lines {
		got = append(got, d.Line("stdout", detectT0.Add(time.Duration(i)*time.Second), line)...)
	}
	// The incident is out as soon as its fifth after line is in.
	if len(got) != 1 {
		t.Fatalf("incidents = %d, want 1 before any flush", len(got))
	}
	inc := got[0]
	if len(inc.Before) != detectBeforeLines || inc.Before[0] != "INFO before 2" {
		t.Fatalf("before = %q", inc.Before)
	}
	if len(inc.After) != detectAfterLines || inc.After[4] != "INFO after 4" {
		t.Fatalf("after = %q", inc.After)
	}
	if !inc.At.Equal(detectT0.Add(12 * time.Second)) {
		t.Fatalf("at = %v", inc.At)
	}

	// A new incident gives the waiting one out with the after lines it has.
	two := detectFeed(NewDetector("grp/api", Rules{}), "stdout", "ERROR one", "INFO between", "ERROR two")
	if len(two) != 2 || len(two[0].After) != 1 || two[1].Before[len(two[1].Before)-1] != "INFO between" {
		t.Fatalf("two incidents = %#v", two)
	}
}

func TestDetectBlockLimits(t *testing.T) {
	lines := []string{"java.lang.RuntimeException: deep"}
	for i := range 300 {
		lines = append(lines, fmt.Sprintf("\tat com.example.app.Deep.call%d(Deep.java:%d)", i, i))
	}
	got := detectOne(t, "grp/api", lines...)
	if len(got.Lines) != detectMaxLines+1 || got.Lines[detectMaxLines] != "… 101줄 생략" {
		t.Fatalf("lines = %d, last %q", len(got.Lines), got.Lines[len(got.Lines)-1])
	}

	big := []string{"ERROR big"}
	for range 20 {
		big = append(big, "  "+strings.Repeat("x", 3000))
	}
	got = detectOne(t, "grp/api", big...)
	size := 0
	for _, line := range got.Lines {
		size += len(line) + 1
	}
	if size > detectMaxBytes+64 {
		t.Fatalf("block = %d bytes, over the cap", size)
	}

	long := detectOne(t, "grp/api", "ERROR "+strings.Repeat("é", 5000))
	if len(long.Lines[0]) > detectMaxLineBytes+len("…") {
		t.Fatalf("line = %d bytes, over the cap", len(long.Lines[0]))
	}
}

func TestDetectFlushWaitsForQuiet(t *testing.T) {
	d := NewDetector("grp/api", Rules{})
	d.Line("stderr", detectT0, "java.lang.IllegalStateException: boom")
	d.Line("stderr", detectT0, "\tat com.example.app.A.b(A.java:1)")
	now := detectT0.Add(time.Minute)
	if got := d.Flush(now); len(got) != 0 {
		t.Fatal("the first flush gave the block out")
	}
	if got := d.Flush(now.Add(DetectIdle - time.Millisecond)); len(got) != 0 {
		t.Fatal("a flush before the quiet time gave the block out")
	}
	// A line resets the quiet time.
	d.Line("stderr", detectT0, "\tat com.example.app.C.d(C.java:2)")
	if got := d.Flush(now.Add(DetectIdle)); len(got) != 0 {
		t.Fatal("a flush right after a line gave the block out")
	}
	got := d.Flush(now.Add(2 * DetectIdle))
	if len(got) != 1 || len(got[0].Lines) != 3 || got[0].Stream != "stderr" {
		t.Fatalf("flushed = %#v", got)
	}
	if got := d.Flush(now.Add(time.Hour)); len(got) != 0 {
		t.Fatal("a block came out twice")
	}
}

func TestDetectRules(t *testing.T) {
	d := NewDetector("grp/api", Rules{
		Include: []string{`^WARN .*deadline exceeded`, `(`},
		Exclude: []string{`ClientAbortException`},
	})
	got := detectFeed(d, "stdout",
		"WARN rpc deadline exceeded",
		"INFO ok",
		"ERROR write failed",
		"org.apache.catalina.connector.ClientAbortException: java.io.IOException: Broken pipe",
		"\tat org.apache.catalina.connector.OutputBuffer.realWriteBytes(OutputBuffer.java:310)",
		"INFO ok",
	)
	if len(got) != 1 || got[0].Lines[0] != "WARN rpc deadline exceeded" {
		t.Fatalf("incidents = %#v, want only the included warning", got)
	}
}

func TestDetectStreamsApart(t *testing.T) {
	d := NewDetector("grp/api", Rules{})
	var got []Incident
	got = append(got, d.Line("stderr", detectT0, "java.lang.IllegalStateException: boom")...)
	got = append(got, d.Line("stdout", detectT0, "INFO handled request")...)
	got = append(got, d.Line("stderr", detectT0, "\tat com.example.app.A.b(A.java:1)")...)
	got = append(got, d.Line("stdout", detectT0, "ERROR other")...)
	got = append(got, detectFlushAll(d)...)
	if len(got) != 2 {
		t.Fatalf("incidents = %d, want 2", len(got))
	}
	for _, inc := range got {
		if inc.Stream == "stderr" && (len(inc.Lines) != 2 || inc.Location != "com.example.app.A.b(A.java:1)") {
			t.Fatalf("stderr block = %#v", inc)
		}
		if inc.Stream == "stdout" && (len(inc.Lines) != 1 || inc.Before[0] != "INFO handled request") {
			t.Fatalf("stdout block = %#v", inc)
		}
	}
}

func TestDetectSeparateTracebacks(t *testing.T) {
	one := []string{
		"Traceback (most recent call last):",
		`  File "/app/a.py", line 1, in f`,
		"ValueError: a",
	}
	lines := append(append([]string{}, one...), one...)
	got := detectFeed(NewDetector("grp/api", Rules{}), "stderr", lines...)
	if len(got) != 2 || got[0].ExceptionKind != "ValueError" || got[1].Location != "/app/a.py:1 in f" {
		t.Fatalf("two tracebacks = %#v", got)
	}
}
