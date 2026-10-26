import { describe, expect, it } from "vitest";
import { createFullAppI18n } from "../i18n/all-messages";
import { APP_LOCALES } from "../../shared/ui-locale";
import { LadoApiError } from "./lado";
import { ladoErrorText, ladoPolicyProblemText, ladoQueueErrorText, ladoReasonText } from "./lado-errors";

// Every code the rules routes and the approval send (backend/internal/service/ladopolicy,
// ladotask and httpd/controllers/lado_rules.go).
const ruleCodes = [
	"LADO_RULE_NOT_FOUND",
	"LADO_RULE_NAME_REQUIRED",
	"LADO_RULE_NAME_TOO_LONG",
	"LADO_RULE_CATEGORY_TOO_LONG",
	"LADO_RULE_INSTRUCTION_REQUIRED",
	"LADO_RULE_INSTRUCTION_TOO_LONG",
	"LADO_RULE_CONFLICT_UNKNOWN",
	"LADO_RULE_CONFLICT_SELF",
	"LADO_RULE_CONFLICT_PROTECTED",
	"LADO_RULE_CONFLICTS_TOO_MANY",
	"LADO_RULE_UPDATE_EMPTY",
	"LADO_RULE_CEILING_INVALID",
	"LADO_RULE_CHANGED",
	"LADO_RULE_ARCHIVED",
	"LADO_RULE_PROTECTED",
	"LADO_RULE_BUILTIN",
	"LADO_RULE_REFERENCED",
	"LADO_BINDING_SCOPE_INVALID",
	"LADO_BINDING_STATE_INVALID",
	"LADO_SCOPE_NOT_FOUND",
	"LADO_EFFECTIVE_SCOPE_REQUIRED",
	"LADO_EFFECTIVE_SCOPE_INVALID",
	"LADO_POLICY_SHA_REQUIRED",
	"LADO_RULES_UNAVAILABLE",
	"LADO_RULES_CHANGED",
	"LADO_RULES_LOAD_FAILED",
	"LADO_RULES_SAVE_FAILED",
	"INVALID_QUERY",
];

const problemCodes = [
	"LADO_AGENT_NOT_CHOSEN",
	"LADO_AGENT_CAPABILITY_UNSUPPORTED",
	"LADO_AGENT_CAPABILITY_UNVERIFIED",
	"LADO_AGENT_CAPABILITY_DEGRADED",
	"LADO_CODEX_ASKS_APPROVAL",
	"LADO_RULES_CONFLICT",
	"LADO_AO_RULES_FILE_UNREADABLE",
	"LADO_CEILING_MISSING",
	"LADO_PLAN_CEILING_CLAMPED",
];

// LADO fork: rule errors and rules problems in the developer's language.
describe("LADO rule errors", () => {
	it("translates rule error codes", () => {
		const en = createFullAppI18n("en").t;
		const ko = createFullAppI18n("ko").t;
		const daemon = "The daemon's English text";
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const code of ruleCodes) {
				const text = ladoErrorText(t, new LadoApiError(daemon, code));
				expect(text, `${locale} ${code}`).not.toBe(daemon);
				expect(text, `${locale} ${code}`).not.toMatch(/^lado\./);
			}
		}

		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_RULE_NAME_TOO_LONG", { max: 120 }))).toBe(
			"Keep the name within 120 characters.",
		);
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_RULE_CONFLICT_UNKNOWN", { rule: "team.gone" }))).toBe(
			"There is no rule team.gone.",
		);
		expect(ladoErrorText(ko, new LadoApiError(daemon, "LADO_RULES_CHANGED"))).toBe(
			"승인하는 동안 규칙이 바뀌었습니다. 규칙을 다시 확인한 뒤 승인하세요.",
		);
		// The presence errors now also answer a permission ceiling change.
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_PRESENCE_REQUIRED"))).toBe(
			"Do this in the AO desktop app. It asks you to confirm first.",
		);
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_RULES_SAVE_FAILED"))).toBe(
			en("lado.error.internal"),
		);
	});

	it("words every rules problem with its details", () => {
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const code of problemCodes) {
				const text = ladoPolicyProblemText(t, { code, rules: [], details: {} });
				expect(text, `${locale} ${code}`).not.toBe(code);
				expect(text, `${locale} ${code}`).not.toMatch(/^lado\./);
			}
		}

		const en = createFullAppI18n("en").t;
		expect(
			ladoPolicyProblemText(en, {
				code: "LADO_PLAN_CEILING_CLAMPED",
				rules: ["agent.permission.ceiling"],
				details: { plan: "bypass-permissions", ceiling: "auto" },
			}),
		).toBe("The plan asks for Bypass permissions; the permission ceiling lowers it to Auto.");
		expect(
			ladoPolicyProblemText(
				en,
				{ code: "LADO_AGENT_CAPABILITY_UNSUPPORTED", rules: ["team.review"], details: { agent: "cursor", capability: "prompt" } },
				{ agent: (harness) => (harness === "cursor" ? "Cursor" : harness) },
			),
		).toBe("Cursor cannot apply rule text, which these rules need: team.review.");
		expect(
			ladoPolicyProblemText(en, {
				code: "LADO_AO_RULES_FILE_UNREADABLE",
				details: { path: "docs/agents.md", reason: "file is larger than 4 MiB" },
			}),
		).toBe("LADO could not read the project's agent rules file docs/agents.md: file is larger than 4 MiB");
		// A code from a newer daemon shows as is.
		expect(ladoPolicyProblemText(en, { code: "LADO_SOMETHING_NEW" })).toBe("LADO_SOMETHING_NEW");
	});
});

