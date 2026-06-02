module github.com/kungfusheep/recap

go 1.25.1

require (
	github.com/kungfusheep/glyph v0.0.0
	github.com/kungfusheep/riffkey v0.0.0
	github.com/mattn/go-sqlite3 v1.14.37
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/clipperhouse/stringish v0.1.1 // indirect
	github.com/clipperhouse/uax29/v2 v2.4.0 // indirect
	github.com/junegunn/fzf v0.67.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.40.0 // indirect
)

replace github.com/kungfusheep/glyph => ../tui

replace github.com/kungfusheep/riffkey => ../riffkey
