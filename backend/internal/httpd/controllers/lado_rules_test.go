package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var ladoRuleTime = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

type fakeLadoRuleService struct {
	views     []ladopolicy.RuleView
	versions  []domain.LadoRuleVersion
	result    ladopolicy.ChangeResult
	effective ladopolicy.EffectiveView
	err       error
	calls     int

	gotArchived  bool
	gotID        domain.LadoRuleID
	gotCreate    ladopolicy.CreateInput
	gotUpdate    ladopolicy.UpdateInput
	gotBinding   ladopolicy.BindingInput
	gotEffective ladopolicy.EffectiveInput
	gotProof     domain.LadoPresenceProof
}

func (f *fakeLadoRuleService) List(_ context.Context, includeArchived bool) ([]ladopolicy.RuleView, error) {
	f.calls++
	f.gotArchived = includeArchived
	return f.views, f.err
}

func (f *fakeLadoRuleService) Rule(_ context.Context, id domain.LadoRuleID) (ladopolicy.RuleView, []domain.LadoRuleVersion, error) {
	f.calls++
	f.gotID = id
	return f.views[0], f.versions, f.err
}

func (f *fakeLadoRuleService) Create(_ context.Context, in ladopolicy.CreateInput) (ladopolicy.ChangeResult, error) {
	f.calls++
	f.gotCreate = in
	return f.result, f.err
}

// Update asks for presence the way the real service does for a ceiling
// change.
func (f *fakeLadoRuleService) Update(_ context.Context, id domain.LadoRuleID, in ladopolicy.UpdateInput) (ladopolicy.ChangeResult, error) {
	f.calls++
	f.gotID, f.gotUpdate = id, in
	if in.CeilingMode != nil && !in.DryRun {
		proof, err := in.Presence(domain.LadoPresenceRuleChange, string(id))
		if err != nil {
			return ladopolicy.ChangeResult{}, err
		}
		f.gotProof = proof
	}
	return f.result, f.err
}

func (f *fakeLadoRuleService) Delete(_ context.Context, id domain.LadoRuleID) error {
	f.calls++
	f.gotID = id
	return f.err
}

func (f *fakeLadoRuleService) SetBinding(_ context.Context, in ladopolicy.BindingInput) (ladopolicy.ChangeResult, error) {
	f.calls++
	f.gotBinding = in
	return f.result, f.err
}

func (f *fakeLadoRuleService) Effective(_ context.Context, in ladopolicy.EffectiveInput) (ladopolicy.EffectiveView, error) {
	f.calls++
	f.gotEffective = in
	return f.effective, f.err
}

// recordingPresence accepts every proof and keeps what it was asked to
// check.
type recordingPresence struct {
	header, method, path, action, subject string
	body                                  []byte
}

func (p *recordingPresence) Verify(header, method, path string, body []byte, action, subject string) (domain.LadoPresenceProof, error) {
	p.header, p.method, p.path, p.action, p.subject = header, method, path, action, subject
	p.body = append([]byte(nil), body...)
	return domain.LadoPresenceProof{Nonce: "BBBBBBBBBBBBBBBBBBBBBB", Action: action, SubjectID: subject}, nil
}

func ceilingRuleView() ladopolicy.RuleView {
	content := domain.LadoRuleContent{
		Name: "Permission ceiling", Category: "tools",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementToolPolicy},
		Config:      json.RawMessage(`{"mode":"auto"}`),
	}.Normalize()
	return ladopolicy.RuleView{Rule: domain.LadoRule{
		ID: domain.LadoRulePermissionCeiling, Builtin: true, Protected: true,
		Version: domain.LadoRuleVersion{
			ID: "v-ceiling-2", RuleID: domain.LadoRulePermissionCeiling, Number: 2, Content: content,
			SHA256: content.SHA256(), Actor: "human", CreatedAt: ladoRuleTime.Add(time.Hour),
		},
		Global:    domain.LadoBindingOn,
		Overrides: []domain.LadoRuleBinding{},
		CreatedAt: ladoRuleTime,
	}, Labels: map[string]string{}}
}

