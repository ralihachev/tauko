<h1 align="center">tauko</h1>

<p align="center">
  <b>Turn AI wait time into skill.</b><br>
  Flashcards that appear beside Claude Code while it works, and get out of the way when it's done.
</p>

<p align="center">
  <a href="https://github.com/ralihachev/tauko/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/ralihachev/tauko"></a>
  <a href="https://github.com/ralihachev/tauko/actions/workflows/ci.yml"><img alt="ci" src="https://github.com/ralihachev/tauko/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="platforms" src="https://img.shields.io/badge/macOS%20%7C%20Linux-lightgrey">
</p>

![The tauko pane next to Claude Code](docs/img/pane.png)

You send Claude a prompt and wait. Thirty seconds, two minutes, fifty times a day. That's an hour
of dead time, and it usually goes to the phone.

**tauko** puts that time to work. When you press enter, a card opens next to the chat: a word, a
grammar drill, a question about Go channels, whatever you're learning. You answer it, maybe another
one, and when Claude finishes, tauko says so and you're back. Spaced repetition (FSRS) picks the
card you're about to forget, so those half-minutes turn into things you remember.

*tauko* is Finnish for "a pause".

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/ralihachev/tauko/main/install.sh | sh
```

Then start a new Claude Code session and send any prompt. That's all.

The installer:
1. Downloads a single ~5 MB binary for your OS and CPU and checks it against its checksum.
2. Puts it in `~/.local/bin` (no sudo).
3. Starts a small local server and installs the Claude Code plugin.

Nothing is sent anywhere: no account, no telemetry. Run `tauko doctor` any time to check the setup.

<details>
<summary>Other ways to install</summary>

```sh
go install github.com/ralihachev/tauko/cmd/tauko@latest && tauko setup   # Go 1.26+

git clone https://github.com/ralihachev/tauko && cd tauko && make install && tauko setup
```
</details>

## How it works

```
 you press enter ──▶ Claude starts working ──▶ a card opens beside the chat
                                                 │
                       answer · grade · next  ◀──┘   (as many as you like)
                                                 │
 back to Claude  ◀── "✓ Claude is done"  ◀───────┘
```

- **The card comes to you.** There's no app to open and nothing to remember. The wait itself is the trigger.
- **Recall, not reading.** You type the answer (or recall it and reveal). Typos and missing ä/ö count as "almost", not wrong.
- **One card is 5 to 20 seconds.** Leave mid-card when Claude is done; nothing is lost.
- **The schedule adapts.** Grade each card Again, Hard, Good or Easy, and FSRS spaces its next review accordingly: minutes for new cards, weeks for well-known ones.

## What the cards look like

A course mixes five kinds of cards:

| Kind | Example prompt | You answer |
|---|---|---|
| **recognize** | luotettava | reliable |
| **produce** | reliable / trustworthy | luotettava |
| **fill in** | Pekka on ___ kuin veljensä. *(pitkä → comparative)* | pidempi |
| **rewrite** | Rewrite in the passive past: Me pidimme kokouksen. | Kokous pidettiin. |
| **question** | What happens when you send on a closed channel? | it panics |

Open questions with no single right answer ("When is an unbuffered channel the better choice?") are
**self-graded**: you write your answer, see a reference answer next to it, and grade yourself.

After you answer, you see the verdict, every accepted answer, a translation or explanation, and an example:

```
Täydennä
  Pekka on ___ kuin veljensä.
  (pitkä → komparatiivi)
  > pitempi
  ✓ correct
  pidempi  |  pitempi
  Pekka is taller than his brother.
  💡 pitkä → pidempi (also pitempi)
  1 again · 2 hard · 3 good · 4 easy  [enter=3]
```

## Courses

| Course | Cards | Where |
|---|---|---|
| **Suomi B1** (Finnish B1): 8 units following the *Suomen mestari 3* topics and grammar, original content | 504 | built in |
| **Go concurrency**: channels, select, sync, context | 20 | [`examples/`](examples/courses/go-concurrency) |

### Try the example course

```sh
mkdir -p ~/.tauko/courses
curl -fsSL https://github.com/ralihachev/tauko/archive/refs/heads/main.tar.gz \
  | tar -xz -C ~/.tauko/courses --strip-components=3 tauko-main/examples/courses/go-concurrency
tauko restart
tauko learn -course go-concurrency
```

```
Channels and select · Nil and closed channels · new  [0 due · 20 new left]
Answer
  What happens when you send on a closed channel?
  > it panic
  ≈ almost (typo) → it panics
  it panics  |  panic  |  panics
  💡 Closing an already closed channel panics as well.
  1 again · 2 hard · 3 good · 4 easy  [enter=2]
