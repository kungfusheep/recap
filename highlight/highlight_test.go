package highlight

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/kungfusheep/recap/theme"
)

// SetTheme mirrors the mfd vim scheme's "monotone with decoration": keywords are
// Bright+bold, functions bold, strings italic, types underlined, comments Muted
// italic, numbers/operators plain FG. Falsifiable per token class.
func TestSetThemeMapsPalette(t *testing.T) {
	amber, ok := theme.ByName("mfd-amber")
	if !ok {
		t.Fatal("mfd-amber should resolve")
	}
	SetTheme(amber)

	colour := func(tok chroma.TokenType) [3]uint8 {
		e := syntaxStyle.Get(tok)
		if !e.Colour.IsSet() {
			t.Fatalf("%v: no colour set", tok)
		}
		return [3]uint8{e.Colour.Red(), e.Colour.Green(), e.Colour.Blue()}
	}
	rgb := func(c [3]uint8, r, g, b uint8) bool { return c == [3]uint8{r, g, b} }

	if c := colour(chroma.Keyword); !rgb(c, amber.Bright.R, amber.Bright.G, amber.Bright.B) {
		t.Fatalf("keyword colour %v, want Bright", c)
	}
	if syntaxStyle.Get(chroma.Keyword).Bold != chroma.Yes {
		t.Fatal("keywords should be bold")
	}
	if c := colour(chroma.LiteralString); !rgb(c, amber.FG.R, amber.FG.G, amber.FG.B) {
		t.Fatalf("string colour %v, want FG", c)
	}
	if syntaxStyle.Get(chroma.LiteralString).Italic != chroma.Yes {
		t.Fatal("strings should be italic")
	}
	if syntaxStyle.Get(chroma.NameFunction).Bold != chroma.Yes {
		t.Fatal("function names should be bold")
	}
	if syntaxStyle.Get(chroma.KeywordType).Underline != chroma.Yes {
		t.Fatal("types should be underlined (vim Type)")
	}
	if c := colour(chroma.Comment); !rgb(c, amber.Muted.R, amber.Muted.G, amber.Muted.B) {
		t.Fatalf("comment colour %v, want Muted", c)
	}
	if syntaxStyle.Get(chroma.Comment).Italic != chroma.Yes {
		t.Fatal("comments should be italic")
	}
	// numbers/operators stay plain fg — the monotone body
	if c := colour(chroma.LiteralNumber); !rgb(c, amber.FG.R, amber.FG.G, amber.FG.B) {
		t.Fatalf("number colour %v, want plain FG", c)
	}
	if syntaxStyle.Get(chroma.LiteralNumber).Bold == chroma.Yes {
		t.Fatal("numbers should not be bold")
	}

	// NON-mono themes use a stock multi-hue style: dark → nord (c319)
	dark := theme.Dark
	if dark.Mono {
		t.Fatal("the default dark theme must not be marked Mono")
	}
	SetTheme(dark)
	nord := styles.Get("nord")
	want := nord.Get(chroma.Keyword).Colour
	if c := colour(chroma.Keyword); !rgb(c, want.Red(), want.Green(), want.Blue()) {
		t.Fatalf("non-mono dark should use nord: keyword %v, want %v", c, want)
	}

	// light non-mono themes get monokailight (nord is a dark scheme)
	light := theme.Light
	if light.Mono {
		t.Fatal("the default light theme must not be marked Mono")
	}
	SetTheme(light)
	ml := styles.Get("monokailight").Get(chroma.Keyword).Colour
	if c := colour(chroma.Keyword); !rgb(c, ml.Red(), ml.Green(), ml.Blue()) {
		t.Fatalf("light should use monokailight: keyword %v, want %v", c, ml)
	}
}
