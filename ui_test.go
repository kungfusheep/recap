package main

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// selecting a draft comment scrolls the diff layer to the line it's anchored to.
func TestSyncDiffToDraft(t *testing.T) {
	diffLayer = NewLayer()
	diffLayer.Render = func() {}
	t.Cleanup(func() { diffLayer = nil; diffMeta = nil; draftComments = nil; draftSel = 0 })

	// a diff buffer tall enough to scroll, with known anchor rows.
	diffMeta = []diffLineMeta{
		{}, // 0 file header
		{File: "a.go", Line: 1, Commentable: true},  // 1
		{File: "a.go", Line: 2, Commentable: true},  // 2
		{File: "b.go", Line: 10, Commentable: true}, // 3
		{File: "b.go", Line: 11, Commentable: true}, // 4
	}
	buf := NewBuffer(40, len(diffMeta)+50) // taller than viewport so maxScroll>0
	diffLayer.SetBuffer(buf)
	diffLayer.SetViewport(40, 3)

	draftComments = []draftCommentVM{
		{File: "b.go", Line: 11},
		{File: "a.go", Line: 2},
	}

	draftSel = 0
	syncDiffToDraft()
	if got := diffLayer.ScrollY(); got != 4 {
		t.Fatalf("b.go:11 should scroll to row 4, got %d", got)
	}

	draftSel = 1
	syncDiffToDraft()
	if got := diffLayer.ScrollY(); got != 2 {
		t.Fatalf("a.go:2 should scroll to row 2, got %d", got)
	}

	// a general (unanchored) comment leaves scroll untouched
	draftComments = []draftCommentVM{{File: ""}}
	draftSel = 0
	before := diffLayer.ScrollY()
	syncDiffToDraft()
	if diffLayer.ScrollY() != before {
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

	id, err := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// no draft yet → pane hidden, no rows, no hint
	loadDraftPane(id)
	if hasDraft || len(draftComments) != 0 || draftNote != "" {
		t.Fatalf("expected empty draft state, got hasDraft=%v rows=%d note=%q", hasDraft, len(draftComments), draftNote)
	}

	// a line-anchored comment and a general one accumulate into the draft
	if _, err := st.AddReviewComment(id, "you", "needs a test", "calc.go", 3, "@@", "func sub(){}"); err != nil {
		t.Fatalf("comment 1: %v", err)
	}
	if _, err := st.AddReviewComment(id, "you", "overall nit", "", 0, "", ""); err != nil {
		t.Fatalf("comment 2: %v", err)
	}

	loadDraftPane(id)
	if !hasDraft {
		t.Fatal("expected hasDraft=true once comments exist")
	}
	if draftNote != "✎ 2 draft" {
		t.Fatalf("draftNote = %q, want \"✎ 2 draft\"", draftNote)
	}
	if len(draftComments) != 2 {
		t.Fatalf("want 2 draft rows, got %d", len(draftComments))
	}
	// general (unanchored) row sorts first, falls back to "general", no snippet
	if draftComments[0].Location != "general" || draftComments[0].Snippet != "" {
		t.Errorf("row0 = %+v, want general/no-snippet", draftComments[0])
	}
	// the line-anchored row follows, carrying location + snippet
	if draftComments[1].Location != "calc.go · line 3" {
		t.Errorf("row1 location = %q", draftComments[1].Location)
	}
	if draftComments[1].Snippet != "func sub(){}" || draftComments[1].Body != "needs a test" {
		t.Errorf("row1 snippet/body = %q / %q", draftComments[1].Snippet, draftComments[1].Body)
	}

	// the two are draft (editable) before submit
	if !draftComments[0].Draft {
		t.Fatalf("comments should be Draft before submit")
	}

	// after submit the comments PERSIST (read-only) — the pane stays, feedback
	// remains visible, but rows are no longer Draft.
	if _, err := st.SubmitReview(id, VerdictComment, "fyi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	loadDraftPane(id)
	if !hasDraft || len(draftComments) != 2 {
		t.Fatalf("comments should persist after submit, got hasDraft=%v rows=%d", hasDraft, len(draftComments))
	}
	for _, c := range draftComments {
		if c.Draft {
			t.Fatalf("submitted comments should not be Draft: %+v", c)
		}
	}
	if draftNote != "2 comments" {
		t.Fatalf("settled note = %q, want \"2 comments\"", draftNote)
	}
}

// loadDraftPane also records which diff lines carry a comment (the gutter cue)
// and threads the comment id through for edit/delete.
func TestCommentedLinesCue(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; clear(commentedLines); draftComments = nil })

	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	cid, _ := st.AddReviewComment(id, "you", "note", "main.go", 12, "@@", "x := 1")
	st.AddReviewComment(id, "you", "general note", "", 0, "", "")

	loadDraftPane(id)
	if !commentedLines[lineKey("main.go", 12)] {
		t.Fatalf("expected main.go:12 marked as commented, got %v", commentedLines)
	}
	if len(commentedLines) != 1 {
		t.Fatalf("only anchored comments should mark lines, got %d", len(commentedLines))
	}
	// general sorts first, so the anchored comment (cid) is row 1; assert the VM
	// carries the right id regardless of position.
	var found bool
	for _, c := range draftComments {
		if c.ID == cid {
			found = true
		}
	}
	if !found {
		t.Fatalf("VM should carry comment id %d, got %+v", cid, draftComments)
	}
}

