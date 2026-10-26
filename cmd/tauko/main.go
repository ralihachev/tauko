// Command tauko is a spaced-repetition trainer for the gaps while your AI agent works.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	shipped "github.com/personal/tauko/content"
	"github.com/personal/tauko/internal/content"
	"github.com/personal/tauko/internal/server"
	"github.com/personal/tauko/internal/store"
	"github.com/personal/tauko/web"
)

const usage = `tauko: learn anything in the gaps while Claude works

usage:
  tauko serve [-addr 127.0.0.1:8787] [-db ~/.tauko/tauko.db] [-new-per-day 20] [-courses dir]
  tauko learn [-mode tap|type] [-course id] [-unit id]   review due + new cards (default command)
  tauko quiz  [-course id] [-unit id] [-n 10]            typed self-quiz, scored
  tauko units                                  courses, units and progress
  tauko stats                                  today's numbers
  tauko status                                 one line for the Claude Code status line
  tauko ensure                                 start the server in the background unless it runs
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
		err = learn(args)
	case "quiz":
		err = quiz(args)
	case "units":
		err = units()
	case "stats":
		err = stats()
	case "ensure":
		err = ensure()
	case "status":
		status()
	case "signal":
		signal(args)
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
	coursesDir := fl.String("courses", "", "load courses from this directory instead of the embedded ones (for editing content)")
	fl.Parse(args)

	var courseFS fs.FS = shipped.Courses
	dir := "courses"
	if *coursesDir != "" {
		courseFS, dir = os.DirFS(*coursesDir), "."
	}
	lib, err := content.Load(courseFS, dir)
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
	srv := server.New(lib, st, server.Config{NewPerDay: *newPerDay, Token: token})
	if token == "" && !isLoopback(*addr) {
		return errors.New("refusing to listen on a non-loopback address without TAUKO_TOKEN")
	}
	log.Printf("tauko: %d courses, %d units, %d cards, db %s", len(lib.Courses), len(lib.Units), len(lib.Cards), *dbPath)
	log.Printf("tauko: listening on http://%s", *addr)
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(web.Files), ReadHeaderTimeout: 10 * time.Second}
	return hs.ListenAndServe()
}

func isLoopback(addr string) bool {
	for _, p := range []string{"127.0.0.1:", "localhost:", "[::1]:"} {
		if len(addr) >= len(p) && addr[:len(p)] == p {
			return true
		}
	}
	return false
}
