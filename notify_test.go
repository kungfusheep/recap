package main

import (
	"os"
	"testing"
)

// the pidfile round-trips, prunes dead PIDs, and registerUIPID's cleanup removes
// only its own entry — the machinery `recap add` relies on to signal open TUIs.
func TestPIDFileRoundTrip(t *testing.T) {
	db := t.TempDir() + "/recap.db"
	t.Setenv("RECAP_DB", db)

	path, err := pidFilePath()
	if err != nil {
		t.Fatalf("pidFilePath: %v", err)
	}
	if path != db+".pids" {
		t.Fatalf("pidfile = %q, want %q", path, db+".pids")
	}

	// seed a live PID (ourselves) and a dead one (impossible PID).
	me := os.Getpid()
	if err := writePIDs(path, []int{me, 2147480000}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := prunePIDs(readPIDs(path))
	if len(got) != 1 || got[0] != me {
		t.Fatalf("prune kept %v, want just %d", got, me)
	}

	// registerUIPID adds us; cleanup removes only us, leaving other *live* TUIs
	// intact. Use the parent PID as the stand-in other TUI (guaranteed alive, so
	// register's prune won't drop it).
	other := os.Getppid()
	if err := writePIDs(path, []int{other}); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	cleanup := registerUIPID()
	after := readPIDs(path)
	if !contains(after, me) || !contains(after, other) {
		t.Fatalf("register should add me alongside live others, got %v", after)
	}
	cleanup()
	final := readPIDs(path)
	if contains(final, me) {
		t.Fatalf("cleanup left my PID behind: %v", final)
	}
	if !contains(final, other) {
		t.Fatalf("cleanup removed another live TUI's PID: %v", final)
	}
}

// notifyReload is a harmless no-op when no TUI is registered (headless CLI use).
func TestNotifyReloadNoPIDs(t *testing.T) {
	db := t.TempDir() + "/recap.db"
	t.Setenv("RECAP_DB", db)
	notifyReload() // must not panic with no pidfile
	// and with only a dead PID, it prunes to empty without error
	path, _ := pidFilePath()
	writePIDs(path, []int{2147480000})
	notifyReload()
	if pids := readPIDs(path); len(pids) != 0 {
		t.Fatalf("dead PID not pruned: %v", pids)
	}
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
