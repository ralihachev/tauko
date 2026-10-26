package main

import (
	"encoding/json"
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
