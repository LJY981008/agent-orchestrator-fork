package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// fakeLadoDBChangeService keeps the settings and secret values in memory and,
// like the real service, checks presence before it changes anything.
type fakeLadoDBChangeService struct {
	settings  []domain.LadoDBChangeSettings
	secrets   map[string]string
	summaries []domain.LadoDBChangeSummary
	execution domain.LadoDBChangeExecutionView
	content   domain.LadoDBChangeFileContent
	run       domain.LadoDBChangeRun
	err       error
	calls     int

	gotProject domain.ProjectID
	gotUpdate  domain.LadoDBChangeSettingsUpdate
	gotSession domain.SessionID
	gotPRURL   string
	gotBlob    string
	gotApply   domain.LadoDBChangeApplyRequest
	gotProof   domain.LadoPresenceProof
}

func (f *fakeLadoDBChangeService) view() domain.LadoDBChangeSettingsView {
	stored := slices.Sorted(maps.Keys(f.secrets))
	view := domain.LadoDBChangeSettingsView{SHA256: domain.LadoDBChangeSettingsListSHA256(f.settings)}
	for _, s := range f.settings {
		name := s.Repo
		if name == "" {
			name = "demo"
		}
		view.Repositories = append(view.Repositories, domain.LadoDBChangeSettingsRepo{LadoDBChangeSettings: s, Name: name, StoredSecretNames: stored})
	}
	return view
}

func (f *fakeLadoDBChangeService) DBChangeSettings(_ context.Context, project domain.ProjectID) (domain.LadoDBChangeSettingsView, error) {
	f.calls++
	f.gotProject = project
	if f.err != nil {
		return domain.LadoDBChangeSettingsView{}, f.err
	}
	return f.view(), nil
}

func (f *fakeLadoDBChangeService) UpdateDBChangeSettings(_ context.Context, project domain.ProjectID, in domain.LadoDBChangeSettingsUpdate, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeSettingsView, error) {
	f.calls++
	f.gotProject, f.gotUpdate = project, in
	proof, err := presence(domain.LadoPresenceDBChanges, domain.LadoDBChangeSettingsSubject(project, in.Repo))
	if err != nil {
		return domain.LadoDBChangeSettingsView{}, err
	}
	f.gotProof = proof
	if f.err != nil {
		return domain.LadoDBChangeSettingsView{}, f.err
	}
	if f.secrets == nil {
		f.secrets = map[string]string{}
	}
	maps.Copy(f.secrets, in.Secrets.Set)
	for _, name := range in.Secrets.Remove {
		delete(f.secrets, name)
	}
	s := in.Settings(project)
	s.BaselineCommit, s.EnabledAt, s.UpdatedAt = strings.Repeat("b", 40), ladoTaskTime, ladoTaskTime
	f.settings = []domain.LadoDBChangeSettings{s}
	return f.view(), nil
}

func (f *fakeLadoDBChangeService) ProjectDBChanges(_ context.Context, project domain.ProjectID) ([]domain.LadoDBChangeSummary, error) {
	f.calls++
	f.gotProject = project
	return f.summaries, f.err
}

func (f *fakeLadoDBChangeService) ExecutionDBChanges(_ context.Context, session domain.SessionID) (domain.LadoDBChangeExecutionView, error) {
	f.calls++
	f.gotSession = session
	return f.execution, f.err
}

func (f *fakeLadoDBChangeService) DBChangeFile(_ context.Context, session domain.SessionID, prURL, blobSHA string) (domain.LadoDBChangeFileContent, error) {
	f.calls++
	f.gotSession, f.gotPRURL, f.gotBlob = session, prURL, blobSHA
	return f.content, f.err
}

func (f *fakeLadoDBChangeService) ApplyDBChanges(_ context.Context, req domain.LadoDBChangeApplyRequest, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoDBChangeRun, error) {
	f.calls++
	f.gotApply = req
	proof, err := presence(domain.LadoPresenceDBChangeApply, domain.LadoDBChangeApplySubject(req.SessionID, req.HeadSHA, req.Phase))
	if err != nil {
		return domain.LadoDBChangeRun{}, err
	}
	f.gotProof = proof
	return f.run, f.err
}

var _ controllers.LadoDBChangeService = (*fakeLadoDBChangeService)(nil)

