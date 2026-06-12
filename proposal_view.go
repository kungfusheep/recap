package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/config"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
	"github.com/kungfusheep/recap/todo"
)

// The proposal detail pane — the SAME aesthetic as the task diff/comments
// layout, but its own code top to bottom (c454): own layer + template + line
// meta + thread column + staged loader, so proposal features progress without
// touching the diff machinery (and vice versa). Nothing here writes diffUI or
// draftUI.

// propOpen caches the open-proposal count for the header badge — set by
// applyInbox alongside the rest of the reload's projections.
var propOpen int

type propLineMeta struct {
	Line        int    // 1-based SOURCE line in the stored document
	Text        string // the source line (trimmed) — the comment snippet
	Commentable bool
}

type propRowVM struct {
	Spans []Span
	BG    Color
}

// propView is the pane's state in one concrete struct: the document layer +
// row/meta projection, the pick machinery, and the thread column. One bound
// package instance (propUI) — stable var, pointer-bound into compiled views.
type propView struct {
	Active bool // a proposal row is selected → the middle column shows this pane

	// the document source, set at apply time; prepPropRows projects it into
	// Rows/Meta at the layer's width (content/size change only, never per frame).
	Prop    db.Proposal
	Parties []string

	Layer   *Layer
	Tmpl    *Template
	Rows    []propRowVM
	Meta    []propLineMeta // parallel to Rows: jump/anchor coordinates by row
	ViewRef NodeRef        // screen rect of the LayerView, for jump-target coords

	Commented  map[int]bool // source lines carrying a comment → gutter wash
	DocVersion int          // the rendered document's version (1 = original)

	Focused float64 // scrollbar fade target, mirrors the diff pane's cue
}

var propUI = propView{Commented: map[int]bool{}}

// --- staged loader -----------------------------------------------------------

// propResult is the staged fetch: raw db rows, projected thread VMs, washes.
type propResult struct {
	key        string
	reset      bool
	prop       db.Proposal
	docVersion int
	parties    []string
	comments   []db.TaskComment // projected for the SHARED comments pane (one component, two sources)
	washes     map[int]bool
}

var (
	propMu     sync.Mutex
	propStaged *propResult
)

func stageProp(r *propResult) {
	propMu.Lock()
	propStaged = r
	propMu.Unlock()
}

func takeStagedProp() *propResult {
	propMu.Lock()
	r := propStaged
	propStaged = nil
	propMu.Unlock()
	return r
}

// propDetailKick dispatches the proposal fetch — the staged hand-off seam,
// proposal-owned. A package var so tests can run it synchronously.
var propDetailKick = func(p db.Proposal, key string, reset bool) {
	app := uiApp // snapshot: the goroutine must not read the mutable global
	go func() {
		stageProp(fetchPropDetail(p, key, reset))
		if app != nil {
			app.RequestRender()
		}
	}()
}

// fetchPropDetail does the I/O off the render thread: re-reads the proposal
// (the row snapshot may be stale), its parties, and projects the thread.
func fetchPropDetail(p db.Proposal, key string, reset bool) *propResult {
	r := &propResult{key: key, reset: reset, prop: p, washes: map[int]bool{}}
	if uiStore == nil {
		return r
	}
	if fresh, err := uiStore.ProposalByID(p.ID); err == nil {
		r.prop = fresh
	}
	// the pane renders the document as it STANDS (latest revision, pc44)
	r.prop.Body, r.docVersion = uiStore.ProposalCurrentBody(r.prop)
	r.parties, _ = uiStore.ProposalParties(p.ID)
	comments, _ := uiStore.ProposalComments(p.ID)
	for _, c := range comments {
		tc := db.TaskComment{}
		tc.ID, tc.ParentID, tc.Body, tc.CreatedAt = c.ID, c.ParentID, c.Body, c.CreatedAt
		tc.Who = propSender(c.WhoName, c.WhoRepo) // "Name@repo" — the pane colours it by identity
		if c.WhoRepo == "" {
			tc.Who = "you" // the pane's own "You" treatment
		}
		if c.Line > 0 {
			tc.File, tc.Line, tc.Snippet = propDocFile, c.Line, c.Snippet
			r.washes[c.Line] = true
		}
		// receipts filled so no misleading unread dots (proposal comments have
		// no receipt protocol yet)
		tc.ReadAgent, tc.ReadUser = c.CreatedAt, c.CreatedAt
		r.comments = append(r.comments, tc)
	}
	return r
}

