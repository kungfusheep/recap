package main

import (
	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
)

// taskVM is the per-row view-model. Selected is updated in place each frame so
// the row fill reacts (mail's pattern); rebuilt only when the task set changes.
type taskVM struct {
	ID           int64
	IDText       string // "#6", dim, for cross-referencing with CLI / chat
	Title        string
	When         string
	Repo         string
	State        string // derived review state (pending/rework/done) — the TEMPLATE owns the glyph + colour (Switch with pointer FGs, so a theme switch recolors live)
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
	Header       bool   // a normal task header row (vs a revision child or load-more row)
	LoadMore     bool   // a "load more" pseudo-row at the bottom of the paginated done section
	Proposal     bool   // an open proposal row — ID is the PROPOSAL id (PropByID resolves it, not TaskByID)
	SectionRow   bool   // a COLLAPSED section's stub row — o/Enter/z unfolds it
	SecCue       string // stub only: "▸ N hidden"

	// revision threading (mail-style): a task with >1 diff is expandable with `o`.
	// A header row (RevIdx < 0) shows the latest diff by default; expanding splices
	// one child row per revision (RevIdx >= 0) beneath it, each loading its own diff.
	RevIdx     int    // -1 = task header row; >=0 = revision child (index into Revisions, latest first)
	DiffSHA    string // the commit this row's diff shows (header = latest revision)
	Expanded   bool   // header only: its revisions are spliced in below
	Grouped    bool   // child only: drives the indented, distinct-bg rendering
	ExpandPill string // header only: revision-count cue, e.g. "▸ 3" / "▾ 3"
	RevLabel   string // child only: e.g. "rev 2 · a1b2c3 · 12:45 · added line two"
	RevWhen    string // child only: the revision's full submitted-at stamp (detail meta)
	Summary    string // this row's reviewer briefing (header = latest revision's; child = its own)
}

// inboxView is the inbox's state in one concrete struct (the 5a–5d pattern): the
// task list and its flattened row VMs, selection, repo filter, expand/lookup maps,
// and the reload/refresh bookkeeping driven by selection changes. One package
// instance (inboxUI) — fields are pointer-bound into the compiled views
// (&inboxUI.Rows, &inboxUI.CountText…), so the struct must be a stable package
// var. No interfaces, no injection.
type inboxView struct {
	Tasks []db.Task
	Rows  []taskVM // flattened: task headers + (when expanded) their revision children
	Sel   int      // index into Rows, NOT Tasks (a row may be a revision child)
	// KeepSelOnReload makes the next reloadTasks hold the cursor at its current index
	// instead of chasing the selected task by id. Set by user marks (approve/submit) so
	// the marked item leaves and the NEXT item slides up under the cursor — a clean path
	// down the list — without changing the async-insert behaviour (which still tracks the
	// task so a pushed item never yanks the reader's place).
	KeepSelOnReload bool
	RepoFilter      string
	Repos           []string

	// Expanded tracks which tasks are expanded into their revision children
	// (mail's thread-expand). Keyed by task id so it survives Rows rebuilds.
	Expanded map[int64]bool
	// TaskByID resolves the selected row's task without re-querying (rebuilt each
	// reloadTasks). Rows carry only a task ID; this maps back to the full db.Task.
	TaskByID map[int64]db.Task
	// PropByID resolves a proposal row's document the same way (proposal ids and
	// task ids are independent sequences, so proposal rows must never hit TaskByID).
	PropByID map[int64]db.Proposal

	Count                 int // number of pending (inbox) tasks — shown in the header
	CountText, FilterText string

	// selection-change bookkeeping: the watch loop compares these against the live
	// values each frame and refreshes the detail pane when they drift.
	LastSel, LastLen int
	LastFilter       string
	DetailDirty      bool
	// LastDiffKey identifies the diff currently shown (task:rev:sha). refreshDetail only
	// resets the diff scroll when this changes — so an inbox reload that adds an item but
	// leaves the selected task unchanged keeps the reader's scroll position.
	LastDiffKey string
	// DoneLimit caps how many completed items the inbox list renders; the rest
	// sit behind a "load more" row (avoids rendering a huge done history). The
	// done sort is last-completed-first, so the visible page is the most recent
	// activity. "load more" raises this by a batch.
	DoneLimit int
}

// inboxUI is the single instance the views bind against.
var inboxUI = inboxView{
	Expanded:  map[int64]bool{},
	TaskByID:  map[int64]db.Task{},
	PropByID:  map[int64]db.Proposal{},
	DoneLimit: 10,
}
