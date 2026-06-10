package main

import "testing"

// human comments from the ledger have no sender repo — headers collapse to the
// bare name everywhere (TUI rows, recap next titles, recap messages lines)
// instead of rendering a dangling "you@".
func TestMsgSender(t *testing.T) {
	if got := msgSender("Kestrel", "recap"); got != "Kestrel@recap" {
		t.Fatalf("agent sender = %q", got)
	}
	if got := msgSender("you", ""); got != "you" {
		t.Fatalf("human sender = %q, want bare name", got)
	}
}

// a human comment is a normal message row threaded under the agent's message and
// addressed to its SENDER repo — it must surface in that agent's unread queue,
// carry the thread parent + task anchor, and reload into the ledger with the
// repo-less head format.
func TestHumanCommentRoundTrip(t *testing.T) {
	st := testStore(t)
	uiStore = st
	t.Cleanup(func() { uiStore = nil; msgUI = msgView{} })

	orig, err := st.SendMessage("recap", "Kestrel", "tui", 0, 7, "Rich() now takes a pointer")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// the comment path: from the TUI, addressed to the original sender's repo
	mid, err := st.SendMessage("", "you", "recap", orig, 7, "small correction: it always did")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if err := st.MarkMessageReadUser(mid); err != nil {
		t.Fatalf("self read-receipt: %v", err)
	}

	// it lands in the sender repo's agent queue
	un, err := st.UnreadMessages("recap")
	if err != nil || len(un) != 1 {
		t.Fatalf("recap unread = %d (%v), want 1", len(un), err)
	}
	if un[0].FromWho != "you" || un[0].FromRepo != "" || un[0].ParentID != orig || un[0].TaskID != 7 {
		t.Fatalf("comment row lost shape: %+v", un[0])
	}

	// the ledger reload renders it with the bare-name head and carries reply context
	if !msgUI.load() {
		t.Fatalf("load failed")
	}
	if len(msgUI.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(msgUI.Rows))
	}
	last := msgUI.Rows[1]
	if last.FromRepo != "" || last.FromWho != "you" || last.TaskID != 7 {
		t.Fatalf("vm lost reply context: %+v", last)
	}
	want := "m2  you → recap  ↳m1"
	if last.Head != want {
		t.Fatalf("head = %q, want %q", last.Head, want)
	}
}
