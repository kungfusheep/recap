package main

import (
	"github.com/kungfusheep/recap/db"
	"os"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// the command palette must expose every action (not just quit) so the keys are
// discoverable, and each entry must be wired to a handler with searchable text.
func TestOmniCommandsCoverActions(t *testing.T) {
	cmds := omniCommands()

	want := []string{
		"approve", "comment", "submit (amends)", "unsubmit → inbox",
		"re-run verification (--check)", "filter repo", "next pane", "previous pane", "help", "quit",
	}
	have := map[string]omniItem{}
	for _, c := range cmds {
		have[c.Label] = c
	}
	for _, label := range want {
		it, ok := have[label]
		if !ok {
			t.Fatalf("palette missing %q action", label)
		}
		if it.Action == nil {
			t.Fatalf("%q has no Action wired", label)
		}
		if it.Section == "" {
			t.Fatalf("%q has no Section", label)
		}
		if omniSearchText(&it) == "" {
			t.Fatalf("%q has empty search text", label)
		}
	}
	// guard against regressing back to the single quit-only palette
	if len(cmds) < len(want) {
		t.Fatalf("palette shrank to %d commands, want >= %d", len(cmds), len(want))
	}
}

// regression (#162/#165): an omnibox action that opens another modal (e.g. todo:) must
// not leave the omnibox's modal router orphaned — dead keys until kill. The fix lives in
// glyph (an exiting overlay releases its router), so exec needs NO manual drain: once the
// omnibox closes and renders out, the input stack is back to base.
func TestOmniActionNoOrphanedRouter(t *testing.T) {
	prev := uiStore
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() { uiStore = prev; uiApp = nil; omni = nil; vmRows = nil })
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()
	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	base := uiApp.Input().Depth()
	omni.Open()
	uiApp.RenderNow()
	if uiApp.Input().Depth() <= base {
		t.Fatalf("omnibox should push a modal router (base=%d)", base)
	}
	omni.exec()       // closes the omnibox + runs the action — no manual drain
	uiApp.RenderNow() // the omnibox exits → glyph releases its router
	if d := uiApp.Input().Depth(); d != base {
		t.Fatalf("omnibox router orphaned after exec+render: depth %d, want base %d", d, base)
	}
}

// regression (#165): the EXACT reported path — selecting the `todo:<project>` omnibox
// item opens the TODO editor, and its keys must be live. Opening it switches to the
// named "todo" view via app.Go, which deactivates the inbox view and POPS the omnibox
// modal it had pushed (detachRouteScopes) — deterministically, not via a fade-out
// animation. So afterwards the todo view is active with ONLY its base router (depth 1):
// no orphaned omnibox router shadowing it (the dead-keys / kill-to-recover bug). The
// old If(&todoOpen)-panel kept the inbox view active, leaving the omnibox to self-release
// on a render that animation timing could defer — which is why it was still broken.
func TestOmniTodoItemSwitchesToTodoViewCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RECAP_CONFIG", home+"/config.toml")
	if err := os.WriteFile(home+"/config.toml", []byte("todo_template = \"~/notes/{relpath}/TODO.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home+"/notes/r", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/notes/r/TODO.md", []byte("- [ ] one\n- [ ] two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := uiStore
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() { uiStore = prev; uiApp = nil; omni = nil; vmRows = nil })
	st.Add(db.Task{Repo: "r", RepoPath: home + "/r", Title: "t", Status: db.StatusPending})
	reloadTasks()
	// register the same named views runUI does, and start on main
	uiApp.View("main", buildMain()).NoCounts()
	uiApp.View("todo", buildTodoView()).NoCounts()
	uiApp.Go("main")
	uiApp.RenderNow()

	// open the omnibox → it pushes its modal router over the inbox view
	omni.Open()
	uiApp.RenderNow()
	if d := uiApp.Input().Depth(); d <= 1 {
		t.Fatalf("omnibox should push a modal router over main (depth %d)", d)
	}

	// faithfully reproduce exec() for the todo item: Close() the omnibox, then run its
	// Action (openTodoFor) — exactly what selecting `todo: r` does. openTodoFor calls
	// app.Go("todo").
	omni.Close()
	todoUI.openFor("r", home+"/r")
	uiApp.RenderNow()

	if v := uiApp.CurrentView(); v != "todo" {
		t.Fatalf("selecting the todo: item should switch to the todo view, got %q", v)
	}
	if d := uiApp.Input().Depth(); d != 1 {
		t.Fatalf("todo view should be active with ONLY its base router (depth 1), got %d "+
			"(>1 means the omnibox modal was orphaned across the view switch — dead keys)", d)
	}

	// the heart of the bug was DEAD KEYS — prove a todo key actually fires now. openTodoFor
	// opened scrolled to the bottom (todoUI.Sel = last); 'k' must move the selection up. If the
	// omnibox router were orphaned it would swallow this and todoUI.Sel wouldn't budge.
	if todoUI.Sel != len(todoUI.Items)-1 {
		t.Fatalf("setup: todo should open at the last item, got sel=%d of %d", todoUI.Sel, len(todoUI.Items))
	}
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'k'}) {
		t.Fatal("'k' was not handled by the active router — todo keys are dead")
	}
	if todoUI.Sel != len(todoUI.Items)-2 {
		t.Fatalf("'k' should move the todo selection up to %d, got %d (keys not live in the todo view)", len(todoUI.Items)-2, todoUI.Sel)
	}

	// 'q' closes the editor → app.Go("main"); we must land back on the inbox view with a
	// balanced stack (the round-trip, not a one-way trip into a stuck view).
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'q'}) {
		t.Fatal("'q' was not handled in the todo view")
	}
	uiApp.RenderNow()
	if v := uiApp.CurrentView(); v != "main" {
		t.Fatalf("'q' should return to the inbox view, got %q", v)
	}
	if d := uiApp.Input().Depth(); d != 1 {
		t.Fatalf("back on the inbox view the stack should be base 1, got %d", d)
	}
}
