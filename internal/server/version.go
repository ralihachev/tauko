package server

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// updateEvery is how often the server asks for the latest release, at most.
const updateEvery = 24 * time.Hour

type versionResponse struct {
	Version string `json:"version"`          // this server's tauko version ("dev" for local builds)
	Update  string `json:"update,omitempty"` // a newer release, when there is one
}

// handleVersion reports the running version, so the pane and `tauko ensure` can tell an
// old server or pane apart, and a newer release when the update check found one.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	latest, checked := s.latest, s.latestChecked
	stale := s.cfg.Latest != nil && !s.latestRunning && s.now().Sub(checked) > updateEvery
	if stale {
		s.latestRunning = true
	}
	s.mu.Unlock()
	if stale {
		go s.checkLatest()
	}
	resp := versionResponse{Version: s.cfg.Version}
	if Newer(latest, s.cfg.Version) {
		resp.Update = latest
	}
	writeJSON(w, resp)
}

func (s *Server) checkLatest() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	latest, err := s.cfg.Latest(ctx)
	if err != nil {
		log.Printf("update check: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestRunning, s.latestChecked = false, s.now()
	if err == nil {
		s.latest = latest
	}
}

// handleReload reloads the courses on disk, for `tauko gen` and `tauko add` in a
// terminal. The active course stays as it was.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Reload == nil {
		http.Error(w, "reloading is not available", http.StatusServiceUnavailable)
		return
	}
	lib, warnings, err := s.cfg.Reload()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.libMu.Lock()
	s.lib, s.warnings = lib, warnings
	s.libMu.Unlock()
	writeJSON(w, map[string]any{"courses": len(lib.Courses), "cards": len(lib.Cards), "warnings": warnings})
}

// Newer reports whether version a (like "v0.6.0") is a later release than b. A local
// "dev" build, or anything that isn't a version, is never older or newer.
func Newer(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// SameMinor reports whether a and b are the same major.minor release; false when
// either isn't a version.
func SameMinor(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	return okA && okB && va[0] == vb[0] && va[1] == vb[1]
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
