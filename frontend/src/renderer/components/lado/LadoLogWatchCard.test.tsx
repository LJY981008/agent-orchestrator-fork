import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import * as logWatch from "../../lib/lado-log-watch";
import { TooltipProvider } from "../ui/tooltip";
import { LadoLogWatchCard } from "./LadoLogWatchCard";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoProjectRepositories: vi.fn(),
}));

vi.mock("../../lib/lado-log-watch", async (original) => ({
	...await original<typeof import("../../lib/lado-log-watch")>(),
	fetchLadoLogWatch: vi.fn(),
	updateLadoLogWatch: vi.fn(),
	importLadoLogCI: vi.fn(),
	checkLadoLogSource: vi.fn(),
	fetchLadoLogIncidents: vi.fn(),
	setLadoLogFingerprintIgnored: vi.fn(),
}));

const demo: lado.LadoRepository = { ref: "gitlab:grp/demo", name: "demo", provider: "gitlab", host: "gitlab.example.test", path: "grp/demo" };

const plain: logWatch.LadoLogSource = {
	id: "src-plain",
	kind: "docker_tcp",
	displayName: "prod1",
	endpoint: "tcp://192.0.2.10:2375",
	tls: false,
	paused: false,
	status: { state: "watching", insecure: true },
	tlsStored: { ca: false, cert: false, key: false },
	targets: [{ id: "tgt-1", kind: "compose_project", value: "grp", paused: false, status: { instances: 2 } }],
};

const secured: logWatch.LadoLogSource = {
	id: "src-tls",
	kind: "docker_tcp",
	displayName: "prod2",
	endpoint: "tcp://prod2.example.test:2376",
	tls: true,
	paused: false,
	status: { state: "off", insecure: false, detail: "no CA" },
	tlsStored: { ca: false, cert: false, key: false },
	targets: [],
};

function settings(fields: Partial<logWatch.LadoLogWatch> = {}): logWatch.LadoLogWatch {
	return {
		projectId: "demo",
		enabled: true,
		ladoMode: true,
		repository: "",
		include: [],
		exclude: [],
		hourlyIssueCap: 5,
		autoPlan: true,
		autoPlanDailyCap: 5,
		autoPlanMinCount: 1,
		disconnectAlertMinutes: 10,
		sources: [plain, secured],
		...fields,
	};
}

const ignoredFingerprint: logWatch.LadoLogFingerprint = {
	id: 31,
	projectId: "demo",
	fingerprint: "e".repeat(64),
	service: "grp/worker",
	exceptionKind: "TimeoutError",
	count: 12,
	firstSeenAt: "2026-11-09T00:00:00Z",
	lastSeenAt: "2026-11-10T00:00:00Z",
	ignored: true,
	instances: [],
	serviceInstances: 0,
};

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoLogWatchCard projectId="demo" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.fetchLadoProjectRepositories).mockResolvedValue([demo]);
	vi.mocked(logWatch.fetchLadoLogWatch).mockResolvedValue(settings());
	vi.mocked(logWatch.fetchLadoLogIncidents).mockResolvedValue([]);
	vi.mocked(logWatch.updateLadoLogWatch).mockImplementation(async () => settings());
});

afterEach(cleanup);

test("warns that LADO only reads, and marks the host without TLS", async () => {
	renderCard();

	const card = await screen.findByTestId("lado-log-watch-card");
	expect(await within(card).findByTestId("lado-log-watch-readonly-warning")).toHaveTextContent("LADO only reads from it");
	const sources = await within(card).findAllByTestId("lado-log-watch-source");
	expect(sources).toHaveLength(2);
	expect(within(sources[0]).getByTestId("lado-log-watch-insecure")).toHaveTextContent("No TLS");
	expect(within(sources[0]).getByTestId("lado-log-watch-source-state")).toHaveTextContent("Watching");
	expect(within(sources[0]).getByTestId("lado-log-watch-target-state")).toHaveTextContent("2 containers followed");
	// The TLS host shows no warning, but asks for its CA.
	expect(within(sources[1]).queryByTestId("lado-log-watch-insecure")).not.toBeInTheDocument();
	expect(within(sources[1]).getByTestId("lado-log-watch-tls")).toHaveTextContent("until you import the CA certificate");
	expect(screen.queryByTestId("lado-log-watch-mode-off")).not.toBeInTheDocument();
	expect(logWatch.fetchLadoLogWatch).toHaveBeenCalledWith("demo");
});

test("turning TLS off shows the warning at once, and saves only on Save", async () => {
	renderCard();

	const sources = await screen.findAllByTestId("lado-log-watch-source");
	await userEvent.click(within(sources[1]).getByRole("switch", { name: "TLS for prod2" }));
	expect(within(sources[1]).getByTestId("lado-log-watch-insecure")).toBeInTheDocument();
	expect(logWatch.updateLadoLogWatch).not.toHaveBeenCalled();
	await userEvent.click(screen.getByTestId("lado-log-watch-save"));
	await waitFor(() => expect(logWatch.updateLadoLogWatch).toHaveBeenCalledTimes(1));
	const body = vi.mocked(logWatch.updateLadoLogWatch).mock.calls[0][1];
	expect(body.sources[1]).toMatchObject({ id: "src-tls", tls: false });
});

test("says when the project's LADO mode is off", async () => {
	vi.mocked(logWatch.fetchLadoLogWatch).mockResolvedValue(settings({ ladoMode: false }));
	renderCard();

	expect(await screen.findByTestId("lado-log-watch-mode-off")).toHaveTextContent("LADO mode is off");
});

