package main

import (
	. "github.com/kungfusheep/glyph"
	"path/filepath"
	"testing"
)

func TestAgentIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))

	if name, _ := loadIdentity("recap"); name != "" {
		t.Fatalf("unnamed repo should have no identity")
	}
	if err := saveIdentity("recap", "Kestrel", "#66CCEE"); err != nil {
		t.Fatalf("save: %v", err)
	}
	name, col := loadIdentity("recap")
	if name != "Kestrel" {
		t.Fatalf("name = %q, want Kestrel", name)
	}
	if col != Hex(0x66CCEE) {
		t.Fatalf("colour = %v, want #66CCEE", col)
	}
	// the bug: another repo must NOT inherit this repo's identity (no more
	// "I'm Kestrel and so is my wife" — a loop elsewhere names itself fresh).
	if name, _ := loadIdentity("tui"); name != "" {
		t.Fatalf("another repo should have its OWN (empty) identity, got %q", name)
	}
	// bad colour rejected
	if err := saveIdentity("recap", "X", "nothex"); err == nil {
		t.Fatal("invalid colour should error")
	}
	// clear
	if err := saveIdentity("recap", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ := loadIdentity("recap"); n != "" {
		t.Fatal("cleared identity should be empty")
	}
}
