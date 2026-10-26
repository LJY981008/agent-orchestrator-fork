// LADO human-presence approvals shared by Electron main, preload and the
// renderer (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). The
// renderer names the plan revision, the rule and mode, or the project, and
// nothing else.
// Main reads the subject from the daemon, asks the human in a native dialog,
// and signs the request itself, so the renderer never holds the key.
import type { components } from "../api/schema";

export const LADO_PRESENCE_APPROVE_PLAN_CHANNEL = "lado:presence:approvePlan";
export const LADO_PRESENCE_CHANGE_RULE_CHANNEL = "lado:presence:changeRule";
export const LADO_PRESENCE_SET_LADO_MODE_CHANNEL = "lado:presence:setLadoMode";

export type LadoPresenceApproveRequest = { revisionId: string };

export type LadoPresenceApproval = components["schemas"]["LadoPlanApprovalResponse"];

/** code and details come from the daemon's error envelope when it answered with one. */
export type LadoPresenceFailure = { status: "error"; code?: string; message: string; details?: Record<string, unknown> };

export type LadoPresenceApproveResult =
	| { status: "approved"; approval: LadoPresenceApproval }
	| { status: "cancelled" }
	| LadoPresenceFailure;

export type LadoCeilingMode = components["schemas"]["LadoRuleCeiling"]["mode"];

/** The permission ceiling's modes, lowest first; "default" is never a ceiling. */
export const LADO_CEILING_MODES: readonly LadoCeilingMode[] = ["accept-edits", "auto", "bypass-permissions"];

/** Only the permission ceiling's mode needs the human; every other rule change does not. */
export type LadoPresenceRuleChangeRequest = { ruleId: string; ceiling: { mode: LadoCeilingMode } };

export type LadoPresenceRule = components["schemas"]["LadoRuleResponse"];

export type LadoPresenceRuleChangeResult =
	| { status: "changed"; rule: LadoPresenceRule }
	| { status: "cancelled" }
	| LadoPresenceFailure;

/**
 * Only turning LADO mode off needs the human: main refuses ladoMode true,
 * which the renderer sends to the daemon itself.
 */
export type LadoPresenceModeRequest = { projectId: string; ladoMode: boolean };

export type LadoPresenceProjectSettings = components["schemas"]["LadoProjectSettingsResponse"];

export type LadoPresenceModeResult =
	| { status: "changed"; settings: LadoPresenceProjectSettings }
	| { status: "cancelled" }
	| LadoPresenceFailure;
