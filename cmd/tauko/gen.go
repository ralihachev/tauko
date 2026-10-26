package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	shipped "github.com/ralihachev/tauko/content"
	"github.com/ralihachev/tauko/internal/content"
)

// generator sends a system prompt and a prompt to a model and returns its text reply.
// files are paths the model may read (for -from); nil means no tools at all.
type generator func(ctx context.Context, system, prompt string, files []string) (string, error)

// genCourse is what the model writes: a course without IDs. tauko assigns the IDs itself,
// so they're stable and unique no matter what the model does.
type genCourse struct {
	Course struct {
		Title       string            `json:"title"`
		TitleEn     string            `json:"title_en,omitempty"`
		Subject     string            `json:"subject,omitempty"`
		Description string            `json:"description,omitempty"`
		Labels      map[string]string `json:"labels,omitempty"`
		FocusLabel  string            `json:"focus_label,omitempty"`
		Chars       []string          `json:"chars,omitempty"`
	} `json:"course"`
	Units []struct {
		Title     string `json:"title"`
		Subtitle  string `json:"subtitle,omitempty"`
		Level     string `json:"level,omitempty"`
		Focus     string `json:"focus,omitempty"`
		FocusNote string `json:"focus_note,omitempty"`
		Vocab     []struct {
			Term               string   `json:"term"`
			Meanings           []string `json:"meanings"`
			Example            string   `json:"example,omitempty"`
			ExampleTranslation string   `json:"example_translation,omitempty"`
		} `json:"vocab,omitempty"`
		Drills []struct {
			Type        string   `json:"type"`
			Prompt      string   `json:"prompt"`
			Hint        string   `json:"hint,omitempty"`
			Answers     []string `json:"answers"`
			Translation string   `json:"translation,omitempty"`
			Note        string   `json:"note,omitempty"`
			SelfGrade   bool     `json:"self_grade,omitempty"`
		} `json:"drills,omitempty"`
	} `json:"units"`
	Changes []string `json:"changes,omitempty"` // what the verification pass fixed
}

const genSystem = `You write flashcard courses for tauko, a spaced-repetition app used in 10-60 second gaps.
You reply with ONE JSON object and nothing else: no prose, no markdown fences.`

const genFormat = `JSON format (no "id" fields anywhere; the app assigns them):
{
  "course": {
    "title": "short course title, in the course's language",
    "title_en": "English title if the title isn't English, else omit",
    "subject": "one word: language | programming | science | ...",
    "description": "one sentence: what the learner can do after this course",
    "labels": {"recognize": "...", "produce": "...", "cloze": "...", "transform": "...", "qa": "..."},
    "focus_label": "what a unit's focus point is called, e.g. Grammar or Concept",
    "chars": ["special characters a phone keyboard may lack, e.g. ä, ö; else omit"]
  },
  "units": [{
    "title": "...", "subtitle": "...", "level": "...",
    "focus": "the one idea or rule this unit drills",
    "focus_note": "2-4 sentence explanation with an example",
    "vocab": [{"term": "...", "meanings": ["...", "synonym"], "example": "...", "example_translation": "..."}],
    "drills": [{"type": "qa | cloze | transform", "prompt": "...", "hint": "...",
                "answers": ["canonical answer", "every other correct variant"],
                "translation": "...", "note": "one-line explanation shown after answering",
                "self_grade": false}]
  }]
}

Card rules:
- Every card must be answerable in under 20 seconds. Prefer answers of 1-5 words that can be typed and checked.
- "answers" lists EVERY acceptable answer: synonyms, word orders, spelling variants, with and without articles. answers[0] is canonical.
- cloze: the prompt contains exactly one "___"; the hint makes the answer unambiguous (base form + what to produce).
- transform: the prompt states the instruction and the sentence; answers are full sentences.
- qa: a direct question. Use "self_grade": true only for open "explain/compare/when" questions with no single right answer (at most 1 in 5 cards); give a model answer.
- vocab only for term <-> meaning pairs (languages, jargon). meanings are short glosses in the learner's language.
- "labels" are the prompts shown above cards; for a language course write them in that language (e.g. "Mitä tarkoittaa?"); otherwise omit "labels".
- Every fact must be correct. If unsure about a fact, leave that card out.
- Write original content; never copy text from books or courses.`

