// LADO fork: how Settings → Local setup reads the daemon's inventory
// (docs/LJY-PRD/HARNESS.md §9): the tools and kinds in the order the page
// shows them, an entry's state in one line, paths under the folder a
// developer knows, and what an adoption can take.
import type { TFunction } from "i18next";
import type { MessageKey } from "../../../i18n/messages";
import { ladoSetupReasonText } from "../../../lib/lado-errors";
import type {
	LadoSetupConflict,
	LadoSetupContent,
	LadoSetupEntry,
	LadoSetupInventory,
	LadoSetupItemState,
	LadoSetupKind,
	LadoSetupRestoredEntry,
	LadoSetupTool,
} from "../../../lib/lado-setup";

/** The tools whose setup LADO manages, in the order the page shows them. */
export const ladoSetupTools: readonly LadoSetupTool[] = ["claude-code", "codex"];

/** The kinds of setup items in the order the page groups them. */
export const ladoSetupKinds: readonly LadoSetupKind[] = [
	"instructions",
	"setting",
	"skill",
	"agent",
	"command",
	"output_style",
	"hook_script",
	"mcp_server",
];

const groupKeys: Record<LadoSetupKind, MessageKey> = {
	instructions: "lado.setup.group.instructions",
	setting: "lado.setup.group.settings",
	skill: "lado.setup.group.skills",
	agent: "lado.setup.group.agents",
	command: "lado.setup.group.commands",
	output_style: "lado.setup.group.outputStyles",
	hook_script: "lado.setup.group.hookScripts",
	mcp_server: "lado.setup.group.mcpServers",
};

/** The kind an entry is grouped under; an entry that is no item, such as a folder LADO cannot take, has none. */
export function ladoSetupGroupKind(kind: string): LadoSetupKind | "" {
	return (ladoSetupKinds as readonly string[]).includes(kind) ? (kind as LadoSetupKind) : "";
}

/** A kind group's heading. */
export function ladoSetupGroupLabel(t: TFunction, kind: LadoSetupKind | ""): string {
	return kind ? t(groupKeys[kind]) : t("lado.setup.group.other");
}

/** An item's identity within its tool and kind, as an adoption names it. */
export function ladoSetupEntryKey(entry: { tool: string; kind: string; name: string }): string {
	// Tools and kinds hold no slash, so a nested command name cannot collide.
	return `${entry.tool}/${entry.kind}/${entry.name}`;
}

// The states a tool's card lists; what LADO leaves to the tool or the
// repository waits in its collapsed group.
const shownStatuses: ReadonlySet<string> = new Set(["managed", "adoptable", "not_adoptable"]);

/** What a tool's card lists: managed items and what LADO can or cannot adopt. */
export function ladoSetupShownEntries(inventory: LadoSetupInventory, tool: LadoSetupTool): LadoSetupEntry[] {
	return inventory.entries.filter((entry) => entry.tool === tool && shownStatuses.has(entry.status));
}

/** What LADO never adopts or leaves to the tool, and the files projects' repositories track. */
export function ladoSetupOtherEntries(inventory: LadoSetupInventory, tool: LadoSetupTool): LadoSetupEntry[] {
	return inventory.entries.filter((entry) => entry.tool === tool && !shownStatuses.has(entry.status));
}

/** The names LADO manages now. */
export function ladoSetupManagedKeys(inventory: LadoSetupInventory): Set<string> {
	return new Set(inventory.entries.filter((entry) => entry.location === "managed").map(ladoSetupEntryKey));
}

/**
 * What an adoption can take: adoptable entries in the tools' own folders
 * whose name no managed item holds (the daemon refuses those as already
 * managed), in the order the page lists them.
 */
export function ladoSetupAdoptable(inventory: LadoSetupInventory): LadoSetupEntry[] {
	const managed = ladoSetupManagedKeys(inventory);
	return inventory.entries
		.filter((entry) => entry.status === "adoptable" && entry.location !== "managed" && entry.location !== "project")
		.filter((entry) => !managed.has(ladoSetupEntryKey(entry)))
		.sort(compareEntries);
}

