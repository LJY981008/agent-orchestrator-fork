import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createFullAppI18n } from "../../i18n/all-messages";
import * as lado from "../../lib/lado";
import { LadoAccountLimitRelease, ladoAccountLimitLabel, ladoTaskAccountLimited } from "./LadoAccountLimitRelease";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoAccountLimits: vi.fn(),
	releaseLadoAccountLimit: vi.fn(),
}));

/** An active limit on this device's Claude login unless said otherwise. */
function accountLimit(fields: Partial<lado.LadoAccountLimit> = {}): lado.LadoAccountLimit {
	return {
		accountKey: "claude:device-claude",
		provider: "claude",
		accountId: "device-claude",
		active: true,
		identityKnown: true,
		detectedAt: "2026-11-10T14:00:00Z",
		resetAt: "2026-11-10T19:00:00Z",
		releasedWaiting: 0,
		resetGuessed: false,
		updatedAt: "2026-11-10T14:00:00Z",
		...fields,
	};
}

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "queued",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-11-10T09:00:00Z" },
	plan: { requestPending: false },
	createdAt: "2026-11-10T09:00:00Z",
};

const part: lado.LadoTaskPart = {
	id: "api",
	title: "API",
	phase: 1,
	steps: [1],
	affectedPaths: ["api/**"],
	dependsOn: [],
	state: "waiting",
	attempts: 0,
	mergeAttempts: 0,
	updatedAt: "2026-11-10T09:00:00Z",
};

function apiError(code: string): lado.LadoApiError {
	return new lado.LadoApiError("The daemon's English text", code);
}

function renderRelease() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	render(
		<QueryClientProvider client={client}>
			<LadoAccountLimitRelease />
		</QueryClientProvider>,
	);
	return { invalidate };
}

beforeEach(() => {
	vi.resetAllMocks();
});

afterEach(cleanup);

describe("ladoTaskAccountLimited", () => {
	it("holds for a queued task, a waiting plan worker or a waiting part on an account limit", () => {
		expect(ladoTaskAccountLimited({ ...task, stateReason: "account_limited" })).toBe(true);
		expect(ladoTaskAccountLimited({ ...task, state: "dispatching", stateReason: "account_limited" })).toBe(true);
		expect(ladoTaskAccountLimited({ ...task, stateReason: "slot_global" })).toBe(false);
		// A reason left from the queue does not hold once the task moved on.
		expect(ladoTaskAccountLimited({ ...task, state: "failed", stateReason: "account_limited" })).toBe(false);

		const worker: lado.LadoPlanWorker = { requestId: "req-1", phase: "waiting" };
		const drafted = { ...task, state: "draft" as const, plan: { requestPending: true, worker: { ...worker, waitingReason: "account_limited" as const } } };
		expect(ladoTaskAccountLimited(drafted)).toBe(true);
		expect(ladoTaskAccountLimited({ ...drafted, plan: { requestPending: true, worker: { ...worker, waitingReason: "memory_tight" } } })).toBe(false);

		const running = { ...task, state: "running" as const };
		expect(ladoTaskAccountLimited(running, [part, { ...part, id: "web", waitingReason: "account_limited" }])).toBe(true);
		expect(ladoTaskAccountLimited(running, [{ ...part, waitingReason: "part_dependency" }])).toBe(false);
		expect(ladoTaskAccountLimited(running)).toBe(false);
	});

	it("names the device's login and a named account", () => {
		const t = createFullAppI18n("en").t;
		expect(ladoAccountLimitLabel(accountLimit(), t)).toBe("Claude · this device's login");
		expect(ladoAccountLimitLabel(accountLimit({ accountKey: "codex:work", provider: "codex", accountId: "work" }), t)).toBe("Codex · work");
	});
});

