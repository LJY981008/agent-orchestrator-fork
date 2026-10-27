// LADO fork: local setup in the app (docs/LJY-PRD/HARNESS.md §9 and §10): the
// Claude Code and Codex setup found on this computer, adopting it into LADO's
// managed folder and restoring it, the managed items as they apply at Global,
// to a project or to a task, their conflicts, and changes to them. Adoption,
// restore, and a change that can run commands or widen access go through the
// desktop app's confirmation: the main process previews them itself and signs
// what the developer confirmed. In a browser they go to the daemon directly,
// which accepts them only in development (AO_LADO_DEV_PRESENCE=1).
import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { components } from "../../api/schema";
import type { LadoPresenceSetupChangeRequest } from "../../shared/lado-presence";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { LadoApiError, ladoEffectiveQueryRoot, ladoError, ladoTasksQueryRoot, ladoTaskViewQueryRoot } from "./lado";

export type LadoSetupInventory = components["schemas"]["LadoSetupInventoryResponse"];
export type LadoSetupEntry = components["schemas"]["LadoSetupEntryResponse"];
export type LadoSetupRoot = components["schemas"]["LadoSetupRootResponse"];
export type LadoSetupDrift = components["schemas"]["LadoSetupDriftResponse"];
export type LadoSetupAdoption = components["schemas"]["LadoSetupAdoptionResponse"];
export type LadoSetupAdoptionDetail = components["schemas"]["LadoSetupAdoptionDetailResponse"];
export type LadoSetupItem = components["schemas"]["LadoSetupItemResponse"];
export type LadoSetupItemState = components["schemas"]["LadoSetupItemStateResponse"];
export type LadoSetupItemDetail = components["schemas"]["LadoSetupItemDetailResponse"];
export type LadoSetupItemVersion = components["schemas"]["LadoSetupItemVersionResponse"];
export type LadoSetupContent = components["schemas"]["LadoSetupContentResponse"];
export type LadoSetupConflict = components["schemas"]["LadoSetupConflictResponse"];
export type LadoSetupAdoptEntry = components["schemas"]["LadoSetupAdoptEntryRequest"];
export type LadoSetupAdoptionPreview = components["schemas"]["LadoSetupAdoptionPreviewResponse"];
export type LadoSetupAdoptResult = components["schemas"]["LadoSetupAdoptResponse"];
export type LadoSetupRestoreResult = components["schemas"]["LadoSetupRestoreResponse"];
export type LadoSetupRestoredEntry = components["schemas"]["LadoSetupRestoredEntryResponse"];
export type LadoSetupChangeResult = components["schemas"]["LadoSetupChangeResponse"];
export type LadoSetupCreateInput = components["schemas"]["CreateLadoSetupItemRequest"];
export type LadoSetupUpdateInput = components["schemas"]["UpdateLadoSetupItemRequest"];
export type LadoSetupBindingInput = components["schemas"]["SetLadoSetupBindingRequest"];
export type LadoSetupDriftInput = components["schemas"]["ResolveLadoSetupDriftRequest"];
export type LadoSetupAcceptInput = components["schemas"]["AcceptLadoSetupConflictRequest"];
export type LadoSetupTool = LadoSetupItem["tool"];
export type LadoSetupKind = LadoSetupItem["kind"];
/** Where setup resolves: Global without ids, a project, or a task and its project. */
export type LadoSetupScope = { projectId?: string; taskId?: string };

/** Every local setup query key starts with this root. */
export const ladoSetupQueryRoot = ["lado", "setup"] as const;
export const ladoSetupInventoryQueryKey = ["lado", "setup", "inventory"] as const;
export const ladoSetupItemsQueryRoot = ["lado", "setup", "items"] as const;
export const ladoSetupItemsQueryKey = (scope: LadoSetupScope = {}) =>
	["lado", "setup", "items", scope.projectId ?? "", scope.taskId ?? ""] as const;
export const ladoSetupConflictsQueryRoot = ["lado", "setup", "conflicts"] as const;
export const ladoSetupConflictsQueryKey = (scope: LadoSetupScope = {}) =>
	["lado", "setup", "conflicts", scope.projectId ?? "", scope.taskId ?? ""] as const;
export const ladoSetupItemQueryKey = (itemId: string) => ["lado", "setup", "item", itemId] as const;
export const ladoSetupAdoptionQueryKey = (adoptionId: string) => ["lado", "setup", "adoption", adoptionId] as const;
export const ladoSetupAdoptionPreviewQueryKey = (entries: readonly LadoSetupAdoptEntry[]) =>
	["lado", "setup", "adoption-preview", entries] as const;

