package main

import (
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the theme pack is the full set (dark, light, + the mfd pack), every theme has a
// unique name + non-empty label, lookups work, and unknown names fail cleanly.
func TestThemePack(t *testing.T) {
	themes := allThemes()
	if len(themes) != 20 { // dark + light + 18 mfd
		t.Fatalf("want 20 themes, got %d", len(themes))
	}

	seen := map[string]bool{}
	for _, n := range themes {
		if n.Name == "" || n.Label == "" {
			t.Fatalf("theme with empty name/label: %+v", n)
		}
		if seen[n.Name] {
			t.Fatalf("duplicate theme name %q", n.Name)
		}
		seen[n.Name] = true
		// every colour must be a real RGB value (mode set), not the zero Color
		// (a zero Color renders as terminal-default and would break the palette).
		for label, c := range map[string]Color{
			"BG": n.Palette.BG, "FG": n.Palette.FG, "Bright": n.Palette.Bright,
			"Subtle": n.Palette.Subtle, "Muted": n.Palette.Muted, "SelBG": n.Palette.SelBG,
			"GroupBG": n.Palette.GroupBG, "ThreadBG": n.Palette.ThreadBG,
			"Success": n.Palette.Success, "Error": n.Palette.Error, "Info": n.Palette.Info,
		} {
			if c.Mode == 0 {
				t.Fatalf("theme %q has unset %s colour", n.Name, label)
			}
		}
	}

	// known lookups resolve; unknown fails
	if _, ok := themeByName("mfd-nerv"); !ok {
		t.Fatal("mfd-nerv should resolve")
	}
	if got, ok := themeByName("dark"); !ok || got != themeDark {
		t.Fatal("dark should resolve to themeDark")
	}
	if _, ok := themeByName("nope"); ok {
		t.Fatal("unknown theme should not resolve")
	}

	// mfdColor unpacks hex channels correctly
	c := mfdColor(0x102030)
	if c.Mode != ColorRGB || c.R != 0x10 || c.G != 0x20 || c.B != 0x30 {
		t.Fatalf("mfdColor unpack wrong: %+v", c)
	}
}
