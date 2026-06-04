package main

import (
	"reflect"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the spinner flare animates only when there's an in-flight item AND no external
// editor owns the terminal — so dropping into $EDITOR stops the flare drawing over it.
func TestSpinnerActiveGuard(t *testing.T) {
	defer func() { hasCurrent = false; inEditor.Store(false) }()

	hasCurrent = false
	inEditor.Store(false)
	if spinnerActive() {
		t.Fatal("no in-flight item → spinner must be idle")
	}
	hasCurrent = true
	if !spinnerActive() {
		t.Fatal("in-flight + no editor → spinner should animate")
	}
	inEditor.Store(true)
	if spinnerActive() {
		t.Fatal("editor owns the terminal → spinner must NOT animate (no overdraw)")
	}
	inEditor.Store(false)
	if !spinnerActive() {
		t.Fatal("editor closed → spinner resumes")
	}
}

// editorArgs builds +line argv for vim-family editors, splits EDITOR flags, and
// omits the +N when there's no real line.
func TestEditorArgs(t *testing.T) {
	cases := []struct {
		editor string
		file   string
		line   int
		want   []string
	}{
		{"nvim", "a.go", 12, []string{"nvim", "+12", "a.go"}},
		{"vim", "a.go", 0, []string{"vim", "a.go"}},
		{"nvim --clean", "x/y.go", 3, []string{"nvim", "--clean", "+3", "x/y.go"}},
		{"", "a.go", 5, []string{"vim", "+5", "a.go"}},
	}
	for _, tc := range cases {
		if got := editorArgs(tc.editor, tc.file, tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("editorArgs(%q,%q,%d) = %v, want %v", tc.editor, tc.file, tc.line, got, tc.want)
		}
	}
}

// the line-picker rides glyph's jump engine: openEditorPick sets the editor
// pickAction and enters jump mode; rendering the diff registers one jump target per
// visible commentable row (glyph assigns the labels), and firing a target's
// OnSelect runs pickAction with THAT row's meta. We assert the wiring end to end:
// target count == commentable rows, labels assigned, and OnSelect picks the right
// line (a spy, so no editor launches).
func TestJumpPickFlow(t *testing.T) {
	prevLayer, prevAction := diffLayer, pickAction
	uiApp = NewApp()
	diffLayer = NewLayer()
	diffLayer.Render = renderDiffLayer
	t.Cleanup(func() {
		if uiApp.JumpModeActive() {
			uiApp.ExitJumpMode()
		}
		uiApp = nil
		diffLayer = prevLayer
		pickAction = prevAction
		diffMeta, diffLines = nil, nil
	})

	// banner row (not commentable) + two commentable code rows
	diffMeta = []diffLineMeta{
		{Text: "@@ hunk @@"},
		{File: "main.go", Line: 42, Text: "a := 1", Commentable: true},
		{File: "util.go", Line: 7, Text: "b := 2", Commentable: true},
	}
	diffLines = [][]Span{
		{{Text: "@@ hunk @@"}},
		{{Text: "a := 1"}},
		{{Text: "b := 2"}},
	}
	// put the diff on screen so renderDiffLayer has a real viewport + screen rect
	uiApp.SetView(VBox.Width(80).Height(20)(
		HBox.Grow(1).NodeRef(&diffViewRef)(LayerView(diffLayer).Grow(1)),
	))
	uiApp.RenderNow()

	openEditorPick()
	if !uiApp.JumpModeActive() {
		t.Fatal("openEditorPick should enter glyph jump mode")
	}
	targets := uiApp.JumpMode().Targets
	if len(targets) != 2 {
		t.Fatalf("want 2 jump targets (commentable rows), got %d", len(targets))
	}
	for i, tg := range targets {
		if tg.Label == "" {
			t.Fatalf("target %d has no assigned label", i)
		}
	}

	// firing the second target's OnSelect should pick util.go:7 via pickAction
	var got diffLineMeta
	pickAction = func(m diffLineMeta) { got = m }
	targets[1].OnSelect()
	if got.File != "util.go" || got.Line != 7 {
		t.Fatalf("OnSelect picked the wrong line: %+v", got)
	}
}
