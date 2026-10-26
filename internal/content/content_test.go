package content

import (
	"strings"
	"testing"
	"testing/fstest"

	shipped "github.com/ralihachev/tauko/content"
)

// TestShippedCourses validates the embedded content, so a broken deck fails the build.
func TestShippedCourses(t *testing.T) {
	lib, err := Load(shipped.Courses, "courses")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Courses) == 0 || len(lib.Units) == 0 {
		t.Fatal("no courses or units")
	}
	for _, c := range lib.Cards {
		if c.Kind == KindCloze && strings.Count(c.Prompt, "___") != 1 {
			t.Errorf("%s: cloze needs exactly one ___: %q", c.ID, c.Prompt)
		}
		for _, a := range c.Answers {
			if strings.TrimSpace(a) == "" {
				t.Errorf("%s: empty answer", c.ID)
			}
		}
		if c.Prompt == "" || c.Instruction == "" {
			t.Errorf("%s: empty prompt or instruction", c.ID)
		}
		if _, ok := lib.Course(c.Course); !ok {
			t.Errorf("%s: unknown course %q", c.ID, c.Course)
		}
	}
	for _, u := range lib.Units {
		if u.Focus != "" && u.FocusNote == "" {
			t.Errorf("unit %s: focus without focus_note", u.ID)
		}
	}
}

// TestOrderInterleaves checks that a unit's first cards mix kinds rather than
// front-loading all recognition cards.
func TestOrderInterleaves(t *testing.T) {
	lib, err := Load(shipped.Courses, "courses")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, c := range lib.Cards[:20] {
		kinds[c.Kind] = true
	}
	for _, k := range []string{KindRecognize, KindProduce} {
		if !kinds[k] {
			t.Errorf("first 20 new cards contain no %s cards", k)
		}
	}
	if !kinds[KindCloze] && !kinds[KindTransform] {
		t.Error("first 20 new cards contain no drills")
	}
}

// TestNonLanguageCourse: a course with only q&a drills and no labels of its own.
func TestNonLanguageCourse(t *testing.T) {
	fsys := fstest.MapFS{
		"c/go/course.json": {Data: []byte(`{"id": "go-conc", "title": "Go concurrency", "subject": "programming"}`)},
		"c/go/decks/01.json": {Data: []byte(`{
			"id": "go-01", "title": "Channels",
			"drills": [
				{"id": "go-01-q1", "type": "qa", "prompt": "What does a send on a nil channel do?", "answers": ["blocks forever"]},
				{"id": "go-01-q2", "type": "qa", "prompt": "Explain when to use a buffered channel.", "answers": ["..."], "self_grade": true}
			]}`)},
	}
	lib, err := Load(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(lib.Cards))
	}
	c := lib.Cards[0]
	if c.Kind != KindQA || c.Instruction != "Answer" || c.Course != "go-conc" || !c.IsDrill() || c.Lenient() {
		t.Errorf("qa card = %+v", c)
	}
	if !lib.Cards[1].SelfGrade {
		t.Error("self_grade not carried over")
	}
}

func TestDuplicateIDsAcrossCourses(t *testing.T) {
	deck := []byte(`{"id": "u1", "title": "U", "drills": [{"id": "x", "type": "qa", "prompt": "p", "answers": ["a"]}]}`)
	fsys := fstest.MapFS{
		"c/a/course.json":   {Data: []byte(`{"id": "a", "title": "A"}`)},
		"c/a/decks/01.json": {Data: deck},
		"c/b/course.json":   {Data: []byte(`{"id": "b", "title": "B"}`)},
		"c/b/decks/01.json": {Data: deck},
	}
	if _, err := Load(fsys, "c"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("Load = %v, want duplicate id error", err)
	}
}
