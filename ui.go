package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/diff"
	"github.com/kungfusheep/recap/highlight"
	"github.com/kungfusheep/recap/links"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/notify"
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
	cFileHdrBG = Hex(0x33322e) // full-width band behind a file-name header in the diff
)

// canonical diff hues. setThemeVars blends these toward each theme's fg so the
// diff stays add=green / del=red / hunk=blue (distinct + readable) while taking on
// the theme's tone (light vs dark, warm vs cool) instead of clashing.
var (
	diffAddBase  = Hex(0x8aa872)
	diffDelBase  = Hex(0xc08a72)
	diffHunkBase = Hex(0x6f8fa8)
)

// repo identity bar colours (like mail's per-sender tick).
var repoPalette = []Color{
	Hex(0x6f8fa8), Hex(0x8aa872), Hex(0xc08a72), Hex(0xa88fb0),
	Hex(0xc0a86a), Hex(0x6fa8a0), Hex(0xb07a7a),
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
	InFlight     bool // this task is the in-flight amends item → flare it in place (spinner)
	HasGroup     bool
	GroupLabel   string
	Header       bool // a normal task header row (vs a revision child or load-more row)
	LoadMore     bool // a "load more" pseudo-row at the bottom of the paginated done section

	// revision threading (mail-style): a task with >1 diff is expandable with `o`.
	// A header row (RevIdx < 0) shows the latest diff by default; expanding splices
	// one child row per revision (RevIdx >= 0) beneath it, each loading its own diff.
	RevIdx     int    // -1 = task header row; >=0 = revision child (index into Revisions, latest first)
	DiffSHA    string // the commit this row's diff shows (header = latest revision)
	Expanded   bool   // header only: its revisions are spliced in below
	Grouped    bool   // child only: drives the indented, distinct-bg rendering
	ExpandPill string // header only: revision-count cue, e.g. "▸ 3" / "▾ 3"
	RevLabel   string // child only: e.g. "rev 2 · a1b2c3 · added line two"
	Summary    string // this row's reviewer briefing (header = latest revision's; child = its own)
}

// draftCommentVM is one row in the draft-review overview pane: the location
// line (file:line), the captured snippet, and the reviewer's note. File/Line
// keep the raw anchor so selecting a row can scroll the diff to it.
type draftCommentVM struct {
	ID       int64  // comment id, for edit/delete
	ParentID int64  // 0 = top-level; else the comment this replies to
	Who      string // "you" | "agent" (used to label replies)
	Emote    string // optional reaction shown below the body (e.g. 👍)
	HasEmote bool   // gates the emote line; mirrors Emote != ""
	ReadUser bool   // the user has seen this comment (guards the optimistic re-mark)
	ReadDot  string // ●/○ — has the OPPOSITE party read this? (you-comment → agent read it; agent-comment → you read it). You don't see a receipt on your own read.
	Location string // "file · line N" / "general" / "↳ who" for a reply
	LocColor Color  // colour for the location line — the agent's personal colour on its replies
	Indent   string // leading spaces for nested replies (precomputed; build-once safe)
	When     string // comment time (HH:MM) from CreatedAt
	Snippet  string // the diff line commented on (may be empty)
	Body     string
	File     string
	Line     int
	Draft    bool // on the open draft (editable); else submitted (read-only)
	Selected bool // updated each frame like the inbox rows, drives the fill
}

var (
	uiStore *db.Store
	uiApp   *App
	omni    *OmniBox

	tasks  []db.Task
	vmRows []taskVM // flattened: task headers + (when expanded) their revision children
	sel    int      // index into vmRows, NOT tasks (a row may be a revision child)
	// keepSelOnReload makes the next reloadTasks hold the cursor at its current index
	// instead of chasing the selected task by id. Set by user marks (approve/submit) so
	// the marked item leaves and the NEXT item slides up under the cursor — a clean path
	// down the list — without changing the async-insert behaviour (which still tracks the
	// task so a pushed item never yanks the reader's place).
	keepSelOnReload bool
	repoFltr        string
	repos           []string

	// expandedTasks tracks which tasks are expanded into their revision children
	// (mail's thread-expand). Keyed by task id so it survives vmRows rebuilds.
	expandedTasks = map[int64]bool{}
	// taskByID resolves the selected row's task without re-querying (rebuilt each
	// reloadTasks). Rows carry only a task ID; this maps back to the full db.Task.
	taskByID = map[int64]db.Task{}

	// diff pane: a native-scroll Layer. renderDiffLayer builds a component tree from
	// diffFiles/diffBanner into the buffer on content/size change (see buildDiffView),
	// then the framework blits the visible window each frame — scroll is free.
	diffLayer  *Layer
	diffMeta   []diffLineMeta // one entry per rendered row (render order): anchor info
	diffBanner [][]Span       // optional context rows prepended to the diff

	// line-comment "pick a line" mode rides glyph's jump-label engine: while
	// uiApp.JumpModeActive(), renderDiffLayer registers one jump target per visible
	// commentable row at its on-screen position. glyph assigns the labels (home-row,
	// multi-char automatically when there are many), paints them onto the frame, and
	// routes the keystrokes (including multi-char prefixes + Esc). The diff is a
	// scrolled layer so the row→screen mapping is ours (diffViewRef = the LayerView's
	// screen rect); only the label engine is glyph's.
	diffViewRef NodeRef // screen rect of the diff LayerView, for jump-target coords
	// pickAction is what to do with the picked diff line (comment on it, or open it in
	// $EDITOR). Set before EnterJumpMode; the picked target's onSelect calls it.
	pickAction func(diffLineMeta)
	// pickHeaders switches jump-pick from commentable body rows to file-header rows
	// (the fold-pick mode); fileFolded collapses a file to its header in the diff.
	pickHeaders bool
	fileFolded  = map[string]bool{}

	// commentedLines marks diff rows that already carry a draft comment, keyed by
	// "file:line", so renderDiffLayer can draw a visual cue in the gutter.
	commentedLines = map[string]bool{}

	// diffFocused mirrors pane=="diff" as a 0/1 opacity target so the diff
	// scrollbar fades in only when the diff column has focus (mail's cue).
	diffFocused = 0.0

	// draftFocused does the same for the comments (draft) column's scrollbar; the
	// scroll ints are the live window the List publishes via ScrollState (read by the
	// ScrollbarDyn beside it).
	draftFocused                                            = 0.0
	draftScrollOffset, draftScrollVisible, draftScrollTotal int

	helpOpen bool // ? cheatsheet overlay

	// the anchor of the line currently being commented on (set when picked).
	pcFile, pcAnchor, pcSnippet string
	pcLine                      int

	// display strings for the line-comment prompt
	pcLocation    string
	pcSnippetView string

	// comment view/edit: which draft comment is open, and its display strings.
	editingCommentID int64
	replyingToID     int64 // comment being replied to (TUI 'r' in the comments pane)
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
	spinFrame             int // animation frame for the in-flight spinner flare
	inboxCount            int // number of pending (inbox) tasks — shown in the header
	detailTitle           string
	metaRepo, metaWhen    string
	metaResult            string
	metaResultColor       = cSubtle
	filesText             string
	diffFiles             []diff.File
	statusMsg             string

	lastSel, lastLen int
	lastFltr         string
	detailDirty      bool
	// lastDiffKey identifies the diff currently shown (task:rev:sha). refreshDetail only
	// resets the diff scroll when this changes — so an inbox reload that adds an item but
	// leaves the selected task unchanged keeps the reader's scroll position.
	lastDiffKey string
	// doneOldLimit caps how many completed items OLDER THAN A DAY the inbox list renders;
	// the rest sit behind a "load more" row (avoids rendering a huge done history). Recent
	// (< 24h) completed items always show. "load more" raises this by a batch.
	doneOldLimit = 20

	// set by the SIGUSR1 handler; consumed on the render thread to reload the
	// inbox when another process (e.g. `recap add`) changes the db.
	reloadRequested atomic.Bool
)

