package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO #22: workspace asset copy policy and readable setup failures.

// ladoApplyWorkspaceAssets resolves the project's workspaceAssets setting
// (defaults included) into the adapter config.
func ladoApplyWorkspaceAssets(cfg *ports.WorkspaceProjectConfig, project domain.ProjectRecord) {
	cfg.AssetExclude, cfg.KeepAbsoluteAssetLinks = project.Config.ResolvedWorkspaceAssets()
}

// ladoWarnSkippedAssetLinks logs the asset links the adapter left out of the
// session workspace.
func (m *Manager) ladoWarnSkippedAssetLinks(id domain.SessionID, info ports.WorkspaceProjectInfo) {
	count := info.SkippedAssetLinkCount
	if count < len(info.SkippedAssetLinks) {
		count = len(info.SkippedAssetLinks)
	}
	if count == 0 || m.logger == nil {
		return
	}
	m.logger.Warn("spawn: skipped workspace asset links",
		"sessionID", id, "links", info.SkippedAssetLinks, "count", count)
}

// ladoProvisionError is the reason a failed asynchronous start records: the
// short setup summary when the workspace adapter gave one, otherwise the
// full error text.
func ladoProvisionError(cause error) string {
	if summary, ok := ports.WorkspaceSetupSummary(cause); ok {
		return summary
	}
	return cause.Error()
}
