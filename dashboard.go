package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/agents"
)

// The agent dashboard ('A'): one concise row per named agent — colour + name,
// current status (working / parked / idle), and the last thing they recorded.
// The DATA lives in the agents package (agents.Snapshot — handler-side
// acquisition); this file is only the view: VM mapping + the overlay template.

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

// openAgentsDash snapshots the fleet and shows the overlay.
func openAgentsDash() {
	snap, err := agents.Snapshot(uiStore)
	if err != nil {
		toast("agents: " + err.Error())
		return
	}
	dashUI.Rows = dashUI.Rows[:0]
	for _, a := range snap {
		vm := agentVM{Dot: "●", Name: a.Name, NameColor: cBright, Repo: strings.Join(a.Repos, ", ")}
		if c, ok := parseHexColor(a.ColorHex); ok {
			vm.NameColor = c
		}
		switch a.Status {
		case agents.Working:
			vm.Status = "working: " + clipTo(a.Flare, 40) + "  · " + shortAge(a.FlareAge)
			vm.StatusColor = cAdd
		case agents.Parked:
			vm.Status = "parked — listening for work"
			vm.StatusColor = cHunk
		default:
			vm.Status = "idle"
			vm.StatusColor = cMuted
		}
		if a.LastWork != "" {
			vm.Last = "last: " + clipTo(a.LastWork, 48)
			vm.When = hhmm(a.LastAt)
		} else {
			vm.Last = "last: —"
		}
		dashUI.Rows = append(dashUI.Rows, vm)
	}
	sort.Slice(dashUI.Rows, func(i, j int) bool { return dashUI.Rows[i].Name < dashUI.Rows[j].Name })
	if len(dashUI.Rows) == 0 {
		toast("no named agents yet (agents run `recap whoami`)")
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

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
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
