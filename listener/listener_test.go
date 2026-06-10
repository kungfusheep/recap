package listener

import (
	"os"
	"path/filepath"
	"testing"
)

// Register/Active round-trip: a live registration is visible, cleanup removes it,
// and a stale pidfile (dead process) is pruned rather than reported.
func TestRegisterActivePrune(t *testing.T) {
	dirp := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dirp, "recap.db"))

	if got := Active(); len(got) != 0 {
		t.Fatalf("fresh dir should have no listeners, got %v", got)
	}

	cleanup := Register("alpha")
	if got := Active(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("live listener not visible: %v", got)
	}

	// a stale entry for a dead pid is pruned on read
	d, _ := dir()
	if err := os.WriteFile(filepath.Join(d, "ghost.pid"), []byte("999999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Active(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("ghost should be pruned: %v", got)
	}
	if _, err := os.Stat(filepath.Join(d, "ghost.pid")); !os.IsNotExist(err) {
		t.Fatal("stale pidfile should be removed")
	}

	cleanup()
	if got := Active(); len(got) != 0 {
		t.Fatalf("cleanup should deregister: %v", got)
	}

	// cleanup must not remove a NEWER registration by another pid
	Register("alpha")
	d2, _ := dir()
	os.WriteFile(filepath.Join(d2, "alpha.pid"), []byte("1"), 0o644) // pid 1: launchd, always alive
	cleanup() // stale closure from the earlier registration
	if b, err := os.ReadFile(filepath.Join(d2, "alpha.pid")); err != nil || string(b) != "1" {
		t.Fatalf("cleanup clobbered a newer registration: %v %q", err, b)
	}
}
