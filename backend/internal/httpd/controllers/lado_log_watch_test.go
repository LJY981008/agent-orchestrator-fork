package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladologwatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var ladoLogTime = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// The TLS file paths and contents a request names; no response may carry
// them.
const (
	ladoLogCAPath   = "/home/dev/certs/prod-ca.pem"
	ladoLogCertPath = "/home/dev/certs/prod-cert.pem"
	ladoLogKeyPath  = "/home/dev/certs/prod-key.pem"
	ladoLogCopyPath = "/home/dev/.ao/data/lado/credentials/docker/0123456789abcdef/ca.pem"
)

// fakeLadoLogWatch is a LadoLogWatchService that keeps the settings it was
// given, the way the runner does, and records its calls.
type fakeLadoLogWatch struct {
	view       domain.LadoLogWatchView
	err        error
	updates    int
	gotProject domain.ProjectID
	gotInput   domain.LadoLogWatchInput
	gotSource  domain.LadoLogSourceID
	gotTLS     domain.LadoLogTLSImport
	check      domain.LadoLogWatchCheck
	ci         domain.LadoLogCIImport
	incidents  []domain.LadoLogFingerprint
	gotIgnore  struct {
		id      domain.LadoLogFingerprintID
		ignored bool
	}
}

var _ controllers.LadoLogWatchService = (*fakeLadoLogWatch)(nil)

func newFakeLadoLogWatch() *fakeLadoLogWatch {
	return &fakeLadoLogWatch{view: domain.LadoLogWatchView{
		Settings:  domain.DefaultLadoLogWatchSettings("mer"),
		LadoMode:  true,
		TLSStored: map[domain.LadoLogSourceID]domain.LadoLogTLSStored{},
	}}
}

func (f *fakeLadoLogWatch) Settings(_ context.Context, project domain.ProjectID) (domain.LadoLogWatchView, error) {
	f.gotProject = project
	return f.view, f.err
}

func (f *fakeLadoLogWatch) UpdateSettings(_ context.Context, project domain.ProjectID, in domain.LadoLogWatchInput) (domain.LadoLogWatchView, error) {
	f.updates++
	f.gotProject, f.gotInput = project, in
	if f.err != nil {
		return domain.LadoLogWatchView{}, f.err
	}
	s := domain.DefaultLadoLogWatchSettings(project)
	s.Enabled, s.Repository, s.Include, s.Exclude = in.Enabled, in.Repository, in.Include, in.Exclude
	s.HourlyIssueCap, s.AutoPlan, s.AutoPlanDailyCap, s.AutoPlanMinCount = in.HourlyIssueCap, in.AutoPlan, in.AutoPlanDailyCap, in.AutoPlanMinCount
	s.DisconnectAlertMinutes, s.UpdatedAt = in.DisconnectAlertMinutes, ladoLogTime
	for i, src := range in.Sources {
		id := src.ID
		if id == "" {
			id = domain.LadoLogSourceID(fmt.Sprintf("src-%d", i+1))
		}
		source := domain.LadoLogSource{ID: id, Kind: domain.LadoLogSourceKindDockerTCP, DisplayName: src.DisplayName, Endpoint: src.Endpoint, TLS: src.TLS, Paused: src.Paused, CertKey: "0123456789abcdef"}
		for j, t := range src.Targets {
			tid := t.ID
			if tid == "" {
				tid = domain.LadoLogTargetID(fmt.Sprintf("%s-t%d", id, j+1))
			}
			source.Targets = append(source.Targets, domain.LadoLogTarget{ID: tid, SourceID: id, Kind: t.Kind, Value: t.Value, ComposeProject: t.ComposeProject, Service: t.Service, Paused: t.Paused})
		}
		s.Sources = append(s.Sources, source)
	}
	f.view.Settings = s
	return f.view, nil
}

