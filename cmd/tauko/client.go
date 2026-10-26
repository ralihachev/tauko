package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ANSI styling.
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
	cyan   = "\033[36m"
	blue   = "\033[34m"
)

type card struct {
	ID          string   `json:"id"`
	Unit        string   `json:"unit"`
	UnitTitle   string   `json:"unit_title"`
	Kind        string   `json:"kind"`
	Instruction string   `json:"instruction"`
	Prompt      string   `json:"prompt"`
	Hint        string   `json:"hint"`
	Answers     []string `json:"answers"`
	Translation string   `json:"translation"`
	Note        string   `json:"note"`
	Example     string   `json:"example"`
	ExampleTr   string   `json:"example_translation"`
	SelfGrade   bool     `json:"self_grade"`
}

type checkResult struct {
	Verdict  string `json:"verdict"`
	Expected string `json:"expected"`
	Reason   string `json:"reason"`
	Rating   int    `json:"suggested_rating"`
}

type signalState struct {
	State string    `json:"state"`
	Since time.Time `json:"since"`
}

type client struct {
	base  string
	token string
	http  *http.Client
	in    *bufio.Reader
}

func newClient(timeout time.Duration) *client {
	base := os.Getenv("TAUKO_URL")
	if base == "" {
		base = "http://127.0.0.1:8787"
	}
	return &client{base: strings.TrimRight(base, "/"), token: os.Getenv("TAUKO_TOKEN"),
		http: &http.Client{Timeout: timeout}, in: bufio.NewReader(os.Stdin)}
}

