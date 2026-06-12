package main

import (
	"fmt"
	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
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
	draftUI.Comments = []draftCommentVM{{ID: 1, Location: "general", Body: long, BodyRows: bodyMarkupRows(long), Visible: true}}
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
	c := draftCommentVM{Location: "↳ agent", Indent: "  ", When: "10:20", Body: "renamed it", BodyRows: bodyMarkupRows("renamed it"), Visible: true}
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
			// c460: rows in the regular comments view lead with their author
			if c.WhoLabel != "You" {
				t.Fatalf("human comment should lead with You, got %q", c.WhoLabel)
			}
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

// the help overlay must render every description in full — no mid-word clipping,
// no column running into its neighbour's keys (the layout/truncation report,
// todo:f6c96daf, screenshot-verified). Renders the real overlay through buildMain.
func TestHelpOverlayNoTruncation(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
		helpOpen = false
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()
	helpOpen = true

	tmpl := Build(buildMain())
	buf := NewBuffer(140, 40)
	tmpl.Execute(buf, 140, 40)
	var lines []string
	for y := 0; y < 40; y++ {
		lines = append(lines, buf.GetLine(y))
	}
	all := strings.Join(lines, "\n")

	// every section's longest descriptions render whole, on one line
	for _, want := range []string{
		"revisions / fold thread",
		"unsubmit → inbox",
		"open [[file]] link",
		"submit (amends)",
		"open in $EDITOR",
		"next / prev file",
		"fold file / all",
		"focus column",
		"agent messages",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("help overlay clips %q:\n%s", want, all)
		}
	}
}

// the focus underline: inked on the bottom row by the FocusLine effect at the
// pane's x/width (targets set at focus events), backgrounds preserved, and
// SUB-CELL edges via quadrant caps when the animated position is fractional.
func TestFocusBarTracksPane(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
		pane = paneList
		focusLineX, focusLineW = 0, 0
		statusMsg = ""
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()
	pane = paneList

	const W, H = 140, 40
	tmpl := Build(buildMain())
	render := func(x, w float64) *Buffer {
		buf := NewBuffer(W, H)
		tmpl.Execute(buf, W, H)
		fl := FocusLine{FG: &cFG, x: StaticEffectFloat64(x), w: StaticEffectFloat64(w)}
		fl.Apply(buf, PostContext{Width: W, Height: H})
		return buf
	}
	span := func(buf *Buffer) (x, w int) {
		x, w = -1, 0
		for cx := 0; cx < W; cx++ {
			c := buf.Get(cx, H-1)
			if c.Rune == '▁' && c.Style.FG == cFG {
				if x < 0 {
					x = cx
				}
				w++
			}
		}
		return
	}

	render(0, 0) // first layout populates the pane NodeRefs
	applyPaneFocus()
	if focusLineX != float64(listPaneRef.X) || focusLineW != float64(listPaneRef.W) {
		t.Fatalf("focus event targets = %v/%v, want pane rect %d/%d", focusLineX, focusLineW, listPaneRef.X, listPaneRef.W)
	}
	buf := render(focusLineX, focusLineW)
	x, w := span(buf)
	if x != listPaneRef.X || w != listPaneRef.W {
		t.Fatalf("list-focused line = x%d w%d, want pane rect x%d w%d", x, w, listPaneRef.X, listPaneRef.W)
	}
	if bg := buf.Get(2, H-1).Style.BG; bg != cPaneBG {
		t.Fatalf("inked line over the list column must keep cPaneBG beneath, got %v", bg)
	}

	setPane(paneDiff)
	if focusLineX != float64(diffPaneRef.X) || focusLineW != float64(diffPaneRef.W) {
		t.Fatalf("diff focus targets = %v/%v, want %d/%d", focusLineX, focusLineW, diffPaneRef.X, diffPaneRef.W)
	}
	buf = render(focusLineX, focusLineW)
	x, w = span(buf)
	if x != diffPaneRef.X || w != diffPaneRef.W {
		t.Fatalf("diff-focused line = x%d w%d, want pane rect x%d w%d", x, w, diffPaneRef.X, diffPaneRef.W)
	}
	if bg := buf.Get(diffPaneRef.X+2, H-1).Style.BG; bg != cBG {
		t.Fatalf("inked line over the diff column must keep cBG beneath, got %v", bg)
	}

	// mid-slide fractional positions round to whole cells: a solid ▁ span,
	// no mixed-height caps, no dot toggling (c424)
	buf = render(10.5, 20)
	if got := buf.Get(11, H-1).Rune; got != '▁' {
		t.Fatalf("rounded leading cell = %q, want ▁", got)
	}
	if got := buf.Get(30, H-1).Rune; got != '▁' {
		t.Fatalf("rounded trailing cell = %q, want ▁", got)
	}
	if got := buf.Get(10, H-1).Rune; got == '▁' {
		t.Fatalf("cell left of the rounded span should be un-inked")
	}

	// the old status bar is GONE (todo:a5f726bf): a bare statusMsg renders
	// nowhere — status streams through the corner feed instead (toast(),
	// pinned by TestToastRendersBottomRight), so nothing can collide with the
	// focus line's row again.
	statusMsg = "recorded #1"
	buf = render(focusLineX, focusLineW)
	for y := 0; y < H; y++ {
		if strings.Contains(buf.GetLine(y), "recorded #1") {
			t.Fatalf("bare statusMsg still renders (row %d) — the status bar should be gone", y)
		}
	}
}

