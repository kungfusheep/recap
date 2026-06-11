package main

import (
	"fmt"
	"github.com/kungfusheep/recap/cursor"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kungfusheep/recap/config"
	"github.com/kungfusheep/recap/todo"
)

// The "upcoming" section is a compact, read-only peek at the next few incomplete
// TODO tasks for the selected task's repo, shown above the inbox — a mini version
// of the TODO display, not interactive. The TODO file is read OFF the render
// thread (no main-thread I/O): updateUpcoming stages an async load whenever the
// selected repo changes and the result is swapped in on the render thread, the
// same hand-off the SIGUSR1 inbox reload uses.
var (
	upcomingItems   []upcomingRow // ALL incomplete TODO tasks (the view's ForEach Limit caps the rendered rows)
	upcomingNone    bool          // no incomplete tasks → the band shows a placeholder row
	hasUpcoming     bool          // gates the whole section
	currentRef      string        // the in-flight item's ref (amends:N / todo:hash) — for in-place flaring
	hasCurrent      bool          // true when something is in flight; gates the spinner animation
	upcomingWidth   int16         // inbox column's rendered width (from its NodeRef) — explicit width for the section so its rows don't content-size/truncate
	upcomingReady   bool          // one-shot: force a second frame so the column's NodeRef width is known before sizing the section
	upcomingRepo    string        // repo path currently shown (render-thread owned)
	upcomingLoading string        // repo path being loaded (render-thread owned, dedupe)

	upcomingMu     sync.Mutex
	upcomingStaged *upcomingResult // handed off from the loader goroutine
)

// upcomingRow is one TODO line in the upcoming list. Rows render via a ForEach
// with per-item field bindings (&r.Line/&r.InFlight are offset-resolved per row)
// capped by Limit(upcomingMax) — the cap is a template concern. The in-flight row
// flares in place: its bullet swaps for a Spinner bound to spinFrame.
type upcomingRow struct {
	Line     string // the raw task text (the bullet/spinner prefix is added in the blob)
	InFlight bool   // this row is the in-flight item → spinner prefix instead of a bullet
}

type upcomingResult struct {
	repo    string
	ref     string // the in-flight item's ref at load time
	items   []string
	hasPath bool // the repo resolved a TODO path (template configured) — gates the section
}

const upcomingMax = 4 // how many upcoming tasks to surface

// invalidateUpcoming forces the next updateUpcoming to reload from disk: clears the
// shown-repo + in-flight guard and discards any stale staged result. Call it after
// anything that changes the source (the `recap next` cursor, an in-app TODO edit, or a
// SIGUSR1 reload) so the upcoming section + spinner reflect current state on push.
func invalidateUpcoming() {
	upcomingRepo = ""
	upcomingLoading = ""
	upcomingMu.Lock()
	upcomingStaged = nil
	upcomingMu.Unlock()
}

// updateUpcoming runs on the render thread (from refreshDetail). It swaps in any
// finished async load, then kicks off a new load when the selected task's repo
// differs from what's shown. Cheap and idempotent — safe to call every frame.
func updateUpcoming() {
	upcomingMu.Lock()
	staged := upcomingStaged
	upcomingStaged = nil
	upcomingMu.Unlock()
	if staged != nil {
		upcomingRepo = staged.repo
		upcomingLoading = "" // load landed — clear the in-flight guard so forced reloads work
		currentRef = staged.ref
		hasCurrent = currentRef != ""
		markInFlight() // re-mark inbox rows now the fresh cursor ref has landed (no reload lag)
		upcomingItems = buildUpcomingRows(staged.items, currentRef)
		upcomingNone = len(upcomingItems) == 0
		// show the section for any repo with a resolvable TODO path — NOT just when it
		// has items — so it reserves a fixed block and the inbox below doesn't jump as
		// you move between projects with different numbers of upcoming tasks.
		hasUpcoming = staged.hasPath
	}

	t, ok := selectedTask()
	if !ok {
		return
	}
	if t.RepoPath == upcomingRepo || t.RepoPath == upcomingLoading {
		return // already shown or in flight
	}
	upcomingLoading = t.RepoPath
	repo := t.RepoPath
	app := uiApp // snapshot: the goroutine must not read the mutable global
	go func() {
		ref, _ := cursor.Load(filepath.Base(repo)) // the displayed repo's in-flight item ref
		items, hasPath := loadUpcoming(repo)       // TODO tasks — file read + parse, off the render thread
		upcomingMu.Lock()
		upcomingStaged = &upcomingResult{repo: repo, ref: ref, items: items, hasPath: hasPath}
		upcomingMu.Unlock()
		if app != nil {
			app.RequestRender()
		}
	}()
}

// loadUpcoming resolves repoPath's TODO file and returns its next incomplete tasks
// plus whether a TODO path resolved at all (template configured). Runs on a goroutine
// only. hasPath gates the section: a resolvable path shows a fixed block (even with
// zero tasks) so the layout is stable; no template at all hides it entirely.
func loadUpcoming(repoPath string) (items []string, hasPath bool) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, false
	}
	path, err := todo.PathFor(cfg.TODOTemplate, repoPath)
	if err != nil || path == "" {
		return nil, false
	}
	parsed, err := todo.Read(path)
	if err != nil {
		return nil, true // path is configured (just unreadable/missing) — still reserve the block
	}
	return upcomingFromItems(parsed), true
}

// buildUpcomingRows turns the upcoming task texts into plain bulleted rows. Runs on
// the render thread so it can read the current theme colours. (The in-flight marker
// is rendered separately with a spinner, not as one of these rows.)
func buildUpcomingRows(texts []string, currentRef string) []upcomingRow {
	rows := make([]upcomingRow, 0, len(texts))
	for _, txt := range texts {
		inFlight := currentRef != "" && fmt.Sprintf("todo:%08x", fnvHash(strings.TrimSpace(txt))) == currentRef
		rows = append(rows, upcomingRow{Line: txt, InFlight: inFlight})
	}
	return rows
}

// upcomingFromItems picks the incomplete tasks, in file order — ALL of them; the
// view's ForEach caps the rendered rows (Limit(upcomingMax)), so the cap is a
// template concern, not a data one. Full text — the row Text clips to the inbox
// column width at render time, so the list uses whatever width the display gives
// it (no hard-coded truncation). Pure — the testable core of loadUpcoming.
func upcomingFromItems(items []todo.Item) []string {
	var out []string
	for _, it := range items {
		if !it.IsTask || it.Done {
			continue
		}
		out = append(out, strings.TrimSpace(it.Text))
	}
	return out
}
