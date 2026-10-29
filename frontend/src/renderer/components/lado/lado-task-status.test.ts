import { describe, expect, it } from "vitest";
import { createFullAppI18n } from "../../i18n/all-messages";
import { APP_LOCALES } from "../../../shared/ui-locale";
import type { LadoTask, LadoTaskState, LadoTaskStateReason } from "../../lib/lado";
import { ladoTaskReason, ladoTaskStatus } from "./lado-task-status";

function task(state: LadoTaskState, stateReason?: LadoTaskStateReason): LadoTask {
	return {
		createdAt: "2026-10-27T00:00:00Z",
		id: "lado-task-1",
		issue: {
			number: 12,
			observedAt: "2026-10-27T00:00:00Z",
			ref: "github:acme/demo#12",
			state: "open",
			title: "Fix the flaky login test",
			url: "https://github.com/acme/demo/issues/12",
		},
		plan: { requestPending: false },
		projectId: "demo",
		repository: "github:acme/demo",
		state,
		...(stateReason ? { stateReason } : {}),
		execution: {
			planRevisionId: "lado-plan-1",
			sessionEnded: false,
			sessionId: "demo-7",
			startedAt: "2026-10-27T00:00:00Z",
			timedOut: false,
		},
	};
}

// LADO fork: the states a pull request's verification and integration give
// a task (docs/LJY-PRD/TDD.md §3.2).
describe("ladoTaskStatus", () => {
	const en = createFullAppI18n("en").t;
	const ko = createFullAppI18n("ko").t;

	it("names the verification states with their tones", () => {
		expect(ladoTaskStatus(task("validating", "checks_pending"), en)).toEqual({ label: "Validating", tone: "validating" });
		expect(ladoTaskStatus(task("integration_pending"), en)).toEqual({ label: "Integration pending", tone: "attention" });
		expect(ladoTaskStatus(task("integrating"), en)).toEqual({ label: "Integrating", tone: "ready" });
		expect(ladoTaskStatus(task("integrating", "status_not_posted"), en)).toEqual({ label: "Integrating", tone: "ready" });
		// Work merged without an approval needs the developer's attention.
		expect(ladoTaskStatus(task("completed", "merged_without_approval"), en)).toEqual({ label: "Completed", tone: "attention" });
		expect(ladoTaskStatus(task("completed"), en)).toEqual({ label: "Completed", tone: "merged" });
		expect(["validating", "integration_pending", "integrating"].map((state) => ladoTaskStatus(task(state as LadoTaskState), ko).label)).toEqual([
			"검증 중",
			"통합 대기",
			"통합 중",
		]);
	});

	it("says what verification waits for or found, an unposted status, and a merge without approval", () => {
		expect(ladoTaskReason(task("validating", "verifying"), en, "en")).toBe("LADO is verifying the pull request's head");
		expect(ladoTaskReason(task("validating", "checks_failed"), en, "en")).toBe("A required check failed");
		expect(ladoTaskReason(task("validating", "protected_path"), ko, "ko")).toBe("계획이 이름을 대지 않은 보호 경로를 PR이 바꿈");
		expect(ladoTaskReason(task("integrating", "status_not_posted"), en, "en")).toBe("LADO could not post the integration status yet");
		expect(ladoTaskReason(task("completed", "merged_without_approval"), ko, "ko")).toBe("통합 승인 없이 병합됨");
		// Nothing more to say.
		expect(ladoTaskReason(task("integration_pending"), en, "en")).toBeNull();
		expect(ladoTaskReason(task("integrating"), en, "en")).toBeNull();
		expect(ladoTaskReason(task("completed"), en, "en")).toBeNull();
	});

	it("names where a plan worker stands while a new plan is on its way", () => {
		const draft = { ...task("draft"), execution: undefined };
		const withWorker = (base: LadoTask, worker: NonNullable<LadoTask["plan"]["worker"]>, requestPending = true): LadoTask => ({
			...base,
			plan: { ...base.plan, requestPending, worker },
		});
		const waiting = withWorker(draft, { requestId: "r1", phase: "waiting", waitingReason: "plan_worker_capacity" });
		expect(ladoTaskStatus(waiting, en)).toEqual({ label: "Waiting for a plan worker", tone: "muted" });
		expect(ladoTaskReason(waiting, en, "en")).toBe("Every plan worker slot is in use");
		const designing = withWorker(draft, { requestId: "r1", phase: "designing", designerSessionId: "demo-plan-1" });
		// A plan worker writing is an agent at work.
		expect(ladoTaskStatus(designing, en)).toEqual({ label: "Plan worker writing", tone: "working" });
		expect(ladoTaskReason(designing, en, "en")).toBe("Designing the plan");
		expect(ladoTaskReason(withWorker(draft, { requestId: "r1", phase: "critiquing", review: "running" }), ko, "ko")).toBe("초안을 검수하는 중");
		// A change request on a failed run, and the fix of a pinned plan's problems.
		const failedRun = withWorker(task("failed", "ended_without_pr"), { requestId: "r2", phase: "revising" });
		expect(ladoTaskStatus(failedRun, en)).toEqual({ label: "Plan worker writing", tone: "working" });
		expect(ladoTaskReason(failedRun, en, "en")).toBe("Answering the review");
		const fixing = withWorker({ ...task("planned"), execution: undefined }, { requestId: "r3", phase: "fixing" }, false);
		expect(ladoTaskStatus(fixing, en)).toEqual({ label: "Plan worker writing", tone: "working" });
		expect(ladoTaskReason(fixing, en, "en")).toBe("Fixing the plan's problems");
	});

	it("says why a plan worker failed, and keeps a request made before plan workers as it was", () => {
		const draft = { ...task("draft"), execution: undefined };
		const failed: LadoTask = {
			...draft,
			plan: { requestPending: false, requestError: "no draft within 1h0m0s", worker: { requestId: "r1", phase: "failed", failureCode: "plan_worker_timed_out" } },
		};
		expect(ladoTaskStatus(failed, en)).toEqual({ label: "Plan request failed", tone: "attention" });
		expect(ladoTaskReason(failed, en, "en")).toBe("The plan worker ran out of time");
		// A done request says nothing more; the plan it pinned speaks.
		const done: LadoTask = { ...task("planned"), execution: undefined, plan: { requestPending: false, worker: { requestId: "r1", phase: "done" } } };
		expect(ladoTaskReason(done, en, "en")).toBeNull();
		// An orchestrator still writes a request made before plan workers.
		const legacy: LadoTask = { ...draft, plan: { requestPending: true } };
		expect(ladoTaskStatus(legacy, en)).toEqual({ label: "Plan requested", tone: "muted" });
		expect(ladoTaskReason(legacy, en, "en")).toBeNull();
	});

	it("names the new states in every language", () => {
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const state of ["validating", "integration_pending", "integrating"] as const) {
				const { label } = ladoTaskStatus(task(state), t);
				expect(label, `${locale} ${state}`).not.toMatch(/^lado\./);
				expect(label.length, `${locale} ${state}`).toBeGreaterThan(0);
			}
		}
	});
});

