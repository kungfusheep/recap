package main

import . "github.com/kungfusheep/glyph"

// Floating prompt overlays (review #152 / TODO): comment add/edit, todo add/edit,
// and the comment read view render as CENTRED OVERLAYS over the current view —
// keeping the inbox/diff/TODO visible behind a backdrop — instead of a full-screen
// PushView. inputPromptOverlay + readCommentOverlay are included in both buildMain
// and the todoedit view so a prompt floats over whichever is on screen.
var (
	promptOpen      bool
	promptTitle     string
	promptLoc       string // optional location line (line comments)
	promptSnip      string // optional snippet line (line comments)
	promptOnSave    func() // runs on enter; reads commentText
	commentViewOpen bool   // the read-only comment overlay
)

// openInputPrompt shows the input overlay with a title, optional location/snippet
// context rows, a prefilled body, and a save action (which reads commentText).
func openInputPrompt(title, loc, snip, prefill string, onSave func()) {
	promptTitle, promptLoc, promptSnip, promptOnSave = title, loc, snip, onSave
	setCommentText(prefill)
	promptOpen = true
	uiApp.RequestRender()
}

func closePrompt() {
	promptOpen = false
	promptOnSave = nil
	setCommentText("")
	uiApp.RequestRender()
}

// submitPrompt runs the save action (which reads commentText) then closes.
func submitPrompt() {
	fn := promptOnSave
	promptOpen = false
	promptOnSave = nil
	if fn != nil {
		fn()
	}
	setCommentText("")
	uiApp.RequestRender()
}

// inputPromptOverlay is the floating add/edit prompt. On.Modal captures its keys
// exclusively while open; printable typing falls through to the host router's
// HandleUnmatched (gated on promptOpen — see runUI and setupTodoView).
func inputPromptOverlay() Component {
	return If(&promptOpen).Then(
		Overlay.Centered().Backdrop()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).PaddingVH(1, 2)(
				On.Modal(
					Key("<CR>", submitPrompt),
					Key("<Enter>", submitPrompt),
					Key("<Esc>", closePrompt),
					Key("<BS>", backspaceComment),
					Key("<Space>", func() { setCommentText(commentText + " ") }),
					Key("<C-v>", pasteImageIntoComment),
				),
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

// readCommentOverlay is the read-in-full comment view (the pane truncates long
// notes). e edits, d deletes, esc/q closes.
func readCommentOverlay() Component {
	return If(&commentViewOpen).Then(
		Overlay.Centered().Backdrop()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).PaddingVH(1, 2)(
				On.Modal(
					Key("e", func() { commentViewOpen = false; editDraftComment() }),
					Key("d", func() { commentViewOpen = false; deleteDraftComment() }),
					Key("<Esc>", func() { commentViewOpen = false; uiApp.RequestRender() }),
					Key("q", func() { commentViewOpen = false; uiApp.RequestRender() }),
				),
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
