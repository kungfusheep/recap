package main

import (
	"reflect"
	"testing"
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

// diffTarget picks the first code row (file + real line) at or after the diff
// scroll position, using the selected task's repo path.
func TestDiffTarget(t *testing.T) {
	st := testStore(t)
	prevStore, prevRows, prevSel := uiStore, vmRows, sel
	prevMeta, prevLayer := diffMeta, diffLayer
	uiStore = st
	diffLayer = nil // no scroll → start at 0
	t.Cleanup(func() {
		uiStore = prevStore
		vmRows = prevRows
		sel = prevSel
		diffMeta = prevMeta
		diffLayer = prevLayer
		taskByID = map[int64]Task{}
	})
	st.Add(Task{Repo: "r", RepoPath: "/repo/path", SHA: "abc", Title: "t", Status: StatusPending})
	reloadTasks()
	sel = 0

	// banner + file header (no line) then a real code row
	diffMeta = []diffLineMeta{
		{},                         // banner
		{File: "main.go"},          // file header, Line 0
		{File: "main.go", Line: 0}, // deletion, Line 0
		{File: "main.go", Line: 42, Commentable: true}, // the target
	}
	file, line, repo := diffTarget()
	if file != "main.go" || line != 42 {
		t.Fatalf("diffTarget = %s:%d, want main.go:42", file, line)
	}
	if repo != "/repo/path" {
		t.Fatalf("repo = %q, want /repo/path", repo)
	}

	// no code rows → empty file (caller shows a message, doesn't launch)
	diffMeta = []diffLineMeta{{}, {File: "x.go"}}
	if f, _, _ := diffTarget(); f != "" {
		t.Fatalf("no code row should yield empty file, got %q", f)
	}
}
