package main

import (
	"fmt"
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
)

// Proposals in the inbox (slice 3 of docs/proposal-workflow.md): open proposals
// lead the list as a PROPOSALS section — documents awaiting the human's verdict
// sit above the task queue. Selecting one renders the document + deliberation
// thread in the detail pane (no diff — the document IS the detail), and `c`
// threads a human comment, pinging every party through the digest model.

// propOpen caches the open-proposal count for the header badge — set by
// applyInbox alongside the rest of the reload's projections.
var propOpen int

// propDetailKick dispatches the proposal document fetch — the same staged
// hand-off seam as the task detail loader (fetch on a goroutine, stage, swap at
// frame top). A package var so tests can run it synchronously.
var propDetailKick = func(p db.Proposal, key string, reset bool) {
	app := uiApp // snapshot: the goroutine must not read the mutable global
	go func() {
		stageDetail(fetchProposalDetail(p, key, reset))
		if app != nil {
			app.RequestRender()
		}
	}()
}

// fetchProposalDetail does the I/O off the render thread: re-reads the proposal
// (the row snapshot may have gone stale since the reload), renders the DOCUMENT
// as line-commentable banner rows, and projects the deliberation thread into
// the standard comments pane (c447: the diff layout's comment machinery works
// here too — line anchors, washes, the right-hand thread column).
func fetchProposalDetail(p db.Proposal, key string, reset bool) *detailResult {
	r := &detailResult{key: key, reset: reset, propID: p.ID}
	if uiStore == nil {
		return r
	}
	if fresh, err := uiStore.ProposalByID(p.ID); err == nil {
		p = fresh
	}
	parties, _ := uiStore.ProposalParties(p.ID)
	comments, _ := uiStore.ProposalComments(p.ID)
	r.banner, r.bannerMeta = proposalDoc(p, parties)
	r.filesText = fmt.Sprintf("proposal #%d  ·  %d comment(s)", p.ID, len(comments))
	// the thread rides the comments pane: synthesize TaskComment rows (read
	// flags filled so no misleading unread dots; mutations are gated by PropID).
	for _, c := range comments {
		tc := db.TaskComment{}
		tc.ID, tc.Who, tc.Body, tc.Line, tc.Snippet, tc.CreatedAt = c.ID, propSender(c.WhoName, c.WhoRepo), c.Body, c.Line, c.Snippet, c.CreatedAt
		if c.WhoRepo == "" && c.WhoName == "you" {
			tc.Who = "you"
		}
		if c.Line > 0 {
			tc.File = propDocFile // matches the document rows' meta → wash lights up
		}
		tc.ReadAgent, tc.ReadUser = c.CreatedAt, c.CreatedAt
		r.comments = append(r.comments, tc)
	}
	return r
}

// propDocFile is the pseudo-file every document anchor uses — one document per
// proposal, so wash/anchor keys are "document:<line>".
const propDocFile = "document"

func proposalStatusColor(status string) Color {
	switch status {
	case db.ProposalApproved:
		return cAdd
	case db.ProposalDeclined:
		return cDel
	default:
		return cHunk
	}
}

// proposalDoc renders the proposal detail: a meta header, then the document
// body through the briefing markup (plus headings + code fences, which
// proposals use and task summaries don't). The parallel meta slice anchors
// every body row to its SOURCE line in p.Body, so jump-pick line comments
// land on stable line numbers however the text wraps. The deliberation
// thread is NOT here — it rides the standard comments pane (c447).
func proposalDoc(p db.Proposal, parties []string) ([][]Span, []diffLineMeta) {
	var rows [][]Span
	var meta []diffLineMeta
	put := func(m diffLineMeta, r []Span) {
		rows = append(rows, r)
		meta = append(meta, m)
	}
	put(diffLineMeta{}, []Span{
		span(fmt.Sprintf("proposal #%d", p.ID), cHunk, true),
		span("  ·  ", cMuted, false),
		span(strings.ToUpper(p.Status), proposalStatusColor(p.Status), true),
	})
	put(diffLineMeta{}, []Span{
		span(fmt.Sprintf("%s → %s", propSender(p.ProposerWho, p.ProposerRepo), p.TargetRepo), cMuted, false),
		span("   parties: "+strings.Join(parties, ", "), cSubtle, false),
	})
	put(diffLineMeta{}, []Span{})
	bodyRows, bodyMeta := propBodyRows(p.Body, 72)
	rows = append(rows, bodyRows...)
	meta = append(meta, bodyMeta...)
	put(diffLineMeta{}, []Span{})
	return rows, meta
}

