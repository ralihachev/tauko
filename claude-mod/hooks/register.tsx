import { atom, read, update } from 'claude-code'
import type { Color, EngineInterface, Register, RenderChildren } from 'claude-code'

import type { Card, Check, CourseInfo, Mode, RetiredRef, View } from '../types'

// The tauko server (`tauko serve`) holds the cards and the schedule; this pane is
// one more client of it, like the terminal and the PWA. The plugin also starts the
// server, reports busy/idle and keeps the status line, so installing it is all it takes.
const BASE = 'http://127.0.0.1:8787'
const PANE = 'tauko'
export const INSTALL = 'curl -fsSL https://raw.githubusercontent.com/ralihachev/tauko/main/install.sh | sh'

const EMPTY: View = {
  phase: 'loading',
  card: null,
  isNew: false,
  isPractice: false,
  noCourses: false,
  explanation: '',
  isExplaining: false,
  scope: '',
  focus: '',
  courseTitle: '',
  due: 0,
  newLeft: 0,
  check: null,
  answer: '',
  message: '',
}

// Change the tag when View's fields change: it declines what an earlier version stored.
const view = atom({ plugin: 'tauko', key: 'view' } as const, EMPTY, { shape: 'view-5' })
const isBusy = atom({ plugin: 'tauko', key: 'isBusy' } as const, false)
const mode = atom({ plugin: 'tauko', key: 'mode' } as const, 'type' as Mode)
const lastId = atom({ plugin: 'tauko', key: 'lastId' } as const, '')
const reviewed = atom({ plugin: 'tauko', key: 'reviewed' } as const, 0)
const isInstalled = atom({ plugin: 'tauko', key: 'isInstalled' } as const, true)
const isPicking = atom({ plugin: 'tauko', key: 'isPicking' } as const, false)
const courses = atom({ plugin: 'tauko', key: 'courses' } as const, [] as CourseInfo[], { shape: 'courses-1' })
// The card just marked "I know this", so the next card can offer to undo it.
const lastRetired = atom({ plugin: 'tauko', key: 'lastRetired' } as const, null as RetiredRef | null, { shape: 'retired-1' })

type NextResponse = {
  card: Card | null
  is_new: boolean
  practice?: boolean
  no_courses?: boolean
  scope?: string
  due: number
  new_left: number
  next_due?: string
  focus?: string
  course_title?: string
}

async function api<T>($: EngineInterface, method: string, path: string, body?: unknown): Promise<T> {
  const res = await $.http.fetch(BASE + path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    throw new Error(`${res.status} ${res.text.trim()}`)
  }
  return JSON.parse(res.text) as T
}

async function loadNext($: EngineInterface): Promise<void> {
  const exclude = await read($, lastId)
  try {
    const next = await api<NextResponse>($, 'GET', '/api/next?exclude=' + encodeURIComponent(exclude))
    if (!next.card) {
      const when = next.next_due ? relTime(next.next_due) : ''
      await update($, view, () => ({ ...EMPTY, phase: 'empty' as const, message: when, noCourses: next.no_courses ?? false }))
      return
    }
    const card = next.card
    await update($, view, () => ({
      ...EMPTY,
      phase: 'ask' as const,
      card,
      isNew: next.is_new,
      isPractice: next.practice ?? false,
      scope: next.scope ?? '',
      focus: next.focus ?? '',
      courseTitle: next.course_title ?? '',
      due: next.due,
      newLeft: next.new_left,
    }))
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
  }
}

async function submit($: EngineInterface, answer: string): Promise<void> {
  const v = await read($, view)
  if (!v.card || v.phase !== 'ask') {
    return
  }
  const card = v.card
  if (card.self_grade) {
    // Free-form answer: show it beside the reference and let the learner grade.
    await update($, view, cur => ({ ...cur, phase: 'revealed' as const, check: null, answer }))
    return
  }
  try {
    const check = await api<Check>($, 'POST', '/api/check', { card_id: card.id, answer })
    await update($, view, cur => ({ ...cur, phase: 'revealed' as const, check, answer }))
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
  }
}

async function reveal($: EngineInterface): Promise<void> {
  await update($, view, cur => (cur.phase === 'ask' ? { ...cur, phase: 'revealed' as const } : cur))
}

