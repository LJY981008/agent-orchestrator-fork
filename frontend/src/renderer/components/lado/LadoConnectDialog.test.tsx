import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import type { WorkspaceSummary } from "../../types/workspace";
import { LadoConnectDialog } from "./LadoConnectDialog";

const { navigate, projects } = vi.hoisted(() => ({
	navigate: vi.fn(),
	projects: { current: [] as unknown[] },
}));

vi.mock("@tanstack/react-router", async (original) => ({
	...await original<typeof import("@tanstack/react-router")>(),
	useNavigate: () => navigate,
}));

vi.mock("../../hooks/useWorkspaceQuery", async (original) => ({
	...await original<typeof import("../../hooks/useWorkspaceQuery")>(),
	useWorkspaceQuery: () => ({ data: projects.current }),
}));

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	checkLadoConnection: vi.fn(),
	createLadoConnection: vi.fn(),
}));

function ready(overrides: Partial<lado.LadoConnectionCheck> = {}): lado.LadoConnectionCheck {
	return {
		provider: "github",
		host: "github.com",
		repository: "acme/demo",
		token: {
			entered: true,
			stored: false,
			login: "ljy",
			permissions: [
				{ name: "repository", status: "ok" },
				{ name: "issues", status: "ok" },
				{ name: "pull_requests", status: "ok" },
				{ name: "branch_rules", status: "unverified", detail: "GitHub shows branch rules only to admins." },
				{ name: "commit_statuses", status: "missing", detail: "Posting commit statuses needs write access to the repository." },
			],
		},
		ssh: { ok: true },
		ready: true,
		...overrides,
	};
}

const product: WorkspaceSummary = { id: "product", name: "Product", kind: "workspace", path: "/src/product", sessions: [] };
const single: WorkspaceSummary = { id: "solo", name: "Solo", kind: "single_repo", path: "/src/solo", sessions: [] };

function renderDialog() {
	const onClose = vi.fn();
	const onBack = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<LadoConnectDialog open onBack={onBack} onClose={onClose} />
		</QueryClientProvider>,
	);
	return { onClose, onBack };
}

function fill(label: string, value: string) {
	fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

async function checkReady(url = "git@github.com:acme/demo.git") {
	fill("Repository SSH URL", url);
	fireEvent.click(screen.getByRole("button", { name: "Check" }));
	await screen.findByRole("button", { name: "Connect" });
}

beforeEach(() => {
	vi.resetAllMocks();
	projects.current = [single, product];
	window.localStorage.removeItem("ao.clone.lastDestinationParent");
});

afterEach(cleanup);

test("checks the repository, then links the registered project", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({ project: { id: "demo", name: "Demo", path: "/src/demo" } }));
	vi.mocked(lado.createLadoConnection).mockResolvedValue({ projectId: "demo", outcome: "linked" });
	const { onClose } = renderDialog();

	fill("Repository SSH URL", " git@github.com:acme/demo.git ");
	fill("Access token", "tok-secret");
	fireEvent.click(screen.getByRole("button", { name: "Check" }));

	await waitFor(() => expect(lado.checkLadoConnection).toHaveBeenCalledWith({
		url: "git@github.com:acme/demo.git",
		token: "tok-secret",
		sshKeyPath: undefined,
	}));
	expect(await screen.findByText("Signed in to github.com as ljy")).toBeInTheDocument();
	expect(screen.getByText("Links to the project Demo")).toBeInTheDocument();
	expect(screen.getByText("Post commit statuses: no access")).toHaveAttribute(
		"title",
		"Posting commit statuses needs write access to the repository.",
	);
	expect(screen.getByText("Read branch rules: can't be verified before the first write")).toBeInTheDocument();
	expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Connect" }));
	await waitFor(() => expect(lado.createLadoConnection).toHaveBeenCalledWith({
		url: "git@github.com:acme/demo.git",
		token: "tok-secret",
		sshKeyPath: undefined,
	}));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "demo" } }));
	expect(onClose).toHaveBeenCalled();
});

test("names the workspace repository a connection links to", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({
		repository: "acme/front",
		project: { id: "product", name: "Product", path: "/src/product", repository: "front" },
	}));
	renderDialog();

	await checkReady("git@github.com:acme/front.git");
	expect(screen.getByText("Links to front in the workspace Product")).toBeInTheDocument();
});

test("clones as a project of its own by default", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready());
	vi.mocked(lado.createLadoConnection).mockResolvedValue({ projectId: "demo", outcome: "cloned" });
	renderDialog();

	fill("SSH key file (optional)", "~/.ssh/id_lado");
	await checkReady();

	expect(screen.getByRole("radio", { name: "As its own project" })).toBeChecked();
	expect(screen.getByLabelText("Clone into")).toHaveValue("~/ao/projects");
	expect(screen.getByText("Clones into ~/ao/projects/demo")).toBeInTheDocument();
	fill("Clone into", "/work");
	fireEvent.click(screen.getByRole("button", { name: "Connect" }));

	await waitFor(() => expect(lado.createLadoConnection).toHaveBeenCalledWith({
		url: "git@github.com:acme/demo.git",
		token: undefined,
		sshKeyPath: "~/.ssh/id_lado",
		placement: "standalone",
		destinationParent: "/work",
	}));
	expect(window.localStorage.getItem("ao.clone.lastDestinationParent")).toBe("/work");
});

