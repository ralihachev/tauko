package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ralihachev/tauko/internal/content"
	"github.com/ralihachev/tauko/internal/store"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	fsys := fstest.MapFS{
		"c/fi/course.json":   {Data: []byte(`{"id": "fi", "title": "Suomi", "labels": {"qa": "Vastaa"}, "chars": ["ä", "ö"]}`)},
		"c/fi/decks/01.json": {Data: []byte(`{"id": "fi-01", "title": "Perusteet", "focus": "Partitiivi", "focus_note": "…", "drills": [{"id": "fi-q1", "type": "qa", "prompt": "Kissa (partitiivi)?", "answers": ["kissaa"]}]}`)},
		"c/go/course.json":   {Data: []byte(`{"id": "go", "title": "Go"}`)},
		"c/go/decks/01.json": {Data: []byte(`{"id": "go-01", "title": "Channels", "drills": [
			{"id": "go-q1", "type": "qa", "prompt": "Send on a nil channel?", "answers": ["blocks forever"]},
			{"id": "go-q2", "type": "qa", "prompt": "Why buffer?", "answers": ["…"], "self_grade": true}]}`)},
	}
	lib, err := content.Load(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(lib, st, Config{})
	s.now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.Local) }
	testServer = s
	return s.Handler(fstest.MapFS{})
}

// testServer is the Server behind the last newTestServer handler.
var testServer *Server

// newClockedServer is newTestServer with a settable clock and new-card limit.
func newClockedServer(t *testing.T, newPerDay int) (http.Handler, *time.Time) {
	t.Helper()
	h := newTestServer(t)
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.Local)
	testServer.now = func() time.Time { return clock }
	testServer.cfg.NewPerDay = newPerDay
	return h, &clock
}

func call(t *testing.T, h http.Handler, method, path, body string, out any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNextRespectsCourse(t *testing.T) {
	h := newTestServer(t)
	var next nextResponse
	call(t, h, "GET", "/api/next?course=go", "", &next)
	if next.Card == nil || next.Card.Course != "go" || next.CourseTitle != "Go" || next.Card.Instruction != "Answer" {
		t.Fatalf("next(course=go) = %+v", next)
	}
	call(t, h, "GET", "/api/next?course=fi", "", &next)
	if next.Card == nil || next.Card.ID != "fi-q1" || next.Card.Instruction != "Vastaa" || next.Focus != "Partitiivi" || len(next.Chars) != 2 {
		t.Fatalf("next(course=fi) = %+v", next)
	}
}

func TestCoursesAndQuiz(t *testing.T) {
	h := newTestServer(t)
	call(t, h, "POST", "/api/review", `{"card_id": "go-q1", "rating": 3, "mode": "type"}`, nil)

	var courses []courseInfo
	call(t, h, "GET", "/api/courses", "", &courses)
	if len(courses) != 2 || courses[1].ID != "go" || courses[1].Total != 2 || courses[1].Seen != 1 {
		t.Fatalf("courses = %+v", courses)
	}

	// Self-graded cards can't be scored, so a quiz leaves them out.
	var quiz struct {
		Cards []content.Card `json:"cards"`
	}
	call(t, h, "POST", "/api/quiz", `{"course": "go", "n": 10}`, &quiz)
	if len(quiz.Cards) != 1 || quiz.Cards[0].ID != "go-q1" {
		t.Fatalf("quiz = %+v", quiz.Cards)
	}
}

func TestNoCourses(t *testing.T) {
	lib, err := content.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var next nextResponse
	call(t, New(lib, st, Config{}).Handler(fstest.MapFS{}), "GET", "/api/next", "", &next)
	if next.Card != nil || !next.NoCourses {
		t.Errorf("empty library: %+v", next)
	}
}

func review(t *testing.T, h http.Handler, id string) {
	t.Helper()
	call(t, h, "POST", "/api/review", `{"card_id": "`+id+`", "rating": 4, "mode": "tap"}`, nil)
}

func TestPracticeWhenNothingLeft(t *testing.T) {
	h, clock := newClockedServer(t, 3)
	for _, id := range []string{"fi-q1", "go-q1", "go-q2"} {
		review(t, h, id) // Easy: due in days
	}
	*clock = clock.Add(time.Hour)

	var next nextResponse
	call(t, h, "GET", "/api/next?exclude=go-q2", "", &next)
	if next.Card == nil || !next.Practice || next.Card.ID == "go-q2" {
		t.Fatalf("want a practice card other than the excluded one, got %+v", next)
	}

	// A card practiced a minute ago doesn't come straight back while others are available.
	review(t, h, next.Card.ID)
	*clock = clock.Add(time.Minute)
	var again nextResponse
	call(t, h, "GET", "/api/next", "", &again)
	if again.Card == nil || again.Card.ID == next.Card.ID {
		t.Errorf("practice repeated %s right away", next.Card.ID)
	}
}

func TestMoreNewCards(t *testing.T) {
	h, _ := newClockedServer(t, 1)
	review(t, h, "fi-q1")
	var next nextResponse
	call(t, h, "GET", "/api/next", "", &next)
	if next.NewLeft != 0 || next.IsNew {
		t.Fatalf("limit not reached: %+v", next)
	}
	var more map[string]int
	call(t, h, "POST", "/api/more", `{"n": 10}`, &more)
	if more["new_left"] != 10 {
		t.Fatalf("new_left after more = %d, want 10", more["new_left"])
	}
	call(t, h, "GET", "/api/next", "", &next)
	if !next.IsNew {
		t.Errorf("want a new card after /api/more, got %+v", next)
	}
}

func TestActiveCourseAndRotation(t *testing.T) {
	h, _ := newClockedServer(t, 20)

	// No course chosen: new cards rotate. fi was introduced first, so go is next.
	review(t, h, "fi-q1")
	var next nextResponse
	call(t, h, "GET", "/api/next", "", &next)
	if next.Card == nil || next.Card.Course != "go" || next.Scope != "" {
		t.Fatalf("rotation: want a go card, got %+v", next)
	}

	var set settings
	call(t, h, "POST", "/api/settings", `{"active_course": "fi"}`, &set)
	if set.ActiveCourse != "fi" {
		t.Fatalf("settings = %+v", set)
	}
	call(t, h, "GET", "/api/next", "", &next)
	if next.Scope != "fi" || (next.Card != nil && next.Card.Course != "fi") {
		t.Errorf("active course ignored: %+v", next)
	}
	// An explicit ?course= wins over the setting; empty means all courses.
	call(t, h, "GET", "/api/next?course=go", "", &next)
	if next.Card == nil || next.Card.Course != "go" {
		t.Errorf("?course=go ignored: %+v", next)
	}

	req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"active_course": "nope"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown course accepted: %d", rec.Code)
	}
}

