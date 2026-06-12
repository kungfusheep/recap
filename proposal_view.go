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
// (the row snapshot may have gone stale since the reload), its parties and
// thread, and renders everything as banner spans.
func fetchProposalDetail(p db.Proposal, key string, reset bool) *detailResult {
	r := &detailResult{key: key, reset: reset}
	if uiStore == nil {
		return r
	}
	if fresh, err := uiStore.ProposalByID(p.ID); err == nil {
		p = fresh
	}
	parties, _ := uiStore.ProposalParties(p.ID)
	comments, _ := uiStore.ProposalComments(p.ID)
	r.banner = proposalDoc(p, parties, comments)
	r.filesText = fmt.Sprintf("proposal #%d  ·  %d comment(s)", p.ID, len(comments))
	return r
}

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

// proposalDoc renders the full proposal detail: a meta header, the document
// body through the briefing markup (plus headings + code fences, which
// proposals use and task summaries don't), then the deliberation thread with
// each commenter in their identity colour.
func proposalDoc(p db.Proposal, parties []string, comments []db.ProposalComment) [][]Span {
	var rows [][]Span
	rows = append(rows, []Span{
		span(fmt.Sprintf("proposal #%d", p.ID), cHunk, true),
		span("  ·  ", cMuted, false),
		span(strings.ToUpper(p.Status), proposalStatusColor(p.Status), true),
	})
	rows = append(rows, []Span{
		span(fmt.Sprintf("%s → %s", propSender(p.ProposerWho, p.ProposerRepo), p.TargetRepo), cMuted, false),
		span("   parties: "+strings.Join(parties, ", "), cSubtle, false),
	})
	rows = append(rows, []Span{})
	rows = append(rows, propBodySpans(p.Body, 72)...)
	if len(comments) > 0 {
		rows = append(rows, []Span{}, []Span{span(fmt.Sprintf("thread (%d)", len(comments)), cHunk, true)})
		for _, c := range comments {
			color := cBright
			if c.WhoRepo != "" {
				if _, ic := loadIdentity(c.WhoRepo); ic.Mode != 0 {
					color = ic
				}
			}
			rows = append(rows, []Span{})
			rows = append(rows, []Span{
				span(propSender(c.WhoName, c.WhoRepo), color, true),
				span("  "+c.CreatedAt, cMuted, false),
			})
			rows = append(rows, summaryBody(c.Body, 72)...)
		}
	}
	rows = append(rows, []Span{})
	return rows
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

// propBodySpans renders a proposal document: `#` headings read bold, ``` fence
// content renders raw (no markup, indentation preserved), everything else goes
// through the briefing markup + wrap that task summaries use.
func propBodySpans(text string, width int) [][]Span {
	var rows [][]Span
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			rows = append(rows, []Span{span("  "+strings.TrimRight(line, " "), cSubtle, false)})
			continue
		}
		if trimmed == "" {
			rows = append(rows, []Span{})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			rows = append(rows, []Span{span(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), cBright, true)})
			continue
		}
		rows = append(rows, summaryBody(line, width)...)
	}
	return rows
}

// commentOnProposal threads a human comment onto the selected proposal and
// pings every party — the digest model: one unread attention message per party,
// no spam if one's already pending.
func commentOnProposal(row *taskVM) {
	p, ok := inboxUI.PropByID[row.ID]
	if !ok {
		return
	}
	promptUI.open(
		fmt.Sprintf("comment on proposal #%d", p.ID),
		"", p.Title, "",
		func() {
			body := promptUI.Field.Value
			if body == "" {
				return
			}
			if _, err := uiStore.AddProposalComment(p.ID, "", "you", body); err != nil {
				statusMsg = "comment: " + err.Error()
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
			reloadTasks()
			statusMsg = fmt.Sprintf("commented on proposal #%d", p.ID)
		},
	)
}
