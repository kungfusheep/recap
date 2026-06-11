package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"strings"
	"testing"
	"time"
)

// openOrLoadMore: on a "load more" row it raises inboxUI.DoneLimit (and reloads); otherwise it
// opens the diff pane.
func TestOpenOrLoadMore(t *testing.T) {
	prev, prevLimit, prevPane := uiStore, inboxUI.DoneLimit, pane
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; inboxUI.DoneLimit = prevLimit; pane = prevPane; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	inboxUI.DoneLimit = 20
	inboxUI.Rows = []taskVM{{LoadMore: true, RevIdx: -1}}
	inboxUI.Sel = 0
	openOrLoadMore()
	if inboxUI.DoneLimit != 40 {
		t.Fatalf("load-more row should raise inboxUI.DoneLimit to 40, got %d", inboxUI.DoneLimit)
	}

	inboxUI.Rows = []taskVM{{ID: 1, RevIdx: -1}}
	inboxUI.Sel = 0
	pane = paneList
	openOrLoadMore()
	if pane != paneDiff {
		t.Fatalf("non-load-more Enter should open the diff pane, got %q", pane)
	}
}

// reloadTasks paginates ALL completed items — recency is no exemption: only
// inboxUI.DoneLimit render (last-completed-first, so the visible page is the most
// recent activity), the rest sit behind a "load more" row; raising the limit
// reveals them. (#163, tightened by todo:8880fc29 — a busy day used to flood the
// list because <24h done items bypassed the cap.)
func TestReloadPaginatesDone(t *testing.T) {
	prev, prevLimit := uiStore, inboxUI.DoneLimit
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = prev; inboxUI.DoneLimit = prevLimit; inboxUI.Rows = nil; inboxUI.Sel = 0 })
	inboxUI.DoneLimit = 2

	old := time.Now().Add(-72 * time.Hour).Format("2006-01-02 15:04:05")
	for i := 0; i < 5; i++ {
		id, _ := st.Add(db.Task{Repo: "r", Title: fmt.Sprintf("old%d", i), CreatedAt: old})
		st.SubmitReview(id, db.VerdictApprove, "")
	}
	rid, _ := st.Add(db.Task{Repo: "r", Title: "recentdone"}) // CreatedAt = now
	st.SubmitReview(rid, db.VerdictApprove, "")

	reloadTasks()
	loadMore, lmTitle, recentShown := 0, "", false
	for _, r := range inboxUI.Rows {
		if r.LoadMore {
			loadMore++
			lmTitle = r.Title
		}
		if r.Title == "recentdone" {
			recentShown = true
		}
	}
	if loadMore != 1 {
		t.Fatalf("want 1 load-more row, got %d", loadMore)
	}
	if !strings.Contains(lmTitle, "4 more") { // 6 done − limit 2 = 4 hidden
		t.Fatalf("load-more title = %q, want '4 more'", lmTitle)
	}
	// last-completed-first: the most recently reviewed item is on the visible page
	if !recentShown {
		t.Fatal("the most recently completed item should be on the first page")
	}

	inboxUI.DoneLimit = 10 // reveal all
	reloadTasks()
	for _, r := range inboxUI.Rows {
		if r.LoadMore {
			t.Fatal("no load-more row once all done fit under the limit")
		}
	}
}
