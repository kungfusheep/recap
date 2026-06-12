package main

import (
	"fmt"
	"strings"
	"sync"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
)

// The proposal detail pane — the SAME aesthetic as the task diff/comments
// layout, but its own code top to bottom (c454): own layer + template + line
// meta + thread column + staged loader, so proposal features progress without
// touching the diff machinery (and vice versa). Nothing here writes diffUI or
// draftUI.

// propOpen caches the open-proposal count for the header badge — set by
// applyInbox alongside the rest of the reload's projections.
var propOpen int

type propLineMeta struct {
	Line        int    // 1-based SOURCE line in the stored document
	Text        string // the source line (trimmed) — the comment snippet
	Commentable bool
}

type propRowVM struct {
	Spans []Span
	BG    Color
}

// propThreadVM is one comment row in the proposal's thread column — the
// draft pane's look (location line, snippet, body, time), proposal-owned.
type propThreadVM struct {
	Who      string
	WhoColor Color
	Location string
	Snippet  string
	Body     string
	When     string
}

// propView is the pane's state in one concrete struct: the document layer +
// row/meta projection, the pick machinery, and the thread column. One bound
// package instance (propUI) — stable var, pointer-bound into compiled views.
type propView struct {
	Active bool // a proposal row is selected → the middle column shows this pane

	// the document source, set at apply time; prepPropRows projects it into
	// Rows/Meta at the layer's width (content/size change only, never per frame).
	Prop    db.Proposal
	Parties []string

	Layer   *Layer
	Tmpl    *Template
	Rows    []propRowVM
	Meta    []propLineMeta // parallel to Rows: jump/anchor coordinates by row
	ViewRef NodeRef        // screen rect of the LayerView, for jump-target coords

	Commented map[int]bool // source lines carrying a comment → gutter wash

	Thread    []propThreadVM
	ThreadSel int
	HasThread bool
	Note      string // thread column header pill ("3 comments")

	Focused float64 // scrollbar fade target, mirrors the diff pane's cue
	PaneRef NodeRef // thread column's screen rect — focus line + shade target
}

var propUI = propView{Commented: map[int]bool{}}

// --- staged loader -----------------------------------------------------------

// propResult is the staged fetch: raw db rows, projected thread VMs, washes.
type propResult struct {
	key     string
	reset   bool
	prop    db.Proposal
	parties []string
	thread  []propThreadVM
	washes  map[int]bool
}

var (
	propMu     sync.Mutex
	propStaged *propResult
)

func stageProp(r *propResult) {
	propMu.Lock()
	propStaged = r
	propMu.Unlock()
}

func takeStagedProp() *propResult {
	propMu.Lock()
	r := propStaged
	propStaged = nil
	propMu.Unlock()
	return r
}

// propDetailKick dispatches the proposal fetch — the staged hand-off seam,
// proposal-owned. A package var so tests can run it synchronously.
var propDetailKick = func(p db.Proposal, key string, reset bool) {
	app := uiApp // snapshot: the goroutine must not read the mutable global
	go func() {
		stageProp(fetchPropDetail(p, key, reset))
		if app != nil {
			app.RequestRender()
		}
	}()
}

// fetchPropDetail does the I/O off the render thread: re-reads the proposal
// (the row snapshot may be stale), its parties, and projects the thread.
func fetchPropDetail(p db.Proposal, key string, reset bool) *propResult {
	r := &propResult{key: key, reset: reset, prop: p, washes: map[int]bool{}}
	if uiStore == nil {
		return r
	}
	if fresh, err := uiStore.ProposalByID(p.ID); err == nil {
		r.prop = fresh
	}
	r.parties, _ = uiStore.ProposalParties(p.ID)
	comments, _ := uiStore.ProposalComments(p.ID)
	for _, c := range comments {
		vm := propThreadVM{
			Who:      propSender(c.WhoName, c.WhoRepo),
			WhoColor: cBright,
			Body:     c.Body,
			When:     shortStamp(c.CreatedAt),
			Location: "general",
		}
		// the human's comments carry a name too (todo:5a724f62) — "You", to
		// read alongside the agents' identity-coloured names.
		if c.WhoRepo == "" {
			vm.Who = "You"
		}
		if c.WhoRepo != "" {
			if _, ic := loadIdentity(c.WhoRepo); ic.Mode != 0 {
				vm.WhoColor = ic
			}
		}
		if c.Line > 0 {
			vm.Location = fmt.Sprintf("document · line %d", c.Line)
			vm.Snippet = cleanLine(c.Snippet)
			r.washes[c.Line] = true
		}
		r.thread = append(r.thread, vm)
	}
	return r
}

