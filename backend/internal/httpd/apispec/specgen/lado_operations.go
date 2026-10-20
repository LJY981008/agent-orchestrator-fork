package specgen

// LADO fork: LADO's API operations and schema names (docs/LJY-PRD/TDD.md
// §1.1 and §13). build.go only appends ladoOperations(); the schema names join
// schemaNames here so LADO stays out of that upstream map.

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func init() {
	for defaultName, clean := range ladoSchemaNames {
		schemaNames[defaultName] = clean
	}
}

var ladoSchemaNames = map[string]string{ //nolint:gosec // G101: schema names, not credentials.
	"ControllersLadoProjectIDParam":               "LadoProjectIDParam",
	"ControllersLadoProjectSettingsResponse":      "LadoProjectSettingsResponse",
	"ControllersUpdateLadoProjectSettingsRequest": "UpdateLadoProjectSettingsRequest",
	"ControllersCheckLadoConnectionRequest":       "CheckLadoConnectionRequest",
	"ControllersCreateLadoConnectionRequest":      "CreateLadoConnectionRequest",
	"ControllersLadoPermissionResponse":           "LadoPermissionResponse",
	"ControllersLadoTokenCheckResponse":           "LadoTokenCheckResponse",
	"ControllersLadoSSHCheckResponse":             "LadoSSHCheckResponse",
	"ControllersLadoLinkedProjectResponse":        "LadoLinkedProjectResponse",
	"ControllersLadoConnectionCheckResponse":      "LadoConnectionCheckResponse",
	"ControllersLadoConnectionResponse":           "LadoConnectionResponse",
	"ControllersLadoCredentialHostParam":          "LadoCredentialHostParam",
	"ControllersStoreLadoCredentialRequest":       "StoreLadoCredentialRequest",
	"ControllersLadoCredentialResponse":           "LadoCredentialResponse",
	"ControllersLadoCredentialListResponse":       "LadoCredentialListResponse",
	"ControllersLadoTaskIDParam":                  "LadoTaskIDParam",
	"ControllersListLadoTasksQuery":               "ListLadoTasksQuery",
	"ControllersLadoTaskIssueResponse":            "LadoTaskIssueResponse",
	"ControllersLadoTaskResponse":                 "LadoTaskResponse",
	"ControllersLadoTaskListResponse":             "LadoTaskListResponse",
	"ControllersCreateLadoTaskRequest":            "CreateLadoTaskRequest",
	"ControllersUpdateLadoTaskRequest":            "UpdateLadoTaskRequest",
	"ControllersImportLadoTasksRequest":           "ImportLadoTasksRequest",
	"ControllersImportLadoTasksResponse":          "ImportLadoTasksResponse",
	"ControllersLadoIssueResponse":                "LadoIssueResponse",
	"ControllersLadoProjectIssuesResponse":        "LadoProjectIssuesResponse",
	"ControllersLadoRepositoryResponse":           "LadoRepositoryResponse",
	"ControllersLadoRepositoryFailureResponse":    "LadoRepositoryFailureResponse",
	"ControllersLadoProjectRepositoriesResponse":  "LadoProjectRepositoriesResponse",
}

