package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/notify"
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

// mail-inspired warm-dark palette (borderless, fill + whitespace). The shade
// hierarchy mirrors mail: app/diff sit on cBG, the side columns claim their area
// with the darker cPaneBG, and rows step up cGroupBG → cSelBG.
var (
	cBG        = Hex(0x1c1c1c) // app + diff background
	cPaneBG    = Hex(0x191918) // side-column fill (mail's ThreadBG) — darkest
	cBright    = Hex(0xe8e6e3)
	cFG        = Hex(0xb8b5b0)
	cSubtle    = Hex(0x8b8780)
	cMuted     = Hex(0x3f3c38)
	cSelBG     = Hex(0x302f2c) // selected row
	cGroupBG   = Hex(0x252421) // grouped row / float panels
	cFloat     = Hex(0x252421)
	cAdd       = Hex(0x8aa872) // diff +, muted green
	cDel       = Hex(0xc08a72) // diff -, muted terracotta
	cHunk      = Hex(0x6f8fa8) // @@ hunk, muted blue
	cCommentBG = Hex(0x23282e) // faint blue wash on a commented diff line
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
	ID           int64
	IDText       string // "#6", dim, for cross-referencing with CLI / chat
	Title        string
	When         string
	Repo         string
	Glyph        string
	GlyphColor   Color
	RepoColor    Color
	Pending      bool
	HasDraft     bool   // unsubmitted feedback in progress → row pill
	DraftPill    string // e.g. "✎ 2"
	ReReview     bool   // fix-forward task awaiting re-review
	ReReviewPill string // e.g. "↩ #9"
	Selected     bool
	HasGroup     bool
	GroupLabel   string

	// revision threading (mail-style): a task with >1 diff is expandable with `o`.
	// A header row (RevIdx < 0) shows the latest diff by default; expanding splices
	// one child row per revision (RevIdx >= 0) beneath it, each loading its own diff.
	RevIdx     int    // -1 = task header row; >=0 = revision child (index into Revisions, latest first)
	DiffSHA    string // the commit this row's diff shows (header = latest revision)
	Expanded   bool   // header only: its revisions are spliced in below
	Grouped    bool   // child only: drives the indented, distinct-bg rendering
	ExpandPill string // header only: revision-count cue, e.g. "▸ 3" / "▾ 3"
	RevLabel   string // child only: e.g. "rev 2 · a1b2c3 · added line two"
}

// draftCommentVM is one row in the draft-review overview pane: the location
// line (file:line), the captured snippet, and the reviewer's note. File/Line
// keep the raw anchor so selecting a row can scroll the diff to it.
type draftCommentVM struct {
	ID       int64  // comment id, for edit/delete
	Location string // "file · line N" or "general"
	Snippet  string // the diff line commented on (may be empty)
	Body     string
	File     string
	Line     int
	Draft    bool // on the open draft (editable); else submitted (read-only)
	Selected bool // updated each frame like the inbox rows, drives the fill
}

var (
	uiStore *Store
	uiApp   *App
	omni    *OmniBox

	tasks    []Task
	vmRows   []taskVM // flattened: task headers + (when expanded) their revision children
	sel      int      // index into vmRows, NOT tasks (a row may be a revision child)
	repoFltr string
	repos    []string

	// expandedTasks tracks which tasks are expanded into their revision children
	// (mail's thread-expand). Keyed by task id so it survives vmRows rebuilds.
	expandedTasks = map[int64]bool{}
	// taskByID resolves the selected row's task without re-querying (rebuilt each
	// reloadTasks). Rows carry only a task ID; this maps back to the full Task.
	taskByID = map[int64]Task{}

	// diff pane: a native-scroll Layer. diffLines is the full styled content
	// (every span carries BG so cells never fall back to terminal default);
	// renderDiffLayer builds the buffer on content/size change, then the
	// framework blits the visible window each frame — scroll is free.
	diffLayer  *Layer
	diffLines  [][]Span
	diffMeta   []diffLineMeta // parallel to diffLines: anchor info per row
	diffBanner [][]Span       // optional context rows prepended to the diff

	// line-comment "pick a line" mode: renderDiffLayer draws label chars in the
	// gutter of visible commentable rows; diffLabelByRow maps label → row.
	// pickMode mirrors diffCommentMode as a string so view conditionals (If.Eq)
	// can gate key scopes on it ("on"/"off"); always set both via setPickMode.
	diffCommentMode bool
	pickMode        = "off"
	diffLabelByRow  = map[rune]int{}

	// commentedLines marks diff rows that already carry a draft comment, keyed by
	// "file:line", so renderDiffLayer can draw a visual cue in the gutter.
	commentedLines = map[string]bool{}

	// diffFocused mirrors pane=="diff" as a 0/1 opacity target so the diff
	// scrollbar fades in only when the diff column has focus (mail's cue).
	diffFocused = 0.0

	helpOpen bool // ? cheatsheet overlay

	// the anchor of the line currently being commented on (set when picked).
	pcFile, pcAnchor, pcSnippet string
	pcLine                      int

	// display strings for the line-comment prompt
	pcLocation    string
	pcSnippetView string

	// comment view/edit: which draft comment is open, and its display strings.
	editingCommentID int64
	cvLocation       string
	cvSnippet        string
	cvBodyLines      []string // body wrapped to the modal width

	draftNote string // e.g. "✎ 2 draft" when the current task has draft comments

	// draft review pane (conditional): shows the selected task's accumulated
	// draft comments in one place, like a PR's conversation overview. Only
	// rendered when hasDraft is true, so it costs no width otherwise.
	hasDraft      bool
	draftComments []draftCommentVM

	countText, filterText string
	detailTitle           string
	metaRepo, metaWhen    string
	metaResult            string
	metaResultColor       = cSubtle
	filesText             string
	diffFiles             []DiffFile
	statusMsg             string
	commentText           string
	commentLines          []string // commentText wrapped for the prompt display

	lastSel, lastLen int
	lastFltr         string
	detailDirty      bool

	// set by the SIGUSR1 handler; consumed on the render thread to reload the
	// inbox when another process (e.g. `recap add`) changes the db.
	reloadRequested atomic.Bool
)

