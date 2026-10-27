package server

import (
	"net/http"
	"time"
)

// Several Claude Code sessions, the terminal and the browser can study at once. Each
// keeps the card it shows, so the server counts changes (rev) and clients poll
// GET /api/sync: when rev moves, something else answered a card, switched the course,
// or added one, and the client fetches its card again.

// changes are the requests that change what clients show.
var changes = map[string]bool{
	"/api/review": true, "/api/retire": true, "/api/unretire": true, "/api/settings": true,
	"/api/more": true, "/api/reload": true, "/api/quiz/result": true,
}

// counting bumps rev after each successful change.
func (s *Server) counting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !changes[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.code == http.StatusOK {
			s.mu.Lock()
			s.rev++
			s.mu.Unlock()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

type syncResponse struct {
	Rev int  `json:"rev"`
	Job *Job `json:"job"`
}

// handleSync is what clients poll: the change counter and the course being added.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	resp := syncResponse{Rev: s.rev}
	if s.job != nil {
		j := *s.job
		resp.Job = &j
	}
	s.mu.Unlock()
	writeJSON(w, resp)
}

// busyFor is how long a session counts as busy without saying idle: a session closed
// in the middle of a turn never does.
const busyFor = 3 * time.Hour

func (s *Server) handleGetSignal(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signal.State != "" { // "" until a session reports
		s.combine()
	}
	writeJSON(w, s.signal)
}

// handleSetSignal records one session's state. Claude counts as busy while any session
// is; clients without a session id (older plugins, `tauko signal`) share one.
func (s *Server) handleSetSignal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State   string `json:"state"`
		Session string `json:"session"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.State != "busy" && req.State != "idle" {
		http.Error(w, `state must be "busy" or "idle"`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]Signal{}
	}
	if req.State == "idle" {
		delete(s.sessions, req.Session)
	} else if _, ok := s.sessions[req.Session]; !ok {
		s.sessions[req.Session] = Signal{State: "busy", Since: s.now()}
	}
	s.combine()
	sig := s.signal
	s.mu.Unlock()
	writeJSON(w, sig)
}

// combine sets the overall signal from the sessions'. The caller holds s.mu.
func (s *Server) combine() {
	state := "idle"
	for id, sig := range s.sessions {
		if s.now().Sub(sig.Since) > busyFor {
			delete(s.sessions, id)
			continue
		}
		state = "busy"
	}
	if s.signal.State != state {
		s.signal = Signal{State: state, Since: s.now()}
	}
}
