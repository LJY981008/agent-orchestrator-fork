import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { aoBridge } from "./bridge";
import { LadoApiError, type LadoExecution, type LadoVerification } from "./lado";
import {
	applyLadoDBChanges,
	ladoDBChangeAction,
	ladoDBChangeDirty,
	ladoDBChangeDraft,
	ladoDBChangeInput,
	ladoDBChangeLatestRun,
	ladoDBChangeMissingSecrets,
	ladoDBChangeProblemField,
	ladoDBChangeProblems,
	ladoDBChangeSummariesBy,
	ladoDBChangeUrgent,
	ladoExecutionHasDBChanges,
	updateLadoDBChangeSettings,
	type LadoDBChangePR,
	type LadoDBChangeRepoSettings,
	type LadoDBChangeRun,
	type LadoDBChangeSummary,
} from "./lado-db-changes";

const { changeDbChangesMock, applyDbChangesMock } = vi.hoisted(() => ({
	changeDbChangesMock: vi.fn(),
	applyDbChangesMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn(), GET: vi.fn(), PUT: vi.fn() },
	apiErrorCode: (error: unknown) => (error as { code?: string } | undefined)?.code,
	apiErrorDetails: (error: unknown) => (error as { details?: Record<string, unknown> } | undefined)?.details,
	apiErrorMessage: (error: unknown, fallback: string) => (error as { message?: string } | undefined)?.message ?? fallback,
}));

vi.mock("./bridge", () => ({
	aoBridge: { lado: { presence: { changeDbChanges: changeDbChangesMock, applyDbChanges: applyDbChangesMock } } },
}));

const put = apiClient.PUT as unknown as ReturnType<typeof vi.fn>;
const post = apiClient.POST as unknown as ReturnType<typeof vi.fn>;

const repo: LadoDBChangeRepoSettings = {
	repo: "",
	name: "demo",
	enabled: true,
	paths: ["scripts/migrations/*.sql"],
	command: "psql \"$LADO_DB_DSN\" -f {file}",
	timeoutMinutes: 0,
	secretNames: ["LADO_DB_DSN", "PGPASSWORD"],
	storedSecretNames: ["LADO_DB_DSN"],
	sha256: "s".repeat(64),
	baselineCommit: "b".repeat(40),
};

function run(fields: Partial<LadoDBChangeRun>): LadoDBChangeRun {
	return {
		id: "run-1",
		kind: "pre_merge",
		trigger: "approval",
		state: "queued",
		attempts: 0,
		createdAt: "2026-11-10T00:00:00Z",
		updatedAt: "2026-11-10T00:00:00Z",
		headSha: "h".repeat(40),
		prUrl: "https://github.com/o/r/pull/1",
		repo: "",
		sessionId: "demo-3",
		taskId: "lado-task-1",
		...fields,
	};
}

function summary(fields: Partial<LadoDBChangeSummary>): LadoDBChangeSummary {
	return {
		taskId: "lado-task-1",
		sessionId: "demo-3",
		prUrl: "https://github.com/o/r/pull/1",
		repo: "",
		state: "apply_waiting",
		pending: 1,
		failed: 0,
		...fields,
	};
}

const pr: LadoDBChangePR = {
	prUrl: "https://github.com/o/r/pull/1",
	headSha: "h".repeat(40),
	repo: "",
	files: [
		{ path: "scripts/migrations/001_add.sql", blobSha: "a1", phase: "pre_merge", status: "applied", attempts: 1 },
		{ path: "scripts/migrations/002_drop.sql", blobSha: "a2", phase: "after_deploy", status: "pending", attempts: 0 },
		{ path: "scripts/migrations/003_more.sql", blobSha: "a3", phase: "pre_merge", status: "failed", attempts: 1 },
	],
	modified: [],
	runs: [],
};

beforeEach(() => {
	vi.clearAllMocks();
});

