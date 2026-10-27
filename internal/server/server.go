// Package server exposes the tauko HTTP API and serves the PWA.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ralihachev/tauko/internal/check"
	"github.com/ralihachev/tauko/internal/content"
	"github.com/ralihachev/tauko/internal/llm"
	"github.com/ralihachev/tauko/internal/store"
)

// Config for the server.
type Config struct {
	NewPerDay int    // new cards introduced per day
	Token     string // optional bearer token; empty disables auth
	Warnings  []string
	Explain   llm.Func // for "Explain this"; nil when Claude Code isn't installed

	// Adding courses from a client. Each is nil when unavailable.
	Gen     func(ctx context.Context, topic, level string, progress func(string)) (courseID string, err error)
	Add     func(ctx context.Context, id string) error // install a gallery course
	Gallery func(ctx context.Context) ([]GalleryEntry, error)
	Reload  func() (*content.Library, []string, error) // reloads courses after one is added, with warnings

	Version string                                    // this build's version, for GET /api/version
	Latest  func(ctx context.Context) (string, error) // the latest release; nil turns the update check off
}

// Server handles API requests.
type Server struct {
	lib   *content.Library
	store *store.Store
	cfg   Config
	now   func() time.Time

	mu     sync.Mutex
	signal Signal
	job    *Job

	latest        string // the latest release, from the update check
	latestChecked time.Time
	latestRunning bool

	libMu    sync.RWMutex
	warnings []string // courses that didn't load; replaced on reload
}

// library is the loaded courses; adding a course swaps it.
func (s *Server) library() *content.Library {
	s.libMu.RLock()
	defer s.libMu.RUnlock()
	return s.lib
}

// libWarnings lists the courses that didn't load.
func (s *Server) libWarnings() []string {
	s.libMu.RLock()
	defer s.libMu.RUnlock()
	return s.warnings
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
	return &Server{lib: lib, store: st, cfg: cfg, now: time.Now, warnings: cfg.Warnings}
}

// Handler returns the HTTP handler: API under /api/, PWA files at /.
func (s *Server) Handler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/next", s.handleNext)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("POST /api/review", s.handleReview)
	mux.HandleFunc("GET /api/courses", s.handleCourses)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("POST /api/settings", s.handleSetSettings)
	mux.HandleFunc("POST /api/more", s.handleMore)
	mux.HandleFunc("POST /api/explain", s.handleExplain)
	mux.HandleFunc("POST /api/retire", s.handleRetire)
	mux.HandleFunc("POST /api/unretire", s.handleUnretire)
	mux.HandleFunc("GET /api/retired", s.handleRetired)
	mux.HandleFunc("GET /api/units", s.handleUnits)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /api/quiz", s.handleQuiz)
	mux.HandleFunc("POST /api/quiz/result", s.handleQuizResult)
	mux.HandleFunc("GET /api/gallery", s.handleGallery)
	mux.HandleFunc("POST /api/courses/add", s.handleAddCourse)
	mux.HandleFunc("POST /api/courses/gen", s.handleGenCourse)
	mux.HandleFunc("GET /api/job", s.handleGetJob)
	mux.HandleFunc("POST /api/reload", s.handleReload)
	mux.HandleFunc("GET /api/version", s.handleVersion)
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
	Practice    bool          `json:"practice,omitempty"` // nothing due, no new left: reviewing ahead
	Due         int           `json:"due"`
	NewLeft     int           `json:"new_left"`
	NextDue     *time.Time    `json:"next_due,omitempty"`
	Focus       string        `json:"focus,omitempty"`
	CourseTitle string        `json:"course_title,omitempty"`
	Chars       []string      `json:"chars,omitempty"`
	Scope       string        `json:"scope"`                // the course being studied; "" = all courses
	NoCourses   bool          `json:"no_courses,omitempty"` // nothing installed yet
}

// practiceGap keeps a just-practiced card from coming straight back in practice mode.
const practiceGap = 10 * time.Minute

