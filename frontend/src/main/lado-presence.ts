// LADO fork: plan approval, permission ceiling and protected path changes,
// turning LADO mode off, changing a project's check commands, adopting,
// restoring and changing local setup, approving a run's integration,
// turning Claude in Chrome on, and changing a repository's production
// database change settings or applying a pull request's SQL by hand (#97),
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
	LADO_PRESENCE_APPLY_DB_CHANGES_CHANNEL,
	LADO_PRESENCE_APPROVE_INTEGRATION_CHANNEL,
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	LADO_PRESENCE_CHANGE_CLAUDE_CHROME_CHANNEL,
	LADO_PRESENCE_CHANGE_DB_CHANGES_CHANNEL,
	LADO_PRESENCE_CHANGE_PROJECT_CHECKS_CHANNEL,
	LADO_PRESENCE_CHANGE_RULE_CHANNEL,
	LADO_PRESENCE_CHANGE_SETUP_CHANNEL,
	LADO_PRESENCE_RESTORE_SETUP_CHANNEL,
	LADO_PRESENCE_SET_LADO_MODE_CHANNEL,
	type LadoCeilingMode,
	type LadoPresenceAdoptSetupResult,
	type LadoPresenceApplyDbChangesResult,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
	type LadoPresenceCheckCommand,
	type LadoPresenceChecksResult,
	type LadoPresenceClaudeChromeResult,
	type LadoPresenceClaudeChromeSettings,
	type LadoPresenceDbChangePhase,
	type LadoPresenceDbChangeRun,
	type LadoPresenceDbChangeSettings,
	type LadoPresenceDbChangesRequest,
	type LadoPresenceDbChangesResult,
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
const CLAUDE_CHROME_PATH = "/api/v1/lado/claude-chrome";
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
// A check command is not cut: the dialog shows all it runs.
const MAX_CONTENT_EXCERPT = 300;
// A check command is one line (S2 of #15): a control character, such as a
// line break or a tab, or a direction control would make the dialog show
// other text than the shell runs. The daemon refuses them as well.
const COMMAND_CONTROL = /[\u0000-\u001f\u007f-\u009f\u2028\u2029]/u;
const COMMAND_BIDI = /[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/u;
// How many paths, commits or entries an integration's confirmation names for
// each thing to confirm, and how much of each.
const MAX_EXAMPLES = 3;
const MAX_EXAMPLE_CHARS = 120;
// The daemon's limits of a repository's production database change settings
// (backend/internal/domain/lado_db_change.go, ladodbchange.SecretValueMax).
const MAX_DB_PATHS = 10;
const MAX_DB_COMMAND = 2000;
const MAX_DB_TIMEOUT_MINUTES = 120;
const DB_DEFAULT_TIMEOUT_MINUTES = 10;
const MAX_DB_SECRETS = 10;
const MAX_DB_SECRET_VALUE_BYTES = 8 << 10;
const MAX_DB_TEXT = 1024;
const DB_SECRET_NAME = /^[A-Z_][A-Z0-9_]{0,63}$/;
const DB_PHASES: readonly LadoPresenceDbChangePhase[] = ["pre_merge", "after_deploy"];
// A run the developer may start again: it waits for the button, failed, or
// its merge failed (domain.LadoDBChangeRunState.Retryable).
const DB_RETRYABLE_RUN_STATES: readonly unknown[] = ["awaiting_human", "failed", "merge_failed"];
// How many SQL files a confirmation names, how many of an integration's it
// quotes and how many of each one's first lines.
const MAX_DB_FILES_SHOWN = 20;
const MAX_DB_EXCERPT_FILES = 5;
const MAX_DB_EXCERPT_LINES = 3;
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
type PresenceMessageKey = Extract<TrayMessageKey, `lado.presence.${string}` | `lado.setup.kind.${string}` | `lado.protection.part.${string}`>;
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
// One open pull request an integration approval names, from
// LadoExecutionResponse: repo is its repository's folder under the
// workspace's, empty for a single-repository project, and approved says its
// current head has an approval that holds.
type IntegrationPR = { number: number; url: string; headSha: string; branch: string; repo: string; approved: boolean };
// The run an integration approval names: its task's title and its open pull
// requests in merge order, none when it has no open pull request.
type IntegrationRun = { title: string; prs: IntegrationPR[] };
type VerifyItem = { kind: string; outcome: string; code: string; details: Record<string, unknown> };
// One SQL file a head's db_changes item names, from its details.files.
type DbFileRef = { path: string; blobSha: string; phase: LadoPresenceDbChangePhase };
// The first lines of each SQL file an integration's confirmation quotes, by
// pull request and blob.
type DbExcerpts = Map<string, string[]>;
// One repository's production database change settings as the daemon has
// them, from LadoDBChangeRepoSettingsResponse.
type DbRepoSettings = {
	repo: string;
	name: string;
	enabled: boolean;
	paths: string[];
	command: string;
	timeoutMinutes: number;
	secretNames: string[];
	storedSecretNames: string[];
	sha256: string;
};
// A settings change as main checked it.
type DbSettingsChange = Omit<LadoPresenceDbChangesRequest, "projectId" | "secrets"> & {
	secrets: { set: Record<string, string>; remove: string[] };
};
// One file of a pull request's database changes, from LadoDBChangeFileResponse.
type DbFileView = DbFileRef & { status: string };
// One run of a pull request's database changes, from LadoDBChangeRunResponse.
type DbRunView = { headSha: string; kind: string; trigger: string; state: string };
// A pull request's database changes at its current head, from LadoDBChangePRResponse.
type DbPRView = { prUrl: string; headSha: string; repo: string; files: DbFileView[]; runs: DbRunView[] };
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
	handle(LADO_PRESENCE_CHANGE_CLAUDE_CHROME_CHANNEL, changeClaudeChrome, "Turning on Claude in Chrome failed.");
	handle(LADO_PRESENCE_CHANGE_DB_CHANGES_CHANNEL, changeDbChanges, "The database change settings change failed.");
	handle(LADO_PRESENCE_APPLY_DB_CHANGES_CHANNEL, applyDbChanges, "Applying the database changes failed.");
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
	// One line per glob, each whole: a removal weakens the protection, so
	// none may hide behind a cut (review of #31).
	const detail = [
		[
			...added.map((glob) => t("lado.presence.paths.added", { globs: glob })),
			...removed.map((glob) => t("lado.presence.paths.removed", { globs: glob })),
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
	if (run.prs.length === 0) return failure("LADO_EXECUTION_NO_PR", "This run has no open pull request to integrate.", { sessionId });
	const approvePath = `${executionPath}/integration-approve`;
	if (run.prs.length > 1) return approveIntegrations(deps, sessionId, run, approvePath);
	const pr = run.prs[0]!;
	if (!pr.headSha) {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO has not verified this pull request's head yet.", { reasons: ["verifying"] });
	}
	if (pr.approved) {
		return failure("LADO_INTEGRATION_ALREADY_APPROVED", "The integration of this head is approved already.");
	}
	// The dry run reads the live head, the remote rules and the base branch
	// again and verifies the head as the approval would.
	const preview = await callDaemon(
		deps,
		approvePath,
		jsonRequest("POST", JSON.stringify({ prUrl: pr.url, headSha: pr.headSha, dryRun: true })),
		INTEGRATION_TIMEOUT_MS,
	);
	if (!preview.ok) return preview.failure;
	const verified = verifiedSubject(preview.body, pr);
	if (!verified) return failure(ANSWER_UNREADABLE, "The AO daemon's dry run of this approval could not be read.");
	if (verified.outcome === "wait" || verified.outcome === "fail") {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO is still verifying this head, or a check on it failed.", {
			reasons: verified.reasons,
		});
	}
	const excerpts = await dbExcerpts(deps, sessionId, [verified]);
	const t = await translator(deps);
	const title = t("lado.presence.integration.title", { number: pr.number, branch: pr.branch || "—" });
	if (!(await confirm(deps, t, title, integrationDetail(t, run.title, verified, excerpts), t("lado.presence.integration.approve")))) {
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

// A task that opened a pull request in each of several repositories (#64) is
// approved as one: main sends one dry run naming every open pull request,
// lists them all in one confirmation, then signs one request naming each head
// with what its dry run verified. The daemon refuses the approval unless it
// names exactly the open pull requests it tracks.
async function approveIntegrations(
	deps: LadoPresenceDeps,
	sessionId: string,
	run: IntegrationRun,
	approvePath: string,
): Promise<LadoPresenceIntegrationResult> {
	if (run.prs.some((pr) => !pr.headSha)) {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO has not verified the head of each pull request yet.", { reasons: ["verifying"] });
	}
	if (run.prs.every((pr) => pr.approved)) {
		return failure("LADO_INTEGRATION_ALREADY_APPROVED", "The integration of these heads is approved already.");
	}
	const preview = await callDaemon(
		deps,
		approvePath,
		jsonRequest("POST", JSON.stringify({ prs: run.prs.map((pr) => ({ prUrl: pr.url, headSha: pr.headSha })), dryRun: true })),
		INTEGRATION_TIMEOUT_MS,
	);
	if (!preview.ok) return preview.failure;
	const verified = verifiedSubjects(preview.body, run.prs);
	if (!verified) return failure(ANSWER_UNREADABLE, "The AO daemon's dry run of this approval could not be read.");
	const held = verified.filter((one) => one.outcome === "wait" || one.outcome === "fail");
	if (held.length > 0) {
		return failure("LADO_INTEGRATION_NOT_READY", "LADO is still verifying a head, or a check on one failed.", {
			reasons: [...new Set(held.flatMap((one) => one.reasons))],
		});
	}
	const excerpts = await dbExcerpts(deps, sessionId, verified);
	const t = await translator(deps);
	const title = t("lado.presence.integration.titleMany", { count: run.prs.length });
	if (!(await confirm(deps, t, title, integrationsDetail(t, run, verified, excerpts), t("lado.presence.integration.approve")))) {
		return { status: "cancelled" };
	}

	// Each head with its record's hash and the codes confirmed, in merge
	// order, the heads approved already too: the daemon leaves those as they
	// are.
	const body = JSON.stringify({
		prs: verified.map((one) => ({
			prUrl: one.prUrl,
			headSha: one.headSha,
			verificationSha256: one.sha256,
			acknowledge: one.confirm,
		})),
	});
	const sent = await callDaemon(deps, approvePath, signed(deps, "POST", approvePath, body), INTEGRATION_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const integration = record(sent.body);
	const prs = integration?.prs;
	if (!Array.isArray(prs) || prs.length !== verified.length || !prs.every((pr) => record(record(pr)?.approval))) {
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

	// The daemon checks the proof against exactly these bytes, and refuses the
	// change when the list is no longer the one this dialog compared with.
	const body = JSON.stringify({ checkCommands: commands.commands, checkCommandsBase: settings.checkCommandsSha256 });
	const sent = await callDaemon(deps, settingsPath, signed(deps, "PUT", settingsPath, body));
	if (!sent.ok) return sent.failure;
	const changed = projectSettings(sent.body, projectId);
	if (!changed) return failure(ANSWER_UNREADABLE, "The check commands were changed, but the AO daemon's answer could not be read.");
	return { status: "changed", settings: changed };
}

// Claude in Chrome lets agents control the Chrome the developer is signed in
// to (issue #40), and any local process can call the daemon, so only the
// human may turn it on. Main reads the setting from the daemon, names what
// turns on, then signs the whole setting. Turning parts off needs no
// confirmation: the renderer sends that to the daemon itself, and when
// nothing turns on this channel sends nothing.
async function changeClaudeChrome(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceClaudeChromeResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can turn on Claude in Chrome.");
	}
	// The two switches are all the renderer gives; anything else it sends is ignored.
	const request = record(input);
	const chatSessions = request?.chatSessions;
	const ladoWorkers = request?.ladoWorkers;
	if (typeof chatSessions !== "boolean" || typeof ladoWorkers !== "boolean") {
		return failure("LADO_CLAUDE_CHROME_INVALID", "Send chatSessions and ladoWorkers.");
	}
	const read = await callDaemon(deps, CLAUDE_CHROME_PATH, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const settings = claudeChromeSettings(read.body);
	if (!settings) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about the Claude in Chrome setting could not be read.");
	const turningOn: PresenceMessageKey[] = [];
	if (chatSessions && !settings.chatSessions) turningOn.push("lado.presence.claudeChromeChatSessions");
	if (ladoWorkers && !settings.ladoWorkers) turningOn.push("lado.presence.claudeChromeLadoWorkers");
	// Nothing turns on, so there is nothing to confirm.
	if (turningOn.length === 0) return { status: "changed", settings };
	const t = await translator(deps);
	const detail = [...turningOn.map((key) => t(key)), t("lado.presence.claudeChromeDetail")].join("\n\n");
	if (!(await confirm(deps, t, t("lado.presence.claudeChromeTitle"), detail, t("lado.presence.claudeChromeTurnOn")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes.
	const body = JSON.stringify({ chatSessions, ladoWorkers });
	const sent = await callDaemon(deps, CLAUDE_CHROME_PATH, signed(deps, "PUT", CLAUDE_CHROME_PATH, body));
	if (!sent.ok) return sent.failure;
	const changed = claudeChromeSettings(sent.body);
	if (!changed) return failure(ANSWER_UNREADABLE, "Claude in Chrome was turned on, but the AO daemon's answer could not be read.");
	return { status: "changed", settings: changed };
}

// A repository's production database change settings name a command that
// runs outside any agent sandbox with secrets against the production
// database (#97), so only the human may change them. Main reads the settings
// from the daemon, names only what changes, never shows a secret's value,
// then signs the change against the settings it read.
async function changeDbChanges(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceDbChangesResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can change the database change settings.");
	}
	// These fields are all the renderer gives; anything else it sends is ignored.
	const request = record(input);
	const projectId = request?.projectId;
	if (typeof projectId !== "string" || !PROJECT_ID.test(projectId) || projectId.includes("..")) {
		return failure("LADO_PROJECT_ID_INVALID", "Invalid project id.");
	}
	const change = dbSettingsChange(request);
	if (!change.ok) {
		return failure("LADO_DB_CHANGE_INVALID", "The database change settings are not valid.", { problems: [change.problem] });
	}
	const settingsPath = `/api/v1/lado/projects/${encodeURIComponent(projectId)}/db-changes/settings`;
	const read = await callDaemon(deps, settingsPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const settings = dbChangeSettings(read.body, projectId);
	if (!settings) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this project's database change settings could not be read.");
	const current = settings.repositories.find((repo) => repo.repo === change.change.repo);
	if (!current) {
		return failure("LADO_DB_CHANGE_INVALID", "This project has no such repository.", { problems: [{ field: "repo", reason: "invalid" }] });
	}
	const t = await translator(deps);
	const lines = dbSettingsLines(t, current, change.change);
	// Nothing would change, so there is nothing to confirm.
	if (lines.length === 0) return { status: "changed", settings: settings.response };
	const title = t("lado.presence.dbChanges.title", { repo: current.name || current.repo || projectId });
	const detail = [lines.join("\n"), t("lado.presence.dbChanges.detail", { id: projectId })].join("\n\n");
	if (!(await confirm(deps, t, title, detail, t("lado.presence.change")))) {
		return { status: "cancelled" };
	}

	// The daemon checks the proof against exactly these bytes, and refuses the
	// change when the settings are no longer the ones this dialog compared with.
	const { secrets, ...fields } = change.change;
	const body = JSON.stringify({
		...fields,
		...(Object.keys(secrets.set).length > 0 || secrets.remove.length > 0
			? {
					secrets: {
						...(Object.keys(secrets.set).length > 0 ? { set: secrets.set } : {}),
						...(secrets.remove.length > 0 ? { remove: secrets.remove } : {}),
					},
				}
			: {}),
		base: current.sha256,
	});
	// Turning the settings on fetches the default branch to record its SQL files.
	const sent = await callDaemon(deps, settingsPath, signed(deps, "PUT", settingsPath, body), INTEGRATION_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const changed = dbChangeSettings(sent.body, projectId);
	if (!changed) return failure(ANSWER_UNREADABLE, "The settings were changed, but the AO daemon's answer could not be read.");
	return { status: "changed", settings: changed.response };
}

// Applying a pull request's SQL by hand runs it against the production
// database (#97), so only the human may start it: a retry of a failed or held
// pre-merge run, the pre-merge SQL of a pull request merged outside LADO, or
// the after-deploy SQL. Main reads the run's database changes from the
// daemon, names the head and each file with whether it runs, warns about a
// file whose command was interrupted, then signs the files it read.
async function applyDbChanges(
	deps: LadoPresenceDeps,
	sender: WebContents,
	input: unknown,
): Promise<LadoPresenceApplyDbChangesResult> {
	if (!deps.isTrustedSender(sender)) {
		return failure("LADO_PRESENCE_UNTRUSTED_SENDER", "Only the AO app window can apply database changes.");
	}
	// The session, the pull request and the phase are all the renderer gives;
	// anything else it sends is ignored.
	const request = record(input);
	const { sessionId, prUrl, phase } = request ?? {};
	if (typeof sessionId !== "string" || !SESSION_ID.test(sessionId) || sessionId.includes("..")) {
		return failure("LADO_SESSION_ID_INVALID", "Invalid session id.");
	}
	if (typeof prUrl !== "string" || prUrl === "" || prUrl.length > MAX_DB_TEXT || UNSAFE_CHAR.test(prUrl) || !isDbPhase(phase)) {
		return failure("LADO_DB_CHANGE_INVALID", "Name the pull request and pre_merge or after_deploy.");
	}
	const changesPath = `/api/v1/lado/executions/${encodeURIComponent(sessionId)}/db-changes`;
	const read = await callDaemon(deps, changesPath, { method: "GET", headers: { accept: "application/json" } });
	if (!read.ok) return read.failure;
	const prs = dbExecutionPRs(read.body, sessionId);
	if (!prs) return failure(ANSWER_UNREADABLE, "The AO daemon's answer about this run's database changes could not be read.");
	const pr = prs.find((one) => one.prUrl === prUrl);
	const files = pr?.headSha ? pr.files.filter((file) => file.phase === phase) : [];
	if (!pr || files.length === 0) {
		return failure("LADO_DB_CHANGE_NOT_FOUND", "LADO knows no SQL of this phase at this pull request's head.", { prUrl, phase });
	}
	const run = pr.runs.find((one) => one.kind === phase && one.headSha === pr.headSha);
	// The daemon refuses these as well; asking the human first would only
	// waste a confirmation.
	if ((run && !DB_RETRYABLE_RUN_STATES.includes(run.state)) || files.some((file) => file.status === "applying")) {
		return failure("LADO_DB_CHANGE_RUN_STATE", "LADO is applying this SQL now, or applied it already.", { state: run?.state ?? "applying" });
	}
	// Without a run, only a merged pull request's SQL can be applied by hand.
	const trigger = run?.trigger ?? (phase === "after_deploy" ? "after_merge" : "merged_unapplied");
	const pending = files.filter((file) => file.status !== "applied");
	const mergeOnly = pending.length === 0 && (run?.state === "merge_failed" || trigger === "approval");
	if (pending.length === 0 && !mergeOnly) {
		return failure("LADO_DB_CHANGE_RUN_STATE", "Every SQL file of this head is applied already.", { state: run?.state ?? "" });
	}
	const t = await translator(deps);
	const title = t(
		trigger === "approval"
			? "lado.presence.dbApply.titleApproval"
			: trigger === "after_merge"
				? "lado.presence.dbApply.titleAfterDeploy"
				: "lado.presence.dbApply.titleMerged",
	);
	if (!(await confirm(deps, t, title, dbApplyDetail(t, pr, files, trigger, mergeOnly), t("lado.presence.dbApply.apply")))) {
		return { status: "cancelled" };
	}

	// Every file of the phase at the head, in path order: the daemon skips
	// those applied already and refuses files that are not the head's.
	const body = JSON.stringify({
		prUrl: pr.prUrl,
		headSha: pr.headSha,
		phase,
		files: files.map((file) => ({ path: file.path, blobSha: file.blobSha, phase: file.phase })),
	});
	const applyPath = `${changesPath}/apply`;
	const sent = await callDaemon(deps, applyPath, signed(deps, "POST", applyPath, body), INTEGRATION_TIMEOUT_MS);
	if (!sent.ok) return sent.failure;
	const queued = record(sent.body);
	if (typeof queued?.id !== "string" || queued.prUrl !== pr.prUrl || typeof queued.state !== "string") {
		return failure(ANSWER_UNREADABLE, "The request was sent, but the AO daemon's answer could not be read.");
	}
	return { status: "queued", run: queued as LadoPresenceDbChangeRun };
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

// The Claude in Chrome setting, from LadoClaudeChromeSettingsResponse.
function claudeChromeSettings(body: unknown): LadoPresenceClaudeChromeSettings | null {
	const settings = record(body);
	if (typeof settings?.chatSessions !== "boolean" || typeof settings.ladoWorkers !== "boolean") return null;
	return { chatSessions: settings.chatSessions, ladoWorkers: settings.ladoWorkers };
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
type CheckCommandProblem = { index: number; field: string; reason: string; position?: number; character?: string };

function checkCommands(value: unknown): { ok: true; commands: LadoPresenceCheckCommand[] } | { ok: false; details: CheckCommandProblem } {
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
		const characters = Array.from(command);
		if (characters.length > MAX_CHECK_COMMAND) return problem("command", "too_long");
		const at = characters.findIndex((character) => COMMAND_CONTROL.test(character) || COMMAND_BIDI.test(character));
		if (at >= 0) {
			const character = characters[at] ?? "";
			const reason = COMMAND_BIDI.test(character) ? "bidi_control" : "control_character";
			const codePoint = `U+${(character.codePointAt(0) ?? 0).toString(16).toUpperCase().padStart(4, "0")}`;
			return { ok: false, details: { index, field: "command", reason, position: at + 1, character: codePoint } };
		}
		commands.push({ name, command });
	}
	return { ok: true, commands };
}

// What a change of the check commands does: one line for each command added
// or changed, with all the command runs as it runs it (checkCommands refused
// the characters that could fake a line), one for each removed, and the
// order they run in when that changes.
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
		if (old === undefined) lines.push(t("lado.presence.checks.added", { name, command }));
		else if (old !== command) lines.push(t("lado.presence.checks.changed", { name, command }));
	}
	for (const { name } of from) if (!after.has(name)) lines.push(t("lado.presence.checks.removed", { name }));
	const kept = to.filter((entry) => before.has(entry.name)).map((entry) => entry.name);
	const was = from.filter((entry) => after.has(entry.name)).map((entry) => entry.name);
	if (kept.join("\0") !== was.join("\0")) {
		lines.push(t("lado.presence.checks.order", { names: to.map((entry) => entry.name).join(", ") }));
	}
	return lines;
}

// The run an integration names, from LadoExecutionResponse: its task's title
// and its open pull requests, each with whether its head is approved
// already. A run that tracks several pull requests (#64) lists them in
// trackedPrs; an older daemon sends its one PR alone. A merged or closed pull
// request has nothing left to integrate.
function integrationRun(body: unknown, sessionId: string): IntegrationRun | null {
	const run = record(body);
	const task = record(run?.task);
	if (run?.sessionId !== sessionId || typeof task?.title !== "string") return null;
	const title = oneLine(task.title);
	if (run.trackedPrs !== undefined) {
		if (!Array.isArray(run.trackedPrs)) return null;
		const tracked: (IntegrationPR & { position: number; open: boolean })[] = [];
		for (const value of run.trackedPrs) {
			const entry = record(value);
			const parsed = entry ? integrationPR(entry) : null;
			const integration = record(entry?.integration);
			const { position, repo } = entry ?? {};
			if (!parsed || typeof position !== "number" || typeof repo !== "string" || typeof integration?.headApproved !== "boolean") return null;
			tracked.push({ ...parsed.pr, repo: oneLine(repo), approved: integration.headApproved, position, open: parsed.open });
		}
		tracked.sort((a, b) => a.position - b.position);
		const prs = tracked.filter((pr) => pr.open).map(({ position: _position, open: _open, ...pr }) => pr);
		return { title, prs };
	}
	const pr = run.pr === undefined ? null : record(run.pr);
	const integration = run.integration === undefined ? null : record(run.integration);
	if ((run.pr !== undefined && !pr) || (run.integration !== undefined && typeof integration?.headSha !== "string")) return null;
	const approvedHead = typeof integration?.headSha === "string" ? integration.headSha.toLowerCase() : "";
	if (!pr) return { title, prs: [] };
	const parsed = integrationPR(pr);
	if (!parsed) return null;
	if (!parsed.open) return { title, prs: [] };
	return { title, prs: [{ ...parsed.pr, approved: approvedHead !== "" && approvedHead === parsed.pr.headSha }] };
}

// One pull request of a run, from LadoExecutionPRResponse or
// LadoExecutionTrackedPRResponse, and whether it is still open.
function integrationPR(pr: Record<string, unknown>): { pr: Omit<IntegrationPR, "approved">; open: boolean } | null {
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
	return { pr: { number, url, headSha: headSha.toLowerCase(), branch: oneLine(targetBranch), repo: "" }, open: !merged && !closed };
}

// The dry run of several pull requests, from LadoIntegrationApproveResponse's
// prs: one verification for each pull request asked for, in their order.
function verifiedSubjects(body: unknown, prs: readonly IntegrationPR[]): VerifiedSubject[] | null {
	const answers = record(body)?.prs;
	if (!Array.isArray(answers) || answers.length !== prs.length) return null;
	const verified: VerifiedSubject[] = [];
	for (const pr of prs) {
		const matching = answers.filter((answer) => record(answer)?.prUrl === pr.url);
		if (matching.length !== 1) return null;
		const one = verifiedSubject(matching[0], pr);
		if (!one) return null;
		verified.push(one);
	}
	return verified;
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
function integrationDetail(t: Translate, task: string, verified: VerifiedSubject, excerpts: DbExcerpts): string {
	const lines = [t("lado.presence.integration.task", { task }), ...headLines(t, verified, excerpts)];
	return [lines.join("\n"), nextLines(t, [verified], false)].join("\n\n");
}

// The confirmation of a task's pull requests approved together: the task,
// then each pull request with its repository, target, head and what to
// confirm about it, then what happens next.
function integrationsDetail(t: Translate, run: IntegrationRun, verified: readonly VerifiedSubject[], excerpts: DbExcerpts): string {
	const blocks = [t("lado.presence.integration.task", { task: run.title })];
	verified.forEach((one, index) => {
		const pr = run.prs[index]!;
		blocks.push(
			[
				t("lado.presence.integration.prLine", {
					number: pr.number,
					repo: excerpt(pr.repo, MAX_EXAMPLE_CHARS) || "—",
					branch: excerpt(pr.branch, MAX_EXAMPLE_CHARS) || "—",
				}),
				...headLines(t, one, excerpts),
				...(pr.approved ? [t("lado.presence.integration.approvedAlready")] : []),
			].join("\n"),
		);
	});
	return [...blocks, nextLines(t, verified, true)].join("\n\n");
}

// The head, how many of its items passed, the SQL files the approval signs
// with their phase and first lines, and one line for each to confirm.
function headLines(t: Translate, verified: VerifiedSubject, excerpts: DbExcerpts): string[] {
	const counted = verified.items.filter((item) => item.outcome !== "info");
	return [
		t("lado.presence.integration.head", { head: verified.headSha.slice(0, 12) }),
		t("lado.presence.integration.passed", {
			passed: counted.filter((item) => item.outcome === "pass").length,
			total: counted.length,
		}),
		...dbFileLines(t, verified, excerpts),
		...verified.items.filter((item) => item.outcome === "confirm").map((item) => confirmLine(t, item)),
	];
}

// The SQL files a head's db_changes item names (#97), each with its phase and
// up to three of its first lines; the approval signs their blobs.
function dbFileLines(t: Translate, verified: VerifiedSubject, excerpts: DbExcerpts): string[] {
	const files = dbFiles(verified);
	const lines: string[] = [];
	for (const file of files.slice(0, MAX_DB_FILES_SHOWN)) {
		const key = file.phase === "after_deploy" ? "lado.presence.integration.dbFileAfterDeploy" : "lado.presence.integration.dbFilePreMerge";
		lines.push(t(key, { path: excerpt(file.path, MAX_EXAMPLE_CHARS) }));
		for (const line of excerpts.get(dbExcerptKey(verified.prUrl, file.blobSha)) ?? []) lines.push(`    ${line}`);
	}
	if (files.length > MAX_DB_FILES_SHOWN) lines.push(t("lado.presence.integration.dbMore", { count: files.length - MAX_DB_FILES_SHOWN }));
	return lines;
}

// The SQL files of a head's db_changes item, in the item's order (paths in
// byte order); none when the head's repository has no settings on.
function dbFiles(verified: VerifiedSubject): DbFileRef[] {
	const item = verified.items.find((one) => one.kind === "db_changes");
	return item ? dbFileRefs(item.details.files) : [];
}

function dbFileRefs(value: unknown): DbFileRef[] {
	const files: DbFileRef[] = [];
	for (const entry of recordList(value)) {
		const { path, blobSha, phase } = entry;
		if (typeof path === "string" && path !== "" && typeof blobSha === "string" && blobSha !== "" && isDbPhase(phase)) {
			files.push({ path, blobSha, phase });
		}
	}
	return files;
}

function dbExcerptKey(prUrl: string, blobSha: string): string {
	return `${prUrl}\n${blobSha}`;
}

// The first lines of the first SQL files the heads name, read from the
// daemon by their blobs, so the confirmation quotes what the approval signs.
// A file the daemon does not serve is named without its lines.
async function dbExcerpts(deps: LadoPresenceDeps, sessionId: string, verified: readonly VerifiedSubject[]): Promise<DbExcerpts> {
	const excerpts: DbExcerpts = new Map();
	const wanted = verified
		.flatMap((one) => dbFiles(one).slice(0, MAX_DB_FILES_SHOWN).map((file) => ({ prUrl: one.prUrl, file })))
		.slice(0, MAX_DB_EXCERPT_FILES);
	for (const { prUrl, file } of wanted) {
		const query = new URLSearchParams({ prUrl, blobSha: file.blobSha });
		const path = `/api/v1/lado/executions/${encodeURIComponent(sessionId)}/db-changes/file?${query.toString()}`;
		const read = await callDaemon(deps, path, { method: "GET", headers: { accept: "application/json" } });
		const content = read.ok ? record(read.body) : null;
		if (content?.blobSha !== file.blobSha || typeof content.content !== "string") continue;
		const lines = content.content
			.split(/\r?\n/)
			.map((line) => excerpt(line, MAX_EXAMPLE_CHARS))
			.filter((line) => line !== "")
			.slice(0, MAX_DB_EXCERPT_LINES);
		excerpts.set(dbExcerptKey(prUrl, file.blobSha), lines);
	}
	return excerpts;
}

// What happens next. LADO posts its status on GitHub and GitLab (#21); a
// record from before that names GitLab's protection unprotected_gitlab, and
// such a head gets no status. A branch whose rules do not require the status
// lets a pull request merge without the approval, which LADO only notices.
//
// A head with pre-merge SQL makes the approval apply it and LADO merge the
// run's pull requests itself (ADR 0013, #97); after-deploy SQL waits for the
// developer's button after the merge.
function nextLines(t: Translate, verified: readonly VerifiedSubject[], many: boolean): string {
	const protections = verified.map((one) => one.items.find((item) => item.kind === "protection"));
	// Rules LADO could not read may well protect the branch (U4 of #15).
	const unprotected = protections.some((protection) => protection?.code === "unprotected" || protection?.code === "unprotected_gitlab");
	const posts = protections.some((protection) => protection?.code !== "unprotected_gitlab");
	const files = verified.flatMap(dbFiles);
	const preMerge = files.some((file) => file.phase === "pre_merge");
	const afterDeploy = files.some((file) => file.phase === "after_deploy");
	const status = preMerge
		? [t(many ? "lado.presence.integration.nextDbChangesMany" : "lado.presence.integration.nextDbChanges")]
		: posts
			? [t(many ? "lado.presence.integration.nextStatusMany" : "lado.presence.integration.nextStatus")]
			: [];
	const next = [
		...status,
		...(afterDeploy ? [t("lado.presence.integration.nextAfterDeploy")] : []),
		...(unprotected ? [t(many ? "lado.presence.integration.nextUnprotectedMany" : "lado.presence.integration.nextUnprotected")] : []),
	];
	return next.join(" ");
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
		case "base_deleted":
			return t("lado.presence.integration.baseDeleted", {
				branch: excerpt(String(details.branch ?? ""), MAX_EXAMPLE_CHARS),
				commit: excerpt(String(details.dispatchCommit ?? ""), MAX_EXAMPLE_CHARS).slice(0, 7),
			});
		case "base_advanced":
			return t("lado.presence.integration.baseAdvanced", {
				branch: excerpt(String(details.branch ?? ""), MAX_EXAMPLE_CHARS),
				commit: excerpt(String(details.advancedTip ?? ""), MAX_EXAMPLE_CHARS).slice(0, 7),
			});
		case "base_not_default": {
			// The PR is in another repository than its issue (#59).
			const branch = excerpt(String(details.branch ?? ""), MAX_EXAMPLE_CHARS);
			return t("lado.presence.integration.baseNotDefault", {
				branch,
				dispatchBranch: excerpt(String(details.dispatchBranch ?? ""), MAX_EXAMPLE_CHARS) || branch,
				defaultBranch: excerpt(String(details.defaultBranch ?? ""), MAX_EXAMPLE_CHARS),
			});
		}
		case "unprotected": {
			// The parts by name, as the run panel names them (U1 of #15).
			const missing = stringList(details.missing);
			const line = t("lado.presence.integration.unprotected", {
				count: missing.length,
				examples: examples(
					missing.map((part) => protectionPart(t, part)),
					missing.length,
				),
			});
			// A check from one app only cannot be LADO's status (Q3 of #15).
			return missing.includes(ANY_SOURCE_PART) ? `${line}\n${t("lado.presence.integration.anySource")}` : line;
		}
		case "protection_unknown":
			return t("lado.presence.integration.protectionUnknown", {
				error: excerpt(String(details.error || details.errorClass || ""), MAX_EXAMPLE_CHARS),
			});
		case "unprotected_gitlab":
			return t("lado.presence.integration.unprotectedGitlab");
		case "review_inconclusive":
			return t("lado.presence.integration.reviewInconclusive", { count: typeof details.rounds === "number" ? details.rounds : 0 });
		case "no_tests":
			return t("lado.presence.integration.noTests");
		case "db_change_applied_modified": {
			// An applied SQL file the head changes or deletes (#97).
			const paths = recordList(details.modified).map((entry) => String(entry.path ?? ""));
			return t("lado.presence.integration.dbAppliedModified", { count: paths.length, examples: examples(paths, paths.length) });
		}
		case "db_change_unplanned":
			return t("lado.presence.integration.dbUnplanned");
		default:
			// A code from a newer daemon still names what is acknowledged.
			return t("lado.presence.integration.otherCode", { code: excerpt(item.code, MAX_EXAMPLE_CHARS) });
	}
}

// What a base branch's rules lack, as the daemon names it (ladoverify
// MissingProtection), by the words the app shows it with.
const ANY_SOURCE_PART = "required_status_checks:lado/integration:any_source";
const PROTECTION_PARTS: Partial<Record<string, PresenceMessageKey>> = {
	pull_request: "lado.protection.part.pullRequest",
	"required_status_checks:lado/integration": "lado.protection.part.integrationCheck",
	[ANY_SOURCE_PART]: "lado.protection.part.integrationCheckSource",
	non_fast_forward: "lado.protection.part.nonFastForward",
	deletion: "lado.protection.part.deletion",
	// A GitLab base branch's protection and project settings (#21).
	protected_branch: "lado.protection.part.protectedBranch",
	push_access: "lado.protection.part.pushAccess",
	force_push: "lado.protection.part.forcePush",
	pipeline_must_succeed: "lado.protection.part.pipelineMustSucceed",
	skipped_pipeline: "lado.protection.part.skippedPipeline",
	integration_status: "lado.protection.part.integrationStatus",
};

// A part from a newer daemon reads as the daemon names it.
function protectionPart(t: Translate, part: string): string {
	const key = PROTECTION_PARTS[part];
	return key ? t(key) : part;
}

// Up to three values as one line, each cleaned on its own; "…" says more exist.
function examples(values: readonly string[], count: number): string {
	const shown = values.slice(0, MAX_EXAMPLES).map((value) => excerpt(value, MAX_EXAMPLE_CHARS));
	return count > shown.length ? `${shown.join(", ")}, …` : shown.join(", ");
}

// A settings change the renderer asks for, checked as the daemon checks it
// where the dialog depends on it: the lists' sizes, one line of a command
// and of each path, the secret names' form, and values that are one line.
function dbSettingsChange(
	request: Record<string, unknown> | null,
): { ok: true; change: DbSettingsChange } | { ok: false; problem: { field: string; reason: string } } {
	const bad = (field: string, reason = "invalid") => ({ ok: false as const, problem: { field, reason } });
	const { repo, enabled, paths, command, timeoutMinutes = 0, secretNames, secrets } = request ?? {};
	if (typeof repo !== "string" || repo.length > MAX_DB_TEXT || UNSAFE_CHAR.test(repo)) return bad("repo");
	if (typeof enabled !== "boolean") return bad("enabled");
	if (!isStringList(paths)) return bad("paths");
	if (paths.length > MAX_DB_PATHS) return bad("paths", "too_many");
	const pathAt = paths.findIndex((path) => path === "" || path.length > MAX_DB_TEXT || UNSAFE_CHAR.test(path));
	if (pathAt >= 0) return bad(`paths[${pathAt}]`);
	if (typeof command !== "string") return bad("command");
	const line = command.trim();
	if (Array.from(line).length > MAX_DB_COMMAND) return bad("command", "too_long");
	if (COMMAND_CONTROL.test(line)) return bad("command", "control");
	if (COMMAND_BIDI.test(line)) return bad("command", "bidi");
	if (typeof timeoutMinutes !== "number" || !Number.isInteger(timeoutMinutes) || timeoutMinutes < 0 || timeoutMinutes > MAX_DB_TIMEOUT_MINUTES) {
		return bad("timeoutMinutes", "out_of_range");
	}
	if (!isStringList(secretNames)) return bad("secretNames");
	if (secretNames.length > MAX_DB_SECRETS) return bad("secretNames", "too_many");
	const nameAt = secretNames.findIndex((name) => !DB_SECRET_NAME.test(name));
	if (nameAt >= 0) return bad(`secretNames[${nameAt}]`);
	const update = secrets === undefined ? {} : record(secrets);
	if (!update) return bad("secrets");
	const set = update.set === undefined ? {} : record(update.set);
	const remove = update.remove === undefined ? [] : update.remove;
	if (!set) return bad("secrets.set");
	if (!isStringList(remove) || remove.some((name) => !DB_SECRET_NAME.test(name))) return bad("secrets.remove");
	const values: Record<string, string> = {};
	for (const name of Object.keys(set).sort()) {
		const value = set[name];
		const field = `secrets.set.${name}`;
		if (!secretNames.includes(name) || remove.includes(name)) return bad(field);
		if (typeof value !== "string" || value === "") return bad(field, "required");
		if (Buffer.byteLength(value, "utf8") > MAX_DB_SECRET_VALUE_BYTES) return bad(field, "too_long");
		if (/[\u0000\r\n]/.test(value)) return bad(field, "control");
		values[name] = value;
	}
	return {
		ok: true,
		change: {
			repo,
			enabled,
			paths: [...paths],
			command: line,
			timeoutMinutes,
			secretNames: [...secretNames],
			secrets: { set: values, remove: [...new Set(remove)] },
		},
	};
}

// What a settings change changes, one line each, in the settings' order; a
// secret's value is never named, only that it is set. None when nothing
// changes.
function dbSettingsLines(t: Translate, current: DbRepoSettings, next: DbSettingsChange): string[] {
	const lines: string[] = [];
	const list = (values: readonly string[]) => (values.length > 0 ? values.map((value) => excerpt(value, MAX_EXAMPLE_CHARS)).join(", ") : "—");
	const pathsChanged = !sameList(current.paths, next.paths);
	if (next.enabled && !current.enabled) lines.push(t("lado.presence.dbChanges.turnOn"));
	if (!next.enabled && current.enabled) lines.push(t("lado.presence.dbChanges.turnOff"));
	if (pathsChanged) lines.push(t("lado.presence.dbChanges.paths", { from: list(current.paths), to: list(next.paths) }));
	if (current.command !== next.command) {
		// The command is not cut: the dialog shows all it runs.
		lines.push(t("lado.presence.dbChanges.command", { from: oneLine(current.command) || "—", to: next.command || "—" }));
	}
	const minutes = (value: number) => value || DB_DEFAULT_TIMEOUT_MINUTES;
	if (minutes(current.timeoutMinutes) !== minutes(next.timeoutMinutes)) {
		lines.push(t("lado.presence.dbChanges.timeout", { from: minutes(current.timeoutMinutes), to: minutes(next.timeoutMinutes) }));
	}
	const added = next.secretNames.filter((name) => !current.secretNames.includes(name));
	const dropped = current.secretNames.filter((name) => !next.secretNames.includes(name));
	if (added.length > 0) lines.push(t("lado.presence.dbChanges.secretNamesAdded", { names: list(added) }));
	if (dropped.length > 0) lines.push(t("lado.presence.dbChanges.secretNamesRemoved", { names: list(dropped) }));
	const set = Object.keys(next.secrets.set);
	if (set.length > 0) lines.push(t("lado.presence.dbChanges.secretsSet", { names: list(set) }));
	const removed = next.secrets.remove.filter((name) => current.storedSecretNames.includes(name) && !dropped.includes(name));
	if (removed.length > 0) lines.push(t("lado.presence.dbChanges.secretsRemoved", { names: list(removed) }));
	// Turning on, or changing the paths while on, records the default
	// branch's matching files as applied.
	if (next.enabled && (!current.enabled || pathsChanged)) lines.push(t("lado.presence.dbChanges.baseline"));
	return lines;
}

// The confirmation of applying a pull request's SQL: the pull request, its
// repository and head, each file with whether it runs, and what happens next.
function dbApplyDetail(t: Translate, pr: DbPRView, files: readonly DbFileView[], trigger: string, mergeOnly: boolean): string {
	const head = [
		t("lado.presence.dbApply.pr", { url: excerpt(pr.prUrl, MAX_DB_TEXT) }),
		...(pr.repo ? [t("lado.presence.dbApply.repo", { repo: excerpt(pr.repo, MAX_EXAMPLE_CHARS) })] : []),
		t("lado.presence.integration.head", { head: pr.headSha.slice(0, 12) }),
	];
	const lines = files.slice(0, MAX_DB_FILES_SHOWN).map((file) => {
		const path = excerpt(file.path, MAX_EXAMPLE_CHARS);
		switch (file.status) {
			case "applied":
				return t("lado.presence.dbApply.fileApplied", { path });
			case "failed":
				return t("lado.presence.dbApply.fileFailed", { path });
			case "interrupted":
				return t("lado.presence.dbApply.fileInterrupted", { path });
			default:
				return t("lado.presence.dbApply.fileRuns", { path });
		}
	});
	if (files.length > MAX_DB_FILES_SHOWN) lines.push(t("lado.presence.integration.dbMore", { count: files.length - MAX_DB_FILES_SHOWN }));
	const next = mergeOnly
		? t("lado.presence.dbApply.nextMergeOnly")
		: trigger === "approval"
			? t("lado.presence.dbApply.nextApproval")
			: trigger === "after_merge"
				? t("lado.presence.dbApply.nextAfterDeploy")
				: t("lado.presence.dbApply.nextMerged");
	const blocks = [head.join("\n"), lines.join("\n"), next];
	// Whether an interrupted command took effect is unknown (#97 decision 7).
	if (files.some((file) => file.status === "interrupted")) blocks.push(t("lado.presence.dbApply.interruptedWarning"));
	return blocks.join("\n\n");
}

// The project's production database change settings, from
// LadoDBChangeSettingsResponse: the response to hand the renderer and each
// repository's settings as the dialog compares them.
function dbChangeSettings(body: unknown, projectId: string): { response: LadoPresenceDbChangeSettings; repositories: DbRepoSettings[] } | null {
	const settings = record(body);
	if (settings?.projectId !== projectId || typeof settings.sha256 !== "string" || !Array.isArray(settings.repositories)) return null;
	const repositories: DbRepoSettings[] = [];
	for (const value of settings.repositories) {
		const repo = record(value);
		if (
			typeof repo?.repo !== "string" ||
			typeof repo.name !== "string" ||
			typeof repo.enabled !== "boolean" ||
			!isStringList(repo.paths) ||
			typeof repo.command !== "string" ||
			typeof repo.timeoutMinutes !== "number" ||
			!isStringList(repo.secretNames) ||
			!isStringList(repo.storedSecretNames) ||
			typeof repo.sha256 !== "string"
		) {
			return null;
		}
		repositories.push({
			repo: repo.repo,
			name: oneLine(repo.name),
			enabled: repo.enabled,
			paths: repo.paths,
			command: repo.command,
			timeoutMinutes: repo.timeoutMinutes,
			secretNames: repo.secretNames,
			storedSecretNames: repo.storedSecretNames,
			sha256: repo.sha256,
		});
	}
	return { response: settings as LadoPresenceDbChangeSettings, repositories };
}

// A run's pull requests with database changes, from LadoDBChangeExecutionResponse.
function dbExecutionPRs(body: unknown, sessionId: string): DbPRView[] | null {
	const view = record(body);
	if (view?.sessionId !== sessionId || !Array.isArray(view.prs)) return null;
	const prs: DbPRView[] = [];
	for (const value of view.prs) {
		const pr = record(value);
		const { prUrl, headSha, repo = "", files, runs } = pr ?? {};
		if (typeof prUrl !== "string" || typeof headSha !== "string" || typeof repo !== "string" || !Array.isArray(files) || !Array.isArray(runs)) {
			return null;
		}
		const fileViews: DbFileView[] = [];
		for (const entry of files) {
			const file = record(entry);
			const [ref] = dbFileRefs([file]);
			if (!ref || typeof file?.status !== "string") return null;
			fileViews.push({ ...ref, status: file.status });
		}
		const runViews: DbRunView[] = [];
		for (const entry of runs) {
			const run = record(entry);
			const { headSha: runHead, kind, trigger, state } = run ?? {};
			if (typeof runHead !== "string" || typeof kind !== "string" || typeof trigger !== "string" || typeof state !== "string") return null;
			runViews.push({ headSha: runHead.toLowerCase(), kind, trigger, state });
		}
		prs.push({ prUrl, headSha: headSha.toLowerCase(), repo: oneLine(repo), files: fileViews, runs: runViews });
	}
	return prs;
}

function isDbPhase(value: unknown): value is LadoPresenceDbChangePhase {
	return typeof value === "string" && (DB_PHASES as readonly string[]).includes(value);
}

function sameList(a: readonly string[], b: readonly string[]): boolean {
	return a.length === b.length && a.every((value, index) => value === b[index]);
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