func runUI() error {
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	uiStore = st

	uiApp = NewApp()

	// load the persisted theme and set the colour vars BEFORE any view is built,
	// so the compiled templates bake the right palette.
	initTheme()

	diffLayer = NewLayer()
	diffLayer.Render = renderDiffLayer

	omni = newOmniBox(uiApp, omniCommands())

	reloadTasks()
	setupCommentView()
	setupReviewViews()
	setupTodoView()

	// live refresh: register this TUI so `recap add` can SIGUSR1 us to reload the
	// inbox without a restart. The handler only flags + requests a render; the
	// actual reload runs on the render thread (refreshDetail) to avoid races.
	cleanup := notify.Register()
	defer cleanup()
	sigReload := make(chan os.Signal, 1)
	signal.Notify(sigReload, syscall.SIGUSR1)
	defer signal.Stop(sigReload)
	go func() {
		for range sigReload {
			reloadRequested.Store(true)
			uiApp.RequestRender()
		}
	}()

	// SetView once — glyph re-layouts the template against the new terminal size
	// every frame, so no SetView-on-resize is needed (and rebuilding the tree on
	// resize would discard the diff layer's scroll state). The diff layer itself
	// re-renders on width change via renderDiffLayer/NeedsRender.
	uiApp.SetView(buildMain())
	uiApp.OnBeforeRender(refreshDetail)
	uiApp.Router().NoCounts()
	// while picking a diff line, label letters overlap many bound keys, so the
	// view scopes are suppressed (see buildMain) and the labels are caught here
	// at the root, taking priority over nothing else once those scopes are off.
	if r := uiApp.Router(); r != nil {
		r.HandleUnmatched(func(k riffkey.Key) bool {
			if diffCommentMode && k.Rune != 0 && k.Mod == 0 {
				pickDiffLine(k.Rune)
				uiApp.RequestRender()
				return true
			}
			return false
		})
	}
	return uiApp.Run()
}

// --- data ------------------------------------------------------------------

// derived-state ordering + labels (pending → rework → approved).
func statePriority(s string) int {
	switch s {
	case StatePending:
		return 0
	case StateRework:
		return 1
	default:
		return 2
	}
}

func stateLabel(s string) string {
	switch s {
	case StatePending:
		return "INBOX"
	case StateRework:
		return "AMENDS"
	default:
		return "DONE"
	}
}

func stateGlyph(s string) string {
	switch s {
	case StateRework:
		return "↻"
	case StateDone:
		return "✓"
	default:
		return "●"
	}
}

func stateColor(s string) Color {
	switch s {
	case StateDone:
		return cSubtle
	case StateRework:
		return cDel
	default:
		return cBright
	}
}

func reloadTasks() {
	tasks, _ = uiStore.List("", repoFltr)
	// derived state per task (computed from reviews, never a stale flag).
	state := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		state[t.ID] = uiStore.ReviewState(t.ID)
	}
	// sections: inbox, then amends, then done. Within inbox, oldest-first (work
	// the queue front-to-back); amends/done newest-first (most recent activity).
	sort.SliceStable(tasks, func(i, j int) bool {
		si, sj := state[tasks[i].ID], state[tasks[j].ID]
		pi, pj := statePriority(si), statePriority(sj)
		if pi != pj {
			return pi < pj
		}
		if si == StatePending {
			return tasks[i].ID < tasks[j].ID // oldest first in the inbox
		}
		return tasks[i].ID > tasks[j].ID
	})
	taskByID = make(map[int64]Task, len(tasks))
	for _, t := range tasks {
		taskByID[t.ID] = t
	}

	vmRows = vmRows[:0]
	prev := ""
	for _, t := range tasks {
		st := state[t.ID]
		vm := taskVM{
			ID:         t.ID,
			IDText:     fmt.Sprintf("#%d", t.ID),
			Title:      t.Title,
			Repo:       t.Repo,
			Glyph:      stateGlyph(st),
			GlyphColor: stateColor(st),
			RepoColor:  repoColor(t.Repo),
			Pending:    st == StatePending,
			RevIdx:     -1, // task header row
		}
		if len(t.CreatedAt) >= 16 {
			vm.When = t.CreatedAt[11:16]
		}
		// unsubmitted draft feedback → a pill on the row (doesn't affect state).
		if _, n, ok := uiStore.DraftInfo(t.ID); ok && n > 0 {
			vm.HasDraft = true
			vm.DraftPill = fmt.Sprintf("✎ %d", n)
		}
		// a fix-forward task still awaiting review is a re-review: flag it so
		// resubmissions stand out from net-new inbox items.
		if t.ParentID != 0 && st == StatePending {
			vm.ReReview = true
			vm.ReReviewPill = fmt.Sprintf("↩ #%d", t.ParentID)
		}
		// revision history: the header shows the latest diff by default; a task with
		// more than one diff is expandable (`o`) into one child row per revision.
		revs, _ := uiStore.Revisions(t.ID)
		if len(revs) > 0 {
			vm.DiffSHA = revs[len(revs)-1].SHA // latest
		}
		if len(revs) > 1 {
			vm.Expanded = expandedTasks[t.ID]
			if vm.Expanded {
				vm.ExpandPill = fmt.Sprintf("▾ %d", len(revs))
			} else {
				vm.ExpandPill = fmt.Sprintf("▸ %d", len(revs))
			}
		}
		if st != prev {
			vm.HasGroup = true
			vm.GroupLabel = stateLabel(st)
			prev = st
		}
		vmRows = append(vmRows, vm)

		// expanded → splice a child row per revision, latest first (mail's order).
		if vm.Expanded {
			for j := len(revs) - 1; j >= 0; j-- {
				r := revs[j]
				vmRows = append(vmRows, taskVM{
					ID:       t.ID,
					RevIdx:   j,
					DiffSHA:  r.SHA,
					Grouped:  true,
					RevLabel: revLabel(j, r),
				})
			}
		}
	}
	if sel >= len(vmRows) {
		sel = len(vmRows) - 1
	}
	if sel < 0 {
		sel = 0
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

// revLabel is a revision child row's caption: "original" for the base diff (the
// task's own commit), "rev N" for each fix-forward, plus the short sha and the
// revision summary when present.
func revLabel(idx int, r Revision) string {
	head := fmt.Sprintf("rev %d", idx)
	if r.Base {
		head = "original"
	}
	sha := r.SHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	if r.Summary != "" {
		return fmt.Sprintf("%s · %s · %s", head, sha, r.Summary)
	}
	if sha != "" {
		return fmt.Sprintf("%s · %s", head, sha)
	}
	return head
}

// selectedRow returns the currently selected flattened row (header or revision
// child), or nil when the list is empty.
func selectedRow() *taskVM {
	if sel < 0 || sel >= len(vmRows) {
		return nil
	}
	return &vmRows[sel]
}

// selectedTask resolves the task behind the selected row — the same task whether a
// header or one of its revision children is selected. Actions operate on this.
func selectedTask() (Task, bool) {
	r := selectedRow()
	if r == nil {
		return Task{}, false
	}
	t, ok := taskByID[r.ID]
	return t, ok
}

// toggleExpand expands/collapses the selected row's task into its revisions, then
// parks the selection on that task's header so navigation stays oriented.
func toggleExpand() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	revs, _ := uiStore.Revisions(t.ID)
	if len(revs) < 2 {
		return // nothing to expand — only the base diff
	}
	expandedTasks[t.ID] = !expandedTasks[t.ID]
	reloadTasks()
	for i := range vmRows {
		if vmRows[i].ID == t.ID && vmRows[i].RevIdx < 0 {
			sel = i
			break
		}
	}
	detailDirty = true
}

