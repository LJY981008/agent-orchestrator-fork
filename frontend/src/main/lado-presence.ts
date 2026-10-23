// LADO fork: plan approval, permission ceiling changes and turning LADO mode
// off, through the human at the desktop app
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md). Any local
// process can call the daemon's loopback API, so these must come from here.
// The renderer names a plan revision, the ceiling and a mode, or a project;
// main reads the subject from the daemon itself, so the renderer cannot
// misstate it, shows a native confirmation naming it, and on confirm signs
// and sends the request. An approval also signs the hash of the rules that
// apply to the task right now. The key is derived in memory from the
// per-launch desktop token and never leaves main.
// backend/internal/service/ladopresence verifies the proof, and both sides
// test the same fixed vector.
import { createHash, createHmac, randomBytes as cryptoRandomBytes } from "node:crypto";
import type { BaseWindow, Dialog, IpcMain, IpcMainInvokeEvent, MessageBoxOptions, WebContents } from "electron";
import {
	LADO_CEILING_MODES,
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	LADO_PRESENCE_CHANGE_RULE_CHANNEL,
	LADO_PRESENCE_SET_LADO_MODE_CHANNEL,
	type LadoCeilingMode,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
	type LadoPresenceFailure,
	type LadoPresenceModeResult,
	type LadoPresenceProjectSettings,
	type LadoPresenceRule,
	type LadoPresenceRuleChangeResult,
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
// AO's project id alphabet (backend/internal/service/project), within a
// folder name's length; AO also refuses "..".
const PROJECT_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/;
const SHA256_HEX = /^[0-9a-f]{64}$/;
// The one rule whose change needs the human (docs/LJY-PRD/HARNESS.md §14).
const CEILING_RULE_ID = "agent.permission.ceiling";
// Line breaks or direction overrides in an issue title or a project name
// could fake the dialog's other lines.
const UNSAFE_TEXT = /[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028\u2029\u202a-\u202e\u2066-\u2069]+/g;

type Failure = LadoPresenceFailure;
type PresenceMessageKey = Extract<TrayMessageKey, `lado.presence.${string}`>;
type Translate = (key: PresenceMessageKey, vars?: Record<string, string | number>) => string;
type DaemonAnswer = { ok: true; body: unknown } | { ok: false; failure: Failure };
type PlanSubject = {
	taskId: string;
	title: string;
	issue: string;
	revision: number;
	sha256: string;
	valid: boolean;
	latest: boolean;
};
// What the task's rules give right now, from LadoEffectiveResponse.
type RulesSubject = { sha256: string; on: number; ceiling: LadoCeilingMode; blocking: number };
// The permission ceiling as the daemon has it, from LadoRuleDetailResponse.
type CeilingSubject = { rule: LadoPresenceRule; version: number; mode: LadoCeilingMode | null };

const MODE_KEYS: Record<LadoCeilingMode, PresenceMessageKey> = {
	"accept-edits": "lado.presence.mode.acceptEdits",
	auto: "lado.presence.mode.auto",
	"bypass-permissions": "lado.presence.mode.bypassPermissions",
};

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
	deps.ipcMain.handle(LADO_PRESENCE_CHANGE_RULE_CHANNEL, async (event: IpcMainInvokeEvent, input: unknown) => {
		try {
			return await changeRule(deps, event.sender, input);
		} catch (error) {
			return failure(undefined, error instanceof Error && error.message ? error.message : "The rule change failed.");
		}
	});
	deps.ipcMain.handle(LADO_PRESENCE_SET_LADO_MODE_CHANNEL, async (event: IpcMainInvokeEvent, input: unknown) => {
		try {
			return await setLadoMode(deps, event.sender, input);
		} catch (error) {
			return failure(undefined, error instanceof Error && error.message ? error.message : "Turning LADO mode off failed.");
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
	// The approval binds the rules as they apply to the task now; the daemon
	// refuses it when they changed before it arrives.
	const effectivePath = `/api/v1/lado/effective?taskId=${encodeURIComponent(plan.taskId)}`;
	const effective = await callDaemon(deps, effectivePath, { method: "GET", headers: { accept: "application/json" } });
	if (!effective.ok) return effective.failure;
	const rules = rulesSubject(effective.body, plan.taskId);
	if (!rules) return failure(undefined, "The AO daemon's answer about this task's rules could not be read.");
	const t = await translator(deps);
	const detail = [
		t("lado.presence.approveDetail", {
			task: plan.title,
			issue: plan.issue,
			revision: plan.revision,
			sha: plan.sha256.slice(0, 12),
		}),
		[
			t("lado.presence.approveRules", { on: rules.on, ceiling: t(MODE_KEYS[rules.ceiling]), sha: rules.sha256.slice(0, 12) }),
			...(rules.blocking > 0 ? [t("lado.presence.approveRulesBlocking", { blocking: rules.blocking })] : []),
		].join("\n"),
	].join("\n\n");
	if (!(await confirm(deps, t, t("lado.presence.approveTitle"), detail, t("lado.presence.approve")))) {
		return { status: "cancelled" };
	}

	const approvePath = `${planPath}/approve`;
	const body = JSON.stringify({ sha256: plan.sha256, policySha256: rules.sha256 });
	const sent = await callDaemon(deps, approvePath, signed(deps, "POST", approvePath, body));
	if (!sent.ok) return sent.failure;
	const approval = record(sent.body);
	if (!approval) return failure(undefined, "The approval was sent, but the AO daemon's answer could not be read.");
	return { status: "approved", approval: approval as LadoPresenceApproval };
}

async function changeRule(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceRuleChangeResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can change a rule.");
	}
	// The rule id and the mode are all the renderer gives; anything else it sends is ignored.
	const request = record(input);
	const ruleId = request?.ruleId;
	const mode = record(request?.ceiling)?.mode;
	if (ruleId !== CEILING_RULE_ID) {
		return failure("LADO_RULE_CEILING_INVALID", "Only the permission ceiling's mode changes here.");
	}
	if (!isCeilingMode(mode)) {
		return failure("LADO_RULE_CEILING_INVALID", "Choose accept-edits, auto or bypass-permissions as the permission ceiling.");
	}
	const rulePath = `/api/v1/lado/rules/${encodeURIComponent(ruleId)}`;
	const read = await callDaemon(deps, rulePath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const rule = record(record(read.body)?.rule);
	if (rule?.id !== CEILING_RULE_ID || rule.protected !== true) {
		return failure("LADO_RULE_CEILING_INVALID", "The AO daemon does not have this rule as the protected permission ceiling.");
	}
	const ceiling = ceilingSubject(rule);
	if (!ceiling) return failure(undefined, "The AO daemon's answer about this rule could not be read.");
	// Nothing would change, so there is nothing to confirm.
	if (ceiling.mode === mode) return { status: "changed", rule: ceiling.rule };
	const t = await translator(deps);
	const detail = t("lado.presence.ceilingChangeDetail", {
		// A ceiling without a valid mode blocks every task; setting one repairs it.
		from: ceiling.mode ? t(MODE_KEYS[ceiling.mode]) : "—",
		to: t(MODE_KEYS[mode]),
	});
	if (!(await confirm(deps, t, t("lado.presence.ruleChangeTitle"), detail, t("lado.presence.change")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes.
	const body = JSON.stringify({ baseVersion: ceiling.version, ceiling: { mode } });
	const sent = await callDaemon(deps, rulePath, signed(deps, "PATCH", rulePath, body));
	if (!sent.ok) return sent.failure;
	const changed = record(record(sent.body)?.rule);
	if (!changed) return failure(undefined, "The change was sent, but the AO daemon's answer could not be read.");
	return { status: "changed", rule: changed as LadoPresenceRule };
}

// Turning LADO mode off lets the project's workers start without an approved
// plan, so only the human may do it. Turning it on needs no confirmation: the
// renderer sends that to the daemon itself, and this channel refuses it.
async function setLadoMode(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceModeResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can turn LADO mode off.");
	}
	// The project id and the mode are all the renderer gives; anything else it sends is ignored.
	const request = record(input);
	const projectId = request?.projectId;
	if (typeof projectId !== "string" || !PROJECT_ID.test(projectId) || projectId.includes("..")) {
		return failure("LADO_PROJECT_ID_INVALID", "Invalid project id.");
	}
	if (request?.ladoMode !== false) {
		return failure("LADO_MODE_CHANGE_INVALID", "Only turning LADO mode off is confirmed here. Turn it on from the app.");
	}
	const settingsPath = `/api/v1/lado/projects/${encodeURIComponent(projectId)}/settings`;
	const read = await callDaemon(deps, settingsPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const settings = projectSettings(read.body, projectId);
	if (!settings) return failure(undefined, "The AO daemon's answer about this project's LADO settings could not be read.");
	// Nothing would change, so there is nothing to confirm.
	if (!settings.ladoMode) return { status: "changed", settings };
	const projectPath = `/api/v1/projects/${encodeURIComponent(projectId)}`;
	const project = await callDaemon(deps, projectPath, { method: "GET", headers: { accept: "application/json" } });
	if (!project.ok) return project.failure;
	const name = projectName(project.body, projectId);
	if (name === null) return failure(undefined, "The AO daemon's answer about this project could not be read.");
	const t = await translator(deps);
	const title = t("lado.presence.modeOffTitle", { project: name });
	if (!(await confirm(deps, t, title, t("lado.presence.modeOffDetail", { id: projectId }), t("lado.presence.turnOff")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes.
	const body = JSON.stringify({ ladoMode: false });
	const sent = await callDaemon(deps, settingsPath, signed(deps, "PUT", settingsPath, body));
	if (!sent.ok) return sent.failure;
	const changed = projectSettings(sent.body, projectId);
	if (!changed) return failure(undefined, "LADO mode was turned off, but the AO daemon's answer could not be read.");
	return { status: "changed", settings: changed };
}

// A request carrying a fresh proof. Signed after the confirmation, which can
// stay open longer than a proof lives.
function signed(deps: LadoPresenceDeps, method: string, path: string, body: string): RequestInit {
	const random = deps.randomBytes ?? ((size: number) => cryptoRandomBytes(size));
	const nonce = Buffer.from(random(16)).toString("base64url");
	const expires = Math.floor((deps.now ?? Date.now)() / 1000) + PROOF_LIFETIME_SECONDS;
	const proof = signLadoPresence(ladoPresenceKey(deps.token()), method, path, body, nonce, expires);
	return { method, headers: { "content-type": "application/json", [LADO_PRESENCE_HEADER]: proof }, body };
}

// Main's catalog in the current language, read for each confirmation.
async function translator(deps: LadoPresenceDeps): Promise<Translate> {
	const catalog = trayCatalogs[await deps.locale()] ?? trayCatalogs.en;
	return (key, vars) => format(catalog[key] ?? trayCatalogs.en[key], vars);
}

async function confirm(
	deps: LadoPresenceDeps,
	t: Translate,
	message: string,
	detail: string,
	confirmLabel: string,
): Promise<boolean> {
	const options: MessageBoxOptions = {
		type: "question",
		message,
		detail,
		buttons: [t("lado.presence.cancel"), confirmLabel],
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
	const task = record(plan?.task);
	const issue = record(task?.issue);
	const revision = record(plan?.revision);
	if (!plan || !task || !issue || !revision) return null;
	const { latest } = plan;
	const { title, ref } = issue;
	const { id, number: revisionNumber, sha256, valid, taskId } = revision;
	if (
		id !== revisionId ||
		typeof task.id !== "string" ||
		task.id === "" ||
		taskId !== task.id ||
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
	return { taskId: task.id, title: oneLine(title), issue: oneLine(ref), revision: revisionNumber, sha256, valid, latest };
}

// The rule snapshot's hash and what the dialog says about it.
function rulesSubject(body: unknown, taskId: string): RulesSubject | null {
	const effective = record(body);
	const rules = effective?.rules;
	const problems = effective?.problems;
	const sha256 = effective?.snapshotSha256;
	const ceiling = effective?.permissionCeiling;
	if (
		effective?.taskId !== taskId ||
		typeof sha256 !== "string" ||
		!SHA256_HEX.test(sha256) ||
		!isCeilingMode(ceiling) ||
		!Array.isArray(rules) ||
		!Array.isArray(problems)
	) {
		return null;
	}
	return {
		sha256,
		on: rules.filter((rule) => record(rule)?.on === true).length,
		ceiling,
		blocking: problems.filter((problem) => record(problem)?.severity === "blocking").length,
	};
}

// The project's LADO settings, from LadoProjectSettingsResponse.
function projectSettings(body: unknown, projectId: string): LadoPresenceProjectSettings | null {
	const settings = record(body);
	if (
		settings?.projectId !== projectId ||
		typeof settings.ladoMode !== "boolean" ||
		typeof settings.maxConcurrent !== "number"
	) {
		return null;
	}
	return settings as LadoPresenceProjectSettings;
}

// The name the dialog shows, from ProjectGetResponse; a project without one
// shows its id.
function projectName(body: unknown, projectId: string): string | null {
	const project = record(record(body)?.project);
	if (project?.id !== projectId || typeof project.name !== "string") return null;
	return oneLine(project.name) || projectId;
}

// The change's base version and current mode; a missing mode reads as null.
function ceilingSubject(rule: Record<string, unknown>): CeilingSubject | null {
	const { version } = rule;
	if (typeof version !== "number" || !Number.isSafeInteger(version) || version < 1) return null;
	const mode = record(rule.ceiling)?.mode;
	return { rule: rule as LadoPresenceRule, version, mode: isCeilingMode(mode) ? mode : null };
}

function isCeilingMode(value: unknown): value is LadoCeilingMode {
	return typeof value === "string" && (LADO_CEILING_MODES as readonly string[]).includes(value);
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
