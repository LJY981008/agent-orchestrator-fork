// @vitest-environment node
import type { BaseWindow, MessageBoxOptions, WebContents } from "electron";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../api/schema";
import {
	LADO_PRESENCE_APPROVE_PLAN_CHANNEL,
	type LadoPresenceApproval,
	type LadoPresenceApproveResult,
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
const BASE = "http://127.0.0.1:3001";
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

type Routes = {
	plan?: () => Response | Promise<Response>;
	approve?: () => Response | Promise<Response>;
};

function setup(overrides: Partial<LadoPresenceDeps> = {}, routes: Routes = {}) {
	type Handler = (event: unknown, input?: unknown) => Promise<LadoPresenceApproveResult>;
	const handlers = new Map<string, Handler>();
	const shell = { id: 1 } as unknown as WebContents;
	const window = { id: 2 } as unknown as BaseWindow;
	const calls: { url: string; init: RequestInit }[] = [];
	const fetch = vi.fn(async (url: string, init: RequestInit) => {
		calls.push({ url, init });
		if (init.method === "GET") return routes.plan ? routes.plan() : json(200, planResponse());
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
		// With these, a confirmed approval of lado-plan-1 signs exactly VECTOR.
		now: () => 1_792_399_940_000,
		randomBytes: (size) => new Uint8Array(size),
		...overrides,
	});
	const handler = handlers.get(LADO_PRESENCE_APPROVE_PLAN_CHANNEL);
	if (!handler) throw new Error("the approvePlan channel is not registered");
	return {
		approve: (input: unknown, sender: unknown = shell) => handler({ sender }, input),
		calls,
		dialog,
		fetch,
		window,
	};
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

		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([["GET", `${BASE}/api/v1/lado/plans/lado-plan-1`]]);
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

	it("signs and sends the approval the human confirmed", async () => {
		const { approve, calls } = setup();
		// The renderer cannot pick the hash or add headers.
		const result = await approve({
			revisionId: "lado-plan-1",
			sha256: "f".repeat(64),
			headers: { [LADO_PRESENCE_HEADER]: "v1.forged" },
		});

		expect(result).toEqual({ status: "approved", approval });
		expect(calls.map(({ url, init }) => [init.method, url])).toEqual([
			["GET", `${BASE}/api/v1/lado/plans/lado-plan-1`],
			["POST", `${BASE}/api/v1/lado/plans/lado-plan-1/approve`],
		]);
		const post = calls[1]!;
		const headers = post.init.headers as Record<string, string>;
		expect(post.init.body).toBe(VECTOR_BODY);
		expect(headers).toEqual({ "content-type": "application/json", [LADO_PRESENCE_HEADER]: VECTOR });
		// The proof covers exactly the path and the bytes that were sent.
		const [, nonce, expires] = VECTOR.split(".");
		expect(
			signLadoPresence(ladoPresenceKey("desktop-token"), "POST", new URL(post.url).pathname, post.init.body as string, nonce!, Number(expires)),
		).toBe(headers[LADO_PRESENCE_HEADER]);
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
		expect(noDialog.calls).toHaveLength(1);
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
			detail: ko["lado.presence.approveDetail"]
				.replace("{{task}}", "Fix the flaky login test")
				.replace("{{issue}}", "github:acme/demo#12")
				.replace("{{revision}}", "3")
				.replace("{{sha}}", "aaaaaaaaaaaa"),
		});
		expect(dialogOptions(dialog).message).not.toBe(trayCatalogs.en["lado.presence.approveTitle"]);

		// A language change applies to the next confirmation.
		locale = "en";
		await approve({ revisionId: "lado-plan-1" });
		expect(dialogOptions(dialog)).toMatchObject({
			message: "Approve this plan?",
			detail: `Fix the flaky login test\ngithub:acme/demo#12, revision 3\nsha256 aaaaaaaaaaaa\n\nLADO starts work only from the plan you approve.`,
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
