package content

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// examples is the public course gallery (examples/courses), validated so a broken
// course fails CI.
var examples = Source{FS: os.DirFS("../../examples/courses"), Root: "."}

// TestExampleCourses validates every course in the gallery.
func TestExampleCourses(t *testing.T) {
	lib, err := LoadAll(examples)
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
	var vocab, drills []string
	for i := 1; i <= 15; i++ {
		vocab = append(vocab, fmt.Sprintf(`{"id": "v%d", "term": "t%d", "meanings": ["m%d"]}`, i, i, i))
		drills = append(drills, fmt.Sprintf(`{"id": "q%d", "type": "qa", "prompt": "p%d", "answers": ["a"]}`, i, i))
	}
	deck := fmt.Sprintf(`{"id": "u1", "title": "U", "vocab": [%s], "drills": [%s]}`, strings.Join(vocab, ","), strings.Join(drills, ","))
	lib, err := Load(fstest.MapFS{
		"c/x/course.json":   {Data: []byte(`{"id": "x", "title": "X"}`)},
		"c/x/decks/01.json": {Data: []byte(deck)},
	}, "c")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, c := range lib.Cards[:20] {
		kinds[c.Kind] = true
	}
	for _, k := range []string{KindRecognize, KindProduce, KindQA} {
		if !kinds[k] {
			t.Errorf("first 20 new cards contain no %s cards", k)
		}
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

func TestLoadTolerantSkipsBrokenCourse(t *testing.T) {
	base := []Source{{FS: fstest.MapFS{
		"c/a/course.json":   {Data: []byte(`{"id": "a", "title": "A"}`)},
		"c/a/decks/01.json": {Data: []byte(`{"id": "a1", "title": "U", "drills": [{"id": "a-x", "type": "qa", "prompt": "p", "answers": ["x"]}]}`)},
	}, Root: "c"}}
	user := fstest.MapFS{
		"u/good/course.json":    {Data: []byte(`{"id": "good", "title": "Good"}`)},
		"u/good/decks/01.json":  {Data: []byte(`{"id": "g1", "title": "U", "drills": [{"id": "g-x", "type": "qa", "prompt": "p", "answers": ["x"]}]}`)},
		"u/bad/course.json":     {Data: []byte(`{"id": "bad", "title": "Bad"`)},
		"u/clash/course.json":   {Data: []byte(`{"id": "clash", "title": "Clash"}`)},
		"u/clash/decks/01.json": {Data: []byte(`{"id": "c1", "title": "U", "drills": [{"id": "a-x", "type": "qa", "prompt": "p", "answers": ["x"]}]}`)},
	}
	lib, skipped, err := LoadTolerant(base, Source{FS: user, Root: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lib.Course("good"); !ok {
		t.Error("good course not loaded")
	}
	if len(lib.Courses) != 2 || len(skipped) != 2 || skipped["bad"] == nil || skipped["clash"] == nil {
		t.Errorf("courses=%d skipped=%v", len(lib.Courses), skipped)
	}
}

func TestLessonsComeBeforeTheirCards(t *testing.T) {
	deck := `{"id": "u1", "title": "U",
		"lessons": [{"id": "l1", "title": "Superposition", "body": "A qubit holds a mix of 0 and 1 until measured."}],
		"drills": [
			{"id": "q0", "type": "qa", "prompt": "untagged", "answers": ["x"]},
			{"id": "q1", "type": "qa", "prompt": "What does measuring do?", "answers": ["picks 0 or 1"], "lesson": "l1"},
			{"id": "q2", "type": "qa", "prompt": "later", "answers": ["y"], "lesson": "l1"}
		]}`
	lib, err := Load(fstest.MapFS{
		"c/x/course.json":   {Data: []byte(`{"id": "x", "title": "X"}`)},
		"c/x/decks/01.json": {Data: []byte(deck)},
	}, "c")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range lib.Cards {
		ids = append(ids, c.ID)
	}
	if got := strings.Join(ids, ","); got != "l1,q1,q2,q0" {
		t.Errorf("order = %s, want l1,q1,q2,q0", got)
	}
	l, _ := lib.Card("l1")
	if !l.IsLesson() || l.IsDrill() || l.Body == "" || l.Instruction != "Learn" {
		t.Errorf("lesson card = %+v", l)
	}
}

func TestLessonErrors(t *testing.T) {
	for name, deck := range map[string]string{
		"unknown lesson": `{"id": "u1", "title": "U", "drills": [{"id": "q", "type": "qa", "prompt": "p", "answers": ["a"], "lesson": "nope"}]}`,
		"empty body":     `{"id": "u1", "title": "U", "lessons": [{"id": "l1", "title": "T", "body": " "}]}`,
	} {
		_, err := Load(fstest.MapFS{
			"c/x/course.json":   {Data: []byte(`{"id": "x", "title": "X"}`)},
			"c/x/decks/01.json": {Data: []byte(deck)},
		}, "c")
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