const (
	ladoDBSettingsPath = "/api/v1/lado/projects/proj-1/db-changes/settings"
	ladoDBSecretValue  = "postgres://deploy:s3cr3t-pa55@db.internal:5432/app"
	ladoDBPRURL        = "https://github.com/acme/demo/pull/7"
)

var (
	ladoDBBlob  = strings.Repeat("a", 40)
	ladoDBBlob2 = strings.Repeat("c", 40)
)

func TestLadoAPI_DBChangeSettingsKeepSecretValuesIn(t *testing.T) {
	svc := &fakeLadoDBChangeService{settings: []domain.LadoDBChangeSettings{{ProjectID: "proj-1"}}}
	presence := &recordingPresence{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc, LadoPresence: presence})

	// A repository that never saved settings has no base hash.
	body, status, _ := doRequest(t, srv, "GET", ladoDBSettingsPath, "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}
	got := decodeLadoJSON[controllers.LadoDBChangeSettingsResponse](t, body)
	if got.ProjectID != "proj-1" || len(got.Repositories) != 1 || got.Repositories[0].SHA256 != "" ||
		got.Repositories[0].UpdatedAt != nil || got.Repositories[0].Paths == nil || got.Repositories[0].StoredSecretNames == nil {
		t.Fatalf("unsaved settings = %+v", got)
	}

	sent := `{"repo":"","enabled":true,"paths":["scripts/migrations/*.sql"],"command":"psql \"$LADO_DB_DSN\" -v ON_ERROR_STOP=1 -f {file}",` +
		`"timeoutMinutes":15,"secretNames":["LADO_DB_DSN"],"secrets":{"set":{"LADO_DB_DSN":"` + ladoDBSecretValue + `"},"remove":["OLD_DSN"]},"base":""}`
	header := map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"}
	body, status = ladoHeaderRequest(t, srv, "PUT", ladoDBSettingsPath, sent, header)
	if status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	if presence.method != "PUT" || presence.path != ladoDBSettingsPath || string(presence.body) != sent ||
		presence.action != domain.LadoPresenceDBChanges || presence.subject != "proj-1:" {
		t.Fatalf("presence saw %s %s %q for %s %s", presence.method, presence.path, presence.body, presence.action, presence.subject)
	}
	in := svc.gotUpdate
	want := domain.LadoDBChangeSettingsUpdate{
		Enabled: true, Paths: []string{"scripts/migrations/*.sql"}, Command: `psql "$LADO_DB_DSN" -v ON_ERROR_STOP=1 -f {file}`,
		TimeoutMinutes: 15, SecretNames: []string{"LADO_DB_DSN"},
		Secrets: domain.LadoDBChangeSecretsUpdate{Set: map[string]string{"LADO_DB_DSN": ladoDBSecretValue}, Remove: []string{"OLD_DSN"}},
	}
	if svc.gotProject != "proj-1" || !reflect.DeepEqual(in, want) || svc.gotProof.Nonce != "BBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("service got %+v for %s with proof %+v", in, svc.gotProject, svc.gotProof)
	}
	// The value reached the service but never comes back.
	if strings.Contains(string(body), "s3cr3t") || strings.Contains(string(body), ladoDBSecretValue) {
		t.Fatalf("the response leaks the secret: %s", body)
	}
	got = decodeLadoJSON[controllers.LadoDBChangeSettingsResponse](t, body)
	saved := svc.settings[0]
	wantRepo := controllers.LadoDBChangeRepoSettingsResponse{
		Repo: "", Name: "demo", Enabled: true, Paths: []string{"scripts/migrations/*.sql"}, Command: saved.Command,
		TimeoutMinutes: 15, SecretNames: []string{"LADO_DB_DSN"}, StoredSecretNames: []string{"LADO_DB_DSN"},
		BaselineCommit: strings.Repeat("b", 40), EnabledAt: &ladoTaskTime, UpdatedAt: &ladoTaskTime, SHA256: saved.SHA256(),
	}
	if len(got.Repositories) != 1 || !reflect.DeepEqual(got.Repositories[0], wantRepo) ||
		got.SHA256 != domain.LadoDBChangeSettingsListSHA256(svc.settings) {
		t.Fatalf("saved settings = %+v\nwant %+v", got, wantRepo)
	}

	// The GET after it lists the name only, too.
	body, status, _ = doRequest(t, srv, "GET", ladoDBSettingsPath, "")
	if status != http.StatusOK || strings.Contains(string(body), "s3cr3t") || !strings.Contains(string(body), `"storedSecretNames":["LADO_DB_DSN"]`) {
		t.Fatalf("GET after save = %d %s", status, body)
	}

	// A workspace repository signs its own subject.
	if body, status := ladoHeaderRequest(t, srv, "PUT", ladoDBSettingsPath,
		`{"repo":"back","enabled":false,"paths":[],"command":"","secretNames":[]}`, header); status != http.StatusOK || presence.subject != "proj-1:back" {
		t.Fatalf("workspace PUT = %d %s, subject %q", status, body, presence.subject)
	}
}