// the draft pane lists general (top-level) comments first, then anchored ones
// grouped by file, then line.
func TestDraftPaneOrdering(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; clear(commentedLines); draftComments = nil; draftSel = 0 })

	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	// add deliberately out of order
	st.AddReviewComment(id, "you", "general", "", 0, "", "")
	st.AddReviewComment(id, "you", "b high", "b.go", 40, "@@", "x")
	st.AddReviewComment(id, "you", "a low", "a.go", 5, "@@", "y")
	st.AddReviewComment(id, "you", "b low", "b.go", 9, "@@", "z")

	loadDraftPane(id)
	got := make([]string, len(draftComments))
	for i, c := range draftComments {
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
	t.Cleanup(func() { uiStore = nil; vmRows = nil; sel = 0 })

	orig, _ := st.Add(Task{Repo: "recap", RepoPath: "/tmp/r", Title: "orig", Status: StatusRedo})
	fix, _ := st.Add(Task{Repo: "recap", RepoPath: "/tmp/r", Title: "fix", Status: StatusPending, ParentID: orig})
	st.Add(Task{Repo: "recap", RepoPath: "/tmp/r", Title: "fresh", Status: StatusPending}) // net-new, no parent

	repoFltr = "recap"
	t.Cleanup(func() { repoFltr = "" })
	reloadTasks()

	byID := map[int64]taskVM{}
	for _, vm := range vmRows {
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
	st.SubmitReview(fix, VerdictApprove, "")
	reloadTasks()
	for _, vm := range vmRows {
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

	orig, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "orig", Status: StatusPending})
	st.AddReviewComment(orig, "you", "tighten this", "a.go", 5, "@@", "x")
	rv, _ := st.SubmitReview(orig, VerdictRequestChanges, "needs work on a.go")

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
	fix, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "fix", Status: StatusPending, ParentID: orig,
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
	plain, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "plain", Status: StatusPending})
	pt, _ := st.Get(plain)
	if b := buildBanner(pt); b != nil {
		t.Fatalf("plain task should have no banner, got %d rows", len(b))
	}

	// inbox item WITH a reviewer briefing → summary banner.
	briefed, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "briefed", Status: StatusPending,
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
	prevRows, prevSel := vmRows, sel
	t.Cleanup(func() { vmRows = prevRows; sel = prevSel })

	vmRows = []taskVM{
		{ID: 1, IDText: "#1", Title: "first task", When: "10:00", Repo: "recap", Glyph: "●", GlyphColor: cSubtle, Selected: true},
		{ID: 2, IDText: "#2", Title: "second task", When: "10:01", Repo: "recap", Glyph: "●", GlyphColor: cSubtle},
	}
	sel = 0

	render := func(marker string) string {
		node := List(&vmRows).
			Selection(&sel).
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
	prevFltr := repoFltr
	repoFltr = ""
	t.Cleanup(func() { uiStore = nil; vmRows = nil; sel = 0; repoFltr = prevFltr; inboxCount = 0 })

	// 3 pending (inbox), 1 amends (request_changes), 1 done (approved)
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "p1", Status: StatusPending})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "p2", Status: StatusPending})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "p3", Status: StatusPending})
	amends, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "a", Status: StatusPending})
	st.SubmitReview(amends, VerdictRequestChanges, "fix")
	done, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "d", Status: StatusPending})
	st.SubmitReview(done, VerdictApprove, "")

	reloadTasks()
	if inboxCount != 3 {
		t.Fatalf("inbox count = %d, want 3 (only pending); total tasks = %d", inboxCount, len(tasks))
	}
	if len(tasks) != 5 {
		t.Fatalf("sanity: want 5 total tasks, got %d", len(tasks))
	}
}

