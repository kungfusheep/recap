package main

import (
	"path/filepath"
	"testing"
)

// the in-flight cursor round-trips through the file beside the db (ref + title), and
// clearing removes it (so the upcoming flare disappears).
func TestCurrentMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))

	if ref, _ := loadCurrent("wed"); ref != "" {
		t.Fatalf("fresh cursor should be empty")
	}
	if err := saveCurrent("wed", "amends:50", "in-progress flare on the upcoming section"); err != nil {
		t.Fatalf("save: %v", err)
	}
	ref, title := loadCurrent("wed")
	if ref != "amends:50" || title != "in-progress flare on the upcoming section" {
		t.Fatalf("loadCurrent = %q / %q", ref, title)
	}
	if currentTitle("wed") != "in-progress flare on the upcoming section" {
		t.Fatalf("currentTitle = %q", currentTitle("wed"))
	}
	// per-repo isolation: a different repo's cursor is independent
	if currentTitle("tui") != "" {
		t.Fatalf("another repo's cursor should be empty, got %q", currentTitle("tui"))
	}
	// clear
	if err := saveCurrent("wed", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ref, _ := loadCurrent("wed"); ref != "" {
		t.Fatalf("cursor should be cleared")
	}
	// clearing again is a no-op (no error on missing file)
	if err := saveCurrent("wed", "", ""); err != nil {
		t.Fatalf("double clear should be a no-op: %v", err)
	}
}
