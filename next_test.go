package main

import (
	"os"
	"strings"
	"testing"
)

// markTodoLineDone must flip ONLY the named open line (surgical), leaving headers,
// prose, other todos, and already-done lines byte-for-byte intact — recap done runs
// against the user's real TODO, so a stray reformat is unacceptable.
func TestMarkTodoLineDone(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/TODO.md"
	seed := "# TODO\n\nsome prose line\n- [ ] first task\n- [ ] second task\n- [x] already done\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := markTodoLineDone(path, "second task"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)

	if !strings.Contains(s, "- [x] second task  done ") {
		t.Fatalf("second task not marked done:\n%s", s)
	}
	// everything else untouched
	for _, keep := range []string{"# TODO", "some prose line", "- [ ] first task", "- [x] already done"} {
		if !strings.Contains(s, keep) {
			t.Fatalf("unrelated line %q was altered:\n%s", keep, s)
		}
	}
	if strings.Contains(s, "- [x] first task") {
		t.Fatalf("flipped the wrong line:\n%s", s)
	}
	// a line that isn't an open match errors (don't silently no-op)
	if err := markTodoLineDone(path, "nonexistent"); err == nil {
		t.Fatal("expected an error for a missing/closed todo")
	}
}

// advance is the cursor: it walks forward through the live queue, wraps at the end,
// reports a skip when the current item is still present (passed without completing),
// and restarts at the top when the current is gone (completed) or unset.
func TestAdvance(t *testing.T) {
	q := []WorkItem{
		{Ref: "amends:1", Title: "a"},
		{Ref: "reply:2", Title: "b"},
		{Ref: "todo:3", Title: "c"},
	}

	// no current → first item, not a skip
	got, skipped, ok := advance(q, "")
	if !ok || skipped || got.Ref != "amends:1" {
		t.Fatalf("empty cursor: got %q skip=%v ok=%v", got.Ref, skipped, ok)
	}

	// current still present → advance to the next AND flag the skip
	got, skipped, ok = advance(q, "amends:1")
	if !ok || !skipped || got.Ref != "reply:2" {
		t.Fatalf("from amends:1: got %q skip=%v", got.Ref, skipped)
	}

	// wrap at the end
	got, _, _ = advance(q, "todo:3")
	if got.Ref != "amends:1" {
		t.Fatalf("wrap: got %q, want amends:1", got.Ref)
	}

	// current gone from queue (completed) → restart at top, NOT a skip
	got, skipped, _ = advance(q, "amends:99")
	if skipped || got.Ref != "amends:1" {
		t.Fatalf("completed cursor: got %q skip=%v", got.Ref, skipped)
	}

	// empty queue → nothing
	if _, _, ok := advance(nil, "x"); ok {
		t.Fatalf("empty queue should report nothing to do")
	}
}

// buildQueue orders work amends → replies → todos, scopes to the repo, and dedups a
// reply whose task is already an amends item (it rides with the amends work order).
func TestBuildQueuePriority(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	// amends task (this repo) — a submitted request_changes review → rework
	amend, _ := st.Add(Task{Repo: "wed", RepoPath: "/tmp/wed", Title: "amends one", Status: StatusPending})
	if _, err := st.SubmitReview(amend, VerdictRequestChanges, "fix it"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// a separate task with an unread reviewer reply (this repo)
	chat, _ := st.Add(Task{Repo: "wed", RepoPath: "/tmp/wed", Title: "chat task", Status: StatusPending})
	parent, _ := st.AddComment(chat, "agent", "here's what I did")
	if _, err := st.AddReply(parent, "you", "but did you check X?"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	// a reply on the AMENDS task — should be deduped (rides with the amends item)
	ap, _ := st.AddComment(amend, "agent", "note")
	st.AddReply(ap, "you", "this is on the amends task")

	// another repo's amends — must NOT appear (scoping)
	other, _ := st.Add(Task{Repo: "tui", RepoPath: "/tmp/tui", Title: "other repo", Status: StatusPending})
	st.SubmitReview(other, VerdictRequestChanges, "nope")

	q := buildQueue(st, "wed", "") // repoPath "" → skip the todo tier

	if len(q) != 2 {
		t.Fatalf("want 2 items (amends + 1 reply), got %d: %+v", len(q), q)
	}
	if q[0].Kind != "amends" || q[0].TaskID != amend {
		t.Fatalf("amends must come first, got %+v", q[0])
	}
	if q[1].Kind != "reply" || q[1].TaskID != chat {
		t.Fatalf("reply (non-amends task) must be second, got %+v", q[1])
	}
}
