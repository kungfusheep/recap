package db

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The db is DATA, not config: Path resolves to the XDG data dir and a one-shot
// migration moves the loop's state out of ~/.config/recap — everything except
// config.toml, which is genuinely config and stays for the dotfiles backup.
func TestPathMigratesOutOfConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RECAP_DB", "")
	t.Setenv("XDG_DATA_HOME", "")
	old := filepath.Join(home, ".config", "recap")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"recap.db", "identity-tui", "current-tui", "skipped-recap", "config.toml"} {
		if err := os.WriteFile(filepath.Join(old, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	migrateOnce = sync.Once{} // the one-shot is per-process; this test owns it

	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "share", "recap", "recap.db")
	if p != want {
		t.Fatalf("Path() = %q, want %q", p, want)
	}
	newDir := filepath.Dir(p)
	for _, f := range []string{"recap.db", "identity-tui", "current-tui", "skipped-recap"} {
		if _, err := os.Stat(filepath.Join(newDir, f)); err != nil {
			t.Fatalf("%s not migrated: %v", f, err)
		}
		if _, err := os.Stat(filepath.Join(old, f)); err == nil {
			t.Fatalf("%s still in the old config dir", f)
		}
	}
	// config.toml is config — it STAYS with the dotfiles
	if _, err := os.Stat(filepath.Join(old, "config.toml")); err != nil {
		t.Fatal("config.toml must stay in ~/.config/recap")
	}
	if _, err := os.Stat(filepath.Join(newDir, "config.toml")); err == nil {
		t.Fatal("config.toml must not migrate")
	}

	// a populated new home is never clobbered
	os.WriteFile(filepath.Join(old, "recap.db"), []byte("resurrected"), 0o644)
	migrateOnce = sync.Once{}
	if _, err := Path(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(newDir, "recap.db"))
	if string(b) != "recap.db" {
		t.Fatalf("migration clobbered the populated new home: %q", b)
	}
}
