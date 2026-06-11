package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// selecting a draft comment scrolls the diff layer to the line it's anchored to.
func TestSyncDiffToDraft(t *testing.T) {
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() { diffUI.Layer = nil; diffUI.Meta = nil; draftUI.Comments = nil; draftUI.Sel = 0 })

	// a diff buffer tall enough to scroll, with known anchor rows.
	diffUI.Meta = []diffLineMeta{
		{}, // 0 file header
		{File: "a.go", Line: 1, Commentable: true},  // 1
		{File: "a.go", Line: 2, Commentable: true},  // 2
		{File: "b.go", Line: 10, Commentable: true}, // 3
		{File: "b.go", Line: 11, Commentable: true}, // 4
	}
	buf := NewBuffer(40, len(diffUI.Meta)+50) // taller than viewport so maxScroll>0
	diffUI.Layer.SetBuffer(buf)
	diffUI.Layer.SetViewport(40, 3)

	draftUI.Comments = []draftCommentVM{
		{File: "b.go", Line: 11},
		{File: "a.go", Line: 2},
	}

	draftUI.Sel = 0
	syncDiffToDraft()
	if got := diffUI.Layer.ScrollY(); got != 4 {
		t.Fatalf("b.go:11 should scroll to row 4, got %d", got)
	}

	draftUI.Sel = 1
	syncDiffToDraft()
	if got := diffUI.Layer.ScrollY(); got != 2 {
		t.Fatalf("a.go:2 should scroll to row 2, got %d", got)
	}

	// a general (unanchored) comment leaves scroll untouched
	draftUI.Comments = []draftCommentVM{{File: ""}}
	draftUI.Sel = 0
	before := diffUI.Layer.ScrollY()
	syncDiffToDraft()
	if diffUI.Layer.ScrollY() != before {
		t.Fatalf("general comment should not scroll the diff")
	}
}

// the draft-review overview pane is conditional + data-driven: loadDraftPane
// must (a) hide the pane when there's no draft, (b) show it with one VM per
// draft comment, carrying location + snippet + body, when there is.
func TestLoadDraftPane(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil })

	id, err := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// no draft yet → pane hidden, no rows, no hint
	loadDraftPane(id)
	if draftUI.Has || len(draftUI.Comments) != 0 || draftUI.Note != "" {
		t.Fatalf("expected empty draft state, got draftUI.Has=%v rows=%d note=%q", draftUI.Has, len(draftUI.Comments), draftUI.Note)
	}

	// a line-anchored comment and a general one accumulate into the draft
	if _, err := st.AddReviewComment(id, "you", "needs a test", "calc.go", 3, "@@", "func sub(){}"); err != nil {
		t.Fatalf("comment 1: %v", err)
	}
	if _, err := st.AddReviewComment(id, "you", "overall nit", "", 0, "", ""); err != nil {
		t.Fatalf("comment 2: %v", err)
	}

	loadDraftPane(id)
	if !draftUI.Has {
		t.Fatal("expected draftUI.Has=true once comments exist")
	}
	if draftUI.Note != "✎ 2 draft" {
		t.Fatalf("draftUI.Note = %q, want \"✎ 2 draft\"", draftUI.Note)
	}
	if len(draftUI.Comments) != 2 {
		t.Fatalf("want 2 draft rows, got %d", len(draftUI.Comments))
	}
	// general (unanchored) row sorts first, falls back to "general", no snippet
	if draftUI.Comments[0].Location != "general" || draftUI.Comments[0].Snippet != "" {
		t.Errorf("row0 = %+v, want general/no-snippet", draftUI.Comments[0])
	}
	// the line-anchored row follows, carrying location + snippet
	if draftUI.Comments[1].Location != "calc.go · line 3" {
		t.Errorf("row1 location = %q", draftUI.Comments[1].Location)
	}
	if draftUI.Comments[1].Snippet != "func sub(){}" || draftUI.Comments[1].Body != "needs a test" {
		t.Errorf("row1 snippet/body = %q / %q", draftUI.Comments[1].Snippet, draftUI.Comments[1].Body)
	}

	// the two are draft (editable) before submit
	if !draftUI.Comments[0].Draft {
		t.Fatalf("comments should be Draft before submit")
	}

	// after submit the comments PERSIST (read-only) — the pane stays, feedback
	// remains visible, but rows are no longer Draft.
	if _, err := st.SubmitReview(id, db.VerdictComment, "fyi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	loadDraftPane(id)
	if !draftUI.Has || len(draftUI.Comments) != 2 {
		t.Fatalf("comments should persist after submit, got draftUI.Has=%v rows=%d", draftUI.Has, len(draftUI.Comments))
	}
	for _, c := range draftUI.Comments {
		if c.Draft {
			t.Fatalf("submitted comments should not be Draft: %+v", c)
		}
	}
	if draftUI.Note != "2 comments" {
		t.Fatalf("settled note = %q, want \"2 comments\"", draftUI.Note)
	}
}

// loadDraftPane also records which diff lines carry a comment (the gutter cue)
// and threads the comment id through for edit/delete.
func TestCommentedLinesCue(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; clear(diffUI.Commented); draftUI.Comments = nil })

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	cid, _ := st.AddReviewComment(id, "you", "note", "main.go", 12, "@@", "x := 1")
	st.AddReviewComment(id, "you", "general note", "", 0, "", "")

	loadDraftPane(id)
	if !diffUI.Commented[lineKey("main.go", 12)] {
		t.Fatalf("expected main.go:12 marked as commented, got %v", diffUI.Commented)
	}
	if len(diffUI.Commented) != 1 {
		t.Fatalf("only anchored comments should mark lines, got %d", len(diffUI.Commented))
	}
	// general sorts first, so the anchored comment (cid) is row 1; assert the VM
	// carries the right id regardless of position.
	var found bool
	for _, c := range draftUI.Comments {
		if c.ID == cid {
			found = true
		}
	}
	if !found {
		t.Fatalf("VM should carry comment id %d, got %+v", cid, draftUI.Comments)
	}
}

