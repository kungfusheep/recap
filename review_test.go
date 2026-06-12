package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kungfusheep/recap/db"
)

// the full async review loop: draft comments -> submit (status flips) ->
// the work order reads back -> resolve.
func TestReviewLifecycle(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	id, err := st.Add(db.Task{Repo: "wed", RepoPath: "/tmp/wed", Title: "split editor", Status: db.StatusPending})
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

	drafts, _ := st.ListReviews(db.ReviewDraft, "")
	if len(drafts) != 1 {
		t.Fatalf("want 1 draft review, got %d", len(drafts))
	}

	// submit request_changes -> task flips to redo, review becomes submitted
	rv, err := st.SubmitReview(id, db.VerdictRequestChanges, "break the editor->app config edge")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rv.State != db.ReviewSubmitted || rv.Verdict != db.VerdictRequestChanges {
		t.Fatalf("review not submitted correctly: %+v", rv)
	}
	if rv.SubmittedAt == "" {
		t.Fatalf("submitted_at not stamped")
	}
	got, _ := st.Get(id)
	if got.Status != db.StatusRedo {
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
	if again.State != db.ReviewResolved {
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
		{db.VerdictApprove, db.StatusApproved},
		{db.VerdictRequestChanges, db.StatusRedo},
		{db.VerdictComment, db.StatusPending}, // non-blocking: status untouched
	}
	for _, tc := range cases {
		id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
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
	orig, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "original", Status: db.StatusRedo})
	fix, err := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "fix forward", Status: db.StatusPending, ParentID: orig})
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

// draft comments are editable/deletable individually; submitted ones are not.
func TestEditDeleteComment(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	c1, _ := st.AddReviewComment(id, "you", "first", "a.go", 1, "@@", "x")
	c2, _ := st.AddReviewComment(id, "you", "second", "a.go", 2, "@@", "y")

	// edit c1
	if err := st.UpdateComment(c1, "first edited"); err != nil {
		t.Fatalf("update: %v", err)
	}
	cs, _ := st.Comments(id)
	if cs[0].Body != "first edited" {
		t.Fatalf("edit didn't take: %q", cs[0].Body)
	}
	// empty body rejected
	if err := st.UpdateComment(c1, ""); err == nil {
		t.Fatal("expected error updating to empty body")
	}

	// delete c2 — c1 survives
	if err := st.DeleteComment(c2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	cs, _ = st.Comments(id)
	if len(cs) != 1 || cs[0].ID != c1 {
		t.Fatalf("delete removed the wrong comment: %+v", cs)
	}

	// once submitted, comments are immutable
	if _, err := st.SubmitReview(id, db.VerdictComment, "done"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := st.UpdateComment(c1, "too late"); err == nil {
		t.Fatal("expected error editing a submitted comment")
	}
	if err := st.DeleteComment(c1); err == nil {
		t.Fatal("expected error deleting a submitted comment")
	}
}

// unsubmit reverses a submitted review: AMENDS → INBOX, comments preserved and
// editable again.
func TestUnsubmitReview(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	cid, _ := st.AddReviewComment(id, "you", "fix this", "a.go", 1, "@@", "x")
	st.SubmitReview(id, db.VerdictRequestChanges, "")
	if got := st.ReviewState(id); got != db.StateRework {
		t.Fatalf("after submit: want rework, got %s", got)
	}

	if err := st.UnsubmitReview(id); err != nil {
		t.Fatalf("unsubmit: %v", err)
	}
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("after unsubmit: want pending(inbox), got %s", got)
	}
	// the comment is back on a draft and editable again
	if _, n, ok := st.DraftInfo(id); !ok || n != 1 {
		t.Fatalf("comment not returned to draft: ok=%v n=%d", ok, n)
	}
	if err := st.UpdateComment(cid, "fix this properly"); err != nil {
		t.Fatalf("comment should be editable after unsubmit: %v", err)
	}

	// nothing to unsubmit when there's no submitted review
	if err := st.UnsubmitReview(id); err == nil {
		t.Fatal("expected error unsubmitting with no submitted review")
	}
}

// derived state is the truth: drafts don't move a task, submit→rework,
// resolve→back to pending, approve→approved, and a direct approval is honoured.
func TestReviewStateDerivation(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})

	// fresh task: pending
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("fresh: want pending, got %s", got)
	}

	// a draft comment must NOT change state
	st.AddReviewComment(id, "you", "wip", "a.go", 1, "@@", "x")
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("draft present: want pending, got %s", got)
	}

	// submit request_changes → rework
	if _, err := st.SubmitReview(id, db.VerdictRequestChanges, "fix"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := st.ReviewState(id); got != db.StateRework {
		t.Fatalf("after request_changes: want rework, got %s", got)
	}

	// resolve → back to pending (addressed via fix-forward)
	rv, _ := st.ListReviews(db.ReviewSubmitted, "")
	if err := st.ResolveReview(rv[0].ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("after resolve: want pending, got %s", got)
	}

	// approve review → approved
	if _, err := st.SubmitReview(id, db.VerdictApprove, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := st.ReviewState(id); got != db.StateDone {
		t.Fatalf("after approve: want approved, got %s", got)
	}

	// a task approved directly (legacy flag, no review) is honoured
	id2, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t2", Status: db.StatusApproved})
	if got := st.ReviewState(id2); got != db.StateDone {
		t.Fatalf("direct approval: want approved, got %s", got)
	}
}

// the rework queue must be DERIVED from ReviewState, never read off the stale
// `status` flag. Resolving a review leaves status=='redo' behind (the flag is
// only set, never cleared), so a flag-driven `recap redo` showed resolved inboxUI.Tasks
// forever — the "why does this keep coming back to me?" phantom. This pins the
// divergence: after resolve, the flag still says redo but the derived state is
// pending, so the queue (which filters on ReviewState) excludes it.
func TestReworkQueueIsDerivedNotFlagged(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	st.AddReviewComment(id, "you", "fix this", "a.go", 1, "@@", "x")
	st.SubmitReview(id, db.VerdictRequestChanges, "")

	// while submitted: both agree it needs rework.
	if got, _ := st.Get(id); got.Status != db.StatusRedo {
		t.Fatalf("submit should set the legacy flag to redo, got %q", got.Status)
	}
	if got := st.ReviewState(id); got != db.StateRework {
		t.Fatalf("submitted request_changes: want rework, got %s", got)
	}

	rv, _ := st.ListReviews(db.ReviewSubmitted, "")
	st.ResolveReview(rv[0].ID)

	// the divergence: the flag is stale, the derived state is the truth.
	if got, _ := st.Get(id); got.Status != db.StatusRedo {
		t.Fatalf("resolve intentionally leaves the legacy flag at redo, got %q", got.Status)
	}
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("resolved task must derive to pending (out of the rework queue), got %s", got)
	}
}

