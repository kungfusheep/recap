package main

import (
	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/diff"
)

// diffView is the diff pane's state in one concrete struct (the 5a/5b/5c pattern):
// the native-scroll layer + its row metadata, the parsed files, the compile-once
// row VMs, and the jump-pick machinery. One package instance (diffUI) — fields are
// pointer-bound into compiled templates (&diffUI.Focused, &diffUI.FilesText…), so
// the struct must be a stable package var. No interfaces, no injection.
type diffView struct {
	// the native-scroll Layer. renderDiffLayer builds a component tree from
	// Files/Banner into the buffer on content/size change (see prepDiffRows),
	// then the framework blits the visible window each frame — scroll is free.
	Layer  *Layer
	Meta   []diffLineMeta // one entry per rendered row (render order): anchor info
	Banner [][]Span       // optional context rows prepended to the diff
	Files  []diff.File
	Rows   []diffRowVM // compile-once span rows the template's ForEach binds
	Tmpl   *Template   // the pane's single compiled template (built on first use)

	FilesText string // "N files changed" header line (or the no-diff explanation)

	// line-comment "pick a line" mode rides glyph's jump-label engine: while
	// uiApp.JumpModeActive(), renderDiffLayer registers one jump target per visible
	// commentable row at its on-screen position. glyph assigns the labels, paints
	// them onto the frame, and routes the keystrokes. The diff is a scrolled layer
	// so the row→screen mapping is ours (ViewRef = the LayerView's screen rect);
	// only the label engine is glyph's.
	ViewRef NodeRef // screen rect of the diff LayerView, for jump-target coords
	// PickAction is what to do with the picked diff line (comment on it, or open it
	// in $EDITOR). Set before EnterJumpMode; the picked target's onSelect calls it.
	PickAction func(diffLineMeta)
	// PickHeaders switches jump-pick from commentable body rows to file-header rows
	// (the fold-pick mode).
	PickHeaders bool
	// the anchor of the line currently being commented on (set when picked,
	// consumed by saveLineComment).
	PickFile, PickAnchor, PickSnippet string
	PickLine                          int

	Folded map[string]bool // file → collapsed to its header row
	// Commented marks diff rows that already carry a draft comment, keyed by
	// "file:line", so renderDiffLayer can draw a visual cue in the gutter.
	Commented map[string]bool

	// Focused mirrors pane=="diff" as a 0/1 opacity target so the diff scrollbar
	// fades in only when the diff column has focus (mail's cue).
	Focused float64
}

// diffUI is the single instance the templates bind against.
var diffUI = diffView{
	Folded:    map[string]bool{},
	Commented: map[string]bool{},
}

// diffRowVM is one rendered diff row: a span slice the compiled template re-reads
// every frame (Rich pointer binding — see prepDiffRows). BG carries a full-width
// band (file headers, comment washes) via the row container's per-item Fill —
// zero Mode means no band; no padding spans are allocated.
type diffRowVM struct {
	Spans []Span
	BG    Color
}

// diffTemplate returns the diff pane's single compiled template (built on first use:
// one ForEach over the row VMs, one Rich per row).
func diffTemplate() *Template {
	if diffUI.Tmpl == nil {
		diffUI.Tmpl = Build(VBox.Fill(&cBG).Gap(0)(
			// the row container's Fill carries full-width bands (file headers,
			// comment washes) — per-item colour bindings resolve per rendered
			// element, so no padding spans are needed to reach the edge.
			ForEach(&diffUI.Rows, func(r *diffRowVM) Component {
				return HBox.Fill(&r.BG)(Rich(&r.Spans).CharWrap())
			}),
		))
	}
	return diffUI.Tmpl
}
