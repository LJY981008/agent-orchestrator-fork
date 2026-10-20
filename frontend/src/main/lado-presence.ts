// LADO fork: plan approval through the human at the desktop app
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). Any local
// process can call the daemon's loopback API, so an approval must come from
// here. The renderer names a plan revision; main reads that revision from the
// daemon itself, so the renderer cannot misstate the subject, shows a native
// confirmation naming it, and on confirm signs and sends the approval. The
// key is derived in memory from the per-launch desktop token and never leaves
// main. backend/internal/service/ladopresence verifies the proof, and both
// sides test the same fixed vector.
import { createHash, createHmac, randomBytes as cryptoRandomBytes } from "node:crypto";
import type { BaseWindow, Dialog, IpcMain, IpcMainInvokeEvent, MessageBoxOptions, WebContents } from "electron";
import {
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
} from "../shared/lado-presence";
import type { AppLocale } from "../shared/ui-locale";
// Main's own catalog: the dialog's words never come from the renderer.
import { trayCatalogs, type TrayMessageKey } from "./tray-catalog.generated";

export const LADO_PRESENCE_HEADER = "X-LADO-Presence";

// Separates the presence key from the desktop token and starts every signed message.
const KEY_LABEL = "lado-presence-v1";
// The daemon accepts a proof that expires at most 120 s ahead.
const PROOF_LIFETIME_SECONDS = 60;
const REQUEST_TIMEOUT_MS = 10_000;
const REVISION_ID = /^[A-Za-z0-9_-]{1,128}$/;
const SHA256_HEX = /^[0-9a-f]{64}$/;
// Line breaks or direction overrides in an issue title could fake the
// dialog's revision and sha256 lines.
const UNSAFE_TEXT = /[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028\u2029\u202a-\u202e\u2066-\u2069]+/g;

type Failure = Extract<LadoPresenceApproveResult, { status: "error" }>;
type PresenceMessageKey = Extract<TrayMessageKey, `lado.presence.${string}`>;
type DaemonAnswer = { ok: true; body: unknown } | { ok: false; failure: Failure };
type PlanSubject = { title: string; issue: string; revision: number; sha256: string; valid: boolean; latest: boolean };

export type LadoPresenceDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	dialog: Pick<Dialog, "showMessageBox">;
	/** net.fetch in the app. */
	fetch: (url: string, init: RequestInit) => Promise<Response>;
	/** The per-launch desktop token the daemon received over stdin. */
	token: () => string;
	/** http://127.0.0.1:<port> while the daemon is ready, else null. */
	daemonBaseUrl: () => string | null;
	/** The window the confirmation attaches to, when there is one. */
	parentWindow: () => BaseWindow | null;
	/** The UI language, read for each confirmation. */
	locale: () => AppLocale | Promise<AppLocale>;
	/** Only the app's own shell may ask for an approval. */
	isTrustedSender: (sender: WebContents) => boolean;
	/** Milliseconds since the epoch. */
	now?: () => number;
	randomBytes?: (size: number) => Uint8Array;
};

/** key = HMAC-SHA256(desktop token, "lado-presence-v1"). */
export function ladoPresenceKey(token: string): Buffer {
	return createHmac("sha256", token).update(KEY_LABEL).digest();
}

/**
 * The X-LADO-Presence value for one request, v1.<nonce>.<expires>.<mac>,
 * where mac covers the method, the escaped path, the body's sha256, the
 * nonce and the expiry. body must be the exact bytes sent.
 */
export function signLadoPresence(
	key: Uint8Array,
	method: string,
	path: string,
	body: string | Uint8Array,
	nonce: string,
	expiresUnixSeconds: number,
): string {
	const expires = String(expiresUnixSeconds);
	const bodySha = createHash("sha256").update(body).digest("hex");
	const mac = createHmac("sha256", key)
		.update(`${KEY_LABEL}\n${method}\n${path}\n${bodySha}\n${nonce}\n${expires}`)
		.digest("base64url");
	return `v1.${nonce}.${expires}.${mac}`;
}

export function registerLadoPresenceIpc(deps: LadoPresenceDeps): void {
	deps.ipcMain.handle(LADO_PRESENCE_APPROVE_PLAN_CHANNEL, async (event: IpcMainInvokeEvent, input: unknown) => {
		try {
			return await approvePlan(deps, event.sender, input);
		} catch (error) {
			// Nothing throws across IPC: the renderer always gets a result to show.
			return failure(undefined, error instanceof Error && error.message ? error.message : "The approval failed.");
		}
	});
}

