package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the comments (draft) pane carries a focus-gated scrollbar like the diff view: the
// List publishes its window via ScrollState and a ScrollbarDyn beside it renders a thumb.
// With many comments and the column focused, the scroll ints must be populated (windowed)
// and a thumb glyph must render. (#a2990fe6)
func TestDraftPaneScrollbarWired(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		inboxUI.Rows, draftUI.Comments = nil, nil
		draftUI.Has = false
		pane = paneList
		draftUI.Focused = 0
		draftUI.ScrollOffset, draftUI.ScrollVisible, draftUI.ScrollTotal = 0, 0, 0
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()

	draftUI.Comments = make([]draftCommentVM, 30)
	for i := range draftUI.Comments {
		draftUI.Comments[i] = draftCommentVM{Body: fmt.Sprintf("comment-%02d", i), Location: "general", Visible: true}
	}
	draftUI.Has = true
	pane = paneDraft      // focused on the comments column
	draftUI.Focused = 1.0 // scrollbar fully visible
	draftUI.Sel = len(draftUI.Comments) - 1

	tmpl := Build(buildMain())
	buf := NewBuffer(140, 24)
	tmpl.Execute(buf, 140, 24)

	// the draft List published its window via ScrollState — this is the data the
	// ScrollbarDyn beside it consumes (its rendering is covered by glyph's
	// TestScrollbarDynTracksListWindow; here we prove recap wires the List end-to-end
	// through buildMain). The values are in SCREEN ROWS (not item counts), so the bar
	// tracks the visual scroll even with variable-height comments — total must be at
	// least one row per comment, the window a strict subset, and the offset > 0 once the
	// last comment is selected.
	if draftUI.ScrollTotal < len(draftUI.Comments) {
		t.Fatalf("draftUI.ScrollTotal = %d rows, want >= %d (one row per comment min — ScrollState not wired)", draftUI.ScrollTotal, len(draftUI.Comments))
	}
	if draftUI.ScrollVisible <= 0 || draftUI.ScrollVisible >= draftUI.ScrollTotal {
		t.Fatalf("draftUI.ScrollVisible = %d rows, want a window 0 < v < %d (list not windowed)", draftUI.ScrollVisible, draftUI.ScrollTotal)
	}
	if draftUI.ScrollOffset <= 0 {
		t.Fatalf("draftUI.ScrollOffset = %d rows, want > 0 (selecting the last comment scrolls the window down)", draftUI.ScrollOffset)
	}
}

// end-to-end thumb check for the draft pane: real draftRow heights (variable, with a
// wrapping TextBlock body) feed ScrollState, which feeds a bare ScrollbarDyn (no opacity,
// so a single render shows the thumb). With the last of many tall comments selected the
// thumb must be PARTIAL (content overflows) and sit toward the BOTTOM (scrolled down) —
// catching any height-measurement bug between draftRow and the bar.
func TestDraftScrollbarThumbPartialAndPositioned(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		draftUI.Comments = nil
		draftUI.ScrollOffset, draftUI.ScrollVisible, draftUI.ScrollTotal = 0, 0, 0
	})

	draftUI.Comments = make([]draftCommentVM, 20)
	for i := range draftUI.Comments {
		// mix flat comments and NESTED replies (indented) with wrapping bodies — the real
		// thread shape. Indent narrows the body's wrap width, so item-height measurement
		// and render must agree on that width or totalRows drifts and the thumb is wrong.
		indent := ""
		if i%3 == 1 {
			indent = "  ↳ "
		} else if i%3 == 2 {
			indent = "    ↳ "
		}
		draftUI.Comments[i] = draftCommentVM{
			Location: "general",
			Indent:   indent,
			Body:     fmt.Sprintf("comment %02d with a body long enough to wrap across more than one row in a narrow column", i),
			Visible:  true,
		}
	}
	draftUI.Sel = len(draftUI.Comments) - 1

	const W, H = 40, 12
	view := VBox.Height(H)(
		HBox.Grow(1)(
			VBox.Grow(1)(
				List(&draftUI.Comments).Selection(&draftUI.Sel).Marker("  ").
					SelectedStyle(Style{}).Render(draftRow).
					ScrollState(&draftUI.ScrollOffset, &draftUI.ScrollVisible, &draftUI.ScrollTotal),
			),
			ScrollbarDyn(&draftUI.ScrollTotal, &draftUI.ScrollVisible, &draftUI.ScrollOffset),
		),
	)
	tmpl := Build(view)
	buf := NewBuffer(W, H)
	tmpl.Execute(buf, W, H)

	if draftUI.ScrollTotal <= H {
		t.Fatalf("setup: content should overflow the %d-row viewport, got total=%d rows", H, draftUI.ScrollTotal)
	}

	// scan the rightmost column for the thumb
	col := W - 1
	firstThumb, lastThumb, thumbRows := -1, -1, 0
	for y := 0; y < H; y++ {
		r := buf.Get(col, y).Rune
		if r != '│' && r != ' ' && r != 0 {
			thumbRows++
			if firstThumb < 0 {
				firstThumb = y
			}
			lastThumb = y
		}
	}
	if thumbRows == 0 {
		t.Fatal("no scrollbar thumb rendered in the draft pane column")
	}
	if thumbRows >= H {
		t.Fatalf("thumb fills the whole track (%d/%d) — should be partial since content overflows", thumbRows, H)
	}
	// last comment selected → scrolled to the bottom → thumb should reach the last row
	if lastThumb != H-1 {
		t.Fatalf("thumb bottom at row %d, want %d (scrolled to the end, thumb should touch the bottom)", lastThumb, H-1)
	}
	if firstThumb == 0 {
		t.Fatalf("thumb starts at the top while scrolled to the bottom (rows %d..%d) — position not tracking", firstThumb, lastThumb)
	}
}

