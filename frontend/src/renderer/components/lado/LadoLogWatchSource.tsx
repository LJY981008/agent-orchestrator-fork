// LADO fork: one Docker host in the log watch card (issue #87): its display
// name, its Engine API endpoint, TLS and pause, the containers LADO follows on
// it, and, once saved, how the daemon follows it, its TLS copies and a check.
// A host without TLS shows that its API is unauthenticated and its logs cross
// the network in plain text. In a project with two repositories or more,
// each target picks the repository its issues go to, the project's default
// unless it names one, and says when it needs one the project does not set.
// The edits stay in the card's draft until Save.
import { Plus, ShieldAlert, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCheckLadoLogSource, useDeleteLadoLogTLS, useImportLadoLogTLS } from "../../hooks/useLado";
import { formatTimeCompact } from "../../lib/format-time";
import type { LadoRepository } from "../../lib/lado";
import { ladoErrorText } from "../../lib/lado-errors";
import {
	LADO_LOG_MAX_TARGETS,
	ladoLogNewTarget,
	ladoLogTargetKinds,
	ladoLogTargetNeedsRepository,
	type LadoLogSource,
	type LadoLogSourceDraft,
	type LadoLogSourceState,
	type LadoLogTargetDraft,
	type LadoLogTargetKind,
	type LadoLogWatchDraft,
} from "../../lib/lado-log-watch";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";

const inputClass = "settings-field-control h-(--size-settings-action-height) rounded-md!";

const stateKeys = {
	off: "lado.logWatch.stateOff",
	connecting: "lado.logWatch.stateConnecting",
	watching: "lado.logWatch.stateWatching",
	backoff: "lado.logWatch.stateBackoff",
	error: "lado.logWatch.stateError",
} as const satisfies Record<LadoLogSourceState, string>;

const stateDotClass: Record<LadoLogSourceState, string> = {
	off: "bg-[var(--color-status-idle)]",
	connecting: "bg-[var(--color-status-working)]",
	watching: "bg-[var(--color-status-ready)]",
	backoff: "bg-[var(--color-status-needs-you)]",
	error: "bg-[var(--color-status-exited)]",
};

const kindKeys = {
	compose_project: "lado.logWatch.kindComposeProject",
	compose_service: "lado.logWatch.kindComposeService",
	name: "lado.logWatch.kindName",
	label: "lado.logWatch.kindLabel",
} as const satisfies Record<LadoLogTargetKind, string>;

const valuePlaceholderKeys = {
	compose_project: "lado.logWatch.valueComposeProjectPlaceholder",
	compose_service: "lado.logWatch.valueComposeServicePlaceholder",
	name: "lado.logWatch.valueNamePlaceholder",
	label: "lado.logWatch.valueLabelPlaceholder",
} as const satisfies Record<LadoLogTargetKind, string>;

// The select's value for "the project's default", since an item cannot be "".
const projectDefaultValue = "__project__";

