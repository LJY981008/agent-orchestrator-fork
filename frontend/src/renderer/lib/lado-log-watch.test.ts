import { describe, expect, it } from "vitest";
import {
	ladoLogEndpointPort,
	ladoLogIncidentShown,
	ladoLogIncidentsByTask,
	ladoLogSourceFromCI,
	ladoLogWatchDraft,
	ladoLogWatchInput,
	ladoLogWatchSameInput,
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
