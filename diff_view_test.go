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
