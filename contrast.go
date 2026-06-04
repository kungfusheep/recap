package main

import (
	"math"

	. "github.com/kungfusheep/glyph"
)

// WCAG contrast helpers, so theme-derived colours (e.g. the diff +/-/@@ hues) stay
// legible against the background instead of melting into it on low-contrast themes.

const wcagAA = 4.5 // WCAG 2.1 AA contrast ratio for normal-size text

func channelLuminance(c uint8) float64 {
	s := float64(c) / 255.0
	if s <= 0.03928 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

// relLuminance is the WCAG relative luminance of an RGB colour (0..1).
func relLuminance(c Color) float64 {
	return 0.2126*channelLuminance(c.R) + 0.7152*channelLuminance(c.G) + 0.0722*channelLuminance(c.B)
}

// contrastRatio is the WCAG contrast ratio between two RGB colours (1..21).
func contrastRatio(a, b Color) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ensureContrast nudges c's lightness (toward white on a dark bg, toward black on a
// light one) just far enough to meet the target contrast ratio against bg, keeping
// as much of the original hue as possible. Returns c unchanged if it already passes
// or if either colour isn't RGB; returns the best attempt if the target is
// unreachable (e.g. a hue that can't hit AA on a mid-tone bg).
func ensureContrast(c, bg Color, min float64) Color {
	if c.Mode != ColorRGB || bg.Mode != ColorRGB {
		return c
	}
	if contrastRatio(c, bg) >= min {
		return c
	}
	// push toward whichever extreme maximises contrast against this bg — for a
	// mid-tone bg that's often black even though the bg isn't "dark" (the ratio is
	// asymmetric), so choose by actual contrast, not a luminance threshold.
	black := Color{Mode: ColorRGB, R: 0, G: 0, B: 0}
	white := Color{Mode: ColorRGB, R: 255, G: 255, B: 255}
	target := black
	if contrastRatio(white, bg) > contrastRatio(black, bg) {
		target = white
	}
	best := c
	for i := 1; i <= 20; i++ {
		cand := Lerp(c, target, float64(i)/20.0)
		if contrastRatio(cand, bg) >= min {
			return cand
		}
		best = cand
	}
	return best
}
