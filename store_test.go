package main

import (
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenAt(filepath.Join(t.TempDir(), "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAddAndGetRoundTrip(t *testing.T) {
	st := testStore(t)
	in := Task{
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
	if got.Status != StatusPending {
		t.Errorf("default status = %q, want pending", got.Status)
	}
	if got.CreatedAt == "" {
		t.Error("created_at not set")
	}
}

func TestStatusFiltering(t *testing.T) {
	st := testStore(t)
	id, _ := st.Add(Task{Repo: "wed", Title: "t1"})

	if got := mustList(t, st, StatusPending, ""); len(got) != 1 {
		t.Fatalf("pending list = %d, want 1", len(got))
	}
	if got := mustList(t, st, StatusRedo, ""); len(got) != 0 {
		t.Fatalf("redo list = %d, want 0", len(got))
	}

	if err := st.SetStatus(id, StatusRedo); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := mustList(t, st, StatusRedo, ""); len(got) != 1 {
		t.Fatalf("after set, redo list = %d, want 1", len(got))
	}
	if got := mustList(t, st, StatusPending, ""); len(got) != 0 {
		t.Fatalf("after set, pending list = %d, want 0", len(got))
	}
}

func TestRepoFilter(t *testing.T) {
	st := testStore(t)
	st.Add(Task{Repo: "wed", Title: "a"})
	st.Add(Task{Repo: "mail", Title: "b"})
	if got := mustList(t, st, "", "wed"); len(got) != 1 || got[0].Title != "a" {
		t.Fatalf("repo filter wed = %+v", got)
	}
}

func TestCommentThread(t *testing.T) {
	st := testStore(t)
	id, _ := st.Add(Task{Repo: "wed", Title: "t"})
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
	if _, err := st.Add(Task{Repo: "wed"}); err == nil {
		t.Error("expected error adding task with no title")
	}
	id, _ := st.Add(Task{Repo: "wed", Title: "t"})
	if err := st.SetStatus(id, "bogus"); err == nil {
		t.Error("expected error setting invalid status")
	}
	if err := st.SetStatus(99999, StatusApproved); err == nil {
		t.Error("expected error setting status on missing task")
	}
	if _, err := st.AddComment(99999, "you", "hi"); err == nil {
		t.Error("expected error commenting on missing task")
	}
}

func mustList(t *testing.T, st *Store, status, repo string) []Task {
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

	a, _ := st.Add(Task{Repo: "wed", Title: "a"}) // id 1
	b, _ := st.Add(Task{Repo: "wed", Title: "b"}) // id 2
	c, _ := st.Add(Task{Repo: "wed", Title: "c"}) // id 3
	// approve in the order c, a, b → b is the last completed, then a, then c
	for _, id := range []int64{c, a, b} {
		if _, err := st.SubmitReview(id, VerdictApprove, ""); err != nil {
			t.Fatalf("approve %d: %v", id, err)
		}
	}

	reloadTasks()
	var done []int64
	for _, tk := range tasks {
		if uiStore.ReviewState(tk.ID) == StateDone {
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
	tid, _ := st.Add(Task{Repo: "wed", Title: "t"})

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
	var reply *Comment
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
	cs := []Comment{
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
