package main

import (
	"strings"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/links"
)

// Floating prompt overlays: the comment and todo add/edit prompts, and the comment
// read view, render as centred overlays over the current view (inbox/diff/todo stays
// visible behind a backdrop).
//
// Typing uses glyph's blessed pattern: an On.Modal scope (Enter/Esc/Ctrl-V) plus an
// Input() component bound with .Bind() — glyph wires the modal router's
// HandleUnmatched to the field's TextHandler (full editing + a real cursor), the
// same way the omnibox works. glyph owns the modal-router push/pop, so there's no
// race (an earlier hand-pushed router raced the render thread and broke typing).

// promptView is the prompt overlays' state in one concrete struct: the add/edit
// input prompt (title/context/field/save action) and the read-only comment overlay's
// open flag. One package instance (promptUI) — fields are pointer-bound into the
// compiled overlays (&promptUI.Title, .Field, .Ref…), so the struct must be a stable
// package var. No interfaces, no injection — plain data + methods.
type promptView struct {
	Open     bool
	Title    string
	Loc      string     // optional location line (line comments)
	Snip     string     // optional snippet line (line comments)
	OnSave   func()     // runs on enter; reads Field.Value
	Field    InputState // the prompt's text field (Value + Cursor)
	ReadOpen bool       // the read-only comment overlay

	Ref     NodeRef // anchors the input overlay's screen effects
	ReadRef NodeRef // anchors the read overlay's screen effects
}

// promptUI is the single instance the overlays bind against.
var promptUI promptView

// open shows the input overlay with a title, optional location/snippet context, a
// prefilled body, and a save action (which reads promptUI.Field.Value).
func (pv *promptView) open(title, loc, snip, prefill string, onSave func()) {
	pv.Title, pv.Loc, pv.Snip, pv.OnSave = title, loc, snip, onSave
	pv.Field.Value = prefill
	pv.Field.Cursor = len(prefill)
	pv.Open = true
	uiApp.RequestRender()
}

func (pv *promptView) close() {
	pv.Open = false
	pv.OnSave = nil
	pv.Field.Clear()
	uiApp.RequestRender()
}

// submit runs the save action (which reads pv.Field.Value) then closes.
func (pv *promptView) submit() {
	fn := pv.OnSave
	pv.Open = false
	pv.OnSave = nil
	if fn != nil {
		fn()
	}
	pv.Field.Clear()
	uiApp.RequestRender()
}

// openRead shows the read-in-full comment overlay (e edit, d delete, esc).
func (pv *promptView) openRead() {
	pv.ReadOpen = true
	uiApp.RequestRender()
}

// inputPromptOverlay is the floating add/edit prompt. On.Modal declares the
// decision keys; the bound Input() captures typing via glyph's TextHandler wiring.
func inputPromptOverlay() Component {
	return If(&promptUI.Open).Then(
		Overlay.Centered()(
			VBox.Width(72).Fill(&cFloat).CascadeStyle(&floatStyle).
				PaddingVH(1, 2).NodeRef(&promptUI.Ref).Opacity(In(1).Out(Animate(0)))(
				On.Modal(
					Key("<Enter>", func() { promptUI.submit() }),
					Key("<Esc>", func() { promptUI.close() }),
					Key("<C-v>", func() { promptUI.pasteImage() }),
				),
				// dim everything except the panel (drop shadow + dodged vignette), the
				// same treatment as the omnibox/help overlays — no flat Backdrop.
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&promptUI.Ref),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&promptUI.Ref),
				),
				HBox(Text(&promptUI.Title).FG(&cBright).Bold(), Space(), Text("esc cancel · enter save").FG(&cMuted)),
				SpaceH(1),
				// line-comment context (location + snippet); empty strings render blank
				// for non-line prompts. Kept flat (no nested If) — a conditional inside a
				// modal scope confuses glyph's route-modal pop.
				Text(&promptUI.Loc).FG(&cSubtle),
				Text(&promptUI.Snip).FG(&cMuted),
				Input().Field(&promptUI.Field).Bind().Placeholder("…").Width(66).MultiLine(),
			),
		),
	)
}

// insertLink inserts a [[path]] reference into the field at the cursor
// (space-separated when needed). Pure — the testable half of pasteImage.
func (pv *promptView) insertLink(path string) {
	ref := "[[" + path + "]]"
	v := pv.Field.Value
	if v != "" && !strings.HasSuffix(v, " ") {
		ref = " " + ref
	}
	pv.Field.Value = v + ref
	pv.Field.Cursor = len(pv.Field.Value)
}

// pasteImage grabs a clipboard screenshot to a persistent PNG and inserts a
// [[path]] link (recap can't render images inline; open with O).
func (pv *promptView) pasteImage() {
	path, err := links.PasteImage()
	if err != nil {
		toast("paste: " + err.Error())
		uiApp.RequestRender()
		return
	}
	pv.insertLink(path)
	toast("pasted screenshot → " + path)
	uiApp.RequestRender()
}

func readCommentOverlay() Component {
	return If(&promptUI.ReadOpen).Then(
		Overlay.Centered()(
			VBox.Width(72).Fill(&cFloat).CascadeStyle(&floatStyle).
				PaddingVH(1, 2).NodeRef(&promptUI.ReadRef).Opacity(In(1).Out(Animate(0)))(
				On.Modal(
					Key("e", func() { promptUI.ReadOpen = false; editDraftComment() }),
					Key("d", func() { promptUI.ReadOpen = false; deleteDraftComment() }),
					Key("<Esc>", func() { promptUI.ReadOpen = false; uiApp.RequestRender() }),
					Key("q", func() { promptUI.ReadOpen = false; uiApp.RequestRender() }),
				),
				ScreenEffect(
					SEDropShadow().Strength(0.3).Focus(&promptUI.ReadRef),
					SEVignette().Smooth().Strength(In(Animate(0.3)).Out(Animate(0))).Dodge(&promptUI.ReadRef),
				),
				HBox(Text("comment").FG(&cBright).Bold(), Space(), Text("e edit · d delete · esc back").FG(&cMuted)),
				SpaceH(1),
				Text(&draftUI.ViewLoc).FG(&cSubtle),
				If(&draftUI.ViewSnip).Then(Text(&draftUI.ViewSnip).FG(&cMuted)),
				SpaceH(1),
				ForEach(&draftUI.ViewBody, func(s *string) Component { return Text(s).FG(&cBright) }),
			),
		),
	)
}