// faithful geometry repro of the draft pane (header + SpaceH(2) + HBox(list, scrollbar),
// CascadeStyle, narrow column) minus the opacity — so a single render shows the thumb.
// Renders a small thread (like the reviewer's 6 tallish comments) at top and at bottom and
// checks the thumb tracks: at top it starts at the track top, at bottom it reaches the
// track bottom, and it's the SAME partial size both times. Catches header-offset / track-
// height bugs the isolated test misses.
func TestDraftPaneScrollbarGeometry(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		draftUI.Comments = nil
		draftUI.ScrollOffset, draftUI.ScrollVisible, draftUI.ScrollTotal = 0, 0, 0
	})

	draftUI.Comments = make([]draftCommentVM, 6)
	for i := range draftUI.Comments {
		draftUI.Comments[i] = draftCommentVM{
			Location: "general",
			Body:     fmt.Sprintf("comment %d — a multi-line body that wraps to several rows in this narrow comments column so the thread overflows the viewport and actually scrolls", i),
			Visible:  true,
		}
	}

	const W, H = 34, 18 // ~the draft column width in a normal terminal
	// mirror buildMain's draft pane structure (sans the focus-fade opacity)
	pane := func() Component {
		return VBox.Height(H).Width(W).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0)(
			HBox(SpaceW(3), Text("comments").FG(&cBright).Bold(), Space(), SpaceW(2)),
			SpaceH(2),
			HBox.Grow(1)(
				VBox.Grow(1)(
					List(&draftUI.Comments).Selection(&draftUI.Sel).Marker("  ").
						SelectedStyle(Style{}).Render(draftRow).
						ScrollState(&draftUI.ScrollOffset, &draftUI.ScrollVisible, &draftUI.ScrollTotal),
				),
				ScrollbarDyn(&draftUI.ScrollTotal, &draftUI.ScrollVisible, &draftUI.ScrollOffset),
			),
		)
	}
	thumb := func() (first, last, n int) {
		tmpl := Build(pane())
		buf := NewBuffer(W, H)
		tmpl.Execute(buf, W, H)
		first, last = -1, -1
		for y := 0; y < H; y++ {
			r := buf.Get(W-1, y).Rune
			if r != '│' && r != ' ' && r != 0 {
				if first < 0 {
					first = y
				}
				last = y
				n++
			}
		}
		return
	}

	draftUI.Sel = 0 // top
	topFirst, topLast, topN := thumb()
	draftUI.Sel = len(draftUI.Comments) - 1 // bottom
	botFirst, botLast, botN := thumb()

	if topN == 0 || botN == 0 {
		t.Fatalf("no thumb rendered (top n=%d, bottom n=%d)", topN, botN)
	}
	if draftUI.ScrollTotal <= draftUI.ScrollVisible {
		t.Fatalf("content should overflow (total=%d, visible=%d)", draftUI.ScrollTotal, draftUI.ScrollVisible)
	}
	// the scrollbar sits BELOW the header (its track = the list area, not the whole pane),
	// so the thumb at top starts at the track top and at bottom reaches the track bottom.
	// Correct behaviour: same thumb SIZE both ways, and it moves strictly DOWN when scrolled.
	if topN != botN {
		t.Fatalf("thumb changed size between top (%d cells) and bottom (%d cells) — should be constant", topN, botN)
	}
	if botFirst <= topFirst || botLast <= topLast {
		t.Fatalf("scrolling to the bottom must move the thumb DOWN (top %d..%d, bottom %d..%d)", topFirst, topLast, botFirst, botLast)
	}
	// the thumb must be a real PARTIAL bar (a few cells), not most of the track
	trackRows := botLast - topFirst + 1
	if topN >= trackRows {
		t.Fatalf("thumb (%d cells) fills the whole ~%d-row track — should be partial for an overflowing thread", topN, trackRows)
	}
}

