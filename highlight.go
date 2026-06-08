package main

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	. "github.com/kungfusheep/glyph"
)

// syntaxStyle is the chroma style added-line code is coloured with. monokai reads well on
// the dark diff background; tokens with no explicit colour fall back to the line's base
// colour (so plain identifiers/operators aren't garish). Theme-aware mapping is a possible
// follow-up — for now one good dark style.
var syntaxStyle = styles.Get("monokai")

// lexerFor resolves a chroma lexer for a file path (by name/extension), coalesced so
// adjacent same-type tokens merge into one span. Returns nil when the language is unknown
// (the caller then renders the line unhighlighted).
func lexerFor(path string) chroma.Lexer {
	l := lexers.Match(path)
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

// highlightParts tokenises a single line of code with the given lexer and returns Textf
// parts (one FG span per token), each token coloured by syntaxStyle. Tokens without a
// style colour use fallback. With no lexer / empty code / a tokenise error it returns a
// single fallback-coloured span, so the line still renders. The input must NOT carry
// leading whitespace (render that separately — Rich trims it) or a newline.
func highlightParts(code string, lexer chroma.Lexer, fallback Color) []any {
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
