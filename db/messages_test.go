package db

import (
	"path/filepath"
	"testing"
)

func testStoreDB(t *testing.T) *Store {
	t.Helper()
	st, err := OpenAt(filepath.Join(t.TempDir(), "recap.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// messages queue durably for a REPO: sending requires no listener; the target's
// unread query surfaces them FIFO; the agent read-receipt clears them; the human
// ledger and the cross-repo badge count see everything.
func TestMessageLifecycle(t *testing.T) {
	st := testStoreDB(t)

	id1, err := st.SendMessage("recap", "Kestrel", "glyph", 0, 0, "riffkey grew AllowNewlines — bump your go.mod after push")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	id2, _ := st.SendMessage("recap", "Kestrel", "glyph", 0, 0, "second note")
	st.SendMessage("recap", "Kestrel", "mail", 0, 0, "unrelated repo")

	un, err := st.UnreadMessages("glyph")
	if err != nil || len(un) != 2 {
		t.Fatalf("glyph unread = %d (%v), want 2", len(un), err)
	}
	if un[0].ID != id1 || un[1].ID != id2 {
		t.Fatalf("unread not FIFO: %+v", un)
	}
	if un[0].FromWho != "Kestrel" || un[0].FromRepo != "recap" {
		t.Fatalf("provenance lost: %+v", un[0])
	}

	// cross-repo badge counts every unread
	if n, _ := st.UnreadMessageCount(); n != 3 {
		t.Fatalf("badge count = %d, want 3", n)
	}

	// agent read clears it from the queue
	if err := st.MarkMessageReadAgent(id1); err != nil {
		t.Fatalf("read: %v", err)
	}
	if un, _ = st.UnreadMessages("glyph"); len(un) != 1 || un[0].ID != id2 {
		t.Fatalf("after read, unread = %+v", un)
	}

	// the ledger shows both directions for a repo
	ms, _ := st.Messages("recap")
	if len(ms) != 3 { // recap sent all three
		t.Fatalf("recap ledger = %d, want 3", len(ms))
	}
	if ms[0].ReadAgent == "" {
		t.Fatal("ledger should show the read receipt on m1")
	}

	// replies thread under a parent; replying to a ghost errors
	rid, err := st.SendMessage("glyph", "Wren", "recap", id1, 0, "bumped, thanks")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	rs, _ := st.UnreadMessages("recap")
	if len(rs) != 1 || rs[0].ID != rid || rs[0].ParentID != id1 {
		t.Fatalf("threaded reply wrong: %+v", rs)
	}
	if _, err := st.SendMessage("a", "x", "b", 99999, 0, "ghost"); err == nil {
		t.Fatal("reply to missing parent should error")
	}

	// validation: an agent CAN address the human (empty target = their
	// DM/ledger); the human sending to nobody is the error case.
	if _, err := st.SendMessage("a", "x", "", 0, 0, "to the human"); err != nil {
		t.Fatalf("agent→human message should be legal: %v", err)
	}
	if _, err := st.SendMessage("", "you", "", 0, 0, "to nobody"); err == nil {
		t.Fatal("human→nobody should error")
	}
	if _, err := st.SendMessage("a", "x", "b", 0, 0, ""); err == nil {
		t.Fatal("empty body should error")
	}
	if err := st.MarkMessageReadAgent(424242); err == nil {
		t.Fatal("read on a missing message should error")
	}
}

// the human can edit their OWN comments — draft or submitted — and the edit
// clears the agent read-receipt so changed feedback re-enters the agent's
// queue; the agent's comments are not editable.
func TestEditOwnComment(t *testing.T) {
	st := testStoreDB(t)
	id, _ := st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	cid, err := st.AddReviewComment(id, "you", "first draft wording", "", 0, "", "")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := st.SubmitReview(id, VerdictRequestChanges, "go"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := st.MarkReadAgent(cid); err != nil {
		t.Fatalf("agent read: %v", err)
	}

	// edit the SUBMITTED comment: body changes, agent receipt clears
	if err := st.EditOwnComment(cid, "sharper wording"); err != nil {
		t.Fatalf("edit submitted own comment: %v", err)
	}
	cs, _ := st.Comments(id)
	var got *Comment
	for i := range cs {
		if cs[i].ID == cid {
			got = &cs[i]
		}
	}
	if got == nil || got.Body != "sharper wording" {
		t.Fatalf("body not updated: %+v", got)
	}
	if got.ReadAgent != "" {
		t.Fatalf("edit must clear the agent read-receipt, got %q", got.ReadAgent)
	}

	// the agent's comments are not editable
	aid, _ := st.AddReply(cid, "agent", "the agent's words")
	if err := st.EditOwnComment(aid, "tampered"); err == nil {
		t.Fatal("editing an agent comment must be refused")
	}
}

// proposals: open with deduped parties, list/fetch, decide once (open→decided
// is one-way; double-decide refused), parties grow idempotently.
func TestProposalLifecycle(t *testing.T) {
	st := testStoreDB(t)
	id, err := st.AddProposal(Proposal{
		Title: "PreserveBG write mode", Body: "## Problem\n…",
		ProposerRepo: "recap", ProposerWho: "Kestrel", TargetRepo: "tui",
	}, []string{"mail", "recap", " "}) // proposer dup + blank are dropped
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	parties, _ := st.ProposalParties(id)
	if len(parties) != 3 { // recap, tui, mail
		t.Fatalf("parties = %v, want 3 deduped", parties)
	}
	if err := st.AddProposalParty(id, "mail"); err != nil {
		t.Fatalf("idempotent party add: %v", err)
	}
	if parties, _ = st.ProposalParties(id); len(parties) != 3 {
		t.Fatalf("party re-add duplicated: %v", parties)
	}

	open, _ := st.Proposals(ProposalOpen)
	if len(open) != 1 || open[0].Status != ProposalOpen {
		t.Fatalf("open list = %+v", open)
	}
	if err := st.DecideProposal(id, "maybe"); err == nil {
		t.Fatal("invalid status must be refused")
	}
	if err := st.DecideProposal(id, ProposalApproved); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := st.DecideProposal(id, ProposalDeclined); err == nil {
		t.Fatal("double-decide must be refused")
	}
	p, _ := st.ProposalByID(id)
	if p.Status != ProposalApproved || p.DecidedAt == "" {
		t.Fatalf("decided proposal = %+v", p)
	}
	if open, _ = st.Proposals(ProposalOpen); len(open) != 0 {
		t.Fatalf("approved proposal still listed open: %+v", open)
	}
}
