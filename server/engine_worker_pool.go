package server

import (
	"context"
	"sync"
	"time"
)

// busy includes retiring slots until their current intent has fully settled.
type taskWorkerPool struct {
	mu      sync.Mutex
	desired map[string]int64
	running map[string]int64
	busy    int
}

func (p *taskWorkerPool) contains(slot WorkerSlot) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.desired[slot.Name]
	return ok && id == slot.ProfileID
}

func (p *taskWorkerPool) acquire(slot WorkerSlot) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.desired[slot.Name]
	if !ok || id != slot.ProfileID || p.busy >= len(p.desired) {
		return false
	}
	p.busy++
	return true
}

func (p *taskWorkerPool) release() {
	p.mu.Lock()
	p.busy--
	p.mu.Unlock()
}

func (e *Engine) workerPoolLoop(ctx context.Context, t *Task, initial []WorkerSlot) {
	pool := &taskWorkerPool{running: make(map[string]int64)}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	slots := initial
	lastCount := -1
	for {
		count := t.lifecycleSnapshot().WorkerCount
		if count != lastCount {
			if next, err := workerSlotsForTask(&Task{WorkerCount: count}, e.m.WorkerPools(), e.m.Workers()); err == nil {
				slots = next
			}
			lastCount = count
		}
		e.deleteMu.RLock()
		if ctx.Err() != nil || e.IsDeleting(t.ID) {
			e.deleteMu.RUnlock()
			return
		}
		plan := make(map[string]int64, len(slots))
		for _, slot := range slots {
			plan[slot.Name] = slot.ProfileID
		}
		pool.mu.Lock()
		pool.desired = plan
		// Keep retired owners available for individually resumed intents.
		owners := make(map[string]int64)
		if old, ok := e.slotPlans.Load(t.ID); ok {
			for name, id := range old.(map[string]int64) {
				owners[name] = id
			}
		}
		for _, slot := range slots {
			if _, exists := pool.running[slot.Name]; exists {
				continue
			}
			owners[slot.Name] = slot.ProfileID
			pool.running[slot.Name] = slot.ProfileID
			rt := e.registerTaskRoutines(ctx, t.ID, 1)
			runTaskRoutine(rt, func(loopCtx context.Context) {
				defer func() {
					pool.mu.Lock()
					delete(pool.running, slot.Name)
					pool.mu.Unlock()
				}()
				e.workerLoop(loopCtx, t, slot, pool)
			})
		}
		e.slotPlans.Store(t.ID, owners)
		pool.mu.Unlock()
		e.deleteMu.RUnlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