// handleNext picks the next card: due cards first, then new ones within today's limit,
// then practice (the seen cards you're closest to forgetting), so there is always a card.
// ?course= or ?unit= set the scope (default: the active course setting); ?exclude= skips
// a card (the one just answered).
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	course, unit, exclude := q.Get("course"), q.Get("unit"), q.Get("exclude")
	if !q.Has("course") && unit == "" {
		active, err := s.store.Get(keyActiveCourse)
		if err != nil {
			serverError(w, err)
			return
		}
		if _, ok := s.library().Course(active); ok {
			course = active
		}
	}
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
		if c, ok := s.library().Card(id); ok && inScope(c) {
			due = append(due, c)
		}
	}
	newLeft, err := s.newLeft(now)
	if err != nil {
		serverError(w, err)
		return
	}

	resp := nextResponse{Due: len(due), NewLeft: newLeft, Scope: course, NoCourses: len(s.library().Courses) == 0}
	for _, c := range due {
		if c.ID != exclude {
			resp.Card = &c
			break
		}
	}
	if resp.Card == nil && newLeft > 0 {
		c, err := s.nextNew(now, inScope, exclude)
		if err != nil {
			serverError(w, err)
			return
		}
		resp.Card, resp.IsNew = c, c != nil
	}
	// Only the excluded card is due: better to repeat it than to show nothing.
	if resp.Card == nil && len(due) > 0 {
		resp.Card = &due[0]
	}
	if resp.Card == nil {
		c, err := s.practiceCard(now, inScope, exclude)
		if err != nil {
			serverError(w, err)
			return
		}
		resp.Card, resp.Practice = c, c != nil
	}
	if resp.Card == nil {
		if t, err := s.store.NextDue(); err == nil && !t.IsZero() {
			resp.NextDue = &t
		}
	} else {
		if u, ok := s.library().Unit(resp.Card.Unit); ok {
			resp.Focus = u.Focus
		}
		if c, ok := s.library().Course(resp.Card.Course); ok {
			resp.CourseTitle, resp.Chars = c.Title, c.Chars
		}
	}
	writeJSON(w, resp)
}

// nextNew returns the next unseen card in curriculum order. With several courses in
// scope it rotates: the course that introduced the fewest new cards today goes next.
func (s *Server) nextNew(now time.Time, inScope func(content.Card) bool, exclude string) (*content.Card, error) {
	seen, err := s.store.Seen()
	if err != nil {
		return nil, err
	}
	introduced, err := s.store.NewIntroducedIDsSince(dayStart(now))
	if err != nil {
		return nil, err
	}
	retired, err := s.store.Retired()
	if err != nil {
		return nil, err
	}
	today := map[string]int{}
	for _, id := range introduced {
		if c, ok := s.library().Card(id); ok {
			today[c.Course]++
		}
	}
	var best *content.Card
	firstOf := map[string]bool{}
	for i := range s.library().Cards {
		c := &s.library().Cards[i]
		if seen[c.ID] || retired[c.ID] || !inScope(*c) || c.ID == exclude || firstOf[c.Course] {
			continue
		}
		firstOf[c.Course] = true // only each course's next card competes
		if best == nil || today[c.Course] < today[best.Course] {
			best = c
		}
	}
	if best == nil {
		return nil, nil
	}
	card := *best
	return &card, nil
}

// practiceCard returns the seen card in scope with the lowest recall probability that
// wasn't reviewed in the last practiceGap; failing that, the least recently reviewed.
func (s *Server) practiceCard(now time.Time, inScope func(content.Card) bool, exclude string) (*content.Card, error) {
	recalls, err := s.store.Recalls(now)
	if err != nil {
		return nil, err
	}
	var weakest, oldest *store.Recall
	for i := range recalls {
		rc := &recalls[i]
		c, ok := s.library().Card(rc.ID)
		if !ok || !inScope(c) || rc.ID == exclude || c.IsLesson() {
			continue
		}
		if now.Sub(rc.LastReview) >= practiceGap && (weakest == nil || rc.R < weakest.R) {
			weakest = rc
		}
		if oldest == nil || rc.LastReview.Before(oldest.LastReview) {
			oldest = rc
		}
	}
	pick := weakest
	if pick == nil {
		pick = oldest
	}
	if pick == nil {
		return nil, nil
	}
	c, _ := s.library().Card(pick.ID)
	return &c, nil
}

const (
	keyActiveCourse = "active_course"
	keyExtraNew     = "extra_new:" // + YYYY-MM-DD
)

func (s *Server) newLeft(now time.Time) (int, error) {
	n, err := s.store.NewIntroducedSince(dayStart(now))
	if err != nil {
		return 0, err
	}
	extra, err := s.extraNew(now)
	if err != nil {
		return 0, err
	}
	return max(0, s.cfg.NewPerDay+extra-n), nil
}

func (s *Server) extraNew(now time.Time) (int, error) {
	v, err := s.store.Get(keyExtraNew + now.Format("2006-01-02"))
	if err != nil || v == "" {
		return 0, err
	}
	n, _ := strconv.Atoi(v)
	return n, nil
}