describe("LadoAccountLimitRelease", () => {
	it("hides without an active limit", async () => {
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValue([accountLimit({ active: false, releasedAt: "2026-11-10T15:00:00Z", releaseReason: "manual" })]);
		renderRelease();

		await waitFor(() => expect(lado.fetchLadoAccountLimits).toHaveBeenCalled());
		expect(screen.queryByTestId("lado-account-limit-release")).not.toBeInTheDocument();
	});

	it("lists the active limits, releases the one chosen with the app's reason and reads the limits and tasks again", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValue([
			accountLimit(),
			accountLimit({ accountKey: "codex:device-codex", provider: "codex", accountId: "device-codex", resetGuessed: true }),
			accountLimit({ accountKey: "codex:old", provider: "codex", accountId: "old", active: false, releaseReason: "reset" }),
		]);
		vi.mocked(lado.releaseLadoAccountLimit).mockResolvedValue({ limit: accountLimit({ active: false, releaseReason: "manual" }), released: true, waiting: 3 });
		const { invalidate } = renderRelease();

		await user.click(await screen.findByRole("button", { name: "Release limit (I switched accounts)" }));
		const dialog = await screen.findByTestId("lado-account-limit-dialog");
		const rows = within(dialog).getAllByTestId("lado-account-limit");
		expect(rows.map((row) => row.getAttribute("data-account-key"))).toEqual(["claude:device-claude", "codex:device-codex"]);
		expect(rows[0]).toHaveTextContent("Claude · this device's login");
		expect(rows[0]).toHaveTextContent("Resets at");
		expect(rows[0]).not.toHaveTextContent("(estimated)");
		expect(rows[1]).toHaveTextContent("(estimated)");

		await user.click(within(dialog).getByRole("button", { name: "Release the Claude · this device's login limit" }));
		await waitFor(() =>
			expect(lado.releaseLadoAccountLimit).toHaveBeenCalledWith("claude:device-claude", "Released in the app: I switched accounts"),
		);
		await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: lado.ladoAccountLimitsQueryKey }));
		expect(invalidate).toHaveBeenCalledWith({ queryKey: lado.ladoTasksQueryRoot });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: lado.ladoTaskViewQueryRoot });
		// Another limit still holds, so the dialog stays for it.
		expect(screen.getByTestId("lado-account-limit-dialog")).toBeInTheDocument();
	});

	it("closes once the last active limit is released", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValueOnce([accountLimit()]).mockResolvedValue([accountLimit({ active: false, releaseReason: "manual" })]);
		vi.mocked(lado.releaseLadoAccountLimit).mockResolvedValue({ limit: accountLimit({ active: false, releaseReason: "manual" }), released: true, waiting: 1 });
		renderRelease();

		await user.click(await screen.findByTestId("lado-account-limit-release"));
		await user.click(await screen.findByRole("button", { name: /^Release the / }));
		await waitFor(() => expect(screen.queryByTestId("lado-account-limit-dialog")).not.toBeInTheDocument());
		await waitFor(() => expect(screen.queryByTestId("lado-account-limit-release")).not.toBeInTheDocument());
	});

	it.each([
		["LADO_ACCOUNT_LIMIT_NOT_FOUND", "LADO has no usage limit for this account."],
		["LADO_ACCOUNT_LIMIT_NOT_ACTIVE", "This account's usage limit was already released or has reset."],
	])("says why a release was refused (%s) and reads the limits again", async (code, text) => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoAccountLimits).mockResolvedValue([accountLimit()]);
		vi.mocked(lado.releaseLadoAccountLimit).mockRejectedValue(apiError(code));
		const { invalidate } = renderRelease();

		await user.click(await screen.findByTestId("lado-account-limit-release"));
		const dialog = await screen.findByTestId("lado-account-limit-dialog");
		await user.click(within(dialog).getByRole("button", { name: /^Release the / }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent(text);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: lado.ladoAccountLimitsQueryKey });
		expect(invalidate).not.toHaveBeenCalledWith({ queryKey: lado.ladoTasksQueryRoot });
	});
});
