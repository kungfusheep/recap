package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

// FocusShade dims (lerps toward black) only real text cells inside its rect; it
// leaves spaces, out-of-rect cells, and default-colour cells alone, and is a no-op
// at strength 0.
func TestFocusShadeDims(t *testing.T) {
	white := RGB(255, 255, 255)
	mk := func() *Buffer {
		b := NewBuffer(20, 10)
		b.Set(3, 2, Cell{Rune: 'X', Style: Style{FG: white}})  // in-rect text
		b.Set(4, 2, Cell{Rune: ' ', Style: Style{FG: white}})  // in-rect space (untouched)
		b.Set(15, 2, Cell{Rune: 'Y', Style: Style{FG: white}}) // out-of-rect text
		return b
	}
	ref := NodeRef{X: 0, Y: 0, W: 10, H: 10} // left half only

	// strength 0.5 dims the in-rect text toward black
	b := mk()
	f := NewFocusShade(&ref)
	f.strength = StaticEffectFloat64(0.5)
	f.Apply(b, PostContext{Width: 20, Height: 10})

	if got := b.Get(3, 2).Style.FG; got.R >= white.R || got == white {
		t.Fatalf("in-rect text not darkened: %+v", got)
	}
	if got := b.Get(4, 2).Style.FG; got != white {
		t.Fatalf("space should be untouched: %+v", got)
	}
	if got := b.Get(15, 2).Style.FG; got != white {
		t.Fatalf("out-of-rect text should be untouched: %+v", got)
	}

	// strength 0 is a no-op (focused column — full strength)
	b0 := mk()
	f0 := NewFocusShade(&ref)
	f0.strength = StaticEffectFloat64(0)
	f0.Apply(b0, PostContext{Width: 20, Height: 10})
	if got := b0.Get(3, 2).Style.FG; got != white {
		t.Fatalf("strength 0 should not dim: %+v", got)
	}
}
