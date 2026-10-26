import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import {
	adoptionPreview,
	adoptResult,
	CLAUDE_DIR,
	CODEX_HOME,
	daemonCalls,
	MANAGED,
	routeDaemon,
	setupChangeResult,
	setupEntry,
	setupInventory,
	setupItem,
	type DaemonRoutes,
} from "../../test/lado-setup-fixtures";
import { LocalSetupSection } from "./LocalSetupSection";

vi.mock("../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

const inventory = setupInventory({
	entries: [
		setupEntry({
			tool: "claude-code",
			kind: "instructions",
			name: "CLAUDE.md",
			status: "managed",
			location: "managed",
			path: `${MANAGED}/claude/CLAUDE.md`,
			itemId: "item-claude-md",
		}),
		setupEntry({
			tool: "claude-code",
			kind: "hook_script",
			name: "notify.sh",
			status: "managed",
			location: "managed",
			path: `${MANAGED}/claude/hooks/notify.sh`,
			itemId: "item-hook",
		}),
		setupEntry({ tool: "claude-code", kind: "setting", name: "permissions", status: "adoptable", path: `${CLAUDE_DIR}/settings.json#permissions`, defaultAction: "move" }),
		setupEntry({ tool: "claude-code", kind: "skill", name: "review", status: "adoptable", path: `${CLAUDE_DIR}/skills/review`, defaultAction: "move" }),
		setupEntry({ tool: "claude-code", kind: "skill", name: "linked", status: "not_adoptable", reason: "symlink", path: `${CLAUDE_DIR}/skills/linked` }),
		setupEntry({ tool: "claude-code", name: ".credentials.json", status: "never", reason: "credentials", path: `${CLAUDE_DIR}/.credentials.json` }),
		setupEntry({
			tool: "claude-code",
			kind: "instructions",
			name: "CLAUDE.md",
			status: "repo_tracked",
			reason: "repo_tracked",
			location: "project",
			projectId: "demo",
			path: "/work/demo/CLAUDE.md",
			tracked: true,
		}),
		setupEntry({ tool: "codex", kind: "instructions", name: "AGENTS.md", status: "adoptable", path: `${CODEX_HOME}/AGENTS.md`, defaultAction: "move" }),
		setupEntry({ tool: "codex", kind: "mcp_server", name: "notion", status: "not_adoptable", reason: "literal_credentials", path: `${CODEX_HOME}/config.toml#mcp_servers.notion` }),
		setupEntry({ tool: "codex", name: "auth.json", status: "never", reason: "credentials", path: `${CODEX_HOME}/auth.json` }),
	],
	items: [
		setupItem({ itemId: "item-claude-md", kind: "instructions", name: "CLAUDE.md" }),
		setupItem({ itemId: "item-hook", kind: "hook_script", name: "notify.sh", global: "off" }),
	],
	projects: [{ id: "demo", name: "demo", path: "/work/demo", error: "" }],
});

function serve(routes: DaemonRoutes = {}) {
	routeDaemon(apiClient, {
		"GET /api/v1/lado/setup/inventory": () => ({ data: inventory }),
		"GET /api/v1/lado/setup/conflicts": () => ({ data: { conflicts: [] } }),
		...routes,
	});
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LocalSetupSection />
		</QueryClientProvider>,
	);
}

function toolCard(tool: string): HTMLElement {
	const card = document.querySelector<HTMLElement>(`[data-testid="lado-setup-tool"][data-tool="${tool}"]`);
	if (!card) throw new Error(`no card for ${tool}`);
	return card;
}

function entryRow(path: string): HTMLElement {
	const row = document.querySelector<HTMLElement>(`[data-testid="lado-setup-entry"][data-path="${path}"]`);
	if (!row) throw new Error(`no row for ${path}`);
	return row;
}