// resolveSHA must pin a ref (HEAD, branch, short hash) to a concrete commit hash,
// so a recorded review never stores the literal "HEAD" (which `git show` would
// always resolve to the current tip, making every diff drift to the latest commit).
func TestResolveSHA(t *testing.T) {
	dir := t.TempDir()
	git(dir, "init")
	git(dir, "config", "user.email", "t@t")
	git(dir, "config", "user.name", "t")
	if err := writeFileT(dir+"/a.txt", "a\n"); err != nil {
		t.Fatal(err)
	}
	git(dir, "add", "-A")
	git(dir, "commit", "-m", "first")

	got, err := resolveSHA(dir, "HEAD")
	if err != nil {
		t.Fatalf("resolveSHA: %v", err)
	}
	if got == "" || got == "HEAD" {
		t.Fatalf("HEAD not resolved to a concrete hash, got %q", got)
	}
	// it equals the real short head, and stays fixed when a new commit lands
	head, _ := gitShortHead(dir)
	if got != head {
		t.Fatalf("resolveSHA(HEAD)=%q, want %q", got, head)
	}
	if err := writeFileT(dir+"/b.txt", "b\n"); err != nil {
		t.Fatal(err)
	}
	git(dir, "add", "-A")
	git(dir, "commit", "-m", "second")
	if again, _ := resolveSHA(dir, got); again != got {
		t.Fatalf("a pinned hash should resolve to itself, got %q want %q", again, got)
	}
}

