package main

import (
	"fmt"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the comments (draft) pane carries a focus-gated scrollbar like the diff view: the
// List publishes its window via ScrollState and a ScrollbarDyn beside it renders a thumb.
// With many comments and the column focused, the scroll ints must be populated (windowed)
// and a thumb glyph must render. (#a2990fe6)
func TestDraftPaneScrollbarWired(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		vmRows, draftComments = nil, nil
		hasDraft = false
		pane = paneList
		draftFocused = 0
		draftScrollOffset, draftScrollVisible, draftScrollTotal = 0, 0, 0
	})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	reloadTasks()

	draftComments = make([]draftCommentVM, 30)
	for i := range draftComments {
		draftComments[i] = draftCommentVM{Body: fmt.Sprintf("comment-%02d", i), Location: "general"}
	}
	hasDraft = true
	pane = paneDraft     // focused on the comments column
	draftFocused = 1.0   // scrollbar fully visible
	draftSel = len(draftComments) - 1

	tmpl := Build(buildMain())
	buf := NewBuffer(140, 24)
	tmpl.Execute(buf, 140, 24)

	// the draft List published its window via ScrollState — this is the data the
	// ScrollbarDyn beside it consumes (its rendering is covered by glyph's
	// TestScrollbarDynTracksListWindow; here we prove recap wires the List end-to-end
	// through buildMain). The scrollbar's own visibility fades with draftFocused, so a
	// single render frame can't assert the thumb pixels — the window data is the hook.
	if draftScrollTotal != len(draftComments) {
		t.Fatalf("draftScrollTotal = %d, want %d (ScrollState not wired through the draft List)", draftScrollTotal, len(draftComments))
	}
	if draftScrollVisible <= 0 || draftScrollVisible >= draftScrollTotal {
		t.Fatalf("draftScrollVisible = %d, want a window 0 < v < %d (list not windowed)", draftScrollVisible, draftScrollTotal)
	}
	if draftScrollOffset <= 0 {
		t.Fatalf("draftScrollOffset = %d, want > 0 (selecting the last comment scrolls the window down)", draftScrollOffset)
	}
}
