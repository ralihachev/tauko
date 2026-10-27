package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// installScript upgrades tauko in place: the binary, the plugin and the server.
const installScript = "https://raw.githubusercontent.com/ralihachev/tauko/main/install.sh"

// latestRelease asks GitHub for the newest tauko release. It is the server's daily
// update check, turned off with TAUKO_NO_UPDATE_CHECK=1.
func latestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", envOr("TAUKO_RELEASES", "https://api.github.com/repos/ralihachev/tauko/releases/latest"), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: %s", resp.Status)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.Tag, nil
}

type versionResp struct {
	Version string `json:"version"`
	Update  string `json:"update"`
}

// serverVersion is the running server's version; "" for one older than v0.5.2,
// which has no /api/version.
func serverVersion(c *client) (versionResp, error) {
	var v versionResp
	err := c.do("GET", "/api/version", nil, &v)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusNotFound {
		return versionResp{}, nil
	}
	return v, err
}

// reloadServer loads newly installed courses into the running server, or starts one.
// A server too old to reload in place is restarted.
func reloadServer() error {
	c := newClient(5 * time.Second)
	if !c.up() {
		return ensure()
	}
	err := c.do("POST", "/api/reload", nil, nil)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusNotFound {
		return restartServer()
	}
	return err
}

// update reruns the installer, which replaces the binary, updates the plugin and
// restarts the server.
func update() error {
	cmd := exec.Command("sh", "-c", "curl -fsSL "+installScript+" | sh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
