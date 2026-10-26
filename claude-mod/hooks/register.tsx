import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Card, Check, Mode, View } from '../types'

// The tauko server (`tauko serve`) holds the cards and the schedule; this pane is
// one more client of it, like the terminal and the PWA.
const BASE = 'http://127.0.0.1:8787'
const PANE = 'tauko'

const EMPTY: View = {
  phase: 'loading',
  card: null,
  isNew: false,
  focus: '',
  courseTitle: '',
  due: 0,
  newLeft: 0,
  check: null,
  answer: '',
  message: '',
}

// Change the tag when View's fields change: it declines what an earlier version stored.
const view = atom({ plugin: 'tauko', key: 'view' } as const, EMPTY, { shape: 'view-2' })
const isBusy = atom({ plugin: 'tauko', key: 'isBusy' } as const, false)
const mode = atom({ plugin: 'tauko', key: 'mode' } as const, 'type' as Mode)
const lastId = atom({ plugin: 'tauko', key: 'lastId' } as const, '')
const reviewed = atom({ plugin: 'tauko', key: 'reviewed' } as const, 0)

type NextResponse = {
  card: Card | null
  is_new: boolean
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
      await update($, view, () => ({ ...EMPTY, phase: 'empty' as const, message: when }))
      return
    }
    const card = next.card
    await update($, view, () => ({
      ...EMPTY,
      phase: 'ask' as const,
      card,
      isNew: next.is_new,
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
  await loadNext($)
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
      await openPane($, true)
    } catch (err) {
      $.ui.log('tauko: ' + String(err))
    }
    return result
  })

  on('turn.complete', async ($, e, next) => {
    await update($, isBusy, () => false)
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

    const header = (
      <Box flexDirection="column" marginBottom={1}>
        {busy ? (
          <Text color="claude">● Claude is working, your turn</Text>
        ) : (
          <Text color="success" bold>
            ✓ Claude finished. Esc to go back, or keep going
          </Text>
        )}
        <Text dimColor>
          {v.due} due · {v.newLeft} new · {count} this session · {m} mode
        </Text>
      </Box>
    )

    const toggle = Input ? (
      <Button
        key="mode"
        hotkey="m"
        plain
        dimColor
        label={m === 'type' ? 'switch to tap' : 'switch to type'}
        onPress={async () => {
          await update($, mode, cur => (cur === 'type' ? 'tap' : 'type'))
        }}
      />
    ) : null

    if (v.phase === 'offline') {
      return (
        <Box flexDirection="column">
          {header}
          <Text color="error">tauko server not reachable</Text>
          <Text dimColor>Start it with: ~/go/bin/tauko serve</Text>
          <Button key="retry" hotkey="r" variant="primary" autoFocus label="Retry" onPress={() => loadNext($)} />
        </Box>
      )
    }

    if (v.phase === 'loading') {
      return (
        <Box flexDirection="column">
          {header}
          <Text dimColor>Loading…</Text>
        </Box>
      )
    }

    if (v.phase === 'empty' || !v.card) {
      return (
        <Box flexDirection="column">
          {header}
          <Text color="success" bold>
            ✓ All done!
          </Text>
          <Text dimColor>Nothing due right now{v.message ? `, next card ${v.message}` : ''}.</Text>
          <Button key="refresh" hotkey="r" label="Check again" onPress={() => loadNext($)} />
        </Box>
      )
    }

    const card = v.card
    const prompt = (
      <Box flexDirection="column" marginBottom={1}>
        <Text dimColor>
          {v.courseTitle ? `${v.courseTitle} · ` : ''}
          {card.unit_title}
          {v.focus ? ` · ${v.focus}` : ''}
          {v.isNew ? ' · new' : ''}
        </Text>
        <Text color="suggestion" bold>
          {card.instruction}
        </Text>
        <Text bold>{card.prompt}</Text>
        {card.hint ? <Text dimColor>({card.hint})</Text> : null}
      </Box>
    )

    if (v.phase === 'ask') {
      return (
        <Box flexDirection="column">
          {header}
          {prompt}
          {m === 'type' && Input ? (
            <Input
              key={'answer:' + card.id}
              placeholder="Your answer…"
              submitLabel={card.self_grade ? 'compare' : 'check'}
              autoFocus
              onSubmit={(value: string) => submit($, value)}
            />
          ) : (
            <Button key="reveal" hotkey="r" variant="primary" autoFocus label="Reveal" onPress={() => reveal($)} />
          )}
          {toggle}
        </Box>
      )
    }

    const check = v.check
    const suggested = check ? check.suggested_rating : 3
    const verdict = check ? (
      check.verdict === 'correct' ? (
        <Text color="success" bold>
          ✓ Correct
        </Text>
      ) : check.verdict === 'close' ? (
        <Text color="warning" bold>
          ≈ Almost ({check.reason}): {v.answer}
        </Text>
      ) : (
        <Text color="error" bold>
          ✗ Wrong{v.answer ? `: ${v.answer}` : ''}
        </Text>
      )
    ) : v.answer ? (
      <Text dimColor>Your answer: {v.answer}</Text>
    ) : null
    const grades = [
      { n: 1, label: 'Again' },
      { n: 2, label: 'Hard' },
      { n: 3, label: 'Good' },
      { n: 4, label: 'Easy' },
    ]

    return (
      <Box flexDirection="column">
        {header}
        {prompt}
        {verdict}
        <Text color="success" bold>
          {card.answers.join(' · ')}
        </Text>
        {card.translation ? <Text dimColor>{card.translation}</Text> : null}
        {card.note ? <Text>💡 {card.note}</Text> : null}
        {card.example ? (
          <Text dimColor>
            {card.example}
            {card.example_translation ? ` (${card.example_translation})` : ''}
          </Text>
        ) : null}
        <Box marginTop={1} gap={1}>
          {grades.map(g =>
            g.n === suggested ? (
              <Button
                key={'grade:' + g.n}
                hotkey={String(g.n)}
                variant="primary"
                autoFocus
                label={`${g.n} ${g.label}`}
                onPress={() => grade($, g.n)}
              />
            ) : (
              <Button key={'grade:' + g.n} hotkey={String(g.n)} label={`${g.n} ${g.label}`} onPress={() => grade($, g.n)} />
            ),
          )}
        </Box>
        <Text dimColor>Enter = {grades[suggested - 1]?.label ?? 'Good'} · 1–4 to grade · Esc to go back</Text>
      </Box>
    )
  })
}