func writeFileT(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

// a task accumulates revisions (fix-forward diffs) instead of spawning child
// inboxUI.Tasks: Revisions returns the synthetic base (the task's own SHA) first, then
// each appended revision oldest-first, with the latest diff last.
func TestRevisions(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", SHA: "base000", Title: "t", Summary: "original", Status: db.StatusPending})

	// a fresh task has exactly the base revision
	revs, err := st.Revisions(id)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revs) != 1 || !revs[0].Base || revs[0].SHA != "base000" {
		t.Fatalf("want one base revision base000, got %+v", revs)
	}

	// append two fix-forward revisions
	if _, err := st.AddRevision(id, "fix111", "first fix"); err != nil {
		t.Fatalf("add rev 1: %v", err)
	}
	if _, err := st.AddRevision(id, "fix222", "second fix"); err != nil {
		t.Fatalf("add rev 2: %v", err)
	}

	revs, _ = st.Revisions(id)
	if len(revs) != 3 {
		t.Fatalf("want 3 revisions, got %d", len(revs))
	}
	// order: base, then appended oldest-first; latest is last
	if revs[0].SHA != "base000" || !revs[0].Base {
		t.Fatalf("revs[0] should be the base, got %+v", revs[0])
	}
	if revs[1].SHA != "fix111" || revs[2].SHA != "fix222" {
		t.Fatalf("revision order wrong: %+v", revs)
	}
	if revs[2].Base {
		t.Fatalf("appended revision should not be flagged Base")
	}
	// the latest diff is the last element
	if latest := revs[len(revs)-1]; latest.SHA != "fix222" {
		t.Fatalf("latest = %q, want fix222", latest.SHA)
	}

	// an empty sha is rejected; a missing task errors
	if _, err := st.AddRevision(id, "", "x"); err == nil {
		t.Fatal("empty sha should be rejected")
	}
	if _, err := st.AddRevision(9999, "abc", "x"); err == nil {
		t.Fatal("revision on a missing task should error")
	}
}

// revising an amended task returns it to the inbox as ONE item: appending a
// revision + resolving the open request_changes review derives the state back to
// pending, with the new diff in its history — no separate child task.
func TestReviseReturnsToInbox(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", SHA: "base000", Title: "t", Status: db.StatusPending})
	st.AddReviewComment(id, "you", "fix this", "a.go", 1, "@@", "x")
	st.SubmitReview(id, db.VerdictRequestChanges, "needs work")
	if got := st.ReviewState(id); got != db.StateRework {
		t.Fatalf("after submit: want rework, got %s", got)
	}

	// the revise flow: append the fix-forward diff, then resolve the open review
	if _, err := st.AddRevision(id, "fix111", "addressed it"); err != nil {
		t.Fatalf("add revision: %v", err)
	}
	resolved, err := st.ResolveOpenRequestChanges(id)
	if err != nil {
		t.Fatalf("resolve open: %v", err)
	}
	if resolved == 0 {
		t.Fatal("expected an open request_changes review to resolve")
	}

	// same task, back in the inbox (pending), now carrying two diffs
	if got := st.ReviewState(id); got != db.StatePending {
		t.Fatalf("after revise: want pending(inbox), got %s", got)
	}
	// and the legacy status flag tracks, so flag-based `recap ls` agrees
	if got, _ := st.Get(id); got.Status != db.StatusPending {
		t.Fatalf("after revise: legacy status should be pending, got %q", got.Status)
	}
	revs, _ := st.Revisions(id)
	if len(revs) != 2 || revs[1].SHA != "fix111" {
		t.Fatalf("want base+fix revisions, got %+v", revs)
	}
	// the original feedback is still visible (so you can recontextualise)
	if cs, _ := st.TaskReviewComments(id); len(cs) != 1 || cs[0].Body != "fix this" {
		t.Fatalf("original comment should persist, got %+v", cs)
	}

	// with no open request_changes, resolveOpenRequestChanges is a no-op (0)
	again, err := st.ResolveOpenRequestChanges(id)
	if err != nil || again != 0 {
		t.Fatalf("second resolve should be a no-op, got id=%d err=%v", again, err)
	}
}

