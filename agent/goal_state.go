package agent

import "context"

type goalStateGuardKey struct{}

// WithGoalStateGuard serializes goal verdicts against manual goal changes.
// acquire returns a release function and whether this round is still current.
func WithGoalStateGuard(ctx context.Context, acquire func() (func(), bool)) context.Context {
	return context.WithValue(ctx, goalStateGuardKey{}, acquire)
}

func acquireGoalState(ctx context.Context) (func(), bool) {
	if acquire, ok := ctx.Value(goalStateGuardKey{}).(func() (func(), bool)); ok {
		release, current := acquire()
		return release, current && ctx.Err() == nil
	}
	return func() {}, ctx.Err() == nil
}
