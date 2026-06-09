package main

import (
	"os"
	"testing"

	. "github.com/kungfusheep/glyph"
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
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
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
// item opens the TODO panel, and its keys must be live. The omnibox closes (fades) while
// the action opens the todo panel's On.Modal; if the fading omnibox's router were orphaned
// it would shadow the panel (dead keys, kill-to-recover). With the glyph fix the omnibox
// releases its router, so after the render the ONLY active modal is the todo panel —
// depth is base+1 (the panel), never base+2 (orphan + panel).
func TestOmniTodoItemLeavesTodoPanelLive(t *testing.T) {
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
	t.Cleanup(func() { uiStore = prev; uiApp = nil; omni = nil; vmRows = nil; todoOpen = false })
	st.Add(Task{Repo: "r", RepoPath: home + "/r", Title: "t", Status: StatusPending})
	reloadTasks()
	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	base := uiApp.Input().Depth()
	omni.Open()
	uiApp.RenderNow()

	// faithfully reproduce exec() for the todo item: Close() the omnibox, then run its
	// Action (openTodoFor) — exactly what selecting `todo: r` does.
	omni.Close()
	openTodoFor("r", home+"/r")
	uiApp.RenderNow()

	if !todoOpen {
		t.Fatal("todo panel should be open after selecting the todo: item")
	}
	if d := uiApp.Input().Depth(); d != base+1 {
		t.Fatalf("after omnibox→todo, want exactly the todo panel modal (depth base+1=%d), got %d "+
			"(base+2 would mean the omnibox router orphaned and shadowed the panel — dead keys)", base+1, d)
	}
}
