import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../../lib/api-client";
import { aoBridge } from "../../../lib/bridge";
import type { LadoSetupItemDetail } from "../../../lib/lado-setup";
import {
	daemonCalls,
	MANAGED,
	routeDaemon,
	setupChangeResult,
	setupConflict,
	setupContent,
	setupItemDetail,
	setupItemRecord,
	type DaemonRoutes,
} from "../../../test/lado-setup-fixtures";
import { LadoSetupHistoryDialog } from "./LadoSetupHistoryDialog";
import { LadoSetupItemEditor } from "./LadoSetupItemEditor";

vi.mock("../../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), POST: vi.fn(), PUT: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

// The diff renderer draws into a shadow root; the tests read what it was given.
vi.mock("@pierre/diffs/react", () => ({
	MultiFileDiff: ({ oldFile, newFile }: { oldFile: { contents: string }; newFile: { contents: string } }) => (
		<div data-testid="pierre-diff" data-old={oldFile.contents} data-new={newFile.contents} />
	),
}));

const ITEM = "/api/v1/lado/setup/items/{itemId}";

const instructions = setupItemDetail(setupItemRecord({ id: "item-claude-md", kind: "instructions", name: "CLAUDE.md" }), [
	{ content: setupContent({ text: "# Rules\n" }) },
]);

function serve(detail: LadoSetupItemDetail | (() => LadoSetupItemDetail), routes: DaemonRoutes = {}) {
	routeDaemon(apiClient, {
		[`GET ${ITEM}`]: () => ({ data: typeof detail === "function" ? detail() : detail }),
		[`PATCH ${ITEM}`]: ({ body }) => {
			const input = body as { content: unknown; dryRun?: boolean };
			return {
				data: setupChangeResult({
					op: "edit",
					applied: !input.dryRun,
					before: setupContent({ text: "# Rules\n" }),
					after: setupContent({ text: String(input.content) }),
				}),
			};
		},
		...routes,
	});
}

function renderDialog(element: ReactElement) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
}

/** Types at the start of a CodeMirror editor, the way the Markdown editor's own tests do. */
async function typeInto(user: ReturnType<typeof userEvent.setup>, editor: HTMLElement, text: string) {
	const content = editor.matches(".cm-content") ? editor : editor.querySelector<HTMLElement>(".cm-content");
	if (!content) throw new Error("no editor content");
	await user.click(content);
	await user.type(content, text);
}

function patchBodies() {
	return daemonCalls(apiClient, "PATCH", ITEM).map((call) => call.body);
}

