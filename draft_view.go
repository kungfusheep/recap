package main

import (
	"fmt"
	"sort"
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/links"
)

// The comments (draft) pane: the right-hand column's state, row VMs, projection,
// fold machinery, and handlers — extracted from ui.go in the slice-6 per-struct
// file pass (service-principles layout).

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
	WhoLabel string // the author leading the row — "You" or the agent's name (c460); empty on replies (↳ who covers it)
	WhoColor Color
	Indent   string // leading spaces for nested replies (precomputed; build-once safe)
	When     string // comment time (HH:MM) from CreatedAt
	Snippet  string // the diff line commented on (may be empty)
	FoldCue  string // "▸ N replies" on a collapsed thread root (empty = expanded/no replies)
	Body     string
	File     string
	Line     int
	Draft    bool // on the open draft (editable); else submitted (read-only)
	Visible  bool // false while this row's thread root is collapsed — the template's If skips it
	Reply    bool // a nested reply row (depth > 0); rows are in thread order, so a thread = a root + its run of Reply rows
}

// draftView is the comments (draft) pane's state in one concrete struct (the
// cohesive-structs pattern, like todoView/promptView): the row VMs + selection,
// the focus-aware band + scrollbar state, the column gate, and the read-overlay
// content. One bound package instance (draftUI) — fields are pointer-bound into
// the compiled view, so the struct must be a stable package var.
type draftView struct {
	Comments []draftCommentVM
	Sel      int
	LastSel  int // selection watermark: re-sync the diff highlight on change

	ScrollOffset, ScrollVisible, ScrollTotal int // List ScrollState → ScrollbarDyn

	Focused float64 // scrollbar fade target: 1 while the column has focus
	PaneRef NodeRef
	Has     bool   // gates the whole column (any comments on the selected task)
	Note    string // header pill (e.g. draft count)

	// read-overlay content (rendered by prompt.go's readCommentOverlay)
	ViewLoc  string
	ViewSnip string
	ViewBody []string

	EditingID  int64 // comment being edited via the prompt
	ReplyingTo int64 // parent comment for a reply in flight

	TaskID int64 // the task whose comments are loaded (for in-place reloads)
	// Collapsed thread roots ('o'): the root row stays with a "▸ N replies" cue,
	// its reply rows are dropped from Comments. Keyed by comment id so it
	// survives reloads while the task's pane stays open.
	Collapsed map[int64]bool
}

// draftUI is the single instance the view tree binds against.
var draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}

// draftSelStyle is the comments List's selection band — a *Style binding, so
// List re-reads it every frame. applyPaneFocus mutates the BG (bright while the
// column has focus, dim elsewhere): the List owns selection painting; recap
// stopped hand-painting per-row Selected flags here (glyph audit m69 item 2).
var draftSelStyle = Style{BG: cFloat}

// loadDraftPane refreshes the draft-review overview for a task synchronously —
// the HANDLER-side composition (handlers acquire; the render path goes through
// fetchDetail/applyDetail instead).
func loadDraftPane(taskID int64) {
	cs, _ := uiStore.TaskReviewComments(taskID)
	applyDraftComments(taskID, cs)
}

// applyDraftComments projects a task's comments into the pane's row VMs — render
// thread, no I/O. Shows ALL review comments on the task — not just the open
// draft — so feedback stays visible after submit. Each row knows if it's still
// an editable draft.
func applyDraftComments(taskID int64, cs []db.TaskComment) {
	draftUI.TaskID = taskID
	draftUI.Comments = nil
	for k := range diffUI.Commented {
		delete(diffUI.Commented, k)
	}
	if len(cs) == 0 {
		draftUI.Has, draftUI.Note = false, ""
		return
	}
	draftUI.Has = true
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
			diffUI.Commented[lineKey(c.File, c.Line)] = true
		} else {
			vm.Location = "general"
		}
		if c.Snippet != "" {
			vm.Snippet = cleanLine(c.Snippet)
		}
		vm.LocColor = cSubtle
		// every row leads with its author (c460), like the proposal thread:
		// "You" for the human, the agent's name in its colour otherwise.
		vm.WhoLabel, vm.WhoColor = dash(c.Who), agentColor
		if c.Who == "you" {
			vm.WhoLabel, vm.WhoColor = "You", cBright
		}
		draftUI.Comments = append(draftUI.Comments, vm)
	}
	// header reflects draft-in-progress vs settled comments.
	if drafts > 0 {
		draftUI.Note = fmt.Sprintf("✎ %d draft", drafts)
	} else {
		draftUI.Note = fmt.Sprintf("%d comment%s", len(cs), plural(len(cs)))
	}
	if draftUI.Sel >= len(draftUI.Comments) {
		draftUI.Sel = len(draftUI.Comments) - 1
	}
	if draftUI.Sel < 0 {
		draftUI.Sel = 0
	}
	// order top-level comments (general first, then anchored by file:line) with each
	// reply nested under its parent.
	draftUI.Comments = threadComments(draftUI.Comments)
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
			v.Reply = true
			v.Location = "↳ " + dash(v.Who)
			v.WhoLabel = "" // the reply label carries the author already
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
	// every row is ALWAYS in the slice — folding only flips per-row Visible
	// flags; the template's If(&row.Visible) decides what renders.
	setFoldFlags(out)
	return out
}

