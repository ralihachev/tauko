package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// marketplace is where `claude plugin marketplace add` finds the tauko plugin.
const marketplace = "ralihachev/tauko"

// setup installs the Claude Code plugin, removes hooks from older tauko versions and
// starts the server. Running it again is safe.
func setup(args []string) error {
	fl := flag.NewFlagSet("setup", flag.ExitOnError)
	local := fl.String("local", "", "install the plugin from this tauko checkout instead of GitHub (for development)")
	fl.Parse(args)

	source := marketplace
	if *local != "" {
		abs, err := filepath.Abs(*local)
		if err != nil {
			return err
		}
		source = abs
	}

	step("Starting the tauko server")
	if err := ensure(); err != nil {
		return err
	}
	ok("server on " + serverURL())

	if _, err := exec.LookPath("claude"); err != nil {
		warn("Claude Code not found, so the pane is skipped. Install it from https://claude.com/claude-code, then run `tauko setup` again.")
		done()
		return nil
	}

	step("Installing the Claude Code plugin")
	if out, err := claudeCLI("plugin", "marketplace", "add", source); err != nil {
		return fmt.Errorf("adding the plugin marketplace: %w\n%s", err, out)
	}
	if out, err := claudeCLI("plugin", "install", "tauko@tauko", "--scope", "user"); err != nil {
		return fmt.Errorf("installing the plugin: %w\n%s", err, out)
	}
	// Already installed? Bring it up to date, so re-running the installer upgrades both halves.
	claudeCLI("plugin", "marketplace", "update", "tauko")
	claudeCLI("plugin", "update", "tauko@tauko", "--scope", "user")
	ok("plugin tauko@tauko installed and up to date (restart open Claude Code sessions to load an update)")

	if path, removed, err := removeLegacyHooks(); err != nil {
		warn("couldn't check " + path + " for old tauko hooks: " + err.Error())
	} else if removed > 0 {
		ok(fmt.Sprintf("removed %d hook(s) from an older tauko version in %s (backup next to it)", removed, path))
	}
	done()
	return nil
}

func done() {
	if dirs, _ := os.ReadDir(filepath.Join(dataDir(), "courses")); len(dirs) == 0 {
		fmt.Printf(`
%sNext: get a course.%s
  tauko gen "Kubernetes networking"   generate one about anything with Claude
  tauko add                           browse the gallery (tauko add go-concurrency)
`, bold, reset)
	}
	fmt.Printf(`
%sDone.%s Run %stauko claude%s and send a prompt: the tauko pane opens beside the chat while Claude works.
  /tauko         open the pane by hand (ctrl+x tab focuses it, Esc goes back)
  tauko          learn in the terminal
  %s  learn in the browser or on your phone
  tauko doctor   check the setup
`, green+bold, reset, bold, reset, serverURL())
}

// doctor checks every piece of the setup and says how to fix what's wrong.
func doctor() error {
	fmt.Printf("%stauko %s%s\n", bold, version, reset)
	problems := 0
	check := func(good bool, label, fix string) {
		if good {
			ok(label)
			return
		}
		problems++
		fmt.Printf("  %s✗%s %s\n    %s→ %s%s\n", red, reset, label, dim, fix, reset)
	}

	self, _ := os.Executable()
	check(self != "", "binary: "+self, "reinstall: curl -fsSL https://raw.githubusercontent.com/ralihachev/tauko/main/install.sh | sh")

	up := newClient(500 * time.Millisecond).up()
	check(up, "server answers on "+serverURL(), "run `tauko ensure`, or see ~/.tauko/serve.log")
	if up {
		var s statsResp
		if err := newClient(time.Second).do("GET", "/api/stats", nil, &s); err == nil {
			ok(fmt.Sprintf("%d cards · %d due · %d new left today", s.Cards, s.Due, s.NewLeft))
			for _, w := range s.Warnings {
				check(false, w, "fix the course (see docs/COURSES.md), then `tauko restart`")
			}
		}
	}

	_, err := exec.LookPath("claude")
	check(err == nil, "Claude Code CLI found", "install Claude Code from https://claude.com/claude-code (the terminal and browser work without it)")
	if err == nil {
		out, _ := claudeCLI("plugin", "list")
		check(strings.Contains(out, "tauko@tauko"), "Claude Code plugin installed", "run `tauko setup`")
	}
	if path, n, err := legacyHookCount(); err == nil && n > 0 {
		check(false, fmt.Sprintf("%d old tauko hook(s) in %s", n, path), "run `tauko setup` to remove them (the plugin does their job now)")
	}

	if problems == 0 {
		fmt.Printf("\n%sAll good.%s\n", green+bold, reset)
		return nil
	}
	return fmt.Errorf("%d problem(s) found", problems)
}

