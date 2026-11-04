import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LadoAlert } from "./lado";

const clickListeners = vi.hoisted(() => new Set<(id: string) => void>());

vi.mock("./bridge", () => ({
	aoBridge: {
		notifications: {
			show: vi.fn(async () => undefined),
			onClick: (listener: (id: string) => void) => {
				clickListeners.add(listener);
				return () => clickListeners.delete(listener);
			},
		},
	},
}));

import {
	claimLadoAlert,
	LADO_ALERT_FRESH_MS,
	LADO_ALERT_RING_KEY,
	LADO_ALERT_RING_SIZE,
	ladoAlertShown,
	listenLadoAlertClicks,
	showLadoAlertNotifications,
	subscribeLadoTaskOpenRequests,
	takeLadoTaskOpenRequest,
} from "./lado-alert-notifications";

const NOW = Date.parse("2026-11-04T09:01:00Z");

function alert(change: Partial<LadoAlert> = {}): LadoAlert {
	const id = change.id ?? 7;
	return {
		id,
		projectId: "demo",
		taskId: "lado-task-1",
		sessionId: "demo-3",
		issueRef: "github:acme/demo#12",
		kind: "session_cost",
		level: 1,
		value: 31_00,
		threshold: 30_00,
		line: "LADO 알림: #12 세션 demo-3 비용 추정 $31, 기준 $30 넘음",
		notificationId: `lado-alert:${id}`,
		seenAt: null,
		createdAt: "2026-11-04T09:00:00Z",
		...change,
	};
}

function deps(alerts: LadoAlert[]) {
	return {
		fetchAlerts: vi.fn(async () => alerts),
		show: vi.fn(),
		now: () => NOW,
	};
}

const event = (alertId: number, taskId = "lado-task-1") => ({ alertId, projectId: "demo", taskId });

beforeEach(() => {
	window.localStorage.clear();
	clickListeners.clear();
});

afterEach(() => {
	window.localStorage.clear();
});

describe("showLadoAlertNotifications", () => {
	it("shows a new alert once, with its id, its issue and its line in the developer's language", async () => {
		const d = deps([alert()]);
		await showLadoAlertNotifications([event(7)], d);

		expect(d.fetchAlerts).toHaveBeenCalledWith("demo", "lado-task-1");
		expect(d.show).toHaveBeenCalledTimes(1);
		expect(d.show).toHaveBeenCalledWith({
			id: "lado-alert:7",
			title: "LADO alert · acme/demo#12",
			body: "Session cost estimate $31, past the $30 threshold",
			type: "lado_alert",
		});
		expect(ladoAlertShown(7)).toBe(true);
	});

	it("shows nothing again for a replay, a reload or a second window sharing the ring", async () => {
		const d = deps([alert()]);
		await showLadoAlertNotifications([event(7), event(7)], d);
		// The same event replayed after a reconnect.
		await showLadoAlertNotifications([event(7)], d);
		// A second window reads the same localStorage ring.
		const other = deps([alert()]);
		await showLadoAlertNotifications([event(7)], other);

		expect(d.show).toHaveBeenCalledTimes(1);
		expect(other.fetchAlerts).not.toHaveBeenCalled();
		expect(other.show).not.toHaveBeenCalled();
	});

	it("leaves out alerts already seen, gone or raised long before the event", async () => {
		const d = deps([
			alert({ id: 1, seenAt: "2026-11-04T09:00:30Z" }),
			alert({ id: 2, createdAt: new Date(NOW - LADO_ALERT_FRESH_MS - 1000).toISOString() }),
			alert({ id: 3 }),
		]);
		await showLadoAlertNotifications([event(1), event(2), event(3), event(4)], d);

		expect(d.show).toHaveBeenCalledTimes(1);
		expect(d.show.mock.calls[0][0].id).toBe("lado-alert:3");
	});

	it("reads one task's alerts once for its events and shows the oldest first", async () => {
		const d = deps([alert({ id: 9, createdAt: "2026-11-04T09:00:50Z" }), alert({ id: 8 })]);
		await showLadoAlertNotifications([event(8), event(9)], d);

		expect(d.fetchAlerts).toHaveBeenCalledTimes(1);
		expect(d.show.mock.calls.map(([notification]) => notification.id)).toEqual(["lado-alert:8", "lado-alert:9"]);
	});

	it("shows nothing when the alerts cannot be read", async () => {
		const d = { ...deps([]), fetchAlerts: vi.fn(async () => Promise.reject(new Error("offline"))) };
		await showLadoAlertNotifications([event(7)], d);
		expect(d.show).not.toHaveBeenCalled();
		expect(ladoAlertShown(7)).toBe(false);
	});

	it("keeps the newest shown alerts in a bounded ring", () => {
		for (let id = 1; id <= LADO_ALERT_RING_SIZE + 3; id += 1) {
			expect(claimLadoAlert({ id, projectId: "demo", taskId: "lado-task-1" })).toBe(true);
		}
		expect(claimLadoAlert({ id: LADO_ALERT_RING_SIZE, projectId: "demo", taskId: "lado-task-1" })).toBe(false);
		const ring = JSON.parse(window.localStorage.getItem(LADO_ALERT_RING_KEY) ?? "[]") as unknown[];
		expect(ring).toHaveLength(LADO_ALERT_RING_SIZE);
		expect(ladoAlertShown(1)).toBe(false);
		expect(ladoAlertShown(LADO_ALERT_RING_SIZE + 3)).toBe(true);
	});

	it("reads a broken ring as empty", () => {
		window.localStorage.setItem(LADO_ALERT_RING_KEY, "{not json");
		expect(ladoAlertShown(7)).toBe(false);
		expect(claimLadoAlert({ id: 7, projectId: "demo", taskId: "lado-task-1" })).toBe(true);
	});
});

describe("listenLadoAlertClicks", () => {
	it("opens the alert's task on its Usage tab and leaves AO's own ids alone", () => {
		claimLadoAlert({ id: 7, projectId: "demo", taskId: "lado-task-1" });
		const navigate = vi.fn();
		const notified = vi.fn();
		const unsubscribe = subscribeLadoTaskOpenRequests(notified);
		const stop = listenLadoAlertClicks(navigate);

		for (const listener of clickListeners) listener("notification-1");
		expect(navigate).not.toHaveBeenCalled();
		expect(notified).not.toHaveBeenCalled();

		for (const listener of clickListeners) listener("lado-alert:7");
		expect(navigate).toHaveBeenCalledWith("demo");
		expect(notified).toHaveBeenCalledTimes(1);
		// Only the alert's project's board takes the request, once.
		expect(takeLadoTaskOpenRequest("other")).toBeNull();
		expect(takeLadoTaskOpenRequest("demo")).toEqual({ projectId: "demo", taskId: "lado-task-1", tab: "usage" });
		expect(takeLadoTaskOpenRequest("demo")).toBeNull();

		// An alert this app never showed opens nothing.
		for (const listener of clickListeners) listener("lado-alert:99");
		expect(navigate).toHaveBeenCalledTimes(1);

		stop();
		unsubscribe();
		expect(clickListeners.size).toBe(0);
	});
});