// setFoldFlags syncs each row's Visible flag + the roots' "▸ N replies" cues to
// draftUI.Collapsed, in place. The rows are in THREAD ORDER (a root, then its run
// of Reply rows), so one pass does it: remember the current root, hide its run
// when collapsed, cue the root with the run's length.
func setFoldFlags(vms []draftCommentVM) {
	root, replies := -1, 0
	flush := func() { // finish the previous thread: cue its root if collapsed
		if root >= 0 && replies > 0 && draftUI.Collapsed[vms[root].ID] {
			vms[root].FoldCue = fmt.Sprintf("▸ %d repl%s", replies, map[bool]string{true: "y", false: "ies"}[replies == 1])
		}
	}
	for i := range vms {
		if !vms[i].Reply { // a new thread root
			flush()
			root, replies = i, 0
			vms[i].Visible, vms[i].FoldCue = true, ""
			continue
		}
		replies++
		vms[i].Visible = !draftUI.Collapsed[vms[root].ID]
		vms[i].FoldCue = ""
	}
	flush()
}

// toggleCommentThread ('o' in the comments pane) collapses/expands the selected
// row's thread: the root stays with a "▸ N replies" cue, replies hide. Selecting
// a reply folds its whole thread (selection lands back on the root). Folding is
// pure viewing state — it flips flags on the rows in place; the data never moves.
// collapseAllCommentThreads folds/unfolds every thread in one stroke (Z) —
// the diff pane's fold-all, for comments. Toggle semantics match foldAllFiles:
// if every reply-bearing root is collapsed, expand all; else collapse all.
func collapseAllCommentThreads() {
	var roots []int64
	for i := range draftUI.Comments {
		if !draftUI.Comments[i].Reply && i+1 < len(draftUI.Comments) && draftUI.Comments[i+1].Reply {
			roots = append(roots, draftUI.Comments[i].ID)
		}
	}
	if len(roots) == 0 {
		return
	}
	all := true
	for _, id := range roots {
		if !draftUI.Collapsed[id] {
			all = false
			break
		}
	}
	for _, id := range roots {
		draftUI.Collapsed[id] = !all
	}
	setFoldFlags(draftUI.Comments)
	// park the selection on its thread root so it never sits on a hidden row
	root := draftUI.Sel
	for root > 0 && root < len(draftUI.Comments) && draftUI.Comments[root].Reply {
		root--
	}
	if root >= 0 && root < len(draftUI.Comments) {
		draftUI.Sel = root
	}
	onDraftSelChanged()
}

func toggleCommentThread() {
	if draftUI.Sel < 0 || draftUI.Sel >= len(draftUI.Comments) {
		return
	}
	// thread order again: the selected row's root is the nearest non-reply at or
	// above the cursor.
	root := draftUI.Sel
	for root > 0 && draftUI.Comments[root].Reply {
		root--
	}
	draftUI.Collapsed[draftUI.Comments[root].ID] = !draftUI.Collapsed[draftUI.Comments[root].ID]
	setFoldFlags(draftUI.Comments)
	draftUI.Sel = root
	onDraftSelChanged()
}