// propSender formats "who@repo", collapsing to just the name when there's no
// repo (the human's comments).
func propSender(who, repo string) string {
	if who == "" {
		who = "agent"
	}
	if repo == "" {
		return who
	}
	return who + "@" + repo
}

// propBodyRows renders a proposal document: `#` headings read bold, ``` fence
// content renders raw (no markup, indentation preserved), everything else goes
// through the briefing markup + wrap that task summaries use. Every rendered
// row's meta carries the 1-based SOURCE line it came from (a wrapped line
// yields several rows with the same anchor); non-empty content rows are
// commentable.
func propBodyRows(text string, width int) ([][]Span, []diffLineMeta) {
	var rows [][]Span
	var meta []diffLineMeta
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		m := diffLineMeta{File: propDocFile, Line: i + 1, Text: trimmed, Commentable: trimmed != ""}
		put := func(r []Span) {
			rows = append(rows, r)
			meta = append(meta, m)
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			put([]Span{span("  "+strings.TrimRight(line, " "), cSubtle, false)})
			continue
		}
		if trimmed == "" {
			m.Commentable = false
			put([]Span{})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			put([]Span{span(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), cBright, true)})
			continue
		}
		for _, w := range summaryBody(line, width) {
			put(w)
		}
	}
	return rows, meta
}

// commentOnProposal threads a general human comment onto the selected proposal
// and pings every party — the digest model: one unread attention message per
// party, no spam if one's already pending.
func commentOnProposal(row *taskVM) {
	p, ok := inboxUI.PropByID[row.ID]
	if !ok {
		return
	}
	promptUI.open(
		fmt.Sprintf("comment on proposal #%d", p.ID),
		"", p.Title, "",
		func() {
			saveProposalComment(p, 0, "")
		},
	)
}

// commentOnProposalLine is the jump-pick action over a proposal document row
// (the diff pane's `c`, same pick engine): the comment anchors to the picked
// SOURCE line of the document.
func commentOnProposalLine(m diffLineMeta) {
	row := selectedRow()
	if row == nil || !row.Proposal {
		return
	}
	p, ok := inboxUI.PropByID[row.ID]
	if !ok {
		return
	}
	snip := "  " + m.Text
	if len(snip) > 68 {
		snip = snip[:67] + "…"
	}
	promptUI.open(
		fmt.Sprintf("line comment · proposal #%d", p.ID),
		fmt.Sprintf("document · line %d", m.Line), snip, "",
		func() {
			saveProposalComment(p, m.Line, m.Text)
		},
	)
}

// saveProposalComment is the shared save: thread the comment (anchored when
// line > 0), ping every party once, and refresh the detail in place.
func saveProposalComment(p db.Proposal, line int, snippet string) {
	body := promptUI.Field.Value
	if body == "" {
		return
	}
	if _, err := uiStore.AddProposalLineComment(p.ID, "", "you", body, line, snippet); err != nil {
		toast("comment: " + err.Error())
		return
	}
	parties, _ := uiStore.ProposalParties(p.ID)
	for _, r := range parties {
		if r == "" {
			continue
		}
		_ = uiStore.SendProposalPing(p.ID, "", "you", r,
			fmt.Sprintf("proposal #%d has new comments: %q — recap proposal show %d", p.ID, p.Title, p.ID))
	}
	notify.Reload() // wakes any party parked on --wait
	inboxUI.DetailDirty = true
	refreshDetailNow()
	toast(fmt.Sprintf("commented on proposal #%d", p.ID))
}
