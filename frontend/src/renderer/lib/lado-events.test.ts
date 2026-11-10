import { QueryClient, type QueryKey } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
	type LadoTask,
	ladoAlertsQueryKey,
	ladoEffectiveProjectQueryKey,
	ladoEffectiveQueryKey,
	ladoProjectUsageQueryKey,
	ladoRuleQueryKey,
	ladoRulesQueryKey,
	ladoTaskRelaysQueryKey,
	ladoTasksQueryKey,
	ladoTaskUsageQueryKey,
	ladoTaskViewQueryKey,
} from "./lado";
import { createLadoEventBatch } from "./lado-events";
import { ladoLogIncidentsQueryKey, ladoLogWatchQueryKey } from "./lado-log-watch";
import {
	ladoSetupAdoptionQueryKey,
	ladoSetupConflictsQueryKey,
	ladoSetupInventoryQueryKey,
	ladoSetupItemQueryKey,
	ladoSetupItemsQueryKey,
} from "./lado-setup";

// The views a test caches, by name.
const views: Record<string, QueryKey> = {
	inventory: ladoSetupInventoryQueryKey,
	globalConflicts: ladoSetupConflictsQueryKey(),
	projectConflicts: ladoSetupConflictsQueryKey({ projectId: "demo" }),
	taskConflicts: ladoSetupConflictsQueryKey({ projectId: "demo", taskId: "lado-task-1" }),
	projectItems: ladoSetupItemsQueryKey({ projectId: "demo" }),
	item: ladoSetupItemQueryKey("item-1"),
	adoption: ladoSetupAdoptionQueryKey("adoption-1"),
	projectEffective: ladoEffectiveProjectQueryKey("demo"),
	taskEffective: ladoEffectiveQueryKey("demo", "lado-task-1"),
	tasks: ladoTasksQueryKey("demo"),
	taskView: ladoTaskViewQueryKey("lado-task-1"),
	taskRelays: ladoTaskRelaysQueryKey("lado-task-1"),
	otherTaskRelays: ladoTaskRelaysQueryKey("lado-task-2"),
	otherEffective: ladoEffectiveProjectQueryKey("other"),
	otherTasks: ladoTasksQueryKey("other"),
	rules: ladoRulesQueryKey(false),
	rule: ladoRuleQueryKey("team.review"),
};

const setupViews = ["adoption", "globalConflicts", "inventory", "item", "projectConflicts", "projectItems", "taskConflicts"];

type Payload = Parameters<ReturnType<typeof createLadoEventBatch>["note"]>[0];
type Envelope = Parameters<ReturnType<typeof createLadoEventBatch>["note"]>[1];

// Notes the events, flushes the batch into a query client holding every view,
// and names the views the flush invalidated.
function invalidatedBy(events: { payload: Payload; event?: Envelope; lado: boolean }[]): string[] {
	const queryClient = new QueryClient();
	// Empty lists: a task list must read as one when an AO event checks it.
	for (const key of Object.values(views)) queryClient.setQueryData(key, []);
	const batch = createLadoEventBatch(queryClient);
	for (const { payload, event, lado } of events) {
		expect(batch.note(payload, event), JSON.stringify(payload)).toBe(lado);
	}
	batch.flush((queryKey) => void queryClient.invalidateQueries({ queryKey }));
	return Object.entries(views)
		.filter(([, key]) => queryClient.getQueryState(key)?.isInvalidated)
		.map(([name]) => name)
		.sort();
}

