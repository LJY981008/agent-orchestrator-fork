package project_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// LADO #22: the settings path used by PUT /api/v1/projects/{id} validates and
// stores workspaceAssets.

func TestUpdateSettingsWorkspaceAssets(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	bad := []*domain.WorkspaceAssetsConfig{
		{Exclude: []string{"/abs"}},
		{Exclude: []string{"../up"}},
		{Exclude: []string{"["}},
		{AbsoluteLinks: "follow"},
	}
	for _, assets := range bad {
		_, err := m.UpdateSettings(ctx, "ao", project.UpdateSettingsInput{
			DisplayName: "AO",
			Config:      domain.ProjectConfig{WorkspaceAssets: assets},
		})
		wantCode(t, err, "INVALID_PROJECT_CONFIG")
	}
	got, err := m.Get(ctx, "ao")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project.Config != nil && got.Project.Config.WorkspaceAssets != nil {
		t.Fatalf("rejected workspaceAssets persisted: %#v", got.Project.Config.WorkspaceAssets)
	}

	for _, want := range []*domain.WorkspaceAssetsConfig{
		{Exclude: []string{".venv", "tools/cache"}, AbsoluteLinks: domain.WorkspaceAssetLinksKeep},
		{Exclude: []string{}},
		{AbsoluteLinks: domain.WorkspaceAssetLinksSkip},
	} {
		if _, err := m.UpdateSettings(ctx, "ao", project.UpdateSettingsInput{
			DisplayName: "AO",
			Config:      domain.ProjectConfig{WorkspaceAssets: want},
		}); err != nil {
			t.Fatalf("UpdateSettings(%#v): %v", want, err)
		}
		got, err := m.Get(ctx, "ao")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Project.Config == nil || !reflect.DeepEqual(got.Project.Config.WorkspaceAssets, want) {
			t.Fatalf("stored workspaceAssets = %#v, want %#v", got.Project.Config, want)
		}
	}
}
