package highlight

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/kungfusheep/recap/theme"
)

// SetTheme derives the chroma style from the palette: keywords take Info, strings
// Success, comments Muted (italic), errors Error — so highlighted code matches the
// active recap theme instead of fixed monokai. Falsifiable per token class.
func TestSetThemeMapsPalette(t *testing.T) {
	amber, ok := theme.ByName("mfd-amber")
	if !ok {
		t.Fatal("mfd-amber should resolve")
	}
	SetTheme(amber)

	check := func(tok chroma.TokenType, want [3]uint8, label string) {
		e := syntaxStyle.Get(tok)
		if !e.Colour.IsSet() {
			t.Fatalf("%s: no colour set", label)
		}
		got := [3]uint8{e.Colour.Red(), e.Colour.Green(), e.Colour.Blue()}
		if got != want {
			t.Fatalf("%s: colour %v, want %v", label, got, want)
		}
	}
	check(chroma.Keyword, [3]uint8{amber.Info.R, amber.Info.G, amber.Info.B}, "keyword→Info")
	check(chroma.LiteralString, [3]uint8{amber.Success.R, amber.Success.G, amber.Success.B}, "string→Success")
	check(chroma.Comment, [3]uint8{amber.Muted.R, amber.Muted.G, amber.Muted.B}, "comment→Muted")
	check(chroma.Error, [3]uint8{amber.Error.R, amber.Error.G, amber.Error.B}, "error→Error")
	if syntaxStyle.Get(chroma.Comment).Italic != chroma.Yes {
		t.Fatal("comments should be italic")
	}

	// sub-token inheritance: KeywordType (Go's int/string/etc.) falls back to Keyword
	check(chroma.KeywordType, [3]uint8{amber.Info.R, amber.Info.G, amber.Info.B}, "keyword-type inherits")

	// switching themes switches the style
	dark := theme.Dark
	SetTheme(dark)
	check(chroma.Keyword, [3]uint8{dark.Info.R, dark.Info.G, dark.Info.B}, "after switch, keyword→dark Info")
}
