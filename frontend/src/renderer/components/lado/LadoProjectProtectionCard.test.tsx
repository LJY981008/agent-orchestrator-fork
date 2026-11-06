import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as lado from "../../lib/lado";
import { LadoProjectProtectionCard } from "./LadoProjectProtectionCard";

vi.mock("../../lib/lado", async (original) => ({
	...(await original<typeof import("../../lib/lado")>()),
	fetchLadoProtection: vi.fn(),
}));

const CHECKED = "2026-10-27T09:00:00Z";

function branch(fields: Partial<lado.LadoBranchProtection> & Pick<lado.LadoBranchProtection, "repository" | "state">): lado.LadoBranchProtection {
	return { branch: "main", missing: [], requiredChecks: [], checkedAt: CHECKED, ...fields };
}

const protectedRepo = branch({ repository: "github:acme/demo", state: "protected", requiredChecks: ["build"] });
const unprotectedRepo = branch({
	repository: "github:acme/api",
	branch: "develop",
	state: "unprotected",
	missing: ["pull_request", "required_status_checks:lado/integration", "non_fast_forward", "deletion"],
});
const unreadableRepo = branch({
	repository: "github:acme/web",
	state: "unknown",
	error: "github tracker: read the branch rules: the host answered 403: Resource not accessible",
	checkedAt: "2026-10-27T08:55:00Z",
});
const gitlabRepo = branch({ repository: "gitlab:group/tools@gitlab.example.com", state: "unsupported" });

function renderCard() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<LadoProjectProtectionCard projectId="demo" />
		</QueryClientProvider>,
	);
}

function rowOf(label: string): HTMLElement {
	const row = screen.getAllByTestId("lado-protection-repository").find((element) => element.textContent?.includes(label));
	if (!row) throw new Error(`no row for ${label}`);
	return row;
}

const guide =
	"In GitHub, add a ruleset for the base branch under Settings → Rules → Rulesets: require a pull request and the status check lado/integration, and block force pushes and deletions.";

