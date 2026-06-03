package main

import (
	"strings"

	. "github.com/kungfusheep/glyph"
)

// The TODO editor: a modal view over the selected task's repo TODO file. Toggle
// checkboxes (space/x), add lines (a), and changes write straight back to disk
// (creating the file if it doesn't exist). The file is the human-owned queue the
// autonomous loop reads, so editing it here closes the loop from inside recap.
var (
	todoItems []todoItem
	todoSel   int
	todoPath  string
	todoTitle string
)

// openTodoEditor resolves the selected task's repo TODO path (via the config
// template), loads it, and opens the editor. Reports why if it can't.
func openTodoEditor() {
	t, ok := selectedTask()
	if !ok {
		statusMsg = "no task selected"
		return
	}
	cfg, _ := LoadConfig()
	path, err := cfg.todoPathFor(t.RepoPath)
	if err != nil {
		statusMsg = "todo path: " + err.Error()
		return
	}
	if path == "" {
		statusMsg = "no todo_template configured (~/.config/recap/config.toml)"
		return
	}
	items, err := readTodo(path)
	if err != nil {
		statusMsg = "todo read: " + err.Error()
		return
	}
	todoPath = path
	todoItems = items
	todoSel = 0
	todoTitle = "TODO · " + t.Repo
	uiApp.PushView("todoedit")
}

func todoMove(d int) {
	todoSel += d
	if todoSel >= len(todoItems) {
		todoSel = len(todoItems) - 1
	}
	if todoSel < 0 {
		todoSel = 0
	}
}

func todoSave() {
	if err := writeTodo(todoPath, todoItems); err != nil {
		statusMsg = "todo write: " + err.Error()
	}
}

func todoToggle() {
	toggleTodo(todoItems, todoSel)
	todoSave()
}

func todoAdd() {
	setCommentText("")
	uiApp.PushView("todoadd")
}

// todoRow renders one TODO line. The checkbox/branch is pointer-bound (If(&...))
// so each row reflects its own item — a Go if would bake the placeholder element's
// branch into the single compiled row template (the List-builds-once trap).
func todoRow(it *todoItem) Component {
	return HBox(
		SpaceW(1),
		If(&it.Done).Then(Text("[x] ").FG(&cAdd)).Else(
			If(&it.IsTask).Then(Text("[ ] ").FG(&cSubtle)).Else(Text("")),
		),
		If(&it.IsTask).Then(Text(&it.Text).FG(&cFG)).Else(Text(&it.Raw).FG(&cMuted)),
	)
}

func setupTodoView() {
	uiApp.View("todoedit",
		VBox.Fill(cBG).CascadeStyle(&Style{Fill: cBG, BG: cBG, FG: cFG}).PaddingTRBL(1, 2, 1, 2)(
			On(
				Key("j", func() { todoMove(1) }),
				Key("k", func() { todoMove(-1) }),
				Key("<Space>", todoToggle),
				Key("x", todoToggle),
				Key("a", todoAdd),
				Key("<Esc>", func() { uiApp.PopView() }),
				Key("q", func() { uiApp.PopView() }),
			),
			HBox(
				Text(&todoTitle).FG(cBright).Bold(),
				Space(),
				Text("space toggle · a add · esc close").FG(cMuted),
			),
			SpaceH(1),
			List(&todoItems).
				Selection(&todoSel).
				Marker("  ").
				SelectedStyle(Style{BG: cSelBG}).
				Render(todoRow),
		),
	).NoCounts()

	// add-line prompt (reuses the comment input machinery).
	save := func() {
		text := strings.TrimSpace(commentText)
		setCommentText("")
		uiApp.PopView()
		if text != "" {
			todoItems = addTodoItem(todoItems, text)
			todoSel = len(todoItems) - 1
			todoSave()
		}
	}
	cancel := func() { setCommentText(""); uiApp.PopView() }
	uiApp.View("todoadd",
		VBox.Fill(cBG)(
			promptKeys(save, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("add todo").FG(cBright).Bold(), Space(), Text("esc cancel · enter add").FG(cMuted)),
				SpaceH(1),
				commentInput(),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("todoadd")
}