// Delete removes a task and everything scoped to it (comments + reviews) and
// detaches fix-forward children so they don't dangle at a deleted parent.
func TestDeleteTask(t *testing.T) {
	st := testStore(t)
	defer st.Close()

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "doomed", Status: db.StatusPending})
	st.AddReviewComment(id, "you", "a note", "a.go", 1, "@@", "x")
	st.SubmitReview(id, db.VerdictRequestChanges, "fix it")
	child, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "fix", Status: db.StatusPending, ParentID: id})

	if err := st.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// the task is gone
	if _, err := st.Get(id); err == nil {
		t.Fatal("task should be gone after delete")
	}
	// its comments and reviews are gone
	if cs, _ := st.Comments(id); len(cs) != 0 {
		t.Fatalf("comments survived delete: %d", len(cs))
	}
	if rs, _ := st.ListReviews("", ""); len(rs) != 0 {
		t.Fatalf("reviews survived delete: %d", len(rs))
	}
	// the fix-forward child survives but is detached (no dangling parent)
	gotChild, err := st.Get(child)
	if err != nil {
		t.Fatalf("child should survive: %v", err)
	}
	if gotChild.ParentID != 0 {
		t.Fatalf("child parent_id should be cleared, got %d", gotChild.ParentID)
	}
	// deleting a non-existent task errors (no silent no-op)
	if err := st.Delete(9999); err == nil {
		t.Fatal("deleting a missing task should error")
	}
}

// ListReviews can scope to a repo, so the loop only drains its own reviews.
func TestListReviewsByRepo(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	a, _ := st.Add(db.Task{Repo: "alpha", RepoPath: "/tmp/alpha", Title: "a", Status: db.StatusPending})
	b, _ := st.Add(db.Task{Repo: "beta", RepoPath: "/tmp/beta", Title: "b", Status: db.StatusPending})
	st.SubmitReview(a, db.VerdictRequestChanges, "fix a")
	st.SubmitReview(b, db.VerdictRequestChanges, "fix b")

	all, _ := st.ListReviews(db.ReviewSubmitted, "")
	if len(all) != 2 {
		t.Fatalf("unscoped: want 2 reviews, got %d", len(all))
	}
	onlyAlpha, _ := st.ListReviews(db.ReviewSubmitted, "alpha")
	if len(onlyAlpha) != 1 || onlyAlpha[0].TaskID != a {
		t.Fatalf("repo scope alpha = %+v, want just task %d", onlyAlpha, a)
	}
	if none, _ := st.ListReviews(db.ReviewSubmitted, "gamma"); len(none) != 0 {
		t.Fatalf("repo scope gamma: want 0, got %d", len(none))
	}
}

// discarding a draft removes it and its comments.
func TestDiscardReview(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	st.AddReviewComment(id, "you", "wip note", "", 0, "", "")
	if err := st.DiscardReview(id); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if drafts, _ := st.ListReviews(db.ReviewDraft, ""); len(drafts) != 0 {
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

// the read-receipt lands where the READING happens: `review show` stamps the
// comments it prints; resolve marks NOTHING — so a ruling added after the agent
// read the work order stays unread and gets served on the next `recap next`
// (the c435 fix: the old resolve blanket-mark silently swallowed late comments).
func TestReadReceiptLandsAtReviewShow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(tmp, "recap.db"))
	st, err := db.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	prev := uiStore
	uiStore = st
	t.Cleanup(func() { uiStore = prev })

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	st.AddReviewComment(id, "you", "fix A", "f.go", 1, "@@", "x")
	st.AddReviewComment(id, "you", "fix B", "f.go", 2, "@@", "y")
	rv, err := st.SubmitReview(id, db.VerdictRequestChanges, "changes")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if un, _ := st.UnreadByAgent(""); len(un) != 2 {
		t.Fatalf("both comments start unread, got %d", len(un))
	}

	// the agent reads the work order → the printed comments get their receipt
	if err := cmdReviewShow([]string{fmt.Sprint(rv.ID)}); err != nil {
		t.Fatalf("review show: %v", err)
	}
	if un, _ := st.UnreadByAgent(""); len(un) != 0 {
		t.Fatalf("review show should stamp the printed comments, %d still unread", len(un))
	}

	// a ruling lands AFTER the agent read the order, BEFORE the revise/resolve
	st.AddReviewComment(id, "you", "also: make it global", "", 0, "", "")
	if err := st.ResolveReview(rv.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	un, _ := st.UnreadByAgent("")
	if len(un) != 1 || un[0].Body != "also: make it global" {
		t.Fatalf("the late ruling must SURVIVE resolve unread (so next serves it), got %+v", un)
	}
}

// TODO-template + AppendTODO behaviour moved to the config package
// (config/config_test.go) when config was extracted to its own package.
