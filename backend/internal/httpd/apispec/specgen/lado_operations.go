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
	"ControllersLadoProjectIDParam":                "LadoProjectIDParam",
	"ControllersLadoProjectSettingsResponse":       "LadoProjectSettingsResponse",
	"ControllersUpdateLadoProjectSettingsRequest":  "UpdateLadoProjectSettingsRequest",
	"ControllersCheckLadoConnectionRequest":        "CheckLadoConnectionRequest",
	"ControllersCreateLadoConnectionRequest":       "CreateLadoConnectionRequest",
	"ControllersLadoPermissionResponse":            "LadoPermissionResponse",
	"ControllersLadoTokenCheckResponse":            "LadoTokenCheckResponse",
	"ControllersLadoSSHCheckResponse":              "LadoSSHCheckResponse",
	"ControllersLadoLinkedProjectResponse":         "LadoLinkedProjectResponse",
	"ControllersLadoConnectionCheckResponse":       "LadoConnectionCheckResponse",
	"ControllersLadoConnectionResponse":            "LadoConnectionResponse",
	"ControllersLadoCredentialHostParam":           "LadoCredentialHostParam",
	"ControllersStoreLadoCredentialRequest":        "StoreLadoCredentialRequest",
	"ControllersLadoCredentialResponse":            "LadoCredentialResponse",
	"ControllersLadoCredentialListResponse":        "LadoCredentialListResponse",
	"ControllersLadoTaskIDParam":                   "LadoTaskIDParam",
	"ControllersListLadoTasksQuery":                "ListLadoTasksQuery",
	"ControllersLadoTaskIssueResponse":             "LadoTaskIssueResponse",
	"ControllersLadoTaskResponse":                  "LadoTaskResponse",
	"ControllersLadoTaskListResponse":              "LadoTaskListResponse",
	"ControllersCreateLadoTaskRequest":             "CreateLadoTaskRequest",
	"ControllersUpdateLadoTaskRequest":             "UpdateLadoTaskRequest",
	"ControllersImportLadoTasksRequest":            "ImportLadoTasksRequest",
	"ControllersImportLadoTasksResponse":           "ImportLadoTasksResponse",
	"ControllersLadoIssueResponse":                 "LadoIssueResponse",
	"ControllersLadoProjectIssuesResponse":         "LadoProjectIssuesResponse",
	"ControllersLadoRepositoryResponse":            "LadoRepositoryResponse",
	"ControllersLadoRepositoryFailureResponse":     "LadoRepositoryFailureResponse",
	"ControllersLadoProjectRepositoriesResponse":   "LadoProjectRepositoriesResponse",
	"ControllersLadoPlanIDParam":                   "LadoPlanIDParam",
	"ControllersLadoPresenceHeaderParam":           "LadoPresenceHeaderParam",
	"ControllersLadoPlanRevisionSummaryResponse":   "LadoPlanRevisionSummaryResponse",
	"ControllersLadoTaskPlanResponse":              "LadoTaskPlanResponse",
	"ControllersLadoPlanProblemResponse":           "LadoPlanProblemResponse",
	"ControllersLadoPlanRevisionResponse":          "LadoPlanRevisionResponse",
	"ControllersLadoPlanRequestResponse":           "LadoPlanRequestResponse",
	"ControllersLadoPlanApprovalResponse":          "LadoPlanApprovalResponse",
	"ControllersLadoPlanHistoryResponse":           "LadoPlanHistoryResponse",
	"ControllersLadoPlanContentResponse":           "LadoPlanContentResponse",
	"ControllersLadoPlanResponse":                  "LadoPlanResponse",
	"ControllersCreateLadoPlanRequestRequest":      "CreateLadoPlanRequestRequest",
	"ControllersApproveLadoPlanRequest":            "ApproveLadoPlanRequest",
	"ControllersLadoIssueTextResponse":             "LadoIssueTextResponse",
	"ControllersLadoIssueCommentResponse":          "LadoIssueCommentResponse",
	"ControllersLadoTaskViewResponse":              "LadoTaskViewResponse",
	"ControllersLadoRuleIDParam":                   "LadoRuleIDParam",
	"ControllersListLadoRulesQuery":                "ListLadoRulesQuery",
	"ControllersLadoEffectiveQuery":                "LadoEffectiveQuery",
	"ControllersLadoRuleCeiling":                   "LadoRuleCeiling",
	"ControllersLadoRuleOverrideResponse":          "LadoRuleOverrideResponse",
	"ControllersLadoRuleResponse":                  "LadoRuleResponse",
	"ControllersLadoRuleListResponse":              "LadoRuleListResponse",
	"ControllersLadoRuleVersionResponse":           "LadoRuleVersionResponse",
	"ControllersLadoRuleDetailResponse":            "LadoRuleDetailResponse",
	"ControllersLadoImpactProjectResponse":         "LadoImpactProjectResponse",
	"ControllersLadoImpactTaskResponse":            "LadoImpactTaskResponse",
	"ControllersLadoRuleImpactResponse":            "LadoRuleImpactResponse",
	"ControllersLadoRuleChangeResponse":            "LadoRuleChangeResponse",
	"ControllersCreateLadoRuleRequest":             "CreateLadoRuleRequest",
	"ControllersUpdateLadoRuleRequest":             "UpdateLadoRuleRequest",
	"ControllersSetLadoBindingRequest":             "SetLadoBindingRequest",
	"ControllersLadoEffectiveAgentResponse":        "LadoEffectiveAgentResponse",
	"ControllersLadoRuleSourceResponse":            "LadoRuleSourceResponse",
	"ControllersLadoEffectiveRuleResponse":         "LadoEffectiveRuleResponse",
	"ControllersLadoAORuleResponse":                "LadoAORuleResponse",
	"ControllersLadoCapabilityResponse":            "LadoCapabilityResponse",
	"ControllersLadoPolicyProblemResponse":         "LadoPolicyProblemResponse",
	"ControllersLadoApprovedRulesResponse":         "LadoApprovedRulesResponse",
	"ControllersLadoEffectiveResponse":             "LadoEffectiveResponse",
	"ControllersListLadoQueueQuery":                "ListLadoQueueQuery",
	"ControllersLadoQueueItemResponse":             "LadoQueueItemResponse",
	"ControllersLadoQueueResponse":                 "LadoQueueResponse",
	"ControllersLadoTaskQueueResponse":             "LadoTaskQueueResponse",
	"ControllersLadoExecutionPRResponse":           "LadoExecutionPRResponse",
	"ControllersLadoTaskExecutionResponse":         "LadoTaskExecutionResponse",
	"ControllersLadoSessionIDParam":                "LadoSessionIDParam",
	"ControllersLadoExecutionTaskResponse":         "LadoExecutionTaskResponse",
	"ControllersLadoExecutionPlanResponse":         "LadoExecutionPlanResponse",
	"ControllersLadoExecutionRuleResponse":         "LadoExecutionRuleResponse",
	"ControllersLadoExecutionSnapshotResponse":     "LadoExecutionSnapshotResponse",
	"ControllersLadoExecutionBaseResponse":         "LadoExecutionBaseResponse",
	"ControllersLadoExecutionResponse":             "LadoExecutionResponse",
	"ControllersLadoDispatchSettingsResponse":      "LadoDispatchSettingsResponse",
	"ControllersUpdateLadoDispatchSettingsRequest": "UpdateLadoDispatchSettingsRequest",
}