func (f *fakeLadoLogWatch) ImportTLS(_ context.Context, project domain.ProjectID, source domain.LadoLogSourceID, in domain.LadoLogTLSImport) (domain.LadoLogWatchView, error) {
	f.gotProject, f.gotSource, f.gotTLS = project, source, in
	if f.err != nil {
		return domain.LadoLogWatchView{}, f.err
	}
	f.view.TLSStored[source] = domain.LadoLogTLSStored{CA: true, Cert: in.CertPath != "", Key: in.KeyPath != ""}
	return f.view, nil
}

func (f *fakeLadoLogWatch) DeleteTLS(_ context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchView, error) {
	f.gotProject, f.gotSource = project, source
	if f.err != nil {
		return domain.LadoLogWatchView{}, f.err
	}
	delete(f.view.TLSStored, source)
	return f.view, nil
}

func (f *fakeLadoLogWatch) Check(_ context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchCheck, error) {
	f.gotProject, f.gotSource = project, source
	return f.check, f.err
}

func (f *fakeLadoLogWatch) ImportFromCI(_ context.Context, project domain.ProjectID) (domain.LadoLogCIImport, error) {
	f.gotProject = project
	return f.ci, f.err
}

func (f *fakeLadoLogWatch) Incidents(_ context.Context, project domain.ProjectID) ([]domain.LadoLogFingerprint, error) {
	f.gotProject = project
	return f.incidents, f.err
}

func (f *fakeLadoLogWatch) SetIgnored(_ context.Context, project domain.ProjectID, id domain.LadoLogFingerprintID, ignored bool) (domain.LadoLogFingerprint, error) {
	f.gotProject = project
	f.gotIgnore.id, f.gotIgnore.ignored = id, ignored
	if f.err != nil {
		return domain.LadoLogFingerprint{}, f.err
	}
	return domain.LadoLogFingerprint{ID: id, ProjectID: project, Fingerprint: "fp", Service: "grp/api", ExceptionKind: "NullPointerException", Count: 3, FirstSeenAt: ladoLogTime, LastSeenAt: ladoLogTime, Ignored: ignored, IgnoredAt: ladoLogTime}, nil
}

// ladoLogWatchBody is a valid settings body with sources replaced by the
// given JSON array.
func ladoLogWatchBody(sources string) string {
	return `{"enabled":true,"repository":"github:acme/demo","include":["OutOfMemory"],"exclude":["healthcheck"],` +
		`"hourlyIssueCap":5,"autoPlan":true,"autoPlanDailyCap":5,"autoPlanMinCount":2,"disconnectAlertMinutes":10,"sources":` + sources + `}`
}

func ladoLogErrorField(t *testing.T, body []byte) string {
	t.Helper()
	var apiErr envelope.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	field, _ := apiErr.Details["field"].(string)
	return field
}