// LADO fork: editing a managed setup item (docs/LJY-PRD/HARNESS.md §9).
describe("LadoSetupItemEditor", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(() => {
		cleanup();
		vi.restoreAllMocks();
	});

	it("shows the difference and saves after the dry run", async () => {
		const user = userEvent.setup();
		serve(instructions, {
			[`PATCH ${ITEM}`]: ({ body }) => {
				const input = body as { content: unknown; dryRun?: boolean };
				return {
					data: setupChangeResult({
						op: "edit",
						applied: !input.dryRun,
						before: setupContent({ text: "# Rules\n" }),
						after: setupContent({ text: String(input.content) }),
						conflictsAdded: [setupConflict({ code: "LADO_SETUP_HOOK_ADDS_PROMPT_TEXT", severity: "warning", details: { event: "SessionStart" } })],
					}),
				};
			},
		});
		const onOpenChange = vi.fn();
		renderDialog(<LadoSetupItemEditor itemId="item-claude-md" open onOpenChange={onOpenChange} />);

		const dialog = await screen.findByRole("dialog", { name: "Edit CLAUDE.md" });
		const review = within(dialog).getByRole("button", { name: "Review changes" });
		// Nothing to review before an edit.
		expect(review).toBeDisabled();
		await typeInto(user, await within(dialog).findByRole("textbox", { name: "Edit CLAUDE.md" }), "Use tabs. ");
		await user.click(review);

		// The dry run shows what the save does, and nothing is saved yet.
		const diff = await within(dialog).findByTestId("pierre-diff");
		expect(diff).toHaveAttribute("data-old", "# Rules\n");
		expect(diff).toHaveAttribute("data-new", "Use tabs. # Rules\n");
		expect(patchBodies()).toEqual([{ baseVersion: 1, content: "Use tabs. # Rules\n", dryRun: true }]);
		const added = within(dialog).getByRole("list", { name: "Conflicts this change adds" });
		expect(added).toHaveTextContent("Warning · A SessionStart hook adds its output to the agent's context.");
		expect(within(dialog).queryByTestId("lado-setup-presence")).not.toBeInTheDocument();

		await user.click(within(dialog).getByRole("button", { name: "Save" }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(patchBodies()).toEqual([
			{ baseVersion: 1, content: "Use tabs. # Rules\n", dryRun: true },
			{ baseVersion: 1, content: "Use tabs. # Rules\n", dryRun: true },
			{ baseVersion: 1, content: "Use tabs. # Rules\n" },
		]);
	});

	it("saves a change that can run commands through the desktop confirmation", async () => {
		const user = userEvent.setup();
		const hook = setupItemDetail(setupItemRecord({ id: "item-hook", kind: "hook_script", name: "notify.sh" }), [
			{ content: setupContent({ text: "echo done\n", executable: true }) },
		]);
		serve(hook, {
			[`PATCH ${ITEM}`]: ({ body }) => ({
				data: setupChangeResult({
					op: "edit",
					needsPresence: true,
					before: setupContent({ text: "echo done\n" }),
					after: setupContent({ text: String((body as { content: unknown }).content) }),
				}),
			}),
		});
		const changeSetup = vi
			.spyOn(aoBridge.lado.presence, "changeSetup")
			.mockResolvedValue({ status: "changed", change: setupChangeResult({ op: "edit", applied: true, actor: "human" }) });
		const onOpenChange = vi.fn();
		renderDialog(<LadoSetupItemEditor itemId="item-hook" open onOpenChange={onOpenChange} />);

		const dialog = await screen.findByRole("dialog", { name: "Edit notify.sh" });
		// A script is plain text, not Markdown.
		expect(within(dialog).getByTestId("lado-setup-content")).toHaveAttribute("data-editor", "code");
		await typeInto(user, await within(dialog).findByRole("textbox", { name: "Edit notify.sh" }), "set -e; ");
		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));
		expect(await within(dialog).findByTestId("lado-setup-presence")).toHaveTextContent(
			"Saving asks for the desktop app's confirmation, since this item can run commands or widen access.",
		);

		await user.click(within(dialog).getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(changeSetup).toHaveBeenCalledWith({
				method: "PATCH",
				path: "/api/v1/lado/setup/items/item-hook",
				body: { baseVersion: 1, content: "set -e; echo done\n" },
			}),
		);
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		// The renderer only asked; the desktop app made the change.
		expect(patchBodies().every((body) => (body as { dryRun?: boolean }).dryRun)).toBe(true);
	});

	it("says when a JSON value does not parse, before asking the daemon", async () => {
		const user = userEvent.setup();
		serve(setupItemDetail(setupItemRecord({ id: "item-model", kind: "setting", name: "model" }), [{ content: setupContent({ contentKind: "json", text: '"sonnet"' }) }]));
		renderDialog(<LadoSetupItemEditor itemId="item-model" open onOpenChange={vi.fn()} />);

		const dialog = await screen.findByRole("dialog", { name: "Edit model" });
		const editor = await within(dialog).findByRole("textbox", { name: "Edit model" });
		await typeInto(user, editor, "x");
		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));

		expect(await within(dialog).findByRole("alert")).toHaveTextContent("This is not valid JSON.");
		expect(editor).toHaveAttribute("aria-invalid", "true");
		expect(patchBodies()).toEqual([]);
	});

	it("stops at a newer version and keeps the edits on top of it after a reload", async () => {
		const user = userEvent.setup();
		let detail = instructions;
		let refuse = true;
		serve(() => detail, {
			[`PATCH ${ITEM}`]: ({ body }) =>
				refuse
					? { error: { code: "LADO_SETUP_ITEM_CHANGED", message: "newer", details: { item: "item-claude-md", version: 2 } } }
					: {
							data: setupChangeResult({
								op: "edit",
								before: setupContent({ text: "# Rules v2\n" }),
								after: setupContent({ text: String((body as { content: unknown }).content) }),
							}),
						},
		});
		renderDialog(<LadoSetupItemEditor itemId="item-claude-md" open onOpenChange={vi.fn()} />);

		const dialog = await screen.findByRole("dialog", { name: "Edit CLAUDE.md" });
		await typeInto(user, await within(dialog).findByRole("textbox", { name: "Edit CLAUDE.md" }), "Use tabs. ");
		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));

		expect(await within(dialog).findByText("The item got another version meanwhile. Reload to keep your edits on top of it.")).toHaveAttribute("role", "status");
		expect(within(dialog).getByRole("button", { name: "Review changes" })).toBeDisabled();

		detail = setupItemDetail(instructions.item, [{ content: setupContent({ text: "# Rules\n" }) }, { content: setupContent({ text: "# Rules v2\n" }) }]);
		refuse = false;
		await user.click(within(dialog).getByRole("button", { name: "Reload" }));
		expect(await within(dialog).findByText("Your edits are kept on top of the latest version. Review them again before saving.")).toBeInTheDocument();

		await user.click(within(dialog).getByRole("button", { name: "Review changes" }));
		await within(dialog).findByTestId("pierre-diff");
		expect(patchBodies().at(-1)).toEqual({ baseVersion: 2, content: "Use tabs. # Rules\n", dryRun: true });
		expect(within(dialog).getByTestId("pierre-diff")).toHaveAttribute("data-old", "# Rules v2\n");
	});

	it("shows a folder item's files read-only, to change in the managed folder", async () => {
		serve(
			setupItemDetail(
				setupItemRecord({ id: "item-skill", kind: "skill", name: "review" }),
				[
					{
						content: setupContent({
							contentKind: "tree",
							files: [
								{ path: "SKILL.md", mode: "file", size: 120, sha256: "1".repeat(64) },
								{ path: "scripts/run.sh", mode: "exec", size: 2048, sha256: "2".repeat(64) },
							],
						}),
					},
				],
				{ managedPath: `${MANAGED}/claude/skills/review` },
			),
		);
		renderDialog(<LadoSetupItemEditor itemId="item-skill" open onOpenChange={vi.fn()} />);

		const dialog = await screen.findByRole("dialog", { name: "Edit review" });
		const files = await within(dialog).findByRole("list", { name: "Files" });
		expect(within(files).getByText("SKILL.md")).toHaveClass("font-mono");
		expect(within(files).getByText("scripts/run.sh").nextElementSibling).toHaveTextContent("2 kB · Executable");
		expect(within(dialog).getByTestId("lado-setup-managed-path")).toHaveTextContent(`${MANAGED}/claude/skills/review`);
		expect(within(dialog).getByText(/This item is a folder\./)).toBeInTheDocument();
		// Nothing here edits it.
		expect(within(dialog).queryByRole("textbox")).not.toBeInTheDocument();
		expect(within(dialog).queryByRole("button", { name: "Review changes" })).not.toBeInTheDocument();
		expect(within(dialog).getAllByRole("button", { name: "Close" }).length).toBeGreaterThan(0);
	});
});

