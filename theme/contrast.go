package theme

import (
	. "github.com/kungfusheep/glyph"
)

// WCAG contrast helpers, so theme-derived colours (e.g. the diff +/-/@@ hues) stay
// legible against the background instead of melting into it on low-contrast themes.
// The ratio maths and the lerp-to-contrast search live in glyph (ContrastRatio,
// LerpToContrast); this only adds the auto target pick.

const WCAGAA = 4.5 // WCAG 2.1 AA contrast ratio for normal-size text

// EnsureContrast nudges c toward whichever of black/white contrasts harder against
// bg, just far enough to meet the min WCAG ratio, keeping as much of the original
// hue as possible. Chosen by actual contrast, not a luminance threshold — on a
// mid-tone bg black can win even though the bg isn't "dark" (the ratio is
// asymmetric). Non-RGB colours pass through; an unreachable target returns the
// best attempt.
func EnsureContrast(c, bg Color, min float64) Color {
	if c.Mode != ColorRGB || bg.Mode != ColorRGB {
		return c
	}
	black := Color{Mode: ColorRGB, R: 0, G: 0, B: 0}
	white := Color{Mode: ColorRGB, R: 255, G: 255, B: 255}
	target := black
	if ContrastRatio(white, bg) > ContrastRatio(black, bg) {
		target = white
	}
	return LerpToContrast(c, target, bg, min)
}