// propDocFile is the pseudo-file every document anchor uses — one document per
// proposal, so anchor locations read "document · line N".
const propDocFile = "document"

// drainPropDetail swaps a staged fetch into the pane — render thread, called
// from the staged-apply seam. A result keyed for a selection we've moved off
// is dropped (the newer kick's result is on its way).
func drainPropDetail() {
	r := takeStagedProp()
	if r == nil {
		return
	}
	if !propUI.Active || r.prop.ID != propUI.Prop.ID {
		return
	}
	propUI.Prop, propUI.Parties, propUI.DocVersion = r.prop, r.parties, r.docVersion
	propUI.Commented = r.washes
	// the SHARED comments pane carries the thread (the user's call on
	// todo:6d9eb05e — one component, two sources; the split pane was a
	// mistake). PropID routes its mutations at the proposal, not task tables.
	applyDraftComments(0, r.comments)
	draftUI.PropID = r.prop.ID
	if propUI.Layer != nil {
		if r.reset {
			propUI.Layer.ScrollToTop()
		}
		propUI.Layer.Invalidate()
	}
}

// openPropDetail switches the detail area to a proposal — called by
// refreshDetailNow when a proposal row is selected.
func openPropDetail(p db.Proposal, key string, reset bool) {
	propUI.Active = true
	propUI.Prop = p
	propDetailKick(p, key, reset)
}

// closePropDetail returns the detail area to task content.
func closePropDetail() {
	propUI.Active = false
	draftUI.PropID = 0 // the pane goes back to task comments
}

// --- document projection -----------------------------------------------------

// renderPropLayer mirrors renderDiffLayer for the document: project rows at
// the viewport width, register jump targets for commentable lines while a
// pick is live, blit, restore scroll.
func renderPropLayer() {
	w := propUI.Layer.ViewportWidth()
	if w <= 0 {
		return
	}
	prepPropRows(w - 2)

	h := len(propUI.Rows)
	if vh := propUI.Layer.ViewportHeight(); h < vh {
		h = vh
	}
	buf := NewBuffer(w, h)
	propTemplate().Execute(buf, int16(w), int16(h))

	if uiApp != nil && uiApp.JumpModeActive() {
		top, vh := propUI.Layer.ScrollY(), propUI.Layer.ViewportHeight()
		lblStyle := Style{FG: cBG, BG: cHunk, Attr: AttrBold}
		for y := top; y < top+vh && y < len(propUI.Meta); y++ {
			if !propUI.Meta[y].Commentable {
				continue
			}
			row := y
			sx, sy := propUI.ViewRef.X, propUI.ViewRef.Y+(y-top)
			uiApp.AddJumpTarget(int16(sx), int16(sy), func() {
				if row < len(propUI.Meta) {
					commentOnProposalLine(propUI.Meta[row])
				}
			}, lblStyle)
		}
	}

	scrollY := propUI.Layer.ScrollY()
	propUI.Layer.SetBuffer(buf)
	propUI.Layer.ScrollTo(scrollY)
}

func propTemplate() *Template {
	if propUI.Tmpl == nil {
		propUI.Tmpl = Build(VBox.Fill(&cBG).Gap(0)(
			ForEach(&propUI.Rows, func(r *propRowVM) Component {
				return HBox.Fill(&r.BG)(Rich(&r.Spans).CharWrap())
			}),
		))
	}
	return propUI.Tmpl
}

// prepPropRows projects the stored document into Rows/Meta at width w — pure
// data prep, runs on content/size change only.
func prepPropRows(w int) {
	propUI.Rows = propUI.Rows[:0]
	propUI.Meta = propUI.Meta[:0]
	put := func(m propLineMeta, spans []Span) {
		bg := cBG
		if m.Line > 0 && propUI.Commented[m.Line] {
			// commented document lines carry the diff pane's FAINT wash — and
			// the spans take it too, else text cells punch default-bg holes in
			// the band (the c467 artifact; cFileHdrBG here read as broken bands).
			bg = cCommentBG
			for i := range spans {
				spans[i].Style.BG = cCommentBG
			}
		}
		propUI.Rows = append(propUI.Rows, propRowVM{Spans: spans, BG: bg})
		propUI.Meta = append(propUI.Meta, m)
	}
	p := propUI.Prop
	put(propLineMeta{}, []Span{
		span(fmt.Sprintf("proposal #%d", p.ID), cHunk, true),
		span("  ·  ", cMuted, false),
		span(strings.ToUpper(p.Status), proposalStatusColor(p.Status), true),
	})
	metaSpans := []Span{
		span(fmt.Sprintf("%s → %s", propSender(p.ProposerWho, p.ProposerRepo), p.TargetRepo), cMuted, false),
		span("   parties: "+strings.Join(propUI.Parties, ", "), cSubtle, false),
	}
	if propUI.DocVersion > 1 {
		metaSpans = append(metaSpans, span(fmt.Sprintf("   doc rev %d", propUI.DocVersion), cHunk, false))
	}
	put(propLineMeta{}, metaSpans)
	put(propLineMeta{}, []Span{})
	rows, meta := propBodyRows(p.Body, w)
	for i := range rows {
		put(meta[i], rows[i])
	}
	put(propLineMeta{}, []Span{})
}

