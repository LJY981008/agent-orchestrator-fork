import { describe, expect, it } from "vitest";
import { createFullAppI18n } from "../i18n/all-messages";
import { APP_LOCALES } from "../../shared/ui-locale";
import { LadoApiError } from "./lado";
import { ladoErrorText, ladoPolicyProblemText } from "./lado-errors";

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
		).toBe("Cursor cannot apply rule text, which these rules need.");
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
