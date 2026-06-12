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
// effect pipeline, the FocusShade-Strength pattern), so the slide renders at
// SUB-CELL resolution: edge cells with partial coverage draw quadrant caps
// (▗ leading / ▖ trailing — the block set has no partial-width lower-eighths,
// so the quadrant pair is the finest cap available; settled edges are integral
// and render pure ▁).
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
	start, end := x, x+w
	for cx := int(math.Floor(start)); cx < int(math.Ceil(end)) && cx < ctx.Width; cx++ {
		if cx < 0 {
			continue
		}
		lo, hi := math.Max(start, float64(cx)), math.Min(end, float64(cx+1))
		cov := hi - lo
		if cov <= 0.25 {
			continue // sliver — leave the cell alone
		}
		var r rune
		switch {
		case cov >= 0.75:
			r = '▁'
		case lo > float64(cx): // covered part is the cell's right side: leading cap
			r = '▗'
		default: // covered part is the cell's left side: trailing cap
			r = '▖'
		}
		cell := buf.Get(cx, y)
		cell.Rune = r
		if f.FG != nil {
			cell.Style.FG = *f.FG
		}
		// background + attrs untouched: the line inks over whatever is beneath
		buf.Set(cx, y, cell)
	}
}