describe("the settings draft", () => {
	it("reads the saved settings without any value and with the default time limit", () => {
		const draft = ladoDBChangeDraft(repo);
		expect(draft).toMatchObject({ enabled: true, paths: "scripts/migrations/*.sql", timeoutMinutes: 10 });
		expect(draft.secrets.map((secret) => [secret.name, secret.value])).toEqual([
			["LADO_DB_DSN", ""],
			["PGPASSWORD", ""],
		]);
		expect(ladoDBChangeDirty(repo, draft)).toBe(false);
	});

	it("sends the globs, the trimmed command, the names and only the values typed", () => {
		const draft = ladoDBChangeDraft(repo);
		draft.paths = " db/*.sql \n\n scripts/migrations/*.sql ";
		draft.command = "  psql -f {file}  ";
		draft.secrets[1].value = "s3cret";
		draft.secrets.push({ key: 99, name: "  ", value: "" });
		expect(ladoDBChangeDirty(repo, draft)).toBe(true);
		expect(ladoDBChangeInput(repo, draft)).toEqual({
			repo: "",
			base: repo.sha256,
			enabled: true,
			paths: ["db/*.sql", "scripts/migrations/*.sql"],
			command: "psql -f {file}",
			timeoutMinutes: 10,
			secretNames: ["LADO_DB_DSN", "PGPASSWORD"],
			secrets: { set: { PGPASSWORD: "s3cret" } },
		});
	});

	it("counts a typed value alone as a change, and sends no secrets without one", () => {
		const draft = ladoDBChangeDraft(repo);
		draft.secrets[0].value = "x";
		expect(ladoDBChangeDirty(repo, draft)).toBe(true);
		draft.secrets[0].value = "";
		draft.enabled = false;
		expect(ladoDBChangeInput(repo, draft)).not.toHaveProperty("secrets");
	});

	it("names the secrets without a stored value", () => {
		expect(ladoDBChangeMissingSecrets(repo)).toEqual(["PGPASSWORD"]);
	});
});

describe("refused settings", () => {
	it("reads the problems of LADO_DB_CHANGE_INVALID only", () => {
		const invalid = new LadoApiError("bad", "LADO_DB_CHANGE_INVALID", {
			problems: [{ field: "paths[1]", reason: "parent_dir" }, { field: "command", reason: "no_file_placeholder" }, "junk"],
		});
		expect(ladoDBChangeProblems(invalid)).toEqual([
			{ field: "paths[1]", reason: "parent_dir" },
			{ field: "command", reason: "no_file_placeholder" },
		]);
		expect(ladoDBChangeProblems(new LadoApiError("x", "LADO_DB_CHANGE_SETTINGS_CHANGED"))).toEqual([]);
		expect(ladoDBChangeProblemField("secretNames[0]")).toEqual({ name: "secretNames", index: 0 });
		expect(ladoDBChangeProblemField("command")).toEqual({ name: "command" });
	});
});

describe("saving and applying through the desktop app", () => {
	it("asks main to confirm a settings change and never calls the daemon itself", async () => {
		const saved = { projectId: "demo", sha256: "x", repositories: [repo] };
		changeDbChangesMock.mockResolvedValueOnce({ status: "changed", settings: saved });
		const input = ladoDBChangeInput(repo, { ...ladoDBChangeDraft(repo), secrets: [{ key: 1, name: "LADO_DB_DSN", value: "pw" }] });
		await expect(updateLadoDBChangeSettings("demo", input)).resolves.toBe(saved);
		expect(changeDbChangesMock).toHaveBeenCalledWith({
			projectId: "demo",
			repo: "",
			enabled: true,
			paths: ["scripts/migrations/*.sql"],
			command: repo.command,
			timeoutMinutes: 10,
			secretNames: ["LADO_DB_DSN"],
			secrets: { set: { LADO_DB_DSN: "pw" } },
		});
		expect(put).not.toHaveBeenCalled();
	});

	it("resolves null for a cancel and throws main's refusal", async () => {
		changeDbChangesMock.mockResolvedValueOnce({ status: "cancelled" });
		await expect(updateLadoDBChangeSettings("demo", ladoDBChangeInput(repo, ladoDBChangeDraft(repo)))).resolves.toBeNull();
		changeDbChangesMock.mockResolvedValueOnce({ status: "error", code: "LADO_DB_CHANGE_SETTINGS_CHANGED", message: "changed" });
		await expect(updateLadoDBChangeSettings("demo", ladoDBChangeInput(repo, ladoDBChangeDraft(repo)))).rejects.toMatchObject({
			code: "LADO_DB_CHANGE_SETTINGS_CHANGED",
		});
	});

	it("puts the settings with base when there is no desktop app", async () => {
		changeDbChangesMock.mockResolvedValueOnce(null);
		put.mockResolvedValueOnce({ data: { projectId: "demo", sha256: "y", repositories: [] } });
		await updateLadoDBChangeSettings("demo", ladoDBChangeInput(repo, ladoDBChangeDraft(repo)));
		expect(put).toHaveBeenCalledWith("/api/v1/lado/projects/{projectId}/db-changes/settings", {
			params: { path: { projectId: "demo" } },
			body: expect.objectContaining({ base: repo.sha256, repo: "" }),
		});
	});

	it("asks main to apply a phase, and posts that phase's files in a browser", async () => {
		applyDbChangesMock.mockResolvedValueOnce({ status: "queued", run: run({ state: "queued" }) });
		await expect(applyLadoDBChanges("demo-3", pr, "pre_merge")).resolves.toMatchObject({ state: "queued" });
		expect(applyDbChangesMock).toHaveBeenCalledWith({ sessionId: "demo-3", prUrl: pr.prUrl, phase: "pre_merge" });
		expect(post).not.toHaveBeenCalled();

		applyDbChangesMock.mockResolvedValueOnce(null);
		post.mockResolvedValueOnce({ data: run({}) });
		await applyLadoDBChanges("demo-3", pr, "pre_merge");
		expect(post).toHaveBeenCalledWith("/api/v1/lado/executions/{sessionId}/db-changes/apply", {
			params: { path: { sessionId: "demo-3" } },
			body: {
				prUrl: pr.prUrl,
				headSha: pr.headSha,
				phase: "pre_merge",
				files: [
					{ path: "scripts/migrations/001_add.sql", blobSha: "a1", phase: "pre_merge" },
					{ path: "scripts/migrations/003_more.sql", blobSha: "a3", phase: "pre_merge" },
				],
			},
		});
		expect(aoBridge.lado?.presence?.applyDbChanges).toHaveBeenCalledTimes(2);
	});
});