type settings struct {
	ActiveCourse string `json:"active_course"` // "" = all courses
	NewPerDay    int    `json:"new_per_day"`
	ExtraToday   int    `json:"extra_new_today"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.Get(keyActiveCourse)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, ok := s.library().Course(active); !ok {
		active = ""
	}
	extra, err := s.extraNew(s.now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, settings{ActiveCourse: active, NewPerDay: s.cfg.NewPerDay, ExtraToday: extra})
}

func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActiveCourse *string `json:"active_course"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ActiveCourse != nil {
		if _, ok := s.library().Course(*req.ActiveCourse); *req.ActiveCourse != "" && !ok {
			http.Error(w, "unknown course", http.StatusBadRequest)
			return
		}
		if err := s.store.Set(keyActiveCourse, *req.ActiveCourse); err != nil {
			serverError(w, err)
			return
		}
	}
	s.handleGetSettings(w, r)
}

// handleMore raises today's new-card limit by n (default 10).
func (s *Server) handleMore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		N int `json:"n"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.N <= 0 || req.N > 100 {
		req.N = 10
	}
	now := s.now()
	extra, err := s.extraNew(now)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := s.store.Set(keyExtraNew+now.Format("2006-01-02"), strconv.Itoa(extra+req.N)); err != nil {
		serverError(w, err)
		return
	}
	left, err := s.newLeft(now)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]int{"new_left": left})
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
	c, ok := s.library().Card(req.CardID)
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
	card, ok := s.library().Card(req.CardID)
	if !ok {
		http.Error(w, "unknown card", http.StatusNotFound)
		return
	}
	if card.IsLesson() {
		if err := s.store.MarkRead(card.ID, s.now()); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"read": true})
		return
	}
	due, err := s.store.Review(req.CardID, req.Rating, req.Mode, req.Answer, req.Verdict, s.now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"next_due": due})
}

type lessonInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Example   string `json:"example,omitempty"`
	ExampleTr string `json:"example_translation,omitempty"`
	Read      bool   `json:"read"`
}

type unitInfo struct {
	content.Unit
	Lessons    []lessonInfo `json:"lessons,omitempty"`
	Total      int          `json:"total"`
	Seen       int          `json:"seen"`
	Due        int          `json:"due"`
	BestQuiz   *float64     `json:"best_quiz,omitempty"`
	DrillCount int          `json:"drills"`
}

type courseInfo struct {
	content.Course
	Total int `json:"total"`
	Seen  int `json:"seen"`
	Due   int `json:"due"`
}

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request) {
	seen, err := s.seenOrRetired()
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
	out := make([]courseInfo, 0, len(s.library().Courses))
	for _, c := range s.library().Courses {
		info := courseInfo{Course: c}
		for _, card := range s.library().Cards {
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
	seen, err := s.seenOrRetired()
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
	out := make([]unitInfo, 0, len(s.library().Units))
	for _, u := range s.library().Units {
		info := unitInfo{Unit: u, Total: len(u.CardIDs)}
		for _, id := range u.CardIDs {
			if seen[id] {
				info.Seen++
			}
			if isDue[id] {
				info.Due++
			}
			c, _ := s.library().Card(id)
			if c.IsDrill() {
				info.DrillCount++
			}
			if c.IsLesson() {
				info.Lessons = append(info.Lessons, lessonInfo{ID: c.ID, Title: c.Prompt, Body: c.Body,
					Example: c.Example, ExampleTr: c.ExampleTr, Read: seen[c.ID]})
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
	writeJSON(w, map[string]any{"stats": st, "due": len(dueIDs), "new_left": newLeft, "cards": len(s.library().Cards), "warnings": s.libWarnings()})
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
	retired, err := s.store.Retired()
	if err != nil {
		serverError(w, err)
		return
	}
	var pool []content.Card
	for _, c := range s.library().Cards {
		if retired[c.ID] {
			continue
		}
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

const explainSystem = `You are a patient tutor inside a flashcard app. The learner reads your reply in a
small pane in a few seconds. Reply in plain text, at most 70 words, no headings or lists.
Be specific: explain why the accepted answer is right and, if the learner's answer differs,
exactly what is wrong with it. End with one short extra example. Write in English.`

// handleExplain asks Claude why a card's answer is what it is, for this learner's
// answer. Replies are cached per card and answer, so asking again is free.
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CardID string `json:"card_id"`
		Answer string `json:"answer"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	card, ok := s.library().Card(req.CardID)
	if !ok {
		http.Error(w, "unknown card", http.StatusNotFound)
		return
	}
	key := "explain:" + card.ID + ":" + strings.ToLower(strings.TrimSpace(req.Answer))
	if cached, err := s.store.Get(key); err == nil && cached != "" {
		writeJSON(w, map[string]any{"text": cached, "cached": true})
		return
	}
	if s.cfg.Explain == nil {
		http.Error(w, "Explain needs Claude Code (the claude command) installed", http.StatusServiceUnavailable)
		return
	}

	var b strings.Builder
	if c, ok := s.library().Course(card.Course); ok {
		fmt.Fprintf(&b, "Course: %s\n", c.Title)
	}
	if u, ok := s.library().Unit(card.Unit); ok {
		fmt.Fprintf(&b, "Unit: %s", u.Title)
		if u.Focus != "" {
			fmt.Fprintf(&b, " (focus: %s)", u.Focus)
		}
		b.WriteString("\n")
	}
	if card.IsLesson() {
		fmt.Fprintf(&b, "The learner read this lesson and wants it explained another way, more simply:\n%s\n%s\n", card.Prompt, card.Body)
	} else {
		fmt.Fprintf(&b, "Card (%s): %s\n", card.Instruction, card.Prompt)
		if card.Hint != "" {
			fmt.Fprintf(&b, "Hint shown: %s\n", card.Hint)
		}
		fmt.Fprintf(&b, "Accepted answers: %s\n", strings.Join(card.Answers, " | "))
		if card.Translation != "" {
			fmt.Fprintf(&b, "Translation: %s\n", card.Translation)
		}
		if card.Note != "" {
			fmt.Fprintf(&b, "Note on the card: %s\n", card.Note)
		}
		if a := strings.TrimSpace(req.Answer); a != "" {
			fmt.Fprintf(&b, "The learner answered: %s\n", a)
		} else {
			b.WriteString("The learner didn't type an answer.\n")
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	text, err := s.cfg.Explain(ctx, explainSystem, b.String(), nil)
	if err != nil {
		log.Printf("explain %s: %v", card.ID, err)
		http.Error(w, "Claude couldn't explain this right now", http.StatusBadGateway)
		return
	}
	text = strings.TrimSpace(text)
	if err := s.store.Set(key, text); err != nil {
		log.Printf("explain cache: %v", err)
	}
	writeJSON(w, map[string]any{"text": text, "cached": false})
}

// seenOrRetired is every card the learner has met, retired ones included.
func (s *Server) seenOrRetired() (map[string]bool, error) {
	seen, err := s.store.Seen()
	if err != nil {
		return nil, err
	}
	retired, err := s.store.Retired()
	if err != nil {
		return nil, err
	}
	for id := range retired {
		seen[id] = true
	}
	return seen, nil
}

type cardRef struct {
	CardID string `json:"card_id"`
}

// handleRetire marks a card "I know this": out of reviews, practice and quizzes.
func (s *Server) handleRetire(w http.ResponseWriter, r *http.Request) {
	var req cardRef
	if !readJSON(w, r, &req) {
		return
	}
	if _, ok := s.library().Card(req.CardID); !ok {
		http.Error(w, "unknown card", http.StatusNotFound)
		return
	}
	if err := s.store.Retire(req.CardID, s.now()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"retired": true})
}