func userRuleView() ladopolicy.RuleView {
	content := domain.LadoRuleContent{
		Name: "No force push", Category: "git", Instruction: "Never force push.",
		Enforcement: []domain.LadoEnforcement{domain.LadoEnforcementPrompt},
		Conflicts:   []domain.LadoRuleID{domain.LadoRuleKoreanCommits},
	}.Normalize()
	return ladopolicy.RuleView{Rule: domain.LadoRule{
		ID: "lado-rule-1",
		Version: domain.LadoRuleVersion{
			ID: "v-1", RuleID: "lado-rule-1", Number: 1, Content: content, SHA256: content.SHA256(), Actor: "api", CreatedAt: ladoRuleTime,
		},
		Global: domain.LadoBindingOff,
		Overrides: []domain.LadoRuleBinding{
			{RuleID: "lado-rule-1", Scope: domain.LadoScopeProject, ScopeID: "mer", State: domain.LadoBindingForceOn, UpdatedAt: ladoRuleTime},
			{RuleID: "lado-rule-1", Scope: domain.LadoScopeTask, ScopeID: "lado-task-gone", State: domain.LadoBindingForceOff, UpdatedAt: ladoRuleTime},
		},
		CreatedAt:  ladoRuleTime,
		ArchivedAt: ladoRuleTime.Add(2 * time.Hour),
	}, Labels: map[string]string{ladopolicy.LabelKey(domain.LadoScopeProject, "mer"): "Mercury"}}
}

func sampleImpact() ladopolicy.Impact {
	return ladopolicy.Impact{
		Projects: []ladopolicy.ImpactProject{{ID: "mer", Name: "Mercury"}},
		Tasks:    []ladopolicy.ImpactTask{{ID: "lado-task-1", ProjectID: "mer", Title: "Add login", IssueRef: "github:acme/demo#12"}},
	}
}

func wantImpact() controllers.LadoRuleImpactResponse {
	return controllers.LadoRuleImpactResponse{
		Projects: []controllers.LadoImpactProjectResponse{{ID: "mer", Name: "Mercury"}},
		Tasks:    []controllers.LadoImpactTaskResponse{{ID: "lado-task-1", ProjectID: "mer", Title: "Add login", IssueRef: "github:acme/demo#12"}},
	}
}

// ladoHeaderRequest sends a request with extra headers.
func ladoHeaderRequest(t *testing.T, srv *httptest.Server, method, path, body string, header map[string]string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range header {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode
}

func wantLadoErrorCode(t *testing.T, body []byte, status, wantStatus int, wantCode string) {
	t.Helper()
	var apiErr envelope.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil || status != wantStatus || apiErr.Code != wantCode {
		t.Fatalf("status = %d, body %s; want %d %s", status, body, wantStatus, wantCode)
	}
}

func TestLadoAPI_RuleRoutesAnswerNotImplementedWithoutAService(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/rules", ""},
		{"POST", "/api/v1/lado/rules", `{"name":"x","instruction":"y","scope":"global"}`},
		{"GET", "/api/v1/lado/rules/lado-rule-1", ""},
		{"PATCH", "/api/v1/lado/rules/lado-rule-1", `{"baseVersion":1,"name":"x"}`},
		{"DELETE", "/api/v1/lado/rules/lado-rule-1", ""},
		{"PUT", "/api/v1/lado/bindings", `{"ruleId":"lado-rule-1","scope":"global","state":"on"}`},
		{"GET", "/api/v1/lado/effective?projectId=mer", ""},
	} {
		body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
		if status != http.StatusNotImplemented {
			t.Errorf("%s %s status = %d, body %s; want 501", route.method, route.path, status, body)
		}
	}
}