// the draft pane lists general (top-level) comments first, then anchored ones
// grouped by file, then line.
func TestDraftPaneOrdering(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; clear(diffUI.Commented); draftUI.Comments = nil; draftUI.Sel = 0 })

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	// add deliberately out of order
	st.AddReviewComment(id, "you", "general", "", 0, "", "")
	st.AddReviewComment(id, "you", "b high", "b.go", 40, "@@", "x")
	st.AddReviewComment(id, "you", "a low", "a.go", 5, "@@", "y")
	st.AddReviewComment(id, "you", "b low", "b.go", 9, "@@", "z")

	loadDraftPane(id)
	got := make([]string, len(draftUI.Comments))
	for i, c := range draftUI.Comments {
		got[i] = c.Location
	}
	want := []string{"general", "a.go · line 5", "b.go · line 9", "b.go · line 40"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// a fix-forward task awaiting review gets the re-review flag; once it's been
// re-reviewed (kicked back to amends, or approved) the flag clears.
func TestReReviewFlag(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	orig, _ := st.Add(db.Task{Repo: "recap", RepoPath: "/tmp/r", Title: "orig", Status: db.StatusRedo})
	fix, _ := st.Add(db.Task{Repo: "recap", RepoPath: "/tmp/r", Title: "fix", Status: db.StatusPending, ParentID: orig})
	st.Add(db.Task{Repo: "recap", RepoPath: "/tmp/r", Title: "fresh", Status: db.StatusPending}) // net-new, no parent

	inboxUI.RepoFilter = "recap"
	t.Cleanup(func() { inboxUI.RepoFilter = "" })
	reloadTasks()

	byID := map[int64]taskVM{}
	for _, vm := range inboxUI.Rows {
		byID[vm.ID] = vm
	}
	if !byID[fix].ReReview {
		t.Fatalf("fix task (parent, pending) should be flagged re-review: %+v", byID[fix])
	}
	if byID[fix].ReReviewPill == "" {
		t.Fatalf("re-review pill text missing")
	}
	if byID[orig].ReReview {
		t.Fatalf("non-parented task should not be re-review")
	}

	// once the fix is approved, it leaves the inbox and the re-review flag clears.
	st.SubmitReview(fix, db.VerdictApprove, "")
	reloadTasks()
	for _, vm := range inboxUI.Rows {
		if vm.ID == fix && vm.ReReview {
			t.Fatalf("approved fix should no longer be re-review")
		}
	}
}

// the preview banner: an AMENDS task leads with the submitted review (summary +
// comments); a fix-forward task gets an "amends review #N" header.
func TestBuildBanner(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil })

	orig, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "orig", Status: db.StatusPending})
	st.AddReviewComment(orig, "you", "tighten this", "a.go", 5, "@@", "x")
	rv, _ := st.SubmitReview(orig, db.VerdictRequestChanges, "needs work on a.go")

	// AMENDS task → banner leads with the review (summary + comment), withComments.
	ot, _ := st.Get(orig)
	b := buildBanner(ot)
	flat := flattenSpans(b)
	if !contains2(flat, "changes requested") || !contains2(flat, "needs work on a.go") || !contains2(flat, "tighten this") {
		t.Fatalf("amends banner missing summary/comment: %q", flat)
	}

	// fix-forward task → stacks BOTH the original review (header + summary +
	// original comments, so you can recontextualise) AND "what changed" (my fix
	// summary), above the new diff.
	fix, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "fix", Status: db.StatusPending, ParentID: orig,
		Summary: "rewrote a.go to use the slice form"})
	ft, _ := st.Get(fix)
	ff := flattenSpans(buildBanner(ft))
	if !contains2(ff, fmt.Sprintf("amends review #%d", rv.ID)) {
		t.Fatalf("fix banner missing the ↩ header: %q", ff)
	}
	if !contains2(ff, "needs work on a.go") {
		t.Fatalf("fix banner missing the original review summary: %q", ff)
	}
	if !contains2(ff, "tighten this") {
		t.Fatalf("fix banner missing the ORIGINAL comments (needed to recontextualise): %q", ff)
	}
	if !contains2(ff, "what changed") || !contains2(ff, "slice form") {
		t.Fatalf("fix banner missing my 'what changed' summary: %q", ff)
	}

	// ordinary inbox item with no summary → no banner.
	plain, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "plain", Status: db.StatusPending})
	pt, _ := st.Get(plain)
	if b := buildBanner(pt); b != nil {
		t.Fatalf("plain task should have no banner, got %d rows", len(b))
	}

	// inbox item WITH a reviewer briefing → summary banner.
	briefed, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "briefed", Status: db.StatusPending,
		Summary: "swapped the parser to a streaming one; watch the EOF edge case"})
	bt, _ := st.Get(briefed)
	sb := flattenSpans(buildBanner(bt))
	if !contains2(sb, "summary") || !contains2(sb, "streaming one") || !contains2(sb, "EOF edge case") {
		t.Fatalf("summary banner missing briefing: %q", sb)
	}
}

// regression (review #25): the selected inbox row must NOT show a "> " marker —
// the full-width selection band is the only selection cue (mail style). The trap
// is that glyph's List treats Marker("") as "unset" and falls back to the default
// "> ", so suppression needs a non-empty blank marker. This verifies by RENDER
// (build → execute → read the buffer), not by trusting the marker string: it
// renders the real taskRow list both ways and asserts the production marker hides
// the ">" while the buggy Marker("") would show it (so the test can actually fail).
func TestSelectedRowHasNoCaretMarker(t *testing.T) {
	prevRows, prevSel := inboxUI.Rows, inboxUI.Sel
	t.Cleanup(func() { inboxUI.Rows = prevRows; inboxUI.Sel = prevSel })

	inboxUI.Rows = []taskVM{
		{ID: 1, IDText: "#1", Title: "first task", When: "10:00", Repo: "recap", State: db.StatePending, Selected: true},
		{ID: 2, IDText: "#2", Title: "second task", When: "10:01", Repo: "recap", State: db.StatePending},
	}
	inboxUI.Sel = 0

	render := func(marker string) string {
		node := List(&inboxUI.Rows).
			Selection(&inboxUI.Sel).
			Style(&listBaseStyle).
			SelectedStyle(Style{}).
			Marker(marker).
			Render(taskRow)
		tmpl := Build(node)
		buf := NewBuffer(48, 10)
		tmpl.Execute(buf, 48, 10)
		var b strings.Builder
		for y := 0; y < 10; y++ {
			b.WriteString(buf.GetLine(y))
			b.WriteByte('\n')
		}
		return b.String()
	}

	// sanity: the buggy form (what Marker("") compiles to) DOES render the caret,
	// so this assertion proves the test is capable of catching the regression.
	if !strings.Contains(render(""), ">") {
		t.Fatal("precondition failed: Marker(\"\") should fall back to the default \"> \" caret")
	}
	// the production form must NOT render a caret anywhere.
	if got := render("  "); strings.Contains(got, ">") {
		t.Fatalf("selected row still shows a \">\" marker:\n%s", got)
	}
}

