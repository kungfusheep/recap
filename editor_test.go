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

// makePickLabels uses single chars while they fit, then 2-char combos so a tall
// diff never runs out of jump labels (the "ran out of jump characters" bug).
func TestMakePickLabels(t *testing.T) {
	if got := makePickLabels(3); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("n=3 single labels wrong: %v", got)
	}
	if got := makePickLabels(52); len(got) != 52 || len(got[51]) != 1 {
		t.Fatalf("n=52 should stay single-char: last=%q", got[51])
	}
	got := makePickLabels(100) // > 52 → 2-char
	if len(got) != 100 || len(got[0]) != 2 || got[0] != "aa" || got[26] != "ba" {
		t.Fatalf("n=100 2-char labels wrong: first=%q [26]=%q", got[0], got[26])
	}
	// no duplicates
	seen := map[string]bool{}
	for _, l := range got {
		if seen[l] {
			t.Fatalf("duplicate label %q", l)
		}
		seen[l] = true
	}
}

// a 2-char label needs both keystrokes: the first is a prefix (no pick yet), the
// second completes the match and fires the action.
func TestPickTwoCharLabel(t *testing.T) {
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
		{File: "x.go", Line: 1, Commentable: true},
		{File: "y.go", Line: 9, Commentable: true},
	}
	setPickMode(true)
	clear(diffLabelByRow)
	diffLabelByRow["aa"] = 0
	diffLabelByRow["ab"] = 1

	var got diffLineMeta
	picks := 0
	pickAction = func(m diffLineMeta) { got = m; picks++ }

	pickDiffLine('a') // prefix of "aa"/"ab" → buffer, no pick
	if picks != 0 || pickBuffer != "a" {
		t.Fatalf("after first char: picks=%d buffer=%q (want 0, \"a\")", picks, pickBuffer)
	}
	pickDiffLine('b') // completes "ab" → picks row 1
	if picks != 1 || got.File != "y.go" || got.Line != 9 {
		t.Fatalf("after second char: picks=%d got=%+v", picks, got)
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
	diffLabelByRow["a"] = 1
	diffLabelByRow["b"] = 2
	pickDiffLine('b')

	if got.File != "util.go" || got.Line != 7 {
		t.Fatalf("picked the wrong line: %+v", got)
	}
	if pickMode != "off" {
		t.Fatal("pick mode should clear after a pick")
	}
}