func TestLadoAPI_ListRules(t *testing.T) {
	svc := &fakeLadoRuleService{views: []ladopolicy.RuleView{ceilingRuleView(), userRuleView()}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/rules?includeArchived=true", "")
	if status != http.StatusOK || !svc.gotArchived {
		t.Fatalf("status = %d, body %s, includeArchived %v", status, body, svc.gotArchived)
	}
	resp := decodeLadoJSON[controllers.LadoRuleListResponse](t, body)
	if len(resp.Rules) != 2 {
		t.Fatalf("rules = %+v", resp.Rules)
	}
	ceiling, user := resp.Rules[0], resp.Rules[1]
	if ceiling.ID != "agent.permission.ceiling" || !ceiling.Builtin || !ceiling.Protected || ceiling.Archived || ceiling.ArchivedAt != nil ||
		ceiling.Global != "on" || ceiling.Ceiling == nil || ceiling.Ceiling.Mode != "auto" || ceiling.Strength != "prevents" ||
		ceiling.Version != 2 || ceiling.VersionID != "v-ceiling-2" || !ceiling.UpdatedAt.Equal(ladoRuleTime.Add(time.Hour)) ||
		!reflect.DeepEqual(ceiling.Enforcement, []string{"tool_policy"}) || len(ceiling.Overrides) != 0 {
		t.Fatalf("ceiling rule = %+v", ceiling)
	}
	wantOverrides := []controllers.LadoRuleOverrideResponse{
		{Scope: "project", ScopeID: "mer", State: "force_on", Label: "Mercury"},
		{Scope: "task", ScopeID: "lado-task-gone", State: "force_off", Label: ""},
	}
	if user.ID != "lado-rule-1" || user.Name != "No force push" || user.Instruction != "Never force push." || user.Builtin || user.Protected ||
		!user.Archived || user.ArchivedAt == nil || !user.ArchivedAt.Equal(ladoRuleTime.Add(2*time.Hour)) || user.Global != "off" ||
		user.Ceiling != nil || user.Strength != "advisory" || !reflect.DeepEqual(user.ConflictsWith, []string{"git.commit.message.korean"}) ||
		!reflect.DeepEqual(user.Overrides, wantOverrides) || user.SHA256 != userRuleView().Rule.Version.SHA256 {
		t.Fatalf("user rule = %+v", user)
	}

	// The wire shape: lists are [] rather than null, and only the ceiling
	// carries a ceiling.
	var raw struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Rules[0]["overrides"]) != "[]" || string(raw.Rules[0]["conflictsWith"]) != "[]" || string(raw.Rules[0]["ceiling"]) != `{"mode":"auto"}` {
		t.Fatalf("ceiling rule JSON = %s", body)
	}
	if _, ok := raw.Rules[1]["ceiling"]; ok {
		t.Fatalf("a user rule carries a ceiling: %s", body)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/rules?includeArchived=maybe", "")
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")
}

func TestLadoAPI_CreateRuleDryRun(t *testing.T) {
	svc := &fakeLadoRuleService{result: ladopolicy.ChangeResult{Rule: userRuleView(), Impact: sampleImpact()}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc})

	req := `{"name":"No force push","category":"git","instruction":"Never force push.","conflictsWith":["git.commit.message.korean"],"scope":"project","scopeId":"mer","dryRun":true}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/rules", req)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	want := ladopolicy.CreateInput{
		Name: "No force push", Category: "git", Instruction: "Never force push.",
		Conflicts: []domain.LadoRuleID{domain.LadoRuleKoreanCommits},
		Scope:     domain.LadoScopeProject, ScopeID: "mer", DryRun: true,
	}
	if !reflect.DeepEqual(svc.gotCreate, want) {
		t.Fatalf("service got %+v, want %+v", svc.gotCreate, want)
	}
	resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body)
	if resp.Applied || resp.Rule == nil || resp.Rule.ID != "lado-rule-1" || !reflect.DeepEqual(resp.Impact, wantImpact()) {
		t.Fatalf("response = %+v", resp)
	}

	svc.result = ladopolicy.ChangeResult{Rule: userRuleView(), Impact: ladopolicy.Impact{}, Applied: true}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/rules", `{"name":"No force push","instruction":"Never force push.","scope":"global","on":true}`)
	resp = decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body)
	if status != http.StatusOK || !resp.Applied || svc.gotCreate.DryRun || !svc.gotCreate.On || svc.gotCreate.Scope != domain.LadoScopeGlobal ||
		resp.Impact.Projects == nil || resp.Impact.Tasks == nil {
		t.Fatalf("applied create: status %d, response %+v, service got %+v", status, resp, svc.gotCreate)
	}
}

func TestLadoAPI_UpdateRulePassesTheExactBodyToPresence(t *testing.T) {
	presence := &recordingPresence{}
	view := ceilingRuleView()
	svc := &fakeLadoRuleService{result: ladopolicy.ChangeResult{Rule: view, Impact: sampleImpact(), Applied: true}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc, LadoPresence: presence})

	// Odd spacing and key order: the proof signs these bytes, not a
	// re-encoding of them.
	path := "/api/v1/lado/rules/agent.permission.ceiling"
	sent := "{ \"ceiling\": {\"mode\":\"bypass-permissions\"},\n  \"baseVersion\": 2, \"reason\":\"network for the build\" }"
	body, status := ladoHeaderRequest(t, srv, "PATCH", path, sent, map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	if presence.method != "PATCH" || presence.path != path || string(presence.body) != sent || presence.header != "v1.proof-from-the-app" ||
		presence.action != domain.LadoPresenceRuleChange || presence.subject != "agent.permission.ceiling" {
		t.Fatalf("verifier got %s %s body %q header %q for %s %s", presence.method, presence.path, presence.body, presence.header, presence.action, presence.subject)
	}
	in := svc.gotUpdate
	if svc.gotID != domain.LadoRulePermissionCeiling || in.BaseVersion != 2 || in.CeilingMode == nil || *in.CeilingMode != domain.PermissionModeBypassPermissions ||
		in.Reason != "network for the build" || in.Name != nil || in.Conflicts != nil || in.Archived != nil || in.DryRun || svc.gotProof.Nonce != "BBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("service got %q %+v with proof %+v", svc.gotID, in, svc.gotProof)
	}
	resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body)
	if !resp.Applied || resp.Rule == nil || resp.Rule.Ceiling == nil || !reflect.DeepEqual(resp.Impact, wantImpact()) {
		t.Fatalf("response = %+v", resp)
	}

	// Content edits reach the service as pointers, an empty conflict list
	// included, so it can tell "clear" from "keep".
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/lado/rules/lado-rule-1", `{"baseVersion":1,"name":"No force pushes","conflictsWith":[],"archived":true,"dryRun":true}`)
	in = svc.gotUpdate
	if status != http.StatusOK || in.Name == nil || *in.Name != "No force pushes" || in.Conflicts == nil || len(*in.Conflicts) != 0 ||
		in.Archived == nil || !*in.Archived || !in.DryRun || in.CeilingMode != nil || in.Category != nil {
		t.Fatalf("edit: status %d body %s, service got %+v", status, body, in)
	}

	before := svc.calls
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/lado/rules/lado-rule-1", `{"baseVersion":1,"name":"x","enforcement":["tool_policy"]}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.calls != before {
		t.Fatalf("the service was called for a body with an unknown field")
	}
}

