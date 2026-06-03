package main

import (
	"os"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the TODO editor rows render each item's own checkbox state — the branch is
// pointer-bound (If(&it.Done)), so a done task shows [x] and an undone one [ ],
// rather than the List's single compiled template baking one placeholder branch.
func TestTodoRowRendersPerItem(t *testing.T) {
	prev, prevSel := todoItems, todoSel
	t.Cleanup(func() { todoItems = prev; todoSel = prevSel })
	todoItems = []todoItem{
		{IsTask: false, Raw: "# Heading"},
		{IsTask: true, Done: false, Text: "undone task line that is long enough to reveal truncation bugs"},
		{IsTask: true, Done: true, Text: "done two"},
	}
	todoSel = 0
	todoPrep() // precomputes Display/FGColor/Selected used by todoRow
	node := List(&todoItems).Selection(&todoSel).Marker("  ").
		SelectedStyle(Style{}).Render(todoRow)
	tmpl := Build(node)
	buf := NewBuffer(80, 12)
	tmpl.Execute(buf, 80, 12)
	full := ""
	for y := 0; y < 12; y++ {
		full += buf.GetLine(y) + "\n"
	}
	// full text (no truncation) + correct per-row checkbox state
	for _, want := range []string{"# Heading", "[ ] undone task line that is long enough to reveal truncation bugs", "[x] done two"} {
		if !strings.Contains(full, want) {
			t.Fatalf("todo render missing %q:\n%s", want, full)
		}
	}
	if strings.Contains(full, "[ ] done two") || strings.Contains(full, "[x] undone") {
		t.Fatalf("per-row checkbox state baked wrong:\n%s", full)
	}
}

// the TODO editor parses checkbox lines, carries non-task lines through verbatim,
// toggles/adds, and round-trips to disk (creating a missing file) without
// reformatting the prose around the tasks.
func TestTodoEditRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/TODO.md"

	// missing file → empty list, no error
	items, err := readTodo(path)
	if err != nil || len(items) != 0 {
		t.Fatalf("missing file: want empty, got %d items err=%v", len(items), err)
	}

	// seed a realistic file: a header, two tasks (one done), a blank, prose
	seed := "# TODO\n- [ ] first\n- [x] second\n\nsome prose\n"
	if err := os.MkdirAll(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err = readTodo(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("want 5 lines, got %d (%+v)", len(items), items)
	}
	// task detection + done state
	if !items[1].IsTask || items[1].Done || items[1].Text != "first" {
		t.Fatalf("line1 should be an undone task 'first', got %+v", items[1])
	}
	if !items[2].IsTask || !items[2].Done || items[2].Text != "second" {
		t.Fatalf("line2 should be a done task 'second', got %+v", items[2])
	}
	// non-task lines carried through
	if items[0].IsTask || items[0].Raw != "# TODO" {
		t.Fatalf("header should be passthrough, got %+v", items[0])
	}
	if items[4].IsTask || items[4].Raw != "some prose" {
		t.Fatalf("prose should be passthrough, got %+v", items[4])
	}

	// toggle the first task, add a new one, write back
	toggleTodo(items, 1)
	items = addTodoItem(items, "third")
	// toggling a non-task line is a no-op
	toggleTodo(items, 0)
	if items[0].IsTask {
		t.Fatal("toggling a header must not turn it into a task")
	}
	if err := writeTodo(path, items); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := "# TODO\n- [x] first\n- [x] second\n\nsome prose\n- [ ] third\n"
	if string(got) != want {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", string(got), want)
	}

	// blank add is ignored
	before := len(items)
	if items = addTodoItem(items, "   "); len(items) != before {
		t.Fatal("blank add should be ignored")
	}
}

// indented and mixed-case checkboxes are recognised, and re-rendered normalised.
func TestTodoParseVariants(t *testing.T) {
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
		it, _ := parseTodoLine(line)
		if it.IsTask != want.isTask || (it.IsTask && (it.Done != want.done || it.Text != want.text)) {
			t.Fatalf("parse %q = %+v, want task=%v done=%v text=%q", line, it, want.isTask, want.done, want.text)
		}
	}
	// normalisation on render
	if got := renderTodoItem(todoItem{IsTask: true, Done: true, Text: "x"}); got != "- [x] x" {
		t.Fatalf("render normalise = %q", got)
	}
	if got := renderTodoItem(todoItem{Raw: "## keep"}); got != "## keep" {
		t.Fatalf("passthrough render = %q", got)
	}
	_ = strings.TrimSpace
}
