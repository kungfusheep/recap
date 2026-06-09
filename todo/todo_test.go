package todo

import (
	"os"
	"strings"
	"testing"
)

// PathFor resolves {relpath} relative to home, and Append writes a line. An empty
// template disables resolution (no TODO located for that repo).
func TestPathForAndAppend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := home + "/code/wed"
	path, err := PathFor("~/notes/{relpath}/TODO.md", repo)
	if err != nil {
		t.Fatalf("PathFor: %v", err)
	}
	want := home + "/notes/code/wed/TODO.md"
	if path != want {
		t.Fatalf("want %q, got %q", want, path)
	}

	if err := Append(path, "- [ ] one"); err != nil {
		t.Fatalf("append: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "- [ ] one\n") {
		t.Fatalf("first line missing:\n%s", b)
	}

	// empty template disables resolution
	if p, _ := PathFor("", repo); p != "" {
		t.Fatalf("want empty path with no template, got %q", p)
	}
}
