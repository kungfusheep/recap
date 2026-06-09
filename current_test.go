package main

import (
	"github.com/kungfusheep/recap/cursor"
	"path/filepath"
	"testing"
)

// the in-flight cursor round-trips through the file beside the db (ref + title), and
// clearing removes it (so the upcoming flare disappears).
func TestCurrentMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))

	if ref, _ := cursor.Load("wed"); ref != "" {
		t.Fatalf("fresh cursor should be empty")
	}
	if err := cursor.Save("wed", "amends:50", "in-progress flare on the upcoming section"); err != nil {
		t.Fatalf("save: %v", err)
	}
	ref, title := cursor.Load("wed")
	if ref != "amends:50" || title != "in-progress flare on the upcoming section" {
		t.Fatalf("cursor.Load = %q / %q", ref, title)
	}
	if cursor.Title("wed") != "in-progress flare on the upcoming section" {
		t.Fatalf("cursor.Title = %q", cursor.Title("wed"))
	}
	// per-repo isolation: a different repo's cursor is independent
	if cursor.Title("tui") != "" {
		t.Fatalf("another repo's cursor should be empty, got %q", cursor.Title("tui"))
	}
	// clear
	if err := cursor.Save("wed", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ref, _ := cursor.Load("wed"); ref != "" {
		t.Fatalf("cursor should be cleared")
	}
	// clearing again is a no-op (no error on missing file)
	if err := cursor.Save("wed", "", ""); err != nil {
		t.Fatalf("double clear should be a no-op: %v", err)
	}
}
