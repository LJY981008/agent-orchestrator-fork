import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import * as lado from "../../lib/lado";
import type { WorkspaceSession } from "../../types/workspace";
import { BoardSessionCardAdapter } from "../SessionsBoardAdapters";
import { TooltipProvider } from "../ui/tooltip";
import { LadoSessionLine } from "./LadoSessionLine";

vi.mock("../../lib/lado", async (original) => ({
	...await original<typeof import("../../lib/lado")>(),
	fetchLadoProjectSettings: vi.fn(),
	fetchLadoTasks: vi.fn(),
}));

// The card's PR facts come from the daemon; these cards have none.
vi.mock("../../hooks/useSessionScmSummary", async (original) => ({
	...await original<typeof import("../../hooks/useSessionScmSummary")>(),
	useSessionScmSummary: () => ({ data: undefined }),
}));

function session(id: string, fields: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId: "demo",
		workspaceName: "Demo",
		title: `Session ${id}`,
		provider: "claude-code",
		kind: "worker",
		status: "working",
		updatedAt: "2026-10-23T09:00:00Z",
		prs: [],
		...fields,
	};
}

const execution: lado.LadoTaskExecution = {
	sessionId: "demo-7",
	planRevisionId: "lado-plan-3",
	planRevisionNumber: 3,
	policySha256: "1a2b3c4d".padEnd(64, "0"),
	startedAt: "2026-10-23T08:00:00Z",
	sessionEnded: false,
	timedOut: false,
};

const task: lado.LadoTask = {
	id: "lado-task-1",
	projectId: "demo",
	repository: "github:acme/demo",
	state: "running",
	issue: { ref: "github:acme/demo#12", number: 12, title: "Add login", state: "open", url: "https://github.com/acme/demo/issues/12", observedAt: "2026-10-23T07:00:00Z" },
	plan: { requestPending: false },
	createdAt: "2026-10-23T07:00:00Z",
	execution,
};

function renderWith(children: React.ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider delayDuration={0}>{children}</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.resetAllMocks();
	vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue({ projectId: "demo", ladoMode: true, maxConcurrent: 1 });
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([task, { ...task, id: "lado-task-2", state: "queued", execution: undefined }]);
});

afterEach(cleanup);

test("only a LADO run's board card gets the LADO line", async () => {
	renderWith(
		<div role="list">
			<BoardSessionCardAdapter onOpen={() => undefined} onOpenArtifact={() => undefined} session={session("demo-7")} />
			<BoardSessionCardAdapter onOpen={() => undefined} onOpenArtifact={() => undefined} session={session("demo-8")} />
		</div>,
	);

	const line = await screen.findByTestId("lado-session-line");
	expect(line).toHaveTextContent("LADO · plan r3 · rules 1a2b3c4");
	expect(within(line).getByText("1a2b3c4")).toHaveClass("font-mono");
	expect(line).toHaveClass("text-muted-foreground");
	const [run, other] = screen.getAllByTestId("board-session-card");
	expect(run).toHaveAttribute("data-session-id", "demo-7");
	expect(within(run).getByTestId("lado-session-line")).toBe(line);
	expect(within(other).queryByTestId("lado-session-line")).not.toBeInTheDocument();
	// Above the card's status line.
	expect(line.compareDocumentPosition(within(run).getByTestId("session-status")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	// Every card reads the one task list the board loads.
	expect(lado.fetchLadoTasks).toHaveBeenCalledTimes(1);
	expect(lado.fetchLadoTasks).toHaveBeenCalledWith("demo");
});

test("leaves out what the task list omits", async () => {
	vi.mocked(lado.fetchLadoTasks).mockResolvedValue([{ ...task, execution: { ...execution, planRevisionNumber: undefined, policySha256: undefined } }]);
	renderWith(<LadoSessionLine session={session("demo-7")} />);

	const line = await screen.findByTestId("lado-session-line");
	expect(line).toHaveTextContent(/^LADO$/);
});

test("a project out of LADO mode shows no line and reads no tasks", async () => {
	vi.mocked(lado.fetchLadoProjectSettings).mockResolvedValue({ projectId: "demo", ladoMode: false, maxConcurrent: 1 });
	const { container } = renderWith(<LadoSessionLine session={session("demo-7")} />);

	await waitFor(() => expect(lado.fetchLadoProjectSettings).toHaveBeenCalledWith("demo"));
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(lado.fetchLadoTasks).not.toHaveBeenCalled();
	expect(container).toBeEmptyDOMElement();
});

test("another computer's and orchestrator sessions ask nothing", async () => {
	const { container } = renderWith(
		<>
			<LadoSessionLine session={session("demo-7", { hostId: "host-2" })} />
			<LadoSessionLine session={session("demo-orchestrator", { kind: "orchestrator" })} />
		</>,
	);

	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(lado.fetchLadoProjectSettings).not.toHaveBeenCalled();
	expect(lado.fetchLadoTasks).not.toHaveBeenCalled();
	expect(container).toBeEmptyDOMElement();
});
