// LADO fork: the desktop notification of a new LADO alert (issue #42). An
// alert's INSERT event shows it once, with the id lado-alert:<alertId>. The
// ids shown are kept in a ring in localStorage, which every window of the app
// shares, so a replayed event, the seen UPDATE, a reload or a second window
// shows nothing again. A click on one opens the alert's task view on its
// Usage tab; AO's own notification listener ignores ids it does not know, so
// LADO listens for its prefix on its own (main.ts and NotificationCenter stay
// as upstream has them).
import { appI18n } from "../i18n/instance";
import { aoBridge } from "./bridge";
import { fetchLadoAlerts, type LadoAlert } from "./lado";
import { ladoAlertText } from "./lado-usage";

export const LADO_ALERT_NOTIFICATION_PREFIX = "lado-alert:";
/** The localStorage key of the ring of shown alerts. */
export const LADO_ALERT_RING_KEY = "ao.lado.shownAlerts";
/** How many shown alerts the ring remembers. */
export const LADO_ALERT_RING_SIZE = 512;
/**
 * An alert raised longer ago than this when its event arrives does not show:
 * a daemon from before the stream cursor replays its whole log on connect.
 */
export const LADO_ALERT_FRESH_MS = 15 * 60_000;

/** A new alert as its INSERT event names it. */
export type LadoAlertEvent = { alertId: number; projectId: string; taskId: string };

/** Where a shown alert leads; the ring keeps it so a click after a reload still opens its task. */
type ShownAlert = { id: number; projectId: string; taskId: string };

function storage(): Storage | undefined {
	try {
		return typeof window === "undefined" ? undefined : window.localStorage;
	} catch {
		return undefined;
	}
}

function readRing(): ShownAlert[] {
	const raw = storage()?.getItem(LADO_ALERT_RING_KEY);
	if (!raw) return [];
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!Array.isArray(parsed)) return [];
		return parsed.filter(
			(entry): entry is ShownAlert =>
				typeof entry === "object" &&
				entry !== null &&
				typeof (entry as ShownAlert).id === "number" &&
				typeof (entry as ShownAlert).projectId === "string" &&
				typeof (entry as ShownAlert).taskId === "string",
		);
	} catch {
		return [];
	}
}

function writeRing(entries: readonly ShownAlert[]): void {
	try {
		storage()?.setItem(LADO_ALERT_RING_KEY, JSON.stringify(entries.slice(-LADO_ALERT_RING_SIZE)));
	} catch {
		// A full or blocked storage shows the alert in this window anyway.
	}
}

/** Whether this or another window already showed the alert. */
export function ladoAlertShown(alertId: number): boolean {
	return readRing().some((entry) => entry.id === alertId);
}

/**
 * Records the alert as shown and says whether this call was first. The ring
 * is read again right before the write, so of two windows the later one sees
 * the earlier one's entry.
 */
export function claimLadoAlert(entry: ShownAlert): boolean {
	const ring = readRing();
	if (ring.some((shown) => shown.id === entry.id)) return false;
	writeRing([...ring, entry]);
	return true;
}

/** "github:acme/demo#42" without its provider. */
function issueText(issueRef: string): string {
	return issueRef.replace(/^[a-z]+:/, "");
}

type NotifyDeps = {
	fetchAlerts: (projectId: string, taskId: string) => Promise<LadoAlert[]>;
	show: (notification: { id: string; title: string; body?: string; type?: string }) => Promise<void> | void;
	now: () => number;
};

const defaultDeps: NotifyDeps = {
	fetchAlerts: (projectId, taskId) => fetchLadoAlerts(projectId, { taskId }, 50),
	show: (notification) => aoBridge.notifications.show(notification),
	now: () => Date.now(),
};

/**
 * Shows each new alert once across windows. The events of one task read its
 * alerts once; an alert already shown, already seen, gone or too old shows
 * nothing.
 */
export async function showLadoAlertNotifications(events: readonly LadoAlertEvent[], deps: NotifyDeps = defaultDeps): Promise<void> {
	const byTask = new Map<string, { projectId: string; taskId: string; ids: Set<number> }>();
	for (const event of events) {
		if (ladoAlertShown(event.alertId)) continue;
		const key = `${event.projectId}\0${event.taskId}`;
		const group = byTask.get(key) ?? { projectId: event.projectId, taskId: event.taskId, ids: new Set<number>() };
		group.ids.add(event.alertId);
		byTask.set(key, group);
	}
	for (const group of byTask.values()) {
		let alerts: LadoAlert[];
		try {
			alerts = await deps.fetchAlerts(group.projectId, group.taskId);
		} catch {
			continue;
		}
		// Oldest first, so the newest alert's toast lands on top.
		for (const alert of [...alerts].reverse()) {
			if (!group.ids.has(alert.id) || alert.seenAt) continue;
			if (deps.now() - Date.parse(alert.createdAt) > LADO_ALERT_FRESH_MS) continue;
			if (!claimLadoAlert({ id: alert.id, projectId: alert.projectId, taskId: alert.taskId })) continue;
			const locale = appI18n.language;
			const issue = issueText(alert.issueRef);
			void deps.show({
				id: alert.notificationId || `${LADO_ALERT_NOTIFICATION_PREFIX}${alert.id}`,
				title: issue ? appI18n.t("lado.alert.notificationTitle", { issue }) : appI18n.t("lado.alert.notificationTitleNoIssue"),
				body: ladoAlertText(appI18n.t, alert, locale),
				type: "lado_alert",
			});
		}
	}
}

/** The task a click asks to open, until the board of its project takes it. */
export type LadoTaskOpenRequest = { projectId: string; taskId: string; tab: "usage" };

let openRequest: LadoTaskOpenRequest | null = null;
const openListeners = new Set<() => void>();

export function requestLadoTaskOpen(request: LadoTaskOpenRequest): void {
	openRequest = request;
	for (const listener of openListeners) listener();
}

/** Takes the request if it is for the project's board. */
export function takeLadoTaskOpenRequest(projectId: string): LadoTaskOpenRequest | null {
	if (!openRequest || openRequest.projectId !== projectId) return null;
	const request = openRequest;
	openRequest = null;
	return request;
}

export function subscribeLadoTaskOpenRequests(listener: () => void): () => void {
	openListeners.add(listener);
	return () => {
		openListeners.delete(listener);
	};
}

/** Shows the project's board, where the task view opens. */
function showProjectBoard(projectId: string): void {
	const hash = `#/projects/${encodeURIComponent(projectId)}`;
	if (window.location.hash !== hash) window.location.hash = hash;
}

/**
 * Listens for clicks on LADO's notifications and opens the alert's task on
 * its Usage tab. Other ids are AO's own and left to its listener.
 */
export function listenLadoAlertClicks(navigate: (projectId: string) => void = showProjectBoard): () => void {
	return aoBridge.notifications.onClick((id) => {
		if (!id.startsWith(LADO_ALERT_NOTIFICATION_PREFIX)) return;
		const alertId = Number(id.slice(LADO_ALERT_NOTIFICATION_PREFIX.length));
		const shown = readRing().find((entry) => entry.id === alertId);
		if (!shown) return;
		requestLadoTaskOpen({ projectId: shown.projectId, taskId: shown.taskId, tab: "usage" });
		navigate(shown.projectId);
	});
}
