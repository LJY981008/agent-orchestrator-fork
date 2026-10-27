// LADO human-presence approvals shared by Electron main, preload and the
// renderer (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). The
// renderer names the plan revision, the rule and mode, the project, the
// setup entries to adopt, the adoption to restore, or the setup change, and
// nothing else.
// Main reads the subject from the daemon, asks the human in a native dialog,
// and signs the request itself, so the renderer never holds the key.
import type { components } from "../api/schema";

export const LADO_PRESENCE_APPROVE_PLAN_CHANNEL = "lado:presence:approvePlan";
export const LADO_PRESENCE_CHANGE_RULE_CHANNEL = "lado:presence:changeRule";
export const LADO_PRESENCE_SET_LADO_MODE_CHANNEL = "lado:presence:setLadoMode";
export const LADO_PRESENCE_ADOPT_SETUP_CHANNEL = "lado:presence:adoptSetup";
export const LADO_PRESENCE_RESTORE_SETUP_CHANNEL = "lado:presence:restoreSetup";
export const LADO_PRESENCE_CHANGE_SETUP_CHANNEL = "lado:presence:changeSetup";

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

/** An inventory entry to adopt, and whether it moves or stays where it is. */
export type LadoPresenceSetupEntry = components["schemas"]["LadoSetupAdoptEntryRequest"];

/** Main previews the adoption of these entries itself and signs that plan. */
export type LadoPresenceAdoptSetupRequest = { entries: LadoPresenceSetupEntry[] };

export type LadoPresenceSetupAdoption = components["schemas"]["LadoSetupAdoptResponse"];

export type LadoPresenceAdoptSetupResult =
	| { status: "adopted"; adoption: LadoPresenceSetupAdoption }
	| { status: "cancelled" }
	| LadoPresenceFailure;

export type LadoPresenceRestoreSetupRequest = { adoptionId: string };

export type LadoPresenceSetupRestore = components["schemas"]["LadoSetupRestoreResponse"];

export type LadoPresenceRestoreSetupResult =
	| { status: "restored"; restore: LadoPresenceSetupRestore }
	| { status: "cancelled" }
	| LadoPresenceFailure;

/**
 * A setup change that can need the human: creating, editing or deleting an
 * item, setting where it applies, or keeping its drift. The body is the
 * route's request without dryRun, and empty for a delete; main checks the
 * route and the fields, previews the change itself and signs exactly this
 * request.
 */
export type LadoPresenceSetupChangeRequest = {
	method: "POST" | "PATCH" | "DELETE" | "PUT";
	path: string;
	body: Record<string, unknown>;
};

export type LadoPresenceSetupChange = components["schemas"]["LadoSetupChangeResponse"];

export type LadoPresenceSetupChangeResult =
	| { status: "changed"; change: LadoPresenceSetupChange }
	| { status: "cancelled" }
	| LadoPresenceFailure;
