package todo

import (
	"os"
	"testing"
)

// Read parses checkbox lines, carries non-task lines through verbatim, Toggle/Add
// mutate, and Write round-trips to disk (creating a missing file) without reformatting
// the prose around the tasks.
func TestReadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/TODO.md"

	// missing file → empty list, no error
	items, err := Read(path)
	if err != nil || len(items) != 0 {
		t.Fatalf("missing file: want empty, got %d items err=%v", len(items), err)
	}

	seed := "# TODO\n- [ ] first\n- [x] second\n\nsome prose\n"
	if err := os.MkdirAll(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err = Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("want 5 lines, got %d (%+v)", len(items), items)
	}
	if !items[1].IsTask || items[1].Done || items[1].Text != "first" {
		t.Fatalf("line1 should be an undone task 'first', got %+v", items[1])
	}
	if !items[2].IsTask || !items[2].Done || items[2].Text != "second" {
		t.Fatalf("line2 should be a done task 'second', got %+v", items[2])
	}
	if items[0].IsTask || items[0].Raw != "# TODO" {
		t.Fatalf("header should be passthrough, got %+v", items[0])
	}
	if items[4].IsTask || items[4].Raw != "some prose" {
		t.Fatalf("prose should be passthrough, got %+v", items[4])
	}

	Toggle(items, 1)
	items = Add(items, "third")
	Toggle(items, 0) // toggling a non-task line is a no-op
	if items[0].IsTask {
		t.Fatal("toggling a header must not turn it into a task")
	}
	if err := Write(path, items); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := "# TODO\n- [x] first\n- [x] second\n\nsome prose\n- [ ] third\n"
	if string(got) != want {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", string(got), want)
	}

	before := len(items)
	if items = Add(items, "   "); len(items) != before {
		t.Fatal("blank add should be ignored")
	}
}

// indented and mixed-case checkboxes are recognised, and re-rendered normalised.
func TestParseLineVariants(t *testing.T) {
	cases := map[string]struct {
		isTask bool
		done   bool
		text   string
	}{
		"- [ ] a":     {true, false, "a"},
		"  - [x] b":   {true, true, "b"},
		"- [X] c":     {true, true, "c"},
		"## heading":  {false, false, ""},
		"":            {false, false, ""},
		"- not a box": {false, false, ""},
	}
	for line, want := range cases {
		it, _ := ParseLine(line)
		if it.IsTask != want.isTask || (it.IsTask && (it.Done != want.done || it.Text != want.text)) {
			t.Fatalf("parse %q = %+v, want task=%v done=%v text=%q", line, it, want.isTask, want.done, want.text)
		}
	}
	if got := (Item{IsTask: true, Done: true, Text: "x"}).Line(); got != "- [x] x" {
		t.Fatalf("render normalise = %q", got)
	}
	if got := (Item{Raw: "## keep"}).Line(); got != "## keep" {
		t.Fatalf("passthrough render = %q", got)
	}
}
