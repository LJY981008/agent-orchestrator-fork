package controllers_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoproject"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

const presenceToken = "desktop-token"

var presenceNow = time.Now().UTC().Truncate(time.Second)

func presenceVerifier() *ladopresence.Verifier {
	return ladopresence.New(presenceToken, true, false, func() time.Time { return presenceNow })
}

// signedRequest sends a request with a presence proof made the way the
// desktop app makes it, over signedBody, while sending body.
func signedRequest(t *testing.T, srv *httptest.Server, method, path, signedBody, body string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ladopresence.Header, ladopresence.Sign(ladopresence.Key(presenceToken), method, path, []byte(signedBody), "AAAAAAAAAAAAAAAAAAAAAA", presenceNow.Add(time.Minute)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode
}

func TestLadoAPI_RequestPlan(t *testing.T) {
	svc := &fakeLadoTaskService{task: sampleLadoTask(), request: domain.LadoPlanRequest{ID: "req-1", TaskID: "lado-task-1", SessionID: "mer-orchestrator-1", RequestedAt: ladoTaskTime}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/tasks/lado-task-1/plan-requests", `{"sourceCommentId":"30"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoPlanRequestResponse](t, body)
	if got.ID != "req-1" || got.SessionID != "mer-orchestrator-1" || got.DeliveredAt != nil {
		t.Fatalf("response = %+v", got)
	}
	if svc.gotPlanReq.TaskID != "lado-task-1" || svc.gotPlanReq.SourceCommentID != "30" || svc.gotPlanReq.Note != "" {
		t.Fatalf("service got %+v", svc.gotPlanReq)
	}
}

func TestLadoAPI_PlanRevisionAndHistory(t *testing.T) {
	rev := domain.LadoPlanRevision{
		ID: "lado-plan-1", TaskID: "lado-task-1", RequestID: "req-1", Number: 1, Source: "agent", SHA256: strings.Repeat("a", 64),
		Problems: []domain.LadoPlanProblem{{Code: domain.LadoPlanMissingField, Field: "steps"}}, CreatedAt: ladoTaskTime,
	}
	svc := &fakeLadoTaskService{
		task:     sampleLadoTask(),
		planView: ladotask.PlanView{Task: sampleLadoTask(), Revision: rev, Content: "---\n---\n", Latest: true},
		history:  ladotask.PlanHistory{Revisions: []domain.LadoPlanRevision{rev}},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/plans/lado-plan-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	plan := decodeLadoJSON[controllers.LadoPlanResponse](t, body)
	if plan.Revision.Valid || len(plan.Revision.Problems) != 1 || plan.Revision.Problems[0].Field != "steps" || !plan.Latest || plan.Plan.Steps == nil || plan.Task.ID != "lado-task-1" {
		t.Fatalf("plan = %+v", plan)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1/plans", "")
	history := decodeLadoJSON[controllers.LadoPlanHistoryResponse](t, body)
	if status != http.StatusOK || len(history.Revisions) != 1 || history.Requests == nil || history.Approvals == nil {
		t.Fatalf("history = %+v (status %d)", history, status)
	}
}

func TestLadoAPI_TaskViewMarksComments(t *testing.T) {
	task := sampleLadoTask()
	task.Plan.Latest = domain.LadoPlanRevision{ID: "lado-plan-1", Number: 1, SHA256: strings.Repeat("a", 64), Source: "agent"}
	at := ladoTaskTime.Add(time.Hour)
	svc := &fakeLadoTaskService{task: task, view: ladotask.TaskView{
		Task:  task,
		Issue: domain.Issue{Title: "Add login", Body: "See the plan below", State: domain.IssueOpen, URL: "https://github.com/acme/demo/issues/12"},
		Comments: []domain.LadoIssueComment{
			{ID: "30", Author: "ljy", AuthorTrusted: true, CreatedAt: at, Body: "## Plan"},
			{ID: "31", Author: "drive-by", CreatedAt: at, Body: "spam"},
			{ID: "32", Author: "ljy", AuthorTrusted: true, CreatedAt: at, Body: "copy " + domain.LadoCommentMarkerPrefix + "x -->"},
		},
		IssueChanged: true,
	}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	view := decodeLadoJSON[controllers.LadoTaskViewResponse](t, body)
	if view.Issue == nil || view.Issue.Body != "See the plan below" || !view.IssueChangedSincePlan || view.Task.State != "planned" || view.Task.Plan.Latest == nil {
		t.Fatalf("view = %+v", view)
	}
	flags := map[string][3]bool{}
	for _, c := range view.Comments {
		flags[c.ID] = [3]bool{c.AuthorTrusted, c.ByLado, c.Included}
	}
	want := map[string][3]bool{"30": {true, false, true}, "31": {false, false, false}, "32": {true, true, false}}
	for id, w := range want {
		if flags[id] != w {
			t.Fatalf("comment %s flags (trusted, byLado, included) = %v, want %v", id, flags[id], w)
		}
	}

	svc.view.IssueReadError = "tracker down"
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks/lado-task-1", "")
	if view := decodeLadoJSON[controllers.LadoTaskViewResponse](t, body); view.Issue != nil || len(view.Comments) != 0 || view.IssueReadError != "tracker down" {
		t.Fatalf("view with a read error = %+v", view)
	}
}

func TestLadoAPI_ApprovePlanNeedsPresence(t *testing.T) {
	path := "/api/v1/lado/plans/lado-plan-1/approve"
	approveBody := `{"sha256":"` + strings.Repeat("a", 64) + `"}`

	t.Run("signed by the app", func(t *testing.T) {
		svc := &fakeLadoTaskService{task: sampleLadoTask(), approval: domain.LadoPlanApproval{ID: "a-1", RevisionID: "lado-plan-1", TaskID: "lado-task-1", SHA256: strings.Repeat("a", 64), ApprovedAt: presenceNow}}
		srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc, LadoPresence: presenceVerifier()})
		body, status := signedRequest(t, srv, "POST", path, approveBody, approveBody)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		if svc.gotRevision != "lado-plan-1" || svc.gotSHA != strings.Repeat("a", 64) || svc.gotProof.Nonce != "AAAAAAAAAAAAAAAAAAAAAA" || svc.gotProof.Action != domain.LadoPresencePlanApprove || svc.gotProof.SubjectID != "lado-plan-1" {
			t.Fatalf("service got revision %q sha %q proof %+v", svc.gotRevision, svc.gotSHA, svc.gotProof)
		}
	})
	for _, tc := range []struct {
		name     string
		presence controllers.LadoPresence
		send     func(t *testing.T, srv *httptest.Server) ([]byte, int)
		code     string
	}{
		{"curl without a proof", presenceVerifier(), func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			body, status, _ := doRequest(t, srv, "POST", path, approveBody)
			return body, status
		}, "LADO_PRESENCE_REQUIRED"},
		{"a proof for another plan's bytes", presenceVerifier(), func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			return signedRequest(t, srv, "POST", path, approveBody, `{"sha256":"`+strings.Repeat("b", 64)+`"}`)
		}, "LADO_PRESENCE_INVALID"},
		{"a daemon without the app's token", nil, func(t *testing.T, srv *httptest.Server) ([]byte, int) {
			return signedRequest(t, srv, "POST", path, approveBody, approveBody)
		}, "LADO_PRESENCE_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeLadoTaskService{task: sampleLadoTask()}
			deps := httpd.APIDeps{LadoTasks: svc}
			if tc.presence != nil {
				deps.LadoPresence = tc.presence
			}
			srv := newLadoTestServer(t, deps)
			body, status := tc.send(t, srv)
			if status != http.StatusForbidden || !strings.Contains(string(body), tc.code) {
				t.Fatalf("status = %d, body %s; want 403 %s", status, body, tc.code)
			}
			if svc.calls != 0 {
				t.Fatalf("the service was called %d times without a valid proof", svc.calls)
			}
		})
	}
}

