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

	if ref, _ := loadCurrent(); ref != "" {
		t.Fatalf("fresh cursor should be empty")
	}
	if err := saveCurrent("amends:50", "in-progress flare on the upcoming section"); err != nil {
		t.Fatalf("save: %v", err)
	}
	ref, title := loadCurrent()
	if ref != "amends:50" || title != "in-progress flare on the upcoming section" {
		t.Fatalf("loadCurrent = %q / %q", ref, title)
	}
	if currentTitle() != "in-progress flare on the upcoming section" {
		t.Fatalf("currentTitle = %q", currentTitle())
	}
	// clear
	if err := saveCurrent("", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ref, _ := loadCurrent(); ref != "" {
		t.Fatalf("cursor should be cleared")
	}
	// clearing again is a no-op (no error on missing file)
	if err := saveCurrent("", ""); err != nil {
		t.Fatalf("double clear should be a no-op: %v", err)
	}
}