// Every code the queue, run and dispatch settings routes, the gate's run
// checks and the mode switch send (backend/internal/service/ladotask,
// ladoproject, ladoqueue, ladogate), and the desktop app's own refusal.
const queueCodes = [
	"LADO_APPROVAL_ABOVE_CEILING",
	"LADO_EXECUTION_FIXED",
	"LADO_TASK_ALREADY_QUEUED",
	"LADO_TASK_RUNNING",
	"LADO_DISPATCH_SETTINGS_INVALID",
	"LADO_SETTINGS_EMPTY",
	"LADO_EXECUTION_NOT_FOUND",
	"LADO_TRACKER_INTAKE_ENABLED",
	"LADO_PROJECT_ID_INVALID",
	"LADO_QUEUE_LOAD_FAILED",
	"LADO_EXECUTION_LOAD_FAILED",
	"LADO_DISPATCH_SETTINGS_LOAD_FAILED",
	"LADO_DISPATCH_SETTINGS_UPDATE_FAILED",
	"LADO_SETTINGS_UPDATE_FAILED",
	"LADO_ISSUE_NOT_FOUND",
];

// Every reason a task's stateReason or a queue entry's reason carries
// (backend/internal/domain/lado_queue.go).
const reasons = [
	"rules_changed",
	"issue_edited",
	"plan_superseded",
	"approved_before_queue",
	"blocking_snapshot",
	"base_ref_mismatch",
	"agent_unsupported",
	"attempts_exhausted",
	"timed_out",
	"ended_without_pr",
	"slot_global",
	"slot_project",
	"memory_tight",
	"issue_unreadable",
	"retry",
	"lado_mode_off",
	"run_live",
];

