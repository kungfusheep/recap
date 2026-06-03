package main

import (
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

// the jump-pick flow (review #148): openEditorPick arms pick mode with the editor
// action, and picking a labelled line runs that action with the picked row's meta
// (here a spy, so no editor is launched), then leaves pick mode.
func TestEditorPickFlow(t *testing.T) {
	prevLayer, prevAction := diffLayer, pickAction
	diffLayer = NewLayer()
	diffLayer.Render = func() {}
	t.Cleanup(func() {
		diffLayer = prevLayer
		pickAction = prevAction
		setPickMode(false)
		diffMeta = nil
		clear(diffLabelByRow)
	})

	diffMeta = []diffLineMeta{
		{}, // banner (not commentable)
		{File: "main.go", Line: 42, Commentable: true}, // labelled row 1
		{File: "util.go", Line: 7, Commentable: true},  // labelled row 2
	}

	// arming: openEditorPick enters pick mode with the editor action
	openEditorPick()
	if pickMode != "on" {
		t.Fatalf("openEditorPick should enter pick mode, got %q", pickMode)
	}
	if pickAction == nil {
		t.Fatal("openEditorPick should set the editor pickAction")
	}

	// swap in a spy so picking doesn't launch a real editor, then pick label 'b'→row2
	var got diffLineMeta
	pickAction = func(m diffLineMeta) { got = m }
	diffLabelByRow['a'] = 1
	diffLabelByRow['b'] = 2
	pickDiffLine('b')

	if got.File != "util.go" || got.Line != 7 {
		t.Fatalf("picked the wrong line: %+v", got)
	}
	if pickMode != "off" {
		t.Fatal("pick mode should clear after a pick")
	}
}
