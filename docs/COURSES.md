# Writing a course

A course is a folder of JSON files in `~/.tauko/courses/`. No code changes are needed: add the
folder, run `tauko restart`, and it shows up in every client. The quickest start is
`tauko gen "topic"`, which writes one for you; [examples/courses](../examples/courses) has
hand-checked ones to copy from.

```
~/.tauko/courses/<course>/          (or examples/courses/<course>/ for the gallery)
  course.json
  decks/01-<unit>.json, 02-<unit>.json, …    one file per unit, in order
```

## course.json

```jsonc
{
  "id": "go-concurrency",            // unique and permanent
  "title": "Go concurrency",
  "title_en": "",                     // optional English title for non-English courses
  "subject": "programming",
  "description": "Channels, select and the memory model, one gap at a time.",
  "labels": { "qa": "Answer" },       // optional prompt label per card kind
  "focus_label": "Concept",           // what a unit's focus note is called ("Grammar" etc.)
  "chars": []                         // extra keys for phone keyboards, e.g. ["ä", "ö"]
}
```

## A unit (`decks/*.json`)

```jsonc
{
  "id": "go-01",
  "title": "Channels",
  "subtitle": "Sending, receiving, closing",
  "level": "",
  "focus": "Nil and closed channels",
  "focus_note": "Short summary, shown on the course page.",
  "lessons": [
    // a micro-lesson, read once before the cards that point to it
    { "id": "go-01-l1", "title": "Closed channels",
      "body": "40-80 words that teach one idea, with a concrete example.",
      "example": "v, ok := <-ch", "example_translation": "" }
  ],
  "vocab": [
    // term ↔ meaning: each item becomes two cards (recognize and produce)
    { "id": "go-01-v01", "lesson": "go-01-l1", "term": "select", "meanings": ["wait on several channels"],
      "example": "select { case v := <-in: … }", "example_translation": "" }
  ],
  "drills": [
    // type: cloze (one ___ in the prompt) | transform | qa
    { "id": "go-01-q01", "lesson": "go-01-l1", "type": "qa", "prompt": "What does a send on a nil channel do?",
      "answers": ["blocks forever"], "note": "Receives block forever too." },
    // self_grade: free-form answers the learner compares and grades; quizzes skip these
    { "id": "go-01-q02", "type": "qa", "prompt": "When is a buffered channel the right call?",
      "answers": ["When producer and consumer run at different rhythms…"], "self_grade": true }
  ]
}
```

## Card kinds

| Kind | From | Shows | Checked how |
|---|---|---|---|
| `recognize` | each `vocab` item | the term | answer = any of `meanings`, lenient ("to apply" = "apply") |
| `produce` | each `vocab` item | the meanings | answer = the term |
| `cloze` | drill | a sentence with `___` | answer = the missing word(s) |
| `transform` | drill | an instruction + sentence | answer = the full rewritten sentence |
| `qa` | drill | any question | answer = any of `answers`, or self-graded |
| `lesson` | each `lessons` item | a 40-80 word explanation | not graded: read once ("got it"), never reviewed |

## Lessons

Cards are for remembering; lessons are for meeting an idea the first time. A lesson teaches **one
idea in 40-80 words** (under 40 seconds to read) with one concrete example. Cards point to the lesson
that teaches them with `"lesson": "<lesson id>"`, and the learner sees the lesson first, then its
cards, easiest first: the first card is a quick check. Cards without a lesson come after.

Lessons are read once and never come back for review. They stay on the course page for re-reading.
A course without lessons works as before; add them when learners may meet the topic for the
first time. `tauko gen` writes them for you.

After answering any card (or on a lesson), **Explain** (`e`) asks Claude why the answer is what it
is, for the answer the learner gave. It takes a few seconds, is cached per card and answer, and needs
Claude Code installed.

Typed answers ignore case, punctuation and extra spaces. A missing diacritic (ä → a) or one
typo counts as *almost* and is graded Hard.

## Rules

- **IDs are permanent and unique across all courses.** Review history is keyed by them: fix a
  card's text freely, but never reuse an ID for a different card.
- **List every correct answer.** `answers[0]` is shown as the canonical one; add alternatives
  (other word orders, variant spellings) so typed answers are judged fairly.
- **Make cloze hints unambiguous.** `"hint": "the upper bound, a number"` leaves exactly one right answer.
- **Original content only.** Don't paste text or exercises from books or paid courses; follow their
  topics and level, write your own sentences.

## Testing a course

```sh
tauko restart                                      # reload ~/.tauko/courses
make test                                          # validates every gallery course
tauko serve -courses examples/courses -db /tmp/dev.db  # serve the gallery from disk
```

A course with an error (bad JSON, a duplicate ID) is skipped and the rest keep working.
`tauko doctor` names the course and the problem.
