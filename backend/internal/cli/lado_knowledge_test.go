package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A plan worker may write a knowledge note only as one command with a quoted
// here-document whose delimiter line comes once, last: the form the plan
// worker's standing instructions give.
func TestLadoPlanWorkerAllowsAKnowledgeNoteInLadosForm(t *testing.T) {
	note := "---\ntitle: Hook rules\nkind: map\nsources:\n  - path: backend/internal/cli/lado_plan_worker.go\n---\n\n$(rm -rf /) `id` > out.txt\n"
	cases := []struct {
		name    string
		command string
		allow   bool
	}{
		{"new note", "ao lado knowledge write --note hook-rules <<'LADO_NOTE_EOF'\n" + note + "LADO_NOTE_EOF", true},
		{"update with base", "ao lado knowledge write --note hook-rules --base 3 <<'EOF'\n" + note + "EOF", true},
		{"trailing newline", "ao lado knowledge write --note a <<'EOF'\nbody\nEOF\n", true},
		{"digit slug", "ao lado knowledge write --note 0-ports-and-env <<'EOF'\nbody\nEOF", true},

		{"command after the delimiter", "ao lado knowledge write --note a <<'EOF'\nbody\nEOF\nrm -rf /", false},
		{"delimiter twice", "ao lado knowledge write --note a <<'EOF'\nbody\nEOF\nmore\nEOF", false},
		{"list on the head line", "ao lado knowledge write --note a <<'EOF' && rm x\nbody\nEOF", false},
		{"pipe on the head line", "ao lado knowledge write --note a <<'EOF' | tee out\nbody\nEOF", false},
		{"redirection on the head line", "ao lado knowledge write --note a <<'EOF' > out.txt\nbody\nEOF", false},
		{"stderr redirection", "ao lado knowledge write --note a 2>&1 <<'EOF'\nbody\nEOF", false},
		{"unquoted delimiter", "ao lado knowledge write --note a <<EOF\nbody\nEOF", false},
		{"double-quoted delimiter", "ao lado knowledge write --note a <<\"EOF\"\nbody\nEOF", false},
		{"tab-stripping here-document", "ao lado knowledge write --note a <<-'EOF'\nbody\nEOF", false},
		{"carriage return", "ao lado knowledge write --note a <<'EOF'\r\nbody\r\nEOF", false},
		{"no body", "ao lado knowledge write --note a <<'EOF'", false},
		{"piped in", "printf '%s' 'body' | ao lado knowledge write --note a", false},
		{"from a file", "ao lado knowledge write --note a < note.md", false},
		{"slug escapes", "ao lado knowledge write --note ../x <<'EOF'\nbody\nEOF", false},
		{"uppercase slug", "ao lado knowledge write --note Hook <<'EOF'\nbody\nEOF", false},
		{"slug starts with a hyphen", "ao lado knowledge write --note -x <<'EOF'\nbody\nEOF", false},
		{"slug too long", "ao lado knowledge write --note " + strings.Repeat("a", 65) + " <<'EOF'\nbody\nEOF", false},
		{"slug with a dot", "ao lado knowledge write --note a.md <<'EOF'\nbody\nEOF", false},
		{"no slug", "ao lado knowledge write <<'EOF'\nbody\nEOF", false},
		{"base zero", "ao lado knowledge write --note a --base 0 <<'EOF'\nbody\nEOF", false},
		{"base not a number", "ao lado knowledge write --note a --base $(id) <<'EOF'\nbody\nEOF", false},
		{"base before note", "ao lado knowledge write --base 2 --note a <<'EOF'\nbody\nEOF", false},
		{"another flag", "ao lado knowledge write --note a --project other <<'EOF'\nbody\nEOF", false},
		{"another subcommand", "ao lado knowledge delete --note a <<'EOF'\nbody\nEOF", false},
		{"cat into the folder", "cat > /home/u/.ao/data/lado/knowledge/p/notes/a.md <<'EOF'\nbody\nEOF", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ladoPlanWorkerCommand(tc.command); got != tc.allow {
				t.Fatalf("ladoPlanWorkerCommand(%q) = %v, want %v", tc.command, got, tc.allow)
			}
		})
	}
}

// A refused plan worker reads the one form a knowledge note may take.
func TestLadoPlanWorkerDenyMessageNamesTheKnowledgeForm(t *testing.T) {
	for _, want := range []string{"ao lado knowledge write --note <slug> [--base <revision>] <<'LADO_NOTE_EOF'", "before its final plan submit"} {
		if !strings.Contains(ladoPlanWorkerDenyMessage, want) {
			t.Errorf("deny message lacks %q:\n%s", want, ladoPlanWorkerDenyMessage)
		}
	}
}

