// LADO human-presence approvals shared by Electron main, preload and the
// renderer (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). The
// renderer names the plan revision and nothing else. Main reads the subject
// from the daemon, asks the human in a native dialog, and signs the approval
// itself, so the renderer never holds the key.
import type { components } from "../api/schema";

export const LADO_PRESENCE_APPROVE_PLAN_CHANNEL = "lado:presence:approvePlan";

export type LadoPresenceApproveRequest = { revisionId: string };

export type LadoPresenceApproval = components["schemas"]["LadoPlanApprovalResponse"];

export type LadoPresenceApproveResult =
	| { status: "approved"; approval: LadoPresenceApproval }
	| { status: "cancelled" }
	/** code and details come from the daemon's error envelope when it answered with one. */
	| { status: "error"; code?: string; message: string; details?: Record<string, unknown> };