// propBodyRows renders the document: `#` headings read bold, ``` fence content
// renders raw (no markup, indentation preserved), everything else goes through
// the briefing markup + wrap task summaries use. Every rendered row's meta
// carries the 1-based SOURCE line it came from (a wrapped line yields several
// rows with the same anchor); non-empty content rows are commentable.
func propBodyRows(text string, width int) ([][]Span, []propLineMeta) {
	if width > 72 {
		width = 72 // briefing measure: long lines wrap at the summary width
	}
	var rows [][]Span
	var meta []propLineMeta
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		m := propLineMeta{Line: i + 1, Text: trimmed, Commentable: trimmed != ""}
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

// --- pane components ---------------------------------------------------------

// propDocPane is the middle column's content while a proposal is selected —
// the document layer + flush-right scrollbar, the diff pane's structure.
func propDocPane() Component {
	return HBox.Grow(1).NodeRef(&propUI.ViewRef)(
		LayerView(propUI.Layer).Grow(1),
		ScrollbarForLayer(propUI.Layer).
			TrackStyle(&scrollTrackStyle).
			ThumbStyle(&scrollThumbStyle).
			Opacity(Animate(&propUI.Focused)),
	)
}

// --- comment actions ---------------------------------------------------------

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

// commentOnProposalLine is the jump-pick action over a document row: the
// comment anchors to the picked SOURCE line.
func commentOnProposalLine(m propLineMeta) {
	p := propUI.Prop
	if p.ID == 0 {
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
	saveProposalThreadComment(p, line, snippet, 0)
}

// saveProposalReply threads the prompt's body under a parent comment.
func saveProposalReply(p db.Proposal, parentID int64) {
	saveProposalThreadComment(p, 0, "", parentID)
}

func saveProposalThreadComment(p db.Proposal, line int, snippet string, parentID int64) {
	body := promptUI.Field.Value
	if body == "" {
		return
	}
	if _, err := uiStore.AddProposalThreadComment(p.ID, "", "you", body, line, snippet, parentID); err != nil {
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

// openPropLineComment enters the jump pick over the document's commentable
// rows — the diff pane's `c`, proposal-owned.
func openPropLineComment() {
	any := false
	for _, m := range propUI.Meta {
		if m.Commentable {
			any = true
			break
		}
	}
	if !any {
		toast("(no document lines to comment on)")
		return
	}
	uiApp.EnterJumpMode()
}

// --- sign-off (slice 4) --------------------------------------------------------
//
// Verdicts stay human: a/X on a proposal row in the TUI. Approval MATERIALISES
// the decision — an ADR in the target repo (docs/adr/<proposal-id>-<slug>.md;
// the proposal id IS the ADR number, recap-global, gaps fine) and an
// implementation todo on the TARGET repo's queue (its loop is the managing
// agent). Decline records the verdict and tells the parties; no artifacts.

// signOffProposal CONFIRMS before deciding (the P7 lesson: a verdict is
// irreversible and materialises artifacts — it must never ride a single
// keypress; the human once approved a proposal while meaning to agree with
// the thread's withdrawal recommendation).
func signOffProposal(row *taskVM, verdict string) {
	p, ok := inboxUI.PropByID[row.ID]
	if !ok {
		return
	}
	word := "APPROVE"
	if verdict == db.ProposalDeclined {
		word = "DECLINE"
	}
	promptUI.open(
		fmt.Sprintf("%s proposal #%d? type y to confirm", word, p.ID),
		"", p.Title, "",
		func() {
			v := strings.ToLower(strings.TrimSpace(promptUI.Field.Value))
			if v != "y" && v != "yes" {
				toast("sign-off cancelled")
				return
			}
			decideProposal(p, verdict)
		},
	)
}

// decideProposal is the confirmed verdict: record it and materialise.
func decideProposal(p db.Proposal, verdict string) {
	if err := uiStore.DecideProposal(p.ID, verdict); err != nil {
		toast("sign-off: " + err.Error())
		return
	}
	msg := fmt.Sprintf("proposal #%d (%q) DECLINED — thread: recap proposal show %d", p.ID, p.Title, p.ID)
	if verdict == db.ProposalApproved {
		msg = fmt.Sprintf("proposal #%d (%q) APPROVED", p.ID, p.Title)
		if rel, err := writeProposalADR(p); err != nil {
			toast("ADR not written: " + err.Error())
		} else {
			msg += " — ADR " + rel
		}
		if err := queueProposalTodo(p); err != nil {
			toast("todo not queued: " + err.Error())
		} else {
			msg += fmt.Sprintf(", implementation todo queued on %s", p.TargetRepo)
		}
	}
	// a verdict is a TERMINAL event — every party hears it directly (a plain
	// message, not the digest ping, so it can never coalesce away).
	parties, _ := uiStore.ProposalParties(p.ID)
	for _, r := range parties {
		if r == "" {
			continue
		}
		_, _ = uiStore.SendMessage("", "you", r, 0, 0, msg)
	}
	notify.Reload()
	toast(msg)
	inboxUI.KeepSelOnReload = true // the decided proposal leaves the open list
	reloadTasks()
}

// writeProposalADR materialises the decision record in the TARGET repo.
func writeProposalADR(p db.Proposal) (string, error) {
	rp, err := uiStore.RepoPathFor(p.TargetRepo)
	if err != nil {
		return "", err
	}
	rel := filepath.Join("docs", "adr", fmt.Sprintf("%d-%s.md", p.ID, slugify(p.Title)))
	full := filepath.Join(rp, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	comments, _ := uiStore.ProposalComments(p.ID)
	parties, _ := uiStore.ProposalParties(p.ID)
	// the LATEST document version is the record (versioning, pc44)
	docBody, _ := uiStore.ProposalCurrentBody(p)
	p.Body = docBody
	var b strings.Builder
	fmt.Fprintf(&b, "# ADR %d: %s\n\n", p.ID, p.Title)
	fmt.Fprintf(&b, "- status: accepted\n- date: %s\n", db.NowStamp())
	fmt.Fprintf(&b, "- proposer: %s\n", propSender(p.ProposerWho, p.ProposerRepo))
	fmt.Fprintf(&b, "- parties: %s\n", strings.Join(parties, ", "))
	fmt.Fprintf(&b, "- deliberation: recap proposal show %d (%d comments)\n\n", p.ID, len(comments))
	body := stripDocStatusLines(p.Body)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// stripDocStatusLines drops "status:" lines from the document's PREAMBLE (the
// run of blank/heading/metadata lines at the top) — the proposal row owns the
// lifecycle, so an embedded "status: proposed" under the generated "status:
// accepted" header read as a contradiction (m192, the human's ADR-1 review).
func stripDocStatusLines(body string) string {
	lines := strings.Split(body, "\n")
	out := lines[:0]
	preamble := true
	for _, ln := range lines {
		t := strings.ToLower(strings.TrimSpace(ln))
		if preamble {
			switch {
			case strings.HasPrefix(t, "status:"):
				continue // the lifecycle line the materialiser owns
			case t == "" || strings.HasPrefix(t, "#") || regexpMetaLine(t):
				// still in the preamble
			default:
				preamble = false
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// regexpMetaLine reports a "key: value" metadata-shaped preamble line.
func regexpMetaLine(t string) bool {
	i := strings.Index(t, ":")
	if i <= 0 || i > 24 {
		return false
	}
	for _, r := range t[:i] {
		if !(r >= 'a' && r <= 'z' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// queueProposalTodo appends the implementation item to the TARGET repo's TODO —
// the managing agent's loop picks it up through its normal todo tier.
func queueProposalTodo(p db.Proposal) error {
	rp, err := uiStore.RepoPathFor(p.TargetRepo)
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	path, err := todo.PathFor(cfg.TODOTemplate, rp)
	if err != nil || path == "" {
		return fmt.Errorf("no TODO path resolves for %s", p.TargetRepo)
	}
	items, err := todo.Read(path)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("implement approved proposal #%d: %s (ADR docs/adr/%d-%s.md)", p.ID, p.Title, p.ID, slugify(p.Title))
	return todo.Write(path, todo.Add(items, text))
}

// slugify reduces a title to a filename slug.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	return out
}
