package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

// c469: thread comment bodies must INHERIT the background they sit on — the
// pane fill normally, the selection band when selected. span() bakes BG=cBG
// for the diff panes; projected thread rows must not carry it (the dark-block
// artifact). Asserted on rendered buffer cells, per the verify-by-render rule.
func TestPropThreadBodiesInheritBand(t *testing.T) {
	mk := func(body string) propThreadVM {
		vm := propThreadVM{Who: "Agent", WhoColor: cBright, Location: "general", When: "13:51"}
		for _, row := range summaryBody(body, 1<<20) {
			for i := range row {
				row[i].Style.BG = Color{}
			}
			vm.BodyRows = append(vm.BodyRows, propRowVM{Spans: row})
		}
		return vm
	}
	propUI.Thread = []propThreadVM{mk("first comment body text"), mk("second **bold** body text")}
	propUI.HasThread = true
	propUI.ThreadSel = 1
	t.Cleanup(func() { propUI = propView{Commented: map[int]bool{}} })

	buf := NewBuffer(70, 24)
	// flex context like the app's column row — standalone the pane has no
	// parent to stretch it and rows wrap uselessly narrow
	Build(HBox.Grow(1)(propThreadPane())).Execute(buf, 70, 24)
	checked := 0
	for y := 0; y < 24; y++ {
		line := buf.GetLine(y)
		for _, want := range []struct {
			text string
			bg   Color
		}{
			{"first comment body", cPaneBG},        // unselected row: pane fill
			{"second", draftSelStyle.BG},           // selected row: the band
		} {
			if x := index(line, want.text); x >= 0 {
				if got := buf.Get(x, y).Style.BG; got != want.bg {
					t.Fatalf("%q cell bg = %v, want %v", want.text, got, want.bg)
				}
				checked++
			}
		}
	}
	if checked < 2 {
		t.Fatalf("expected both body rows on screen, checked %d", checked)
	}
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