// markRead records that a lesson was read and moves on to its quick check.
async function markRead($: EngineInterface): Promise<void> {
  const v = await read($, view)
  if (!v.card || v.card.kind !== 'lesson') {
    return
  }
  const card = v.card
  try {
    await api($, 'POST', '/api/review', { card_id: card.id, rating: 3, mode: 'lesson' })
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
    return
  }
  await update($, lastId, () => card.id)
  await update($, reviewed, n => n + 1)
  await update($, lastRetired, () => null)
  await loadNext($)
  void refreshStatus($)
}

// explain asks Claude why the answer is what it is, for what you typed (about 4 s;
// instant when asked before).
async function explain($: EngineInterface): Promise<void> {
  const v = await read($, view)
  if (!v.card || v.isExplaining) {
    return
  }
  const card = v.card
  await update($, view, cur => ({ ...cur, isExplaining: true, explanation: '' }))
  let text: string
  try {
    text = (await api<{ text: string }>($, 'POST', '/api/explain', { card_id: card.id, answer: v.answer })).text
  } catch (err) {
    text = String(err).includes('503') ? 'Explain needs Claude Code (the claude command) installed.' : "Couldn't get an explanation right now."
  }
  await update($, view, cur => (cur.card?.id === card.id ? { ...cur, isExplaining: false, explanation: text } : cur))
}

// retire marks the card "I know this": it won't come back unless you undo.
async function retire($: EngineInterface): Promise<void> {
  const v = await read($, view)
  if (!v.card || v.phase !== 'revealed') {
    return
  }
  const card = v.card
  try {
    await api($, 'POST', '/api/retire', { card_id: card.id })
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
    return
  }
  await update($, lastRetired, () => ({ id: card.id, prompt: card.prompt }))
  await update($, lastId, () => card.id)
  await loadNext($)
  void refreshStatus($)
}

async function undoRetire($: EngineInterface): Promise<void> {
  const r = await read($, lastRetired)
  if (!r) {
    return
  }
  try {
    await api($, 'POST', '/api/unretire', { card_id: r.id })
  } catch (err) {
    $.ui.log('tauko: ' + String(err))
  }
  await update($, lastRetired, () => null)
  await update($, lastId, () => '')
  void refreshStatus($)
}

async function grade($: EngineInterface, rating: number): Promise<void> {
  const v = await read($, view)
  if (!v.card || v.phase !== 'revealed') {
    return
  }
  const card = v.card
  try {
    await api($, 'POST', '/api/review', {
      card_id: card.id,
      rating,
      mode: (await read($, mode)) + ':claude-pane',
      answer: v.answer,
      verdict: v.check ? v.check.verdict : '',
    })
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
    return
  }
  await update($, lastId, () => card.id)
  await update($, reviewed, n => n + 1)
  await update($, lastRetired, () => null)
  await loadNext($)
  void refreshStatus($)
}

// Where install.sh, `go install` and Homebrew put the binary; PATH first.
async function candidates($: EngineInterface): Promise<string[]> {
  const home = await $.env.get('HOME').catch(() => undefined)
  const out = ['tauko']
  if (home) {
    out.push(home + '/.local/bin/tauko', home + '/go/bin/tauko')
  }
  out.push('/opt/homebrew/bin/tauko', '/usr/local/bin/tauko')
  return out
}

// ensureServer starts the server unless it already answers (`tauko ensure` returns at
// once then). It reports whether a tauko binary was found at all.
async function ensureServer($: EngineInterface): Promise<boolean> {
  for (const bin of await candidates($)) {
    try {
      const run = await $.process.run([bin, 'ensure'], { timeoutMs: 8000 })
      if (run.exitCode === 0) {
        return true
      }
      $.ui.log('tauko ensure: ' + run.stderr.trim())
      return true
    } catch {
      // Not at this path; try the next one.
    }
  }
  return false
}

// signal tells other clients (browser, phone) whether Claude is busy. Best effort.
async function signal($: EngineInterface, state: 'busy' | 'idle'): Promise<void> {
  try {
    await api($, 'POST', '/api/signal', { state })
  } catch {
    // Server down: the pane says so when it opens.
  }
}

type StatsResponse = { due: number; new_left: number; stats: { reviews_today: number; streak_days: number } }

