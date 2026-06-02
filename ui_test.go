package main

import (
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

	// after submit the draft is gone → pane hides again
	if _, err := st.SubmitReview(id, VerdictComment, "fyi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	loadDraftPane(id)
	if hasDraft || len(draftComments) != 0 {
		t.Fatalf("expected pane hidden after submit, got hasDraft=%v rows=%d", hasDraft, len(draftComments))
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