// drainPropDetail swaps a staged fetch into the pane — render thread, called
// from the staged-apply seam. A result keyed for a selection we've moved off
// is dropped (the newer kick's result is on its way).
func drainPropDetail() {
	r := takeStagedProp()
	if r == nil {
		return
	}
	if !propUI.Active || r.prop.ID != propUI.Prop.ID {
		return
	}
	propUI.Prop, propUI.Parties = r.prop, r.parties
	propUI.Thread = r.thread
	propUI.HasThread = len(r.thread) > 0
	propUI.Note = fmt.Sprintf("%d comment%s", len(r.thread), plural(len(r.thread)))
	propUI.Commented = r.washes
	if propUI.ThreadSel >= len(propUI.Thread) {
		propUI.ThreadSel = len(propUI.Thread) - 1
	}
	if propUI.ThreadSel < 0 {
		propUI.ThreadSel = 0
	}
	if propUI.Layer != nil {
		if r.reset {
			propUI.Layer.ScrollToTop()
		}
		propUI.Layer.Invalidate()
	}
}

// openPropDetail switches the detail area to a proposal — called by
// refreshDetailNow when a proposal row is selected.
func openPropDetail(p db.Proposal, key string, reset bool) {
	propUI.Active = true
	propUI.Prop = p
	// the task panes go dark: the draft column is task-only again (no gates).
	draftUI.Has, draftUI.Comments = false, nil
	propDetailKick(p, key, reset)
}

// closePropDetail returns the detail area to task content.
func closePropDetail() {
	propUI.Active = false
	propUI.HasThread = false
}

// --- document projection -----------------------------------------------------

// renderPropLayer mirrors renderDiffLayer for the document: project rows at
// the viewport width, register jump targets for commentable lines while a
// pick is live, blit, restore scroll.
func renderPropLayer() {
	w := propUI.Layer.ViewportWidth()
	if w <= 0 {
		return
	}
	prepPropRows(w - 2)

	h := len(propUI.Rows)
	if vh := propUI.Layer.ViewportHeight(); h < vh {
		h = vh
	}
	buf := NewBuffer(w, h)
	propTemplate().Execute(buf, int16(w), int16(h))

	if uiApp != nil && uiApp.JumpModeActive() {
		top, vh := propUI.Layer.ScrollY(), propUI.Layer.ViewportHeight()
		lblStyle := Style{FG: cBG, BG: cHunk, Attr: AttrBold}
		for y := top; y < top+vh && y < len(propUI.Meta); y++ {
			if !propUI.Meta[y].Commentable {
				continue
			}
			row := y
			sx, sy := propUI.ViewRef.X, propUI.ViewRef.Y+(y-top)
			uiApp.AddJumpTarget(int16(sx), int16(sy), func() {
				if row < len(propUI.Meta) {
					commentOnProposalLine(propUI.Meta[row])
				}
			}, lblStyle)
		}
	}

	scrollY := propUI.Layer.ScrollY()
	propUI.Layer.SetBuffer(buf)
	propUI.Layer.ScrollTo(scrollY)
}

func propTemplate() *Template {
	if propUI.Tmpl == nil {
		propUI.Tmpl = Build(VBox.Fill(&cBG).Gap(0)(
			ForEach(&propUI.Rows, func(r *propRowVM) Component {
				return HBox.Fill(&r.BG)(Rich(&r.Spans).CharWrap())
			}),
		))
	}
	return propUI.Tmpl
}

