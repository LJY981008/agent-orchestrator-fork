// LADO fork: "Import from .gitlab-ci.yml" in the log watch card (issue #87).
// LADO reads the project's .gitlab-ci.yml files, each workspace repository's
// in a workspace, and offers the Docker hosts and the compose projects they
// deploy to. The developer picks some; Add only fills the card's draft with
// unsaved sources, one compose project target per project picked in that
// file, with the repository the file suggests when the guess is sure (see
// ladoLogCISuggestedRepository). Nothing reaches the daemon's settings before the card's Save.
import { X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useImportLadoLogCI } from "../../hooks/useLado";
import { ladoErrorText } from "../../lib/lado-errors";
import { ladoLogCIHostKey, ladoLogSourceFromCI, type LadoLogCIFile, type LadoLogSourceDraft } from "../../lib/lado-log-watch";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

/** A compose project's identity in the import list: its file, then its name. */
function composeKey(file: LadoLogCIFile, project: string): string {
	return `${file.repo}\0${file.path}\0${project}`;
}

export function LadoLogWatchImportDialog({
	projectId,
	open,
	existingEndpoints,
	room,
	repositoryRefs,
	onOpenChange,
	onAdd,
}: {
	projectId: string;
	open: boolean;
	/** The endpoints the draft has already, which the list offers no more. */
	existingEndpoints: ReadonlySet<string>;
	/** How many sources the draft can take still. */
	room: number;
	/** The repositories a target may pick; none in a project with one repository, where nothing is suggested. */
	repositoryRefs: readonly string[];
	onOpenChange: (open: boolean) => void;
	onAdd: (sources: LadoLogSourceDraft[]) => void;
}) {
	const { t } = useTranslation();
	const read = useImportLadoLogCI(projectId);
	const [hosts, setHosts] = useState<ReadonlySet<string>>(new Set());
	const [projects, setProjects] = useState<ReadonlySet<string>>(new Set());
	const { mutate, reset } = read;

	// Each opening reads the files again; nothing stays picked from before.
	useEffect(() => {
		if (!open) return;
		setHosts(new Set());
		setProjects(new Set());
		reset();
		mutate();
	}, [open, mutate, reset]);

	const files = read.data?.files ?? [];
	const toggle = (set: ReadonlySet<string>, key: string, on: boolean) => {
		const next = new Set(set);
		if (on) next.add(key);
		else next.delete(key);
		return next;
	};
	const add = () => {
		const sources: LadoLogSourceDraft[] = [];
		for (const file of files) {
			const picked = file.composeProjects.filter((project) => projects.has(composeKey(file, project)));
			for (const host of file.hosts) {
				if (hosts.has(ladoLogCIHostKey(file, host))) sources.push(ladoLogSourceFromCI(host, picked, file, repositoryRefs));
			}
		}
		onAdd(sources.slice(0, room));
		onOpenChange(false);
	};
	const tooMany = hosts.size > room;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={cn(settingsDialogContentClass, "w-[min(640px,calc(100vw-24px))]")}
				data-testid="lado-log-watch-import-dialog"
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.logWatch.importTitle")}</DialogTitle>
				</div>
				{/* The div scrolls; a fieldset as the scroll container spills under the footer. */}
				<div className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
					<p className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.importHint")}</p>
					{read.isPending ? (
						<p className="text-control text-settings-muted" role="status">
							{t("common.loading")}
						</p>
					) : read.isError ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{ladoErrorText(t, read.error)}
						</p>
					) : read.isSuccess && files.every((file) => file.hosts.length === 0 && file.composeProjects.length === 0) ? (
						<p className="text-control text-settings-muted">{t("lado.logWatch.importEmpty")}</p>
					) : null}
					{files.map((file) => (
						<section key={`${file.repo}\0${file.path}`} className="flex flex-col gap-2" data-testid="lado-log-watch-import-file">
							<h3 className="font-mono text-caption leading-4 text-settings-label">{file.repo ? `${file.repo}/${file.path}` : file.path}</h3>
							{file.hosts.length > 0 ? (
								<div className="flex flex-col gap-1.5">
									<span className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.importHosts")}</span>
									{file.hosts.map((host) => {
										const key = ladoLogCIHostKey(file, host);
										const known = host.endpoint !== "" && existingEndpoints.has(host.endpoint);
										const id = `lado-log-ci-host-${encodeURIComponent(key)}`;
										return (
											<div key={key} className="flex min-w-0 items-center gap-2" data-testid="lado-log-watch-import-host">
												<Checkbox
													id={id}
													checked={hosts.has(key)}
													disabled={known}
													onCheckedChange={(checked) => setHosts(toggle(hosts, key, checked === true))}
												/>
												<label htmlFor={id} className="flex min-w-0 flex-1 items-center gap-2 text-control">
													<span className="shrink-0 text-settings-label">{host.displayName}</span>
													<span className="min-w-0 truncate font-mono text-settings-muted">
														{host.endpoint || t("lado.logWatch.importUnresolved", { variable: host.unresolved ?? "" })}
													</span>
													{known ? <span className="ml-auto shrink-0 text-caption text-settings-muted">{t("lado.logWatch.importKnown")}</span> : null}
												</label>
											</div>
										);
									})}
								</div>
							) : null}
							{file.composeProjects.length > 0 ? (
								<div className="flex flex-col gap-1.5">
									<span className="text-caption leading-4 text-settings-muted">{t("lado.logWatch.importComposeProjects")}</span>
									{file.composeProjects.map((project) => {
										const key = composeKey(file, project);
										const id = `lado-log-ci-compose-${encodeURIComponent(key)}`;
										return (
											<div key={key} className="flex min-w-0 items-center gap-2" data-testid="lado-log-watch-import-compose">
												<Checkbox
													id={id}
													checked={projects.has(key)}
													onCheckedChange={(checked) => setProjects(toggle(projects, key, checked === true))}
												/>
												<label htmlFor={id} className="min-w-0 truncate font-mono text-control text-settings-label">
													{project}
												</label>
											</div>
										);
									})}
								</div>
							) : null}
							{file.notes.length > 0 ? (
								<ul className="flex flex-col gap-0.5">
									{file.notes.map((note) => (
										<li key={note} className="text-caption leading-4 text-settings-muted">
											{note}
										</li>
									))}
								</ul>
							) : null}
						</section>
					))}
					{tooMany ? (
						<p className="text-caption leading-4 text-error" role="alert">
							{t("lado.logWatch.sourcesLimit", { max: room })}
						</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					<Button type="button" variant="footer" className={compactButtonClass} onClick={() => onOpenChange(false)}>
						{t("confirm.cancel")}
					</Button>
					<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={hosts.size === 0 || tooMany} onClick={add}>
						{t("lado.logWatch.importAdd")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
