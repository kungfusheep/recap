package config

import (
	"os"
	"strings"
	"testing"
)

// the TODO template resolves {relpath} relative to home, and AppendTODO writes a
// line (used by the in-TUI todo editor; recap no longer drops review breadcrumbs —
// the agent reads amends straight from the db via recap next / recap redo).
func TestTODOTemplate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := Config{TODOTemplate: "~/notes/{relpath}/TODO.md"}
	repo := home + "/code/wed"
	path, err := cfg.TODOPathFor(repo)
	if err != nil {
		t.Fatalf("TODOPathFor: %v", err)
	}
	want := home + "/notes/code/wed/TODO.md"
	if path != want {
		t.Fatalf("want %q, got %q", want, path)
	}

	if err := AppendTODO(path, "- [ ] one"); err != nil {
		t.Fatalf("append: %v", err)
	}
	data := readFile(t, path)
	if !strings.Contains(data, "- [ ] one\n") {
		t.Fatalf("first line missing:\n%s", data)
	}

	// empty template disables writing
	if p, _ := (Config{}).TODOPathFor(repo); p != "" {
		t.Fatalf("want empty path with no template, got %q", p)
	}
}

// LoadConfig parses the trivial key = "value" file and tolerates a missing one
// (zero Config, no error). $RECAP_CONFIG overrides the default path.
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	// missing file → zero Config, no error
	t.Setenv("RECAP_CONFIG", dir+"/none.toml")
	if c, err := LoadConfig(); err != nil || c.TODOTemplate != "" || c.NameTheme != "" {
		t.Fatalf("missing file should give zero Config, got %+v err=%v", c, err)
	}

	p := dir + "/config.toml"
	if err := os.WriteFile(p, []byte("# comment\ntodo_template = \"~/n/{relpath}/T.md\"\nname_theme = \"birds\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECAP_CONFIG", p)
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.TODOTemplate != "~/n/{relpath}/T.md" {
		t.Fatalf("todo_template = %q", c.TODOTemplate)
	}
	if c.NameTheme != "birds" {
		t.Fatalf("name_theme = %q", c.NameTheme)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