// prepPropRows projects the stored document into Rows/Meta at width w — pure
// data prep, runs on content/size change only.
func prepPropRows(w int) {
	propUI.Rows = propUI.Rows[:0]
	propUI.Meta = propUI.Meta[:0]
	put := func(m propLineMeta, spans []Span) {
		bg := cBG
		if m.Line > 0 && propUI.Commented[m.Line] {
			bg = cFileHdrBG // commented document lines wash, the diff pane's cue
		}
		propUI.Rows = append(propUI.Rows, propRowVM{Spans: spans, BG: bg})
		propUI.Meta = append(propUI.Meta, m)
	}
	p := propUI.Prop
	put(propLineMeta{}, []Span{
		span(fmt.Sprintf("proposal #%d", p.ID), cHunk, true),
		span("  ·  ", cMuted, false),
		span(strings.ToUpper(p.Status), proposalStatusColor(p.Status), true),
	})
	put(propLineMeta{}, []Span{
		span(fmt.Sprintf("%s → %s", propSender(p.ProposerWho, p.ProposerRepo), p.TargetRepo), cMuted, false),
		span("   parties: "+strings.Join(propUI.Parties, ", "), cSubtle, false),
	})
	put(propLineMeta{}, []Span{})
	rows, meta := propBodyRows(p.Body, w)
	for i := range rows {
		put(meta[i], rows[i])
	}
	put(propLineMeta{}, []Span{})
}

// propBodyRows renders the document: `#` headings read bold, ``` fence content
// renders raw (no markup, indentation preserved), everything else goes through
// the briefing markup + wrap task summaries use. Every rendered row's meta
// carries the 1-based SOURCE line it came from (a wrapped line yields several
// rows with the same anchor); non-empty content rows are commentable.
func propBodyRows(text string, width int) ([][]Span, []propLineMeta) {
	if width > 72 {
		width = 72 // briefing measure: long lines wrap at the summary width
	}
	var rows [][]Span
	var meta []propLineMeta
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		m := propLineMeta{Line: i + 1, Text: trimmed, Commentable: trimmed != ""}
		put := func(r []Span) {
			rows = append(rows, r)
			meta = append(meta, m)
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			put([]Span{span("  "+strings.TrimRight(line, " "), cSubtle, false)})
			continue
		}
		if trimmed == "" {
			m.Commentable = false
			put([]Span{})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			put([]Span{span(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), cBright, true)})
			continue
		}
		for _, w := range summaryBody(line, width) {
			put(w)
		}
	}
	return rows, meta
}

func proposalStatusColor(status string) Color {
	switch status {
	case db.ProposalApproved:
		return cAdd
	case db.ProposalDeclined:
		return cDel
	default:
		return cHunk
	}
}

// propSender formats "who@repo", collapsing to just the name when there's no
// repo (the human's comments).
func propSender(who, repo string) string {
	if who == "" {
		who = "agent"
	}
	if repo == "" {
		return who
	}
	return who + "@" + repo
}

// --- pane components ---------------------------------------------------------

// propDocPane is the middle column's content while a proposal is selected —
// the document layer + flush-right scrollbar, the diff pane's structure.
func propDocPane() Component {
	return HBox.Grow(1).NodeRef(&propUI.ViewRef)(
		LayerView(propUI.Layer).Grow(1),
		ScrollbarForLayer(propUI.Layer).
			TrackStyle(&scrollTrackStyle).
			ThumbStyle(&scrollThumbStyle).
			Opacity(Animate(&propUI.Focused)),
	)
}

// propThreadRow renders one thread comment — the draft pane's row shape.
func propThreadRow(c *propThreadVM) Component {
	return VBox.PaddingVH(0, 1)(
		HBox(
			Text(&c.Who).FG(&c.WhoColor).Bold(),
			SpaceW(2),
			Text(&c.Location).FG(&cSubtle),
			Space(),
			Text(&c.When).FG(&cMuted),
		),
		If(&c.Snippet).Eq("").Then(Text("")).Else(HBox(SpaceW(2), Text(&c.Snippet).FG(&cSubtle))),
		HBox(SpaceW(2), TextBlock(&c.Body).FG(&cFG)),
		Text(" "),
	)
}

