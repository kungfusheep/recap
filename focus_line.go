package main

import (
	. "github.com/kungfusheep/glyph"
)

// FocusLine draws the focus underline as a POST-PROCESS: a ▁ (lower one-eighth)
// glyph across the carrier ref's rect, written into the EXISTING cells — rune +
// foreground only, each cell's background preserved. The carrier is an invisible
// width-tweened box (glyph animations own the slide); this effect just inks the
// line wherever the carrier currently sits, so the line blends over any pane it
// crosses, mid-slide included. No block, ever — c408's shape.
type FocusLine struct {
	Ref *NodeRef
	FG  *Color
}

func NewFocusLine(ref *NodeRef, fg *Color) FocusLine {
	return FocusLine{Ref: ref, FG: fg}
}

func (f FocusLine) CompileEffect(c EffectCompiler) Effect { return f }

func (f FocusLine) Apply(buf *Buffer, ctx PostContext) {
	if f.Ref == nil || f.Ref.W <= 0 || f.Ref.H <= 0 {
		return
	}
	y := f.Ref.Y + f.Ref.H - 1 // the carrier's bottom row
	if y < 0 || y >= ctx.Height {
		return
	}
	for x := f.Ref.X; x < f.Ref.X+f.Ref.W && x < ctx.Width; x++ {
		if x < 0 {
			continue
		}
		cell := buf.Get(x, y)
		cell.Rune = '▁'
		if f.FG != nil {
			cell.Style.FG = *f.FG
		}
		// background + attrs untouched: the line inks over whatever is beneath
		buf.Set(x, y, cell)
	}
}
