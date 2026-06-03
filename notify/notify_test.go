package notify

import (
	"os"
	"testing"
)

// the pidfile round-trips, prunes dead PIDs, and Register's cleanup removes only
// its own entry — the machinery `recap add` relies on to signal open TUIs.
func TestPIDFileRoundTrip(t *testing.T) {
	// Register/Reload resolve the pidfile from $RECAP_DB (db path + ".pids"),
	// so point that at our temp db to isolate the test.
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	path := dir + "/recap.db.pids"

	// seed a live PID (ourselves) and a dead one (impossible PID).
	me := os.Getpid()
	if err := WritePIDs(path, []int{me, 2147480000}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := PrunePIDs(ReadPIDs(path))
	if len(got) != 1 || got[0] != me {
		t.Fatalf("prune kept %v, want just %d", got, me)
	}

	// Register adds us; cleanup removes only us, leaving other *live* TUIs intact.
	// Use the parent PID as the stand-in other TUI (guaranteed alive, so
	// Register's prune won't drop it).
	other := os.Getppid()
	if err := WritePIDs(path, []int{other}); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	cleanup := Register()
	after := ReadPIDs(path)
	if !contains(after, me) || !contains(after, other) {
		t.Fatalf("register should add me alongside live others, got %v", after)
	}
	cleanup()
	final := ReadPIDs(path)
	if contains(final, me) {
		t.Fatalf("cleanup left my PID behind: %v", final)
	}
	if !contains(final, other) {
		t.Fatalf("cleanup removed another live TUI's PID: %v", final)
	}
}

// Reload is a harmless no-op when no TUI is registered (headless CLI use), and
// prunes dead PIDs.
func TestReloadNoPIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	path := dir + "/recap.db.pids"
	Reload() // must not panic with no pidfile
	WritePIDs(path, []int{2147480000})
	Reload()
	if pids := ReadPIDs(path); len(pids) != 0 {
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
