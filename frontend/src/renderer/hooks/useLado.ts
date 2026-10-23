// LADO fork: query hooks for LADO's project settings, issue-backed tasks, plans,
// approvals and rules. Rule changes that can reach approved tasks go through
// useLadoRuleChange (lib/lado-rules.ts).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	approveLadoPlan,
	cancelLadoTask,
	createLadoTask,
	deleteLadoRule,
	fetchLadoEffective,
	fetchLadoPlan,
	fetchLadoProjectIssues,
	fetchLadoProjectRepositories,
	fetchLadoProjectSettings,
	fetchLadoRule,
	fetchLadoRules,
	fetchLadoTasks,
	fetchLadoTaskView,
	importLadoTasks,
	invalidateLadoRuleQueries,
	ladoEffectiveQueryKey,
	ladoPlanQueryKey,
	ladoPlanQueryRoot,
	ladoProjectIssuesQueryKey,
	ladoProjectRepositoriesQueryKey,
	ladoProjectSettingsQueryKey,
	ladoRuleQueryKey,
	ladoRulesQueryKey,
	ladoTasksQueryKey,
	ladoTaskViewQueryKey,
	requestLadoPlan,
} from "../lib/lado";

/**
 * Whether a project is in LADO mode, and whether that is known yet. Pass no
 * project id for projects LADO never governs (remote hosts, cloud,
 * standalone); they read as off at once.
 */
export function useLadoModeState(projectId: string | undefined): { ladoMode: boolean; known: boolean } {
	const query = useQuery({
		queryKey: ladoProjectSettingsQueryKey(projectId ?? ""),
		queryFn: () => fetchLadoProjectSettings(projectId ?? ""),
		enabled: Boolean(projectId),
		staleTime: 30_000,
		retry: false,
	});
	return { ladoMode: query.data?.ladoMode === true, known: !projectId || query.isFetched };
}

/** Whether a project is in LADO mode; see useLadoModeState. */
export function useLadoMode(projectId: string | undefined): boolean {
	return useLadoModeState(projectId).ladoMode;
}

export function useLadoTasks(projectId: string, enabled = true) {
	return useQuery({
		queryKey: ladoTasksQueryKey(projectId),
		queryFn: () => fetchLadoTasks(projectId),
		enabled: enabled && Boolean(projectId),
		retry: false,
	});
}

export function useLadoProjectIssues(projectId: string, enabled = true) {
	return useQuery({
		queryKey: ladoProjectIssuesQueryKey(projectId),
		queryFn: () => fetchLadoProjectIssues(projectId),
		enabled: enabled && Boolean(projectId),
		refetchOnMount: "always",
		retry: false,
	});
}

/** The repositories a project's issues can live in; more than one in a workspace. */
export function useLadoRepositories(projectId: string, enabled = true) {
	return useQuery({
		queryKey: ladoProjectRepositoriesQueryKey(projectId),
		queryFn: () => fetchLadoProjectRepositories(projectId),
		enabled: enabled && Boolean(projectId),
		staleTime: 30_000,
		retry: false,
	});
}

function useInvalidateLadoTasks(projectId: string) {
	const queryClient = useQueryClient();
	return () => {
		void queryClient.invalidateQueries({ queryKey: ladoTasksQueryKey(projectId) });
		void queryClient.invalidateQueries({ queryKey: ladoProjectIssuesQueryKey(projectId) });
	};
}

export function useCreateLadoTask(projectId: string) {
	const invalidate = useInvalidateLadoTasks(projectId);
	return useMutation({
		mutationFn: (input: { repository?: string; text: string; agent?: string; model?: string }) =>
			createLadoTask({ projectId, ...input }),
		onSuccess: invalidate,
		retry: false,
	});
}

export function useCancelLadoTask(projectId: string) {
	const invalidate = useInvalidateLadoTasks(projectId);
	return useMutation({
		mutationFn: (taskId: string) => cancelLadoTask(taskId),
		onSuccess: invalidate,
		retry: false,
	});
}

