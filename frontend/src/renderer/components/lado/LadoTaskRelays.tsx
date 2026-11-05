// LADO fork: the task view's Relays tab (issue #49): the developer
// instructions LADO relayed from the project's Chat orchestrator to the task's
// running workers, newest first. Each relay shows when it was asked, the worker
// it went to (role and part), what the worker was told, the developer's own
// words it rests on, whether the instruction goes beyond those words or may
// leave the approved plan, and why a failed relay was not delivered. The
// evidence proves only that the developer wrote those words, not that the
// instruction says the same (docs/LJY-PRD/adr/0011-worker-relay.md); the
// beyond-evidence mark says when it does not. The task's events refresh the
// tab (lado-events.ts), so a relay shows as LADO records, delivers or fails it.
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useLadoTaskRelays } from "../../hooks/useLado";
import type { LadoRelay, LadoRelayState } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import { cn } from "../../lib/utils";
import { roleKeys } from "./LadoTaskTimings";

const stateKeys = {
	sending: "lado.relays.state.sending",
	delivered: "lado.relays.state.delivered",
	failed: "lado.relays.state.failed",
} as const satisfies Record<LadoRelayState, string>;

const stateDotClass = {
	sending: "bg-[var(--color-status-working)]",
	delivered: "bg-[var(--color-status-ready)]",
	failed: "bg-[var(--color-status-needs-you)]",
} as const satisfies Record<LadoRelayState, string>;

export function LadoTaskRelays({ taskId }: { taskId: string }) {
	const { t } = useTranslation();
	const relays = useLadoTaskRelays(taskId);
	if (relays.isPending) {
		return <p className="text-control text-settings-muted" role="status">{t("common.loading")}</p>;
	}
	if (relays.isError) {
		return <p className="text-caption leading-4 text-error" role="alert">{ladoErrorText(t, relays.error)}</p>;
	}
	return (
		<section className="flex flex-col gap-2" aria-label={t("lado.relays.title")} data-testid="lado-task-relays">
			<p className="text-caption leading-4 text-settings-muted">{t("lado.relays.note")}</p>
			{relays.data.length === 0 ? (
				<p className="text-xs text-muted-foreground">{t("lado.relays.none")}</p>
			) : (
				<ul className="flex flex-col divide-y divide-[var(--color-border-settings-dialog)] overflow-hidden rounded-md border border-[var(--color-border-settings-dialog)]">
					{relays.data.map((relay) => (
						<RelayRow key={relay.id} relay={relay} />
					))}
				</ul>
			)}
		</section>
	);
}

function RelayRow({ relay }: { relay: LadoRelay }) {
	const { t, i18n } = useTranslation();
	const time = (iso: string) => new Date(iso).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	const role = t(roleKeys[relay.workerRole ?? "worker"]);
	return (
		<li
			className="flex min-w-0 flex-col gap-2 px-3 py-2.5 text-xs leading-5"
			data-testid="lado-relay"
			data-relay-id={relay.id}
			data-state={relay.state}
		>
			<div className="flex min-w-0 items-baseline gap-2">
				<span className="shrink-0 font-medium text-foreground" data-testid="lado-relay-worker">
					{relay.partId ? t("lado.relays.workerPart", { role, part: relay.partId }) : role}
				</span>
				<span className="min-w-0 truncate font-mono text-2xs text-passive" title={relay.workerSessionId}>
					{relay.workerSessionId}
				</span>
				<span className="ml-auto flex shrink-0 items-center gap-1.5 text-2xs text-muted-foreground" data-testid="lado-relay-state">
					<span className={cn("size-1.5 rounded-full", stateDotClass[relay.state])} aria-hidden="true" />
					{t(stateKeys[relay.state])}
				</span>
				<span className="shrink-0 tabular-nums text-2xs text-muted-foreground">{time(relay.createdAt)}</span>
			</div>
			<Field label={t("lado.relays.instruction")}>
				<p className="whitespace-pre-wrap break-words text-foreground" data-testid="lado-relay-instruction">
					{relay.instruction}
				</p>
			</Field>
			<Field label={t("lado.relays.evidence", { time: time(relay.evidenceAt) })}>
				<blockquote
					className="whitespace-pre-wrap break-words border-l-2 border-[var(--color-border-settings-dialog)] pl-2 text-muted-foreground"
					data-testid="lado-relay-evidence"
				>
					{relay.evidence}
				</blockquote>
			</Field>
			{relay.beyondEvidence ? (
				<p className="text-caption leading-4 text-[var(--color-status-in-review)]" data-testid="lado-relay-beyond-evidence">
					{t("lado.relays.beyondEvidence")}
				</p>
			) : null}
			{relay.outsidePlan ? (
				<div className="flex flex-col gap-0.5 text-caption leading-4 text-[var(--color-status-in-review)]" data-testid="lado-relay-outside-plan">
					<p>{t("lado.relays.outsidePlan")}</p>
					{relay.outsidePlanReasons.length > 0 ? (
						<ul className="list-disc pl-4">
							{relay.outsidePlanReasons.map((reason) => (
								<li key={reason} className="break-words">{reason}</li>
							))}
						</ul>
					) : null}
				</div>
			) : null}
			{relay.state === "failed" ? (
				<p className="break-words text-caption leading-4 text-error" data-testid="lado-relay-failure">
					{relay.failure ? t("lado.relays.failure", { reason: relay.failure }) : t("lado.relays.failureUnknown")}
				</p>
			) : null}
			<p className="min-w-0 truncate text-2xs text-passive" title={relay.orchestratorSessionId}>
				{t("lado.relays.orchestrator", { session: relay.orchestratorSessionId })}
			</p>
		</li>
	);
}

function Field({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<span className="text-2xs font-medium text-muted-foreground">{label}</span>
			{children}
		</div>
	);
}