func runUI() error {
	st, err := db.Open()
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

	uiRepo = currentRepo() // cache the TUI's repo once (refreshIdentity runs on the render thread; no git there)
	refreshIdentity()      // load this repo's agent name + colour
	reloadTasks()

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
			uiApp.RequestRender() // App.Suspend() gates the draw if vim owns the screen; Resume repaints on exit
		}
	}()

	// animate the in-flight spinner flare, but only while there's an in-flight marker
	// (no idle re-renders) and not while an external $EDITOR owns the terminal (else
	// the flare draws over vim).
	go func() {
		for range time.Tick(120 * time.Millisecond) {
			if hasCurrent { // App.Suspend() gates the actual draw while $EDITOR owns the screen
				spinFrame++
				uiApp.RequestRender()
			}
		}
	}()

	// Two named views, registered once (glyph re-layouts each against the terminal
	// size every frame, so no re-register on resize is needed — and rebuilding would
	// discard the diff layer's scroll state). The TODO editor is its OWN view reached
	// via app.Go, not an in-place panel: a full view switch deactivates the inbox view
	// and pops any modal it had pushed (the omnibox), so opening the todo editor can't
	// strand an orphaned router (the dead-keys bug). NoCounts on both so digits type in
	// their prompts instead of buffering vim counts.
	uiApp.View("main", buildMain()).NoCounts()
	uiApp.View("todo", buildTodoView()).NoCounts()
	uiApp.OnBeforeRender(refreshDetail)
	// diff line-picking uses glyph's jump engine (EnterJumpMode pushes its own
	// router for the label keystrokes), so no root unmatched handler is needed.
	return uiApp.RunFrom("main")
}

// --- data ------------------------------------------------------------------

// derived-state ordering + labels (pending → rework → approved).
func statePriority(s string) int {
	switch s {
	case db.StatePending:
		return 0
	case db.StateRework:
		return 1
	default:
		return 2
	}
}

func stateLabel(s string) string {
	switch s {
	case db.StatePending:
		return "INBOX"
	case db.StateRework:
		return "AMENDS"
	default:
		return "DONE"
	}
}

func stateGlyph(s string) string {
	switch s {
	case db.StateRework:
		return "↻"
	case db.StateDone:
		return "✓"
	default:
		return "●"
	}
}

func stateColor(s string) Color {
	switch s {
	case db.StateDone:
		return cSubtle
	case db.StateRework:
		return cDel
	default:
		return cBright
	}
}