test("creates a new workspace around the clone", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({ repository: "acme/back" }));
	vi.mocked(lado.createLadoConnection).mockResolvedValue({ projectId: "product", outcome: "cloned" });
	renderDialog();

	await checkReady("git@github.com:acme/back.git");
	fireEvent.click(screen.getByRole("radio", { name: "In a new workspace" }));
	expect(screen.getByText("Finish choosing where the clone goes.")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();

	fill("Workspace folder name", "product");
	fill("Create the workspace in", "/src");
	expect(screen.getByText("Creates /src/product and clones into /src/product/back")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Connect" }));

	await waitFor(() => expect(lado.createLadoConnection).toHaveBeenCalledWith({
		url: "git@github.com:acme/back.git",
		token: undefined,
		sshKeyPath: undefined,
		placement: "new_workspace",
		destinationParent: "/src",
		workspaceName: "product",
	}));
	await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/projects/$projectId", params: { projectId: "product" } }));
});

test("adds the clone to a registered workspace", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({ repository: "acme/front" }));
	vi.mocked(lado.createLadoConnection).mockResolvedValue({ projectId: "product", outcome: "cloned" });
	renderDialog();

	await checkReady("git@github.com:acme/front.git");
	await userEvent.click(screen.getByRole("radio", { name: "In an existing workspace" }));
	await userEvent.click(screen.getByRole("combobox", { name: "Workspace" }));
	expect(screen.queryByRole("option", { name: "Solo" })).not.toBeInTheDocument();
	await userEvent.click(await screen.findByRole("option", { name: "Product" }));
	expect(screen.getByText("Clones into /src/product/front")).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Connect" }));

	await waitFor(() => expect(lado.createLadoConnection).toHaveBeenCalledWith({
		url: "git@github.com:acme/front.git",
		token: undefined,
		sshKeyPath: undefined,
		placement: "workspace",
		workspaceId: "product",
	}));
	expect(window.localStorage.getItem("ao.clone.lastDestinationParent")).toBeNull();
});

test("offers an existing workspace only when there is one", async () => {
	projects.current = [single];
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready());
	renderDialog();

	await checkReady();
	const existing = screen.getByRole("radio", { name: "In an existing workspace" });
	expect(existing).toBeDisabled();
	expect(existing).toHaveAccessibleDescription("No workspace project yet. Choose “In a new workspace” to make one.");
});

test("shows token and SSH problems under their fields", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({
		token: { entered: false, stored: true, permissions: [], problem: "github.com rejected the token.", problemCode: "token_rejected" },
		ssh: {
			ok: false,
			problem: "ssh: Could not resolve hostname github.com",
			problemCode: "ssh_failed",
			problemDetail: "ssh: Could not resolve hostname github.com",
		},
		ready: false,
	}));
	renderDialog();

	fill("Repository SSH URL", "git@github.com:acme/demo.git");
	fireEvent.click(screen.getByRole("button", { name: "Check" }));

	expect(await screen.findByText("github.com rejected the token.")).toBeInTheDocument();
	expect(screen.getByText("Could not reach the repository over SSH.")).toBeInTheDocument();
	expect(screen.getByText("ssh: Could not resolve hostname github.com")).toBeInTheDocument();
	expect(screen.getByLabelText("Access token")).toHaveAttribute("aria-invalid", "true");
	expect(screen.getByRole("button", { name: "Check" })).toBeEnabled();
	expect(lado.createLadoConnection).not.toHaveBeenCalled();
});

test("explains an untrusted SSH host key with the host filled in", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready({
		host: "gitlab.example.com",
		ssh: { ok: false, problem: "The SSH host key of gitlab.example.com is not trusted yet.", problemCode: "host_key_untrusted" },
		ready: false,
	}));
	renderDialog();

	fill("Repository SSH URL", "git@gitlab.example.com:group/project.git");
	fireEvent.click(screen.getByRole("button", { name: "Check" }));

	expect(await screen.findByText(/The SSH host key of gitlab\.example\.com is not trusted yet\./)).toHaveTextContent(
		"git@gitlab.example.com",
	);
});

test("puts a refused URL under the URL field", async () => {
	vi.mocked(lado.checkLadoConnection).mockRejectedValue(
		new lado.LadoApiError("Use the repository's SSH URL, such as git@github.com:owner/repo.git.", "LADO_REPOSITORY_URL_INVALID"),
	);
	renderDialog();

	fill("Repository SSH URL", "https://github.com/acme/demo");
	fireEvent.click(screen.getByRole("button", { name: "Check" }));

	const error = await screen.findByText("Use the repository's SSH URL, such as git@github.com:owner/repo.git.");
	expect(error).toHaveAttribute("id", "ladoConnectUrlError");
	expect(screen.getByLabelText("Repository SSH URL")).toHaveAttribute("aria-invalid", "true");
});

test("translates a refused connection by its code", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready());
	vi.mocked(lado.createLadoConnection).mockRejectedValue(new lado.LadoApiError(
		"A folder with this name already exists in the selected destination.",
		"LADO_WORKSPACE_FOLDER_EXISTS",
	));
	renderDialog();

	await checkReady();
	fireEvent.click(screen.getByRole("radio", { name: "In a new workspace" }));
	fill("Workspace folder name", "product");
	fireEvent.click(screen.getByRole("button", { name: "Connect" }));

	expect(await screen.findByRole("alert")).toHaveTextContent(
		"A folder with that name already exists there. Choose another name or location.",
	);
});

test("editing a field after a check asks for a new check", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready());
	renderDialog();

	await checkReady();
	fill("Access token", "tok-other");
	expect(screen.getByRole("button", { name: "Check" })).toBeInTheDocument();
	expect(screen.queryByText("Signed in to github.com as ljy")).not.toBeInTheDocument();
});

test("never echoes the token outside its password field", async () => {
	vi.mocked(lado.checkLadoConnection).mockResolvedValue(ready());
	renderDialog();

	fill("Access token", "tok-secret-value");
	await checkReady();

	expect(screen.getByLabelText("Access token")).toHaveAttribute("type", "password");
	expect(document.body.textContent).not.toContain("tok-secret-value");
});