/**
 * Refreshes what a setup change can reach: the setup views, every effective
 * view, and the tasks, whose approvals can need review again.
 */
export function invalidateLadoSetupQueries(queryClient: QueryClient): void {
	for (const queryKey of [ladoSetupQueryRoot, ladoEffectiveQueryRoot, ladoTasksQueryRoot, ladoTaskViewQueryRoot]) {
		void queryClient.invalidateQueries({ queryKey });
	}
}

function scopeQuery(scope: LadoSetupScope): LadoSetupScope {
	return { ...(scope.projectId ? { projectId: scope.projectId } : {}), ...(scope.taskId ? { taskId: scope.taskId } : {}) };
}

/** The setup the daemon found at its last scan, the managed items at Global, drift and adoptions. */
export async function fetchLadoSetupInventory(): Promise<LadoSetupInventory> {
	const { data, error } = await apiClient.GET("/api/v1/lado/setup/inventory");
	if (error) throw ladoError(error, "Could not load the local setup");
	return data;
}

/** Scans the tools' folders and the projects again; the answer is the new inventory. */
export async function scanLadoSetup(): Promise<LadoSetupInventory> {
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/scan");
	if (error) throw ladoError(error, "Could not scan the local setup");
	return data;
}

/** The managed items of both tools as they resolve in the scope, on or off. */
export async function fetchLadoSetupItems(scope: LadoSetupScope = {}): Promise<LadoSetupItemState[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/setup/items", {
		params: { query: scopeQuery(scope) },
	});
	if (error) throw ladoError(error, "Could not load the managed setup");
	return data?.items ?? [];
}

/** The conflicts of the setup that applies in the scope, with the acceptances of warnings. */
export async function fetchLadoSetupConflicts(scope: LadoSetupScope = {}): Promise<LadoSetupConflict[]> {
	const { data, error } = await apiClient.GET("/api/v1/lado/setup/conflicts", {
		params: { query: scopeQuery(scope) },
	});
	if (error) throw ladoError(error, "Could not load the setup conflicts");
	return data?.conflicts ?? [];
}

/** One managed item with the content of every version, oldest first. */
export async function fetchLadoSetupItem(itemId: string): Promise<LadoSetupItemDetail> {
	const { data, error } = await apiClient.GET("/api/v1/lado/setup/items/{itemId}", {
		params: { path: { itemId } },
	});
	if (error) throw ladoError(error, "Could not load the setup item");
	return data;
}

/** One adoption with its items, its backup and its restore's report. */
export async function fetchLadoSetupAdoption(adoptionId: string): Promise<LadoSetupAdoptionDetail> {
	const { data, error } = await apiClient.GET("/api/v1/lado/setup/adoptions/{adoptionId}", {
		params: { path: { adoptionId } },
	});
	if (error) throw ladoError(error, "Could not load the adoption");
	return data;
}

/**
 * What adopting the entries would do: where each goes, the commands it
 * rewrites, the conflicts it brings and what holds it back. It changes
 * nothing and needs no confirmation.
 */
export async function previewLadoSetupAdoption(entries: LadoSetupAdoptEntry[]): Promise<LadoSetupAdoptionPreview> {
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/adoptions", {
		body: { entries, dryRun: true },
	});
	if (error) throw ladoError(error, "Could not preview the adoption");
	return data.preview;
}

/**
 * Adopts the entries. In the desktop app the main process previews them
 * again, asks the developer to confirm and signs that plan itself; null means
 * the developer cancelled. In a browser the call sends planSha256, the hash
 * of the preview the developer saw.
 */
export async function adoptLadoSetup(entries: LadoSetupAdoptEntry[], planSha256: string): Promise<LadoSetupAdoptResult | null> {
	// The bridge answers null when there is no desktop main process to ask.
	const result = (await aoBridge.lado?.presence?.adoptSetup(entries)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.adoption;
	}
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/adoptions", {
		body: { entries, planSha256 },
	});
	if (error) throw ladoError(error, "Could not adopt the local setup");
	return data;
}

/**
 * Puts back what an adoption removed, through the desktop app's confirmation
 * like an adoption; null means the developer cancelled.
 */