func newLessonServer(t *testing.T) (http.Handler, *time.Time) {
	t.Helper()
	lib, err := content.Load(fstest.MapFS{
		"c/q/course.json": {Data: []byte(`{"id": "q", "title": "Quantum"}`)},
		"c/q/decks/01.json": {Data: []byte(`{"id": "q1", "title": "Qubits",
			"lessons": [{"id": "q-l1", "title": "Superposition", "body": "A qubit holds a mix of 0 and 1 until measured."}],
			"drills": [{"id": "q-c1", "type": "qa", "prompt": "What does measuring do?", "answers": ["picks 0 or 1"], "lesson": "q-l1"}]}`)},
	}, "c")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(lib, st, Config{})
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.Local)
	s.now = func() time.Time { return clock }
	return s.Handler(fstest.MapFS{}), &clock
}

func TestLessonIsReadOnceThenItsCheck(t *testing.T) {
	h, clock := newLessonServer(t)
	var next nextResponse
	call(t, h, "GET", "/api/next", "", &next)
	if next.Card == nil || next.Card.ID != "q-l1" || next.Card.Body == "" {
		t.Fatalf("first card should be the lesson, got %+v", next.Card)
	}
	var read map[string]bool
	call(t, h, "POST", "/api/review", `{"card_id": "q-l1", "rating": 3}`, &read)
	if !read["read"] {
		t.Fatalf("lesson not marked read: %v", read)
	}
	call(t, h, "GET", "/api/next?exclude=q-l1", "", &next)
	if next.Card == nil || next.Card.ID != "q-c1" {
		t.Fatalf("after the lesson comes its check, got %+v", next.Card)
	}
	review(t, h, "q-c1")

	// Weeks later the lesson is never due and never practiced; the check card is.
	*clock = clock.AddDate(0, 0, 40)
	for i := 0; i < 3; i++ {
		call(t, h, "GET", "/api/next", "", &next)
		if next.Card == nil || next.Card.IsLesson() {
			t.Fatalf("lesson came back: %+v", next.Card)
		}
	}

	var units []unitInfo
	call(t, h, "GET", "/api/units", "", &units)
	if len(units[0].Lessons) != 1 || !units[0].Lessons[0].Read {
		t.Errorf("units lessons = %+v", units[0].Lessons)
	}
}

