package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"unicode"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// cleanLine makes arbitrary text safe to render as one terminal row: tabs are
// expanded to a 4-col stop, and carriage returns / C0 control chars /
// zero-width & invisible (Cf) runes are dropped. Raw git/source content is full
// of these and they wreck glyph's cell layout (cursor drift, ghosting, bleed).
func cleanLine(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 4 - (col % 4)
			for i := 0; i < n; i++ {
				b.WriteByte(' ')
				col++
			}
		case r == '\r' || r == '\n' || r < 0x20:
			// drop carriage returns and other C0 control characters
		case r > 0x7F && unicode.Is(unicode.Cf, r):
			// drop zero-width / invisible formatting runes (BOM, ZWSP, etc.)
		default:
			b.WriteRune(r)
			col++
		}
	}
	return b.String()
}

// mail-inspired warm-dark palette (borderless, fill + whitespace).
var (
	cBG     = Hex(0x1c1c1c)
	cBright = Hex(0xe8e6e3)
	cFG     = Hex(0xb8b5b0)
	cSubtle = Hex(0x8b8780)
	cMuted  = Hex(0x3f3c38)
	cSelBG  = Hex(0x302f2c)
	cFloat  = Hex(0x252421)
	cAdd    = Hex(0x8aa872) // diff +, muted green
	cDel    = Hex(0xc08a72) // diff -, muted terracotta
	cHunk   = Hex(0x6f8fa8) // @@ hunk, muted blue
)

// repo identity bar colours (like mail's per-sender tick).
var repoPalette = []Color{
	Hex(0x6f8fa8), Hex(0x8aa872), Hex(0xc08a72), Hex(0xa88fb0),
	Hex(0xc0a86a), Hex(0x6fa8a0), Hex(0xb07a7a),
}

func repoColor(name string) Color {
	var h int
	for _, r := range name {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return repoPalette[h%len(repoPalette)]
}

// taskVM is the per-row view-model. Selected is updated in place each frame so
// the row fill reacts (mail's pattern); rebuilt only when the task set changes.
type taskVM struct {
	ID         int64
	Title      string
	When       string
	Repo       string
	Glyph      string
	GlyphColor Color
	RepoColor  Color
	Pending    bool
	Selected   bool
	HasGroup   bool
	GroupLabel string
}

var (
	uiStore *Store
	uiApp   *App

	tasks    []Task
	vmRows   []taskVM
	sel      int
	repoFltr string
	repos    []string

	// diff pane: a native-scroll Layer. diffLines is the full styled content
	// (every span carries BG so cells never fall back to terminal default);
	// renderDiffLayer builds the buffer on content/size change, then the
	// framework blits the visible window each frame — scroll is free.
	diffLayer *Layer
	diffLines [][]Span
	diffMeta  []diffLineMeta // parallel to diffLines: anchor info per row

	// line-comment "pick a line" mode: renderDiffLayer draws label chars in the
	// gutter of visible commentable rows; diffLabelByRow maps label → row.
	diffCommentMode bool
	diffLabelByRow  = map[rune]int{}

	// the anchor of the line currently being commented on (set when picked).
	pcFile, pcAnchor, pcSnippet string
	pcLine                      int

	// in-flight submit-review verdict (chosen before the summary prompt).
	reviewVerdict string
	verdictLabel  string

	// display strings for the line-comment prompt
	pcLocation    string
	pcSnippetView string

	draftNote string // e.g. "✎ 2 draft" when the current task has draft comments

	countText, filterText string
	detailTitle           string
	metaRepo, metaWhen    string
	metaCheck, metaResult string
	metaResultColor       = cSubtle
	filesText             string
	diffFiles             []DiffFile
	statusMsg             string
	commentText           string

	lastSel, lastLen int
	lastFltr         string
	detailDirty      bool
)

func runUI() error {
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	uiStore = st

	uiApp = NewApp()

	diffLayer = NewLayer()
	diffLayer.Render = renderDiffLayer

	reloadTasks()
	setupCommentView()
	setupReviewViews()
	uiApp.SetView(buildMain())
	uiApp.OnResize(func(w, h int) { uiApp.SetView(buildMain()) })
	uiApp.OnBeforeRender(refreshDetail)
	uiApp.Router().NoCounts()
	return uiApp.Run()
}

// --- data ------------------------------------------------------------------

func statusPriority(s string) int {
	switch s {
	case StatusPending:
		return 0
	case StatusRedo:
		return 1
	default:
		return 2
	}
}

func groupLabel(s string) string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusRedo:
		return "NEEDS REWORK"
	default:
		return "APPROVED"
	}
}

