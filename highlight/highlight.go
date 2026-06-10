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

// Spans tokenises a single line of code with the given lexer and returns one glyph
// Span per token, coloured + decorated by the active syntax style (bold keywords,
// italic strings, underlined types — the mfd model). Tokens without a style colour
// use fg; every span carries bg so cells never fall back to the terminal default.
// With no lexer / empty code / a tokenise error it returns a single fg-coloured span,
// so the line still renders. The input must not carry a trailing newline.
func Spans(code string, lexer chroma.Lexer, fg, bg Color) []Span {
	plain := []Span{{Text: code, Style: Style{FG: fg, BG: bg}}}
	if lexer == nil || code == "" {
		return plain
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return plain
	}
	st := syntaxStyle
	if st == nil {
		st = styles.Fallback
	}
	var out []Span
	for _, tok := range it.Tokens() {
		val := strings.ReplaceAll(tok.Value, "\n", "")
		if val == "" {
			continue
		}
		e := st.Get(tok.Type)
		style := Style{FG: fg, BG: bg}
		if e.Colour.IsSet() {
			style.FG = RGB(e.Colour.Red(), e.Colour.Green(), e.Colour.Blue())
		}
		if e.Bold == chroma.Yes {
			style.Attr |= AttrBold
		}
		if e.Italic == chroma.Yes {
			style.Attr |= AttrItalic
		}
		if e.Underline == chroma.Yes {
			style.Attr |= AttrUnderline
		}
		out = append(out, Span{Text: val, Style: style})
	}
	if len(out) == 0 {
		return plain
	}
	return out
}

// SetTheme rebuilds the syntax style from a recap theme. The mfd vim scheme this
// mirrors is "monotone with decoration": hierarchy comes from the brightness ramp
// (Bright/FG/Muted) plus bold/italic/underline, NOT from hues — keywords bright+bold,
// functions bold, strings italic, types underlined, comments dim italic, the rest
// plain fg. Applied uniformly to every theme so code always matches the palette.
// Falls back to monokai if the style can't build (it shouldn't).
func SetTheme(t theme.Theme) {
	// non-mono themes (dark/light) read better with a stock multi-hue style:
	// nord on dark backgrounds, monokailight on light ones (nord is a dark
	// scheme). The decoration model below is the mfd pack's identity.
	if !t.Mono {
		name := "nord"
		if int(t.BG.R)+int(t.BG.G)+int(t.BG.B) > int(t.FG.R)+int(t.FG.G)+int(t.FG.B) {
			name = "monokailight"
		}
		if st := styles.Get(name); st != nil {
			syntaxStyle = st
			return
		}
	}
	hex := func(c Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	st, err := chroma.NewStyle("recap-theme", chroma.StyleEntries{
		chroma.Text:            hex(t.FG),
		chroma.Name:            hex(t.FG),
		chroma.Comment:         "italic " + hex(t.Muted),
		chroma.LiteralString:   "italic " + hex(t.FG),
		chroma.NameFunction:    "bold " + hex(t.FG),
		chroma.Keyword:         "bold " + hex(t.Bright),
		chroma.KeywordType:     "underline " + hex(t.FG), // vim Type: int/string/…
		chroma.KeywordConstant: "bold " + hex(t.FG),      // vim Boolean: true/false/nil
		chroma.NameClass:       "underline " + hex(t.FG),
		chroma.LiteralNumber:   hex(t.FG),
		chroma.Operator:        hex(t.FG),
		chroma.Punctuation:     hex(t.FG),
		chroma.Error:           "bold underline " + hex(t.FG),
	})
	if err != nil {
		syntaxStyle = styles.Get("monokai")
		return
	}
	syntaxStyle = st
}
