# tauko

*tauko* (fi): a pause, a break.

Learn anything in the 30-second gaps while Claude Code works, instead of scrolling reels.
When you press enter, a card is waiting next to the chat. When Claude finishes, tauko tells you
to go back.

- **Any subject:** a course is a folder of JSON. It ships with **Finnish B1** (8 units, 504 cards,
  following the *Suomen mestari 3* progression with original content)
- **Spaced repetition** with FSRS: due cards first, then 20 new per day
- **Two modes:** *tap* (recall, reveal, self-grade) and *type* (auto-checked, tolerant of missing
  diacritics and typos)
- **Self-quizzes:** 10 typed questions per unit or course, scored
- **Everywhere:** a pane inside Claude Code, the terminal, the browser and the phone, all
  sharing one state
- **Claude Code aware:** hooks report busy/idle, start the server on session start, and show a
  status line

See [docs/DESIGN.md](docs/DESIGN.md) for the why, the content model and the roadmap.

## Install (one time)

```sh
make install                                                   # tauko binary → ~/go/bin
claude plugin marketplace add ~/go/src/github.com/personal/tauko
claude plugin install tauko@tauko --scope user                 # the pane, in every session
```

Then merge [`hooks/claude-settings.json`](hooks/claude-settings.json) into `~/.claude/settings.json`:

| Hook | Runs | Effect |
|---|---|---|
| `SessionStart` | `tauko ensure` | starts the server in the background if it isn't running |
| `UserPromptSubmit` | `tauko signal busy` | clients show "Claude is working, your turn" |
| `Stop`, `Notification` | `tauko signal idle` | clients show "✓ Claude finished" |
| `statusLine` | `tauko status` | `📖 14 to do · 6 today · 🔥3` |

The hooks are fast and never fail, so they won't slow Claude down.

## Use

**In Claude Code:** press enter on a prompt and the **tauko** pane opens with the cursor in the
answer field. Type the answer and press `enter` to check it, then `enter` or `1–4` to grade. `m`
switches between tap and type, `Esc` goes back to the prompt, and `/tauko` opens the pane by hand.
After editing the plugin, run `/reload-plugins`.

**Terminal:**

```sh
tauko                          # learn, type mode (default)
tauko learn -mode tap          # recall + self-grade
tauko learn -course fi-b1      # one course (or -unit b1-03)
tauko quiz -unit b1-03         # 10-question self-quiz
tauko units                    # courses and progress
tauko stats
```

**Browser:** http://127.0.0.1:8787. Keys: `space` reveals, `1–4` grade, `enter` accepts the
suggested grade.

**Phone:** the server only listens on localhost unless you set a token:

```sh
TAUKO_TOKEN=$(openssl rand -hex 16) tauko serve -addr 0.0.0.0:8787
```

Reach it over [Tailscale](https://tailscale.com) (free) at `http://<mac-tailscale-ip>:8787/?token=<token>`
and use **Add to Home Screen**. Put it where Instagram used to be.

## Adding a course

```
content/courses/<course>/
  course.json
  decks/01-<unit>.json, 02-<unit>.json, …    one file per unit, in order
```

`course.json`:

```jsonc
{
  "id": "go-concurrency",            // unique, permanent
  "title": "Go concurrency",
  "subject": "programming",
  "description": "…",
  "labels": { "qa": "Answer" },       // optional prompt labels per card kind
  "focus_label": "Concept",           // what a unit's focus note is called
  "chars": []                         // extra keys for phone keyboards, e.g. ["ä", "ö"]
}
```

A unit (`decks/*.json`):

```jsonc
{
  "id": "go-01", "title": "Channels", "subtitle": "…", "level": "",
  "focus": "Nil and closed channels", "focus_note": "Short explanation shown on demand.",
  "vocab": [                          // term ↔ meaning: two cards each (recognize, produce)
    { "id": "go-01-v01", "term": "select", "meanings": ["wait on several channels"],
      "example": "…", "example_translation": "…" }
  ],
  "drills": [                         // cloze | transform | qa
    { "id": "go-01-q01", "type": "qa", "prompt": "What does a send on a nil channel do?",
      "answers": ["blocks forever"], "note": "…" },
    { "id": "go-01-q02", "type": "qa", "prompt": "When is a buffered channel the right call?",
      "answers": ["reference answer"], "self_grade": true }   // free-form: learner grades
  ]
}
```

**IDs are permanent and unique across all courses:** review history is keyed by them, so fix
a card's text freely but never reuse an ID for something else. `make test` validates every shipped
course. `make dev` serves courses from disk, so edits apply on restart without rebuilding.

## Layout

```
cmd/tauko          CLI: serve, ensure, learn, quiz, units, stats, status, signal
internal/content   course/unit loading, card expansion, new-card ordering
internal/check     typed-answer grading (diacritic/typo tolerance)
internal/store     SQLite + FSRS scheduling, review log, quiz results
internal/server    HTTP API, card selection, busy signal, token auth
web/               PWA (vanilla JS, embedded)
claude-mod/        Claude Code plugin: the pane (claude plugin test claude-mod)
content/courses/   courses
hooks/             Claude Code settings snippet
```
