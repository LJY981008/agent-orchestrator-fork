import { QueryClient, type QueryKey } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import {
	ladoEffectiveProjectQueryKey,
	ladoEffectiveQueryKey,
	ladoRuleQueryKey,
	ladoRulesQueryKey,
	ladoTasksQueryKey,
	ladoTaskViewQueryKey,
} from "./lado";
import { createLadoEventBatch } from "./lado-events";
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
