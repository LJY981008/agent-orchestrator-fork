// LADO fork: the parts the setup editor, the drift review and the new-item
// dialog share (docs/LJY-PRD/HARNESS.md §9 and §10): the content field
// (Markdown for files, a plain code editor for JSON values and scripts), the
// difference a change makes, a folder's files, and what a dry run says about
// a change before it is made.
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { indentUnit } from "@codemirror/language";
import { searchKeymap } from "@codemirror/search";
import { Compartment, EditorState } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, keymap, lineNumbers, placeholder as placeholderText } from "@codemirror/view";
import { MultiFileDiff } from "@pierre/diffs/react";
import type { TFunction } from "i18next";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../../../i18n/messages";
import { ladoSetupConflictText, ladoSetupWarningText } from "../../../lib/lado-errors";
import type { LadoSetupChangeResult, LadoSetupConflict, LadoSetupContent } from "../../../lib/lado-setup";
import { cn } from "../../../lib/utils";
import { useUiStore } from "../../../stores/ui-store";
import { AO_PIERRE_SURFACE_CSS } from "../../diffs/pierreTheme";
import { MarkdownEditor } from "../../MarkdownEditor";
import { settingsDialogContentClass } from "../../ui/dialog";
import {
	ladoSetupConflictsBySeverity,
	ladoSetupFolderContent,
	ladoSetupJSONKind,
	ladoSetupMarkdownKind,
	ladoSetupSeverityDotClass,
	ladoSetupSeverityKeys,
	ladoSetupSize,
} from "./lado-setup-view";

/** The setup dialogs that show content are wide enough for an editor and a diff. */
export const ladoSetupWideDialogClass = cn(
	settingsDialogContentClass,
	"w-[min(var(--size-settings-dialog-wide),calc(100vw-var(--space-8)))]",
);

const codeTheme = EditorView.theme(
	{
		"&": {
			backgroundColor: "var(--color-bg-primary)",
			color: "var(--color-text-primary)",
			fontSize: "var(--font-size-sm)",
			height: "100%",
		},
		".cm-scroller": { fontFamily: "var(--font-family-mono)", overflow: "auto" },
		".cm-content": { caretColor: "var(--color-text-primary)", minHeight: "100%", padding: "0.75rem 1rem" },
		".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--color-text-primary)" },
		".cm-placeholder": { color: "var(--color-text-passive)" },
		".cm-gutters": { backgroundColor: "var(--color-bg-secondary)", border: "0", color: "var(--color-text-muted)" },
		".cm-activeLineGutter, .cm-activeLine": { backgroundColor: "var(--color-interactive-hover)" },
		".cm-selectionBackground, ::selection": { backgroundColor: "var(--color-interactive-active) !important" },
	},
	{ dark: false },
);

function contentAttributes(label: string, invalid: boolean, describedBy: string | undefined) {
	return EditorView.contentAttributes.of({
		"aria-label": label,
		...(invalid ? { "aria-invalid": "true" } : {}),
		...(describedBy ? { "aria-describedby": describedBy } : {}),
	});
}

/**
 * A plain text editor for a JSON value or a script, with the Markdown
 * editor's look: line numbers, undo and search, no formatting toolbar.
 */
export function LadoSetupCodeEditor({
	value,
	onChange,
	label,
	placeholder,
	invalid = false,
	describedBy,
}: {
	value: string;
	onChange: (value: string) => void;
	label: string;
	placeholder?: string;
	invalid?: boolean;
	describedBy?: string;
}) {
	const containerRef = useRef<HTMLDivElement>(null);
	const viewRef = useRef<EditorView | null>(null);
	const attributesRef = useRef(new Compartment());
	const onChangeRef = useRef(onChange);
	const applyingRef = useRef(false);
	onChangeRef.current = onChange;

	useEffect(() => {
		if (!containerRef.current) return;
		const view = new EditorView({
			state: EditorState.create({
				doc: value,
				extensions: [
					lineNumbers(),
					indentUnit.of("  "),
					history(),
					drawSelection(),
					highlightActiveLine(),
					codeTheme,
					keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
					...(placeholder ? [placeholderText(placeholder)] : []),
					attributesRef.current.of(contentAttributes(label, invalid, describedBy)),
					EditorView.updateListener.of((update) => {
						if (update.docChanged && !applyingRef.current) onChangeRef.current(update.state.sliceDoc());
					}),
				],
			}),
			parent: containerRef.current,
		});
		viewRef.current = view;
		return () => {
			view.destroy();
			viewRef.current = null;
		};
		// The editor is created once; later values arrive through the effect below,
		// so the developer's selection and undo history survive a re-render.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	useEffect(() => {
		viewRef.current?.dispatch({ effects: attributesRef.current.reconfigure(contentAttributes(label, invalid, describedBy)) });
	}, [label, invalid, describedBy]);

	useEffect(() => {
		const view = viewRef.current;
		if (!view || view.state.sliceDoc() === value) return;
		applyingRef.current = true;
		view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } });
		applyingRef.current = false;
	}, [value]);

	return <div ref={containerRef} className="h-full min-h-0 overflow-hidden" />;
}

