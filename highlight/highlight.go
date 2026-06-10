package highlight

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/theme"
)

// syntaxStyle is the chroma style added-line code is coloured with. It starts as
// monokai and is rebuilt from the active recap theme via SetTheme, so code colours
// follow the palette. Tokens with no explicit colour fall back to the line's base
// colour (so plain identifiers/operators aren't garish).
var syntaxStyle = styles.Get("monokai")

// LexerFor resolves a chroma lexer for a file path (by name/extension), coalesced so
// adjacent same-type tokens merge into one span. Returns nil when the language is unknown
// (the caller then renders the line unhighlighted).
func LexerFor(path string) chroma.Lexer {
	l := lexers.Match(path)
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

// Parts tokenises a single line of code with the given lexer and returns Textf
// parts (one FG span per token), each token coloured by syntaxStyle. Tokens without a
// style colour use fallback. With no lexer / empty code / a tokenise error it returns a
// single fallback-coloured span, so the line still renders. The input must NOT carry
// leading whitespace (render that separately — Rich trims it) or a newline.
func Parts(code string, lexer chroma.Lexer, fallback Color) []any {
	if lexer == nil || code == "" {
		return []any{FG(code, fallback)}
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return []any{FG(code, fallback)}
	}
	st := syntaxStyle
	if st == nil {
		st = styles.Fallback
	}
	var parts []any
	for _, tok := range it.Tokens() {
		val := strings.ReplaceAll(tok.Value, "\n", "")
		if val == "" {
			continue
		}
		col := fallback
		if e := st.Get(tok.Type); e.Colour.IsSet() {
			col = RGB(e.Colour.Red(), e.Colour.Green(), e.Colour.Blue())
		}
		parts = append(parts, FG(val, col))
	}
	if len(parts) == 0 {
		return []any{FG(code, fallback)}
	}
	return parts
}

// SetTheme rebuilds the syntax style from a recap theme, so highlighted code uses
// the palette's own hues instead of a fixed monokai — the mfd themes were scraped
// from a vim colourscheme, so their slots map naturally onto token classes. The
// mapping is ONE table below; tweak freely. Falls back to monokai if the style
// can't build (it shouldn't).
func SetTheme(t theme.Theme) {
	hex := func(c Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	st, err := chroma.NewStyle("recap-theme", chroma.StyleEntries{
		chroma.Text:          hex(t.FG),
		chroma.Name:          hex(t.FG),
		chroma.Comment:       "italic " + hex(t.Muted),
		chroma.Keyword:       hex(t.Info),
		chroma.NameClass:     hex(t.Info),
		chroma.NameBuiltin:   hex(t.Info),
		chroma.NameFunction:  hex(t.Bright),
		chroma.LiteralString: hex(t.Success),
		chroma.LiteralNumber: hex(t.Bright),
		chroma.Operator:      hex(t.Subtle),
		chroma.Punctuation:   hex(t.Subtle),
		chroma.Error:         hex(t.Error),
	})
	if err != nil {
		syntaxStyle = styles.Get("monokai")
		return
	}
	syntaxStyle = st
}