func TestLadoAPI_DBChangeSettingsNeedPresence(t *testing.T) {
	sent := `{"repo":"","enabled":true,"paths":["db/*.sql"],"command":"psql -f {file}","secretNames":["LADO_DB_DSN"],"secrets":{"set":{"LADO_DB_DSN":"` + ladoDBSecretValue + `"}}}`

	// Without the desktop app, nothing is saved.
	svc := &fakeLadoDBChangeService{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc})
	body, status := ladoHeaderRequest(t, srv, "PUT", ladoDBSettingsPath, sent, nil)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_UNAVAILABLE")
	if len(svc.settings) != 0 || len(svc.secrets) != 0 {
		t.Fatalf("a refused change saved %+v %v", svc.settings, svc.secrets)
	}

	// A proof made over other bytes is refused.
	svc = &fakeLadoDBChangeService{}
	srv = newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc, LadoPresence: presenceVerifier()})
	body, status = signedRequest(t, srv, "PUT", ladoDBSettingsPath, strings.Replace(sent, "db/*.sql", "other/*.sql", 1), sent)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_INVALID")
	if len(svc.settings) != 0 || strings.Contains(string(body), "s3cr3t") {
		t.Fatalf("a forged change saved %+v or leaked %s", svc.settings, body)
	}

	// The app's proof over these bytes saves them.
	body, status = signedRequest(t, srv, "PUT", ladoDBSettingsPath, sent, sent)
	if status != http.StatusOK || len(svc.settings) != 1 || svc.secrets["LADO_DB_DSN"] != ladoDBSecretValue {
		t.Fatalf("signed PUT = %d %s", status, body)
	}
}

func TestLadoAPI_DBChangeErrors(t *testing.T) {
	invalid := &domain.LadoDBChangeInvalidError{Problems: []domain.LadoDBChangeProblem{
		{Field: "secretNames[0]", Reason: domain.LadoDBChangeProblemReservedSecret},
		{Field: "command", Reason: domain.LadoDBChangeProblemNoPlaceholder},
	}}
	pending := &domain.LadoDBChangePendingError{PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead, Files: []domain.LadoDBChangeFileRef{
		{Path: "db/001_add.sql", BlobSHA: ladoDBBlob, Phase: domain.LadoDBChangePreMerge},
	}}
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"invalid":           {fmt.Errorf("save: %w", invalid), http.StatusBadRequest, "LADO_DB_CHANGE_INVALID"},
		"settings changed":  {fmt.Errorf("save: %w", domain.ErrLadoDBChangeSettingsChanged), http.StatusConflict, "LADO_DB_CHANGE_SETTINGS_CHANGED"},
		"baseline failed":   {fmt.Errorf("fetch: %w", domain.ErrLadoDBChangeBaselineFailed), http.StatusConflict, "LADO_DB_CHANGE_BASELINE_FAILED"},
		"not found":         {domain.ErrLadoDBChangeNotFound, http.StatusNotFound, "LADO_DB_CHANGE_NOT_FOUND"},
		"run state":         {domain.ErrLadoDBChangeRunState, http.StatusConflict, "LADO_DB_CHANGE_RUN_STATE"},
		"pending":           {pending, http.StatusConflict, "LADO_DB_CHANGE_PENDING"},
		"presence replayed": {domain.ErrLadoPresenceReplayed, http.StatusForbidden, "LADO_PRESENCE_REPLAYED"},
		"service apierr":    {apierr.Conflict("LADO_DB_CHANGE_SETTINGS_CHANGED", "changed", nil), http.StatusConflict, "LADO_DB_CHANGE_SETTINGS_CHANGED"},
		"other":             {errors.New("disk: " + ladoDBSecretValue), http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeLadoDBChangeService{err: tc.err}
			srv := newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc, LadoPresence: &recordingPresence{}})
			header := map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"}
			body, status := ladoHeaderRequest(t, srv, "PUT", ladoDBSettingsPath, `{"repo":"","enabled":true,"paths":["db/*.sql"],"command":"x","secretNames":["PATH"]}`, header)
			wantLadoErrorCode(t, body, status, tc.status, tc.code)
			if strings.Contains(string(body), "s3cr3t") {
				t.Fatalf("the error leaks the secret: %s", body)
			}
			apiErr := decodeLadoJSON[envelope.APIError](t, body)
			switch name {
			case "invalid":
				if fmt.Sprint(apiErr.Details["problems"]) != "[map[field:secretNames[0] reason:reserved] map[field:command reason:no_file_placeholder]]" {
					t.Fatalf("problems = %v", apiErr.Details)
				}
			case "pending":
				if apiErr.Details["prUrl"] != ladoDBPRURL || apiErr.Details["headSha"] != ladoVerifyHead ||
					fmt.Sprint(apiErr.Details["files"]) != "[map[blobSha:"+ladoDBBlob+" path:db/001_add.sql phase:pre_merge]]" {
					t.Fatalf("pending details = %v", apiErr.Details)
				}
			}
			// The same errors answer the read routes.
			body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/executions/mer-1/db-changes", "")
			wantLadoErrorCode(t, body, status, tc.status, tc.code)
		})
	}
}

