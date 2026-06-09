package main

import (
	"testing"

	"github.com/kungfusheep/recap/db"
)

// A user mark (approve/submit) holds the cursor at its index so the marked item leaves
// and the NEXT item slides up under it — a clean path down the list. An external/async
// reload still tracks the selected task by id, so a pushed item never yanks the reader's
// place. (todo #120)
func TestMarkHoldsSelectionIndex(t *testing.T) {
	st := testStore(t)
	prev, prevFltr := uiStore, repoFltr
	uiStore, repoFltr = st, ""
	t.Cleanup(func() {
		uiStore = prev
		repoFltr = prevFltr
		vmRows = nil
		sel = 0
		keepSelOnReload = false
		undoStack = nil
	})

	st.Add(db.Task{Repo: "z", Title: "a"}) // id 1, stays in inbox above the rest
	b, _ := st.Add(db.Task{Repo: "z", Title: "b"})
	c, _ := st.Add(db.Task{Repo: "z", Title: "c"})
	d, _ := st.Add(db.Task{Repo: "z", Title: "d"})
	reloadTasks() // inbox oldest-first: [a, b, c, d]

	// --- external reload tracks the task by id (no yank) ---
	// select c (index 2), approve b ABOVE it via the store, reload WITHOUT the flag.
	sel = 2
	if vmRows[sel].ID != c {
		t.Fatalf("setup: index 2 should be c (%d), got %d", c, vmRows[sel].ID)
	}
	if _, err := submitReview(uiStore, b, db.VerdictApprove, ""); err != nil {
		t.Fatalf("approve b: %v", err)
	}
	reloadTasks() // no keepSelOnReload → must follow c by id as it shifts up
	if vmRows[sel].ID != c {
		t.Fatalf("external reload must track c (%d) by id, got %d at sel=%d", c, vmRows[sel].ID, sel)
	}

	// --- a user mark holds the index so the next item slides up ---
	// sel is on c; approving it should leave the cursor on the NEXT item (d), not chase
	// c down into the done section.
	approveSelected() // sets keepSelOnReload, approves c, reloads
	if vmRows[sel].ID != d {
		t.Fatalf("after mark: cursor should hold its index and land on next item d (%d), got %d", d, vmRows[sel].ID)
	}
}
