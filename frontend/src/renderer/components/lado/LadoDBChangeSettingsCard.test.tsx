import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { appCatalogs } from "../../i18n/all-messages";
import { LadoApiError } from "../../lib/lado";
import * as dbChanges from "../../lib/lado-db-changes";
import { TooltipProvider } from "../ui/tooltip";
import { LadoDBChangeSettingsCard } from "./LadoDBChangeSettingsCard";

vi.mock("../../lib/lado-db-changes", async (original) => ({
	...await original<typeof import("../../lib/lado-db-changes")>(),
	fetchLadoDBChangeSettings: vi.fn(),
	updateLadoDBChangeSettings: vi.fn(),
}));

const SECRET = "postgres://app:hunter2@db.example.test/prod";

function repo(fields: Partial<dbChanges.LadoDBChangeRepoSettings> = {}): dbChanges.LadoDBChangeRepoSettings {
	return {
		repo: "",
		name: "demo",
		enabled: true,
		paths: ["scripts/migrations/*.sql"],
		command: "psql \"$LADO_DB_DSN\" -v ON_ERROR_STOP=1 -f {file}",
		timeoutMinutes: 15,
		secretNames: ["LADO_DB_DSN"],
		storedSecretNames: ["LADO_DB_DSN"],
		sha256: "s".repeat(64),
		baselineCommit: "0123456789abcdef0123456789abcdef01234567",
		...fields,
	};
}

function settings(repositories: dbChanges.LadoDBChangeRepoSettings[]): dbChanges.LadoDBChangeSettings {
	return { projectId: "demo", sha256: "t".repeat(64), repositories };
}

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>
				<LadoDBChangeSettingsCard projectId="demo" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

test("shows a repository's files, command, time limit and secret names, never a value", async () => {
	vi.mocked(dbChanges.fetchLadoDBChangeSettings).mockResolvedValue(
		settings([repo({ secretNames: ["LADO_DB_DSN", "PGPASSWORD"], storedSecretNames: ["LADO_DB_DSN"] })]),
	);
	renderCard();

	const toggle = await screen.findByRole("switch", { name: "Apply SQL with the integration approval" });
	expect(toggle).toBeChecked();
	expect(screen.getByLabelText("SQL files")).toHaveValue("scripts/migrations/*.sql");
	expect(screen.getByLabelText("Apply command")).toHaveValue("psql \"$LADO_DB_DSN\" -v ON_ERROR_STOP=1 -f {file}");
	expect(screen.getByRole("button", { name: "Time limit per file" })).toHaveTextContent("15 minutes");
	expect(screen.getByLabelText("Secret 1 name")).toHaveValue("LADO_DB_DSN");
	const stored = screen.getByLabelText("Secret 1 value");
	expect(stored).toHaveValue("");
	expect(stored).toHaveAttribute("type", "password");
	expect(stored).toHaveAttribute("placeholder", "Stored · type to replace");
	expect(screen.getByLabelText("Secret 2 value")).toHaveAttribute("placeholder", "Value");
	expect(screen.getByTestId("lado-db-changes-missing")).toHaveTextContent("No value stored for PGPASSWORD");
	expect(screen.getByTestId("lado-db-changes-baseline")).toHaveTextContent("0123456");
	// Nothing changed yet: no save.
	expect(screen.queryByTestId("lado-db-changes-save")).toBeNull();
});

test("saves through the desktop confirmation with the base, and forgets a typed value once saved", async () => {
	const user = userEvent.setup();
	const saved = repo();
	vi.mocked(dbChanges.fetchLadoDBChangeSettings).mockResolvedValue(settings([saved]));
	vi.mocked(dbChanges.updateLadoDBChangeSettings).mockResolvedValue(settings([repo({ sha256: "u".repeat(64), paths: ["db/*.sql"] })]));
	renderCard();

	const paths = await screen.findByLabelText("SQL files");
	await user.clear(paths);
	await user.type(paths, "db/*.sql");
	await user.type(screen.getByLabelText("Secret 1 value"), SECRET);
	await user.click(screen.getByTestId("lado-db-changes-save"));

	await waitFor(() => expect(dbChanges.updateLadoDBChangeSettings).toHaveBeenCalledTimes(1));
	expect(dbChanges.updateLadoDBChangeSettings).toHaveBeenCalledWith("demo", {
		repo: "",
		base: saved.sha256,
		enabled: true,
		paths: ["db/*.sql"],
		command: saved.command,
		timeoutMinutes: 15,
		secretNames: ["LADO_DB_DSN"],
		secrets: { set: { LADO_DB_DSN: SECRET } },
	});
	await waitFor(() => expect(screen.getByLabelText("Secret 1 value")).toHaveValue(""));
	expect(screen.getByLabelText("SQL files")).toHaveValue("db/*.sql");
	expect(document.body.innerHTML).not.toContain("hunter2");
	expect(screen.queryByTestId("lado-db-changes-save")).toBeNull();
});

