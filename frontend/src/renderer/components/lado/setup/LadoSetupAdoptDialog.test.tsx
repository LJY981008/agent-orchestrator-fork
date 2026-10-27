import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import type { LadoSetupAdoptEntry, LadoSetupAdoptionPreview } from "../../../lib/lado-setup";
import {
	adoptionPreview,
	adoptResult,
	CLAUDE_DIR,
	CODEX_HOME,
	daemonCalls,
	MANAGED,
	routeDaemon,
	setupConflict,
	setupEntry,
	setupInventory,
} from "../../../test/lado-setup-fixtures";
import { LadoSetupAdoptDialog } from "./LadoSetupAdoptDialog";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

const inventory = setupInventory({
	entries: [
		setupEntry({ tool: "claude-code", kind: "setting", name: "hooks", status: "adoptable", path: `${CLAUDE_DIR}/settings.json#hooks`, defaultAction: "move" }),
		setupEntry({ tool: "claude-code", kind: "setting", name: "theme", status: "adoptable", path: `${CLAUDE_DIR}/settings.json#theme`, defaultAction: "keep" }),
		setupEntry({ tool: "claude-code", kind: "hook_script", name: "notify.sh", status: "adoptable", path: `${CLAUDE_DIR}/hooks/notify.sh`, defaultAction: "move" }),
		// Not offered: LADO cannot take it, or already manages its name.
		setupEntry({ tool: "claude-code", kind: "skill", name: "linked", status: "not_adoptable", reason: "symlink", path: `${CLAUDE_DIR}/skills/linked` }),
		setupEntry({ tool: "claude-code", kind: "skill", name: "review", status: "adoptable", path: `${CLAUDE_DIR}/skills/review`, defaultAction: "move" }),
		setupEntry({
			tool: "claude-code",
			kind: "skill",
			name: "review",
			status: "managed",
			location: "managed",
			path: `${MANAGED}/claude/skills/review`,
			itemId: "item-review",
		}),
	],
});

const offered: LadoSetupAdoptEntry[] = [
	{ tool: "claude-code", kind: "setting", name: "hooks", action: "move" },
	{ tool: "claude-code", kind: "setting", name: "theme", action: "keep" },
	{ tool: "claude-code", kind: "hook_script", name: "notify.sh", action: "move" },
];

function previewOf(entries: LadoSetupAdoptEntry[], fields: Partial<LadoSetupAdoptionPreview> = {}): LadoSetupAdoptionPreview {
	return adoptionPreview({
		entries: entries.map((entry) => ({
			...entry,
			delivery: entry.action === "move" ? "home" : "",
			keptInPlace: [],
			managedPath:
				entry.action === "move"
					? entry.kind === "setting"
						? `${MANAGED}/claude/settings.json#${entry.name}`
						: `${MANAGED}/claude/hooks/${entry.name}`
					: "",
			originPath: `${CLAUDE_DIR}/${entry.name}`,
			sha256: "a".repeat(64),
			size: 10,
		})),
		warnings: entries.some((entry) => entry.action === "move") ? ["LADO_SETUP_DIRECT_USE_STOPS"] : [],
		...fields,
	});
}

/** The daemon previews whatever entries it is sent; fields shape every preview. */
function serve(fields: (entries: LadoSetupAdoptEntry[]) => Partial<LadoSetupAdoptionPreview> = () => ({})) {
	routeDaemon(apiClient, {
		"POST /api/v1/lado/setup/adoptions": ({ body }) => {
			const entries = (body as { entries: LadoSetupAdoptEntry[] }).entries;
			return { data: adoptResult({ preview: previewOf(entries, fields(entries)) }) };
		},
	});
}

function renderDialog(onOpenChange = vi.fn(), shown = inventory) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<LadoSetupAdoptDialog open inventory={shown} onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return onOpenChange;
}

function previewBodies() {
	return daemonCalls(apiClient, "POST", "/api/v1/lado/setup/adoptions").map((call) => call.body);
}

function adoptButton(): HTMLElement {
	return screen.getByRole("button", { name: /^Adopt \d+ items?$/ });
}

