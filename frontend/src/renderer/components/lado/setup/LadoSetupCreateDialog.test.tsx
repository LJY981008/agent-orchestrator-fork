import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { daemonCalls, routeDaemon, setupChangeResult, setupContent, type DaemonRoutes } from "../../../test/lado-setup-fixtures";
import { LadoSetupCreateDialog } from "./LadoSetupCreateDialog";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

vi.mock("@pierre/diffs/react", () => ({
	MultiFileDiff: ({ oldFile, newFile }: { oldFile: { contents: string }; newFile: { contents: string } }) => (
		<div data-testid="pierre-diff" data-old={oldFile.contents} data-new={newFile.contents} />
	),
}));

const ITEMS = "/api/v1/lado/setup/items";

function serve(routes: DaemonRoutes = {}) {
	routeDaemon(apiClient, {
		[`POST ${ITEMS}`]: ({ body }) => {
			const input = body as { content: unknown; dryRun?: boolean };
			return {
				data: setupChangeResult({
					op: "create",
					applied: !input.dryRun,
					after: setupContent({ contentKind: "json", text: JSON.stringify(input.content) }),
				}),
			};
		},
		...routes,
	});
}

function renderDialog(onOpenChange = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<LadoSetupCreateDialog open onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return onOpenChange;
}

async function choose(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, select: string, option: string) {
	await user.click(within(dialog).getByRole("button", { name: select }));
	await user.click(await screen.findByRole("menuitem", { name: option }));
}

function content(dialog: HTMLElement): HTMLElement {
	return within(dialog).getByTestId("lado-setup-content");
}

function postBodies() {
	return daemonCalls(apiClient, "POST", ITEMS).map((call) => call.body);
}

// LADO fork: a new managed setup item (docs/LJY-PRD/HARNESS.md §9).
describe("LadoSetupCreateDialog", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("takes each kind's content in its own editor and creates it after the dry run", async () => {
		const user = userEvent.setup();
		serve();
		const onOpenChange = renderDialog();

		const dialog = await screen.findByRole("dialog", { name: "New local setup item" });
		const name = within(dialog).getByLabelText("Name");
		expect(name).toHaveFocus();
		// Instructions are Markdown.
		expect(name).toHaveAttribute("placeholder", "CLAUDE.md");
		expect(content(dialog)).toHaveAttribute("data-editor", "markdown");
		expect(within(dialog).getByRole("button", { name: "Review changes" })).toBeDisabled();

		// A skill starts from the front matter its SKILL.md needs.
		await choose(user, dialog, "Kind", "Skill");
		expect(name).toHaveAttribute("placeholder", "review");
		expect(content(dialog).querySelector(".cm-content")).toHaveTextContent("name:");
		expect(content(dialog).querySelector(".cm-content")).toHaveTextContent("description:");

		// Hook scripts come only from an adoption, and Codex takes instructions and MCP servers only.
		await user.click(within(dialog).getByRole("button", { name: "Kind" }));
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual([
			"Instructions",
			"Setting",
			"Skill",
			"Agent",
			"Command",
			"Output style",
			"MCP server",
		]);
		await user.keyboard("{Escape}");
		await choose(user, dialog, "Tool", "Codex");
		await user.click(within(dialog).getByRole("button", { name: "Kind" }));
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual(["Instructions", "MCP server"]);
		await user.keyboard("{Escape}");
		// The skill was not Codex's, so the kind went back to instructions.
		expect(within(dialog).getByRole("button", { name: "Kind" })).toHaveTextContent("Instructions");
		expect(name).toHaveAttribute("placeholder", "AGENTS.md");

		// A setting is a JSON value in the code editor.
		await choose(user, dialog, "Tool", "Claude Code");
		await choose(user, dialog, "Kind", "Setting");
		expect(content(dialog)).toHaveAttribute("data-editor", "code");
		await user.type(name, "model");
		const value = within(dialog).getByRole("textbox", { name: "Edit Setting" });
		await user.click(value);
		await user.type(value, '"opus"');
		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));

		const diff = await within(dialog).findByTestId("pierre-diff");
		expect(diff).toHaveAttribute("data-old", "");
		expect(diff).toHaveAttribute("data-new", '"opus"');
		expect(within(dialog).getByText("Claude Code · Setting · model")).toBeInTheDocument();
		expect(postBodies()).toEqual([{ tool: "claude-code", kind: "setting", name: "model", content: "opus", dryRun: true }]);

		await user.click(within(dialog).getByRole("button", { name: "Create" }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(postBodies()).toEqual([
			{ tool: "claude-code", kind: "setting", name: "model", content: "opus", dryRun: true },
			{ tool: "claude-code", kind: "setting", name: "model", content: "opus", dryRun: true },
			{ tool: "claude-code", kind: "setting", name: "model", content: "opus" },
		]);
	});

	it("says under the name when LADO already manages one of that name", async () => {
		const user = userEvent.setup();
		serve({
			[`POST ${ITEMS}`]: () => ({
				error: { code: "LADO_SETUP_ITEM_EXISTS", message: "exists", details: { tool: "claude-code", kind: "instructions", name: "CLAUDE.md" } },
			}),
		});
		renderDialog();

		const dialog = await screen.findByRole("dialog", { name: "New local setup item" });
		const name = within(dialog).getByLabelText("Name");
		await user.type(name, "CLAUDE.md");
		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));

		const error = await within(dialog).findByRole("alert");
		expect(error).toHaveTextContent("LADO already manages CLAUDE.md. Edit that item, or choose another name.");
		expect(name).toHaveAttribute("aria-invalid", "true");
		expect(name).toHaveAttribute("aria-describedby", error.id);
		expect(name).toHaveFocus();
		// Still editing: nothing to create.
		expect(within(dialog).queryByRole("button", { name: "Create" })).not.toBeInTheDocument();

		// A new name clears it.
		await user.type(name, "x");
		expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
		expect(name).not.toHaveAttribute("aria-invalid");
	});
});