// the header count is the INBOX (pending) count, not the whole task set
// (review/TODO: "rework (count) at the top should just be the inbox count").
func TestInboxCount(t *testing.T) {
	st := testStore(t)
	uiStore = st
	prevFltr := inboxUI.RepoFilter
	inboxUI.RepoFilter = ""
	t.Cleanup(func() {
		uiStore = nil
		inboxUI.Rows = nil
		inboxUI.Sel = 0
		inboxUI.RepoFilter = prevFltr
		inboxUI.Count = 0
	})

	// 3 pending (inbox), 1 amends (request_changes), 1 done (approved)
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "p1", Status: db.StatusPending})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "p2", Status: db.StatusPending})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "p3", Status: db.StatusPending})
	amends, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "a", Status: db.StatusPending})
	st.SubmitReview(amends, db.VerdictRequestChanges, "fix")
	done, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "d", Status: db.StatusPending})
	st.SubmitReview(done, db.VerdictApprove, "")

	reloadTasks()
	if inboxUI.Count != 3 {
		t.Fatalf("inbox count = %d, want 3 (only pending); total inboxUI.Tasks = %d", inboxUI.Count, len(inboxUI.Tasks))
	}
	if len(inboxUI.Tasks) != 5 {
		t.Fatalf("sanity: want 5 total inboxUI.Tasks, got %d", len(inboxUI.Tasks))
	}
}

// regression (review #28): a long comment in the draft pane must WRAP to the
// column width, not truncate at one line. Verified by render: a long body in a
// narrow column produces several non-empty body rows and keeps its trailing text.
func TestDraftCommentBodyWraps(t *testing.T) {
	prev, prevSel := draftUI.Comments, draftUI.Sel
	t.Cleanup(func() { draftUI.Comments = prev; draftUI.Sel = prevSel })

	long := "this is a deliberately long top-level comment that should wrap across several lines inside the narrow draft column instead of truncating at a single line in the available space"
	draftUI.Comments = []draftCommentVM{{ID: 1, Location: "general", Body: long, Visible: true}}
	draftUI.Sel = 0
	node := List(&draftUI.Comments).Selection(&draftUI.Sel).Style(&listBaseStyle).
		SelectedStyle(Style{}).Marker("  ").Render(draftRow)
	tmpl := Build(node)
	buf := NewBuffer(34, 16) // narrow, like the Grow(2) column — forces wrapping
	tmpl.Execute(buf, 34, 16)

	bodyLines, full := 0, ""
	for y := 0; y < 16; y++ {
		line := strings.TrimSpace(buf.GetLine(y))
		full += " " + line
		if line != "" && !strings.Contains(line, "general") {
			bodyLines++
		}
	}
	if bodyLines < 3 {
		t.Fatalf("body did not wrap: %d body lines (want >=3)", bodyLines)
	}
	if !strings.Contains(full, "available space") {
		t.Fatalf("wrapped body lost trailing text: %q", full)
	}
}

// regression (TODO: preview title padding): the three column headers must sit on
// the same row. The middle column is unfilled, so its top padding collapsed and
// the title rode one row higher than the left/right headers. Verified by render:
// build the real main view and assert all three title rows are equal.
func TestColumnHeadersAlign(t *testing.T) {
	prevApp, prevOmni, prevHasDraft := uiApp, omni, draftUI.Has
	prevRows, prevDrafts, prevTitle := inboxUI.Rows, draftUI.Comments, detailTitle
	t.Cleanup(func() {
		uiApp = prevApp
		omni = prevOmni
		draftUI.Has = prevHasDraft
		inboxUI.Rows = prevRows
		draftUI.Comments = prevDrafts
		detailTitle = prevTitle
	})

	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	detailTitle = "PREVIEWTITLE"
	draftUI.Has = true
	inboxUI.Rows = []taskVM{{ID: 1, Title: "a task", Repo: "recap", State: db.StatePending}}
	draftUI.Comments = []draftCommentVM{{Location: "general", Body: "x", Visible: true}}

	tmpl := Build(buildMain())
	buf := NewBuffer(120, 40)
	tmpl.Execute(buf, 120, 40)

	find := func(needle string) int {
		for y := 0; y < 40; y++ {
			if strings.Contains(buf.GetLine(y), needle) {
				return y
			}
		}
		return -1
	}
	left, mid, right := find("recap"), find("PREVIEWTITLE"), find("comments")
	if left < 0 || mid < 0 || right < 0 {
		t.Fatalf("a header didn't render: left=%d mid=%d right=%d", left, mid, right)
	}
	if !(left == mid && mid == right) {
		t.Fatalf("column headers misaligned: recap=%d preview=%d comments=%d", left, mid, right)
	}
}

