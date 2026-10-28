package engine

import "context"

type hardStopKey struct{}

// WithHardStop returns a context that carries a last-resort stop signal. A
// post-hook normally runs to its own timeout even when the operation was
// cancelled, because it must learn about the result; closing stop ends it
// too. The daemon closes it when the shutdown budget is used up.
func WithHardStop(ctx context.Context, stop <-chan struct{}) context.Context {
	return context.WithValue(ctx, hardStopKey{}, stop)
}

// hardStopContext derives from ctx a context that ends when stop (if any) closes.
func hardStopContext(parent, ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop, ok := parent.Value(hardStopKey{}).(<-chan struct{})
	if !ok {
		return ctx, cancel
	}
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
