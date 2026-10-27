// LADO fork: query hooks for local setup: the inventory and its scan, the
// managed items and conflicts of a scope, one item or adoption, and adopting
// and restoring through the desktop app's confirmation. Changes to managed
// items go through useLadoSetupChange (lib/lado-setup.ts).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ladoEffectiveQueryRoot } from "../lib/lado";
import {
	adoptLadoSetup,
	fetchLadoSetupAdoption,
	fetchLadoSetupConflicts,
	fetchLadoSetupInventory,
	fetchLadoSetupItem,
	fetchLadoSetupItems,
	invalidateLadoSetupQueries,
	ladoSetupAdoptionPreviewQueryKey,
	ladoSetupAdoptionQueryKey,
	ladoSetupConflictsQueryKey,
	ladoSetupConflictsQueryRoot,
	ladoSetupInventoryQueryKey,
	ladoSetupItemQueryKey,
	ladoSetupItemsQueryKey,
	ladoSetupItemsQueryRoot,
	previewLadoSetupAdoption,
	restoreLadoSetup,
	scanLadoSetup,
	type LadoSetupAdoptEntry,
	type LadoSetupScope,
} from "../lib/lado-setup";

/** The setup the daemon found at its last scan, with the managed items at Global, drift and adoptions. */
export function useLadoSetupInventory(enabled = true) {
	return useQuery({
		queryKey: ladoSetupInventoryQueryKey,
		queryFn: fetchLadoSetupInventory,
		enabled,
		retry: false,
	});
}

/**
 * Scans again; the inventory shows the new scan, and the conflicts, the
 * managed items and the effective views, which read the same folders, load
 * again.
 */
export function useScanLadoSetup() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: scanLadoSetup,
		onSuccess: (inventory) => {
			queryClient.setQueryData(ladoSetupInventoryQueryKey, inventory);
			for (const queryKey of [ladoSetupConflictsQueryRoot, ladoSetupItemsQueryRoot, ladoEffectiveQueryRoot]) {
				void queryClient.invalidateQueries({ queryKey });
			}
		},
		retry: false,
	});
}

/** The managed items of both tools in a scope; Global without ids. */
export function useLadoSetupItems(scope: LadoSetupScope = {}, enabled = true) {
	return useQuery({
		queryKey: ladoSetupItemsQueryKey(scope),
		queryFn: () => fetchLadoSetupItems(scope),
		enabled,
		retry: false,
	});
}

/** The conflicts of the setup that applies in a scope; Global without ids. */
export function useLadoSetupConflicts(scope: LadoSetupScope = {}, enabled = true) {
	return useQuery({
		queryKey: ladoSetupConflictsQueryKey(scope),
		queryFn: () => fetchLadoSetupConflicts(scope),
		enabled,
		retry: false,
	});
}

/** One managed item with its version history. */
export function useLadoSetupItem(itemId: string | null) {
	return useQuery({
		queryKey: ladoSetupItemQueryKey(itemId ?? ""),
		queryFn: () => fetchLadoSetupItem(itemId ?? ""),
		enabled: Boolean(itemId),
		retry: false,
	});
}

/** One adoption with its items, backup and restore report. */
export function useLadoSetupAdoption(adoptionId: string | null) {
	return useQuery({
		queryKey: ladoSetupAdoptionQueryKey(adoptionId ?? ""),
		queryFn: () => fetchLadoSetupAdoption(adoptionId ?? ""),
		enabled: Boolean(adoptionId),
		retry: false,
	});
}

/**
 * What adopting the entries would do. The daemon scans again for each
 * preview, so it does not refetch when the window regains focus.
 */
export function useLadoSetupAdoptionPreview(entries: LadoSetupAdoptEntry[], enabled = true) {
	return useQuery({
		queryKey: ladoSetupAdoptionPreviewQueryKey(entries),
		queryFn: () => previewLadoSetupAdoption(entries),
		enabled: enabled && entries.length > 0,
		refetchOnWindowFocus: false,
		retry: false,
	});
}

/**
 * Adopts the previewed entries. Resolves to null when the developer cancelled
 * the desktop confirmation. A failed adoption can still have moved some
 * setup (a partial one), so the views refresh either way.
 */
export function useAdoptLadoSetup() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (input: { entries: LadoSetupAdoptEntry[]; planSha256: string }) => adoptLadoSetup(input.entries, input.planSha256),
		onSettled: (result, error) => {
			if (result || error) invalidateLadoSetupQueries(queryClient);
		},
		retry: false,
	});
}

/**
 * Restores an adoption. Resolves to null when the developer cancelled the
 * desktop confirmation; a failed restore can have put back some originals.
 */
export function useRestoreLadoSetup() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (adoptionId: string) => restoreLadoSetup(adoptionId),
		onSettled: (result, error) => {
			if (result || error) invalidateLadoSetupQueries(queryClient);
		},
		retry: false,
	});
}
