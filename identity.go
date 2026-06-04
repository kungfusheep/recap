package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/kungfusheep/glyph"
)

// The agent's session identity: a name + personal colour the agent assigns itself
// (per the tododo/recap loop), so its comments/replies are legible as a distinct
// voice in the review threads. Recap-only — never touches git. Stored beside the db
// ("name\n#RRGGBB") so the CLI (which authors comments) and the TUI (which renders
// them) share it. Works with no config; an optional config name-theme can guide the
// agent's choice (see Config.NameTheme).

// agentName / agentColor are the render-thread view of the identity (TUI). The CLI
// reads the file directly via identityWho().
var (
	agentName  string
	agentColor = Hex(0x6f8fa8) // sensible default until the agent names itself
)

func identityPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "identity"), nil
}

// loadIdentity reads the stored name + colour ("" / default if unset). Tiny file —
// read on the reload path alongside the inbox reload.
func loadIdentity() (name string, color Color) {
	color = Hex(0x6f8fa8)
	p, err := identityPath()
	if err != nil {
		return "", color
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", color
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	name = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		if c, ok := parseHexColor(strings.TrimSpace(lines[1])); ok {
			color = c
		}
	}
	return name, color
}

// refreshIdentity reloads the identity into the TUI's agentName/agentColor.
func refreshIdentity() { agentName, agentColor = loadIdentity() }

// saveIdentity persists the agent's chosen name + colour (empty name clears it).
func saveIdentity(name, hex string) error {
	p, err := identityPath()
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if _, ok := parseHexColor(hex); hex != "" && !ok {
		return fmt.Errorf("colour must be #RRGGBB, got %q", hex)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(name)+"\n"+strings.TrimSpace(hex)+"\n"), 0o644)
}

// identityWho returns the agent's session name for authoring comments, or "agent"
// if it hasn't named itself. Used by the CLI verbs.
func identityWho() string {
	if name, _ := loadIdentity(); name != "" {
		return name
	}
	return "agent"
}

// parseHexColor turns "#RRGGBB" (or "RRGGBB") into a glyph Color.
func parseHexColor(s string) (Color, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return Color{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return Color{}, false
	}
	return Hex(uint32(v)), true
}