// refreshDetail updates selection fill + the right-hand detail when selection,
// filter, or task set changes — never per-frame git calls.
func refreshDetail() {
	// a SIGUSR1 from another process (e.g. `recap add`) requests an inbox reload;
	// do it here on the render thread, then force the detail to rebuild.
	if reloadRequested.CompareAndSwap(true, false) {
		reloadTasks()
		detailDirty = true
	}
	for i := range vmRows {
		vmRows[i].Selected = i == sel
	}
	for i := range draftComments {
		draftComments[i].Selected = i == draftSel
	}
	// focus-aware selection bands: the active column's selected row reads bright,
	// the others dim. Painted per-row (in taskRow/draftRow) so the band covers
	// only the row body, never a group header sharing the same list item.
	curSelBG = cFloat
	draftSelBG = cFloat
	switch pane {
	case paneList:
		curSelBG = cSelBG
	case paneDraft:
		draftSelBG = cSelBG
	}
	// fade the diff scrollbar in only while the diff column has focus
	if pane == paneDiff {
		diffFocused = 1.0
	} else {
		diffFocused = 0.0
	}
	if draftSel != lastDraftSel {
		lastDraftSel = draftSel
		syncDiffToDraft()
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

	row := selectedRow()
	t, ok := selectedTask()
	if !ok || row == nil {
		detailTitle, metaRepo, metaWhen, metaResult = "no tasks", "", "", ""
		filesText, diffFiles, draftNote = "", nil, ""
		hasDraft, draftComments = false, nil
		setDiff()
		return
	}
	detailTitle = t.Title
	if row.RevIdx >= 0 { // a revision child: title it so the diff in view is clear
		detailTitle = t.Title + "  ·  " + row.RevLabel
	}
	metaRepo, metaWhen = t.Repo, t.CreatedAt
	metaResult = dash(t.Result)
	metaResultColor = resultColor(t.Result)
	loadDraftPane(t.ID)
	// the review-context banner (amends summary / re-review header) sits above the
	// task's own diff, which still shows for context.
	diffBanner = buildBanner(t)

	// the diff shown follows the selected row: a header shows the latest revision,
	// a revision child shows its own commit.
	sha := row.DiffSHA
	if sha == "" || t.RepoPath == "" {
		filesText, diffFiles = "no diff — task has no sha", nil
		setDiff()
		return
	}
	filesText = changedFiles(t.RepoPath, sha)
	full, err := git(t.RepoPath, "show", "--format=", sha)
	if err != nil {
		diffFiles = nil
	} else {
		diffFiles = parseUnifiedDiff(full)
	}
	setDiff()
}

// buildBanner produces the context rows shown above the diff:
//   - AMENDS task (latest submitted review is request_changes): the review I
//     submitted — summary + anchored comments (what I asked for).
//   - fix-forward task (has a parent) awaiting re-review: a "↩ amends review #N"
//     header + that review's summary, above the new commit's diff.
//   - ordinary inbox/done task: the agent-written reviewer briefing (task.Summary),
//     the contextual "what I did + why + what to watch" — richer than the commit.
//
// Returns nil when there's no context to show.
func buildBanner(t Task) [][]Span {
	if uiStore.ReviewState(t.ID) == StateRework {
		// the active request_changes review on this task.
		for _, rv := range latestSubmitted(t.ID) {
			return reviewBanner("changes requested", rv, true)
		}
	}
	if t.ParentID != 0 {
		// re-review: show BOTH why it's back (the parent's review) and what I
		// changed about it (this fix task's summary), so the reviewer doesn't have
		// to reverse-engineer the fix.
		var rows [][]Span
		for _, rv := range latestSubmitted(t.ParentID) {
			// withComments=true: show the original line comments so the reviewer can
			// recontextualise what they asked for when re-reviewing the fix.
			rows = append(rows, reviewBanner(fmt.Sprintf("↩ amends review #%d", rv.ID), rv, true)...)
		}
		if strings.TrimSpace(t.Summary) != "" {
			rows = append(rows, summaryBanner("what changed", t.Summary)...)
		}
		if len(rows) > 0 {
			return rows
		}
	}
	// plain inbox/done item: lead with the reviewer briefing if there is one.
	if strings.TrimSpace(t.Summary) != "" {
		return summaryBanner("summary", t.Summary)
	}
	return nil
}

// summaryBanner renders an agent-written summary as wrapped banner rows under a
// titled header.
func summaryBanner(title, summary string) [][]Span {
	var rows [][]Span
	rows = append(rows, []Span{span(title, cHunk, true)})
	for _, line := range wrapText(summary, 72) {
		rows = append(rows, []Span{span("  "+line, cFG, false)})
	}
	rows = append(rows, []Span{}) // blank separator before the diff
	return rows
}

// latestSubmitted returns the newest submitted/resolved review for a task as a
// 0-or-1 slice (so callers can range without a nil check).
func latestSubmitted(taskID int64) []Review {
	revs, _ := uiStore.Reviews(taskID)
	for i := len(revs) - 1; i >= 0; i-- {
		if revs[i].State == ReviewSubmitted || revs[i].State == ReviewResolved {
			return revs[i : i+1]
		}
	}
	return nil
}

// reviewBanner renders a review's summary (+ optional anchored comments) as
// banner rows. withComments lists the line comments (used for the AMENDS view).
func reviewBanner(title string, rv Review, withComments bool) [][]Span {
	var rows [][]Span
	add := func(s ...Span) { rows = append(rows, s) }
	add(span(title, cHunk, true))
	if rv.Summary != "" {
		add(span("  "+cleanLine(rv.Summary), cFG, false))
	}
	if withComments {
		cs, _ := uiStore.ReviewComments(rv.ID)
		for _, c := range cs {
			loc := "general"
			if c.File != "" {
				loc = c.File
				if c.Line > 0 {
					loc = fmt.Sprintf("%s:%d", c.File, c.Line)
				}
			}
			add(span("  · "+loc, cSubtle, false))
			add(span("    "+cleanLine(c.Body), cFG, false))
		}
	}
	rows = append(rows, []Span{}) // blank separator before the diff (or end)
	return rows
}

// loadDraftPane refreshes the draft-review overview for a task: the ✎ N hint and
// the conditional pane's comment rows, sourced from the open draft review.
func loadDraftPane(taskID int64) {
	draftComments = nil
	for k := range commentedLines {
		delete(commentedLines, k)
	}
	// show ALL review comments on the task — not just the open draft — so feedback
	// stays visible after submit. Each row knows if it's still an editable draft.
	cs, _ := uiStore.TaskReviewComments(taskID)
	if len(cs) == 0 {
		hasDraft, draftNote = false, ""
		return
	}
	hasDraft = true
	drafts := 0
	for _, c := range cs {
		if c.Draft {
			drafts++
		}
		vm := draftCommentVM{ID: c.ID, Body: c.Body, File: c.File, Line: c.Line, Draft: c.Draft}
		if c.File != "" {
			vm.Location = c.File
			if c.Line > 0 {
				vm.Location += fmt.Sprintf(" · line %d", c.Line)
			}
			commentedLines[lineKey(c.File, c.Line)] = true
		} else {
			vm.Location = "general"
		}
		if c.Snippet != "" {
			vm.Snippet = cleanLine(c.Snippet)
		}
		draftComments = append(draftComments, vm)
	}
	// header reflects draft-in-progress vs settled comments.
	if drafts > 0 {
		draftNote = fmt.Sprintf("✎ %d draft", drafts)
	} else {
		draftNote = fmt.Sprintf("%d comment%s", len(cs), plural(len(cs)))
	}
	if draftSel >= len(draftComments) {
		draftSel = len(draftComments) - 1
	}
	if draftSel < 0 {
		draftSel = 0
	}
	// general (top-level) comments first, then anchored ones grouped by file:line.
	sort.SliceStable(draftComments, func(i, j int) bool {
		a, b := draftComments[i], draftComments[j]
		if (a.File == "") != (b.File == "") {
			return a.File == "" // general (unanchored) before anchored
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func lineKey(file string, line int) string { return fmt.Sprintf("%s:%d", file, line) }

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
func changedFiles(repoPath, sha string) string {
	out, err := git(repoPath, "show", "--name-status", "--format=", sha)
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
	lines, meta := buildDiffLines(diffFiles)
	// prepend the context banner (amends summary / re-review header) if any;
	// banner rows carry empty meta so they're never commentable / labelled.
	if len(diffBanner) > 0 {
		bMeta := make([]diffLineMeta, len(diffBanner))
		diffLines = append(append([][]Span{}, diffBanner...), lines...)
		diffMeta = append(append([]diffLineMeta{}, bMeta...), meta...)
	} else {
		diffLines, diffMeta = lines, meta
	}
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
	// leave a small right margin so code never butts against the scrollbar edge.
	textW := w - 2
	if textW < 1 {
		textW = w
	}

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
		if y >= len(diffLines) {
			continue
		}
		buf.WriteSpans(0, y, diffLines[y], textW)
		switch {
		case diffCommentMode && y >= top && y < top+vh && li < len(labels) &&
			y < len(diffMeta) && diffMeta[y].Commentable:
			// pick mode: overlay the selection label in the gutter
			r := rune(labels[li])
			li++
			diffLabelByRow[r] = y
			buf.WriteSpans(0, y, []Span{{Text: string(r), Style: Style{FG: cBG, BG: cHunk, Attr: AttrBold}}}, w)
		case !diffCommentMode && y < len(diffMeta) && diffMeta[y].Commentable &&
			commentedLines[lineKey(diffMeta[y].File, diffMeta[y].Line)]:
			// normal: a bright filled gutter block marks a commented line, with a
			// faint tinted wash across the row so it's easy to spot at a glance.
			for cx := 0; cx < w; cx++ {
				cell := buf.Get(cx, y)
				cell.Style.BG = cCommentBG
				buf.Set(cx, y, cell)
			}
			buf.WriteSpans(0, y, []Span{{Text: "█", Style: Style{FG: cHunk, BG: cCommentBG, Attr: AttrBold}}}, w)
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
	return VBox.Fill(cBG).CascadeStyle(&Style{Fill: cBG, BG: cBG, FG: cFG})(
		// global keys — suppressed while picking a diff line, so the label
		// letters (which overlap a/c/d/g/…) route to the in-place picker instead.
		If(&pickMode).Eq("off").Then(On(
			Key("q", uiApp.Stop),
			Key("?", toggleHelp),
			Key("<Space>", func() { omni.Open() }),
			Key("<C-p>", func() { omni.Open() }),
			Key("<Tab>", togglePane),
			Key("h", focusPrev),
			Key("l", focusNext),
			Key("f", cycleFilter),
			Key("S", submitSelected),
			Key("U", unsubmitSelected),
			Key("t", openTodoEditor),
		)),
		// pick-a-line scope: Esc cancels. The label letters themselves are caught
		// by the main router's unmatched handler (see runUI) since they overlap
		// many bound keys and must take priority while picking.
		If(&pickMode).Eq("on").Then(On(
			Key("<Esc>", cancelDiffPick),
		)),
		HBox.Grow(1).Gap(4)(
			// left — review inbox (darker column fill claims the area)
			VBox.Grow(2).Fill(cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0)(
				HBox(
					SpaceW(3),
					Text("recap").FG(cBright).Bold(),
					SpaceW(1),
					Text(&countText).FG(cSubtle),
					Space(),
					Text(&filterText).FG(cSubtle),
					SpaceW(2),
				),
				SpaceH(2),
				List(&vmRows).
					Selection(&sel).
					Style(&listBaseStyle).
					SelectedStyle(Style{}). // band painted per-row, excludes group headers
					Marker("  ").           // blank gutter: Marker("") falls back to the default "> "
					Render(taskRow),
				// list-focused keys. status is review-derived, so there are no direct
				// redo/pending flips — rework happens only via S → request_changes.
				// a = quick-approve (submits an approve review).
				If(&pane).Eq(paneList).Then(On(
					Key("j", func() { moveSel(1) }),
					Key("k", func() { moveSel(-1) }),
					Key("<Enter>", func() { setPane(paneDiff) }),
					Key("a", approveSelected),
					Key("c", openComment),
					Key("v", rerun),
					Key("o", toggleExpand), // expand a task into its revision diffs
				)),
			),
			// middle — detail + diff (no side padding; scrollbar flush right).
			// SpaceH(1) drops the title to row 1 to line up with the left/right
			// column headers: those get their top row from .Fill()+PaddingTRBL, but
			// this unfilled column's top padding collapses, so the title rode at row 0.
			VBox.Grow(3).PaddingTRBL(1, 0, 0, 0)(
				SpaceH(1),
				HBox(
					Text(&detailTitle).FG(cBright).Bold(),
					SpaceW(2),
				),
				SpaceH(1),
				HBox(
					Text(&metaRepo).FG(cSubtle),
					Text("  ·  ").FG(cMuted),
					Text(&metaWhen).FG(cSubtle),
					Text("  ·  ").FG(cMuted),
					Text(&metaResult).FG(&metaResultColor),
				),
				SpaceH(2),
				// diff + a flush-right scrollbar. No Length: a vertical scrollbar
				// with height 0 is auto-stretched to fill the row, so it tracks the
				// full column height (same structure ScrollView builds internally).
				// It fades in only while the diff column has focus (mail's cue).
				HBox.Grow(1)(
					LayerView(diffLayer).Grow(1),
					ScrollbarForLayer(diffLayer).
						TrackStyle(Style{FG: cMuted, BG: cBG}).
						ThumbStyle(Style{FG: cSubtle, BG: cBG}).
						Opacity(Animate(&diffFocused)),
				),
				// diff-focused keys (suppressed during pick mode so label letters
				// like c/j/k/d/g aren't swallowed before the picker sees them).
				If(&pickMode).Eq("off").Then(If(&pane).Eq(paneDiff).Then(On(
					Key("j", diffDown),
					Key("k", diffUp),
					Key("d", diffHalfDown),
					Key("u", diffHalfUp),
					Key("g", diffTop),
					Key("G", diffBottom),
					Key("c", openDiffLineComment),
					Key("e", openInEditor), // open this file:line in $EDITOR
					Key("<Enter>", func() { setPane(paneList) }),
					Key("<Esc>", func() { setPane(paneList) }),
				))),
			),
			// right — comments overview (shown whenever the task has any comments)
			If(&hasDraft).Then(
				VBox.Grow(2).Fill(cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0)(
					HBox(SpaceW(3), Text("comments").FG(cBright).Bold(), Space(), Text(&draftNote).FG(cSubtle), SpaceW(2)),
					SpaceH(2),
					List(&draftComments).
						Selection(&draftSel).
						Style(&listBaseStyle).
						SelectedStyle(Style{}). // band painted per-row
						Marker("  ").           // blank gutter: Marker("") falls back to the default "> "
						Render(draftRow),
					If(&pane).Eq(paneDraft).Then(On(
						Key("j", func() { moveDraft(1) }),
						Key("k", func() { moveDraft(-1) }),
						Key("<Enter>", openCommentView),
						Key("e", editDraftComment),
						Key("d", deleteDraftComment),
						Key("O", openDraftLinks), // open [[file]] refs (e.g. screenshots)
						Key("<Esc>", func() { setPane(paneList) }),
					)),
				),
			),
		),
		// transient status (errors/confirmations) only — no permanent keybar
		If(&statusMsg).Then(HBox(SpaceW(3), Text(&statusMsg).FG(cSubtle))),
		// keyboard help overlay, toggled with ? (modal scope captures esc/?)
		If(&helpOpen).Then(helpOverlay()),
		// command palette overlay, opened with <C-p> / <Space>
		omni.View(),
	)
}

// help cheatsheet rows, split into two columns (mail's layout).
type helpRow struct{ Key, Desc string }

var helpNavRows = []helpRow{
	{"j / k", "move"},
	{"h / l", "focus column"},
	{"tab", "next pane"},
	{"↵", "open"},
	{"f", "filter repo"},
	{"space / ^p", "commands"},
}

var helpActionRows = []helpRow{
	{"o", "expand revisions"},
	{"t", "edit TODO"},
	{"e", "open in $EDITOR"},
	{"c", "comment"},
	{"e / d", "edit / delete"},
	{"O", "open [[file]] link"},
	{"a", "approve"},
	{"S", "submit (amends)"},
	{"U", "unsubmit → inbox"},
	{"?", "help"},
	{"q", "quit"},
}

var helpRef NodeRef

// helpOverlay is the ? cheatsheet — centred, two-column, mail's dimensions and
// screen-effect treatment (animated dodged vignette + focused drop shadow).
func helpOverlay() Component {
	return Overlay.Centered()(
		VBox.Width(56).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).
			PaddingVH(1, 2).NodeRef(&helpRef).
			Opacity(In(Animate(1.0)).Out(Animate(0))).
			Gap(1)(
			On.Modal(
				Key("?", toggleHelp),
				Key("<Esc>", toggleHelp),
				Key("q", toggleHelp),
			),
			Text("keyboard").FG(cBright).Bold(),
			HBox(
				helpSection("navigate", 3, 12, &helpNavRows),
				helpSection("actions", 2, 8, &helpActionRows),
			),
			ScreenEffect(
				SEVignette().Strength(In(Animate.From(0)(0.55)).Out(Animate(0))).Dodge(&helpRef).Smooth(),
				SEDropShadow().Focus(&helpRef),
			),
		),
	)
}

func helpSection(title string, grow, keyWidth int, rows *[]helpRow) Component {
	return VBox.Grow(grow)(
		Text(title).FG(cSubtle),
		ForEach(rows, func(r *helpRow) Component {
			return HBox.Gap(2)(
				Text(&r.Key).FG(cFG).Width(int16(keyWidth)),
				Text(&r.Desc).FG(cSubtle),
			)
		}),
	)
}

func toggleHelp() { helpOpen = !helpOpen }

// draftRow renders one draft comment in the inbox's visual style: a filled card
// (selection-aware, accent bar) with the location, the snippet, then the note.
func draftRow(c *draftCommentVM) Component {
	// per-row body fill = full-width flat band (no list marker), focus-aware.
	itemBG := If(&c.Selected).Then(&draftSelBG).Else(&cPaneBG)
	return VBox.Fill(itemBG).PaddingVH(1, 1)(
		Text(&c.Location).FG(cSubtle),
		If(&c.Snippet).Then(Text(&c.Snippet).FG(cMuted)),
		// TextBlock re-wraps to the column width, so a long comment flows onto
		// several lines instead of truncating at one (Text clips to a single line).
		TextBlock(&c.Body).FG(cFG),
	)
}

func taskRow(r *taskVM) Component {
	// per-row body fill = full-width flat band (no list marker), focus-aware. The
	// group header sits OUTSIDE the filled body, so selecting a row never
	// highlights its PENDING/APPROVED header.
	// one icon system: the status dot (● pending / ↻ rework / ✓ approved); the
	// repo is shown plainly, tinted by its identity colour.
	itemBG := If(&r.Selected).Then(&curSelBG).Else(&cPaneBG)
	headerBody := VBox.Fill(itemBG).PaddingVH(1, 1)(
		HBox(
			// FG must be a *Color, not a value: List builds the row template once
			// from a placeholder element, so a by-value .FG() bakes the zero colour
			// and every icon falls back to the inherited (cyan) cascade. The pointer
			// is re-read per row each frame.
			Text(&r.Glyph).FG(&r.GlyphColor),
			SpaceW(1),
			HBox.Grow(1)(
				// FG must live inside Style: .Style() replaces the whole style, so a
				// separate .FG() would be wiped — which left the title untinted/cyan.
				Text(&r.Title).Style(If(&r.Pending).Then(Style{FG: cBright, Attr: AttrBold}).Else(Style{FG: cBright})),
			),
			SpaceW(2),
			// revision-count cue (▸ collapsed / ▾ expanded), only when >1 diff.
			If(&r.ExpandPill).Eq("").Then(Text("")).Else(HBox(Text(&r.ExpandPill).FG(cSubtle), SpaceW(2))),
			// re-review pill: this is a resubmitted fix for a kicked-back task.
			If(&r.ReReview).Then(HBox(Text(&r.ReReviewPill).FG(cAdd), SpaceW(2))),
			// draft-feedback pill: unsubmitted comments in progress on this task.
			If(&r.HasDraft).Then(HBox(Text(&r.DraftPill).FG(cHunk), SpaceW(2))),
			Text(&r.When).FG(cSubtle),
		),
		HBox(
			SpaceW(2),
			Text(&r.Repo).FG(cSubtle), // match the right column's muted meta, not a cyan tint
			Space(),
			Text(&r.IDText).FG(cMuted), // dim id for cross-referencing
			SpaceW(1),
		),
	)
	// revision child row: indented, distinct fill, one line per diff in the history.
	childBG := If(&r.Selected).Then(&curSelBG).Else(&cGroupBG)
	childBody := VBox.Fill(childBG).PaddingVH(0, 1)(
		HBox(
			SpaceW(3),
			Text("·").FG(cMuted),
			SpaceW(1),
			Text(&r.RevLabel).FG(cSubtle),
		),
	)
	// Grouped is pointer-bound, so each row picks header vs child per frame (a Go
	// if would bake the placeholder's branch into the one compiled row template).
	return VBox(
		If(&r.HasGroup).Then(
			VBox.PaddingTRBL(1, 0, 0, 0)(
				Text(&r.GroupLabel).FG(cMuted).Bold(),
			),
		),
		If(&r.Grouped).Then(childBody).Else(headerBody),
	)
}

// --- focus & keys ----------------------------------------------------------
//
// Focusable panes (list, diff, and the conditional draft overview). h/l/Tab move
// focus; within a pane hjkl and the actions are contextual, bound via On(Key) in
// buildMain behind If(&pane).Eq(...) — no global ^* shortcuts. The draft pane is
// only reachable while hasDraft is true.

const (
	paneList  = "list"
	paneDiff  = "diff"
	paneDraft = "draft"
)

var (
	pane         = paneList
	curSelBG     = cSelBG
	draftSelBG   = cSelBG
	draftSel     int
	lastDraftSel = -1

	// the list's base style fills unselected rows with the pane colour; the
	// selection band is painted per-row (taskRow/draftRow) so it never covers a
	// group header. curSelBG/draftSelBG carry the focus-aware band colour.
	listBaseStyle = Style{BG: cPaneBG}

	// side columns cascade this so header text cells sit on the pane bg (not the
	// app bg) — without it, text renders on cBG while the fill is cPaneBG, giving
	// the headers a slightly different background to the rest of the column.
	paneStyle = Style{Fill: cPaneBG, BG: cPaneBG, FG: cFG}
)

// syncDiffToDraft scrolls the diff pane to the line the selected draft comment
// is anchored to (GitHub-style: click a comment, jump to its code). Native
// layer scroll, so no re-render.
func syncDiffToDraft() {
	if draftSel < 0 || draftSel >= len(draftComments) {
		return
	}
	c := draftComments[draftSel]
	if c.File == "" {
		return
	}
	for y, m := range diffMeta {
		if m.File == c.File && (c.Line == 0 || m.Line == c.Line) && m.Commentable {
			diffLayer.ScrollTo(y)
			return
		}
	}
}

func setPane(p string) {
	if p == paneDraft && !hasDraft {
		p = paneList // can't focus a pane that isn't shown
	}
	if p == paneDraft && pane != paneDraft {
		lastDraftSel = -1 // force a diff sync to the current comment on focus-in
	}
	pane = p
	if p == paneList {
		curSelBG = cSelBG // selection reads bright while the list is focused
	} else {
		curSelBG = cFloat // …and dims while you're elsewhere
	}
}

// panes returns the focus ring in left-to-right order, including the draft pane
// only when it's visible.
func panes() []string {
	if hasDraft {
		return []string{paneList, paneDiff, paneDraft}
	}
	return []string{paneList, paneDiff}
}

func togglePane() { focusNext() }

func focusNext() { stepFocus(1) }
func focusPrev() { stepFocus(-1) }

func stepFocus(d int) {
	ring := panes()
	for i, p := range ring {
		if p == pane {
			setPane(ring[(i+d+len(ring))%len(ring)])
			return
		}
	}
	setPane(paneList)
}

func moveDraft(d int) {
	draftSel += d
	if draftSel >= len(draftComments) {
		draftSel = len(draftComments) - 1
	}
	if draftSel < 0 {
		draftSel = 0
	}
}

// selectedDraft returns the comment under the draft cursor, or nil.
func selectedDraft() *draftCommentVM {
	if draftSel < 0 || draftSel >= len(draftComments) {
		return nil
	}
	return &draftComments[draftSel]
}

// openCommentView shows the full comment (wrapped body + snippet) in a modal —
// the pane truncates long notes; this is the read-in-full view.
func openCommentView() {
	c := selectedDraft()
	if c == nil {
		return
	}
	editingCommentID = c.ID
	cvLocation = c.Location
	cvSnippet = c.Snippet
	cvBodyLines = wrapText(c.Body, 66)
	uiApp.PushView("commentview")
}

// wrapText word-wraps s to width columns, returning the lines.
func wrapText(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				out = append(out, line)
				line = w
			} else {
				line += " " + w
			}
		}
		out = append(out, line)
	}
	return out
}

// editDraftComment opens the body prompt pre-filled with the selected comment's
// text; saving calls UpdateComment.
func editDraftComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	if !c.Draft {
		statusMsg = "submitted comments are read-only (unsubmit with U to edit)"
		return
	}
	editingCommentID = c.ID
	setCommentText(c.Body)
	uiApp.PushView("editcomment")
}

func saveEditedComment() {
	body := strings.TrimSpace(commentText)
	setCommentText("")
	uiApp.PopView()
	if editingCommentID == 0 {
		return
	}
	if body == "" {
		statusMsg = "(empty — comment unchanged)"
		return
	}
	if err := uiStore.UpdateComment(editingCommentID, body); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = "comment updated"
	detailDirty = true
}

func deleteDraftComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	if !c.Draft {
		statusMsg = "submitted comments are read-only (unsubmit with U to edit)"
		return
	}
	if err := uiStore.DeleteComment(c.ID); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = "comment deleted"
	if draftSel > 0 {
		draftSel--
	}
	detailDirty = true
}

