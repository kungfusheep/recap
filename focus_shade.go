package main

import (
	. "github.com/kungfusheep/glyph"
)

// FocusShade is the column focus-fade effect, ported from mail: a post-process
// that lerps each text cell's foreground toward black within a column's NodeRef
// rect. Gated by If(&pane).Ne(col) in the tree so it's present (fading in) only
// while that column is unfocused; the strength animates via an In/Out tween.
// Column styles stay full-strength — the dimming is entirely this effect. Cells
// with the default colour, spaces, and soft-border glyphs are left untouched.
type FocusShade struct {
	Ref         *NodeRef
	strengthArg any
	strength    EffectFloat64
	dodgeRefs   []*NodeRef
}

func NewFocusShade(ref *NodeRef) FocusShade {
	return FocusShade{Ref: ref, strengthArg: 0.28}
}

func (f FocusShade) Strength(v any) FocusShade {
	f.strengthArg = v
	return f
}

// Dodge excludes overlay regions (help, omnibox, prompts) so a popup over a
// shaded column isn't double-dimmed.
func (f FocusShade) Dodge(refs ...*NodeRef) FocusShade {
	f.dodgeRefs = append(f.dodgeRefs, refs...)
	return f
}

func (f FocusShade) CompileEffect(c EffectCompiler) Effect {
	f.strength = c.Float64(f.strengthArg)
	return f
}

func (f FocusShade) Apply(buf *Buffer, ctx PostContext) {
	strength := f.strength.Float64()
	if f.Ref == nil || strength <= 0 {
		return
	}
	black := RGB(0, 0, 0)
	for y := range ctx.Height {
		if y < f.Ref.Y || y >= f.Ref.Y+f.Ref.H {
			continue
		}
		for x := range ctx.Width {
			if x < f.Ref.X || x >= f.Ref.X+f.Ref.W {
				continue
			}
			cellStrength := strength * (1 - f.dodgeOpacityAt(x, y))
			if cellStrength <= 0 {
				continue
			}
			cell := buf.Get(x, y)
			if cell.Rune == 0 {
				continue
			}
			if cell.Rune != ' ' && !isSoftBorder(cell.Rune) {
				cell.Style.FG = shadeColor(cell.Style.FG, black, cellStrength)
			}
			buf.Set(x, y, cell)
		}
	}
}

func (f FocusShade) dodgeOpacityAt(x, y int) float64 {
	opacity := 0.0
	for _, ref := range f.dodgeRefs {
		if ref == nil {
			continue
		}
		if x >= ref.X && x < ref.X+ref.W && y >= ref.Y && y < ref.Y+ref.H {
			opacity = max(opacity, NodeOpacity(ref))
		}
	}
	return opacity
}

func shadeColor(c Color, target Color, strength float64) Color {
	if c.Mode == ColorDefault {
		return c
	}
	return Lerp(c, target, strength)
}

func isSoftBorder(r rune) bool {
	switch r {
	case '▀', '▄', '█':
		return true
	default:
		return false
	}
}
