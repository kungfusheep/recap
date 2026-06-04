package main

import (
	. "github.com/kungfusheep/glyph"
)

// omniItem is one command in the omnibox palette. Preview (optional) runs as the
// selection moves; Action runs on Enter. Mirrors mail's omnibox command shape so
// theme/preview commands (task 5) slot in without restructuring.
type omniItem struct {
	Label       string
	Description string
	Section     string
	Action      func()
	Preview     func()
}

func omniSearchText(it *omniItem) string {
	return it.Label + " " + it.Description + " " + it.Section
}

// omniCommands is recap's command palette contents: every action the keybindings
// expose, so the palette is a discoverable home for them (no need to memorise the
// keys). Each Action reuses the same handler the key binding calls. Theme commands
// (task 5) will be appended here.
func omniCommands() []omniItem {
	cmds := []omniItem{
		// review actions — operate on the selected task
		{Label: "approve", Description: "approve the selected task (submits an approve review)", Section: "review", Action: approveSelected},
		{Label: "comment", Description: "add a comment to the selected task", Section: "review", Action: openComment},
		{Label: "submit (amends)", Description: "submit the draft review → request changes", Section: "review", Action: submitSelected},
		{Label: "unsubmit → inbox", Description: "move the selected task from amends back to the inbox", Section: "review", Action: unsubmitSelected},
		{Label: "re-run verification (--check)", Description: "run the task's recorded --check command and show PASS/FAIL", Section: "review", Action: rerun},
		// view actions
		{Label: "filter repo", Description: "cycle the inbox filter through repos", Section: "view", Action: cycleFilter},
		{Label: "next pane", Description: "move focus to the next column", Section: "view", Action: focusNext},
		{Label: "previous pane", Description: "move focus to the previous column", Section: "view", Action: focusPrev},
		{Label: "edit TODO", Description: "open the selected task's repo TODO file for editing", Section: "view", Action: openTodoEditor},
		{Label: "help", Description: "toggle the keyboard cheatsheet", Section: "view", Action: toggleHelp},
		// app actions
		{Label: "quit", Description: "exit recap", Section: "app", Action: func() { uiApp.Stop() }},
	}
	// theme commands — one per palette. Selecting applies + persists it. Applied on
	// Enter (the omnibox closes first), since applyTheme rebuilds the view tree.
	for _, nt := range allThemes() {
		nt := nt // capture per iteration
		cmds = append(cmds, omniItem{
			Label:       "theme: " + nt.Label,
			Description: "apply and save the " + nt.Name + " theme",
			Section:     "theme",
			Action: func() {
				applyTheme(nt.Name, nt.Palette)
				if err := saveThemeName(nt.Name); err != nil {
					statusMsg = "theme save failed: " + err.Error()
				} else {
					statusMsg = "theme: " + nt.Label
				}
			},
		})
	}
	return cmds
}

// OmniBox is recap's command palette: a fuzzy-filtered command list rendered as a
// centred overlay over the live inbox, opened with <C-p> / <Space>. It uses
// On.Modal inside If(&open) so the keys are both conditional (only while open)
// and modal (captured exclusively) — mail's pattern. View() is rendered once near
// the top of the main tree.
type OmniBox struct {
	app   *App
	open  bool
	items []omniItem
	list  *FilterListC[omniItem]
	ref   NodeRef
}

func newOmniBox(app *App, items []omniItem) *OmniBox {
	return &OmniBox{app: app, items: items}
}

func (b *OmniBox) Open() {
	if b.open {
		return
	}
	if b.list != nil {
		b.list.Clear()
		b.list.Refresh()
	}
	b.open = true
	b.app.HideCursor()
	b.app.RequestRender()
}

func (b *OmniBox) Close() {
	if !b.open {
		return
	}
	if b.list != nil {
		b.list.Clear()
	}
	b.open = false
	b.app.RequestRender()
}

func (b *OmniBox) exec() {
	it := b.selected()
	b.Close()
	if it != nil && it.Action != nil {
		it.Action()
	}
}

func (b *OmniBox) selected() *omniItem {
	if b.list == nil {
		return nil
	}
	if it := b.list.Selected(); it != nil {
		return it
	}
	// a fresh filter may have no explicit selection yet; fall back to the first
	// matching item so Enter always acts on something.
	if b.list.Filter().Len() > 0 && len(b.items) > 0 {
		return &b.items[0]
	}
	return nil
}

func (b *OmniBox) move(d int) {
	if b.list == nil {
		return
	}
	if d > 0 {
		b.list.SelectNext()
	} else {
		b.list.SelectPrev()
	}
	if it := b.list.Selected(); it != nil && it.Preview != nil {
		it.Preview()
		b.app.RequestRender()
	}
}

// View returns the overlay; render it once near the top of the main view tree.
// Ref returns the panel's node ref, so focus-shade effects can dodge it (avoid
// double-dimming the column behind the open palette).
func (b *OmniBox) Ref() *NodeRef { return &b.ref }

// While closed it renders nothing and binds no keys; while open it floats a
// centred panel over the inbox and captures keys via On.Modal.
func (b *OmniBox) View() Component {
	b.list = FilterList(&b.items, omniSearchText).
		Placeholder("type a command").
		Marker("  ").
		Style(Style{BG: cFloat}).
		SelectedStyle(Style{FG: cBright, BG: cSelBG}).
		Render(func(it *omniItem) Component {
			return VBox.PaddingVH(1, 2)(
				HBox(
					Text(&it.Label).FG(cBright),
					Space(),
					Text(&it.Section).FG(cSubtle),
				),
				Text(&it.Description).FG(cSubtle),
			)
		})

	return If(&b.open).Then(
		Overlay.Centered()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).
				PaddingTRBL(1, 2, 1, 2).NodeRef(&b.ref).Opacity(In(1).Out(Animate(0)))(
				On.Modal(
					Key("<CR>", b.exec),
					Key("<Enter>", b.exec),
					Key("<Esc>", b.Close),
					Key("<C-c>", b.Close),
					Key("<Down>", func() { b.move(1) }),
					Key("<C-n>", func() { b.move(1) }),
					Key("<Up>", func() { b.move(-1) }),
					Key("<C-p>", func() { b.move(-1) }),
				),
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&b.ref),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&b.ref),
				),
				HBox(
					Text("recap").FG(cBright).Bold(),
					SpaceW(1),
					Text("commands").FG(cSubtle),
					Space(),
					Text("↵ run").FG(cMuted),
					SpaceW(2),
					Text("esc").FG(cMuted),
				),
				SpaceH(1),
				b.list,
			),
		),
	)
}