func TestLadoAPI_ApprovePlanPassesThePolicySHA(t *testing.T) {
	path := "/api/v1/lado/plans/lado-plan-1/approve"
	plan, rules := strings.Repeat("a", 64), strings.Repeat("b", 64)
	svc := &fakeLadoTaskService{task: sampleLadoTask(), approval: domain.LadoPlanApproval{ID: "a-1", RevisionID: "lado-plan-1", TaskID: "lado-task-1", SHA256: plan, ApprovedAt: presenceNow}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc, LadoPresence: presenceVerifier()})

	approveBody := `{"sha256":"` + plan + `","policySha256":"` + rules + `"}`
	if body, status := signedRequest(t, srv, "POST", path, approveBody, approveBody); status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if svc.gotRevision != "lado-plan-1" || svc.gotSHA != plan || svc.gotPolicySHA != rules {
		t.Fatalf("service got revision %q sha %q policy %q, want both hashes from the body", svc.gotRevision, svc.gotSHA, svc.gotPolicySHA)
	}

	// Without the field the service gets an empty hash and decides; its
	// refusal reaches the app as sent.
	svc.err = apierr.Invalid("LADO_POLICY_SHA_REQUIRED", "Send the sha256 of the rule snapshot you reviewed with the approval.", nil)
	planOnly := `{"sha256":"` + plan + `"}`
	body, status := signedRequest(t, srv, "POST", path, planOnly, planOnly)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "LADO_POLICY_SHA_REQUIRED") || svc.gotPolicySHA != "" {
		t.Fatalf("status = %d, body %s, service got policy %q; want 400 LADO_POLICY_SHA_REQUIRED from the service", status, body, svc.gotPolicySHA)
	}
}

// presenceProjectService turns LADO mode off through the presence check the
// controller hands it, as the real service does.
type presenceProjectService struct {
	fakeLadoProjectService
	proof domain.LadoPresenceProof
}

func (f *presenceProjectService) UpdateSettings(ctx context.Context, projectID domain.ProjectID, in ladoproject.UpdateInput) (domain.LadoProjectSettings, error) {
	if in.LadoMode != nil && !*in.LadoMode {
		proof, err := in.Presence(domain.LadoPresenceModeOff, string(projectID))
		if err != nil {
			return domain.LadoProjectSettings{}, err
		}
		f.proof = proof
	}
	return f.fakeLadoProjectService.UpdateSettings(ctx, projectID, in)
}

func TestLadoAPI_TurningLadoModeOffChecksPresence(t *testing.T) {
	svc := &presenceProjectService{fakeLadoProjectService: fakeLadoProjectService{settings: domain.LadoProjectSettings{ProjectID: "mer"}}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoProjects: svc, LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/projects/mer/settings"
	off := `{"ladoMode":false}`

	if body, status := signedRequest(t, srv, "PUT", path, off, off); status != http.StatusOK || svc.proof.SubjectID != "mer" || svc.proof.Action != domain.LadoPresenceModeOff {
		t.Fatalf("status = %d, body %s, proof %+v", status, body, svc.proof)
	}
	if body, status, _ := doRequest(t, srv, "PUT", path, off); status != http.StatusForbidden || !strings.Contains(string(body), "LADO_PRESENCE_REQUIRED") {
		t.Fatalf("unsigned mode off: status = %d, body %s", status, body)
	}
	if body, status, _ := doRequest(t, srv, "PUT", path, `{"ladoMode":true}`); status != http.StatusOK {
		t.Fatalf("turning LADO mode on needs no proof: status = %d, body %s", status, body)
	}
}
