// LADO fork: query hooks for LADO's project settings, issue-backed tasks, plans,
// approvals, rules, runs, their verification and integration, branch
// protection and the dispatch settings. Rule changes
// that can reach approved tasks go through useLadoRuleChange (lib/lado-rules.ts).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	approveLadoIntegration,
	approveLadoPlan,
	cancelLadoTask,
	changeLadoProtectedPaths,
	createLadoTask,
	deleteLadoRule,
	fetchLadoCheckLog,
	fetchLadoDispatchSettings,
	fetchLadoEffective,
	fetchLadoExecution,
	fetchLadoPlan,
	fetchLadoProjectIssues,
	fetchLadoProjectRepositories,
	fetchLadoProjectSettings,
	fetchLadoProtection,
	fetchLadoRule,
	fetchLadoRules,
	fetchLadoTasks,
	fetchLadoTaskView,
	importLadoTasks,
	invalidateLadoRuleQueries,
	ladoCheckLogQueryKey,
	ladoDispatchSettingsQueryKey,
	ladoEffectiveQueryKey,
	ladoExecutionQueryKey,
	ladoExecutionQueryRoot,
	ladoPlanQueryKey,
	ladoPlanQueryRoot,
	ladoProjectIssuesQueryKey,
	ladoProjectRepositoriesQueryKey,
	ladoProjectSettingsQueryKey,
	ladoProtectionQueryKey,
	ladoRuleQueryKey,
	ladoRulesQueryKey,
	ladoTasksQueryKey,
	ladoTaskViewQueryKey,
	ladoTaskViewQueryRoot,
	mergeLadoPR,
	requestLadoPlan,
	runLadoChecks,
	setLadoMode,
	updateLadoCheckTimeout,
	updateLadoDispatchSettings,
	updateLadoProjectChecks,
	updateLadoProjectSettings,
	type LadoCheckCommand,
	type LadoDispatchSettingsInput,
} from "../lib/lado";

/** A project's LADO settings: its mode and how many of its executions run at once. */
export function useLadoProjectSettings(projectId: string | undefined) {
	return useQuery({
		queryKey: ladoProjectSettingsQueryKey(projectId ?? ""),
		queryFn: () => fetchLadoProjectSettings(projectId ?? ""),
		enabled: Boolean(projectId),
		staleTime: 30_000,
		retry: false,
	});
}

/**
 * Whether a project is in LADO mode, and whether that is known yet. Pass no
 * project id for projects LADO never governs (remote hosts, cloud,
 * standalone); they read as off at once.
 */
export function useLadoModeState(projectId: string | undefined): { ladoMode: boolean; known: boolean } {
	const query = useLadoProjectSettings(projectId);
	return { ladoMode: query.data?.ladoMode === true, known: !projectId || query.isFetched };
}

/** Whether a project is in LADO mode; see useLadoModeState. */
export function useLadoMode(projectId: string | undefined): boolean {
	return useLadoModeState(projectId).ladoMode;
}

/**
 * Turns the project's LADO mode on or off. Resolves to null when the
 * developer cancelled the desktop confirmation of turning it off; nothing
 * changed then.
 */
export function useSetLadoMode(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (on: boolean) => setLadoMode(projectId, on),
		onSuccess: (settings) => {
			if (!settings) return;
			queryClient.setQueryData(ladoProjectSettingsQueryKey(projectId), settings);
			// Queued work waits while the mode is off, and goes on when it is on again.
			void queryClient.invalidateQueries({ queryKey: ladoTasksQueryKey(projectId) });
		},
		retry: false,
	});
}

/** Changes how many executions run at once in the project. */
export function useUpdateLadoProjectSettings(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (input: { maxConcurrent: number }) => updateLadoProjectSettings(projectId, input),
		onSuccess: (settings) => queryClient.setQueryData(ladoProjectSettingsQueryKey(projectId), settings),
		retry: false,
	});
}

/**
 * Replaces the project's check commands. Resolves to null when the developer
 * cancelled the desktop confirmation; nothing changed then.
 */
export function useUpdateLadoProjectChecks(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (commands: LadoCheckCommand[]) => updateLadoProjectChecks(projectId, commands),
		onSuccess: (settings) => {
			if (!settings) return;
			queryClient.setQueryData(ladoProjectSettingsQueryKey(projectId), settings);
			// Runs check their current heads with the new commands.
			void queryClient.invalidateQueries({ queryKey: ladoExecutionQueryRoot });
			void queryClient.invalidateQueries({ queryKey: ladoTasksQueryKey(projectId) });
		},
		retry: false,
	});
}

/** Changes how long each of the project's check commands may run, in minutes. */
export function useUpdateLadoCheckTimeout(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (minutes: number) => updateLadoCheckTimeout(projectId, minutes),
		onSuccess: (settings) => queryClient.setQueryData(ladoProjectSettingsQueryKey(projectId), settings),
		retry: false,
	});
}

