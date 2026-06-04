package main

import (
	"strings"

	. "github.com/kungfusheep/glyph"
)

// Floating prompt overlays (TODO: "use overlay instead of PushView"). The comment
// and todo add/edit prompts, and the comment read view, render as centred overlays
// over the current view (inbox/diff/todo stays visible behind a backdrop).
//
// Typing uses glyph's blessed pattern: an On.Modal scope (Enter/Esc/Ctrl-V) plus an
// Input() component bound with .Bind() — glyph wires the modal router's
// HandleUnmatched to the field's TextHandler (full editing + a real cursor), the
// same way the omnibox works. glyph owns the modal-router push/pop, so there's no
// race (an earlier hand-pushed router raced the render thread and broke typing).
var (
	promptOpen      bool
	promptTitle     string
	promptLoc       string     // optional location line (line comments)
	promptSnip      string     // optional snippet line (line comments)
	promptOnSave    func()     // runs on enter; reads commentField.Value
	commentField    InputState // the prompt's text field (Value + Cursor)
	commentViewOpen bool       // the read-only comment overlay

	promptRef NodeRef // anchors the input overlay's screen effects
	readRef   NodeRef // anchors the read overlay's screen effects
)

// openInputPrompt shows the input overlay with a title, optional location/snippet
// context, a prefilled body, and a save action (which reads commentField.Value).
func openInputPrompt(title, loc, snip, prefill string, onSave func()) {
	promptTitle, promptLoc, promptSnip, promptOnSave = title, loc, snip, onSave
	commentField.Value = prefill
	commentField.Cursor = len(prefill)
	promptOpen = true
	uiApp.RequestRender()
}

func closePrompt() {
	promptOpen = false
	promptOnSave = nil
	commentField.Clear()
	uiApp.RequestRender()
}

// submitPrompt runs the save action (which reads commentField.Value) then closes.
func submitPrompt() {
	fn := promptOnSave
	promptOpen = false
	promptOnSave = nil
	if fn != nil {
		fn()
	}
	commentField.Clear()
	uiApp.RequestRender()
}

// inputPromptOverlay is the floating add/edit prompt. On.Modal declares the
// decision keys; the bound Input() captures typing via glyph's TextHandler wiring.
func inputPromptOverlay() Component {
	return If(&promptOpen).Then(
		Overlay.Centered()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).
				PaddingVH(1, 2).NodeRef(&promptRef).Opacity(In(1).Out(Animate(0)))(
				On.Modal(
					Key("<CR>", submitPrompt),
					Key("<Enter>", submitPrompt),
					Key("<Esc>", closePrompt),
					Key("<C-v>", pasteImageIntoComment),
				),
				// dim everything except the panel (drop shadow + dodged vignette), the
				// same treatment as the omnibox/help overlays — no flat Backdrop.
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&promptRef),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&promptRef),
				),
				HBox(Text(&promptTitle).FG(cBright).Bold(), Space(), Text("esc cancel · enter save").FG(cMuted)),
				SpaceH(1),
				// line-comment context (location + snippet) shows only when set.
				If(&promptLoc).Eq("").Then(Text("")).Else(
					VBox(Text(&promptLoc).FG(cSubtle), Text(&promptSnip).FG(cMuted), SpaceH(1)),
				),
				Input().Field(&commentField).Bind().Placeholder("…").Width(66),
			),
		),
	)
}

// insertCommentLink inserts a [[path]] reference into the field at the cursor
// (space-separated when needed). Pure — the testable half of pasteImageIntoComment.
func insertCommentLink(path string) {
	ref := "[[" + path + "]]"
	v := commentField.Value
	if v != "" && !strings.HasSuffix(v, " ") {
		ref = " " + ref
	}
	commentField.Value = v + ref
	commentField.Cursor = len(commentField.Value)
}

// pasteImageIntoComment grabs a clipboard screenshot to a persistent PNG and
// inserts a [[path]] link (recap can't render images inline; open with O).
func pasteImageIntoComment() {
	path, err := pasteClipboardImage()
	if err != nil {
		statusMsg = "paste: " + err.Error()
		uiApp.RequestRender()
		return
	}
	insertCommentLink(path)
	statusMsg = "pasted screenshot → " + path
	uiApp.RequestRender()
}

// openReadComment shows the read-in-full comment overlay (e edit, d delete, esc).
func openReadComment() {
	commentViewOpen = true
	uiApp.RequestRender()
}

func readCommentOverlay() Component {
	return If(&commentViewOpen).Then(
		Overlay.Centered()(
			VBox.Width(72).Fill(cFloat).CascadeStyle(&Style{Fill: cFloat, BG: cFloat, FG: cFG}).
				PaddingVH(1, 2).NodeRef(&readRef).Opacity(In(1).Out(Animate(0)))(
				On.Modal(
					Key("e", func() { commentViewOpen = false; editDraftComment() }),
					Key("d", func() { commentViewOpen = false; deleteDraftComment() }),
					Key("<Esc>", func() { commentViewOpen = false; uiApp.RequestRender() }),
					Key("q", func() { commentViewOpen = false; uiApp.RequestRender() }),
				),
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&readRef),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&readRef),
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
