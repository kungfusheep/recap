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
func setThemeVars(t Theme) {
	cBG = t.BG
	cPaneBG = t.ThreadBG
	cBright = t.Bright
	cFG = t.FG
	cSubtle = t.Subtle
	// muted text (timestamps, hints, separators) maps to the palette's border
	// colour, which is too dark to read on several dark themes (#169). Nudge it
	// toward the foreground so it stays clearly dimmer than normal text but legible
	// — works for light themes too (fg is dark there, so it darkens).
	cMuted = Lerp(t.Muted, t.FG, 0.3)
	cSelBG = t.SelBG
	cGroupBG = t.GroupBG
	cFloat = t.GroupBG
	cAdd = t.Success
	cDel = t.Error
	cHunk = t.Info
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
		// A modal launched this (the command palette). Its On.Modal router is pushed
		// on the input stack, and the overlay's fade-out exit animation defers the
		// route-modal pop by a few frames. SetView rebuilds the tree right now —
		// before that fade finishes — so the router is orphaned on the stack, where
		// it swallows every key but its own (Up/Down/Enter/Esc): you couldn't even
		// quit (#64). The old tree's pushed routers are about to be discarded with it,
		// so drain the input stack back to its base router before rebuilding.
		if omni != nil {
			omni.Close()
		}
		for in := uiApp.Input(); in != nil && in.Depth() > 1; {
			uiApp.PopRouter()
		}
		uiApp.SetView(buildMain())
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
