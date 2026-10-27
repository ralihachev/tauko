package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStripLegacy(t *testing.T) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(`{
		"model": "opus",
		"statusLine": {"type": "command", "command": "~/go/bin/tauko status"},
		"hooks": {
			"SessionStart": [{"hooks": [{"type": "command", "command": "~/go/bin/tauko ensure"}]}],
			"Stop": [{"hooks": [
				{"type": "command", "command": "~/go/bin/tauko signal idle"},
				{"type": "command", "command": "say done"}
			]}],
			"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "log.sh"}]}]
		}
	}`), &settings); err != nil {
		t.Fatal(err)
	}
	got, n := stripLegacy(settings)
	if n != 3 {
		t.Errorf("removed %d, want 3", n)
	}
	out, _ := json.Marshal(got)
	want := `{"hooks":{"PreToolUse":[{"hooks":[{"command":"log.sh","type":"command"}],"matcher":"Bash"}],"Stop":[{"hooks":[{"command":"say done","type":"command"}]}]},"model":"opus"}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestStripLegacyLeavesOtherStatusLine(t *testing.T) {
	settings := map[string]any{"statusLine": map[string]any{"type": "command", "command": "starship"}}
	if _, n := stripLegacy(settings); n != 0 || settings["statusLine"] == nil {
		t.Errorf("a non-tauko status line was touched")
	}
}

// The pane compares its version with the server's, so it must match the plugin manifest.
func TestPaneVersionMatchesPlugin(t *testing.T) {
	raw, err := os.ReadFile("../../claude-mod/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../claude-mod/hooks/register.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if want := "export const PANE_VERSION = '" + manifest.Version + "'"; !strings.Contains(string(src), want) {
		t.Errorf("register.tsx lacks %q (plugin.json says %s)", want, manifest.Version)
	}
}
