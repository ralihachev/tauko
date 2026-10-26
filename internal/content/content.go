// Package content loads courses and their units, and expands them into reviewable cards.
//
// Layout: <root>/<course>/course.json plus <root>/<course>/decks/*.json, one deck per unit.
package content

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
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
)

var defaultLabels = map[string]string{
	KindRecognize: "What does it mean?",
	KindProduce:   "Translate",
	KindCloze:     "Fill in",
	KindTransform: "Rewrite",
	KindQA:        "Answer",
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

type vocabItem struct {
	ID                 string   `json:"id"`
	Term               string   `json:"term"`
	Meanings           []string `json:"meanings"`
	Example            string   `json:"example"`
	ExampleTranslation string   `json:"example_translation"`
}

type drill struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Prompt      string   `json:"prompt"`
	Hint        string   `json:"hint"`
	Answers     []string `json:"answers"`
	Translation string   `json:"translation"`
	Note        string   `json:"note"`
	SelfGrade   bool     `json:"self_grade"`
}

type deckFile struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Subtitle  string      `json:"subtitle"`
	Level     string      `json:"level"`
	Focus     string      `json:"focus"`
	FocusNote string      `json:"focus_note"`
	Vocab     []vocabItem `json:"vocab"`
	Drills    []drill     `json:"drills"`
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
}

// IsDrill reports whether the card is a drill rather than vocabulary (used by quizzes).
func (c Card) IsDrill() bool { return c.Kind != KindRecognize && c.Kind != KindProduce }

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

// Load reads every course directory under root in fsys, ordered by directory name.
// Card and unit IDs must be unique across all courses: review history is keyed by them.
func Load(fsys fs.FS, root string) (*Library, error) {
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

	lib := &Library{byID: map[string]*Card{}, units: map[string]*Unit{}, courses: map[string]*Course{}}
	for _, dir := range dirs {
		if err := lib.loadCourse(fsys, path.Join(root, dir)); err != nil {
			return nil, fmt.Errorf("course %s: %w", dir, err)
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
	if _, dup := l.courses[cf.ID]; dup {
		return fmt.Errorf("duplicate course id %q", cf.ID)
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
		if len(c.Answers) == 0 {
			return fmt.Errorf("card %q has no answers", c.ID)
		}
		c.Course, c.Unit, c.UnitTitle = course, d.ID, d.Title
		l.Cards = append(l.Cards, c)
		u.CardIDs = append(u.CardIDs, c.ID)
		return nil
	}

	recognize := func(v vocabItem) Card {
		return Card{ID: v.ID + ":r", Kind: KindRecognize, Instruction: labels[KindRecognize],
			Prompt: v.Term, Answers: v.Meanings, Example: v.Example, ExampleTr: v.ExampleTranslation}
	}
	produce := func(v vocabItem) Card {
		return Card{ID: v.ID + ":p", Kind: KindProduce, Instruction: labels[KindProduce],
			Prompt: strings.Join(v.Meanings, " / "), Answers: []string{v.Term}, Example: v.Example, ExampleTr: v.ExampleTranslation}
	}
	var drills []Card
	for _, g := range d.Drills {
		switch g.Type {
		case KindCloze, KindTransform, KindQA:
		default:
			return fmt.Errorf("drill %q: unknown type %q", g.ID, g.Type)
		}
		drills = append(drills, Card{ID: g.ID, Kind: g.Type, Instruction: labels[g.Type], Prompt: g.Prompt, Hint: g.Hint,
			Answers: g.Answers, Translation: g.Translation, Note: g.Note, SelfGrade: g.SelfGrade})
	}

	// New-card order interleaves small chunks so a day's new cards mix all kinds:
	// recognize chunk i, produce chunk i-1 (terms seen a little earlier), then a slice
	// of drills. A unit without vocab is just its drills in order.
	const chunk = 5
	var groups [][]vocabItem
	for i := 0; i < len(d.Vocab); i += chunk {
		groups = append(groups, d.Vocab[i:min(i+chunk, len(d.Vocab))])
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
	order = append(order, drills...)
	for _, c := range order {
		if err := push(c); err != nil {
			return err
		}
	}
	l.Units = append(l.Units, u)
	return nil
}