// the agent dashboard ('A'): one row per named agent — identity colour + name,
// status (working / parked / idle), and the last recorded task. Loads at open
// (handler acquires), renders through the real overlay. Dispatched key proof.
func TestAgentDashboard(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
		dashUI = dashView{}
		pane = paneList
	})
	// RECAP_DB redirects db.Path() — identity/cursor/listener files land in the
	// temp dir, never the real config (the first run of this test polluted it).
	t.Setenv("RECAP_DB", filepath.Join(t.TempDir(), "recap.db"))
	// identities + a recorded task + a cursor flare in the isolated config dir.
	// Wren spans TWO repos (the c436 duplicate-name case) — one grouped row.
	for repo, pair := range map[string][2]string{
		"wren-repo":  {"Wren", "#aabbcc"},
		"wren-lab":   {"Wren", "#aabbcc"},
		"finch-repo": {"Finch", "#ccaabb"},
		"stale-repo": {"Heron", "#bbccaa"},
	} {
		if err := saveIdentity(repo, pair[0], pair[1]); err != nil {
			t.Fatalf("identity: %v", err)
		}
	}
	st.Add(db.Task{Repo: "wren-repo", RepoPath: "/tmp/w", Title: "shipped the wrenizer", Status: db.StatusPending})
	cursor.Save("finch-repo", "todo:abc", "polishing the finch cache")
	// Heron's flare is OLD: an untouched cursor file must read idle, not working
	cursor.Save("stale-repo", "todo:zzz", "ancient business")
	if p, err := db.Path(); err == nil {
		old := time.Now().Add(-3 * time.Hour)
		os.Chtimes(filepath.Join(filepath.Dir(p), "current-stale-repo"), old, old)
	}
	reloadTasks()

	uiApp.SetView(buildMain())
	uiApp.RenderNow()
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'A'}) {
		t.Fatal("'A' was not handled")
	}
	if !dashUI.Open {
		t.Fatal("dashboard did not open")
	}
	uiApp.RenderNow()

	buf := NewBuffer(140, 40)
	Build(buildMain()).Execute(buf, 140, 40)
	var all string
	for y := 0; y < 40; y++ {
		all += buf.GetLine(y) + "\n"
	}
	for _, want := range []string{"Finch", "working: polishing the finch cache", "last: shipped the wrenizer"} {
		if !strings.Contains(all, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, all)
		}
	}
	// duplicate-name grouping: ONE Wren row listing both repos
	if strings.Count(all, "Wren") != 1 {
		t.Fatalf("Wren should appear exactly once (grouped):\n%s", all)
	}
	if !strings.Contains(all, "wren-lab, wren-repo") {
		t.Fatalf("grouped row should list both repos:\n%s", all)
	}
	// the stale flare reads idle, never working
	if strings.Contains(all, "ancient business") {
		t.Fatalf("3h-old flare still shows as working:\n%s", all)
	}
	for _, r := range dashUI.Rows {
		if r.Name == "Heron" && r.Status != "idle" {
			t.Fatalf("Heron's stale flare should read idle, got %q", r.Status)
		}
	}
	// c448: the rows keep the snapshot's last-active order (no name re-sort) —
	// Heron, 3h quiet, sits LAST — and the side time reads as an AGE of that
	// activity ("active … ago"), so the column visibly explains the order.
	if last := dashUI.Rows[len(dashUI.Rows)-1]; last.Name != "Heron" {
		t.Fatalf("least-recently-active should be last, got %q", last.Name)
	}
	if top := dashUI.Rows[0]; !strings.HasPrefix(top.When, "active ") {
		t.Fatalf("side time should read as activity age, got %q", top.When)
	}
}