/** Tool, then kind in the page's order, then name. */
export function compareEntries(a: LadoSetupEntry, b: LadoSetupEntry): number {
	const rank = (entry: LadoSetupEntry) => {
		const kind = ladoSetupGroupKind(entry.kind);
		return kind ? ladoSetupKinds.indexOf(kind) : ladoSetupKinds.length;
	};
	return (
		ladoSetupTools.indexOf(a.tool) - ladoSetupTools.indexOf(b.tool) ||
		rank(a) - rank(b) ||
		a.name.localeCompare(b.name) ||
		Number(b.location === "managed") - Number(a.location === "managed")
	);
}

/**
 * An entry's state in one line, with the reason LADO leaves it where it is.
 * A managed item says whether it is on at Global; an entry another managed
 * item already holds the name of cannot be adopted as it is.
 */
export function ladoSetupStatusText(
	t: TFunction,
	entry: LadoSetupEntry,
	context: { managed: Set<string>; globalOff: Set<string> },
): string {
	const why = (status: MessageKey, reason: string) => {
		const text = ladoSetupReasonText(t, reason);
		return text ? `${t(status)} · ${text}` : t(status);
	};
	switch (entry.status) {
		case "managed":
			return t(context.globalOff.has(entry.itemId) ? "lado.setup.status.managedOff" : "lado.setup.status.managedOn");
		case "adoptable":
			return entry.location !== "managed" && context.managed.has(ladoSetupEntryKey(entry))
				? why("lado.setup.status.notAdoptable", "already_managed")
				: t("lado.setup.status.adoptable");
		case "not_adoptable":
			return why("lado.setup.status.notAdoptable", entry.reason);
		case "never":
			return why("lado.setup.status.never", entry.reason);
		case "not_managed":
			return why("lado.setup.status.notManaged", entry.reason);
		case "repo_tracked":
			return t("lado.setup.status.repoTracked");
	}
}

const separator = /[\\/]/;

function under(path: string, base: string): string | null {
	if (!base) return null;
	if (path === base) return "";
	const next = path.charAt(base.length);
	return path.startsWith(base) && (separator.test(next) || next === "#") ? path.slice(base.length) : null;
}

function baseName(path: string): string {
	return path.split(separator).filter(Boolean).at(-1) ?? path;
}

/**
 * A path as the page shows it: under the name of the tool folder or the
 * project it is in (".claude/skills/review", "demo/CLAUDE.md"), or from
 * LADO's own folder for a managed item. The full path goes in a tooltip.
 */
export function ladoSetupShortPath(path: string, inventory: Pick<LadoSetupInventory, "roots" | "projects">): string {
	for (const root of inventory.roots) {
		const rest = under(path, root.path);
		if (rest !== null) return baseName(root.path) + rest;
	}
	for (const project of inventory.projects) {
		const rest = under(path, project.path);
		if (rest !== null) return project.name + rest;
	}
	const managed = /[\\/](lado[\\/].*)$/.exec(path);
	if (managed) return managed[1];
	return path.split(separator).filter(Boolean).slice(-2).join("/") || path;
}

/**
 * A path the daemon gives under one of the tool roots, as in a restore
 * report or a stopped adoption, short and in full. Claude's global config is
 * a file, so its root is its folder.
 */
export function ladoSetupRootedPath(
	entry: { root: LadoSetupRestoredEntry["root"] },
	relative: string,
	inventory: Pick<LadoSetupInventory, "roots" | "projects">,
): { short: string; full: string } {
	const root = inventory.roots.find((candidate) => candidate.location === entry.root);
	if (!root?.path) return { short: relative, full: relative };
	const folder = entry.root === "claude_json" ? root.path.replace(/[\\/][^\\/]*$/, "") : root.path;
	const windows = folder.includes("\\") && !folder.includes("/");
	const full = `${folder}${windows ? "\\" : "/"}${windows ? relative.replaceAll("/", "\\") : relative}`;
	return { short: ladoSetupShortPath(full, inventory), full };
}

