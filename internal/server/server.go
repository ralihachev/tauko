// Package server exposes the tauko HTTP API and serves the PWA.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ralihachev/tauko/internal/check"
	"github.com/ralihachev/tauko/internal/content"
	"github.com/ralihachev/tauko/internal/store"
)

// Config for the server.
type Config struct {
	NewPerDay int    // new cards introduced per day
	Token     string // optional bearer token; empty disables auth
}

// Server handles API requests.
type Server struct {
	lib   *content.Library
	store *store.Store
	cfg   Config
	now   func() time.Time

	mu     sync.Mutex
	signal Signal
}

// Signal is the Claude Code busy/idle state reported by hooks.
type Signal struct {
	State string    `json:"state"` // "busy", "idle" or "" (unknown)
	Since time.Time `json:"since"`
}

// New creates a server.
func New(lib *content.Library, st *store.Store, cfg Config) *Server {
	if cfg.NewPerDay <= 0 {
		cfg.NewPerDay = 20
	}
	return &Server{lib: lib, store: st, cfg: cfg, now: time.Now}
}

// Handler returns the HTTP handler: API under /api/, PWA files at /.
func (s *Server) Handler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/next", s.handleNext)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("POST /api/review", s.handleReview)
	mux.HandleFunc("GET /api/courses", s.handleCourses)
	mux.HandleFunc("GET /api/units", s.handleUnits)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /api/quiz", s.handleQuiz)
	mux.HandleFunc("POST /api/quiz/result", s.handleQuizResult)
	mux.HandleFunc("GET /api/signal", s.handleGetSignal)
	mux.HandleFunc("POST /api/signal", s.handleSetSignal)
	mux.Handle("GET /", http.FileServerFS(static))
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	if s.cfg.Token == "" {
		return next
	}
	want := []byte("Bearer " + s.cfg.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static files are public; the PWA passes the token on API calls.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		if got == "" && r.URL.Query().Get("token") != "" {
			got = "Bearer " + r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type nextResponse struct {
	Card        *content.Card `json:"card"`
	IsNew       bool          `json:"is_new"`
	Due         int           `json:"due"`
	NewLeft     int           `json:"new_left"`
	NextDue     *time.Time    `json:"next_due,omitempty"`
	Focus       string        `json:"focus,omitempty"`
	CourseTitle string        `json:"course_title,omitempty"`
	Chars       []string      `json:"chars,omitempty"`
}

// handleNext picks the next card: due cards first, then new ones within the daily limit.
// ?course= or ?unit= restrict the scope; ?exclude= skips a card (the one just answered).
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	course, unit, exclude := q.Get("course"), q.Get("unit"), q.Get("exclude")
	now := s.now()
	inScope := func(c content.Card) bool {
		return (course == "" || c.Course == course) && (unit == "" || c.Unit == unit)
	}

	dueIDs, err := s.store.Due(now)
	if err != nil {
		serverError(w, err)
		return
	}
	var due []content.Card
	for _, id := range dueIDs {
		if c, ok := s.lib.Card(id); ok && inScope(c) {
			due = append(due, c)
		}
	}
	newLeft, err := s.newLeft(now)
	if err != nil {
		serverError(w, err)
		return
	}
	seen, err := s.store.Seen()
	if err != nil {
		serverError(w, err)
		return
	}

	resp := nextResponse{Due: len(due), NewLeft: newLeft}
	for _, c := range due {
		if c.ID != exclude {
			resp.Card = &c
			break
		}
	}
	if resp.Card == nil && newLeft > 0 {
		for _, c := range s.lib.Cards {
			if !seen[c.ID] && inScope(c) && c.ID != exclude {
				resp.Card, resp.IsNew = &c, true
				break
			}
		}
	}
	// Only the excluded card is due: better to repeat it than to show nothing.
	if resp.Card == nil && len(due) > 0 {
		resp.Card = &due[0]
	}
	if resp.Card == nil {
		if t, err := s.store.NextDue(); err == nil && !t.IsZero() {
			resp.NextDue = &t
		}
	} else {
		if u, ok := s.lib.Unit(resp.Card.Unit); ok {
			resp.Focus = u.Focus
		}
		if c, ok := s.lib.Course(resp.Card.Course); ok {
			resp.CourseTitle, resp.Chars = c.Title, c.Chars
		}
	}
	writeJSON(w, resp)
}

func (s *Server) newLeft(now time.Time) (int, error) {
	n, err := s.store.NewIntroducedSince(dayStart(now))
	if err != nil {
		return 0, err
	}
	return max(0, s.cfg.NewPerDay-n), nil
}

type checkRequest struct {
	CardID string `json:"card_id"`
	Answer string `json:"answer"`
}

type checkResponse struct {
	check.Result
	Rating int `json:"suggested_rating"`
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if !readJSON(w, r, &req) {
		return
	}
	c, ok := s.lib.Card(req.CardID)
	if !ok {
		http.Error(w, "unknown card", http.StatusNotFound)
		return
	}
	res := check.Answer(req.Answer, c.Answers, c.Lenient())
	writeJSON(w, checkResponse{Result: res, Rating: res.Verdict.Rating()})
}

type reviewRequest struct {
	CardID  string `json:"card_id"`
	Rating  int    `json:"rating"`
	Mode    string `json:"mode"`
	Answer  string `json:"answer"`
	Verdict string `json:"verdict"`
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	var req reviewRequest
	if !readJSON(w, r, &req) {
		return
	}
	if _, ok := s.lib.Card(req.CardID); !ok {
		http.Error(w, "unknown card", http.StatusNotFound)
		return
	}
	due, err := s.store.Review(req.CardID, req.Rating, req.Mode, req.Answer, req.Verdict, s.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"next_due": due})
}