// LADO fork: Project settings → LADO → Branch protection (docs/LJY-PRD/TDD.md §7.1).
describe("LadoProjectProtectionCard", () => {
	beforeEach(() => {
		vi.resetAllMocks();
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({
			projectId: "demo",
			repositories: [protectedRepo, unprotectedRepo, unreadableRepo, gitlabRepo],
		});
	});

	afterEach(cleanup);

	it("names each repository's base branch with its state and what it lacks", async () => {
		renderCard();

		expect(await screen.findByRole("heading", { name: "Branch protection" })).toBeInTheDocument();
		await waitFor(() => expect(screen.getAllByTestId("lado-protection-repository")).toHaveLength(4));
		expect(lado.fetchLadoProtection).toHaveBeenCalledWith("demo");

		const ready = rowOf("acme/demo · main");
		expect(ready).toHaveTextContent(/^acme\/demo · mainProtected$/);
		expect(within(ready).getByText("acme/demo")).toHaveClass("font-mono");
		expect(within(ready).getByText("main")).toHaveClass("font-mono");
		expect(ready.querySelector("[aria-hidden='true'].rounded-full")).toHaveClass("bg-[var(--color-status-ready)]");

		const open = rowOf("acme/api · develop");
		expect(within(open).getByText("Not protected")).toBeInTheDocument();
		expect(
			within(open).getByText(
				"Missing: Pull requests required, lado/integration required, Force pushes blocked, Deletion blocked",
			),
		).toBeInTheDocument();
		expect(open.querySelector("[aria-hidden='true'].rounded-full")).toHaveClass("bg-[var(--color-status-in-review)]");

		const unreadable = rowOf("acme/web · main");
		expect(within(unreadable).getByText("Could not check")).toBeInTheDocument();
		expect(
			within(unreadable).getByText("Could not read the rules: github tracker: read the branch rules: the host answered 403: Resource not accessible"),
		).toBeInTheDocument();

		const gitlab = rowOf("group/tools · main");
		expect(within(gitlab).getByText("Not protected")).toBeInTheDocument();
		expect(within(gitlab).getByText("GitLab: LADO only detects")).toBeInTheDocument();

		// What to set in GitHub, under the card, once a branch lacks it.
		expect(screen.getByTestId("lado-protection-guide")).toHaveTextContent(guide);
		expect(within(screen.getByTestId("lado-protection-guide")).getByText("lado/integration")).toHaveClass("font-mono");
		// The oldest read is the time the card stands for.
		expect(screen.getByText(/^Last checked /)).toHaveAttribute("title", expect.stringContaining("2026"));
	});

	// Q3 of #15: LADO's status cannot fill a check taken from one app only.
	it("says to take lado/integration from any source", async () => {
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({
			projectId: "demo",
			repositories: [{ ...unprotectedRepo, missing: ["required_status_checks:lado/integration:any_source"] }],
		});
		renderCard();

		const open = await waitFor(() => rowOf("acme/api · develop"));
		expect(open).toHaveTextContent("Missing: lado/integration from any source");
		// The guide is a line of its own, not run on after the missing part.
		expect(within(open).getByText("Missing: lado/integration from any source")).not.toHaveTextContent("Any source");
		expect(within(open).getByText(/set the check's source to “Any source”/)).not.toHaveTextContent("Missing");
	});

	it("shows no guide while every branch is protected or on GitLab", async () => {
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({ projectId: "demo", repositories: [protectedRepo, gitlabRepo] });
		renderCard();

		await waitFor(() => expect(screen.getAllByTestId("lado-protection-repository")).toHaveLength(2));
		expect(screen.queryByTestId("lado-protection-guide")).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-protection-guide-gitlab")).not.toBeInTheDocument();
	});

	// #21: LADO reads a GitLab branch's protection and the project's merge settings.
	it("names what a GitLab branch lacks and how to set it in GitLab", async () => {
		const gitlabOpen = branch({
			repository: "gitlab:group/app@gitlab.example.com",
			state: "unprotected",
			missing: ["protected_branch", "pipeline_must_succeed", "skipped_pipeline", "integration_status"],
		});
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({ projectId: "demo", repositories: [protectedRepo, gitlabOpen] });
		renderCard();

		const open = await waitFor(() => rowOf("group/app · main"));
		expect(within(open).getByText("Not protected")).toBeInTheDocument();
		expect(
			within(open).getByText(
				"Missing: Protected branch, Pipelines must succeed, Skipped pipelines not counted as successful, LADO's lado/integration status",
			),
		).toBeInTheDocument();
		// GitLab's guide, not GitHub's rulesets.
		const gitlabGuide = screen.getByTestId("lado-protection-guide-gitlab");
		expect(gitlabGuide).toHaveTextContent(
			"In GitLab, protect the base branch under Settings → Repository → Protected branches with Allowed to push set to No one and Allowed to force push off. Under Settings → Merge requests, turn on “Pipelines must succeed” and turn off “Skipped pipelines are considered successful”. LADO's token needs the Developer role or higher and the api scope.",
		);
		expect(within(gitlabGuide).getByText("api")).toHaveClass("font-mono");
		expect(screen.queryByTestId("lado-protection-guide")).not.toBeInTheDocument();
	});

	it("shows GitLab's guide for an unreadable GitLab branch and both guides for both hosts", async () => {
		const gitlabUnknown = branch({ repository: "gitlab:group/app@gitlab.example.com", state: "unknown", error: "gitlab tracker: 403" });
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({ projectId: "demo", repositories: [unprotectedRepo, gitlabUnknown] });
		renderCard();

		const unreadable = await waitFor(() => rowOf("group/app · main"));
		expect(within(unreadable).getByText("Could not read the rules: gitlab tracker: 403")).toBeInTheDocument();
		expect(screen.getByTestId("lado-protection-guide")).toHaveTextContent(guide);
		expect(screen.getByTestId("lado-protection-guide-gitlab")).toHaveTextContent(/^In GitLab, protect the base branch/);
	});

	it("asks the host again on Check again", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoProtection)
			.mockResolvedValueOnce({ projectId: "demo", repositories: [unprotectedRepo] })
			.mockResolvedValue({ projectId: "demo", repositories: [{ ...unprotectedRepo, state: "protected", missing: [] }] });
		renderCard();

		await screen.findByText(/^Missing: /);
		await user.click(screen.getByRole("button", { name: "Check again" }));

		await waitFor(() => expect(within(rowOf("acme/api · develop")).getByText("Protected")).toBeInTheDocument());
		expect(vi.mocked(lado.fetchLadoProtection).mock.calls).toEqual([["demo"], ["demo", true]]);
		expect(screen.queryByText(/^Missing: /)).not.toBeInTheDocument();
		expect(screen.queryByTestId("lado-protection-guide")).not.toBeInTheDocument();
	});

	it("shows a failed read in place", async () => {
		const user = userEvent.setup();
		vi.mocked(lado.fetchLadoProtection)
			.mockResolvedValueOnce({ projectId: "demo", repositories: [protectedRepo] })
			.mockRejectedValue(new lado.LadoApiError("project not found", "PROJECT_NOT_FOUND", {}));
		renderCard();

		await waitFor(() => expect(screen.getAllByTestId("lado-protection-repository")).toHaveLength(1));
		await user.click(screen.getByRole("button", { name: "Check again" }));

		expect(await screen.findByRole("alert")).toBeInTheDocument();
		// The last read stays on the card.
		expect(within(rowOf("acme/demo · main")).getByText("Protected")).toBeInTheDocument();
	});

	it("says when the project has no repository to check", async () => {
		vi.mocked(lado.fetchLadoProtection).mockResolvedValue({ projectId: "demo", repositories: [] });
		renderCard();

		expect(await screen.findByText("No repositories to check.")).toBeInTheDocument();
	});
});