// the o-to-expand mechanic: a task with >1 diff shows the latest by default and a
// ▸ N cue; pressing o (toggleExpand) splices one child row per revision (latest
// first), each carrying its own DiffSHA so selecting it shows that diff; pressing
// o again collapses. Single-diff tasks aren't expandable.
func TestRevisionExpand(t *testing.T) {
	st := testStore(t)
	prevStore, prevRows, prevSel := uiStore, inboxUI.Rows, inboxUI.Sel
	prevExpanded, prevByID, prevTasks, prevFltr := inboxUI.Expanded, inboxUI.TaskByID, inboxUI.Tasks, inboxUI.RepoFilter
	uiStore = st
	inboxUI.Expanded = map[int64]bool{}
	inboxUI.RepoFilter = ""
	t.Cleanup(func() {
		uiStore = prevStore
		inboxUI.Rows = prevRows
		inboxUI.Sel = prevSel
		inboxUI.Expanded = prevExpanded
		inboxUI.TaskByID = prevByID
		inboxUI.Tasks = prevTasks
		inboxUI.RepoFilter = prevFltr
	})

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", SHA: "base000", Title: "fix me", Status: db.StatusPending})
	st.AddRevision(id, "fix111", "first fix")

	// collapsed: a single header row showing the latest diff + a ▸ 2 cue
	reloadTasks()
	if len(inboxUI.Rows) != 1 {
		t.Fatalf("collapsed: want 1 row, got %d", len(inboxUI.Rows))
	}
	h := inboxUI.Rows[0]
	if h.RevIdx != -1 {
		t.Fatalf("header RevIdx should be -1, got %d", h.RevIdx)
	}
	if h.ExpandPill != "▸ 2" {
		t.Fatalf("expand cue = %q, want \"▸ 2\"", h.ExpandPill)
	}
	if h.DiffSHA != "fix111" {
		t.Fatalf("header should show the LATEST diff, got %q", h.DiffSHA)
	}

	// expand
	inboxUI.Sel = 0
	toggleExpand()
	if !inboxUI.Expanded[id] {
		t.Fatal("task should be marked expanded")
	}
	if len(inboxUI.Rows) != 3 {
		t.Fatalf("expanded: want header + 2 children = 3 rows, got %d", len(inboxUI.Rows))
	}
	if inboxUI.Rows[0].ExpandPill != "▾ 2" {
		t.Fatalf("expanded cue = %q, want \"▾ 2\"", inboxUI.Rows[0].ExpandPill)
	}
	// children are latest-first, each with its own diff sha
	c1, c2 := inboxUI.Rows[1], inboxUI.Rows[2]
	if c1.RevIdx != 1 || c1.DiffSHA != "fix111" || !strings.Contains(c1.RevLabel, "rev 1") || !strings.Contains(c1.RevLabel, "first fix") {
		t.Fatalf("child 1 wrong: %+v", c1)
	}
	if c2.RevIdx != 0 || c2.DiffSHA != "base000" || !strings.Contains(c2.RevLabel, "original") {
		t.Fatalf("child 2 (base) wrong: %+v", c2)
	}

	// render shows the child labels (verify the expansion is visible)
	node := List(&inboxUI.Rows).Selection(&inboxUI.Sel).Style(&listBaseStyle).
		SelectedStyle(Style{}).Marker("  ").Render(taskRow)
	tmpl := Build(node)
	buf := NewBuffer(80, 30)
	tmpl.Execute(buf, 80, 30)
	full := ""
	for y := 0; y < 30; y++ {
		full += buf.GetLine(y) + "\n"
	}
	if !strings.Contains(full, "first fix") || !strings.Contains(full, "original") {
		t.Fatalf("expanded render missing revision children:\n%s", full)
	}

	// collapse
	toggleExpand()
	if inboxUI.Expanded[id] {
		t.Fatal("task should be collapsed")
	}
	if len(inboxUI.Rows) != 1 {
		t.Fatalf("collapsed again: want 1 row, got %d", len(inboxUI.Rows))
	}

	// a single-diff task is not expandable
	id2, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", SHA: "solo", Title: "solo", Status: db.StatusPending})
	reloadTasks()
	for _, vm := range inboxUI.Rows {
		if vm.ID == id2 && vm.RevIdx < 0 && vm.ExpandPill != "" {
			t.Fatalf("single-diff task should have no expand cue, got %q", vm.ExpandPill)
		}
	}
}

