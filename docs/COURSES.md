# Writing a course

A course is a folder of JSON files. No code changes are needed: drop the folder in, and it
shows up in every client.

```
content/courses/<course>/
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
  "focus_note": "Short explanation, shown on demand.",
  "vocab": [
    // term ↔ meaning: each item becomes two cards (recognize and produce)
    { "id": "go-01-v01", "term": "select", "meanings": ["wait on several channels"],
      "example": "select { case v := <-in: … }", "example_translation": "" }
  ],
  "drills": [
    // type: cloze (one ___ in the prompt) | transform | qa
    { "id": "go-01-q01", "type": "qa", "prompt": "What does a send on a nil channel do?",
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

Typed answers ignore case, punctuation and extra spaces. A missing diacritic (ä → a) or one
typo counts as *almost* and is graded Hard.

## Rules

- **IDs are permanent and unique across all courses.** Review history is keyed by them: fix a
  card's text freely, but never reuse an ID for a different card.
- **List every correct answer.** `answers[0]` is shown as the canonical one; add alternatives
  (other word orders, variant spellings) so typed answers are judged fairly.
- **Make cloze hints unambiguous.** `"hint": "pitkä → comparative"` leaves exactly one right answer.
- **Original content only.** Don't paste text or exercises from books or paid courses; follow their
  topics and level, write your own sentences.

## Testing a course

```sh
make test                                          # validates every shipped course
tauko serve -courses content/courses -db /tmp/dev.db   # serve from disk; restart to reload
```
