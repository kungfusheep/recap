package main

import (
	"testing"

	"github.com/kungfusheep/recap/notify"
)

// the main shim derives the pidfile from the db path and registers/cleans up via
// the notify package.
func TestPidFileShim(t *testing.T) {
	db := t.TempDir() + "/recap.db"
	t.Setenv("RECAP_DB", db)

	path, err := pidFilePath()
	if err != nil {
		t.Fatalf("pidFilePath: %v", err)
	}
	if path != db+".pids" {
		t.Fatalf("pidfile = %q, want %q", path, db+".pids")
	}

	cleanup := registerUIPID()
	if pids := notify.ReadPIDs(path); len(pids) == 0 {
		t.Fatal("registerUIPID did not record this process")
	}
	cleanup()

	notifyReload() // no-op, must not panic with no registered TUIs
}