// Slice 3 of the proposal workflow (c442: "i can't see them anywhere in the
// inbox"): open proposals lead the inbox as a PROPOSALS section, the detail
// pane renders the document + thread, a proposal row never resolves through
// TaskByID (proposal and task ids are independent sequences), and `c` threads
// a human comment that pings each party exactly once (the digest model).
func TestProposalInboxSection(t *testing.T) {
	prevApp, prevStore, prevOmni, prevLayer, prevKick := uiApp, uiStore, omni, diffUI.Layer, propDetailKick
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiApp, uiStore, omni, diffUI.Layer, propDetailKick = prevApp, prevStore, prevOmni, prevLayer, prevKick
		inboxUI = inboxView{Expanded: map[int64]bool{}, TaskByID: map[int64]db.Task{}, PropByID: map[int64]db.Proposal{}, DoneLimit: 10}
		promptUI.Field = InputState{}
		propOpen = 0
		statusMsg = ""
		propUI = propView{Commented: map[int]bool{}}
	})
	t.Setenv("RECAP_DB", filepath.Join(t.TempDir(), "recap.db"))

	// task #1 exists so proposal #1 COLLIDES on raw id — the falsifiable check
	// that proposal rows resolve through PropByID, never TaskByID.
	if _, err := st.Add(db.Task{Repo: "recap", RepoPath: "/tmp/r", Title: "an ordinary task", Status: db.StatusPending}); err != nil {
		t.Fatal(err)
	}
	pid, err := st.AddProposal(db.Proposal{
		Title: "oscillators", Body: "# heading\n\nbody text",
		ProposerRepo: "tui", ProposerWho: "Glyph Smith", TargetRepo: "tui",
	}, []string{"recap"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProposalComment(pid, "recap", "Kestrel", "endorse: the seam collapses"); err != nil {
		t.Fatal(err)
	}

	// synchronous kick keeps refreshDetailNow deterministic.
	propDetailKick = func(p db.Proposal, key string, reset bool) { stageProp(fetchPropDetail(p, key, reset)) }

	reloadTasks()
	uiApp.SetView(buildMain())

	if len(inboxUI.Rows) < 2 {
		t.Fatalf("expected proposal + task rows, got %d", len(inboxUI.Rows))
	}
	row := inboxUI.Rows[0]
	if !row.Proposal || row.GroupLabel != "PROPOSALS" || row.IDText != "P1" || row.State != "proposal" {
		t.Fatalf("proposal row malformed: %+v", row)
	}
	if !strings.Contains(inboxUI.CountText, "◆ 1") {
		t.Fatalf("header missing the open-proposal badge: %q", inboxUI.CountText)
	}

	inboxUI.Sel = 0
	syncSelectionFlags()
	if _, ok := selectedTask(); ok {
		t.Fatal("selectedTask resolved a PROPOSAL row to a task (id collision)")
	}

	inboxUI.DetailDirty = true
	refreshDetailNow()
	if !propUI.Active {
		t.Fatal("proposal selection should activate the proposal pane")
	}
	drainPropDetail()
	if detailTitle != "oscillators" {
		t.Fatalf("detailTitle = %q", detailTitle)
	}
	// the document projects through the PROPOSAL pane's own rows/meta — the
	// diff machinery never sees it (c454's split)
	prepPropRows(80)
	text := ""
	for _, r := range propUI.Rows {
		for _, sp := range r.Spans {
			text += sp.Text
		}
		text += "\n"
	}
	for _, want := range []string{"proposal #1", "heading", "body text"} {
		if !strings.Contains(text, want) {
			t.Fatalf("document missing %q:\n%s", want, text)
		}
	}
	// the thread rides the SHARED comments pane (todo:6d9eb05e — one
	// component, two sources), routed by PropID
	if !draftUI.Has || draftUI.PropID != pid {
		t.Fatalf("shared pane not carrying the thread: has=%v prop=%d", draftUI.Has, draftUI.PropID)
	}
	if len(draftUI.Comments) != 1 || draftUI.Comments[0].WhoLabel != "Kestrel@recap" {
		t.Fatalf("thread row wrong: %+v", draftUI.Comments)
	}
	// task-comment-only verbs gate off; the author colours by its repo identity
	editDraftComment()
	if promptUI.Open {
		t.Fatal("editDraftComment must gate on a proposal thread")
	}
	// document rows are line-commentable, anchored to SOURCE lines: the doc is
	// "# heading\n\nbody text" so "body text" anchors to source line 3.
	var bodyMeta *propLineMeta
	for i := range propUI.Meta {
		if propUI.Meta[i].Commentable && propUI.Meta[i].Text == "body text" {
			bodyMeta = &propUI.Meta[i]
		}
	}
	if bodyMeta == nil || bodyMeta.Line != 3 {
		t.Fatalf("document line meta wrong: %+v", bodyMeta)
	}
	// a line comment through the pick action lands anchored with the snippet
	commentOnProposalLine(*bodyMeta)
	if !promptUI.Open {
		t.Fatal("line-comment prompt did not open")
	}
	promptUI.Field.Value = "tighten this sentence"
	promptUI.submit()
	if cs, _ := st.ProposalComments(pid); len(cs) != 2 || cs[1].Line != 3 || cs[1].Snippet != "body text" {
		t.Fatalf("line comment not anchored: %+v", cs)
	}
	drainPropDetail()
	if !propUI.Commented[3] {
		t.Fatal("commented document line should wash")
	}
	// c467: the wash is the diff pane's FAINT cCommentBG and covers the text
	// cells too — assert the rendered buffer cell UNDER the text, not the VM.
	prepPropRows(60)
	wbuf := NewBuffer(60, len(propUI.Rows)+2)
	propTemplate().Execute(wbuf, 60, int16(len(propUI.Rows)+2))
	washOK := false
	for y := 0; y < len(propUI.Rows); y++ {
		if x := strings.Index(wbuf.GetLine(y), "body text"); x >= 0 {
			cell := wbuf.Get(x, y)
			if cell.Style.BG == cCommentBG {
				washOK = true
			} else {
				t.Fatalf("washed line's text cell bg = %v, want cCommentBG %v", cell.Style.BG, cCommentBG)
			}
		}
	}
	if !washOK {
		t.Fatal("commented document line not found in the rendered layer")
	}
	// the human's comment carries a name too (todo:5a724f62)
	foundYou := false
	for _, c := range draftUI.Comments {
		if c.WhoLabel == "You" {
			foundYou = true
		}
	}
	if len(draftUI.Comments) != 2 || !foundYou {
		t.Fatalf("human comment should read as You: %+v", draftUI.Comments)
	}

	// structural proof the If-swapped pane actually renders: execute the real
	// view with the proposal active and find the document on screen (an
	// If-wrapped LayerView collapsing to zero height would fail this).
	propUI.Layer = NewLayer()
	propUI.Layer.Render = renderPropLayer
	buf := NewBuffer(140, 40)
	Build(buildMain()).Execute(buf, 140, 40)
	foundDoc := false
	for y := 0; y < 40; y++ {
		if strings.Contains(buf.GetLine(y), "body text") {
			foundDoc = true
		}
	}
	if !foundDoc {
		t.Fatal("proposal document did not render through the swapped pane")
	}
	// c455: the thread column shows WHO wrote each comment — names lead rows
	foundWho := false
	for y := 0; y < 40; y++ {
		if strings.Contains(buf.GetLine(y), "Kestrel@recap") {
			foundWho = true
		}
	}
	if !foundWho {
		t.Fatal("agent name missing from the rendered thread column")
	}
	// todo:7b4ae660 — the focus line follows focus into the comments pane on
	// FIRST open: the SHARED pane carries the thread, so its ref is live.
	draftUI.PaneRef = NodeRef{X: 100, W: 30}
	setPane(paneDraft)
	if focusLineX != 100 || focusLineW != 30 {
		t.Fatalf("focus line should target the comments pane: x=%v w=%v", focusLineX, focusLineW)
	}
	setPane(paneList)

	// `c`: the human comment threads, joins no phantom "" party, and pings each
	// party once — a second comment while pings sit unread adds NO new pings.
	openComment()
	if !promptUI.Open {
		t.Fatal("comment prompt did not open for the proposal row")
	}
	promptUI.Field.Value = "ruling: direction approved"
	promptUI.submit()
	cs, _ := st.ProposalComments(pid)
	if len(cs) != 3 || cs[2].WhoName != "you" || cs[2].WhoRepo != "" || cs[2].Line != 0 {
		t.Fatalf("human comment not threaded: %+v", cs)
	}
	parties, _ := st.ProposalParties(pid)
	for _, p := range parties {
		if p == "" {
			t.Fatal("phantom empty party joined from the human comment")
		}
	}
	openComment()
	promptUI.Field.Value = "second ruling"
	promptUI.submit()
	ms, _ := st.Messages("")
	pings := map[string]int{}
	for _, m := range ms {
		pings[m.ToRepo]++
	}
	if pings["tui"] != 1 || pings["recap"] != 1 {
		t.Fatalf("digest model broken — expected exactly one unread ping per party, got %v", pings)
	}

	// c462: threaded replies — r on a pane row nests the reply under it,
	// routed at the PROPOSAL (never the task comment tables)
	drainPropDetail()
	draftUI.Sel = 0 // Kestrel's root comment
	rootID := draftUI.Comments[0].ID
	replyToComment()
	if !promptUI.Open {
		t.Fatal("reply prompt did not open from the shared pane")
	}
	promptUI.Field.Value = "agreed — **bold call** and `clean code`"
	promptUI.submit()
	drainPropDetail()
	if cs, _ := st.ProposalComments(pid); len(cs) != 5 || cs[4].ParentID != rootID {
		t.Fatalf("reply not threaded at the proposal: %+v", cs)
	}
	var replyVM *draftCommentVM
	for i := range draftUI.Comments {
		if draftUI.Comments[i].ParentID == rootID {
			replyVM = &draftUI.Comments[i]
		}
	}
	if replyVM == nil || !replyVM.Reply || replyVM.Indent == "" {
		t.Fatalf("reply row should nest and indent: %+v", draftUI.Comments)
	}
	// c463: bodies render through the summary markup — the markers are
	// consumed and the styled words survive as spans
	bodyText := ""
	for _, r := range replyVM.BodyRows {
		for _, sp := range r.Spans {
			bodyText += sp.Text
		}
	}
	if !strings.Contains(bodyText, "bold call") || !strings.Contains(bodyText, "clean code") || strings.Contains(bodyText, "**") {
		t.Fatalf("markup not applied to the thread body: %q", bodyText)
	}
	// and through to the screen: the markup-styled reply renders in the column
	bufT := NewBuffer(160, 45)
	Build(buildMain()).Execute(bufT, 160, 45)
	foundBody := false
	for y := 0; y < 45; y++ {
		if strings.Contains(bufT.GetLine(y), "bold call") {
			foundBody = true
		}
	}
	if !foundBody {
		t.Fatal("markup-rendered reply body missing from the rendered thread column")
	}
}

// The corner notification feed (todo:a5f726bf): entries live for the TTL, fade
// over the tail, expire, and cap at the limit — under a fake clock so the
// lifecycle is deterministic.
func TestFeedLifecycle(t *testing.T) {
	now := time.Now()
	f := newFeed(func() time.Time { return now })
	f.push("first")
	v, ok := f.take()
	if !ok || len(v) != 1 || v[0].Text != "first" || v[0].Opacity != 1 {
		t.Fatalf("fresh push wrong: ok=%v v=%+v", ok, v)
	}
	// inside the fade window: opacity strictly between 0 and 1
	now = now.Add(f.ttl - f.fade/2)
	if live := f.tick(); !live {
		t.Fatal("feed should still be live mid-fade")
	}
	v, _ = f.take()
	if len(v) != 1 || v[0].Opacity <= 0 || v[0].Opacity >= 1 {
		t.Fatalf("mid-fade opacity wrong: %+v", v)
	}
	// past the TTL: expired, feed reports not-live (the ticker's gate)
	now = now.Add(f.fade)
	if live := f.tick(); live {
		t.Fatal("feed should be dead past the TTL")
	}
	if v, _ = f.take(); len(v) != 0 {
		t.Fatalf("expired entries still in view: %+v", v)
	}
	// the limit: pushing limit+2 keeps only the newest limit entries
	for i := 0; i < f.limit+2; i++ {
		f.push(fmt.Sprintf("n%d", i))
	}
	v, _ = f.take()
	if len(v) != f.limit || v[0].Text != "n2" {
		t.Fatalf("limit not enforced: len=%d first=%q", len(v), v[0].Text)
	}
}

// toast() streams into the bottom-right overlay and the old status bar is gone:
// the text renders in the lower-right region of the frame, not on the row above
// the focus line at the left edge.
func TestToastRendersBottomRight(t *testing.T) {
	prevStore, prevApp, prevOmni, prevFeed := uiStore, uiApp, omni, statusFeed
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	statusFeed = newFeed(nil)
	t.Cleanup(func() {
		uiStore, uiApp, omni, statusFeed = prevStore, prevApp, prevOmni, prevFeed
		feedItems, feedVisible = nil, false
		statusMsg = ""
		inboxUI.Rows = nil
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()

	toast("recorded #97 — all good")
	drainFeed()
	if !feedVisible || len(feedItems) != 1 {
		t.Fatalf("toast did not reach the bound feed: visible=%v items=%+v", feedVisible, feedItems)
	}
	if statusMsg != "recorded #97 — all good" {
		t.Fatalf("statusMsg compatibility broken: %q", statusMsg)
	}

	tmpl := Build(buildMain())
	buf := NewBuffer(120, 40)
	tmpl.Execute(buf, 120, 40)
	foundY, foundX := -1, -1
	for y := 0; y < 40; y++ {
		if x := strings.Index(buf.GetLine(y), "recorded #97"); x >= 0 {
			foundY, foundX = y, x
		}
	}
	if foundY < 0 {
		t.Fatal("toast text not rendered anywhere")
	}
	if foundY < 30 || foundX < 60 {
		t.Fatalf("toast rendered at (%d,%d) — expected the bottom-right corner", foundX, foundY)
	}
}

// Revision times are visible (todo:22cea19f): a revised task's row time shows
// when the work LAST changed (not first arrival), each expanded child carries
// its own submitted stamp in the label, and selecting a child moves the detail
// meta time to that revision.
func TestRevisionTimesVisible(t *testing.T) {
	prevStore, prevApp, prevOmni, prevLayer := uiStore, uiApp, omni, diffUI.Layer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiStore, uiApp, omni, diffUI.Layer = prevStore, prevApp, prevOmni, prevLayer
		inboxUI.Rows = nil
		inboxUI.Expanded = map[int64]bool{}
	})
	yesterday := time.Now().Add(-26 * time.Hour).Format("2006-01-02 15:04:05")
	id, err := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "revised work", Status: db.StatusPending, CreatedAt: yesterday})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRevision(id, "deadbeef1234567", "fix forward"); err != nil {
		t.Fatal(err)
	}
	inboxUI.Expanded[id] = true
	reloadTasks()

	var header, child *taskVM
	for i := range inboxUI.Rows {
		switch {
		case inboxUI.Rows[i].ID == id && inboxUI.Rows[i].Header:
			header = &inboxUI.Rows[i]
		case inboxUI.Rows[i].ID == id && inboxUI.Rows[i].RevIdx == 1:
			child = &inboxUI.Rows[i]
		}
	}
	if header == nil || child == nil {
		t.Fatalf("rows missing: header=%v child=%v", header, child)
	}
	// header time = the LATEST revision (today, HH:MM), not yesterday's arrival
	if strings.Contains(header.When, "Jan") || strings.Contains(header.When, time.Now().Add(-26*time.Hour).Format("Jan")) || len(header.When) != 5 {
		t.Fatalf("header time should be the latest revision's HH:MM today, got %q", header.When)
	}
	// the child label carries its submitted stamp; the base child carries yesterday's date
	if !strings.Contains(child.RevLabel, hhmm(child.RevWhen)) {
		t.Fatalf("rev child label missing its stamp: %q (when %q)", child.RevLabel, child.RevWhen)
	}
	var base *taskVM
	for i := range inboxUI.Rows {
		if inboxUI.Rows[i].ID == id && inboxUI.Rows[i].RevIdx == 0 {
			base = &inboxUI.Rows[i]
		}
	}
	if base == nil || !strings.Contains(base.RevLabel, time.Now().Add(-26*time.Hour).Format("Jan 2")) {
		t.Fatalf("base revision label should carry yesterday's date: %+v", base)
	}
	// selecting the child moves the detail meta time to the revision's stamp
	for i := range inboxUI.Rows {
		if inboxUI.Rows[i].ID == id && inboxUI.Rows[i].RevIdx == 1 {
			inboxUI.Sel = i
		}
	}
	inboxUI.DetailDirty = true
	refreshDetailNow()
	if metaWhen != child.RevWhen {
		t.Fatalf("detail meta time should follow the revision: got %q want %q", metaWhen, child.RevWhen)
	}
}

