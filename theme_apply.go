package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/kungfusheep/glyph"
)

// currentThemeName is the active theme id (persisted in settings.json).
var currentThemeName = "dark"

type recapSettings struct {
	Theme string `json:"theme,omitempty"`
}

// settingsPath sits beside the review db ($RECAP_DB's dir or ~/.config/recap), so
// an isolated db gets isolated settings.
func settingsPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "settings.json"), nil
}

func loadSettings() recapSettings {
	p, err := settingsPath()
	if err != nil {
		return recapSettings{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return recapSettings{}
	}
	var s recapSettings
	_ = json.Unmarshal(data, &s)
	return s
}

func saveThemeName(name string) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(recapSettings{Theme: name}, "", "  ")
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// setThemeVars maps a palette onto recap's package colour vars and the reactive
// style vars. No rebuild — used at startup, before the view trees are built.
// diffColour picks a theme's explicit diff hue when set, else blends the canonical
// base toward the foreground for tonal sympathy. (Contrast is enforced by caller.)
func diffColour(override, base, fg Color) Color {
	if override.Mode != 0 {
		return override
	}
	return Lerp(base, fg, 0.25)
}

func setThemeVars(t Theme) {
	cBG = t.BG
	cPaneBG = t.ThreadBG
	cBright = t.Bright
	cFG = t.FG
	cSubtle = t.Subtle
	// muted text (timestamps, hints, separators) maps to the palette's border
	// colour, which is too dark to read on several dark themes. Nudge it toward the
	// foreground so it stays clearly dimmer than normal text but legible — works for
	// light themes too (fg is dark there, so it darkens).
	cMuted = Lerp(t.Muted, t.FG, 0.3)
	cSelBG = t.SelBG
	cGroupBG = t.GroupBG
	cFloat = t.GroupBG
	// diff colours: blend the canonical add/del/hunk hues toward the theme's fg so
	// they stay distinct (green/red/blue) but sympathetic to the palette's tone —
	// the mfd themes map Success/Error/Info to fg/bright/fg, which made +/@@ identical
	// and the diff unreadable, so derive them instead.
	// diff colours: use the theme's explicit diff hues if it sets them (e.g. lumon's
	// terminal ANSI colours, which read better), otherwise blend the canonical hues
	// toward the theme tone for sympathy. Either way enforce WCAG AA against the bg.
	cAdd = ensureContrast(diffColour(t.DiffAdd, diffAddBase, t.FG), t.BG, wcagAA)
	cDel = ensureContrast(diffColour(t.DiffDel, diffDelBase, t.FG), t.BG, wcagAA)
	cHunk = ensureContrast(diffColour(t.DiffHunk, diffHunkBase, t.FG), t.BG, wcagAA)
	cCommentBG = Lerp(t.BG, t.Info, 0.18) // faint wash of the accent over the bg
	listBaseStyle = Style{BG: cPaneBG}
	paneStyle = Style{Fill: cPaneBG, BG: cPaneBG, FG: cFG}
}

// initTheme loads the persisted theme (falling back to dark) and sets the colour
// vars. Call once at startup BEFORE building any view.
func initTheme() {
	name := loadSettings().Theme
	if name == "" {
		name = "dark"
	}
	t, ok := themeByName(name)
	if !ok {
		name, t = "dark", themeDark
	}
	currentThemeName = name
	setThemeVars(t)
}

// applyTheme switches the palette at runtime. recap bakes most colours into the
// compiled templates at build time, so a re-render alone won't repaint them — the
// view trees must be rebuilt. This re-runs the modal view setups and buildMain so
// every tree picks up the new colours, then requests a render.
func applyTheme(name string, t Theme) {
	currentThemeName = name
	setThemeVars(t)
	if uiApp != nil {
		// A modal launched this (the command palette). Closing it flips its If false;
		// UpdateView then recompiles each named view, and because it deactivates the
		// active view first (detachRouteScopes pops the palette's pushed modal) and
		// reactivates after, the input stack stays balanced — no manual drain, and no
		// orphaned router swallowing keys. (This is why the todo editor moved to a
		// named view too: glyph's router owns the push/pop, not hand-rolled draining.)
		if omni != nil {
			omni.Close()
		}
		uiApp.UpdateView("main", buildMain())
		uiApp.UpdateView("todo", buildTodoView())
		// the diff pane is a native-scroll Layer whose spans bake their colours at
		// build time; forcing a detail refresh rebuilds those spans with the new
		// palette and re-invalidates the layer (otherwise it keeps the old colours,
		// even while scrolling).
		detailDirty = true
		if diffLayer != nil {
			diffLayer.Invalidate()
		}
		uiApp.RequestRender()
	}
}
