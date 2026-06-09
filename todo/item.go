package todo

import (
	"os"
	"path/filepath"
	"strings"
)

// Item is one line of a repo's plain-text TODO. Task lines ("- [ ] …" / "- [x] …")
// are toggleable; everything else (headers, blanks, prose) is carried through verbatim
// as Raw so writing back never reformats the file. This is PURE DATA — no UI/display
// fields; the TUI builds its own view-model from these (data/UI separation).
type Item struct {
	IsTask bool
	Done   bool
	Text   string // task body without the "- [ ] " prefix (task lines only)
	Raw    string // the original line (non-task lines, and the source of truth otherwise)
}

// ParseLine recognises a markdown checkbox line, tolerating leading indentation and
// either case of x. The bool reports whether it's a task line.
func ParseLine(line string) (Item, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	for _, pfx := range []string{"- [ ] ", "- [] "} {
		if strings.HasPrefix(trimmed, pfx) {
			return Item{IsTask: true, Done: false, Text: strings.TrimPrefix(trimmed, pfx), Raw: line}, true
		}
	}
	for _, pfx := range []string{"- [x] ", "- [X] "} {
		if strings.HasPrefix(trimmed, pfx) {
			return Item{IsTask: true, Done: true, Text: strings.TrimPrefix(trimmed, pfx), Raw: line}, true
		}
	}
	return Item{IsTask: false, Raw: line}, false
}

// Line renders an item back to a single line: task items are normalised to
// "- [ ] text" / "- [x] text"; non-task lines are emitted verbatim.
func (it Item) Line() string {
	if !it.IsTask {
		return it.Raw
	}
	box := "[ ]"
	if it.Done {
		box = "[x]"
	}
	return "- " + box + " " + it.Text
}

// Read parses a TODO file into items. A missing file is not an error — it returns an
// empty slice so the editor can start a new list.
func Read(path string) ([]Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Item
	lines := strings.Split(string(data), "\n")
	// a trailing newline yields a final empty element; drop it so we don't grow a
	// blank line on every write.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for _, ln := range lines {
		it, _ := ParseLine(ln)
		out = append(out, it)
	}
	return out, nil
}

// Write renders the items back to the file (creating dirs), with a trailing newline.
// Atomic via a temp file + rename so a crash never truncates the TODO.
func Write(path string, items []Item) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Line())
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".todo-*.md")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// Toggle flips a task item's done state (no-op for non-task lines).
func Toggle(items []Item, i int) {
	if i < 0 || i >= len(items) || !items[i].IsTask {
		return
	}
	items[i].Done = !items[i].Done
}

// Add appends a new unchecked task with the given text.
func Add(items []Item, text string) []Item {
	text = strings.TrimSpace(text)
	if text == "" {
		return items
	}
	return append(items, Item{IsTask: true, Done: false, Text: text})
}