func flattenSpans(rows [][]Span) string {
	var b strings.Builder
	for _, r := range rows {
		for _, s := range r {
			b.WriteString(s.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func contains2(hay, needle string) bool { return strings.Contains(hay, needle) }

func TestWrapText(t *testing.T) {
	got := wrapText("the quick brown fox jumps", 9)
	want := []string{"the quick", "brown fox", "jumps"}
	if len(got) != len(want) {
		t.Fatalf("wrap lines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	// newlines force paragraph breaks
	if g := wrapText("a\nb", 80); len(g) != 2 || g[0] != "a" || g[1] != "b" {
		t.Fatalf("newline handling: %v", g)
	}
}

// the focus ring includes the draft pane only when it's visible, so h/l/Tab
// never land on a pane that isn't on screen.
func TestPaneRingRespectsDraftVisibility(t *testing.T) {
	draftUI.Has = false
	t.Cleanup(func() { draftUI.Has = false; pane = paneList })
	if got := panes(); len(got) != 2 {
		t.Fatalf("no draft: want 2 panes, got %v", got)
	}
	draftUI.Has = true
	if got := panes(); len(got) != 3 || got[2] != paneDraft {
		t.Fatalf("with draft: want [list diff draft], got %v", got)
	}

	// stepping focus forward from diff reaches draft when present…
	pane = paneDiff
	focusNext()
	if pane != paneDraft {
		t.Fatalf("focusNext from diff = %q, want draft", pane)
	}
	// …and setPane refuses the draft pane when it's hidden
	draftUI.Has = false
	setPane(paneDraft)
	if pane == paneDraft {
		t.Fatal("setPane(draft) should be refused when draftUI.Has is false")
	}
}

// regression (review #26): the status icon must render its per-state colour, never
// the cyan repo tint. The earlier version of this test checked stateColor()'s
// return value — which was already correct — and so missed the REAL bug: taskRow
// passed the colour to .FG() by value, but List builds the row template once from
// a placeholder element, baking the zero colour; only *pointer*-bound values
// update per row, so every icon fell back to the inherited cyan cascade. This
// verifies by RENDER: it executes the real taskRow list and reads the icon cell's
// foreground out of the buffer, so a by-value regression fails it.
func TestStatusIconColorByRender(t *testing.T) {
	prevRows, prevSel := inboxUI.Rows, inboxUI.Sel
	t.Cleanup(func() { inboxUI.Rows = prevRows; inboxUI.Sel = prevSel })

	inboxUI.Rows = []taskVM{
		{ID: 1, Title: "pending", Repo: "recap", State: db.StatePending, Pending: true, Header: true},
		{ID: 2, Title: "rework", Repo: "recap", State: db.StateRework, Header: true},
		{ID: 3, Title: "done", Repo: "recap", State: db.StateDone, Header: true},
	}
	inboxUI.Sel = 0
	node := List(&inboxUI.Rows).Selection(&inboxUI.Sel).Style(&listBaseStyle).
		SelectedStyle(Style{}).Marker("  ").Render(taskRow)
	tmpl := Build(node)
	buf := NewBuffer(48, 18)
	tmpl.Execute(buf, 48, 18)

	cyan := repoPalette[0] // 0x6f8fa8, the wrong tint the icon used to fall back to
	want := map[rune]Color{
		'●': cBright,
		'↻': cDel,
		'✓': cSubtle,
	}
	seen := map[rune]bool{}
	for y := 0; y < 18; y++ {
		for x := 0; x < 48; x++ {
			c := buf.Get(x, y)
			exp, ok := want[c.Rune]
			if !ok {
				continue
			}
			seen[c.Rune] = true
			if c.Style.FG == cyan || c.Style.FG == cHunk {
				t.Fatalf("icon %q rendered cyan (%v)", string(c.Rune), c.Style.FG)
			}
			if c.Style.FG != exp {
				t.Fatalf("icon %q FG = %v, want %v", string(c.Rune), c.Style.FG, exp)
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected all 3 state icons rendered, saw %v", seen)
	}
}

// the omnibox caps its visible rows (MaxVisible) so a long command list scrolls
// rather than overflowing the screen (TODO "omnibox doesn't scroll … max height").
func TestOmniMaxVisible(t *testing.T) {
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() { uiApp = nil; omni = nil })
	_ = omni.View() // side effect: builds omni.list
	omni.list.Clear()
	tmpl := Build(VBox.Width(120)(omni.list))
	buf := NewBuffer(120, 80)
	tmpl.Execute(buf, 120, 80)
	var sb strings.Builder
	for y := 0; y < 80; y++ {
		sb.WriteString(buf.GetLine(y))
		sb.WriteByte('\n')
	}
	out := sb.String()
	total := len(omni.items)
	if total <= 10 {
		t.Skipf("need >10 commands to prove windowing (have %d)", total)
	}
	vis := 0
	for _, it := range omni.items {
		if it.Label != "" && strings.Contains(out, it.Label) {
			vis++
		}
	}
	if vis > 12 {
		t.Fatalf("MaxVisible(10) not bounding: %d of %d command labels rendered", vis, total)
	}
}

// hhmm pulls HH:MM out of a nowStamp ("2006-01-02 15:04:05") and degrades to ""
// for short/empty input, so a comment row with no time renders blank not garbled.
func TestHHMM(t *testing.T) {
	if got := hhmm("2026-06-04 09:45:05"); got != "09:45" {
		t.Fatalf("hhmm = %q, want 09:45", got)
	}
	if got := hhmm(""); got != "" {
		t.Fatalf("hhmm(empty) = %q, want empty", got)
	}
	if got := hhmm("2026-06-04"); got != "" {
		t.Fatalf("hhmm(date-only) = %q, want empty", got)
	}
}

// threadComments nests replies under their parent: a reply follows its parent,
// gets an "↳ who" location, an indent, and no repeated snippet. Top-level order is
// general-before-anchored, then file:line.
func TestThreadComments(t *testing.T) {
	in := []draftCommentVM{
		{ID: 1, Location: "main.go · line 5", File: "main.go", Line: 5, Snippet: "x"},
		{ID: 2, ParentID: 1, Who: "agent", Body: "fixed"},
		{ID: 3, Location: "general"}, // top-level general → sorts first
	}
	out := threadComments(in)
	if len(out) != 3 {
		t.Fatalf("got %d rows, want 3", len(out))
	}
	// general (id 3) first, then the anchored thread (1 then its reply 2)
	if out[0].ID != 3 || out[1].ID != 1 || out[2].ID != 2 {
		t.Fatalf("order = [%d %d %d], want [3 1 2]", out[0].ID, out[1].ID, out[2].ID)
	}
	reply := out[2]
	if reply.Location != "↳ agent" {
		t.Fatalf("reply location = %q, want ↳ agent", reply.Location)
	}
	if reply.Indent != "  " {
		t.Fatalf("reply indent = %q, want two spaces", reply.Indent)
	}
	if reply.Snippet != "" {
		t.Fatalf("reply should not carry a snippet, got %q", reply.Snippet)
	}
}

// a reply row renders its "↳ who" label and body indented in the comments pane.
func TestReplyRowRenders(t *testing.T) {
	c := draftCommentVM{Location: "↳ agent", Indent: "  ", When: "10:20", Body: "renamed it", Visible: true}
	tmpl := Build(VBox.Width(50)(draftRow(&c)))
	buf := NewBuffer(50, 6)
	tmpl.Execute(buf, 50, 6)
	var sb strings.Builder
	for y := 0; y < 6; y++ {
		sb.WriteString(buf.GetLine(y))
		sb.WriteByte('\n')
	}
	out := sb.String()
	if !strings.Contains(out, "↳ agent") || !strings.Contains(out, "renamed it") {
		t.Fatalf("reply row missing label/body:\n%s", out)
	}
}

// a general (unanchored) comment must show in the comments pane immediately after
// saving — saveGeneralComment has to mark the detail dirty or refreshDetail's
// early-return (selection unchanged) leaves the pane stale and the comment looks
// "lost" (it's in the db, just not displayed).
func TestGeneralCommentAppearsAfterSave(t *testing.T) {
	prev := uiStore
	prevLayer := diffUI.Layer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiStore = prev
		uiApp = nil
		omni = nil
		diffUI.Layer = prevLayer
		promptUI.Field = InputState{}
		draftUI.Comments = nil
		inboxUI.DetailDirty = false
	})
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	_ = id
	reloadTasks()
	inboxUI.Sel = 0
	uiApp.SetView(buildMain())
	onInboxSelChanged() // establishes LastSel/LastLen; no comments yet
	if len(draftUI.Comments) != 0 {
		t.Fatalf("precondition: expected 0 comments, got %d", len(draftUI.Comments))
	}

	promptUI.Field.Value = "a general note"
	saveGeneralComment()
	onInboxSelChanged() // selection unchanged → refreshes only if saveGeneralComment marked it dirty

	found := false
	for _, c := range draftUI.Comments {
		if c.Body == "a general note" && c.Location == "general" {
			found = true
		}
	}
	if !found {
		t.Fatalf("general comment not shown after save (lost): %+v", draftUI.Comments)
	}
}

// the detail briefing follows the selected row's revision: the header shows the
// latest revision's summary (so it updates when a revise lands), and selecting an
// older revision child shows that revision's own summary in full — not the
// original task summary, and not the truncated left-column label.
func TestSummaryFollowsSelectedRevision(t *testing.T) {
	prev, prevLayer := uiStore, diffUI.Layer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiStore = prev
		uiApp = nil
		omni = nil
		diffUI.Layer = prevLayer
		clear(inboxUI.Expanded)
		inboxUI.DetailDirty = false
	})
	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending, Summary: "original briefing"})
	if _, err := st.AddRevision(id, "deadbeef", "revised briefing"); err != nil {
		t.Fatalf("AddRevision: %v", err)
	}
	inboxUI.Expanded[id] = true
	reloadTasks()
	uiApp.SetView(buildMain())

	// rows: [header(-1), child rev1 "revised", child rev0 "original"]
	hdr, origChild := -1, -1
	for i, r := range inboxUI.Rows {
		if r.RevIdx < 0 {
			hdr = i
		} else if r.RevIdx == 0 {
			origChild = i
		}
	}
	if hdr < 0 || origChild < 0 {
		t.Fatalf("expected a header + an original-revision child row, got %+v", inboxUI.Rows)
	}

	inboxUI.Sel = hdr
	inboxUI.DetailDirty = true
	onInboxSelChanged()
	if b := flattenSpans(diffUI.Banner); !contains2(b, "revised briefing") {
		t.Fatalf("header should show the latest revision summary, banner=%q", b)
	}

	inboxUI.Sel = origChild
	inboxUI.DetailDirty = true
	onInboxSelChanged()
	if b := flattenSpans(diffUI.Banner); !contains2(b, "original briefing") {
		t.Fatalf("selecting rev 0 should show its own summary, banner=%q", b)
	}
}

// the inbox lists oldest-first so the newest pending task sits at the BOTTOM (work
// the queue front-to-back). DONE is newest-at-top, but the inbox must not be —
// guard against that regression.
func TestInboxOrderLatestAtBottom(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	a, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "oldest", Status: db.StatusPending})
	b, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "middle", Status: db.StatusPending})
	c, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "newest", Status: db.StatusPending})

	reloadTasks()
	var ids []int64
	for _, r := range inboxUI.Rows {
		if r.RevIdx < 0 && r.Pending {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) != 3 || ids[0] != a || ids[1] != b || ids[2] != c {
		t.Fatalf("inbox order = %v, want [%d %d %d] (oldest→newest, latest at bottom)", ids, a, b, c)
	}
}

