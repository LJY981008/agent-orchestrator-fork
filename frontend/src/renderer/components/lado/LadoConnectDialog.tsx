// LADO fork: connect a repository with an access token and SSH, then link the
// registered project or clone the repository (docs/LJY-PRD/TDD.md §5.1). A
// clone goes into a project of its own, a new workspace, or a registered
// workspace beside its other repositories. It follows the Clone Repository
// dialog: same shell, mono code-like fields, and errors below the field they
// belong to.
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronLeft, Folder, FolderTree, KeyRound, Link2, X } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery, workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { aoBridge } from "../../lib/bridge";
import {
	checkLadoConnection,
	createLadoConnection,
	LadoApiError,
	ladoQueryRoot,
	type LadoClonePlacement,
	type LadoConnectionCheck,
	type LadoConnectionRequest,
} from "../../lib/lado";
import { type LadoCheckProblem, ladoErrorText, ladoSSHProblem, ladoTokenProblem } from "../../lib/lado-errors";
import { joinCloneDestination, LAST_CLONE_DESTINATION_KEY, repositoryNameFromGitUrl } from "../CloneRepositoryDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";

type ConnectionInputs = { url: string; token: string; sshKeyPath: string };

const EMPTY_INPUTS: ConnectionInputs = { url: "", token: "", sshKeyPath: "" };

const PLACEMENTS: readonly LadoClonePlacement[] = ["standalone", "new_workspace", "workspace"];

const fieldLabelClass = "text-[13px] font-semibold text-[var(--color-text-import-title)]";
const helpClass = "text-pretty text-[12px] leading-5 text-[var(--color-text-import-muted)]";
const problemClass = "text-pretty text-[12px] leading-5 text-destructive";

function initialDestination(): string {
	try {
		return window.localStorage.getItem(LAST_CLONE_DESTINATION_KEY) || "~/ao/projects";
	} catch {
		return "~/ao/projects";
	}
}

function sameInputs(left: ConnectionInputs, right: ConnectionInputs): boolean {
	return left.url === right.url && left.token === right.token && left.sshKeyPath === right.sshKeyPath;
}