// propThreadPane is the right-hand thread column while a proposal with
// comments is selected — the comments pane's aesthetic, proposal-owned.
func propThreadPane() Component {
	return If(&propUI.HasThread).Then(
		VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0).NodeRef(&propUI.PaneRef)(
			HBox(SpaceW(3), Text("thread").FG(&cBright).Bold(), Space(), Text(&propUI.Note).FG(&cSubtle), SpaceW(2)),
			SpaceH(2),
			List(&propUI.Thread).
				Selection(&propUI.ThreadSel).
				Style(&listBaseStyle).
				SelectedStyle(&draftSelStyle).
				Marker("  ").
				Render(propThreadRow),
			If(&pane).Eq(paneDraft).Then(On(
				Key("j", func() { movePropThread(1) }),
				Key("k", func() { movePropThread(-1) }),
				Key("<Enter>", func() { setPane(paneList) }),
				Key("<Esc>", func() { setPane(paneList) }),
			)),
		),
	)
}

// --- comment actions ---------------------------------------------------------

// commentOnProposal threads a general human comment onto the selected proposal
// and pings every party — the digest model: one unread attention message per
// party, no spam if one's already pending.
func commentOnProposal(row *taskVM) {
	p, ok := inboxUI.PropByID[row.ID]
	if !ok {
		return
	}
	promptUI.open(
		fmt.Sprintf("comment on proposal #%d", p.ID),
		"", p.Title, "",
		func() {
			saveProposalComment(p, 0, "")
		},
	)
}

// commentOnProposalLine is the jump-pick action over a document row: the
// comment anchors to the picked SOURCE line.
func commentOnProposalLine(m propLineMeta) {
	p := propUI.Prop
	if p.ID == 0 {
		return
	}
	snip := "  " + m.Text
	if len(snip) > 68 {
		snip = snip[:67] + "…"
	}
	promptUI.open(
		fmt.Sprintf("line comment · proposal #%d", p.ID),
		fmt.Sprintf("document · line %d", m.Line), snip, "",
		func() {
			saveProposalComment(p, m.Line, m.Text)
		},
	)
}

// saveProposalComment is the shared save: thread the comment (anchored when
// line > 0), ping every party once, and refresh the detail in place.
func saveProposalComment(p db.Proposal, line int, snippet string) {
	body := promptUI.Field.Value
	if body == "" {
		return
	}
	if _, err := uiStore.AddProposalLineComment(p.ID, "", "you", body, line, snippet); err != nil {
		toast("comment: " + err.Error())
		return
	}
	parties, _ := uiStore.ProposalParties(p.ID)
	for _, r := range parties {
		if r == "" {
			continue
		}
		_ = uiStore.SendProposalPing(p.ID, "", "you", r,
			fmt.Sprintf("proposal #%d has new comments: %q — recap proposal show %d", p.ID, p.Title, p.ID))
	}
	notify.Reload() // wakes any party parked on --wait
	inboxUI.DetailDirty = true
	refreshDetailNow()
	toast(fmt.Sprintf("commented on proposal #%d", p.ID))
}

// openPropLineComment enters the jump pick over the document's commentable
// rows — the diff pane's `c`, proposal-owned.
func openPropLineComment() {
	any := false
	for _, m := range propUI.Meta {
		if m.Commentable {
			any = true
			break
		}
	}
	if !any {
		toast("(no document lines to comment on)")
		return
	}
	uiApp.EnterJumpMode()
}

// movePropThread moves the thread column's selection.
func movePropThread(d int) {
	propUI.ThreadSel += d
	if propUI.ThreadSel >= len(propUI.Thread) {
		propUI.ThreadSel = len(propUI.Thread) - 1
	}
	if propUI.ThreadSel < 0 {
		propUI.ThreadSel = 0
	}
}
