package theme

import . "github.com/kungfusheep/glyph"

// Colour themes, ported from the mail app's mfd theme pack. A Theme is a flat
// palette; applyTheme (see theme_apply.go) maps it onto recap's package-level
// colour vars and rebuilds the views. Kept concrete in package main — no separate
// package — since recap is a single binary.

// Theme is a named palette's colour set. Field meaning mirrors mail so the mfd
// pack ports verbatim.
type Theme struct {
	BG       Color
	Bright   Color
	FG       Color
	Subtle   Color
	Dim      Color
	Muted    Color
	Accent   Color
	Info     Color
	Success  Color
	Warning  Color
	Error    Color
	SelBG    Color
	GroupBG  Color
	ThreadBG Color
	// optional per-theme diff colours (zero = derive generically). When a theme's
	// own palette has better add/del/hunk hues (e.g. its terminal ANSI colours),
	// set these and setThemeVars uses them (still WCAG-checked) instead of deriving.
	DiffAdd  Color
	DiffDel  Color
	DiffHunk Color
}

// Named is a Theme with its id (for persistence/lookup) and display label.
type Named struct {
	Name    string
	Label   string
	Palette Theme
}

var Dark = Theme{
	BG:       Hex(0x1c1c1c),
	Bright:   Hex(0xe8e6e3),
	FG:       Hex(0xb8b5b0),
	Subtle:   Hex(0x8b8780),
	Dim:      Hex(0x5f5b55),
	Muted:    Hex(0x3f3c38),
	Accent:   Hex(0xe8e6e3),
	Info:     Hex(0x7aa2f7),
	Success:  Hex(0x9ece6a),
	Warning:  Hex(0xe0af68),
	Error:    Hex(0xf7768e),
	SelBG:    Hex(0x302f2c),
	GroupBG:  Hex(0x252421),
	ThreadBG: Hex(0x191918),
}

var Light = Theme{
	BG:       Hex(0xf6f6f6),
	Bright:   Hex(0x111111),
	FG:       Hex(0x333333),
	Subtle:   Hex(0x777777),
	Dim:      Hex(0xaaaaaa),
	Muted:    Hex(0xcccccc),
	Accent:   Hex(0xe60012),
	Info:     Hex(0x2563eb),
	Success:  Hex(0x16803c),
	Warning:  Hex(0xb45309),
	Error:    Hex(0xc2410c),
	SelBG:    Hex(0xe8e8e8),
	GroupBG:  Hex(0xeeeeee),
	ThreadBG: Hex(0xf1f3f1),
}

// mfdPalette is the compact source form for the mfd pack; mfdTheme expands it
// into a full Theme (the field mapping matches mail's exactly).
type mfdPalette struct {
	bg      uint32
	fg      uint32
	dim     uint32
	bright  uint32
	subtle  uint32
	visual  uint32
	cursor  uint32
	border  uint32
	floatBG uint32
	// optional diff hues (0 = derive). Lifted from the theme's own terminal ANSI
	// colours in the mfd.nvim lua source where they read better than the generic blend.
	diffAdd  uint32
	diffDel  uint32
	diffHunk uint32
}

