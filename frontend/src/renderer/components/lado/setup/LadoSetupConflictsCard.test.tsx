import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import type { LadoRuleImpact } from "../../../lib/lado";
import {
	CLAUDE_DIR,
	daemonCalls,
	routeDaemon,
	setupChangeResult,
	setupConflict,
	setupContent,
	setupInventory,
	setupItem,
	setupItemDetail,
	setupItemRecord,
	type DaemonRoutes,
} from "../../../test/lado-setup-fixtures";
import { LadoSetupConflictsCard } from "./LadoSetupConflictsCard";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

const inventory = setupInventory({
	items: [
		setupItem({ itemId: "item-perms", kind: "setting", name: "permissions" }),
		setupItem({ itemId: "item-hooks", kind: "setting", name: "hooks" }),
		setupItem({ itemId: "item-ao", kind: "mcp_server", name: "ao" }),
		setupItem({ itemId: "item-off", kind: "setting", name: "env", global: "off" }),
	],
});

// In the daemon's order, which is not the severity order.
const note = setupConflict({ code: "LADO_SETUP_HOOK_SHARES_AO_EVENT", severity: "info", itemId: "item-hooks", detail: "Stop", details: { event: "Stop" } });
const warning = setupConflict({
	code: "LADO_SETUP_DENIES_GIT",
	severity: "warning",
	itemId: "item-perms",
	detail: "Bash(git push:*)",
	details: { rule: "Bash(git push:*)" },
});
const accepted = setupConflict({
	code: "LADO_SETUP_HOOK_ADDS_PROMPT_TEXT",
	severity: "warning",
	itemId: "item-hooks",
	detail: "SessionStart",
	details: { event: "SessionStart" },
	acceptanceId: "acceptance-1",
});
const blocking = setupConflict({ code: "LADO_SETUP_MCP_NAME_TAKEN", severity: "blocking", itemId: "item-ao" });
const leftInPlace = setupConflict({
	code: "LADO_SETUP_DENIES_AO",
	severity: "blocking",
	path: `${CLAUDE_DIR}/settings.json#permissions`,
	details: { rule: "Bash(ao:*)" },
});

function serve(routes: DaemonRoutes = {}) {
	routeDaemon(apiClient, routes);
}

function renderCard(conflicts = [note, warning, accepted, blocking, leftInPlace]) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const card = (shown: typeof conflicts) => (
		<QueryClientProvider client={client}>
			<LadoSetupConflictsCard conflicts={shown} inventory={inventory} />
		</QueryClientProvider>
	);
	const view = render(card(conflicts));
	return { ...view, showConflicts: (shown: typeof conflicts) => view.rerender(card(shown)) };
}

function conflictRow(code: string): HTMLElement {
	const row = document.querySelector<HTMLElement>(`[data-testid="lado-setup-conflict"][data-code="${code}"]`);
	if (!row) throw new Error(`no row for ${code}`);
	return row;
}

