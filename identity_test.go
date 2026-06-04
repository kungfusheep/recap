package main

import (
	. "github.com/kungfusheep/glyph"
	"path/filepath"
	"testing"
)

func TestAgentIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))

	if identityWho() != "agent" {
		t.Fatalf("unnamed agent should author as 'agent'")
	}
	if err := saveIdentity("Kestrel", "#66CCEE"); err != nil {
		t.Fatalf("save: %v", err)
	}
	name, col := loadIdentity()
	if name != "Kestrel" {
		t.Fatalf("name = %q, want Kestrel", name)
	}
	if col != Hex(0x66CCEE) {
		t.Fatalf("colour = %v, want #66CCEE", col)
	}
	if identityWho() != "Kestrel" {
		t.Fatalf("named agent should author as 'Kestrel', got %q", identityWho())
	}
	// bad colour rejected
	if err := saveIdentity("X", "nothex"); err == nil {
		t.Fatal("invalid colour should error")
	}
	// clear
	if err := saveIdentity("", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if identityWho() != "agent" {
		t.Fatal("cleared identity should fall back to 'agent'")
	}
}
