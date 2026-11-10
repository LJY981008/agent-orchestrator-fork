import { describe, expect, it } from "vitest";
import {
	ladoLogCISuggestedRepository,
	ladoLogEndpointPort,
	ladoLogIncidentShown,
	ladoLogIncidentsByTask,
	ladoLogSourceFromCI,
	ladoLogTargetNeedsRepository,
	ladoLogTargetRepository,
	ladoLogWatchDraft,
	ladoLogWatchInput,
	ladoLogWatchSameInput,
	type LadoLogCIFile,
	type LadoLogFingerprint,
	type LadoLogWatch,
} from "./lado-log-watch";

const saved: LadoLogWatch = {
	projectId: "demo",
	enabled: true,
	ladoMode: true,
	repository: "",
	include: ["OutOfMemory"],
	exclude: [],
	hourlyIssueCap: 5,
	autoPlan: true,
	autoPlanDailyCap: 5,
	autoPlanMinCount: 1,
	disconnectAlertMinutes: 10,
	sources: [
		{
			id: "src-1",
			kind: "docker_tcp",
			displayName: "prod1",
			endpoint: "tcp://192.0.2.10:2375",
			tls: false,
			paused: false,
			status: { state: "watching", insecure: true },
			tlsStored: { ca: false, cert: false, key: false },
			targets: [
				{ id: "tgt-1", kind: "compose_project", value: "grp", paused: false, status: { instances: 2 } },
				{ id: "tgt-2", kind: "compose_service", value: "api", composeProject: "grp", service: "billing", paused: true, status: { instances: 1 } },
				{ id: "tgt-3", kind: "name", value: "web", repository: "gitlab:grp/front", paused: false, status: { instances: 1 } },
			],
		},
	],
};

function fingerprint(over: Partial<LadoLogFingerprint>): LadoLogFingerprint {
	return {
		id: 1,
		projectId: "demo",
		fingerprint: "abc",
		service: "grp/api",
		exceptionKind: "java.lang.NullPointerException",
		count: 1,
		firstSeenAt: "2026-11-10T00:00:00Z",
		lastSeenAt: "2026-11-10T00:01:00Z",
		ignored: false,
		instances: [],
		serviceInstances: 0,
		...over,
	};
}

describe("the log watch draft", () => {
	it("round-trips saved settings into the same input", () => {
		const draft = ladoLogWatchDraft(saved);
		expect(draft.sources[0].key).toBe("src-1");
		expect(ladoLogWatchInput(draft)).toEqual({
			enabled: true,
			include: ["OutOfMemory"],
			exclude: [],
			hourlyIssueCap: 5,
			autoPlan: true,
			autoPlanDailyCap: 5,
			autoPlanMinCount: 1,
			disconnectAlertMinutes: 10,
			sources: [
				{
					id: "src-1",
					kind: "docker_tcp",
					displayName: "prod1",
					endpoint: "tcp://192.0.2.10:2375",
					tls: false,
					paused: false,
					targets: [
						{ id: "tgt-1", kind: "compose_project", value: "grp", paused: false },
						{ id: "tgt-2", kind: "compose_service", value: "api", composeProject: "grp", service: "billing", paused: true },
						{ id: "tgt-3", kind: "name", value: "web", repository: "gitlab:grp/front", paused: false },
					],
				},
			],
		});
		expect(ladoLogWatchSameInput(draft, ladoLogWatchDraft(saved))).toBe(true);
	});

	it("trims text, drops blank pattern lines and the compose project of other kinds", () => {
		const draft = ladoLogWatchDraft(saved);
		draft.include = " a \n\n b\n";
		draft.sources[0].displayName = " prod1 ";
		draft.sources[0].targets[0].composeProject = "ignored";
		const input = ladoLogWatchInput(draft);
		expect(input.include).toEqual(["a", "b"]);
		expect(input.sources[0].displayName).toBe("prod1");
		expect(input.sources[0].targets[0]).not.toHaveProperty("composeProject");
		expect(ladoLogWatchSameInput(draft, ladoLogWatchDraft(saved))).toBe(false);
	});

	it("keeps a target's repository, sends none when it uses the project's, and compares it", () => {
		const draft = ladoLogWatchDraft(saved);
		expect(draft.sources[0].targets.map((target) => target.repository)).toEqual(["", "", "gitlab:grp/front"]);
		draft.sources[0].targets[2].repository = "gitlab:grp/back";
		expect(ladoLogWatchInput(draft).sources[0].targets[2]).toMatchObject({ repository: "gitlab:grp/back" });
		expect(ladoLogWatchSameInput(draft, ladoLogWatchDraft(saved))).toBe(false);
		draft.sources[0].targets[2].repository = "";
		expect(ladoLogWatchInput(draft).sources[0].targets[2]).not.toHaveProperty("repository");
	});
});

