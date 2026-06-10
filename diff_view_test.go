package main

import (
	"fmt"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/diff"
	"github.com/kungfusheep/recap/theme"
	"os"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// renderDiff drives the REAL compile-once path: set the model, prep the row VMs,
// execute the single compiled template (no Build at render time — the cardinal rule).
// Returns the buffer; diffMeta is populated as a side effect, like production.
func renderDiff(t *testing.T, files []diff.File, w, h int) *Buffer {
	t.Helper()
	prevFiles, prevBanner := diffFiles, diffBanner
	t.Cleanup(func() { diffFiles, diffBanner = prevFiles, prevBanner })
	diffFiles, diffBanner = files, nil
	prepDiffRows(w)
	buf := NewBuffer(w, h)
	diffTemplate().Execute(buf, int16(w), int16(h))
	return buf
}

// buildDiffView renders the diff as components equivalently to the hand-rolled path:
// a full-width header band, the body lines (+/-/context) present, and a full-width wash
// on a commented line. Asserted on the rendered buffer. (diff-renderer-as-components)
func TestBuildDiffViewRenders(t *testing.T) {
	defer func() { clear(commentedLines) }()
	files := []diff.File{{
		Path:   "main.go",
		Status: "modified",
		Hunks: []diff.Hunk{{
			Header: "@@ -1,3 +1,3 @@",
			Lines: []diff.Line{
				{Kind: diff.LineAdd, Text: "added line"},
				{Kind: diff.LineDel, Text: "removed line"},
				{Text: "context line"}, // zero Kind → context
			},
		}},
	}}
	start := hunkNewStart("@@ -1,3 +1,3 @@") // new-side start = the added line's number
	commentedLines[lineKey("main.go", start)] = true

	buf := renderDiff(t, files, 40, 30)
	meta := diffMeta
	w, h := 40, len(meta)+2
	_ = w

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
	files := []diff.File{{
		Path:   "main.go",
		Status: "modified",
		Hunks:  []diff.Hunk{{Header: "@@ -1,2 +1,2 @@", Lines: []diff.Line{{Kind: diff.LineAdd, Text: "x"}, {Kind: diff.LineDel, Text: "y"}}}},
	}}
	renderDiff(t, files, 40, 4)
	openMeta := append([]diffLineMeta(nil), diffMeta...)
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
	renderDiff(t, files, 40, 4)
	foldMeta := append([]diffLineMeta(nil), diffMeta...)
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

// regression (c179): the diff body must PRESERVE leading whitespace/indentation. The
// Rich Textf path trims it; plain Text keeps it. Asserted on the rendered buffer.
func TestBuildDiffViewPreservesIndent(t *testing.T) {
	files := []diff.File{{
		Path:   "main.go",
		Status: "modified",
		Hunks:  []diff.Hunk{{Header: "@@ -a,b +c,d @@", Lines: []diff.Line{{Kind: diff.LineAdd, Text: "        deeplyIndented()"}}}},
	}}
	buf := renderDiff(t, files, 60, 30)
	meta := diffMeta

	found := false
	for y := 0; y < len(meta)+2; y++ {
		line := buf.GetLine(y)
		if strings.Contains(line, "deeplyIndented") {
			found = true
			// "+ " gutter + 8 spaces of indent must be intact before the code
			if !strings.Contains(line, "+         deeplyIndented") {
				t.Fatalf("indentation lost: %q", line)
			}
		}
	}
	if !found {
		t.Fatal("added line not rendered")
	}
}

// added code is syntax-highlighted while gutters and removed lines keep their diff colour:
// the '+' gutter stays cAdd, an added keyword gets a syntax colour (≠ cAdd, ≠ cFG), and a
// removed line stays cDel (not highlighted). (chroma highlighting)
func TestDiffAddedLineHighlighted(t *testing.T) {
	files := []diff.File{{
		Path:   "main.go",
		Status: "modified",
		Hunks: []diff.Hunk{{Header: "@@ -1,2 +1,2 @@", Lines: []diff.Line{
			{Kind: diff.LineAdd, Text: "func main() {"},
			{Kind: diff.LineDel, Text: "func old() {"},
		}}},
	}}
	buf := renderDiff(t, files, 60, 30)
	meta := diffMeta

	addY, delY := -1, -1
	for y := 0; y < len(meta)+2; y++ {
		line := buf.GetLine(y)
		if strings.Contains(line, "func main") {
			addY = y
		}
		if strings.Contains(line, "func old") {
			delY = y
		}
	}
	if addY < 0 || delY < 0 {
		t.Fatalf("added/removed lines not rendered (add=%d del=%d)", addY, delY)
	}

	// added: '+' gutter green; 'func' keyword syntax-coloured (not the gutter green, not plain fg)
	if g := buf.Get(0, addY).Style.FG; g != cAdd {
		t.Fatalf("added gutter '+' = %v, want cAdd", g)
	}
	if kw := buf.Get(2, addY).Style.FG; kw == cAdd || kw == cFG {
		t.Fatalf("added 'func' keyword not highlighted: %v (cAdd=%v cFG=%v)", kw, kw == cAdd, kw == cFG)
	}
	// removed: gutter + code stay cDel (no highlighting)
	if g := buf.Get(0, delY).Style.FG; g != cDel {
		t.Fatalf("removed gutter '-' = %v, want cDel", g)
	}
	if code := buf.Get(2, delY).Style.FG; code != cDel {
		t.Fatalf("removed code should stay cDel (unhighlighted), got %v", code)
	}
}

// regression (#127): an inbox reload that adds an item but leaves the SELECTED task
// unchanged must keep the diff scroll position; switching to a different task resets it.
func TestDiffScrollPreservedOnReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	g := func(a ...string) { git(dir, a...) }
	g("init")
	g("config", "user.email", "t@t")
	g("config", "user.name", "t")
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(dir+"/a.txt", []byte(b.String()), 0o644)
	g("add", "-A")
	g("commit", "-m", "add a")
	sha, _ := git(dir, "rev-parse", "--short", "HEAD")

	st := testStore(t)
	prevStore, prevApp, prevLayer := uiStore, uiApp, diffLayer
	uiStore = st
	uiApp = NewApp()
	diffLayer = NewLayer()
	diffLayer.Render = renderDiffLayer
	expandedTasks = map[int64]bool{}
	t.Cleanup(func() {
		uiStore = prevStore
		uiApp = prevApp
		diffLayer = prevLayer
		vmRows, diffMeta, diffFiles = nil, nil, nil
		sel, lastSel, lastLen, lastDiffKey, detailDirty = 0, 0, 0, "", false
	})
	st.Add(db.Task{Repo: "r", RepoPath: dir, SHA: sha, Title: "t1", Status: db.StatusPending})
	reloadTasks()
	sel = 0
	uiApp.SetView(VBox.Width(80).Height(8)(
		HBox.Grow(1).NodeRef(&diffViewRef)(LayerView(diffLayer).Grow(1)),
	))
	detailDirty = true
	refreshDetail()
	uiApp.RenderNow()

	diffLayer.ScrollTo(5)
	uiApp.RenderNow()
	scrolled := diffLayer.ScrollY()
	if scrolled == 0 {
		t.Fatal("could not scroll the diff (setup)")
	}

	// inbox reload: add a NEWER task (sorts after in the oldest-first inbox), so sel still
	// points at t1 — the shown diff is unchanged, scroll must be kept.
	st.Add(db.Task{Repo: "r", RepoPath: dir, SHA: sha, Title: "t2", Status: db.StatusPending})
	reloadTasks()
	detailDirty = true
	refreshDetail()
	uiApp.RenderNow()
	if diffLayer.ScrollY() != scrolled {
		t.Fatalf("scroll reset on same-task reload: was %d, now %d", scrolled, diffLayer.ScrollY())
	}

	// switching to a different task DOES reset scroll to the top
	sel = 1
	detailDirty = true
	refreshDetail()
	uiApp.RenderNow()
	if diffLayer.ScrollY() != 0 {
		t.Fatalf("switching tasks should reset scroll, got %d", diffLayer.ScrollY())
	}
}

// foldAllFiles toggles every file between folded and open. (close all files)
func TestFoldAllFiles(t *testing.T) {
	defer func() { clear(fileFolded); diffFiles = nil }()
	diffFiles = []diff.File{{Path: "a.go"}, {Path: "b.go"}}
	foldAllFiles() // none folded → fold all
	if !fileFolded["a.go"] || !fileFolded["b.go"] {
		t.Fatal("foldAllFiles should fold every file")
	}
	foldAllFiles() // all folded → unfold all
	if fileFolded["a.go"] || fileFolded["b.go"] {
		t.Fatal("foldAllFiles again should unfold every file")
	}
}

// nextFile / prevFile scroll to the next / previous file header. (file navigation)
func TestNextPrevFile(t *testing.T) {
	prev := diffLayer
	t.Cleanup(func() { diffLayer = prev; diffMeta = nil })
	diffLayer = NewLayer()
	diffLayer.SetViewport(80, 5)
	diffLayer.SetBuffer(NewBuffer(80, 30)) // maxScroll = 25
	diffMeta = make([]diffLineMeta, 30)
	diffMeta[2].FileHeader = true
	diffMeta[12].FileHeader = true
	diffMeta[20].FileHeader = true

	diffLayer.ScrollTo(0)
	nextFile()
	if diffLayer.ScrollY() != 2 {
		t.Fatalf("nextFile from 0 → %d, want 2", diffLayer.ScrollY())
	}
	nextFile()
	if diffLayer.ScrollY() != 12 {
		t.Fatalf("nextFile from 2 → %d, want 12", diffLayer.ScrollY())
	}
	prevFile()
	if diffLayer.ScrollY() != 2 {
		t.Fatalf("prevFile from 12 → %d, want 2", diffLayer.ScrollY())
	}
	prevFile() // before the first header → top
	if diffLayer.ScrollY() != 0 {
		t.Fatalf("prevFile from 2 → %d, want 0 (top)", diffLayer.ScrollY())
	}
}

// a renamed file's header shows BOTH ends of the move (old → new) — a pure rename
// has no hunks, so without this the header read like an untouched file (#f6b4dfba).
func TestDiffViewShowsRenames(t *testing.T) {
	files := []diff.File{{
		Path:    "new/place.go",
		OldPath: "old/place.go",
		Status:  "renamed",
	}}
	buf := renderDiff(t, files, 80, 6)
	full := ""
	for y := 0; y < 6; y++ {
		full += buf.GetLine(y) + "\n"
	}
	if !strings.Contains(full, "old/place.go → new/place.go") {
		t.Fatalf("rename header should show old → new, got:\n%s", full)
	}
	if !strings.Contains(full, "»") {
		t.Fatalf("rename header should keep the » marker:\n%s", full)
	}
}

// syntax colours FOLLOW THE THEME (todo:810c70a9 + c307): the mfd vim scheme is
// monotone-with-decoration, so the rendered cells must carry it — 'return' keyword =
// theme Bright + BOLD, the string literal = theme FG + ITALIC — and the ramp re-hues
// on theme switch.
func TestSyntaxColoursFollowTheme(t *testing.T) {
	t.Cleanup(func() { setThemeVars(theme.Dark) })

	files := []diff.File{{Path: "main.go", Status: "modified", Hunks: []diff.Hunk{{
		Header: "@@ -a,b +c,d @@",
		Lines:  []diff.Line{{Kind: diff.LineAdd, Text: `return "hi"`}},
	}}}}

	cellAt := func(needle rune) (Cell, bool) {
		buf := renderDiff(t, files, 60, 8)
		for y := 0; y < 8; y++ {
			for x := 0; x < 60; x++ {
				if buf.Get(x, y).Rune == needle {
					return buf.Get(x, y), true
				}
			}
		}
		return Cell{}, false
	}

	// dark is NON-mono → nord (multi-hue): keyword takes nord's blue, not the ramp
	setThemeVars(theme.Dark)
	kw, ok := cellAt('r') // 'return'
	if !ok {
		t.Fatal("keyword not rendered")
	}
	nordKW := styles.Get("nord").Get(chroma.Keyword).Colour
	if kw.Style.FG != RGB(nordKW.Red(), nordKW.Green(), nordKW.Blue()) {
		t.Fatalf("dark keyword: fg=%v, want nord %v", kw.Style.FG, nordKW)
	}

	amber, _ := theme.ByName("mfd-amber")
	setThemeVars(amber)
	kw2, ok := cellAt('r')
	if !ok {
		t.Fatal("keyword not rendered (amber)")
	}
	if kw2.Style.FG != amber.Bright || kw2.Style.Attr&AttrBold == 0 {
		t.Fatalf("amber keyword: fg=%v attr=%v, want amber Bright+bold", kw2.Style.FG, kw2.Style.Attr)
	}
	if kw2.Style.FG == kw.Style.FG {
		t.Fatal("keyword colour did not follow the theme switch")
	}
}
