import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import * as lado from "../../lib/lado";
import { LadoProjectKnowledgeCard } from "./LadoProjectKnowledgeCard";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoKnowledge: vi.fn(),
	importLadoKnowledge: vi.fn(),
	claimLadoKnowledge: vi.fn(),
}));

const dir = "/home/u/.ao/data/lado/knowledge/demo";

const note = (slug: string, state: lado.LadoKnowledgeNoteState, extra: Partial<lado.LadoKnowledgeNote> = {}): lado.LadoKnowledgeNote => ({
	slug,
	title: `Title of ${slug}`,
	kind: "pitfall",
	revision: 2,
	updatedAt: "2026-11-01T00:00:00Z",
	author: "ao-1",
	state,
	...extra,
});

const knowledge: lado.LadoKnowledge = {
	projectId: "demo",
	dir,
	foreign: false,
	notes: [note("a-fresh", "fresh", { kind: "map" }), note("b-stale", "stale"), note("c-plain", "unchecked", { kind: "custom" })],
	truncated: false,
	importable: [
		{ projectId: "old-demo", notes: 3, registered: false, ownerPath: "/home/u/old/demo" },
		{ projectId: "other", notes: 1, registered: true },
	],
};

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoProjectKnowledgeCard projectId="demo" />
		</QueryClientProvider>,
	);
}

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
	vi.restoreAllMocks();
});

beforeEach(() => {
	vi.mocked(lado.fetchLadoKnowledge).mockResolvedValue(knowledge);
});

