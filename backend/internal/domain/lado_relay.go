package domain

// LADO fork: relaying a developer's instruction to a running worker (issue
// #49). In a LADO project the orchestrator may not message a worker itself
// (LADO_SEND_REFUSED). When the developer tells the orchestrator to have a
// live worker go on, the orchestrator asks LADO to relay the instruction
// (ao lado relay). LADO checks the caller, the task, the worker and the
// developer message the instruction rests on, sends the message under its
// own name and records each relay in lado_relays.
//
// The evidence proves only that the developer wrote that sentence; it does
// not prove what the instruction says. An instruction that is not part of the
// developer's message is marked beyond evidence, and one that may leave the
// approved plan is marked outside plan. Neither is refused.

import (
	"strings"
	"time"
	"unicode/utf8"
)

// LadoRelayID names one relay: "lado-relay-<id>".
type LadoRelayID string

// LadoRelayState is where one relay stands.
type LadoRelayState string

// Relay states. LADO writes a relay as sending before it delivers, then
// delivered or failed with the outcome, so no delivery goes unrecorded.
const (
	LadoRelaySending   LadoRelayState = "sending"
	LadoRelayDelivered LadoRelayState = "delivered"
	LadoRelayFailed    LadoRelayState = "failed"
)

// Limits of a relay request. Lengths count characters (runes) after
// LadoRelayNormalize.
const (
	// LadoRelayMaxInstructionChars caps the instruction.
	LadoRelayMaxInstructionChars = 8000
	// LadoRelayMinEvidenceChars and LadoRelayMaxEvidenceChars bound the
	// developer sentence quoted as evidence. A very short quote would match
	// almost any message.
	LadoRelayMinEvidenceChars = 4
	LadoRelayMaxEvidenceChars = 2000
	// LadoRelayEvidenceMaxAge is how old the developer message the evidence
	// matches may be.
	LadoRelayEvidenceMaxAge = 24 * time.Hour
)

// LadoRelaySenderLabel is the sender name a Chat worker shows on a relayed
// message. It is stored with the message, so it stays in this language.
const LadoRelaySenderLabel = "LADO 전달 · 개발자 지시"

// Error codes of a relay request (apierr codes).
const (
	// LadoRelayInvalid: a field is missing, too long or too short.
	LadoRelayInvalid = "LADO_RELAY_INVALID"
	// LadoRelayNotOrchestrator: the calling session is not a live
	// orchestrator of the project.
	LadoRelayNotOrchestrator = "LADO_RELAY_NOT_ORCHESTRATOR"
	// LadoRelayNotLadoProject: the project does not run in LADO mode.
	LadoRelayNotLadoProject = "LADO_RELAY_NOT_LADO_PROJECT"
	// LadoRelayTaskNotFound: no task has that id or issue number.
	LadoRelayTaskNotFound = "LADO_RELAY_TASK_NOT_FOUND"
	// LadoRelayTaskAmbiguous: an issue number names tasks in more than one
	// repository; the details list them.
	LadoRelayTaskAmbiguous = "LADO_RELAY_TASK_AMBIGUOUS"
	// LadoRelayWrongProject: the task belongs to another project.
	LadoRelayWrongProject = "LADO_RELAY_WRONG_PROJECT"
	// LadoRelayNoWorker: the task has no live execution worker; the details
	// carry a LadoRelayNoWorker* reason.
	LadoRelayNoWorker = "LADO_RELAY_NO_WORKER"
	// LadoRelayNotExecution: the named session is not an execution of the
	// task (a plan worker, for one).
	LadoRelayNotExecution = "LADO_RELAY_NOT_EXECUTION"
	// LadoRelayAmbiguous: more than one worker of the task runs and no
	// session was named; the details list the candidates.
	LadoRelayAmbiguous = "LADO_RELAY_AMBIGUOUS"
	// LadoRelayEvidenceUnverified: the evidence matches no developer message
	// LADO can verify; the details carry a LadoRelayEvidence* reason.
	LadoRelayEvidenceUnverified = "LADO_RELAY_EVIDENCE_UNVERIFIED"
	// LadoRelayDeliveryFailed: LADO could not deliver the message; the relay
	// is recorded as failed with the reason.
	LadoRelayDeliveryFailed = "LADO_RELAY_DELIVERY_FAILED"
)

// Reasons of LadoRelayNoWorker: why the newest execution of the task no
// longer takes messages, or none when the task never had one.
const (
	LadoRelayNoWorkerEnded     = "ended"
	LadoRelayNoWorkerExited    = "exited"
	LadoRelayNoWorkerTimedOut  = "timed_out"
	LadoRelayNoWorkerSettled   = "settled"
	LadoRelayNoWorkerCancelled = "cancelled"
	LadoRelayNoWorkerNone      = "none"
)

// Reasons of LadoRelayEvidenceUnverified.
const (
	// LadoRelayEvidenceTerminalOrchestrator: a terminal orchestrator's last
	// prompt is not only what the developer typed, so LADO cannot verify
	// evidence for it.
	LadoRelayEvidenceTerminalOrchestrator = "terminal_orchestrator"
	// LadoRelayEvidenceNotFound: no developer message of the orchestrator's
	// conversation contains the evidence.
	LadoRelayEvidenceNotFound = "not_found"
	// LadoRelayEvidenceExpired: only developer messages older than
	// LadoRelayEvidenceMaxAge contain it.
	LadoRelayEvidenceExpired = "expired"
)

