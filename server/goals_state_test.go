package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

func goalStateFixture(t *testing.T) (*Server, *Task) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	task, err := m.CreateTask("manual goal states", "verify goal states", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.DeleteTask(task.ID, DeleteTaskOptions{}) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Admission can be tested without launching model calls.
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	t.Cleanup(func() { s.engine.StopTask(task.ID) })
	oldEnabled, oldLimit := m.ConcurrencyLimit()
	t.Cleanup(func() { m.SetConcurrency(oldEnabled, oldLimit) })
	if err := m.SetConcurrency(false, 1); err != nil {
		t.Fatal(err)
	}
	return s, task
}

func goalStateRequest(t *testing.T, s *Server, taskID, goalID, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+taskID+"/goals/"+goalID+"/state", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestManualGoalStatesCompleteAndReopenTask(t *testing.T) {
	s, task := goalStateFixture(t)
	first, err := task.Store.AddGoal(map[string]any{"text": "first", "vulnclass": "authz"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	second, err := task.Store.AddGoal(map[string]any{"text": "second"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	execCtx := s.engine.execContextFor(context.Background(), task.ID)
	set := func(id int64, state string) GoalDTO {
		t.Helper()
		rec := goalStateRequest(t, s, task.ID, strconv.FormatInt(id, 10), `{"state":"`+state+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("state=%s status=%d body=%s", state, rec.Code, rec.Body.String())
		}
		var goal GoalDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &goal); err != nil {
			t.Fatal(err)
		}
		if goal.State != state {
			t.Fatalf("goal=%+v", goal)
		}
		return goal
	}
	goal := set(first, "met")
	if goal.Text != "first" || goal.VulnClass != "authz" || goal.Origin != "human" {
		t.Fatalf("goal metadata changed: %+v", goal)
	}
	if isTerminalStatus(task.lifecycleSnapshot().Status) || execCtx.Err() != nil {
		t.Fatal("completing one goal stopped a task with another open goal")
	}
	set(second, "met")
	if task.lifecycleSnapshot().Status != "done" || execCtx.Err() == nil {
		t.Fatal("last completed goal did not end the task and cancel execution")
	}
	revision := task.goalRevision.Load()
	set(second, "met")
	if task.goalRevision.Load() != revision {
		t.Fatal("identical request changed the revision")
	}
	oldRound := task.goalRevision.Load()
	set(first, "open")
	state := task.lifecycleSnapshot()
	if state.Status != "running" || state.CompletedAt != 0 || state.Paused || state.Queued {
		t.Fatalf("reopened task state = %+v", state)
	}
	node, err := task.Store.GetNode(first)
	if err != nil || node.State != "open" {
		t.Fatalf("reopened goal not persisted: node=%+v err=%v", node, err)
	}
	// A planner round that began before the manual change must not re-finish it.
	if won, err := s.engine.completePlannerGoals(context.Background(), task, oldRound); err != nil || won {
		t.Fatalf("stale planner completed the task: won=%v err=%v", won, err)
	}
	if task.lifecycleSnapshot().Status != "running" {
		t.Fatal("stale completion changed task status")
	}
	triggers := task.drainTriggers()
	if len(triggers) != 3 || triggers[2].Kind != "goal_state" || triggers[2].GoalState != "open" {
		t.Fatalf("manual state triggers = %+v", triggers)
	}
	var auditCount int
	if err := s.m.pg.QueryRow(`SELECT count(*) FROM activity
WHERE exploration_id=$1 AND worker='system' AND summary LIKE '用户将目标标记为%'`, task.ExpID).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("state change audit count=%d err=%v", auditCount, err)
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	persisted, err := s.m.pg.GetTask(id)
	if err != nil || persisted.Status != "running" || persisted.CompletedAt != nil {
		t.Fatalf("task revival not persisted: %+v err=%v", persisted, err)
	}
}

func TestManualGoalStateRejectsInvalidTargetsAndDeletion(t *testing.T) {
	s, task := goalStateFixture(t)
	goal, err := task.Store.AddGoal(map[string]any{"text": "local"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := task.Store.AddIntent(map[string]any{"summary": "not a goal"}, 0, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.m.CreateTask("other task", "other goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.m.DeleteTask(other.ID, DeleteTaskOptions{})
	foreign, err := other.Store.AddGoal(map[string]any{"text": "foreign"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		taskID, goalID, body string
		status               int
	}{
		{task.ID, fmt.Sprint(goal), `{"state":"running"}`, 400},
		{task.ID, fmt.Sprint(goal), `{"state":"abandoned"}`, 400},
		{task.ID, fmt.Sprint(goal), `{}`, 400},
		{task.ID, fmt.Sprint(goal), `{"state":null}`, 400},
		{task.ID, fmt.Sprint(goal), `{`, 400},
		{task.ID, "bad-id", `{"state":"met"}`, 400},
		{task.ID, "0", `{"state":"met"}`, 400},
		{task.ID, fmt.Sprint(intent), `{"state":"met"}`, 404},
		{task.ID, fmt.Sprint(foreign), `{"state":"met"}`, 404},
		{"9223372036854775807", fmt.Sprint(goal), `{"state":"met"}`, 404},
	} {
		rec := goalStateRequest(t, s, tc.taskID, tc.goalID, tc.body)
		if rec.Code != tc.status {
			t.Fatalf("%+v status=%d body=%s", tc, rec.Code, rec.Body.String())
		}
	}
	s.engine.BeginDelete(task.ID)
	rec := goalStateRequest(t, s, task.ID, fmt.Sprint(goal), `{"state":"met"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting task: status=%d body=%s", rec.Code, rec.Body.String())
	}
	node, _ := task.Store.GetNode(goal)
	if node.State != "open" {
		t.Fatal("invalid requests mutated the goal")
	}
}

func TestReopenedGoalRespectsTaskConcurrencyQueue(t *testing.T) {
	s, task := goalStateFixture(t)
	goal, err := task.Store.AddGoal(map[string]any{"text": "reopen with no capacity"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	task.Store.SetGoalState(goal, "met")
	if err := s.m.SetTaskStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetConcurrency(true, 1); err != nil {
		t.Fatal(err)
	}
	holder, err := s.m.CreateTask("occupy slot", "holder goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.m.DeleteTask(holder.ID, DeleteTaskOptions{})
	s.engine.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) {
		return new(agent.Planner), new(agent.Worker)
	})
	rec := goalStateRequest(t, s, task.ID, fmt.Sprint(goal), `{"state":"open"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen: status=%d body=%s", rec.Code, rec.Body.String())
	}
	state := task.lifecycleSnapshot()
	if state.Status != "running" || !state.Queued || !s.engine.IsPaused(task.ID) {
		t.Fatalf("reopened task bypassed admission: %+v", state)
	}
}
