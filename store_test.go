package main

import (
	"github.com/kungfusheep/recap/db"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	st, err := db.OpenAt(filepath.Join(t.TempDir(), "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAddAndGetRoundTrip(t *testing.T) {
	st := testStore(t)
	in := db.Task{
		Repo: "wed", RepoPath: "/x/wed", SHA: "abc123", Title: "save-as",
		Criterion: `parseOpenTarget("f:12:5")=={f,12,5}`, CheckCmd: "go test -run OpenTarget", Result: "PASS",
	}
	id, err := st.Add(in)
	if err != nil || id == 0 {
		t.Fatalf("add: id=%d err=%v", id, err)
	}
	got, err := st.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != in.Title || got.Repo != in.Repo || got.SHA != in.SHA ||
		got.Criterion != in.Criterion || got.CheckCmd != in.CheckCmd || got.Result != in.Result {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Status != db.StatusPending {
		t.Errorf("default status = %q, want pending", got.Status)
	}
	if got.CreatedAt == "" {
		t.Error("created_at not set")
	}
}

func TestStatusFiltering(t *testing.T) {
	st := testStore(t)
	id, _ := st.Add(db.Task{Repo: "wed", Title: "t1"})

	if got := mustList(t, st, db.StatusPending, ""); len(got) != 1 {
		t.Fatalf("pending list = %d, want 1", len(got))
	}
	if got := mustList(t, st, db.StatusRedo, ""); len(got) != 0 {
		t.Fatalf("redo list = %d, want 0", len(got))
	}

	if err := st.SetStatus(id, db.StatusRedo); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := mustList(t, st, db.StatusRedo, ""); len(got) != 1 {
		t.Fatalf("after set, redo list = %d, want 1", len(got))
	}
	if got := mustList(t, st, db.StatusPending, ""); len(got) != 0 {
		t.Fatalf("after set, pending list = %d, want 0", len(got))
	}
}

func TestRepoFilter(t *testing.T) {
	st := testStore(t)
	st.Add(db.Task{Repo: "wed", Title: "a"})
	st.Add(db.Task{Repo: "mail", Title: "b"})
	if got := mustList(t, st, "", "wed"); len(got) != 1 || got[0].Title != "a" {
		t.Fatalf("repo filter wed = %+v", got)
	}
}

func TestCommentThread(t *testing.T) {
	st := testStore(t)
	id, _ := st.Add(db.Task{Repo: "wed", Title: "t"})
	if _, err := st.AddComment(id, "you", "pre-fill cwd?"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := st.AddComment(id, "agent", "done in #7"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	cs, err := st.Comments(id)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(cs) != 2 || cs[0].Who != "you" || cs[1].Who != "agent" || cs[0].Body != "pre-fill cwd?" {
		t.Fatalf("thread = %+v", cs)
	}
}

func TestValidation(t *testing.T) {
	st := testStore(t)
	if _, err := st.Add(db.Task{Repo: "wed"}); err == nil {
		t.Error("expected error adding task with no title")
	}
	id, _ := st.Add(db.Task{Repo: "wed", Title: "t"})
	if err := st.SetStatus(id, "bogus"); err == nil {
		t.Error("expected error setting invalid status")
	}
	if err := st.SetStatus(99999, db.StatusApproved); err == nil {
		t.Error("expected error setting status on missing task")
	}
	if _, err := st.AddComment(99999, "you", "hi"); err == nil {
		t.Error("expected error commenting on missing task")
	}
}

func mustList(t *testing.T, st *db.Store, status, repo string) []db.Task {
	t.Helper()
	got, err := st.List(status, repo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return got
}

// done tasks sort by most recent review activity (last completed first), not by
// creation id: approve out of id order and the newest approval must lead.
func TestDoneOrderLastCompletedFirst(t *testing.T) {
	st := testStore(t)
	prev := uiStore
	uiStore = st
	t.Cleanup(func() { uiStore = prev })

	a, _ := st.Add(db.Task{Repo: "wed", Title: "a"}) // id 1
	b, _ := st.Add(db.Task{Repo: "wed", Title: "b"}) // id 2
	c, _ := st.Add(db.Task{Repo: "wed", Title: "c"}) // id 3
	// approve in the order c, a, b → b is the last completed, then a, then c
	for _, id := range []int64{c, a, b} {
		if _, err := st.SubmitReview(id, db.VerdictApprove, ""); err != nil {
			t.Fatalf("approve %d: %v", id, err)
		}
	}

	reloadTasks()
	var done []int64
	for _, tk := range tasks {
		if uiStore.ReviewState(tk.ID) == db.StateDone {
			done = append(done, tk.ID)
		}
	}
	want := []int64{b, a, c} // last completed first
	if len(done) != 3 || done[0] != want[0] || done[1] != want[1] || done[2] != want[2] {
		t.Fatalf("done order = %v, want %v (last completed first)", done, want)
	}
}

// AddReply threads a reply under an existing comment: it inherits the parent's
// task + review context, sets parent_id, and defaults who to "agent". Works the
// same whether the parent is a line comment (review-scoped) or a loose message.
func TestAddReplyThreads(t *testing.T) {
	st := testStore(t)
	tid, _ := st.Add(db.Task{Repo: "wed", Title: "t"})

	// reply to a review (line) comment → inherits its review_id
	pid, err := st.AddReviewComment(tid, "you", "fix this", "main.go", 10, "@@", "x := 1")
	if err != nil {
		t.Fatalf("parent review comment: %v", err)
	}
	rid, err := st.AddReply(pid, "", "done, see new diff")
	if err != nil {
		t.Fatalf("AddReply: %v", err)
	}
	cs, _ := st.Comments(tid)
	var reply *db.Comment
	for i := range cs {
		if cs[i].ID == rid {
			reply = &cs[i]
		}
	}
	if reply == nil {
		t.Fatal("reply not stored on the task")
	}
	if reply.ParentID != pid {
		t.Fatalf("reply.ParentID = %d, want %d", reply.ParentID, pid)
	}
	if reply.Who != "agent" {
		t.Fatalf("reply.Who = %q, want agent (default)", reply.Who)
	}
	parent := cs[0]
	if reply.ReviewID == 0 || reply.ReviewID != parent.ReviewID {
		t.Fatalf("reply.ReviewID = %d, want parent's %d", reply.ReviewID, parent.ReviewID)
	}

	// reply to a loose thread message → stays loose (review_id 0)
	lid, _ := st.AddComment(tid, "you", "a loose note")
	lr, err := st.AddReply(lid, "agent", "loose reply")
	if err != nil {
		t.Fatalf("AddReply loose: %v", err)
	}
	cs, _ = st.Comments(tid)
	for _, c := range cs {
		if c.ID == lr {
			if c.ParentID != lid || c.ReviewID != 0 {
				t.Fatalf("loose reply: parent=%d review=%d, want parent=%d review=0", c.ParentID, c.ReviewID, lid)
			}
		}
	}

	// replying to a non-existent comment errors, doesn't silently no-op
	if _, err := st.AddReply(99999, "agent", "x"); err == nil {
		t.Fatal("AddReply to missing parent should error")
	}
}

// splitThread separates top-level comments from replies and indexes replies by
// parent; an orphan reply (parent absent) is surfaced as top-level, never hidden.
func TestSplitThread(t *testing.T) {
	cs := []db.Comment{
		{ID: 1, ParentID: 0, Body: "top a"},
		{ID: 2, ParentID: 1, Body: "reply to a"},
		{ID: 3, ParentID: 0, Body: "top b"},
		{ID: 4, ParentID: 2, Body: "reply to reply"},
		{ID: 5, ParentID: 999, Body: "orphan"},
	}
	top, byParent := splitThread(cs)
	if len(top) != 3 { // 1, 3, and the orphan 5
		t.Fatalf("top = %d, want 3: %+v", len(top), top)
	}
	if len(byParent[1]) != 1 || byParent[1][0].ID != 2 {
		t.Fatalf("replies of 1 wrong: %+v", byParent[1])
	}
	if len(byParent[2]) != 1 || byParent[2][0].ID != 4 {
		t.Fatalf("nested reply of 2 wrong: %+v", byParent[2])
	}
}

// undoLast reverses the most recent approve/submit (LIFO), unsubmitting that
// task back to the inbox, regardless of current selection.
func TestUndoCategorise(t *testing.T) {
	st := testStore(t)
	prev := uiStore
	uiStore = st
	undoStack = nil
	t.Cleanup(func() { uiStore = prev; undoStack = nil })

	a, _ := st.Add(db.Task{Repo: "wed", Title: "a"})
	b, _ := st.Add(db.Task{Repo: "wed", Title: "b"})
	reloadTasks()

	// approve both (via the real handler path so the undo stack is populated)
	sel = indexOfTask(a)
	approveSelected()
	sel = indexOfTask(b)
	approveSelected()
	if uiStore.ReviewState(a) != db.StateDone || uiStore.ReviewState(b) != db.StateDone {
		t.Fatalf("setup: both should be done (a=%s b=%s)", uiStore.ReviewState(a), uiStore.ReviewState(b))
	}

	// undo → reverses b (last in), not a
	undoLast()
	if uiStore.ReviewState(b) != db.StatePending {
		t.Fatalf("after undo, b should be back in inbox, got %s", uiStore.ReviewState(b))
	}
	if uiStore.ReviewState(a) != db.StateDone {
		t.Fatalf("a should still be done after undoing b, got %s", uiStore.ReviewState(a))
	}
	// undo again → reverses a; a third undo is a no-op (nothing to undo)
	undoLast()
	if uiStore.ReviewState(a) != db.StatePending {
		t.Fatalf("after second undo, a should be back in inbox, got %s", uiStore.ReviewState(a))
	}
	if len(undoStack) != 0 {
		t.Fatalf("undo stack should be empty, got %d", len(undoStack))
	}
}

// indexOfTask returns the vmRows index of a task header row, for tests that drive
// selection-based handlers.
func indexOfTask(id int64) int {
	for i, r := range vmRows {
		if r.ID == id && r.RevIdx < 0 {
			return i
		}
	}
	return 0
}

// SetEmote attaches/overwrites/clears a reaction on a comment, and errors on a
// missing comment (so an emote can never silently vanish).
func TestSetEmote(t *testing.T) {
	st := testStore(t)
	tid, _ := st.Add(db.Task{Repo: "wed", Title: "t"})
	cid, _ := st.AddReviewComment(tid, "you", "please fix", "", 0, "", "")

	if err := st.SetEmote(cid, "👍"); err != nil {
		t.Fatalf("SetEmote: %v", err)
	}
	cs, _ := st.Comments(tid)
	if cs[0].Emote != "👍" {
		t.Fatalf("emote = %q, want 👍", cs[0].Emote)
	}
	// overwrite
	if err := st.SetEmote(cid, "✅"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	cs, _ = st.Comments(tid)
	if cs[0].Emote != "✅" {
		t.Fatalf("emote overwrite = %q, want ✅", cs[0].Emote)
	}
	// clear
	if err := st.SetEmote(cid, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cs, _ = st.Comments(tid)
	if cs[0].Emote != "" {
		t.Fatalf("emote should be cleared, got %q", cs[0].Emote)
	}
	// missing comment errors
	if err := st.SetEmote(99999, "👍"); err == nil {
		t.Fatal("SetEmote on a missing comment should error")
	}
}

// read receipts: reviewer comments start unread-by-agent and show in UnreadByAgent;
// marking read clears them; agent comments are never in the agent's unread inbox.
func TestReadReceipts(t *testing.T) {
	st := testStore(t)
	tid, _ := st.Add(db.Task{Repo: "wed", Title: "t"})
	c1, _ := st.AddReviewComment(tid, "you", "fix this", "", 0, "", "")
	c2, _ := st.AddComment(tid, "you", "a loose note")
	st.AddComment(tid, "agent", "my own note") // agent's own — never 'unread by agent'

	un, _ := st.UnreadByAgent("")
	if len(un) != 2 {
		t.Fatalf("want 2 unread reviewer comments, got %d", len(un))
	}
	if err := st.MarkReadAgent(c1); err != nil {
		t.Fatalf("MarkReadAgent: %v", err)
	}
	un, _ = st.UnreadByAgent("")
	if len(un) != 1 || un[0].ID != c2 {
		t.Fatalf("after read c1, unread should be just c2, got %+v", un)
	}
	// the read flag is visible on the comment
	cs, _ := st.Comments(tid)
	for _, c := range cs {
		if c.ID == c1 && c.ReadAgent == "" {
			t.Fatalf("c1 should carry a read_agent stamp")
		}
	}
	// user read-receipt round-trips independently
	if err := st.MarkReadUser(c2); err != nil {
		t.Fatalf("MarkReadUser: %v", err)
	}
	cs, _ = st.Comments(tid)
	for _, c := range cs {
		if c.ID == c2 && c.ReadUser == "" {
			t.Fatalf("c2 should carry a read_user stamp")
		}
	}
}

// cross-project safety: UnreadByAgent scoped to a repo must NOT surface another
// repo's reviewer comments (the bug where a loop in repo B answered repo A's
// feedback). Empty repo = all (explicit --all).
func TestUnreadScopedByRepo(t *testing.T) {
	st := testStore(t)
	a, _ := st.Add(db.Task{Repo: "alpha", Title: "a"})
	b, _ := st.Add(db.Task{Repo: "beta", Title: "b"})
	st.AddComment(a, "you", "alpha feedback")
	st.AddComment(b, "you", "beta feedback")

	al, _ := st.UnreadByAgent("alpha")
	if len(al) != 1 || al[0].TaskID != a {
		t.Fatalf("scoped to alpha should return only alpha's comment, got %+v", al)
	}
	be, _ := st.UnreadByAgent("beta")
	if len(be) != 1 || be[0].TaskID != b {
		t.Fatalf("scoped to beta should return only beta's comment, got %+v", be)
	}
	all, _ := st.UnreadByAgent("")
	if len(all) != 2 {
		t.Fatalf("unscoped should return both, got %d", len(all))
	}
}