describe("a target's repository", () => {
	const back = "gitlab:grp/back";
	const target = { repository: "", paused: false };
	const source = { paused: false };

	it("is its own, else the project's", () => {
		expect(ladoLogTargetRepository({ repository: back }, { repository: "" })).toBe(back);
		expect(ladoLogTargetRepository({ repository: back }, { repository: "gitlab:grp/front" })).toBe("gitlab:grp/front");
		expect(ladoLogTargetRepository({ repository: "" }, { repository: "" })).toBe("");
	});

	it("is needed as the daemon checks it: two repositories, on, nothing paused, none set", () => {
		const on = { enabled: true, repository: "" };
		expect(ladoLogTargetNeedsRepository(on, source, target, 2)).toBe(true);
		// One repository: the daemon picks it.
		expect(ladoLogTargetNeedsRepository(on, source, target, 1)).toBe(false);
		// Off or paused: no issue can come, so the save goes through.
		expect(ladoLogTargetNeedsRepository({ ...on, enabled: false }, source, target, 2)).toBe(false);
		expect(ladoLogTargetNeedsRepository(on, { paused: true }, target, 2)).toBe(false);
		expect(ladoLogTargetNeedsRepository(on, source, { ...target, paused: true }, 2)).toBe(false);
		// Set on the target or inherited from the project.
		expect(ladoLogTargetNeedsRepository(on, source, { ...target, repository: back }, 2)).toBe(false);
		expect(ladoLogTargetNeedsRepository({ ...on, repository: back }, source, target, 2)).toBe(false);
	});
});