async function refreshStatus($: EngineInterface): Promise<void> {
  try {
    const s = await api<StatsResponse>($, 'GET', '/api/stats')
    const streak = s.stats.streak_days > 0 ? ` · 🔥${s.stats.streak_days}` : ''
    $.ui.status(`📖 ${s.due + s.new_left} to do · ${s.stats.reviews_today} today${streak}`)
  } catch {
    $.ui.status((await read($, isInstalled)) ? 'tauko: server off' : 'tauko: not installed')
  }
}

// The course list: pick one to study, or all of them.
async function openPicker($: EngineInterface): Promise<void> {
  try {
    const list = await api<CourseInfo[]>($, 'GET', '/api/courses')
    await update($, courses, () => list)
    await update($, isPicking, () => true)
  } catch (err) {
    await update($, view, () => ({ ...EMPTY, phase: 'offline' as const, message: String(err) }))
  }
}

async function chooseCourse($: EngineInterface, id: string): Promise<void> {
  try {
    await api($, 'POST', '/api/settings', { active_course: id })
  } catch (err) {
    $.ui.log('tauko: ' + String(err))
  }
  await update($, isPicking, () => false)
  await update($, lastId, () => '')
  await loadNext($)
  void refreshStatus($)
}

// moreNew raises today's new-card limit by 10 and moves on to a new card.
async function moreNew($: EngineInterface): Promise<void> {
  try {
    await api($, 'POST', '/api/more', { n: 10 })
  } catch (err) {
    $.ui.log('tauko: ' + String(err))
  }
  await loadNext($)
  void refreshStatus($)
}

function relTime(iso: string): string {
  const min = Math.round((Date.parse(iso) - Date.now()) / 60000)
  if (min < 1) return 'in a moment'
  if (min < 60) return `in ${min} min`
  const h = Math.round(min / 60)
  return h < 24 ? `in ${h} h` : `in ${Math.round(h / 24)} d`
}

