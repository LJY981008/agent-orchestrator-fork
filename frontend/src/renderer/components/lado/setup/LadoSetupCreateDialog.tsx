// LADO fork: a new managed setup item made in the app (docs/LJY-PRD/HARNESS.md
// §9, plan §12). The developer picks the tool and the kind, names the item
// and writes its content, then reviews the dry run as the editor does: the
// content, the conflicts it brings, and whether creating it needs the desktop
// app's confirmation. Hook scripts only come from an adoption.
import { useMutation } from "@tanstack/react-query";
import { agentLabel } from "@aoagents/product-ui";
import { X } from "lucide-react";
import { useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../../hooks/useRestoreFocusOnClose";
import type { MessageKey } from "../../../i18n/messages";
import { LadoApiError } from "../../../lib/lado";
import { ladoErrorText, ladoSetupKindLabel } from "../../../lib/lado-errors";
import {
	previewLadoSetupChange,
	useLadoSetupChange,
	type LadoSetupChangeRequest,
	type LadoSetupCreateInput,
	type LadoSetupTool,
} from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { SettingsOptionMenu } from "../../settings/SettingsOptionMenu";
import { Button } from "../../ui/button";
import { Dialog, DialogClose, DialogContent, DialogTitle, settingsDialogBodyClass, settingsDialogFooterClass, settingsDialogHeaderClass } from "../../ui/dialog";
import { LadoRuleImpactDialog } from "../LadoRuleImpactDialog";
import { LadoSetupChangeReview, LadoSetupContentField, ladoSetupContentValue, ladoSetupWideDialogClass } from "./LadoSetupContent";
import { ladoSetupCreatableKinds, ladoSetupTools } from "./lado-setup-view";

const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";

type Kind = LadoSetupCreateInput["kind"];

// A one-file skill's SKILL.md needs both fields in its front matter.
const SKILL_TEMPLATE = "---\nname: \ndescription: \n---\n\n";

// An example name for each kind, as the tool lays its folder out.
const nameExamples: Record<Kind, MessageKey> = {
	instructions: "lado.setup.create.example.instructions",
	setting: "lado.setup.create.example.setting",
	skill: "lado.setup.create.example.skill",
	agent: "lado.setup.create.example.agent",
	command: "lado.setup.create.example.command",
	output_style: "lado.setup.create.example.outputStyle",
	mcp_server: "lado.setup.create.example.mcpServer",
};

// Where the daemon's refusals belong (backend/internal/service/ladosetup/changes.go).
type Field = "name" | "content";

function fieldOf(error: unknown, kind: Kind, name: string): Field | undefined {
	if (!(error instanceof LadoApiError)) return undefined;
	switch (error.code) {
		case "LADO_SETUP_ITEM_EXISTS":
			return "name";
		case "LADO_SETUP_CHANGE_INVALID":
			return error.details?.field === "name" ? "name" : undefined;
		case "LADO_SETUP_CONTENT_INVALID":
			return "content";
		case "LADO_SETUP_LOGIN_KEY":
			// A login key named as a setting is the name; one inside env is the content.
			return kind === "setting" && error.details?.key === name ? "name" : "content";
		default:
			return undefined;
	}
}

/** Open while the developer creates an item. Mount it again for each opening, so it starts empty. */
export function LadoSetupCreateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const change = useLadoSetupChange();
	const preview = useMutation({ mutationFn: previewLadoSetupChange, retry: false });
	const nameRef = useRef<HTMLInputElement>(null);
	const nameId = useId();
	const contentId = useId();
	const [tool, setTool] = useState<LadoSetupTool>("claude-code");
	const [kind, setKind] = useState<Kind>("instructions");
	const [name, setName] = useState("");
	const [content, setContent] = useState("");
	const [reviewing, setReviewing] = useState(false);
	const [invalidJSON, setInvalidJSON] = useState(false);

	const kinds = ladoSetupCreatableKinds(tool) as Kind[];
	const trimmed = name.trim();
	const busy = preview.isPending || change.isPending;
	// A failed apply after the impact dialog's confirm shows in that dialog.
	const error = (change.pending ? null : change.error) ?? preview.error;
	const field = fieldOf(error, kind, trimmed);
	const nameError = error && field === "name" ? ladoErrorText(t, error) : null;
	const contentError = invalidJSON ? t("lado.setup.editor.invalidJson") : error && field === "content" ? ladoErrorText(t, error) : null;
	const formError = error && !field ? ladoErrorText(t, error) : null;

	const close = () => onOpenChange(false);
	const edited = () => {
		setInvalidJSON(false);
		if (preview.error) preview.reset();
		if (change.error) change.reset();
	};
	const chooseKind = (next: Kind) => {
		setKind(next);
		// A skill starts from the front matter its SKILL.md needs; that template
		// goes again, untouched, when another kind is chosen.
		if (next === "skill" && content.trim() === "") setContent(SKILL_TEMPLATE);
		if (next !== "skill" && content === SKILL_TEMPLATE) setContent("");
		edited();
	};
	const chooseTool = (next: LadoSetupTool) => {
		setTool(next);
		if (!(ladoSetupCreatableKinds(next) as string[]).includes(kind)) chooseKind("instructions");
		edited();
	};

	const request = (): LadoSetupChangeRequest | null => {
		const value = ladoSetupContentValue(kind, content);
		if (!trimmed || !value.ok) return null;
		return { op: "create", input: { tool, kind, name: trimmed, content: value.value } };
	};

	const review = () => {
		if (busy || !trimmed) return;
		if (!ladoSetupContentValue(kind, content).ok) {
			setInvalidJSON(true);
			return;
		}
		const next = request();
		if (!next) return;
		change.reset();
		preview.mutate(next, {
			onSuccess: () => setReviewing(true),
			onError: (failure) => {
				if (fieldOf(failure, kind, trimmed) === "name") nameRef.current?.focus();
			},
		});
	};

	const create = () => {
		const next = request();
		if (busy || !next) return;
		change
			.change(next)
			.then((outcome) => {
				if (outcome.status === "applied") close();
			})
			.catch((failure: unknown) => {
				// A refused name or content goes back to its field.
				if (fieldOf(failure, kind, trimmed)) setReviewing(false);
			});
	};

	// The content field's name stays the kind's while the name is typed, so the
	// Markdown editor does not start again on every key.
	const contentName = kind === "skill" ? "SKILL.md" : ladoSetupKindLabel(t, kind);
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && busy) return;
				onOpenChange(next);
			}}
		>
			<DialogContent
				showCloseButton={false}
				aria-describedby={undefined}
				className={ladoSetupWideDialogClass}
				data-testid="lado-setup-create"
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					nameRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("lado.setup.create.title")}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "min-w-0 gap-(--size-settings-section-inner-gap) px-5 pt-0 pb-4")}>
					{reviewing && preview.data ? (
						<>
							<p className="text-sm text-settings-label">
								{[agentLabel(tool), ladoSetupKindLabel(t, kind), trimmed].join(" · ")}
							</p>
							<LadoSetupChangeReview result={preview.data} kind={kind} name={trimmed} presence="lado.setup.create.needsPresence" />
						</>
					) : (
						<fieldset disabled={busy} className="flex min-w-0 flex-col gap-(--size-settings-section-inner-gap)">
							<div className="flex flex-wrap gap-3">
								<Field label={t("lado.setup.create.tool")}>
									<SettingsOptionMenu<LadoSetupTool>
										aria-label={t("lado.setup.create.tool")}
										value={tool}
										options={ladoSetupTools.map((value) => ({ value, label: agentLabel(value) }))}
										menuAlign="start"
										triggerClassName="w-fit"
										onChange={chooseTool}
									/>
								</Field>
								<Field label={t("lado.setup.create.kind")}>
									<SettingsOptionMenu<Kind>
										aria-label={t("lado.setup.create.kind")}
										value={kind}
										options={kinds.map((value) => ({ value, label: ladoSetupKindLabel(t, value) }))}
										menuAlign="start"
										triggerClassName="w-fit"
										onChange={chooseKind}
									/>
								</Field>
							</div>
							<div className="flex flex-col gap-1.5">
								<label htmlFor={nameId} className="settings-field-label">
									{t("lado.setup.create.name")}
								</label>
								<input
									ref={nameRef}
									id={nameId}
									value={name}
									onChange={(event) => {
										setName(event.target.value);
										edited();
									}}
									placeholder={tool === "codex" && kind === "instructions" ? t("lado.setup.create.example.codexInstructions") : t(nameExamples[kind])}
									aria-invalid={nameError ? true : undefined}
									aria-describedby={nameError ? `${nameId}-error` : undefined}
									autoComplete="off"
									spellCheck={false}
									className="settings-field-control h-(--size-settings-action-height) rounded-md! font-mono"
								/>
								{nameError ? (
									<p id={`${nameId}-error`} role="alert" className="text-caption leading-4 text-error">
										{nameError}
									</p>
								) : null}
							</div>
							<div className="flex flex-col gap-1.5">
								<span className="settings-field-label">{t("lado.setup.create.content")}</span>
								<LadoSetupContentField
									kind={kind}
									name={contentName}
									value={content}
									onChange={(next) => {
										setContent(next);
										edited();
									}}
									error={contentError}
									errorId={contentId}
									placeholder={kind === "mcp_server" ? t("lado.setup.create.example.mcpServerValue") : kind === "setting" ? t("lado.setup.create.example.settingValue") : undefined}
									autoFocus={false}
								/>
							</div>
						</fieldset>
					)}
				</div>
				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					{formError ? (
						<p role="alert" className="mr-auto text-caption leading-4 text-error">
							{formError}
						</p>
					) : null}
					{reviewing ? (
						<>
							<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={() => setReviewing(false)}>
								{t("lado.setup.editor.back")}
							</Button>
							<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={busy} onClick={create}>
								{change.isPending ? t("lado.rules.editor.saving") : t("lado.rules.editor.create")}
							</Button>
						</>
					) : (
						<>
							<Button type="button" variant="footer" className={compactButtonClass} disabled={busy} onClick={close}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="footer-primary" className={compactButtonClass} disabled={busy || !trimmed} onClick={review}>
								{preview.isPending ? t("lado.setup.editor.reviewing") : t("lado.setup.editor.review")}
							</Button>
						</>
					)}
				</div>
				<LadoRuleImpactDialog
					impact={change.pending?.result.impact ?? null}
					busy={change.isPending}
					error={change.pending ? change.error : null}
					title={t("lado.setup.impact.title")}
					onConfirm={() => {
						change
							.confirm()
							.then((outcome) => {
								if (outcome.status === "applied") close();
							})
							.catch(() => undefined);
					}}
					onCancel={change.cancel}
				/>
			</DialogContent>
		</Dialog>
	);
}

function Field({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<span className="settings-field-label">{label}</span>
			{children}
		</div>
	);
}