```

### Write your own in 2 minutes

A course is a folder with two small JSON files. Make `~/.tauko/courses/kubernetes/course.json`:

```json
{ "id": "k8s", "title": "Kubernetes basics" }
```

…and `~/.tauko/courses/kubernetes/decks/01-pods.json`:

```json
{
  "id": "k8s-01",
  "title": "Pods",
  "drills": [
    { "id": "k8s-01-q1", "type": "qa", "prompt": "Which kubectl command shows a pod's events?",
      "answers": ["kubectl describe pod", "describe pod"] },
    { "id": "k8s-01-q2", "type": "cloze", "prompt": "A pod stuck in ___ can't pull its image.",
      "hint": "the status, CamelCase", "answers": ["ImagePullBackOff"] }
  ]
}
```

Run `tauko restart`, and it's in every client. The full format (vocabulary pairs, notes, examples,
special-character keys) is in **[docs/COURSES.md](docs/COURSES.md)**. If you write a good one, please send
a pull request.

## Everywhere you wait

**In Claude Code** (installed by the quick start):

| Key | Does |
|---|---|
| type + `enter` | check your answer |
| `enter` or `1`–`4` | grade it and move on |
| `m` | switch between typing and tap-to-reveal |
| `Esc` / `ctrl+x tab` | back to the prompt / back to the card |
| `/tauko` | open the pane by hand |

In Claude Code's fullscreen mode (`/config`), the pane docks beside the chat; otherwise it sits above
the prompt. The status line shows what's left: `📖 14 to do · 6 today · 🔥3`.

<img src="docs/img/web.png" alt="tauko in the browser" width="280" align="right">

**In the terminal:**

```sh
tauko                          # learn (type mode)
tauko learn -mode tap          # recall, then reveal
tauko learn -course fi-b1      # one course; -unit b1-03 for one unit
tauko quiz -unit b1-03         # 10 questions, scored
tauko units                    # courses and progress
tauko stats                    # today, streak, totals
```

**In the browser:** http://127.0.0.1:8787 shows the same cards, plus "Claude is working" and "Claude is done".

**On your phone:** let the server listen beyond your machine, with a token:

```sh
TAUKO_TOKEN=$(openssl rand -hex 16) tauko serve -addr 0.0.0.0:8787
```

Open `http://<your-computer>:8787/?token=<token>` (over [Tailscale](https://tailscale.com), for example) and
**Add to Home Screen**. Put it where Instagram used to be.

<br clear="right">

## FAQ

**Does it slow Claude down?**
Not noticeably. When you press enter, the plugin makes one call to the local server, which takes milliseconds. If the server is down, the pane says so and Claude carries on.

**Where is my data?**
In `~/.tauko/tauko.db` (SQLite) on your machine. tauko makes no network calls except the ones you
point it at.

**Can I use it without Claude Code?**
Yes. The terminal and browser work on their own, and `tauko setup` skips the plugin if Claude Code isn't installed.

**How is this different from Anki or Duolingo?**
Same science (spaced repetition), different trigger. Those apps need you to decide to study. tauko
uses the waits you already have, a few seconds at a time, so it doesn't compete for your evening.

**Windows?**
Not yet natively. It works in WSL.

**Something's off.**
Run `tauko doctor`: it checks the binary, server and plugin, and says how to fix each. The server log is `~/.tauko/serve.log`.

## Commands

| Command | Does |
|---|---|
| `tauko setup` | install the Claude Code plugin and start the server (safe to re-run) |
| `tauko doctor` | check everything and say how to fix it |
| `tauko learn` / `quiz` / `units` / `stats` | study and track progress in the terminal |
| `tauko restart` | restart the server after adding or editing a course |
| `tauko serve` | run the server in the foreground (`-addr`, `-db`, `-new-per-day`) |
| `tauko uninstall` | remove the plugin and stop the server; `-purge` also deletes your progress |

## Roadmap

- **Course generator:** `tauko gen "kubernetes networking"` drafts a course and double-checks every answer.
- **Diff quizzes:** questions about the code Claude just changed, to keep your own skills sharp.
- **Imports** from Anki and CSV.
- **More places to wait:** Cursor/VS Code, build and CI waits (`tauko run make test`).
- **Teams:** shared courses and small weekly leagues.

The thinking behind it is in [docs/DESIGN.md](docs/DESIGN.md).

## Contributing

Courses, bug reports and code are all welcome. See [CONTRIBUTING.md](CONTRIBUTING.md). If tauko
helps you, a ⭐ helps others find it.

## License

The code is [Apache-2.0](LICENSE). Course content is [CC BY-SA 4.0](content/LICENSE).
