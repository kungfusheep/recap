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
	// line-anchored row carries location + snippet
	if draftComments[0].Location != "calc.go · line 3" {
		t.Errorf("row0 location = %q", draftComments[0].Location)
	}
	if draftComments[0].Snippet != "func sub(){}" || draftComments[0].Body != "needs a test" {
		t.Errorf("row0 snippet/body = %q / %q", draftComments[0].Snippet, draftComments[0].Body)
	}
	// general (unanchored) row falls back to "general", no snippet
	if draftComments[1].Location != "general" || draftComments[1].Snippet != "" {
		t.Errorf("row1 = %+v, want general/no-snippet", draftComments[1])
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
	if draftComments[0].ID != cid {
		t.Fatalf("VM should carry comment id %d, got %d", cid, draftComments[0].ID)
	}
}

// the draft pane is ordered by file, then line; general comments sort last.
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
	want := []string{"a.go · line 5", "b.go · line 9", "b.go · line 40", "general"}
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

// regression: the task status icon must never use the cyan repo tint (the
// pre-derived-state bug flagged in review). stateColor governs it now.
func TestStatusIconNotCyan(t *testing.T) {
	cyan := repoPalette[0] // 0x6f8fa8, the old cyan tint
	for _, s := range []string{StatePending, StateRework, StateDone} {
		if c := stateColor(s); c == cyan || c == cHunk {
			t.Fatalf("state %s icon is cyan (%v)", s, c)
		}
	}
}
