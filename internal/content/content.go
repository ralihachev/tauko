// Package content loads courses and their units, and expands them into reviewable cards.
//
// Layout: <root>/<course>/course.json plus <root>/<course>/decks/*.json, one deck per unit.
package content

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// Card kinds.
const (
	KindRecognize = "recognize" // vocab: term → meaning
	KindProduce   = "produce"   // vocab: meaning → term
	KindCloze     = "cloze"     // fill the blank
	KindTransform = "transform" // rewrite the sentence
	KindQA        = "qa"        // question → answer, any subject
	KindLesson    = "lesson"    // a short explanation, read once before its cards
)

var defaultLabels = map[string]string{
	KindRecognize: "What does it mean?",
	KindProduce:   "Translate",
	KindCloze:     "Fill in",
	KindTransform: "Rewrite",
	KindQA:        "Answer",
	KindLesson:    "Learn",
}

type courseFile struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	TitleEn     string            `json:"title_en"`
	Subject     string            `json:"subject"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	FocusLabel  string            `json:"focus_label"`
	Chars       []string          `json:"chars"`
}

// lessonItem is a micro-lesson: 40-80 words that introduce one idea before its cards.
type lessonItem struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	Example            string `json:"example"`
	ExampleTranslation string `json:"example_translation"`
}

type vocabItem struct {
	ID                 string   `json:"id"`
	Lesson             string   `json:"lesson"` // the lesson that introduces it, if any
	Term               string   `json:"term"`
	Meanings           []string `json:"meanings"`
	Example            string   `json:"example"`
	ExampleTranslation string   `json:"example_translation"`
}

type drill struct {
	ID          string   `json:"id"`
	Lesson      string   `json:"lesson"` // the lesson that introduces it, if any
	Type        string   `json:"type"`
	Prompt      string   `json:"prompt"`
	Hint        string   `json:"hint"`
	Answers     []string `json:"answers"`
	Translation string   `json:"translation"`
	Note        string   `json:"note"`
	SelfGrade   bool     `json:"self_grade"`
}

type deckFile struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Subtitle  string       `json:"subtitle"`
	Level     string       `json:"level"`
	Focus     string       `json:"focus"`
	FocusNote string       `json:"focus_note"`
	Lessons   []lessonItem `json:"lessons"`
	Vocab     []vocabItem  `json:"vocab"`
	Drills    []drill      `json:"drills"`
}

// Course is a set of units on one subject.
type Course struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	TitleEn     string   `json:"title_en,omitempty"`
	Subject     string   `json:"subject,omitempty"`
	Description string   `json:"description,omitempty"`
	FocusLabel  string   `json:"focus_label,omitempty"`
	Chars       []string `json:"chars,omitempty"` // extra characters for on-screen keyboards
	UnitIDs     []string `json:"units"`
}

// Unit is one topic with an optional focus point (a grammar point, a concept).
type Unit struct {
	ID        string   `json:"id"`
	Course    string   `json:"course"`
	Title     string   `json:"title"`
	Subtitle  string   `json:"subtitle,omitempty"`
	Level     string   `json:"level,omitempty"`
	Focus     string   `json:"focus,omitempty"`
	FocusNote string   `json:"focus_note,omitempty"`
	CardIDs   []string `json:"-"`
}

// Card is one reviewable item.
type Card struct {
	ID          string   `json:"id"`
	Course      string   `json:"course"`
	Unit        string   `json:"unit"`
	UnitTitle   string   `json:"unit_title"`
	Kind        string   `json:"kind"`
	Instruction string   `json:"instruction"`
	Prompt      string   `json:"prompt"`
	Hint        string   `json:"hint,omitempty"`
	Answers     []string `json:"answers"`
	Translation string   `json:"translation,omitempty"`
	Note        string   `json:"note,omitempty"`
	Example     string   `json:"example,omitempty"`
	ExampleTr   string   `json:"example_translation,omitempty"`
	SelfGrade   bool     `json:"self_grade,omitempty"` // answer can't be auto-checked; the learner grades
	Body        string   `json:"body,omitempty"`       // a lesson's text
}

// IsDrill reports whether the card is a drill rather than vocabulary or a lesson (used by quizzes).
func (c Card) IsDrill() bool {
	return c.Kind != KindRecognize && c.Kind != KindProduce && c.Kind != KindLesson
}

// IsLesson reports whether the card is a lesson: read once, never graded or scheduled.
func (c Card) IsLesson() bool { return c.Kind == KindLesson }

// Lenient reports whether typed answers are checked leniently ("to apply" = "apply"):
// true when the answer is a gloss in the learner's language.
func (c Card) Lenient() bool { return c.Kind == KindRecognize }

// Library holds all courses, units and cards in curriculum order.
type Library struct {
	Courses []Course
	Units   []Unit
	Cards   []Card
	byID    map[string]*Card
	units   map[string]*Unit
	courses map[string]*Course
}

// Card returns the card with the given ID.
func (l *Library) Card(id string) (Card, bool) {
	c, ok := l.byID[id]
	if !ok {
		return Card{}, false
	}
	return *c, true
}

// Unit returns the unit with the given ID.
func (l *Library) Unit(id string) (Unit, bool) {
	u, ok := l.units[id]
	if !ok {
		return Unit{}, false
	}
	return *u, true
}

// Course returns the course with the given ID.
func (l *Library) Course(id string) (Course, bool) {
	c, ok := l.courses[id]
	if !ok {
		return Course{}, false
	}
	return *c, true
}

// Source is a directory of course folders.
type Source struct {
	FS   fs.FS
	Root string
	Only []string // if set, load only these course folders
}

// Load reads every course directory under root in fsys, ordered by directory name.
func Load(fsys fs.FS, root string) (*Library, error) {
	return LoadAll(Source{FS: fsys, Root: root})
}

// LoadAll reads the courses of every source, in order (built-in courses first, then the
// user's). Card, unit and course IDs must be unique across all of them: review history
// is keyed by them.
func LoadAll(sources ...Source) (*Library, error) {
	lib := &Library{byID: map[string]*Card{}, units: map[string]*Unit{}, courses: map[string]*Course{}}
	for _, src := range sources {
		entries, err := fs.ReadDir(src.FS, src.Root)
		if err != nil {
			return nil, err
		}
		var dirs []string
		for _, e := range entries {
			if e.IsDir() && (src.Only == nil || slices.Contains(src.Only, e.Name())) {
				dirs = append(dirs, e.Name())
			}
		}
		sort.Strings(dirs)
		for _, dir := range dirs {
			if err := lib.loadCourse(src.FS, path.Join(src.Root, dir)); err != nil {
				return nil, fmt.Errorf("course %s: %w", dir, err)
			}
		}
	}
	for i := range lib.Cards {
		if _, dup := lib.byID[lib.Cards[i].ID]; dup {
			return nil, fmt.Errorf("duplicate card id %q", lib.Cards[i].ID)
		}
		lib.byID[lib.Cards[i].ID] = &lib.Cards[i]
	}
	for i := range lib.Units {
		if _, dup := lib.units[lib.Units[i].ID]; dup {
			return nil, fmt.Errorf("duplicate unit id %q", lib.Units[i].ID)
		}
		lib.units[lib.Units[i].ID] = &lib.Units[i]
	}
	for i := range lib.Courses {
		if _, dup := lib.courses[lib.Courses[i].ID]; dup {
			return nil, fmt.Errorf("duplicate course id %q", lib.Courses[i].ID)
		}
		lib.courses[lib.Courses[i].ID] = &lib.Courses[i]
	}
	return lib, nil
}

func (l *Library) loadCourse(fsys fs.FS, dir string) error {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "course.json"))
	if err != nil {
		return err
	}
	var cf courseFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		return fmt.Errorf("course.json: %w", err)
	}
	if cf.ID == "" || cf.Title == "" {
		return fmt.Errorf("course.json needs id and title")
	}
	labels := map[string]string{}
	for k, v := range defaultLabels {
		labels[k] = v
	}
	for k, v := range cf.Labels {
		labels[k] = v
	}
	course := Course{ID: cf.ID, Title: cf.Title, TitleEn: cf.TitleEn, Subject: cf.Subject,
		Description: cf.Description, FocusLabel: cf.FocusLabel, Chars: cf.Chars}

	names, err := fs.Glob(fsys, path.Join(dir, "decks", "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		var d deckFile
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("%s: %w", path.Base(name), err)
		}
		if err := l.addUnit(cf.ID, labels, d); err != nil {
			return fmt.Errorf("%s: %w", path.Base(name), err)
		}
		course.UnitIDs = append(course.UnitIDs, d.ID)
	}
	l.Courses = append(l.Courses, course)
	return nil
}

func (l *Library) addUnit(course string, labels map[string]string, d deckFile) error {
	if d.ID == "" {
		return fmt.Errorf("deck without id")
	}
	u := Unit{ID: d.ID, Course: course, Title: d.Title, Subtitle: d.Subtitle, Level: d.Level, Focus: d.Focus, FocusNote: d.FocusNote}
	push := func(c Card) error {
		if len(c.Answers) == 0 && c.Kind != KindLesson {
			return fmt.Errorf("card %q has no answers", c.ID)
		}
		c.Course, c.Unit, c.UnitTitle = course, d.ID, d.Title
		l.Cards = append(l.Cards, c)
		u.CardIDs = append(u.CardIDs, c.ID)
		return nil
	}

	lessons := map[string]bool{}
	for _, ls := range d.Lessons {
		if ls.ID == "" || strings.TrimSpace(ls.Body) == "" {
			return fmt.Errorf("lesson %q needs an id and a body", ls.ID)
		}
		lessons[ls.ID] = true
	}
	known := func(id, lesson string) error {
		if lesson != "" && !lessons[lesson] {
			return fmt.Errorf("card %q points to unknown lesson %q", id, lesson)
		}
		return nil
	}

	// Group vocab and drills by the lesson that introduces them ("" = none).
	vocabOf, drillsOf := map[string][]vocabItem{}, map[string][]Card{}
	for _, v := range d.Vocab {
		if err := known(v.ID, v.Lesson); err != nil {
			return err
		}
		vocabOf[v.Lesson] = append(vocabOf[v.Lesson], v)
	}
	for _, g := range d.Drills {
		switch g.Type {
		case KindCloze, KindTransform, KindQA:
		default:
			return fmt.Errorf("drill %q: unknown type %q", g.ID, g.Type)
		}
		if err := known(g.ID, g.Lesson); err != nil {
			return err
		}
		drillsOf[g.Lesson] = append(drillsOf[g.Lesson], Card{ID: g.ID, Kind: g.Type, Instruction: labels[g.Type],
			Prompt: g.Prompt, Hint: g.Hint, Answers: g.Answers, Translation: g.Translation, Note: g.Note, SelfGrade: g.SelfGrade})
	}

	// New-card order: each lesson, then the cards it introduces (its first drill works
	// as the quick check); then the cards no lesson introduces.
	var order []Card
	for _, ls := range d.Lessons {
		order = append(order, Card{ID: ls.ID, Kind: KindLesson, Instruction: labels[KindLesson], Prompt: ls.Title,
			Body: ls.Body, Example: ls.Example, ExampleTr: ls.ExampleTranslation})
		order = append(order, interleave(labels, vocabOf[ls.ID], drillsOf[ls.ID])...)
	}
	order = append(order, interleave(labels, vocabOf[""], drillsOf[""])...)
	for _, c := range order {
		if err := push(c); err != nil {
			return err
		}
	}
	l.Units = append(l.Units, u)
	return nil
}

// interleave orders vocab and drills in small chunks so a day's new cards mix all kinds:
// recognize chunk i, produce chunk i-1 (terms seen a little earlier), then a slice of
// drills. Without vocab it's the drills in order; with no vocab before them, drills
// lead, so a lesson's first drill comes right after the lesson.
func interleave(labels map[string]string, vocab []vocabItem, drills []Card) []Card {
	recognize := func(v vocabItem) Card {
		return Card{ID: v.ID + ":r", Kind: KindRecognize, Instruction: labels[KindRecognize],
			Prompt: v.Term, Answers: v.Meanings, Example: v.Example, ExampleTr: v.ExampleTranslation}
	}
	produce := func(v vocabItem) Card {
		return Card{ID: v.ID + ":p", Kind: KindProduce, Instruction: labels[KindProduce],
			Prompt: strings.Join(v.Meanings, " / "), Answers: []string{v.Term}, Example: v.Example, ExampleTr: v.ExampleTranslation}
	}
	const chunk = 5
	var groups [][]vocabItem
	for i := 0; i < len(vocab); i += chunk {
		groups = append(groups, vocab[i:min(i+chunk, len(vocab))])
	}
	perGroup := 0
	if len(groups) > 0 {
		perGroup = (len(drills) + len(groups) - 1) / len(groups)
	}
	var order []Card
	for i, g := range groups {
		for _, v := range g {
			order = append(order, recognize(v))
		}
		if i > 0 {
			for _, v := range groups[i-1] {
				order = append(order, produce(v))
			}
		}
		n := min(perGroup, len(drills))
		order, drills = append(order, drills[:n]...), drills[n:]
	}
	if len(groups) > 0 {
		for _, v := range groups[len(groups)-1] {
			order = append(order, produce(v))
		}
	}
	return append(order, drills...)
}

// CourseDirs lists the course folders directly under root.
func CourseDirs(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// LoadTolerant loads base, then adds each course folder of extra that loads cleanly
// alongside what is already accepted. A broken course is skipped, not fatal: its error
// is returned in skipped, keyed by folder name.
func LoadTolerant(base []Source, extra Source) (*Library, map[string]error, error) {
	lib, err := LoadAll(base...)
	if err != nil {
		return nil, nil, err
	}
	dirs, err := CourseDirs(extra.FS, extra.Root)
	if err != nil {
		return lib, nil, nil
	}
	skipped := map[string]error{}
	var accepted []string
	for _, dir := range dirs {
		try := Source{FS: extra.FS, Root: extra.Root, Only: append(slices.Clone(accepted), dir)}
		next, err := LoadAll(append(slices.Clone(base), try)...)
		if err != nil {
			skipped[dir] = err
			continue
		}
		accepted, lib = append(accepted, dir), next
	}
	return lib, skipped, nil
}