func TestExplainIsSpecificAndCached(t *testing.T) {
	lib, err := content.Load(fstest.MapFS{
		"c/es/course.json":   {Data: []byte(`{"id": "es", "title": "Spanish"}`)},
		"c/es/decks/01.json": {Data: []byte(`{"id": "es1", "title": "Past", "drills": [{"id": "es-q1", "type": "cloze", "prompt": "Ayer ___ en casa.", "hint": "cenar, yo", "answers": ["cené"]}]}`)},
	}, "c")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := 0
	explain := func(_ context.Context, system, prompt string, _ []string) (string, error) {
		calls++
		if !strings.Contains(prompt, "The learner answered: cene") || !strings.Contains(prompt, "Accepted answers: cené") {
			t.Errorf("prompt lacks the card or the answer:\n%s", prompt)
		}
		return " Regular -ar verbs take -é in the yo form. ", nil
	}
	h := New(lib, st, Config{Explain: explain}).Handler(fstest.MapFS{})

	var out map[string]any
	call(t, h, "POST", "/api/explain", `{"card_id": "es-q1", "answer": "cene"}`, &out)
	if out["text"] != "Regular -ar verbs take -é in the yo form." || out["cached"] != false {
		t.Errorf("explain = %v", out)
	}
	call(t, h, "POST", "/api/explain", `{"card_id": "es-q1", "answer": " CENE "}`, &out)
	if calls != 1 || out["cached"] != true {
		t.Errorf("second explain should come from the cache: calls=%d %v", calls, out)
	}

	// Without Claude Code, explain says so.
	h2 := New(lib, st, Config{}).Handler(fstest.MapFS{})
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("POST", "/api/explain", strings.NewReader(`{"card_id": "es-q1", "answer": "other"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no Claude: status %d", rec.Code)
	}
}

func TestRetireAndBringBack(t *testing.T) {
	h, clock := newClockedServer(t, 20)
	review(t, h, "go-q1")
	call(t, h, "POST", "/api/retire", `{"card_id": "go-q1"}`, nil)
	call(t, h, "POST", "/api/retire", `{"card_id": "fi-q1"}`, nil) // never seen: it must not show up as new either

	*clock = clock.AddDate(0, 3, 0) // long after go-q1 would be due
	var next nextResponse
	for i := 0; i < 3; i++ {
		call(t, h, "GET", "/api/next", "", &next)
		if next.Card != nil && (next.Card.ID == "go-q1" || next.Card.ID == "fi-q1") {
			t.Fatalf("retired card served: %s", next.Card.ID)
		}
	}
	// go-q1 is the course's only quizzable card (go-q2 is self-graded); retired, nothing is left.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/quiz", strings.NewReader(`{"course": "go"}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("quiz with only a retired card: status %d, want 404", rec.Code)
	}
	var list []retiredCard
	call(t, h, "GET", "/api/retired", "", &list)
	if len(list) != 2 {
		t.Fatalf("retired list = %+v", list)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/unretire", strings.NewReader(`{"card_id": "made-up"}`)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unretire of an unknown id: status %d, want 404", rec.Code)
	}
	call(t, h, "POST", "/api/unretire", `{"card_id": "go-q1"}`, nil)
	call(t, h, "GET", "/api/next?course=go", "", &next)
	if next.Card == nil || next.Card.ID != "go-q1" || next.Practice {
		t.Errorf("unretired card should be due again, got %+v", next)
	}
}

func TestGenerateACourseFromAClient(t *testing.T) {
	h := newTestServer(t)
	release := make(chan struct{})
	testServer.cfg.Gen = func(ctx context.Context, topic, level string, progress func(string)) (string, error) {
		progress("Drafting")
		<-release
		return "ph", nil
	}
	testServer.cfg.Reload = func() (*content.Library, []string, error) {
		lib, err := content.Load(fstest.MapFS{
			"c/ph/course.json":   {Data: []byte(`{"id": "ph", "title": "Photography"}`)},
			"c/ph/decks/01.json": {Data: []byte(`{"id": "ph-01", "title": "Exposure", "drills": [{"id": "ph-q1", "type": "qa", "prompt": "Depth of field?", "answers": ["aperture"]}]}`)},
		}, "c")
		return lib, nil, err
	}

	var job Job
	call(t, h, "POST", "/api/courses/gen", `{"topic": "Photography"}`, &job)
	if job.State != "running" || job.Label != "Photography" {
		t.Fatalf("job = %+v", job)
	}
	// One job at a time.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/courses/gen", strings.NewReader(`{"topic": "Go"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second job: %d, want 409", rec.Code)
	}

	close(release)
	for deadline := time.Now().Add(5 * time.Second); job.State == "running"; {
		if time.Now().After(deadline) {
			t.Fatal("job still running")
		}
		time.Sleep(5 * time.Millisecond)
		call(t, h, "GET", "/api/job", "", &job)
	}
	if job.State != "done" || job.CourseID != "ph" || job.CourseTitle != "Photography" {
		t.Fatalf("job = %+v", job)
	}
	// The new course is loaded and studied next.
	var next nextResponse
	call(t, h, "GET", "/api/next", "", &next)
	if next.Scope != "ph" || next.Card == nil || next.Card.ID != "ph-q1" {
		t.Fatalf("next = %+v", next)
	}
}