const verifySystem = `You are a strict reviewer of flashcards for tauko. You reply with ONE JSON object and nothing else.`

const verifyPrompt = `Review this course card by card, as an expert in the subject would.

For each card check:
1. Is answers[0] correct? Fix it if not.
2. Are other correct answers missing (synonyms, word orders, spelling variants)? Add them.
3. Is the prompt unambiguous, so that only the listed answers are correct? Sharpen the prompt or hint if not.
4. Could a learner answer it in under 20 seconds? Shorten it if not.
5. Is every note, translation and example correct?
Remove any card you can't make correct and unambiguous.

Return the whole course in the same JSON format, corrected, plus a "changes" array of short strings,
one per change you made (empty if none). Course:
`

func gen(args []string) error {
	fl := flag.NewFlagSet("gen", flag.ExitOnError)
	level := fl.String("level", "", "learner level, e.g. beginner, intermediate, B1")
	units := fl.Int("units", 3, "number of units")
	cards := fl.Int("cards", 12, "cards per unit")
	from := fl.String("from", "", "build the course from this file (text, markdown or PDF)")
	id := fl.String("id", "", "course id (default: from the title)")
	lang := fl.String("lang", "English", "language for explanations and translations")
	model := fl.String("model", "", "Claude model to use (default: Claude Code's default)")
	yes := fl.Bool("y", false, "install without asking")
	dry := fl.Bool("dry-run", false, "show the course, don't install it")
	fl.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tauko gen [flags] \"topic\"\n\nGenerates a course with Claude Code (claude -p), verifies every answer, and installs it.\n\nflags:")
		fl.PrintDefaults()
	}
	fl.Parse(args)
	topic := strings.TrimSpace(strings.Join(fl.Args(), " "))
	if topic == "" && *from == "" {
		fl.Usage()
		return errors.New(`give a topic, e.g. tauko gen "Kubernetes networking"`)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("tauko gen uses Claude Code (claude -p); install it from https://claude.com/claude-code")
	}
	return runGen(context.Background(), claudeGenerator(*model), genOptions{
		Topic: topic, Level: *level, Units: *units, Cards: *cards, From: *from, ID: *id,
		Lang: *lang, Yes: *yes, DryRun: *dry, Dir: filepath.Join(dataDir(), "courses"),
		Restart: restartServer,
	})
}

// restartServer reloads courses by restarting the local server.
func restartServer() error {
	if newClient(300 * time.Millisecond).up() {
		stopServer()
		time.Sleep(300 * time.Millisecond)
	}
	return ensure()
}

type genOptions struct {
	Topic, Level, From, ID, Lang string
	Units, Cards                 int
	Yes, DryRun                  bool
	Dir                          string       // where courses are installed
	Restart                      func() error // reloads the server after installing
}

func runGen(ctx context.Context, model generator, o genOptions) error {
	var files []string
	source := ""
	if o.From != "" {
		abs, err := filepath.Abs(o.From)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err != nil {
			return err
		}
		files = []string{abs}
		source = fmt.Sprintf("\nBase the course on the material in the file %s (read it first). Cover its most useful facts; don't invent facts it doesn't support.", abs)
	}
	level := o.Level
	if level == "" {
		level = "unspecified: start accessible, end challenging"
	}
	prompt := fmt.Sprintf("Write a course.\nTopic: %s\nLearner level: %s\nUnits: %d, about %d cards each (a vocab item counts as 2 cards), ordered from basic to advanced.\nExplanations, notes and translations in: %s%s\n\n%s",
		orDefault(o.Topic, "the material in the file"), level, o.Units, o.Cards, o.Lang, source, genFormat)

	step("Drafting the course with Claude (about a minute)")
	draft, err := askJSON(ctx, model, genSystem, prompt, files)
	if err != nil {
		return fmt.Errorf("draft: %w", err)
	}
	ok(fmt.Sprintf("%d units, %d cards", len(draft.Units), countCards(draft)))

	step("Verifying every answer with a second, independent pass")
	raw, _ := json.Marshal(draft)
	checked, err := askJSON(ctx, model, verifySystem, verifyPrompt+string(raw), nil)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if len(checked.Changes) == 0 {
		ok("no problems found")
	} else {
		ok(fmt.Sprintf("%d fixes:", len(checked.Changes)))
		for _, c := range checked.Changes {
			fmt.Printf("      %s- %s%s\n", dim, c, reset)
		}
	}

	courseID := o.ID
	if courseID == "" {
		courseID = slug(orDefault(checked.Course.Title, o.Topic))
	}
	files2, err := courseFiles(courseID, checked)
	if err != nil {
		return err
	}

	step("Checking the course")
	if err := validateCourse(o.Dir, courseID, files2); err != nil {
		return err
	}
	ok("valid")

	preview(courseID, checked)
	if o.DryRun {
		return nil
	}
	if !o.Yes && !confirm(fmt.Sprintf("Install it to %s?", filepath.Join(o.Dir, courseID))) {
		fmt.Println("Not installed.")
		return nil
	}
	if err := writeCourse(filepath.Join(o.Dir, courseID), files2); err != nil {
		return err
	}
	ok("installed " + filepath.Join(o.Dir, courseID))
	if o.Restart != nil {
		if err := o.Restart(); err != nil {
			return err
		}
		ok("server restarted")
	}
	fmt.Printf("\nStudy it: press %sc%s in the tauko pane, or run %stauko course %s%s\n", bold, reset, bold, courseID, reset)
	return nil
}

