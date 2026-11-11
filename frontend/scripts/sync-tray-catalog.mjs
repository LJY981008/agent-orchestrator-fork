#!/usr/bin/env node
// Regenerates src/main/tray-catalog.generated.ts from the renderer locale JSON.
//
// The tray (Electron main) needs ~9 strings. Importing the renderer catalogs
// put all 8 locale files (1.3 MB, 58% of main.js) on the main process's startup
// path. tray-catalog.test.ts fails when this file drifts from the JSON; run
// `node scripts/sync-tray-catalog.mjs` from frontend/ to fix it.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const frontend = join(dirname(fileURLToPath(import.meta.url)), "..");
const LOCALES = ["en", "zh-CN", "ja", "ko", "es", "fr", "de", "pt-BR"];
const KEYS = [
	"tray.untitledSession",
	"tray.attentionTooltip_one",
	"tray.attentionTooltip_other",
	"tray.empty",
	"tray.more",
	"tray.show",
	"tray.quit",
	"zone.merge",
	"zone.action",
	// LADO fork: the plan approval, permission ceiling and LADO mode off
	// confirmations (src/main/lado-presence.ts).
	"lado.presence.approveTitle",
	"lado.presence.approveDetail",
	"lado.presence.approve",
	"lado.presence.cancel",
	"lado.presence.approveRules",
	"lado.presence.approveRulesBlocking",
	"lado.presence.ruleChangeTitle",
	"lado.presence.ceilingChangeDetail",
	"lado.presence.change",
	"lado.presence.mode.acceptEdits",
	"lado.presence.mode.auto",
	"lado.presence.mode.bypassPermissions",
	"lado.presence.modeOffTitle",
	"lado.presence.modeOffDetail",
	"lado.presence.turnOff",
	// LADO fork: adopting, restoring and changing local setup, and the setup
	// item kinds the change confirmation names.
	"lado.presence.setupAdoptTitle",
	"lado.presence.setupAdoptDetail",
	"lado.presence.setupAdoptBlocking",
	"lado.presence.setupAdopt",
	"lado.presence.setupRestoreTitle",
	"lado.presence.setupRestoreDetail",
	"lado.presence.setupRestore",
	"lado.presence.setupChangeTitle",
	"lado.presence.setupChangeDetail",
	"lado.presence.setupChangeCreate",
	"lado.presence.setupChangeEdit",
	"lado.presence.setupChangeOnGlobal",
	"lado.presence.setupChangeOnProject",
	"lado.presence.setupChangeOnTask",
	"lado.presence.setupChangeKeepDrift",
	"lado.presence.setupChangeDelete",
	"lado.presence.setupChangeOffGlobal",
	"lado.presence.setupChangeOffProject",
	"lado.presence.setupChangeOffTask",
	"lado.presence.setupChangeInheritProjectOn",
	"lado.presence.setupChangeInheritProjectOff",
	"lado.presence.setupChangeInheritTaskOn",
	"lado.presence.setupChangeInheritTaskOff",
	"lado.presence.setupChangeContent",
	"lado.presence.setupChangeBlocking",
	"lado.setup.kind.instructions",
	"lado.setup.kind.setting",
	"lado.setup.kind.skill",
	"lado.setup.kind.agent",
	"lado.setup.kind.command",
	"lado.setup.kind.outputStyle",
	"lado.setup.kind.hookScript",
	"lado.setup.kind.mcpServer",
	// LADO fork: approving a run's integration, changing a project's check
	// commands and changing the protected paths (step 8).
	"lado.presence.integration.title",
	"lado.presence.integration.task",
	"lado.presence.integration.head",
	"lado.presence.integration.passed",
	"lado.presence.integration.outsidePlan",
	"lado.presence.integration.commitsUnclear",
	"lado.presence.integration.prTitle",
	"lado.presence.integration.baseMoved",
	"lado.presence.integration.reflogOff",
	"lado.presence.integration.alreadyOnBase",
	"lado.presence.integration.baseDeleted",
	"lado.presence.integration.baseAdvanced",
	"lado.presence.integration.baseNotDefault",
	"lado.presence.integration.unprotected",
	"lado.presence.integration.protectionUnknown",
	"lado.presence.integration.unprotectedGitlab",
	"lado.presence.integration.anySource",
	"lado.protection.part.pullRequest",
	"lado.protection.part.integrationCheck",
	"lado.protection.part.integrationCheckSource",
	"lado.protection.part.nonFastForward",
	"lado.protection.part.deletion",
	// LADO fork: a GitLab base branch's protection (#21).
	"lado.protection.part.protectedBranch",
	"lado.protection.part.pushAccess",
	"lado.protection.part.forcePush",
	"lado.protection.part.pipelineMustSucceed",
	"lado.protection.part.skippedPipeline",
	"lado.protection.part.integrationStatus",
	"lado.presence.integration.reviewInconclusive",
	"lado.presence.integration.noTests",
	"lado.presence.integration.otherCode",
	"lado.presence.integration.nextStatus",
	"lado.presence.integration.nextUnprotected",
	"lado.presence.integration.approve",
	// LADO fork: approving a workspace task's pull requests together (#64).
	"lado.presence.integration.titleMany",
	"lado.presence.integration.prLine",
	"lado.presence.integration.approvedAlready",
	"lado.presence.integration.nextStatusMany",
	"lado.presence.integration.nextUnprotectedMany",
	"lado.presence.checks.title",
	"lado.presence.checks.detail",
	"lado.presence.checks.added",
	"lado.presence.checks.changed",
	"lado.presence.checks.removed",
	"lado.presence.checks.order",
	"lado.presence.paths.title",
	"lado.presence.paths.detail",
	"lado.presence.paths.added",
	"lado.presence.paths.removed",
	// LADO fork: turning Claude in Chrome on (issue #40).
	"lado.presence.claudeChromeTitle",
	"lado.presence.claudeChromeChatSessions",
	"lado.presence.claudeChromeLadoWorkers",
	"lado.presence.claudeChromeDetail",
	"lado.presence.claudeChromeTurnOn",
	// LADO fork: changing a repository's production database change settings,
	// applying a pull request's SQL by hand, and the SQL lines of the
	// integration approval (#97).
	"lado.presence.dbChanges.title",
	"lado.presence.dbChanges.turnOn",
	"lado.presence.dbChanges.turnOff",
	"lado.presence.dbChanges.paths",
	"lado.presence.dbChanges.command",
	"lado.presence.dbChanges.timeout",
	"lado.presence.dbChanges.secretNamesAdded",
	"lado.presence.dbChanges.secretNamesRemoved",
	"lado.presence.dbChanges.secretsSet",
	"lado.presence.dbChanges.secretsRemoved",
	"lado.presence.dbChanges.baseline",
	"lado.presence.dbChanges.detail",
	"lado.presence.dbApply.titleApproval",
	"lado.presence.dbApply.titleMerged",
	"lado.presence.dbApply.titleAfterDeploy",
	"lado.presence.dbApply.pr",
	"lado.presence.dbApply.repo",
	"lado.presence.dbApply.fileRuns",
	"lado.presence.dbApply.fileApplied",
	"lado.presence.dbApply.fileFailed",
	"lado.presence.dbApply.fileInterrupted",
	"lado.presence.dbApply.nextApproval",
	"lado.presence.dbApply.nextMergeOnly",
	"lado.presence.dbApply.nextMerged",
	"lado.presence.dbApply.nextAfterDeploy",
	"lado.presence.dbApply.interruptedWarning",
	"lado.presence.dbApply.apply",
	"lado.presence.integration.dbFilePreMerge",
	"lado.presence.integration.dbFileAfterDeploy",
	"lado.presence.integration.dbMore",
	"lado.presence.integration.dbAppliedModified",
	"lado.presence.integration.dbUnplanned",
	"lado.presence.integration.nextDbChanges",
	"lado.presence.integration.nextDbChangesMany",
	"lado.presence.integration.nextAfterDeploy",
];

const catalogs = {};
for (const locale of LOCALES) {
	const messages = JSON.parse(readFileSync(join(frontend, "src/renderer/i18n", `${locale}.json`), "utf8"));
	catalogs[locale] = {};
	for (const key of KEYS) {
		if (typeof messages[key] !== "string") throw new Error(`${locale}.json is missing "${key}"`);
		catalogs[locale][key] = messages[key];
	}
}

const body = `// Generated by scripts/sync-tray-catalog.mjs from src/renderer/i18n/*.json. Do not edit.
import type { AppLocale } from "../shared/ui-locale";

export const TRAY_MESSAGE_KEYS = ${JSON.stringify(KEYS, null, "\t").replace(/\n/g, "\n")} as const;

export type TrayMessageKey = (typeof TRAY_MESSAGE_KEYS)[number];

export const trayCatalogs: Record<AppLocale, Record<TrayMessageKey, string>> = ${JSON.stringify(catalogs, null, "\t")};
`;
writeFileSync(join(frontend, "src/main/tray-catalog.generated.ts"), body);
console.log("wrote src/main/tray-catalog.generated.ts");