export async function restoreLadoSetup(adoptionId: string): Promise<LadoSetupRestoreResult | null> {
	const result = (await aoBridge.lado?.presence?.restoreSetup(adoptionId)) ?? null;
	if (result) {
		if (result.status === "cancelled") return null;
		if (result.status === "error") throw new LadoApiError(result.message, result.code, result.details);
		return result.restore;
	}
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/adoptions/{adoptionId}/restore", {
		params: { path: { adoptionId } },
	});
	if (error) throw ladoError(error, "Could not restore the adoption");
	return data;
}

/** With dryRun each change below only shows what it would do. */
export async function createLadoSetupItem(input: LadoSetupCreateInput): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/items", { body: input });
	if (error) throw ladoError(error, "Could not create the setup item");
	return data;
}

/** Edits a text item from baseVersion, its latest version. */
export async function updateLadoSetupItem(itemId: string, input: LadoSetupUpdateInput): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.PATCH("/api/v1/lado/setup/items/{itemId}", {
		params: { path: { itemId } },
		body: input,
	});
	if (error) throw ladoError(error, "Could not change the setup item");
	return data;
}

/** Deletes a managed item; its versions stay readable. */
export async function deleteLadoSetupItem(itemId: string, dryRun = false): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.DELETE("/api/v1/lado/setup/items/{itemId}", {
		params: { path: { itemId }, query: dryRun ? { dryRun } : {} },
	});
	if (error) throw ladoError(error, "Could not delete the setup item");
	return data;
}

/** Turns an item on or off at Global, or forces or inherits it for a project or task. */
export async function setLadoSetupBinding(input: LadoSetupBindingInput): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.PUT("/api/v1/lado/setup/bindings", { body: input });
	if (error) throw ladoError(error, "Could not change where the setup item applies");
	return data;
}

/** Keeps a managed copy's drift as a new version, or reverts the copy. */
export async function resolveLadoSetupDrift(itemId: string, input: LadoSetupDriftInput): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/items/{itemId}/drift", {
		params: { path: { itemId } },
		body: input,
	});
	if (error) throw ladoError(error, "Could not resolve the drift");
	return data;
}

/** Accepts a conflict warning for the item's current content in a scope. */
export async function acceptLadoSetupConflict(input: LadoSetupAcceptInput): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.POST("/api/v1/lado/setup/conflicts/acceptances", { body: input });
	if (error) throw ladoError(error, "Could not accept the conflict");
	return data;
}

export async function revokeLadoSetupConflictAcceptance(acceptanceId: string, dryRun = false): Promise<LadoSetupChangeResult> {
	const { data, error } = await apiClient.DELETE("/api/v1/lado/setup/conflicts/acceptances/{acceptanceId}", {
		params: { path: { acceptanceId }, query: dryRun ? { dryRun } : {} },
	});
	if (error) throw ladoError(error, "Could not revoke the acceptance");
	return data;
}

/** A change of the managed setup. Each runs first as a dry run, which needs no confirmation. */
export type LadoSetupChangeRequest =
	| { op: "create"; input: Omit<LadoSetupCreateInput, "dryRun"> }
	| { op: "edit"; itemId: string; input: Omit<LadoSetupUpdateInput, "dryRun"> }
	| { op: "delete"; itemId: string }
	| { op: "binding"; input: Omit<LadoSetupBindingInput, "dryRun"> }
	| { op: "drift"; itemId: string; input: Omit<LadoSetupDriftInput, "dryRun"> }
	| { op: "accept"; input: Omit<LadoSetupAcceptInput, "dryRun"> }
	| { op: "revoke"; acceptanceId: string };

export type LadoSetupChangeOutcome =
	| { status: "applied"; result: LadoSetupChangeResult }
	/** The change reaches approved tasks and waits for confirm or cancel; result is its dry run. */
	| { status: "confirm"; result: LadoSetupChangeResult }
	/** The developer cancelled the desktop confirmation; nothing changed. */
	| { status: "cancelled" };

/** A change waiting for the developer, with its dry run: what it reaches. */
export type LadoPendingSetupChange = { request: LadoSetupChangeRequest; result: LadoSetupChangeResult };

function runLadoSetupChange(request: LadoSetupChangeRequest, dryRun: boolean): Promise<LadoSetupChangeResult> {
	const flag = dryRun ? { dryRun } : {};
	switch (request.op) {
		case "create":
			return createLadoSetupItem({ ...request.input, ...flag });
		case "edit":
			return updateLadoSetupItem(request.itemId, { ...request.input, ...flag });
		case "delete":
			return deleteLadoSetupItem(request.itemId, dryRun);
		case "binding":
			return setLadoSetupBinding({ ...request.input, ...flag });
		case "drift":
			return resolveLadoSetupDrift(request.itemId, { ...request.input, ...flag });
		case "accept":
			return acceptLadoSetupConflict({ ...request.input, ...flag });
		case "revoke":
			return revokeLadoSetupConflictAcceptance(request.acceptanceId, dryRun);
	}
}

