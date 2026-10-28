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
