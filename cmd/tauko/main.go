// Command tauko is a spaced-repetition trainer for the gaps while your AI agent works.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	shipped "github.com/ralihachev/tauko/content"
	"github.com/ralihachev/tauko/internal/content"
	"github.com/ralihachev/tauko/internal/server"
	"github.com/ralihachev/tauko/internal/store"
	"github.com/ralihachev/tauko/web"
)

// version is set at release time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `tauko: learn anything in the gaps while Claude works

usage:
  tauko setup                                  install the Claude Code pane, start the server
  tauko doctor                                 check the setup and say how to fix it
  tauko uninstall [-purge]                     remove the pane and stop the server
  tauko serve [-addr 127.0.0.1:8787] [-db ~/.tauko/tauko.db] [-new-per-day 20] [-courses dir]
  tauko learn [-mode tap|type] [-course id] [-unit id]   review due + new cards (default command)
  tauko quiz  [-course id] [-unit id] [-n 10]            typed self-quiz, scored
  tauko units                                  courses, units and progress
  tauko course [id|all]                        show courses, or switch the one you study
  tauko more [n]                               n more new cards today (default 10)
  tauko stats                                  today's numbers
  tauko status                                 one line for the Claude Code status line
  tauko ensure                                 start the server in the background unless it runs
  tauko restart                                restart the server (after adding or editing a course)
  tauko signal busy|idle                       called from Claude Code hooks

env:
  TAUKO_URL    server URL for clients (default http://127.0.0.1:8787)
  TAUKO_TOKEN  shared secret; required by the server when set
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
	case "more":
		err = withServer(func() error { return more(args) })
	case "stats":
		err = withServer(stats)
	case "ensure":
		err = ensure()
	case "restart":
		stopServer()
		time.Sleep(300 * time.Millisecond)
		if err = ensure(); err == nil {
			ok("server restarted on " + serverURL())
		}
	case "setup":
		err = setup(args)
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
	coursesDir := fl.String("courses", "", "load courses from this directory instead of the built-in ones (for editing content)")
	userDir := fl.String("user-courses", filepath.Join(home, ".tauko", "courses"), "your own courses, loaded after the built-in ones (skipped if missing)")
	fl.Parse(args)

	base := []content.Source{{FS: shipped.Courses, Root: "courses"}}
	if *coursesDir != "" {
		base = []content.Source{{FS: os.DirFS(*coursesDir), Root: "."}}
	}
	// A broken course of the user's is skipped (and reported), never fatal.
	var warnings []string
	lib, skipped, err := content.LoadTolerant(base, content.Source{FS: os.DirFS(*userDir), Root: "."})
	if err != nil {
		return fmt.Errorf("load courses: %w", err)
	}
	for dir, err := range skipped {
		w := fmt.Sprintf("skipped course %s: %v", filepath.Join(*userDir, dir), err)
		log.Printf("tauko: %s", w)
		warnings = append(warnings, w)
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
	srv := server.New(lib, st, server.Config{NewPerDay: *newPerDay, Token: token, Warnings: warnings})
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
