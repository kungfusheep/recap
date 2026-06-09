package main

import (
	"path/filepath"
	"testing"
)

// togglePin pins the selected task, persists it (survives a fresh load), and unpins on a
// second toggle. Persistence is the `pins` file beside the db.
func TestTogglePinPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	prev := uiStore
	st, err := OpenAt(filepath.Join(dir, "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uiStore = st
	pinned = nil
	t.Cleanup(func() { uiStore = prev; pinned = nil; vmRows = nil; sel = 0; st.Close() })

	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t"})
	reloadTasks()
	sel = indexOfTask(id)

	togglePin()
	if !pinned[id] {
		t.Fatal("task should be pinned after toggle")
	}
	// a fresh load from disk sees the pin (persisted, not just in-memory)
	if fresh := loadPins(); !fresh[id] {
		t.Fatalf("pin not persisted to disk: %+v", fresh)
	}

	sel = indexOfTask(id)
	togglePin()
	if pinned[id] {
		t.Fatal("task should be unpinned after second toggle")
	}
	if fresh := loadPins(); fresh[id] {
		t.Fatalf("unpin not persisted: %+v", fresh)
	}
}

// a pinned task floats into a "PINNED" section at the top of the inbox, ahead of its
// normal state group — even when it would otherwise sort lower (older inbox item).
func TestReloadPinnedSectionOnTop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	prev := uiStore
	st, err := OpenAt(filepath.Join(dir, "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uiStore = st
	pinned = nil
	t.Cleanup(func() { uiStore = prev; pinned = nil; vmRows = nil; sel = 0 })

	a, _ := st.Add(Task{Repo: "r", Title: "a"}) // oldest → leads the inbox normally
	b, _ := st.Add(Task{Repo: "r", Title: "b"})
	c, _ := st.Add(Task{Repo: "r", Title: "c"})

	// pin the LAST one — without pinning it would sort to the bottom of the inbox.
	ensurePins()
	pinned[c] = true
	savePins(pinned)

	reloadTasks()

	// the group label rides on the task row itself (not a separate row): the first row
	// is the pinned task, carrying the PINNED section header.
	if len(vmRows) == 0 || !vmRows[0].HasGroup || vmRows[0].GroupLabel != "PINNED" {
		t.Fatalf("first row should carry the PINNED section header, got %+v", vmRows[0])
	}
	if vmRows[0].ID != c {
		t.Fatalf("pinned task should lead, got id %d want %d", vmRows[0].ID, c)
	}
	// the unpinned tasks still appear, in their own (non-PINNED) section below
	var sawA, sawB, sawOtherSection bool
	for _, r := range vmRows[1:] {
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
		t.Fatalf("unpinned tasks dropped: a=%v b=%v", sawA, sawB)
	}
	if !sawOtherSection {
		t.Fatal("unpinned tasks should sit under their own state section, not PINNED")
	}
}
