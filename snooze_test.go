package main

import (
	"path/filepath"
	"testing"
)

// snoozeTodo persists a skipped todo ref per-repo; loadSnoozed returns it while fresh,
// drops it after the TTL, and never leaks across repos. (lets recap next --wait park past
// a permanently-blocked todo)
func TestSnoozeTodoTTL(t *testing.T) {
	t.Setenv("RECAP_DB", filepath.Join(t.TempDir(), "r.db"))
	prevNow := snoozeNow
	defer func() { snoozeNow = prevNow }()

	base := int64(1_000_000)
	snoozeNow = func() int64 { return base }

	if err := snoozeTodo("repoX", "todo:abc"); err != nil {
		t.Fatalf("snoozeTodo: %v", err)
	}
	if !loadSnoozed("repoX")["todo:abc"] {
		t.Fatal("freshly snoozed ref should be active")
	}
	if loadSnoozed("repoY")["todo:abc"] {
		t.Fatal("snooze must be per-repo (leaked into repoY)")
	}

	// once the TTL elapses the snooze lifts and the todo re-surfaces
	snoozeNow = func() int64 { return base + int64(snoozeTTL.Seconds()) + 1 }
	if loadSnoozed("repoX")["todo:abc"] {
		t.Fatal("expired snooze should be gone (todo re-surfaces)")
	}
}