// openDraftLinks opens any [[file]] references in the selected comment (e.g. a
// screenshot path the reviewer or agent attached). recap can't render images
// inline, so this hands them to the OS opener.
func openDraftLinks() {
	c := selectedDraft()
	if c == nil {
		return
	}
	links := extractLinks(c.Body)
	if len(links) == 0 {
		statusMsg = "no [[file]] links in this comment"
		return
	}
	n := openLinks(c.Body)
	statusMsg = fmt.Sprintf("opened %d/%d link(s)", n, len(links))
}

func openComment() {
	if _, ok := selectedTask(); ok {
		setCommentText("")
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
	if sel >= len(vmRows) {
		sel = len(vmRows) - 1
	}
	if sel < 0 {
		sel = 0
	}
}

// approveSelected quick-approves the selected task by submitting an approve
// review, so its derived state becomes APPROVED (no direct status flip).
func approveSelected() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	if _, _, err := submitReview(uiStore, t.ID, VerdictApprove, ""); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("#%d approved", t.ID)
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
	t, ok := selectedTask()
	if !ok {
		return
	}
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
		setCommentText("")
		uiApp.PopView()
		if t, ok := selectedTask(); body != "" && ok {
			// a general (unanchored) review comment — same draft as line comments,
			// so it shows in the comments pane and submits with the review (a loose
			// thread comment would be invisible to the pane and get "lost").
			if _, err := uiStore.AddReviewComment(t.ID, "you", body, "", 0, "", ""); err != nil {
				statusMsg = "error: " + err.Error()
			} else {
				statusMsg = fmt.Sprintf("commented on #%d", t.ID)
			}
		}
	}
	cancel := func() { setCommentText(""); uiApp.PopView() }
	uiApp.View("comment",
		VBox.Fill(cBG)(
			promptKeys(save, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("comment").FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				commentInput(),
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
		setCommentText(string(rs[:len(rs)-1]))
	}
}

// the comment prompt's input width (panel Width(72) minus padding/"> " gutter).
const commentWrapW = 66

// commentInput renders the wrapped comment text as the prompt body: a "> " gutter
// on the first line, the rest hanging-indented, so long comments wrap instead of
// truncating off-screen.
func commentInput() Component {
	return ForEach(&commentLines, func(line *string) Component {
		return HBox(Text("  ").FG(cSubtle), Text(line).FG(cBright))
	})
}

// setCommentText updates the input and its wrapped display mirror together, so a
// long comment line-wraps in the prompt instead of truncating off-screen.
// the block caret appended to the input's display mirror so the prompt shows a
// cursor at the insertion point. Display-only: commentText (what gets saved) never
// carries it.
const inputCaret = "▌"

func setCommentText(s string) {
	commentText = s
	lines := wrapText(s, commentWrapW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	lines[len(lines)-1] += inputCaret // visible caret at the insertion point
	commentLines = lines
}

// promptKeys is the standard text-prompt binding scope: enter/esc/backspace/
// space. Embed it in a prompt view's tree via On(Key(...)). Printable typing is
// wired separately with wireTyping (the router catch-all).
func promptKeys(save, cancel func()) OnC {
	return On(
		Key("<CR>", save),
		Key("<Esc>", cancel),
		Key("<BS>", backspaceComment),
		Key("<Space>", func() { setCommentText(commentText + " ") }),
		Key("<C-v>", pasteImageIntoComment), // paste a clipboard screenshot as a [[path]] link
	)
}

// insertCommentLink appends a [[path]] reference to the prompt text (space-
// separated when needed). Pure — the testable half of pasteImageIntoComment.
func insertCommentLink(path string) {
	ref := "[[" + path + "]]"
	if commentText != "" && !strings.HasSuffix(commentText, " ") {
		ref = " " + ref
	}
	setCommentText(commentText + ref)
}

// pasteImageIntoComment grabs a clipboard screenshot to a temp PNG and inserts a
// [[path]] link to it (recap can't render images inline, so the link is opened
// with O / the OS opener). No-op with a clear message if the clipboard has no image.
func pasteImageIntoComment() {
	path, err := pasteClipboardImage()
	if err != nil {
		statusMsg = "paste: " + err.Error()
		uiApp.RequestRender()
		return
	}
	insertCommentLink(path)
	statusMsg = "pasted screenshot → " + path
	uiApp.RequestRender()
}

// wireTyping routes printable keystrokes into commentText for a prompt view
// (the catch-all path; there is no On(Key) form for "any rune").
func wireTyping(view string) {
	if r, ok := uiApp.ViewRouter(view); ok {
		r.HandleUnmatched(func(k riffkey.Key) bool {
			if k.Rune != 0 && k.Mod == 0 {
				setCommentText(commentText + string(k.Rune))
				uiApp.RequestRender()
				return true
			}
			return false
		})
	}
}

// openDiffLineComment toggles "pick a line" mode in place: renderDiffLayer draws
// labels over the visible commentable rows of the diff that's already on screen
// (no view switch). The diff-pane key scope captures the label keystroke.
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
	setPickMode(true)
	diffLayer.Invalidate()
}

// setPickMode toggles in-place label mode, keeping the bool and the conditional
// string mirror in sync.
func setPickMode(on bool) {
	diffCommentMode = on
	if on {
		pickMode = "on"
	} else {
		pickMode = "off"
	}
}

func cancelDiffPick() {
	setPickMode(false)
	diffLayer.Invalidate()
}

// pickDiffLine resolves a label to its row, captures the anchor, leaves pick
// mode, and opens the body prompt.
func pickDiffLine(r rune) {
	if !diffCommentMode {
		return
	}
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
	setPickMode(false)
	diffLayer.Invalidate()
	setCommentText("")
	uiApp.PushView("linecomment")
}

func saveLineComment() {
	body := strings.TrimSpace(commentText)
	setCommentText("")
	uiApp.PopView()
	t, ok := selectedTask()
	if body == "" || !ok {
		return
	}
	if _, err := uiStore.AddReviewComment(t.ID, "you", body, pcFile, pcLine, pcAnchor, pcSnippet); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("commented on %s:%d", pcFile, pcLine)
	detailDirty = true
}

// submitSelected publishes the selected task's draft review as request_changes
// straight away — no verdict picker, no summary prompt. The comments (line +
// general) you've already left carry the detail; approve is handled by `a`,
// done by the same. Moves the task into AMENDS.
func submitSelected() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	_, res, err := submitReview(uiStore, t.ID, VerdictRequestChanges, "")
	if err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	msg := fmt.Sprintf("#%d submitted → amends", t.ID)
	if res.line != "" && res.wrote {
		msg += " · queued in TODO"
	}
	statusMsg = msg
	reloadTasks()
}

