package main

import (
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/config"
	"github.com/kungfusheep/recap/todo"
)

// The TODO editor: a modal view over the selected task's repo TODO file. Toggle
// checkboxes (space/x), add lines (a), and changes write straight back to disk
// (creating the file if it doesn't exist). The file is the human-owned queue the
// autonomous loop reads, so editing it here closes the loop from inside recap.

// todoView is the TODO editor's state in one concrete struct: the data (source of
// truth), the render view-models, the selection, and the add/edit bookkeeping.
// One package instance (todoUI) — its fields are pointer-bound into the compiled
// view (&todoUI.Items etc.), so the struct must be a stable package var, never
// reallocated. No interfaces, no injection — plain data + methods.
type todoView struct {
	Data  []todo.Item // the source of truth (pure data, from the todo package)
	Items []todoVM    // the render view-models, rebuilt from Data by prep
	Sel   int
	Path  string
	Title string

	// the shared add/edit prompt: EditingIdx is -1 for add (append a new task),
	// or the index of the line being edited.
	EditingIdx int
}

// todoUI is the single instance the view tree binds against.
var todoUI = todoView{EditingIdx: -1}

// todoVM is the UI view-model for one TODO line: a render-only projection of a
// todo.Item (data) with the per-row UI state the List binds by pointer. Data and UI
// are kept separate (like the inbox's tasks → vmRows) — the todo package owns no
// glyph/UI fields, this struct owns no file format.
type todoVM struct {
	IsTask   bool   // for the checkbox conditional (mirrors todo.Item.IsTask)
	Done     bool   // for the checkbox conditional (mirrors todo.Item.Done)
	Selected bool   // drives the per-row selection band
	Display  string // precomputed row text (Text for tasks, Raw otherwise) — see prep
	FGColor  Color  // precomputed row colour
}

// openTodoEditor resolves the selected task's repo TODO path (via the config
// template), loads it, and opens the editor. Reports why if it can't.
func openTodoEditor() {
	t, ok := selectedTask()
	if !ok {
		toast("no task selected")
		return
	}
	todoUI.openFor(t.Repo, t.RepoPath)
}

// openFor opens the TODO editor for a specific repo (by name + path), so the omnibox
// can launch any project's todo list, not just the selected task's. Reports why if it
// can't resolve/read the file.
func (tv *todoView) openFor(repo, repoPath string) {
	cfg, _ := config.LoadConfig()
	path, err := todo.PathFor(cfg.TODOTemplate, repoPath)
	if err != nil {
		toast("todo path: " + err.Error())
		return
	}
	if path == "" {
		toast("no todo_template configured (~/.config/recap/config.toml)")
		return
	}
	items, err := todo.Read(path)
	if err != nil {
		toast("todo read: " + err.Error())
		return
	}
	tv.Path = path
	tv.Data = items
	tv.Sel = len(items) - 1 // open scrolled to the bottom (newest items / where adds land)
	if tv.Sel < 0 {
		tv.Sel = 0
	}
	tv.Title = "TODO · " + repo
	tv.prep()
	// switch to the dedicated "todo" view via glyph's router (app.Go), NOT a manual
	// in-buildMain panel: a full view switch deactivates the inbox view — which pops
	// any modal it had pushed (e.g. the omnibox that launched this) deterministically,
	// instead of relying on a fade-out exit animation to release it. That fade timing
	// was the "todo opens but keys are dead, must kill" bug.
	uiApp.Go("todo")
}

func closeTodoEditor() {
	uiApp.Go("main")
}

// prep recomputes the render VMs from the data (selection band + the display text
// and colour). The display text is precomputed into one field so the row can render
// a plain Text in a Grow(1) box — a Then/Else conditional over pointer-bound Texts
// measures the empty placeholder branch at build time and clips every row (the
// truncation bug). Called after any change to Sel/Data.
func (tv *todoView) prep() {
	tv.Items = make([]todoVM, len(tv.Data))
	for i, d := range tv.Data {
		vm := todoVM{IsTask: d.IsTask, Done: d.Done, Selected: i == tv.Sel}
		if d.IsTask {
			vm.Display = d.Text
			vm.FGColor = cFG
		} else {
			vm.Display = d.Raw
			vm.FGColor = cMuted
		}
		tv.Items[i] = vm
	}
}

func (tv *todoView) move(d int) {
	tv.Sel += d
	if tv.Sel >= len(tv.Data) {
		tv.Sel = len(tv.Data) - 1
	}
	if tv.Sel < 0 {
		tv.Sel = 0
	}
	tv.prep()
}

// vim-style navigation for the TODO list (matches the diff pane's g/G/C-d/C-u).
const todoHalfPage = 10

