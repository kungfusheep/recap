package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

// vim nav for the TODO list: g/G jump to top/bottom, C-d/C-u half-page, all
// clamped to the list bounds.
func TestTodoVimNav(t *testing.T) {
	prev := todoItems
	t.Cleanup(func() { todoItems = prev; todoSel = 0 })
	todoItems = make([]todoItem, 20)
	for i := range todoItems {
		todoItems[i] = todoItem{IsTask: true, Text: "x"}
	}

	todoSel = 5
	todoBottom()
	if todoSel != 19 {
		t.Fatalf("G should jump to last (19), got %d", todoSel)
	}
	todoTop()
	if todoSel != 0 {
		t.Fatalf("g should jump to first (0), got %d", todoSel)
	}
	todoHalfDown()
	if todoSel != todoHalfPage {
		t.Fatalf("C-d should move down a half-page to %d, got %d", todoHalfPage, todoSel)
	}
	todoHalfUp()
	if todoSel != 0 {
		t.Fatalf("C-u should move back to 0, got %d", todoSel)
	}
	// clamping past the ends
	todoHalfUp()
	if todoSel != 0 {
		t.Fatalf("C-u at top should clamp to 0, got %d", todoSel)
	}
	todoSel = 18
	todoHalfDown()
	if todoSel != 19 {
		t.Fatalf("C-d near bottom should clamp to 19, got %d", todoSel)
	}
}

// editing the TODO in-app must refresh the upcoming section: todoSave resets
// upcomingRepo so updateUpcoming reloads (a just-added todo would otherwise stay
// invisible until the selected repo changed).
func TestTodoEditRefreshesUpcoming(t *testing.T) {
	dir := t.TempDir()
	prevPath, prevItems := todoPath, todoItems
	uiApp = NewApp()
	t.Cleanup(func() { todoPath = prevPath; todoItems = prevItems; uiApp = nil; upcomingRepo = "" })
	todoPath = dir + "/TODO.md"
	todoItems = []todoItem{{IsTask: true, Text: "a", Raw: "- [ ] a"}}
	upcomingRepo = "/some/repo" // pretend the upcoming list is loaded for a repo
	todoSave()
	if upcomingRepo != "" {
		t.Fatalf("todoSave should reset upcomingRepo to force an upcoming reload, got %q", upcomingRepo)
	}
}