var mfdPalettes = []Named{
	mfdTheme("mfd", "MFD", mfdPalette{bg: 0x7A8B69, fg: 0x1E2D1E, dim: 0x5A6B49, bright: 0x0D1D0D, subtle: 0x687858, visual: 0x6A7B59, cursor: 0x848F72, border: 0x5A6B4A, floatBG: 0x5A6B4A}),
	mfdTheme("mfd-dark", "MFD Dark", mfdPalette{bg: 0x1E2D1E, fg: 0x8A9B70, dim: 0x3A4A3A, bright: 0xA0B180, subtle: 0x2E3E2E, visual: 0x2A3D2A, cursor: 0x253525, border: 0x3A4B2A, floatBG: 0x253525}),
	mfdTheme("mfd-stealth", "MFD Stealth", mfdPalette{bg: 0x0D1410, fg: 0x7A9A7A, dim: 0x253828, bright: 0x9ABB9A, subtle: 0x2A3A2A, visual: 0x1A2A1A, cursor: 0x151F18, border: 0x2A3A2A, floatBG: 0x101810}),
	mfdTheme("mfd-amber", "MFD Amber", mfdPalette{bg: 0x0F0C08, fg: 0xCC9944, dim: 0x382818, bright: 0xFFBB55, subtle: 0x4A3820, visual: 0x2A1C10, cursor: 0x1A1408, border: 0x3A2810, floatBG: 0x141008}),
	mfdTheme("mfd-mono", "MFD Mono", mfdPalette{bg: 0x08080C, fg: 0xD0D0D8, dim: 0x282830, bright: 0xF0F0FF, subtle: 0x383840, visual: 0x1A1A22, cursor: 0x101014, border: 0x2A2A32, floatBG: 0x0C0C10}),
	mfdTheme("mfd-scarlet", "MFD Scarlet", mfdPalette{bg: 0x0C0404, fg: 0xCC5545, dim: 0x3A1812, bright: 0xDD6655, subtle: 0x2A100A, visual: 0x1A0808, cursor: 0x140606, border: 0x2A100A, floatBG: 0x100505}),
	mfdTheme("mfd-paper", "MFD Paper", mfdPalette{bg: 0xBBC5B7, fg: 0x002611, dim: 0x8A9A88, bright: 0x001008, subtle: 0xA5B2A2, visual: 0xA0B0A0, cursor: 0xB0BAB0, border: 0x95A592, floatBG: 0xC5CFC2}),
	mfdTheme("mfd-hud", "MFD HUD", mfdPalette{bg: 0x060C06, fg: 0x55BB55, dim: 0x1A3018, bright: 0x77DD77, subtle: 0x1A2A18, visual: 0x0A1A0A, cursor: 0x081208, border: 0x1A3018, floatBG: 0x081008}),
	mfdTheme("mfd-nvg", "MFD NVG", mfdPalette{bg: 0x162014, fg: 0x78B858, dim: 0x4A7A3A, bright: 0x90D868, subtle: 0x2E4822, visual: 0x1E3018, cursor: 0x1A2816, border: 0x3A5C2E, floatBG: 0x182416}),
	mfdTheme("mfd-gbl-light", "MFD GBL Light", mfdPalette{bg: 0x02B582, fg: 0x004F3A, dim: 0x009A70, bright: 0x01694A, subtle: 0x01694A, visual: 0x01694A, cursor: 0x01A878, border: 0x01694A, floatBG: 0x02B582}),
	mfdTheme("mfd-gbl-dark", "MFD GBL Dark", mfdPalette{bg: 0x004F3A, fg: 0x02B582, dim: 0x01694A, bright: 0x009A70, subtle: 0x01694A, visual: 0x008560, cursor: 0x005A44, border: 0x009A70, floatBG: 0x004F3A}),
	mfdTheme("mfd-lumon", "MFD Lumon", mfdPalette{bg: 0x0A1520, fg: 0x5AC8D8, dim: 0x1A3848, bright: 0xA0F0FF, subtle: 0x143040, visual: 0x122838, cursor: 0x0E1E2C, border: 0x1A3848, floatBG: 0x0C1822, diffAdd: 0x66DD88, diffDel: 0xDD8899, diffHunk: 0x66CCEE}),
	mfdTheme("mfd-nerv", "MFD NERV", mfdPalette{bg: 0x1A0A02, fg: 0xEE8822, dim: 0x6B3510, bright: 0xFFAA44, subtle: 0x4A2008, visual: 0x2A1208, cursor: 0x221005, border: 0x4A2008, floatBG: 0x180A02}),
	mfdTheme("mfd-blackout", "MFD Blackout", mfdPalette{bg: 0x000000, fg: 0x24282C, dim: 0x181C20, bright: 0x24282C, subtle: 0x0C0E10, visual: 0x0C0E10, cursor: 0x080A0C, border: 0x101214, floatBG: 0x060808}),
	mfdTheme("mfd-flir", "MFD FLIR", mfdPalette{bg: 0x181818, fg: 0x909090, dim: 0x404040, bright: 0xA8A8A8, subtle: 0x2E2E2E, visual: 0x242424, cursor: 0x202020, border: 0x2E2E2E, floatBG: 0x1C1C1C}),
	mfdTheme("mfd-flir-bh", "MFD FLIR BH", mfdPalette{bg: 0xB8B8B8, fg: 0x505050, dim: 0x909090, bright: 0x383838, subtle: 0xA8A8A8, visual: 0xACACAC, cursor: 0xB0B0B0, border: 0xA8A8A8, floatBG: 0xBCBCBC}),
	mfdTheme("mfd-flir-rh", "MFD FLIR RH", mfdPalette{bg: 0x181818, fg: 0x909090, dim: 0x404040, bright: 0xC03838, subtle: 0x2E2E2E, visual: 0x242424, cursor: 0x202020, border: 0x2E2E2E, floatBG: 0x1C1C1C}),
	mfdTheme("mfd-flir-fusion", "MFD FLIR Fusion", mfdPalette{bg: 0x1A0A20, fg: 0xC08840, dim: 0x503060, bright: 0xE8C050, subtle: 0x2A1830, visual: 0x241430, cursor: 0x201028, border: 0x2A1830, floatBG: 0x1C0C22}),
}

// All returns every selectable theme: dark, light, then the mfd pack.
func All() []Named {
	out := []Named{
		{Name: "dark", Label: "Dark", Palette: Dark},
		{Name: "light", Label: "Light", Palette: Light},
	}
	return append(out, mfdPalettes...)
}

// ByName looks up a palette by its id; ok is false if unknown.
func ByName(name string) (Theme, bool) {
	for _, n := range All() {
		if n.Name == name {
			return n.Palette, true
		}
	}
	return Theme{}, false
}

func mfdTheme(name, label string, p mfdPalette) Named {
	return Named{
		Name:  name,
		Label: label,
		Palette: Theme{
			BG:       MfdColor(p.bg),
			Bright:   MfdColor(p.bright),
			FG:       MfdColor(p.fg),
			Subtle:   MfdColor(p.dim),
			Dim:      MfdColor(p.subtle),
			Muted:    MfdColor(p.border),
			Accent:   MfdColor(p.bright),
			Info:     MfdColor(p.fg),
			Success:  MfdColor(p.fg),
			Warning:  MfdColor(p.bright),
			Error:    MfdColor(p.bright),
			SelBG:    MfdColor(p.visual),
			GroupBG:  MfdColor(p.cursor),
			ThreadBG: MfdColor(p.floatBG),
			DiffAdd:  optColor(p.diffAdd),
			DiffDel:  optColor(p.diffDel),
			DiffHunk: optColor(p.diffHunk),
		},
	}
}

// optColor returns a real colour for a non-zero hex, or the zero Color (unset) for
// 0 — so a palette can opt into explicit diff hues without 0 meaning pure black.
func optColor(hex uint32) Color {
	if hex == 0 {
		return Color{}
	}
	return MfdColor(hex)
}

func MfdColor(hex uint32) Color {
	return Color{
		Mode: ColorRGB,
		R:    uint8((hex >> 16) & 0xFF),
		G:    uint8((hex >> 8) & 0xFF),
		B:    uint8(hex & 0xFF),
	}
}
