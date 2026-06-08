package main

import (
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
