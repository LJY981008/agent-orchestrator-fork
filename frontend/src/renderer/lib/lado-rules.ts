// LADO fork: how the app names and changes rules (docs/LJY-PRD/HARNESS.md
// §3-§8 and §14). A change runs as a dry run first; when it reaches tasks
// whose approval matches the rules now, the developer sees them before those
// approvals need review again.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useState } from "react";
import type { MessageKey } from "../i18n/messages";
import {
	changeLadoCeiling,
	createLadoRule,
	invalidateLadoRuleQueries,
	setLadoBinding,
	updateLadoRule,
	type LadoBindingInput,
	type LadoCeilingMode,
	type LadoCreateRuleInput,
	type LadoPolicyProblem,
	type LadoRule,
	type LadoRuleChangeResult,
	type LadoRuleImpact,
	type LadoRuleStrength,
	type LadoUpdateRuleInput,
} from "./lado";
import { ladoPolicyProblemText } from "./lado-errors";

export const LADO_RULE_PLAN_APPROVAL = "plan.approval.required";
export const LADO_RULE_PERMISSION_CEILING = "agent.permission.ceiling";
export const LADO_RULE_KOREAN_COMMITS = "git.commit.message.korean";

type BuiltinKeys = { seedName: string; name: MessageKey; description: MessageKey };

// LADO's built-in rules (backend/internal/service/ladopolicy/builtins.go).
// A built-in that is not protected can be renamed; then its own name shows.
const builtinRules: Partial<Record<string, BuiltinKeys>> = {
	[LADO_RULE_PLAN_APPROVAL]: {
		seedName: "Plan approval required",
		name: "lado.rules.builtin.planApproval.name",
		description: "lado.rules.builtin.planApproval.description",
	},
	[LADO_RULE_PERMISSION_CEILING]: {
		seedName: "Permission ceiling",
		name: "lado.rules.builtin.permissionCeiling.name",
		description: "lado.rules.builtin.permissionCeiling.description",
	},
	[LADO_RULE_KOREAN_COMMITS]: {
		seedName: "Commit messages in Korean",
		name: "lado.rules.builtin.koreanCommits.name",
		description: "lado.rules.builtin.koreanCommits.description",
	},
};

const strengthKeys: Record<LadoRuleStrength, MessageKey> = {
	prevents: "lado.rules.strength.prevents",
	detects: "lado.rules.strength.detects",
	advisory: "lado.rules.strength.advisory",
};

/** A rule's name in the developer's language; built-in names are translated. */
export function ladoRuleName(t: TFunction, ruleId: string, name: string): string {
	const keys = builtinRules[ruleId];
	return keys && name === keys.seedName ? t(keys.name) : name;
}

/** What a built-in rule does; undefined for the developer's own rules. */
export function ladoRuleDescription(t: TFunction, ruleId: string, name: string): string | undefined {
	const keys = builtinRules[ruleId];
	return keys && name === keys.seedName ? t(keys.description) : undefined;
}

/** How strongly LADO applies a rule: it prevents, detects, or only advises. */
export function ladoStrengthLabel(t: TFunction, strength: LadoRuleStrength): string {
	return t(strengthKeys[strength]);
}

/** A rules problem, naming the rules as the app shows them. */
export function ladoProblemText(
	t: TFunction,
	problem: LadoPolicyProblem,
	rules: readonly { ruleId: string; name: string }[] = [],
): string {
	const names = new Map(rules.map((rule) => [rule.ruleId, ladoRuleName(t, rule.ruleId, rule.name)]));
	return ladoPolicyProblemText(t, problem, { rule: (ruleId) => names.get(ruleId) ?? ruleId });
}

/**
 * A rule change. Each runs first as a dry run, which needs no confirmation;
 * the permission ceiling's mode is then changed through the desktop app's
 * confirmation (changeLadoCeiling).
 */
export type LadoRuleChangeRequest =
	| { kind: "create"; input: Omit<LadoCreateRuleInput, "dryRun"> }
	| { kind: "update"; ruleId: string; input: Omit<LadoUpdateRuleInput, "dryRun" | "ceiling"> }
	| { kind: "binding"; input: Omit<LadoBindingInput, "dryRun"> }
	| { kind: "ceiling"; ruleId: string; baseVersion: number; mode: LadoCeilingMode };

export type LadoRuleChangeOutcome =
	| { status: "applied"; rule?: LadoRule }
	/** The change reaches approved tasks and waits for confirm or cancel; rule is how it would be. */
	| { status: "confirm"; impact: LadoRuleImpact; rule?: LadoRule }
	/** The developer cancelled the desktop confirmation; nothing changed. */
	| { status: "cancelled" };

/** A change waiting for the developer, with what it reaches. */
export type LadoPendingRuleChange = { request: LadoRuleChangeRequest; impact: LadoRuleImpact; rule?: LadoRule };

function dryRun(request: LadoRuleChangeRequest): Promise<LadoRuleChangeResult> {
	switch (request.kind) {
		case "create":
			return createLadoRule({ ...request.input, dryRun: true });
		case "update":
			return updateLadoRule(request.ruleId, { ...request.input, dryRun: true });
		case "binding":
			return setLadoBinding({ ...request.input, dryRun: true });
		case "ceiling":
			return updateLadoRule(request.ruleId, {
				baseVersion: request.baseVersion,
				ceiling: { mode: request.mode },
				dryRun: true,
			});
	}
}

async function apply(request: LadoRuleChangeRequest): Promise<LadoRuleChangeOutcome> {
	switch (request.kind) {
		case "create":
			return { status: "applied", rule: (await createLadoRule(request.input)).rule };
		case "update":
			return { status: "applied", rule: (await updateLadoRule(request.ruleId, request.input)).rule };
		case "binding":
			return { status: "applied", rule: (await setLadoBinding(request.input)).rule };
		case "ceiling": {
			const rule = await changeLadoCeiling(request.ruleId, request.mode);
			return rule ? { status: "applied", rule } : { status: "cancelled" };
		}
	}
}

/**
 * Runs rule changes. change() asks the daemon what the change reaches; with
 * no approved task in it the change applies at once, otherwise it waits in
 * pending until confirm() or cancel(). A failed call rejects with the
 * daemon's LadoApiError, which error also holds.
 */
export function useLadoRuleChange() {
	const queryClient = useQueryClient();
	const [pending, setPending] = useState<LadoPendingRuleChange | null>(null);
	const mutation = useMutation({
		mutationFn: async (input: { request: LadoRuleChangeRequest; confirmed: boolean }): Promise<LadoRuleChangeOutcome> => {
			if (!input.confirmed) {
				const preview = await dryRun(input.request);
				if (preview.impact.tasks.length > 0) return { status: "confirm", impact: preview.impact, rule: preview.rule };
			}
			return apply(input.request);
		},
		onSuccess: (outcome, input) => {
			if (outcome.status === "confirm") {
				setPending({ request: input.request, impact: outcome.impact, rule: outcome.rule });
				return;
			}
			setPending(null);
			if (outcome.status === "applied") invalidateLadoRuleQueries(queryClient);
		},
		retry: false,
	});
	return {
		change: (request: LadoRuleChangeRequest) => mutation.mutateAsync({ request, confirmed: false }),
		pending,
		confirm: (): Promise<LadoRuleChangeOutcome> =>
			pending ? mutation.mutateAsync({ request: pending.request, confirmed: true }) : Promise.resolve({ status: "cancelled" }),
		cancel: () => {
			setPending(null);
			mutation.reset();
		},
		isPending: mutation.isPending,
		error: mutation.error,
		reset: mutation.reset,
	};
}
