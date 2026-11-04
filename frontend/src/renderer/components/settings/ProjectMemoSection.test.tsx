import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import type { ProjectMemo, ProjectMemoView } from "../../hooks/useProjectMemo";
import { ProjectMemoSection } from "./ProjectMemoSection";

vi.mock("../../lib/api-client", async (original) => ({
	...(await original<typeof import("../../lib/api-client")>()),
	apiClient: { GET: vi.fn(), PUT: vi.fn() },
}));

const GET = vi.mocked(apiClient.GET) as unknown as ReturnType<typeof vi.fn>;
const PUT = vi.mocked(apiClient.PUT) as unknown as ReturnType<typeof vi.fn>;

const MEMO_ROUTE = "/api/v1/projects/{id}/orchestrator-memo";
const REVISION_ROUTE = "/api/v1/projects/{id}/orchestrator-memo/revisions/{revisionId}";

function memo(overrides: Partial<ProjectMemo> = {}): ProjectMemo {
	const body = overrides.body ?? "Scope: step 3\nNext: run checks";
	return {
		revision: "r2",
		source: "clear",
		updatedAt: "2026-10-20T09:00:00Z",
		body,
		chars: Array.from(body).length,
		stale: false,
		...overrides,
	};
}

function view(current: ProjectMemo | null, revisions: ProjectMemoView["revisions"] = []): ProjectMemoView {
	return { memo: current, revisions, maxChars: 4000, staleAfterDays: 7 };
}

const earlier = { revision: "r1", source: "user" as const, updatedAt: "2026-10-18T09:00:00Z", chars: 12 };

/** Answers GETs with the given views in order, the last one repeating. */
function answerGets(views: ProjectMemoView[], revisionBody = "Older memo") {
	let index = 0;
	GET.mockImplementation(async (route: string, init: { params: { path: { revisionId?: string } } }) => {
		if (route === REVISION_ROUTE) {
			return { data: { memo: memo({ revision: init.params.path.revisionId, source: "user", body: revisionBody }) } };
		}
		const next = views[Math.min(index, views.length - 1)];
		index += 1;
		return { data: next };
	});
}

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ProjectMemoSection projectId="proj-1" />
		</QueryClientProvider>,
	);
}

const field = () => screen.getByRole("textbox", { name: "Memo" });

