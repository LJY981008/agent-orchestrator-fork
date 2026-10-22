// @vitest-environment node
import type { BaseWindow, MessageBoxOptions, WebContents } from "electron";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../api/schema";
import {
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	LADO_PRESENCE_CHANGE_RULE_CHANNEL,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
	type LadoPresenceRule,
	type LadoPresenceRuleChangeResult,
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
		plan: { acceptanceCriteria: [], affectedPaths: [], body: "", reviewRequired: false, steps: [], testCommands: [] },
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

type Route = () => Response | Promise<Response>;
type Routes = { plan?: Route; effective?: Route; approve?: Route; rule?: Route; patch?: Route };

function setup(overrides: Partial<LadoPresenceDeps> = {}, routes: Routes = {}) {
	type Handler = (event: unknown, input?: unknown) => Promise<unknown>;
	const handlers = new Map<string, Handler>();
	const shell = { id: 1 } as unknown as WebContents;
	const window = { id: 2 } as unknown as BaseWindow;
	const calls: { url: string; init: RequestInit }[] = [];
	const fetch = vi.fn(async (url: string, init: RequestInit) => {
		calls.push({ url, init });
		const { pathname } = new URL(url);
		if (init.method === "GET" && pathname.startsWith("/api/v1/lado/plans/")) {
			return routes.plan ? routes.plan() : json(200, planResponse());
		}
		if (init.method === "GET" && pathname === "/api/v1/lado/effective") {
			return routes.effective ? routes.effective() : json(200, effectiveResponse());
		}
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
	if (!approveHandler || !changeRuleHandler) throw new Error("a presence channel is not registered");
	return {
		approve: (input: unknown, sender: unknown = shell) =>
			approveHandler({ sender }, input) as Promise<LadoPresenceApproveResult>,
		changeRule: (input: unknown, sender: unknown = shell) =>
			changeRuleHandler({ sender }, input) as Promise<LadoPresenceRuleChangeResult>,
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
			["no snapshot hash", () => json(200, effectiveResponse({ snapshotSha256: "" })), undefined],
			["a short snapshot hash", () => json(200, effectiveResponse({ snapshotSha256: "c".repeat(63) })), undefined],
			["another task's rules", () => json(200, effectiveResponse({ taskId: "lado-task-2" })), undefined],
			["no rules", () => json(200, { ...effectiveResponse(), rules: undefined }), undefined],
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
