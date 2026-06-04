package main

import (
	"os"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// regression (review #49): the diff pane is a native-scroll Layer whose spans bake
// their colours at build time, so a theme switch must REBUILD the diff content —
// otherwise it keeps the old palette even while scrolling. This drives the real
// path (refreshDetail) against a git diff and asserts an added line's colour
// changes from one theme's add-colour to the next's.
func TestThemeRepaintsDiffLayer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	g := func(a ...string) { git(dir, a...) }
	g("init")
	g("config", "user.email", "t@t")
	g("config", "user.name", "t")
	os.WriteFile(dir+"/a.txt", []byte("line one\n"), 0o644)
	g("add", "-A")
	g("commit", "-m", "add a.txt")
	sha, _ := git(dir, "rev-parse", "--short", "HEAD")

	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	expandedTasks = map[int64]bool{}
	t.Cleanup(func() {
		uiStore = nil
		uiApp = nil
		omni = nil
		vmRows = nil
		setThemeVars(themeDark)
	})
	st.Add(Task{Repo: "r", RepoPath: dir, SHA: sha, Title: "t", Status: StatusPending})
	setThemeVars(themeDark)
	reloadTasks()
	sel = 0

	addLineFG := func() (Color, bool) {
		detailDirty = true
		lastSel = -99
		refreshDetail()
		for _, row := range diffLines {
			for _, s := range row {
				if strings.HasPrefix(s.Text, "+ ") {
					return s.Style.FG, true
				}
			}
		}
		return Color{}, false
	}

	darkFG, ok := addLineFG()
	if !ok {
		t.Fatal("no added line found in diff")
	}
	if darkFG != themeDark.Success {
		t.Fatalf("dark add-line colour = %v, want %v", darkFG, themeDark.Success)
	}

	amber, _ := themeByName("mfd-amber")
	applyTheme("mfd-amber", amber)
	amberFG, ok := addLineFG()
	if !ok {
		t.Fatal("no added line after theme switch")
	}
	if amberFG != amber.Success {
		t.Fatalf("after switch: add-line colour = %v, want amber %v", amberFG, amber.Success)
	}
	if amberFG == darkFG {
		t.Fatal("diff colour did not change on theme switch (the layer-repaint bug)")
	}
}

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

// the persisted theme round-trips through settings.json (beside the db), and
// initTheme restores it; an unknown/blank name falls back to dark.
func TestThemePersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	prevName := currentThemeName
	t.Cleanup(func() { currentThemeName = prevName; setThemeVars(themeDark) })

	// nothing saved yet → initTheme falls back to dark
	initTheme()
	if currentThemeName != "dark" {
		t.Fatalf("no settings: want dark, got %q", currentThemeName)
	}

	if err := saveThemeName("mfd-nerv"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadSettings().Theme; got != "mfd-nerv" {
		t.Fatalf("loaded theme = %q, want mfd-nerv", got)
	}
	// initTheme picks it up and applies its palette to the colour vars
	initTheme()
	if currentThemeName != "mfd-nerv" {
		t.Fatalf("after restore: want mfd-nerv, got %q", currentThemeName)
	}
	nerv, _ := themeByName("mfd-nerv")
	if cBG != nerv.BG {
		t.Fatalf("initTheme did not apply the palette: cBG=%v want %v", cBG, nerv.BG)
	}

	// a corrupt/unknown saved name falls back to dark
	if err := saveThemeName("does-not-exist"); err != nil {
		t.Fatal(err)
	}
	initTheme()
	if currentThemeName != "dark" {
		t.Fatalf("unknown saved theme should fall back to dark, got %q", currentThemeName)
	}
}

// the command palette exposes every theme, and selecting one applies + persists
// it (here with uiApp nil so applyTheme just sets vars — no rebuild).
func TestThemeCommandsInPalette(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")
	prevApp, prevName := uiApp, currentThemeName
	uiApp = nil
	t.Cleanup(func() { uiApp = prevApp; currentThemeName = prevName; setThemeVars(themeDark) })

	cmds := omniCommands()
	var amberCmd *omniItem
	count := 0
	for i := range cmds {
		if cmds[i].Section == "theme" {
			count++
			if cmds[i].Label == "theme: MFD Amber" {
				amberCmd = &cmds[i]
			}
		}
	}
	if count != 20 {
		t.Fatalf("want 20 theme commands in the palette, got %d", count)
	}
	if amberCmd == nil || amberCmd.Action == nil {
		t.Fatal("MFD Amber theme command missing or unwired")
	}

	amberCmd.Action()
	amber, _ := themeByName("mfd-amber")
	if currentThemeName != "mfd-amber" {
		t.Fatalf("selecting the theme command did not switch: %q", currentThemeName)
	}
	if cBG != amber.BG {
		t.Fatalf("palette colour not applied: cBG=%v want %v", cBG, amber.BG)
	}
	if got := loadSettings().Theme; got != "mfd-amber" {
		t.Fatalf("theme not persisted: settings.Theme=%q", got)
	}
}

// applying a theme must actually repaint: recap bakes colours at build time, so
// this verifies by RENDER that rebuilding buildMain after setThemeVars changes a
// background cell's colour to the new palette's BG (a re-render alone wouldn't).
func TestApplyThemeRepaints(t *testing.T) {
	st := testStore(t)
	prevStore, prevApp, prevOmni, prevName := uiStore, uiApp, omni, currentThemeName
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore = prevStore
		uiApp = prevApp
		omni = prevOmni
		currentThemeName = prevName
		setThemeVars(themeDark) // restore the default palette for other tests
	})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	reloadTasks()

	bgCell := func() Color {
		tmpl := Build(buildMain())
		buf := NewBuffer(100, 30)
		tmpl.Execute(buf, 100, 30)
		// the middle column sits on the app bg (cBG); sample a cell there
		return buf.Get(60, 5).Style.BG
	}

	setThemeVars(themeDark)
	darkBG := bgCell()
	if darkBG != themeDark.BG {
		t.Fatalf("dark: bg cell = %v, want %v", darkBG, themeDark.BG)
	}

	amber, _ := themeByName("mfd-amber")
	setThemeVars(amber)
	amberBG := bgCell()
	if amberBG != amber.BG {
		t.Fatalf("after theme switch: bg cell = %v, want amber %v", amberBG, amber.BG)
	}
	if amberBG == darkBG {
		t.Fatal("background colour did not change on theme switch")
	}
}

// changing theme while the command palette is open must not orphan the palette's
// modal router on the input stack — otherwise it swallows every key but its own
// and you can't even quit (#64). After applyTheme the stack is back to base.
func TestThemeChangeNoOrphanedModalRouter(t *testing.T) {
	prevStore := uiStore
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() { uiStore = prevStore; uiApp = nil; omni = nil })
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "t", Status: StatusPending})
	reloadTasks()
	uiApp.SetView(buildMain())
	uiApp.RenderNow()

	base := uiApp.Input().Depth()
	omni.Open()
	uiApp.RenderNow()
	if d := uiApp.Input().Depth(); d <= base {
		t.Fatalf("omnibox should push a modal router: base=%d open=%d", base, d)
	}

	// simulate selecting a theme command: exec() closes the palette, then the action
	// runs applyTheme (which rebuilds the view via SetView).
	omni.Close()
	th := allThemes()[1]
	applyTheme(th.Name, th.Palette)

	if d := uiApp.Input().Depth(); d != base {
		t.Fatalf("after theme change the input stack should be back to base %d, got %d (orphaned modal router → keys dead)", base, d)
	}
}