describe("CI candidates", () => {
	it("become an unsaved source with a compose project target per pick", () => {
		const source = ladoLogSourceFromCI({ variable: "PROD1_HOST", displayName: "prod1", endpoint: "tcp://192.0.2.10:2375" }, ["grp"]);
		expect(source.id).toBeUndefined();
		expect(source.tls).toBe(false);
		expect(source.targets).toEqual([expect.objectContaining({ kind: "compose_project", value: "grp" })]);
		expect(source.targets[0].id).toBeUndefined();
	});

	it("turn TLS on for the TLS port and keep an unresolved endpoint empty", () => {
		expect(ladoLogSourceFromCI({ displayName: "prod2", endpoint: "tcp://prod2.example.test:2376" }, []).tls).toBe(true);
		const unresolved = ladoLogSourceFromCI({ displayName: "secret", endpoint: "", unresolved: "$SECRET_HOST" }, []);
		expect(unresolved.endpoint).toBe("");
		expect(unresolved.tls).toBe(false);
	});

	const refs = ["gitlab:grp/back", "gitlab:grp/front"];
	const ciFile = (fields: Partial<LadoLogCIFile>): LadoLogCIFile => ({
		repo: "back",
		path: ".gitlab-ci.yml",
		repository: "gitlab:grp/back",
		hosts: [],
		composeProjects: ["back", "front"],
		notes: [],
		...fields,
	});

	it("suggest the file's repository for the compose project named like its folder only", () => {
		const file = ciFile({});
		expect(ladoLogCISuggestedRepository(file, "back", refs)).toBe("gitlab:grp/back");
		expect(ladoLogCISuggestedRepository(file, "front", refs)).toBe("");
		// The name must match exactly.
		expect(ladoLogCISuggestedRepository(file, "Back", refs)).toBe("");
		const source = ladoLogSourceFromCI({ displayName: "prod1", endpoint: "tcp://192.0.2.10:2376" }, ["back", "front"], file, refs);
		expect(source.targets.map((target) => [target.value, target.repository])).toEqual([
			["back", "gitlab:grp/back"],
			["front", ""],
		]);
	});

	it("suggest the file's repository for the one compose project it deploys", () => {
		const file = ciFile({ repo: "deploy", repository: "gitlab:grp/front", composeProjects: ["shop"] });
		expect(ladoLogCISuggestedRepository(file, "shop", refs)).toBe("gitlab:grp/front");
		expect(ladoLogSourceFromCI({ displayName: "prod1", endpoint: "" }, ["shop"], file, refs).targets[0].repository).toBe("gitlab:grp/front");
	});

	it("suggest nothing for a root file, a repository the project does not offer, or no list", () => {
		expect(ladoLogCISuggestedRepository(ciFile({ repo: "", repository: undefined, composeProjects: ["grp"] }), "grp", refs)).toBe("");
		expect(ladoLogCISuggestedRepository(ciFile({ repository: "gitlab:grp/other" }), "back", refs)).toBe("");
		expect(ladoLogCISuggestedRepository(ciFile({}), "back", [])).toBe("");
		expect(ladoLogSourceFromCI({ displayName: "prod1", endpoint: "" }, ["back"]).targets[0].repository).toBe("");
	});

	it("read the endpoint's port", () => {
		expect(ladoLogEndpointPort("tcp://198.51.100.4:2376")).toBe(2376);
		expect(ladoLogEndpointPort("tcp://[2001:db8::1]:2375/")).toBe(2375);
		expect(ladoLogEndpointPort("tcp://example.test")).toBeUndefined();
	});
});

describe("the board's incident mark", () => {
	it("shows for an open log task only", () => {
		expect(ladoLogIncidentShown({ source: "log", state: "draft" })).toBe(true);
		expect(ladoLogIncidentShown({ source: "log", state: "planned" })).toBe(true);
		expect(ladoLogIncidentShown({ source: "log", state: "running" })).toBe(true);
		expect(ladoLogIncidentShown({ source: "log", state: "completed" })).toBe(false);
		expect(ladoLogIncidentShown({ source: "log", state: "cancelled" })).toBe(false);
		expect(ladoLogIncidentShown({ source: "log", state: "closed_unmerged" })).toBe(false);
		expect(ladoLogIncidentShown({ state: "draft" })).toBe(false);
		expect(ladoLogIncidentShown({ source: "log", state: "draft", issue: { state: "open" } })).toBe(true);
		expect(ladoLogIncidentShown({ source: "log", state: "draft", issue: { state: "done" } })).toBe(false);
		expect(ladoLogIncidentShown({ source: "log", state: "planned", issue: { state: "cancelled" } })).toBe(false);
	});

	it("finds each task's fingerprint, the more frequent one when two name it", () => {
		const byTask = ladoLogIncidentsByTask([
			fingerprint({ id: 1, taskId: "t1", count: 3 }),
			fingerprint({ id: 2, taskId: "t1", count: 9 }),
			fingerprint({ id: 3, taskId: "t2", count: 1 }),
			fingerprint({ id: 4, count: 7 }),
		]);
		expect(byTask.get("t1")?.id).toBe(2);
		expect(byTask.get("t2")?.id).toBe(3);
		expect(byTask.size).toBe(2);
	});
});