/**
 * Whether each of the project's repositories protects its base branch as
 * LADO needs. Nothing announces a change on the host, so it reads again when
 * stale; useRefreshLadoProtection asks the host at once.
 */
export function useLadoProtection(projectId: string | undefined, enabled = true) {
	return useQuery({
		queryKey: ladoProtectionQueryKey(projectId ?? ""),
		queryFn: () => fetchLadoProtection(projectId ?? ""),
		enabled: enabled && Boolean(projectId),
		staleTime: 60_000,
		retry: false,
	});
}

/** Check again: the daemon reads the branch rules from the host instead of its cache. */
export function useRefreshLadoProtection(projectId: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: () => fetchLadoProtection(projectId, true),
		onSuccess: (protection) => {
			queryClient.setQueryData(ladoProtectionQueryKey(projectId), protection);
			// The protection item of every run's verification reads the same rules.
			void queryClient.invalidateQueries({ queryKey: ladoExecutionQueryRoot });
		},
		retry: false,
	});
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

/** The execution a worker session runs; null for a session LADO did not start. */
export function useLadoExecution(sessionId: string | null | undefined, enabled = true) {
	return useQuery({
		queryKey: ladoExecutionQueryKey(sessionId ?? ""),
		queryFn: () => fetchLadoExecution(sessionId ?? ""),
		enabled: enabled && Boolean(sessionId),
		retry: false,
	});
}

/** A check command run's output, read when its log opens; a finished run's output does not change. */
export function useLadoCheckLog(sessionId: string, checkRunId: string | null | undefined, enabled = true) {
	return useQuery({
		queryKey: ladoCheckLogQueryKey(sessionId, checkRunId ?? ""),
		queryFn: () => fetchLadoCheckLog(sessionId, checkRunId ?? ""),
		enabled: enabled && Boolean(sessionId && checkRunId),
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
}

// What an integration approval, a merge or a new check run moves: the run's
// panel, its task's lanes and the open task view.
function useInvalidateLadoRun(projectId: string, sessionId: string) {
	const queryClient = useQueryClient();
	return () => {
		void queryClient.invalidateQueries({ queryKey: ladoExecutionQueryKey(sessionId) });
		void queryClient.invalidateQueries({ queryKey: ladoTasksQueryKey(projectId) });
		void queryClient.invalidateQueries({ queryKey: ladoTaskViewQueryRoot });
	};
}

/**
 * Approves the integration of the run's current pull request head. Resolves
 * to null when the developer cancelled the desktop confirmation.
 */
export function useApproveLadoIntegration(projectId: string, sessionId: string) {
	const invalidate = useInvalidateLadoRun(projectId, sessionId);
	return useMutation({
		mutationFn: () => approveLadoIntegration(sessionId),
		onSuccess: (result) => {
			if (result) invalidate();
		},
		retry: false,
	});
}

/** Runs the project's check commands again for the run's current head. */
export function useRunLadoChecks(projectId: string, sessionId: string) {
	const invalidate = useInvalidateLadoRun(projectId, sessionId);
	return useMutation({
		mutationFn: () => runLadoChecks(sessionId),
		onSuccess: invalidate,
		retry: false,
	});
}

/** Merges the run's pull request at its approved head through AO's merge route. */
export function useMergeLadoPR(projectId: string, sessionId: string) {
	const invalidate = useInvalidateLadoRun(projectId, sessionId);
	return useMutation({
		mutationFn: (pr: { number: number; url: string; headSha: string }) => mergeLadoPR(pr),
		onSuccess: invalidate,
		retry: false,
	});
}

/**
 * Replaces the protected paths' globs. Resolves to null when the developer
 * cancelled the desktop confirmation. A change reaches every approval's
 * rules, like the ceiling's.
 */
export function useChangeLadoProtectedPaths() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (globs: string[]) => changeLadoProtectedPaths(globs),
		onSuccess: (rule) => {
			if (rule) invalidateLadoRuleQueries(queryClient);
		},
		retry: false,
	});
}

/** The cap on running executions, their maximum run time and the memory hold. */
export function useLadoDispatchSettings(enabled = true) {
	return useQuery({
		queryKey: ladoDispatchSettingsQueryKey,
		queryFn: fetchLadoDispatchSettings,
		enabled,
		retry: false,
	});
}

/** Saves all three dispatch settings together. */
export function useUpdateLadoDispatchSettings() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (input: LadoDispatchSettingsInput) => updateLadoDispatchSettings(input),
		onSuccess: (settings) => queryClient.setQueryData(ladoDispatchSettingsQueryKey, settings),
		retry: false,
	});
}
