package main

import (
	"math"

	. "github.com/kungfusheep/glyph"
)

// FocusLine draws the focus underline as a POST-PROCESS along the screen's
// bottom row: a ▁ (lower one-eighth) line inked into the EXISTING cells — rune
// + foreground only, every cell's background preserved, blending over any pane
// it crosses.
//
// Position and width are glyph-animated floats (Animate compiled through the
// effect pipeline, the FocusShade-Strength pattern); the line renders as a
// SOLID ▁ at whole-cell edges — the rounded span of the animated position.
// (Sub-cell edge caps were tried twice: quadrants read as height bumps,
// braille dots read as on/off toggling instead of motion — c424 settled on
// the flat line.)
type FocusLine struct {
	xArg, wArg any
	x, w       EffectFloat64
	FG         *Color
}

func NewFocusLine(x, w any, fg *Color) FocusLine {
	return FocusLine{xArg: x, wArg: w, FG: fg}
}

func (f FocusLine) CompileEffect(c EffectCompiler) Effect {
	f.x = c.Float64(f.xArg)
	f.w = c.Float64(f.wArg)
	return f
}

func (f FocusLine) Apply(buf *Buffer, ctx PostContext) {
	x, w := f.x.Float64(), f.w.Float64()
	if w <= 0 || ctx.Height == 0 {
		return
	}
	y := ctx.Height - 1
	start := int(math.Round(x))
	end := int(math.Round(x + w))
	for cx := start; cx < end && cx < ctx.Width; cx++ {
		if cx < 0 {
			continue
		}
		cell := buf.Get(cx, y)
		cell.Rune = '▁'
		if f.FG != nil {
			cell.Style.FG = *f.FG
		}
		// background + attrs untouched: the line inks over whatever is beneath
		buf.Set(cx, y, cell)
	}
}