describe("LadoSetupHistoryDialog", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});

	afterEach(cleanup);

	it("lists every version newest first with who made it, and shows the one picked", async () => {
		const user = userEvent.setup();
		serve(
			setupItemDetail(instructions.item, [
				{ content: setupContent({ text: "# Old rules\n" }), actor: "human", createdAt: "2026-10-24T09:00:00Z" },
				{ content: setupContent({ text: "# New rules\n" }), actor: "api", createdAt: "2026-10-25T09:00:00Z" },
			]),
		);
		renderDialog(<LadoSetupHistoryDialog itemId="item-claude-md" open onOpenChange={vi.fn()} />);

		const dialog = await screen.findByRole("dialog", { name: "History of CLAUDE.md" });
		const list = await within(dialog).findByRole("list", { name: "Versions" });
		const rows = within(list).getAllByTestId("lado-setup-version");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^Version 2Current.* · API$/),
			expect.stringMatching(/^Version 1.* · Confirmed on the desktop$/),
		]);
		expect(within(dialog).getByTestId("lado-setup-version-content")).toHaveTextContent("# New rules");

		await user.click(within(rows[1]).getByRole("button"));
		expect(within(rows[1]).getByRole("button")).toHaveAttribute("aria-pressed", "true");
		expect(within(dialog).getByTestId("lado-setup-version-content")).toHaveTextContent("# Old rules");
	});
});
