package main

import (
	"github.com/kungfusheep/recap/snooze"
	"path/filepath"
	"testing"
)

// snooze.Record persists a skipped todo ref per-repo; snooze.Load returns it while fresh,
// drops it after the TTL, and never leaks across repos. (lets recap next --wait park past
// a permanently-blocked todo)
func TestSnoozeTodoTTL(t *testing.T) {
	t.Setenv("RECAP_DB", filepath.Join(t.TempDir(), "r.db"))
	prevNow := snooze.Now
	defer func() { snooze.Now = prevNow }()

	base := int64(1_000_000)
	snooze.Now = func() int64 { return base }

	if err := snooze.Record("repoX", "todo:abc"); err != nil {
		t.Fatalf("snooze.Record: %v", err)
	}
	if !snooze.Load("repoX")["todo:abc"] {
		t.Fatal("freshly snoozed ref should be active")
	}
	if snooze.Load("repoY")["todo:abc"] {
		t.Fatal("snooze must be per-repo (leaked into repoY)")
	}

	// once the TTL elapses the snooze lifts and the todo re-surfaces
	snooze.Now = func() int64 { return base + int64(snooze.TTL.Seconds()) + 1 }
	if snooze.Load("repoX")["todo:abc"] {
		t.Fatal("expired snooze should be gone (todo re-surfaces)")
	}
}
