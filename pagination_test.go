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

// openOrLoadMore: on a "load more" row it raises inboxUI.DoneOldLimit (and reloads); otherwise it
// opens the diff pane.
func TestOpenOrLoadMore(t *testing.T) {
	prev, prevLimit, prevPane := uiStore, inboxUI.DoneOldLimit, pane
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; inboxUI.DoneOldLimit = prevLimit; pane = prevPane; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	inboxUI.DoneOldLimit = 20
	inboxUI.Rows = []taskVM{{LoadMore: true, RevIdx: -1}}
	inboxUI.Sel = 0
	openOrLoadMore()
	if inboxUI.DoneOldLimit != 40 {
		t.Fatalf("load-more row should raise inboxUI.DoneOldLimit to 40, got %d", inboxUI.DoneOldLimit)
	}

	inboxUI.Rows = []taskVM{{ID: 1, RevIdx: -1}}
	inboxUI.Sel = 0
	pane = paneList
	openOrLoadMore()
	if pane != paneDiff {
		t.Fatalf("non-load-more Enter should open the diff pane, got %q", pane)
	}
}

// reloadTasks shows recent done always but paginates completed items older than a day:
// only inboxUI.DoneOldLimit of them render, the rest sit behind a "load more" row; raising the
// limit reveals them. (#163)
func TestReloadPaginatesOldDone(t *testing.T) {
	prev, prevLimit := uiStore, inboxUI.DoneOldLimit
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; inboxUI.DoneOldLimit = prevLimit; inboxUI.Rows = nil; inboxUI.Sel = 0 })
	inboxUI.DoneOldLimit = 2

	old := time.Now().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	for i := 0; i < 5; i++ {
		id, _ := st.Add(db.Task{Repo: "r", Title: fmt.Sprintf("old%d", i), CreatedAt: old})
		st.SubmitReview(id, db.VerdictApprove, "")
	}
	rid, _ := st.Add(db.Task{Repo: "r", Title: "recent"}) // CreatedAt = now → recent
	st.SubmitReview(rid, db.VerdictApprove, "")

	reloadTasks()
	loadMore, lmTitle := 0, ""
	for _, r := range inboxUI.Rows {
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

	inboxUI.DoneOldLimit = 10 // reveal all
	reloadTasks()
	for _, r := range inboxUI.Rows {
		if r.LoadMore {
			t.Fatal("no load-more row once all old done fit under the limit")
		}
	}
}