// regression (review #28): a long comment in the draft pane must WRAP to the
// column width, not truncate at one line. Verified by render: a long body in a
// narrow column produces several non-empty body rows and keeps its trailing text.
func TestDraftCommentBodyWraps(t *testing.T) {
	prev, prevSel := draftComments, draftSel
	t.Cleanup(func() { draftComments = prev; draftSel = prevSel })

	long := "this is a deliberately long top-level comment that should wrap across several lines inside the narrow draft column instead of truncating at a single line in the available space"
	draftComments = []draftCommentVM{{ID: 1, Location: "general", Body: long, Selected: true}}
	draftSel = 0
	node := List(&draftComments).Selection(&draftSel).Style(&listBaseStyle).
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
	prevApp, prevOmni, prevHasDraft := uiApp, omni, hasDraft
	prevRows, prevDrafts, prevTitle := vmRows, draftComments, detailTitle
	t.Cleanup(func() {
		uiApp = prevApp
		omni = prevOmni
		hasDraft = prevHasDraft
		vmRows = prevRows
		draftComments = prevDrafts
		detailTitle = prevTitle
	})

	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	detailTitle = "PREVIEWTITLE"
	hasDraft = true
	vmRows = []taskVM{{ID: 1, Title: "a task", Repo: "recap", Glyph: "●", GlyphColor: cBright}}
	draftComments = []draftCommentVM{{Location: "general", Body: "x"}}

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
	prevStore, prevRows, prevSel := uiStore, vmRows, sel
	prevExpanded, prevByID, prevTasks, prevFltr := expandedTasks, taskByID, tasks, repoFltr
	uiStore = st
	expandedTasks = map[int64]bool{}
	repoFltr = ""
	t.Cleanup(func() {
		uiStore = prevStore
		vmRows = prevRows
		sel = prevSel
		expandedTasks = prevExpanded
		taskByID = prevByID
		tasks = prevTasks
		repoFltr = prevFltr
	})

	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", SHA: "base000", Title: "fix me", Status: StatusPending})
	st.AddRevision(id, "fix111", "first fix")

	// collapsed: a single header row showing the latest diff + a ▸ 2 cue
	reloadTasks()
	if len(vmRows) != 1 {
		t.Fatalf("collapsed: want 1 row, got %d", len(vmRows))
	}
	h := vmRows[0]
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
	sel = 0
	toggleExpand()
	if !expandedTasks[id] {
		t.Fatal("task should be marked expanded")
	}
	if len(vmRows) != 3 {
		t.Fatalf("expanded: want header + 2 children = 3 rows, got %d", len(vmRows))
	}
	if vmRows[0].ExpandPill != "▾ 2" {
		t.Fatalf("expanded cue = %q, want \"▾ 2\"", vmRows[0].ExpandPill)
	}
	// children are latest-first, each with its own diff sha
	c1, c2 := vmRows[1], vmRows[2]
	if c1.RevIdx != 1 || c1.DiffSHA != "fix111" || !strings.Contains(c1.RevLabel, "rev 1") || !strings.Contains(c1.RevLabel, "first fix") {
		t.Fatalf("child 1 wrong: %+v", c1)
	}
	if c2.RevIdx != 0 || c2.DiffSHA != "base000" || !strings.Contains(c2.RevLabel, "original") {
		t.Fatalf("child 2 (base) wrong: %+v", c2)
	}

	// render shows the child labels (verify the expansion is visible)
	node := List(&vmRows).Selection(&sel).Style(&listBaseStyle).
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
	if expandedTasks[id] {
		t.Fatal("task should be collapsed")
	}
	if len(vmRows) != 1 {
		t.Fatalf("collapsed again: want 1 row, got %d", len(vmRows))
	}

	// a single-diff task is not expandable
	id2, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", SHA: "solo", Title: "solo", Status: StatusPending})
	reloadTasks()
	for _, vm := range vmRows {
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
	hasDraft = false
	t.Cleanup(func() { hasDraft = false; pane = paneList })
	if got := panes(); len(got) != 2 {
		t.Fatalf("no draft: want 2 panes, got %v", got)
	}
	hasDraft = true
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
	hasDraft = false
	setPane(paneDraft)
	if pane == paneDraft {
		t.Fatal("setPane(draft) should be refused when hasDraft is false")
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
	prevRows, prevSel := vmRows, sel
	t.Cleanup(func() { vmRows = prevRows; sel = prevSel })

	vmRows = []taskVM{
		{ID: 1, Title: "pending", Repo: "recap", Glyph: stateGlyph(StatePending), GlyphColor: stateColor(StatePending), Pending: true, Selected: true},
		{ID: 2, Title: "rework", Repo: "recap", Glyph: stateGlyph(StateRework), GlyphColor: stateColor(StateRework)},
		{ID: 3, Title: "done", Repo: "recap", Glyph: stateGlyph(StateDone), GlyphColor: stateColor(StateDone)},
	}
	sel = 0
	node := List(&vmRows).Selection(&sel).Style(&listBaseStyle).
		SelectedStyle(Style{}).Marker("  ").Render(taskRow)
	tmpl := Build(node)
	buf := NewBuffer(48, 18)
	tmpl.Execute(buf, 48, 18)

	cyan := repoPalette[0] // 0x6f8fa8, the wrong tint the icon used to fall back to
	want := map[rune]Color{
		'●': stateColor(StatePending),
		'↻': stateColor(StateRework),
		'✓': stateColor(StateDone),
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
	c := draftCommentVM{Location: "↳ agent", Indent: "  ", When: "10:20", Body: "renamed it"}
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
	prevLayer := diffLayer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffLayer = NewLayer()
	diffLayer.Render = func() {}
	t.Cleanup(func() {
		uiStore = prev
		uiApp = nil
		omni = nil
		diffLayer = prevLayer
		commentField = InputState{}
		draftComments = nil
		detailDirty = false
	})
	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	_ = id
	reloadTasks()
	sel = 0
	uiApp.SetView(buildMain())
	refreshDetail() // establishes lastSel/lastLen; no comments yet
	if len(draftComments) != 0 {
		t.Fatalf("precondition: expected 0 comments, got %d", len(draftComments))
	}

	commentField.Value = "a general note"
	saveGeneralComment()
	refreshDetail() // selection unchanged → refreshes only if saveGeneralComment marked it dirty

	found := false
	for _, c := range draftComments {
		if c.Body == "a general note" && c.Location == "general" {
			found = true
		}
	}
	if !found {
		t.Fatalf("general comment not shown after save (lost): %+v", draftComments)
	}
}

// the detail briefing follows the selected row's revision: the header shows the
// latest revision's summary (so it updates when a revise lands), and selecting an
// older revision child shows that revision's own summary in full — not the
// original task summary, and not the truncated left-column label.
func TestSummaryFollowsSelectedRevision(t *testing.T) {
	prev, prevLayer := uiStore, diffLayer
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	diffLayer = NewLayer()
	diffLayer.Render = func() {}
	t.Cleanup(func() {
		uiStore = prev
		uiApp = nil
		omni = nil
		diffLayer = prevLayer
		clear(expandedTasks)
		detailDirty = false
	})
	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending, Summary: "original briefing"})
	if _, err := st.AddRevision(id, "deadbeef", "revised briefing"); err != nil {
		t.Fatalf("AddRevision: %v", err)
	}
	expandedTasks[id] = true
	reloadTasks()
	uiApp.SetView(buildMain())

	// vmRows: [header(-1), child rev1 "revised", child rev0 "original"]
	hdr, origChild := -1, -1
	for i, r := range vmRows {
		if r.RevIdx < 0 {
			hdr = i
		} else if r.RevIdx == 0 {
			origChild = i
		}
	}
	if hdr < 0 || origChild < 0 {
		t.Fatalf("expected a header + an original-revision child row, got %+v", vmRows)
	}

	sel = hdr
	detailDirty = true
	refreshDetail()
	if b := flattenSpans(diffBanner); !contains2(b, "revised briefing") {
		t.Fatalf("header should show the latest revision summary, banner=%q", b)
	}

	sel = origChild
	detailDirty = true
	refreshDetail()
	if b := flattenSpans(diffBanner); !contains2(b, "original briefing") {
		t.Fatalf("selecting rev 0 should show its own summary, banner=%q", b)
	}
}