/**
 * An item's content as the developer edits it: Markdown in the Markdown
 * editor, a JSON value or a script in the code editor. name labels it;
 * changing name in the Markdown editor starts it again, so a dialog whose
 * name field is still being typed passes something steady instead.
 */
export function LadoSetupContentField({
	kind,
	name,
	value,
	onChange,
	error,
	errorId,
	placeholder,
	autoFocus = true,
}: {
	kind: string;
	name: string;
	value: string;
	onChange: (value: string) => void;
	error: string | null;
	errorId: string;
	placeholder?: string;
	autoFocus?: boolean;
}) {
	const { t } = useTranslation();
	return (
		<div className="flex flex-col gap-1.5" data-testid="lado-setup-content" data-editor={ladoSetupMarkdownKind(kind) ? "markdown" : "code"}>
			<div className="h-72 min-h-0 overflow-hidden rounded-md border border-border">
				{ladoSetupMarkdownKind(kind) ? (
					<MarkdownEditor filePath={name} value={value} onChange={onChange} autoFocus={autoFocus} />
				) : (
					<LadoSetupCodeEditor
						value={value}
						onChange={onChange}
						label={t("markdownEditor.edit", { filePath: name })}
						placeholder={placeholder}
						invalid={Boolean(error)}
						describedBy={error ? errorId : undefined}
					/>
				)}
			</div>
			{error ? (
				<p id={errorId} role="alert" className="text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}

/**
 * The value a change sends for the text the developer typed: the text itself
 * for a file, the parsed value for a JSON kind. Text that is no JSON is not ok.
 */
export function ladoSetupContentValue(kind: string, text: string): { ok: true; value: unknown } | { ok: false } {
	if (!ladoSetupJSONKind(kind)) return { ok: true, value: text };
	try {
		return { ok: true, value: JSON.parse(text) as unknown };
	} catch {
		return { ok: false };
	}
}

/** The file name a diff highlights the content by. */
function diffFileName(kind: string, name: string): string {
	if (ladoSetupJSONKind(kind)) return `${name}.json`;
	if (kind === "skill") return "SKILL.md";
	return name.split("/").at(-1) ?? name;
}

/** What changed between two texts, as the Files panel shows a diff. */
export function LadoSetupDiff({ kind, name, before, after }: { kind: string; name: string; before: string; after: string }) {
	const { t } = useTranslation();
	const resolvedTheme = useUiStore((state) => state.resolvedTheme);
	if (before === after) {
		return <p className="text-control leading-4 text-settings-muted">{t("lado.setup.diff.none")}</p>;
	}
	const file = diffFileName(kind, name);
	return (
		<div role="region" aria-label={t("lado.setup.diff.label")} className="ao-pierre-surface min-w-0 select-text overflow-hidden rounded-md" data-testid="lado-setup-diff">
			<MultiFileDiff
				disableWorkerPool
				oldFile={{ name: file, contents: before }}
				newFile={{ name: file, contents: after }}
				options={{
					diffIndicators: "classic",
					diffStyle: "unified",
					disableFileHeader: true,
					hunkSeparators: "line-info",
					lineDiffType: "word-alt",
					overflow: "wrap",
					theme: { dark: "github-dark", light: "github-light" },
					themeType: resolvedTheme,
					unsafeCSS: AO_PIERRE_SURFACE_CSS,
				}}
			/>
		</div>
	);
}

/** A folder item's files, read-only. */
export function LadoSetupFileList({ files, label }: { files: LadoSetupContent["files"]; label: string }) {
	const { t, i18n } = useTranslation();
	return (
		<ul aria-label={label} className="settings-grouped-rows flex w-full flex-col" data-testid="lado-setup-files">
			{files.map((file) => (
				<li key={file.path} className="settings-row-bar">
					<span className="min-w-0 truncate font-mono text-xs text-settings-label" title={file.path}>
						{file.path}
					</span>
					<span className="shrink-0 text-xs tabular-nums text-settings-muted">
						{file.mode === "exec"
							? `${ladoSetupSize(file.size, i18n.language)} · ${t("lado.setup.editor.executable")}`
							: ladoSetupSize(file.size, i18n.language)}
					</span>
				</li>
			))}
		</ul>
	);
}

/** The paths a folder gained, lost or changed between two of its versions. */
function folderChanges(before: LadoSetupContent["files"], after: LadoSetupContent["files"]) {
	const old = new Map(before.map((file) => [file.path, file]));
	const next = new Map(after.map((file) => [file.path, file]));
	return {
		added: after.filter((file) => !old.has(file.path)).map((file) => file.path),
		removed: before.filter((file) => !next.has(file.path)).map((file) => file.path),
		changed: after
			.filter((file) => {
				const was = old.get(file.path);
				return was !== undefined && (was.sha256 !== file.sha256 || was.mode !== file.mode);
			})
			.map((file) => file.path),
	};
}

function ConflictLines({ label, conflicts }: { label: string; conflicts: readonly LadoSetupConflict[] }) {
	const { t } = useTranslation();
	if (conflicts.length === 0) return null;
	return (
		<ul aria-label={label} className="flex flex-col gap-1.5">
			{ladoSetupConflictsBySeverity(conflicts).map((conflict, index) => (
				<li
					key={`${conflict.code}-${conflict.itemId}-${conflict.detail}-${index}`}
					className="flex min-w-0 items-baseline gap-2 text-caption leading-4"
					data-testid="lado-setup-change-conflict"
					data-code={conflict.code}
				>
					<span className={cn("size-1.5 shrink-0 translate-y-[-1px] rounded-full", ladoSetupSeverityDotClass[conflict.severity])} aria-hidden="true" />
					<span className="min-w-0 text-foreground">
						<span className="font-medium">{t(ladoSetupSeverityKeys[conflict.severity])}</span>
						<span className="text-muted-foreground"> · </span>
						{ladoSetupConflictText(t, conflict)}
					</span>
				</li>
			))}
		</ul>
	);
}

function contentText(content: LadoSetupContent | undefined): string {
	return content?.text ?? "";
}

/**
 * What a dry run says about a change: the difference it makes, the conflicts
 * it adds and ends, what LADO could not show, and, when presence names the
 * sentence, that making it asks for the desktop app's confirmation.
 */
export function LadoSetupChangeReview({
	result,
	kind,
	name,
	presence,
}: {
	result: LadoSetupChangeResult;
	kind: string;
	name: string;
	presence: MessageKey;
}) {
	const { t } = useTranslation();
	const before = result.before;
	const after = result.after;
	const folder = (before && ladoSetupFolderContent(kind, before)) || (after && ladoSetupFolderContent(kind, after));
	return (
		<div className="flex min-w-0 flex-col gap-(--size-settings-section-inner-gap)" data-testid="lado-setup-review">
			{folder ? (
				<FolderChanges t={t} before={before?.files ?? []} after={after?.files ?? []} />
			) : (
				<LadoSetupDiff kind={kind} name={name} before={contentText(before)} after={contentText(after)} />
			)}
			<ConflictLines label={t("lado.setup.editor.conflictsAdded")} conflicts={result.conflictsAdded} />
			<ConflictLines label={t("lado.setup.editor.conflictsRemoved")} conflicts={result.conflictsRemoved} />
			{result.warnings.map((code) => (
				<p key={code} className="text-caption leading-4 text-settings-muted">
					{ladoSetupWarningText(t, code)}
				</p>
			))}
			{result.needsPresence ? (
				<p className="text-caption leading-4 text-foreground" data-testid="lado-setup-presence">
					{t(presence)}
				</p>
			) : null}
		</div>
	);
}

function FolderChanges({ t, before, after }: { t: TFunction; before: LadoSetupContent["files"]; after: LadoSetupContent["files"] }) {
	const changes = folderChanges(before, after);
	const lines = [
		{ key: "added", paths: changes.added, label: (files: string) => t("lado.setup.drift.filesAdded", { files }) },
		{ key: "removed", paths: changes.removed, label: (files: string) => t("lado.setup.drift.filesRemoved", { files }) },
		{ key: "changed", paths: changes.changed, label: (files: string) => t("lado.setup.drift.filesChanged", { files }) },
	].filter((line) => line.paths.length > 0);
	if (lines.length === 0) {
		return <p className="text-control leading-4 text-settings-muted">{t("lado.setup.diff.none")}</p>;
	}
	return (
		<div className="flex flex-col gap-1" data-testid="lado-setup-folder-changes">
			{lines.map((line) => (
				<p key={line.key} className="break-words text-control leading-4 text-settings-label">
					{line.label(line.paths.join(", "))}
				</p>
			))}
		</div>
	);
}