// The ceiling change through the real presence check and the real rule
// service: only a proof the app made over the exact body changes the
// ceiling, and a proof works once.
func TestLadoAPI_CeilingChangeChecksPresenceEndToEnd(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	rules := ladopolicy.New(ladopolicy.Deps{Store: store, Projects: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := rules.EnsureBuiltins(ctx); err != nil {
		t.Fatalf("EnsureBuiltins: %v", err)
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: rules, LadoPresence: presenceVerifier()})
	path := "/api/v1/lado/rules/" + string(domain.LadoRulePermissionCeiling)
	change := `{"baseVersion":1,"ceiling":{"mode":"bypass-permissions"}}`
	// wantCeiling checks the active version of the ceiling in the store.
	wantCeiling := func(t *testing.T, number int, mode domain.PermissionMode) {
		t.Helper()
		rule, ok, err := store.LadoRule(ctx, domain.LadoRulePermissionCeiling)
		if err != nil || !ok {
			t.Fatalf("LadoRule = (%v, %v)", ok, err)
		}
		var config domain.LadoCeilingConfig
		if err := json.Unmarshal(rule.Version.Content.Config, &config); err != nil || rule.Version.Number != number || config.Mode != mode {
			t.Fatalf("ceiling = version %d %s (%v), want version %d %s", rule.Version.Number, rule.Version.Content.Config, err, number, mode)
		}
	}

	t.Run("without a proof", func(t *testing.T) {
		body, status, _ := doRequest(t, srv, "PATCH", path, change)
		wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REQUIRED")
		wantCeiling(t, 1, domain.PermissionModeAuto)
	})
	t.Run("a body changed after signing", func(t *testing.T) {
		body, status := signedRequest(t, srv, "PATCH", path, change, `{"baseVersion":1,"ceiling":{"mode":"accept-edits"}}`)
		wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
		wantCeiling(t, 1, domain.PermissionModeAuto)
	})
	t.Run("a proof over the body", func(t *testing.T) {
		body, status := signedRequest(t, srv, "PATCH", path, change, change)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body %s", status, body)
		}
		resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body)
		if !resp.Applied || resp.Rule == nil || resp.Rule.Version != 2 || resp.Rule.Ceiling == nil || resp.Rule.Ceiling.Mode != string(domain.PermissionModeBypassPermissions) {
			t.Fatalf("response = %+v, want version 2 at bypass-permissions", resp)
		}
		wantCeiling(t, 2, domain.PermissionModeBypassPermissions)
	})
	t.Run("the same proof again", func(t *testing.T) {
		// Sent again as it was, the request names version 1, which is gone.
		body, status := signedRequest(t, srv, "PATCH", path, change, change)
		wantLadoErrorCode(t, body, status, http.StatusConflict, "LADO_RULE_CHANGED")
		// signedRequest signs with one nonce, so this proof over the next
		// change reuses the stored one.
		next := `{"baseVersion":2,"ceiling":{"mode":"accept-edits"}}`
		body, status = signedRequest(t, srv, "PATCH", path, next, next)
		wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_REPLAYED")
		wantCeiling(t, 2, domain.PermissionModeBypassPermissions)
	})
}