describe("LadoProjectKnowledgeCard", () => {
	it("shows the folder, the counts and the notes with the ones to check first", async () => {
		const user = userEvent.setup();
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		renderCard();
		expect(await screen.findByTestId("lado-knowledge-dir")).toHaveTextContent(dir);
		expect(lado.fetchLadoKnowledge).toHaveBeenCalledWith("demo");
		expect(screen.getByTestId("lado-knowledge-counts")).toHaveTextContent("3 notes · 1 to check");

		const rows = screen.getAllByTestId("lado-knowledge-note");
		expect(rows.map((row) => row.getAttribute("data-state"))).toEqual(["stale", "fresh", "unchecked"]);
		expect(rows[0]).toHaveTextContent("Title of b-stale");
		expect(rows[0]).toHaveTextContent("b-stale · Pitfall · rev 2");
		expect(rows[0]).toHaveTextContent("Check first");
		expect(rows[1]).toHaveTextContent("Code map");
		expect(rows[1]).toHaveTextContent("Current");
		expect(rows[2]).toHaveTextContent("custom");
		expect(rows[2]).toHaveTextContent("No sources");

		await user.click(screen.getByRole("button", { name: "Copy the folder path" }));
		expect(writeText).toHaveBeenCalledWith(dir);
		expect(await screen.findByText("Copied")).toBeInTheDocument();
	});

	it("lists ten notes until asked for all", async () => {
		const user = userEvent.setup();
		const notes = Array.from({ length: 12 }, (_, index) => note(`n-${String(index).padStart(2, "0")}`, "fresh"));
		vi.mocked(lado.fetchLadoKnowledge).mockResolvedValue({ ...knowledge, notes });
		renderCard();
		await screen.findByTestId("lado-knowledge-dir");
		expect(screen.getAllByTestId("lado-knowledge-note")).toHaveLength(10);
		expect(screen.getByText("2 more notes")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Show all" }));
		expect(screen.getAllByTestId("lado-knowledge-note")).toHaveLength(12);
	});

	it("shows the empty state and turns the folder import off without other folders", async () => {
		vi.mocked(lado.fetchLadoKnowledge).mockResolvedValue({ ...knowledge, notes: [], importable: [], truncated: true });
		renderCard();
		expect(await screen.findByText("No notes yet")).toBeInTheDocument();
		expect(screen.getByTestId("lado-knowledge-counts")).toHaveTextContent("0 notes");
		expect(screen.getByTestId("lado-knowledge-counts")).not.toHaveTextContent("to check");
		expect(screen.getByText("The list is long, so sessions get only part of it.")).toBeInTheDocument();
		expect(screen.queryAllByTestId("lado-knowledge-note")).toHaveLength(0);
		expect(screen.getByText("There is no other knowledge folder.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Import from another knowledge folder" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Import Claude Code memory" })).toBeEnabled();
	});

	it("shows why the knowledge could not be read", async () => {
		vi.mocked(lado.fetchLadoKnowledge).mockRejectedValue(new lado.LadoApiError("no project", "LADO_KNOWLEDGE_PROJECT_NOT_FOUND"));
		renderCard();
		expect(await screen.findByRole("alert")).toHaveTextContent("This project no longer exists.");
		expect(screen.queryByTestId("lado-knowledge-dir")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-knowledge-import")).not.toBeInTheDocument();
	});

	it("warns about another repository's folder and continues it here after a confirmation", async () => {
		const user = userEvent.setup();
		const foreign: lado.LadoKnowledge = {
			...knowledge,
			foreign: true,
			notes: [],
			owner: { projectId: "demo", path: "/home/u/elsewhere/demo", claimedAt: "2026-10-01T00:00:00Z" },
		};
		vi.mocked(lado.fetchLadoKnowledge).mockResolvedValue(foreign);
		vi.mocked(lado.claimLadoKnowledge).mockResolvedValue(knowledge);
		renderCard();
		const warning = await screen.findByTestId("lado-knowledge-foreign");
		expect(warning).toHaveTextContent("Knowledge from another repository");
		expect(warning).toHaveTextContent("/home/u/elsewhere/demo");
		expect(screen.queryByTestId("lado-knowledge-counts")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-knowledge-import")).not.toBeInTheDocument();

		await user.click(within(warning).getByRole("button", { name: "Continue here" }));
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent("/home/u/elsewhere/demo");
		expect(lado.claimLadoKnowledge).not.toHaveBeenCalled();
		await user.click(within(dialog).getByRole("button", { name: "Continue" }));
		await waitFor(() => expect(lado.claimLadoKnowledge).toHaveBeenCalledWith("demo"));
		await waitFor(() => expect(screen.queryByTestId("lado-knowledge-foreign")).not.toBeInTheDocument());
		expect(screen.getAllByTestId("lado-knowledge-note")).toHaveLength(3);
		expect(screen.getByTestId("lado-knowledge-import")).toBeInTheDocument();
	});

	it("imports Claude Code memory from this project's folder or a given path and shows the result", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.importLadoKnowledge)
			.mockResolvedValueOnce({
				imported: ["setup", "pitfalls"],
				skipped: [
					{ name: "a-note", reason: "exists" },
					{ name: "key", reason: "secret" },
				],
			})
			.mockResolvedValueOnce({ imported: [], skipped: [{ name: "-home-u-old", reason: "claude_memory_not_found" }] });
		renderCard();
		await screen.findByTestId("lado-knowledge-dir");

		await user.click(screen.getByRole("button", { name: "Import Claude Code memory" }));
		const dialog = await screen.findByTestId("lado-knowledge-import-dialog");
		const field = within(dialog).getByRole("textbox", { name: "Project folder (optional)" });
		expect(field).toHaveFocus();
		await user.click(within(dialog).getByRole("button", { name: "Import" }));
		await waitFor(() => expect(lado.importLadoKnowledge).toHaveBeenCalledWith("demo", { source: "claude_memory", path: "" }));
		const result = await screen.findByTestId("lado-knowledge-import-result");
		expect(screen.queryByTestId("lado-knowledge-import-dialog")).not.toBeInTheDocument();
		expect(result).toHaveTextContent("Imported 2 notes: setup, pitfalls");
		expect(result).toHaveTextContent("Skipped 2:");
		const skips = within(result).getAllByTestId("lado-knowledge-skip");
		expect(skips[0]).toHaveTextContent("a-note — a note with this name exists");
		expect(skips[1]).toHaveTextContent("key — looks like it holds a secret");

		await user.click(screen.getByRole("button", { name: "Import Claude Code memory" }));
		expect(screen.queryByTestId("lado-knowledge-import-result")).not.toBeInTheDocument();
		const again = await screen.findByTestId("lado-knowledge-import-dialog");
		await user.type(within(again).getByRole("textbox", { name: "Project folder (optional)" }), "/home/u/old");
		await user.click(within(again).getByRole("button", { name: "Import" }));
		await waitFor(() => expect(lado.importLadoKnowledge).toHaveBeenLastCalledWith("demo", { source: "claude_memory", path: "/home/u/old" }));
		const second = await screen.findByTestId("lado-knowledge-import-result");
		expect(second).toHaveTextContent("No notes were imported.");
		expect(second).toHaveTextContent("-home-u-old — Claude Code has no memory for this folder");
	});

	it("imports from a folder that is not registered and keeps the dialog open on an error", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.importLadoKnowledge)
			.mockRejectedValueOnce(new lado.LadoApiError("gone", "LADO_KNOWLEDGE_IMPORT_NOT_FOUND"))
			.mockResolvedValueOnce({ imported: ["one"], skipped: [] });
		renderCard();
		await screen.findByTestId("lado-knowledge-dir");

		await user.click(screen.getByRole("button", { name: "Import from another knowledge folder" }));
		const dialog = await screen.findByTestId("lado-knowledge-import-dialog");
		expect(within(dialog).getByTestId("lado-knowledge-import-owner")).toHaveTextContent("/home/u/old/demo");
		await user.click(within(dialog).getByRole("button", { name: "Import" }));
		await waitFor(() => expect(lado.importLadoKnowledge).toHaveBeenCalledWith("demo", { source: "project", fromProjectId: "old-demo" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("That knowledge folder no longer exists.");

		await user.click(within(dialog).getByRole("button", { name: "Knowledge folder" }));
		expect(screen.getByText("Not registered")).toBeInTheDocument();
		expect(screen.getByText("Registered projects")).toBeInTheDocument();
		await user.click(screen.getByRole("menuitem", { name: "other · 1 note" }));
		expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
		expect(within(dialog).queryByTestId("lado-knowledge-import-owner")).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Import" }));
		await waitFor(() => expect(lado.importLadoKnowledge).toHaveBeenLastCalledWith("demo", { source: "project", fromProjectId: "other" }));
		expect(await screen.findByTestId("lado-knowledge-import-result")).toHaveTextContent("Imported 1 note: one");
		expect(screen.queryByTestId("lado-knowledge-skip")).not.toBeInTheDocument();
	});
});
