package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/riffkey"
)

// Dispatch-level coverage for the comments-pane keys. Function-level tests
// (calling toggleCommentThread()/replyToComment() directly) pass even with the
// Key(...) lines deleted — c348's mutation finding. These go through the real
// input stack: registered view, rendered frame, dispatched rune.

func setupCommentsPane(t *testing.T) int64 {
	t.Helper()
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
		pane = paneList
		promptUI = promptView{}
		draftUI = draftView{LastSel: -1, Collapsed: map[int64]bool{}}
	})

	id, _ := st.Add(db.Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: db.StatusPending})
	rootID, _ := st.AddReviewComment(id, "you", "root note", "calc.go", 3, "@@", "snip")
	if _, err := st.AddReply(rootID, "agent", "a reply"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	reloadTasks()
	loadDraftPane(id)
	draftUI.Has = true
	draftUI.Sel = 0
	pane = paneDraft

	uiApp.SetView(buildMain())
	uiApp.RenderNow() // pane-scoped On() registers for the active pane
	return id
}

// 'o' through the input stack folds the selected thread (kills the Key("o") mutant).
func TestOKeyFoldsThread(t *testing.T) {
	setupCommentsPane(t)
	if !draftUI.Comments[1].Visible {
		t.Fatal("precondition: reply visible")
	}
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'o'}) {
		t.Fatal("'o' was not handled in the comments pane")
	}
	if draftUI.Comments[1].Visible {
		t.Fatal("dispatched 'o' did not fold the thread")
	}
	if draftUI.Comments[0].FoldCue == "" {
		t.Fatal("folded root missing its cue after dispatched 'o'")
	}
}

// 'r' through the input stack opens the reply prompt targeting the selected
// comment (kills the Key("r") mutant).
func TestRKeyOpensReplyPrompt(t *testing.T) {
	setupCommentsPane(t)
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'r'}) {
		t.Fatal("'r' was not handled in the comments pane")
	}
	if !promptUI.Open {
		t.Fatal("dispatched 'r' did not open the reply prompt")
	}
	if draftUI.ReplyingTo != draftUI.Comments[0].ID {
		t.Fatalf("reply target = %d, want the selected comment %d", draftUI.ReplyingTo, draftUI.Comments[0].ID)
	}
}

// 'e' through the input stack opens the edit prompt prefilled with the selected
// comment's body — including on a SUBMITTED comment of your own (todo:a4aa0003;
// previously refused as read-only). Kills the Key("e") mutant.
func TestEKeyEditsOwnComment(t *testing.T) {
	id := setupCommentsPane(t)
	// submit the draft so the comment is no longer draft-state
	if _, err := uiStore.SubmitReview(id, db.VerdictRequestChanges, "go"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	loadDraftPane(id)
	draftUI.Sel = 0 // the root comment, who="you", now submitted
	uiApp.RenderNow()

	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'e'}) {
		t.Fatal("'e' was not handled in the comments pane")
	}
	if !promptUI.Open {
		t.Fatal("dispatched 'e' did not open the edit prompt on a submitted own comment")
	}
	if promptUI.Field.Value != "root note" {
		t.Fatalf("edit prompt not prefilled with the comment body: %q", promptUI.Field.Value)
	}

	// the agent's reply stays read-only
	promptUI.close()
	draftUI.Sel = 1 // the agent reply
	uiApp.RenderNow()
	uiApp.Input().Dispatch(riffkey.Key{Rune: 'e'})
	if promptUI.Open {
		t.Fatal("the agent's comment must not be editable")
	}
}