// LADO fork: adopting local setup (docs/LJY-PRD/HARNESS.md §9).
describe("LadoSetupAdoptDialog", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("shows the items with their actions, the rewrites, the warning and the conflicts", async () => {
		serve(() => ({
			rewrites: [
				{
					setting: "hooks",
					event: "PreToolUse",
					matcher: "Bash",
					before: "~/.claude/hooks/notify.sh",
					after: '"$CLAUDE_CONFIG_DIR"/hooks/notify.sh',
					paths: ["hooks/notify.sh"],
				},
			],
			conflicts: [
				setupConflict({
					code: "LADO_SETUP_HOOK_MAY_BLOCK_TOOLS",
					severity: "warning",
					details: { matcher: "Bash" },
					path: `${CLAUDE_DIR}/settings.json#hooks`,
				}),
			],
		}));
		renderDialog();

		const dialog = await screen.findByRole("dialog", { name: "Adopt local setup" });
		// Every adoptable entry starts chosen with its default action; one LADO
		// already manages the name of is not offered.
		const items = within(dialog).getAllByTestId("lado-setup-adopt-item");
		expect(items.map((item) => item.dataset.name)).toEqual(["hooks", "theme", "notify.sh"]);
		for (const name of ["hooks", "theme", "notify.sh"]) {
			expect(within(dialog).getByRole("checkbox", { name })).toBeChecked();
		}
		expect(within(dialog).getByRole("checkbox", { name: "All items" })).toBeChecked();
		expect(within(dialog).getByRole("button", { name: "What to do with hooks" })).toHaveTextContent("Move");
		expect(within(dialog).getByRole("button", { name: "What to do with theme" })).toHaveTextContent("Keep in place");
		expect(previewBodies()).toEqual([{ entries: offered, dryRun: true }]);

		const hooks = items[0];
		expect(await within(hooks).findByText("lado/harness/claude/settings.json#hooks")).toHaveClass("font-mono");
		expect(within(items[1]).getByText("Stays in place; LADO does not manage it.")).toBeInTheDocument();
		expect(
			within(dialog).getByText(
				"Claude Code and Codex used outside this app no longer load the items that move. LADO keeps a backup, and Restore puts them back.",
			),
		).toBeInTheDocument();

		const rewrites = within(dialog).getByTestId("lado-setup-adopt-rewrites");
		expect(within(rewrites).getByText("hooks · PreToolUse · Bash")).toBeInTheDocument();
		expect(within(rewrites).getByText("~/.claude/hooks/notify.sh")).toBeInTheDocument();
		expect(within(rewrites).getByText('"$CLAUDE_CONFIG_DIR"/hooks/notify.sh')).toBeInTheDocument();

		const conflict = within(dialog).getByTestId("lado-setup-adopt-conflict");
		expect(conflict).toHaveTextContent("Warning · A PreToolUse hook matching Bash can block Bash or AO's tools.");
		await waitFor(() => expect(adoptButton()).toBeEnabled());
		expect(adoptButton()).toHaveTextContent("Adopt 3 items");
	});

	it("lists what is running and keeps Adopt off until it is closed", async () => {
		const user = userEvent.setup();
		let running = true;
		serve(() =>
			running
				? {
						preconditions: {
							ok: false,
							os: "linux",
							sessions: [{ id: "ses-1", tool: "claude-code" }],
							processes: [{ pid: 4242, name: "codex", tool: "codex" }],
							processesIgnored: false,
							unverified: [],
						},
					}
				: {},
		);
		renderDialog();

		const list = await screen.findByTestId("lado-setup-adopt-running");
		expect(within(list).getByText("Session ses-1")).toBeInTheDocument();
		expect(within(list).getByText("codex (PID 4242)")).toBeInTheDocument();
		expect(adoptButton()).toBeDisabled();
		expect(screen.getByTestId("lado-setup-adopt-hold")).toHaveTextContent(
			"Close the sessions and programs listed above, then check again.",
		);

		running = false;
		await user.click(screen.getByRole("button", { name: "Check again" }));

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		expect(screen.queryByTestId("lado-setup-adopt-running")).not.toBeInTheDocument();
		expect(previewBodies()).toHaveLength(2);
	});

	it("keeps Adopt off while a command names a moved file LADO cannot rewrite", async () => {
		serve(() => ({
			conflicts: [
				setupConflict({
					code: "LADO_SETUP_HOOK_PATH_MOVED",
					severity: "blocking",
					details: { path: "hooks/notify.sh" },
					path: `${CLAUDE_DIR}/settings.json#statusLine`,
				}),
			],
		}));
		renderDialog();

		const conflict = await screen.findByTestId("lado-setup-adopt-conflict");
		expect(conflict).toHaveAttribute("data-tone", "blocking");
		expect(conflict).toHaveTextContent("Stops the adoption · A command names hooks/notify.sh, which moves, and LADO cannot rewrite it.");
		expect(adoptButton()).toBeDisabled();
		expect(screen.getByTestId("lado-setup-adopt-hold")).toHaveTextContent(
			"A command names hooks/notify.sh, which would move, and LADO cannot rewrite it. Adopt the setting that holds the command too, or keep the file in place.",
		);
	});

	it("shows other blocking conflicts in the caution tone without stopping the adoption", async () => {
		serve(() => ({
			conflicts: [setupConflict({ code: "LADO_SETUP_MCP_NAME_TAKEN", severity: "blocking", path: `${CLAUDE_DIR}/settings.json#hooks` })],
		}));
		renderDialog();

		const conflict = await screen.findByTestId("lado-setup-adopt-conflict");
		expect(conflict).toHaveAttribute("data-tone", "warning");
		expect(conflict).toHaveTextContent("Blocks the run · An MCP server named ao takes the name of AO's own tool server.");
		await waitFor(() => expect(adoptButton()).toBeEnabled());
	});

	it("needs at least one item that moves", async () => {
		const user = userEvent.setup();
		serve();
		renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(screen.getByRole("checkbox", { name: "hooks" }));
		await user.click(screen.getByRole("checkbox", { name: "notify.sh" }));

		expect(adoptButton()).toBeDisabled();
		expect(screen.getByTestId("lado-setup-adopt-hold")).toHaveTextContent("Choose at least one item to move.");
		await user.click(screen.getByRole("checkbox", { name: "theme" }));
		expect(screen.getByTestId("lado-setup-adopt-hold")).toHaveTextContent("Choose the items to adopt.");
		expect(screen.getByRole("checkbox", { name: "All items" })).not.toBeChecked();
	});

	it("adopts exactly the chosen entries through the desktop confirmation and reports the result", async () => {
		const user = userEvent.setup();
		serve();
		const adoptSetup = vi.spyOn(aoBridge.lado.presence, "adoptSetup").mockResolvedValue({
			status: "adopted",
			adoption: adoptResult({
				adoptionId: "adoption-1",
				applied: true,
				state: "completed",
				items: [
					{ itemId: "item-hooks", tool: "claude-code", kind: "setting", name: "hooks", managedPath: "", sha256: "a".repeat(64), versionId: "v1" },
					{ itemId: "item-notify", tool: "claude-code", kind: "hook_script", name: "notify.sh", managedPath: "", sha256: "a".repeat(64), versionId: "v2" },
				],
				verification: [
					{ check: "managed_copy", itemId: "item-hooks", ok: true, error: "" },
					{ check: "home", itemId: "item-notify", ok: true, error: "" },
				],
			}),
		});
		renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(screen.getByRole("checkbox", { name: "theme" }));
		await user.click(screen.getByRole("button", { name: "What to do with notify.sh" }));
		await user.click(await screen.findByRole("menuitem", { name: "Keep in place" }));

		const chosen: LadoSetupAdoptEntry[] = [
			{ tool: "claude-code", kind: "setting", name: "hooks", action: "move" },
			{ tool: "claude-code", kind: "hook_script", name: "notify.sh", action: "keep" },
		];
		// The preview follows the choices once they settle, and Adopt waits for it.
		await waitFor(() => expect(previewBodies().at(-1)).toEqual({ entries: chosen, dryRun: true }));
		await waitFor(() => expect(adoptButton()).toBeEnabled());
		expect(adoptButton()).toHaveTextContent("Adopt 2 items");
		await user.click(adoptButton());

		await waitFor(() => expect(adoptSetup).toHaveBeenCalledWith(chosen));
		const report = await screen.findByTestId("lado-setup-adopted");
		expect(within(report).getByRole("status")).toHaveTextContent("Adopted 2 items.");
		expect(within(report).getByText("Moved: 1 · Kept in place: 1")).toBeInTheDocument();
		expect(within(report).getByText("Every check passed: the sessions AO starts get these items.")).toBeInTheDocument();
		// The desktop app previewed and signed it; the renderer sent no adoption itself.
		expect(previewBodies().every((body) => (body as { dryRun?: boolean }).dryRun)).toBe(true);
	});

	it("names the checks that failed after an adoption", async () => {
		const user = userEvent.setup();
		serve();
		vi.spyOn(aoBridge.lado.presence, "adoptSetup").mockResolvedValue({
			status: "adopted",
			adoption: adoptResult({
				applied: true,
				items: [{ itemId: "item-hooks", tool: "claude-code", kind: "setting", name: "hooks", managedPath: "", sha256: "a".repeat(64), versionId: "v1" }],
				verification: [{ check: "managed_copy", itemId: "item-hooks", ok: false, error: "hash mismatch" }],
			}),
		});
		const onOpenChange = renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(adoptButton());

		expect(await screen.findByText("Managed copy check failed for hooks: hash mismatch")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Done" }));
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("stays open when the desktop confirmation is cancelled", async () => {
		const user = userEvent.setup();
		serve();
		const adoptSetup = vi.spyOn(aoBridge.lado.presence, "adoptSetup").mockResolvedValue({ status: "cancelled" });
		const onOpenChange = renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(adoptButton());

		await waitFor(() => expect(adoptSetup).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(adoptButton()).toBeEnabled());
		expect(screen.queryByTestId("lado-setup-adopted")).not.toBeInTheDocument();
		expect(onOpenChange).not.toHaveBeenCalled();
	});

	it("says at which stage and path a partial adoption stopped", async () => {
		const user = userEvent.setup();
		serve();
		vi.spyOn(aoBridge.lado.presence, "adoptSetup").mockResolvedValue({
			status: "error",
			code: "LADO_SETUP_ADOPTION_FAILED",
			message: "The adoption stopped while removing originals.",
			details: { stage: "remove", root: "claude_dir", path: "hooks/notify.sh", adoption: "adoption-1", state: "partial" },
		});
		renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(adoptButton());

		const stopped = await screen.findByTestId("lado-setup-adopt-stopped");
		expect(within(stopped).getByRole("alert")).toHaveTextContent(
			"The adoption stopped while removing the originals. What was moved is managed now, and Restore puts it back.",
		);
		expect(within(stopped).getByText("Where it stopped: Removing the originals")).toBeInTheDocument();
		expect(within(stopped).getByText(".claude/hooks/notify.sh")).toHaveAttribute("title", `${CLAUDE_DIR}/hooks/notify.sh`);
	});

	it("shows a refused adoption under the actions and lets the developer try again", async () => {
		const user = userEvent.setup();
		serve();
		vi.spyOn(aoBridge.lado.presence, "adoptSetup").mockResolvedValue({
			status: "error",
			code: "LADO_SETUP_CHANGED",
			message: "The local setup changed.",
		});
		renderDialog();

		await waitFor(() => expect(adoptButton()).toBeEnabled());
		await user.click(adoptButton());

		expect(await screen.findByRole("alert")).toHaveTextContent(
			"The local setup changed since you reviewed it. Review it again, then confirm.",
		);
		// The views refresh, so the preview runs again.
		await waitFor(() => expect(previewBodies()).toHaveLength(2));
		await waitFor(() => expect(adoptButton()).toBeEnabled());
	});

	it("warns when only one of Codex's instruction files moves", async () => {
		const user = userEvent.setup();
		serve();
		renderDialog(
			vi.fn(),
			setupInventory({
				entries: [
					setupEntry({ tool: "codex", kind: "instructions", name: "AGENTS.md", status: "adoptable", path: `${CODEX_HOME}/AGENTS.md`, defaultAction: "move" }),
					setupEntry({
						tool: "codex",
						kind: "instructions",
						name: "AGENTS.override.md",
						status: "adoptable",
						path: `${CODEX_HOME}/AGENTS.override.md`,
						defaultAction: "move",
					}),
				],
			}),
		);

		await screen.findByRole("dialog", { name: "Adopt local setup" });
		expect(screen.queryByTestId("lado-setup-adopt-codex-note")).not.toBeInTheDocument();
		await user.click(screen.getByRole("checkbox", { name: "AGENTS.override.md" }));
		expect(screen.getByTestId("lado-setup-adopt-codex-note")).toHaveTextContent(
			"Codex also reads the instructions file left in its folder, so sessions get both files.",
		);

		await user.click(screen.getByRole("checkbox", { name: "AGENTS.override.md" }));
		await user.click(screen.getByRole("button", { name: "What to do with AGENTS.md" }));
		await user.click(await screen.findByRole("menuitem", { name: "Keep in place" }));
		expect(screen.getByTestId("lado-setup-adopt-codex-note")).toHaveTextContent(
			"Codex still reads instructions kept in place, and LADO adds the same text again.",
		);
	});
});