// the comments pane can reply to the selected comment ('r'): saveReply threads a
// reply under it (who="you") and it shows nested in the pane.
func TestReplyToCommentFromPane(t *testing.T) {
	prev := uiStore
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	t.Cleanup(func() {
		uiStore = prev
		uiApp = nil
		draftUI.Comments = nil
		draftUI.Sel = 0
		promptUI = promptView{} // replyToComment OPENED the prompt — close it or it floats over later renders
		draftUI.ReplyingTo = 0
		statusMsg = ""
	})

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	parent, _ := st.AddReviewComment(id, "you", "the original", "", 0, "", "")
	loadDraftPane(id)
	draftUI.Sel = 0
	if selectedDraft() == nil || selectedDraft().ID != parent {
		t.Fatalf("setup: selected draft should be the parent comment")
	}

	replyToComment()
	if draftUI.ReplyingTo != parent {
		t.Fatalf("replyToComment should target the selected comment %d, got %d", parent, draftUI.ReplyingTo)
	}
	promptUI.Field.Value = "my reply"
	saveReply()

	cs, _ := st.Comments(id)
	var found *db.Comment
	for i := range cs {
		if cs[i].ParentID == parent && cs[i].Body == "my reply" {
			found = &cs[i]
		}
	}
	if found == nil {
		t.Fatalf("reply not threaded under the parent: %+v", cs)
	}
	if found.Who != "you" {
		t.Fatalf("TUI reply should be who=you, got %q", found.Who)
	}
}

// a reload that inserts new tasks ABOVE the selected one must keep the selection on
// the same task (by id), not the same index — otherwise the list jumps under the
// reader when items arrive (e.g. via recap add).
func TestReloadKeepsSelectionByTask(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; inboxUI.Rows = nil; inboxUI.Sel = 0 })

	// inbox is oldest-first, so a NEW task lands above older ones? no — newest at
	// bottom. To force insertion ABOVE the selection we approve the selected one's
	// elders... simpler: select a task, then add an OLDER-sorting item. Inbox sorts
	// by id asc, so a new id is always below. Instead, select the last (newest) and
	// confirm a new newer one pushes in below but selection stays on our task.
	a, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "a", Status: db.StatusPending})
	b, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "b", Status: db.StatusPending})
	reloadTasks()
	// select task b
	for i, r := range inboxUI.Rows {
		if r.ID == b {
			inboxUI.Sel = i
		}
	}
	selectedBefore := inboxUI.Rows[inboxUI.Sel].ID

	// a SIGUSR1-style reload after another task arrives must keep us on b
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "c", Status: db.StatusPending})
	reloadTasks()
	if inboxUI.Rows[inboxUI.Sel].ID != selectedBefore {
		t.Fatalf("selection jumped: was on task %d, now on %d", selectedBefore, inboxUI.Rows[inboxUI.Sel].ID)
	}
	_ = a
}

// the inbox (left) column must NOT change width when the comments column appears —
// it's percentage-sized, so only the middle column absorbs the right pane. With Grow
// it re-flowed 2/5 ↔ 2/7 of the screen on every selection change that toggled
// draftUI.Has, a distracting jump (#e4393fae).
func TestLeftColumnStableWhenDraftToggles(t *testing.T) {
	st := testStore(t)
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		inboxUI.Rows, draftUI.Comments = nil, nil
		draftUI.Has = false
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()

	tmpl := Build(buildMain())
	render := func() int {
		buf := NewBuffer(140, 40)
		tmpl.Execute(buf, 140, 40)
		return int(listPaneRef.W)
	}

	draftUI.Has = false
	wNoDraft := render()
	if wNoDraft <= 0 {
		t.Fatalf("left column width not captured: %d", wNoDraft)
	}

	draftUI.Has = true
	draftUI.Comments = []draftCommentVM{{Location: "general", Body: "x", Visible: true}}
	wDraft := render()

	if wNoDraft != wDraft {
		t.Fatalf("left column jumped when the comments pane appeared: %d → %d (should be stable)", wNoDraft, wDraft)
	}
}

