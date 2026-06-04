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

	// the shared add/edit prompt: editingTodoIdx is -1 for add (append a new task),
	// or the index of the line being edited.
	editingTodoIdx = -1

	// todoOpen gates the TODO editor panel inside buildMain. It used to be a
	// separate PushView, but glyph's If-gated modal pop doesn't fire reliably in a
	// secondary view, so the prompt over it leaked a modal router (multiple-Esc) and
	// its screen effects didn't apply. As an in-buildMain panel it behaves like the
	// inbox: one view, consistent modal + effect handling.
	todoOpen bool
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
	todoSel = len(items) - 1 // open scrolled to the bottom (newest items / where adds land)
	if todoSel < 0 {
		todoSel = 0
	}
	todoTitle = "TODO · " + t.Repo
	todoPrep()
	todoOpen = true
	uiApp.RequestRender()
}

func closeTodoEditor() {
	todoOpen = false
	uiApp.RequestRender()
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

// vim-style navigation for the TODO list (matches the diff pane's g/G/C-d/C-u).
const todoHalfPage = 10

func todoTop()      { todoSel = 0; todoPrep() }
func todoBottom()   { todoSel = len(todoItems) - 1; todoMove(0) } // clamps + preps
func todoHalfDown() { todoMove(todoHalfPage) }
func todoHalfUp()   { todoMove(-todoHalfPage) }

func todoSave() {
	if err := writeTodo(todoPath, todoItems); err != nil {
		statusMsg = "todo write: " + err.Error()
		return
	}
	// editing the TODO in-app changes the same file the upcoming section reads, but
	// that's an in-process write (no SIGUSR1, same repo), so force the upcoming list
	// to reload — otherwise a just-added todo wouldn't appear until the repo changed.
	upcomingRepo = ""
	if uiApp != nil {
		uiApp.RequestRender()
	}
}

func todoToggle() {
	toggleTodo(todoItems, todoSel)
	todoPrep()
	todoSave()
}

func todoAdd() {
	editingTodoIdx = -1
	openInputPrompt("add todo", "", "", "", func() { applyTodoPromptText(commentField.Value) })
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
	openInputPrompt("edit todo", "", "", prefill, func() { applyTodoPromptText(commentField.Value) })
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

// todoEditorPanel is the full-screen TODO editor, swapped in for the columns by
// buildMain when todoOpen. On.Modal makes it exclusive (suppressing the inbox
// keys) while open; the add/edit prompt overlay (in buildMain) floats over it.
func todoEditorPanel() Component {
	return VBox.Fill(cBG).CascadeStyle(&Style{Fill: cBG, BG: cBG, FG: cFG}).Grow(1).PaddingTRBL(1, 2, 1, 2)(
		On.Modal(
			Key("j", func() { todoMove(1) }),
			Key("k", func() { todoMove(-1) }),
			Key("g", todoTop),
			Key("G", todoBottom),
			Key("<C-d>", todoHalfDown),
			Key("<C-u>", todoHalfUp),
			Key("<Space>", todoToggle),
			Key("x", todoToggle),
			Key("a", todoAdd),
			Key("e", todoEditLine),
			Key("<Esc>", closeTodoEditor),
			Key("q", closeTodoEditor),
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
	)
}