func TestLadoAPI_DBChangeViews(t *testing.T) {
	applied := ladoTaskTime.Add(time.Minute)
	run := domain.LadoDBChangeRun{
		ID: "lado-dbrun-1", ProjectID: "proj-1", Repo: "back", ExecutionSessionID: "mer-1", TaskID: "lado-task-1",
		PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead, Kind: domain.LadoDBChangeRunPreMerge, Trigger: domain.LadoDBChangeTriggerApproval,
		State: domain.LadoDBChangeRunFailed, ApprovalID: "lado-integration-1", Attempts: 1, Error: "002_drop.sql: exit 1",
		CreatedAt: ladoTaskTime, UpdatedAt: applied,
	}
	svc := &fakeLadoDBChangeService{
		summaries: []domain.LadoDBChangeSummary{{
			TaskID: "lado-task-1", SessionID: "mer-1", PRURL: ladoDBPRURL, Repo: "back",
			State: domain.LadoDBChangeBoardApplyFailed, Pending: 1, Failed: 1, Message: "002_drop.sql: exit 1",
		}},
		execution: domain.LadoDBChangeExecutionView{SessionID: "mer-1", TaskID: "lado-task-1", PRs: []domain.LadoDBChangePRView{{
			PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead, Repo: "back",
			Files: []domain.LadoDBChangeFileView{
				{LadoDBChangeFileRef: domain.LadoDBChangeFileRef{Path: "db/001_add.sql", BlobSHA: ladoDBBlob, Phase: domain.LadoDBChangePreMerge},
					Status: domain.LadoDBChangeFileApplied, AppliedAt: &applied, Attempts: 1, OutputSHA256: strings.Repeat("0", 64)},
				{LadoDBChangeFileRef: domain.LadoDBChangeFileRef{Path: "db/002_drop.sql", BlobSHA: ladoDBBlob2, Phase: domain.LadoDBChangePreMerge},
					Status: domain.LadoDBChangeFileFailed, Attempts: 1, LastMessage: "exit 1: relation missing"},
			},
			Runs: []domain.LadoDBChangeRun{run},
		}}},
		content: domain.LadoDBChangeFileContent{Path: "db/001_add.sql", BlobSHA: ladoDBBlob, Content: "ALTER TABLE t ADD c int;\n", Truncated: false},
	}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/projects/proj-1/db-changes", "")
	if status != http.StatusOK || svc.gotProject != "proj-1" {
		t.Fatalf("summaries = %d %s", status, body)
	}
	summaries := decodeLadoJSON[controllers.LadoDBChangeSummaryListResponse](t, body)
	if !reflect.DeepEqual(summaries.Summaries, []controllers.LadoDBChangeSummaryResponse{{
		TaskID: "lado-task-1", SessionID: "mer-1", PRURL: ladoDBPRURL, Repo: "back", State: "apply_failed", Pending: 1, Failed: 1, Message: "002_drop.sql: exit 1",
	}}) {
		t.Fatalf("summaries = %+v", summaries)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/lado/executions/mer-1/db-changes", "")
	if status != http.StatusOK || svc.gotSession != "mer-1" {
		t.Fatalf("execution = %d %s", status, body)
	}
	execution := decodeLadoJSON[controllers.LadoDBChangeExecutionResponse](t, body)
	wantPR := controllers.LadoDBChangePRResponse{
		PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead, Repo: "back",
		Files: []controllers.LadoDBChangeFileResponse{
			{Path: "db/001_add.sql", BlobSHA: ladoDBBlob, Phase: "pre_merge", Status: "applied", AppliedAt: &applied, Attempts: 1, OutputSHA256: strings.Repeat("0", 64)},
			{Path: "db/002_drop.sql", BlobSHA: ladoDBBlob2, Phase: "pre_merge", Status: "failed", Attempts: 1, LastMessage: "exit 1: relation missing"},
		},
		Modified: []controllers.LadoDBChangeModifiedResponse{},
		Runs: []controllers.LadoDBChangeRunResponse{{
			ID: "lado-dbrun-1", Repo: "back", SessionID: "mer-1", TaskID: "lado-task-1", PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead,
			Kind: "pre_merge", Trigger: "approval", State: "failed", ApprovalID: "lado-integration-1", Attempts: 1,
			Error: "002_drop.sql: exit 1", CreatedAt: ladoTaskTime, UpdatedAt: applied,
		}},
	}
	if execution.SessionID != "mer-1" || execution.TaskID != "lado-task-1" || len(execution.PRs) != 1 || !reflect.DeepEqual(execution.PRs[0], wantPR) {
		t.Fatalf("execution = %+v\nwant PR %+v", execution, wantPR)
	}

	filePath := "/api/v1/lado/executions/mer-1/db-changes/file?prUrl=" + ladoDBPRURL + "&blobSha=" + ladoDBBlob
	body, status, _ = doRequest(t, srv, "GET", filePath, "")
	if status != http.StatusOK || svc.gotPRURL != ladoDBPRURL || svc.gotBlob != ladoDBBlob {
		t.Fatalf("file = %d %s, service got %q %q", status, body, svc.gotPRURL, svc.gotBlob)
	}
	if got := decodeLadoJSON[controllers.LadoDBChangeFileContentResponse](t, body); got != (controllers.LadoDBChangeFileContentResponse{
		Path: "db/001_add.sql", BlobSHA: ladoDBBlob, Content: "ALTER TABLE t ADD c int;\n",
	}) {
		t.Fatalf("file = %+v", got)
	}

	// A file request that names no PR or no blob never reaches the service.
	before := svc.calls
	for _, query := range []string{"blobSha=" + ladoDBBlob, "prUrl=x", "prUrl=x&blobSha=HEAD", "prUrl=x&blobSha=" + strings.Repeat("A", 40)} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/lado/executions/mer-1/db-changes/file?"+query, "")
		wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_REQUEST")
	}
	if svc.calls != before {
		t.Fatalf("the service was called for a bad file request")
	}

	// Empty lists are [] for the app, not null.
	svc.summaries, svc.execution = nil, domain.LadoDBChangeExecutionView{SessionID: "mer-1"}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/projects/proj-1/db-changes", "")
	if !strings.Contains(string(body), `"summaries":[]`) {
		t.Fatalf("empty summaries = %s", body)
	}
	body, _, _ = doRequest(t, srv, "GET", "/api/v1/lado/executions/mer-1/db-changes", "")
	if !strings.Contains(string(body), `"prs":[]`) {
		t.Fatalf("empty execution = %s", body)
	}
}

