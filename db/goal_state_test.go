package db

import "testing"

func TestGoalStateScopeIdempotenceAndCounts(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	exp, err := d.CreateExploration("goal-state-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, exp)
	store := d.Exploration(exp)
	if met, err := store.AllGoalsMet(); err != nil || met {
		t.Fatalf("empty exploration: met=%v err=%v", met, err)
	}
	goal, err := store.AddGoal(map[string]any{"text": "complete then reopen"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	d.QueryRow(`SELECT content_version FROM exploration_nodes WHERE id=$1`, goal).Scan(&version)
	for _, state := range []string{"met", "met", "open"} {
		node, changed, err := store.SetGoalState(goal, state)
		if err != nil || node == nil || node.State != state {
			t.Fatalf("state=%s node=%+v err=%v", state, node, err)
		}
		if changed {
			version++
		}
		var gotVersion int64
		if err := d.QueryRow(`SELECT content_version FROM exploration_nodes WHERE id=$1`, goal).Scan(&gotVersion); err != nil || gotVersion != version {
			t.Fatalf("version=%d want=%d err=%v", gotVersion, version, err)
		}
		if met, err := store.AllGoalsMet(); err != nil || met != (state == "met") {
			t.Fatalf("state=%s allMet=%v err=%v", state, met, err)
		}
	}
	if _, _, err := store.SetGoalState(goal, "running"); err == nil {
		t.Fatal("invalid goal state accepted")
	}
	if node, changed, err := d.Exploration(exp+1).SetGoalState(goal, "met"); node != nil || changed || err != nil {
		t.Fatalf("foreign goal mutated: node=%+v changed=%v err=%v", node, changed, err)
	}
	intent, err := store.AddIntent(map[string]any{"summary": "intent"}, 0, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	if node, changed, err := store.SetGoalState(intent, "met"); node != nil || changed || err != nil {
		t.Fatalf("intent mutated: node=%+v changed=%v err=%v", node, changed, err)
	}
	store.SetNodeState(goal, "abandoned")
	if met, err := store.AllGoalsMet(); err != nil || met {
		t.Fatalf("abandoned goal treated as completed: met=%v err=%v", met, err)
	}
}