func (s *Server) handleUnretire(w http.ResponseWriter, r *http.Request) {
	var req cardRef
	if !readJSON(w, r, &req) {
		return
	}
	retired, err := s.store.Retired()
	if err != nil {
		serverError(w, err)
		return
	}
	if !retired[req.CardID] {
		http.Error(w, "no retired card "+req.CardID+" (see tauko retired)", http.StatusNotFound)
		return
	}
	if err := s.store.Unretire(req.CardID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"retired": false})
}

type retiredCard struct {
	ID        string `json:"id"`
	Course    string `json:"course"`
	Unit      string `json:"unit"`
	UnitTitle string `json:"unit_title"`
	Prompt    string `json:"prompt"`
	Answer    string `json:"answer"`
}

// handleRetired lists retired cards (?course= to narrow), newest first.
func (s *Server) handleRetired(w http.ResponseWriter, r *http.Request) {
	retired, err := s.store.Retired()
	if err != nil {
		serverError(w, err)
		return
	}
	course := r.URL.Query().Get("course")
	out := []retiredCard{}
	for _, c := range s.library().Cards {
		if !retired[c.ID] || (course != "" && c.Course != course) {
			continue
		}
		rc := retiredCard{ID: c.ID, Course: c.Course, Unit: c.Unit, UnitTitle: c.UnitTitle, Prompt: c.Prompt}
		if len(c.Answers) > 0 {
			rc.Answer = c.Answers[0]
		}
		out = append(out, rc)
	}
	writeJSON(w, out)
}
