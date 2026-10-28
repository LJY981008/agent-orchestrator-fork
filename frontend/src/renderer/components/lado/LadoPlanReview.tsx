// LADO fork: the review of a plan worker's revision in the task view
// (docs/LJY-PRD/TDD.md §5.1): one line for what making it took (the design's
// and the review's time and tokens, the findings and how the plan answered
// them), that LADO skipped the review or that it failed, and each finding of
// the critic with the answer the plan's "## 계획 검수" section gives it.
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { formatCostNanos } from "../../lib/format-cost";
import type { LadoPlan, LadoPlanFinding, LadoPlanMetrics } from "../../lib/lado";
import { cn } from "../../lib/utils";
import { formatCompactNumber } from "../settings/CodexAccountDetails";

const severityOrder: Record<LadoPlanFinding["severity"], number> = { P1: 0, major: 1, minor: 2 };

/**
 * The metrics line: "Design 21 min · 1.2M tokens / Review 15 min · 0.6M
 * tokens · P1 2 · major 1 · minor 0 → addressed 2 · not addressed 1", with
 * the cost at the end when AO priced every session. The design's time counts
 * the draft and the turn that answered the review.
 */
export function ladoPlanMetricsLine(metrics: LadoPlanMetrics, t: TFunction, locale: string): string {
	const duration = (ms: number) => t("lado.review.metrics.minutes", { minutes: Math.max(1, Math.round(ms / 60_000)) });
	const tokens = (role: LadoPlanMetrics["sessions"][number]["role"]) => {
		const total = metrics.sessions
			.filter((session) => session.role === role)
			.reduce((sum, session) => sum + session.inputTokens + session.outputTokens + session.cacheReadTokens + session.cacheWriteTokens, 0);
		return total > 0 ? t("lado.review.metrics.tokens", { tokens: formatCompactNumber(total, locale) }) : null;
	};
	const join = (...parts: (string | null)[]) => parts.filter(Boolean).join(" · ");
	const parts = [join(t("lado.review.metrics.design", { duration: duration(metrics.designMs + metrics.reviseMs) }), tokens("design"))];
	if (metrics.review === "done" || metrics.review === "failed") {
		const blocking = metrics.p1 + metrics.major;
		const answers = blocking > 0 ? ` → ${t("lado.review.metrics.answers", { addressed: metrics.addressed, notAddressed: metrics.notAddressed })}` : "";
		const findings =
			metrics.review === "done" ? t("lado.review.metrics.findings", { p1: metrics.p1, major: metrics.major, minor: metrics.minor }) + answers : null;
		parts.push(join(t("lado.review.metrics.critique", { duration: duration(metrics.critiqueMs) }), tokens("critique"), findings));
	}
	const priced = metrics.sessions.length > 0 && metrics.sessions.every((session) => session.costNanos != null);
	const cost = priced ? formatCostNanos(metrics.sessions.reduce((sum, session) => sum + (session.costNanos ?? 0), 0)) : null;
	return join(parts.join(" / "), cost);
}

/** How the plan answers a finding, in words; only a P1 or major one it did not take takes the caution color. */
function answerText(finding: LadoPlanFinding, t: TFunction): { text: string; caution: boolean } {
	switch (finding.answer) {
		case "addressed":
			return { text: t("lado.review.answer.addressed"), caution: false };
		case "not_addressed":
			return { text: t("lado.review.answer.not_addressed"), caution: finding.severity !== "minor" };
		default:
			return { text: t("lado.review.answer.none"), caution: false };
	}
}

export function LadoPlanReview({ plan }: { plan: LadoPlan }) {
	const { t, i18n } = useTranslation();
	const metrics = plan.revision.metrics;
	const critique = plan.critique;
	if (!metrics && !critique) return null;
	const review = critique?.state ?? metrics?.review;
	const findings = [...(critique?.findings ?? [])].sort((a, b) => severityOrder[a.severity] - severityOrder[b.severity]);
	return (
		<div className="flex flex-col gap-1.5" data-testid="lado-plan-review">
			<h4 className="text-xs font-semibold text-foreground">{t("lado.review.title")}</h4>
			{metrics ? (
				<p className="text-caption leading-4 text-settings-muted" data-testid="lado-plan-metrics">
					{ladoPlanMetricsLine(metrics, t, i18n.language)}
				</p>
			) : null}
			{review === "skipped" ? (
				<p className="text-xs text-muted-foreground">{t("lado.review.skipped")}</p>
			) : review === "failed" ? (
				// The plan went without a second look, which the developer weighs before approving.
				<p className="text-xs text-[var(--color-status-in-review)]" title={critique?.failureReason} data-testid="lado-plan-review-failed">
					{t("lado.review.failed")}
				</p>
			) : review === "done" && findings.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.review.noFindings")}</p>
			) : null}
			{findings.length > 0 ? (
				<ul className="flex flex-col gap-1 text-xs leading-5" data-testid="lado-plan-findings">
					{findings.map((finding) => {
						const answer = answerText(finding, t);
						return (
							<li key={finding.id} className="flex min-w-0 items-baseline gap-2">
								<span className="shrink-0 font-mono text-muted-foreground">{finding.id}</span>
								<span className="shrink-0 text-muted-foreground">{finding.severity}</span>
								<span className="min-w-0 flex-1 break-words text-foreground">{finding.title}</span>
								<span className={cn("shrink-0", answer.caution ? "text-[var(--color-status-in-review)]" : "text-muted-foreground")}>
									{answer.text}
								</span>
							</li>
						);
					})}
				</ul>
			) : null}
		</div>
	);
}