// LADO fork: the conflicts of the managed local setup (docs/LJY-PRD/HARNESS.md §10).
describe("LadoSetupConflictsCard", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("lists blocking conflicts first, then warnings, then notes", () => {
		serve();
		renderCard();

		const rows = screen.getAllByTestId("lado-setup-conflict");
		expect(rows.map((row) => row.dataset.severity)).toEqual(["blocking", "blocking", "warning", "warning", "info"]);
		expect(rows.map((row) => row.dataset.code)).toEqual([
			"LADO_SETUP_MCP_NAME_TAKEN",
			"LADO_SETUP_DENIES_AO",
			"LADO_SETUP_DENIES_GIT",
			"LADO_SETUP_HOOK_ADDS_PROMPT_TEXT",
			"LADO_SETUP_HOOK_SHARES_AO_EVENT",
		]);
		const ao = conflictRow("LADO_SETUP_MCP_NAME_TAKEN");
		expect(ao).toHaveTextContent("Blocks the run · An MCP server named ao takes the name of AO's own tool server.");
		expect(within(ao).getByText("Claude Code · MCP server · ao")).toBeInTheDocument();
		// Setup without an item names where it lives.
		expect(within(conflictRow("LADO_SETUP_DENIES_AO")).getByText(".claude/settings.json#permissions")).toBeInTheDocument();
		expect(conflictRow("LADO_SETUP_HOOK_SHARES_AO_EVENT")).toHaveTextContent("Note · A hook runs on Stop, where AO runs its own hook too.");
	});

	it("offers Accept only for a warning not accepted yet", () => {
		serve();
		renderCard();

		expect(within(conflictRow("LADO_SETUP_DENIES_GIT")).getByRole("button", { name: /^Accept this warning for / })).toBeEnabled();
		const done = conflictRow("LADO_SETUP_HOOK_ADDS_PROMPT_TEXT");
		expect(within(done).queryByRole("button", { name: /^Accept this warning for / })).not.toBeInTheDocument();
		expect(within(done).getByText("Claude Code · Setting · hooks · Accepted")).toBeInTheDocument();
		for (const code of ["LADO_SETUP_MCP_NAME_TAKEN", "LADO_SETUP_DENIES_AO", "LADO_SETUP_HOOK_SHARES_AO_EVENT"]) {
			expect(within(conflictRow(code)).queryByRole("button", { name: /^Accept this warning for / })).not.toBeInTheDocument();
		}
		// Only an item that is on at Global can be turned off there.
		expect(within(conflictRow("LADO_SETUP_MCP_NAME_TAKEN")).getByRole("button", { name: /^Turn off / })).toBeEnabled();
		expect(within(conflictRow("LADO_SETUP_DENIES_AO")).queryByRole("button", { name: /^Turn off / })).not.toBeInTheDocument();
	});

	it("accepts a warning for its current content at Global after a dry run", async () => {
		const user = userEvent.setup();
		serve({
			"POST /api/v1/lado/setup/conflicts/acceptances": ({ body }) => ({
				data: setupChangeResult({ op: "accept", applied: !(body as { dryRun?: boolean }).dryRun }),
			}),
		});
		renderCard();

		await user.click(within(conflictRow("LADO_SETUP_DENIES_GIT")).getByRole("button", { name: /^Accept this warning for / }));

		await waitFor(() => expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/conflicts/acceptances")).toHaveLength(2));
		const acceptance = {
			itemId: "item-perms",
			code: "LADO_SETUP_DENIES_GIT",
			detail: "Bash(git push:*)",
			scope: "global",
			scopeId: "",
		};
		expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/conflicts/acceptances").map((call) => call.body)).toEqual([
			{ ...acceptance, dryRun: true },
			acceptance,
		]);
	});

	it("turns the item off at Global without the desktop app", async () => {
		const user = userEvent.setup();
		serve({
			"PUT /api/v1/lado/setup/bindings": ({ body }) => ({
				data: setupChangeResult({ applied: !(body as { dryRun?: boolean }).dryRun }),
			}),
		});
		const changeSetup = vi.spyOn(aoBridge.lado.presence, "changeSetup");
		renderCard();

		await user.click(within(conflictRow("LADO_SETUP_MCP_NAME_TAKEN")).getByRole("button", { name: /^Turn off / }));

		await waitFor(() => expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")).toHaveLength(2));
		const binding = { itemId: "item-ao", scope: "global", scopeId: "", state: "off" };
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings").map((call) => call.body)).toEqual([
			{ ...binding, dryRun: true },
			binding,
		]);
		expect(changeSetup).not.toHaveBeenCalled();
	});

	it("names the approved tasks a change reaches before making it", async () => {
		const user = userEvent.setup();
		const impact: LadoRuleImpact = {
			projects: [{ id: "demo", name: "Demo" }],
			tasks: [{ id: "lado-task-1", projectId: "demo", title: "Add login", issueRef: "github:acme/demo#12" }],
		};
		serve({
			"PUT /api/v1/lado/setup/bindings": ({ body }) => ({
				data: setupChangeResult({ applied: !(body as { dryRun?: boolean }).dryRun, impact }),
			}),
		});
		renderCard();

		await user.click(within(conflictRow("LADO_SETUP_MCP_NAME_TAKEN")).getByRole("button", { name: /^Turn off / }));
		const dialog = await screen.findByRole("dialog", { name: "Change the local setup for approved tasks?" });
		expect(within(dialog).getByText("Add login")).toBeInTheDocument();
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")).toHaveLength(1);

		await user.click(within(dialog).getByRole("button", { name: "Change" }));

		await waitFor(() => expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")).toHaveLength(2));
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")[1].body).toEqual({
			itemId: "item-ao",
			scope: "global",
			scopeId: "",
			state: "off",
		});
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("shows a failed change under its conflict", async () => {
		const user = userEvent.setup();
		serve({
			"POST /api/v1/lado/setup/conflicts/acceptances": () => ({
				error: { code: "LADO_SETUP_CONFLICT_NOT_FOUND", message: "no such result" },
			}),
		});
		renderCard();

		await user.click(within(conflictRow("LADO_SETUP_DENIES_GIT")).getByRole("button", { name: /^Accept this warning for / }));

		expect(await within(conflictRow("LADO_SETUP_DENIES_GIT")).findByRole("alert")).toHaveTextContent(
			"This item's content does not give that warning. Check the conflicts again.",
		);
	});

	it("renders nothing without conflicts", () => {
		serve();
		renderCard([]);

		expect(screen.queryByTestId("lado-setup-conflicts")).not.toBeInTheDocument();
	});

	it("names the item each repeated action is about", () => {
		serve();
		renderCard();

		expect(within(conflictRow("LADO_SETUP_MCP_NAME_TAKEN")).getByRole("button", { name: "Turn off ao" })).toBeEnabled();
		expect(within(conflictRow("LADO_SETUP_DENIES_GIT")).getByRole("button", { name: "Accept this warning for permissions" })).toBeEnabled();
		expect(within(conflictRow("LADO_SETUP_HOOK_ADDS_PROMPT_TEXT")).getByRole("button", { name: "Revoke the acceptance for hooks" })).toBeEnabled();
	});

	it("revokes an accepted warning after a dry run, without the desktop app", async () => {
		const user = userEvent.setup();
		const path = "/api/v1/lado/setup/conflicts/acceptances/{acceptanceId}";
		serve({
			[`DELETE ${path}`]: ({ params }) => ({
				data: setupChangeResult({ op: "revoke", applied: !(params?.query as { dryRun?: boolean } | undefined)?.dryRun }),
			}),
		});
		const changeSetup = vi.spyOn(aoBridge.lado.presence, "changeSetup");
		renderCard();

		await user.click(within(conflictRow("LADO_SETUP_HOOK_ADDS_PROMPT_TEXT")).getByRole("button", { name: "Revoke the acceptance for hooks" }));

		await waitFor(() => expect(daemonCalls(apiClient, "DELETE", path)).toHaveLength(2));
		expect(daemonCalls(apiClient, "DELETE", path).map((call) => call.params)).toEqual([
			{ path: { acceptanceId: "acceptance-1" }, query: { dryRun: true } },
			{ path: { acceptanceId: "acceptance-1" }, query: {} },
		]);
		expect(changeSetup).not.toHaveBeenCalled();
	});

	it("keeps the editor it opened when the last conflict goes away", async () => {
		const user = userEvent.setup();
		serve({
			"GET /api/v1/lado/setup/items/{itemId}": () => ({
				data: setupItemDetail(setupItemRecord({ id: "item-ao", kind: "mcp_server", name: "ao" }), [
					{ content: setupContent({ contentKind: "json", text: '{ "type": "http" }' }) },
				]),
			}),
		});
		const { showConflicts } = renderCard([blocking]);

		await user.click(within(conflictRow("LADO_SETUP_MCP_NAME_TAKEN")).getByRole("button", { name: "Edit ao" }));
		expect(await screen.findByRole("dialog", { name: "Edit ao" })).toBeInTheDocument();

		// Fixing the item ends its conflict while the editor is still open.
		showConflicts([]);

		expect(screen.queryByTestId("lado-setup-conflicts")).not.toBeInTheDocument();
		expect(screen.getByRole("dialog", { name: "Edit ao" })).toBeInTheDocument();
	});
});
