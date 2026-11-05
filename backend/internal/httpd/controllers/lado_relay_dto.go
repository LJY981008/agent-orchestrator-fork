package controllers

// LADO fork: DTOs of relaying a developer's instruction to a running worker
// (issue #49). Times are RFC 3339.

import "time"

// CreateLadoRelayRequest asks LADO to relay a developer's instruction to a
// running worker of a task.
type CreateLadoRelayRequest struct {
	SessionID       string `json:"sessionId" description:"The orchestrator asking (AO_SESSION_ID); it must be a live Chat orchestrator of a project in LADO mode."`
	Task            string `json:"task" description:"The task: its id (lado-task-…), an issue number (#49 or 49) or a canonical issue id of the orchestrator's project."`
	WorkerSessionID string `json:"workerSessionId,omitempty" description:"The worker to relay to, an execution of the task; omitted picks the task's one live worker."`
	Message         string `json:"message" description:"The instruction the worker receives; best the developer's own words."`
	Evidence        string `json:"evidence" description:"The developer's sentence the instruction rests on, quoted as the developer typed it in the orchestrator's conversation."`
	OutsidePlan     bool   `json:"outsidePlan,omitempty" description:"The orchestrator marks the instruction as leaving the approved plan (a new step or another path)."`
}

// LadoRelayResponse is one relay of a developer's instruction to a worker.
type LadoRelayResponse struct {
	ID                    string     `json:"id"`
	TaskID                string     `json:"taskId"`
	ProjectID             string     `json:"projectId"`
	OrchestratorSessionID string     `json:"orchestratorSessionId" description:"The orchestrator that asked for the relay."`
	WorkerSessionID       string     `json:"workerSessionId" description:"The execution worker that received it."`
	WorkerRole            string     `json:"workerRole,omitempty" enum:"part,merge,integration" description:"What the worker does in a task split into parts; omitted for a task's one worker."`
	PartID                string     `json:"partId,omitempty" description:"The part of a part or merge worker."`
	Instruction           string     `json:"instruction" description:"What the worker was told."`
	Evidence              string     `json:"evidence" description:"The developer's sentence the instruction rests on, as the orchestrator quoted it."`
	EvidenceMessageID     string     `json:"evidenceMessageId" description:"The developer's message of the orchestrator's conversation that contains the evidence."`
	EvidenceAt            time.Time  `json:"evidenceAt" description:"When the developer wrote that message."`
	BeyondEvidence        bool       `json:"beyondEvidence" description:"The instruction is not part of the developer's message; the worker was told to judge by the developer's words."`
	OutsidePlan           bool       `json:"outsidePlan" description:"The instruction may leave the approved plan; outsidePlanReasons say why."`
	OutsidePlanReasons    []string   `json:"outsidePlanReasons"`
	State                 string     `json:"state" enum:"sending,delivered,failed"`
	Failure               string     `json:"failure,omitempty" description:"Why a failed relay was not delivered."`
	CreatedAt             time.Time  `json:"createdAt"`
	DeliveredAt           *time.Time `json:"deliveredAt,omitempty"`
}

// LadoRelayListResponse is a task's relays, newest first.
type LadoRelayListResponse struct {
	Relays []LadoRelayResponse `json:"relays"`
}