func TestFailedJobAndGallery(t *testing.T) {
	h := newTestServer(t)
	testServer.cfg.Add = func(ctx context.Context, id string) error { return errors.New("no course " + id) }
	testServer.cfg.Reload = func() (*content.Library, []string, error) { t.Fatal("reloaded after a failure"); return nil, nil, nil }
	testServer.cfg.Gallery = func(ctx context.Context) ([]GalleryEntry, error) {
		return []GalleryEntry{{ID: "go", Title: "Go"}, {ID: "spanish", Title: "Español"}}, nil
	}

	var gallery []GalleryEntry
	call(t, h, "GET", "/api/gallery", "", &gallery)
	if !gallery[0].Installed || gallery[1].Installed {
		t.Fatalf("gallery = %+v", gallery)
	}

	var job Job
	call(t, h, "POST", "/api/courses/add", `{"id": "nope"}`, &job)
	for job.State == "running" {
		time.Sleep(5 * time.Millisecond)
		call(t, h, "GET", "/api/job", "", &job)
	}
	if job.State != "failed" || job.Error != "no course nope" {
		t.Fatalf("job = %+v", job)
	}

	// Without Claude Code, generating says why.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/courses/gen", strings.NewReader(`{"topic": "x"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("gen without Claude: %d", rec.Code)
	}
}

func TestVersionAndUpdateCheck(t *testing.T) {
	h := newTestServer(t)
	testServer.cfg.Version = "v0.5.2"
	var checks atomic.Int32
	testServer.cfg.Latest = func(ctx context.Context) (string, error) {
		checks.Add(1)
		return "v0.6.0", nil
	}

	// The first call starts the check in the background; a later one reports the update.
	var v versionResponse
	for range 200 {
		call(t, h, "GET", "/api/version", "", &v)
		if v.Update != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if v.Version != "v0.5.2" || v.Update != "v0.6.0" {
		t.Fatalf("version = %+v", v)
	}
	if n := checks.Load(); n != 1 {
		t.Errorf("checked %d times within a day, want 1", n)
	}

	// Up to date: no update to report.
	testServer.cfg.Version = "v0.6.0"
	v = versionResponse{}
	call(t, h, "GET", "/api/version", "", &v)
	if v.Update != "" {
		t.Errorf("update = %q for the latest version", v.Update)
	}
}

func TestReloadKeepsTheActiveCourse(t *testing.T) {
	h := newTestServer(t)
	call(t, h, "POST", "/api/settings", `{"active_course": "go"}`, nil)
	testServer.cfg.Reload = func() (*content.Library, []string, error) {
		lib, err := content.Load(fstest.MapFS{
			"c/go/course.json":   {Data: []byte(`{"id": "go", "title": "Go"}`)},
			"c/go/decks/01.json": {Data: []byte(`{"id": "go-01", "title": "Channels", "drills": [{"id": "go-q1", "type": "qa", "prompt": "Send on a nil channel?", "answers": ["blocks forever"]}]}`)},
			"c/ph/course.json":   {Data: []byte(`{"id": "ph", "title": "Photography"}`)},
			"c/ph/decks/01.json": {Data: []byte(`{"id": "ph-01", "title": "Exposure", "drills": [{"id": "ph-q1", "type": "qa", "prompt": "Depth of field?", "answers": ["aperture"]}]}`)},
		}, "c")
		return lib, []string{"skipped course broken: no course.json"}, err
	}

	var got struct {
		Courses  int      `json:"courses"`
		Warnings []string `json:"warnings"`
	}
	call(t, h, "POST", "/api/reload", "", &got)
	if got.Courses != 2 || len(got.Warnings) != 1 {
		t.Fatalf("reload = %+v", got)
	}
	var next nextResponse
	call(t, h, "GET", "/api/next", "", &next)
	if next.Scope != "go" {
		t.Errorf("scope after reload = %q, want go", next.Scope)
	}
	var stats struct {
		Cards    int      `json:"cards"`
		Warnings []string `json:"warnings"`
	}
	call(t, h, "GET", "/api/stats", "", &stats)
	if stats.Cards != 2 || len(stats.Warnings) != 1 {
		t.Errorf("stats after reload = %+v", stats)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.6.0", "v0.5.2", true},
		{"v0.5.10", "v0.5.9", true},
		{"v0.5.2", "v0.5.2", false},
		{"v0.5.1", "v0.5.2", false},
		{"v1.0.0", "dev", false},
		{"", "v0.5.2", false},
		{"0.6.0", "v0.5.2", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
	if !SameMinor("v0.5.2", "0.5.0") || SameMinor("v0.5.2", "0.4.9") || SameMinor("dev", "dev") {
		t.Error("SameMinor")
	}
}
