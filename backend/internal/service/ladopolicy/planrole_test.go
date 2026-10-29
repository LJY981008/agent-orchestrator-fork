package ladopolicy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The plan worker roles' rules reach plan workers only: no resolution, no
// snapshot and no worker prompt holds them, they take no binding and are not
// archived, and editing their text changes no approval's rules.
func TestPlanRoleRulesStayOutOfResolution(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	view, err := fx.svc.Effective(ctx, EffectiveInput{ProjectID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range view.Entries {
		if entry.Rule.ID == domain.LadoRulePlanDesigner || entry.Rule.ID == domain.LadoRulePlanCritic {
			t.Fatalf("a plan role's rule entered the resolution: %+v", entry.Rule.ID)
		}
	}
	for _, rule := range view.Content.Rules {
		if rule.RuleID == domain.LadoRulePlanDesigner || rule.RuleID == domain.LadoRulePlanCritic {
			t.Fatalf("a plan role's rule entered the snapshot: %+v", rule)
		}
	}
	if strings.Contains(view.Prompt, "계획 검수") || strings.Contains(view.Prompt, "adversarially") {
		t.Fatalf("a plan role's text reached the worker prompt: %q", view.Prompt)
	}
	before := view.SHA256
	edited, err := fx.svc.Update(ctx, domain.LadoRulePlanCritic, UpdateInput{BaseVersion: 1, Instruction: ptr("Find what breaks the plan.")})
	if err != nil || !edited.Applied || edited.Rule.Rule.Version.Number != 2 {
		t.Fatalf("edit the critic's text = (%+v, %v)", edited, err)
	}
	if after, err := fx.svc.Effective(ctx, EffectiveInput{ProjectID: "demo"}); err != nil || after.SHA256 != before {
		t.Fatalf("the rules hash moved from %s to %s (%v) after editing a plan role's text", before, after.SHA256, err)
	}
	for name, change := range map[string]func() error{
		"binding": func() error {
			_, err := fx.svc.SetBinding(ctx, BindingInput{RuleID: domain.LadoRulePlanDesigner, Scope: domain.LadoScopeGlobal, State: domain.LadoBindingOff})
			return err
		},
		"archive": func() error {
			archived := true
			_, err := fx.svc.Update(ctx, domain.LadoRulePlanDesigner, UpdateInput{BaseVersion: 1, Archived: &archived})
			return err
		},
	} {
		var apiErr *apierr.Error
		if err := change(); !errors.As(err, &apiErr) || apiErr.Code != "LADO_RULE_PLAN_ROLE" {
			t.Fatalf("%s of a plan role's rule = %v, want LADO_RULE_PLAN_ROLE", name, err)
		}
	}
}