// Z is context-aware fold-all (todo:d044e2dd): in the LIST pane it expands or
// collapses every multi-revision task at once; in the COMMENTS pane it folds
// or unfolds every reply thread — toggle semantics matching the diff pane's Z.
func TestCollapseAllContexts(t *testing.T) {
	prevStore, prevApp, prevOmni, prevLayer := uiStore, uiApp, omni, diffUI.Layer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiStore, uiApp, omni, diffUI.Layer = prevStore, prevApp, prevOmni, prevLayer
		inboxUI.Rows = nil
		inboxUI.Expanded = map[int64]bool{}
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
	})
	idA, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "task a", Status: db.StatusPending})
	idB, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "task b", Status: db.StatusPending})
	st.AddRevision(idA, "aaaaaaa1234", "fix a")
	st.AddRevision(idB, "bbbbbbb1234", "fix b")
	reloadTasks()

	countChildren := func() int {
		n := 0
		for _, r := range inboxUI.Rows {
			if r.Grouped {
				n++
			}
		}
		return n
	}
	if countChildren() != 0 {
		t.Fatalf("precondition: nothing expanded, got %d children", countChildren())
	}
	collapseAllRevisions()
	if countChildren() != 4 { // 2 tasks × (original + rev 1)
		t.Fatalf("Z should expand every multi-rev task: %d children", countChildren())
	}
	collapseAllRevisions()
	if countChildren() != 0 {
		t.Fatalf("second Z should collapse everything: %d children", countChildren())
	}

	// comments context: two threads, each root + one reply
	cs := []db.TaskComment{}
	mk := func(id, parent int64, who, body string) db.TaskComment {
		c := db.TaskComment{}
		c.ID, c.ParentID, c.Who, c.Body = id, parent, who, body
		return c
	}
	cs = append(cs, mk(1, 0, "you", "root one"), mk(2, 1, "agent", "reply one"),
		mk(3, 0, "you", "root two"), mk(4, 3, "agent", "reply two"))
	applyDraftComments(idA, cs)
	visibleReplies := func() int {
		n := 0
		for _, c := range draftUI.Comments {
			if c.Reply && c.Visible {
				n++
			}
		}
		return n
	}
	if visibleReplies() != 2 {
		t.Fatalf("precondition: both replies visible, got %d", visibleReplies())
	}
	collapseAllCommentThreads()
	if visibleReplies() != 0 {
		t.Fatalf("Z should fold every thread: %d replies still visible", visibleReplies())
	}
	collapseAllCommentThreads()
	if visibleReplies() != 2 {
		t.Fatalf("second Z should unfold every thread: %d visible", visibleReplies())
	}
}

