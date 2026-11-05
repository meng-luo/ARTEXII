package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func TestStaleGoalVerdictsAreRejected(t *testing.T) {
	tools := NewToolSet(new(db.ExplorationStore), "planner")
	for _, tool := range []actool.CoreTool{tools.proveGoal(), tools.goalMet()} {
		t.Run(tool.Name(), func(t *testing.T) {
			released := false
			ctx := WithGoalStateGuard(context.Background(), func() (func(), bool) {
				return func() { released = true }, false
			})
			result, err := tool.Call(ctx, []byte(`{"goal_id":1,"evidence_id":2,"reason":"old verdict"}`), nil)
			if err != nil || !result.IsError || !released || tools.GoalMet {
				t.Fatalf("stale verdict not rejected: result=%+v err=%v released=%v", result, err, released)
			}
		})
	}
}

func TestCurrentGoalVerdictStillCompletes(t *testing.T) {
	tools := NewToolSet(nil, "planner")
	released := false
	ctx := WithGoalStateGuard(context.Background(), func() (func(), bool) {
		return func() { released = true }, true
	})
	result, err := tools.goalMet().Call(ctx, []byte(`{"reason":"current verdict"}`), nil)
	if err != nil || result.IsError || !tools.GoalMet || !released {
		t.Fatalf("current verdict rejected: result=%+v err=%v released=%v", result, err, released)
	}
}

func TestGoalStateTriggerExplainsManualDecision(t *testing.T) {
	got := renderTriggers(new(db.ExplorationStore), []TriggerEvent{
		{Kind: "goal_state", Detail: "reopened goal", GoalState: "open"},
		{Kind: "goal_state", Detail: "completed goal", GoalState: "met"},
	})
	for _, text := range []string{"reopened goal", "此前的完成判定已撤回", "completed goal", "保留该状态"} {
		if !strings.Contains(got, text) {
			t.Fatalf("trigger missing %q: %s", text, got)
		}
	}
}