// uninstall removes the plugin and stops the server. Data in ~/.tauko is kept unless purge.
func uninstall(args []string) error {
	fl := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fl.Bool("purge", false, "also delete your progress in ~/.tauko")
	fl.Parse(args)

	if _, err := exec.LookPath("claude"); err == nil {
		claudeCLI("plugin", "uninstall", "tauko@tauko", "--scope", "user")
		claudeCLI("plugin", "marketplace", "remove", "tauko")
		ok("Claude Code plugin removed")
	}
	if path, removed, err := removeLegacyHooks(); err == nil && removed > 0 {
		ok(fmt.Sprintf("removed %d old tauko hook(s) from %s", removed, path))
	}
	if stopServer() {
		ok("server stopped")
	}
	dir := dataDir()
	if *purge {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		ok("deleted " + dir)
	} else {
		fmt.Printf("  Your progress is kept in %s (remove it with `tauko uninstall -purge`).\n", dir)
	}
	if self, err := os.Executable(); err == nil {
		fmt.Printf("  Last step: delete the binary with `rm %s`\n", self)
	}
	return nil
}

// launchClaude replaces tauko with Claude Code in fullscreen mode, where the pane docks
// beside the chat. Any flags go to claude unchanged.
func launchClaude(args []string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("Claude Code not found: install it from https://claude.com/claude-code")
	}
	if err := ensure(); err != nil {
		warn("tauko server not running, so the pane will say so: " + err.Error())
	}
	env := append(os.Environ(), "CLAUDE_CODE_NO_FLICKER=1")
	return syscall.Exec(path, append([]string{"claude"}, args...), env)
}

func claudeCLI(args ...string) (string, error) {
	out, err := exec.Command("claude", args...).CombinedOutput()
	return string(out), err
}

func dataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tauko")
}

func serverURL() string {
	if u := os.Getenv("TAUKO_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:8787"
}

// stopServer stops the server started by `tauko ensure`/`serve`, found by its pid file.
func stopServer() bool {
	raw, err := os.ReadFile(filepath.Join(dataDir(), "serve.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil || p.Signal(syscall.SIGTERM) != nil {
		return false
	}
	os.Remove(filepath.Join(dataDir(), "serve.pid"))
	return true
}

// Older tauko versions asked users to add hooks and a statusLine to settings.json;
// the plugin does all of that now.
func isLegacyCommand(cmd string) bool {
	return strings.Contains(cmd, "tauko signal") || strings.Contains(cmd, "tauko ensure") || strings.Contains(cmd, "tauko status")
}

func settingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func legacyHookCount() (string, int, error) {
	path := settingsPath()
	settings, err := readSettings(path)
	if err != nil {
		return path, 0, err
	}
	_, n := stripLegacy(settings)
	return path, n, nil
}

// removeLegacyHooks rewrites settings.json without old tauko hooks, after backing it up.
func removeLegacyHooks() (string, int, error) {
	path := settingsPath()
	settings, err := readSettings(path)
	if err != nil || settings == nil {
		return path, 0, err
	}
	cleaned, n := stripLegacy(settings)
	if n == 0 {
		return path, 0, nil
	}
	raw, _ := os.ReadFile(path)
	backup := fmt.Sprintf("%s.tauko-backup-%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return path, 0, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cleaned); err != nil {
		return path, 0, err
	}
	return path, n, os.WriteFile(path, buf.Bytes(), 0o600)
}

func readSettings(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	return settings, nil
}

// stripLegacy drops tauko hook commands (and groups/events left empty) and a tauko
// statusLine. It returns how many it removed; other settings are untouched.
func stripLegacy(settings map[string]any) (map[string]any, int) {
	if settings == nil {
		return nil, 0
	}
	removed := 0
	if sl, ok := settings["statusLine"].(map[string]any); ok {
		if cmd, _ := sl["command"].(string); isLegacyCommand(cmd) {
			delete(settings, "statusLine")
			removed++
		}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return settings, removed
	}
	for event, v := range hooks {
		groups, ok := v.([]any)
		if !ok {
			continue
		}
		var keptGroups []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			inner, _ := group["hooks"].([]any)
			var kept []any
			for _, h := range inner {
				if hm, ok := h.(map[string]any); ok {
					if cmd, _ := hm["command"].(string); isLegacyCommand(cmd) {
						removed++
						continue
					}
				}
				kept = append(kept, h)
			}
			if len(kept) == 0 && len(inner) > 0 {
				continue
			}
			group["hooks"] = kept
			keptGroups = append(keptGroups, group)
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return settings, removed
}

func step(s string) { fmt.Printf("%s→ %s%s\n", cyan, s, reset) }
func ok(s string)   { fmt.Printf("  %s✓%s %s\n", green, reset, s) }
func warn(s string) { fmt.Printf("  %s!%s %s\n", yellow, reset, s) }
