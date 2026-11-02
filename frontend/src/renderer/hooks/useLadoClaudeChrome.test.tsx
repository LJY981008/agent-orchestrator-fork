import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { GET: getMock },
}));

import {
	browserMentionState,
	ladoClaudeChromeQueryRoot,
	ladoClaudeChromeSessionQueryKey,
	useLadoClaudeChrome,
} from "./useLadoClaudeChrome";

const PATH = "/api/v1/lado/sessions/{sessionId}/claude-chrome";

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => getMock.mockReset());

describe("query keys", () => {
	// The settings screen invalidates the root, /clear one session's key; both
	// have to match what the other parts use.
	it("share one root", () => {
		expect(ladoClaudeChromeQueryRoot).toEqual(["lado", "claude-chrome"]);
		expect(ladoClaudeChromeSessionQueryKey("s1")).toEqual(["lado", "claude-chrome", "session", "s1"]);
	});
});

describe("browserMentionState", () => {
	it("maps a supported session", () => {
		expect(browserMentionState({ sessionId: "s1", supported: true, enabled: true, settingOn: true, basis: "launch", kind: "chat" }))
			.toEqual({ enabled: true, settingOn: true, kind: "chat" });
		expect(browserMentionState({ sessionId: "s1", supported: true, enabled: false, settingOn: false, basis: "setting", kind: "lado_worker" }))
			.toEqual({ enabled: false, settingOn: false, kind: "lado_worker" });
	});

	it("has no mention for an unsupported session or no answer", () => {
		expect(browserMentionState({ sessionId: "s1", supported: false, enabled: false, settingOn: false })).toBeUndefined();
		expect(browserMentionState(undefined)).toBeUndefined();
	});
});

describe("useLadoClaudeChrome", () => {
	it("reads the session's state", async () => {
		getMock.mockResolvedValue({
			data: { sessionId: "s1", supported: true, enabled: true, settingOn: true, basis: "launch", kind: "chat" },
			error: undefined,
		});
		const { result } = renderHook(() => useLadoClaudeChrome("s1"), { wrapper });

		await waitFor(() => expect(result.current).toEqual({ enabled: true, settingOn: true, kind: "chat" }));
		expect(getMock).toHaveBeenCalledWith(PATH, { params: { path: { sessionId: "s1" } } });
	});

	it("gives no mention on an error", async () => {
		getMock.mockResolvedValue({ data: undefined, error: { code: "SESSION_NOT_FOUND", message: "not found" } });
		const { result } = renderHook(() => useLadoClaudeChrome("s1"), { wrapper });

		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(result.current).toBeUndefined();
	});

	it("does not ask when disabled", () => {
		const { result } = renderHook(() => useLadoClaudeChrome("s1", false), { wrapper });

		expect(getMock).not.toHaveBeenCalled();
		expect(result.current).toBeUndefined();
	});
});
