package poll

import (
	"context"
	"testing"
	"time"
)

func TestWaitReadyImmediately(t *testing.T) {
	if got := Wait(context.Background(), time.Millisecond, time.Second, func() bool { return true }); got != Ready {
		t.Fatalf("ready-up-front should return Ready, got %v", got)
	}
}

func TestWaitBecomesReady(t *testing.T) {
	calls := 0
	got := Wait(context.Background(), 2*time.Millisecond, time.Second, func() bool {
		calls++
		return calls >= 3 // false, false, true
	})
	if got != Ready {
		t.Fatalf("should become Ready once predicate flips, got %v", got)
	}
}

func TestWaitTimesOut(t *testing.T) {
	got := Wait(context.Background(), 2*time.Millisecond, 20*time.Millisecond, func() bool { return false })
	if got != TimedOut {
		t.Fatalf("never-ready should TimeOut, got %v", got)
	}
}

func TestWaitCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	got := Wait(ctx, 2*time.Millisecond, time.Second, func() bool { return false })
	if got != Canceled {
		t.Fatalf("canceled ctx should return Canceled, got %v", got)
	}
}
