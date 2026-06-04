package main

import (
	"path/filepath"
	"testing"
)

// the in-flight marker round-trips through the file beside the db, and clearing
// removes it (so the upcoming flare disappears).
func TestWorkingMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))

	if loadWorking() != "" {
		t.Fatalf("fresh marker should be empty")
	}
	if err := saveWorking("addressing review #176"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadWorking(); got != "addressing review #176" {
		t.Fatalf("loadWorking = %q", got)
	}
	// whitespace trimmed
	if err := saveWorking("  spaced  "); err != nil {
		t.Fatalf("save spaced: %v", err)
	}
	if got := loadWorking(); got != "spaced" {
		t.Fatalf("trim = %q", got)
	}
	// clear
	if err := saveWorking(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if loadWorking() != "" {
		t.Fatalf("marker should be cleared")
	}
	// clearing again is a no-op (no error on missing file)
	if err := saveWorking(""); err != nil {
		t.Fatalf("double clear should be a no-op: %v", err)
	}
}
