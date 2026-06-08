// Package poll provides long-poll waiting: block until a predicate becomes true, a
// timeout elapses, or the wait is canceled. It's the mechanic behind `recap next --wait`,
// which parks an idle agent until review feedback or new work appears instead of exiting
// the loop. The package knows nothing about recap — it just polls a predicate — so the
// public surface is one function and one result enum.
package poll

import (
	"context"
	"time"
)

// Outcome reports why Wait returned.
type Outcome int

const (
	Ready    Outcome = iota // ready() reported work is available
	TimedOut                // the timeout elapsed first
	Canceled                // the context was canceled first (e.g. SIGINT)
)

// Wait blocks until ready() returns true, the timeout elapses, or ctx is canceled,
// re-checking every interval. ready() is evaluated once up front, so an already-satisfied
// condition returns immediately with no poll delay. A non-positive timeout waits forever
// (until ready or canceled); a non-positive interval defaults to one second.
func Wait(ctx context.Context, interval, timeout time.Duration, ready func() bool) Outcome {
	if ready() {
		return Ready
	}
	if interval <= 0 {
		interval = time.Second
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return Canceled
		case <-deadline:
			return TimedOut
		case <-tick.C:
			if ready() {
				return Ready
			}
		}
	}
}
