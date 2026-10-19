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

var ladoSchemaNames = map[string]string{
	"ControllersLadoProjectIDParam":               "LadoProjectIDParam",
	"ControllersLadoProjectSettingsResponse":      "LadoProjectSettingsResponse",
	"ControllersUpdateLadoProjectSettingsRequest": "UpdateLadoProjectSettingsRequest",
}

func ladoOperations() []operation {
	projectID := []any{controllers.LadoProjectIDParam{}}
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
	}
}
