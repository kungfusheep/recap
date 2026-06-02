package main

import (
	"os"
	"strings"
	"testing"
)

// the full async review loop: draft comments -> submit (status flips) ->
// the work order reads back -> resolve.
func TestReviewLifecycle(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	id, err := st.Add(Task{Repo: "wed", RepoPath: "/tmp/wed", Title: "split editor", Status: StatusPending})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// two line-anchored comments accumulate into one draft review
	if _, err := st.AddReviewComment(id, "you", "layering violation", "editor.go", 12, "@@ -10,7 +10,9 @@", `import ".../config"`); err != nil {
		t.Fatalf("comment 1: %v", err)
	}
	if _, err := st.AddReviewComment(id, "you", "app-owned", "keybindings.go", 90, "@@ -88,4 +88,6 @@", "app.HandleNamed(...)"); err != nil {
		t.Fatalf("comment 2: %v", err)
	}

	drafts, _ := st.ListReviews(ReviewDraft)
	if len(drafts) != 1 {
		t.Fatalf("want 1 draft review, got %d", len(drafts))
	}

	// submit request_changes -> task flips to redo, review becomes submitted
	rv, err := st.SubmitReview(id, VerdictRequestChanges, "break the editor->app config edge")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rv.State != ReviewSubmitted || rv.Verdict != VerdictRequestChanges {
		t.Fatalf("review not submitted correctly: %+v", rv)
	}
	if rv.SubmittedAt == "" {
		t.Fatalf("submitted_at not stamped")
	}
	got, _ := st.Get(id)
	if got.Status != StatusRedo {
		t.Fatalf("want task redo after request_changes, got %q", got.Status)
	}

	// the work order: comments survive with their anchors intact
	cs, _ := st.ReviewComments(rv.ID)
	if len(cs) != 2 {
		t.Fatalf("want 2 review comments, got %d", len(cs))
	}
	if cs[0].File != "editor.go" || cs[0].Line != 12 || cs[0].Anchor == "" || cs[0].Snippet == "" {
		t.Fatalf("anchor lost: %+v", cs[0])
	}

	// resolve after a (hypothetical) fix-forward
	if err := st.ResolveReview(rv.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	again, _ := st.GetReview(rv.ID)
	if again.State != ReviewResolved {
		t.Fatalf("want resolved, got %q", again.State)
	}
}

// approve and the non-blocking comment verdict drive task status differently.
func TestSubmitVerdictStatus(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	cases := []struct {
		verdict string
		want    string
	}{
		{VerdictApprove, StatusApproved},
		{VerdictRequestChanges, StatusRedo},
		{VerdictComment, StatusPending}, // non-blocking: status untouched
	}
	for _, tc := range cases {
		id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
		if _, err := st.SubmitReview(id, tc.verdict, "note"); err != nil {
			t.Fatalf("%s submit: %v", tc.verdict, err)
		}
		got, _ := st.Get(id)
		if got.Status != tc.want {
			t.Fatalf("verdict %s: want status %q, got %q", tc.verdict, tc.want, got.Status)
		}
	}

	if _, err := st.SubmitReview(1, "bogus", ""); err == nil {
		t.Fatalf("expected error on invalid verdict")
	}
}

// a fix-forward task links back to the one it fixes, so recap can walk lineage.
func TestParentLineage(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	orig, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "original", Status: StatusRedo})
	fix, err := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "fix forward", Status: StatusPending, ParentID: orig})
	if err != nil {
		t.Fatalf("add fix: %v", err)
	}
	got, _ := st.Get(fix)
	if got.ParentID != orig {
		t.Fatalf("want parent %d, got %d", orig, got.ParentID)
	}
	// a root task has no parent
	gotOrig, _ := st.Get(orig)
	if gotOrig.ParentID != 0 {
		t.Fatalf("want no parent on root, got %d", gotOrig.ParentID)
	}
}

// discarding a draft removes it and its comments.
func TestDiscardReview(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	st.AddReviewComment(id, "you", "wip note", "", 0, "", "")
	if err := st.DiscardReview(id); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if drafts, _ := st.ListReviews(ReviewDraft); len(drafts) != 0 {
		t.Fatalf("want 0 drafts after discard, got %d", len(drafts))
	}
	// the comment went with it (review comments are scoped to the review)
	cs, _ := st.Comments(id)
	for _, c := range cs {
		if c.ReviewID != 0 {
			t.Fatalf("review comment survived discard: %+v", c)
		}
	}
}

// the TODO template resolves {relpath} relative to home and the breadcrumb
// carries the review id so the agent can expand it.
func TestTODOTemplateAndBreadcrumb(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := Config{TODOTemplate: "~/notes/{relpath}/TODO.md"}
	repo := home + "/code/wed"
	path, err := cfg.todoPathFor(repo)
	if err != nil {
		t.Fatalf("todoPathFor: %v", err)
	}
	want := home + "/notes/code/wed/TODO.md"
	if path != want {
		t.Fatalf("want %q, got %q", want, path)
	}

	if err := appendTODO(path, "- [ ] one"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := appendTODO(path, todoBreadcrumb(Review{ID: 7, Summary: "fix the edge"}, Task{Title: "x"})); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	data := readFile(t, path)
	if !strings.Contains(data, "- [ ] one\n") {
		t.Fatalf("first line missing:\n%s", data)
	}
	if !strings.Contains(data, "recap review show 7") {
		t.Fatalf("breadcrumb missing review id:\n%s", data)
	}

	// empty template disables writing
	if p, _ := (Config{}).todoPathFor(repo); p != "" {
		t.Fatalf("want empty path with no template, got %q", p)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
