package main

import (
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
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

	// the shared add/edit prompt: editingTodoIdx is -1 for add (append a new task),
	// or the index of the line being edited.
	editingTodoIdx = -1
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
	todoPrep()
	uiApp.PushView("todoedit")
}

// todoPrep recomputes the per-row UI fields (selection band + the display text and
// colour). The display text is precomputed into one field so the row can render a
// plain Text in a Grow(1) box — a Then/Else conditional over pointer-bound Texts
// measures the empty placeholder branch at build time and clips every row (the
// truncation bug). Called after any change to todoSel/todoItems.
func todoPrep() {
	for i := range todoItems {
		todoItems[i].Selected = i == todoSel
		if todoItems[i].IsTask {
			todoItems[i].Display = todoItems[i].Text
			todoItems[i].FGColor = cFG
		} else {
			todoItems[i].Display = todoItems[i].Raw
			todoItems[i].FGColor = cMuted
		}
	}
}

func todoMove(d int) {
	todoSel += d
	if todoSel >= len(todoItems) {
		todoSel = len(todoItems) - 1
	}
	if todoSel < 0 {
		todoSel = 0
	}
	todoPrep()
}

func todoSave() {
	if err := writeTodo(todoPath, todoItems); err != nil {
		statusMsg = "todo write: " + err.Error()
	}
}

func todoToggle() {
	toggleTodo(todoItems, todoSel)
	todoPrep()
	todoSave()
}

func todoAdd() {
	editingTodoIdx = -1
	openInputPrompt("add todo", "", "", "", func() { applyTodoPromptText(commentText) })
}

// applyTodoPromptText commits the prompt text: in edit mode it rewrites the line
// being edited (task body or raw), otherwise it appends a new task. Empty input is
// ignored. Writes back to disk. Extracted from the prompt save so it's testable.
func applyTodoPromptText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if editingTodoIdx >= 0 && editingTodoIdx < len(todoItems) {
		if todoItems[editingTodoIdx].IsTask {
			todoItems[editingTodoIdx].Text = text
		} else {
			todoItems[editingTodoIdx].Raw = text
		}
	} else {
		todoItems = addTodoItem(todoItems, text)
		todoSel = len(todoItems) - 1
	}
	todoPrep()
	todoSave()
}

// todoEditLine opens the prompt pre-filled with the selected line's text; saving
// rewrites that line (its task body, or the raw text for a non-task line).
func todoEditLine() {
	if todoSel < 0 || todoSel >= len(todoItems) {
		return
	}
	it := todoItems[todoSel]
	editingTodoIdx = todoSel
	prefill := it.Raw
	if it.IsTask {
		prefill = it.Text
	}
	openInputPrompt("edit todo", "", "", prefill, func() { applyTodoPromptText(commentText) })
}

// todoRow renders one TODO line. The checkbox/branch is pointer-bound (If(&...))
// so each row reflects its own item — a Go if would bake the placeholder element's
// branch into the single compiled row template (the List-builds-once trap).
func todoRow(it *todoItem) Component {
	// per-row Fill claims the FULL row width — without it the List measures the row
	// from the empty placeholder element and clips every line to that tiny width
	// (the truncation bug). The Fill also paints the selection band. Grow(1) lets
	// the text occupy the remaining width.
	bg := If(&it.Selected).Then(&cSelBG).Else(&cBG)
	return VBox.Fill(bg).PaddingVH(0, 1)(
		HBox(
			// fixed-string checkbox conditionals are fine (they measure to their
			// literal width); only the variable text needed precomputing.
			If(&it.Done).Then(Text("[x] ").FG(&cAdd)).Else(
				If(&it.IsTask).Then(Text("[ ] ").FG(&cSubtle)).Else(Text("")),
			),
			// plain pointer Text (Display/FGColor precomputed in todoPrep) so the
			// Grow(1) slot measures full width instead of clipping to a placeholder.
			HBox.Grow(1)(
				Text(&it.Display).FG(&it.FGColor),
			),
		),
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
				Key("e", todoEditLine),
				Key("<Esc>", func() { uiApp.PopView() }),
				Key("q", func() { uiApp.PopView() }),
			),
			HBox(
				Text(&todoTitle).FG(cBright).Bold(),
				Space(),
				Text("space toggle · a add · e edit · esc close").FG(cMuted),
			),
			SpaceH(1),
			List(&todoItems).
				Selection(&todoSel).
				Marker("  ").
				SelectedStyle(Style{}). // band painted per-row (todoRow Fill)
				Render(todoRow),
			// the add/edit prompt floats over the TODO list (overlay, not PushView).
			inputPromptOverlay(),
		),
	).NoCounts()

	// route printable typing into the prompt while it's open over the TODO list
	// (its On.Modal captures the bound keys; runes fall through to here).
	if r, ok := uiApp.ViewRouter("todoedit"); ok {
		r.HandleUnmatched(func(k riffkey.Key) bool {
			if promptOpen && k.Rune != 0 && k.Mod == 0 {
				setCommentText(commentText + string(k.Rune))
				uiApp.RequestRender()
				return true
			}
			return false
		})
	}
}
