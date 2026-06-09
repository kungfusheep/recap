package main

import (
	"github.com/kungfusheep/recap/db"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/todo"
	"github.com/kungfusheep/riffkey"
)

// the comment/todo prompts are OVERLAYS: they float over the current view with the
// content still visible behind, not a full-screen PushView takeover. Verified by
// render — the inbox shows behind the open prompt panel.
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
		commentField = InputState{}
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "a task", Status: db.StatusPending})
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
	if !strings.Contains(full, "recap") || !strings.Contains(full, "a task") {
		t.Fatalf("inbox not visible behind overlay:\n%s", full)
	}
	if !strings.Contains(full, "comment") || !strings.Contains(full, "esc cancel") {
		t.Fatalf("prompt overlay not rendered:\n%s", full)
	}
}

// openInputPrompt arms the overlay (prefilled field, open); submitPrompt runs the
// save action reading commentField.Value, then closes+clears; closePrompt cancels.
func TestPromptOpenSubmitClose(t *testing.T) {
	prevApp := uiApp
	uiApp = NewApp()
	t.Cleanup(func() { uiApp = prevApp; promptOpen = false; promptOnSave = nil; commentField = InputState{} })

	var saved string
	openInputPrompt("edit comment", "", "", "prefilled", func() { saved = commentField.Value })
	if !promptOpen || promptTitle != "edit comment" || commentField.Value != "prefilled" {
		t.Fatalf("openInputPrompt state wrong: open=%v title=%q val=%q", promptOpen, promptTitle, commentField.Value)
	}

	commentField.Value = "edited body"
	submitPrompt()
	if saved != "edited body" {
		t.Fatalf("submit should pass commentField.Value to save, got %q", saved)
	}
	if promptOpen || commentField.Value != "" {
		t.Fatalf("submit should close + clear, open=%v val=%q", promptOpen, commentField.Value)
	}

	saved = ""
	openInputPrompt("comment", "", "", "", func() { saved = "SHOULD-NOT-RUN" })
	commentField.Value = "discard me"
	closePrompt()
	if promptOpen || commentField.Value != "" || saved != "" {
		t.Fatalf("close should cancel without saving: open=%v val=%q saved=%q", promptOpen, commentField.Value, saved)
	}
}

// regression (todo:6d79ece2): opt+Backspace in the prompt must EDIT the text (delete a
// word), not close the dialog and lose everything. The fix is upstream (riffkey parses
// ESC+0x7f as Alt+Backspace instead of a lone Escape), but verify the real recap path:
// with the prompt open, dispatching Alt+Backspace deletes a word AND leaves the prompt
// open (the modal's Esc binding must not fire).
func TestPromptAltBackspaceEditsNotCloses(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		promptOpen = false
		commentField = InputState{}
		vmRows = nil
	})
	reloadTasks()

	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	openInputPrompt("add todo", "", "", "hello world", func() {})
	uiApp.RenderNow() // prompt overlay renders → its modal router is pushed

	if !uiApp.Input().Dispatch(riffkey.Key{Special: riffkey.SpecialBackspace, Mod: riffkey.ModAlt}) {
		t.Fatal("Alt+Backspace was not handled by the prompt — would fall through")
	}
	if !promptOpen {
		t.Fatal("prompt closed on Alt+Backspace — text lost (the reported bug)")
	}
	if commentField.Value != "hello " {
		t.Fatalf("Alt+Backspace should delete the last word: got %q, want %q", commentField.Value, "hello ")
	}
}

// the TODO editor is its own named view (buildTodoView, reached via app.Go), and the
// add/edit prompt renders IN that view (inputPromptOverlay) so it floats over the editor
// — the prompt behaves like the inbox's: one view, consistent modal push/pop.
func TestTodoEditorInTodoView(t *testing.T) {
	st := testStore(t)
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		promptOpen = false
		vmRows, todoItems, todoData = nil, nil, nil
		commentField = InputState{}
	})

	todoTitle = "TODO · r"
	todoData = []todo.Item{{IsTask: true, Text: "buy milk"}}
	todoPrep()

	render := func() string {
		tmpl := Build(buildTodoView())
		buf := NewBuffer(120, 40)
		tmpl.Execute(buf, 120, 40)
		s := ""
		for y := 0; y < 40; y++ {
			s += buf.GetLine(y) + "\n"
		}
		return s
	}

	full := render()
	if !strings.Contains(full, "TODO · r") || !strings.Contains(full, "buy milk") {
		t.Fatalf("todo editor not rendered in buildTodoView:\n%s", full)
	}

	// the prompt floats over the editor (same view)
	openInputPrompt("add todo", "", "", "", func() {})
	full = render()
	if !strings.Contains(full, "add todo") || !strings.Contains(full, "buy milk") {
		t.Fatalf("prompt should float over the todo editor:\n%s", full)
	}
}
