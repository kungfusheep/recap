package main

import (
	"os"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/todo"
)

// the TODO editor rows render each item's own checkbox state — the branch is
// pointer-bound (If(&it.Done)), so a done task shows [x] and an undone one [ ],
// rather than the List's single compiled template baking one placeholder branch.
// The rows are todoVM (UI), built by todoPrep from todoData (todo.Item, data).
func TestTodoRowRendersPerItem(t *testing.T) {
	prevData, prevItems, prevSel := todoData, todoItems, todoSel
	t.Cleanup(func() { todoData = prevData; todoItems = prevItems; todoSel = prevSel })
	todoData = []todo.Item{
		{IsTask: false, Raw: "# Heading"},
		{IsTask: true, Done: false, Text: "undone task line that is long enough to reveal truncation bugs"},
		{IsTask: true, Done: true, Text: "done two"},
	}
	todoSel = 0
	todoPrep() // builds todoItems (VMs) from todoData, precomputing Display/FGColor/Selected
	node := List(&todoItems).Selection(&todoSel).Marker("  ").
		SelectedStyle(Style{}).Render(todoRow)
	tmpl := Build(node)
	buf := NewBuffer(80, 12)
	tmpl.Execute(buf, 80, 12)
	full := ""
	for y := 0; y < 12; y++ {
		full += buf.GetLine(y) + "\n"
	}
	for _, want := range []string{"# Heading", "[ ] undone task line that is long enough to reveal truncation bugs", "[x] done two"} {
		if !strings.Contains(full, want) {
			t.Fatalf("todo render missing %q:\n%s", want, full)
		}
	}
	if strings.Contains(full, "[ ] done two") || strings.Contains(full, "[x] undone") {
		t.Fatalf("per-row checkbox state baked wrong:\n%s", full)
	}
}

// the editor can add new tasks and edit existing lines, writing back to disk: add
// appends, edit rewrites the targeted line. Operates on todoData (data), and the disk
// round-trip proves the data layer is wired through.
func TestTodoAddAndEdit(t *testing.T) {
	dir := t.TempDir()
	prevPath, prevData, prevSel, prevIdx := todoPath, todoData, todoSel, editingTodoIdx
	todoPath = dir + "/TODO.md"
	todoData = []todo.Item{
		{IsTask: false, Raw: "# TODO"},
		{IsTask: true, Done: false, Text: "first"},
	}
	todoSel = 1
	todoPrep()
	t.Cleanup(func() {
		todoPath, todoData, todoSel, editingTodoIdx = prevPath, prevData, prevSel, prevIdx
	})

	// add mode: append a new task
	editingTodoIdx = -1
	applyTodoPromptText("second task")
	if len(todoData) != 3 || !todoData[2].IsTask || todoData[2].Text != "second task" {
		t.Fatalf("add failed: %+v", todoData)
	}

	// edit mode: rewrite the first task's body
	editingTodoIdx = 1
	applyTodoPromptText("first (edited)")
	if todoData[1].Text != "first (edited)" {
		t.Fatalf("edit task failed: %q", todoData[1].Text)
	}

	// edit a non-task line rewrites its Raw
	editingTodoIdx = 0
	applyTodoPromptText("# TASKS")
	if todoData[0].IsTask || todoData[0].Raw != "# TASKS" {
		t.Fatalf("edit header failed: %+v", todoData[0])
	}

	// empty input is a no-op
	editingTodoIdx = 1
	applyTodoPromptText("   ")
	if todoData[1].Text != "first (edited)" {
		t.Fatalf("empty edit should be ignored: %q", todoData[1].Text)
	}

	// it all persisted to disk
	got, _ := os.ReadFile(todoPath)
	want := "# TASKS\n- [ ] first (edited)\n- [ ] second task\n"
	if string(got) != want {
		t.Fatalf("disk mismatch:\n got %q\nwant %q", string(got), want)
	}
}
