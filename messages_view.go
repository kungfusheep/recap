package main

import (
	"fmt"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/notify"
)

// The messages view: the agent→agent conversation ledger, readable by the human.
// A named view (app.Go, like the todo editor) over the full two-way message table —
// every repo, both directions, threading markers, read state. Opening it stamps the
// user read-receipt on everything shown. The human can also speak: r threads a
// comment under the selected message, addressed to its sender's repo, so corrections
// and observations land in that agent's `recap next` queue like any other message.

// msgView is the view's state in one concrete struct (the 5a/5b pattern): row VMs
// + selection, one bound package instance (msgUI). No interfaces, no injection.
type msgView struct {
	Rows []msgVM
	Sel  int
}

var msgUI msgView

// msgUnread caches the cross-repo unread count for the header badge. The render
// path reads it; recountMsgUnread refreshes it from reload/handler paths only.
var msgUnread int

func recountMsgUnread() {
	if uiStore == nil {
		return
	}
	if n, err := uiStore.UnreadMessageCount(); err == nil {
		msgUnread = n
	}
}

// msgVM is one rendered message: precomputed header/body strings + the sender's
// identity colour, pointer-bound per row. ID/FromRepo/TaskID carry enough of the
// underlying row to thread a human comment back at it.
type msgVM struct {
	ID        int64  // message id (reply parent)
	FromRepo  string // sender repo — where a human comment on this row is addressed
	FromWho   string
	TaskID    int64  // carried onto human comments so the anchor survives the thread
	Head      string // "m12  Kestrel@recap → tui  ↳m9"
	HeadColor Color  // sender's per-repo identity colour (fallback bright)
	When      string
	Body      string // full body, wraps in the row's TextBlock
	AgentRead string // ●/○ — has the TARGET repo's agent consumed it
	Selected  bool
}

// msgSender formats "who@repo" for a message header, collapsing to just the name
// when there's no sender repo (human comments sent from the TUI).
func msgSender(who, repo string) string {
	if repo == "" {
		return who
	}
	return who + "@" + repo
}

// openMessages loads the ledger, stamps the user read-receipts, and switches to the
// named view. Loading is one query + tiny identity file reads — selection-time work,
// not render-thread I/O.
func openMessages() {
	if !msgUI.load() {
		return
	}
	msgUI.Sel = len(msgUI.Rows) - 1 // open at the newest
	if msgUI.Sel < 0 {
		msgUI.Sel = 0
	}
	msgUI.prep()
	uiApp.Go("messages")
}

// load (re)reads the full ledger into Rows and stamps user read-receipts.
// Returns false on a query error (reported via status).
func (mv *msgView) load() bool {
	ms, err := uiStore.Messages("")
	if err != nil {
		statusMsg = "messages: " + err.Error()
		return false
	}
	var unseen []int64
	mv.Rows = make([]msgVM, 0, len(ms))
	for _, m := range ms {
		if m.ReadUser == "" {
			unseen = append(unseen, m.ID)
		}
		thread := ""
		if m.ParentID != 0 {
			thread = fmt.Sprintf("  ↳m%d", m.ParentID)
		}
		color := cBright
		if m.FromRepo != "" {
			if _, c := loadIdentity(m.FromRepo); c.Mode != 0 {
				color = c
			}
		}
		mv.Rows = append(mv.Rows, msgVM{
			ID:        m.ID,
			FromRepo:  m.FromRepo,
			FromWho:   m.FromWho,
			TaskID:    m.TaskID,
			Head:      fmt.Sprintf("m%d  %s → %s%s", m.ID, msgSender(m.FromWho, m.FromRepo), m.ToRepo, thread),
			HeadColor: color,
			When:      m.CreatedAt,
			Body:      m.Body,
			AgentRead: readDot(m.ReadAgent != ""),
		})
	}
	if len(unseen) > 0 {
		_ = uiStore.MarkMessageReadUser(unseen...)
	}
	return true
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

// comment opens the prompt to thread a human note under the selected message. It's
// addressed to that message's SENDER repo — you comment on what was said, and the
// author's loop picks it up via `recap next`. To address the other side of a
// conversation, select one of its rows instead.
func (mv *msgView) comment() {
	if mv.Sel < 0 || mv.Sel >= len(mv.Rows) {
		return
	}
	row := mv.Rows[mv.Sel]
	if row.FromRepo == "" {
		statusMsg = "that's your own comment — reply to an agent's message"
		uiApp.RequestRender()
		return
	}
	target := row.FromRepo
	id, taskID := row.ID, row.TaskID
	promptUI.open(
		fmt.Sprintf("comment on m%d → %s", id, msgSender(row.FromWho, row.FromRepo)),
		"", firstLine(row.Body), "",
		func() {
			body := promptUI.Field.Value
			if body == "" {
				return
			}
			mid, err := uiStore.SendMessage("", "you", target, id, taskID, body)
			if err != nil {
				statusMsg = "comment: " + err.Error()
				return
			}
			// the human has read their own words
			_ = uiStore.MarkMessageReadUser(mid)
			notify.Reload() // wakes the target's parked --wait
			recountMsgUnread()
			mv.load()
			mv.Sel = len(mv.Rows) - 1
			mv.prep()
			statusMsg = fmt.Sprintf("sent m%d → %s", mid, target)
		},
	)
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
// compiled once at registration, bound to msgUI by pointer. The shared prompt
// overlay floats here too (same instance/state as the inbox's), for r comments.
func buildMessagesView() Component {
	return VBox.Fill(&cBG).CascadeStyle(&bgStyle).Grow(1).PaddingTRBL(1, 2, 1, 2)(
		On(
			Key("j", func() { msgUI.move(1) }),
			Key("k", func() { msgUI.move(-1) }),
			Key("g", func() { msgUI.Sel = 0; msgUI.prep() }),
			Key("G", func() { msgUI.Sel = len(msgUI.Rows) - 1; msgUI.move(0) }),
			Key("r", func() { msgUI.comment() }),
			Key("<Esc>", closeMessages),
			Key("q", closeMessages),
			Key("m", closeMessages),
		),
		HBox(
			Text("agent messages").FG(&cBright).Bold(),
			Space(),
			Text("● read by agent · ○ waiting · r comment · esc close").FG(&cMuted),
		),
		SpaceH(1),
		List(&msgUI.Rows).
			Selection(&msgUI.Sel).
			Marker("  ").
			SelectedStyle(Style{}). // band painted per-row
			Render(msgRow),
		inputPromptOverlay(),
	)
}