describe("the board's summaries", () => {
	it("keeps each task's and each session's most urgent state", () => {
		const summaries = [
			summary({ state: "after_deploy_pending", prUrl: "a" }),
			summary({ state: "merged_unapplied", prUrl: "b" }),
			summary({ state: "apply_waiting", prUrl: "c", taskId: "lado-task-2", sessionId: "demo-4" }),
		];
		expect(ladoDBChangeUrgent(summaries)?.state).toBe("merged_unapplied");
		const { byTask, bySession } = ladoDBChangeSummariesBy(summaries);
		expect(byTask.get("lado-task-1")?.prUrl).toBe("b");
		expect(byTask.get("lado-task-2")?.state).toBe("apply_waiting");
		expect(bySession.get("demo-4")?.prUrl).toBe("c");
		expect(ladoDBChangeSummariesBy(undefined).byTask.size).toBe(0);
	});
});

describe("a run's database changes", () => {
	const record = (items: LadoVerification["items"]): LadoVerification => ({ headSha: "h", items }) as unknown as LadoVerification;

	it("shows only when a record's db_changes item names files or changed applied files", () => {
		const withFiles = record([
			{ kind: "db_changes", outcome: "pass", details: { files: [{ path: "a.sql", blobSha: "a", phase: "pre_merge" }] } },
		]);
		const none = record([{ kind: "db_changes", outcome: "pass", code: "no_db_changes", details: {} }]);
		const off = record([{ kind: "paths", outcome: "pass", details: {} }]);
		expect(ladoExecutionHasDBChanges({ verification: withFiles } as LadoExecution)).toBe(true);
		expect(ladoExecutionHasDBChanges({ verification: none } as LadoExecution)).toBe(false);
		expect(ladoExecutionHasDBChanges({ verification: off } as LadoExecution)).toBe(false);
		expect(
			ladoExecutionHasDBChanges({ trackedPrs: [{ record: none }, { record: withFiles }] } as unknown as LadoExecution),
		).toBe(true);
	});

	it("offers the button the newest run of a kind asks for", () => {
		const runs = [
			run({ id: "old", state: "done", createdAt: "2026-11-09T00:00:00Z" }),
			run({ id: "new", state: "failed", createdAt: "2026-11-10T00:00:00Z" }),
			run({ id: "after", kind: "after_deploy", trigger: "after_merge", state: "awaiting_human" }),
		];
		expect(ladoDBChangeLatestRun({ runs }, "pre_merge")?.id).toBe("new");
		expect(ladoDBChangeAction(ladoDBChangeLatestRun({ runs }, "pre_merge"))).toBe("retry");
		expect(ladoDBChangeAction(ladoDBChangeLatestRun({ runs }, "after_deploy"))).toBe("after_deploy");
		expect(ladoDBChangeAction(run({ trigger: "merged_unapplied", state: "awaiting_human" }))).toBe("merged_unapplied");
		expect(ladoDBChangeAction(run({ state: "merge_failed" }))).toBe("retry");
		expect(ladoDBChangeAction(run({ state: "applying" }))).toBeUndefined();
		expect(ladoDBChangeAction(run({ state: "done" }))).toBeUndefined();
		expect(ladoDBChangeAction(undefined)).toBeUndefined();
	});
});