// vim jumps in the comments pane (todo:99856b95): gg lands on the first
// VISIBLE row, G on the last — both skipping rows hidden by collapsed threads.
func TestDraftVimJumps(t *testing.T) {
	prev := draftUI
	t.Cleanup(func() { draftUI = prev })
	draftUI.Comments = []draftCommentVM{
		{ID: 1, Visible: false}, // hidden (collapsed reply)
		{ID: 2, Visible: true},
		{ID: 3, Visible: true},
		{ID: 4, Visible: false},
	}
	draftUI.Sel = 2
	draftSelTop()
	if draftUI.Sel != 1 {
		t.Fatalf("gg should land on the first VISIBLE row, got %d", draftUI.Sel)
	}
	draftSelBottom()
	if draftUI.Sel != 2 {
		t.Fatalf("G should land on the last VISIBLE row, got %d", draftUI.Sel)
	}
}

// Slice 4 — sign-off (c473: "how do i sign off a proposal??"): pressing a on a
// proposal row approves it, which MATERIALISES the decision — an ADR in the
// target repo (proposal id = ADR number), an implementation todo on the target
// repo's TODO, and a direct message to every party. The decided proposal
// leaves the open list.
func TestProposalSignOff(t *testing.T) {
	prevApp, prevStore, prevOmni, prevLayer, prevKick := uiApp, uiStore, omni, diffUI.Layer, propDetailKick
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = func() {}
	t.Cleanup(func() {
		uiApp, uiStore, omni, diffUI.Layer, propDetailKick = prevApp, prevStore, prevOmni, prevLayer, prevKick
		inboxUI = inboxView{Expanded: map[int64]bool{}, TaskByID: map[int64]db.Task{}, PropByID: map[int64]db.Proposal{}, DoneLimit: 10}
		propUI = propView{Commented: map[int]bool{}}
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
		propOpen = 0
		statusMsg = ""
	})
	t.Setenv("RECAP_DB", filepath.Join(t.TempDir(), "recap.db"))
	targetTree := t.TempDir()
	// the target repo's TODO resolves through the config template
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("todo_template = \""+filepath.Join(targetTree, "TODO.md")+"\"\n"), 0o644)
	t.Setenv("RECAP_CONFIG", cfgPath)
	os.WriteFile(filepath.Join(targetTree, "TODO.md"), []byte("- [ ] existing item\n"), 0o644)

	// the target repo is known via a recorded task (RepoPathFor resolution)
	st.Add(db.Task{Repo: "tui", RepoPath: targetTree, Title: "prior work", Status: db.StatusPending})
	pid, err := st.AddProposal(db.Proposal{
		Title: "Oscillators & Gates!", Body: "# the plan\n\ndo the thing",
		ProposerRepo: "tui", ProposerWho: "Glyph Smith", TargetRepo: "tui",
	}, []string{"recap"})
	if err != nil {
		t.Fatal(err)
	}
	propDetailKick = func(p db.Proposal, key string, reset bool) {}
	reloadTasks()
	inboxUI.Sel = 0
	syncSelectionFlags()
	if row := selectedRow(); row == nil || !row.Proposal {
		t.Fatalf("precondition: proposal row selected, got %+v", selectedRow())
	}

	approveSelected() // a on a proposal row IS the sign-off

	p, _ := st.ProposalByID(pid)
	if p.Status != db.ProposalApproved {
		t.Fatalf("status = %q, want approved", p.Status)
	}
	adr := filepath.Join(targetTree, "docs", "adr", fmt.Sprintf("%d-oscillators-gates.md", pid))
	b, err := os.ReadFile(adr)
	if err != nil {
		t.Fatalf("ADR not written: %v", err)
	}
	for _, want := range []string{"# ADR 1: Oscillators & Gates!", "status: accepted", "do the thing", "Glyph Smith@tui"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("ADR missing %q:\n%s", want, b)
		}
	}
	tb, _ := os.ReadFile(filepath.Join(targetTree, "TODO.md"))
	if !strings.Contains(string(tb), "implement approved proposal #1: Oscillators & Gates!") {
		t.Fatalf("implementation todo not queued:\n%s", tb)
	}
	// every party hears the verdict directly
	ms, _ := st.Messages("")
	heard := map[string]bool{}
	for _, m := range ms {
		if strings.Contains(m.Body, "APPROVED") {
			heard[m.ToRepo] = true
		}
	}
	if !heard["tui"] || !heard["recap"] {
		t.Fatalf("parties not notified of the verdict: %v", heard)
	}
	// the decided proposal leaves the open list
	for _, r := range inboxUI.Rows {
		if r.Proposal {
			t.Fatalf("decided proposal still in the inbox: %+v", r)
		}
	}
	// double-decide refused (X after a)
	if err := st.DecideProposal(pid, db.ProposalDeclined); err == nil {
		t.Fatal("double-decide should refuse")
	}
}