func TestLadoAPI_ApplyDBChanges(t *testing.T) {
	path := "/api/v1/lado/executions/mer-1/db-changes/apply"
	run := domain.LadoDBChangeRun{
		ID: "lado-dbrun-2", ExecutionSessionID: "mer-1", PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead,
		Kind: domain.LadoDBChangeRunAfterDeploy, Trigger: domain.LadoDBChangeTriggerAfterMerge, State: domain.LadoDBChangeRunQueued,
		CreatedAt: ladoTaskTime, UpdatedAt: ladoTaskTime,
	}
	svc := &fakeLadoDBChangeService{run: run}
	presence := &recordingPresence{}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc, LadoPresence: presence})
	header := map[string]string{"X-LADO-Presence": "v1.proof-from-the-app"}

	sent := `{"prUrl":"` + ladoDBPRURL + `","headSha":"` + ladoVerifyHead + `","phase":"after_deploy","files":[{"path":"db/003_drop.sql","blobSha":"` + ladoDBBlob + `","phase":"after_deploy"}]}`
	body, status := ladoHeaderRequest(t, srv, "POST", path, sent, header)
	if status != http.StatusOK {
		t.Fatalf("apply = %d %s", status, body)
	}
	if presence.action != domain.LadoPresenceDBChangeApply || presence.subject != "mer-1@"+ladoVerifyHead+":after_deploy" ||
		presence.method != "POST" || presence.path != path || string(presence.body) != sent {
		t.Fatalf("presence saw %s %s %q for %s %s", presence.method, presence.path, presence.body, presence.action, presence.subject)
	}
	want := domain.LadoDBChangeApplyRequest{
		SessionID: "mer-1", PRURL: ladoDBPRURL, HeadSHA: ladoVerifyHead, Phase: domain.LadoDBChangeAfterDeploy,
		Files: []domain.LadoDBChangeFileRef{{Path: "db/003_drop.sql", BlobSHA: ladoDBBlob, Phase: domain.LadoDBChangeAfterDeploy}},
	}
	if !reflect.DeepEqual(svc.gotApply, want) || svc.gotProof.Nonce != "BBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("service got %+v with proof %+v", svc.gotApply, svc.gotProof)
	}
	if got := decodeLadoJSON[controllers.LadoDBChangeRunResponse](t, body); got.ID != "lado-dbrun-2" || got.Kind != "after_deploy" ||
		got.Trigger != "after_merge" || got.State != "queued" || got.SessionID != "mer-1" {
		t.Fatalf("run = %+v", got)
	}

	// Bad requests never reach the service.
	before := svc.calls
	for name, bad := range map[string]string{
		"no PR":         `{"prUrl":"","headSha":"` + ladoVerifyHead + `","phase":"pre_merge","files":[{"path":"a.sql","blobSha":"` + ladoDBBlob + `","phase":"pre_merge"}]}`,
		"short head":    `{"prUrl":"x","headSha":"abc","phase":"pre_merge","files":[{"path":"a.sql","blobSha":"` + ladoDBBlob + `","phase":"pre_merge"}]}`,
		"bad phase":     `{"prUrl":"x","headSha":"` + ladoVerifyHead + `","phase":"later","files":[{"path":"a.sql","blobSha":"` + ladoDBBlob + `","phase":"later"}]}`,
		"no files":      `{"prUrl":"x","headSha":"` + ladoVerifyHead + `","phase":"pre_merge","files":[]}`,
		"file no path":  `{"prUrl":"x","headSha":"` + ladoVerifyHead + `","phase":"pre_merge","files":[{"path":"","blobSha":"` + ladoDBBlob + `","phase":"pre_merge"}]}`,
		"file bad blob": `{"prUrl":"x","headSha":"` + ladoVerifyHead + `","phase":"pre_merge","files":[{"path":"a.sql","blobSha":"zz","phase":"pre_merge"}]}`,
		"mixed phase":   `{"prUrl":"x","headSha":"` + ladoVerifyHead + `","phase":"pre_merge","files":[{"path":"a.sql","blobSha":"` + ladoDBBlob + `","phase":"after_deploy"}]}`,
	} {
		body, status := ladoHeaderRequest(t, srv, "POST", path, bad, header)
		if status != http.StatusBadRequest || !strings.Contains(string(body), `"code":"INVALID_REQUEST"`) {
			t.Fatalf("%s = %d %s", name, status, body)
		}
	}
	body, status = ladoHeaderRequest(t, srv, "POST", path, `{"prUrl":"x","merge":true}`, header)
	wantLadoErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.calls != before {
		t.Fatalf("the service was called for a bad request")
	}

	// Without the desktop app, nothing applies.
	svc = &fakeLadoDBChangeService{run: run}
	srv = newLadoTestServer(t, httpd.APIDeps{LadoDBChanges: svc})
	body, status = ladoHeaderRequest(t, srv, "POST", path, sent, nil)
	wantLadoErrorCode(t, body, status, http.StatusForbidden, "LADO_PRESENCE_UNAVAILABLE")
	if svc.gotProof.Nonce != "" {
		t.Fatalf("applied without presence")
	}
}