// unsubmitSelected reverses a submitted review, moving the task from AMENDS back
// to INBOX (its comments return to draft so you can keep editing).
func unsubmitSelected() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	if err := uiStore.UnsubmitReview(t.ID); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("#%d unsubmitted → inbox", t.ID)
	reloadTasks()
}

func setupReviewViews() {
	// line-comment body prompt
	cancel := func() { setCommentText(""); uiApp.PopView() }
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
				commentInput(),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("linecomment")

	// comment read view — the full comment, wrapped; e edits, d deletes.
	uiApp.View("commentview",
		VBox.Fill(cBG)(
			On(
				Key("e", func() { uiApp.PopView(); editDraftComment() }),
				Key("d", func() { uiApp.PopView(); deleteDraftComment() }),
				Key("<Esc>", func() { uiApp.PopView() }),
				Key("q", func() { uiApp.PopView() }),
			),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("comment").FG(cBright).Bold(), Space(), Text("e edit · d delete · esc back").FG(cMuted)),
				SpaceH(1),
				Text(&cvLocation).FG(cSubtle),
				If(&cvSnippet).Then(Text(&cvSnippet).FG(cMuted)),
				SpaceH(1),
				ForEach(&cvBodyLines, func(s *string) Component { return Text(s).FG(cBright) }),
			), Space()),
			Space(),
		),
	).NoCounts()

	// comment edit prompt (reuses the commentText machinery, pre-filled)
	uiApp.View("editcomment",
		VBox.Fill(cBG)(
			promptKeys(saveEditedComment, cancel),
			Space(),
			HBox(Space(), VBox.Fill(cFloat).PaddingVH(1, 2).Width(72)(
				HBox(Text("edit comment").FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				commentInput(),
			), Space()),
			Space(),
		),
	).NoCounts()
	wireTyping("editcomment")
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
