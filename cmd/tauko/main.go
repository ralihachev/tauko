// Command tauko is a spaced-repetition trainer for the gaps while your AI agent works.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ralihachev/tauko/internal/content"
	"github.com/ralihachev/tauko/internal/llm"
	"github.com/ralihachev/tauko/internal/server"
	"github.com/ralihachev/tauko/internal/store"
	"github.com/ralihachev/tauko/web"
)

// version is set at release time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `tauko: learn anything in the gaps while Claude works

usage:
  tauko setup                                  install the Claude Code pane, start the server
  tauko claude [claude flags…]                 start Claude Code in fullscreen, so the pane docks beside the chat
  tauko doctor                                 check the setup and say how to fix it
  tauko update                                 upgrade to the latest release (reruns the installer)
  tauko uninstall [-purge]                     remove the pane and stop the server
  tauko serve [-addr 127.0.0.1:8787] [-db ~/.tauko/tauko.db] [-new-per-day 20] [-courses dir]
  tauko learn [-mode tap|type] [-course id] [-unit id]   review due + new cards (default command)
  tauko quiz  [-course id] [-unit id] [-n 10]            typed self-quiz, scored
  tauko units                                  courses, units and progress
  tauko course [id|all]                        show courses, or switch the one you study
  tauko more [n]                               learn n more unseen cards today (default 10)
  tauko retired [course]                       cards you marked "I know this" (k after answering)
  tauko restore <card-id>…                     bring retired cards back
  tauko gen "topic" [-level x] [-from file]    generate a course with Claude, verified, installed
  tauko add [id|folder]                        list the course gallery, or install a course
  tauko stats                                  today's numbers
  tauko status                                 one line for the Claude Code status line
  tauko ensure                                 start the server in the background unless it runs
  tauko restart                                restart the server (after editing a course by hand)
  tauko signal busy|idle                       called from Claude Code hooks

env:
  TAUKO_URL    server URL for clients (default http://127.0.0.1:8787)
  TAUKO_TOKEN  shared secret; required by the server when set
  TAUKO_NO_UPDATE_CHECK=1  the server doesn't ask GitHub once a day for a newer release
`

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	cmd := "learn"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "learn":
		err = withServer(func() error { return learn(args) })
	case "quiz":
		err = withServer(func() error { return quiz(args) })
	case "units":
		err = withServer(units)
	case "course", "courses":
		err = withServer(func() error { return course(args) })
	case "gen":
		err = gen(args)
	case "add":
		err = add(args)
	case "retired":
		err = withServer(func() error { return retiredCmd(args) })
	case "restore":
		err = withServer(func() error { return restore(args) })
	case "more":
		err = withServer(func() error { return more(args) })
	case "stats":
		err = withServer(stats)
	case "ensure":
		err = ensure()
	case "restart":
		if err = restartServer(); err == nil {
			ok("server restarted on " + serverURL())
		}
	case "setup":
		err = setup(args)
	case "update":
		err = update()
	case "claude":
		err = launchClaude(args)
	case "doctor":
		err = doctor()
	case "uninstall":
		err = uninstall(args)
	case "status":
		status()
	case "signal":
		signal(args)
	case "version", "-v", "--version":
		fmt.Println("tauko", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("tauko: %v", err)
	}
}

func serve(args []string) error {
	home, _ := os.UserHomeDir()
	fl := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fl.String("addr", "127.0.0.1:8787", "listen address (use 0.0.0.0:8787 + TAUKO_TOKEN for phone access)")
	dbPath := fl.String("db", filepath.Join(home, ".tauko", "tauko.db"), "SQLite database path")
	newPerDay := fl.Int("new-per-day", 20, "new cards per day")
	coursesDir := fl.String("courses", "", "also load courses from this directory (for developing courses)")
	userDir := fl.String("user-courses", filepath.Join(home, ".tauko", "courses"), "your courses (tauko gen and tauko add install here)")
	fl.Parse(args)

	// Courses live in ~/.tauko/courses (tauko gen / tauko add put them there); -courses
	// adds a development folder in front of them.
	var base []content.Source
	if *coursesDir != "" {
		base = []content.Source{{FS: os.DirFS(*coursesDir), Root: "."}}
	}
	// A broken course of the user's is skipped (and reported), never fatal.
	load := func() (*content.Library, []string, error) {
		lib, skipped, err := content.LoadTolerant(base, content.Source{FS: os.DirFS(*userDir), Root: "."})
		var warnings []string
		for dir, err := range skipped {
			w := fmt.Sprintf("skipped course %s: %v", filepath.Join(*userDir, dir), err)
			log.Printf("tauko: %s", w)
			warnings = append(warnings, w)
		}
		return lib, warnings, err
	}
	lib, warnings, err := load()
	if err != nil {
		return fmt.Errorf("load courses: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	token := os.Getenv("TAUKO_TOKEN")
	cfg := server.Config{NewPerDay: *newPerDay, Token: token, Warnings: warnings, Version: version}
	if os.Getenv("TAUKO_NO_UPDATE_CHECK") == "" {
		cfg.Latest = latestRelease
	}
	if llm.Available() {
		cfg.Explain = llm.Claude("")
		cfg.Gen = func(ctx context.Context, topic, level string, progress func(string)) (string, error) {
			return runGen(ctx, generator(llm.Claude("")), genOptions{Topic: topic, Level: level, Units: 3, Cards: 12,
				Lang: "English", Yes: true, Dir: *userDir, Progress: progress})
		}
	}
	// The pane and the browser add courses through the server, which then reloads them.
	cfg.Add = func(ctx context.Context, id string) error {
		return addFromGallery(id, envOr("TAUKO_GALLERY_ARCHIVE", galleryArchive), *userDir, nil)
	}
	cfg.Gallery = func(ctx context.Context) ([]server.GalleryEntry, error) {
		var entries []server.GalleryEntry
		err := getJSON(envOr("TAUKO_GALLERY_INDEX", galleryIndex), &entries)
		return entries, err
	}
	cfg.Reload = load
	srv := server.New(lib, st, cfg)
	if token == "" && !isLoopback(*addr) {
		return errors.New("refusing to listen on a non-loopback address without TAUKO_TOKEN")
	}
	log.Printf("tauko: %d courses, %d units, %d cards, db %s", len(lib.Courses), len(lib.Units), len(lib.Cards), *dbPath)
	log.Printf("tauko: listening on http://%s", *addr)
	pidFile := filepath.Join(filepath.Dir(*dbPath), "serve.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		log.Printf("tauko: pid file: %v", err)
	}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(web.Files), ReadHeaderTimeout: 10 * time.Second}
	return hs.ListenAndServe()
}

// withServer starts the local server first unless TAUKO_URL points somewhere else.
func withServer(run func() error) error {
	if os.Getenv("TAUKO_URL") == "" {
		if err := ensure(); err != nil {
			return err
		}
	}
	return run()
}

func isLoopback(addr string) bool {
	for _, p := range []string{"127.0.0.1:", "localhost:", "[::1]:"} {
		if len(addr) >= len(p) && addr[:len(p)] == p {
			return true
		}
	}
	return false
}
