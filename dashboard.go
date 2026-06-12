package main

import (
	"path/filepath"
	"sort"
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/listener"
)

// The agent dashboard ('A'): one concise row per named agent — colour + name,
// current status (working / parked / idle), and the last thing they recorded.
// Data loads at OPEN (the handler acquires: identity files, cursor files,
// listener pidfiles, one db query); the overlay renders pointers only.

type agentVM struct {
	Dot         string // ●
	Name        string
	NameColor   Color
	Repo        string
	Status      string
	StatusColor Color
	Last        string // last recorded task, truncated
	When        string
}

type dashView struct {
	Open bool
	Rows []agentVM
	Ref  NodeRef
}

var dashUI dashView

// openAgentsDash gathers every named agent's state and shows the overlay.
func openAgentsDash() {
	dbp, err := db.Path()
	if err != nil {
		statusMsg = "agents: " + err.Error()
		return
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dbp), "identity-*"))
	live := map[string]bool{}
	for _, r := range listener.Active() {
		live[r] = true
	}
	var latest map[string]db.Task
	if uiStore != nil {
		latest, _ = uiStore.LatestTaskPerRepo()
	}

	dashUI.Rows = dashUI.Rows[:0]
	for _, m := range matches {
		repo := strings.TrimPrefix(filepath.Base(m), "identity-")
		name, color := loadIdentity(repo)
		if name == "" {
			continue
		}
		vm := agentVM{Dot: "●", Name: name, NameColor: color, Repo: repo}
		switch {
		case cursor.Title(repo) != "":
			vm.Status = "working: " + clipTo(cursor.Title(repo), 44)
			vm.StatusColor = cAdd
		case live[repo]:
			vm.Status = "parked — listening for work"
			vm.StatusColor = cHunk
		default:
			vm.Status = "idle"
			vm.StatusColor = cMuted
		}
		if t, ok := latest[repo]; ok {
			vm.Last = "last: " + clipTo(t.Title, 48)
			vm.When = hhmm(t.CreatedAt)
		} else {
			vm.Last = "last: —"
		}
		dashUI.Rows = append(dashUI.Rows, vm)
	}
	sort.Slice(dashUI.Rows, func(i, j int) bool { return dashUI.Rows[i].Name < dashUI.Rows[j].Name })
	if len(dashUI.Rows) == 0 {
		statusMsg = "no named agents yet (agents run `recap whoami`)"
		return
	}
	dashUI.Open = true
}

func clipTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// agentsOverlay is the dashboard component — compiled once, bound to dashUI.
func agentsOverlay() Component {
	return If(&dashUI.Open).Then(
		Overlay.Centered()(
			VBox.Width(78).Fill(&cFloat).CascadeStyle(&floatStyle).
				PaddingVH(1, 2).NodeRef(&dashUI.Ref).
				Opacity(In(Animate(1.0)).Out(Animate(0))).Gap(1)(
				On.Modal(
					Key("<Esc>", func() { dashUI.Open = false }),
					Key("q", func() { dashUI.Open = false }),
					Key("A", func() { dashUI.Open = false }),
				),
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&dashUI.Ref),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&dashUI.Ref),
				),
				HBox(Text("agents").FG(&cBright).Bold(), Space(), Text("esc close").FG(&cMuted)),
				ForEach(&dashUI.Rows, func(r *agentVM) Component {
					return VBox(
						HBox(
							Text(&r.Dot).FG(&r.NameColor),
							SpaceW(1),
							Text(&r.Name).FG(&r.NameColor).Bold(),
							SpaceW(2),
							Text(&r.Repo).FG(&cSubtle),
							Space(),
							Text(&r.When).FG(&cMuted),
						),
						HBox(SpaceW(2), Text(&r.Status).FG(&r.StatusColor)),
						HBox(SpaceW(2), Text(&r.Last).FG(&cFG)),
					)
				}),
			),
		),
	)
}