// askJSON runs the model and parses its reply as a genCourse, asking once more if the
// reply isn't valid JSON.
func askJSON(ctx context.Context, model generator, system, prompt string, files []string) (*genCourse, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		p := prompt
		if lastErr != nil {
			p += "\n\nYour previous reply was not valid JSON (" + lastErr.Error() + "). Reply with the JSON object only."
		}
		out, err := model(ctx, system, p, files)
		if err != nil {
			return nil, err
		}
		var gc genCourse
		if lastErr = json.Unmarshal([]byte(extractJSON(out)), &gc); lastErr == nil {
			if len(gc.Units) == 0 {
				lastErr = errors.New("no units")
				continue
			}
			return &gc, nil
		}
	}
	return nil, lastErr
}

// extractJSON returns the outermost {...} of s, so a stray code fence or sentence around
// the JSON doesn't break parsing.
func extractJSON(s string) string {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

// courseFiles turns the generated course into course.json and one deck file per unit,
// with IDs assigned by tauko: <course>-uNN, <course>-uNN-vNN, <course>-uNN-qNN.
func courseFiles(courseID string, gc *genCourse) (map[string][]byte, error) {
	files := map[string][]byte{}
	course := map[string]any{"id": courseID, "title": orDefault(gc.Course.Title, courseID)}
	for k, v := range map[string]any{"title_en": gc.Course.TitleEn, "subject": gc.Course.Subject,
		"description": gc.Course.Description, "focus_label": gc.Course.FocusLabel} {
		if v != "" {
			course[k] = v
		}
	}
	if len(gc.Course.Labels) > 0 {
		course["labels"] = gc.Course.Labels
	}
	if len(gc.Course.Chars) > 0 {
		course["chars"] = gc.Course.Chars
	}
	b, err := marshal(course)
	if err != nil {
		return nil, err
	}
	files["course.json"] = b

	for i, u := range gc.Units {
		uid := fmt.Sprintf("%s-u%02d", courseID, i+1)
		deck := map[string]any{"id": uid, "title": orDefault(u.Title, fmt.Sprintf("Unit %d", i+1))}
		for k, v := range map[string]string{"subtitle": u.Subtitle, "level": u.Level, "focus": u.Focus, "focus_note": u.FocusNote} {
			if v != "" {
				deck[k] = v
			}
		}
		var vocab, drills []map[string]any
		for j, v := range u.Vocab {
			if v.Term == "" || len(v.Meanings) == 0 {
				continue
			}
			item := map[string]any{"id": fmt.Sprintf("%s-v%02d", uid, j+1), "term": v.Term, "meanings": v.Meanings}
			if v.Example != "" {
				item["example"], item["example_translation"] = v.Example, v.ExampleTranslation
			}
			vocab = append(vocab, item)
		}
		for j, d := range u.Drills {
			if d.Prompt == "" || len(d.Answers) == 0 {
				continue
			}
			item := map[string]any{"id": fmt.Sprintf("%s-q%02d", uid, j+1), "type": d.Type, "prompt": d.Prompt, "answers": d.Answers}
			for k, v := range map[string]string{"hint": d.Hint, "translation": d.Translation, "note": d.Note} {
				if v != "" {
					item[k] = v
				}
			}
			if d.SelfGrade {
				item["self_grade"] = true
			}
			drills = append(drills, item)
		}
		if len(vocab) > 0 {
			deck["vocab"] = vocab
		}
		if len(drills) > 0 {
			deck["drills"] = drills
		}
		if b, err = marshal(deck); err != nil {
			return nil, err
		}
		files[fmt.Sprintf("decks/%02d-%s.json", i+1, slug(orDefault(u.Title, "unit")))] = b
	}
	return files, nil
}

// validateCourse loads the new course next to every course that exists, with the same
// checks as the shipped courses, and refuses an ID that's already taken.
func validateCourse(dir, courseID string, files map[string][]byte) error {
	if _, err := os.Stat(filepath.Join(dir, courseID)); err == nil {
		return fmt.Errorf("a course named %q already exists in %s; pick another with -id", courseID, dir)
	}
	tmp, err := os.MkdirTemp("", "tauko-gen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := writeCourse(filepath.Join(tmp, courseID), files); err != nil {
		return err
	}
	sources := []content.Source{{FS: shipped.Courses, Root: "courses"}, {FS: os.DirFS(tmp), Root: "."}}
	if _, err := os.Stat(dir); err == nil {
		sources = append(sources, content.Source{FS: os.DirFS(dir), Root: "."})
	}
	lib, err := content.LoadAll(sources...)
	if err != nil {
		return fmt.Errorf("generated course is invalid: %w", err)
	}
	for _, c := range lib.Cards {
		if c.Course == courseID && c.Kind == content.KindCloze && strings.Count(c.Prompt, "___") != 1 {
			return fmt.Errorf("card %s: a fill-in card needs exactly one ___", c.ID)
		}
	}
	return nil
}

func writeCourse(dir string, files map[string][]byte) error {
	for name, b := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func preview(courseID string, gc *genCourse) {
	fmt.Printf("\n%s%s%s %s(%s)%s\n", bold, gc.Course.Title, reset, dim, courseID, reset)
	if gc.Course.Description != "" {
		fmt.Printf("%s%s%s\n", dim, gc.Course.Description, reset)
	}
	for i, u := range gc.Units {
		fmt.Printf("  %2d. %-34s %s%d cards%s\n", i+1, u.Title, dim, len(u.Vocab)*2+len(u.Drills), reset)
	}
	shown := 0
	fmt.Printf("\n%sSample cards%s\n", bold, reset)
	for _, u := range gc.Units {
		for _, d := range u.Drills {
			if shown == 4 || len(d.Answers) == 0 {
				break
			}
			fmt.Printf("  %s  %s→ %s%s\n", d.Prompt, green, d.Answers[0], reset)
			shown++
		}
	}
	fmt.Println()
}

func confirm(q string) bool {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return true // not a terminal (piped/scripted): don't block
	}
	fmt.Printf("%s [Y/n] ", q)
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "" || s == "y" || s == "yes"
}

func countCards(gc *genCourse) int {
	n := 0
	for _, u := range gc.Units {
		n += len(u.Vocab)*2 + len(u.Drills)
	}
	return n
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.NewReplacer("ä", "a", "ö", "o", "å", "a", "ü", "u", "é", "e").Replace(strings.ToLower(s))
	s = strings.Trim(nonSlug.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return orDefault(s, "course")
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return buf.Bytes(), err
}

// claudeGenerator runs Claude Code headless and lean: its own system prompt, no tools
// (Read only for -from files), no MCP servers, no skills, no saved session. That keeps
// a call to a few thousand tokens instead of the ~100k a full session loads.
func claudeGenerator(model string) generator {
	return func(ctx context.Context, system, prompt string, files []string) (string, error) {
		args := []string{"-p", "--output-format", "json", "--system-prompt", system,
			"--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"}
		if len(files) == 0 {
			args = append(args, "--tools", "")
		} else {
			args = append(args, "--tools", "Read")
			for _, f := range files {
				args = append(args, "--add-dir", filepath.Dir(f))
			}
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", args...)
		cmd.Stdin = strings.NewReader(prompt)
		cmd.Dir = os.TempDir()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		var env struct {
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			return "", fmt.Errorf("claude -p: unexpected output: %w", err)
		}
		if env.IsError {
			return "", fmt.Errorf("claude -p: %s", env.Result)
		}
		return env.Result, nil
	}
}
