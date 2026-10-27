// LADO fork: plan approval, permission ceiling and protected path changes,
// turning LADO mode off, changing a project's check commands, adopting,
// restoring and changing local setup, and approving a run's integration,
// through the human at the desktop app
// (docs/LJY-PRD/adr/0002-human-presence-approval-channel.md).
// Any local process can call the daemon's loopback API, so these must come
// from here. The renderer names a plan revision, the ceiling and a mode or the
// protected paths, a project, setup entries, an adoption, a setup change or a
// run; main reads the subject from the daemon itself, so the renderer cannot
// misstate it, shows a native confirmation naming it, and on confirm signs
// and sends the request.
// An approval also signs the hash of the rules that apply to the task right
// now. The key is derived in memory from the per-launch desktop token and
// never leaves main.
// backend/internal/service/ladopresence verifies the proof, and both sides
// test the same fixed vector.
import { createHash, createHmac, randomBytes as cryptoRandomBytes } from "node:crypto";
import type { BaseWindow, Dialog, IpcMain, IpcMainInvokeEvent, MessageBoxOptions, WebContents } from "electron";
import {
	LADO_CEILING_MODES,
	LADO_PRESENCE_ADOPT_SETUP_CHANNEL,
	LADO_PRESENCE_APPROVE_INTEGRATION_CHANNEL,
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	LADO_PRESENCE_CHANGE_PROJECT_CHECKS_CHANNEL,
	LADO_PRESENCE_CHANGE_RULE_CHANNEL,
	LADO_PRESENCE_CHANGE_SETUP_CHANNEL,
	LADO_PRESENCE_RESTORE_SETUP_CHANNEL,
	LADO_PRESENCE_SET_LADO_MODE_CHANNEL,
	type LadoCeilingMode,
	type LadoPresenceAdoptSetupResult,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
	type LadoPresenceCheckCommand,
	type LadoPresenceChecksResult,
	type LadoPresenceFailure,
	type LadoPresenceIntegration,
	type LadoPresenceIntegrationResult,
	type LadoPresenceModeResult,
	type LadoPresenceProjectSettings,
	type LadoPresenceRestoreSetupResult,
	type LadoPresenceRule,
	type LadoPresenceRuleChangeResult,
	type LadoPresenceSetupAdoption,
	type LadoPresenceSetupChange,
	type LadoPresenceSetupChangeResult,
	type LadoPresenceSetupEntry,
	type LadoPresenceSetupRestore,
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
// An adoption's preview scans the tools' folders and every project, and the
// adoption and the restore copy and remove files: they get longer.
const SETUP_TIMEOUT_MS = 120_000;
// An integration's dry run and approval fetch the base branch (at most 30 s)
// and read the remote branch rules.
const INTEGRATION_TIMEOUT_MS = 60_000;
const REVISION_ID = /^[A-Za-z0-9_-]{1,128}$/;
// Setup items, adoptions and tasks have uuid-like ids, as plan revisions do.
const SETUP_ID = REVISION_ID;
// AO's project id alphabet (backend/internal/service/project), within a
// folder name's length; AO also refuses "..".
const PROJECT_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$/;
// AO's session ids take the project's prefix and the same alphabet.
const SESSION_ID = PROJECT_ID;
const SHA256_HEX = /^[0-9a-f]{64}$/;
// The rules whose change needs the human (docs/LJY-PRD/HARNESS.md §14).
const CEILING_RULE_ID = "agent.permission.ceiling";
const PROTECTED_PATHS_RULE_ID = "paths.protected";
// The daemon's limits (backend/internal/domain): globs of the protected
// paths, and the check commands with their names and commands in characters.
const MAX_PROTECTED_PATHS = 50;
const MAX_CHECK_COMMANDS = 10;
const MAX_CHECK_NAME = 40;
const MAX_CHECK_COMMAND = 2000;
const CHECK_NAME = /^[A-Za-z0-9][A-Za-z0-9 ._-]*$/;
// Line breaks or direction overrides in an issue title or a project name
// could fake the dialog's other lines.
const UNSAFE_TEXT = /[\u0000-\u001f\u007f-\u009f\u061c\u200e\u200f\u2028\u2029\u202a-\u202e\u2066-\u2069]+/g;
// One such character, for refusing a value outright.
const UNSAFE_CHAR = new RegExp(UNSAFE_TEXT.source);
const SETUP_ADOPTIONS_PATH = "/api/v1/lado/setup/adoptions";
const SETUP_ITEMS_PATH = "/api/v1/lado/setup/items";
const SETUP_BINDINGS_PATH = "/api/v1/lado/setup/bindings";
const SETUP_ITEM_PATH = /^\/api\/v1\/lado\/setup\/items\/([A-Za-z0-9_-]{1,128})$/;
const SETUP_DRIFT_PATH = /^\/api\/v1\/lado\/setup\/items\/([A-Za-z0-9_-]{1,128})\/drift$/;
// Far above the entries of a large setup; the daemon reads a 1 MiB request.
const MAX_SETUP_ENTRIES = 500;
const MAX_SETUP_NAME = 1024;
// The daemon reads a setup change of at most 8 MiB.
const MAX_SETUP_CHANGE_BYTES = 8 << 20;
// A command, or a setting left in place, that names a moved file adoption
// cannot rewrite: the daemon refuses the adoption, so main does not ask.
const SETUP_UNMOVABLE: Partial<Record<string, string>> = {
	LADO_SETUP_HOOK_PATH_MOVED:
		"A command names a file that would move, and LADO cannot rewrite it. Move the setting that holds it too, or keep the file in place.",
	LADO_SETUP_PATH_USED_IN_PLACE: "A setting left in place names a file that would move. Move that setting too, or keep the file in place.",
};
// The daemon answered, but not as main reads it; a request it sent can still
// have been made.
const ANSWER_UNREADABLE = "LADO_PRESENCE_ANSWER_UNREADABLE";
// How much of a change's content its confirmation quotes, in characters.
const MAX_CONTENT_EXCERPT = 300;
// How many paths, commits or entries an integration's confirmation names for
// each thing to confirm, and how much of each.
const MAX_EXAMPLES = 3;
const MAX_EXAMPLE_CHARS = 120;
// A project's or task's binding: it follows the wider scope, or forces the item.
const SCOPED_BINDING_STATES: readonly unknown[] = ["inherit", "force_on", "force_off"];
const SETUP_TOOLS = ["claude-code", "codex"] as const;
const SETUP_KINDS = [
	"instructions",
	"setting",
	"skill",
	"agent",
	"command",
	"output_style",
	"hook_script",
	"mcp_server",
] as const;

type Failure = LadoPresenceFailure;
// The dialogs' words, and the setup kinds as the renderer names them.
type PresenceMessageKey = Extract<TrayMessageKey, `lado.presence.${string}` | `lado.setup.kind.${string}`>;
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
type SetupTool = (typeof SETUP_TOOLS)[number];
type SetupKind = (typeof SETUP_KINDS)[number];
// What the adoption's preview moves and what holds it back, from
// LadoSetupAdoptionPreviewResponse.
type AdoptionSubject = {
	sha256: string;
	moved: Record<SetupTool, number>;
	kept: number;
	blocking: number;
	// The first command or setting that names a moved file adoption cannot
	// rewrite, with its details.
	unmovable: { code: string; details: Record<string, unknown> } | null;
	os: string;
	unverified: unknown[];
	sessions: unknown[];
	processes: unknown[];
};
// The adoption a restore puts back and what holds the restore back now, from
// LadoSetupAdoptionDetailResponse.
type RestoreSubject = {
	items: number;
	createdAt: string;
	restored: boolean;
	backupReadable: boolean;
	sessions: unknown[];
	processes: unknown[];
};
// A setup change main confirms: its route, the body it sends, and the item
// or binding it names.
type SetupChange = {
	op: "create" | "edit" | "delete" | "binding" | "drift_keep";
	method: "POST" | "PATCH" | "DELETE" | "PUT";
	path: string;
	body: Record<string, unknown>;
	// The item an edit, a delete, a binding or a drift keep changes.
	itemId?: string;
};
// What the change's preview names, from LadoSetupChangeResponse: the item,
// the daemon's own form of the content it sets as one line, and the blocking
// conflicts it adds.
type ChangeSubject = {
	sha256: string;
	needsPresence: boolean;
	tool: SetupTool;
	kind: SetupKind;
	name: string;
	content: string;
	blocking: number;
};

// The protected paths as the daemon has them, from LadoRuleResponse.
type PathsSubject = { rule: LadoPresenceRule; version: number; globs: string[] };
// The run an integration approval names, from LadoExecutionResponse; pr is
// null when it has no open pull request.
type IntegrationRun = {
	title: string;
	pr: { number: number; url: string; headSha: string; branch: string } | null;
	approvedHead: string;
};
type VerifyItem = { kind: string; outcome: string; code: string; details: Record<string, unknown> };
// What the integration's dry run verified and the approval signs, from
// LadoIntegrationApproveResponse; reasons are the codes that hold it back.
type VerifiedSubject = {
	prUrl: string;
	headSha: string;
	sha256: string;
	outcome: "pass" | "wait" | "fail" | "confirm";
	confirm: string[];
	reasons: string[];
	items: VerifyItem[];
};

const MODE_KEYS: Record<LadoCeilingMode, PresenceMessageKey> = {
	"accept-edits": "lado.presence.mode.acceptEdits",
	auto: "lado.presence.mode.auto",
	"bypass-permissions": "lado.presence.mode.bypassPermissions",
};

// Product names stay as they are in every language.
const SETUP_TOOL_NAMES: Record<SetupTool, string> = { "claude-code": "Claude Code", codex: "Codex" };

const SETUP_KIND_KEYS: Record<SetupKind, PresenceMessageKey> = {
	instructions: "lado.setup.kind.instructions",
	setting: "lado.setup.kind.setting",
	skill: "lado.setup.kind.skill",
	agent: "lado.setup.kind.agent",
	command: "lado.setup.kind.command",
	output_style: "lado.setup.kind.outputStyle",
	hook_script: "lado.setup.kind.hookScript",
	mcp_server: "lado.setup.kind.mcpServer",
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
	const handle = (
		channel: string,
		run: (deps: LadoPresenceDeps, sender: WebContents, input: unknown) => Promise<unknown>,
		failed: string,
	) =>
		deps.ipcMain.handle(channel, async (event: IpcMainInvokeEvent, input: unknown) => {
			try {
				return await run(deps, event.sender, input);
			} catch (error) {
				// Nothing throws across IPC: the renderer always gets a result to show.
				return failure(undefined, error instanceof Error && error.message ? error.message : failed);
			}
		});
	handle(LADO_PRESENCE_APPROVE_PLAN_CHANNEL, approvePlan, "The approval failed.");
	handle(LADO_PRESENCE_CHANGE_RULE_CHANNEL, changeRule, "The rule change failed.");
	handle(LADO_PRESENCE_SET_LADO_MODE_CHANNEL, setLadoMode, "Turning LADO mode off failed.");
	handle(LADO_PRESENCE_ADOPT_SETUP_CHANNEL, adoptSetup, "The adoption failed.");
	handle(LADO_PRESENCE_RESTORE_SETUP_CHANNEL, restoreSetup, "The restore failed.");
	handle(LADO_PRESENCE_CHANGE_SETUP_CHANNEL, changeSetup, "The setup change failed.");
	handle(LADO_PRESENCE_APPROVE_INTEGRATION_CHANNEL, approveIntegration, "The integration approval failed.");
	handle(LADO_PRESENCE_CHANGE_PROJECT_CHECKS_CHANNEL, changeProjectChecks, "The check commands change failed.");
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
	if (!plan) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this plan could not be read.");
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
	if (!rules) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this task's rules could not be read.");
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
	if (!approval) return failure(ANSWER_UNREADABLE, "The approval was sent, but the AO daemon's answer could not be read.");
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
	// The rule id and the mode or the globs are all the renderer gives;
	// anything else it sends is ignored.
	const request = record(input);
	const ruleId = request?.ruleId;
	if (ruleId === PROTECTED_PATHS_RULE_ID) return changeProtectedPaths(deps, record(request?.paths)?.globs);
	const mode = record(request?.ceiling)?.mode;
	if (ruleId !== CEILING_RULE_ID) {
		return failure("LADO_RULE_CEILING_INVALID", "Only the permission ceiling's mode and the protected paths change here.");
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
	if (!ceiling) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this rule could not be read.");
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
	if (!changed) return failure(ANSWER_UNREADABLE, "The change was sent, but the AO daemon's answer could not be read.");
	return { status: "changed", rule: changed as LadoPresenceRule };
}

// The protected paths keep workers away from CI, agent instructions and
// LADO's own files unless the approved plan names them, so only the human may
// change them. Main reads the rule, names the globs added and removed, then
// signs the change.
async function changeProtectedPaths(deps: LadoPresenceDeps, value: unknown): Promise<LadoPresenceRuleChangeResult> {
	const globs = protectedGlobs(value);
	if (!globs.ok) {
		return failure("LADO_PROTECTED_PATHS_INVALID", "Give 1 to 50 protected paths, each a relative path or pattern.", globs.details);
	}
	const rulePath = `/api/v1/lado/rules/${encodeURIComponent(PROTECTED_PATHS_RULE_ID)}`;
	const read = await callDaemon(deps, rulePath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const rule = record(record(read.body)?.rule);
	if (rule?.id !== PROTECTED_PATHS_RULE_ID || rule.protected !== true) {
		return failure("LADO_PROTECTED_PATHS_INVALID", "The AO daemon does not have this rule as the protected paths.", {
			rule: PROTECTED_PATHS_RULE_ID,
		});
	}
	const current = pathsSubject(rule);
	if (!current) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this rule could not be read.");
	// Nothing would change, so there is nothing to confirm.
	if (current.globs.join("\0") === globs.globs.join("\0")) return { status: "changed", rule: current.rule };
	const added = globs.globs.filter((glob) => !current.globs.includes(glob));
	const removed = current.globs.filter((glob) => !globs.globs.includes(glob));
	const t = await translator(deps);
	const detail = [
		[
			...(added.length > 0 ? [t("lado.presence.paths.added", { globs: excerpt(added.join(", ")) })] : []),
			...(removed.length > 0 ? [t("lado.presence.paths.removed", { globs: excerpt(removed.join(", ")) })] : []),
		].join("\n"),
		t("lado.presence.paths.detail"),
	]
		.filter(Boolean)
		.join("\n\n");
	if (!(await confirm(deps, t, t("lado.presence.paths.title"), detail, t("lado.presence.change")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes.
	const body = JSON.stringify({ baseVersion: current.version, paths: { globs: globs.globs } });
	const sent = await callDaemon(deps, rulePath, signed(deps, "PATCH", rulePath, body));
	if (!sent.ok) return sent.failure;
	const changed = record(record(sent.body)?.rule);
	if (!changed) return failure(ANSWER_UNREADABLE, "The change was sent, but the AO daemon's answer could not be read.");
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
	if (!settings) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this project's LADO settings could not be read.");
	// Nothing would change, so there is nothing to confirm.
	if (!settings.ladoMode) return { status: "changed", settings };
	const projectPath = `/api/v1/projects/${encodeURIComponent(projectId)}`;
	const project = await callDaemon(deps, projectPath, { method: "GET", headers: { accept: "application/json" } });
	if (!project.ok) return project.failure;
	const name = projectName(project.body, projectId);
	if (name === null) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this project could not be read.");
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
	if (!changed) return failure(ANSWER_UNREADABLE, "LADO mode was turned off, but the AO daemon's answer could not be read.");
	return { status: "changed", settings: changed };
}

// Adoption backs up the developer's own setup, then removes it from the
// tools' folders, so only the human may start it. Main previews the entries
// itself, does not ask about an adoption the daemon would refuse, names what
// moves and what stays, then signs the plan it previewed.
async function adoptSetup(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceAdoptSetupResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can adopt local setup.");
	}
	// The entries are all the renderer gives; anything else it sends is ignored.
	const entries = setupEntries(record(input)?.entries);
	if (!entries) {
		return failure(
			"LADO_SETUP_ADOPTION_INVALID",
			`Choose 1 to ${MAX_SETUP_ENTRIES} entries to adopt, each with its tool, kind, name and action.`,
		);
	}
	const preview = await callDaemon(
		deps,
		SETUP_ADOPTIONS_PATH,
		jsonRequest("POST", JSON.stringify({ entries, dryRun: true })),
		SETUP_TIMEOUT_MS,
	);
	if (!preview.ok) return preview.failure;
	const plan = adoptionSubject(preview.body, entries);
	if (!plan) return failure(ANSWER_UNREADABLE, "The AO daemon's preview of this adoption could not be read.");
	// The daemon refuses these as well, in this order; asking the human first
	// would only waste a confirmation. Other blocking conflicts do not hold the
	// adoption back: the same setup left in place has them too, and once it is
	// adopted the app can turn it off or fix it.
	if (plan.moved["claude-code"] + plan.moved.codex === 0) {
		return failure("LADO_SETUP_ADOPTION_INVALID", "Choose at least one entry to move.");
	}
	if (plan.unmovable) {
		return failure(plan.unmovable.code, SETUP_UNMOVABLE[plan.unmovable.code] ?? "", plan.unmovable.details);
	}
	if (plan.unverified.length > 0) {
		return failure(
			"LADO_SETUP_OS_UNVERIFIED",
			"Adoption is not available on this OS yet: LADO has not checked that sessions get the adopted setup here.",
			{ os: plan.os, tools: plan.unverified },
		);
	}
	if (plan.sessions.length > 0 || plan.processes.length > 0) {
		return failure(
			"LADO_SETUP_TOOL_RUNNING",
			"Close the Claude Code and Codex sessions and programs listed, then adopt again.",
			{ sessions: plan.sessions, processes: plan.processes },
		);
	}
	const t = await translator(deps);
	const detail = [
		t("lado.presence.setupAdoptDetail", {
			claude: plan.moved["claude-code"],
			codex: plan.moved.codex,
			kept: plan.kept,
			sha: plan.sha256.slice(0, 12),
		}),
		...(plan.blocking > 0 ? [t("lado.presence.setupAdoptBlocking", { blocking: plan.blocking })] : []),
	].join("\n\n");
	if (!(await confirm(deps, t, t("lado.presence.setupAdoptTitle"), detail, t("lado.presence.setupAdopt")))) {
		return { status: "cancelled" };
	}

	// The daemon previews the same entries again and adopts them only when
	// they still hash to this plan; it checks the proof against these bytes.
	const body = JSON.stringify({ entries, planSha256: plan.sha256 });
	const sent = await callDaemon(deps, SETUP_ADOPTIONS_PATH, signed(deps, "POST", SETUP_ADOPTIONS_PATH, body), SETUP_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const adoption = record(sent.body);
	if (adoption?.applied !== true) return failure(ANSWER_UNREADABLE, "The adoption was sent, but the AO daemon's answer could not be read.");
	return { status: "adopted", adoption: adoption as LadoPresenceSetupAdoption };
}

// Restore writes the adoption's originals back into the tools' folders, so
// only the human may start it. Main reads the adoption, does not ask while
// the tools it puts back are running, and names when the adoption ran and
// how many items go back.
async function restoreSetup(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceRestoreSetupResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can restore local setup.");
	}
	// The adoption id is all the renderer gives; anything else it sends is ignored.
	const adoptionId = record(input)?.adoptionId;
	if (typeof adoptionId !== "string" || !SETUP_ID.test(adoptionId)) {
		return failure("LADO_SETUP_ADOPTION_ID_INVALID", "Invalid adoption id.");
	}
	const adoptionPath = `${SETUP_ADOPTIONS_PATH}/${encodeURIComponent(adoptionId)}`;
	const read = await callDaemon(deps, adoptionPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const adoption = restoreSubject(read.body, adoptionId);
	if (!adoption) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this adoption could not be read.");
	// The daemon refuses these as well; asking the human first would only
	// waste a confirmation.
	if (adoption.restored) {
		return failure("LADO_SETUP_ALREADY_RESTORED", "This adoption was restored before.", { adoption: adoptionId });
	}
	if (!adoption.backupReadable) {
		return failure(
			"LADO_SETUP_RESTORE_FAILED",
			"LADO cannot read this adoption's backup as it was written, so it restores nothing.",
			{ adoption: adoptionId, stage: "manifest" },
		);
	}
	if (adoption.sessions.length > 0 || adoption.processes.length > 0) {
		return failure(
			"LADO_SETUP_TOOL_RUNNING",
			"Close the Claude Code and Codex sessions and programs listed, then restore again.",
			{ sessions: adoption.sessions, processes: adoption.processes },
		);
	}
	const locale = await deps.locale();
	const t = translate(locale);
	const detail = t("lado.presence.setupRestoreDetail", { when: formatTime(locale, adoption.createdAt), count: adoption.items });
	if (!(await confirm(deps, t, t("lado.presence.setupRestoreTitle"), detail, t("lado.presence.setupRestore")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes: none.
	const restorePath = `${adoptionPath}/restore`;
	const sent = await callDaemon(deps, restorePath, signed(deps, "POST", restorePath, ""), SETUP_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const restore = record(sent.body);
	if (restore?.adoptionId !== adoptionId || restore.applied !== true) {
		return failure(ANSWER_UNREADABLE, "The restore was sent, but the AO daemon's answer could not be read.");
	}
	return { status: "restored", restore: restore as LadoPresenceSetupRestore };
}

// Every change to an item that can run commands or widen access (creating,
// editing or deleting it, setting where it applies, keeping its drift) needs
// the human. Main checks the route and the body, previews the change itself,
// names the item, what the change does, the content it sets and the blocking
// conflicts it adds, then signs exactly the request the renderer named, and
// only when its own preview says the change needs that.
async function changeSetup(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceSetupChangeResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can change local setup.");
	}
	// The route, the method and the body's fields are all the renderer gives.
	const change = setupChange(input);
	if (!change) {
		return failure(
			"LADO_SETUP_CHANGE_INVALID",
			"Only creating, editing or deleting a setup item, setting where it applies and keeping its drift are confirmed here.",
		);
	}
	// A delete sends no body, and the daemon checks the proof against none.
	const body = change.op === "delete" ? "" : JSON.stringify(change.body);
	if (new TextEncoder().encode(body).length > MAX_SETUP_CHANGE_BYTES) {
		return failure("LADO_SETUP_CONTENT_INVALID", "This content is too large for a setup item.", {
			reason: "too_large",
			limit: MAX_SETUP_CHANGE_BYTES,
		});
	}
	const preview =
		change.op === "delete"
			? await callDaemon(deps, `${change.path}?dryRun=true`, { method: "DELETE", headers: { accept: "application/json" } }, SETUP_TIMEOUT_MS)
			: await callDaemon(deps, change.path, jsonRequest(change.method, JSON.stringify({ ...change.body, dryRun: true })), SETUP_TIMEOUT_MS);
	if (!preview.ok) return preview.failure;
	const subject = changeSubject(preview.body, change);
	if (!subject) return failure(ANSWER_UNREADABLE, "The AO daemon's preview of this change could not be read.");
	let init = jsonRequest(change.method, body);
	// The daemon makes a change that needs no confirmation without one, as it
	// would from the renderer; main signs only what the human confirmed.
	if (subject.needsPresence) {
		// Inheriting names what the wider scope turns the item to.
		let inheritsOn = false;
		if (change.op === "binding" && change.body.state === "inherit") {
			const inherited = await inheritedState(deps, change);
			if (!inherited.ok) return inherited.failure;
			inheritsOn = inherited.on;
		}
		const t = await translator(deps);
		const detail = [
			t("lado.presence.setupChangeDetail", {
				item: `${SETUP_TOOL_NAMES[subject.tool]} · ${t(SETUP_KIND_KEYS[subject.kind])} · ${subject.name}`,
				change: changeLine(t, change, inheritsOn),
				sha: subject.sha256.slice(0, 12),
			}),
			...(subject.content ? [t("lado.presence.setupChangeContent", { content: subject.content })] : []),
			...(subject.blocking > 0 ? [t("lado.presence.setupChangeBlocking", { blocking: subject.blocking })] : []),
		].join("\n\n");
		if (!(await confirm(deps, t, t("lado.presence.setupChangeTitle"), detail, t("lado.presence.change")))) {
			return { status: "cancelled" };
		}
		init = signed(deps, change.method, change.path, body);
	}

	const sent = await callDaemon(deps, change.path, init, SETUP_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const changed = record(sent.body);
	if (changed?.applied !== true) return failure(ANSWER_UNREADABLE, "The change was sent, but the AO daemon's answer could not be read.");
	return { status: "changed", change: changed as LadoPresenceSetupChange };
}

// Approving a head's integration lets its pull request merge
// (docs/LJY-PRD/TDD.md §7.1), so only the human may do it. Main reads the run
// and sends the dry run itself, does not ask while LADO still verifies the
// head or a check failed, names the pull request, the head and what needs
// confirming, then signs exactly what the dry run verified.
async function approveIntegration(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceIntegrationResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can approve an integration.");
	}
	// The session id is all the renderer gives; anything else it sends is ignored.
	const sessionId = record(input)?.sessionId;
	if (typeof sessionId !== "string" || !SESSION_ID.test(sessionId) || sessionId.includes("..")) {
		return failure("LADO_SESSION_ID_INVALID", "Invalid session id.");
	}
	const executionPath = `/api/v1/lado/executions/${encodeURIComponent(sessionId)}`;
	const read = await callDaemon(deps, executionPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const run = integrationRun(read.body, sessionId);
	if (!run) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this run could not be read.");
	// The daemon refuses these as well; asking the human first would only
	// waste a confirmation.
	if (!run.pr) return failure("LADO_EXECUTION_NO_PR", "This run has no open pull request to integrate.", { sessionId });
	if (!run.pr.headSha) {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO has not verified this pull request's head yet.", { reasons: ["verifying"] });
	}
	if (run.approvedHead === run.pr.headSha) {
		return failure("LADO_INTEGRATION_ALREADY_APPROVED", "The integration of this head is approved already.");
	}
	// The dry run reads the live head, the remote rules and the base branch
	// again and verifies the head as the approval would.
	const approvePath = `${executionPath}/integration-approve`;
	const preview = await callDaemon(
		deps,
		approvePath,
		jsonRequest("POST", JSON.stringify({ prUrl: run.pr.url, headSha: run.pr.headSha, dryRun: true })),
		INTEGRATION_TIMEOUT_MS,
	);
	if (!preview.ok) return preview.failure;
	const verified = verifiedSubject(preview.body, run.pr);
	if (!verified) return failure(ANSWER_UNREADABLE, "The AO daemon's dry run of this approval could not be read.");
	if (verified.outcome === "wait" || verified.outcome === "fail") {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO is still verifying this head, or a check on it failed.", {
			reasons: verified.reasons,
		});
	}
	const t = await translator(deps);
	const title = t("lado.presence.integration.title", { number: run.pr.number, branch: run.pr.branch || "—" });
	if (!(await confirm(deps, t, title, integrationDetail(t, run.title, verified), t("lado.presence.integration.approve")))) {
		return { status: "cancelled" };
	}

	// The daemon reads the head and verifies it again, and approves only when
	// the head, the verification's hash and what needs confirming still match
	// these bytes; it checks the proof against them.
	const body = JSON.stringify({
		prUrl: verified.prUrl,
		headSha: verified.headSha,
		verificationSha256: verified.sha256,
		acknowledge: verified.confirm,
	});
	const sent = await callDaemon(deps, approvePath, signed(deps, "POST", approvePath, body), INTEGRATION_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const integration = record(sent.body);
	if (!record(integration?.approval)) {
		return failure(ANSWER_UNREADABLE, "The approval was sent, but the AO daemon's answer could not be read.");
	}
	return { status: "approved", integration: integration as LadoPresenceIntegration };
}

// A project's check commands run in every run's worktree outside any agent
// sandbox, so only the human may change them. Main reads the project's
// settings, names the commands added, changed and removed, then signs a
// settings change that holds only the commands; the time limit needs no
// confirmation and the renderer sends it itself.
async function changeProjectChecks(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceChecksResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can change the check commands.");
	}
	// The project id and the commands are all the renderer gives; anything else it sends is ignored.
	const request = record(input);
	const projectId = request?.projectId;
	if (typeof projectId !== "string" || !PROJECT_ID.test(projectId) || projectId.includes("..")) {
		return failure("LADO_PROJECT_ID_INVALID", "Invalid project id.");
	}
	const commands = checkCommands(request?.commands);
	if (!commands.ok) {
		return failure(
			"LADO_CHECK_COMMANDS_INVALID",
			"Give at most 10 check commands, each with a unique name and a command.",
			commands.details,
		);
	}
	const settingsPath = `/api/v1/lado/projects/${encodeURIComponent(projectId)}/settings`;
	const read = await callDaemon(deps, settingsPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const settings = projectSettings(read.body, projectId);
	const current = settings ? checkCommands(settings.checkCommands) : null;
	if (!settings || !current?.ok) {
		return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this project's LADO settings could not be read.");
	}
	// Nothing would change, so there is nothing to confirm.
	if (JSON.stringify(current.commands) === JSON.stringify(commands.commands)) return { status: "changed", settings };
	const t = await translator(deps);
	const detail = [
		checkChangeLines(t, current.commands, commands.commands).join("\n"),
		t("lado.presence.checks.detail", { id: projectId }),
	].join("\n\n");
	if (!(await confirm(deps, t, t("lado.presence.checks.title"), detail, t("lado.presence.change")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes.
	const body = JSON.stringify({ checkCommands: commands.commands });
	const sent = await callDaemon(deps, settingsPath, signed(deps, "PUT", settingsPath, body));
	if (!sent.ok) return sent.failure;
	const changed = projectSettings(sent.body, projectId);
	if (!changed) return failure(ANSWER_UNREADABLE, "The check commands were changed, but the AO daemon's answer could not be read.");
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

// A request without a proof: a preview, or a change that needs none.
function jsonRequest(method: string, body: string): RequestInit {
	return { method, headers: { "content-type": "application/json" }, body };
}

// Main's catalog in the current language, read for each confirmation.
async function translator(deps: LadoPresenceDeps): Promise<Translate> {
	return translate(await deps.locale());
}

function translate(locale: AppLocale): Translate {
	const catalog = trayCatalogs[locale] ?? trayCatalogs.en;
	return (key, vars) => format(catalog[key] ?? trayCatalogs.en[key], vars);
}

// A time as the app's language writes it, in this computer's time zone.
function formatTime(locale: AppLocale, iso: string): string {
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return oneLine(iso);
	try {
		return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
	} catch {
		return date.toISOString();
	}
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
async function callDaemon(
	deps: LadoPresenceDeps,
	path: string,
	init: RequestInit,
	timeoutMs = REQUEST_TIMEOUT_MS,
): Promise<DaemonAnswer> {
	const base = deps.daemonBaseUrl();
	if (!base) return { ok: false, failure: failure("SERVICE_UNAVAILABLE", "The AO daemon is not ready.") };
	let response: Response;
	try {
		response = await deps.fetch(`${base}${path}`, { ...init, signal: AbortSignal.timeout(timeoutMs) });
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

// The entries to adopt, each with exactly its tool, kind, name and action;
// null when any is not one the daemon takes.
function setupEntries(value: unknown): LadoPresenceSetupEntry[] | null {
	if (!Array.isArray(value) || value.length === 0 || value.length > MAX_SETUP_ENTRIES) return null;
	const entries: LadoPresenceSetupEntry[] = [];
	for (const item of value) {
		const entry = setupEntry(item);
		if (!entry) return null;
		entries.push(entry);
	}
	return entries;
}

function setupEntry(value: unknown): LadoPresenceSetupEntry | null {
	const entry = record(value);
	const tool = entry?.tool;
	const kind = entry?.kind;
	const name = entry?.name;
	const action = entry?.action;
	if (!isSetupTool(tool) || !isSetupKind(kind) || !isSetupName(name) || (action !== "move" && action !== "keep")) return null;
	return { tool, kind, name, action };
}

function setupEntryKey(entry: LadoPresenceSetupEntry): string {
	return `${entry.tool}\0${entry.kind}\0${entry.name}`;
}

// The adoption's preview: it must name exactly the entries asked for, each
// with its action, and hash them.
function adoptionSubject(body: unknown, entries: readonly LadoPresenceSetupEntry[]): AdoptionSubject | null {
	const preview = record(record(body)?.preview);
	const preconditions = record(preview?.preconditions);
	const sha256 = preview?.planSha256;
	const previewed = preview?.entries;
	const conflicts = preview?.conflicts;
	if (
		!preconditions ||
		typeof sha256 !== "string" ||
		!SHA256_HEX.test(sha256) ||
		!Array.isArray(previewed) ||
		previewed.length !== entries.length ||
		!Array.isArray(conflicts) ||
		typeof preconditions.os !== "string" ||
		!Array.isArray(preconditions.unverified) ||
		!Array.isArray(preconditions.sessions) ||
		!Array.isArray(preconditions.processes)
	) {
		return null;
	}
	const asked = new Map(entries.map((entry) => [setupEntryKey(entry), entry.action]));
	const seen = new Set<string>();
	const moved: Record<SetupTool, number> = { "claude-code": 0, codex: 0 };
	let kept = 0;
	for (const value of previewed) {
		const entry = setupEntry(value);
		const key = entry ? setupEntryKey(entry) : "";
		if (!entry || seen.has(key) || asked.get(key) !== entry.action) return null;
		seen.add(key);
		if (entry.action === "move") moved[entry.tool] += 1;
		else kept += 1;
	}
	let blocking = 0;
	let unmovable: AdoptionSubject["unmovable"] = null;
	for (const value of conflicts) {
		const conflict = record(value);
		if (typeof conflict?.code !== "string" || typeof conflict.severity !== "string") return null;
		if (SETUP_UNMOVABLE[conflict.code]) unmovable ??= { code: conflict.code, details: record(conflict.details) ?? {} };
		else if (conflict.severity === "blocking") blocking += 1;
	}
	return {
		sha256,
		moved,
		kept,
		blocking,
		unmovable,
		os: preconditions.os,
		unverified: preconditions.unverified,
		sessions: preconditions.sessions,
		processes: preconditions.processes,
	};
}

// The adoption a restore names, from LadoSetupAdoptionDetailResponse. An
// adoption interrupted before it recorded its items puts back the originals
// its backup moved. The daemon leaves out the preconditions once nothing can
// be restored.
function restoreSubject(body: unknown, adoptionId: string): RestoreSubject | null {
	const detail = record(body);
	const adoption = record(detail?.adoption);
	const items = detail?.items;
	const backup = detail?.backup;
	const restoredAt = adoption?.restoredAt;
	const createdAt = adoption?.createdAt;
	const preconditions = detail?.preconditions === undefined ? {} : record(detail.preconditions);
	const sessions = preconditions?.sessions ?? [];
	const processes = preconditions?.processes ?? [];
	if (
		adoption?.id !== adoptionId ||
		typeof createdAt !== "string" ||
		!Array.isArray(items) ||
		!Array.isArray(backup) ||
		typeof detail?.backupReadable !== "boolean" ||
		(restoredAt !== undefined && restoredAt !== null && typeof restoredAt !== "string") ||
		!Array.isArray(sessions) ||
		!Array.isArray(processes)
	) {
		return null;
	}
	return {
		items: items.length || backup.filter((entry) => record(entry)?.action === "move").length,
		createdAt,
		restored: typeof restoredAt === "string" && restoredAt !== "",
		backupReadable: detail.backupReadable,
		sessions,
		processes,
	};
}

// The setup change the renderer names: one of the routes whose change can
// need the human, with exactly that route's fields. Reverting drift and
// accepting or revoking a warning never need the human, so they are not taken
// here.
function setupChange(input: unknown): SetupChange | null {
	const request = record(input);
	const body = record(request?.body);
	const method = request?.method;
	const path = request?.path;
	if (!body || typeof path !== "string") return null;
	if (method === "POST" && path === SETUP_ITEMS_PATH) {
		const fields = onlyFields(body, ["tool", "kind", "name", "content"]);
		if (!fields || !isSetupTool(fields.tool) || !isSetupKind(fields.kind) || !isSetupName(fields.name) || !isJSONValue(fields.content)) {
			return null;
		}
		return { op: "create", method, path, body: fields };
	}
	const edited = SETUP_ITEM_PATH.exec(path)?.[1];
	if (method === "PATCH" && edited) {
		const fields = onlyFields(body, ["baseVersion", "content"]);
		if (!fields || !isVersion(fields.baseVersion) || !isJSONValue(fields.content)) return null;
		return { op: "edit", method, path, body: fields, itemId: edited };
	}
	if (method === "DELETE" && edited) {
		if (!onlyFields(body, [])) return null;
		return { op: "delete", method, path, body: {}, itemId: edited };
	}
	if (method === "PUT" && path === SETUP_BINDINGS_PATH) {
		const fields = onlyFields(body, ["itemId", "scope", "scopeId", "state"]);
		if (!fields || typeof fields.itemId !== "string" || !SETUP_ID.test(fields.itemId) || !isBinding(fields)) return null;
		return { op: "binding", method, path, body: fields, itemId: fields.itemId };
	}
	const drifted = SETUP_DRIFT_PATH.exec(path)?.[1];
	if (method === "POST" && drifted) {
		const fields = onlyFields(body, ["action", "sha256"]);
		if (!fields || fields.action !== "keep" || typeof fields.sha256 !== "string" || !SHA256_HEX.test(fields.sha256)) return null;
		return { op: "drift_keep", method, path, body: fields, itemId: drifted };
	}
	return null;
}

// The body's fields in the given order; null when it has any other.
function onlyFields(body: Record<string, unknown>, names: readonly string[]): Record<string, unknown> | null {
	if (Object.keys(body).some((name) => !names.includes(name))) return null;
	const fields: Record<string, unknown> = {};
	for (const name of names) if (body[name] !== undefined) fields[name] = body[name];
	return fields;
}

// A binding the daemon takes: on or off at Global; inherit, force_on or
// force_off for a project's or task's id.
function isBinding(binding: Record<string, unknown>): boolean {
	const { scope, scopeId = "", state } = binding;
	if (typeof scopeId !== "string") return false;
	switch (scope) {
		case "global":
			return (state === "on" || state === "off") && scopeId === "";
		case "project":
			return SCOPED_BINDING_STATES.includes(state) && PROJECT_ID.test(scopeId) && !scopeId.includes("..");
		case "task":
			return SCOPED_BINDING_STATES.includes(state) && SETUP_ID.test(scopeId);
		default:
			return false;
	}
}

// Whether inheriting turns the item on, from LadoSetupItemListResponse: a
// project follows Global, and a task follows its project, which can follow
// Global in turn.
async function inheritedState(
	deps: LadoPresenceDeps,
	change: SetupChange,
): Promise<{ ok: true; on: boolean } | { ok: false; failure: Failure }> {
	const task = change.body.scope === "task";
	const path = `${SETUP_ITEMS_PATH}?${task ? "taskId" : "projectId"}=${encodeURIComponent(String(change.body.scopeId))}`;
	const read = await callDaemon(deps, path, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read;
	const items = record(read.body)?.items;
	const item = Array.isArray(items) ? items.map(record).find((value) => value?.itemId === change.itemId) : undefined;
	const global = item?.global;
	const project = item?.project;
	if ((global !== "on" && global !== "off") || (task && !SCOPED_BINDING_STATES.includes(project))) {
		return { ok: false, failure: failure(ANSWER_UNREADABLE, "The AO daemon's answer about where this item applies could not be read.") };
	}
	if (task && project !== "inherit") return { ok: true, on: project === "force_on" };
	return { ok: true, on: global === "on" };
}

// The preview of the change: about the item the request names, by the op the
// route makes, and hashed.
function changeSubject(body: unknown, change: SetupChange): ChangeSubject | null {
	const result = record(body);
	const item = record(result?.item);
	const sha256 = result?.changeSha256;
	const needsPresence = result?.needsPresence;
	const tool = item?.tool;
	const kind = item?.kind;
	const name = item?.name;
	const text = record(result?.after)?.text;
	const added = result?.conflictsAdded;
	if (
		!Array.isArray(added) ||
		result?.op !== change.op ||
		result.applied !== false ||
		typeof sha256 !== "string" ||
		!SHA256_HEX.test(sha256) ||
		typeof needsPresence !== "boolean" ||
		!isSetupTool(tool) ||
		!isSetupKind(kind) ||
		typeof name !== "string"
	) {
		return null;
	}
	const named =
		change.op === "create"
			? tool === change.body.tool && kind === change.body.kind && name === change.body.name
			: item?.id === change.itemId;
	if (!named) return null;
	return {
		sha256,
		needsPresence,
		tool,
		kind,
		name: oneLine(name),
		content: typeof text === "string" ? excerpt(text) : "",
		blocking: added.filter((conflict) => record(conflict)?.severity === "blocking").length,
	};
}

// The protected paths' globs and the change's base version, from the rule's
// LadoRuleResponse; a rule without paths has none yet.
function pathsSubject(rule: Record<string, unknown>): PathsSubject | null {
	const { version } = rule;
	if (!isVersion(version)) return null;
	const globs = rule.paths === undefined ? [] : record(rule.paths)?.globs;
	if (!isStringList(globs)) return null;
	return { rule: rule as LadoPresenceRule, version, globs };
}

// The globs as the daemon keeps them: trimmed, 1 to 50 of them. A glob with
// a control or direction character could fake the dialog's other lines, and
// the daemon refuses it as no path anyway. The details are the daemon's.
function protectedGlobs(value: unknown): { ok: true; globs: string[] } | { ok: false; details: { index: number; reason: string } } {
	if (!Array.isArray(value) || value.length === 0) return { ok: false, details: { index: 0, reason: "required" } };
	if (value.length > MAX_PROTECTED_PATHS) return { ok: false, details: { index: MAX_PROTECTED_PATHS, reason: "too_many" } };
	const globs: string[] = [];
	for (const [index, glob] of value.entries()) {
		if (typeof glob !== "string" || glob.trim() === "") return { ok: false, details: { index, reason: "required" } };
		if (UNSAFE_CHAR.test(glob)) return { ok: false, details: { index, reason: "invalid" } };
		globs.push(glob.trim());
	}
	return { ok: true, globs };
}

// The commands as the daemon takes them (backend/internal/domain
// ValidateLadoCheckCommands), each with exactly its name and command, or the
// first problem with the details the daemon would send.
function checkCommands(
	value: unknown,
): { ok: true; commands: LadoPresenceCheckCommand[] } | { ok: false; details: { index: number; field: string; reason: string } } {
	if (!Array.isArray(value)) return { ok: false, details: { index: 0, field: "", reason: "invalid" } };
	if (value.length > MAX_CHECK_COMMANDS) return { ok: false, details: { index: MAX_CHECK_COMMANDS, field: "", reason: "too_many" } };
	const commands: LadoPresenceCheckCommand[] = [];
	for (const [index, item] of value.entries()) {
		const entry = record(item);
		const { name, command } = entry ?? {};
		const problem = (field: string, reason: string) => ({ ok: false as const, details: { index, field, reason } });
		if (typeof name !== "string" || name === "") return problem("name", "required");
		if (Array.from(name).length > MAX_CHECK_NAME) return problem("name", "too_long");
		if (!CHECK_NAME.test(name)) return problem("name", "invalid");
		if (commands.some((earlier) => earlier.name.toLowerCase() === name.toLowerCase())) return problem("name", "duplicate");
		if (typeof command !== "string" || command.trim() === "") return problem("command", "required");
		if (Array.from(command).length > MAX_CHECK_COMMAND) return problem("command", "too_long");
		if (command.includes("\0")) return problem("command", "invalid");
		commands.push({ name, command });
	}
	return { ok: true, commands };
}

// What a change of the check commands does: one line for each command added
// or changed, with the command it runs, one for each removed, and the order
// they run in when that changes.
function checkChangeLines(
	t: Translate,
	from: readonly LadoPresenceCheckCommand[],
	to: readonly LadoPresenceCheckCommand[],
): string[] {
	const before = new Map(from.map((entry) => [entry.name, entry.command]));
	const after = new Set(to.map((entry) => entry.name));
	const lines: string[] = [];
	for (const { name, command } of to) {
		const old = before.get(name);
		if (old === undefined) lines.push(t("lado.presence.checks.added", { name, command: excerpt(command) }));
		else if (old !== command) lines.push(t("lado.presence.checks.changed", { name, command: excerpt(command) }));
	}
	for (const { name } of from) if (!after.has(name)) lines.push(t("lado.presence.checks.removed", { name }));
	const kept = to.filter((entry) => before.has(entry.name)).map((entry) => entry.name);
	const was = from.filter((entry) => after.has(entry.name)).map((entry) => entry.name);
	if (kept.join("\0") !== was.join("\0")) {
		lines.push(t("lado.presence.checks.order", { names: to.map((entry) => entry.name).join(", ") }));
	}
	return lines;
}

// The run an integration names, from LadoExecutionResponse: its task's title,
// its open pull request, and the head an approval already names. A merged or
// closed pull request has nothing left to integrate.
function integrationRun(body: unknown, sessionId: string): IntegrationRun | null {
	const run = record(body);
	const task = record(run?.task);
	if (run?.sessionId !== sessionId || typeof task?.title !== "string") return null;
	const pr = run.pr === undefined ? null : record(run.pr);
	const integration = run.integration === undefined ? null : record(run.integration);
	if ((run.pr !== undefined && !pr) || (run.integration !== undefined && typeof integration?.headSha !== "string")) return null;
	const approvedHead = typeof integration?.headSha === "string" ? integration.headSha.toLowerCase() : "";
	if (!pr) return { title: oneLine(task.title), pr: null, approvedHead };
	const { number, url, merged, closed, headSha = "", targetBranch = "" } = pr;
	if (
		typeof number !== "number" ||
		typeof url !== "string" ||
		url === "" ||
		typeof merged !== "boolean" ||
		typeof closed !== "boolean" ||
		typeof headSha !== "string" ||
		typeof targetBranch !== "string"
	) {
		return null;
	}
	return {
		title: oneLine(task.title),
		pr: merged || closed ? null : { number, url, headSha: headSha.toLowerCase(), branch: oneLine(targetBranch) },
		approvedHead,
	};
}

// The dry run's verification, from LadoIntegrationApproveResponse: about the
// pull request and head asked for, hashed, with its items in order and the
// codes to acknowledge.
function verifiedSubject(body: unknown, pr: { url: string; headSha: string }): VerifiedSubject | null {
	const answer = record(body);
	const verification = record(answer?.verification);
	const sha256 = answer?.verificationSha256;
	const confirmed = answer?.confirm;
	const items = verification?.items;
	const outcome = verification?.outcome;
	const headSha = verification?.headSha;
	if (
		!verification ||
		typeof sha256 !== "string" ||
		!SHA256_HEX.test(sha256) ||
		!isStringList(confirmed) ||
		!Array.isArray(items) ||
		verification.prUrl !== pr.url ||
		typeof headSha !== "string" ||
		headSha.toLowerCase() !== pr.headSha ||
		(outcome !== "pass" && outcome !== "wait" && outcome !== "fail" && outcome !== "confirm")
	) {
		return null;
	}
	const parsed: VerifyItem[] = [];
	for (const value of items) {
		const item = record(value);
		if (typeof item?.kind !== "string" || typeof item.outcome !== "string") return null;
		parsed.push({ kind: item.kind, outcome: item.outcome, code: text(item.code) ?? "", details: record(item.details) ?? {} });
	}
	// What holds the approval back, in the items' order, as the daemon names it.
	const held = parsed.filter((item) => item.outcome === "wait" || item.outcome === "fail").map((item) => item.code || "verifying");
	const reasons = held.length > 0 ? [...new Set(held)] : [text(verification.reason) ?? "verifying"];
	return { prUrl: pr.url, headSha, sha256, outcome, confirm: confirmed, reasons, items: parsed };
}

// The integration's confirmation: the task, the head, how many items passed,
// one line for each thing to confirm, and what happens next. Each line is
// cleaned on its own.
function integrationDetail(t: Translate, task: string, verified: VerifiedSubject): string {
	const counted = verified.items.filter((item) => item.outcome !== "info");
	const lines = [
		t("lado.presence.integration.task", { task }),
		t("lado.presence.integration.head", { head: verified.headSha.slice(0, 12) }),
		t("lado.presence.integration.passed", {
			passed: counted.filter((item) => item.outcome === "pass").length,
			total: counted.length,
		}),
		...verified.items.filter((item) => item.outcome === "confirm").map((item) => confirmLine(t, item)),
	];
	// GitLab gets no status; a branch whose rules do not require the status
	// lets a pull request merge without the approval, which LADO only notices.
	const protection = verified.items.find((item) => item.kind === "protection");
	const next = [
		...(protection?.details.provider === "gitlab" ? [] : [t("lado.presence.integration.nextStatus")]),
		...(protection && protection.outcome !== "pass" ? [t("lado.presence.integration.nextUnprotected")] : []),
	];
	return [lines.join("\n"), next.join(" ")].join("\n\n");
}

// One thing to confirm, with how many there are and up to three examples.
function confirmLine(t: Translate, item: VerifyItem): string {
	const { details } = item;
	switch (item.code) {
		case "outside_plan": {
			const paths = stringList(details.outsidePlan);
			const count = typeof details.outsideCount === "number" ? details.outsideCount : paths.length;
			return t("lado.presence.integration.outsidePlan", { count, examples: examples(paths, count) });
		}
		case "commit_messages_unclear": {
			const commits = recordList(details.commits)
				.filter((commit) => commit.result === "unclear" || commit.result === "body_unclear")
				.map((commit) => `${String(commit.sha ?? "").slice(0, 7)} ${String(commit.subject ?? "")}`);
			const title = record(details.prTitle);
			if (title?.result === "unclear") commits.push(t("lado.presence.integration.prTitle", { title: String(title.text ?? "") }));
			return t("lado.presence.integration.commitsUnclear", { count: commits.length, examples: examples(commits, commits.length) });
		}
		case "base_moved": {
			const moved = recordList(details.moved).map((entry) => `${String(entry.commit ?? "").slice(0, 7)} ${String(entry.subject ?? "")}`);
			return t("lado.presence.integration.baseMoved", { count: moved.length, examples: examples(moved, moved.length) });
		}
		case "reflog_off":
			return t("lado.presence.integration.reflogOff", { branch: excerpt(String(details.branch ?? ""), MAX_EXAMPLE_CHARS) });
		case "already_on_base":
			return t("lado.presence.integration.alreadyOnBase", { branch: excerpt(String(details.branch ?? ""), MAX_EXAMPLE_CHARS) });
		case "unprotected": {
			const missing = stringList(details.missing);
			return t("lado.presence.integration.unprotected", { count: missing.length, examples: examples(missing, missing.length) });
		}
		case "protection_unknown":
			return t("lado.presence.integration.protectionUnknown", { error: excerpt(String(details.error ?? ""), MAX_EXAMPLE_CHARS) });
		case "unprotected_gitlab":
			return t("lado.presence.integration.unprotectedGitlab");
		case "review_inconclusive":
			return t("lado.presence.integration.reviewInconclusive", { count: typeof details.rounds === "number" ? details.rounds : 0 });
		case "no_tests":
			return t("lado.presence.integration.noTests");
		default:
			// A code from a newer daemon still names what is acknowledged.
			return t("lado.presence.integration.otherCode", { code: excerpt(item.code, MAX_EXAMPLE_CHARS) });
	}
}

// Up to three values as one line, each cleaned on its own; "…" says more exist.
function examples(values: readonly string[], count: number): string {
	const shown = values.slice(0, MAX_EXAMPLES).map((value) => excerpt(value, MAX_EXAMPLE_CHARS));
	return count > shown.length ? `${shown.join(", ")}, …` : shown.join(", ");
}

function isStringList(value: unknown): value is string[] {
	return Array.isArray(value) && value.every((item) => typeof item === "string");
}

function stringList(value: unknown): string[] {
	return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function recordList(value: unknown): Record<string, unknown>[] {
	return Array.isArray(value) ? value.map(record).filter((item): item is Record<string, unknown> => item !== null) : [];
}

// What the change does, as the dialog names it; inheritsOn is what an
// inherit binding turns the item to.
function changeLine(t: Translate, change: SetupChange, inheritsOn: boolean): string {
	switch (change.op) {
		case "create":
			return t("lado.presence.setupChangeCreate");
		case "edit":
			return t("lado.presence.setupChangeEdit");
		case "delete":
			return t("lado.presence.setupChangeDelete");
		case "drift_keep":
			return t("lado.presence.setupChangeKeepDrift");
		case "binding": {
			const { scope, state } = change.body;
			const id = String(change.body.scopeId ?? "");
			if (scope === "global") return t(state === "on" ? "lado.presence.setupChangeOnGlobal" : "lado.presence.setupChangeOffGlobal");
			const task = scope === "task";
			if (state === "force_on") return t(task ? "lado.presence.setupChangeOnTask" : "lado.presence.setupChangeOnProject", { id });
			if (state === "force_off") return t(task ? "lado.presence.setupChangeOffTask" : "lado.presence.setupChangeOffProject", { id });
			if (task) return t(inheritsOn ? "lado.presence.setupChangeInheritTaskOn" : "lado.presence.setupChangeInheritTaskOff", { id });
			return t(inheritsOn ? "lado.presence.setupChangeInheritProjectOn" : "lado.presence.setupChangeInheritProjectOff", { id });
		}
	}
}

// Content as one line of at most max characters: control and direction
// characters could fake the dialog's other lines.
function excerpt(value: string, max = MAX_CONTENT_EXCERPT): string {
	const characters = Array.from(value.replace(UNSAFE_TEXT, " ").replace(/\s+/g, " ").trim());
	return characters.length > max ? `${characters.slice(0, max).join("")}…` : characters.join("");
}

function isSetupTool(value: unknown): value is SetupTool {
	return typeof value === "string" && (SETUP_TOOLS as readonly string[]).includes(value);
}

function isSetupKind(value: unknown): value is SetupKind {
	return typeof value === "string" && (SETUP_KINDS as readonly string[]).includes(value);
}

function isSetupName(value: unknown): value is string {
	return typeof value === "string" && value !== "" && value.length <= MAX_SETUP_NAME;
}

function isVersion(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 1;
}

// Content is one JSON value: the text, or a setting's or a server's value.
function isJSONValue(value: unknown): boolean {
	if (value === undefined) return false;
	try {
		return JSON.stringify(value) !== undefined;
	} catch {
		return false;
	}
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
