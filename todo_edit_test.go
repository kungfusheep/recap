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
// The rows are todoVM (UI), built by todoPrep from todoUI.Data (todo.Item, data).
func TestTodoRowRendersPerItem(t *testing.T) {
	prevData, prevItems, prevSel := todoUI.Data, todoUI.Items, todoUI.Sel
	t.Cleanup(func() { todoUI.Data = prevData; todoUI.Items = prevItems; todoUI.Sel = prevSel })
	todoUI.Data = []todo.Item{
		{IsTask: false, Raw: "# Heading"},
		{IsTask: true, Done: false, Text: "undone task line that is long enough to reveal truncation bugs"},
		{IsTask: true, Done: true, Text: "done two"},
	}
	todoUI.Sel = 0
	todoUI.prep() // builds todoUI.Items (VMs) from todoUI.Data, precomputing Display/FGColor/Selected
	node := List(&todoUI.Items).Selection(&todoUI.Sel).Marker("  ").
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
// appends, edit rewrites the targeted line. Operates on todoUI.Data (data), and the disk
// round-trip proves the data layer is wired through.
func TestTodoAddAndEdit(t *testing.T) {
	dir := t.TempDir()
	prevPath, prevData, prevSel, prevIdx := todoUI.Path, todoUI.Data, todoUI.Sel, todoUI.EditingIdx
	todoUI.Path = dir + "/TODO.md"
	todoUI.Data = []todo.Item{
		{IsTask: false, Raw: "# TODO"},
		{IsTask: true, Done: false, Text: "first"},
	}
	todoUI.Sel = 1
	todoUI.prep()
	t.Cleanup(func() {
		todoUI.Path, todoUI.Data, todoUI.Sel, todoUI.EditingIdx = prevPath, prevData, prevSel, prevIdx
	})

	// add mode: append a new task
	todoUI.EditingIdx = -1
	todoUI.applyPromptText("second task")
	if len(todoUI.Data) != 3 || !todoUI.Data[2].IsTask || todoUI.Data[2].Text != "second task" {
		t.Fatalf("add failed: %+v", todoUI.Data)
	}

	// edit mode: rewrite the first task's body
	todoUI.EditingIdx = 1
	todoUI.applyPromptText("first (edited)")
	if todoUI.Data[1].Text != "first (edited)" {
		t.Fatalf("edit task failed: %q", todoUI.Data[1].Text)
	}

	// edit a non-task line rewrites its Raw
	todoUI.EditingIdx = 0
	todoUI.applyPromptText("# TASKS")
	if todoUI.Data[0].IsTask || todoUI.Data[0].Raw != "# TASKS" {
		t.Fatalf("edit header failed: %+v", todoUI.Data[0])
	}

	// empty input is a no-op
	todoUI.EditingIdx = 1
	todoUI.applyPromptText("   ")
	if todoUI.Data[1].Text != "first (edited)" {
		t.Fatalf("empty edit should be ignored: %q", todoUI.Data[1].Text)
	}

	// it all persisted to disk
	got, _ := os.ReadFile(todoUI.Path)
	want := "# TASKS\n- [ ] first (edited)\n- [ ] second task\n"
	if string(got) != want {
		t.Fatalf("disk mismatch:\n got %q\nwant %q", string(got), want)
	}
}