// LADO fork: Project settings → Orchestrator memo (issue #50).
describe("ProjectMemoSection", () => {
	beforeEach(() => {
		vi.resetAllMocks();
	});
	afterEach(cleanup);

	it("shows the memo, when and by whom it was written, and its length", async () => {
		answerGets([view(memo(), [earlier])]);
		renderSection();
		expect(await screen.findByDisplayValue(/Scope: step 3/)).toBeTruthy();
		const meta = screen.getByTestId("project-memo-meta");
		expect(meta.textContent).toContain("Written by the orchestrator before /clear");
		expect(meta.textContent).toContain("2026");
		expect(screen.queryByTestId("project-memo-stale")).toBeNull();
		expect(screen.getByTestId("project-memo-count").textContent).toBe("30 / 4,000");
		expect(GET).toHaveBeenCalledWith(MEMO_ROUTE, { params: { path: { id: "proj-1" } } });
		const rows = screen.getAllByTestId("project-memo-revision");
		expect(rows).toHaveLength(1);
		expect(rows[0].textContent).toContain("Edited in settings");
		expect(rows[0].textContent).toContain("12 characters");
	});

	it("marks a memo older than the stale age", async () => {
		answerGets([view(memo({ stale: true }))]);
		renderSection();
		expect((await screen.findByTestId("project-memo-stale")).textContent).toBe("Older than 7 days");
	});

	it("says there is no memo yet and saves the first one against no revision", async () => {
		answerGets([view(null), view(memo({ revision: "r1", source: "user", body: "First memo" }))]);
		PUT.mockResolvedValue({ data: view(memo({ revision: "r1", source: "user", body: "First memo" })) });
		renderSection();
		expect(await screen.findByText(/No memo yet/)).toBeTruthy();
		const user = userEvent.setup();
		await user.type(field(), "First memo");
		await user.click(screen.getByRole("button", { name: "Save memo" }));
		await waitFor(() =>
			expect(PUT).toHaveBeenCalledWith(MEMO_ROUTE, {
				params: { path: { id: "proj-1" } },
				body: { body: "First memo", baseRevision: "" },
			}),
		);
		expect(await screen.findByText("Saved.")).toBeTruthy();
		expect(screen.getByTestId("project-memo-meta").textContent).toContain("Edited in settings");
	});

	it("saves an edit with the revision it started from", async () => {
		answerGets([view(memo())]);
		const savedMemo = memo({ revision: "r3", source: "user", body: "Scope: step 3\nNext: run checks\nAsk before merging" });
		PUT.mockResolvedValue({ data: view(savedMemo, [{ revision: "r2", source: "clear", updatedAt: "2026-10-20T09:00:00Z", chars: 30 }]) });
		renderSection();
		await screen.findByDisplayValue(/Scope: step 3/);
		const user = userEvent.setup();
		const save = screen.getByRole("button", { name: "Save memo" });
		expect((save as HTMLButtonElement).disabled).toBe(true);
		await user.type(field(), "\nAsk before merging");
		await user.click(save);
		await waitFor(() =>
			expect(PUT).toHaveBeenCalledWith(MEMO_ROUTE, {
				params: { path: { id: "proj-1" } },
				body: { body: "Scope: step 3\nNext: run checks\nAsk before merging", baseRevision: "r2" },
			}),
		);
		expect(await screen.findByText("Saved.")).toBeTruthy();
		expect(screen.getAllByTestId("project-memo-revision")).toHaveLength(1);
	});

	it("does not save a memo over the limit", async () => {
		answerGets([view(memo({ body: "x" }))]);
		renderSection();
		await screen.findByDisplayValue("x");
		const user = userEvent.setup();
		await user.click(field());
		await user.paste("y".repeat(4000));
		expect(screen.getByTestId("project-memo-count").textContent).toBe("4,001 / 4,000");
		expect(screen.getByText("The memo is longer than 4,000 characters. Shorten it before saving.")).toBeTruthy();
		expect((screen.getByRole("button", { name: "Save memo" }) as HTMLButtonElement).disabled).toBe(true);
		expect(PUT).not.toHaveBeenCalled();
	});

	it("explains a conflict, keeps the text and saves over the new memo on a second try", async () => {
		const afterClear = memo({ revision: "r9", body: "Written by /clear meanwhile" });
		answerGets([view(memo()), view(afterClear)]);
		PUT.mockResolvedValueOnce({ error: { code: "PROJECT_MEMO_CONFLICT", message: "The memo changed" } });
		PUT.mockResolvedValueOnce({ data: view(memo({ revision: "r10", source: "user", body: "Mine" })) });
		renderSection();
		await screen.findByDisplayValue(/Scope: step 3/);
		const user = userEvent.setup();
		await user.clear(field());
		await user.type(field(), "Mine");
		await user.click(screen.getByRole("button", { name: "Save memo" }));
		expect((await screen.findByRole("alert")).textContent).toContain("The memo changed while you were editing");
		expect((field() as HTMLTextAreaElement).value).toBe("Mine");
		await waitFor(() => expect(GET).toHaveBeenCalledTimes(2));
		await user.click(screen.getByRole("button", { name: "Save memo" }));
		await waitFor(() => expect(PUT).toHaveBeenCalledTimes(2));
		expect(PUT.mock.calls[1][1]).toEqual({ params: { path: { id: "proj-1" } }, body: { body: "Mine", baseRevision: "r9" } });
	});

	it("shows an earlier revision and restores it as the current memo", async () => {
		answerGets([view(memo(), [earlier])]);
		PUT.mockResolvedValue({ data: view(memo({ revision: "r3", source: "user", body: "Older memo" }), [{ revision: "r2", source: "clear", updatedAt: "2026-10-20T09:00:00Z", chars: 30 }, earlier]) });
		renderSection();
		const row = await screen.findByTestId("project-memo-revision");
		const user = userEvent.setup();
		await user.click(within(row).getByRole("button", { name: /Show the revision from/ }));
		expect((await screen.findByTestId("project-memo-revision-body")).textContent).toBe("Older memo");
		expect(GET).toHaveBeenCalledWith(REVISION_ROUTE, { params: { path: { id: "proj-1", revisionId: "r1" } } });
		await user.click(within(row).getByRole("button", { name: /Restore the revision from/ }));
		await waitFor(() =>
			expect(PUT).toHaveBeenCalledWith(MEMO_ROUTE, {
				params: { path: { id: "proj-1" } },
				body: { body: "Older memo", baseRevision: "r2" },
			}),
		);
		expect(await screen.findByDisplayValue("Older memo")).toBeTruthy();
		expect(screen.getAllByTestId("project-memo-revision")).toHaveLength(2);
	});

	it("holds a restore back while the editor has unsaved changes", async () => {
		answerGets([view(memo(), [earlier])]);
		renderSection();
		const row = await screen.findByTestId("project-memo-revision");
		const user = userEvent.setup();
		await user.type(field(), " more");
		expect((within(row).getByRole("button", { name: /Restore the revision from/ }) as HTMLButtonElement).disabled).toBe(true);
		expect(screen.getByText("Save or discard your changes before restoring a revision.")).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Discard changes" }));
		expect((field() as HTMLTextAreaElement).value).toBe("Scope: step 3\nNext: run checks");
		expect((within(row).getByRole("button", { name: /Restore the revision from/ }) as HTMLButtonElement).disabled).toBe(false);
	});

	it("names the daemon's refusal of a long memo", async () => {
		answerGets([view(memo())]);
		PUT.mockResolvedValue({ error: { code: "PROJECT_MEMO_TOO_LONG", message: "too long" } });
		renderSection();
		await screen.findByDisplayValue(/Scope: step 3/);
		const user = userEvent.setup();
		await user.type(field(), "!");
		await user.click(screen.getByRole("button", { name: "Save memo" }));
		expect((await screen.findByRole("alert")).textContent).toBe("The memo is longer than 4,000 characters. Shorten it before saving.");
	});
});