// the diff summary header carries the writing agent's name (the task repo's identity)
// in its colour — "summary · Kestrel" — so a cross-repo inbox shows who did the work.
// No identity saved → header unchanged. (#8dfaf5e4)
func TestSummaryBannerShowsAgentName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	st := testStore(t)
	prevStore := uiStore
	uiStore = st
	t.Cleanup(func() { uiStore = prevStore })

	if err := saveIdentity("withname", "Kestrel", "#79C0FF"); err != nil {
		t.Fatal(err)
	}

	flat := func(rows [][]Span) string {
		s := ""
		for _, r := range rows {
			for _, sp := range r {
				s += sp.Text
			}
			s += "\n"
		}
		return s
	}

	named := db.Task{ID: 1, Repo: "withname", Summary: "did the thing"}
	out := flat(buildBanner(named))
	if !strings.Contains(out, "summary") || !strings.Contains(out, "Kestrel") {
		t.Fatalf("named banner should carry the agent name:\n%s", out)
	}

	anon := db.Task{ID: 2, Repo: "noname", Summary: "did the thing"}
	out = flat(buildBanner(anon))
	if strings.Contains(out, "Kestrel") {
		t.Fatalf("unnamed repo must not inherit another repo's identity:\n%s", out)
	}
	if !strings.Contains(out, "summary") {
		t.Fatalf("anon banner lost its header:\n%s", out)
	}
}

// pinSHA refuses a sha the checkout can't resolve (recording it would render as an
// empty diff forever — the dangling-sha "no changes" bug); --force stores it verbatim;
// a real ref pins to the concrete short hash.
func TestPinSHARefusesDangling(t *testing.T) {
	dir := t.TempDir()
	git(dir, "init")
	git(dir, "config", "user.email", "t@t")
	git(dir, "config", "user.name", "t")
	os.WriteFile(dir+"/a.txt", []byte("x\n"), 0o644)
	git(dir, "add", "-A")
	git(dir, "commit", "-m", "one")

	h, err := pinSHA(dir, "HEAD", false)
	if err != nil || h == "" || h == "HEAD" {
		t.Fatalf("HEAD should pin to a concrete hash: %q %v", h, err)
	}
	if _, err := pinSHA(dir, "deadbeef1", false); err == nil {
		t.Fatal("a sha unknown to the checkout must be refused")
	}
	forced, err := pinSHA(dir, "deadbeef1", true)
	if err != nil || forced != "deadbeef1" {
		t.Fatalf("--force should store verbatim: %q %v", forced, err)
	}
}

// a task whose sha doesn't exist in its repo shows a LOUD banner ("commit not found"),
// never a silent "no changes" — the symptom that hid the dangling-sha problem.
func TestDiffShowsDanglingShaWarning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	git(dir, "init")
	git(dir, "config", "user.email", "t@t")
	git(dir, "config", "user.name", "t")
	os.WriteFile(dir+"/a.txt", []byte("x\n"), 0o644)
	git(dir, "add", "-A")
	git(dir, "commit", "-m", "one")

	st := testStore(t)
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		inboxUI.Rows, diffUI.Banner, diffUI.Files = nil, nil, nil
	})
	st.Add(db.Task{Repo: "r", RepoPath: dir, SHA: "deadbeef1", Title: "t", Status: db.StatusPending})
	reloadTasks()
	inboxUI.Sel = 0
	inboxUI.DetailDirty = true
	inboxUI.LastSel = -99
	onInboxSelChanged()

	found := false
	for _, row := range diffUI.Banner {
		for _, sp := range row {
			if strings.Contains(sp.Text, "not found") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("dangling sha should produce a 'commit not found' banner, got banner=%v diffUI.FilesText=%q", diffUI.Banner, diffUI.FilesText)
	}
	if diffUI.FilesText != "commit not found" {
		t.Fatalf("diffUI.FilesText = %q, want 'commit not found'", diffUI.FilesText)
	}
}

// the agent-message ledger is visible from the TUI (#d5a1bb8b): 'm' opens a named
// "messages" view showing every message (sender@repo → target, body, agent read-dot),
// and opening it stamps the USER read-receipt on what was shown.
func TestMessagesViewShowsLedger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(dir, "recap.db"))
	st := testStore(t)
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		msgUI = msgView{}
		inboxUI.Rows = nil
	})
	reloadTasks()
	m1, _ := st.SendMessage("recap", "Kestrel", "tui", 0, 0, "need a second pair of eyes on the layout pass")
	st.SendMessage("tui", "Glyph Smith", "recap", m1, 0, "looking now")

	uiApp.View("main", buildMain()).NoCounts()
	uiApp.View("messages", buildMessagesView()).NoCounts()
	uiApp.Go("main")
	uiApp.RenderNow()

	openMessages()
	uiApp.RenderNow()
	if v := uiApp.CurrentView(); v != "messages" {
		t.Fatalf("openMessages should switch to the messages view, got %q", v)
	}

	buf := NewBuffer(120, 30)
	Build(buildMessagesView()).Execute(buf, 120, 30)
	full := ""
	for y := 0; y < 30; y++ {
		full += buf.GetLine(y) + "\n"
	}
	for _, want := range []string{"Kestrel@recap → tui", "second pair of eyes", "Glyph Smith@tui → recap", "↳m1", "agent messages"} {
		if !strings.Contains(full, want) {
			t.Fatalf("ledger missing %q:\n%s", want, full)
		}
	}

	// opening stamped the USER read-receipt on everything shown
	ms, _ := st.Messages("")
	for _, m := range ms {
		if m.ReadUser == "" {
			t.Fatalf("m%d not marked user-read after viewing", m.ID)
		}
	}
}