func (c *client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("server not reachable at %s (run `tauko serve`): %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *client) line(prompt string) (string, bool) {
	fmt.Print(prompt)
	s, err := c.in.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return strings.TrimRight(s, "\r\n"), true
}

func learn(args []string) error {
	fl := flag.NewFlagSet("learn", flag.ExitOnError)
	mode := fl.String("mode", "type", "tap (recall + self-grade) or type (typed, auto-checked)")
	course := fl.String("course", "", "restrict to one course, e.g. fi-b1")
	unit := fl.String("unit", "", "restrict to one unit, e.g. b1-03")
	fl.Parse(args)
	if *mode != "tap" && *mode != "type" {
		return fmt.Errorf("mode must be tap or type")
	}

	c := newClient(5 * time.Second)
	fmt.Printf("%stauko%s · %s mode · %s:q%s to quit\n", bold, reset, *mode, dim, reset)
	last, lastSignal := "", c.signal()
	for n := 0; ; n++ {
		var next struct {
			Card     *card      `json:"card"`
			IsNew    bool       `json:"is_new"`
			Practice bool       `json:"practice"`
			Due      int        `json:"due"`
			NewLeft  int        `json:"new_left"`
			NextDue  *time.Time `json:"next_due"`
			Focus    string     `json:"focus"`
		}
		q := "/api/next?exclude=" + url.QueryEscape(last)
		if *course != "" {
			q += "&course=" + url.QueryEscape(*course)
		}
		if *unit != "" {
			q += "&unit=" + url.QueryEscape(*unit)
		}
		if err := c.do("GET", q, nil, &next); err != nil {
			return err
		}
		if next.Card == nil {
			fmt.Printf("\n%s✓ All done!%s Nothing due", green+bold, reset)
			if next.NextDue != nil {
				fmt.Printf(", next card in %s", time.Until(*next.NextDue).Round(time.Minute))
			}
			fmt.Println(". Try `tauko quiz`.")
			return nil
		}
		k := next.Card
		tag := ""
		if next.IsNew {
			tag = yellow + " · new" + reset
		} else if next.Practice {
			tag = cyan + " · practice" + reset
		}
		fmt.Printf("\n%s%s%s%s%s  %s[%d due · %d new left]%s\n", dim, k.UnitTitle, focusSuffix(next.Focus), reset, tag, dim, next.Due, next.NewLeft, reset)
		fmt.Printf("%s%s%s\n  %s%s%s\n", cyan, k.Instruction, reset, bold, k.Prompt, reset)
		if k.Hint != "" {
			fmt.Printf("  %s(%s)%s\n", dim, k.Hint, reset)
		}

		var rating int
		var answer, verdict string
		if *mode == "tap" || k.SelfGrade {
			if k.SelfGrade && *mode == "type" {
				// Free-form answer: write it down, compare, grade yourself.
				s, ok := c.line("  > ")
				if !ok || s == ":q" {
					return nil
				}
				answer = s
			} else {
				s, ok := c.line(dim + "  [enter] reveal " + reset)
				if !ok || s == ":q" {
					return nil
				}
			}
			reveal(k)
			var ok bool
			if rating, ok = c.grade(3); !ok {
				return nil
			}
		} else {
			s, ok := c.line("  > ")
			if !ok || s == ":q" {
				return nil
			}
			var res checkResult
			if err := c.do("POST", "/api/check", map[string]string{"card_id": k.ID, "answer": s}, &res); err != nil {
				return err
			}
			printVerdict(res)
			reveal(k)
			answer, verdict = s, res.Verdict
			rating, ok = c.grade(res.Rating)
			if !ok {
				return nil
			}
		}
		if err := c.do("POST", "/api/review", map[string]any{"card_id": k.ID, "rating": rating, "mode": *mode, "answer": answer, "verdict": verdict}, nil); err != nil {
			return err
		}
		last = k.ID

		if sig := c.signal(); sig.State == "idle" && lastSignal.State == "busy" {
			fmt.Printf("\a\n%s✓ Claude finished. Back to work, or keep going.%s\n", green+bold, reset)
			lastSignal = sig
		} else if sig.State != "" {
			lastSignal = sig
		}
	}
}

func printVerdict(res checkResult) {
	switch res.Verdict {
	case "correct":
		fmt.Printf("  %s✓ correct%s\n", green+bold, reset)
	case "close":
		fmt.Printf("  %s≈ almost%s %s(%s)%s → %s\n", yellow+bold, reset, dim, res.Reason, reset, res.Expected)
	default:
		fmt.Printf("  %s✗ wrong%s → %s\n", red+bold, reset, res.Expected)
	}
}

func reveal(k *card) {
	fmt.Printf("  %s%s%s\n", green, strings.Join(k.Answers, "  |  "), reset)
	if k.Translation != "" {
		fmt.Printf("  %s%s%s\n", dim, k.Translation, reset)
	}
	if k.Note != "" {
		fmt.Printf("  %s💡 %s%s\n", blue, k.Note, reset)
	}
	if k.Example != "" {
		fmt.Printf("  %s%s%s %s(%s)%s\n", dim, k.Example, reset, dim, k.ExampleTr, reset)
	}
}

// grade asks for a 1–4 rating; enter accepts def.
func (c *client) grade(def int) (int, bool) {
	labels := []string{"", "1 again", "2 hard", "3 good", "4 easy"}
	var parts []string
	for i := 1; i <= 4; i++ {
		if i == def {
			parts = append(parts, bold+labels[i]+reset+dim)
		} else {
			parts = append(parts, labels[i])
		}
	}
	for {
		s, ok := c.line(dim + "  " + strings.Join(parts, " · ") + "  [enter=" + strconv.Itoa(def) + "] " + reset)
		if !ok || s == ":q" {
			return 0, false
		}
		if s == "" {
			return def, true
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 4 {
			return n, true
		}
	}
}

func (c *client) signal() signalState {
	var s signalState
	_ = c.do("GET", "/api/signal", nil, &s)
	return s
}

func quiz(args []string) error {
	fl := flag.NewFlagSet("quiz", flag.ExitOnError)
	course := fl.String("course", "", "course id, e.g. fi-b1 (empty = all courses)")
	unit := fl.String("unit", "", "unit id, e.g. b1-03 (empty = whole course)")
	n := fl.Int("n", 10, "number of questions")
	fl.Parse(args)

	c := newClient(5 * time.Second)
	var q struct {
		Cards []card `json:"cards"`
	}
	if err := c.do("POST", "/api/quiz", map[string]any{"course": *course, "unit": *unit, "n": *n}, &q); err != nil {
		return err
	}
	title := "everything"
	switch {
	case len(q.Cards) > 0 && *unit != "":
		title = q.Cards[0].UnitTitle
	case *course != "":
		title = *course
	}
	fmt.Printf("%sQuiz: %s%s · %d questions · %s:q%s to quit\n", bold, title, reset, len(q.Cards), dim, reset)
	score := 0
	for i, k := range q.Cards {
		fmt.Printf("\n%s%d/%d%s %s%s%s\n  %s%s%s\n", dim, i+1, len(q.Cards), reset, cyan, k.Instruction, reset, bold, k.Prompt, reset)
		if k.Hint != "" {
			fmt.Printf("  %s(%s)%s\n", dim, k.Hint, reset)
		}
		s, ok := c.line("  > ")
		if !ok || s == ":q" {
			return nil
		}
		var res checkResult
		if err := c.do("POST", "/api/check", map[string]string{"card_id": k.ID, "answer": s}, &res); err != nil {
			return err
		}
		printVerdict(res)
		if res.Verdict != "wrong" {
			score++
		}
		if res.Verdict != "correct" && k.Note != "" {
			fmt.Printf("  %s💡 %s%s\n", blue, k.Note, reset)
		}
		if err := c.do("POST", "/api/review", map[string]any{"card_id": k.ID, "rating": res.Rating, "mode": "quiz", "answer": s, "verdict": res.Verdict}, nil); err != nil {
			return err
		}
	}
	pct := 100 * score / max(1, len(q.Cards))
	color := red
	switch {
	case pct >= 80:
		color = green
	case pct >= 60:
		color = yellow
	}
	fmt.Printf("\n%s%sScore: %d/%d (%d%%)%s\n", bold, color, score, len(q.Cards), pct, reset)
	return c.do("POST", "/api/quiz/result", map[string]any{"course": *course, "unit": *unit, "score": score, "total": len(q.Cards)}, nil)
}

func units() error {
	c := newClient(5 * time.Second)
	var courses []struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		TitleEn string   `json:"title_en"`
		Units   []string `json:"units"`
		Total   int      `json:"total"`
		Seen    int      `json:"seen"`
		Due     int      `json:"due"`
	}
	if err := c.do("GET", "/api/courses", nil, &courses); err != nil {
		return err
	}
	var us []struct {
		ID       string   `json:"id"`
		Course   string   `json:"course"`
		Title    string   `json:"title"`
		Subtitle string   `json:"subtitle"`
		Focus    string   `json:"focus"`
		Total    int      `json:"total"`
		Seen     int      `json:"seen"`
		Due      int      `json:"due"`
		BestQuiz *float64 `json:"best_quiz"`
	}
	if err := c.do("GET", "/api/units", nil, &us); err != nil {
		return err
	}
	for i, co := range courses {
		if i > 0 {
			fmt.Println()
		}
		title := co.Title
		if co.TitleEn != "" && co.TitleEn != co.Title {
			title += " (" + co.TitleEn + ")"
		}
		fmt.Printf("%s%s%s  %s%s · %d/%d seen · %d due%s\n", bold+cyan, title, reset, dim, co.ID, co.Seen, co.Total, co.Due, reset)
		for _, u := range us {
			if u.Course != co.ID {
				continue
			}
			quiz := dim + "  —" + reset
			if u.BestQuiz != nil {
				quiz = fmt.Sprintf("%3.0f%%", *u.BestQuiz*100)
			}
			fmt.Printf("  %s%-8s%s %-26s %s %3d/%-3d %s due %-3d%s quiz %s\n",
				bold, u.ID, reset, u.Title, progressBar(u.Seen, u.Total, 12), u.Seen, u.Total, dim, u.Due, reset, quiz)
			if sub := strings.Trim(u.Subtitle+" · "+u.Focus, " ·"); sub != "" {
				fmt.Printf("    %s%s%s\n", dim, sub, reset)
			}
		}
	}
	return nil
}

// focusSuffix renders a unit's focus point after its title.
func focusSuffix(focus string) string {
	if focus == "" {
		return ""
	}
	return " · " + focus
}

func progressBar(n, total, width int) string {
	filled := 0
	if total > 0 {
		filled = n * width / total
	}
	return green + strings.Repeat("█", filled) + dim + strings.Repeat("░", width-filled) + reset
}

type statsResp struct {
	Stats struct {
		ReviewsToday  int     `json:"reviews_today"`
		AccuracyToday float64 `json:"accuracy_today"`
		Streak        int     `json:"streak_days"`
		TotalReviews  int     `json:"total_reviews"`
		Learned       int     `json:"learned"`
	} `json:"stats"`
	Due      int      `json:"due"`
	NewLeft  int      `json:"new_left"`
	Cards    int      `json:"cards"`
	Warnings []string `json:"warnings"`
}

func stats() error {
	c := newClient(5 * time.Second)
	var s statsResp
	if err := c.do("GET", "/api/stats", nil, &s); err != nil {
		return err
	}
	fmt.Printf("today     %d reviews · %.0f%% good\n", s.Stats.ReviewsToday, s.Stats.AccuracyToday*100)
	fmt.Printf("queue     %d due · %d new left today\n", s.Due, s.NewLeft)
	fmt.Printf("streak    %d days 🔥\n", s.Stats.Streak)
	fmt.Printf("learned   %d / %d cards · %d reviews all time\n", s.Stats.Learned, s.Cards, s.Stats.TotalReviews)
	return nil
}

// status prints one short line for the Claude Code status line. It never fails loudly.
func status() {
	c := newClient(400 * time.Millisecond)
	var s statsResp
	if err := c.do("GET", "/api/stats", nil, &s); err != nil {
		fmt.Print("tauko off")
		return
	}
	todo := s.Due + s.NewLeft
	line := fmt.Sprintf("📖 %d to do · %d today", todo, s.Stats.ReviewsToday)
	if s.Stats.Streak > 0 {
		line += fmt.Sprintf(" · 🔥%d", s.Stats.Streak)
	}
	fmt.Print(line)
}

// signal reports Claude Code busy/idle. Hooks must never block or fail, so errors are ignored.
func signal(args []string) {
	if len(args) != 1 || (args[0] != "busy" && args[0] != "idle") {
		fmt.Fprintln(os.Stderr, "usage: tauko signal busy|idle")
		os.Exit(2)
	}
	c := newClient(500 * time.Millisecond)
	_ = c.do("POST", "/api/signal", map[string]string{"state": args[0]}, nil)
}

// course shows the courses, or switches the active one ("all" for every course).
// Every client (pane, terminal, browser) studies the active course.
func course(args []string) error {
	c := newClient(5 * time.Second)
	if len(args) == 1 {
		id := args[0]
		if id == "all" {
			id = ""
		}
		if err := c.do("POST", "/api/settings", map[string]string{"active_course": id}, nil); err != nil {
			return err
		}
		if id == "" {
			id = "all courses"
		}
		ok("now studying " + id)
		return nil
	}
	var settings struct {
		ActiveCourse string `json:"active_course"`
	}
	var courses []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Total int    `json:"total"`
		Seen  int    `json:"seen"`
		Due   int    `json:"due"`
	}
	if err := c.do("GET", "/api/settings", nil, &settings); err != nil {
		return err
	}
	if err := c.do("GET", "/api/courses", nil, &courses); err != nil {
		return err
	}
	mark := func(on bool) string {
		if on {
			return green + "●" + reset
		}
		return " "
	}
	fmt.Printf("%s %-16s %s\n", mark(settings.ActiveCourse == ""), "all", "every course, new cards rotate")
	for _, co := range courses {
		fmt.Printf("%s %-16s %s %s%d/%d seen · %d due%s\n", mark(settings.ActiveCourse == co.ID), co.ID, co.Title, dim, co.Seen, co.Total, co.Due, reset)
	}
	fmt.Printf("%sswitch with: tauko course <id|all>%s\n", dim, reset)
	return nil
}

// more raises today's new-card limit.
func more(args []string) error {
	n := 10
	if len(args) == 1 {
		if v, err := strconv.Atoi(args[0]); err == nil {
			n = v
		}
	}
	var res struct {
		NewLeft int `json:"new_left"`
	}
	if err := newClient(5*time.Second).do("POST", "/api/more", map[string]int{"n": n}, &res); err != nil {
		return err
	}
	ok(fmt.Sprintf("%d new cards left today", res.NewLeft))
	return nil
}
