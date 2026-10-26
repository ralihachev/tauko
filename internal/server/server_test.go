package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	return s.Handler(fstest.MapFS{})
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
