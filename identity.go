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
	agentLabel = "agent"       // agentName, or "agent" when unnamed — for display
	uiRepo     string          // the TUI's repo, cached at startup — refreshIdentity runs on the render thread and can't shell out to git
)

// identityPath is PER-REPO (identity-<repo>) so an agent's name is scoped to the
// project it's working — otherwise a loop in another repo reads this repo's identity
// and "keeps" it (the "everyone names themselves Kestrel" bug). "" repo → shared file.
func identityPath(repo string) (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	name := "identity"
	if repo != "" {
		name = "identity-" + strings.ReplaceAll(repo, string(os.PathSeparator), "_")
	}
	return filepath.Join(filepath.Dir(db), name), nil
}

// loadIdentity reads the repo's stored name + colour ("" / default if unset). Tiny
// file — read on the reload path alongside the inbox reload.
func loadIdentity(repo string) (name string, color Color) {
	color = Hex(0x6f8fa8)
	p, err := identityPath(repo)
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

// refreshIdentity reloads the TUI's repo identity into agentName/agentColor/agentLabel.
// Uses the cached uiRepo (set at startup) — it runs on the render thread, so it must
// not call currentRepo() (which shells out to git).
func refreshIdentity() {
	agentName, agentColor = loadIdentity(uiRepo)
	agentLabel = agentName
	if agentLabel == "" {
		agentLabel = "agent"
	}
}

// saveIdentity persists the repo's chosen name + colour (empty name clears it).
func saveIdentity(repo, name, hex string) error {
	p, err := identityPath(repo)
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
	if name, _ := loadIdentity(currentRepo()); name != "" {
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