// A project without settings reads the defaults; a PUT reaches the service
// as the domain input, and the response carries each source's status, an
// insecure flag without TLS and its targets, never a TLS copy's path.
func TestLadoAPI_LogWatchSettings(t *testing.T) {
	svc := newFakeLadoLogWatch()
	srv := newLadoTestServer(t, httpd.APIDeps{LadoLogWatch: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/log-watch", "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoLogWatchResponse](t, body)
	if svc.gotProject != "mer" || got.ProjectID != "mer" || got.Enabled || !got.LadoMode || got.HourlyIssueCap != 5 || !got.AutoPlan ||
		got.AutoPlanDailyCap != 5 || got.AutoPlanMinCount != 1 || got.DisconnectAlertMinutes != 10 || got.UpdatedAt != nil ||
		got.Include == nil || got.Exclude == nil || got.Sources == nil || len(got.Sources) != 0 {
		t.Fatalf("defaults = %+v", got)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/log-watch", ladoLogWatchBody(`[
		{"displayName":"prod1","endpoint":"tcp://192.0.2.10:2375","targets":[{"kind":"compose_project","value":"grp"}]},
		{"id":"s2","kind":"docker_tcp","displayName":"prod2","endpoint":"tcp://prod2.example.test:2376","tls":true,"paused":true,
		 "targets":[{"id":"t9","kind":"compose_service","value":"api","composeProject":"grp","service":"grp/api","paused":true},{"kind":"label","value":"app=web"}]}]`))
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	in := svc.gotInput
	if svc.updates != 1 || !in.Enabled || in.Repository != "github:acme/demo" || len(in.Include) != 1 || len(in.Exclude) != 1 || in.AutoPlanMinCount != 2 ||
		len(in.Sources) != 2 || in.Sources[0].ID != "" || in.Sources[0].Endpoint != "tcp://192.0.2.10:2375" || in.Sources[0].TLS ||
		in.Sources[1].ID != "s2" || !in.Sources[1].TLS || !in.Sources[1].Paused || len(in.Sources[1].Targets) != 2 {
		t.Fatalf("input = %+v", in)
	}
	if tgt := in.Sources[1].Targets[0]; tgt.ID != "t9" || tgt.Kind != domain.LadoLogTargetComposeService || tgt.Value != "api" || tgt.ComposeProject != "grp" || tgt.Service != "grp/api" || !tgt.Paused {
		t.Fatalf("target input = %+v", tgt)
	}
	got = decodeLadoJSON[controllers.LadoLogWatchResponse](t, body)
	if len(got.Sources) != 2 || got.Sources[0].ID != "src-1" || got.Sources[0].Kind != "docker_tcp" || !got.Sources[0].Status.Insecure ||
		got.Sources[0].Status.State != "off" || got.Sources[1].Status.Insecure || got.Sources[1].Targets[0].ID != "t9" || got.UpdatedAt == nil {
		t.Fatalf("PUT response = %+v", got)
	}

	// The daemon's status of a source: its state, a detail with a copy's
	// path taken out, and a target's instances.
	svc.view.Statuses = []domain.LadoLogSourceStatus{{
		SourceID: "s2", State: domain.LadoLogSourceStateError, DownSince: ladoLogTime,
		Detail:  "open " + ladoLogCopyPath + ": permission denied",
		Targets: []domain.LadoLogTargetStatus{{TargetID: "t9", Instances: 2, LastLineAt: ladoLogTime}},
	}}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/log-watch", "")
	got = decodeLadoJSON[controllers.LadoLogWatchResponse](t, body)
	s2 := got.Sources[1]
	if s2.Status.State != "error" || s2.Status.DownSince == nil || !strings.Contains(s2.Status.Detail, "permission denied") ||
		s2.Targets[0].Status.Instances != 2 || s2.Targets[0].Status.LastLineAt == nil || s2.Targets[1].Status.Instances != 0 {
		t.Fatalf("status = %+v", s2)
	}

	// A TLS import names files; the response tells only what is stored.
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/sources/s2/tls",
		fmt.Sprintf(`{"caPath":%q,"certPath":%q,"keyPath":%q}`, ladoLogCAPath, ladoLogCertPath, ladoLogKeyPath))
	if status != http.StatusOK {
		t.Fatalf("POST tls = %d %s", status, body)
	}
	if svc.gotSource != "s2" || svc.gotTLS != (domain.LadoLogTLSImport{CAPath: ladoLogCAPath, CertPath: ladoLogCertPath, KeyPath: ladoLogKeyPath}) {
		t.Fatalf("tls input = %s %+v", svc.gotSource, svc.gotTLS)
	}
	got = decodeLadoJSON[controllers.LadoLogWatchResponse](t, body)
	if stored := got.Sources[1].TLSStored; !stored.CA || !stored.Cert || !stored.Key {
		t.Fatalf("tlsStored = %+v", stored)
	}
	for _, leak := range []string{ladoLogCAPath, ladoLogCertPath, ladoLogKeyPath, ladoLogCopyPath, "credentials", "0123456789abcdef", "certKey", "BEGIN"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("response carries %q: %s", leak, body)
		}
	}
	body, status, _ = doRequest(t, srv, "DELETE", "/api/v1/lado/projects/mer/log-watch/sources/s2/tls", "")
	if got := decodeLadoJSON[controllers.LadoLogWatchResponse](t, body); status != http.StatusOK || got.Sources[1].TLSStored.CA {
		t.Fatalf("DELETE tls = %d %s", status, body)
	}
}

// A refused setting is 400 LADO_LOG_WATCH_INVALID naming its field, and the
// service never sees it.
func TestLadoAPI_LogWatchRefusesInvalidSettings(t *testing.T) {
	svc := newFakeLadoLogWatch()
	srv := newLadoTestServer(t, httpd.APIDeps{LadoLogWatch: svc})
	source := func(endpoint, targets string) string {
		return fmt.Sprintf(`[{"displayName":"prod1","endpoint":%q,"targets":%s}]`, endpoint, targets)
	}
	ok := `[{"kind":"compose_project","value":"grp"}]`
	for _, tc := range []struct {
		name, body, field string
	}{
		{"unix", ladoLogWatchBody(source("unix:///var/run/docker.sock", ok)), "sources[0].endpoint"},
		{"npipe", ladoLogWatchBody(source("npipe:////./pipe/docker_engine", ok)), "sources[0].endpoint"},
		{"ssh", ladoLogWatchBody(source("ssh://deploy@192.0.2.10", ok)), "sources[0].endpoint"},
		{"http", ladoLogWatchBody(source("http://192.0.2.10:2375", ok)), "sources[0].endpoint"},
		{"user info", ladoLogWatchBody(source("tcp://user:pw@192.0.2.10:2375", ok)), "sources[0].endpoint"},
		{"no port", ladoLogWatchBody(source("tcp://192.0.2.10", ok)), "sources[0].endpoint"},
		{"port 0", ladoLogWatchBody(source("tcp://192.0.2.10:0", ok)), "sources[0].endpoint"},
		{"port 70000", ladoLogWatchBody(source("tcp://192.0.2.10:70000", ok)), "sources[0].endpoint"},
		{"path", ladoLogWatchBody(source("tcp://192.0.2.10:2375/v1.43", ok)), "sources[0].endpoint"},
		{"query", ladoLogWatchBody(source("tcp://192.0.2.10:2375?x=1", ok)), "sources[0].endpoint"},
		{"dash host", ladoLogWatchBody(source("tcp://-oProxyCommand:2375", ok)), "sources[0].endpoint"},
		{"compose project", ladoLogWatchBody(source("tcp://192.0.2.10:2375", `[{"kind":"compose_project","value":"grp;rm"}]`)), "sources[0].targets[0].value"},
		{"compose service", ladoLogWatchBody(source("tcp://192.0.2.10:2375", `[{"kind":"compose_service","value":"api","composeProject":"-x"}]`)), "sources[0].targets[0].composeProject"},
		{"container name", ladoLogWatchBody(source("tcp://192.0.2.10:2375", `[{"kind":"name","value":"/api"}]`)), "sources[0].targets[0].value"},
		{"label", ladoLogWatchBody(source("tcp://192.0.2.10:2375", `[{"kind":"label","value":"app=$(id)"}]`)), "sources[0].targets[0].value"},
		{"target kind", ladoLogWatchBody(source("tcp://192.0.2.10:2375", `[{"kind":"pod","value":"api"}]`)), "sources[0].targets[0].kind"},
		{"regexp", strings.Replace(ladoLogWatchBody(`[]`), `"OutOfMemory"`, `"(unclosed"`, 1), "include[0]"},
		{"cap", strings.Replace(ladoLogWatchBody(`[]`), `"hourlyIssueCap":5`, `"hourlyIssueCap":0`, 1), "hourlyIssueCap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/log-watch", tc.body)
			wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_WATCH_INVALID")
			if field := ladoLogErrorField(t, body); field != tc.field {
				t.Fatalf("field = %q, want %q (%s)", field, tc.field, body)
			}
		})
	}
	if svc.updates != 0 {
		t.Fatalf("the service saw %d refused settings", svc.updates)
	}

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/log-watch", `{"enabled":true,"certKey":"x"}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")

	// The domain's error unwrapped from the service gets its code too, and
	// the service's apierr errors go out as they are.
	svc.err = fmt.Errorf("save: %w", &domain.LadoLogWatchInvalidError{Field: "repository", Message: "unknown repository"})
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/log-watch", ladoLogWatchBody(`[]`))
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_WATCH_INVALID")
	if field := ladoLogErrorField(t, body); field != "repository" {
		t.Fatalf("field = %q", field)
	}
	svc.err = domain.ErrLadoLogSourceNotFound
	body, status, _ = doRequest(t, srv, "DELETE", "/api/v1/lado/projects/mer/log-watch/sources/nope/tls", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_LOG_SOURCE_NOT_FOUND")
	svc.err = apierr.Invalid("LADO_LOG_CERT_KEY_ENCRYPTED", "암호 걸린 키는 쓸 수 없습니다", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/sources/s1/tls", fmt.Sprintf(`{"caPath":%q,"certPath":%q,"keyPath":%q}`, ladoLogCAPath, ladoLogCertPath, ladoLogKeyPath))
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_CERT_KEY_ENCRYPTED")
	svc.err = apierr.Invalid("LADO_LOG_CERT_INVALID", "PEM 인증서가 아닙니다", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/sources/s1/tls", fmt.Sprintf(`{"caPath":%q}`, ladoLogCAPath))
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_CERT_INVALID")
	svc.err = nil

	// The TLS body needs a CA, and a certificate goes with its key.
	for _, bad := range []struct{ body, field string }{
		{`{"caPath":""}`, "caPath"},
		{fmt.Sprintf(`{"caPath":%q,"certPath":%q}`, ladoLogCAPath, ladoLogCertPath), "certPath"},
		{fmt.Sprintf(`{"caPath":%q,"keyPath":%q}`, ladoLogCAPath, ladoLogKeyPath), "certPath"},
	} {
		svc.gotTLS = domain.LadoLogTLSImport{}
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/sources/s1/tls", bad.body)
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_WATCH_INVALID")
		if field := ladoLogErrorField(t, body); field != bad.field || svc.gotTLS != (domain.LadoLogTLSImport{}) {
			t.Fatalf("%s: field %q, service got %+v", bad.body, field, svc.gotTLS)
		}
		if strings.Contains(string(body), ladoLogCAPath) {
			t.Fatalf("refusal carries the path: %s", body)
		}
	}
}

// The check, the CI import, the incidents and ignoring a fingerprint.
func TestLadoAPI_LogWatchCheckImportIncidents(t *testing.T) {
	svc := newFakeLadoLogWatch()
	srv := newLadoTestServer(t, httpd.APIDeps{LadoLogWatch: svc})

	svc.check = domain.LadoLogWatchCheck{OK: true, EngineVersion: "27.3.1", APIVersion: "1.47", Insecure: true, Instances: []domain.LadoLogInstance{
		{TargetID: "t1", Name: "grp-api-1", ID: "0123456789ab", Image: "registry.example.test/grp/api:1.2", Service: "grp/api", State: "running"},
	}}
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/check", `{"sourceId":"s1"}`)
	check := decodeLadoJSON[controllers.LadoLogWatchCheckResponse](t, body)
	if status != http.StatusOK || svc.gotSource != "s1" || !check.OK || check.APIVersion != "1.47" || !check.Insecure ||
		len(check.Instances) != 1 || check.Instances[0].Service != "grp/api" || check.Instances[0].TargetID != "t1" {
		t.Fatalf("check = %d %s", status, body)
	}
	svc.check = domain.LadoLogWatchCheck{OK: false, Detail: "dial tcp: connection refused"}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/check", `{"sourceId":"s1"}`)
	if check := decodeLadoJSON[controllers.LadoLogWatchCheckResponse](t, body); status != http.StatusOK || check.OK || check.Detail == "" || check.Instances == nil {
		t.Fatalf("failed check = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/check", `{}`)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "LADO_LOG_WATCH_INVALID")

	svc.ci = domain.LadoLogCIImport{Files: []domain.LadoLogCIFile{{
		Path:            ".gitlab-ci.yml",
		Hosts:           []domain.LadoLogCIHost{{Variable: "PROD1_HOST", DisplayName: "prod1", Endpoint: "tcp://192.0.2.10:2375"}, {Variable: "SECRET_HOST", DisplayName: "secret", Unresolved: "$SECRET_HOST"}},
		ComposeProjects: []string{"grp"},
	}}}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/import-ci", "")
	ci := decodeLadoJSON[controllers.LadoLogCIImportResponse](t, body)
	if status != http.StatusOK || len(ci.Files) != 1 || len(ci.Files[0].Hosts) != 2 || ci.Files[0].Hosts[1].Unresolved != "$SECRET_HOST" ||
		len(ci.Files[0].ComposeProjects) != 1 || ci.Files[0].Notes == nil || svc.updates != 0 {
		t.Fatalf("import-ci = %d %s", status, body)
	}
	svc.err = apierr.NotFound("LADO_LOG_CI_NOT_FOUND", "no .gitlab-ci.yml")
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/import-ci", "")
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_LOG_CI_NOT_FOUND")
	svc.err = nil

	svc.incidents = []domain.LadoLogFingerprint{{
		ID: 7, ProjectID: "mer", Fingerprint: "abc", Service: "grp/api", ExceptionKind: "NullPointerException", Location: "com.example.Api.get",
		Count: 4, FirstSeenAt: ladoLogTime, LastSeenAt: ladoLogTime.Add(time.Minute), TaskID: "lado-task-1", IssueRef: "github:acme/demo#12",
		AutoPlanHeld: domain.LadoLogAutoPlanHeldCap,
		Instances: []domain.LadoLogInstanceCount{
			{Instance: "prod1/grp-api-1", Image: "grp/api:1.2", Count: 3, LastSeenAt: ladoLogTime},
			{Instance: "prod2/grp-api-1", Image: "grp/api:1.1", Count: 1, LastSeenAt: ladoLogTime},
		},
		ServiceInstances: 3,
	}}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/log-watch/incidents", "")
	list := decodeLadoJSON[controllers.LadoLogIncidentListResponse](t, body)
	if status != http.StatusOK || len(list.Fingerprints) != 1 {
		t.Fatalf("incidents = %d %s", status, body)
	}
	if fp := list.Fingerprints[0]; fp.ID != 7 || fp.Count != 4 || fp.TaskID != "lado-task-1" || fp.AutoPlanHeld != "cap" || fp.AutoPlanRequestedAt != nil ||
		fp.PreviousIssueRef != "" || len(fp.Instances) != 2 || fp.Instances[1].Image != "grp/api:1.1" || fp.ServiceInstances != 3 {
		t.Fatalf("fingerprint = %+v", fp)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/fingerprints/7/ignore", `{"ignored":true}`)
	if fp := decodeLadoJSON[controllers.LadoLogFingerprintResponse](t, body); status != http.StatusOK || !fp.Ignored || fp.IgnoredAt == nil ||
		svc.gotIgnore.id != 7 || !svc.gotIgnore.ignored {
		t.Fatalf("ignore = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/fingerprints/7/ignore", `{"ignored":false}`)
	if status != http.StatusOK || svc.gotIgnore.ignored {
		t.Fatalf("unignore = %d %s", status, body)
	}
	for _, bad := range []string{"0", "x", "-1"} {
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/fingerprints/"+bad+"/ignore", `{"ignored":true}`)
		wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_LOG_FINGERPRINT_NOT_FOUND")
	}
	svc.err = domain.ErrLadoLogFingerprintNotFound
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/lado/projects/mer/log-watch/fingerprints/8/ignore", `{"ignored":true}`)
	wantLadoErrorCode(t, body, status, http.StatusNotFound, "LADO_LOG_FINGERPRINT_NOT_FOUND")
}

// TestLadoAPI_LogWatchUnknownProjectIsNotFound: with the daemon's service and
// store, every log watch route answers 404 PROJECT_NOT_FOUND for a project
// AO does not have, as the API spec says, and a PUT leaves no settings
// behind; the project that exists reads and saves them.
func TestLadoAPI_LogWatchUnknownProjectIsNotFound(t *testing.T) {
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: ladoLogTime}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := ladologwatch.New(ladologwatch.Deps{Store: store, Projects: store, Certs: ladologwatch.NewCertStore(t.TempDir())})
	srv := newLadoTestServer(t, httpd.APIDeps{LadoLogWatch: svc})
	put := ladoLogWatchBody(`[{"displayName":"prod1","endpoint":"tcp://192.0.2.10:2375","targets":[{"kind":"compose_project","value":"grp"}]}]`)

	for _, r := range []struct{ method, path, body string }{
		{"GET", "/log-watch", ""},
		{"PUT", "/log-watch", put},
		{"POST", "/log-watch/sources/s1/tls", `{"caPath":"/nowhere/ca.pem"}`},
		{"DELETE", "/log-watch/sources/s1/tls", ""},
		{"POST", "/log-watch/check", `{"sourceId":"s1"}`},
		{"POST", "/log-watch/import-ci", ""},
		{"GET", "/log-watch/incidents", ""},
		{"POST", "/log-watch/fingerprints/1/ignore", `{"ignored":true}`},
	} {
		body, status, _ := doRequest(t, srv, r.method, "/api/v1/lado/projects/mer-typo"+r.path, r.body)
		t.Run(r.method+" "+r.path, func(t *testing.T) { wantLadoErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND") })
	}
	if _, ok, err := store.LadoLogWatchSettings(ctx, "mer-typo"); err != nil || ok {
		t.Fatalf("settings of the unknown project = %v, %v; want none saved", ok, err)
	}

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/lado/projects/mer/log-watch", put)
	if status != http.StatusOK {
		t.Fatalf("PUT of the project = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/mer/log-watch", "")
	if view := decodeLadoJSON[controllers.LadoLogWatchResponse](t, body); status != http.StatusOK || !view.Enabled || len(view.Sources) != 1 {
		t.Fatalf("GET of the project = %d %s", status, body)
	}
}

// Without a service every log watch route answers 501, and a task the log
// watch created says so in source while others leave it out.
func TestLadoAPI_LogWatchNotImplementedAndTaskSource(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/lado/projects/mer/log-watch", ""},
		{"PUT", "/api/v1/lado/projects/mer/log-watch", ladoLogWatchBody(`[]`)},
		{"POST", "/api/v1/lado/projects/mer/log-watch/sources/s1/tls", `{"caPath":"/x"}`},
		{"DELETE", "/api/v1/lado/projects/mer/log-watch/sources/s1/tls", ""},
		{"POST", "/api/v1/lado/projects/mer/log-watch/check", `{"sourceId":"s1"}`},
		{"POST", "/api/v1/lado/projects/mer/log-watch/import-ci", ""},
		{"GET", "/api/v1/lado/projects/mer/log-watch/incidents", ""},
		{"POST", "/api/v1/lado/projects/mer/log-watch/fingerprints/1/ignore", `{"ignored":true}`},
	} {
		if _, status, _ := doRequest(t, srv, route.method, route.path, route.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d, want 501", route.method, route.path, status)
		}
	}

	task := sampleLadoTask()
	task.Source = domain.LadoTaskSourceLog
	svc := &fakeLadoTaskService{task: task}
	srv = newLadoTestServer(t, httpd.APIDeps{LadoTasks: svc})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	if got := decodeLadoJSON[controllers.LadoTaskListResponse](t, body); status != http.StatusOK || len(got.Tasks) != 1 || got.Tasks[0].Source != "log" {
		t.Fatalf("log task = %d %s", status, body)
	}
	svc.task = sampleLadoTask()
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/tasks?projectId=mer", "")
	var raw struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Tasks) != 1 {
		t.Fatalf("decode %s: %v", body, err)
	}
	if _, has := raw.Tasks[0]["source"]; has {
		t.Fatalf("a developer's task carries source: %s", body)
	}
}
