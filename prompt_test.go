package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the comment/todo prompts are OVERLAYS (review #152): they float over the current
// view with the content still visible behind, not a full-screen PushView takeover.
// Verified by render — the inbox shows behind the open prompt panel.
func TestPromptIsOverlay(t *testing.T) {
	st := testStore(t)
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		promptOpen = false
		vmRows = nil
		setCommentText("")
	})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "a task", Status: StatusPending})
	reloadTasks()
	sel = 0

	openComment()
	if !promptOpen {
		t.Fatal("openComment should open the prompt overlay")
	}

	tmpl := Build(buildMain())
	buf := NewBuffer(120, 40)
	tmpl.Execute(buf, 120, 40)
	full := ""
	for y := 0; y < 40; y++ {
		full += buf.GetLine(y) + "\n"
	}
	// inbox visible behind the overlay (a PushView takeover would hide it)
	if !strings.Contains(full, "recap") || !strings.Contains(full, "a task") {
		t.Fatalf("inbox not visible behind overlay:\n%s", full)
	}
	if !strings.Contains(full, "comment") || !strings.Contains(full, "esc cancel") {
		t.Fatalf("prompt overlay not rendered:\n%s", full)
	}
}

// openInputPrompt arms the overlay (prefilled, open); submitPrompt runs the save
// action with commentText then closes+clears; closePrompt cancels without saving.
func TestPromptOpenSubmitClose(t *testing.T) {
	prevApp := uiApp
	uiApp = NewApp()
	t.Cleanup(func() { uiApp = prevApp; promptOpen = false; promptOnSave = nil; setCommentText("") })

	var saved string
	openInputPrompt("edit comment", "", "", "prefilled", func() { saved = commentText })
	if !promptOpen || promptTitle != "edit comment" || commentText != "prefilled" {
		t.Fatalf("openInputPrompt state wrong: open=%v title=%q text=%q", promptOpen, promptTitle, commentText)
	}

	setCommentText("edited body")
	submitPrompt()
	if saved != "edited body" {
		t.Fatalf("submit should pass commentText to save, got %q", saved)
	}
	if promptOpen || commentText != "" {
		t.Fatalf("submit should close + clear, open=%v text=%q", promptOpen, commentText)
	}

	// cancel path: open, then close without invoking save
	saved = ""
	openInputPrompt("comment", "", "", "", func() { saved = "SHOULD-NOT-RUN" })
	setCommentText("discard me")
	closePrompt()
	if promptOpen || commentText != "" || saved != "" {
		t.Fatalf("close should cancel without saving: open=%v text=%q saved=%q", promptOpen, commentText, saved)
	}
}
