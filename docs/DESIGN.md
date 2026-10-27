# tauko — design

> *tauko* (fi): a pause, a break.

## Problem

AI-assisted work produces dozens of 30 s – 3 min idle gaps a day: you press enter, Claude
works, you reach for your phone and scroll. Those gaps add up to an hour or more of
attention that leaves nothing behind. tauko fills those gaps with **active recall** instead of
passive scrolling.

tauko is subject-agnostic: a **course** is a folder of content, and the engine, scheduler and
clients know nothing about any one subject. The project started with a Finnish course because that's what its
author is studying. The next ones could be anything: another language, a certification, a
codebase.

## Principles

1. **Triggered, not remembered.** The prompt comes to you when the gap opens (Claude Code
   `UserPromptSubmit` hook), and tells you when the gap closes (`Stop` hook). Nothing to
   decide, nothing to open.
2. **Micro-units.** One card is 5–20 s of work. You can leave mid-session with zero penalty.
   No lessons, no "finish the unit".
3. **Retrieval over exposure.** Every card asks you to produce or recall something. Reading
   alone doesn't count.
4. **One brain, many surfaces.** A single scheduler state (FSRS) behind an HTTP API. Terminal,
   browser, and phone are thin clients.
5. **Cheaper than reels.** On the phone it is one thumb gesture per item, from a home-screen icon.

## Modes

| Mode | How it works | Grading |
|---|---|---|
| **tap** | See the prompt, recall in your head, reveal the answer | You grade: 1 Again · 2 Hard · 3 Good · 4 Easy |
| **type** | Type the answer | Auto-checked: exact → Good, near miss (missing diacritics / 1 typo) → Hard, wrong → Again; you can override. Free-form `self_grade` cards show your answer beside the reference and you grade it. |
| **quiz** | 10 drills from one unit, typed, scored at the end | Each answer also feeds the scheduler |

## Content model

```
course   a subject: id, title, labels for its prompts, special characters (ä, ö…)
└ unit   a topic, optionally with a focus point (a grammar rule, a concept) and a note on it
  └ card one retrieval: prompt → accepted answers
```

Card kinds:

| Kind | Source | Example |
|---|---|---|
| `recognize` | each `vocab` item | el andén → *platform* |
| `produce` | each `vocab` item | platform → *el andén* |
| `cloze` | drill | NodePort Services get a port in 30000-___. → *32767* |
| `transform` | drill | Rewrite in the passive: … → *…* |
| `qa` | drill | What does a send on a nil channel do? → *blocks forever* |
| `lesson` | `lessons` item | 40-80 words teaching one idea; read once, then its cards follow |

**Lessons and explanations.** Flashcards keep what you've met; they don't introduce it. So a unit can
open each idea with a micro-lesson (under 40 seconds, sized for the gap), then a quick check, then
the spaced cards: worked example first, retrieval after. Lessons are read once, never scheduled.
For depth on demand, **Explain** asks Claude (lean `claude -p`, cached per card and answer) why the
answer is what it is for the answer the learner actually gave.

`vocab` suits anything with term ↔ meaning pairs (languages, jargon, acronyms). `qa` suits
everything else. A drill with `self_grade: true` has no checkable answer (explain, compare,
design), so the learner grades it and quizzes skip it.

Courses live in `~/.tauko/courses/<course>/`; nothing is built into the binary. They get there
three ways: `tauko gen` (Claude drafts, a second pass verifies, tauko validates), `tauko add`
(the public gallery in `examples/courses`), or by hand. Card IDs are stable and unique across
all courses, so review history survives content edits.

## Architecture

```
            ┌──────────────── tauko serve (Go, single binary) ────────────────┐
            │  HTTP API  ──  scheduler (FSRS v5)  ──  SQLite (~/.tauko/tauko.db) │
            │  courses (~/.tauko/courses/), embedded PWA (web/)              │
            └───────▲───────────────▲───────────────▲───────────────▲──────────┘
                    │               │               │               │
         tauko learn / quiz     browser PWA      phone PWA     tauko signal busy|idle
         (terminal pane)        (desktop)      (home screen)   (Claude Code hooks)
```

- **Scheduler:** FSRS (`go-fsrs/v3`), 90 % target retention, short-term learning steps on.
- **Card selection:** due cards first (oldest due first), then new cards up to a daily
  new-card limit (default 20), in unit order.
- **Answer checking** (`internal/check`): case/punctuation/whitespace-insensitive. Missing
  accents (é, ñ, ä, ø, …) or a single typo is a near miss: shown as correct-ish and graded *Hard*.
- **Busy signal:** hooks POST to `/api/signal`. Clients show "Claude is working, your
  turn" or "✓ Claude finished, back to work".
- **Auth:** none on localhost. If `TAUKO_TOKEN` is set, every API call needs it
  (`Authorization: Bearer …` or `?token=` once in the PWA URL). Required when exposing the
  server, e.g. over Tailscale to the phone.

### Data model

```
card_state   card_id PK, due, stability, difficulty, elapsed_days, scheduled_days,
             reps, lapses, state, last_review           -- FSRS card, one row per seen card
review_log   id, card_id, rating, mode, answer, verdict, reviewed_at
quiz_result  id, unit, score, total, taken_at
kv           key PK, value                               -- busy signal etc.
```

Content is not in the DB. A card without a `card_state` row is *new*.

## Roadmap

**v0.1:** course-agnostic engine, FSRS, first course (8 units), terminal client
(tap/type/quiz), PWA, Claude Code pane plugin, hooks, status line, auto-start.

**v0.2: reach**
- Run the server on the VPS (or over Tailscale) so the phone always works
- Telegram bot as an extra push surface ("Claude has been working for 2 min, here's a card")

**v0.3: content engine** (done: `tauko gen`, `tauko add`, gallery)
- Course generator: "make me a course on X" → Claude drafts units → review → publish

**v0.4: teaching, not only testing** (done locally)
- Lesson cards: learn → quick check → spaced cards; `tauko gen` writes them
- Explain: Claude explains the learner's own answer, from any client
- Next: match material to the wait (lessons on long Claude turns, reviews on short ones)
- *Diff quizzes*: a course generated live from the code Claude just wrote (fights deskilling)
- Import from Anki decks / CSV
- Listening cards (TTS) and dictation for language courses
- More gallery courses: languages, certifications, popular tools

**v0.5: courses without leaving Claude Code** (done)
- The pane makes a course about any topic, or installs one from the gallery; the server does it in
  the background and reloads its courses in place, then the new course opens in the pane
- It's also what a new learner sees before any course is installed

**v0.5.2: staying up to date** (done)
- `GET /api/version`: the pane compares it with its own version and says when a Claude Code
  session still has the old pane, or the server is older than the pane
- `tauko ensure` replaces a server left running by another release, so an upgrade takes effect
  at the next session start
- A daily update check (GitHub's latest release, off with `TAUKO_NO_UPDATE_CHECK=1`); the pane and
  `tauko doctor` mention a newer release, and `tauko update` reruns the installer
- `tauko gen` and `tauko add` load the new course with `POST /api/reload` instead of restarting
  the server

**Product (if it works for me)**
- Multi-user accounts, sync, hosted
- Content packs: languages, certifications, "your own codebase"
- Integrations: Claude Code, Cursor, CI waits, build waits; anywhere there is a gap

## Success metric (personal)

More reviews per day in tauko than reels opened. Measured honestly: `tauko stats` vs Screen Time.