// The operating systems by the name Go gives them.
const osNames: Partial<Record<string, string>> = { linux: "Linux", darwin: "macOS", windows: "Windows" };

export function ladoSetupOSLabel(os: string): string {
	return osNames[os] ?? os;
}

type Severity = LadoSetupConflict["severity"];

const severityRank: Record<Severity, number> = { blocking: 0, warning: 1, info: 2 };

// Blocking takes the needs-you color, a warning the caution color; each also
// says its severity in words.
export const ladoSetupSeverityDotClass: Record<Severity, string> = {
	blocking: "bg-[var(--color-status-needs-you)]",
	warning: "bg-[var(--color-status-in-review)]",
	info: "bg-[var(--color-status-idle)]",
};

export const ladoSetupSeverityKeys: Record<Severity, MessageKey> = {
	blocking: "lado.policy.severity.blocking",
	warning: "lado.policy.severity.warning",
	info: "lado.policy.severity.info",
};

/** Blocking first, then warnings, then notes; the daemon's order within each. */
export function ladoSetupConflictsBySeverity(conflicts: readonly LadoSetupConflict[]): LadoSetupConflict[] {
	return conflicts
		.map((conflict, index) => ({ conflict, index }))
		.sort((a, b) => severityRank[a.conflict.severity] - severityRank[b.conflict.severity] || a.index - b.index)
		.map(({ conflict }) => conflict);
}

/** The kinds the app can create for a tool; hook scripts come only from an adoption. */
export function ladoSetupCreatableKinds(tool: LadoSetupTool): LadoSetupKind[] {
	return tool === "codex" ? ["instructions", "mcp_server"] : ladoSetupKinds.filter((kind) => kind !== "hook_script");
}

/** A settings key and an MCP server hold a JSON value; the other kinds hold a file's text. */
export function ladoSetupJSONKind(kind: string): boolean {
	return kind === "setting" || kind === "mcp_server";
}

/** The kinds whose text is Markdown; a hook script is a program. */
export function ladoSetupMarkdownKind(kind: string): boolean {
	return kind !== "hook_script" && !ladoSetupJSONKind(kind);
}

/**
 * A folder item: a skill with more than its SKILL.md, or a hook folder. Its
 * files are changed in the managed folder, not in the app.
 */
export function ladoSetupFolderContent(kind: string, content: Pick<LadoSetupContent, "contentKind" | "files">): boolean {
	if (content.contentKind !== "tree") return false;
	return !(kind === "skill" && content.files.length === 1 && content.files[0]?.path === "SKILL.md");
}

/** Tool, then kind in the page's order, then name. */
export function compareItems(a: Pick<LadoSetupItemState, "tool" | "kind" | "name">, b: Pick<LadoSetupItemState, "tool" | "kind" | "name">): number {
	return (
		ladoSetupTools.indexOf(a.tool) - ladoSetupTools.indexOf(b.tool) ||
		ladoSetupKinds.indexOf(a.kind) - ladoSetupKinds.indexOf(b.kind) ||
		a.name.localeCompare(b.name)
	);
}

/** A size in the developer's language: "820 bytes", "1.2 kB", "3 MB". */
export function ladoSetupSize(bytes: number, locale: string): string {
	const [value, unit] =
		bytes >= 1024 * 1024 ? [bytes / (1024 * 1024), "megabyte"] : bytes >= 1024 ? [bytes / 1024, "kilobyte"] : [bytes, "byte"];
	// Short byte units read as "820 byte" in English, so bytes are spelled out.
	const unitDisplay = unit === "byte" ? "long" : "short";
	return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay, maximumFractionDigits: 1 }).format(value);
}