func ladoOperations() []operation {
	projectID := []any{controllers.LadoProjectIDParam{}}
	taskID := []any{controllers.LadoTaskIDParam{}}
	credentialHost := []any{controllers.LadoCredentialHostParam{}}
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/lado/projects/{projectId}/settings", id: "getLadoProjectSettings", tag: "lado",
			summary:    "Get a project's LADO settings",
			pathParams: projectID,
			resps: []respUnit{
				{http.StatusOK, controllers.LadoProjectSettingsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/lado/projects/{projectId}/settings", id: "updateLadoProjectSettings", tag: "lado",
			summary:    "Change a project's LADO settings",
			pathParams: projectID,
			reqBody:    controllers.UpdateLadoProjectSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.LadoProjectSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/projects/{projectId}/issues", id: "listLadoProjectIssues", tag: "lado",
			summary:    "List the open issues without a task in each of a project's repositories",
			pathParams: projectID,
			resps:      ladoResps(http.StatusOK, controllers.LadoProjectIssuesResponse{}, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/projects/{projectId}/repositories", id: "listLadoProjectRepositories", tag: "lado",
			summary:    "List the repositories a project's issues can live in",
			pathParams: projectID,
			resps:      ladoResps(http.StatusOK, controllers.LadoProjectRepositoriesResponse{}, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/connections/check", id: "checkLadoConnection", tag: "lado",
			summary: "Check a repository token, repository access and SSH; writes nothing",
			reqBody: controllers.CheckLadoConnectionRequest{},
			resps:   ladoResps(http.StatusOK, controllers.LadoConnectionCheckResponse{}, http.StatusBadRequest),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/connections", id: "createLadoConnection", tag: "lado",
			summary: "Store the host token, then link or clone the project",
			reqBody: controllers.CreateLadoConnectionRequest{},
			resps:   ladoResps(http.StatusOK, controllers.LadoConnectionResponse{}, http.StatusBadRequest, http.StatusConflict),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/credentials", id: "listLadoCredentials", tag: "lado",
			summary: "List the hosts with a stored token; no response carries a token",
			resps:   ladoResps(http.StatusOK, controllers.LadoCredentialListResponse{}),
		},
		{
			method: http.MethodPut, path: "/api/v1/lado/credentials/{host}", id: "storeLadoCredential", tag: "lado",
			summary:    "Check a host token with the host, then store it",
			pathParams: credentialHost,
			reqBody:    controllers.StoreLadoCredentialRequest{},
			resps:      ladoResps(http.StatusOK, controllers.LadoCredentialResponse{}, http.StatusBadRequest, http.StatusServiceUnavailable),
		},
		{
			method: http.MethodDelete, path: "/api/v1/lado/credentials/{host}", id: "deleteLadoCredential", tag: "lado",
			summary:    "Delete a host's stored token",
			pathParams: credentialHost,
			resps:      ladoResps(http.StatusNoContent, nil, http.StatusBadRequest),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/tasks", id: "listLadoTasks", tag: "lado",
			summary:    "List a project's tasks",
			pathParams: []any{controllers.ListLadoTasksQuery{}},
			resps:      ladoResps(http.StatusOK, controllers.LadoTaskListResponse{}, http.StatusBadRequest, http.StatusNotFound),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/tasks", id: "createLadoTask", tag: "lado",
			summary: "Create an issue on the project's repository, then its task",
			reqBody: controllers.CreateLadoTaskRequest{},
			resps:   ladoResps(http.StatusCreated, controllers.LadoTaskResponse{}, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/tasks/imports", id: "importLadoTasks", tag: "lado",
			summary: "Import open issues as tasks; writes nothing to the tracker",
			reqBody: controllers.ImportLadoTasksRequest{},
			resps:   ladoResps(http.StatusOK, controllers.ImportLadoTasksResponse{}, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/tasks/{taskId}", id: "getLadoTask", tag: "lado",
			summary:    "Get a task after reading its issue again",
			pathParams: taskID,
			resps:      ladoResps(http.StatusOK, controllers.LadoTaskResponse{}, http.StatusNotFound),
		},
		{
			method: http.MethodPatch, path: "/api/v1/lado/tasks/{taskId}", id: "updateLadoTask", tag: "lado",
			summary:    "Change a task's agent or model",
			pathParams: taskID,
			reqBody:    controllers.UpdateLadoTaskRequest{},
			resps:      ladoResps(http.StatusOK, controllers.LadoTaskResponse{}, http.StatusBadRequest, http.StatusNotFound),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/tasks/{taskId}/cancel", id: "cancelLadoTask", tag: "lado",
			summary:    "Cancel a task; the issue is never closed or edited",
			pathParams: taskID,
			resps:      ladoResps(http.StatusOK, controllers.LadoTaskResponse{}, http.StatusNotFound),
		},
	}
}

// ladoResps lists an operation's success response, the given error statuses,
// and the 500 and 501 every LADO route can answer.
func ladoResps(okStatus int, ok any, errorStatuses ...int) []respUnit {
	resps := make([]respUnit, 0, len(errorStatuses)+3)
	resps = append(resps, respUnit{okStatus, ok})
	for _, status := range append(errorStatuses, http.StatusInternalServerError, http.StatusNotImplemented) {
		resps = append(resps, respUnit{status, envelope.APIError{}})
	}
	return resps
}