func TestLadoAPI_DeleteRule(t *testing.T) {
	svc := &fakeLadoRuleService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc})

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/lado/rules/lado-rule-1", "")
	if status != http.StatusNoContent || len(body) != 0 || svc.gotID != "lado-rule-1" {
		t.Fatalf("status = %d, body %q, service got %q", status, body, svc.gotID)
	}

	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{apierr.Conflict("LADO_RULE_REFERENCED", "An approved plan used this rule, so LADO keeps it. Archive it instead.", map[string]any{"rule": "lado-rule-1"}), http.StatusConflict, "LADO_RULE_REFERENCED"},
		{apierr.New(apierr.KindForbidden, "LADO_RULE_BUILTIN", "LADO's built-in rules cannot be deleted.", map[string]any{"rule": "lado-rule-1"}), http.StatusForbidden, "LADO_RULE_BUILTIN"},
		{apierr.New(apierr.KindNotFound, "LADO_RULE_NOT_FOUND", "Unknown rule", map[string]any{"rule": "lado-rule-1"}), http.StatusNotFound, "LADO_RULE_NOT_FOUND"},
	} {
		svc.err = tc.err
		body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/lado/rules/lado-rule-1", "")
		wantLadoErrorCode(t, body, status, tc.status, tc.code)
		if apiErr := decodeLadoJSON[envelope.APIError](t, body); apiErr.Details["rule"] != "lado-rule-1" {
			t.Fatalf("envelope = %s, want the service's details", body)
		}
	}
}