async function openPane($: EngineInterface, focus: boolean): Promise<void> {
  const v = await read($, view)
  if (v.phase === 'loading' || v.phase === 'offline' || v.phase === 'empty') {
    await loadNext($)
  }
  await $.ui.open(focus ? { id: PANE, title: 'tauko', focus: true } : { id: PANE, title: 'tauko' })
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'tauko',
      description: 'Open the tauko flashcard pane',
    })
    // Start the server in the background; the session doesn't wait for it.
    void (async () => {
      const found = await ensureServer($)
      await update($, isInstalled, () => found)
      await refreshStatus($)
    })().catch(err => $.ui.log('tauko: start-up failed: ' + String(err)))
    return next(e)
  })

  on('command.run', { command: 'tauko' }, async $ => {
    await openPane($, true)
    return { text: 'tauko pane opened. Type the answer, Enter to check, 1-4 to grade, Esc to go back.' }
  })

  // You pressed enter: Claude is busy, so the card comes to you.
  on('prompt.submit', async ($, e, next) => {
    const result = await next(e)
    // Never let the pane get in the way of the prompt itself.
    try {
      await update($, isBusy, () => true)
      void signal($, 'busy')
      await openPane($, true)
    } catch (err) {
      $.ui.log('tauko: ' + String(err))
    }
    return result
  })

  on('turn.complete', async ($, e, next) => {
    await update($, isBusy, () => false)
    void signal($, 'idle')
    void refreshStatus($)
    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const ui = $.ui.resolve(e)
    const { Box, Text, Button } = ui
    // Not every surface draws an Input; there the pane falls back to tap mode.
    const Input = 'Input' in ui ? ui.Input : undefined
    const v = await read($, view)
    const busy = await read($, isBusy)
    const m: Mode = Input ? await read($, mode) : 'tap'
    const count = await read($, reviewed)
    const installed = await read($, isInstalled)
    const picking = await read($, isPicking)
    const retiredRef = await read($, lastRetired)
    const typingNow = v.phase === 'ask' && v.card?.kind !== 'lesson' && m === 'type' && Boolean(Input)
    const scopeName = v.scope ? v.courseTitle || v.scope : 'All courses'
    const rule = '─'.repeat(Math.max(8, Math.min(60, e.props.bodyColumns - 6)))

    // Keys reach the pane only while it holds the focus; Claude Code hands the focus back
    // to the prompt when a turn ends or the person types there. Say so, and how to return.
    const focusKey = e.surface === 'terminal' ? 'ctrl+x tab' : 'click here'
    const status = (
      <Box flexDirection="column" marginBottom={1}>
        <Box justifyContent="space-between">
          {busy ? (
            <Text color="claude" bold>
              ● Claude is working · your turn
            </Text>
          ) : (
            <Text color="success" bold>
              ✓ Claude is done
            </Text>
          )}
          <Text dimColor>
            {scopeName} · {v.due} due · {v.newLeft} new · {count} ✓
          </Text>
        </Box>
        {v.isPractice && v.phase !== 'offline' ? (
          <Text color="suggestion">↻ Practice: nothing due, so you're reviewing what you're closest to forgetting</Text>
        ) : null}
        {retiredRef ? (
          <Box gap={1}>
            <Text dimColor>✓ Retired “{retiredRef.prompt}”</Text>
            <Button key="undo" hotkey={typingNow ? undefined : 'u'} plain dimColor label="undo" onPress={() => undoRetire($)} />
          </Box>
        ) : null}
        {e.props.isFocused ? null : (
          <Text color="warning" bold>
            ⌨ {focusKey} to answer
          </Text>
        )}
      </Box>
    )

    const footer = (keys: string) => (
      <Box marginTop={1}>
        <Text dimColor>{keys}</Text>
      </Box>
    )

    // Course switch, more new cards, answer mode. Letter hotkeys only when no answer field
    // is on screen, so typing an answer never triggers them; Tab reaches them either way.
    const controls = (hot: boolean) => (
      <Box marginTop={1} gap={2} flexWrap="wrap">
        <Button key="courses" hotkey={hot ? 'c' : undefined} plain dimColor label={hot ? 'courses' : 'courses (tab)'} onPress={() => openPicker($)} />
        <Button key="more" hotkey={hot ? 'n' : undefined} plain dimColor label="learn 10 more today" onPress={() => moreNew($)} />
        {Input ? (
          <Button
            key="mode"
            hotkey={hot ? 'm' : undefined}
            plain
            dimColor
            label={m === 'type' ? 'tap mode' : 'type mode'}
            onPress={async () => {
              await update($, mode, cur => (cur === 'type' ? 'tap' : 'type'))
            }}
          />
        ) : null}
      </Box>
    )

    // A framed card: everything about one item lives inside it.
    const frame = (color: Color, children: RenderChildren) => (
      <Box flexDirection="column" borderStyle="round" borderColor={color} paddingX={2} paddingY={1}>
        {children}
      </Box>
    )

    if (picking) {
      const list = await read($, courses)
      const options = [{ id: '', title: 'All courses', line: 'new cards rotate between courses' }].concat(
        list.map(c => ({ id: c.id, title: c.title, line: `${c.seen}/${c.total} seen · ${c.due} due` })),
      )
      return (
        <Box flexDirection="column">
          {status}
          {frame(
            'suggestion',
            <Box flexDirection="column" gap={1}>
              <Text color="suggestion" bold>
                CHOOSE A COURSE
              </Text>
              {options.slice(0, 10).map((o, i) => (
                <Button
                  key={'course:' + (o.id || 'all')}
                  hotkey={String(i)}
                  plain
                  variant={o.id === v.scope ? 'primary' : undefined}
                  autoFocus={o.id === v.scope ? true : undefined}
                  onPress={() => chooseCourse($, o.id)}
                >
                  <Text bold={o.id === v.scope}>
                    {o.id === v.scope ? '● ' : '  '}
                    {o.title}
                  </Text>
                  <Text dimColor> {o.line}</Text>
                </Button>
              ))}
            </Box>,
          )}
          <Box marginTop={1}>
            <Button key="close-courses" hotkey="b" plain dimColor label="back" onPress={() => update($, isPicking, () => false)} />
          </Box>
          {footer('0–9 choose · b back')}
        </Box>
      )
    }

    if (v.phase === 'offline') {
      return (
        <Box flexDirection="column">
          {status}
          {frame(
            'error',
            <Box flexDirection="column">
              <Text color="error" bold>
                Can't reach the tauko server
              </Text>
              {installed ? (
                <Text dimColor>It starts with Claude Code. By hand: tauko ensure (or tauko doctor)</Text>
              ) : (
                <Box flexDirection="column">
                  <Text>tauko isn't installed yet. In a terminal, run:</Text>
                  <Text color="suggestion">{INSTALL}</Text>
                </Box>
              )}
            </Box>,
          )}
          <Box marginTop={1}>
            <Button key="retry" hotkey="r" variant="primary" autoFocus label="r  Retry" onPress={() => loadNext($)} />
          </Box>
        </Box>
      )
    }

    if (v.phase === 'loading') {
      return (
        <Box flexDirection="column">
          {status}
          <Text dimColor>Loading…</Text>
        </Box>
      )
    }

    if (v.phase === 'empty' && v.noCourses) {
      return (
        <Box flexDirection="column">
          {status}
          {frame(
            'suggestion',
            <Box flexDirection="column" gap={1}>
              <Text color="suggestion" bold>
                NO COURSES YET
              </Text>
              <Text>Make one about anything, in a terminal:</Text>
              <Text color="success">  tauko gen "Kubernetes networking"</Text>
              <Text>or install one from the gallery:</Text>
              <Text color="success">  tauko add            (list)</Text>
              <Text color="success">  tauko add go-concurrency</Text>
            </Box>,
          )}
          <Box marginTop={1}>
            <Button key="refresh" hotkey="r" plain dimColor label="check again" onPress={() => loadNext($)} />
          </Box>
          {footer('r check again · esc back to Claude')}
        </Box>
      )
    }

    if (v.phase === 'empty' || !v.card) {
      return (
        <Box flexDirection="column">
          {status}
          {frame(
            'success',
            <Box flexDirection="column" alignItems="center">
              <Text color="success" bold size="title">
                ✓ All done
              </Text>
              <Text dimColor>Nothing to study in {scopeName} right now.</Text>
            </Box>,
          )}
          {controls(true)}
          {footer('n learn 10 more today · c another course')}
        </Box>
      )
    }

    const card = v.card
    const where = [v.courseTitle, card.unit_title].filter(Boolean).join(' · ')
    const question = (
      <Box flexDirection="column">
        <Box justifyContent="space-between">
          <Text dimColor wrap="truncate-end">
            {where}
          </Text>
          {v.isNew ? (
            <Text color="warning" bold>
              {' '}NEW
            </Text>
          ) : v.isPractice ? (
            <Text color="suggestion" bold>
              {' '}PRACTICE
            </Text>
          ) : null}
        </Box>
        {v.focus ? (
          <Text dimColor italic>
            {v.focus}
          </Text>
        ) : null}
        <Box marginTop={1} flexDirection="column">
          <Text color="suggestion" bold>
            {card.instruction.toUpperCase()}
          </Text>
          <Text bold size="title">
            {card.prompt}
          </Text>
          {card.hint ? (
            <Text dimColor italic>
              → {card.hint}
            </Text>
          ) : null}
        </Box>
      </Box>
    )

    // Claude's explanation, inside the card, once asked for.
    const explained = v.isExplaining ? (
      <Box marginTop={1}>
        <Text color="suggestion">Asking Claude…</Text>
      </Box>
    ) : v.explanation ? (
      <Box marginTop={1} flexDirection="column">
        <Text color="suggestion" bold>
          WHY
        </Text>
        <Text>{v.explanation}</Text>
      </Box>
    ) : null

    // A lesson: read it, then go straight to its quick check.
    if (v.phase === 'ask' && card.kind === 'lesson') {
      return (
        <Box flexDirection="column">
          {status}
          {frame(
            'suggestion',
            <Box flexDirection="column">
              <Box justifyContent="space-between">
                <Text dimColor wrap="truncate-end">
                  {where}
                </Text>
                <Text color="suggestion" bold>
                  {' '}LESSON
                </Text>
              </Box>
              <Box marginTop={1} flexDirection="column">
                <Text color="suggestion" bold>
                  {card.instruction.toUpperCase()}
                </Text>
                <Text bold size="title">
                  {card.prompt}
                </Text>
              </Box>
              <Box marginTop={1}>
                <Text>{card.body}</Text>
              </Box>
              {card.example ? (
                <Box marginTop={1} flexDirection="column">
                  <Text italic>“{card.example}”</Text>
                  {card.example_translation ? <Text dimColor italic>{card.example_translation}</Text> : null}
                </Box>
              ) : null}
              {explained}
            </Box>,
          )}
          <Box marginTop={1} gap={2}>
            <Button key="got-it" hotkey="g" variant="primary" autoFocus label="got it → quick check" onPress={() => markRead($)} />
            <Button key="explain" hotkey="e" plain dimColor label={v.isExplaining ? 'explaining…' : 'explain differently'} onPress={() => explain($)} />
          </Box>
          {controls(true)}
          {footer('enter got it · e explain differently · esc back to Claude')}
        </Box>
      )
    }

    if (v.phase === 'ask') {
      const typing = m === 'type' && Input
      return (
        <Box flexDirection="column">
          {status}
          {frame('suggestion', question)}
          <Box marginTop={1} flexDirection="column">
            {typing ? (
              <Input
                key={'answer:' + card.id}
                label="›"
                placeholder="type your answer"
                submitLabel={card.self_grade ? 'compare' : 'check'}
                autoFocus
                onSubmit={(value: string) => submit($, value)}
              />
            ) : (
              <Button key="reveal" hotkey="r" variant="primary" autoFocus label="r  Show answer" onPress={() => reveal($)} />
            )}
          </Box>
          {controls(!typing)}
          {footer(typing ? 'enter check · esc back to Claude' : 'enter reveal · c courses · n learn more · esc back')}
        </Box>
      )
    }

    const check = v.check
    const suggested = check ? check.suggested_rating : 3
    const tone = !check ? 'suggestion' : check.verdict === 'correct' ? 'success' : check.verdict === 'close' ? 'warning' : 'error'
    const verdict = check ? (
      <Box flexDirection="column">
        <Text color={tone} bold>
          {check.verdict === 'correct' ? '✓ Correct' : check.verdict === 'close' ? `≈ Almost · ${check.reason}` : '✗ Not quite'}
        </Text>
        {check.verdict !== 'correct' && v.answer ? (
          <Text dimColor>
            you: <Text strikethrough={check.verdict === 'wrong'}>{v.answer}</Text>
          </Text>
        ) : null}
      </Box>
    ) : v.answer ? (
      <Text dimColor>you: {v.answer}</Text>
    ) : null

    const grades = [
      { n: 1, label: 'Again', color: 'error' },
      { n: 2, label: 'Hard', color: 'warning' },
      { n: 3, label: 'Good', color: 'success' },
      { n: 4, label: 'Easy', color: 'suggestion' },
    ]

    return (
      <Box flexDirection="column">
        {status}
        {frame(
          tone,
          <Box flexDirection="column">
            {question}
            <Text dimColor>{rule}</Text>
            {verdict}
            <Box marginTop={verdict ? 1 : 0} flexDirection="column">
              <Text color="success" bold size="heading">
                {card.answers.join('  ·  ')}
              </Text>
              {card.translation ? <Text dimColor>{card.translation}</Text> : null}
            </Box>
            {card.note ? (
              <Box marginTop={1}>
                <Text>💡 {card.note}</Text>
              </Box>
            ) : null}
            {card.example ? (
              <Box marginTop={1} flexDirection="column">
                <Text italic>“{card.example}”</Text>
                {card.example_translation ? <Text dimColor italic>{card.example_translation}</Text> : null}
              </Box>
            ) : null}
            {explained}
          </Box>,
        )}
        <Box marginTop={1} gap={2}>
          <Button key="explain" hotkey="e" plain dimColor label={v.isExplaining ? 'explaining…' : 'explain this'} onPress={() => explain($)} />
          <Button key="retire" hotkey="k" plain dimColor label="I know this: don't show again" onPress={() => retire($)} />
        </Box>
        <Box marginTop={1} gap={1} flexWrap="wrap">
          {grades.map(g => (
            <Button
              key={'grade:' + g.n}
              hotkey={String(g.n)}
              variant={g.n === suggested ? 'primary' : undefined}
              autoFocus={g.n === suggested ? true : undefined}
              onPress={() => grade($, g.n)}
            >
              <Text color={g.color} bold={g.n === suggested}>
                {g.n} {g.label}
              </Text>
            </Button>
          ))}
        </Box>
        {controls(true)}
        {footer(`enter ${grades[suggested - 1]?.label ?? 'Good'} · 1–4 grade · e why · k know it · esc back`)}
      </Box>
    )
  })
}
