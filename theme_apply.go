package main

import (
	"encoding/json"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/highlight"
	"github.com/kungfusheep/recap/theme"
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
	dbp, err := db.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dbp), "settings.json"), nil
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

func setThemeVars(t theme.Theme) {
	highlight.SetTheme(t) // code colours follow the palette (diff layer invalidates on switch)
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
	cAdd = theme.EnsureContrast(diffColour(t.DiffAdd, diffAddBase, t.FG), t.BG, theme.WCAGAA)
	cDel = theme.EnsureContrast(diffColour(t.DiffDel, diffDelBase, t.FG), t.BG, theme.WCAGAA)
	cHunk = theme.EnsureContrast(diffColour(t.DiffHunk, diffHunkBase, t.FG), t.BG, theme.WCAGAA)
	cCommentBG = Lerp(t.BG, t.Info, 0.18) // faint wash of the accent over the bg
	listBaseStyle = Style{BG: cPaneBG}
	paneStyle = Style{Fill: cPaneBG, BG: cPaneBG, FG: cFG}
	bgStyle = Style{Fill: cBG, BG: cBG, FG: cFG}
	floatStyle = Style{Fill: cFloat, BG: cFloat, FG: cFG}
	omniListStyle = Style{BG: cFloat}
	omniSelStyle = Style{FG: cBright, BG: cSelBG}
	scrollTrackStyle = Style{FG: cMuted, BG: cBG}
	scrollThumbStyle = Style{FG: cSubtle, BG: cBG}
	titleBoldStyle = Style{FG: cBright, Attr: AttrBold}
	titlePlainStyle = Style{FG: cBright}
}

// initTheme loads the persisted theme (falling back to dark) and sets the colour
// vars. Call once at startup BEFORE building any view.
func initTheme() {
	name := loadSettings().Theme
	if name == "" {
		name = "dark"
	}
	t, ok := theme.ByName(name)
	if !ok {
		name, t = "dark", theme.Dark
	}
	currentThemeName = name
	setThemeVars(t)
}

// applyTheme switches the palette at runtime. The views bind every colour by pointer
// (&cFG, CascadeStyle(&paneStyle), …) and setThemeVars mutates those package vars in
// place, so the build-once templates repaint on the next render — NO view rebuild
// (mail's pattern). Only the diff Layer, which bakes its spans into a pre-rendered
// buffer, needs an explicit Invalidate to re-render with the new palette.
func applyTheme(name string, t theme.Theme) {
	currentThemeName = name
	setThemeVars(t)
	if uiApp != nil {
		// the default style backs cells the templates don't paint (e.g. the screen
		// margins) — keep it in step with the palette.
		uiApp.SetDefaultStyle(Style{FG: cFG, BG: cBG})
		detailDirty = true
		if diffUI.Layer != nil {
			diffUI.Layer.Invalidate()
		}
		uiApp.RequestRender()
	}
}
