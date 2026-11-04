// LADO fork: the orchestrator's project memo (issue #50). The orchestrator
// writes it before /clear and a new conversation reads it; a person reads,
// edits and restores it in Project settings → Orchestrator memo.
import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type ProjectMemoView = components["schemas"]["ProjectMemoResponse"];
export type ProjectMemo = components["schemas"]["ProjectMemoPayload"];
export type ProjectMemoRevisionSummary = components["schemas"]["ProjectMemoRevisionSummary"];

/** Prefix of every memo query, for invalidating them all after a /clear. */
export const projectMemoQueryRoot = ["project-orchestrator-memo"] as const;

export function projectMemoQueryKey(projectId: string) {
	return [...projectMemoQueryRoot, projectId] as const;
}

export function projectMemoRevisionQueryKey(projectId: string, revision: string) {
	return [...projectMemoQueryRoot, projectId, "revision", revision] as const;
}

/**
 * The memo's length in the unit the daemon's limit counts: Unicode code
 * points, as Go counts runes.
 */
export function projectMemoChars(text: string): number {
	return Array.from(text).length;
}

export async function fetchProjectMemo(projectId: string): Promise<ProjectMemoView> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/orchestrator-memo", {
		params: { path: { id: projectId } },
	});
	if (error) throw error;
	return data;
}

export async function fetchProjectMemoRevision(projectId: string, revision: string): Promise<ProjectMemo> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/orchestrator-memo/revisions/{revisionId}", {
		params: { path: { id: projectId, revisionId: revision } },
	});
	if (error) throw error;
	return data.memo;
}

/**
 * Makes body the memo. baseRevision is the revision the editor read ("" when
 * there was none); the daemon answers 409 PROJECT_MEMO_CONFLICT when another
 * writer (a /clear) got there first. Errors are the daemon's envelope, so
 * apiErrorCode reads them.
 */
export async function saveProjectMemo(projectId: string, body: string, baseRevision: string): Promise<ProjectMemoView> {
	const { data, error } = await apiClient.PUT("/api/v1/projects/{id}/orchestrator-memo", {
		params: { path: { id: projectId } },
		body: { body, baseRevision },
	});
	if (error) throw error;
	return data;
}

/** The project's memo with its kept earlier revisions. */
export function useProjectMemo(projectId: string, enabled = true) {
	return useQuery({
		queryKey: projectMemoQueryKey(projectId),
		queryFn: () => fetchProjectMemo(projectId),
		enabled: enabled && Boolean(projectId),
		refetchOnMount: "always",
		retry: false,
	});
}

/** One kept revision with its body, read when the person opens it. */
export function useProjectMemoRevision(projectId: string, revision: string | null) {
	return useQuery({
		queryKey: projectMemoRevisionQueryKey(projectId, revision ?? ""),
		queryFn: () => fetchProjectMemoRevision(projectId, revision ?? ""),
		enabled: Boolean(projectId && revision),
		// A revision never changes once written.
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
}

function storeMemoView(queryClient: QueryClient, projectId: string, view: ProjectMemoView) {
	queryClient.setQueryData(projectMemoQueryKey(projectId), view);
}

/** Saves a person's edit. */
export function useSaveProjectMemo(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: ({ body, baseRevision }: { body: string; baseRevision: string }) =>
			saveProjectMemo(projectId, body, baseRevision),
		onSuccess: (view) => storeMemoView(queryClient, projectId, view),
		retry: false,
	});
}

/**
 * Restores a kept revision: reads its body and saves it as a person's edit on
 * top of the current memo, which then becomes a kept revision itself.
 */
export function useRestoreProjectMemo(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ revision, baseRevision }: { revision: string; baseRevision: string }) => {
			const memo = await queryClient.fetchQuery({
				queryKey: projectMemoRevisionQueryKey(projectId, revision),
				queryFn: () => fetchProjectMemoRevision(projectId, revision),
				staleTime: Number.POSITIVE_INFINITY,
			});
			return saveProjectMemo(projectId, memo.body, baseRevision);
		},
		onSuccess: (view) => storeMemoView(queryClient, projectId, view),
		retry: false,
	});
}

/** Reads the memo again, after /clear wrote it or a save hit a conflict. */
export function invalidateProjectMemo(queryClient: QueryClient, projectId: string) {
	return queryClient.invalidateQueries({ queryKey: projectMemoQueryKey(projectId), exact: true });
}
