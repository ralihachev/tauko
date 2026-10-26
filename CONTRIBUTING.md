# Contributing to tauko

Thanks for helping! The most valuable contributions right now are **courses** and **bug reports
from real use**.

## Add a course

1. Read [docs/COURSES.md](docs/COURSES.md) for the format and the rules: permanent unique IDs,
   every correct answer listed, original content only.
2. Add `examples/courses/<your-course>/` with `course.json` and `decks/*.json` (the folder name
   must equal the course id), and list it in `examples/index.json`. `tauko gen -dir examples/courses`
   gives you a verified first draft to edit.
3. Run `make test`. It validates every course (JSON shape, unique IDs, one `___` per cloze, and so on).
4. Try it: `make dev`, then open http://127.0.0.1:8787.
5. Open a pull request saying who the course is for and its level.

Gallery courses are licensed CC BY-SA 4.0. By contributing, you agree to license your content that way.

## Fix a card

Spotted a wrong answer or an awkward sentence? Edit the card's text in its deck file and keep its `id`.
Review history is keyed by IDs, so a fixed card keeps everyone's progress.

## Work on the code

```sh
git clone https://github.com/ralihachev/tauko && cd tauko
make test          # Go tests, including content validation
make dev           # server on :8787 serving courses from disk
claude plugin test claude-mod        # tests for the Claude Code pane
make claude                          # Claude Code with your local pane, fullscreen
                                     # (run `tauko uninstall` first if the released plugin is installed, to avoid loading two copies of the pane)
```

| Path | What |
|---|---|
| `cmd/tauko` | the CLI: `serve`, `setup`, `doctor`, `learn`, `quiz`, … |
| `internal/content` | loading courses, expanding cards, new-card order |
| `internal/check` | grading typed answers |
| `internal/store` | SQLite + FSRS scheduling |
| `internal/server` | HTTP API and the busy/idle signal |
| `web/` | browser and phone app (plain JS, embedded in the binary) |
| `claude-mod/` | the Claude Code plugin (the pane) |

Keep changes small and focused, add a test for behaviour changes, and run `gofmt`.

## Releases

Maintainers tag a version (`git tag v0.2.0 && git push --tags`). GitHub Actions then builds the
binaries with GoReleaser, and `install.sh` picks up the new release automatically.