test("keeps the edits when the developer cancels, and names each refused field", async () => {
	const user = userEvent.setup();
	vi.mocked(dbChanges.fetchLadoDBChangeSettings).mockResolvedValue(settings([repo()]));
	vi.mocked(dbChanges.updateLadoDBChangeSettings).mockResolvedValueOnce(null);
	renderCard();

	const command = await screen.findByLabelText("Apply command");
	await user.clear(command);
	await user.type(command, "psql -f file.sql");
	await user.click(screen.getByTestId("lado-db-changes-save"));
	await waitFor(() => expect(dbChanges.updateLadoDBChangeSettings).toHaveBeenCalledTimes(1));
	expect(screen.getByLabelText("Apply command")).toHaveValue("psql -f file.sql");

	vi.mocked(dbChanges.updateLadoDBChangeSettings).mockRejectedValueOnce(
		new LadoApiError("bad", "LADO_DB_CHANGE_INVALID", {
			problems: [
				{ field: "command", reason: "no_file_placeholder" },
				{ field: "secretNames[0]", reason: "reserved" },
			],
		}),
	);
	await user.click(screen.getByTestId("lado-db-changes-save"));
	const problems = await screen.findByTestId("lado-db-changes-problems");
	expect(within(problems).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
		"Apply command: must contain {file}",
		"Secret names 1: is a name LADO keeps for itself, such as PATH or GIT_SSH_COMMAND",
	]);

	vi.mocked(dbChanges.updateLadoDBChangeSettings).mockRejectedValueOnce(new LadoApiError("no app", "LADO_PRESENCE_UNAVAILABLE"));
	await user.click(screen.getByTestId("lado-db-changes-save"));
	expect(await screen.findByRole("alert")).toHaveTextContent("needs the AO desktop app");
});

test("turns a workspace's child repository on by name, and offers its fields only while on", async () => {
	const user = userEvent.setup();
	vi.mocked(dbChanges.fetchLadoDBChangeSettings).mockResolvedValue(
		settings([
			repo({ repo: "back", name: "back", sha256: "b".repeat(64) }),
			repo({ repo: "front", name: "front", enabled: false, paths: [], command: "", timeoutMinutes: 0, secretNames: [], storedSecretNames: [], sha256: "", baselineCommit: undefined }),
		]),
	);
	vi.mocked(dbChanges.updateLadoDBChangeSettings).mockResolvedValue(null);
	renderCard();

	expect(await screen.findByRole("switch", { name: "Apply SQL in back with the integration approval" })).toBeChecked();
	const front = screen.getByRole("switch", { name: "Apply SQL in front with the integration approval" });
	expect(front).not.toBeChecked();
	expect(screen.getAllByTestId("lado-db-changes-repo").map((element) => element.dataset.repo)).toEqual(["back"]);

	await user.click(front);
	expect(screen.getAllByTestId("lado-db-changes-repo").map((element) => element.dataset.repo)).toEqual(["back", "front"]);
	const frontRepo = screen.getAllByTestId("lado-db-changes-repo")[1];
	await user.type(within(frontRepo).getByLabelText("SQL files"), "sql/*.sql");
	await user.type(within(frontRepo).getByLabelText("Apply command"), "mysql < {{file}");
	await user.click(within(frontRepo).getByRole("button", { name: "Add secret" }));
	await user.type(within(frontRepo).getByLabelText("Secret 1 name"), "db_url");
	expect(within(frontRepo).getByLabelText("Secret 1 name")).toHaveValue("DB_URL");
	await user.click(screen.getByTestId("lado-db-changes-save"));

	await waitFor(() => expect(dbChanges.updateLadoDBChangeSettings).toHaveBeenCalledTimes(1));
	expect(dbChanges.updateLadoDBChangeSettings).toHaveBeenCalledWith("demo", {
		repo: "front",
		base: "",
		enabled: true,
		paths: ["sql/*.sql"],
		command: "mysql < {file}",
		timeoutMinutes: 10,
		secretNames: ["DB_URL"],
	});
});

test("every locale words the card and its problems", () => {
	for (const [locale, catalog] of Object.entries(appCatalogs)) {
		const messages = catalog as Record<string, string>;
		for (const key of ["lado.dbChanges.title", "lado.dbChanges.problem.reserved", "lado.dbChanges.secretStored", "lado.error.dbChangeBaselineFailed"]) {
			expect(messages[key], `${locale} ${key}`).toBeTruthy();
		}
	}
	expect((appCatalogs.ko as Record<string, string>)["lado.dbChanges.title"]).toBe("운영 DB 변경");
});
