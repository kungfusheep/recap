package main

import (
	"github.com/kungfusheep/recap/db"
	"path/filepath"
	"testing"
)

// togglePin pins the selected task, persists it (survives a fresh load), and unpins on a
// second toggle. Persistence is the `pins` file beside the db.
func TestTogglePinPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	prev := uiStore
	st, err := db.OpenAt(filepath.Join(dir, "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uiStore = st
	pinned = nil
	t.Cleanup(func() { uiStore = prev; pinned = nil; inboxUI.Rows = nil; inboxUI.Sel = 0; st.Close() })

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t"})
	reloadTasks()
	inboxUI.Sel = indexOfTask(id)

	togglePin()
	if !pinned[id] {
		t.Fatal("task should be pinned after toggle")
	}
	// a fresh load from disk sees the pin (persisted, not just in-memory)
	if fresh := loadPins(); !fresh[id] {
		t.Fatalf("pin not persisted to disk: %+v", fresh)
	}

	inboxUI.Sel = indexOfTask(id)
	togglePin()
	if pinned[id] {
		t.Fatal("task should be unpinned after second toggle")
	}
	if fresh := loadPins(); fresh[id] {
		t.Fatalf("unpin not persisted: %+v", fresh)
	}
}

// `u` undoes a pin: pinning then undoLast leaves it unpinned; unpinning then undoLast
// restores the pin. The toggle pushes its inverse onto the shared undo stack.
func TestUndoPin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	prev := uiStore
	st, err := db.OpenAt(filepath.Join(dir, "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uiStore = st
	pinned = nil
	undoStack = nil
	t.Cleanup(func() {
		uiStore = prev
		pinned = nil
		undoStack = nil
		inboxUI.Rows = nil
		inboxUI.Sel = 0
		st.Close()
	})

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t"})
	reloadTasks()
	inboxUI.Sel = indexOfTask(id)

	// pin → undo → unpinned
	togglePin()
	if !pinned[id] {
		t.Fatal("setup: task should be pinned")
	}
	undoLast()
	if pinned[id] {
		t.Fatal("undo of a pin should unpin the task")
	}
	if len(undoStack) != 0 {
		t.Fatalf("undo stack should be empty after the undo, got %d", len(undoStack))
	}

	// pin (so it's pinned), then unpin → undo → re-pinned (restores prior state)
	inboxUI.Sel = indexOfTask(id)
	togglePin() // pin
	undoStack = nil
	inboxUI.Sel = indexOfTask(id)
	togglePin() // unpin
	if pinned[id] {
		t.Fatal("setup: task should be unpinned before the undo")
	}
	undoLast()
	if !pinned[id] {
		t.Fatal("undo of an unpin should restore the pin")
	}
}

// a pinned task floats into a "PINNED" section at the top of the inbox, ahead of its
// normal state group — even when it would otherwise sort lower (older inbox item).
func TestReloadPinnedSectionOnTop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	prev := uiStore
	st, err := db.OpenAt(filepath.Join(dir, "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uiStore = st
	pinned = nil
	t.Cleanup(func() { uiStore = prev; pinned = nil; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	a, _ := st.Add(db.Task{Repo: "r", Title: "a"}) // oldest → leads the inbox normally
	b, _ := st.Add(db.Task{Repo: "r", Title: "b"})
	c, _ := st.Add(db.Task{Repo: "r", Title: "c"})

	// pin the LAST one — without pinning it would sort to the bottom of the inbox.
	ensurePins()
	pinned[c] = true
	savePins(pinned)

	reloadTasks()

	// the group label rides on the task row itself (not a separate row): the first row
	// is the pinned task, carrying the PINNED section header.
	if len(inboxUI.Rows) == 0 || !inboxUI.Rows[0].HasGroup || inboxUI.Rows[0].GroupLabel != "PINNED" {
		t.Fatalf("first row should carry the PINNED section header, got %+v", inboxUI.Rows[0])
	}
	if inboxUI.Rows[0].ID != c {
		t.Fatalf("pinned task should lead, got id %d want %d", inboxUI.Rows[0].ID, c)
	}
	// the unpinned inboxUI.Tasks still appear, in their own (non-PINNED) section below
	var sawA, sawB, sawOtherSection bool
	for _, r := range inboxUI.Rows[1:] {
		if r.HasGroup && r.GroupLabel != "PINNED" {
			sawOtherSection = true
		}
		if r.ID == a {
			sawA = true
		}
		if r.ID == b {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("unpinned inboxUI.Tasks dropped: a=%v b=%v", sawA, sawB)
	}
	if !sawOtherSection {
		t.Fatal("unpinned inboxUI.Tasks should sit under their own state section, not PINNED")
	}
}
