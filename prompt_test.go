package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// regression: typing must reach commentText through the prompt's PUSHED router.
// The overlay's keys can't live in an On.Modal tree scope — that swallows
// unmatched runes — so openInputPrompt pushes a router whose HandleUnmatched
// routes runes into the text. This dispatches keystrokes through the input stack
// and asserts they land (the "prompt change broke text input" bug).
func TestPromptTypingReachesText(t *testing.T) {
	prevApp := uiApp
	uiApp = NewApp()
	t.Cleanup(func() { uiApp = prevApp; promptOpen = false; promptRouter = nil; setCommentText("") })

	openInputPrompt("comment", "", "", "", func() {})
	if promptRouter == nil {
		t.Fatal("openInputPrompt should push an input router")
	}
	for _, r := range "fix this" {
		uiApp.Input().Dispatch(riffkey.Key{Rune: r})
	}
	if commentText != "fix this" {
		t.Fatalf("typing did not reach commentText through the pushed router: %q", commentText)
	}
	// backspace binding deletes the last rune
	uiApp.Input().Dispatch(riffkey.Key{Special: riffkey.SpecialBackspace})
	if commentText != "fix thi" {
		t.Fatalf("backspace not handled: %q", commentText)
	}
}

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