func reloadTasks() {
	tasks, _ = uiStore.List("", repoFltr)
	// inbox order: pending, then rework, then approved; newest first within each
	sort.SliceStable(tasks, func(i, j int) bool {
		pi, pj := statusPriority(tasks[i].Status), statusPriority(tasks[j].Status)
		if pi != pj {
			return pi < pj
		}
		return tasks[i].ID > tasks[j].ID
	})
	if sel >= len(tasks) {
		sel = len(tasks) - 1
	}
	if sel < 0 {
		sel = 0
	}

	vmRows = vmRows[:0]
	prev := ""
	for _, t := range tasks {
		vm := taskVM{
			ID:         t.ID,
			Title:      t.Title,
			Repo:       t.Repo,
			Glyph:      statusGlyph(t.Status),
			GlyphColor: glyphColor(t.Status),
			RepoColor:  repoColor(t.Repo),
			Pending:    t.Status == StatusPending,
		}
		if len(t.CreatedAt) >= 16 {
			vm.When = t.CreatedAt[11:16]
		}
		if t.Status != prev {
			vm.HasGroup = true
			vm.GroupLabel = groupLabel(t.Status)
			prev = t.Status
		}
		vmRows = append(vmRows, vm)
	}

	// distinct repos for the filter cycle (from the unfiltered set)
	all, _ := uiStore.List("", "")
	seen := map[string]bool{}
	repos = repos[:0]
	for _, t := range all {
		if !seen[t.Repo] {
			seen[t.Repo] = true
			repos = append(repos, t.Repo)
		}
	}
	detailDirty = true
}

func glyphColor(status string) Color {
	switch status {
	case StatusApproved:
		return cSubtle
	case StatusRedo:
		return cDel
	default:
		return cBright
	}
}

// refreshDetail updates selection fill + the right-hand detail when selection,
// filter, or task set changes — never per-frame git calls.
func refreshDetail() {
	for i := range vmRows {
		vmRows[i].Selected = i == sel
	}
	filterText = "all"
	if repoFltr != "" {
		filterText = repoFltr
	}
	countText = fmt.Sprintf("%d", len(tasks))

	if sel == lastSel && len(tasks) == lastLen && repoFltr == lastFltr && !detailDirty {
		return
	}
	lastSel, lastLen, lastFltr, detailDirty = sel, len(tasks), repoFltr, false

	if len(tasks) == 0 || sel < 0 || sel >= len(tasks) {
		detailTitle, metaRepo, metaWhen, metaCheck, metaResult = "no tasks", "", "", "", ""
		filesText, diffFiles, draftNote = "", nil, ""
		setDiff()
		return
	}
	t := tasks[sel]
	detailTitle = t.Title
	metaRepo, metaWhen = t.Repo, t.CreatedAt
	metaCheck = "check: " + dash(t.Criterion)
	metaResult = dash(t.Result)
	metaResultColor = resultColor(t.Result)
	if _, n, ok := uiStore.DraftInfo(t.ID); ok && n > 0 {
		draftNote = fmt.Sprintf("✎ %d draft", n)
	} else {
		draftNote = ""
	}

	if t.SHA == "" || t.RepoPath == "" {
		filesText, diffFiles = "no diff — task has no sha", nil
		setDiff()
		return
	}
	filesText = changedFiles(t)
	full, err := git(t.RepoPath, "show", "--format=", t.SHA)
	if err != nil {
		diffFiles = nil
	} else {
		diffFiles = parseUnifiedDiff(full)
	}
	setDiff()
}