/**
 * Shows a change without making it: the content before and after, the
 * conflicts it adds or ends, what it reaches, and whether making it needs the
 * desktop app's confirmation.
 */
export function previewLadoSetupChange(request: LadoSetupChangeRequest): Promise<LadoSetupChangeResult> {
	return runLadoSetupChange(request, true);
}

/**
 * The request the desktop app's main process confirms and signs, for the
 * changes that can need it: every change to an item that can run commands or
 * widen access (creating, editing or deleting it, setting where it applies,
 * keeping its drift). Reverting drift and acceptances never do.
 */
export function ladoSetupPresenceRequest(request: LadoSetupChangeRequest): LadoPresenceSetupChangeRequest | null {
	switch (request.op) {
		case "create":
			return { method: "POST", path: "/api/v1/lado/setup/items", body: { ...request.input } };
		case "edit":
			return { method: "PATCH", path: `/api/v1/lado/setup/items/${encodeURIComponent(request.itemId)}`, body: { ...request.input } };
		case "delete":
			return { method: "DELETE", path: `/api/v1/lado/setup/items/${encodeURIComponent(request.itemId)}`, body: {} };
		case "binding":
			return { method: "PUT", path: "/api/v1/lado/setup/bindings", body: { ...request.input } };
		case "drift":
			return {
				method: "POST",
				path: `/api/v1/lado/setup/items/${encodeURIComponent(request.itemId)}/drift`,
				body: { ...request.input },
			};
		default:
			return null;
	}
}

// Makes a change its dry run allowed. One that needs the developer goes to
// the desktop app's main process, which previews and confirms it itself.
async function applyLadoSetupChange(request: LadoSetupChangeRequest, needsPresence: boolean): Promise<LadoSetupChangeOutcome> {
	const presence = needsPresence ? ladoSetupPresenceRequest(request) : null;
	if (presence) {
		// The bridge answers null when there is no desktop main process to ask.
		const answer = (await aoBridge.lado?.presence?.changeSetup(presence)) ?? null;
		if (answer) {
			if (answer.status === "cancelled") return { status: "cancelled" };
			if (answer.status === "error") throw new LadoApiError(answer.message, answer.code, answer.details);
			return { status: "applied", result: answer.change };
		}
	}
	return { status: "applied", result: await runLadoSetupChange(request, false) };
}

/**
 * Runs setup changes. change() asks the daemon first: a change that reaches
 * approved tasks waits in pending until confirm() or cancel(); any other is
 * made at once, through the desktop app's confirmation when the dry run says
 * it needs one. A failed call rejects with the daemon's LadoApiError, which
 * error also holds; the views refresh then too, since a change the desktop
 * app sent can have been made before its answer went missing.
 */
export function useLadoSetupChange() {
	const queryClient = useQueryClient();
	const [pending, setPending] = useState<LadoPendingSetupChange | null>(null);
	const mutation = useMutation({
		mutationFn: async (input: { request: LadoSetupChangeRequest; preview?: LadoSetupChangeResult }): Promise<LadoSetupChangeOutcome> => {
			let preview = input.preview;
			if (!preview) {
				preview = await previewLadoSetupChange(input.request);
				if (preview.impact.tasks.length > 0) return { status: "confirm", result: preview };
			}
			return applyLadoSetupChange(input.request, preview.needsPresence);
		},
		onSuccess: (outcome, input) => {
			if (outcome.status === "confirm") {
				setPending({ request: input.request, result: outcome.result });
				return;
			}
			setPending(null);
			if (outcome.status === "applied") invalidateLadoSetupQueries(queryClient);
		},
		onError: () => invalidateLadoSetupQueries(queryClient),
		retry: false,
	});
	return {
		change: (request: LadoSetupChangeRequest) => mutation.mutateAsync({ request }),
		pending,
		confirm: (): Promise<LadoSetupChangeOutcome> =>
			pending
				? mutation.mutateAsync({ request: pending.request, preview: pending.result })
				: Promise.resolve({ status: "cancelled" }),
		cancel: () => {
			setPending(null);
			mutation.reset();
		},
		isPending: mutation.isPending,
		error: mutation.error,
		reset: mutation.reset,
	};
}