func ladoOperations() []operation {
	projectID := []any{controllers.LadoProjectIDParam{}}
	taskID := []any{controllers.LadoTaskIDParam{}}
	revisionID := []any{controllers.LadoPlanIDParam{}}
	credentialHost := []any{controllers.LadoCredentialHostParam{}}
	ruleID := []any{controllers.LadoRuleIDParam{}}
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
			summary:    "Change a project's LADO mode or its cap on running executions; turning LADO mode off needs a human-presence proof",
			pathParams: []any{controllers.LadoProjectIDParam{}, controllers.LadoPresenceHeaderParam{}},
			reqBody:    controllers.UpdateLadoProjectSettingsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.LadoProjectSettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
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
			summary:    "Get the task view: the task, its issue and comments read again, and its plan history",
			pathParams: taskID,
			resps:      ladoResps(http.StatusOK, controllers.LadoTaskViewResponse{}, http.StatusNotFound),
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
		{
			method: http.MethodPost, path: "/api/v1/lado/tasks/{taskId}/plan-requests", id: "requestLadoPlan", tag: "lado",
			summary:    "Pin the issue with its trusted comments and ask the project's orchestrator for a plan or a changed plan",
			pathParams: taskID,
			reqBody:    controllers.CreateLadoPlanRequestRequest{},
			resps:      ladoResps(http.StatusCreated, controllers.LadoPlanRequestResponse{}, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/tasks/{taskId}/plans", id: "listLadoPlans", tag: "lado",
			summary:    "List a task's plan requests, revisions and approvals",
			pathParams: taskID,
			resps:      ladoResps(http.StatusOK, controllers.LadoPlanHistoryResponse{}, http.StatusNotFound),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/plans/{revisionId}", id: "getLadoPlan", tag: "lado",
			summary:    "Get a plan revision with its pinned content",
			pathParams: revisionID,
			resps:      ladoResps(http.StatusOK, controllers.LadoPlanResponse{}, http.StatusNotFound),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/plans/{revisionId}/approve", id: "approveLadoPlan", tag: "lado",
			summary:    "Approve one exact plan revision; needs a human-presence proof from the AO desktop app",
			pathParams: []any{controllers.LadoPlanIDParam{}, controllers.LadoPresenceHeaderParam{}},
			reqBody:    controllers.ApproveLadoPlanRequest{},
			resps:      ladoResps(http.StatusOK, controllers.LadoPlanApprovalResponse{}, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/rules", id: "listLadoRules", tag: "lado",
			summary:    "List the rules with their active versions and bindings",
			pathParams: []any{controllers.ListLadoRulesQuery{}},
			resps:      ladoResps(http.StatusOK, controllers.LadoRuleListResponse{}, http.StatusBadRequest),
		},
		{
			method: http.MethodPost, path: "/api/v1/lado/rules", id: "createLadoRule", tag: "lado",
			summary: "Create a user rule, or show what creating it reaches with dryRun",
			reqBody: controllers.CreateLadoRuleRequest{},
			resps:   ladoResps(http.StatusOK, controllers.LadoRuleChangeResponse{}, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/rules/{ruleId}", id: "getLadoRule", tag: "lado",
			summary:    "Get a rule with its versions",
			pathParams: ruleID,
			resps:      ladoResps(http.StatusOK, controllers.LadoRuleDetailResponse{}, http.StatusNotFound),
		},
		{
			method: http.MethodPatch, path: "/api/v1/lado/rules/{ruleId}", id: "updateLadoRule", tag: "lado",
			summary:    "Edit, archive or unarchive a rule, or show what the change reaches with dryRun; the permission ceiling's mode needs a human-presence proof",
			pathParams: []any{controllers.LadoRuleIDParam{}, controllers.LadoPresenceHeaderParam{}},
			reqBody:    controllers.UpdateLadoRuleRequest{},
			resps:      ladoResps(http.StatusOK, controllers.LadoRuleChangeResponse{}, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodDelete, path: "/api/v1/lado/rules/{ruleId}", id: "deleteLadoRule", tag: "lado",
			summary:    "Delete a user rule no approval used",
			pathParams: ruleID,
			resps:      ladoResps(http.StatusNoContent, nil, http.StatusForbidden, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodPut, path: "/api/v1/lado/bindings", id: "setLadoBinding", tag: "lado",
			summary: "Turn a rule on or off at Global, or force it for a project or task, or show what that reaches with dryRun",
			reqBody: controllers.SetLadoBindingRequest{},
			resps:   ladoResps(http.StatusOK, controllers.LadoRuleChangeResponse{}, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/effective", id: "getLadoEffective", tag: "lado",
			summary:    "Resolve the rules for a project, or for a task at its latest plan revision",
			pathParams: []any{controllers.LadoEffectiveQuery{}},
			resps:      ladoResps(http.StatusOK, controllers.LadoEffectiveResponse{}, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/queue", id: "listLadoQueue", tag: "lado",
			summary:    "List the approved plans waiting for the dispatcher, in the order it takes them",
			pathParams: []any{controllers.ListLadoQueueQuery{}},
			resps:      ladoResps(http.StatusOK, controllers.LadoQueueResponse{}),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/executions/{sessionId}", id: "getLadoExecution", tag: "lado",
			summary:    "Get the execution a worker session runs: its task, plan, rule snapshot, base and run",
			pathParams: []any{controllers.LadoSessionIDParam{}},
			resps:      ladoResps(http.StatusOK, controllers.LadoExecutionResponse{}, http.StatusNotFound),
		},
		{
			method: http.MethodGet, path: "/api/v1/lado/dispatch-settings", id: "getLadoDispatchSettings", tag: "lado",
			summary: "Get the dispatch settings: the cap on running executions, their maximum run time and the memory hold",
			resps:   ladoResps(http.StatusOK, controllers.LadoDispatchSettingsResponse{}),
		},
		{
			method: http.MethodPut, path: "/api/v1/lado/dispatch-settings", id: "updateLadoDispatchSettings", tag: "lado",
			summary: "Replace the dispatch settings; needs no human-presence proof",
			reqBody: controllers.UpdateLadoDispatchSettingsRequest{},
			resps:   ladoResps(http.StatusOK, controllers.LadoDispatchSettingsResponse{}, http.StatusBadRequest),
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
