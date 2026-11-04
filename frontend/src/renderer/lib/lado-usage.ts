// LADO fork: how the worker watch (issue #42) reads: estimated costs at API
// prices, token counts, and each alert's one line in the developer's
// language. The daemon keeps costs in nanodollars and thresholds in cents.
import type { TFunction } from "i18next";
import type { LadoAlert, LadoAlertKind } from "./lado";

const NANOS_PER_DOLLAR = 1_000_000_000;

function dollars(locale: string, amount: number, fractionDigits: number): string {
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		minimumFractionDigits: fractionDigits,
		maximumFractionDigits: fractionDigits,
	}).format(amount);
}

/**
 * An estimated cost in nanodollars as dollars and cents. A cost that rounds
 * to nothing but is not nothing reads as under a cent, so a session that
 * spent little does not look free.
 */
export function ladoCostText(nanos: number, locale: string): string {
	const amount = Math.max(0, nanos) / NANOS_PER_DOLLAR;
	if (amount > 0 && amount < 0.005) return `<${dollars(locale, 0.01, 2)}`;
	return dollars(locale, amount, 2);
}

/** A threshold in cents: whole dollars without cents. */
export function ladoCentsText(cents: number, locale: string): string {
	return dollars(locale, cents / 100, cents % 100 === 0 ? 0 : 2);
}

/** A token count in the locale's short form: 950, 12K, 1.2M. */
export function ladoTokensText(tokens: number, locale: string): string {
	return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(Math.max(0, tokens));
}

/** Whether the alert counts against a cost threshold, whose value and threshold are cents. */
function costKind(kind: LadoAlertKind): boolean {
	return kind === "task_cost" || kind === "session_cost";
}

/** The alert's one line in the developer's language, without the task. */
export function ladoAlertText(t: TFunction, alert: LadoAlert, locale: string): string {
	const doubled = alert.level >= 2;
	const values = costKind(alert.kind)
		? { cost: ladoCentsText(alert.value, locale), threshold: ladoCentsText(alert.threshold, locale) }
		: undefined;
	switch (alert.kind) {
		case "task_cost":
			return doubled ? t("lado.alert.taskCostDoubled", values) : t("lado.alert.taskCost", values);
		case "session_cost":
			return doubled ? t("lado.alert.sessionCostDoubled", values) : t("lado.alert.sessionCost", values);
		case "session_compactions":
			return t("lado.alert.sessionCompactions", { compactions: alert.value, threshold: alert.threshold });
		case "limited":
			return alert.value > 0
				? t("lado.alert.limitedUntil", {
						time: new Date(alert.value * 1000).toLocaleString(locale, { dateStyle: "short", timeStyle: "short" }),
					})
				: t("lado.alert.limited");
		case "limit_resumed":
			return t("lado.alert.limitResumed", { number: alert.value, max: alert.threshold });
		case "stall_nudged":
			return t("lado.alert.stallNudged", { minutes: alert.value });
		case "stalled":
			return t("lado.alert.stalled", { minutes: alert.value });
		case "agent_unavailable":
			return t("lado.alert.agentUnavailable");
		default:
			// A kind from a newer daemon: its own line.
			return alert.line;
	}
}

/** How many of the alerts nobody marked seen are each task's. */
export function ladoUnseenAlertCounts(alerts: readonly LadoAlert[] | undefined): Map<string, number> {
	const counts = new Map<string, number>();
	for (const alert of alerts ?? []) {
		if (alert.seenAt) continue;
		counts.set(alert.taskId, (counts.get(alert.taskId) ?? 0) + 1);
	}
	return counts;
}
