package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"strings"
	"testing"
	"time"
)

// isRecent: within the last day → true; older → false; blank/unparseable → recent (shown).
func TestIsRecent(t *testing.T) {
	now := time.Now()
	if !isRecent(now.Add(-1 * time.Hour).Format("2006-01-02 15:04:05")) {
		t.Fatal("an hour ago should be recent")
	}
	if isRecent(now.Add(-48 * time.Hour).Format("2006-01-02 15:04:05")) {
		t.Fatal("two days ago should NOT be recent")
	}
	if !isRecent("") {
		t.Fatal("blank stamp should default to recent (shown, never hidden)")
	}
}

// openOrLoadMore: on a "load more" row it raises doneOldLimit (and reloads); otherwise it
// opens the diff pane.
func TestOpenOrLoadMore(t *testing.T) {
	prev, prevLimit, prevPane := uiStore, doneOldLimit, pane
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; doneOldLimit = prevLimit; pane = prevPane; vmRows = nil; sel = 0 })

	doneOldLimit = 20
	vmRows = []taskVM{{LoadMore: true, RevIdx: -1}}
	sel = 0
	openOrLoadMore()
	if doneOldLimit != 40 {
		t.Fatalf("load-more row should raise doneOldLimit to 40, got %d", doneOldLimit)
	}

	vmRows = []taskVM{{ID: 1, RevIdx: -1}}
	sel = 0
	pane = paneList
	openOrLoadMore()
	if pane != paneDiff {
		t.Fatalf("non-load-more Enter should open the diff pane, got %q", pane)
	}
}

// reloadTasks shows recent done always but paginates completed items older than a day:
// only doneOldLimit of them render, the rest sit behind a "load more" row; raising the
// limit reveals them. (#163)
func TestReloadPaginatesOldDone(t *testing.T) {
	prev, prevLimit := uiStore, doneOldLimit
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; doneOldLimit = prevLimit; vmRows = nil; sel = 0 })
	doneOldLimit = 2

	old := time.Now().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	for i := 0; i < 5; i++ {
		id, _ := st.Add(db.Task{Repo: "r", Title: fmt.Sprintf("old%d", i), CreatedAt: old})
		st.SubmitReview(id, db.VerdictApprove, "")
	}
	rid, _ := st.Add(db.Task{Repo: "r", Title: "recent"}) // CreatedAt = now → recent
	st.SubmitReview(rid, db.VerdictApprove, "")

	reloadTasks()
	loadMore, lmTitle := 0, ""
	for _, r := range vmRows {
		if r.LoadMore {
			loadMore++
			lmTitle = r.Title
		}
	}
	if loadMore != 1 {
		t.Fatalf("want 1 load-more row, got %d", loadMore)
	}
	if !strings.Contains(lmTitle, "3 older") { // 5 old − limit 2 = 3 hidden
		t.Fatalf("load-more title = %q, want '3 older'", lmTitle)
	}

	doneOldLimit = 10 // reveal all
	reloadTasks()
	for _, r := range vmRows {
		if r.LoadMore {
			t.Fatal("no load-more row once all old done fit under the limit")
		}
	}
}
