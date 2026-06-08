package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// buildDiffView renders the diff as components equivalently to the hand-rolled path:
// a full-width header band, the body lines (+/-/context) present, and a full-width wash
// on a commented line. Asserted on the rendered buffer. (diff-renderer-as-components)
func TestBuildDiffViewRenders(t *testing.T) {
	defer func() { clear(commentedLines) }()
	files := []DiffFile{{
		Path:   "main.go",
		Status: "modified",
		Hunks: []DiffHunk{{
			Header: "@@ -1,3 +1,3 @@",
			Lines: []DiffLine{
				{Kind: LineAdd, Text: "added line"},
				{Kind: LineDel, Text: "removed line"},
				{Text: "context line"}, // zero Kind → context
			},
		}},
	}}
	start := hunkNewStart("@@ -1,3 +1,3 @@") // new-side start = the added line's number
	commentedLines[lineKey("main.go", start)] = true

	tree, meta := buildDiffView(files, 40)
	w, h := 40, len(meta)+2
	buf := NewBuffer(w, h)
	Build(tree).Execute(buf, int16(w), int16(h))

	// file header row: banded full width + carries the path
	hdr := -1
	for i, m := range meta {
		if m.FileHeader {
			hdr = i
			break
		}
	}
	if hdr < 0 {
		t.Fatal("no file-header row in meta")
	}
	banded := 0
	for cx := 0; cx < w; cx++ {
		if buf.Get(cx, hdr).Style.BG == cFileHdrBG {
			banded++
		}
	}
	if banded < w/2 {
		t.Fatalf("header band not full width: %d/%d cells", banded, w)
	}
	if !strings.Contains(buf.GetLine(hdr), "main.go") {
		t.Fatalf("header row missing path: %q", buf.GetLine(hdr))
	}

	// body content present
	var all string
	for y := 0; y < h; y++ {
		all += buf.GetLine(y)
	}
	for _, want := range []string{"added line", "removed line", "context line"} {
		if !strings.Contains(all, want) {
			t.Fatalf("body missing %q\nfull:%q", want, all)
		}
	}

	// the commented (added) line is washed full width
	washRow := -1
	for i, m := range meta {
		if m.Commentable && m.File == "main.go" && m.Line == start {
			washRow = i
			break
		}
	}
	if washRow < 0 {
		t.Fatal("no commented row in meta")
	}
	washed := 0
	for cx := 0; cx < w; cx++ {
		if buf.Get(cx, washRow).Style.BG == cCommentBG {
			washed++
		}
	}
	if washed < w/2 {
		t.Fatalf("commented line not washed: %d/%d cells", washed, w)
	}
}

// a folded file collapses to its header only (no body rows in meta); an open file keeps
// its body. (per-file fold)
func TestBuildDiffViewFold(t *testing.T) {
	defer func() { clear(fileFolded) }()
	files := []DiffFile{{
		Path:   "main.go",
		Status: "modified",
		Hunks:  []DiffHunk{{Header: "@@ -1,2 +1,2 @@", Lines: []DiffLine{{Kind: LineAdd, Text: "x"}, {Kind: LineDel, Text: "y"}}}},
	}}
	_, openMeta := buildDiffView(files, 40)
	openBody := 0
	for _, m := range openMeta {
		if m.Commentable {
			openBody++
		}
	}
	if openBody != 2 {
		t.Fatalf("open file: want 2 commentable body rows, got %d", openBody)
	}

	fileFolded["main.go"] = true
	_, foldMeta := buildDiffView(files, 40)
	for _, m := range foldMeta {
		if m.Commentable {
			t.Fatal("folded file should have no commentable body rows")
		}
	}
	hasHdr := false
	for _, m := range foldMeta {
		if m.FileHeader && m.File == "main.go" {
			hasHdr = true
		}
	}
	if !hasHdr {
		t.Fatal("folded file should still show its header")
	}
}

// toggleFileFold flips a file's fold state and clears fold-pick mode. (diffLayer nil →
// setDiff is a no-op, so this exercises the state flip alone.)
func TestToggleFileFold(t *testing.T) {
	defer func() { clear(fileFolded); pickHeaders = false }()
	pickHeaders = true
	toggleFileFold(diffLineMeta{File: "x.go"})
	if !fileFolded["x.go"] {
		t.Fatal("toggle should fold the file")
	}
	if pickHeaders {
		t.Fatal("toggle should clear fold-pick mode")
	}
	toggleFileFold(diffLineMeta{File: "x.go"})
	if fileFolded["x.go"] {
		t.Fatal("toggle again should unfold")
	}
}