func TestLadoKnowledgeWriteSendsTheNoteAsTheSession(t *testing.T) {
	note := "---\ntitle: Hook rules\nkind: map\n---\n\nThe hook lives in cli/lado_plan_worker.go.\n"
	cases := []struct {
		name string
		args []string
		base *int
	}{
		{"new note", []string{"lado", "knowledge", "write", "--note", "hook-rules"}, nil},
		{"update", []string{"lado", "knowledge", "write", "--note", "hook-rules", "--base", "3"}, intPtr(3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "demo-7")
			cfg := setConfigEnv(t)
			srv, capture, method := ladoKnowledgeServer(t, `{"slug":"hook-rules","title":"Hook rules","kind":"map","revision":4,"author":"demo-7","state":"unchecked"}`)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(note), ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err != nil {
				t.Fatalf("write: %v\nstderr=%s", err, errOut)
			}
			if *method != http.MethodPut || capture.path != "/api/v1/lado/knowledge/notes/hook-rules" {
				t.Fatalf("%s %s", *method, capture.path)
			}
			var raw map[string]any
			if err := json.Unmarshal([]byte(capture.body), &raw); err != nil {
				t.Fatalf("body %q: %v", capture.body, err)
			}
			if raw["sessionId"] != "demo-7" || raw["content"] != note {
				t.Fatalf("body = %s", capture.body)
			}
			base, has := raw["baseRevision"]
			if tc.base == nil && has {
				t.Fatalf("a new note sent baseRevision: %s", capture.body)
			}
			if tc.base != nil && base != float64(*tc.base) {
				t.Fatalf("baseRevision = %v, want %d", base, *tc.base)
			}
			if out != "LADO saved hook-rules as revision 4.\n" {
				t.Fatalf("out = %q", out)
			}
		})
	}
}

// A note that changed since the session read it comes back as the daemon's
// envelope, with the current revision, and a runtime exit code.
func TestLadoKnowledgeWriteShowsAChangedNote(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-7")
	cfg := setConfigEnv(t)
	srv, _ := ladoServer(t, http.StatusConflict, `{"code":"LADO_KNOWLEDGE_CHANGED","message":"The note is at revision 5 now. Read it again, merge, and send it with revision 5 as the base.","requestId":"req-1","details":{"currentRevision":5}}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{In: strings.NewReader("---\ntitle: x\nkind: map\n---\nx\n"), ProcessAlive: func(int) bool { return true }},
		"lado", "knowledge", "write", "--note", "hook-rules", "--base", "3")
	if err == nil {
		t.Fatal("a changed note was saved")
	}
	for _, want := range []string{"The note is at revision 5 now.", "with revision 5 as the base", "(LADO_KNOWLEDGE_CHANGED)", "[request req-1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if code := ExitCode(err); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "LADO_KNOWLEDGE_CHANGED" {
		t.Fatalf("err = %#v", err)
	}
}

func TestLadoKnowledgeWriteUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		session string
		in      string
		args    []string
	}{
		{"outside a session", "", "note", []string{"--note", "a"}},
		{"empty stdin", "demo-7", " \n", []string{"--note", "a"}},
		{"no slug", "demo-7", "note", nil},
		{"slug escapes", "demo-7", "note", []string{"--note", "../x"}},
		{"uppercase slug", "demo-7", "note", []string{"--note", "Notes"}},
		{"slug too long", "demo-7", "note", []string{"--note", strings.Repeat("a", 65)}},
		{"base zero", "demo-7", "note", []string{"--note", "a", "--base", "0"}},
		{"base not a number", "demo-7", "note", []string{"--note", "a", "--base", "x"}},
		{"stdin over the cap", "demo-7", strings.Repeat("a", maxLadoSubmitBytes+1), []string{"--note", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tc.session)
			cfg := setConfigEnv(t)
			srv, capture := ladoServer(t, http.StatusOK, `{"slug":"a","revision":1}`)
			writeRunFileFor(t, cfg, srv)
			args := append([]string{"lado", "knowledge", "write"}, tc.args...)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(tc.in), ProcessAlive: func(int) bool { return true }}, args...)
			if code := ExitCode(err); code != 2 {
				t.Fatalf("exit code = %d (err %v), want 2", code, err)
			}
			if capture.hits != 0 {
				t.Fatalf("the daemon was called: %s", capture.body)
			}
		})
	}
}

func intPtr(n int) *int { return &n }

// ladoKnowledgeServer answers like ladoServer with 200 and also keeps the
// last request's method.
func ladoKnowledgeServer(t *testing.T, respBody string) (*httptest.Server, *activityCapture, *string) {
	t.Helper()
	capture := &activityCapture{}
	method := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture.body, capture.path, *method = string(body), r.URL.Path, r.Method
		capture.hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture, method
}