async function approvePlan(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceApproveResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can ask for an approval.");
	}
	// The revision id is all the renderer gives; anything else it sends is ignored.
	const revisionId = record(input)?.revisionId;
	if (typeof revisionId !== "string" || !REVISION_ID.test(revisionId)) {
		return failure("LADO_PLAN_REVISION_ID_INVALID", "Invalid plan revision id.");
	}
	const planPath = `/api/v1/lado/plans/${encodeURIComponent(revisionId)}`;
	const read = await callDaemon(deps, planPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const plan = planSubject(read.body, revisionId);
	if (!plan) return failure(undefined, "The AO daemon's answer about this plan could not be read.");
	// The daemon refuses these as well, in this order; asking the human first
	// would only waste a confirmation.
	if (!plan.latest) {
		return failure("LADO_PLAN_SUPERSEDED", "A newer version of this plan exists. Review the latest plan and approve that one.");
	}
	if (!plan.valid) {
		return failure("LADO_PLAN_INVALID", "This plan has problems and cannot be approved. Ask for changes first.");
	}
	if (!(await confirm(deps, plan))) return { status: "cancelled" };

	// Signed after the confirmation, which can stay open longer than a proof lives.
	const approvePath = `${planPath}/approve`;
	const body = JSON.stringify({ sha256: plan.sha256 });
	const random = deps.randomBytes ?? ((size: number) => cryptoRandomBytes(size));
	const nonce = Buffer.from(random(16)).toString("base64url");
	const expires = Math.floor((deps.now ?? Date.now)() / 1000) + PROOF_LIFETIME_SECONDS;
	const proof = signLadoPresence(ladoPresenceKey(deps.token()), "POST", approvePath, body, nonce, expires);
	const sent = await callDaemon(deps, approvePath, {
		method: "POST",
		headers: { "content-type": "application/json", [LADO_PRESENCE_HEADER]: proof },
		body,
	});
	if (!sent.ok) return sent.failure;
	const approval = record(sent.body);
	if (!approval) return failure(undefined, "The approval was sent, but the AO daemon's answer could not be read.");
	return { status: "approved", approval: approval as LadoPresenceApproval };
}

async function confirm(deps: LadoPresenceDeps, plan: PlanSubject): Promise<boolean> {
	const catalog = trayCatalogs[await deps.locale()] ?? trayCatalogs.en;
	const t = (key: PresenceMessageKey, vars?: Record<string, string | number>) =>
		format(catalog[key] ?? trayCatalogs.en[key], vars);
	const options: MessageBoxOptions = {
		type: "question",
		message: t("lado.presence.approveTitle"),
		detail: t("lado.presence.approveDetail", {
			task: plan.title,
			issue: plan.issue,
			revision: plan.revision,
			sha: plan.sha256.slice(0, 12),
		}),
		buttons: [t("lado.presence.cancel"), t("lado.presence.approve")],
		defaultId: 0,
		cancelId: 0,
		noLink: true,
	};
	const parent = deps.parentWindow();
	const { response } = parent
		? await deps.dialog.showMessageBox(parent, options)
		: await deps.dialog.showMessageBox(options);
	return response === 1;
}

// The daemon base URL is read for each call, so a daemon restarted while the
// confirmation was open still gets the approval.
async function callDaemon(deps: LadoPresenceDeps, path: string, init: RequestInit): Promise<DaemonAnswer> {
	const base = deps.daemonBaseUrl();
	if (!base) return { ok: false, failure: failure("SERVICE_UNAVAILABLE", "The AO daemon is not ready.") };
	let response: Response;
	try {
		response = await deps.fetch(`${base}${path}`, { ...init, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
	} catch {
		return { ok: false, failure: failure("SERVICE_UNAVAILABLE", "The AO daemon could not be reached.") };
	}
	const body: unknown = await response.json().catch(() => undefined);
	if (response.ok) return { ok: true, body };
	// The daemon's error envelope: { error, code, message, requestId?, details? }.
	const envelope = record(body);
	return {
		ok: false,
		failure: failure(
			text(envelope?.code),
			text(envelope?.message) ?? `The AO daemon answered HTTP ${response.status}.`,
			record(envelope?.details),
		),
	};
}

// What the dialog names and the approval binds to, from LadoPlanResponse.
function planSubject(body: unknown, revisionId: string): PlanSubject | null {
	const plan = record(body);
	const issue = record(record(plan?.task)?.issue);
	const revision = record(plan?.revision);
	if (!plan || !issue || !revision) return null;
	const { latest } = plan;
	const { title, ref } = issue;
	const { id, number: revisionNumber, sha256, valid } = revision;
	if (
		id !== revisionId ||
		typeof title !== "string" ||
		typeof ref !== "string" ||
		typeof revisionNumber !== "number" ||
		typeof sha256 !== "string" ||
		!SHA256_HEX.test(sha256) ||
		typeof valid !== "boolean" ||
		typeof latest !== "boolean"
	) {
		return null;
	}
	return { title: oneLine(title), issue: oneLine(ref), revision: revisionNumber, sha256, valid, latest };
}

// tray.ts's {{var}} formatting, which it does not export.
function format(template: string, vars?: Record<string, string | number>): string {
	return vars ? template.replace(/\{\{(\w+)\}\}/g, (match, name: string) => String(vars[name] ?? match)) : template;
}

function oneLine(value: string): string {
	return value.replace(UNSAFE_TEXT, " ").trim();
}

function failure(code: string | undefined, message: string, details?: Record<string, unknown> | null): Failure {
	return { status: "error", ...(code ? { code } : {}), message, ...(details ? { details } : {}) };
}

function record(value: unknown): Record<string, unknown> | null {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function text(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}
