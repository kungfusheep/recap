package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
)

// c469: comment bodies (markup-rendered for both sources) must INHERIT the
// background they sit on — the pane fill normally, the selection band when
// selected. span() bakes BG=cBG for the diff panes; the pane's projection must
// zero it (the dark-block artifact). Asserted on rendered buffer cells, per
// the verify-by-render rule.
func TestCommentBodiesInheritBand(t *testing.T) {
	mk := func(id int64, body string) db.TaskComment {
		c := db.TaskComment{}
		c.ID, c.Who, c.Body, c.ReadUser, c.ReadAgent = id, "agent", body, "x", "x"
		return c
	}
	applyDraftComments(1, []db.TaskComment{mk(1, "first comment body text"), mk(2, "second **bold** body text")})
	draftUI.Sel = 1
	for i := range draftUI.Comments {
		draftUI.Comments[i].Visible = true
	}
	t.Cleanup(func() { draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}} })

	// the draft column's structure: pane fill + List with the focus-aware band
	pane := VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle)(
		List(&draftUI.Comments).
			Selection(&draftUI.Sel).
			Style(&listBaseStyle).
			SelectedStyle(&draftSelStyle).
			Marker("  ").
			Render(draftRow),
	)
	buf := NewBuffer(70, 24)
	Build(HBox.Grow(1)(pane)).Execute(buf, 70, 24)
	checked := 0
	for y := 0; y < 24; y++ {
		line := buf.GetLine(y)
		for _, want := range []struct {
			text string
			bg   Color
		}{
			{"first comment body", cPaneBG},
			{"second", draftSelStyle.BG},
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