func TestLadoAPI_DBChangesNotImplemented(t *testing.T) {
	srv := newLadoTestServer(t, httpd.APIDeps{})
	for _, req := range []struct{ method, path, body string }{
		{"GET", ladoDBSettingsPath, ""},
		{"PUT", ladoDBSettingsPath, `{}`},
		{"GET", "/api/v1/lado/projects/proj-1/db-changes", ""},
		{"GET", "/api/v1/lado/executions/mer-1/db-changes", ""},
		{"GET", "/api/v1/lado/executions/mer-1/db-changes/file?prUrl=x&blobSha=" + ladoDBBlob, ""},
		{"POST", "/api/v1/lado/executions/mer-1/db-changes/apply", `{}`},
	} {
		if body, status, _ := doRequest(t, srv, req.method, req.path, req.body); status != http.StatusNotImplemented {
			t.Fatalf("%s %s = %d %s", req.method, req.path, status, body)
		}
	}
}

// The verification response carries the db_changes item after evidence with
// its files, the applied files the head changes and whether the plan names
// db_schema.
func TestLadoAPI_VerificationDBChangesDetails(t *testing.T) {
	record := ladoVerifyRecord(t, "mer-1", ladoDBPRURL, ladoVerifyHead, presenceNow)
	record.Items = append(record.Items, mustLadoItem(t, domain.LadoVerifyDBChanges, domain.LadoVerifyConfirm, domain.LadoCodeDBChangeAppliedModified,
		domain.LadoDBChangesDetails{
			Repo: "back",
			Files: []domain.LadoDBChangeFileRef{
				{Path: "db/003_add.sql", BlobSHA: ladoDBBlob, Phase: domain.LadoDBChangePreMerge},
				{Path: "db/004_drop.sql", BlobSHA: ladoDBBlob2, Phase: domain.LadoDBChangeAfterDeploy},
			},
			Modified: []domain.LadoDBChangeModified{{Path: "db/001_init.sql", AppliedBlobSHA: strings.Repeat("1", 40), Deleted: true}},
			Planned:  true,
		}))
	sum := ladoRecordSHA(t, record)
	svc := &fakeLadoVerifyService{result: ladoverify.ApproveResult{Verification: record, SHA256: sum, Confirm: record.ConfirmCodes()}}
	srv := newLadoTestServer(t, httpd.APIDeps{LadoVerify: svc})

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/lado/executions/mer-1/integration-approve", ladoApproveBody("", true))
	if status != http.StatusOK {
		t.Fatalf("dry run = %d %s", status, body)
	}
	resp := decodeLadoJSON[controllers.LadoIntegrationApproveResponse](t, body)
	items := resp.Verification.Items
	if len(items) != 9 {
		t.Fatalf("items = %d, want the eight and db_changes", len(items))
	}
	got := items[8]
	if got.Kind != "db_changes" || got.Outcome != "confirm" || got.Code != "db_change_applied_modified" {
		t.Fatalf("db_changes item = %+v", got)
	}
	wantDetails := controllers.LadoVerifyItemDetailsResponse{
		Repo: "back",
		Files: []controllers.LadoDBChangeFileRefBody{
			{Path: "db/003_add.sql", BlobSHA: ladoDBBlob, Phase: "pre_merge"},
			{Path: "db/004_drop.sql", BlobSHA: ladoDBBlob2, Phase: "after_deploy"},
		},
		Modified: []controllers.LadoDBChangeModifiedResponse{{Path: "db/001_init.sql", AppliedBlobSHA: strings.Repeat("1", 40), Deleted: true}},
		Planned:  true,
	}
	if !reflect.DeepEqual(got.Details, wantDetails) {
		t.Fatalf("details = %+v\nwant %+v", got.Details, wantDetails)
	}
	if !slices.Contains(resp.Confirm, "db_change_applied_modified") {
		t.Fatalf("confirm = %v, want the db_changes code", resp.Confirm)
	}
	// The eight items keep their details: protection's repo is its own.
	if items[5].Kind != "protection" || items[5].Details.Repo != "acme/demo" || items[5].Details.Files != nil {
		t.Fatalf("protection item = %+v", items[5])
	}
}
