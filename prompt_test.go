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
		promptUI.Open = false
		vmRows = nil
		promptUI.Field = InputState{}
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "a task", Status: db.StatusPending})
	reloadTasks()
	sel = 0

	openComment()
	if !promptUI.Open {
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
// save action reading promptUI.Field.Value, then closes+clears; closePrompt cancels.
func TestPromptOpenSubmitClose(t *testing.T) {
	prevApp := uiApp
	uiApp = NewApp()
	t.Cleanup(func() { uiApp = prevApp; promptUI.Open = false; promptUI.OnSave = nil; promptUI.Field = InputState{} })

	var saved string
	promptUI.open("edit comment", "", "", "prefilled", func() { saved = promptUI.Field.Value })
	if !promptUI.Open || promptUI.Title != "edit comment" || promptUI.Field.Value != "prefilled" {
		t.Fatalf("openInputPrompt state wrong: open=%v title=%q val=%q", promptUI.Open, promptUI.Title, promptUI.Field.Value)
	}

	promptUI.Field.Value = "edited body"
	promptUI.submit()
	if saved != "edited body" {
		t.Fatalf("submit should pass promptUI.Field.Value to save, got %q", saved)
	}
	if promptUI.Open || promptUI.Field.Value != "" {
		t.Fatalf("submit should close + clear, open=%v val=%q", promptUI.Open, promptUI.Field.Value)
	}

	saved = ""
	promptUI.open("comment", "", "", "", func() { saved = "SHOULD-NOT-RUN" })
	promptUI.Field.Value = "discard me"
	promptUI.close()
	if promptUI.Open || promptUI.Field.Value != "" || saved != "" {
		t.Fatalf("close should cancel without saving: open=%v val=%q saved=%q", promptUI.Open, promptUI.Field.Value, saved)
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
		promptUI.Open = false
		promptUI.Field = InputState{}
		vmRows = nil
	})
	reloadTasks()

	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	promptUI.open("add todo", "", "", "hello world", func() {})
	uiApp.RenderNow() // prompt overlay renders → its modal router is pushed

	if !uiApp.Input().Dispatch(riffkey.Key{Special: riffkey.SpecialBackspace, Mod: riffkey.ModAlt}) {
		t.Fatal("Alt+Backspace was not handled by the prompt — would fall through")
	}
	if !promptUI.Open {
		t.Fatal("prompt closed on Alt+Backspace — text lost (the reported bug)")
	}
	if promptUI.Field.Value != "hello " {
		t.Fatalf("Alt+Backspace should delete the last word: got %q, want %q", promptUI.Field.Value, "hello ")
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
		promptUI.Open = false
		vmRows, todoUI.Items, todoUI.Data = nil, nil, nil
		promptUI.Field = InputState{}
	})

	todoUI.Title = "TODO · r"
	todoUI.Data = []todo.Item{{IsTask: true, Text: "buy milk"}}
	todoUI.prep()

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
	promptUI.open("add todo", "", "", "", func() {})
	full = render()
	if !strings.Contains(full, "add todo") || !strings.Contains(full, "buy milk") {
		t.Fatalf("prompt should float over the todo editor:\n%s", full)
	}
}

// todo:1a80554a — newlines in text entry. True Shift+Enter is indistinguishable from
// Enter without an enhanced keyboard protocol, so the working-today keys are
// Alt/Option+Enter (riffkey parses ESC+CR as Alt+Enter) and Ctrl+J. Real path: with the
// multiline prompt open, Alt+Enter inserts '\n' and the prompt STAYS open; plain Enter
// still submits. The newline is gated to multiline fields (AllowNewlines), so a
// single-line filter can't be corrupted.
func TestPromptAltEnterInsertsNewline(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		promptUI.Open = false
		promptUI.Field = InputState{}
		vmRows = nil
	})
	reloadTasks()

	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	var saved string
	promptUI.open("add todo", "", "", "line one", func() { saved = promptUI.Field.Value })
	uiApp.RenderNow() // overlay renders → its modal router is pushed

	// Alt+Enter → newline appended at the cursor, prompt still open
	if !uiApp.Input().Dispatch(riffkey.Key{Special: riffkey.SpecialEnter, Mod: riffkey.ModAlt}) {
		t.Fatal("Alt+Enter was not handled")
	}
	if !promptUI.Open {
		t.Fatal("prompt closed on Alt+Enter — should insert a newline and stay open")
	}
	if promptUI.Field.Value != "line one\n" {
		t.Fatalf("Alt+Enter should insert a newline: got %q", promptUI.Field.Value)
	}

	// Ctrl+J does the same
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'j', Mod: riffkey.ModCtrl}) {
		t.Fatal("Ctrl+J was not handled")
	}
	if promptUI.Field.Value != "line one\n\n" {
		t.Fatalf("Ctrl+J should insert a newline: got %q", promptUI.Field.Value)
	}

	// plain Enter still SUBMITS (the modal router's binding wins over the field)
	if !uiApp.Input().Dispatch(riffkey.Key{Special: riffkey.SpecialEnter}) {
		t.Fatal("Enter was not handled")
	}
	if promptUI.Open {
		t.Fatal("plain Enter should still submit and close the prompt")
	}
	if saved != "line one\n\n" {
		t.Fatalf("submit should save the multiline body: got %q", saved)
	}
}