// regression for #174 c267 ("the track should be the size of the display, but it's like
// 2 lines high"): the scrollbar TRACK must span the full list column height, not collapse.
// Renders the draft pane in the real Grow-based column layout (VBox.Grow column, the
// HBox.Grow(1)(VBox.Grow(1)(List), Scrollbar) body) and asserts the track ≈ the column
// height for BOTH overflowing and short content. CRUCIALLY the pane is wrapped in
// If(&draftUI.Has) exactly like buildMain — glyph lays an If branch out at CONTENT height
// (layout(0)) and only stretches the branch ROOT afterwards, so without the glyph fix
// the branch's internal flex never sees the real height and the scrollbar collapses to
// ~0-2 rows (the "track is like 2 lines high" bug, #174 c267).
func TestDraftScrollbarTrackFullHeight(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		draftUI.Comments = nil
		draftUI.ScrollOffset, draftUI.ScrollVisible, draftUI.ScrollTotal = 0, 0, 0
	})

	const W, H = 90, 24
	hasDraftLocal := true
	trackRows := func() int {
		view := VBox.Fill(&cBG).Height(H).Width(W)(
			HBox.Grow(1).Gap(4)(
				VBox.Grow(2).Fill(&cPaneBG)(Text("LEFT")),
				// the If wrapper is the load-bearing part of this repro (see doc comment)
				If(&hasDraftLocal).Then(
					VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0)(
						HBox(SpaceW(3), Text("comments").FG(&cBright).Bold(), Space(), SpaceW(2)),
						SpaceH(2),
						HBox.Grow(1)(
							VBox.Grow(1)(
								List(&draftUI.Comments).Selection(&draftUI.Sel).Marker("  ").
									SelectedStyle(Style{}).Render(draftRow).
									ScrollState(&draftUI.ScrollOffset, &draftUI.ScrollVisible, &draftUI.ScrollTotal),
							),
							ScrollbarDyn(&draftUI.ScrollTotal, &draftUI.ScrollVisible, &draftUI.ScrollOffset),
						),
					),
				),
			),
		)
		buf := NewBuffer(W, H)
		Build(view).Execute(buf, W, H)
		n := 0
		for y := 0; y < H; y++ {
			if r := buf.Get(W-1, y).Rune; r == '│' || r == '█' || (r >= '▁' && r <= '▇') {
				n++
			}
		}
		return n
	}

	// overflowing thread
	draftUI.Comments = make([]draftCommentVM, 8)
	for i := range draftUI.Comments {
		draftUI.Comments[i] = draftCommentVM{Location: "general", Body: fmt.Sprintf("comment %d wraps across several rows in the narrow column overflowing it now", i), Visible: true}
	}
	draftUI.Sel = 0
	if n := trackRows(); n < H-6 {
		t.Fatalf("overflowing: track only %d rows of a %d-row pane — collapsed track (#174 c267)", n, H)
	}

	// short thread (does NOT overflow) — track must STILL be full
	draftUI.Comments = []draftCommentVM{{Location: "general", Body: "short one", Visible: true}, {Location: "general", Body: "short two", Visible: true}}
	draftUI.Sel = 0
	if n := trackRows(); n < H-6 {
		t.Fatalf("short content: track only %d rows of a %d-row pane — collapsed track", n, H)
	}
}

// the comments List owns the selection band now (SelectedStyle(&draftSelStyle),
// m69 item 2): the selected comment's cells carry the focus-aware band BG, a
// non-selected row does not, and dropping focus dims the band. Asserted on the
// rendered buffer cells (not on helper return values).
func TestDraftSelectionBandPaintedByList(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		inboxUI.Rows = nil
		pane = paneList
		applyPaneFocus()
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()
	draftUI.Comments = []draftCommentVM{
		{Location: "general", Body: "FIRSTBODY", Visible: true},
		{Location: "general", Body: "SECONDBODY", Visible: true},
	}
	draftUI.Has = true
	draftUI.Sel = 0
	pane = paneDraft
	applyPaneFocus()

	render := func() *Buffer {
		tmpl := Build(buildMain())
		buf := NewBuffer(140, 30)
		tmpl.Execute(buf, 140, 30)
		return buf
	}
	cellBG := func(buf *Buffer, needle string) Color {
		for y := 0; y < 30; y++ {
			var line string
			for x := 0; x < 140; x++ {
				line += string(buf.Get(x, y).Rune)
			}
			if i := strings.Index(line, needle); i >= 0 {
				return buf.Get(i, y).Style.BG
			}
		}
		t.Fatalf("%q not rendered", needle)
		return Color{}
	}

	buf := render()
	if got := cellBG(buf, "FIRSTBODY"); got != cSelBG {
		t.Fatalf("selected row band BG = %v, want focused cSelBG %v", got, cSelBG)
	}
	if got := cellBG(buf, "SECONDBODY"); got == cSelBG {
		t.Fatalf("non-selected row carries the selection band")
	}

	// focus leaves the column → the band dims to cFloat (List re-reads *Style)
	pane = paneDiff
	applyPaneFocus()
	buf = render()
	if got := cellBG(buf, "FIRSTBODY"); got != cFloat {
		t.Fatalf("unfocused band BG = %v, want dim cFloat %v", got, cFloat)
	}
}