export function useImportLadoTasks(projectId: string) {
	const invalidate = useInvalidateLadoTasks(projectId);
	return useMutation({
		mutationFn: (issueRefs: string[]) => importLadoTasks(projectId, issueRefs),
		onSuccess: invalidate,
		retry: false,
	});
}

/** The task view. It reads the issue again, so it refetches when it opens. */
export function useLadoTaskView(taskId: string | null) {
	return useQuery({
		queryKey: ladoTaskViewQueryKey(taskId ?? ""),
		queryFn: () => fetchLadoTaskView(taskId ?? ""),
		enabled: Boolean(taskId),
		refetchOnMount: "always",
		retry: false,
	});
}

/** One pinned plan revision with its content. */
export function useLadoPlan(revisionId: string | null) {
	return useQuery({
		queryKey: ladoPlanQueryKey(revisionId ?? ""),
		queryFn: () => fetchLadoPlan(revisionId ?? ""),
		enabled: Boolean(revisionId),
		retry: false,
	});
}

function useInvalidateLadoTask(projectId: string, taskId: string) {
	const queryClient = useQueryClient();
	return () => {
		void queryClient.invalidateQueries({ queryKey: ladoTasksQueryKey(projectId) });
		void queryClient.invalidateQueries({ queryKey: ladoTaskViewQueryKey(taskId) });
		void queryClient.invalidateQueries({ queryKey: ladoPlanQueryRoot });
	};
}

export function useRequestLadoPlan(projectId: string, taskId: string) {
	const invalidate = useInvalidateLadoTask(projectId, taskId);
	return useMutation({
		mutationFn: (input: { sourceCommentId?: string; note?: string }) => requestLadoPlan(taskId, input),
		onSuccess: invalidate,
		retry: false,
	});
}

/**
 * Resolves to null when the developer cancelled the desktop confirmation.
 * policySha256 is the effective rules' snapshotSha256; the desktop app reads
 * and shows the rules itself, so only a browser needs it.
 */
export function useApproveLadoPlan(projectId: string, taskId: string) {
	const invalidate = useInvalidateLadoTask(projectId, taskId);
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (input: { revisionId: string; sha256: string; policySha256?: string }) =>
			approveLadoPlan(input.revisionId, input.sha256, input.policySha256 ?? ""),
		onSuccess: (approval) => {
			if (!approval) return;
			invalidate();
			// The task view compares the approved rules with the rules now.
			void queryClient.invalidateQueries({ queryKey: ladoEffectiveQueryKey(projectId, taskId) });
		},
		retry: false,
	});
}

/** Every rule, oldest first; archived ones only when asked. */
export function useLadoRules(includeArchived = false, enabled = true) {
	return useQuery({
		queryKey: ladoRulesQueryKey(includeArchived),
		queryFn: () => fetchLadoRules(includeArchived),
		enabled,
		retry: false,
	});
}

/** One rule with its version history. */
export function useLadoRule(ruleId: string | null) {
	return useQuery({
		queryKey: ladoRuleQueryKey(ruleId ?? ""),
		queryFn: () => fetchLadoRule(ruleId ?? ""),
		enabled: Boolean(ruleId),
		retry: false,
	});
}

/**
 * The rules that apply to a project, or to one of its tasks when taskId is
 * given. The task view refetches it when it opens: an AO project setting can
 * change what applies without a rule change.
 */
export function useLadoEffective(projectId: string, taskId?: string | null, enabled = true) {
	return useQuery({
		queryKey: ladoEffectiveQueryKey(projectId, taskId ?? ""),
		queryFn: () => fetchLadoEffective(projectId, taskId ?? undefined),
		enabled: enabled && Boolean(projectId),
		refetchOnMount: "always",
		retry: false,
	});
}

/** Deletes a user rule that no approval used, so no approval needs review because of it. */
export function useDeleteLadoRule() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (ruleId: string) => deleteLadoRule(ruleId),
		onSuccess: () => invalidateLadoRuleQueries(queryClient),
		retry: false,
	});
}
