// LADO fork (#22): Project settings → Scripts → Workspace folders.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProjectScriptsSettings } from "./ProjectScriptsSettings";
import { DEFAULT_WORKSPACE_ASSET_EXCLUDES } from "./WorkspaceAssetsSettings";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock, POST: vi.fn() }, apiErrorMessage: () => "request failed" }));

// Other settings the card must keep as stored.
const others = { postCreate: ["npm ci"], env: { TOKEN: "kept" }, autoReview: true };

function project(config: Record<string, unknown>, kind = "workspace") {
	return { id: "p", name: "Example", kind, config: { ...others, ...config } };
}

function serve(config: Record<string, unknown>, kind = "workspace") {
	getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: project(config, kind) } });
}

beforeEach(() => {
	serve({});
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
});

function renderSettings() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><ProjectScriptsSettings projectId="p" /></QueryClientProvider>);
}

async function openEditor() {
	const card = await screen.findByTestId("workspace-assets-settings");
	await userEvent.click(within(card).getByRole("button", { name: "Not copied" }));
	return screen.findByTestId("workspace-assets-exclude-dialog");
}

function savedConfig() {
	expect(putMock).toHaveBeenCalledOnce();
	return putMock.mock.calls[0][1].body.config as Record<string, unknown>;
}

describe("WorkspaceAssetsSettings", () => {
	it("shows the daemon's defaults while the project sets none", async () => {
		renderSettings();
		const card = await screen.findByTestId("workspace-assets-settings");
		expect(within(card).getByTestId("workspace-assets-exclude-count")).toHaveTextContent("Using the defaults (12)");
		expect(within(card).getByTestId("workspace-assets-exclude-list")).toHaveTextContent(".venv, venv, node_modules, __pycache__");
		expect(within(card).getByText("To use an excluded folder from the original as it is in sessions, add it to symlinks.")).toBeInTheDocument();
		expect(within(card).getByText(/Skipped links are written to the daemon log/)).toBeInTheDocument();
		expect(within(card).getByRole("button", { name: "Links outside the workspace" })).toHaveTextContent("Skip");
		const dialog = await openEditor();
		expect(within(dialog).getByLabelText("Patterns, one per line")).toHaveValue(DEFAULT_WORKSPACE_ASSET_EXCLUDES.join("\n"));
		// Saving the defaults unchanged would pin them to the project.
		expect(within(dialog).getByRole("button", { name: "Save patterns" })).toBeDisabled();
		expect(within(dialog).queryByRole("button", { name: "Restore defaults" })).not.toBeInTheDocument();
	});

	it("saves an edited list over the latest settings", async () => {
		renderSettings();
		const dialog = await openEditor();
		fireEvent.change(within(dialog).getByLabelText("Patterns, one per line"), { target: { value: " .venv \n\ndata/raw\n" } });
		// A change made elsewhere since the screen loaded is kept.
		getMock.mockResolvedValue({ data: { status: "ok", project: { ...project({}), name: "Renamed", config: { ...others, preRemove: ["make clean"] } } } });
		await userEvent.click(within(dialog).getByRole("button", { name: "Save patterns" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
		expect(putMock.mock.calls[0][1].body).toEqual({
			displayName: "Renamed",
			config: { ...others, preRemove: ["make clean"], workspaceAssets: { exclude: [".venv", "data/raw"], absoluteLinks: "skip" } },
		});
		await waitFor(() => expect(screen.queryByTestId("workspace-assets-exclude-dialog")).not.toBeInTheDocument());
	});

	it("saves an empty list as [] so every folder is copied", async () => {
		renderSettings();
		const dialog = await openEditor();
		fireEvent.change(within(dialog).getByLabelText("Patterns, one per line"), { target: { value: "" } });
		await userEvent.click(within(dialog).getByRole("button", { name: "Save patterns" }));
		await waitFor(() => expect(savedConfig().workspaceAssets).toEqual({ exclude: [], absoluteLinks: "skip" }));
	});

	it("shows a saved empty list as nothing excluded", async () => {
		serve({ workspaceAssets: { exclude: [], absoluteLinks: "skip" } });
		renderSettings();
		const card = await screen.findByTestId("workspace-assets-settings");
		expect(within(card).getByTestId("workspace-assets-exclude-count")).toHaveTextContent("Nothing; every folder is copied");
		expect(within(card).queryByTestId("workspace-assets-exclude-list")).not.toBeInTheDocument();
	});

	it("keeps links without sending an exclude list while the defaults apply", async () => {
		// The daemon answers a missing list as null.
		serve({ workspaceAssets: { exclude: null } });
		renderSettings();
		const card = await screen.findByTestId("workspace-assets-settings");
		expect(within(card).getByTestId("workspace-assets-exclude-count")).toHaveTextContent("Using the defaults (12)");
		await userEvent.click(within(card).getByRole("button", { name: "Links outside the workspace" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Keep" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
		const assets = savedConfig().workspaceAssets;
		expect(assets).toEqual({ absoluteLinks: "keep" });
		expect(assets).not.toHaveProperty("exclude");
	});

	it("restores the defaults by leaving the exclude key out", async () => {
		serve({ workspaceAssets: { exclude: ["data"], absoluteLinks: "keep" } });
		renderSettings();
		const card = await screen.findByTestId("workspace-assets-settings");
		expect(within(card).getByTestId("workspace-assets-exclude-count")).toHaveTextContent("1 pattern");
		const dialog = await openEditor();
		expect(within(dialog).getByLabelText("Patterns, one per line")).toHaveValue("data");
		await userEvent.click(within(dialog).getByRole("button", { name: "Restore defaults" }));
		await waitFor(() => expect(savedConfig().workspaceAssets).toEqual({ absoluteLinks: "keep" }));
	});

	it("drops workspaceAssets when both settings return to their defaults", async () => {
		serve({ workspaceAssets: { exclude: ["data"], absoluteLinks: "skip" } });
		renderSettings();
		const dialog = await openEditor();
		await userEvent.click(within(dialog).getByRole("button", { name: "Restore defaults" }));
		await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
		expect(savedConfig()).toEqual(others);
	});

	it("shows the daemon's refusal in the dialog", async () => {
		putMock.mockResolvedValue({ error: { message: "invalid" } });
		renderSettings();
		const dialog = await openEditor();
		fireEvent.change(within(dialog).getByLabelText("Patterns, one per line"), { target: { value: "/abs" } });
		await userEvent.click(within(dialog).getByRole("button", { name: "Save patterns" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("request failed");
	});

	it("is not shown for a project that is not a workspace", async () => {
		serve({}, "single_repo");
		renderSettings();
		expect(await screen.findByLabelText("Step 1")).toHaveValue("npm ci");
		expect(screen.queryByTestId("workspace-assets-settings")).not.toBeInTheDocument();
	});
});
