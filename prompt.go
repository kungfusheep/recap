package main

import (
	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/riffkey"
)

// Floating prompt overlays (review #152 / TODO): comment add/edit, todo add/edit,
// and the comment read view render as CENTRED OVERLAYS over the current view —
// keeping the inbox/diff/TODO visible behind a backdrop — instead of a full-screen
// PushView.
//
// Typing is the subtle part: an On.Modal in the tree would swallow unmatched runes
// (its modal router has no fall-through), so the input prompt instead PUSHES a
// dedicated riffkey router whose HandleUnmatched routes runes into commentText and
// swallows everything else (exclusive while open) — mail's compose-field pattern.
// The read overlay (no typing) uses the same pushed-router approach for symmetry.
var (
	promptOpen      bool
	promptTitle     string
	promptLoc       string // optional location line (line comments)
	promptSnip      string // optional snippet line (line comments)
	promptOnSave    func() // runs on enter; reads commentText
	promptRouter    *riffkey.Router
	commentViewOpen bool // the read-only comment overlay
	readRouter      *riffkey.Router
)

// openInputPrompt shows the input overlay (title + optional context rows + prefill)
// and pushes its input router so typing lands in commentText.
func openInputPrompt(title, loc, snip, prefill string, onSave func()) {
	promptTitle, promptLoc, promptSnip, promptOnSave = title, loc, snip, onSave
	setCommentText(prefill)
	promptOpen = true

	r := riffkey.NewRouter().NoCounts()
	r.Handle("<CR>", func(riffkey.Match) { submitPrompt() })
	r.Handle("<Enter>", func(riffkey.Match) { submitPrompt() })
	r.Handle("<Esc>", func(riffkey.Match) { closePrompt() })
	r.Handle("<BS>", func(riffkey.Match) { backspaceComment(); uiApp.RequestRender() })
	r.Handle("<Space>", func(riffkey.Match) { setCommentText(commentText + " "); uiApp.RequestRender() })
	r.Handle("<C-v>", func(riffkey.Match) { pasteImageIntoComment() })
	r.HandleUnmatched(func(k riffkey.Key) bool {
		if k.Rune != 0 && k.Mod == 0 {
			setCommentText(commentText + string(k.Rune))
			uiApp.RequestRender()
		}
		return true // exclusive while open: never leak keys to the view beneath
	})
	promptRouter = r
	uiApp.PushRouter(r)
	uiApp.RequestRender()
}

func popPromptRouter() {
	if promptRouter != nil {
		uiApp.PopRouter()
		promptRouter = nil
	}
}

func closePrompt() {
	promptOpen = false
	promptOnSave = nil
	popPromptRouter()
	setCommentText("")
	uiApp.RequestRender()
}

// submitPrompt runs the save action (which reads commentText) then closes.
func submitPrompt() {
	fn := promptOnSave
	promptOpen = false
	promptOnSave = nil
	popPromptRouter()
	if fn != nil {
		fn()
	}
	setCommentText("")
	uiApp.RequestRender()
}

// inputPromptOverlay renders the floating add/edit prompt. Keys are handled by the
// pushed promptRouter (see openInputPrompt), not by a tree scope.
func inputPromptOverlay() Component {
	return If(&promptOpen).Then(
		Overlay.Centered().Backdrop()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).PaddingVH(1, 2)(
				HBox(Text(&promptTitle).FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				// line-comment context (location + snippet) shows only when set.
				If(&promptLoc).Eq("").Then(commentInput()).Else(
					VBox(
						Text(&promptLoc).FG(cSubtle),
						Text(&promptSnip).FG(cMuted),
						SpaceH(1),
						commentInput(),
					),
				),
			),
		),
	)
}

// openReadComment shows the read-in-full comment overlay (the pane truncates long
// notes) and pushes its key router (e edit, d delete, esc/q close).
func openReadComment() {
	commentViewOpen = true
	r := riffkey.NewRouter().NoCounts()
	r.Handle("e", func(riffkey.Match) { closeReadComment(); editDraftComment() })
	r.Handle("d", func(riffkey.Match) { closeReadComment(); deleteDraftComment() })
	r.Handle("<Esc>", func(riffkey.Match) { closeReadComment() })
	r.Handle("q", func(riffkey.Match) { closeReadComment() })
	r.HandleUnmatched(func(k riffkey.Key) bool { return true }) // exclusive
	readRouter = r
	uiApp.PushRouter(r)
	uiApp.RequestRender()
}

func closeReadComment() {
	commentViewOpen = false
	if readRouter != nil {
		uiApp.PopRouter()
		readRouter = nil
	}
	uiApp.RequestRender()
}

func readCommentOverlay() Component {
	return If(&commentViewOpen).Then(
		Overlay.Centered().Backdrop()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).PaddingVH(1, 2)(
				HBox(Text("comment").FG(cBright).Bold(), Space(), Text("e edit · d delete · esc back").FG(cMuted)),
				SpaceH(1),
				Text(&cvLocation).FG(cSubtle),
				If(&cvSnippet).Then(Text(&cvSnippet).FG(cMuted)),
				SpaceH(1),
				ForEach(&cvBodyLines, func(s *string) Component { return Text(s).FG(cBright) }),
			),
		),
	)
}