// LADO fork: the queue's and the runs' errors and reasons in the developer's language.
describe("LADO queue errors and reasons", () => {
	it("translates the queue, run and dispatch settings codes", () => {
		const daemon = "The daemon's English text";
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const code of queueCodes) {
				const text = ladoErrorText(t, new LadoApiError(daemon, code, { min: 1, max: 8, ceiling: "auto" }));
				expect(text, `${locale} ${code}`).not.toBe(daemon);
				expect(text, `${locale} ${code}`).not.toMatch(/^lado\./);
				expect(text, `${locale} ${code}`).not.toMatch(/{{/);
			}
		}

		const en = createFullAppI18n("en").t;
		const ko = createFullAppI18n("ko").t;
		// The ceiling reads as AO's project settings name the mode.
		expect(
			ladoErrorText(en, new LadoApiError(daemon, "LADO_APPROVAL_ABOVE_CEILING", { sessionId: "demo-7", mode: "default", ceiling: "auto" })),
		).toBe("This worker runs an approved task: its permission mode cannot go above the permission ceiling, Auto.");
		expect(
			ladoErrorText(ko, new LadoApiError(daemon, "LADO_APPROVAL_ABOVE_CEILING", { sessionId: "demo-7", mode: "default", ceiling: "accept-edits" })),
		).toBe("승인한 작업을 실행하는 워커입니다. 권한 모드는 권한 상한을 넘을 수 없습니다(상한: 편집 수락).");
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_DISPATCH_SETTINGS_INVALID", { min: 1, max: 8, runMinutes: [0, 60] }))).toBe(
			"Choose 1 to 8 concurrent runs and a maximum run time from the list.",
		);
		expect(ladoErrorText(ko, new LadoApiError(daemon, "LADO_TASK_ALREADY_QUEUED", { taskId: "lado-task-1", state: "queued" }))).toBe(
			"이 계획은 승인되어 워커를 기다리고 있습니다. 새 리비전만 이 승인을 대신할 수 있습니다.",
		);
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_PROJECT_ID_INVALID"))).toBe(en("lado.error.projectNotFound"));
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_QUEUE_LOAD_FAILED"))).toBe(en("lado.error.internal"));
	});

	// LADO_EXECUTION_FIXED also refuses restoring a run LADO stopped for good
	// (backend/internal/service/ladogate AuthorizeRestore); its reason picks the words.
	it("says why a stopped run cannot be restored", () => {
		const daemon = "The daemon's English text";
		const restore = (reason?: string) => new LadoApiError(daemon, "LADO_EXECUTION_FIXED", { sessionId: "demo-7", ...(reason ? { reason } : {}) });
		const en = createFullAppI18n("en").t;
		expect(ladoErrorText(en, restore("timed_out"))).toBe("LADO stopped this run at its maximum run time, so its session cannot be restored.");
		expect(ladoErrorText(en, restore("task_cancelled"))).toBe("This run's task was cancelled, so its session cannot be restored.");
		// An agent or interface change carries no reason.
		expect(ladoErrorText(en, restore())).toBe(en("lado.error.executionFixed"));
		// A reason from a newer daemon still reads as the run's fixed setup.
		expect(ladoErrorText(en, restore("something_new"))).toBe(en("lado.error.executionFixed"));
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const reason of ["timed_out", "task_cancelled"]) {
				const text = ladoErrorText(t, restore(reason));
				expect(text, `${locale} ${reason}`).not.toBe(daemon);
				expect(text, `${locale} ${reason}`).not.toMatch(/^lado\./);
				expect(text, `${locale} ${reason}`).not.toBe(ladoErrorText(t, restore()));
			}
		}
	});

	// A failed entry's error is the daemon's cleaned-up text; LADO's own
	// "the issue is gone" reads in the developer's language.
	it("words a queue entry's error when LADO wrote it", () => {
		const gone = "LADO_ISSUE_NOT_FOUND: the issue no longer exists on the tracker";
		const en = createFullAppI18n("en").t;
		expect(ladoQueueErrorText(en, gone)).toBe("The issue no longer exists on the tracker.");
		expect(ladoQueueErrorText(createFullAppI18n("ko").t, gone)).toBe("트래커에 이 이슈가 더 이상 없습니다.");
		for (const locale of APP_LOCALES) {
			const text = ladoQueueErrorText(createFullAppI18n(locale).t, gone);
			expect(text, locale).not.toBe(gone);
			expect(text, locale).not.toMatch(/^lado\./);
		}
		// Other codes and plain sentences show as the daemon wrote them.
		for (const error of ["LADO_ISSUE_READ_FAILED: tracker said 502", "another session holds the attempt's client request id"]) {
			expect(ladoQueueErrorText(en, error)).toBe(error);
		}
	});

	it("words every reason in every language", () => {
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const reason of reasons) {
				const text = ladoReasonText(t, reason);
				expect(text, `${locale} ${reason}`).not.toBe(reason);
				expect(text, `${locale} ${reason}`).not.toMatch(/^lado\./);
				expect(text.length, `${locale} ${reason}`).toBeGreaterThan(0);
			}
		}

		const en = createFullAppI18n("en").t;
		const ko = createFullAppI18n("ko").t;
		// Also when the queue entry was cancelled or replaced, not only before step 6.
		expect(ladoReasonText(en, "approved_before_queue")).toBe("Not in the queue — approve again to queue it");
		expect(ladoReasonText(ko, "approved_before_queue")).toBe("대기열에 없음 — 다시 승인하면 대기열에 듭니다");
		expect(ladoReasonText(ko, "slot_project")).toBe("이 프로젝트의 빈 실행 자리를 기다리는 중");
		expect(ladoReasonText(en, "timed_out")).toBe("Stopped at the maximum run time");
		expect(ladoReasonText(ko, "run_live")).toBe("이 작업의 이전 실행이 아직 돌고 있음");
		// No reason reads empty; one from a newer daemon shows as is.
		expect(ladoReasonText(en, undefined)).toBe("");
		expect(ladoReasonText(en, "something_new")).toBe("something_new");
	});

	it("names the run states in every language", () => {
		const states = ["queued", "dispatching", "running", "failed", "completed"] as const;
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const state of states) {
				const key = `lado.task.state.${state}` as const;
				expect(t(key), `${locale} ${state}`).not.toBe(key);
			}
		}
		const ko = createFullAppI18n("ko").t;
		expect(states.map((state) => ko(`lado.task.state.${state}` as const))).toEqual(["대기 중", "시작하는 중", "실행 중", "실패", "완료"]);
	});
});