// LADO fork: a local setup change reaches every LADO project the way a rule
// change does (docs/LJY-PRD/TDD.md §4.3).
describe("createLadoEventBatch and local setup", () => {
	it("refreshes the setup views and what applies in each project the change reaches", () => {
		expect(invalidatedBy([{ payload: { kind: "lado_setup", projectId: "demo" }, lado: true }])).toEqual(
			[...setupViews, "projectEffective", "taskEffective", "taskView", "tasks"].sort(),
		);
		// One event per LADO project: both projects refresh.
		expect(
			invalidatedBy([
				{ payload: { kind: "lado_setup", projectId: "demo" }, lado: true },
				{ payload: { kind: "lado_setup", projectId: "other" }, lado: true },
			]),
		).toEqual([...setupViews, "otherEffective", "otherTasks", "projectEffective", "taskEffective", "taskView", "tasks"].sort());
	});

	it("refreshes only the setup views when the change names no project", () => {
		expect(invalidatedBy([{ payload: { kind: "lado_setup" }, lado: true }])).toEqual(setupViews);
	});

	it("flushes a setup change once", () => {
		const queryClient = new QueryClient();
		const batch = createLadoEventBatch(queryClient);
		batch.note({ kind: "lado_setup", projectId: "demo" });
		const first: QueryKey[] = [];
		batch.flush((queryKey) => first.push(queryKey));
		expect(first).toContainEqual(["lado", "setup"]);
		const second: QueryKey[] = [];
		batch.flush((queryKey) => second.push(queryKey));
		expect(second).toEqual([]);
	});

	it("leaves the setup views alone for every other kind", () => {
		// A rule change reaches the rules and what applies, never the setup views.
		expect(invalidatedBy([{ payload: { kind: "lado_rules", projectId: "demo" }, lado: true }])).toEqual(
			["projectEffective", "rule", "rules", "taskEffective", "taskView", "tasks"].sort(),
		);
		expect(invalidatedBy([{ payload: { kind: "lado_task", projectId: "demo", taskId: "lado-task-1" }, lado: true }])).toEqual([
			"taskEffective",
			"taskRelays",
		]);
		expect(
			invalidatedBy([{ payload: { kind: "lado_execution", projectId: "demo", sessionId: "demo-7", taskId: "lado-task-1" }, lado: true }]),
		).toEqual(["taskView", "tasks"]);
		// An event of AO's own is not LADO's.
		expect(
			invalidatedBy([{ payload: { isTerminated: true }, event: { type: "session_updated", projectId: "demo", sessionId: "demo-7" }, lado: false }]),
		).toEqual([]);
	});
});

// LADO fork: a task approved again keeps its older run as previousExecution
// (issue #61). The approve button waits while that run's session lives, so
// the task list refreshes when AO ends or restores the session.
describe("createLadoEventBatch and a task's older run", () => {
	const previousTask = (sessionEnded: boolean): LadoTask =>
		({
			id: "lado-task-1",
			projectId: "demo",
			state: "running",
			previousExecution: { sessionId: "demo-7", planRevisionId: "lado-plan-1", startedAt: "2026-11-05T00:00:00Z", timedOut: false, sessionEnded },
		}) as LadoTask;

	function sessionUpdated(tasks: LadoTask[], isTerminated: boolean): QueryKey[] {
		const queryClient = new QueryClient();
		queryClient.setQueryData(ladoTasksQueryKey("demo"), tasks);
		const batch = createLadoEventBatch(queryClient);
		expect(batch.note({ isTerminated }, { type: "session_updated", projectId: "demo", sessionId: "demo-7" })).toBe(false);
		const invalidated: QueryKey[] = [];
		batch.flush((queryKey) => invalidated.push(queryKey));
		return invalidated;
	}

	it("refreshes the project's tasks when the older run's session comes back", () => {
		expect(sessionUpdated([previousTask(true)], false)).toContainEqual(ladoTasksQueryKey("demo"));
	});

	it("leaves the tasks alone while the older run's session stays as the list shows it", () => {
		expect(sessionUpdated([previousTask(true)], true)).not.toContainEqual(ladoTasksQueryKey("demo"));
		expect(sessionUpdated([previousTask(false)], false)).not.toContainEqual(ladoTasksQueryKey("demo"));
	});
});

// LADO fork: relays of developer instructions (issue #49). A relay's row is a
// task event when LADO records it and when it is delivered or fails, so the
// task's open Relays tab reads again.
describe("createLadoEventBatch and relays", () => {
	it("refreshes the Relays tab of the task the event names", () => {
		expect(invalidatedBy([{ payload: { kind: "lado_task", projectId: "demo", taskId: "lado-task-1" }, lado: true }])).toContain("taskRelays");
		expect(invalidatedBy([{ payload: { kind: "lado_task", projectId: "demo", taskId: "lado-task-2" }, lado: true }])).toEqual(["otherTaskRelays"]);
	});

	it("leaves the Relays tab alone for an event without a task and for other kinds", () => {
		expect(invalidatedBy([{ payload: { kind: "lado_task", projectId: "demo" }, lado: true }])).toEqual([]);
		expect(invalidatedBy([{ payload: { kind: "lado_rules", projectId: "demo" }, lado: true }])).not.toContain("taskRelays");
		expect(
			invalidatedBy([{ payload: { kind: "lado_execution", projectId: "demo", sessionId: "demo-7", taskId: "lado-task-1" }, lado: true }]),
		).not.toContain("taskRelays");
	});
});

