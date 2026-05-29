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
