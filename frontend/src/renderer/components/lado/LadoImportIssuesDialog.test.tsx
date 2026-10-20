import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoImportIssuesDialog } from "./LadoImportIssuesDialog";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoProjectIssues: vi.fn(),
	fetchLadoProjectRepositories: vi.fn(),
	importLadoTasks: vi.fn(),
}));

const demo: lado.LadoRepository = { ref: "github:acme/demo", name: "demo", provider: "github", host: "github.com", path: "acme/demo" };
const back: lado.LadoRepository = { ref: "github:acme/back", name: "back", provider: "github", host: "github.com", path: "acme/back" };
const front: lado.LadoRepository = { ref: "github:acme/front", name: "front", provider: "github", host: "github.com", path: "acme/front" };

const issues: lado.LadoIssue[] = [
	{ repository: demo, ref: "github:acme/demo#3", number: 3, title: "Fix the build", url: "https://github.com/acme/demo/issues/3", labels: [] },
	{ repository: demo, ref: "github:acme/demo#4", number: 4, title: "Write the docs", url: "https://github.com/acme/demo/issues/4", labels: ["docs"] },
];

function renderDialog(onOpenChange = vi.fn(), onImported = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<LadoImportIssuesDialog open projectId="demo" onOpenChange={onOpenChange} onImported={onImported} />
		</QueryClientProvider>,
	);
	return { onOpenChange, onImported };
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([demo]);
});

afterEach(cleanup);

test("imports the chosen issues and closes", async () => {
	vi.mocked(lado.fetchLadoProjectIssues).mockResolvedValue({ issues, failures: [] });
	vi.mocked(lado.importLadoTasks).mockResolvedValue({ tasks: [], skipped: [] });
	const { onOpenChange, onImported } = renderDialog();

	await screen.findByText("Fix the build");
	expect(screen.getByText("Choose open issues to work on. Importing changes nothing on the tracker.")).toBeInTheDocument();
	expect(screen.queryByText("acme/demo")).not.toBeInTheDocument();
	const importButton = screen.getByRole("button", { name: "Import" });
	expect(importButton).toBeDisabled();

	fireEvent.click(screen.getByRole("checkbox", { name: /Fix the build/ }));
	expect(importButton).toBeEnabled();
	fireEvent.click(importButton);

	await waitFor(() => expect(lado.importLadoTasks).toHaveBeenCalledWith("demo", ["github:acme/demo#3"]));
	await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
	expect(onImported).toHaveBeenCalledTimes(1);
});

test("groups a workspace's issues by repository and imports across them", async () => {
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([back, front]);
	vi.mocked(lado.fetchLadoProjectIssues).mockResolvedValue({
		issues: [
			{ repository: back, ref: "github:acme/back#7", number: 7, title: "Login API returns 500", url: "https://github.com/acme/back/issues/7", labels: [] },
			{ repository: front, ref: "github:acme/front#7", number: 7, title: "Login button misaligned", url: "https://github.com/acme/front/issues/7", labels: [] },
		],
		failures: [],
	});
	vi.mocked(lado.importLadoTasks).mockResolvedValue({ tasks: [], skipped: [] });
	renderDialog();

	const list = await screen.findByRole("list", { name: "Import issues" });
	await within(list).findByText("acme/front");
	const rows = within(list).getAllByRole("listitem").map((row) => row.textContent);
	expect(rows).toEqual(["backacme/back", "#7Login API returns 500", "frontacme/front", "#7Login button misaligned"]);

	fireEvent.click(screen.getByRole("checkbox", { name: /Login API returns 500/ }));
	fireEvent.click(screen.getByRole("checkbox", { name: /Login button misaligned/ }));
	fireEvent.click(screen.getByRole("button", { name: "Import" }));
	await waitFor(() => expect(lado.importLadoTasks).toHaveBeenCalledWith("demo", ["github:acme/back#7", "github:acme/front#7"]));
});

test("a repository that cannot be read does not hide the others", async () => {
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([back, front]);
	vi.mocked(lado.fetchLadoProjectIssues).mockResolvedValue({
		issues: [{ repository: back, ref: "github:acme/back#7", number: 7, title: "Login API returns 500", url: "https://github.com/acme/back/issues/7", labels: [] }],
		failures: [{ repository: front, problem: "403 Resource not accessible by personal access token" }],
	});
	renderDialog();

	expect(await screen.findByRole("alert")).toHaveTextContent(
		"Could not list the issues of front: 403 Resource not accessible by personal access token",
	);
	expect(screen.getByText("Login API returns 500")).toBeInTheDocument();
});

test("says so when every open issue already has a task", async () => {
	vi.mocked(lado.fetchLadoProjectIssues).mockResolvedValue({ issues: [], failures: [] });
	renderDialog();

	expect(await screen.findByText("Every open issue already has a task.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
});

test("keeps the dialog open with the reason when the import fails", async () => {
	vi.mocked(lado.fetchLadoProjectIssues).mockResolvedValue({ issues, failures: [] });
	vi.mocked(lado.importLadoTasks).mockRejectedValue(new lado.LadoApiError("This project is not in LADO mode.", "LADO_MODE_OFF"));
	const { onOpenChange, onImported } = renderDialog();

	fireEvent.click(await screen.findByRole("checkbox", { name: /Write the docs/ }));
	fireEvent.click(screen.getByRole("button", { name: "Import" }));

	expect(await screen.findByText("This project is not in LADO mode.")).toBeInTheDocument();
	expect(onOpenChange).not.toHaveBeenCalled();
	expect(onImported).not.toHaveBeenCalled();
});

test("shows why the issues could not be listed", async () => {
	vi.mocked(lado.fetchLadoProjectIssues).mockRejectedValue(new lado.LadoApiError(
		"LADO cannot reach the repository's tracker: no token for github.com. Connect the repository with an access token.",
		"LADO_TRACKER_UNAVAILABLE",
		{ repository: "github:acme/demo", reason: "no token for github.com" },
	));
	renderDialog();

	expect(await screen.findByRole("alert")).toHaveTextContent(
		"LADO cannot reach the repository's tracker (no token for github.com). Connect the repository with an access token.",
	);
});

test("says the daemon could not be reached when the call never got an answer", async () => {
	vi.mocked(lado.fetchLadoProjectIssues).mockRejectedValue(new TypeError("Failed to fetch"));
	renderDialog();

	expect(await screen.findByRole("alert")).toHaveTextContent("Could not reach the daemon. Check that AO is running.");
});
