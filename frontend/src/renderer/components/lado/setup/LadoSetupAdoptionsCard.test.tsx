import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import type { LadoSetupRestoreResult } from "../../../lib/lado-setup";
import { CLAUDE_DIR, CLAUDE_JSON, setupAdoption, setupInventory } from "../../../test/lado-setup-fixtures";
import { LadoSetupAdoptionsCard } from "./LadoSetupAdoptionsCard";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

const inventory = setupInventory({
	adoptions: [
		setupAdoption({ id: "adoption-old", createdAt: "2026-10-20T09:00:00Z", itemCount: 1, restoredAt: "2026-10-21T09:00:00Z" }),
		setupAdoption({ id: "adoption-partial", createdAt: "2026-10-22T09:00:00Z", itemCount: 2, state: "partial", errorMessage: "remove failed" }),
		setupAdoption({ id: "adoption-new", createdAt: "2026-10-25T09:00:00Z", itemCount: 3 }),
	],
});

const restored: LadoSetupRestoreResult = {
	adoptionId: "adoption-new",
	applied: true,
	restoredAt: "2026-10-26T05:40:00Z",
	warnings: [],
	report: {
		entries: [
			{ itemId: "item-1", tool: "claude-code", kind: "instructions", name: "CLAUDE.md", root: "claude_dir", path: "CLAUDE.md", status: "restored", beside: "", error: "" },
			{
				itemId: "item-2",
				tool: "claude-code",
				kind: "skill",
				name: "review",
				root: "claude_dir",
				path: "skills/review",
				status: "restored_beside",
				beside: "skills/review.lado-restored",
				error: "",
			},
			{
				itemId: "item-3",
				tool: "claude-code",
				kind: "mcp_server",
				name: "search",
				root: "claude_json",
				path: ".claude.json#mcpServers.search",
				status: "key_kept",
				beside: "",
				error: "",
			},
		],
	},
};

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoSetupAdoptionsCard inventory={inventory} />
		</QueryClientProvider>,
	);
}

function adoptionRow(id: string): HTMLElement {
	const row = document.querySelector<HTMLElement>(`[data-testid="lado-setup-adoption"][data-adoption-id="${id}"]`);
	if (!row) throw new Error(`no row for ${id}`);
	return row;
}

// LADO fork: the adoptions of local setup and their restore (docs/LJY-PRD/HARNESS.md §9).
describe("LadoSetupAdoptionsCard", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("lists the adoptions newest first with their items and state", () => {
		renderCard();

		const rows = screen.getAllByTestId("lado-setup-adoption");
		expect(rows.map((row) => row.dataset.adoptionId)).toEqual(["adoption-new", "adoption-partial", "adoption-old"]);
		expect(within(adoptionRow("adoption-new")).getByText("3 items · Completed")).toBeInTheDocument();
		expect(within(adoptionRow("adoption-partial")).getByText("2 items · Stopped partway")).toHaveAttribute("title", "remove failed");
		expect(within(adoptionRow("adoption-old")).getByText(/^1 item · Restored /)).toBeInTheDocument();
		expect(within(adoptionRow("adoption-new")).getByText(/^Adopted /)).toBeInTheDocument();
	});

	it("offers no Restore for an adoption that was restored", () => {
		renderCard();

		expect(within(adoptionRow("adoption-old")).queryByRole("button", { name: "Restore" })).not.toBeInTheDocument();
		expect(within(adoptionRow("adoption-new")).getByRole("button", { name: "Restore" })).toBeEnabled();
		// A partial adoption is undone the same way.
		expect(within(adoptionRow("adoption-partial")).getByRole("button", { name: "Restore" })).toBeEnabled();
	});

	it("restores through the desktop confirmation and reports where each original went", async () => {
		const user = userEvent.setup();
		const restoreSetup = vi.spyOn(aoBridge.lado.presence, "restoreSetup").mockResolvedValue({ status: "restored", restore: restored });
		renderCard();

		await user.click(within(adoptionRow("adoption-new")).getByRole("button", { name: "Restore" }));

		await waitFor(() => expect(restoreSetup).toHaveBeenCalledWith("adoption-new"));
		const report = await within(adoptionRow("adoption-new")).findByTestId("lado-setup-restore-report");
		const entries = within(report).getAllByTestId("lado-setup-restored-entry");
		expect(entries.map((entry) => entry.dataset.status)).toEqual(["restored", "restored_beside", "key_kept"]);
		expect(within(entries[0]).getByText(".claude/CLAUDE.md")).toHaveAttribute("title", `${CLAUDE_DIR}/CLAUDE.md`);
		expect(within(entries[0]).getByText("Put back")).toBeInTheDocument();
		expect(within(entries[1]).getByText("Put beside what is there now")).toBeInTheDocument();
		expect(within(entries[1]).getByText(".claude/skills/review.lado-restored")).toHaveClass("font-mono");
		expect(within(entries[1]).getByText(/^Original now at/)).toHaveAttribute("title", `${CLAUDE_DIR}/skills/review.lado-restored`);
		expect(within(entries[2]).getByText(".claude.json#mcpServers.search")).toHaveAttribute("title", `${CLAUDE_JSON}#mcpServers.search`);
		expect(within(entries[2]).getByText("Kept the value there now")).toBeInTheDocument();
		// The renderer never restored on its own: the desktop app signed it.
		expect(apiClient.POST).not.toHaveBeenCalled();
	});

	it("changes nothing when the desktop confirmation is cancelled", async () => {
		const user = userEvent.setup();
		vi.spyOn(aoBridge.lado.presence, "restoreSetup").mockResolvedValue({ status: "cancelled" });
		renderCard();

		const restore = within(adoptionRow("adoption-new")).getByRole("button", { name: "Restore" });
		await user.click(restore);

		await waitFor(() => expect(restore).toBeEnabled());
		expect(screen.queryByTestId("lado-setup-restore-report")).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("shows a failed restore under its adoption", async () => {
		const user = userEvent.setup();
		vi.spyOn(aoBridge.lado.presence, "restoreSetup").mockResolvedValue({
			status: "error",
			code: "LADO_SETUP_TOOL_RUNNING",
			message: "close them",
			details: { sessions: [{ id: "ses-1", tool: "claude-code" }] },
		});
		renderCard();

		await user.click(within(adoptionRow("adoption-new")).getByRole("button", { name: "Restore" }));

		expect(await within(adoptionRow("adoption-new")).findByRole("alert")).toHaveTextContent(
			"Close these Claude Code and Codex sessions and programs, then try again: ses-1 (Claude Code)",
		);
	});
});
