package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
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
		vmRows, draftComments = nil, nil
		hasDraft = false
		pane = paneList
		draftFocused = 0
		draftScrollOffset, draftScrollVisible, draftScrollTotal = 0, 0, 0
	})
	st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	reloadTasks()

	draftComments = make([]draftCommentVM, 30)
	for i := range draftComments {
		draftComments[i] = draftCommentVM{Body: fmt.Sprintf("comment-%02d", i), Location: "general"}
	}
	hasDraft = true
	pane = paneDraft   // focused on the comments column
	draftFocused = 1.0 // scrollbar fully visible
	draftSel = len(draftComments) - 1

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
	if draftScrollTotal < len(draftComments) {
		t.Fatalf("draftScrollTotal = %d rows, want >= %d (one row per comment min — ScrollState not wired)", draftScrollTotal, len(draftComments))
	}
	if draftScrollVisible <= 0 || draftScrollVisible >= draftScrollTotal {
		t.Fatalf("draftScrollVisible = %d rows, want a window 0 < v < %d (list not windowed)", draftScrollVisible, draftScrollTotal)
	}
	if draftScrollOffset <= 0 {
		t.Fatalf("draftScrollOffset = %d rows, want > 0 (selecting the last comment scrolls the window down)", draftScrollOffset)
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
		draftComments = nil
		draftScrollOffset, draftScrollVisible, draftScrollTotal = 0, 0, 0
	})

	draftComments = make([]draftCommentVM, 20)
	for i := range draftComments {
		// mix flat comments and NESTED replies (indented) with wrapping bodies — the real
		// thread shape. Indent narrows the body's wrap width, so item-height measurement
		// and render must agree on that width or totalRows drifts and the thumb is wrong.
		indent := ""
		if i%3 == 1 {
			indent = "  ↳ "
		} else if i%3 == 2 {
			indent = "    ↳ "
		}
		draftComments[i] = draftCommentVM{
			Location: "general",
			Indent:   indent,
			Body:     fmt.Sprintf("comment %02d with a body long enough to wrap across more than one row in a narrow column", i),
		}
	}
	draftSel = len(draftComments) - 1

	const W, H = 40, 12
	view := VBox.Height(H)(
		HBox.Grow(1)(
			VBox.Grow(1)(
				List(&draftComments).Selection(&draftSel).Marker("  ").
					SelectedStyle(Style{}).Render(draftRow).
					ScrollState(&draftScrollOffset, &draftScrollVisible, &draftScrollTotal),
			),
			ScrollbarDyn(&draftScrollTotal, &draftScrollVisible, &draftScrollOffset),
		),
	)
	tmpl := Build(view)
	buf := NewBuffer(W, H)
	tmpl.Execute(buf, W, H)

	if draftScrollTotal <= H {
		t.Fatalf("setup: content should overflow the %d-row viewport, got total=%d rows", H, draftScrollTotal)
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
		draftComments = nil
		draftScrollOffset, draftScrollVisible, draftScrollTotal = 0, 0, 0
	})

	draftComments = make([]draftCommentVM, 6)
	for i := range draftComments {
		draftComments[i] = draftCommentVM{
			Location: "general",
			Body:     fmt.Sprintf("comment %d — a multi-line body that wraps to several rows in this narrow comments column so the thread overflows the viewport and actually scrolls", i),
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
					List(&draftComments).Selection(&draftSel).Marker("  ").
						SelectedStyle(Style{}).Render(draftRow).
						ScrollState(&draftScrollOffset, &draftScrollVisible, &draftScrollTotal),
				),
				ScrollbarDyn(&draftScrollTotal, &draftScrollVisible, &draftScrollOffset),
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

	draftSel = 0 // top
	topFirst, topLast, topN := thumb()
	draftSel = len(draftComments) - 1 // bottom
	botFirst, botLast, botN := thumb()

	if topN == 0 || botN == 0 {
		t.Fatalf("no thumb rendered (top n=%d, bottom n=%d)", topN, botN)
	}
	if draftScrollTotal <= draftScrollVisible {
		t.Fatalf("content should overflow (total=%d, visible=%d)", draftScrollTotal, draftScrollVisible)
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
// height for BOTH overflowing and short content. (Current code passes in every case —
// a 2-line track only happens on a stale binary / older glyph build.)
func TestDraftScrollbarTrackFullHeight(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		draftComments = nil
		draftScrollOffset, draftScrollVisible, draftScrollTotal = 0, 0, 0
	})

	const W, H = 90, 24
	trackRows := func() int {
		view := VBox.Fill(&cBG).Height(H).Width(W)(
			HBox.Grow(1).Gap(4)(
				VBox.Grow(2).Fill(&cPaneBG)(Text("LEFT")),
				VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0)(
					HBox(SpaceW(3), Text("comments").FG(&cBright).Bold(), Space(), SpaceW(2)),
					SpaceH(2),
					HBox.Grow(1)(
						VBox.Grow(1)(
							List(&draftComments).Selection(&draftSel).Marker("  ").
								SelectedStyle(Style{}).Render(draftRow).
								ScrollState(&draftScrollOffset, &draftScrollVisible, &draftScrollTotal),
						),
						ScrollbarDyn(&draftScrollTotal, &draftScrollVisible, &draftScrollOffset),
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
	draftComments = make([]draftCommentVM, 8)
	for i := range draftComments {
		draftComments[i] = draftCommentVM{Location: "general", Body: fmt.Sprintf("comment %d wraps across several rows in the narrow column overflowing it now", i)}
	}
	draftSel = 0
	if n := trackRows(); n < H-6 {
		t.Fatalf("overflowing: track only %d rows of a %d-row pane — collapsed track (#174 c267)", n, H)
	}

	// short thread (does NOT overflow) — track must STILL be full
	draftComments = []draftCommentVM{{Location: "general", Body: "short one"}, {Location: "general", Body: "short two"}}
	draftSel = 0
	if n := trackRows(); n < H-6 {
		t.Fatalf("short content: track only %d rows of a %d-row pane — collapsed track", n, H)
	}
}
