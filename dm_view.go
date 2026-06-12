package main

import (
	"fmt"
	"sync"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/agents"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
)

// The DM dialogue (the human's direct ask via m273): D opens a per-repo
// conversation between the human and that repo's loop, without leaving the
// TUI for tmux. The channel is the message ledger filtered two-way
// (human→repo, repo→human); sends land in the loop's queue like any
// `recap send`, replies surface back here live through the reload signal.

type dmView struct {
	Repo  string // "" = view closed; also the signal-path fetch key
	Title string // "DM → mail · Ada"
	Rows  []msgVM
	Sel   int
}

var dmUI dmView

// dmSnap mirrors inboxSnap: the signal goroutine reads the open dialogue's
// repo under a mutex so its fetch can include fresh DM rows.
var (
	dmSnapMu  sync.Mutex
	dmSnapVal string
)

func setDMSnap(repo string) {
	dmSnapMu.Lock()
	dmSnapVal = repo
	dmSnapMu.Unlock()
}

func dmSnap() string {
	dmSnapMu.Lock()
	defer dmSnapMu.Unlock()
	return dmSnapVal
}

// dmContextRepo picks the dialogue's repo from the selection: a task row's
// repo, a proposal row's target, else the first known agent repo.
func dmContextRepo() string {
	if t, ok := selectedTask(); ok {
		return t.Repo
	}
	if row := selectedRow(); row != nil && row.Proposal {
		if p, okp := inboxUI.PropByID[row.ID]; okp {
			return p.TargetRepo
		}
	}
	if rs := dmKnownRepos(); len(rs) > 0 {
		return rs[0]
	}
	return ""
}

// dmKnownRepos lists every repo with a named loop (the dashboard's source).
func dmKnownRepos() []string {
	snap, err := agents.Snapshot(nil)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, a := range snap {
		for _, r := range a.Repos {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// openDM opens the dialogue for a repo (handler-side load) and switches view.
func openDM(repo string) {
	if repo == "" {
		toast("no agent repos known yet")
		return
	}
	dmUI.Repo = repo
	setDMSnap(repo)
	dmReload()
	uiApp.Go("dm")
}

// dmCycle moves the dialogue to the next known repo (f, like the inbox filter).
func dmCycle() {
	rs := dmKnownRepos()
	if len(rs) == 0 {
		return
	}
	next := rs[0]
	for i, r := range rs {
		if r == dmUI.Repo {
			next = rs[(i+1)%len(rs)]
			break
		}
	}
	dmUI.Repo = next
	setDMSnap(next)
	dmReload()
}

// dmReload fetches and projects the dialogue — handler-side acquisition.
func dmReload() {
	ms, err := uiStore.MessagesWith(dmUI.Repo)
	if err != nil {
		toast("dm: " + err.Error())
		return
	}
	dmApply(ms)
	dmUI.Sel = len(dmUI.Rows) - 1
	if dmUI.Sel < 0 {
		dmUI.Sel = 0
	}
}

// dmApply projects messages into rows and stamps user read-receipts — render
// thread (called from handlers and the staged seam).
func dmApply(ms []db.Message) {
	name, color := loadIdentity(dmUI.Repo)
	dmUI.Title = "DM → " + dmUI.Repo
	if name != "" {
		dmUI.Title += " · " + name
	}
	var unseen []int64
	dmUI.Rows = dmUI.Rows[:0]
	for _, m := range ms {
		if m.FromRepo != "" && m.ReadUser == "" {
			unseen = append(unseen, m.ID)
		}
		vm := msgVM{
			ID:        m.ID,
			FromRepo:  m.FromRepo,
			FromWho:   m.FromWho,
			TaskID:    m.TaskID,
			When:      m.CreatedAt,
			Body:      m.Body,
			AgentRead: readDot(m.ReadAgent != ""),
		}
		if m.FromRepo == "" {
			vm.Head = fmt.Sprintf("m%d  You", m.ID)
			vm.HeadColor = cBright
		} else {
			vm.Head = fmt.Sprintf("m%d  %s", m.ID, msgSender(m.FromWho, m.FromRepo))
			vm.HeadColor = cBright
			if color.Mode != 0 {
				vm.HeadColor = color
			}
		}
		if m.ParentID != 0 {
			vm.Head += fmt.Sprintf("  ↳m%d", m.ParentID)
		}
		vm.Selected = false
		dmUI.Rows = append(dmUI.Rows, vm)
	}
	if len(unseen) > 0 {
		_ = uiStore.MarkMessageReadUser(unseen...)
	}
	if dmUI.Sel >= len(dmUI.Rows) {
		dmUI.Sel = len(dmUI.Rows) - 1
	}
	if dmUI.Sel < 0 {
		dmUI.Sel = 0
	}
	for i := range dmUI.Rows {
		dmUI.Rows[i].Selected = i == dmUI.Sel
	}
}

func dmMove(d int) {
	dmUI.Sel += d
	if dmUI.Sel >= len(dmUI.Rows) {
		dmUI.Sel = len(dmUI.Rows) - 1
	}
	if dmUI.Sel < 0 {
		dmUI.Sel = 0
	}
	for i := range dmUI.Rows {
		dmUI.Rows[i].Selected = i == dmUI.Sel
	}
}

// dmSend opens the destination-led prompt and queues the message to the
// loop — exactly a `recap send` from the human's side.
func dmSend() {
	repo := dmUI.Repo
	promptUI.open("DM → "+repo, "", "", "", func() {
		body := promptUI.Field.Value
		if body == "" {
			return
		}
		id, err := uiStore.SendMessage("", "you", repo, 0, 0, body)
		if err != nil {
			toast("dm: " + err.Error())
			return
		}
		_ = uiStore.MarkMessageReadUser(id) // your own words are read
		notify.Reload()                     // wakes the loop's parked --wait
		recountMsgUnread()
		dmReload()
		toast(fmt.Sprintf("sent m%d → %s", id, repo))
	})
}

func closeDM() {
	dmUI.Repo = ""
	setDMSnap("")
	uiApp.Go("main")
}

// buildDMView is the named "dm" view: the dialogue rows plus the shared
// prompt overlay (same instance as everywhere else).
func buildDMView() Component {
	return VBox.Fill(&cBG).CascadeStyle(&bgStyle).Grow(1).PaddingTRBL(1, 2, 1, 2)(
		On(
			Key("j", func() { dmMove(1) }),
			Key("k", func() { dmMove(-1) }),
			Key("g", func() { dmUI.Sel = 0; dmMove(0) }),
			Key("G", func() { dmUI.Sel = len(dmUI.Rows) - 1; dmMove(0) }),
			Key("r", dmSend),
			Key("<Enter>", dmSend),
			Key("f", dmCycle),
			Key("<Esc>", closeDM),
			Key("q", closeDM),
			Key("D", closeDM),
		),
		HBox(
			Text(&dmUI.Title).FG(&cBright).Bold(),
			Space(),
			Text("↵/r send · f switch agent · esc close").FG(&cMuted),
		),
		SpaceH(1),
		List(&dmUI.Rows).
			Selection(&dmUI.Sel).
			Marker("  ").
			SelectedStyle(Style{}). // band painted per-row (msgRow)
			Render(msgRow),
		inputPromptOverlay(),
	)
}