// Audit actions of relays.
const (
	LadoRelayAuditSent    = "lado.relay_sent"
	LadoRelayAuditRefused = "lado.relay_refused"
)

// LadoRelayFlaggedReason is the outside-plan reason when the orchestrator
// itself marked the instruction as outside the approved plan.
const LadoRelayFlaggedReason = "오케스트레이터가 계획 밖으로 표시"

// LadoRelayPlanUnreadReason is the outside-plan reason when LADO could not
// read the approved plan, so it could not check the paths the instruction
// names. The relay still goes through: outside plan is a mark, never a
// refusal.
const LadoRelayPlanUnreadReason = "계획을 읽지 못해 경로를 확인하지 못함"

// LadoRelayPathReason is the outside-plan reason for a path the instruction
// names that no affected path of the plan (or the worker's part) covers.
func LadoRelayPathReason(path string) string {
	return "계획 밖 경로: " + path
}

// LadoRelay is one relay of a developer instruction to a worker
// (lado_relays).
type LadoRelay struct {
	ID        LadoRelayID
	TaskID    LadoTaskID
	ProjectID ProjectID
	// OrchestratorSessionID asked for the relay; WorkerSessionID received it,
	// an execution of the task with WorkerRole (LadoRole*) and, for a part or
	// merge worker, PartID.
	OrchestratorSessionID SessionID
	WorkerSessionID       SessionID
	WorkerRole            string
	PartID                string
	// Instruction is what the worker is told and Evidence the developer
	// sentence it rests on, as the orchestrator quoted it.
	// EvidenceMessageID and EvidenceAt name the developer message of the
	// orchestrator's conversation that contains it.
	Instruction       string
	Evidence          string
	EvidenceMessageID string
	EvidenceAt        time.Time
	// BeyondEvidence says the instruction is not part of the developer
	// message the evidence matched.
	BeyondEvidence bool
	// OutsidePlan says the instruction may leave the approved plan, for
	// OutsidePlanReasons.
	OutsidePlan        bool
	OutsidePlanReasons []string
	State              LadoRelayState
	// Failure is why a failed relay was not delivered.
	Failure     string
	CreatedAt   time.Time
	DeliveredAt time.Time
}

// LadoRelayExecution is one execution of a task as a relay reads it: the
// execution with whether its session ended, and the session's activity, ""
// without a session row. Its PR and verification facts are not read.
type LadoRelayExecution struct {
	Execution       LadoExecution
	SessionActivity ActivityState
}

// LadoRelayNormalize collapses runs of white space to one space and trims
// the ends, so evidence and instructions compare as the developer wrote them
// whatever the line breaks.
func LadoRelayNormalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// LadoRelayTextLen is the length of s in characters after
// LadoRelayNormalize, which the relay limits count.
func LadoRelayTextLen(s string) int {
	return utf8.RuneCountInString(LadoRelayNormalize(s))
}

// Marks of the worker's message. Each starts the paragraph it marks, so a
// person and the worker see them first.
const (
	LadoRelayHeader             = "LADO 전달: 개발자 지시"
	LadoRelayBeyondEvidenceMark = "[개발자 원문과 다른 지시]"
	LadoRelayOutsidePlanMark    = "[승인된 계획 밖일 수 있음]"
)

// LadoRelayMessageText is the message a worker receives for r: the header
// with the relay id, the instruction, the developer's message quoted, the
// beyond-evidence and outside-plan marks when they apply, and that the
// approved plan's rules still hold.
func LadoRelayMessageText(r LadoRelay) string {
	var b strings.Builder
	b.WriteString(LadoRelayHeader)
	if r.ID != "" {
		b.WriteString(" (" + string(r.ID) + ")")
	}
	b.WriteString("\n개발자가 오케스트레이터에게 내린 지시를 LADO 가 전달합니다.\n\n지시:\n")
	b.WriteString(strings.TrimSpace(r.Instruction))
	b.WriteString("\n\n개발자 원문:\n")
	b.WriteString(ladoRelayQuote(r.Evidence))
	if r.BeyondEvidence {
		b.WriteString("\n\n" + LadoRelayBeyondEvidenceMark +
			" 이 지시는 개발자 원문과 같지 않습니다. 개발자 원문을 기준으로 판단하고, 원문에 없는 일(특히 삭제·보호 경로·범위 확대)은 하기 전에 보고하세요.")
	}
	if r.OutsidePlan {
		b.WriteString("\n\n" + LadoRelayOutsidePlanMark + " 이 지시는 승인된 계획 범위를 벗어날 수 있습니다")
		if len(r.OutsidePlanReasons) > 0 {
			b.WriteString(": " + strings.Join(r.OutsidePlanReasons, "; "))
		}
		b.WriteString(". 바뀐 경로는 검증에서 '계획 밖 경로'로 확인됩니다.")
	}
	b.WriteString("\n\n승인된 계획의 규칙(권한 상한, 보호 경로, 통합 승인)은 그대로입니다.")
	return b.String()
}

// ladoRelayQuote quotes text line by line in Markdown.
func ladoRelayQuote(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}