export function LadoConnectDialog({
	open,
	onBack,
	onClose,
}: {
	open: boolean;
	onBack: () => void;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const projects = useWorkspaceQuery({ includeCloud: false, enabled: open });
	const [inputs, setInputs] = useState<ConnectionInputs>(EMPTY_INPUTS);
	const [destination, setDestination] = useState(initialDestination);
	const [placement, setPlacement] = useState<LadoClonePlacement>("standalone");
	const [workspaceName, setWorkspaceName] = useState("");
	const [workspaceId, setWorkspaceId] = useState("");
	const [checked, setChecked] = useState<{ inputs: ConnectionInputs; result: LadoConnectionCheck } | null>(null);
	const [phase, setPhase] = useState<"idle" | "checking" | "connecting">("idle");
	const [choosing, setChoosing] = useState(false);
	const [urlError, setUrlError] = useState<string | null>(null);
	const [formError, setFormError] = useState<string | null>(null);
	const request = useRef(0);

	useEffect(() => {
		if (open) return;
		request.current += 1;
		setInputs(EMPTY_INPUTS);
		setPlacement("standalone");
		setWorkspaceName("");
		setWorkspaceId("");
		setChecked(null);
		setPhase("idle");
		setChoosing(false);
		setUrlError(null);
		setFormError(null);
	}, [open]);

	const workspaces = (projects.data ?? []).filter((project) => project.kind === "workspace" && !project.hostId);
	const check = checked && sameInputs(checked.inputs, inputs) ? checked.result : null;
	const busy = phase !== "idle" || choosing;
	// Where to clone is the last decision: it appears once the check passes.
	const needsPlacement = check?.ready === true && !check.project;
	const repositoryName = repositoryNameFromGitUrl(inputs.url);
	const parent = destination.trim();
	const chosenWorkspace = workspaces.find((workspace) => workspace.id === workspaceId);
	const placementReady =
		placement === "standalone" ? parent !== ""
			: placement === "new_workspace" ? parent !== "" && workspaceName.trim() !== ""
				: chosenWorkspace !== undefined;
	const canConnect = check?.ready === true && (!needsPlacement || placementReady);
	const tokenProblem = check ? ladoTokenProblem(t, check) : null;
	const sshProblem = check ? ladoSSHProblem(t, check) : null;
	const notOk = check && !tokenProblem ? check.token.permissions.filter((permission) => permission.status !== "ok") : [];
	const permissionLabels: Record<string, string> = {
		repository: t("lado.connect.permission.repository"),
		issues: t("lado.connect.permission.issues"),
		pull_requests: t("lado.connect.permission.pullRequests"),
		branch_rules: t("lado.connect.permission.branchRules"),
		commit_statuses: t("lado.connect.permission.commitStatuses"),
	};

	const update = (patch: Partial<ConnectionInputs>) => {
		setInputs((previous) => ({ ...previous, ...patch }));
		setUrlError(null);
		setFormError(null);
	};

	const runCheck = async () => {
		const id = ++request.current;
		const snapshot = inputs;
		setPhase("checking");
		setUrlError(null);
		setFormError(null);
		try {
			const result = await checkLadoConnection({
				url: snapshot.url.trim(),
				token: snapshot.token.trim() || undefined,
				sshKeyPath: snapshot.sshKeyPath.trim() || undefined,
			});
			if (id !== request.current) return;
			setChecked({ inputs: snapshot, result });
		} catch (err) {
			if (id !== request.current) return;
			const message = ladoErrorText(t, err);
			if (err instanceof LadoApiError && err.code === "LADO_REPOSITORY_URL_INVALID") setUrlError(message);
			else setFormError(message);
		} finally {
			if (id === request.current) setPhase("idle");
		}
	};

	const connectionRequest = (): LadoConnectionRequest => {
		const base: LadoConnectionRequest = {
			url: inputs.url.trim(),
			token: inputs.token.trim() || undefined,
			sshKeyPath: inputs.sshKeyPath.trim() || undefined,
		};
		if (!needsPlacement) return base;
		if (placement === "workspace") return { ...base, placement, workspaceId };
		if (placement === "new_workspace") return { ...base, placement, destinationParent: parent, workspaceName: workspaceName.trim() };
		return { ...base, placement, destinationParent: parent };
	};

	const runConnect = async () => {
		const id = ++request.current;
		setPhase("connecting");
		setFormError(null);
		try {
			const result = await createLadoConnection(connectionRequest());
			if (id !== request.current) return;
			if (needsPlacement && placement !== "workspace") {
				try {
					window.localStorage.setItem(LAST_CLONE_DESTINATION_KEY, parent);
				} catch {
					// Remembering the folder is a convenience.
				}
			}
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void queryClient.invalidateQueries({ queryKey: ladoQueryRoot });
			onClose();
			void navigate({ to: "/projects/$projectId", params: { projectId: result.projectId } });
		} catch (err) {
			if (id !== request.current) return;
			setFormError(ladoErrorText(t, err));
		} finally {
			if (id === request.current) setPhase("idle");
		}
	};

	const chooseDestination = async () => {
		setChoosing(true);
		try {
			const selected = await aoBridge.app.chooseDirectory({
				title: t("lado.connect.chooseDestination"),
				defaultPath: "~/ao/projects",
			});
			if (selected) setDestination(selected);
		} catch {
			setFormError(t("lado.error.folderPickerFailed"));
		} finally {
			setChoosing(false);
		}
	};

	const submit = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (busy || !inputs.url.trim()) return;
		if (canConnect) void runConnect();
		else void runCheck();
	};

	const submitLabel = phase === "checking"
		? t("lado.connect.checking")
		: phase === "connecting"
			? t("lado.connect.connecting")
			: check?.ready === true
				? t("lado.connect.connect")
				: t("lado.connect.check");

	const placementLabels: Record<LadoClonePlacement, { title: string; help: string }> = {
		standalone: { title: t("lado.connect.placement.standalone"), help: t("lado.connect.placement.standaloneHelp") },
		new_workspace: { title: t("lado.connect.placement.newWorkspace"), help: t("lado.connect.placement.newWorkspaceHelp") },
		workspace: { title: t("lado.connect.placement.workspace"), help: t("lado.connect.placement.workspaceHelp") },
	};

	let placementSummary = "";
	if (repositoryName) {
		if (placement === "standalone" && parent) {
			placementSummary = t("lado.connect.clonesInto", { path: joinCloneDestination(parent, repositoryName) });
		} else if (placement === "new_workspace" && parent && workspaceName.trim()) {
			const root = joinCloneDestination(parent, workspaceName.trim());
			placementSummary = t("lado.connect.createsWorkspace", { path: root, child: joinCloneDestination(root, repositoryName) });
		} else if (placement === "workspace" && chosenWorkspace) {
			placementSummary = t("lado.connect.clonesInto", { path: joinCloneDestination(chosenWorkspace.path, repositoryName) });
		}
	}

	return (
		<Dialog.Root open={open} onOpenChange={(next) => !next && !busy && onClose()}>
			<Dialog.Portal>
				<Dialog.Content className="fixed left-1/2 top-1/2 z-overlay flex max-h-[min(720px,calc(100svh-24px))] w-[min(560px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out motion-reduce:animate-none">
					<div className="relative flex shrink-0 items-center gap-3 px-4 pt-3">
						<Button type="button" variant="outline" size="icon" aria-label={t("lado.connect.back")} disabled={busy} onClick={onBack}>
							<ChevronLeft className="size-4" aria-hidden="true" />
						</Button>
						<div className="min-w-0 flex-1 pr-8">
							<Dialog.Title className="text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
								{t("lado.connect.title")}
							</Dialog.Title>
							<Dialog.Description className="sr-only">{t("lado.connect.description")}</Dialog.Description>
						</div>
						<button type="button" className="settings-close-button" aria-label={t("lado.connect.close")} disabled={busy} onClick={onClose}>
							<X className="size-4" aria-hidden="true" />
						</button>
					</div>

					<form className="min-h-0 overflow-y-auto" onSubmit={submit}>
						<div className="space-y-4 px-4 pb-1 pt-4">
							{formError ? (
								<div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-pretty text-[12px] leading-5 text-destructive" role="alert">
									{formError}
								</div>
							) : null}

							<div className="space-y-2">
								<Label htmlFor="ladoConnectUrl" className={fieldLabelClass}>
									{t("lado.connect.url")}
								</Label>
								<div className="relative">
									<Link2 className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--color-text-import-muted)]" aria-hidden="true" />
									<Input
										id="ladoConnectUrl"
										autoFocus
										autoCapitalize="none"
										autoComplete="off"
										aria-invalid={urlError ? true : undefined}
										aria-describedby={urlError ? "ladoConnectUrlError" : undefined}
										className="bg-[var(--color-bg-import-card)] pl-10 font-mono text-[13px]"
										disabled={busy}
										placeholder={t("lado.connect.urlPlaceholder")}
										spellCheck={false}
										value={inputs.url}
										onChange={(event) => update({ url: event.target.value })}
									/>
								</div>
								{urlError ? (
									<p id="ladoConnectUrlError" className={problemClass} role="alert">{urlError}</p>
								) : null}
							</div>

							<div className="space-y-2">
								<Label htmlFor="ladoConnectToken" className={fieldLabelClass}>
									{t("lado.connect.token")}
								</Label>
								<div className="relative">
									<KeyRound className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--color-text-import-muted)]" aria-hidden="true" />
									<Input
										id="ladoConnectToken"
										type="password"
										autoComplete="off"
										aria-invalid={tokenProblem ? true : undefined}
										aria-describedby="ladoConnectTokenHelp"
										className="bg-[var(--color-bg-import-card)] pl-10 font-mono text-[13px]"
										disabled={busy}
										spellCheck={false}
										value={inputs.token}
										onChange={(event) => update({ token: event.target.value })}
									/>
								</div>
								{tokenProblem ? (
									<CheckProblem id="ladoConnectTokenHelp" problem={tokenProblem} />
								) : (
									<p id="ladoConnectTokenHelp" className={helpClass}>
										{check?.token.stored && !inputs.token.trim()
											? t("lado.connect.tokenStored", { host: check.host })
											: t("lado.connect.tokenHelp")}
									</p>
								)}
							</div>

							<div className="space-y-2">
								<Label htmlFor="ladoConnectKey" className={fieldLabelClass}>
									{t("lado.connect.sshKey")}
								</Label>
								<Input
									id="ladoConnectKey"
									autoCapitalize="none"
									autoComplete="off"
									aria-invalid={sshProblem ? true : undefined}
									aria-describedby="ladoConnectKeyHelp"
									className="bg-[var(--color-bg-import-card)] font-mono text-[13px]"
									disabled={busy}
									placeholder={t("lado.connect.sshKeyPlaceholder")}
									spellCheck={false}
									value={inputs.sshKeyPath}
									onChange={(event) => update({ sshKeyPath: event.target.value })}
								/>
								{sshProblem ? (
									<CheckProblem id="ladoConnectKeyHelp" problem={sshProblem} />
								) : (
									<p id="ladoConnectKeyHelp" className={helpClass}>{t("lado.connect.sshKeyHelp")}</p>
								)}
							</div>

							{check && !tokenProblem ? (
								<div className="space-y-1.5 text-[12px] leading-5" data-testid="lado-connect-summary">
									<p className="text-foreground">{t("lado.connect.signedInAs", { login: check.token.login ?? "", host: check.host })}</p>
									{notOk.map((permission) => (
										<p
											key={permission.name}
											className={permission.status === "missing" ? "text-destructive" : "text-[var(--color-text-import-muted)]"}
											title={permission.detail}
										>
											{t(permission.status === "missing" ? "lado.connect.permissionMissing" : "lado.connect.permissionUnverified", {
												permission: permissionLabels[permission.name] ?? permission.name,
											})}
										</p>
									))}
									{check.project ? (
										<p className="text-[var(--color-text-import-muted)]">
											{check.project.repository
												? t("lado.connect.linksToWorkspaceRepository", { name: check.project.name, repository: check.project.repository })
												: t("lado.connect.linksTo", { name: check.project.name })}
										</p>
									) : null}
								</div>
							) : null}

							{needsPlacement ? (
								<fieldset className="space-y-2">
									<legend className={`${fieldLabelClass} mb-2`}>{t("lado.connect.placement")}</legend>
									<div className="overflow-hidden rounded-md border border-border/60" role="radiogroup" aria-label={t("lado.connect.placement")}>
										{PLACEMENTS.map((option) => {
											const id = `ladoConnectPlacement-${option}`;
											const unavailable = option === "workspace" && workspaces.length === 0;
											return (
												<label
													key={option}
													htmlFor={id}
													className="flex cursor-pointer items-start gap-3 border-b border-border/60 px-3 py-2.5 last:border-b-0 hover:bg-foreground/5 has-[:disabled]:cursor-default has-[:disabled]:opacity-60"
												>
													<input
														id={id}
														type="radio"
														name="ladoConnectPlacement"
														className="mt-1"
														aria-labelledby={`${id}-title`}
														aria-describedby={`${id}-help`}
														checked={placement === option}
														disabled={busy || unavailable}
														onChange={() => setPlacement(option)}
													/>
													<span className="min-w-0 space-y-0.5">
														<span id={`${id}-title`} className="block text-[13px] font-medium text-foreground">{placementLabels[option].title}</span>
														<span id={`${id}-help`} className={`block ${helpClass}`}>
															{unavailable ? t("lado.connect.noWorkspaces") : placementLabels[option].help}
														</span>
													</span>
												</label>
											);
										})}
									</div>
								</fieldset>
							) : null}

							{needsPlacement && placement === "new_workspace" ? (
								<div className="space-y-2">
									<Label htmlFor="ladoConnectWorkspaceName" className={fieldLabelClass}>
										{t("lado.connect.workspaceName")}
									</Label>
									<div className="relative">
										<FolderTree className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--color-text-import-muted)]" aria-hidden="true" />
										<Input
											id="ladoConnectWorkspaceName"
											autoCapitalize="none"
											autoComplete="off"
											className="bg-[var(--color-bg-import-card)] pl-10 font-mono text-[13px]"
											disabled={busy}
											placeholder={t("lado.connect.workspaceNamePlaceholder")}
											spellCheck={false}
											value={workspaceName}
											onChange={(event) => setWorkspaceName(event.target.value)}
										/>
									</div>
								</div>
							) : null}

							{needsPlacement && placement !== "workspace" ? (
								<div className="space-y-2">
									<Label htmlFor="ladoConnectDestination" className={fieldLabelClass}>
										{placement === "new_workspace" ? t("lado.connect.workspaceParent") : t("lado.connect.destination")}
									</Label>
									<div className="flex h-control-form items-center overflow-hidden rounded-md border border-transparent bg-[var(--color-bg-import-card)] text-[13px] text-foreground">
										<div className="relative min-w-0 flex-1">
											<Folder className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--color-text-import-muted)]" aria-hidden="true" />
											<Input
												id="ladoConnectDestination"
												className="border-transparent bg-transparent pl-10 font-mono text-[13px]"
												aria-describedby="ladoConnectPlacementHelp"
												disabled={busy}
												autoCapitalize="none"
												autoComplete="off"
												spellCheck={false}
												value={destination}
												onChange={(event) => setDestination(event.target.value)}
											/>
										</div>
										<button
											aria-label={t("lado.connect.chooseDestination")}
											className="flex h-full shrink-0 items-center border-l border-border/60 px-4 text-foreground outline-none transition-colors hover:bg-foreground/10 focus-visible:bg-foreground/10 disabled:pointer-events-none disabled:opacity-50"
											disabled={busy}
											type="button"
											onClick={() => void chooseDestination()}
										>
											{t("lado.connect.choose")}
										</button>
									</div>
								</div>
							) : null}

							{needsPlacement && placement === "workspace" && workspaces.length > 0 ? (
								<div className="space-y-2">
									<Label htmlFor="ladoConnectWorkspace" className={fieldLabelClass}>
										{t("lado.connect.workspace")}
									</Label>
									<Select value={workspaceId} onValueChange={setWorkspaceId} disabled={busy}>
										<SelectTrigger
											id="ladoConnectWorkspace"
											size="sm"
											aria-describedby="ladoConnectPlacementHelp"
											className="w-full bg-[var(--color-bg-import-card)] text-[13px]"
										>
											<SelectValue placeholder={t("lado.connect.chooseWorkspace")} />
										</SelectTrigger>
										<SelectContent>
											{workspaces.map((workspace) => (
												<SelectItem key={workspace.id} value={workspace.id} title={workspace.path}>
													{workspace.name}
												</SelectItem>
											))}
										</SelectContent>
									</Select>
								</div>
							) : null}

							{needsPlacement ? (
								<p id="ladoConnectPlacementHelp" className={helpClass}>
									{placementSummary || t("lado.connect.placementIncomplete")}
								</p>
							) : null}
						</div>

						<div className="flex shrink-0 justify-end gap-2 px-4 pb-4 pt-3">
							<Button type="submit" variant="primary" disabled={busy || !inputs.url.trim() || (check?.ready === true && !canConnect)}>
								{submitLabel}
							</Button>
						</div>
					</form>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

/** A failed check below its field: what to do, then what the tool printed. */
function CheckProblem({ id, problem }: { id: string; problem: LadoCheckProblem }) {
	return (
		<div id={id} className="space-y-1" role="alert">
			<p className={problemClass}>{problem.text}</p>
			{problem.detail ? (
				<p className="break-words font-mono text-[11px] leading-4 text-[var(--color-text-import-muted)]">{problem.detail}</p>
			) : null}
		</div>
	);
}
