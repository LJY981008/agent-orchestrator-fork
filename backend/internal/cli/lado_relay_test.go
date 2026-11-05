package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const ladoRelayAnswer = `{"id":"lado-relay-1","taskId":"lado-task-1","projectId":"mer","orchestratorSessionId":"mer-orchestrator-1",` +
	`"workerSessionId":"mer-7","workerRole":"integration","instruction":"x","evidence":"y","evidenceMessageId":"m",` +
	`"evidenceAt":"2026-11-05T09:00:00Z","beyondEvidence":true,"outsidePlan":true,` +
	`"outsidePlanReasons":["계획 밖 경로: docs/x.md"],"state":"delivered","createdAt":"2026-11-05T09:00:00Z"}`

func TestLadoRelaySendsTheRequestAsTheSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-orchestrator-1")
	cfg := setConfigEnv(t)
	srv, capture := ladoServer(t, http.StatusCreated, ladoRelayAnswer)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }},
		"lado", "relay", "--task", "#49", "--session", "mer-7", "--evidence", "그 워커에게 후속 2건도 이어서 해",
		"--message", "후속 2건도 이어서 해", "--outside-plan")
	if err != nil {
		t.Fatalf("relay: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/lado/relays" {
		t.Fatalf("path = %q", capture.path)
	}
	var body createLadoRelayRequest
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatalf("body %s: %v", capture.body, err)
	}
	want := createLadoRelayRequest{
		SessionID: "mer-orchestrator-1", Task: "#49", WorkerSessionID: "mer-7",
		Message: "후속 2건도 이어서 해", Evidence: "그 워커에게 후속 2건도 이어서 해", OutsidePlan: true,
	}
	if body != want {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
	for _, s := range []string{"delivered the instruction to mer-7 (integration) of lado-task-1 as lado-relay-1",
		"not in the developer's message", "계획 밖 경로: docs/x.md"} {
		if !strings.Contains(out, s) {
			t.Fatalf("out = %q, lacks %q", out, s)
		}
	}
}

func TestLadoRelayReadsTheInstructionFromStdin(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-orchestrator-1")
	cfg := setConfigEnv(t)
	srv, capture := ladoServer(t, http.StatusCreated, ladoRelayAnswer)
	writeRunFileFor(t, cfg, srv)
	_, errOut, err := executeCLI(t, Deps{In: strings.NewReader("첫 줄\n둘째 줄\n"), ProcessAlive: func(int) bool { return true }},
		"lado", "relay", "--task", "lado-task-1", "--evidence", "이어서 해")
	if err != nil {
		t.Fatalf("relay: %v\nstderr=%s", err, errOut)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatalf("body %s: %v", capture.body, err)
	}
	if body["message"] != "첫 줄\n둘째 줄\n" || body["task"] != "lado-task-1" {
		t.Fatalf("body = %v", body)
	}
	// No worker and no plan mark: the daemon picks the one live worker.
	if _, ok := body["workerSessionId"]; ok {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["outsidePlan"]; ok {
		t.Fatalf("body = %v", body)
	}
}

func TestLadoRelayShowsTheCandidatesOfARefusal(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-orchestrator-1")
	cfg := setConfigEnv(t)
	srv, _ := ladoServer(t, http.StatusConflict, `{"error":"conflict","code":"LADO_RELAY_AMBIGUOUS",`+
		`"message":"Task lado-task-1 has 2 live workers; name one with --session.",`+
		`"details":{"taskId":"lado-task-1","candidates":[{"sessionId":"mer-7","role":"part","partId":"api"},{"sessionId":"mer-8","role":"part","partId":"ui"}]}}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }},
		"lado", "relay", "--task", "lado-task-1", "--evidence", "이어서 해", "--message", "이어서 해")
	var resp apiResponseError
	if !errors.As(err, &resp) || resp.ErrorBody.Code != "LADO_RELAY_AMBIGUOUS" {
		t.Fatalf("err = %v", err)
	}
	for _, s := range []string{"2 live workers", "(LADO_RELAY_AMBIGUOUS)", "--session mer-7  part api", "--session mer-8  part ui"} {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("err = %q, lacks %q", err.Error(), s)
		}
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d", ExitCode(err))
	}
}

func TestLadoRelayPassesARefusalOn(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-orchestrator-1")
	cfg := setConfigEnv(t)
	srv, _ := ladoServer(t, http.StatusConflict, `{"error":"conflict","code":"LADO_RELAY_NO_WORKER",`+
		`"message":"Task lado-task-1 has no live worker to relay to: its worker's session has ended.",`+
		`"details":{"taskId":"lado-task-1","reason":"ended"}}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }},
		"lado", "relay", "--task", "lado-task-1", "--evidence", "이어서 해", "--message", "이어서 해")
	if err == nil || err.Error() != "Task lado-task-1 has no live worker to relay to: its worker's session has ended. (LADO_RELAY_NO_WORKER)" {
		t.Fatalf("err = %v", err)
	}
}

func TestLadoRelayUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		session string
		in      string
		args    []string
	}{
		{"outside a session", "", "", []string{"--task", "#1", "--evidence", "abcd", "--message", "m"}},
		{"no task", "o-1", "", []string{"--evidence", "abcd", "--message", "m"}},
		{"no evidence", "o-1", "", []string{"--task", "#1", "--message", "m"}},
		{"no instruction", "o-1", " \n", []string{"--task", "#1", "--evidence", "abcd"}},
		{"empty message", "o-1", "from stdin", []string{"--task", "#1", "--evidence", "abcd", "--message", " "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tc.session)
			setConfigEnv(t)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(tc.in), ProcessAlive: func(int) bool { return true }},
				append([]string{"lado", "relay"}, tc.args...)...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v, want a usage error", err)
			}
		})
	}
}