// LADO fork: the worker watch (issue #42). A session watch or alert event
// refreshes the board's costs and badges and the task's Usage tab; an alert's
// INSERT also hands the alert on for its desktop notification.
describe("createLadoEventBatch and the worker watch", () => {
	const watchViews: Record<string, QueryKey> = {
		projectUsage: ladoProjectUsageQueryKey("demo"),
		taskUsage: ladoTaskUsageQueryKey("lado-task-1"),
		unseenAlerts: ladoAlertsQueryKey("demo", { unseen: true }),
		taskAlerts: ladoAlertsQueryKey("demo", { taskId: "lado-task-1" }),
		otherAlerts: ladoAlertsQueryKey("other", { unseen: true }),
		tasks: ladoTasksQueryKey("demo"),
	};

	function watched(payloads: Payload[]) {
		const queryClient = new QueryClient();
		for (const key of Object.values(watchViews)) queryClient.setQueryData(key, []);
		const onNewAlerts = vi.fn();
		const batch = createLadoEventBatch(queryClient, { onNewAlerts });
		for (const payload of payloads) expect(batch.note(payload, { type: "session_updated", projectId: "demo", sessionId: "demo-3" })).toBe(true);
		batch.flush((queryKey) => void queryClient.invalidateQueries({ queryKey }));
		const invalidated = Object.entries(watchViews)
			.filter(([, key]) => queryClient.getQueryState(key)?.isInvalidated)
			.map(([name]) => name)
			.sort();
		return { invalidated, onNewAlerts, batch };
	}

	it("hands a raised alert on once and refreshes the usage and alert views", () => {
		const insert = { kind: "lado_alert", alertId: 7, op: "insert", projectId: "demo", taskId: "lado-task-1", sessionId: "demo-3" };
		const { invalidated, onNewAlerts, batch } = watched([insert, insert]);
		expect(invalidated).toEqual(["projectUsage", "taskAlerts", "taskUsage", "unseenAlerts"]);
		expect(onNewAlerts).toHaveBeenCalledTimes(1);
		expect(onNewAlerts).toHaveBeenCalledWith([{ alertId: 7, projectId: "demo", taskId: "lado-task-1" }]);
		// A second flush hands nothing on.
		batch.flush(() => undefined);
		expect(onNewAlerts).toHaveBeenCalledTimes(1);
	});

	it("refreshes the badges for a seen alert without a notification", () => {
		const { invalidated, onNewAlerts } = watched([
			{ kind: "lado_alert", alertId: 7, op: "update", projectId: "demo", taskId: "lado-task-1", sessionId: "" },
		]);
		expect(invalidated).toEqual(["projectUsage", "taskAlerts", "taskUsage", "unseenAlerts"]);
		expect(onNewAlerts).not.toHaveBeenCalled();
	});

	it("refreshes only the usage for a session watch change", () => {
		const { invalidated, onNewAlerts } = watched([{ kind: "lado_watch", projectId: "demo", taskId: "lado-task-1", sessionId: "demo-3" }]);
		expect(invalidated).toEqual(["projectUsage", "taskUsage"]);
		expect(onNewAlerts).not.toHaveBeenCalled();
	});
});

// LADO fork: a log watch event (issue #87) refreshes the project's log watch
// settings and its incidents, which the board's cards count.
describe("createLadoEventBatch and the log watch", () => {
	const logViews: Record<string, QueryKey> = {
		settings: ladoLogWatchQueryKey("demo"),
		incidents: ladoLogIncidentsQueryKey("demo"),
		otherIncidents: ladoLogIncidentsQueryKey("other"),
		tasks: ladoTasksQueryKey("demo"),
	};

	function invalidatedByLog(payload: Payload, event?: Envelope): string[] {
		const queryClient = new QueryClient();
		for (const key of Object.values(logViews)) queryClient.setQueryData(key, []);
		const batch = createLadoEventBatch(queryClient);
		expect(batch.note(payload, event)).toBe(true);
		batch.flush((queryKey) => void queryClient.invalidateQueries({ queryKey }));
		return Object.entries(logViews)
			.filter(([, key]) => queryClient.getQueryState(key)?.isInvalidated)
			.map(([name]) => name)
			.sort();
	}

	it("refreshes the project's incidents and settings for a fingerprint change", () => {
		expect(
			invalidatedByLog(
				{ kind: "lado_log_watch", projectId: "demo", fingerprintId: 4, op: "update" } as Payload,
				{ type: "session_updated", projectId: "demo" },
			),
		).toEqual(["incidents", "settings"]);
	});

	it("refreshes them for a settings change without a fingerprint", () => {
		expect(invalidatedByLog({ kind: "lado_log_watch", projectId: "demo", op: "settings" })).toEqual(["incidents", "settings"]);
	});

	it("leaves other projects alone and ignores an event without a project", () => {
		expect(invalidatedByLog({ kind: "lado_log_watch", projectId: "other", op: "insert" })).toEqual(["otherIncidents"]);
		const batch = createLadoEventBatch(new QueryClient());
		expect(batch.note({ kind: "lado_log_watch" })).toBe(false);
	});
});
