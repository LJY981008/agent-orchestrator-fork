import type { TFunction } from "i18next";
import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import type { LadoAlert } from "./lado";
import { ladoAlertText, ladoCentsText, ladoCostText, ladoTokensText, ladoUnseenAlertCounts } from "./lado-usage";

const t = appI18n.t.bind(appI18n) as unknown as TFunction;

function ladoAlert(change: Partial<LadoAlert> = {}): LadoAlert {
	return {
		id: 7,
		projectId: "demo",
		taskId: "lado-task-1",
		sessionId: "",
		issueRef: "github:acme/demo#12",
		kind: "task_cost",
		level: 1,
		value: 120_00,
		threshold: 100_00,
		line: "LADO 알림: #12 작업 비용 추정 $120, 기준 $100 넘음",
		notificationId: "lado-alert:7",
		seenAt: null,
		createdAt: "2026-11-04T09:00:00Z",
		...change,
	};
}

describe("LADO usage text", () => {
	it("reads nanodollars as dollars, and a little as under a cent", () => {
		expect(ladoCostText(12_345_000_000, "en")).toBe("$12.35");
		expect(ladoCostText(0, "en")).toBe("$0.00");
		expect(ladoCostText(1_000_000, "en")).toBe("<$0.01");
		expect(ladoCostText(-5, "en")).toBe("$0.00");
	});

	it("reads a threshold in cents without cents when it is whole dollars", () => {
		expect(ladoCentsText(100_00, "en")).toBe("$100");
		expect(ladoCentsText(12_50, "en")).toBe("$12.50");
	});

	it("shortens token counts", () => {
		expect(ladoTokensText(950, "en")).toBe("950");
		expect(ladoTokensText(200_000, "en")).toBe("200K");
		expect(ladoTokensText(1_250_000, "en")).toBe("1.3M");
	});

	it("words each alert kind in the developer's language", () => {
		expect(ladoAlertText(t, ladoAlert(), "en")).toBe("Task cost estimate $120, past the $100 threshold");
		expect(ladoAlertText(t, ladoAlert({ level: 2, value: 210_00 }), "en")).toBe("Task cost estimate $210, past twice the $100 threshold");
		expect(ladoAlertText(t, ladoAlert({ kind: "session_cost", value: 31_00, threshold: 30_00 }), "en")).toBe(
			"Session cost estimate $31, past the $30 threshold",
		);
		expect(ladoAlertText(t, ladoAlert({ kind: "session_compactions", value: 3, threshold: 3 }), "en")).toContain(
			"check whether to split the parts again",
		);
		expect(ladoAlertText(t, ladoAlert({ kind: "limited", value: 0, threshold: 0 }), "en")).toBe("Worker stopped at a usage limit");
		expect(ladoAlertText(t, ladoAlert({ kind: "limited", value: 1_762_250_400, threshold: 0 }), "en")).toMatch(/^Worker stopped at a usage limit until /);
		expect(ladoAlertText(t, ladoAlert({ kind: "limit_resumed", value: 2, threshold: 3 }), "en")).toBe("LADO sent the limit resume message (2/3)");
		expect(ladoAlertText(t, ladoAlert({ kind: "stall_nudged", value: 120, threshold: 120 }), "en")).toBe(
			"Worker quiet for over 120 min; LADO sent the nudge",
		);
		expect(ladoAlertText(t, ladoAlert({ kind: "stalled", value: 120 }), "en")).toBe("Worker quiet for over 120 min");
		expect(ladoAlertText(t, ladoAlert({ kind: "agent_unavailable" }), "en")).toContain("LADO holds the new worker");
		expect(ladoAlertText(t, ladoAlert({ kind: "conflict_unresolved", value: 2 }), "en")).toBe(
			"Resolve workers failed 2 times in a row; resolve the conflict yourself",
		);
		// A kind from a newer daemon reads as its own line.
		expect(ladoAlertText(t, ladoAlert({ kind: "future" as LadoAlert["kind"] }), "en")).toBe(ladoAlert().line);
	});

	it("reads each task's unseen alerts from the daemon's counts", () => {
		// The daemon counts every unseen alert, past any list limit.
		const counts = ladoUnseenAlertCounts({
			tasks: [
				{ taskId: "lado-task-1", unseen: 240 },
				{ taskId: "lado-task-2", unseen: 1 },
			],
		});
		expect(counts.get("lado-task-1")).toBe(240);
		expect(counts.get("lado-task-2")).toBe(1);
		expect(ladoUnseenAlertCounts(undefined).size).toBe(0);
	});
});
