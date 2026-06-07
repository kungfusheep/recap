package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

// buildDiffLines must mark the file-name row (and only it) as a FileHeader, so the
// render can paint a full-width band behind it. (todo #122)
func TestBuildDiffLinesMarksFileHeader(t *testing.T) {
	files := []DiffFile{{
		Path:   "main.go",
		Status: "modified",
		Hunks: []DiffHunk{{
			Header: "@@ -1,2 +1,2 @@",
			Lines:  []DiffLine{{Kind: LineAdd, Text: "x := 1"}, {Kind: LineDel, Text: "y := 2"}},
		}},
	}}
	_, meta := buildDiffLines(files)
	if len(meta) < 4 {
		t.Fatalf("want >=4 rows (header, hunk, +, -), got %d", len(meta))
	}
	if !meta[0].FileHeader {
		t.Fatal("row 0 (the file path) should be marked FileHeader")
	}
	for i := 1; i < len(meta); i++ {
		if meta[i].FileHeader {
			t.Fatalf("row %d (hunk/code) must NOT be a file header", i)
		}
	}
}

// paintFileHeaderBands fills the header row's whole width with the band colour (the
// "stretch the width of the diff view" part), leaving other rows untouched. Asserted on
// the rendered buffer, not a helper's return value. (todo #122)
func TestFileHeaderBandFillsFullWidth(t *testing.T) {
	w, h := 20, 4
	buf := NewBuffer(w, h)
	meta := []diffLineMeta{
		{Commentable: true},  // code row
		{FileHeader: true},   // a file header
		{Commentable: true},  // code row
		{},                   // spacer
	}
	paintFileHeaderBands(buf, w, h, meta)

	for cx := 0; cx < w; cx++ {
		if got := buf.Get(cx, 1).Style.BG; got != cFileHdrBG {
			t.Fatalf("header row cell %d bg = %v, want full-width band %v", cx, got, cFileHdrBG)
		}
	}
	for _, y := range []int{0, 2, 3} {
		for cx := 0; cx < w; cx++ {
			if buf.Get(cx, y).Style.BG == cFileHdrBG {
				t.Fatalf("non-header row %d cell %d wrongly got the header band", y, cx)
			}
		}
	}
}
