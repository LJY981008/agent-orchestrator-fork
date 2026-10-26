import { describe, expect, it } from "vitest";
import { createFullAppI18n } from "../i18n/all-messages";
import { APP_LOCALES } from "../../shared/ui-locale";
import { LadoApiError } from "./lado";
import {
	ladoErrorText,
	ladoPolicyProblemText,
	ladoQueueErrorText,
	ladoReasonText,
	ladoSetupConflictText,
	ladoSetupKindLabel,
	ladoSetupReasonText,
	ladoSetupRestoreStatusText,
	ladoSetupWarningText,
} from "./lado-errors";

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
		// The snapshot holds the local setup too, so the rules or the setup changed.
		expect(ladoErrorText(ko, new LadoApiError(daemon, "LADO_RULES_CHANGED"))).toBe(
			"승인하는 동안 규칙이나 로컬 설정이 바뀌었습니다. 다시 확인한 뒤 승인하세요.",
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
		// Review is needed again when the rules or the local setup in the snapshot changed.
		expect(ladoReasonText(en, "rules_changed")).toBe("Rules or local setup changed since approval");
		expect(ladoReasonText(ko, "rules_changed")).toBe("승인 뒤 규칙이나 로컬 설정이 바뀜");
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

// Every code the local setup routes, the session launches and the desktop
// app's main process send (backend/internal/service/ladosetup, ladopolicy and
// httpd/controllers/lado_setup.go; src/main/lado-presence.ts).
const setupCodes = [
	"LADO_SETUP_UNAVAILABLE",
	"LADO_SETUP_NOT_FOUND",
	"LADO_SETUP_SCOPE_INVALID",
	"LADO_SETUP_ADOPTION_INVALID",
	"LADO_SETUP_NOT_ADOPTABLE",
	"LADO_SETUP_CHANGED",
	"LADO_SETUP_OS_UNVERIFIED",
	"LADO_SETUP_TOOL_RUNNING",
	"LADO_SETUP_ADOPTION_FAILED",
	"LADO_SETUP_HOOK_PATH_MOVED",
	"LADO_SETUP_ALREADY_RESTORED",
	"LADO_SETUP_RESTORE_FAILED",
	"LADO_SETUP_DRIFT_UNREADABLE",
	"LADO_SETUP_ITEM_CHANGED",
	"LADO_SETUP_CHANGE_INVALID",
	"LADO_SETUP_CONTENT_INVALID",
	"LADO_SETUP_LOGIN_KEY",
	"LADO_SETUP_ITEM_EXISTS",
	"LADO_SETUP_FOLDER_ITEM",
	"LADO_SETUP_CONFLICT_NOT_FOUND",
	"LADO_SETUP_CONFLICT_NOT_ACCEPTABLE",
	"LADO_SETUP_HOME_FAILED",
	"LADO_SETUP_CODEX_FAILED",
	"LADO_SETUP_LOAD_FAILED",
	"LADO_SETUP_ADOPTION_ID_INVALID",
	"INVALID_JSON",
];

// Why the inventory does not adopt an entry (backend/internal/domain/lado_setup.go)
// and why an adoption or a change refuses one (ladosetup adopt.go, changes.go).
const setupReasons = [
	"credentials",
	"login_key",
	"app_state",
	"plugins_stay",
	"tool_state",
	"no_tui_path",
	"deprecated",
	"inline_table",
	"literal_credentials",
	"name_unsupported",
	"unknown_location",
	"worktrees_do_not_load",
	"ao_sets_per_session",
	"repo_tracked",
	"managed_settings",
	"symlink",
	"unreadable",
	"not_found",
	"already_managed",
	"unknown",
	"missing",
	"not_taken",
	"invalid",
	"not_for_tool",
	"not_created",
	"not_text",
	"too_large",
	"not_json",
	"not_object",
	"front_matter",
	"server_shape",
];

// The conflict checks (backend/internal/service/ladosetup/conflicts.go).
const setupConflicts = [
	"LADO_SETUP_MCP_NAME_TAKEN",
	"LADO_SETUP_SKILL_NAME_TAKEN",
	"LADO_SETUP_ENV_RESERVED",
	"LADO_SETUP_DENIES_AO",
	"LADO_SETUP_HOOK_PATH_MOVED",
	"LADO_SETUP_DENIES_GIT",
	"LADO_SETUP_HOOK_MAY_BLOCK_TOOLS",
	"LADO_SETUP_HOOK_ADDS_PROMPT_TEXT",
	"LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING",
	"LADO_SETUP_HOOK_SHARES_AO_EVENT",
	"LADO_SETUP_PROJECT_OVERRIDES",
	"LADO_SETUP_PLUGIN_HOOKS_UNREADABLE",
];

const setupKinds = ["instructions", "setting", "skill", "agent", "command", "output_style", "hook_script", "mcp_server"];
const restoreStatuses = ["restored", "restored_beside", "key_kept", "failed"];
const setupWarnings = [
	"LADO_SETUP_DIRECT_USE_STOPS",
	"LADO_SETUP_BASELINE_FAILED",
	"LADO_SETUP_MANAGED_WRITE_FAILED",
	"LADO_SETUP_PREVIEW_INCOMPLETE",
];

// Every detail a setup message can name, so no placeholder stays unfilled.
const setupDetails = {
	tool: "claude-code",
	kind: "mcp_server",
	name: "search",
	reason: "literal_credentials",
	stage: "items",
	item: "item-1",
	key: "apiKeyHelper",
	path: "hooks/check.sh",
	field: "baseVersion",
	limit: 1048576,
	sessions: [{ id: "demo-7", tool: "claude-code" }],
	processes: [{ pid: 4242, name: "codex", tool: "codex" }],
	tools: ["claude-code", "codex"],
};

// LADO fork: local setup's errors, reasons, conflicts and warnings in the
// developer's language (step 7).
describe("LADO local setup errors", () => {
	const daemon = "The daemon's English text";
	const en = createFullAppI18n("en").t;
	const ko = createFullAppI18n("ko").t;

	it("translates every setup code in every language", () => {
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const code of setupCodes) {
				const text = ladoErrorText(t, new LadoApiError(daemon, code, setupDetails));
				expect(text, `${locale} ${code}`).not.toBe(daemon);
				expect(text, `${locale} ${code}`).not.toMatch(/^lado\./);
				expect(text, `${locale} ${code}`).not.toMatch(/{{|}}/);
			}
		}
		expect(ladoErrorText(en, new LadoApiError(daemon, "INVALID_JSON"))).toBe(en("lado.error.internal"));
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_ADOPTION_ID_INVALID"))).toBe(en("lado.error.setupNotFound"));
		// A code from a newer daemon shows the daemon's own words.
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_SOMETHING_NEW"))).toBe(daemon);
	});

	it("names the sessions and programs to close as one line", () => {
		const running = new LadoApiError(daemon, "LADO_SETUP_TOOL_RUNNING", {
			sessions: [
				{ id: "demo-7", tool: "claude-code" },
				{ id: "demo-9", tool: "codex" },
			],
			processes: [{ pid: 4242, name: "claude", tool: "claude-code" }],
		});
		expect(ladoErrorText(en, running)).toBe(
			"Close these Claude Code and Codex sessions and programs, then try again: demo-7 (Claude Code), demo-9 (Codex), claude (PID 4242)",
		);
		expect(ladoErrorText(ko, running)).toBe(
			"다음 Claude Code·Codex 세션과 프로그램을 닫은 뒤 다시 시도하세요: demo-7 (Claude Code), demo-9 (Codex), claude (PID 4242)",
		);
		// Entries that are not objects are left out.
		expect(
			ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_TOOL_RUNNING", { sessions: ["demo-7"], processes: [{ pid: 7, name: "codex" }] })),
		).toBe("Close these Claude Code and Codex sessions and programs, then try again: codex (PID 7)");
	});

	it("says why an entry or a change is refused, with the field and limit it names", () => {
		expect(
			ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_NOT_ADOPTABLE", { tool: "codex", kind: "mcp_server", name: "search", reason: "literal_credentials" })),
		).toBe("LADO cannot adopt search. It holds a secret in plain text, which would end up on a command line.");
		expect(ladoErrorText(ko, new LadoApiError(daemon, "LADO_SETUP_NOT_ADOPTABLE", { name: "review", reason: "already_managed" }))).toBe(
			"LADO가 review 항목을 채택할 수 없습니다. LADO가 이미 같은 이름의 항목을 관리합니다.",
		);
		expect(
			ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_CHANGE_INVALID", { op: "edit", field: "baseVersion", reason: "missing" })),
		).toBe("LADO cannot make this setup change. baseVersion is missing.");
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_CONTENT_INVALID", { reason: "too_large", limit: 1048576 }))).toBe(
			"This content does not fit the item. The content is larger than 1048576 bytes.",
		);
		// The desktop app refuses a change without details: no reason, no trailing space.
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_CHANGE_INVALID"))).toBe("LADO cannot make this setup change.");
		// A reason from a newer daemon shows as is.
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_CONTENT_INVALID", { reason: "something_new" }))).toBe(
			"This content does not fit the item. something_new",
		);
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_LOGIN_KEY", { key: "apiKeyHelper" }))).toBe(
			"Logins stay where Claude Code keeps them, so LADO does not manage apiKeyHelper.",
		);
		// Other codes' reasons stay the daemon's words.
		expect(ladoErrorText(en, new LadoApiError(daemon, "LADO_ISSUE_CREATE_FAILED", { reason: "missing" }))).toBe(
			"Could not create the issue: missing",
		);
	});

	it("says by its stage what a stopped adoption or restore did with the files", () => {
		const adoption = (details: Record<string, unknown>) => ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_ADOPTION_FAILED", details));
		expect(adoption({ stage: "copy", adoption: "adoption-1" })).toBe(
			"The adoption stopped before anything was removed. Your files are where they were.",
		);
		expect(adoption({ stage: "remove", adoption: "adoption-1", state: "partial", root: "claude_dir", path: "skills/review" })).toBe(
			"The adoption stopped while removing the originals. What was moved is managed now, and Restore puts it back.",
		);
		expect(adoption({ stage: "record", adoption: "adoption-1" })).toBe(
			"LADO could not record the adoption, so it put back what it had removed.",
		);
		const restore = (details: Record<string, unknown>) => ladoErrorText(en, new LadoApiError(daemon, "LADO_SETUP_RESTORE_FAILED", details));
		expect(restore({ stage: "manifest", adoption: "adoption-1" })).toBe(
			"LADO cannot read this adoption's backup as it was written, so it restored nothing.",
		);
		expect(restore({ stage: "put_back", path: "CLAUDE.md" })).toBe(
			"LADO could not put back CLAUDE.md. What it put back stays where it is; restore again to finish the rest.",
		);
		expect(restore({ stage: "record" })).toBe("LADO put back the originals but could not record the restore. Restore again to record it.");
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			const texts = [
				ladoErrorText(t, new LadoApiError(daemon, "LADO_SETUP_ADOPTION_FAILED", { stage: "copy" })),
				ladoErrorText(t, new LadoApiError(daemon, "LADO_SETUP_ADOPTION_FAILED", { stage: "remove", state: "partial" })),
				ladoErrorText(t, new LadoApiError(daemon, "LADO_SETUP_ADOPTION_FAILED", { stage: "record" })),
			];
			expect(new Set(texts).size, locale).toBe(3);
		}
	});

	it("words every reason, kind, restore status and warning in every language", () => {
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const reason of setupReasons) {
				const text = ladoSetupReasonText(t, reason, { field: "name", limit: 1048576 });
				expect(text, `${locale} ${reason}`).not.toBe(reason);
				expect(text, `${locale} ${reason}`).not.toMatch(/^lado\.|{{/);
			}
			for (const [values, label] of [
				[setupKinds, ladoSetupKindLabel],
				[restoreStatuses, ladoSetupRestoreStatusText],
				[setupWarnings, ladoSetupWarningText],
			] as const) {
				for (const value of values) {
					const text = label(t, value);
					expect(text, `${locale} ${value}`).not.toBe(value);
					expect(text, `${locale} ${value}`).not.toMatch(/^lado\./);
				}
			}
		}
		expect(ladoSetupReasonText(en, "worktrees_do_not_load")).toBe("AO worktrees do not load this file.");
		expect(ladoSetupReasonText(ko, "plugins_stay")).toBe(
			"플러그인은 Claude Code가 설치한 곳에 그대로 둡니다. 어떤 플러그인을 켤지는 enabledPlugins 설정을 채택해 관리하세요.",
		);
		expect(ladoSetupKindLabel(ko, "hook_script")).toBe("hook 스크립트");
		expect(ladoSetupRestoreStatusText(en, "restored_beside")).toBe("Put beside what is there now");
		expect(ladoSetupWarningText(ko, "LADO_SETUP_DIRECT_USE_STOPS")).toBe(
			"이 앱 밖에서 쓰는 Claude Code와 Codex는 옮긴 항목을 더 이상 불러오지 않습니다.",
		);
		// None reads empty; values from a newer daemon show as is.
		expect(ladoSetupReasonText(en, undefined)).toBe("");
		expect(ladoSetupReasonText(en, "something_new")).toBe("something_new");
		expect(ladoSetupKindLabel(en, "something_new")).toBe("something_new");
		expect(ladoSetupRestoreStatusText(en, "something_new")).toBe("something_new");
		expect(ladoSetupWarningText(en, "LADO_SETUP_SOMETHING_NEW")).toBe("LADO_SETUP_SOMETHING_NEW");
	});

	it("words every conflict with what it names, and the plugin whose hook it is", () => {
		const details = {
			key: "AO_SESSION",
			rule: "Bash(git push:*)",
			event: "PreToolUse",
			matcher: "Bash",
			path: "hooks/check.sh",
			plugin: "",
			mode: "bypassPermissions",
			ceiling: "auto",
			item: "item-1",
		};
		for (const locale of APP_LOCALES) {
			const t = createFullAppI18n(locale).t;
			for (const code of setupConflicts) {
				const text = ladoSetupConflictText(t, { code, details });
				expect(text, `${locale} ${code}`).not.toBe(code);
				expect(text, `${locale} ${code}`).not.toMatch(/^lado\.|{{/);
			}
		}
		expect(ladoSetupConflictText(en, { code: "LADO_SETUP_DEFAULT_MODE_ABOVE_CEILING", details })).toBe(
			"The default permission mode, Bypass permissions, is above the permission ceiling, Auto.",
		);
		expect(ladoSetupConflictText(en, { code: "LADO_SETUP_DENIES_GIT", details: { rule: "Bash(git push:*)", item: "item-1" } })).toBe(
			"The permission rule Bash(git push:*) denies git or gh, which workers need for branches and pull requests.",
		);
		expect(
			ladoSetupConflictText(en, { code: "LADO_SETUP_HOOK_SHARES_AO_EVENT", details: { event: "Stop", plugin: "acme@market", item: "item-1" } }),
		).toBe("A hook runs on Stop, where AO runs its own hook too. From the plugin acme@market.");
		expect(ladoSetupConflictText(en, { code: "LADO_SETUP_PLUGIN_HOOKS_UNREADABLE", details: { plugin: "acme@market" } })).toBe(
			"LADO could not read the hooks of the plugin acme@market, so it did not check them.",
		);
		expect(ladoSetupConflictText(ko, { code: "LADO_SETUP_ENV_RESERVED", details: { key: "AO_SESSION" } })).toBe(
			"env 설정이 AO가 세션마다 정하는 AO_SESSION 변수를 설정합니다.",
		);
		// A code from a newer daemon shows as is.
		expect(ladoSetupConflictText(en, { code: "LADO_SETUP_SOMETHING_NEW" })).toBe("LADO_SETUP_SOMETHING_NEW");
		// The rules that apply list setup conflicts among their problems.
		expect(ladoPolicyProblemText(en, { code: "LADO_SETUP_MCP_NAME_TAKEN", rules: [], details: { item: "item-1" } })).toBe(
			"An MCP server named ao takes the name of AO's own tool server.",
		);
	});
});