func (tv *todoView) top()      { tv.Sel = 0; tv.prep() }
func (tv *todoView) bottom()   { tv.Sel = len(tv.Data) - 1; tv.move(0) } // clamps + preps
func (tv *todoView) halfDown() { tv.move(todoHalfPage) }
func (tv *todoView) halfUp()   { tv.move(-todoHalfPage) }

func (tv *todoView) save() {
	if err := todo.Write(tv.Path, tv.Data); err != nil {
		toast("todo write: " + err.Error())
		return
	}
	// editing the TODO in-app changes the same file the upcoming section reads, but
	// that's an in-process write (no SIGUSR1, same repo), so force the upcoming list
	// to reload — otherwise a just-added todo wouldn't appear until the repo changed.
	invalidateUpcoming()
	if uiApp != nil {
		uiApp.RequestRender()
	}
}

func (tv *todoView) toggle() {
	todo.Toggle(tv.Data, tv.Sel)
	tv.prep()
	tv.save()
}

func (tv *todoView) add() {
	tv.EditingIdx = -1
	promptUI.open("add todo → "+tv.Title, "", "", "", func() { tv.applyPromptText(promptUI.Field.Value) })
}

// applyPromptText commits the prompt text: in edit mode it rewrites the line being
// edited (task body or raw), otherwise it appends a new task. Empty input is
// ignored. Writes back to disk. Extracted from the prompt save so it's testable.
func (tv *todoView) applyPromptText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if tv.EditingIdx >= 0 && tv.EditingIdx < len(tv.Data) {
		if tv.Data[tv.EditingIdx].IsTask {
			tv.Data[tv.EditingIdx].Text = text
		} else {
			tv.Data[tv.EditingIdx].Raw = text
		}
	} else {
		tv.Data = todo.Add(tv.Data, text)
		tv.Sel = len(tv.Data) - 1
	}
	tv.prep()
	tv.save()
}

// editLine opens the prompt pre-filled with the selected line's text; saving
// rewrites that line (its task body, or the raw text for a non-task line).
func (tv *todoView) editLine() {
	if tv.Sel < 0 || tv.Sel >= len(tv.Data) {
		return
	}
	it := tv.Data[tv.Sel]
	tv.EditingIdx = tv.Sel
	prefill := it.Raw
	if it.IsTask {
		prefill = it.Text
	}
	promptUI.open("edit todo → "+tv.Title, "", "", prefill, func() { tv.applyPromptText(promptUI.Field.Value) })
}

// todoRow renders one TODO line. The checkbox/branch is pointer-bound (If(&...))
// so each row reflects its own item — a Go if would bake the placeholder element's
// branch into the single compiled row template (the List-builds-once trap).
func todoRow(it *todoVM) Component {
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
			// plain pointer Text (Display/FGColor precomputed in prep) so the
			// Grow(1) slot measures full width instead of clipping to a placeholder.
			HBox.Grow(1)(
				Text(&it.Display).FG(&it.FGColor),
			),
		),
	)
}

// buildTodoView is the full-screen TODO editor, registered as the named "todo" view
// and reached with app.Go (see openFor). As its own top-level view it owns its
// keys on the base router via plain On() (no modal stacking) — the inbox view isn't
// active behind it, so there are no inbox keys to suppress. The add/edit prompt is
// rendered here too (inputPromptOverlay), so it floats over the editor in this view
// and pushes/pops its own modal exactly as it does in the inbox.
func buildTodoView() Component {
	return VBox.Fill(&cBG).CascadeStyle(&bgStyle).Grow(1).PaddingTRBL(1, 2, 1, 2)(
		On(
			Key("j", func() { todoUI.move(1) }),
			Key("k", func() { todoUI.move(-1) }),
			Key("g", func() { todoUI.top() }),
			Key("G", func() { todoUI.bottom() }),
			Key("<C-d>", func() { todoUI.halfDown() }),
			Key("<C-u>", func() { todoUI.halfUp() }),
			Key("<Space>", func() { todoUI.toggle() }),
			Key("x", func() { todoUI.toggle() }),
			Key("a", func() { todoUI.add() }),
			Key("e", func() { todoUI.editLine() }),
			Key("<Esc>", closeTodoEditor),
			Key("q", closeTodoEditor),
		),
		HBox(
			Text(&todoUI.Title).FG(&cBright).Bold(),
			Space(),
			Text("space toggle · a add · e edit · esc close").FG(&cMuted),
		),
		SpaceH(1),
		List(&todoUI.Items).
			Selection(&todoUI.Sel).
			Marker("  ").
			SelectedStyle(Style{}). // band painted per-row (todoRow Fill)
			Render(todoRow),
		// transient status (write errors etc.), mirroring the inbox view
		If(&statusMsg).Then(HBox(Text(&statusMsg).FG(&cSubtle))),
		// the add/edit prompt floats over the editor in THIS view (same pattern as
		// the inbox), so typing works and its modal pops cleanly on close.
		inputPromptOverlay(),
	)
}
