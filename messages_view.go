package main

import (
	"fmt"

	. "github.com/kungfusheep/glyph"
)

// The messages view: the agent→agent conversation ledger, readable by the human.
// A named view (app.Go, like the todo editor) over the full two-way message table —
// every repo, both directions, threading markers, read state. Opening it stamps the
// user read-receipt on everything shown.

// msgView is the view's state in one concrete struct (the 5a/5b pattern): row VMs
// + selection, one bound package instance (msgUI). No interfaces, no injection.
type msgView struct {
	Rows []msgVM
	Sel  int
}

var msgUI msgView

// msgVM is one rendered message: precomputed header/body strings + the sender's
// identity colour, pointer-bound per row.
type msgVM struct {
	Head      string // "m12  Kestrel@recap → tui  ↳m9"
	HeadColor Color  // sender's per-repo identity colour (fallback bright)
	When      string
	Body      string // full body, wraps in the row's TextBlock
	AgentRead string // ●/○ — has the TARGET repo's agent consumed it
	Selected  bool
}

// openMessages loads the ledger, stamps the user read-receipts, and switches to the
// named view. Loading is one query + tiny identity file reads — selection-time work,
// not render-thread I/O.
func openMessages() {
	ms, err := uiStore.Messages("")
	if err != nil {
		statusMsg = "messages: " + err.Error()
		return
	}
	var unseen []int64
	msgUI.Rows = make([]msgVM, 0, len(ms))
	for _, m := range ms {
		if m.ReadUser == "" {
			unseen = append(unseen, m.ID)
		}
		thread := ""
		if m.ParentID != 0 {
			thread = fmt.Sprintf("  ↳m%d", m.ParentID)
		}
		color := cBright
		if _, c := loadIdentity(m.FromRepo); c.Mode != 0 {
			color = c
		}
		msgUI.Rows = append(msgUI.Rows, msgVM{
			Head:      fmt.Sprintf("m%d  %s@%s → %s%s", m.ID, m.FromWho, m.FromRepo, m.ToRepo, thread),
			HeadColor: color,
			When:      m.CreatedAt,
			Body:      m.Body,
			AgentRead: readDot(m.ReadAgent != ""),
		})
	}
	if len(unseen) > 0 {
		_ = uiStore.MarkMessageReadUser(unseen...)
	}
	msgUI.Sel = len(msgUI.Rows) - 1 // open at the newest
	if msgUI.Sel < 0 {
		msgUI.Sel = 0
	}
	msgUI.prep()
	uiApp.Go("messages")
}

func (mv *msgView) prep() {
	for i := range mv.Rows {
		mv.Rows[i].Selected = i == mv.Sel
	}
}

func (mv *msgView) move(d int) {
	mv.Sel += d
	if mv.Sel >= len(mv.Rows) {
		mv.Sel = len(mv.Rows) - 1
	}
	if mv.Sel < 0 {
		mv.Sel = 0
	}
	mv.prep()
}

func closeMessages() {
	uiApp.Go("main")
}

// msgRow renders one message: a coloured header line (sender identity), the agent
// read-dot, the timestamp, then the wrapped body — selection band per row.
func msgRow(m *msgVM) Component {
	bg := If(&m.Selected).Then(&cSelBG).Else(&cBG)
	return VBox.Fill(bg).PaddingVH(0, 1)(
		HBox(
			Text(&m.AgentRead).FG(&cHunk),
			SpaceW(1),
			Text(&m.Head).FG(&m.HeadColor).Bold(),
			Space(),
			Text(&m.When).FG(&cMuted),
		),
		HBox.Grow(1)(TextBlock(&m.Body).FG(&cFG)),
		Text(" "),
	)
}

// buildMessagesView is the named "messages" view — one story-shaped template,
// compiled once at registration, bound to msgUI by pointer.
func buildMessagesView() Component {
	return VBox.Fill(&cBG).CascadeStyle(&bgStyle).Grow(1).PaddingTRBL(1, 2, 1, 2)(
		On(
			Key("j", func() { msgUI.move(1) }),
			Key("k", func() { msgUI.move(-1) }),
			Key("g", func() { msgUI.Sel = 0; msgUI.prep() }),
			Key("G", func() { msgUI.Sel = len(msgUI.Rows) - 1; msgUI.move(0) }),
			Key("<Esc>", closeMessages),
			Key("q", closeMessages),
			Key("m", closeMessages),
		),
		HBox(
			Text("agent messages").FG(&cBright).Bold(),
			Space(),
			Text("● read by agent · ○ waiting · esc close").FG(&cMuted),
		),
		SpaceH(1),
		List(&msgUI.Rows).
			Selection(&msgUI.Sel).
			Marker("  ").
			SelectedStyle(Style{}). // band painted per-row
			Render(msgRow),
	)
}