func resultColor(r string) Color {
	switch {
	case strings.Contains(strings.ToUpper(r), "PASS"):
		return cAdd
	case strings.Contains(strings.ToUpper(r), "FAIL"):
		return cDel
	default:
		return cSubtle
	}
}

// changedFiles renders a clean dim file list (status + path), no --stat graph.
func changedFiles(t Task) string {
	out, err := git(t.RepoPath, "show", "--name-status", "--format=", t.SHA)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) == 2 {
			fmt.Fprintf(&b, "  %s  %s\n", parts[0], parts[1])
		} else {
			fmt.Fprintf(&b, "  %s\n", ln)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// diffLineMeta carries the anchor for one rendered diff row, so a comment picked
// against a visible line knows which file/hunk/line it belongs to.
type diffLineMeta struct {
	File        string
	Anchor      string // the enclosing hunk header
	Line        int    // new-side line number (0 for deletions / non-code rows)
	Text        string // the line content (no gutter), captured as the snippet
	Commentable bool
}

// setDiff rebuilds the diff content and resets scroll. Invalidate tells the
// layer to re-run renderDiffLayer on the next display pass (content changed).
func setDiff() {
	diffLines, diffMeta = buildDiffLines(diffFiles)
	// note: comment mode is owned by openDiffLineComment/pickDiffLine/
	// cancelDiffPick, never reset here — setDiff can run mid-pick (via the
	// OnBeforeRender refresh) and would otherwise clobber the labels.
	if diffLayer != nil {
		diffLayer.ScrollToTop()
		diffLayer.Invalidate()
	}
}

// hunkNewStart parses the new-side start line from a header "@@ -a,b +c,d @@".
func hunkNewStart(header string) int {
	i := strings.Index(header, "+")
	if i < 0 {
		return 0
	}
	rest := header[i+1:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n := 0
	for _, r := range rest[:end] {
		n = n*10 + int(r-'0')
	}
	return n
}

// span builds a styled span with the theme background baked in, so a cell can
// never fall back to the terminal's default colour (the source of bg bleed).
func span(text string, fg Color, bold bool) Span {
	st := Style{FG: fg, BG: cBG}
	if bold {
		st.Attr = AttrBold
	}
	return Span{Text: text, Style: st}
}

// buildDiffLines renders the parsed model as styled rows (a clean per-file
// header, dim hunk context, gutter-marked add/del/context lines) and a parallel
// metadata slice so any row can be anchored back to its file/hunk/line.
func buildDiffLines(files []DiffFile) ([][]Span, []diffLineMeta) {
	if len(files) == 0 {
		return [][]Span{{span("no changes", cSubtle, false)}}, []diffLineMeta{{}}
	}
	var rows [][]Span
	var meta []diffLineMeta
	add := func(text string, c Color, bold bool, m diffLineMeta) {
		rows = append(rows, []Span{span(text, c, bold)})
		meta = append(meta, m)
	}
	for fi, f := range files {
		if fi > 0 {
			rows = append(rows, []Span{}) // blank spacer row (cleared to cBG)
			meta = append(meta, diffLineMeta{})
		}
		sym, c := "~", cBright
		switch f.Status {
		case "new file":
			sym, c = "+", cAdd
		case "deleted":
			sym, c = "-", cDel
		case "renamed":
			sym, c = "»", cBright
		}
		add(sym+"  "+cleanLine(f.Path), c, true, diffLineMeta{})
		for _, hk := range f.Hunks {
			add("  "+cleanLine(hk.Header), cMuted, false, diffLineMeta{})
			cur := hunkNewStart(hk.Header)
			for _, l := range hk.Lines {
				txt := cleanLine(l.Text)
				m := diffLineMeta{File: f.Path, Anchor: hk.Header, Text: txt, Commentable: true}
				switch l.Kind {
				case LineAdd:
					m.Line = cur
					cur++
					add("+ "+txt, cAdd, false, m)
				case LineDel:
					add("- "+txt, cDel, false, m) // del: old-side line, leave Line 0
				default:
					m.Line = cur
					cur++
					add("  "+txt, cSubtle, false, m)
				}
			}
		}
	}
	return rows, meta
}

// renderDiffLayer (re)builds the layer buffer from diffLines. Called by the
// framework only when the viewport width changes or after Invalidate — never
// per-frame. A fresh, exact-size buffer means no stale rows; every cell is
// cleared to cBG and spans carry an explicit BG, so nothing bleeds.
func renderDiffLayer() {
	w := diffLayer.ViewportWidth()
	if w <= 0 {
		return
	}
	h := len(diffLines)
	if vh := diffLayer.ViewportHeight(); h < vh {
		h = vh // pad to viewport so the themed fill covers the whole pane
	}
	clear := Style{Fill: cBG, BG: cBG, FG: cFG}
	buf := NewBuffer(w, h)

	// in pick mode, label visible commentable rows; the label overwrites the
	// 2-col gutter so all code stays visible. Recomputed each render so labels
	// always match what's on screen.
	const labels = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	top, vh, li := diffLayer.ScrollY(), diffLayer.ViewportHeight(), 0
	if diffCommentMode {
		for k := range diffLabelByRow {
			delete(diffLabelByRow, k)
		}
	}

	for y := 0; y < h; y++ {
		buf.ClearLineWithStyle(y, clear)
		if y < len(diffLines) {
			buf.WriteSpans(0, y, diffLines[y], w)
			if diffCommentMode && y >= top && y < top+vh && li < len(labels) &&
				y < len(diffMeta) && diffMeta[y].Commentable {
				r := rune(labels[li])
				li++
				diffLabelByRow[r] = y
				buf.WriteSpans(0, y, []Span{{Text: string(r), Style: Style{FG: cBG, BG: cHunk, Attr: AttrBold}}}, w)
			}
		}
	}
	scrollY := diffLayer.ScrollY()
	diffLayer.SetBuffer(buf)    // resets scrollY to 0…
	diffLayer.ScrollTo(scrollY) // …so restore it (preserves scroll across re-render)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// --- view ------------------------------------------------------------------

func buildMain() Component {
	keybar := "  h/l focus   j/k move   ↵ diff   c comment   S review   a approve   r rework   f filter   q quit"

	return VBox.Fill(cBG).CascadeStyle(&Style{Fill: cBG, BG: cBG, FG: cFG}).PaddingTRBL(1, 0, 0, 3)(
		// global keys (always active)
		On(
			Key("q", uiApp.Stop),
			Key("<Tab>", togglePane),
			Key("h", func() { setPane(paneList) }),
			Key("l", func() { setPane(paneDiff) }),
			Key("f", cycleFilter),
			Key("S", openVerdict),
		),
		HBox.Grow(1).Gap(4)(
			// left — review inbox
			VBox.Grow(2)(
				HBox(
					Text("recap").FG(cBright).Bold(),
					SpaceW(1),
					Text(&countText).FG(cSubtle),
					Space(),
					Text(&filterText).FG(cSubtle),
					SpaceW(1),
				),
				SpaceH(1),
				List(&vmRows).
					Selection(&sel).
					Style(Style{BG: cBG}).
					SelectedStyle(Style{}).
					Marker("  ").
					Render(taskRow),
				// list-focused keys
				If(&pane).Eq(paneList).Then(On(
					Key("j", func() { moveSel(1) }),
					Key("k", func() { moveSel(-1) }),
					Key("<Enter>", func() { setPane(paneDiff) }),
					Key("a", func() { setSel(StatusApproved) }),
					Key("r", func() { setSel(StatusRedo) }),
					Key("u", func() { setSel(StatusPending) }),
					Key("c", openComment),
					Key("v", rerun),
				)),
			),
			// right — detail + diff
			VBox.Grow(3).PaddingTRBL(0, 2, 0, 0)(
				HBox(
					Text(&detailTitle).FG(cBright).Bold(),
					Space(),
					Text(&draftNote).FG(cHunk),
					SpaceW(1),
				),
				SpaceH(1),
				HBox(
					Text(&metaRepo).FG(cSubtle),
					Text("   ·   ").FG(cMuted),
					Text(&metaWhen).FG(cSubtle),
					Text("   ·   ").FG(cMuted),
					Text(&metaCheck).FG(cSubtle),
					Text("   ·   ").FG(cMuted),
					Text(&metaResult).FG(&metaResultColor),
				),
				SpaceH(1),
				LayerView(diffLayer).Grow(1),
				// diff-focused keys
				If(&pane).Eq(paneDiff).Then(On(
					Key("j", diffDown),
					Key("k", diffUp),
					Key("d", diffHalfDown),
					Key("u", diffHalfUp),
					Key("g", diffTop),
					Key("G", diffBottom),
					Key("c", openDiffLineComment),
					Key("<Enter>", func() { setPane(paneList) }),
					Key("<Esc>", func() { setPane(paneList) }),
				)),
			),
		),
		SpaceH(1),
		HBox(Text(&statusMsg).FG(cSubtle), Space(), Text(keybar).FG(cMuted)),
	)
}

func taskRow(r *taskVM) Component {
	itemBG := If(&r.Selected).Then(&curSelBG).Else(&cBG)
	body := VBox.Fill(itemBG).PaddingVH(1, 2)(
		HBox(
			Text(&r.Glyph).FG(r.GlyphColor),
			SpaceW(1),
			HBox.Grow(1)(
				Text(&r.Title).Style(If(&r.Pending).Then(Style{Attr: AttrBold}).Else(Style{})),
			),
			SpaceW(2),
			Text(&r.When).FG(cSubtle),
			SpaceW(1),
		),
		HBox(
			SpaceW(2),
			Text("▌").FG(r.RepoColor),
			SpaceW(1),
			Text(&r.Repo).FG(cSubtle),
		),
	)
	return VBox.Fill(cBG).PaddingTRBL(0, 1, 0, 0)(
		If(&r.HasGroup).Then(
			VBox.Fill(cBG).PaddingTRBL(1, 0, 0, 1)(
				Text(&r.GroupLabel).FG(cMuted).Bold(),
			),
		),
		body,
	)
}

// --- focus & keys ----------------------------------------------------------
//
// Two focusable panes (list, diff). h/l/Tab move focus; within a pane hjkl and
// the actions are contextual, bound via On(Key) in buildMain behind
// If(&pane).Eq(...) — no global ^* shortcuts.

const (
	paneList = "list"
	paneDiff = "diff"
)

var (
	pane     = paneList
	curSelBG = cSelBG
)

func setPane(p string) {
	pane = p
	if p == paneList {
		curSelBG = cSelBG // selection reads bright while the list is focused
	} else {
		curSelBG = cFloat // …and dims while you're in the diff
	}
}

func togglePane() {
	if pane == paneList {
		setPane(paneDiff)
	} else {
		setPane(paneList)
	}
}

func openComment() {
	if len(tasks) > 0 {
		commentText = ""
		uiApp.PushView("comment")
	}
}

// diff scroll is native: adjust the layer's scrollY (clamped internally) and
// the framework re-blits the visible window next frame — no re-render.
func diffDown()     { diffLayer.ScrollDown(1) }
func diffUp()       { diffLayer.ScrollUp(1) }
func diffHalfDown() { diffLayer.HalfPageDown() }
func diffHalfUp()   { diffLayer.HalfPageUp() }
func diffTop()      { diffLayer.ScrollToTop() }
func diffBottom()   { diffLayer.ScrollToEnd() }

func moveSel(d int) {
	sel += d
	if sel >= len(tasks) {
		sel = len(tasks) - 1
	}
	if sel < 0 {
		sel = 0
	}
}

func setSel(status string) {
	if len(tasks) == 0 {
		return
	}
	t := tasks[sel]
	if err := uiStore.SetStatus(t.ID, status); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("#%d → %s", t.ID, status)
	reloadTasks()
}

func cycleFilter() {
	if repoFltr == "" {
		if len(repos) > 0 {
			repoFltr = repos[0]
		}
	} else {
		idx := -1
		for i, rp := range repos {
			if rp == repoFltr {
				idx = i
				break
			}
		}
		if idx < 0 || idx+1 >= len(repos) {
			repoFltr = ""
		} else {
			repoFltr = repos[idx+1]
		}
	}
	sel = 0
	reloadTasks()
}

func rerun() {
	if len(tasks) == 0 {
		return
	}
	t := tasks[sel]
	if strings.TrimSpace(t.CheckCmd) == "" {
		statusMsg = "(no check command)"
		return
	}
	statusMsg = "running: " + t.CheckCmd + " …"
	uiApp.RenderNow()
	cmd := exec.Command("sh", "-c", t.CheckCmd)
	if t.RepoPath != "" {
		cmd.Dir = t.RepoPath
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		statusMsg = "✓ PASS  " + t.CheckCmd
	} else {
		statusMsg = "✗ FAIL  " + t.CheckCmd + "  — " + lastLine(string(out))
	}
}

// --- comment prompt --------------------------------------------------------

func setupCommentView() {
	save := func() {
		body := strings.TrimSpace(commentText)
		commentText = ""
		uiApp.PopView()
		if body != "" && len(tasks) > 0 {
			if _, err := uiStore.AddComment(tasks[sel].ID, "you", body); err != nil {
				statusMsg = "error: " + err.Error()
			} else {
				statusMsg = fmt.Sprintf("commented on #%d", tasks[sel].ID)
			}
		}
	}
	cancel := func() { commentText = ""; uiApp.PopView() }
	uiApp.View("comment",
		VBox.Fill(cBG)(
			promptKeys(save, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("comment").FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				HBox(Text("> ").FG(cSubtle), Text(&commentText).FG(cBright)),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("comment")
}

// --- review UI (line comments + submit) ------------------------------------

func backspaceComment() {
	if len(commentText) > 0 {
		rs := []rune(commentText)
		commentText = string(rs[:len(rs)-1])
	}
}

// promptKeys is the standard text-prompt binding scope: enter/esc/backspace/
// space. Embed it in a prompt view's tree via On(Key(...)). Printable typing is
// wired separately with wireTyping (the router catch-all).
func promptKeys(save, cancel func()) OnC {
	return On(
		Key("<CR>", save),
		Key("<Esc>", cancel),
		Key("<BS>", backspaceComment),
		Key("<Space>", func() { commentText += " " }),
	)
}

// wireTyping routes printable keystrokes into commentText for a prompt view
// (the catch-all path; there is no On(Key) form for "any rune").
func wireTyping(view string) {
	if r, ok := uiApp.ViewRouter(view); ok {
		r.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				commentText += string(k.Rune)
				uiApp.RequestRender()
				return true
			}
			return false
		})
	}
}

// openDiffLineComment enters "pick a line" mode: renderDiffLayer labels the
// visible commentable rows and the diffpick view captures the choice.
func openDiffLineComment() {
	if len(tasks) == 0 {
		return
	}
	has := false
	for _, m := range diffMeta {
		if m.Commentable {
			has = true
			break
		}
	}
	if !has {
		statusMsg = "(no diff lines to comment on)"
		return
	}
	diffCommentMode = true
	diffLayer.Invalidate()
	uiApp.PushView("diffpick")
}

func cancelDiffPick() {
	diffCommentMode = false
	diffLayer.Invalidate()
	uiApp.PopView()
}

// pickDiffLine resolves a label to its row, captures the anchor, and opens the
// body prompt.
func pickDiffLine(r rune) {
	row, ok := diffLabelByRow[r]
	if !ok || row < 0 || row >= len(diffMeta) || !diffMeta[row].Commentable {
		return
	}
	m := diffMeta[row]
	pcFile, pcAnchor, pcSnippet, pcLine = m.File, m.Anchor, m.Text, m.Line
	pcLocation = fmt.Sprintf("%s · line %d", m.File, m.Line)
	pcSnippetView = "  " + m.Text
	if len(pcSnippetView) > 68 {
		pcSnippetView = pcSnippetView[:67] + "…"
	}
	diffCommentMode = false
	diffLayer.Invalidate()
	uiApp.PopView() // leave diffpick
	commentText = ""
	uiApp.PushView("linecomment")
}

func saveLineComment() {
	body := strings.TrimSpace(commentText)
	commentText = ""
	uiApp.PopView()
	if body == "" || len(tasks) == 0 {
		return
	}
	if _, err := uiStore.AddReviewComment(tasks[sel].ID, "you", body, pcFile, pcLine, pcAnchor, pcSnippet); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("commented on %s:%d", pcFile, pcLine)
	detailDirty = true
}

func openVerdict() {
	if len(tasks) == 0 {
		return
	}
	uiApp.PushView("verdict")
}

func chooseVerdict(v string) {
	reviewVerdict = v
	verdictLabel = strings.ToUpper(strings.ReplaceAll(v, "_", " "))
	commentText = ""
	uiApp.PopView() // leave verdict picker
	uiApp.PushView("reviewsummary")
}

func saveReviewSummary() {
	summary := strings.TrimSpace(commentText)
	commentText = ""
	uiApp.PopView()
	if len(tasks) == 0 {
		return
	}
	rv, res, err := submitReview(uiStore, tasks[sel].ID, reviewVerdict, summary)
	if err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	msg := fmt.Sprintf("review #%d submitted [%s]", rv.ID, rv.Verdict)
	if res.line != "" {
		if res.wrote {
			msg += " · queued in TODO"
		} else {
			msg += " · set todo_template to auto-queue"
		}
	}
	statusMsg = msg
	reloadTasks()
}

func setupReviewViews() {
	// pick-a-line view — labels are drawn into the shared diff layer by
	// renderDiffLayer while diffCommentMode is set. A single catch-all maps the
	// typed label rune to its row (cleaner than 52 individual bindings).
	uiApp.View("diffpick",
		VBox.Fill(cBG).CascadeStyle(&Style{Fill: cBG, BG: cBG, FG: cFG}).PaddingTRBL(1, 0, 0, 3)(
			On(Key("<Esc>", cancelDiffPick)),
			HBox(Text("comment").FG(cBright).Bold(), SpaceW(1), Text("pick a line · esc cancel").FG(cMuted)),
			SpaceH(1),
			LayerView(diffLayer).Grow(1),
		),
	).NoCounts()
	if r, ok := uiApp.ViewRouter("diffpick"); ok {
		r.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				pickDiffLine(k.Rune)
				return true
			}
			return false
		})
	}

	// line-comment body prompt
	cancel := func() { commentText = ""; uiApp.PopView() }
	uiApp.View("linecomment",
		VBox.Fill(cBG)(
			promptKeys(saveLineComment, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("line comment").FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				Text(&pcLocation).FG(cSubtle),
				Text(&pcSnippetView).FG(cMuted),
				SpaceH(1),
				HBox(Text("> ").FG(cSubtle), Text(&commentText).FG(cBright)),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("linecomment")

	// verdict picker
	uiApp.View("verdict",
		VBox.Fill(cBG)(
			On(
				Key("r", func() { chooseVerdict(VerdictRequestChanges) }),
				Key("a", func() { chooseVerdict(VerdictApprove) }),
				Key("c", func() { chooseVerdict(VerdictComment) }),
				Key("<Esc>", func() { uiApp.PopView() }),
			),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(60)(
				Text("submit review").FG(cBright).Bold(),
				SpaceH(1),
				Text("r   request changes   → rework + TODO").FG(cFG),
				Text("a   approve").FG(cFG),
				Text("c   comment           note, no status change").FG(cFG),
				SpaceH(1),
				Text("esc cancel").FG(cMuted),
			), Space()),
			Space(),
		),
	).NoCounts()

	// review summary prompt
	uiApp.View("reviewsummary",
		VBox.Fill(cBG)(
			promptKeys(saveReviewSummary, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("summary").FG(cBright).Bold(), Space(), Text(&verdictLabel).FG(cSubtle)),
				SpaceH(1),
				HBox(Text("> ").FG(cSubtle), Text(&commentText).FG(cBright)),
				SpaceH(1),
				Text("enter submit · esc cancel").FG(cMuted),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("reviewsummary")
}

// --- helpers ---------------------------------------------------------------

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return ""
}