export function LadoLogWatchSource({
	projectId,
	index,
	source,
	saved,
	watch,
	repositories,
	busy,
	onChange,
	onRemove,
}: {
	projectId: string;
	index: number;
	source: LadoLogSourceDraft;
	/** The saved source with the same id: its state, its TLS copies; none before it is saved. */
	saved?: LadoLogSource;
	/** The draft's switch and default repository, which decide whether a target needs a repository. */
	watch: Pick<LadoLogWatchDraft, "enabled" | "repository">;
	/** The project's repositories; a target picks one only when there are two or more. */
	repositories: readonly LadoRepository[];
	busy: boolean;
	onChange: (next: LadoLogSourceDraft) => void;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	const number = index + 1;
	const name = source.displayName.trim() || t("lado.logWatch.sourceUnnamed", { number });
	const patch = (fields: Partial<LadoLogSourceDraft>) => onChange({ ...source, ...fields });
	const patchTarget = (key: string, fields: Partial<LadoLogTargetDraft>) =>
		patch({ targets: source.targets.map((target) => (target.key === key ? { ...target, ...fields } : target)) });
	const addTarget = () => patch({ targets: [...source.targets, ladoLogNewTarget()] });
	const status = saved?.status;
	const pickRepository = repositories.length > 1;
	const repositoryName = (ref: string) => repositories.find((repository) => repository.ref === ref)?.name ?? ref;
	const projectDefault = watch.repository
		? t("lado.logWatch.targetRepositoryDefault", { name: repositoryName(watch.repository) })
		: t("lado.logWatch.targetRepositoryDefaultUnset");

	return (
		<div className="settings-content-row flex flex-col gap-2.5" data-testid="lado-log-watch-source">
			<div className="flex min-w-0 items-center gap-2">
				<input
					aria-label={t("lado.logWatch.sourceName", { number })}
					autoComplete="off"
					spellCheck={false}
					disabled={busy}
					placeholder={t("lado.logWatch.sourceNamePlaceholder")}
					value={source.displayName}
					onChange={(event) => patch({ displayName: event.target.value })}
					className={cn(inputClass, "w-36 shrink-0")}
				/>
				{/* Inputs take their font from here (the app's form reset). */}
				<div className="min-w-0 flex-1 font-mono">
					<input
						aria-label={t("lado.logWatch.endpoint", { number })}
						autoComplete="off"
						spellCheck={false}
						disabled={busy}
						placeholder={t("lado.logWatch.endpointPlaceholder")}
						value={source.endpoint}
						onChange={(event) => patch({ endpoint: event.target.value })}
						className={inputClass}
					/>
				</div>
				<Button
					type="button"
					variant="ghost"
					size="icon-sm"
					aria-label={t("lado.logWatch.removeSource", { name })}
					title={t("lado.logWatch.removeSource", { name })}
					disabled={busy}
					onClick={onRemove}
					className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive"
				>
					<Trash2 className="size-3.5" aria-hidden="true" />
				</Button>
			</div>
			<div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-control text-settings-label">
				<label className="inline-flex items-center gap-2">
					<Switch
						aria-label={t("lado.logWatch.tlsFor", { name })}
						checked={source.tls}
						disabled={busy}
						onCheckedChange={(tls) => patch({ tls })}
					/>
					<span>{t("lado.logWatch.tls")}</span>
				</label>
				<label className="inline-flex items-center gap-2">
					<Switch
						aria-label={t("lado.logWatch.pauseSource", { name })}
						checked={source.paused}
						disabled={busy}
						onCheckedChange={(paused) => patch({ paused })}
					/>
					<span>{t("lado.logWatch.paused")}</span>
				</label>
				{status ? (
					<span className="inline-flex min-w-0 items-center gap-1.5 text-caption text-settings-muted" data-testid="lado-log-watch-source-state">
						<span className={cn("size-1.5 shrink-0 rounded-full", stateDotClass[status.state])} aria-hidden="true" />
						<span>{t(stateKeys[status.state])}</span>
						{status.downSince ? <span>{t("lado.logWatch.downSince", { time: formatTimeCompact(status.downSince) })}</span> : null}
						{status.detail ? (
							<span className="min-w-0 truncate" title={status.detail}>
								· {status.detail}
							</span>
						) : null}
					</span>
				) : (
					<span className="text-caption text-settings-muted">{t("lado.logWatch.unsavedSource")}</span>
				)}
			</div>
			{!source.tls ? (
				<p className="flex items-start gap-1.5 text-caption leading-4 text-[var(--color-status-needs-you)]" data-testid="lado-log-watch-insecure">
					<ShieldAlert className="mt-px size-3.5 shrink-0" aria-hidden="true" />
					<span>{t("lado.logWatch.insecure")}</span>
				</p>
			) : saved?.id ? (
				<SourceTLS projectId={projectId} source={saved} busy={busy} />
			) : (
				<p className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.tlsSaveFirst")}</p>
			)}
			<div className="flex flex-col gap-1.5" data-testid="lado-log-watch-targets">
				<span className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.targets")}</span>
				{source.targets.length === 0 ? (
					<p className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.targetsEmpty")}</p>
				) : null}
				{source.targets.map((target, targetIndex) => {
					const targetStatus = saved?.targets.find((candidate) => candidate.id === target.id)?.status;
					const targetNumber = targetIndex + 1;
					return (
						<div key={target.key} className="flex flex-col gap-1" data-testid="lado-log-watch-target">
							<div className="flex min-w-0 items-center gap-2">
								<Select
									value={target.kind}
									disabled={busy}
									onValueChange={(kind) => patchTarget(target.key, { kind: kind as LadoLogTargetKind })}
								>
									<SelectTrigger size="sm" className="w-36 shrink-0 px-2.5 text-xs" aria-label={t("lado.logWatch.targetKind", { number: targetNumber })}>
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{ladoLogTargetKinds.map((kind) => (
											<SelectItem key={kind} value={kind}>
												{t(kindKeys[kind])}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
								<div className="min-w-0 flex-1 font-mono">
									<input
										aria-label={t("lado.logWatch.targetValue", { number: targetNumber })}
										autoComplete="off"
										spellCheck={false}
										disabled={busy}
										placeholder={t(valuePlaceholderKeys[target.kind])}
										value={target.value}
										onChange={(event) => patchTarget(target.key, { value: event.target.value })}
										className={inputClass}
									/>
								</div>
								{target.kind === "compose_service" ? (
									<div className="w-28 shrink-0 font-mono">
										<input
											aria-label={t("lado.logWatch.targetComposeProject", { number: targetNumber })}
											autoComplete="off"
											spellCheck={false}
											disabled={busy}
											placeholder={t("lado.logWatch.targetComposeProjectPlaceholder")}
											value={target.composeProject}
											onChange={(event) => patchTarget(target.key, { composeProject: event.target.value })}
											className={inputClass}
										/>
									</div>
								) : null}
								<div className="w-28 shrink-0 font-mono">
									<input
										aria-label={t("lado.logWatch.targetService", { number: targetNumber })}
										title={t("lado.logWatch.targetServiceHint")}
										autoComplete="off"
										spellCheck={false}
										disabled={busy}
										placeholder={t("lado.logWatch.targetServicePlaceholder")}
										value={target.service}
										onChange={(event) => patchTarget(target.key, { service: event.target.value })}
										className={inputClass}
									/>
								</div>
								<Switch
									aria-label={t("lado.logWatch.pauseTarget", { number: targetNumber })}
									title={t("lado.logWatch.paused")}
									checked={target.paused}
									disabled={busy}
									onCheckedChange={(paused) => patchTarget(target.key, { paused })}
								/>
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									aria-label={t("lado.logWatch.removeTarget", { number: targetNumber })}
									title={t("lado.logWatch.removeTarget", { number: targetNumber })}
									disabled={busy}
									onClick={() => patch({ targets: source.targets.filter((candidate) => candidate.key !== target.key) })}
									className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive"
								>
									<Trash2 className="size-3.5" aria-hidden="true" />
								</Button>
							</div>
							{pickRepository ? (
								<div className="flex min-w-0 items-center gap-2 pl-1">
									<span className="shrink-0 text-caption leading-4 text-settings-muted">{t("lado.logWatch.repository")}</span>
									<Select
										value={target.repository || projectDefaultValue}
										disabled={busy}
										onValueChange={(value) => patchTarget(target.key, { repository: value === projectDefaultValue ? "" : value })}
									>
										<SelectTrigger
											size="sm"
											className="w-56 max-w-full min-w-0 px-2.5 text-xs"
											aria-label={t("lado.logWatch.targetRepository", { number: targetNumber })}
										>
											<SelectValue />
										</SelectTrigger>
										<SelectContent>
											<SelectItem value={projectDefaultValue}>{projectDefault}</SelectItem>
											{/* A saved value the project no longer lists still shows as itself. */}
											{target.repository && !repositories.some((repository) => repository.ref === target.repository) ? (
												<SelectItem value={target.repository}>{target.repository}</SelectItem>
											) : null}
											{repositories.map((repository) => (
												<SelectItem key={repository.ref} value={repository.ref}>
													{repository.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</div>
							) : null}
							{ladoLogTargetNeedsRepository(watch, source, target, repositories.length) ? (
								<p
									className="pl-1 text-caption leading-4 text-[var(--color-status-needs-you)]"
									data-testid="lado-log-watch-target-repository-required"
								>
									{t("lado.logWatch.targetRepositoryRequired")}
								</p>
							) : null}
							{targetStatus ? (
								<p className="pl-1 text-caption leading-4 text-settings-muted" data-testid="lado-log-watch-target-state">
									{t("lado.logWatch.targetInstances", { count: targetStatus.instances })}
									{targetStatus.lastLineAt ? ` · ${t("lado.logWatch.lastLine", { time: formatTimeCompact(targetStatus.lastLineAt) })}` : ""}
								</p>
							) : null}
						</div>
					);
				})}
				<div className="flex items-center justify-between gap-2">
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="-ml-2.5 text-settings-muted hover:text-settings-label"
						disabled={busy || source.targets.length >= LADO_LOG_MAX_TARGETS}
						onClick={addTarget}
					>
						<Plus aria-hidden="true" />
						<span className="text-control">{t("lado.logWatch.addTarget")}</span>
					</Button>
					{saved?.id ? <SourceCheck projectId={projectId} sourceId={saved.id} /> : null}
				</div>
			</div>
		</div>
	);
}

/** A saved TLS source's copies under ~/.ao: which are stored, and the paths to copy them from. */
function SourceTLS({ projectId, source, busy }: { projectId: string; source: LadoLogSource; busy: boolean }) {
	const { t } = useTranslation();
	const importTLS = useImportLadoLogTLS(projectId);
	const deleteTLS = useDeleteLadoLogTLS(projectId);
	const [paths, setPaths] = useState({ caPath: "", certPath: "", keyPath: "" });
	const stored = source.tlsStored;
	const working = busy || importTLS.isPending || deleteTLS.isPending;
	const caPath = paths.caPath.trim();
	const certPath = paths.certPath.trim();
	const keyPath = paths.keyPath.trim();
	// The certificate and the key go together or not at all.
	const ready = caPath !== "" && (certPath === "") === (keyPath === "");
	const error = importTLS.error ?? deleteTLS.error;
	const storedText = [stored.ca ? t("lado.logWatch.tlsCA") : "", stored.cert ? t("lado.logWatch.tlsCert") : "", stored.key ? t("lado.logWatch.tlsKey") : ""]
		.filter(Boolean)
		.join(" · ");
	const field = (key: keyof typeof paths, label: string, placeholder: string) => (
		<div className="min-w-0 flex-1 font-mono">
			<input
				aria-label={label}
				autoComplete="off"
				spellCheck={false}
				disabled={working}
				placeholder={placeholder}
				value={paths[key]}
				onChange={(event) => {
					importTLS.reset();
					setPaths({ ...paths, [key]: event.target.value });
				}}
				className={inputClass}
			/>
		</div>
	);
	return (
		<div className="flex flex-col gap-1.5" data-testid="lado-log-watch-tls">
			<p className={cn("text-caption leading-4", stored.ca ? "text-settings-muted" : "text-[var(--color-status-needs-you)]")}>
				{stored.ca ? t("lado.logWatch.tlsStored", { files: storedText }) : t("lado.logWatch.tlsNeedsCA")}
			</p>
			<div className="flex min-w-0 items-center gap-2">
				{field("caPath", t("lado.logWatch.tlsCAPath"), t("lado.logWatch.tlsCAPlaceholder"))}
				{field("certPath", t("lado.logWatch.tlsCertPath"), t("lado.logWatch.tlsCertPlaceholder"))}
				{field("keyPath", t("lado.logWatch.tlsKeyPath"), t("lado.logWatch.tlsKeyPlaceholder"))}
			</div>
			<div className="flex items-center gap-2">
				<Button
					type="button"
					variant="outline"
					size="sm"
					disabled={working || !ready}
					onClick={() =>
						importTLS.mutate(
							{ sourceId: source.id, input: { caPath, ...(certPath ? { certPath, keyPath } : {}) } },
							{ onSuccess: () => setPaths({ caPath: "", certPath: "", keyPath: "" }) },
						)
					}
				>
					<span className="text-control">{importTLS.isPending ? t("lado.logWatch.tlsImporting") : t("lado.logWatch.tlsImport")}</span>
				</Button>
				{stored.ca || stored.cert || stored.key ? (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						className="text-settings-muted hover:text-destructive"
						disabled={working}
						onClick={() => deleteTLS.mutate(source.id)}
					>
						<span className="text-control">{t("lado.logWatch.tlsDelete")}</span>
					</Button>
				) : null}
			</div>
			{error ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, error)}
				</p>
			) : null}
		</div>
	);
}

/** Checks the saved source: the Engine's versions and the containers each target matches. */
function SourceCheck({ projectId, sourceId }: { projectId: string; sourceId: string }) {
	const { t } = useTranslation();
	const check = useCheckLadoLogSource(projectId);
	const result = check.data;
	return (
		<div className="flex min-w-0 flex-col items-end gap-1">
			<Button type="button" variant="outline" size="sm" disabled={check.isPending} onClick={() => check.mutate(sourceId)}>
				<span className="text-control">{check.isPending ? t("lado.logWatch.checking") : t("lado.logWatch.check")}</span>
			</Button>
			{check.isError ? (
				<p className="text-caption leading-4 text-error" role="alert">
					{ladoErrorText(t, check.error)}
				</p>
			) : result ? (
				<div className="flex min-w-0 flex-col items-end gap-0.5 text-caption leading-4" data-testid="lado-log-watch-check">
					{result.ok ? (
						<span className="text-settings-label">
							{t("lado.logWatch.checkOk", { engine: result.engineVersion ?? "", api: result.apiVersion ?? "", count: result.instances.length })}
						</span>
					) : (
						<span className="text-error" role="alert">
							{t("lado.logWatch.checkFailed", { detail: result.detail ?? "" })}
						</span>
					)}
					{result.instances.map((instance) => (
						<span key={instance.id} className="max-w-full truncate font-mono text-settings-muted">
							{instance.name} · {instance.service} · {instance.image}
						</span>
					))}
				</div>
			) : null}
		</div>
	);
}
