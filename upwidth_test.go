package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
)

// upcomingWidth is computed at resize EVENTS (int16(float32(w)*inboxColPct)),
// not read from layout output per frame — this pins that the formula matches
// glyph's actual WidthPct layout at assorted terminal widths, so drift in
// glyph's layout math fails loudly here instead of mis-sizing the band.
func TestUpcomingWidthFormulaMatchesLayout(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()

	for _, w := range []int{80, 100, 121, 140, 173, 200} {
		tmpl := Build(buildMain())
		buf := NewBuffer(w, 30)
		tmpl.Execute(buf, int16(w), 30)
		if got, want := int16(listPaneRef.W), int16(float32(w)*inboxColPct); got != want {
			t.Fatalf("width %d: layout col W=%d, formula gives %d — formula drifted from glyph layout", w, got, want)
		}
	}
}
