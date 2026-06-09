package main

import (
	"github.com/kungfusheep/recap/diff"
	"reflect"
	"testing"

	. "github.com/kungfusheep/glyph"
)

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
	prevLayer, prevAction, prevFiles := diffLayer, pickAction, diffFiles
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
		diffFiles = prevFiles
		diffMeta = nil
	})

	// two files, each with one commentable code row — renderDiffLayer builds diffMeta from
	// diffFiles (the source of truth). new-side line numbers come from the hunk headers
	// (+42, +7), so the commentable rows are main.go:42 then util.go:7.
	diffFiles = []diff.File{
		{Path: "main.go", Status: "modified", Hunks: []diff.Hunk{{Header: "@@ -1,1 +42,1 @@", Lines: []diff.Line{{Kind: diff.LineAdd, Text: "a := 1"}}}}},
		{Path: "util.go", Status: "modified", Hunks: []diff.Hunk{{Header: "@@ -1,1 +7,1 @@", Lines: []diff.Line{{Kind: diff.LineAdd, Text: "b := 2"}}}}},
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