test("CI candidates fill unsaved hosts, and nothing is saved before Save", async () => {
	vi.mocked(logWatch.fetchLadoLogWatch).mockResolvedValue(settings({ sources: [] }));
	vi.mocked(logWatch.importLadoLogCI).mockResolvedValue({
		files: [
			{
				repo: "",
				path: ".gitlab-ci.yml",
				hosts: [
					{ variable: "PROD1_HOST", displayName: "prod1", endpoint: "tcp://192.0.2.10:2375" },
					{ variable: "PROD2_HOST", displayName: "prod2", endpoint: "tcp://198.51.100.20:2375" },
					{ variable: "SECRET_HOST", displayName: "secret", endpoint: "", unresolved: "$SECRET_HOST" },
				],
				composeProjects: ["grp"],
				notes: ["include: is not followed"],
			},
		],
	});
	renderCard();

	await userEvent.click(await screen.findByTestId("lado-log-watch-import-ci"));
	const dialog = await screen.findByTestId("lado-log-watch-import-dialog");
	expect(logWatch.importLadoLogCI).toHaveBeenCalledWith("demo");
	await within(dialog).findByText("tcp://192.0.2.10:2375");
	expect(within(dialog).getByText("$SECRET_HOST is not defined in the file: fill in the endpoint")).toBeInTheDocument();
	expect(within(dialog).getByText("include: is not followed")).toBeInTheDocument();
	const hosts = within(dialog).getAllByTestId("lado-log-watch-import-host");
	await userEvent.click(within(hosts[0]).getByRole("checkbox"));
	await userEvent.click(within(within(dialog).getByTestId("lado-log-watch-import-compose")).getByRole("checkbox"));
	await userEvent.click(within(dialog).getByRole("button", { name: "Add to the card" }));

	await waitFor(() => expect(screen.queryByTestId("lado-log-watch-import-dialog")).not.toBeInTheDocument());
	const sources = screen.getAllByTestId("lado-log-watch-source");
	expect(sources).toHaveLength(1);
	expect(within(sources[0]).getByRole("textbox", { name: "Endpoint of host 1" })).toHaveValue("tcp://192.0.2.10:2375");
	expect(within(sources[0]).getByRole("textbox", { name: "Display name of host 1" })).toHaveValue("prod1");
	expect(within(sources[0]).getByRole("textbox", { name: "Value of container choice 1" })).toHaveValue("grp");
	expect(within(sources[0]).getByText("Not saved yet")).toBeInTheDocument();
	expect(within(sources[0]).getByTestId("lado-log-watch-insecure")).toBeInTheDocument();
	expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
	expect(logWatch.updateLadoLogWatch).not.toHaveBeenCalled();

	await userEvent.click(screen.getByTestId("lado-log-watch-save"));
	await waitFor(() => expect(logWatch.updateLadoLogWatch).toHaveBeenCalledTimes(1));
	const [projectId, body] = vi.mocked(logWatch.updateLadoLogWatch).mock.calls[0];
	expect(projectId).toBe("demo");
	expect(body.sources).toEqual([
		{
			kind: "docker_tcp",
			displayName: "prod1",
			endpoint: "tcp://192.0.2.10:2375",
			tls: false,
			paused: false,
			targets: [{ kind: "compose_project", value: "grp", paused: false }],
		},
	]);
});

test("lists the ignored errors, and one can be ignored no more", async () => {
	vi.mocked(logWatch.fetchLadoLogIncidents).mockResolvedValue([
		ignoredFingerprint,
		{ ...ignoredFingerprint, id: 32, service: "grp/api", ignored: false },
	]);
	vi.mocked(logWatch.setLadoLogFingerprintIgnored).mockResolvedValue({ ...ignoredFingerprint, ignored: false });
	renderCard();

	const list = await screen.findByTestId("lado-log-watch-ignored");
	const items = await within(list).findAllByTestId("lado-log-watch-ignored-item");
	expect(items).toHaveLength(1);
	expect(items[0]).toHaveTextContent("grp/worker · TimeoutError");
	await userEvent.click(within(items[0]).getByRole("button", { name: "Stop ignoring" }));
	expect(logWatch.setLadoLogFingerprintIgnored).toHaveBeenCalledWith("demo", 31, false);
	await waitFor(() => expect(logWatch.fetchLadoLogIncidents).toHaveBeenCalledTimes(2));
});

test("a refused save names the setting", async () => {
	vi.mocked(logWatch.updateLadoLogWatch).mockRejectedValue(
		new lado.LadoApiError("tcp 만 받습니다", "LADO_LOG_WATCH_INVALID", { field: "sources[0].endpoint" }),
	);
	renderCard();

	const sources = await screen.findAllByTestId("lado-log-watch-source");
	const endpoint = within(sources[0]).getByRole("textbox", { name: "Endpoint of host 1" });
	await userEvent.clear(endpoint);
	await userEvent.type(endpoint, "unix:///var/run/docker.sock");
	await userEvent.click(screen.getByTestId("lado-log-watch-save"));
	const alert = await screen.findByRole("alert");
	// Only the translated line: the daemon's message is in Korean.
	expect(alert).toHaveTextContent(/^A log watch setting is not valid: sources\[0\]\.endpoint\.$/);
	expect(screen.queryByText("tcp 만 받습니다")).not.toBeInTheDocument();
	// The edit stays for the developer to fix.
	expect(endpoint).toHaveValue("unix:///var/run/docker.sock");
});
