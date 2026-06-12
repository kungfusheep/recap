package main

import (
	"fmt"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/diff"
	"github.com/kungfusheep/recap/highlight"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
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

var (
	uiStore *db.Store
	uiApp   *App
	omni    *OmniBox

	helpOpen bool // ? cheatsheet overlay

	spinFrame          int // animation frame for the in-flight spinner flare
	detailTitle        string
	metaRepo, metaWhen string
	metaResult         string
	metaResultColor    = cSubtle
	statusMsg          string
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

	diffUI.Layer = NewLayer()
	diffUI.Layer.Render = renderDiffLayer

	omni = newOmniBox(uiApp, omniCommands())

	uiRepo = currentRepo() // cache the TUI's repo once (refreshIdentity runs on the render thread; no git there)
	refreshIdentity()      // load this repo's agent name + colour
	reloadTasks()
	applyPaneFocus() // initial focus colours (events own them from here on)

	// the upcoming band's explicit width derives from the terminal width at the
	// two events that change it — startup and resize. The formula mirrors the
	// column's WidthPct (single source: inboxColPct) and is pinned against real
	// layout output by TestUpcomingWidthFormulaMatchesLayout.
	applyTermWidth := func(w int) {
		upcomingWidth = int16(float32(w) * inboxColPct)
		// initial focus underline: the list pane owns focus at startup, before
		// any layout has populated the NodeRefs.
		if pane == paneList && focusLineX == 0 {
			focusLineW = float64(upcomingWidth)
		}
	}
	applyTermWidth(uiApp.Size().Width)
	uiApp.OnResize(func(w, _ int) { applyTermWidth(w) })

	// live refresh: register this TUI so `recap add` can SIGUSR1 us to reload the
	// inbox without a restart. The signal handler kicks its own fetch (events own
	// their work): queries run here on the signal goroutine against a mutex-copied
	// filter/pins snapshot, the result stages, and the frame seam swaps it in.
	cleanup := notify.Register()
	defer cleanup()
	sigReload := make(chan os.Signal, 1)
	signal.Notify(sigReload, syscall.SIGUSR1)
	defer signal.Stop(sigReload)
	go func() {
		for range sigReload {
			f, p := inboxSnap()
			stageInbox(fetchInbox(f, p))
			uiApp.RequestRender() // App.Suspend() gates the draw if vim owns the screen; Resume repaints on exit
		}
	}()

	// animate the in-flight spinner flare, but only while there's an in-flight marker
	// (no idle re-renders) and not while an external $EDITOR owns the terminal (else
	// the flare draws over vim).
	go func() {
		for range time.Tick(120 * time.Millisecond) {
			live := false
			if hasCurrent { // App.Suspend() gates the actual draw while $EDITOR owns the screen
				spinFrame++
				live = true
			}
			// the notification feed fades on the same gated ticker — tick()
			// stages the fade frame and reports whether anything still lives,
			// so an idle app with an empty feed requests no frames.
			if statusFeed.tick() {
				live = true
			}
			if live {
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
	uiApp.View("messages", buildMessagesView()).NoCounts()
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

// inboxData is everything an inbox reload needs from disk/db, fetched as one
// bundle: the SIGNAL path acquires it on a goroutine and stages it (the render
// path never acquires); handlers call reloadTasks() — fetch+apply inline, since
// handlers are the acquisition layer.
type inboxData struct {
	tasks      []db.Task
	state      map[int64]string // derived per-task state (computed from reviews)
	lastRev    map[int64]int64
	count      int // pending (inbox) tasks — the header count
	pins       map[int64]bool
	unread     int           // cross-repo unread peer messages — the ✉ badge
	drafts     map[int64]int // open draft comment counts (row pill)
	revs       map[int64][]db.Revision
	props      []db.Proposal // open proposals — the PROPOSALS section at the top of the list
	repos      []string      // distinct repos from the unfiltered set (filter cycle)
	identName  string
	identColor Color
}

// fetchInbox runs the inbox reload's queries — db + small files, no view
// mutation, safe on any goroutine. pins is the caller's snapshot of the current
// pin set (nil = load from disk).
func fetchInbox(repoFilter string, pins map[int64]bool) *inboxData {
	d := &inboxData{pins: pins}
	if d.pins == nil {
		d.pins = loadPins()
	}
	if n, err := uiStore.UnreadMessageCount(); err == nil {
		d.unread = n
	}
	d.identName, d.identColor = loadIdentity(uiRepo)
	d.tasks, _ = uiStore.List("", repoFilter)
	d.state = make(map[int64]string, len(d.tasks))
	d.drafts = map[int64]int{}
	d.revs = map[int64][]db.Revision{}
	for _, t := range d.tasks {
		d.state[t.ID] = uiStore.ReviewState(t.ID)
		if d.state[t.ID] == db.StatePending {
			d.count++
		}
		if _, n, ok := uiStore.DraftInfo(t.ID); ok && n > 0 {
			d.drafts[t.ID] = n
		}
		if revs, _ := uiStore.Revisions(t.ID); len(revs) > 0 {
			d.revs[t.ID] = revs
		}
	}
	d.lastRev, _ = uiStore.LatestReviewIDs()
	// open proposals: cross-repo documents, so the repo filter matches the
	// TARGET (the repo that would own the work).
	if props, err := uiStore.Proposals(db.ProposalOpen); err == nil {
		for _, p := range props {
			if repoFilter == "" || p.TargetRepo == repoFilter {
				d.props = append(d.props, p)
			}
		}
	}
	// distinct repos for the filter cycle (from the unfiltered set)
	all, _ := uiStore.List("", "")
	seen := map[string]bool{}
	for _, t := range all {
		if !seen[t.Repo] {
			seen[t.Repo] = true
			d.repos = append(d.repos, t.Repo)
		}
	}
	return d
}

var (
	inboxStagedMu sync.Mutex
	inboxStaged   *inboxData
)

func stageInbox(d *inboxData) {
	inboxStagedMu.Lock()
	inboxStaged = d
	inboxStagedMu.Unlock()
}

func takeStagedInbox() *inboxData {
	inboxStagedMu.Lock()
	d := inboxStaged
	inboxStaged = nil
	inboxStagedMu.Unlock()
	return d
}

// reloadTasks is the handler-side reload: fetch + apply inline, then the
// reload's consequences (header, flags, detail) — events own their work.
func reloadTasks() {
	applyInbox(fetchInbox(inboxUI.RepoFilter, pinned))
	onInboxReloaded()
}

// onInboxReloaded runs a reload's consequences: header strings, selection
// flags, and the detail refresh. Callers: reloadTasks (handler path) and the
// staged-inbox drain (signal path).
func onInboxReloaded() {
	updateInboxSnap()
	refreshHeader()
	syncSelectionFlags()
	refreshDetailNow()
}

// the SIGUSR1 fetch snapshot: filter + pins, copied under a mutex so the signal
// goroutine can kick fetchInbox directly without racing handler writes.
var (
	inboxSnapMu sync.Mutex
	snapFilter  string
	snapPins    map[int64]bool
)

func updateInboxSnap() {
	inboxSnapMu.Lock()
	snapFilter = inboxUI.RepoFilter
	snapPins = make(map[int64]bool, len(pinned))
	for k, v := range pinned {
		snapPins[k] = v
	}
	inboxSnapMu.Unlock()
}

func inboxSnap() (string, map[int64]bool) {
	inboxSnapMu.Lock()
	defer inboxSnapMu.Unlock()
	return snapFilter, snapPins
}

// applyInbox projects a fetched bundle into the inbox view-model — render
// thread, no I/O.
func applyInbox(d *inboxData) {
	pinned = d.pins
	msgUnread = d.unread
	agentName, agentColor = d.identName, d.identColor
	agentLabel = agentName
	if agentLabel == "" {
		agentLabel = "agent"
	}
	// remember which row is selected (by task id + revision) so a reload that
	// inserts items above it doesn't shift the selection out from under the reader.
	var prevID int64 = -1
	prevRev := -99
	prevProp := false
	if inboxUI.Sel >= 0 && inboxUI.Sel < len(inboxUI.Rows) {
		prevID, prevRev = inboxUI.Rows[inboxUI.Sel].ID, inboxUI.Rows[inboxUI.Sel].RevIdx
		prevProp = inboxUI.Rows[inboxUI.Sel].Proposal
	}
	inboxUI.Tasks = d.tasks
	state := d.state
	inboxUI.Count = d.count
	// sections: inbox, then amends, then done. Within inbox, oldest-first (work
	// the queue front-to-back); amends/done by most recent review activity — last
	// completed first — so the done list reads newest-at-top, not by creation id.
	lastRev := d.lastRev
	sort.SliceStable(inboxUI.Tasks, func(i, j int) bool {
		si, sj := state[inboxUI.Tasks[i].ID], state[inboxUI.Tasks[j].ID]
		pi, pj := statePriority(si), statePriority(sj)
		if pi != pj {
			return pi < pj
		}
		if si == db.StatePending {
			// FIFO by ARRIVAL into the inbox, not creation order: a task resolved
			// back from amends (or unsubmitted) re-queues at the END — its stamped
			// inbox_at is newer — instead of jumping to the top on its old id.
			ai, aj := inboxUI.Tasks[i].InboxAt, inboxUI.Tasks[j].InboxAt
			if ai == "" {
				ai = inboxUI.Tasks[i].CreatedAt // pre-migration rows
			}
			if aj == "" {
				aj = inboxUI.Tasks[j].CreatedAt
			}
			if ai != aj {
				return ai < aj // oldest arrival first
			}
			return inboxUI.Tasks[i].ID < inboxUI.Tasks[j].ID
		}
		// non-pending (amends/done): newest review activity first, then id as a
		// tie-break (covers tasks approved directly, with no review row).
		ri, rj := lastRev[inboxUI.Tasks[i].ID], lastRev[inboxUI.Tasks[j].ID]
		if ri != rj {
			return ri > rj
		}
		return inboxUI.Tasks[i].ID > inboxUI.Tasks[j].ID
	})
	// pinned tasks float to the top in a "PINNED" section, preserving their relative
	// order from the state sort above (stable). Everything else keeps its place.
	sort.SliceStable(inboxUI.Tasks, func(i, j int) bool {
		return pinned[inboxUI.Tasks[i].ID] && !pinned[inboxUI.Tasks[j].ID]
	})
	inboxUI.TaskByID = make(map[int64]db.Task, len(inboxUI.Tasks))
	for _, t := range inboxUI.Tasks {
		inboxUI.TaskByID[t.ID] = t
	}

	// assign each repo a DISTINCT palette colour by sorted order — the old name hash
	// collided (e.g. mail and tui landed on the same colour). Distinct + stable up to
	// the palette size.
	repoColors := map[string]Color{}
	{
		seen := map[string]bool{}
		var rs []string
		for _, t := range inboxUI.Tasks {
			if !seen[t.Repo] {
				seen[t.Repo] = true
				rs = append(rs, t.Repo)
			}
		}
		sort.Strings(rs)
		for i, r := range rs {
			repoColors[r] = repoPalette[i%len(repoPalette)]
		}
	}

	inboxUI.Rows = inboxUI.Rows[:0]
	prevSection := ""
	doneShown, doneSkipped := 0, 0
	// open proposals lead the list: documents awaiting the human's verdict sit
	// above the task queue. Row ID is the PROPOSAL id (its own sequence), so the
	// rows resolve through PropByID and selection-restore matches on the flag.
	inboxUI.PropByID = make(map[int64]db.Proposal, len(d.props))
	propOpen = len(d.props)
	for _, p := range d.props {
		inboxUI.PropByID[p.ID] = p
		color := cBright
		if _, ic := loadIdentity(p.ProposerRepo); ic.Mode != 0 {
			color = ic // proposer's identity colour tints the meta line
		}
		vm := taskVM{
			ID:        p.ID,
			IDText:    fmt.Sprintf("P%d", p.ID),
			Title:     p.Title,
			Repo:      fmt.Sprintf("%s → %s", p.ProposerRepo, p.TargetRepo),
			RepoColor: color,
			State:     "proposal",
			Pending:   true, // bold title — it's awaiting the verdict
			Proposal:  true,
			Header:    true,
			RevIdx:    -1,
			When:      hhmm(p.CreatedAt),
		}
		if prevSection != "PROPOSALS" {
			vm.HasGroup, vm.GroupLabel = true, "PROPOSALS"
			prevSection = "PROPOSALS"
		}
		inboxUI.Rows = append(inboxUI.Rows, vm)
	}
	for _, t := range inboxUI.Tasks {
		st := state[t.ID]
		// paginate completed items: only DoneLimit render — the rest hide behind a
		// "load more" row. The done sort is last-completed-first, so the visible
		// page is always the most recent activity. Pinned items are never
		// paginated away — they always stay visible up top.
		if st == db.StateDone && !pinned[t.ID] {
			if doneShown >= inboxUI.DoneLimit {
				doneSkipped++
				continue
			}
			doneShown++
		}
		vm := taskVM{
			ID:        t.ID,
			IDText:    fmt.Sprintf("#%d", t.ID),
			Title:     t.Title,
			Repo:      t.Repo,
			State:     st,
			RepoColor: repoColors[t.Repo],
			Pending:   st == db.StatePending,
			InFlight:  currentRef == fmt.Sprintf("amends:%d", t.ID),
			RevIdx:    -1, // task header row
			Header:    true,
		}
		vm.When = hhmm(t.CreatedAt)
		// unsubmitted draft feedback → a pill on the row (doesn't affect state).
		if n := d.drafts[t.ID]; n > 0 {
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
		revs := d.revs[t.ID]
		if len(revs) > 0 {
			vm.DiffSHA = revs[len(revs)-1].SHA     // latest
			vm.Summary = revs[len(revs)-1].Summary // briefing follows the latest revision
		} else {
			vm.Summary = t.Summary
		}
		if len(revs) > 1 {
			vm.Expanded = inboxUI.Expanded[t.ID]
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
		inboxUI.Rows = append(inboxUI.Rows, vm)

		// expanded → splice a child row per revision, latest first (mail's order).
		if vm.Expanded {
			for j := len(revs) - 1; j >= 0; j-- {
				r := revs[j]
				inboxUI.Rows = append(inboxUI.Rows, taskVM{
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
	// a "load more" row at the very bottom when completed items are hidden.
	if doneSkipped > 0 {
		inboxUI.Rows = append(inboxUI.Rows, taskVM{
			LoadMore: true,
			RevIdx:   -1,
			Title:    fmt.Sprintf("load more  ·  %d more completed", doneSkipped),
		})
	}

	// restore the selection to the same row (task + revision) it was on before, so
	// items arriving above it don't yank the view; fall back to clamping if it's gone.
	// EXCEPT after a user mark (KeepSelOnReload): hold the index so the marked item
	// leaves and the next one slides up under the cursor.
	if inboxUI.KeepSelOnReload {
		inboxUI.KeepSelOnReload = false // one-shot; external reloads still track by id
	} else if prevID >= 0 {
		for i, r := range inboxUI.Rows {
			if r.ID == prevID && r.RevIdx == prevRev && r.Proposal == prevProp {
				inboxUI.Sel = i
				break
			}
		}
	}
	if inboxUI.Sel >= len(inboxUI.Rows) {
		inboxUI.Sel = len(inboxUI.Rows) - 1
	}
	if inboxUI.Sel < 0 {
		inboxUI.Sel = 0
	}

	// distinct repos for the filter cycle (fetched from the unfiltered set)
	inboxUI.Repos = append(inboxUI.Repos[:0], d.repos...)
	inboxUI.DetailDirty = true
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
	if inboxUI.Sel < 0 || inboxUI.Sel >= len(inboxUI.Rows) {
		return nil
	}
	return &inboxUI.Rows[inboxUI.Sel]
}

// selectedTask resolves the task behind the selected row — the same task whether a
// header or one of its revision children is selected. Actions operate on this.
func selectedTask() (db.Task, bool) {
	r := selectedRow()
	if r == nil || r.Proposal {
		// a proposal row's ID is a PROPOSAL id — it must never resolve through
		// TaskByID (the sequences are independent, so a bare lookup could land on
		// an unrelated task and route task actions at it).
		return db.Task{}, false
	}
	t, ok := inboxUI.TaskByID[r.ID]
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
	inboxUI.Expanded[t.ID] = !inboxUI.Expanded[t.ID]
	reloadTasks()
	for i := range inboxUI.Rows {
		if inboxUI.Rows[i].ID == t.ID && inboxUI.Rows[i].RevIdx < 0 {
			inboxUI.Sel = i
			break
		}
	}
	inboxUI.DetailDirty = true
	onInboxSelChanged()
}

// refreshDetail updates selection fill + the right-hand detail when selection,
// filter, or task set changes — never per-frame git calls.
// refreshDetail is the OnBeforeRender hook — after the event-home decomposition
// it carries ONLY pure derivation and the staged-apply seam (loader results are
// staged off-thread and swapped here, the first thing the next frame does; this
// seam collapses to app.Post(fn) when glyph grows it). Events own their work:
// selection handlers run their selection's consequences, setPane applies focus
// colours, the SIGUSR1 handler kicks its own fetch.
func refreshDetail() {
	// staged-apply seam ONLY: swap in finished loader results (fetched
	// off-thread). No polls, no derivation — events own everything else.
	if d := takeStagedInbox(); d != nil {
		applyInbox(d)
		invalidateUpcoming() // force the in-flight cursor + upcoming list to reflect current state (e.g. after `recap next`)
		inboxUI.DetailDirty = true
		onInboxReloaded()
	}
	drainUpcoming()
	drainFeed()
	// a result whose key no longer matches the shown selection is stale — the
	// newer kick's result is already on its way; drop it.
	if staged := takeStagedDetail(); staged != nil && staged.key == inboxUI.LastDiffKey {
		applyDetail(staged)
	}
}

// syncSelectionFlags re-marks the inbox rows' Selected flags — called by the
// handlers that move the selection (and by applyInbox after a rebuild).
func syncSelectionFlags() {
	for i := range inboxUI.Rows {
		inboxUI.Rows[i].Selected = i == inboxUI.Sel
	}
}

// focusBarX/focusBarW are the focus underline's tween TARGETS — the bar at the
// bottom of the app slides to the focused pane's rect. Set at focus events
// (applyPaneFocus) from the panes' NodeRefs (layout output); the template
// animates toward them (VBox.Width(Animate(&…)), the glyph tween primitive).
// focusLineX/focusLineW are the focus underline's tween TARGETS (floats — the
// FocusLine effect renders at sub-cell resolution). Set at focus events from
// the panes' NodeRefs; the effect's compiled Animate tweens slide toward them.
var (
	focusLineX float64
	focusLineW float64
)

// focusLineTween builds the underline's tween: x and width share duration +
// ease so the line slides as one shape.
func focusLineTween(target *float64) any {
	return Animate.Duration(300 * time.Millisecond).Ease(EaseOutCubic)(target)
}

// applyPaneFocus applies the focus-driven colours: the active column's selection
// band reads bright, the others dim, and the diff/comments scrollbars fade with
// focus. Called by setPane — a focus switch applies its colours in the switch.
func applyPaneFocus() {
	curSelBG = cFloat
	draftSelStyle.BG = cFloat
	switch pane {
	case paneList:
		curSelBG = cSelBG
	case paneDraft:
		draftSelStyle.BG = cSelBG
	}
	if pane == paneDiff {
		diffUI.Focused = 1.0
	} else {
		diffUI.Focused = 0.0
	}
	if pane == paneDraft {
		draftUI.Focused = 1.0
	} else {
		draftUI.Focused = 0.0
	}
	// slide the focus underline to the focused pane's rect. NodeRefs carry the
	// last layout's geometry — valid at any focus event after the first frame;
	// zero-width refs (pre-first-layout) keep the previous targets.
	var r NodeRef
	switch pane {
	case paneList:
		r = listPaneRef
	case paneDiff:
		r = diffPaneRef
	case paneDraft:
		r = draftUI.PaneRef
	}
	if r.W > 0 {
		focusLineX, focusLineW = float64(r.X), float64(r.W)
	}
}

// refreshHeader recomputes the header's count/filter display strings — called
// when their inputs change (reload, filter cycle, badge recount).
func refreshHeader() {
	inboxUI.FilterText = "all"
	if inboxUI.RepoFilter != "" {
		inboxUI.FilterText = inboxUI.RepoFilter
	}
	inboxUI.CountText = fmt.Sprintf("%d", inboxUI.Count)
	if msgUnread > 0 {
		inboxUI.CountText += fmt.Sprintf("  ✉ %d", msgUnread)
	}
	if propOpen > 0 {
		inboxUI.CountText += fmt.Sprintf("  ◆ %d", propOpen)
	}
}

// onInboxSelChanged runs an inbox selection change's consequences — flags, the
// detail refresh, the upcoming peek. The handler that moves the selection calls
// this; nothing polls for the move.
func onInboxSelChanged() {
	syncSelectionFlags()
	refreshDetailNow()
}

// refreshDetailNow rebuilds the detail pane for the current selection. Idempotent
// (keyed on sel/len/filter + the DetailDirty force-flag), so every event that
// could change what the detail shows simply calls it.
func refreshDetailNow() {
	kickUpcoming()
	if inboxUI.Sel == inboxUI.LastSel && len(inboxUI.Tasks) == inboxUI.LastLen && inboxUI.RepoFilter == inboxUI.LastFilter && !inboxUI.DetailDirty {
		return
	}
	inboxUI.LastSel, inboxUI.LastLen, inboxUI.LastFilter, inboxUI.DetailDirty = inboxUI.Sel, len(inboxUI.Tasks), inboxUI.RepoFilter, false

	row := selectedRow()
	// a proposal row: the detail pane shows the document + thread, fetched
	// through the same staged seam. The key holds across thread growth (a
	// comment sets DetailDirty, which re-kicks without resetting the scroll).
	if row != nil && row.Proposal {
		p, okp := inboxUI.PropByID[row.ID]
		diffKey := fmt.Sprintf("prop:%d", row.ID)
		resetScroll := diffKey != inboxUI.LastDiffKey
		inboxUI.LastDiffKey = diffKey
		if !okp {
			return
		}
		detailTitle = p.Title
		metaRepo = fmt.Sprintf("%s → %s", p.ProposerRepo, p.TargetRepo)
		metaWhen = p.CreatedAt
		metaResult = p.Status
		metaResultColor = proposalStatusColor(p.Status)
		propDetailKick(p, diffKey, resetScroll)
		return
	}
	t, ok := selectedTask()
	// only reset the diff scroll when the SHOWN diff (task:rev:sha) actually changes — so an
	// inbox reload that left the selected task unchanged keeps the reader's scroll position.
	diffKey := ""
	if ok && row != nil {
		diffKey = fmt.Sprintf("%d:%d:%s", t.ID, row.RevIdx, row.DiffSHA)
	}
	resetScroll := diffKey != inboxUI.LastDiffKey
	inboxUI.LastDiffKey = diffKey
	if !ok || row == nil {
		detailTitle, metaRepo, metaWhen, metaResult = "no tasks", "", "", ""
		diffUI.FilesText, diffUI.Files, draftUI.Note = "", nil, ""
		draftUI.Has, draftUI.Comments = false, nil
		setDiff(resetScroll)
		return
	}
	// the cheap, already-in-memory fields update synchronously; everything that
	// needs the db or git (banner, comments, the diff body) is acquisition and
	// goes through detailKick → fetchDetail (goroutine) → the staged seam.
	detailTitle = t.Title
	if row.RevIdx >= 0 { // a revision child: title it so the diff in view is clear
		detailTitle = t.Title + "  ·  " + row.RevLabel
	}
	metaRepo, metaWhen = t.Repo, t.CreatedAt
	metaResult = dash(t.Result)
	metaResultColor = resultColor(t.Result)
	// the briefing follows the row in view: the header shows the latest revision's
	// summary (so it updates when a revise lands), a revision child shows its own —
	// in full, not the truncated left-column label.
	t.Summary = row.Summary
	detailKick(t, *row, diffKey, resetScroll)
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
// titled header, through the briefing mini-markup (bullets, `code`, **bold**,
// Label: lead-ins) so structured summaries scan instead of reading as one slab.
func summaryBanner(title, summary string) [][]Span {
	var rows [][]Span
	rows = append(rows, []Span{span(title, cHunk, true)})
	rows = append(rows, summaryBody(summary, 72)...)
	rows = append(rows, []Span{}) // blank separator before the diff
	return rows
}

// summaryBody renders briefing text with a small scanning-friendly markup:
//   - "- " at line start: a bullet row (coloured glyph, hanging indent)
//   - "Label:" at line start (a short capitalised lead): bold
//   - `code`: identifier colouring; **bold**: bold
//
// Lines wrap span-aware to width, preserving styles across the wrap. Plain
// unstructured text renders exactly as before (one wrapped paragraph).
func summaryBody(text string, width int) [][]Span {
	var rows [][]Span
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			rows = append(rows, []Span{})
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "• ") {
			body := strings.TrimSpace(trimmed[2:])
			for i, w := range wrapSpans(markupSpans(body, true), width-4) {
				if i == 0 {
					rows = append(rows, append([]Span{span("  · ", cHunk, false)}, w...))
				} else {
					rows = append(rows, append([]Span{span("    ", cFG, false)}, w...))
				}
			}
			continue
		}
		for _, w := range wrapSpans(markupSpans(trimmed, true), width-2) {
			rows = append(rows, append([]Span{span("  ", cFG, false)}, w...))
		}
	}
	return rows
}

// markupSpans tokenises one logical line: `code` gets identifier colour,
// **bold** bolds, and (when leadLabel) a short "Label:" lead-in renders bold.
func markupSpans(line string, leadLabel bool) []Span {
	var out []Span
	rest := line
	// a short capitalised "Label:" lead (e.g. "Why:", "Verify:", "What changed:")
	if leadLabel {
		if i := strings.Index(rest, ":"); i > 0 && i <= 24 && rest[0] >= 'A' && rest[0] <= 'Z' && !strings.ContainsAny(rest[:i], "`*") {
			out = append(out, span(rest[:i+1], cBright, true))
			rest = rest[i+1:]
		}
	}
	for rest != "" {
		ci, bi := strings.Index(rest, "`"), strings.Index(rest, "**")
		switch {
		case ci >= 0 && (bi < 0 || ci < bi):
			end := strings.Index(rest[ci+1:], "`")
			if end < 0 {
				out = append(out, span(rest, cFG, false))
				rest = ""
				continue
			}
			if ci > 0 {
				out = append(out, span(rest[:ci], cFG, false))
			}
			out = append(out, span(rest[ci+1:ci+1+end], cHunk, false))
			rest = rest[ci+2+end:]
		case bi >= 0:
			end := strings.Index(rest[bi+2:], "**")
			if end < 0 {
				out = append(out, span(rest, cFG, false))
				rest = ""
				continue
			}
			if bi > 0 {
				out = append(out, span(rest[:bi], cFG, false))
			}
			out = append(out, span(rest[bi+2:bi+2+end], cBright, true))
			rest = rest[bi+4+end:]
		default:
			out = append(out, span(rest, cFG, false))
			rest = ""
		}
	}
	return out
}

// wrapSpans wraps styled spans to width at word boundaries, preserving each
// word's style across line breaks.
func wrapSpans(spans []Span, width int) [][]Span {
	type word struct {
		text  string
		style Style
	}
	var words []word
	for _, sp := range spans {
		for _, w := range strings.Fields(sp.Text) {
			words = append(words, word{w, sp.Style})
		}
	}
	if len(words) == 0 {
		return nil
	}
	var out [][]Span
	var line []Span
	used := 0
	for _, w := range words {
		wl := len([]rune(w.text))
		if used > 0 && used+1+wl > width {
			out = append(out, line)
			line, used = nil, 0
		}
		if used > 0 {
			line = append(line, Span{Text: " " + w.text, Style: w.style})
			used += 1 + wl
		} else {
			line = append(line, Span{Text: w.text, Style: w.style})
			used += wl
		}
	}
	return append(out, line)
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
	// renderDiffLayer rebuilds the component tree + diffUI.Meta from diffUI.Files/diffUI.Banner each
	// render, so setDiff only (optionally) resets scroll + invalidates. resetScroll is false
	// when the shown diff is unchanged (an inbox reload that didn't change the selected task,
	// or a fold toggle) so the reader's scroll is kept. jump mode (line-picking) is owned by
	// glyph, not reset here — the next render re-registers targets from the rebuilt diffUI.Meta.
	if diffUI.Layer != nil {
		if resetScroll {
			diffUI.Layer.ScrollToTop()
		}
		diffUI.Layer.Invalidate()
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

// The diff pane follows glyph's cardinal rule — compile once, mutate state. The
// template below is compiled a single time (lazily) and re-executed into the layer
// buffer whenever the content or width changes; per-row content lives in diffUI.Rows
// (a []Span per visual row, a sanctioned derived field rebuilt by prepDiffRows),
// which Rich(&r.Spans) re-reads every execute. No component trees are constructed
// or compiled at render time.
// prepDiffRows flattens the banner + parsed diff into diffUI.Rows (one []Span per visual
// row) and the parallel diffUI.Meta (jump/anchor coordinates by row). Pure data prep —
// runs only when content or width changes, never per frame.
func prepDiffRows(w int) {
	clipN := func(s string, max int) string {
		if r := []rune(s); max > 0 && len(r) > max {
			return string(r[:max])
		}
		return s
	}
	clip := func(s string) string { return clipN(s, w) }

	diffUI.Rows = diffUI.Rows[:0]
	diffUI.Meta = diffUI.Meta[:0]
	row := func(m diffLineMeta, spans ...Span) {
		diffUI.Rows = append(diffUI.Rows, diffRowVM{Spans: spans})
		diffUI.Meta = append(diffUI.Meta, m)
	}
	// bandRow is a row whose container Fill paints bg edge-to-edge (file headers,
	// comment washes) — the band is the container's, not padding spans'.
	bandRow := func(m diffLineMeta, bg Color, spans ...Span) {
		diffUI.Rows = append(diffUI.Rows, diffRowVM{Spans: spans, BG: bg})
		diffUI.Meta = append(diffUI.Meta, m)
	}

	for _, brow := range diffUI.Banner {
		diffUI.Rows = append(diffUI.Rows, diffRowVM{Spans: append([]Span(nil), brow...)})
		diffUI.Meta = append(diffUI.Meta, diffLineMeta{})
	}

	if len(diffUI.Files) == 0 {
		row(diffLineMeta{}, span("no changes", cSubtle, false))
		return
	}

	for fi, f := range diffUI.Files {
		if fi > 0 {
			row(diffLineMeta{}, span(" ", cFG, false)) // blank spacer row between files
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
		// header band: fold caret (▾ open / ▸ folded) + status + path, padded so the
		// band colour reaches the right edge. Renames show both ends of the move.
		folded := diffUI.Folded[f.Path]
		caret := "▾ "
		if folded {
			caret = "▸ "
		}
		label := cleanLine(f.Path)
		if f.Status == "renamed" && f.OldPath != "" {
			label = cleanLine(f.OldPath) + " → " + cleanLine(f.Path)
		}
		hdr := Span{Text: clip(caret + sym + " " + label), Style: Style{FG: c, BG: cFileHdrBG, Attr: AttrBold}}
		bandRow(diffLineMeta{FileHeader: true, File: f.Path}, cFileHdrBG, hdr)
		if folded {
			continue
		}
		lexer := highlight.LexerFor(f.Path) // nil for unknown languages → added lines render unhighlighted
		for _, hk := range f.Hunks {
			row(diffLineMeta{}, span(clip("  "+cleanLine(hk.Header)), cMuted, false))
			cur := hunkNewStart(hk.Header)
			for _, l := range hk.Lines {
				txt := cleanLine(l.Text)
				m := diffLineMeta{File: f.Path, Anchor: hk.Header, Text: txt, Commentable: true}
				var spans []Span
				switch l.Kind {
				case diff.LineAdd:
					m.Line = cur
					cur++
					// ONLY added code is syntax-highlighted: green gutter + indent,
					// then the code tokens in the theme's syntax colours.
					code := clipN(txt, w-2) // leave room for the 2-char gutter
					indent := code[:len(code)-len(strings.TrimLeft(code, " "))]
					rest := code[len(indent):]
					spans = append([]Span{span("+ "+indent, cAdd, false)}, highlight.Spans(rest, lexer, cFG, cBG)...)
				case diff.LineDel:
					spans = []Span{span(clip("- "+txt), cDel, false)} // removed: red, not highlighted
				default:
					m.Line = cur
					cur++
					spans = []Span{span(clip("  "+txt), cSubtle, false)} // context: unchanged, subtle
				}
				// a commented line gets a full-width wash: every span takes the wash
				// background; the row container's Fill carries it to the edge.
				if diffUI.Commented[lineKey(m.File, m.Line)] {
					for i := range spans {
						spans[i].Style.BG = cCommentBG
					}
					bandRow(m, cCommentBG, spans...)
					continue
				}
				row(m, spans...)
			}
		}
	}
}

// renderDiffLayer re-executes the diff's ONE compiled template into a fresh layer
// buffer. Called by the framework only when the viewport width changes or after
// Invalidate — never per-frame. prepDiffRows rebuilds the row data (content and
// width are inputs to the spans); the template itself is never rebuilt.
func renderDiffLayer() {
	w := diffUI.Layer.ViewportWidth()
	if w <= 0 {
		return
	}
	prepDiffRows(w - 2)

	h := len(diffUI.Meta)
	if vh := diffUI.Layer.ViewportHeight(); h < vh {
		h = vh // pad to viewport so the themed fill covers the whole pane
	}
	buf := NewBuffer(w, h)
	diffTemplate().Execute(buf, int16(w), int16(h))

	// while glyph's jump mode is active, register one jump target per visible commentable
	// row at its screen position (screenY = diffUI.ViewRef.Y + row − scroll) — same manual
	// mapping as before, since the diff is a scrolled layer rendered off-screen.
	if uiApp != nil && uiApp.JumpModeActive() {
		top, vh := diffUI.Layer.ScrollY(), diffUI.Layer.ViewportHeight()
		lblStyle := Style{FG: cBG, BG: cHunk, Attr: AttrBold}
		for y := top; y < top+vh && y < len(diffUI.Meta); y++ {
			// fold-pick targets file headers; the normal pick targets commentable lines.
			target := diffUI.Meta[y].Commentable
			if diffUI.PickHeaders {
				target = diffUI.Meta[y].FileHeader
			}
			if !target {
				continue
			}
			row := y // capture per target so each onSelect picks its own row
			sx, sy := diffUI.ViewRef.X, diffUI.ViewRef.Y+(y-top)
			uiApp.AddJumpTarget(int16(sx), int16(sy), func() {
				if diffUI.PickAction != nil && row < len(diffUI.Meta) {
					diffUI.PickAction(diffUI.Meta[row])
				}
			}, lblStyle)
		}
	}

	scrollY := diffUI.Layer.ScrollY()
	diffUI.Layer.SetBuffer(buf)    // resets scrollY to 0…
	diffUI.Layer.ScrollTo(scrollY) // …so restore it (preserves scroll across re-render)
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

// inboxColPct is the left column's share of the terminal — single source for
// the template's WidthPct AND the resize-event computation of upcomingWidth.
const inboxColPct = 0.28

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
			Key("m", openMessages),   // the agent↔agent message ledger
			Key("A", openAgentsDash), // the agent activity dashboard
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
			VBox.WidthPct(inboxColPct).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0).NodeRef(&listPaneRef)(
				HBox(
					SpaceW(3),
					Text("recap").FG(&cBright).Bold(),
					SpaceW(1),
					Text(&inboxUI.CountText).FG(&cSubtle),
					Space(),
					Text(&inboxUI.FilterText).FG(&cSubtle),
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
						// the cap lives in the template: ForEach binds ALL items,
						// Limit(upcomingMax) caps the rendered rows. FIXED HEIGHT
						// (upcomingMax): fewer tasks leave blanks, so the inbox below
						// never shifts between projects with different upcoming
						// counts. The in-flight row's bullet swaps for a Spinner
						// bound to spinFrame (per-item field bindings are
						// offset-resolved inside the ForEach).
						VBox.Height(upcomingMax)(
							If(&upcomingNone).Then(Text("· nothing upcoming").FG(&cSubtle)),
							ForEach(&upcomingItems).Limit(upcomingMax)(func(r *upcomingRow) Component {
								return HBox(
									If(&r.InFlight).
										Then(Spinner(&spinFrame).Frames(SpinnerDots).FG(&cSubtle)).
										Else(Text("·").FG(&cSubtle)),
									SpaceW(1),
									Text(&r.Line).FG(&cSubtle),
								)
							}),
						),
					),
				),
				List(&inboxUI.Rows).
					Selection(&inboxUI.Sel).
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
				HBox.Grow(1).NodeRef(&diffUI.ViewRef)(
					LayerView(diffUI.Layer).Grow(1),
					ScrollbarForLayer(diffUI.Layer).
						TrackStyle(&scrollTrackStyle).
						ThumbStyle(&scrollThumbStyle).
						Opacity(Animate(&diffUI.Focused)),
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
			If(&draftUI.Has).Then(
				VBox.Grow(2).Fill(&cPaneBG).CascadeStyle(&paneStyle).PaddingTRBL(1, 0, 0, 0).NodeRef(&draftUI.PaneRef)(
					HBox(SpaceW(3), Text("comments").FG(&cBright).Bold(), Space(), Text(&draftUI.Note).FG(&cSubtle), SpaceW(2)),
					SpaceH(2),
					// list + a flush-right scrollbar that tracks the list's window
					// (ScrollState → ScrollbarDyn), fading in only while this column
					// has focus — the diff pane's treatment, for a List.
					HBox.Grow(1)(
						VBox.Grow(1)(
							List(&draftUI.Comments).
								Selection(&draftUI.Sel).
								Style(&listBaseStyle).
								SelectedStyle(&draftSelStyle). // focus-aware band; List paints it
								Marker("  ").                  // blank gutter: Marker("") falls back to the default "> "
								Render(draftRow).
								ScrollState(&draftUI.ScrollOffset, &draftUI.ScrollVisible, &draftUI.ScrollTotal),
						),
						ScrollbarDyn(&draftUI.ScrollTotal, &draftUI.ScrollVisible, &draftUI.ScrollOffset).
							TrackStyle(&scrollTrackStyle).
							ThumbStyle(&scrollThumbStyle).
							Opacity(Animate(&draftUI.Focused)),
					),
					If(&pane).Eq(paneDraft).Then(On(
						Key("j", func() { moveDraft(1) }),
						Key("k", func() { moveDraft(-1) }),
						Key("<Enter>", openCommentView),
						Key("r", replyToComment),      // reply to the selected comment (threads under it)
						Key("o", toggleCommentThread), // collapse/expand the selected thread
						Key("e", editDraftComment),
						Key("d", deleteDraftComment),
						Key("O", openDraftLinks), // open [[file]] refs (e.g. screenshots)
						Key("<Esc>", func() { setPane(paneList) }),
					)),
				),
			),
		),

		// transient status (errors/confirmations) streams through the
		// bottom-right notification feed (mail's component, feed.go) — it
		// replaced the status bar that sat one row above the focus line and
		// collided with it (todo:a5f726bf).
		feedOverlay(),
		// per-column focus fade: unfocused columns dim (mail's FocusShade)
		columnShades(),
		// the focus underline is INKED by a post-process over the invisible
		// tween carrier — rune+FG only, every cell's background preserved.
		ScreenEffect(NewFocusLine(focusLineTween(&focusLineX), focusLineTween(&focusLineW), &cFG)),
		// floating comment prompts (add/edit + read), over the inbox/diff
		inputPromptOverlay(),
		readCommentOverlay(),
		// keyboard help overlay, toggled with ? (modal scope captures esc/?)
		If(&helpOpen).Then(helpOverlay()),
		agentsOverlay(),
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
	{"o", "revisions / fold thread"},
	{"p", "pin / unpin"},
	{"m", "agent messages"},
	{"A", "agent dashboard"},
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
	listPaneRef NodeRef
	diffPaneRef NodeRef
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
		// the draft column only exists when draftUI.Has; otherwise its ref is stale.
		If(&draftUI.Has).Then(If(&pane).Ne(paneDraft).Then(ScreenEffect(mk(&draftUI.PaneRef)))),
	)
}

// helpOverlay is the ? cheatsheet — centred, two-column, mail's dimensions and
// screen-effect treatment (animated dodged vignette + focused drop shadow).
func helpOverlay() Component {
	return Overlay.Centered()(
		VBox.Width(96).Fill(&cFloat).CascadeStyle(&floatStyle).
			PaddingVH(1, 2).NodeRef(&helpRef).
			Opacity(In(Animate(1.0)).Out(Animate(0))).
			Gap(1)(
			On.Modal(
				Key("?", toggleHelp),
				Key("<Esc>", toggleHelp),
				Key("q", toggleHelp),
			),
			Text("keyboard").FG(&cBright).Bold(),
			// column shares sized to their content: actions carries the longest
			// descriptions ("revisions / fold thread"), so it gets the biggest
			// share; Gap separates the columns so a clipped description can never
			// visually run into the next column's keys.
			HBox.Gap(3)(
				helpSection("navigate", 4, 12, &helpNavRows),
				helpSection("actions", 5, 8, &helpActionRows),
				helpSection("diff", 4, 9, &helpDiffRows),
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

// markInFlight re-syncs each inbox header row's in-flight flag to the current cursor
// ref. Cheap (no I/O), so it runs whenever currentRef changes (the async cursor load
// lands a frame after the task reload) — otherwise the flare sticks on whatever row
// the last full reload marked, instead of following the cursor.
func markInFlight() {
	for i := range inboxUI.Rows {
		inboxUI.Rows[i].InFlight = inboxUI.Rows[i].RevIdx < 0 && currentRef == fmt.Sprintf("amends:%d", inboxUI.Rows[i].ID)
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
					Else(Switch(&r.State).
						Case(db.StateRework, Text("↻").FG(&cDel)).
						Case(db.StateDone, Text("✓").FG(&cSubtle)).
						Case("proposal", Text("◆").FG(&cHunk)).
						Default(Text("●").FG(&cBright))),
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
// only reachable while draftUI.Has is true.

const (
	paneList  = "list"
	paneDiff  = "diff"
	paneDraft = "draft"
)

var (
	pane     = paneList
	curSelBG = cSelBG

	// the list's base style fills unselected rows with the pane colour; the
	// selection band is painted per-row (taskRow/draftRow) so it never covers a
	// group header. curSelBG/draftUI.SelBG carry the focus-aware band colour.
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
	if draftUI.Sel < 0 || draftUI.Sel >= len(draftUI.Comments) {
		return
	}
	c := draftUI.Comments[draftUI.Sel]
	if c.File == "" {
		return
	}
	for y, m := range diffUI.Meta {
		if m.File == c.File && (c.Line == 0 || m.Line == c.Line) && m.Commentable {
			diffUI.Layer.ScrollTo(y)
			return
		}
	}
}

func setPane(p string) {
	if p == paneDraft && !draftUI.Has {
		p = paneList // can't focus a pane that isn't shown
	}
	if p == paneDraft && pane != paneDraft {
		draftUI.LastSel = -1 // force a diff sync to the current comment on focus-in
	}
	pane = p
	applyPaneFocus() // a focus switch applies its colours in the switch function
	if p == paneDraft {
		onDraftSelChanged() // focus-in: sync the diff to the current comment
	}
}

// panes returns the focus ring in left-to-right order, including the draft pane
// only when it's visible.
func panes() []string {
	if draftUI.Has {
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

func saveEditedComment() {
	body := strings.TrimSpace(promptUI.Field.Value)
	if draftUI.EditingID == 0 {
		return
	}
	if body == "" {
		toast("(empty — comment unchanged)")
		return
	}
	if err := uiStore.EditOwnComment(draftUI.EditingID, body); err != nil {
		toast("error: " + err.Error())
		return
	}
	toast("comment updated")
	inboxUI.DetailDirty = true
	refreshDetailNow()
}

func openComment() {
	if row := selectedRow(); row != nil && row.Proposal {
		commentOnProposal(row)
		return
	}
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
		toast("error: " + err.Error())
		return
	}
	toast(fmt.Sprintf("commented on #%d", t.ID))
	inboxUI.DetailDirty = true // refresh the comments pane so the new comment shows (was "lost")
	refreshDetailNow()
}

// diff scroll is native: adjust the layer's scrollY (clamped internally) and
// the framework re-blits the visible window next frame — no re-render.
func diffDown()     { diffUI.Layer.ScrollDown(1) }
func diffUp()       { diffUI.Layer.ScrollUp(1) }
func diffHalfDown() { diffUI.Layer.HalfPageDown() }
func diffHalfUp()   { diffUI.Layer.HalfPageUp() }
func diffTop()      { diffUI.Layer.ScrollToTop() }
func diffBottom()   { diffUI.Layer.ScrollToEnd() }

func moveSel(d int) {
	inboxUI.Sel += d
	if inboxUI.Sel >= len(inboxUI.Rows) {
		inboxUI.Sel = len(inboxUI.Rows) - 1
	}
	if inboxUI.Sel < 0 {
		inboxUI.Sel = 0
	}
	onInboxSelChanged()
}

// selectTop / selectBottom are the list's vim gg / G — jump to the first / last row.
func selectTop() { inboxUI.Sel = 0; onInboxSelChanged() }
func selectBottom() {
	inboxUI.Sel = len(inboxUI.Rows) - 1
	if inboxUI.Sel < 0 {
		inboxUI.Sel = 0
	}
	onInboxSelChanged()
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
			toast(fmt.Sprintf("undo #%d: %s", taskID, err.Error()))
			return
		}
		toast(fmt.Sprintf("undid #%d → inbox", taskID))
		reloadTasks()
	})
}

// undoLast reverses the most recent undoable action (approve/submit/pin). Independent
// of the current selection — it undoes what you last did, not what's highlighted.
func undoLast() {
	if len(undoStack) == 0 {
		toast("(nothing to undo)")
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
		toast("error: " + err.Error())
		return
	}
	pushCategoriseUndo(t.ID)
	toast(fmt.Sprintf("#%d approved  ·  u to undo", t.ID))
	inboxUI.KeepSelOnReload = true // hold the cursor; let the next item slide up
	reloadTasks()
}

func cycleFilter() {
	if inboxUI.RepoFilter == "" {
		if len(inboxUI.Repos) > 0 {
			inboxUI.RepoFilter = inboxUI.Repos[0]
		}
	} else {
		idx := -1
		for i, rp := range inboxUI.Repos {
			if rp == inboxUI.RepoFilter {
				idx = i
				break
			}
		}
		if idx < 0 || idx+1 >= len(inboxUI.Repos) {
			inboxUI.RepoFilter = ""
		} else {
			inboxUI.RepoFilter = inboxUI.Repos[idx+1]
		}
	}
	inboxUI.Sel = 0
	reloadTasks()
}

// filterOmniItems builds the omnibox's repo-filter choices from the repos currently
// present — "all repos" plus one per project — so the palette lists the filters as
// directly selectable items (not just the f-key cycle). Rebuilt each time the palette
// opens, so a repo that appeared mid-session shows up.
func filterOmniItems() []omniItem {
	items := []omniItem{{
		Label:       "filter: all inboxUI.Repos",
		Description: "show inboxUI.Tasks from every repo",
		Section:     "filter",
		Action:      func() { inboxUI.RepoFilter = ""; inboxUI.Sel = 0; reloadTasks() },
	}}
	for _, rp := range inboxUI.Repos {
		rp := rp // capture per iteration
		items = append(items, omniItem{
			Label:       "filter: " + rp,
			Description: "show only " + rp + " inboxUI.Tasks",
			Section:     "filter",
			Action:      func() { inboxUI.RepoFilter = rp; inboxUI.Sel = 0; reloadTasks() },
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
	for _, t := range inboxUI.Tasks {
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
		toast("(no check command)")
		return
	}
	toast("running: " + t.CheckCmd + " …")
	uiApp.RenderNow()
	cmd := exec.Command("sh", "-c", t.CheckCmd)
	if t.RepoPath != "" {
		cmd.Dir = t.RepoPath
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		toast("✓ PASS  " + t.CheckCmd)
	} else {
		toast("✗ FAIL  " + t.CheckCmd + "  — " + lastLine(string(out)))
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
		inboxUI.DoneLimit += 20
		reloadTasks()
		return
	}
	setPane(paneDiff)
}

func anyCommentableRow() bool {
	for _, m := range diffUI.Meta {
		if m.Commentable {
			return true
		}
	}
	return false
}

// openDiffLineComment starts a line-pick over the on-screen diff using glyph's
// jump labels (no view switch): EnterJumpMode renders, renderDiffLayer registers a
// target per visible commentable row, and picking a label runs diffUI.PickAction on it.
func openDiffLineComment() {
	if len(inboxUI.Tasks) == 0 {
		return
	}
	if !anyCommentableRow() {
		toast("(no diff lines to comment on)")
		return
	}
	diffUI.PickHeaders = false
	diffUI.PickAction = commentOnDiffLine
	uiApp.EnterJumpMode()
}

// openFoldPick starts a header-pick over the diff: jump labels land on the file headers,
// and picking one toggles that file's fold (collapse to header / expand). Reuses the same
// jump engine as line-picking, just targeting FileHeader rows.
func openFoldPick() {
	hasHeader := false
	for _, m := range diffUI.Meta {
		if m.FileHeader {
			hasHeader = true
			break
		}
	}
	if !hasHeader {
		toast("(no files to fold)")
		return
	}
	diffUI.PickHeaders = true
	diffUI.PickAction = toggleFileFold
	uiApp.EnterJumpMode()
}

// toggleFileFold collapses/expands the picked file in the diff, then rebuilds so the
// next render reflects it. Clears the fold-pick mode so the normal line-pick resumes.
func toggleFileFold(m diffLineMeta) {
	diffUI.Folded[m.File] = !diffUI.Folded[m.File]
	diffUI.PickHeaders = false
	setDiff(false)
}

// foldAllFiles closes every file in the diff (collapse to headers); if they're already all
// folded it opens them all — so one key toggles the whole diff between overview and detail.
func foldAllFiles() {
	allFolded := len(diffUI.Files) > 0
	for _, f := range diffUI.Files {
		if !diffUI.Folded[f.Path] {
			allFolded = false
			break
		}
	}
	for _, f := range diffUI.Files {
		diffUI.Folded[f.Path] = !allFolded
	}
	setDiff(false)
}

// nextFile / prevFile scroll the diff so the next / previous file header sits at the top —
// quick movement through a multi-file diff. They read diffUI.Meta's FileHeader rows (buffer Y
// == row index) against the current scroll.
func nextFile() { scrollToFileHeader(1) }
func prevFile() { scrollToFileHeader(-1) }

func scrollToFileHeader(dir int) {
	if diffUI.Layer == nil {
		return
	}
	cur := diffUI.Layer.ScrollY()
	if dir > 0 {
		for y, m := range diffUI.Meta {
			if m.FileHeader && y > cur {
				diffUI.Layer.ScrollTo(y)
				return
			}
		}
		return // already at/past the last file
	}
	target := 0 // before the first file header → top
	for y, m := range diffUI.Meta {
		if m.FileHeader && y < cur {
			target = y
		}
	}
	diffUI.Layer.ScrollTo(target)
}

// commentOnDiffLine captures the picked line's anchor and opens the body prompt.
func commentOnDiffLine(m diffLineMeta) {
	diffUI.PickFile, diffUI.PickAnchor, diffUI.PickSnippet, diffUI.PickLine = m.File, m.Anchor, m.Text, m.Line
	loc := fmt.Sprintf("%s · line %d", m.File, m.Line)
	snip := "  " + m.Text
	if len(snip) > 68 {
		snip = snip[:67] + "…"
	}
	promptUI.open("line comment", loc, snip, "", saveLineComment)
}

func saveLineComment() {
	body := strings.TrimSpace(promptUI.Field.Value)
	t, ok := selectedTask()
	if body == "" || !ok {
		return
	}
	if _, err := uiStore.AddReviewComment(t.ID, "you", body, diffUI.PickFile, diffUI.PickLine, diffUI.PickAnchor, diffUI.PickSnippet); err != nil {
		toast("error: " + err.Error())
		return
	}
	toast(fmt.Sprintf("commented on %s:%d", diffUI.PickFile, diffUI.PickLine))
	inboxUI.DetailDirty = true
	refreshDetailNow()
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
		toast("error: " + err.Error())
		return
	}
	pushCategoriseUndo(t.ID)
	toast(fmt.Sprintf("#%d submitted → amends  ·  u to undo", t.ID))
	inboxUI.KeepSelOnReload = true // hold the cursor; let the next item slide up
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
		toast("error: " + err.Error())
		return
	}
	toast(fmt.Sprintf("#%d unsubmitted → inbox", t.ID))
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
