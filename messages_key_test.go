package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// the advertised 'm' shortcut must actually open the agent-messages ledger from
// the main view (it was in the help overlay but bound nowhere — todo:fabb8010).
// Verified by dispatching the real key, not by calling openMessages directly.
func TestMKeyOpensMessages(t *testing.T) {
	prevApp, prevStore, prevOmni := uiApp, uiStore, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiApp, uiStore, omni = prevApp, prevStore, prevOmni
		inboxUI.Rows = nil
		msgUI = msgView{}
	})

	uiApp.View("main", buildMain()).NoCounts()
	uiApp.View("messages", buildMessagesView()).NoCounts()
	uiApp.Go("main")
	uiApp.RenderNow()

	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'm'}) {
		t.Fatal("'m' was not handled by the main view")
	}
	if got := uiApp.CurrentView(); got != "messages" {
		t.Fatalf("after 'm', current view = %q, want \"messages\"", got)
	}

	// m again closes (the ledger binds m → back to main)
	uiApp.RenderNow()
	if !uiApp.Input().Dispatch(riffkey.Key{Rune: 'm'}) {
		t.Fatal("'m' was not handled by the messages view")
	}
	if got := uiApp.CurrentView(); got != "main" {
		t.Fatalf("after second 'm', current view = %q, want \"main\"", got)
	}
}