type unitInfo struct {
	content.Unit
	Total      int      `json:"total"`
	Seen       int      `json:"seen"`
	Due        int      `json:"due"`
	BestQuiz   *float64 `json:"best_quiz,omitempty"`
	DrillCount int      `json:"drills"`
}

type courseInfo struct {
	content.Course
	Total int `json:"total"`
	Seen  int `json:"seen"`
	Due   int `json:"due"`
}

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request) {
	seen, err := s.store.Seen()
	if err != nil {
		serverError(w, err)
		return
	}
	dueIDs, err := s.store.Due(s.now())
	if err != nil {
		serverError(w, err)
		return
	}
	isDue := map[string]bool{}
	for _, id := range dueIDs {
		isDue[id] = true
	}
	out := make([]courseInfo, 0, len(s.lib.Courses))
	for _, c := range s.lib.Courses {
		info := courseInfo{Course: c}
		for _, card := range s.lib.Cards {
			if card.Course != c.ID {
				continue
			}
			info.Total++
			if seen[card.ID] {
				info.Seen++
			}
			if isDue[card.ID] {
				info.Due++
			}
		}
		out = append(out, info)
	}
	writeJSON(w, out)
}

func (s *Server) handleUnits(w http.ResponseWriter, r *http.Request) {
	seen, err := s.store.Seen()
	if err != nil {
		serverError(w, err)
		return
	}
	dueIDs, err := s.store.Due(s.now())
	if err != nil {
		serverError(w, err)
		return
	}
	best, err := s.store.BestQuiz()
	if err != nil {
		serverError(w, err)
		return
	}
	isDue := map[string]bool{}
	for _, id := range dueIDs {
		isDue[id] = true
	}
	out := make([]unitInfo, 0, len(s.lib.Units))
	for _, u := range s.lib.Units {
		info := unitInfo{Unit: u, Total: len(u.CardIDs)}
		for _, id := range u.CardIDs {
			if seen[id] {
				info.Seen++
			}
			if isDue[id] {
				info.Due++
			}
			if c, _ := s.lib.Card(id); c.IsDrill() {
				info.DrillCount++
			}
		}
		if b, ok := best[u.ID]; ok {
			info.BestQuiz = &b
		}
		out = append(out, info)
	}
	writeJSON(w, out)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	st, err := s.store.Stats(dayStart(now))
	if err != nil {
		serverError(w, err)
		return
	}
	dueIDs, err := s.store.Due(now)
	if err != nil {
		serverError(w, err)
		return
	}
	newLeft, err := s.newLeft(now)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"stats": st, "due": len(dueIDs), "new_left": newLeft, "cards": len(s.lib.Cards)})
}

type quizRequest struct {
	Course string `json:"course"`
	Unit   string `json:"unit"`
	N      int    `json:"n"`
}

// handleQuiz returns n random auto-checkable cards from a unit, a course or everything,
// mostly drills.
func (s *Server) handleQuiz(w http.ResponseWriter, r *http.Request) {
	var req quizRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.N <= 0 || req.N > 50 {
		req.N = 10
	}
	var pool []content.Card
	for _, c := range s.lib.Cards {
		inScope := (req.Course == "" || c.Course == req.Course) && (req.Unit == "" || c.Unit == req.Unit)
		if inScope && !c.SelfGrade && (c.IsDrill() || c.Kind == content.KindProduce) {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		http.Error(w, "nothing to quiz on", http.StatusNotFound)
		return
	}
	// Prefer grammar drills: shuffle each group, drills first, then fill with vocab.
	var drills, vocab []content.Card
	for _, c := range pool {
		if c.IsDrill() {
			drills = append(drills, c)
		} else {
			vocab = append(vocab, c)
		}
	}
	rand.Shuffle(len(drills), func(i, j int) { drills[i], drills[j] = drills[j], drills[i] })
	rand.Shuffle(len(vocab), func(i, j int) { vocab[i], vocab[j] = vocab[j], vocab[i] })
	nDrills := min(len(drills), req.N*7/10)
	picked := append(drills[:nDrills:nDrills], vocab[:min(len(vocab), req.N-nDrills)]...)
	if len(picked) < req.N {
		picked = append(picked, drills[nDrills:min(len(drills), nDrills+req.N-len(picked))]...)
	}
	rand.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	writeJSON(w, map[string]any{"cards": picked})
}

type quizResult struct {
	Course string `json:"course"`
	Unit   string `json:"unit"`
	Score  int    `json:"score"`
	Total  int    `json:"total"`
}

func (s *Server) handleQuizResult(w http.ResponseWriter, r *http.Request) {
	var req quizResult
	if !readJSON(w, r, &req) {
		return
	}
	if req.Total <= 0 || req.Score < 0 || req.Score > req.Total {
		http.Error(w, "bad score", http.StatusBadRequest)
		return
	}
	// Results are keyed by the narrowest scope: a unit, else a course, else "all".
	unit := req.Unit
	if unit == "" {
		unit = req.Course
	}
	if unit == "" {
		unit = "all"
	}
	if err := s.store.SaveQuiz(unit, req.Score, req.Total, s.now()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleGetSignal(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.signal)
}

func (s *Server) handleSetSignal(w http.ResponseWriter, r *http.Request) {
	var req Signal
	if !readJSON(w, r, &req) {
		return
	}
	if req.State != "busy" && req.State != "idle" {
		http.Error(w, `state must be "busy" or "idle"`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.signal.State != req.State {
		s.signal = Signal{State: req.State, Since: s.now()}
	}
	sig := s.signal
	s.mu.Unlock()
	writeJSON(w, sig)
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