func TestLadoAPI_SetBinding(t *testing.T) {
	svc := &fakeLadoRuleService{result: ladopolicy.ChangeResult{Rule: userRuleView(), Impact: sampleImpact()}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/bindings", `{"ruleId":"lado-rule-1","scope":"task","scopeId":"lado-task-1","state":"force_off","dryRun":true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	want := ladopolicy.BindingInput{RuleID: "lado-rule-1", Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingForceOff, DryRun: true}
	if svc.gotBinding != want {
		t.Fatalf("service got %+v, want %+v", svc.gotBinding, want)
	}
	resp := decodeLadoJSON[controllers.LadoRuleChangeResponse](t, body)
	if resp.Applied || resp.Rule == nil || !reflect.DeepEqual(resp.Impact, wantImpact()) {
		t.Fatalf("response = %+v", resp)
	}

	// Values the service refuses still reach it as sent.
	doRequest(t, srv, "PUT", "/api/v1/lado/bindings", `{"ruleId":"plan.approval.required","scope":"global","state":"inherit"}`)
	want = ladopolicy.BindingInput{RuleID: domain.LadoRulePlanApproval, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingInherit}
	if svc.gotBinding != want {
		t.Fatalf("service got %+v, want %+v", svc.gotBinding, want)
	}
	svc.err = apierr.New(apierr.KindForbidden, "LADO_RULE_PROTECTED", "This rule is protected.", nil)
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/bindings", `{"ruleId":"plan.approval.required","scope":"global","state":"off"}`)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_RULE_PROTECTED")
}