// LADO fork: Settings → Local setup (docs/LJY-PRD/HARNESS.md §9).
describe("LocalSetupSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("says it is loading until the inventory arrives", async () => {
		serve({ "GET /api/v1/lado/setup/inventory": () => new Promise(() => undefined) });
		renderSection();

		expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
		expect(screen.getByRole("button", { name: "Scan again" })).toBeDisabled();
	});

	it("shows the daemon's error in the developer's language", async () => {
		serve({
			"GET /api/v1/lado/setup/inventory": () => ({ error: { code: "LADO_SETUP_UNAVAILABLE", message: "no setup service" } }),
		});
		renderSection();

		expect(await screen.findByRole("alert")).toHaveTextContent("Local setup is not available in this AO daemon.");
		expect(document.querySelector('[data-testid="lado-setup-tool"]')).toBeNull();
	});

	it("says when neither tool has setup LADO could manage", async () => {
		serve({
			"GET /api/v1/lado/setup/inventory": () => ({
				data: setupInventory({
					entries: [
						setupEntry({ tool: "claude-code", name: ".credentials.json", status: "never", reason: "credentials", path: `${CLAUDE_DIR}/.credentials.json` }),
						setupEntry({ tool: "codex", name: "history.jsonl", status: "never", reason: "app_state", path: `${CODEX_HOME}/history.jsonl` }),
					],
				}),
			}),
		});
		renderSection();

		expect(await screen.findByText("No Claude Code or Codex setup was found on this computer.")).toBeInTheDocument();
		expect(document.querySelector('[data-testid="lado-setup-tool"]')).toBeNull();
		// Nothing to adopt, so there is no Adopt either.
		expect(screen.queryByRole("button", { name: "Adopt…" })).not.toBeInTheDocument();
	});

	it("groups each tool's setup by kind with its state, reason and place", async () => {
		const user = userEvent.setup();
		serve();
		renderSection();

		await waitFor(() => toolCard("claude-code"));
		const claude = toolCard("claude-code");
		expect(within(claude).getByRole("heading", { name: "Claude Code" })).toBeInTheDocument();
		const groups = [...claude.querySelectorAll<HTMLElement>('[data-testid="lado-setup-kind"]')];
		expect(groups.map((group) => group.dataset.kind)).toEqual(["instructions", "setting", "skill", "hook_script"]);
		expect(within(claude).getByRole("group", { name: "Skills" })).toContainElement(entryRow(`${CLAUDE_DIR}/skills/review`));
		expect(within(claude).getByRole("group", { name: "Hook scripts" })).toContainElement(entryRow(`${MANAGED}/claude/hooks/notify.sh`));

		const managed = entryRow(`${MANAGED}/claude/CLAUDE.md`);
		expect(within(managed).getByText("Managed · On")).toBeInTheDocument();
		expect(within(managed).getByText("lado/harness/claude/CLAUDE.md")).toHaveClass("font-mono");
		expect(within(entryRow(`${MANAGED}/claude/hooks/notify.sh`)).getByText("Managed · Off")).toBeInTheDocument();
		const review = entryRow(`${CLAUDE_DIR}/skills/review`);
		expect(within(review).getByText("Adoptable")).toBeInTheDocument();
		expect(within(review).getByText(".claude/skills/review")).toBeInTheDocument();
		expect(within(review).queryByRole("switch")).not.toBeInTheDocument();
		expect(within(entryRow(`${CLAUDE_DIR}/settings.json#permissions`)).getByText(".claude/settings.json#permissions")).toBeInTheDocument();
		expect(
			within(entryRow(`${CLAUDE_DIR}/skills/linked`)).getByText(
				"Not adoptable · It is a symbolic link or holds one, and LADO never adopts through a link.",
			),
		).toBeInTheDocument();

		const codex = toolCard("codex");
		expect(within(codex).getByRole("heading", { name: "Codex" })).toBeInTheDocument();
		expect(
			within(entryRow(`${CODEX_HOME}/config.toml#mcp_servers.notion`)).getByText(
				"Not adoptable · It holds a secret in plain text, which would end up on a command line.",
			),
		).toBeInTheDocument();

		// What LADO leaves alone waits collapsed under each card.
		const others = within(claude).getByRole("button", { name: /^Not managed/ });
		expect(others).toHaveAttribute("aria-expanded", "false");
		expect(others).toHaveTextContent("2");
		expect(document.querySelector(`[data-path="${CLAUDE_DIR}/.credentials.json"]`)).toBeNull();
		await user.click(others);
		expect(within(entryRow(`${CLAUDE_DIR}/.credentials.json`)).getByText("Never adopted · Logins stay where the tools keep them.")).toBeInTheDocument();
		const repoFile = entryRow("/work/demo/CLAUDE.md");
		expect(within(repoFile).getByText("Repository file")).toBeInTheDocument();
		expect(within(repoFile).getByText("demo/CLAUDE.md")).toBeInTheDocument();
	});

	it("turns a managed item off at Global after a dry run", async () => {
		const user = userEvent.setup();
		serve({
			"PUT /api/v1/lado/setup/bindings": ({ body }) => ({
				data: setupChangeResult({ applied: !(body as { dryRun?: boolean }).dryRun }),
			}),
		});
		const changeSetup = vi.spyOn(aoBridge.lado.presence, "changeSetup");
		renderSection();

		await waitFor(() => entryRow(`${MANAGED}/claude/CLAUDE.md`));
		const toggle = within(entryRow(`${MANAGED}/claude/CLAUDE.md`)).getByRole("switch", { name: "CLAUDE.md for every session" });
		expect(toggle).toBeChecked();
		await user.click(toggle);

		await waitFor(() => expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings")).toHaveLength(2));
		const binding = { itemId: "item-claude-md", scope: "global", scopeId: "", state: "off" };
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings").map((call) => call.body)).toEqual([
			{ ...binding, dryRun: true },
			binding,
		]);
		// Turning off never needs the desktop app.
		expect(changeSetup).not.toHaveBeenCalled();
		await waitFor(() => expect(daemonCalls(apiClient, "GET", "/api/v1/lado/setup/inventory")).toHaveLength(2));
	});

	it("turns an item that can run commands on through the desktop confirmation", async () => {
		const user = userEvent.setup();
		serve({
			"PUT /api/v1/lado/setup/bindings": () => ({ data: setupChangeResult({ needsPresence: true }) }),
		});
		const changeSetup = vi
			.spyOn(aoBridge.lado.presence, "changeSetup")
			.mockResolvedValue({ status: "changed", change: setupChangeResult({ applied: true, actor: "human" }) });
		renderSection();

		await waitFor(() => entryRow(`${MANAGED}/claude/hooks/notify.sh`));
		const toggle = within(entryRow(`${MANAGED}/claude/hooks/notify.sh`)).getByRole("switch", { name: "notify.sh for every session" });
		expect(toggle).not.toBeChecked();
		await user.click(toggle);

		await waitFor(() =>
			expect(changeSetup).toHaveBeenCalledWith({
				method: "PUT",
				path: "/api/v1/lado/setup/bindings",
				body: { itemId: "item-hook", scope: "global", scopeId: "", state: "on" },
			}),
		);
		// The renderer only asked what the change does; the desktop app made it.
		expect(daemonCalls(apiClient, "PUT", "/api/v1/lado/setup/bindings").map((call) => call.body)).toEqual([
			{ itemId: "item-hook", scope: "global", scopeId: "", state: "on", dryRun: true },
		]);
	});

	it("deletes a managed item after its confirmation", async () => {
		const user = userEvent.setup();
		serve({
			"DELETE /api/v1/lado/setup/items/{itemId}": ({ params }) => ({
				data: setupChangeResult({ op: "delete", applied: !params?.query?.dryRun }),
			}),
		});
		renderSection();

		await waitFor(() => entryRow(`${MANAGED}/claude/CLAUDE.md`));
		await user.click(within(entryRow(`${MANAGED}/claude/CLAUDE.md`)).getByRole("button", { name: "Actions for CLAUDE.md" }));
		await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
		const dialog = await screen.findByRole("dialog", { name: "Delete CLAUDE.md?" });
		expect(daemonCalls(apiClient, "DELETE", "/api/v1/lado/setup/items/{itemId}")).toHaveLength(0);

		await user.click(within(dialog).getByRole("button", { name: "Delete" }));

		await waitFor(() => expect(daemonCalls(apiClient, "DELETE", "/api/v1/lado/setup/items/{itemId}")).toHaveLength(2));
		expect(daemonCalls(apiClient, "DELETE", "/api/v1/lado/setup/items/{itemId}").map((call) => call.params)).toEqual([
			{ path: { itemId: "item-claude-md" }, query: { dryRun: true } },
			{ path: { itemId: "item-claude-md" }, query: {} },
		]);
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("keeps adoption off on an OS LADO has not checked, and says why", async () => {
		serve({
			"GET /api/v1/lado/setup/inventory": () => ({
				data: { ...inventory, os: "windows", verified: { "claude-code": false, codex: false } },
			}),
		});
		renderSection();

		const adopt = await screen.findByRole("button", { name: "Adopt…" });
		expect(adopt).toBeDisabled();
		const reason = screen.getByTestId("lado-setup-os");
		expect(reason).toHaveTextContent(
			"Adoption is off on this OS (Windows): LADO has not yet checked that sessions get the adopted setup here.",
		);
		expect(adopt).toHaveAttribute("aria-describedby", reason.id);
	});

	it("is quiet about the OS when LADO checked it, and opens the adoption", async () => {
		const user = userEvent.setup();
		serve({
			"POST /api/v1/lado/setup/adoptions": () => ({ data: adoptResult({ preview: adoptionPreview() }) }),
		});
		renderSection();

		const adopt = await screen.findByRole("button", { name: "Adopt…" });
		expect(screen.queryByTestId("lado-setup-os")).not.toBeInTheDocument();
		await user.click(adopt);

		expect(await screen.findByRole("dialog", { name: "Adopt local setup" })).toBeInTheDocument();
	});

	it("scans again and shows the new scan", async () => {
		const user = userEvent.setup();
		const scanned = { ...inventory, entries: inventory.entries.filter((entry) => entry.name !== "review") };
		serve({ "POST /api/v1/lado/setup/scan": () => ({ data: scanned }) });
		renderSection();

		await waitFor(() => entryRow(`${CLAUDE_DIR}/skills/review`));
		expect(screen.getByText(/^Last scanned /)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Scan again" }));

		await waitFor(() => expect(document.querySelector(`[data-path="${CLAUDE_DIR}/skills/review"]`)).toBeNull());
		expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/scan")).toHaveLength(1);
	});
});
