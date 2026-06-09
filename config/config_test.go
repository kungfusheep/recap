package config

import (
	"os"
	"testing"
)

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
