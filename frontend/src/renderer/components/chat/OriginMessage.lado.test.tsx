import { render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it } from "vitest";
import { OriginMessage } from "./ChatTimelineItems";
import type { ConversationMessage } from "../../types/conversation";
import { TooltipProvider } from "../ui/tooltip";

// LADO fork: LADO relays a developer's instruction to a worker with a display
// name and no sender session (docs/LJY-PRD/TDD.md §6.3).
const ladoRelayLabel = "LADO 전달 · 개발자 지시";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

function automationMessage(overrides: Partial<ConversationMessage> = {}): ConversationMessage {
	return {
		kind: "message",
		id: "relay-1",
		sequence: 3,
		revision: 1,
		role: "user",
		origin: "automation",
		text: "LADO 전달: 개발자 지시\n\n후속 2건을 이어서 하세요.",
		streaming: false,
		createdAt: "2026-11-05T01:00:00Z",
		...overrides,
	};
}

describe("OriginMessage label for LADO's relay", () => {
	it("shows the stored display name when no session sent the message", () => {
		render(<OriginMessage message={automationMessage({ senderDisplayName: ` ${ladoRelayLabel} ` })} />);

		expect(screen.getByText(ladoRelayLabel)).toBeInTheDocument();
		expect(screen.queryByText("automation")).not.toBeInTheDocument();
		expect(screen.queryByRole("link")).not.toBeInTheDocument();
	});

	it("keeps the origin as the label without a display name", () => {
		render(<OriginMessage message={automationMessage()} />);

		expect(screen.getByText("automation")).toBeInTheDocument();
	});

	it("labels a session's message from the session, not as LADO", () => {
		render(
			<OriginMessage
				message={automationMessage({
					text: "rebase onto main",
					senderSessionId: "mer-orchestrator-1",
					senderDisplayName: "Orchestrator",
				})}
			/>,
		);

		expect(screen.getByText(/\[from/)).toHaveTextContent("[from Orchestrator]");
		expect(screen.queryByText(ladoRelayLabel)).not.toBeInTheDocument();
	});
});