// 'o' on a comment thread collapses it to its root row (with a "▸ N replies"
// cue) and expands it back; selecting a nested reply folds the whole thread and
// lands the cursor on the root.
func TestToggleCommentThread(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() {
		uiStore = nil
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
	})

	id, err := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	rootID, err := st.AddReviewComment(id, "you", "root note", "calc.go", 3, "@@", "snippet")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	replyID, err := st.AddReply(rootID, "agent", "first reply")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := st.AddReply(replyID, "you", "nested reply"); err != nil {
		t.Fatalf("nested: %v", err)
	}

	loadDraftPane(id)
	if len(draftUI.Comments) != 3 {
		t.Fatalf("rows = %d, want 3", len(draftUI.Comments))
	}
	for i, v := range draftUI.Comments {
		if !v.Visible {
			t.Fatalf("row %d should start visible", i)
		}
	}

	// select the NESTED reply and fold: the row set NEVER changes — collapsing
	// flips Visible flags and the template's If decides what renders. Cursor on root.
	draftUI.Sel = 2
	toggleCommentThread()
	if len(draftUI.Comments) != 3 {
		t.Fatalf("fold must not change the row set: rows = %d, want 3", len(draftUI.Comments))
	}
	if !draftUI.Comments[0].Visible || draftUI.Comments[1].Visible || draftUI.Comments[2].Visible {
		t.Fatalf("collapsed: want root visible + replies hidden, got %v/%v/%v",
			draftUI.Comments[0].Visible, draftUI.Comments[1].Visible, draftUI.Comments[2].Visible)
	}
	if draftUI.Comments[0].ID != rootID || draftUI.Sel != 0 {
		t.Fatalf("cursor not on root: sel=%d id=%d", draftUI.Sel, draftUI.Comments[0].ID)
	}
	if draftUI.Comments[0].FoldCue != "▸ 2 replies" {
		t.Fatalf("fold cue = %q, want \"▸ 2 replies\"", draftUI.Comments[0].FoldCue)
	}
	// j from the root steps over the hidden replies (stays put — nothing below)
	moveDraft(1)
	if draftUI.Sel != 0 {
		t.Fatalf("j over hidden rows should stay on root, sel = %d", draftUI.Sel)
	}

	// o again expands, cue clears, flags restore
	toggleCommentThread()
	if draftUI.Comments[0].FoldCue != "" || !draftUI.Comments[1].Visible || !draftUI.Comments[2].Visible {
		t.Fatalf("expand failed: cue=%q vis=%v/%v",
			draftUI.Comments[0].FoldCue, draftUI.Comments[1].Visible, draftUI.Comments[2].Visible)
	}

	// a reload (e.g. new comment lands) keeps a collapsed thread collapsed
	toggleCommentThread()
	loadDraftPane(id)
	if len(draftUI.Comments) != 3 || draftUI.Comments[1].Visible {
		t.Fatalf("collapse should survive reload: rows=%d reply visible=%v",
			len(draftUI.Comments), draftUI.Comments[1].Visible)
	}
}

// hidden rows must leave NO trace in the rendered pane — no blank bands, no gap
// where the replies were. Renders the real pane (buildMain) before and after a fold.
func TestFoldedRowsRenderNothing(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		inboxUI.Rows = nil
		pane = paneList
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
	})

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	rootID, _ := st.AddReviewComment(id, "you", "ROOTBODY", "calc.go", 3, "@@", "snip")
	st.AddReply(rootID, "agent", "REPLYBODY")
	st.AddReviewComment(id, "you", "SECONDBODY", "zz.go", 9, "@@", "snip2")
	reloadTasks()
	loadDraftPane(id)
	draftUI.Has = true
	pane = paneDraft

	render := func() string {
		tmpl := Build(buildMain())
		buf := NewBuffer(140, 30)
		tmpl.Execute(buf, 140, 30)
		var out []string
		for y := 0; y < 30; y++ {
			out = append(out, strings.TrimRight(buf.GetLine(y), " "))
		}
		return strings.Join(out, "\n")
	}

	before := render()
	if !strings.Contains(before, "REPLYBODY") || !strings.Contains(before, "SECONDBODY") {
		t.Fatalf("expanded render missing rows:\n%s", before)
	}

	// fold the first thread
	draftUI.Sel = 0
	toggleCommentThread()
	after := render()
	if strings.Contains(after, "REPLYBODY") {
		t.Fatalf("hidden reply still renders:\n%s", after)
	}
	if !strings.Contains(after, "▸ 1") {
		t.Fatalf("fold cue missing from render:\n%s", after)
	}
	// no gap: the second thread must sit close under the folded root — the
	// blank-line run between them can't exceed the rows' own vertical padding.
	lines := strings.Split(after, "\n")
	rootAt, secondAt := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "ROOTBODY") {
			rootAt = i
		}
		if strings.Contains(l, "SECONDBODY") {
			secondAt = i
		}
	}
	if rootAt < 0 || secondAt < 0 {
		t.Fatalf("rows not rendered: root@%d second@%d\n%s", rootAt, secondAt, after)
	}
	if gap := secondAt - rootAt; gap > 6 {
		t.Fatalf("hidden rows left a gap: ROOTBODY@%d -> SECONDBODY@%d (%d apart)\n%s", rootAt, secondAt, gap, after)
	}
}

// the status glyph is template-owned: a per-item Switch on taskVM.State renders
// ●/↻/✓ with POINTER colours — so the right glyph+colour appears per state, and
// a theme-variable change recolors rows live with no reload. Buffer-cell proof.
func TestStatusGlyphTemplateOwned(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	prevDel := cDel
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		cDel = prevDel
		inboxUI.Rows = nil
		pane = paneList
	})

	pid, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "PENDINGROW", Status: db.StatusPending})
	_ = pid
	rid, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "REWORKROW", Status: db.StatusPending})
	st.AddReviewComment(rid, "you", "x", "", 0, "", "")
	st.SubmitReview(rid, db.VerdictRequestChanges, "fix")
	did, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "DONEROW", Status: db.StatusPending})
	st.SubmitReview(did, db.VerdictApprove, "")
	reloadTasks()

	render := func() *Buffer {
		tmpl := Build(buildMain())
		buf := NewBuffer(140, 40)
		tmpl.Execute(buf, 140, 40)
		return buf
	}
	glyphAt := func(buf *Buffer, title string) (rune, Color) {
		for y := 0; y < 40; y++ {
			var line string
			for x := 0; x < 140; x++ {
				line += string(buf.Get(x, y).Rune)
			}
			if strings.Contains(line, title) {
				// the status glyph sits left of the title in the same row
				for x := 0; x < 140; x++ {
					c := buf.Get(x, y)
					if c.Rune == '●' || c.Rune == '↻' || c.Rune == '✓' {
						return c.Rune, c.Style.FG
					}
				}
			}
		}
		t.Fatalf("%s row/glyph not rendered", title)
		return 0, Color{}
	}

	buf := render()
	if g, fg := glyphAt(buf, "PENDINGROW"); g != '●' || fg != cBright {
		t.Fatalf("pending glyph = %q/%v, want ●/%v", g, fg, cBright)
	}
	if g, fg := glyphAt(buf, "REWORKROW"); g != '↻' || fg != cDel {
		t.Fatalf("rework glyph = %q/%v, want ↻/%v", g, fg, cDel)
	}
	if g, fg := glyphAt(buf, "DONEROW"); g != '✓' || fg != cSubtle {
		t.Fatalf("done glyph = %q/%v, want ✓/%v", g, fg, cSubtle)
	}

	// pointer colours: mutating the theme var recolors WITHOUT any reload/rebuild
	cDel = Hex(0x123456)
	buf = render()
	if _, fg := glyphAt(buf, "REWORKROW"); fg != cDel {
		t.Fatalf("theme-var change did not recolor live: fg=%v want %v", fg, cDel)
	}
}
