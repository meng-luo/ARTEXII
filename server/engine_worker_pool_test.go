package server

import (
	"context"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
)

func TestWorkerPoolLoopAddsAndRetiresWorkersAtRuntime(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := &Task{ID: "worker-resize-loop-test", WorkerCount: 1}
	e := NewEngine(m)
	defer e.StopTask(task.ID)
	calls := make(chan struct{}, 20)
	e.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return nil, nil
	})
	rt := e.registerTaskRoutines(ctx, task.ID, 1)
	runTaskRoutine(rt, func(ctx context.Context) {
		e.workerPoolLoop(ctx, task, []WorkerSlot{{Name: "work#1"}})
	})
	waitCalls := func(n int) {
		t.Helper()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for range n {
			select {
			case <-calls:
			case <-timer.C:
				t.Fatal("worker loop did not start or continue")
			}
		}
	}
	waitCalls(1)
	task.updateLifecycle(func(state *taskLifecycleState) { state.WorkerCount = 3 })
	waitCalls(2)
	task.updateLifecycle(func(state *taskLifecycleState) { state.WorkerCount = 1 })
	waitCalls(1)
	select {
	case <-calls:
		t.Fatal("retired worker continued resolving new work")
	case <-time.After(400 * time.Millisecond):
	}
	task.updateLifecycle(func(state *taskLifecycleState) { state.WorkerCount = 4 })
	waitCalls(3)
}

func TestWorkerPoolShrinkDrainsAllBusySlotsBeforeDispatch(t *testing.T) {
	a, b, c := WorkerSlot{Name: "work#1"}, WorkerSlot{Name: "work#2"}, WorkerSlot{Name: "work#3"}
	pool := &taskWorkerPool{desired: map[string]int64{a.Name: 0, b.Name: 0, c.Name: 0}}
	for _, slot := range []WorkerSlot{a, b, c} {
		if !pool.acquire(slot) {
			t.Fatalf("cannot start %s", slot.Name)
		}
	}
	pool.mu.Lock()
	pool.desired = map[string]int64{a.Name: 0}
	pool.mu.Unlock()
	// Even the retained slot must wait if it finishes first and retired slots
	// still have work. Merely disabling the highest slot numbers is insufficient.
	pool.release()
	if pool.acquire(a) || pool.acquire(b) {
		t.Fatal("dispatched while busy count exceeded new limit")
	}
	pool.release()
	if pool.acquire(a) {
		t.Fatal("dispatched while busy count equaled new limit")
	}
	pool.release()
	if !pool.acquire(a) {
		t.Fatal("did not dispatch after drain")
	}
	pool.release()
	pool.mu.Lock()
	pool.desired = map[string]int64{a.Name: 0, b.Name: 0}
	pool.mu.Unlock()
	if !pool.acquire(a) || !pool.acquire(b) || pool.acquire(c) {
		t.Fatal("did not honor increased limit")
	}
}

func TestWorkerPoolRetiresChangedProfile(t *testing.T) {
	pool := &taskWorkerPool{desired: map[string]int64{"work#1": 12}}
	if pool.contains(WorkerSlot{Name: "work#1", ProfileID: 7}) || pool.acquire(WorkerSlot{Name: "work#1", ProfileID: 7}) {
		t.Fatal("old profile was allowed to claim more work")
	}
	if !pool.contains(WorkerSlot{Name: "work#1", ProfileID: 12}) {
		t.Fatal("new profile not available")
	}
}