func TestLadoAPI_Effective(t *testing.T) {
	ceiling := ceilingRuleView().Rule
	user := userRuleView().Rule
	user.ArchivedAt = time.Time{}
	view := ladopolicy.EffectiveView{
		Resolution: ladopolicy.Resolution{
			Entries: []ladopolicy.Entry{
				{Rule: ceiling, On: true, Source: ladopolicy.Source{Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOn}, Project: domain.LadoBindingInherit, Task: domain.LadoBindingInherit},
				{Rule: user, On: false, Source: ladopolicy.Source{Scope: domain.LadoScopeTask, ScopeID: "lado-task-1", State: domain.LadoBindingForceOff}, Project: domain.LadoBindingForceOn, Task: domain.LadoBindingForceOff},
			},
			Ceiling:      domain.PermissionModeAuto,
			Mode:         domain.PermissionModeAcceptEdits,
			Prompt:       "",
			Capabilities: []ladopolicy.Capability{{Class: ladopolicy.CapabilityPermissionMode, Status: ladopolicy.CapabilityUnverified}},
			Problems: []domain.LadoPolicyProblem{
				{Code: ladopolicy.ProblemCapabilityUnverified, Severity: domain.LadoSeverityBlocking, Details: map[string]string{"agent": "aider", "capability": "permission_mode"}},
				{Code: ladopolicy.ProblemPlanCeilingClamped, Severity: domain.LadoSeverityWarning, Rules: []domain.LadoRuleID{domain.LadoRulePermissionCeiling}},
			},
			Content: ladopolicy.SnapshotContent{ProjectID: "mer", TaskID: "lado-task-1"},
			SHA256:  strings.Repeat("c", 64),
		},
		Agent:   ladopolicy.AgentChoice{Harness: "aider", Model: "sonnet", Source: ladopolicy.AgentFromTask},
		AORules: []ladopolicy.AORule{{Kind: ladopolicy.AORuleWorkerInstructionsFile, Path: ".ao/rules.md", ReadError: "file not found"}},
		Approved: &ladopolicy.ApprovedView{
			Snapshot:       domain.LadoPolicySnapshot{SHA256: strings.Repeat("d", 64), Blocking: false},
			RevisionNumber: 3,
			ApprovedAt:     ladoRuleTime,
			Added:          []domain.LadoRuleID{},
			Removed:        []domain.LadoRuleID{"lado-rule-1"},
			Changed:        []domain.LadoRuleID{},
		},
	}
	svc := &fakeLadoRuleService{effective: view}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoRules: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/effective?taskId=lado-task-1", "")
	if status != http.StatusOK || svc.gotEffective != (ladopolicy.EffectiveInput{TaskID: "lado-task-1"}) {
		t.Fatalf("status = %d, body %s, service got %+v", status, body, svc.gotEffective)
	}
	resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body)
	if resp.ProjectID != "mer" || resp.TaskID != "lado-task-1" || resp.Agent != (controllers.LadoEffectiveAgentResponse{Harness: "aider", Model: "sonnet", Source: "task"}) ||
		resp.PermissionCeiling != "auto" || resp.PermissionMode != "accept-edits" || !resp.Blocking || resp.SnapshotSHA256 != strings.Repeat("c", 64) || resp.Prompt != "" {
		t.Fatalf("response = %+v", resp)
	}
	wantRules := []controllers.LadoEffectiveRuleResponse{
		{RuleID: "agent.permission.ceiling", Name: "Permission ceiling", Category: "tools", Builtin: true, Protected: true, Version: 2, On: true, Strength: "prevents",
			Enforcement: []string{"tool_policy"}, Global: "on", Project: "inherit", Task: "inherit", Source: controllers.LadoRuleSourceResponse{Scope: "global", State: "on"}},
		{RuleID: "lado-rule-1", Name: "No force push", Category: "git", Version: 1, On: false, Strength: "advisory",
			Enforcement: []string{"prompt"}, Global: "off", Project: "force_on", Task: "force_off", Source: controllers.LadoRuleSourceResponse{Scope: "task", ScopeID: "lado-task-1", State: "force_off"}},
	}
	if !reflect.DeepEqual(resp.Rules, wantRules) {
		t.Fatalf("rules = %+v\nwant %+v", resp.Rules, wantRules)
	}
	wantProblems := []controllers.LadoPolicyProblemResponse{
		{Code: "LADO_AGENT_CAPABILITY_UNVERIFIED", Severity: "blocking", Rules: []string{}, Details: map[string]string{"agent": "aider", "capability": "permission_mode"}},
		{Code: "LADO_PLAN_CEILING_CLAMPED", Severity: "warning", Rules: []string{"agent.permission.ceiling"}, Details: map[string]string{}},
	}
	if !reflect.DeepEqual(resp.Problems, wantProblems) ||
		!reflect.DeepEqual(resp.AORules, []controllers.LadoAORuleResponse{{Kind: "worker_instructions_file", Path: ".ao/rules.md", ReadError: "file not found"}}) ||
		!reflect.DeepEqual(resp.Capabilities, []controllers.LadoCapabilityResponse{{Class: "permission_mode", Status: "unverified"}}) {
		t.Fatalf("problems %+v, aoRules %+v, capabilities %+v", resp.Problems, resp.AORules, resp.Capabilities)
	}
	wantApproved := &controllers.LadoApprovedRulesResponse{
		SnapshotSHA256: strings.Repeat("d", 64), ApprovedAt: ladoRuleTime, Revision: 3,
		Added: []string{}, Removed: []string{"lado-rule-1"}, Changed: []string{},
		SetupAdded: []string{}, SetupRemoved: []string{}, SetupChanged: []string{},
		Setup: []controllers.LadoSnapshotSetupItemResponse{}, Accepted: []controllers.LadoSetupAcceptedConflictResponse{},
	}
	if !reflect.DeepEqual(resp.Approved, wantApproved) {
		t.Fatalf("approved = %+v, want %+v", resp.Approved, wantApproved)
	}

	// A project has no approval to compare.
	svc.effective = ladopolicy.EffectiveView{Resolution: ladopolicy.Resolution{Ceiling: domain.PermissionModeAuto, Mode: domain.PermissionModeAuto, Content: ladopolicy.SnapshotContent{ProjectID: "mer"}}}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/effective?projectId=mer", "")
	if status != http.StatusOK || svc.gotEffective != (ladopolicy.EffectiveInput{ProjectID: "mer"}) {
		t.Fatalf("project: status = %d, body %s, service got %+v", status, body, svc.gotEffective)
	}
	if resp := decodeLadoJSON[controllers.LadoEffectiveResponse](t, body); resp.ProjectID != "mer" || resp.TaskID != "" || resp.Approved != nil || resp.Blocking ||
		resp.Rules == nil || resp.AORules == nil || resp.Capabilities == nil || resp.Problems == nil {
		t.Fatalf("project response = %+v", resp)
	}

	before := svc.calls
	for _, query := range []string{"", "?projectId=mer&taskId=lado-task-1", "?projectId=&taskId="} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/effective"+query, "")
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_EFFECTIVE_SCOPE_REQUIRED")
	}
	if svc.calls != before {
		t.Fatalf("the service was called without exactly one scope")
	}
}
