package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/todo"
)

// vim nav for the TODO list: g/G jump to top/bottom, C-d/C-u half-page, all
// clamped to the list bounds.
func TestTodoVimNav(t *testing.T) {
	prevData, prevItems := todoUI.Data, todoUI.Items
	t.Cleanup(func() { todoUI.Data = prevData; todoUI.Items = prevItems; todoUI.Sel = 0 })
	todoUI.Data = make([]todo.Item, 20)
	for i := range todoUI.Data {
		todoUI.Data[i] = todo.Item{IsTask: true, Text: "x"}
	}
	todoUI.prep() // build the render VMs from the data

	todoUI.Sel = 5
	todoUI.bottom()
	if todoUI.Sel != 19 {
		t.Fatalf("G should jump to last (19), got %d", todoUI.Sel)
	}
	todoUI.top()
	if todoUI.Sel != 0 {
		t.Fatalf("g should jump to first (0), got %d", todoUI.Sel)
	}
	todoUI.halfDown()
	if todoUI.Sel != todoHalfPage {
		t.Fatalf("C-d should move down a half-page to %d, got %d", todoHalfPage, todoUI.Sel)
	}
	todoUI.halfUp()
	if todoUI.Sel != 0 {
		t.Fatalf("C-u should move back to 0, got %d", todoUI.Sel)
	}
	// clamping past the ends
	todoUI.halfUp()
	if todoUI.Sel != 0 {
		t.Fatalf("C-u at top should clamp to 0, got %d", todoUI.Sel)
	}
	todoUI.Sel = 18
	todoUI.halfDown()
	if todoUI.Sel != 19 {
		t.Fatalf("C-d near bottom should clamp to 19, got %d", todoUI.Sel)
	}
}

// editing the TODO in-app must refresh the upcoming section: todoSave resets
// upcomingRepo so updateUpcoming reloads (a just-added todo would otherwise stay
// invisible until the selected repo changed).
func TestTodoEditRefreshesUpcoming(t *testing.T) {
	dir := t.TempDir()
	prevPath, prevData := todoUI.Path, todoUI.Data
	uiApp = NewApp()
	t.Cleanup(func() { todoUI.Path = prevPath; todoUI.Data = prevData; uiApp = nil; upcomingRepo = "" })
	todoUI.Path = dir + "/TODO.md"
	todoUI.Data = []todo.Item{{IsTask: true, Text: "a", Raw: "- [ ] a"}}
	upcomingRepo = "/some/repo" // pretend the upcoming list is loaded for a repo
	todoUI.save()
	if upcomingRepo != "" {
		t.Fatalf("todoSave should reset upcomingRepo to force an upcoming reload, got %q", upcomingRepo)
	}
}