func reloadTasks() {
	ensurePins()
	// remember which row is selected (by task id + revision) so a reload that
	// inserts items above it doesn't shift the selection out from under the reader.
	var prevID int64 = -1
	prevRev := -99
	if sel >= 0 && sel < len(vmRows) {
		prevID, prevRev = vmRows[sel].ID, vmRows[sel].RevIdx
	}
	tasks, _ = uiStore.List("", repoFltr)
	// derived state per task (computed from reviews, never a stale flag).
	state := make(map[int64]string, len(tasks))
	inboxCount = 0
	for _, t := range tasks {
		state[t.ID] = uiStore.ReviewState(t.ID)
		if state[t.ID] == db.StatePending {
			inboxCount++ // the header count is the inbox, not the whole task set
		}
	}
	// sections: inbox, then amends, then done. Within inbox, oldest-first (work
	// the queue front-to-back); amends/done by most recent review activity — last
	// completed first — so the done list reads newest-at-top, not by creation id.
	lastRev, _ := uiStore.LatestReviewIDs()
	sort.SliceStable(tasks, func(i, j int) bool {
		si, sj := state[tasks[i].ID], state[tasks[j].ID]
		pi, pj := statePriority(si), statePriority(sj)
		if pi != pj {
			return pi < pj
		}
		if si == db.StatePending {
			// FIFO by ARRIVAL into the inbox, not creation order: a task resolved
			// back from amends (or unsubmitted) re-queues at the END — its stamped
			// inbox_at is newer — instead of jumping to the top on its old id.
			ai, aj := tasks[i].InboxAt, tasks[j].InboxAt
			if ai == "" {
				ai = tasks[i].CreatedAt // pre-migration rows
			}
			if aj == "" {
				aj = tasks[j].CreatedAt
			}
			if ai != aj {
				return ai < aj // oldest arrival first
			}
			return tasks[i].ID < tasks[j].ID
		}
		// non-pending (amends/done): newest review activity first, then id as a
		// tie-break (covers tasks approved directly, with no review row).
		ri, rj := lastRev[tasks[i].ID], lastRev[tasks[j].ID]
		if ri != rj {
			return ri > rj
		}
		return tasks[i].ID > tasks[j].ID
	})
	// pinned tasks float to the top in a "PINNED" section, preserving their relative
	// order from the state sort above (stable). Everything else keeps its place.
	sort.SliceStable(tasks, func(i, j int) bool {
		return pinned[tasks[i].ID] && !pinned[tasks[j].ID]
	})
	taskByID = make(map[int64]db.Task, len(tasks))
	for _, t := range tasks {
		taskByID[t.ID] = t
	}

	// assign each repo a DISTINCT palette colour by sorted order — the old name hash
	// collided (e.g. mail and tui landed on the same colour). Distinct + stable up to
	// the palette size.
	repoColors := map[string]Color{}
	{
		seen := map[string]bool{}
		var repos []string
		for _, t := range tasks {
			if !seen[t.Repo] {
				seen[t.Repo] = true
				repos = append(repos, t.Repo)
			}
		}
		sort.Strings(repos)
		for i, r := range repos {
			repoColors[r] = repoPalette[i%len(repoPalette)]
		}
	}

	vmRows = vmRows[:0]
	prevSection := ""
	oldDoneShown, oldDoneSkipped := 0, 0
	for _, t := range tasks {
		st := state[t.ID]
		// paginate completed items older than a day: show recent (<24h) done always, but
		// only doneOldLimit of the older ones — the rest hide behind a "load more" row.
		// pinned items are never paginated away — they always stay visible up top.
		if st == db.StateDone && !isRecent(t.CreatedAt) && !pinned[t.ID] {
			if oldDoneShown >= doneOldLimit {
				oldDoneSkipped++
				continue
			}
			oldDoneShown++
		}
		vm := taskVM{
			ID:         t.ID,
			IDText:     fmt.Sprintf("#%d", t.ID),
			Title:      t.Title,
			Repo:       t.Repo,
			Glyph:      stateGlyph(st),
			GlyphColor: stateColor(st),
			RepoColor:  repoColors[t.Repo],
			Pending:    st == db.StatePending,
			InFlight:   currentRef == fmt.Sprintf("amends:%d", t.ID),
			RevIdx:     -1, // task header row
			Header:     true,
		}
		vm.When = hhmm(t.CreatedAt)
		// unsubmitted draft feedback → a pill on the row (doesn't affect state).
		if _, n, ok := uiStore.DraftInfo(t.ID); ok && n > 0 {
			vm.HasDraft = true
			vm.DraftPill = fmt.Sprintf("✎ %d", n)
		}
		// a fix-forward task still awaiting review is a re-review: flag it so
		// resubmissions stand out from net-new inbox items.
		if t.ParentID != 0 && st == db.StatePending {
			vm.ReReview = true
			vm.ReReviewPill = fmt.Sprintf("↩ #%d", t.ParentID)
		}
		// revision history: the header shows the latest diff by default; a task with
		// more than one diff is expandable (`o`) into one child row per revision.
		revs, _ := uiStore.Revisions(t.ID)
		if len(revs) > 0 {
			vm.DiffSHA = revs[len(revs)-1].SHA     // latest
			vm.Summary = revs[len(revs)-1].Summary // briefing follows the latest revision
		} else {
			vm.Summary = t.Summary
		}
		if len(revs) > 1 {
			vm.Expanded = expandedTasks[t.ID]
			if vm.Expanded {
				vm.ExpandPill = fmt.Sprintf("▾ %d", len(revs))
			} else {
				vm.ExpandPill = fmt.Sprintf("▸ %d", len(revs))
			}
		}
		// section header: pinned items group under "PINNED" regardless of their state;
		// everything else groups by state label. A header emits whenever the section changes.
		section := stateLabel(st)
		if pinned[t.ID] {
			section = "PINNED"
		}
		if section != prevSection {
			vm.HasGroup = true
			vm.GroupLabel = section
			prevSection = section
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
					Summary:  r.Summary,
				})
			}
		}
	}
	// a "load more" row at the very bottom when older completed items are hidden.
	if oldDoneSkipped > 0 {
		vmRows = append(vmRows, taskVM{
			LoadMore: true,
			RevIdx:   -1,
			Title:    fmt.Sprintf("load more  ·  %d older completed", oldDoneSkipped),
		})
	}

	// restore the selection to the same row (task + revision) it was on before, so
	// items arriving above it don't yank the view; fall back to clamping if it's gone.
	// EXCEPT after a user mark (keepSelOnReload): hold the index so the marked item
	// leaves and the next one slides up under the cursor.
	if keepSelOnReload {
		keepSelOnReload = false // one-shot; external reloads still track by id
	} else if prevID >= 0 {
		for i, r := range vmRows {
			if r.ID == prevID && r.RevIdx == prevRev {
				sel = i
				break
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
func revLabel(idx int, r db.Revision) string {
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
func selectedTask() (db.Task, bool) {
	r := selectedRow()
	if r == nil {
		return db.Task{}, false
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
	// track the inbox column's rendered width (populated each frame after layout) so
	// the upcoming section can be given an explicit width — a plain VBox/ForEach
	// content-sizes to its label and truncates the rows otherwise. Request one more
	// render when it first/again changes so the now-sized section paints.
	if !upcomingReady {
		upcomingReady = true
		uiApp.RequestRender() // one extra frame so listPaneRef.W (set after layout) is readable
	}
	if w := int16(listPaneRef.W); w > 0 && w != upcomingWidth {
		upcomingWidth = w
		uiApp.RequestRender()
	}
	// rebuild the upcoming text each frame so the in-flight spinner glyph animates.
	upcomingBlob = buildUpcomingBlob(upcomingItems, spinFrame)
	// a SIGUSR1 from another process (e.g. `recap add`) requests an inbox reload;
	// do it here on the render thread, then force the detail to rebuild.
	if reloadRequested.CompareAndSwap(true, false) {
		reloadTasks()
		invalidateUpcoming() // force the in-flight cursor + upcoming list to reflect current state (e.g. after `recap next`)
		refreshIdentity()    // pick up `recap whoami` (name + colour) on push
		detailDirty = true
	}
	// peek at the selected repo's next TODO tasks + in-flight marker (loaded async;
	// swapped in here), before the change-detection early-return so finished loads
	// and reload-signal refreshes are picked up.
	updateUpcoming()
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
	// …and the comments scrollbar only while the draft column has focus
	if pane == paneDraft {
		draftFocused = 1.0
	} else {
		draftFocused = 0.0
	}
	if draftSel != lastDraftSel {
		lastDraftSel = draftSel
		syncDiffToDraft()
		markSelectedCommentRead()
	}
	filterText = "all"
	if repoFltr != "" {
		filterText = repoFltr
	}
	countText = fmt.Sprintf("%d", inboxCount)

	if sel == lastSel && len(tasks) == lastLen && repoFltr == lastFltr && !detailDirty {
		return
	}
	lastSel, lastLen, lastFltr, detailDirty = sel, len(tasks), repoFltr, false

	row := selectedRow()
	t, ok := selectedTask()
	// only reset the diff scroll when the SHOWN diff (task:rev:sha) actually changes — so an
	// inbox reload that left the selected task unchanged keeps the reader's scroll position.
	diffKey := ""
	if ok && row != nil {
		diffKey = fmt.Sprintf("%d:%d:%s", t.ID, row.RevIdx, row.DiffSHA)
	}
	resetScroll := diffKey != lastDiffKey
	lastDiffKey = diffKey
	if !ok || row == nil {
		detailTitle, metaRepo, metaWhen, metaResult = "no tasks", "", "", ""
		filesText, diffFiles, draftNote = "", nil, ""
		hasDraft, draftComments = false, nil
		setDiff(resetScroll)
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
	// the briefing follows the row in view: the header shows the latest revision's
	// summary (so it updates when a revise lands), a revision child shows its own —
	// in full, not the truncated left-column label.
	t.Summary = row.Summary
	// the review-context banner (amends summary / re-review header) sits above the
	// task's own diff, which still shows for context.
	diffBanner = buildBanner(t)

	// the diff shown follows the selected row: a header shows the latest revision,
	// a revision child shows its own commit.
	sha := row.DiffSHA
	if sha == "" || t.RepoPath == "" {
		filesText, diffFiles = "no diff — task has no sha", nil
		setDiff(resetScroll)
		return
	}
	filesText = changedFiles(t.RepoPath, sha)
	full, err := git(t.RepoPath, "show", "--format=", sha)
	if err != nil {
		diffFiles = nil
	} else {
		diffFiles = diff.Parse(full)
	}
	setDiff(resetScroll)
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
func buildBanner(t db.Task) [][]Span {
	if uiStore.ReviewState(t.ID) == db.StateRework {
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
			rows = append(rows, summaryBannerBy(t, "what changed")...)
		}
		if len(rows) > 0 {
			return rows
		}
	}
	// plain inbox/done item: lead with the reviewer briefing if there is one.
	if strings.TrimSpace(t.Summary) != "" {
		return summaryBannerBy(t, "summary")
	}
	return nil
}

// summaryBannerBy is summaryBanner with the writing agent's name (the task repo's
// per-repo identity, in its colour) appended to the header — so the briefing reads
// as "summary · Kestrel" and a multi-agent inbox shows who did the work. Cheap: the
// banner only rebuilds on selection change, and the identity is a tiny file read.
func summaryBannerBy(t db.Task, title string) [][]Span {
	rows := summaryBanner(title, t.Summary)
	if name, color := loadIdentity(t.Repo); name != "" && len(rows) > 0 {
		rows[0] = append(rows[0], span("  ·  ", cMuted, false), span(name, color, true))
	}
	return rows
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
func latestSubmitted(taskID int64) []db.Review {
	revs, _ := uiStore.Reviews(taskID)
	for i := len(revs) - 1; i >= 0; i-- {
		if revs[i].State == db.ReviewSubmitted || revs[i].State == db.ReviewResolved {
			return revs[i : i+1]
		}
	}
	return nil
}

// reviewBanner renders a review's summary (+ optional anchored comments) as
// banner rows. withComments lists the line comments (used for the AMENDS view).
func reviewBanner(title string, rv db.Review, withComments bool) [][]Span {
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
		vm := draftCommentVM{ID: c.ID, ParentID: c.ParentID, Who: c.Who, Emote: c.Emote, HasEmote: c.Emote != "", Body: c.Body, File: c.File, Line: c.Line, Draft: c.Draft, When: hhmm(c.CreatedAt)}
		vm.ReadUser = c.ReadUser != ""
		// show the OTHER party's read: on your comment, whether the agent read it;
		// on the agent's comment, whether you read it.
		otherRead := c.ReadAgent != ""
		if c.Who != "you" {
			otherRead = vm.ReadUser
		}
		vm.ReadDot = readDot(otherRead)
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
		vm.LocColor = cSubtle
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
	// order top-level comments (general first, then anchored by file:line) with each
	// reply nested under its parent.
	draftComments = threadComments(draftComments)
}

// threadComments orders a flat comment list into threads: top-level comments in
// the display order (general before anchored, then by file:line), each followed by
// its reply subtree (indented). Reply rows get an "↳ who" location + indent so the
// build-once List template renders them uniformly (no per-row Go branching).
func threadComments(vms []draftCommentVM) []draftCommentVM {
	present := make(map[int64]bool, len(vms))
	for _, v := range vms {
		present[v.ID] = true
	}
	byParent := map[int64][]draftCommentVM{}
	var top []draftCommentVM
	for _, v := range vms {
		if v.ParentID != 0 && present[v.ParentID] {
			byParent[v.ParentID] = append(byParent[v.ParentID], v)
		} else {
			top = append(top, v)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		a, b := top[i], top[j]
		if (a.File == "") != (b.File == "") {
			return a.File == "" // general (unanchored) before anchored
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	var out []draftCommentVM
	var walk func(v draftCommentVM, depth int)
	walk = func(v draftCommentVM, depth int) {
		if depth > 0 { // a reply: relabel + indent, drop the repeated snippet
			v.Location = "↳ " + dash(v.Who)
			v.Indent = strings.Repeat("  ", depth)
			v.Snippet = ""
			if v.Who != "you" { // the agent's voice in its personal colour
				v.LocColor = agentColor
			}
		}
		out = append(out, v)
		for _, r := range byParent[v.ID] {
			walk(r, depth+1)
		}
	}
	for _, v := range top {
		walk(v, 0)
	}
	return out
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
	FileHeader  bool // this row is a file-name header → render a full-width bg band
}

// setDiff rebuilds the diff content and resets scroll. Invalidate tells the
// layer to re-run renderDiffLayer on the next display pass (content changed).
func setDiff(resetScroll bool) {
	// renderDiffLayer rebuilds the component tree + diffMeta from diffFiles/diffBanner each
	// render, so setDiff only (optionally) resets scroll + invalidates. resetScroll is false
	// when the shown diff is unchanged (an inbox reload that didn't change the selected task,
	// or a fold toggle) so the reader's scroll is kept. jump mode (line-picking) is owned by
	// glyph, not reset here — the next render re-registers targets from the rebuilt diffMeta.
	if diffLayer != nil {
		if resetScroll {
			diffLayer.ScrollToTop()
		}
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

// buildDiffView builds the diff as a glyph component tree (todo: diff renderer as
// glyph components). It renders the parsed model as a per-file VBox — file chrome (the
// header band, hunk headers) as standard glyph components, the diff body as Rich Textf
// rows, the commented-line wash as a row .Fill — and returns a parallel meta slice with
// one entry per rendered 1-line row in render order, so jump/anchor registration still
// works by buffer row. Text is clipped to w so each row is exactly one line, preserving
// the row==Y mapping the jump registration relies on (no wrap). Built alongside the
// existing renderer; the swap happens once it's render-verified.
func buildDiffView(files []diff.File, w int) (Component, []diffLineMeta) {
	var meta []diffLineMeta
	clipN := func(s string, max int) string {
		if r := []rune(s); max > 0 && len(r) > max {
			return string(r[:max])
		}
		return s
	}
	clip := func(s string) string { return clipN(s, w) }
	if len(files) == 0 {
		meta = append(meta, diffLineMeta{})
		return VBox.Fill(&cBG)(Text("no changes").FG(&cSubtle)), meta
	}
	var fileBoxes []Component
	for fi, f := range files {
		var rows []Component
		if fi > 0 {
			rows = append(rows, Text("")) // blank spacer row between files
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
		// chrome: standard components. The header band is a full-width row .Fill, led by a
		// fold indicator (▾ open / ▸ folded). A folded file collapses to its header only.
		folded := fileFolded[f.Path]
		caret := "▾ "
		if folded {
			caret = "▸ "
		}
		// renames show both ends of the move (old → new), not just the new path —
		// otherwise a pure rename (no hunks) reads like an untouched file.
		label := cleanLine(f.Path)
		if f.Status == "renamed" && f.OldPath != "" {
			label = cleanLine(f.OldPath) + " → " + cleanLine(f.Path)
		}
		rows = append(rows, HBox.Fill(&cFileHdrBG)(Text(clip(caret+sym+" "+label)).FG(c).Bold()))
		meta = append(meta, diffLineMeta{FileHeader: true, File: f.Path})
		if folded {
			fileBoxes = append(fileBoxes, VBox.Gap(0)(rows...))
			continue
		}
		lexer := highlight.LexerFor(f.Path) // nil for unknown languages → added lines render unhighlighted
		for _, hk := range f.Hunks {
			rows = append(rows, Text(clip("  "+cleanLine(hk.Header))).FG(&cMuted))
			meta = append(meta, diffLineMeta{})
			cur := hunkNewStart(hk.Header)
			for _, l := range hk.Lines {
				txt := cleanLine(l.Text)
				m := diffLineMeta{File: f.Path, Anchor: hk.Header, Text: txt, Commentable: true}
				var row Component
				switch l.Kind {
				case diff.LineAdd:
					m.Line = cur
					cur++
					// ONLY added code is syntax-highlighted. The "+ " gutter (green) and the
					// leading indent are a plain Text — Rich trims leading whitespace, so the
					// indent must live outside it; the code after the indent is highlighted Rich.
					code := clipN(txt, w-2) // leave room for the 2-char gutter
					indent := code[:len(code)-len(strings.TrimLeft(code, " "))]
					rest := code[len(indent):]
					row = HBox(Text("+ "+indent).FG(&cAdd), Textf(highlight.Parts(rest, lexer, cFG)...))
				case diff.LineDel:
					row = Text(clip("- " + txt)).FG(&cDel) // removed: stays red, not highlighted
				default:
					m.Line = cur
					cur++
					row = Text(clip("  " + txt)).FG(&cSubtle) // context: unchanged, subtle
				}
				// a commented line gets a full-width wash via row .Fill.
				if commentedLines[lineKey(m.File, m.Line)] {
					row = HBox.Fill(&cCommentBG)(row)
				}
				rows = append(rows, row)
				meta = append(meta, m)
			}
		}
		fileBoxes = append(fileBoxes, VBox.Gap(0)(rows...))
	}
	return VBox.Fill(&cBG).Gap(0)(fileBoxes...), meta
}

// renderDiffLayer (re)builds the layer buffer from diffFiles via buildDiffView. Called by the
// framework only when the viewport width changes or after Invalidate — never
// per-frame. A fresh, exact-size buffer means no stale rows; every cell is
// cleared to cBG and spans carry an explicit BG, so nothing bleeds.
func renderDiffLayer() {
	w := diffLayer.ViewportWidth()
	if w <= 0 {
		return
	}
	// build the diff as a component tree each render: banner rows (Textf from their spans)
	// + the per-file component diff (buildDiffView). diffMeta is rebuilt parallel to the
	// rendered rows so the jump/anchor mapping still works by buffer row. Components own the
	// visuals (band + wash are row .Fills); we still own the line-pick coordinates.
	diffTree, dmeta := buildDiffView(diffFiles, w-2)
	children := make([]Component, 0, len(diffBanner)+1)
	diffMeta = diffMeta[:0]
	for _, brow := range diffBanner {
		parts := make([]any, len(brow))
		for i, sp := range brow {
			parts[i] = sp
		}
		children = append(children, Textf(parts...))
		diffMeta = append(diffMeta, diffLineMeta{})
	}
	children = append(children, diffTree)
	diffMeta = append(diffMeta, dmeta...)

	h := len(diffMeta)
	if vh := diffLayer.ViewportHeight(); h < vh {
		h = vh // pad to viewport so the themed fill covers the whole pane
	}
	buf := NewBuffer(w, h)
	Build(VBox.Fill(&cBG).Gap(0)(children...)).Execute(buf, int16(w), int16(h))

	// while glyph's jump mode is active, register one jump target per visible commentable
	// row at its screen position (screenY = diffViewRef.Y + row − scroll) — same manual
	// mapping as before, since the diff is a scrolled layer rendered off-screen.
	if uiApp != nil && uiApp.JumpModeActive() {
		top, vh := diffLayer.ScrollY(), diffLayer.ViewportHeight()
		lblStyle := Style{FG: cBG, BG: cHunk, Attr: AttrBold}
		for y := top; y < top+vh && y < len(diffMeta); y++ {
			// fold-pick targets file headers; the normal pick targets commentable lines.
			target := diffMeta[y].Commentable
			if pickHeaders {
				target = diffMeta[y].FileHeader
			}
			if !target {
				continue
			}
			row := y // capture per target so each onSelect picks its own row
			sx, sy := diffViewRef.X, diffViewRef.Y+(y-top)
			uiApp.AddJumpTarget(int16(sx), int16(sy), func() {
				if pickAction != nil && row < len(diffMeta) {
					pickAction(diffMeta[row])
				}
			}, lblStyle)
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

// readDot is the read-receipt glyph: filled when read, hollow when not.
func readDot(read bool) string {
	if read {
		return "●"
	}
	return "○"
}

// hhmm extracts the HH:MM time from a "2006-01-02 15:04:05" stamp (nowStamp's
// format); returns "" for anything shorter, so a missing time just renders blank.
func hhmm(stamp string) string {
	if len(stamp) < 16 {
		return ""
	}
	return stamp[11:16]
}

// --- view ------------------------------------------------------------------

func buildMain() Component {
	return VBox.Fill(&cBG).CascadeStyle(&bgStyle)(
		// global keys. While picking a diff line glyph's jump router is pushed on top
		// of the input stack and intercepts every keystroke (labels + Esc + cancel),
		// so these are shadowed automatically — no manual suppression needed.
		On(
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
		),
		// the inbox columns. The TODO editor is no longer swapped in here — it's a
		// separate named view (buildTodoView) reached via app.Go, so opening it cleanly
		// deactivates this view (and pops any modal it had open, e.g. the omnibox).
		HBox.Grow(1).Gap(4)(
			// left — review inbox (darker column fill claims the area)
			// percentage-sized, NOT Grow: with Grow the left column re-flowed every time
			// the comments column appeared/vanished (2/5 ↔ 2/7 of the width) — a
			// distracting jump while reviewing. A fixed share keeps the inbox put; the
			// middle column alone absorbs the right pane.
			VBox.WidthPct(0.28).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0).NodeRef(&listPaneRef)(
				HBox(
					SpaceW(3),
					Text("recap").FG(&cBright).Bold(),
					SpaceW(1),
					Text(&countText).FG(&cSubtle),
					Space(),
					Text(&filterText).FG(&cSubtle),
					SpaceW(2),
				),
				SpaceH(2),
				If(&hasUpcoming).Then(
					// no divider rule: an auto-stretch HRule in a left-padded box
					// overshoots its container and bleeds into the next column, so
					// the section is separated from the inbox by whitespace instead.
					VBox.Width(&upcomingWidth).PaddingTRBL(0, 2, 2, 3).Gap(1)(
						// explicit width from the column's NodeRef (upcomingWidth) — a
						// plain VBox/ForEach content-sizes to the short "UPCOMING" label
						// and truncates the rows; the removed divider HRule used to force
						// the width. This restores it without the bleed.
						Text("UPCOMING").FG(&cSubtle).Bold(),
						// the whole list is ONE multi-line TextBlock — it reads its
						// pointer and wraps to the width-set VBox, unlike a ForEach of
						// pointer-Text rows (which measure empty at build → truncate). The
						// in-flight row's spinner glyph is built into the blob each frame.
						// FIXED HEIGHT (upcomingMax): fewer tasks leave blanks, more/wrapping clip, so
						// the inbox below never shifts between projects with different upcoming counts.
						VBox.Height(upcomingMax)(
							TextBlock(&upcomingBlob).FG(&cSubtle),
						),
					),
				),
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
					Key("gg", selectTop),   // vim: jump to the first task
					Key("G", selectBottom), // vim: jump to the last task
					Key("<Enter>", openOrLoadMore),
					Key("a", approveSelected),
					Key("u", undoLast), // undo the last approve/submit/pin
					Key("c", openComment),
					Key("v", rerun),
					Key("o", toggleExpand), // expand a task into its revision diffs
					Key("p", togglePin),    // pin/unpin → floats to the PINNED section
				)),
			),
			// middle — detail + diff (no side padding; scrollbar flush right).
			// The top padding now lays out correctly (glyph's flex reposition used to
			// drop it — the old SpaceH(1) workaround is gone with that fix).
			VBox.Grow(3).PaddingTRBL(1, 0, 0, 0).NodeRef(&diffPaneRef)(
				HBox(
					Text(&detailTitle).FG(&cBright).Bold(),
					SpaceW(2),
				),
				SpaceH(1),
				HBox(
					Text(&metaRepo).FG(&cSubtle),
					Text("  ·  ").FG(&cMuted),
					Text(&metaWhen).FG(&cSubtle),
					Text("  ·  ").FG(&cMuted),
					Text(&metaResult).FG(&metaResultColor),
				),
				SpaceH(2),
				// diff + a flush-right scrollbar. No Length: a vertical scrollbar
				// with height 0 is auto-stretched to fill the row, so it tracks the
				// full column height (same structure ScrollView builds internally).
				// It fades in only while the diff column has focus (mail's cue).
				// NodeRef on this HBox gives the diff LayerView's screen rect (it's
				// the first child at x=0): renderDiffLayer maps commentable rows to
				// screen coords from it when registering glyph jump targets.
				HBox.Grow(1).NodeRef(&diffViewRef)(
					LayerView(diffLayer).Grow(1),
					ScrollbarForLayer(diffLayer).
						TrackStyle(&scrollTrackStyle).
						ThumbStyle(&scrollThumbStyle).
						Opacity(Animate(&diffFocused)),
				),
				// diff-focused keys. During a line-pick glyph's jump router is
				// pushed on top and intercepts the label keystrokes, so these stay
				// active here without a manual pick-mode guard.
				If(&pane).Eq(paneDiff).Then(On(
					Key("j", diffDown),
					Key("k", diffUp),
					Key("d", diffHalfDown),
					Key("u", diffHalfUp),
					Key("g", diffTop),
					Key("G", diffBottom),
					Key("c", openDiffLineComment),
					Key("e", openEditorPick), // jump-pick a line → open it in $EDITOR
					Key("z", openFoldPick),   // jump-pick a file header → fold/unfold it
					Key("Z", foldAllFiles),   // fold/unfold ALL files (overview ↔ detail)
					Key("]", nextFile),       // jump to the next file header
					Key("[", prevFile),       // jump to the previous file header
					Key("<Enter>", func() { setPane(paneList) }),
					Key("<Esc>", func() { setPane(paneList) }),
				)),
			),
			// right — comments overview (shown whenever the task has any comments)
			If(&hasDraft).Then(
				VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0).NodeRef(&draftPaneRef)(
					HBox(SpaceW(3), Text("comments").FG(&cBright).Bold(), Space(), Text(&draftNote).FG(&cSubtle), SpaceW(2)),
					SpaceH(2),
					// list + a flush-right scrollbar that tracks the list's window
					// (ScrollState → ScrollbarDyn), fading in only while this column
					// has focus — the diff pane's treatment, for a List.
					HBox.Grow(1)(
						VBox.Grow(1)(
							List(&draftComments).
								Selection(&draftSel).
								Style(&listBaseStyle).
								SelectedStyle(Style{}). // band painted per-row
								Marker("  ").           // blank gutter: Marker("") falls back to the default "> "
								Render(draftRow).
								ScrollState(&draftScrollOffset, &draftScrollVisible, &draftScrollTotal),
						),
						ScrollbarDyn(&draftScrollTotal, &draftScrollVisible, &draftScrollOffset).
							TrackStyle(&scrollTrackStyle).
							ThumbStyle(&scrollThumbStyle).
							Opacity(Animate(&draftFocused)),
					),
					If(&pane).Eq(paneDraft).Then(On(
						Key("j", func() { moveDraft(1) }),
						Key("k", func() { moveDraft(-1) }),
						Key("<Enter>", openCommentView),
						Key("r", replyToComment), // reply to the selected comment (threads under it)
						Key("e", editDraftComment),
						Key("d", deleteDraftComment),
						Key("O", openDraftLinks), // open [[file]] refs (e.g. screenshots)
						Key("<Esc>", func() { setPane(paneList) }),
					)),
				),
			),
		),
		// transient status (errors/confirmations) only — no permanent keybar
		If(&statusMsg).Then(HBox(SpaceW(3), Text(&statusMsg).FG(&cSubtle))),
		// per-column focus fade: unfocused columns dim (mail's FocusShade)
		columnShades(),
		// floating comment prompts (add/edit + read), over the inbox/diff
		inputPromptOverlay(),
		readCommentOverlay(),
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
	{"p", "pin / unpin"},
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

// diff-pane keys (shown in the cheatsheet's third column).
var helpDiffRows = []helpRow{
	{"j / k", "scroll"},
	{"d / u", "half page"},
	{"g / G", "top / bottom"},
	{"] / [", "next / prev file"},
	{"z / Z", "fold file / all"},
	{"c", "comment line"},
	{"e", "open in $EDITOR"},
}

var helpRef NodeRef

// column node refs for the focus-fade effect (mail's FocusShade): each column
// dims while it isn't the focused pane.
var (
	listPaneRef  NodeRef
	diffPaneRef  NodeRef
	draftPaneRef NodeRef
)

// columnShades returns the per-column focus-fade effects, gated by pane: a column
// fades (shade present → animates in) while it isn't focused, and fades back when
// focused. The shades dodge the overlays so popups aren't double-dimmed.
func columnShades() Component {
	fade := Animate.Duration(380 * time.Millisecond).Ease(EaseOutCubic)
	mk := func(ref *NodeRef) FocusShade {
		return NewFocusShade(ref).
			Strength(In(fade(0.28)).Out(fade(0))).
			Dodge(&helpRef, &promptUI.Ref, &promptUI.ReadRef, omni.Ref())
	}
	return VBox(
		If(&pane).Ne(paneList).Then(ScreenEffect(mk(&listPaneRef))),
		If(&pane).Ne(paneDiff).Then(ScreenEffect(mk(&diffPaneRef))),
		// the draft column only exists when hasDraft; otherwise its ref is stale.
		If(&hasDraft).Then(If(&pane).Ne(paneDraft).Then(ScreenEffect(mk(&draftPaneRef)))),
	)
}

// helpOverlay is the ? cheatsheet — centred, two-column, mail's dimensions and
// screen-effect treatment (animated dodged vignette + focused drop shadow).
func helpOverlay() Component {
	return Overlay.Centered()(
		VBox.Width(80).Fill(&cFloat).CascadeStyle(&floatStyle).
			PaddingVH(1, 2).NodeRef(&helpRef).
			Opacity(In(Animate(1.0)).Out(Animate(0))).
			Gap(1)(
			On.Modal(
				Key("?", toggleHelp),
				Key("<Esc>", toggleHelp),
				Key("q", toggleHelp),
			),
			Text("keyboard").FG(&cBright).Bold(),
			HBox(
				helpSection("navigate", 3, 12, &helpNavRows),
				helpSection("actions", 2, 8, &helpActionRows),
				helpSection("diff", 3, 9, &helpDiffRows),
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
		Text(title).FG(&cSubtle),
		ForEach(rows, func(r *helpRow) Component {
			return HBox.Gap(2)(
				Text(&r.Key).FG(&cFG).Width(int16(keyWidth)),
				Text(&r.Desc).FG(&cSubtle),
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
	// Indent (precomputed per row) nests replies; empty for top-level comments.
	return VBox.Fill(itemBG).PaddingVH(1, 1)(
		// one read-receipt dot: has the OTHER party read this? (● read / ○ unread)
		HBox(Text(&c.Indent), Text(&c.ReadDot).FG(&cHunk), SpaceW(1), Text(&c.Location).FG(&c.LocColor), Space(), Text(&c.When).FG(&cMuted)),
		If(&c.Snippet).Then(Text(&c.Snippet).FG(&cMuted)),
		// TextBlock must be bounded to the width LEFT after the indent, else it wraps to
		// the full column and the indent shoves it off the right edge (worse the deeper a
		// reply nests). Grow(1) gives it exactly the remaining column width to wrap into.
		HBox(Text(&c.Indent), VBox.Grow(1)(TextBlock(&c.Body).FG(&cFG))),
		// the agent's reaction sits below the body, attributed to the agent's name in
		// its personal colour (Text, not TextBlock, so the emoji renders cleanly).
		If(&c.HasEmote).Then(HBox(Text(&c.Indent), Text(&c.Emote), SpaceW(1), Text(&agentLabel).FG(&agentColor))),
	)
}

// markInFlight re-syncs each inbox header row's in-flight flag to the current cursor
// ref. Cheap (no I/O), so it runs whenever currentRef changes (the async cursor load
// lands a frame after the task reload) — otherwise the flare sticks on whatever row
// the last full reload marked, instead of following the cursor.
func markInFlight() {
	for i := range vmRows {
		vmRows[i].InFlight = vmRows[i].RevIdx < 0 && currentRef == fmt.Sprintf("amends:%d", vmRows[i].ID)
	}
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
			// is re-read per row each frame. The in-flight item flares in place — its
			// status dot becomes the animated spinner.
			// fixed 1-col slot: glyph's If reserves the WIDER branch, and a bare Spinner
			// measures ~10 cols — which padded every row's status slot and shoved titles
			// right ("mental" spacing). Width(1) clamps it so the dot/spinner stay 1 col.
			HBox.Width(1)(
				If(&r.InFlight).
					Then(Spinner(&spinFrame).Frames(SpinnerDots).FG(&cBright)).
					Else(Text(&r.Glyph).FG(&r.GlyphColor)),
			),
			SpaceW(1),
			HBox.Grow(1)(
				// FG must live inside Style: .Style() replaces the whole style, so a
				// separate .FG() would be wiped — which left the title untinted/cyan.
				Text(&r.Title).Style(If(&r.Pending).Then(&titleBoldStyle).Else(&titlePlainStyle)),
			),
			SpaceW(2),
			// revision-count cue (▸ collapsed / ▾ expanded), only when >1 diff.
			If(&r.ExpandPill).Eq("").Then(Text("")).Else(HBox(Text(&r.ExpandPill).FG(&cSubtle), SpaceW(2))),
			// re-review pill: this is a resubmitted fix for a kicked-back task.
			If(&r.ReReview).Then(HBox(Text(&r.ReReviewPill).FG(&cAdd), SpaceW(2))),
			// draft-feedback pill: unsubmitted comments in progress on this task.
			If(&r.HasDraft).Then(HBox(Text(&r.DraftPill).FG(&cHunk), SpaceW(2))),
			Text(&r.When).FG(&cSubtle),
		),
		HBox(
			SpaceW(2),
			Text(&r.Repo).FG(&r.RepoColor), // repo tinted with its identity colour
			Space(),
			Text(&r.IDText).FG(&cMuted), // dim id for cross-referencing
			SpaceW(1),
		),
	)
	// revision child row: indented, distinct fill, one line per diff in the history.
	childBG := If(&r.Selected).Then(&curSelBG).Else(&cGroupBG)
	childBody := VBox.Fill(childBG).PaddingVH(0, 1)(
		HBox(
			SpaceW(3),
			Text("·").FG(&cMuted),
			SpaceW(1),
			Text(&r.RevLabel).FG(&cSubtle),
			Space(), // fill width intrinsically (so independent Ifs don't collapse the row)
		),
	)
	// the "load more" pseudo-row: a plain, focus-aware line at the bottom of the done list.
	loadMoreBG := If(&r.Selected).Then(&curSelBG).Else(&cPaneBG)
	loadMoreBody := VBox.Fill(loadMoreBG).PaddingVH(0, 2)(
		HBox(SpaceW(1), Text(&r.Title).FG(&cHunk), Space()), // Space() fills width so the row
		// keeps full width (a narrow branch would collapse the compiled row's measured width).
	)
	// Grouped/LoadMore are pointer-bound, so each row picks its branch per frame (a Go
	// if would bake the placeholder's branch into the one compiled row template).
	return VBox(
		If(&r.HasGroup).Then(
			VBox.PaddingTRBL(1, 0, 0, 0)(
				Text(&r.GroupLabel).FG(&cMuted).Bold(),
			),
		),
		// exactly one of these is set per row. Independent Ifs (NOT nested If/Else): the nested
		// form collapses a child/load-more row's width to a sibling branch; each body here is
		// intrinsically full-width (trailing Space/Grow) so a single-level If renders it correctly.
		If(&r.LoadMore).Then(loadMoreBody),
		If(&r.Grouped).Then(childBody),
		If(&r.Header).Then(headerBody),
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

	// the app-background cascade (inbox + todo views) and the float-overlay cascade
	// (omnibox / prompt / help). Package vars (not inline &Style{...} literals) so
	// setThemeVars can mutate them in place — the views cascade them by pointer and
	// repaint on the next render, no view rebuild. (mail's pattern.)
	bgStyle    = Style{Fill: cBG, BG: cBG, FG: cFG}
	floatStyle = Style{Fill: cFloat, BG: cFloat, FG: cFG}

	// the omnibox list (base + selected row), the diff scrollbar (track + thumb), and
	// the task-row title (bold for pending, plain otherwise). All package vars so a
	// theme change repaints them via setThemeVars + render — no rebuild.
	omniListStyle    = Style{BG: cFloat}
	omniSelStyle     = Style{FG: cBright, BG: cSelBG}
	scrollTrackStyle = Style{FG: cMuted, BG: cBG}
	scrollThumbStyle = Style{FG: cSubtle, BG: cBG}
	titleBoldStyle   = Style{FG: cBright, Attr: AttrBold}
	titlePlainStyle  = Style{FG: cBright}
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

// markSelectedCommentRead records the user's read-receipt on the selected comment:
// fills its dot now (optimistic) and persists off the render thread (no main-thread
// I/O). The agent sees it on its next poll / review show.
func markSelectedCommentRead() {
	c := selectedDraft()
	// only the AGENT's comments get a user read-receipt — your own comments don't
	// need one (you wrote them); the dot on an agent comment is YOUR receipt to it.
	if c == nil || c.Who == "you" || c.ReadUser {
		return
	}
	c.ReadUser = true
	c.ReadDot = readDot(true) // optimistic: this agent comment's "you read it" dot fills now
	id := c.ID
	go func() {
		if uiStore != nil {
			_ = uiStore.MarkReadUser(id)
		}
	}()
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
	promptUI.openRead()
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

// replyToComment opens the body prompt to reply to the selected comment; saving
// threads the reply under it (who="you", the reviewer). Works on any comment,
// submitted or draft — replies are discussion, not edits.
func replyToComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	replyingToID = c.ID
	promptUI.open("reply", c.Location, "  "+c.Body, "", saveReply)
}

func saveReply() {
	body := strings.TrimSpace(promptUI.Field.Value)
	if replyingToID == 0 || body == "" {
		return
	}
	if _, err := uiStore.AddReply(replyingToID, "you", body); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = "replied"
	detailDirty = true
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
	promptUI.open("edit comment", "", "", c.Body, saveEditedComment)
}

func saveEditedComment() {
	body := strings.TrimSpace(promptUI.Field.Value)
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
	refs := links.Extract(c.Body)
	if len(refs) == 0 {
		statusMsg = "no [[file]] links in this comment"
		return
	}
	n := links.Open(c.Body)
	statusMsg = fmt.Sprintf("opened %d/%d link(s)", n, len(refs))
}

func openComment() {
	if _, ok := selectedTask(); ok {
		promptUI.open("comment", "", "", "", saveGeneralComment)
	}
}

// saveGeneralComment records an unanchored review comment on the selected task —
// same draft as line comments, so it shows in the pane and submits with the review.
func saveGeneralComment() {
	body := strings.TrimSpace(promptUI.Field.Value)
	t, ok := selectedTask()
	if body == "" || !ok {
		return
	}
	if _, err := uiStore.AddReviewComment(t.ID, "you", body, "", 0, "", ""); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	statusMsg = fmt.Sprintf("commented on #%d", t.ID)
	detailDirty = true // refresh the comments pane so the new comment shows (was "lost")
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

// selectTop / selectBottom are the list's vim gg / G — jump to the first / last row.
func selectTop() { sel = 0 }
func selectBottom() {
	sel = len(vmRows) - 1
	if sel < 0 {
		sel = 0
	}
}

// undoStack is a LIFO of reversible actions; `u` in the inbox runs the most recent.
// Each entry is a closure that reverses one action — approve/submit push an "unsubmit"
// closure, pin/unpin push their inverse — so a single undo handles whatever you last
// did, not just one kind of action. Capped so it can't grow without bound.
var undoStack []func()

func pushUndo(fn func()) {
	undoStack = append(undoStack, fn)
	if len(undoStack) > 50 {
		undoStack = undoStack[len(undoStack)-50:]
	}
}

// pushCategoriseUndo records an approve/submit so `u` unsubmits it back to the inbox.
func pushCategoriseUndo(taskID int64) {
	pushUndo(func() {
		if err := uiStore.UnsubmitReview(taskID); err != nil {
			statusMsg = fmt.Sprintf("undo #%d: %s", taskID, err.Error())
			return
		}
		statusMsg = fmt.Sprintf("undid #%d → inbox", taskID)
		reloadTasks()
	})
}

// undoLast reverses the most recent undoable action (approve/submit/pin). Independent
// of the current selection — it undoes what you last did, not what's highlighted.
func undoLast() {
	if len(undoStack) == 0 {
		statusMsg = "(nothing to undo)"
		return
	}
	fn := undoStack[len(undoStack)-1]
	undoStack = undoStack[:len(undoStack)-1]
	fn()
}

// approveSelected quick-approves the selected task by submitting an approve
// review, so its derived state becomes APPROVED (no direct status flip).
func approveSelected() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	if _, err := submitReview(uiStore, t.ID, db.VerdictApprove, ""); err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	pushCategoriseUndo(t.ID)
	statusMsg = fmt.Sprintf("#%d approved  ·  u to undo", t.ID)
	keepSelOnReload = true // hold the cursor; let the next item slide up
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

// filterOmniItems builds the omnibox's repo-filter choices from the repos currently
// present — "all repos" plus one per project — so the palette lists the filters as
// directly selectable items (not just the f-key cycle). Rebuilt each time the palette
// opens, so a repo that appeared mid-session shows up.
func filterOmniItems() []omniItem {
	items := []omniItem{{
		Label:       "filter: all repos",
		Description: "show tasks from every repo",
		Section:     "filter",
		Action:      func() { repoFltr = ""; sel = 0; reloadTasks() },
	}}
	for _, rp := range repos {
		rp := rp // capture per iteration
		items = append(items, omniItem{
			Label:       "filter: " + rp,
			Description: "show only " + rp + " tasks",
			Section:     "filter",
			Action:      func() { repoFltr = rp; sel = 0; reloadTasks() },
		})
	}
	return items
}

// todoOmniItems lists a "todo: <project>" item per repo currently present, each opening
// that project's TODO list (editor) — so you can jump to any project's todos quickly, not
// just the selected task's repo.
func todoOmniItems() []omniItem {
	seen := map[string]bool{}
	var items []omniItem
	for _, t := range tasks {
		if t.Repo == "" || t.RepoPath == "" || seen[t.Repo] {
			continue
		}
		seen[t.Repo] = true
		repo, path := t.Repo, t.RepoPath
		items = append(items, omniItem{
			Label:       "todo: " + repo,
			Description: "open " + repo + "'s TODO list",
			Section:     "todo",
			Action:      func() { todoUI.openFor(repo, path) },
		})
	}
	return items
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

// --- review UI (line comments + submit) ------------------------------------

// anyCommentableRow reports whether the current diff has at least one line that
// can be picked (commented on / opened) — used to gate the jump-label picker.
// openOrLoadMore is the list's Enter: a "load more" row reveals the next batch of older
// completed items; any other row opens the diff pane.
func openOrLoadMore() {
	if r := selectedRow(); r != nil && r.LoadMore {
		doneOldLimit += 20
		reloadTasks()
		return
	}
	setPane(paneDiff)
}

// isRecent reports whether a "2006-01-02 15:04:05[...]" timestamp is within the last day.
// Unparseable/blank stamps count as recent (shown), never hidden behind "load more".
func isRecent(stamp string) bool {
	if len(stamp) < 19 {
		return true
	}
	tm, err := time.ParseInLocation("2006-01-02 15:04:05", stamp[:19], time.Local)
	if err != nil {
		return true
	}
	return time.Since(tm) < 24*time.Hour
}

func anyCommentableRow() bool {
	for _, m := range diffMeta {
		if m.Commentable {
			return true
		}
	}
	return false
}

// openDiffLineComment starts a line-pick over the on-screen diff using glyph's
// jump labels (no view switch): EnterJumpMode renders, renderDiffLayer registers a
// target per visible commentable row, and picking a label runs pickAction on it.
func openDiffLineComment() {
	if len(tasks) == 0 {
		return
	}
	if !anyCommentableRow() {
		statusMsg = "(no diff lines to comment on)"
		return
	}
	pickHeaders = false
	pickAction = commentOnDiffLine
	uiApp.EnterJumpMode()
}

// openFoldPick starts a header-pick over the diff: jump labels land on the file headers,
// and picking one toggles that file's fold (collapse to header / expand). Reuses the same
// jump engine as line-picking, just targeting FileHeader rows.
func openFoldPick() {
	hasHeader := false
	for _, m := range diffMeta {
		if m.FileHeader {
			hasHeader = true
			break
		}
	}
	if !hasHeader {
		statusMsg = "(no files to fold)"
		return
	}
	pickHeaders = true
	pickAction = toggleFileFold
	uiApp.EnterJumpMode()
}

// toggleFileFold collapses/expands the picked file in the diff, then rebuilds so the
// next render reflects it. Clears the fold-pick mode so the normal line-pick resumes.
func toggleFileFold(m diffLineMeta) {
	fileFolded[m.File] = !fileFolded[m.File]
	pickHeaders = false
	setDiff(false)
}

// foldAllFiles closes every file in the diff (collapse to headers); if they're already all
// folded it opens them all — so one key toggles the whole diff between overview and detail.
func foldAllFiles() {
	allFolded := len(diffFiles) > 0
	for _, f := range diffFiles {
		if !fileFolded[f.Path] {
			allFolded = false
			break
		}
	}
	for _, f := range diffFiles {
		fileFolded[f.Path] = !allFolded
	}
	setDiff(false)
}

// nextFile / prevFile scroll the diff so the next / previous file header sits at the top —
// quick movement through a multi-file diff. They read diffMeta's FileHeader rows (buffer Y
// == row index) against the current scroll.
func nextFile() { scrollToFileHeader(1) }
func prevFile() { scrollToFileHeader(-1) }

func scrollToFileHeader(dir int) {
	if diffLayer == nil {
		return
	}
	cur := diffLayer.ScrollY()
	if dir > 0 {
		for y, m := range diffMeta {
			if m.FileHeader && y > cur {
				diffLayer.ScrollTo(y)
				return
			}
		}
		return // already at/past the last file
	}
	target := 0 // before the first file header → top
	for y, m := range diffMeta {
		if m.FileHeader && y < cur {
			target = y
		}
	}
	diffLayer.ScrollTo(target)
}

// commentOnDiffLine captures the picked line's anchor and opens the body prompt.
func commentOnDiffLine(m diffLineMeta) {
	pcFile, pcAnchor, pcSnippet, pcLine = m.File, m.Anchor, m.Text, m.Line
	pcLocation = fmt.Sprintf("%s · line %d", m.File, m.Line)
	pcSnippetView = "  " + m.Text
	if len(pcSnippetView) > 68 {
		pcSnippetView = pcSnippetView[:67] + "…"
	}
	promptUI.open("line comment", pcLocation, pcSnippetView, "", saveLineComment)
}

func saveLineComment() {
	body := strings.TrimSpace(promptUI.Field.Value)
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
	_, err := submitReview(uiStore, t.ID, db.VerdictRequestChanges, "")
	if err != nil {
		statusMsg = "error: " + err.Error()
		return
	}
	pushCategoriseUndo(t.ID)
	statusMsg = fmt.Sprintf("#%d submitted → amends  ·  u to undo", t.ID)
	keepSelOnReload = true // hold the cursor; let the next item slide up
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
