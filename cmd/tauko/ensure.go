package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// ensure starts `tauko serve` detached in the background unless a server already
// answers. It is meant for a Claude Code SessionStart hook: fast when the server is
// up, and the server outlives the session that started it. A server left running by
// an older tauko is replaced, so an upgrade takes effect without `tauko restart`.
func ensure() error {
	c := newClient(300 * time.Millisecond)
	if c.up() {
		if !outdated(c) {
			return nil
		}
		stopServer()
		time.Sleep(300 * time.Millisecond)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".tauko")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(self, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // own session: survives the hook's process group
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		if c.up() {
			return nil
		}
	}
	return fmt.Errorf("server did not come up; see %s", filepath.Join(dir, "serve.log"))
}

// outdated reports whether the local server is a different release than this binary.
// A local "dev" build never replaces a server, and a remote one (TAUKO_URL) is left alone.
func outdated(c *client) bool {
	if os.Getenv("TAUKO_URL") != "" || version == "dev" {
		return false
	}
	v, err := serverVersion(c)
	return err == nil && v.Version != version
}

func (c *client) up() bool {
	resp, err := c.http.Get(c.base + "/api/stats")
	if err != nil {
		return false
	}
	resp.Body.Close()
	// 401 still means a tauko server is listening (token set).
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}