// LADO fork: a task split into parts (docs/LJY-PRD/TDD.md §5.4).
describe("parts and overlapping work", () => {
	const en = createFullAppI18n("en").t;
	const ko = createFullAppI18n("ko").t;

	it("says how many parts of a running task are merged", () => {
		const running = { ...task("running"), execution: undefined, parts: { total: 4, merged: 2 } };
		expect(ladoTaskStatus(running, en)).toEqual({ label: "Running", tone: "working" });
		expect(ladoTaskReason(running, en, "en")).toBe("Parts 2/4");
		expect(ladoTaskReason(running, ko, "ko")).toBe("파트 2/4");
		expect(ladoTaskReason({ ...task("running") }, en, "en")).toBeNull();
	});

	it("names the task a queued task waits for when their files overlap", () => {
		const queued: LadoTask = {
			...task("queued", "paths_overlap"),
			execution: undefined,
			queue: { state: "waiting", reason: "paths_overlap", waitingDetail: "github:acme/demo#7", attempts: 0, enqueuedAt: "2026-10-27T00:00:00Z" },
		};
		expect(ladoTaskReason(queued, en, "en")).toBe("Waiting for #7, which changes the same files");
		expect(ladoTaskReason({ ...queued, queue: { ...queued.queue!, waitingDetail: undefined } }, en, "en")).toBe("Waiting for a task that changes the same files");
		expect(ladoTaskReason({ ...task("queued", "slot_workers"), execution: undefined }, en, "en")).toBe("Waiting for a free worker slot");
		expect(ladoTaskReason({ ...task("queued", "disk_low"), execution: undefined }, ko, "ko")).toBe("디스크 공간을 기다리는 중");
	});
});