// draftRow renders one draft comment in the inbox's visual style: a filled card
// (selection-aware, accent bar) with the location, the snippet, then the note.
func draftRow(c *draftCommentVM) Component {
	// the List owns the selection band (SelectedStyle(&draftSelStyle), focus-aware
	// via applyPaneFocus) — the row paints content only.
	// the row set never changes on fold — collapsing a thread flips Visible and
	// the template chooses here, via control flow, whether to render the row.
	// The If lives INSIDE a concrete root container: List measures/positions the
	// row root directly, so an If at the root renders nothing; padding rides the
	// If branch so a hidden row truly occupies zero height.
	// Indent (precomputed per row) nests replies; empty for top-level comments.
	return VBox(If(&c.Visible).Then(VBox.PaddingVH(1, 1)(
		// one read-receipt dot: has the OTHER party read this? (● read / ○ unread)
		HBox(Text(&c.Indent), Text(&c.ReadDot).FG(&cHunk), SpaceW(1),
			If(&c.WhoLabel).Then(HBox(Text(&c.WhoLabel).FG(&c.WhoColor).Bold(), SpaceW(2))),
			Text(&c.Location).FG(&c.LocColor),
			If(&c.FoldCue).Then(HBox(SpaceW(2), Text(&c.FoldCue).FG(&cMuted))),
			Space(), Text(&c.When).FG(&cMuted)),
		If(&c.Snippet).Then(Text(&c.Snippet).FG(&cMuted)),
		// TextBlock must be bounded to the width LEFT after the indent, else it wraps to
		// the full column and the indent shoves it off the right edge (worse the deeper a
		// reply nests). Grow(1) gives it exactly the remaining column width to wrap into.
		HBox(Text(&c.Indent), VBox.Grow(1)(TextBlock(&c.Body).FG(&cFG))),
		// the agent's reaction sits below the body, attributed to the agent's name in
		// its personal colour (Text, not TextBlock, so the emoji renders cleanly).
		If(&c.HasEmote).Then(HBox(Text(&c.Indent), Text(&c.Emote), SpaceW(1), Text(&agentLabel).FG(&agentColor))),
	)))
}

func moveDraft(d int) {
	// step over rows hidden by a collapsed thread (Visible=false renders nothing);
	// stay put if no visible row exists in that direction.
	for i := draftUI.Sel + d; i >= 0 && i < len(draftUI.Comments); i += d {
		if draftUI.Comments[i].Visible {
			draftUI.Sel = i
			onDraftSelChanged()
			return
		}
	}
}

// onDraftSelChanged runs a comments-pane selection change's consequences: row
// flags, the diff sync, and the read receipt. The handler that moves the
// selection calls this; nothing polls for the move.
func onDraftSelChanged() {
	if draftUI.Sel != draftUI.LastSel {
		draftUI.LastSel = draftUI.Sel
		syncDiffToDraft()
		markSelectedCommentRead()
	}
}

// selectedDraft returns the comment under the draft cursor, or nil.
func selectedDraft() *draftCommentVM {
	if draftUI.Sel < 0 || draftUI.Sel >= len(draftUI.Comments) {
		return nil
	}
	return &draftUI.Comments[draftUI.Sel]
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
	draftUI.EditingID = c.ID
	draftUI.ViewLoc = c.Location
	draftUI.ViewSnip = c.Snippet
	draftUI.ViewBody = wrapText(c.Body, 66)
	promptUI.openRead()
}

// replyToComment opens the body prompt to reply to the selected comment; saving
// threads the reply under it (who="you", the reviewer). Works on any comment,
// submitted or draft — replies are discussion, not edits.
func replyToComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	draftUI.ReplyingTo = c.ID
	promptUI.open("reply", c.Location, "  "+c.Body, "", saveReply)
}

func saveReply() {
	body := strings.TrimSpace(promptUI.Field.Value)
	if draftUI.ReplyingTo == 0 || body == "" {
		return
	}
	if _, err := uiStore.AddReply(draftUI.ReplyingTo, "you", body); err != nil {
		toast("error: " + err.Error())
		return
	}
	toast("replied")
	inboxUI.DetailDirty = true
	refreshDetailNow()
}

// editDraftComment opens the body prompt pre-filled with the selected comment's
// text; saving calls UpdateComment.
func editDraftComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	// your own comments are editable draft OR submitted (editing submitted
	// feedback clears the agent's read receipt so the change re-enters its
	// queue); the agent's comments are read-only — the record stays honest.
	if c.Who != "you" {
		toast("the agent's comments are read-only")
		return
	}
	draftUI.EditingID = c.ID
	promptUI.open("edit comment", "", "", c.Body, saveEditedComment)
}

func deleteDraftComment() {
	c := selectedDraft()
	if c == nil {
		return
	}
	if !c.Draft {
		toast("submitted comments are read-only (unsubmit with U to edit)")
		return
	}
	if err := uiStore.DeleteComment(c.ID); err != nil {
		toast("error: " + err.Error())
		return
	}
	toast("comment deleted")
	if draftUI.Sel > 0 {
		draftUI.Sel--
	}
	inboxUI.DetailDirty = true
	refreshDetailNow()
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
		toast("no [[file]] links in this comment")
		return
	}
	n := links.Open(c.Body)
	toast(fmt.Sprintf("opened %d/%d link(s)", n, len(refs)))
}
