import { expect, mock, test } from 'claude-code/testing'
import type { TestBody } from 'claude-code/testing'

const PANE = {
  plugin: 'tauko',
  component: 'Pane',
  requestId: 'tauko',
  props: {
    title: 'tauko',
    isFocused: true,
    bodyColumns: 50,
    placement: 'dock',
    scroll: { offset: 0, bodyRows: 30 },
    view: {},
  },
} as const

const CARD = {
  id: 'b1-01-g01',
  unit: 'b1-01',
  unit_title: 'Ihmiset ja ihmissuhteet',
  kind: 'cloze',
  instruction: 'Täydennä',
  prompt: 'Pekka on ___ kuin veljensä.',
  hint: 'pitkä → komparatiivi',
  answers: ['pidempi', 'pitempi'],
  note: 'pitkä → pidempi',
}

const json = (body: unknown) => ({ value: { status: 200, ok: true, headers: {}, text: JSON.stringify(body) } })

test('enter opens a card; typing, checking and grading reach the server', async ($, on) => {
  const reviews: unknown[] = []
  // The fake tauko server: one card, then nothing due.
  on('http.fetch', (_, e) => {
    const url = e.url
    if (url.includes('/api/next')) {
      return json(reviews.length === 0
        ? { card: CARD, is_new: true, due: 0, new_left: 20, focus: 'Komparatiivi', course_title: 'Suomi B1' }
        : { card: null, is_new: false, due: 0, new_left: 19 })
    }
    if (url.endsWith('/api/check')) {
      return json({ verdict: 'correct', expected: 'pidempi', suggested_rating: 3 })
    }
    if (url.endsWith('/api/review')) {
      reviews.push(JSON.parse(e.init?.body ?? '{}'))
      return json({ next_due: '2026-10-27T10:00:00Z' })
    }
    return { value: { status: 404, ok: false, headers: {}, text: 'not found' } }
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', (_, e) => ({ text: e.text }))

  await $.prompt.submit({ text: 'refactor the parser' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /Claude is working/ }))?.text).toContain('your turn')
  expect((await ui.find({ type: 'Text', text: /Pekka/ }))?.text).toBe('Pekka on ___ kuin veljensä.')

  await ui.input({ key: 'answer:b1-01-g01', text: 'pidempi', kind: 'submit' })
  expect((await ui.find({ type: 'Text', text: /Correct/ }))?.text).toContain('Correct')

  await ui.press({ key: 'grade:3' })
  expect(reviews).toEqual([
    { card_id: 'b1-01-g01', rating: 3, mode: 'type:claude-pane', answer: 'pidempi', verdict: 'correct' },
  ])
  expect((await ui.find({ type: 'Text', text: /All done/ }))?.text).toContain('All done')
  await ui.unmount()
})

test('the server being down shows how to start it, on mobile too', async ($, on) => {
  on('http.fetch', () => {
    throw new Error('connection refused')
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('command.register', () => ({ value: { command: 'tauko' } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))

  await $.session.start({ cwd: '/', surface: 'mobile', isInteractive: true })
  await $.command.run({ command: 'tauko', args: '' })

  const ui = await $.ui.mount({ ...PANE, surface: 'mobile' })
  expect((await ui.find({ type: 'Text', text: /reach the tauko server/ }))?.text).toBe("Can't reach the tauko server")
  await ui.unmount()
})

test('a self-graded card shows your answer next to the reference, no auto-check', async ($, on) => {
  const QA = {
    id: 'go-01-q2', unit: 'go-01', unit_title: 'Channels', kind: 'qa', instruction: 'Answer',
    prompt: 'When would you use a buffered channel?', answers: ['To decouple sender and receiver timing'], self_grade: true,
  }
  const calls: string[] = []
  const reviews: unknown[] = []
  on('http.fetch', (_, e) => {
    calls.push(e.url)
    if (e.url.includes('/api/next')) {
      return json({ card: QA, is_new: true, due: 0, new_left: 5, course_title: 'Go concurrency' })
    }
    if (e.url.endsWith('/api/review')) {
      reviews.push(JSON.parse(e.init?.body ?? '{}'))
      return json({})
    }
    return { value: { status: 404, ok: false, headers: {}, text: '' } }
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', (_, e) => ({ text: e.text }))

  await $.prompt.submit({ text: 'go' })
  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /Go concurrency/ }))?.text).toContain('Channels')

  await ui.input({ key: 'answer:go-01-q2', text: 'when producer is bursty', kind: 'submit' })
  expect((await ui.find({ type: 'Text', text: /^you:/ }))?.text).toBe('you: when producer is bursty')
  expect(calls.some(u => u.endsWith('/api/check'))).toBe(false)

  await ui.press({ key: 'grade:4' })
  expect(reviews).toEqual([
    { card_id: 'go-01-q2', rating: 4, mode: 'type:claude-pane', answer: 'when producer is bursty', verdict: '' },
  ])
  await ui.unmount()
})

test('an unfocused pane says how to give it the keyboard', async ($, on) => {
  on('http.fetch', () => json({ card: CARD, is_new: false, due: 1, new_left: 0 }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const unfocused = await $.ui.mount({ ...PANE, props: { ...PANE.props, isFocused: false }, surface: 'terminal' })
  expect((await unfocused.find({ type: 'Text', text: /ctrl\+x tab/ }))?.text).toContain('to answer')
  await unfocused.unmount()

  const focused = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await focused.find({ type: 'Text', text: /ctrl\+x tab/ })).toBeUndefined()
  expect((await focused.find({ type: 'Text', text: /esc back/ }))?.text).toContain('esc back to Claude')
  await focused.unmount()
})

// The session-start work runs in the background; let it settle.
const settle = async () => {
  for (let i = 0; i < 200; i++) await Promise.resolve()
}

test('session start starts the server with tauko ensure and sets the status line', async ($, on) => {
  const runs: string[][] = []
  const statuses: (string | undefined)[] = []
  on('env.get', () => ({ value: '/home/u' }))
  on('process.run', (_, e) => {
    runs.push([...e.argv])
    // Not on PATH; found in ~/.local/bin.
    if (e.argv[0] === 'tauko') throw new Error('not found')
    return { value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('http.fetch', () => json({ due: 2, new_left: 5, stats: { reviews_today: 7, streak_days: 3 } }))
  on('ui.status', (_, e) => {
    statuses.push(e.text)
    return { value: undefined }
  })
  on('command.register', () => ({ value: { command: 'tauko' } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))

  await $.session.start({ cwd: '/', surface: 'terminal', isInteractive: true })
  await settle()

  expect(runs.slice(0, 2)).toEqual([['tauko', 'ensure'], ['/home/u/.local/bin/tauko', 'ensure']])
  expect(statuses.at(-1)).toBe('📖 7 to do · 7 today · 🔥3')
})

test('without the binary, the pane shows how to install it', async ($, on) => {
  on('env.get', () => ({ value: '/home/u' }))
  on('process.run', () => {
    throw new Error('not found')
  })
  on('http.fetch', () => {
    throw new Error('connection refused')
  })
  on('ui.status', () => ({ value: undefined }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('command.register', () => ({ value: { command: 'tauko' } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))

  await $.session.start({ cwd: '/', surface: 'terminal', isInteractive: true })
  await settle()
  await $.command.run({ command: 'tauko', args: '' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /install\.sh/ }))?.text).toContain('curl -fsSL')
  await ui.unmount()
})

test('practice cards are labeled when nothing is due', async ($, on) => {
  on('http.fetch', () => json({ card: CARD, is_new: false, practice: true, scope: '', due: 0, new_left: 0 }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /Practice: nothing due/ }))?.text).toContain('closest to forgetting')
  expect((await ui.find({ type: 'Text', text: /PRACTICE/ }))?.text).toContain('PRACTICE')
  expect((await ui.find({ type: 'Text', text: /All courses/ }))?.text).toContain('All courses')
  await ui.unmount()
})

test('the course picker sets the active course and +10 new asks the server', async ($, on) => {
  const posts: { url: string; body: unknown }[] = []
  on('http.fetch', (_, e) => {
    if (e.init?.method === 'POST') posts.push({ url: e.url, body: JSON.parse(e.init.body ?? '{}') })
    if (e.url.endsWith('/api/courses')) {
      return json([
        { id: 'fi-b1', title: 'Suomi B1', total: 504, seen: 120, due: 3 },
        { id: 'go-concurrency', title: 'Go concurrency', total: 20, seen: 0, due: 0 },
      ])
    }
    if (e.url.includes('/api/next')) return json({ card: CARD, is_new: false, scope: 'go-concurrency', due: 0, new_left: 9, course_title: 'Go concurrency' })
    return json({})
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.status', () => ({ value: undefined }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await ui.press({ key: 'courses' })
  expect((await ui.find({ type: 'Text', text: /CHOOSE A COURSE/ }))?.text).toBe('CHOOSE A COURSE')
  expect((await ui.find({ type: 'Text', text: /120\/504 seen/ }))?.text).toContain('3 due')

  await ui.press({ key: 'course:go-concurrency' })
  expect(posts.at(-1)).toEqual({ url: 'http://127.0.0.1:8787/api/settings', body: { active_course: 'go-concurrency' } })
  expect(await ui.find({ type: 'Text', text: /CHOOSE A COURSE/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /Go concurrency ·/ }))?.text).toContain('9 new')

  await ui.press({ key: 'more' })
  expect(posts.at(-1)).toEqual({ url: 'http://127.0.0.1:8787/api/more', body: { n: 10 } })
  await ui.unmount()
})

const GALLERY = [
  { id: 'go-concurrency', title: 'Go concurrency', description: 'channels, select, sync', cards: 20, installed: false },
  { id: 'spanish-a2-travel', title: 'Español de viaje A2', description: 'tickets and hotels', cards: 20, installed: false },
]

test('with no courses installed, the pane makes one with Claude and opens it when ready', async ($, on) => {
  const clock = mock.clock(on)
  const posts: { url: string; body: unknown }[] = []
  const toasts: string[] = []
  let polls = 0
  let ready = false
  on('http.fetch', (_, e) => {
    if (e.init?.method === 'POST') posts.push({ url: e.url, body: JSON.parse(e.init.body ?? '{}') })
    if (e.url.endsWith('/api/gallery')) return json(GALLERY)
    if (e.url.endsWith('/api/courses/gen')) return json({ kind: 'gen', label: 'Photography', state: 'running', step: 'Starting' })
    if (e.url.endsWith('/api/job')) {
      polls++
      return json(ready
        ? { kind: 'gen', label: 'Photography', state: 'done', course_id: 'photography', course_title: 'Photography basics' }
        : { kind: 'gen', label: 'Photography', state: 'running', step: 'Verifying every answer' })
    }
    if (e.url.includes('/api/next')) {
      return json(ready
        ? { card: CARD, is_new: true, scope: 'photography', due: 0, new_left: 20, course_title: 'Photography basics' }
        : { card: null, is_new: false, due: 0, new_left: 20, scope: '', no_courses: true })
    }
    return json({ due: 0, new_left: 20, stats: { reviews_today: 0, streak_days: 0 } })
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.status', () => ({ value: undefined }))
  on('ui.toast', (_, e) => {
    toasts.push(e.text)
    return { value: undefined }
  })
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /NO COURSES YET/ }))?.text).toBe('NO COURSES YET')
  expect((await ui.find({ type: 'Text', text: /Español de viaje/ }))?.text).toContain('Español de viaje A2')

  await ui.input({ key: 'topic', text: 'Photography', kind: 'submit' })
  expect(posts.at(-1)).toEqual({ url: 'http://127.0.0.1:8787/api/courses/gen', body: { topic: 'Photography' } })
  expect((await ui.find({ type: 'Text', text: /on its way/ }))?.text).toContain('opens here')

  await clock.advance(2000)
  expect((await ui.find({ type: 'Text', text: /Making “Photography”/ }))?.text).toContain('Verifying every answer')

  ready = true
  await clock.advance(2000)
  expect(toasts).toEqual(["✓ Photography basics is ready: it's in the tauko pane"])
  expect((await ui.find({ type: 'Text', text: /Pekka/ }))?.text).toBe('Pekka on ___ kuin veljensä.')

  // Done: no more polling.
  const after = polls
  await clock.advance(10000)
  expect(polls).toBe(after)
  await ui.unmount()
})

test('the course picker installs a gallery course; a failure says why', async ($, on) => {
  mock.clock(on)
  const posts: { url: string; body: unknown }[] = []
  on('http.fetch', (_, e) => {
    if (e.init?.method === 'POST') posts.push({ url: e.url, body: JSON.parse(e.init.body ?? '{}') })
    if (e.url.endsWith('/api/courses')) return json([{ id: 'fi-b1', title: 'Suomi B1', total: 504, seen: 120, due: 3 }])
    if (e.url.endsWith('/api/gallery')) return json(GALLERY)
    if (e.url.endsWith('/api/courses/add')) {
      return { value: { status: 409, ok: false, headers: {}, text: 'a course is already being added; wait for it to finish\n' } }
    }
    return json({ card: CARD, is_new: false, scope: 'fi-b1', due: 3, new_left: 0, course_title: 'Suomi B1' })
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.status', () => ({ value: undefined }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await ui.press({ key: 'courses' })
  await ui.press({ key: 'add-course' })
  expect((await ui.find({ type: 'Text', text: /ADD A COURSE/ }))?.text).toBe('ADD A COURSE')

  await ui.press({ key: 'gallery:go-concurrency' })
  expect(posts.at(-1)).toEqual({ url: 'http://127.0.0.1:8787/api/courses/add', body: { id: 'go-concurrency' } })
  expect((await ui.find({ type: 'Text', text: /Couldn't add/ }))?.text).toBe("✗ Couldn't add “go-concurrency”: a course is already being added; wait for it to finish")

  await ui.press({ key: 'dismiss-job' })
  expect(await ui.find({ type: 'Text', text: /Couldn't add/ })).toBeUndefined()
  await ui.press({ key: 'close-add' })
  expect((await ui.find({ type: 'Text', text: /Pekka/ }))?.text).toBe('Pekka on ___ kuin veljensä.')
  await ui.unmount()
})

test('a lesson shows its text; got it marks it read and moves on', async ($, on) => {
  const LESSON = {
    id: 'q-l1', unit: 'q1', unit_title: 'Qubits', kind: 'lesson', instruction: 'Learn', prompt: 'Superposition',
    body: 'A qubit holds a mix of 0 and 1 until you measure it.', answers: [],
  }
  const posts: { url: string; body: unknown }[] = []
  let served = 0
  on('http.fetch', (_, e) => {
    if (e.init?.method === 'POST') posts.push({ url: e.url, body: JSON.parse(e.init.body ?? '{}') })
    if (e.url.includes('/api/next')) {
      served++
      return json(served === 1 ? { card: LESSON, is_new: true, scope: '', due: 0, new_left: 9 } : { card: CARD, is_new: true, scope: '', due: 0, new_left: 8 })
    }
    return json({ read: true })
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.status', () => ({ value: undefined }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /holds a mix/ }))?.text).toContain('until you measure it')
  await ui.press({ key: 'got-it' })
  expect(posts.find(p => p.url.endsWith('/api/review'))?.body).toEqual({ card_id: 'q-l1', rating: 3, mode: 'lesson' })
  expect((await ui.find({ type: 'Text', text: /Pekka/ }))?.text).toBe('Pekka on ___ kuin veljensä.')
  await ui.unmount()
})

test('e explains the answer you gave, inside the card', async ($, on) => {
  const asked: unknown[] = []
  on('http.fetch', (_, e) => {
    if (e.url.endsWith('/api/explain')) {
      asked.push(JSON.parse(e.init?.body ?? '{}'))
      return json({ text: 'Pidempi is the comparative of pitkä: the k becomes d.', cached: false })
    }
    if (e.url.endsWith('/api/check')) return json({ verdict: 'wrong', expected: 'pidempi', suggested_rating: 1 })
    return json({ card: CARD, is_new: false, scope: '', due: 1, new_left: 0 })
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await ui.input({ key: 'answer:b1-01-g01', text: 'pitkämpi', kind: 'submit' })
  await ui.press({ key: 'explain' })
  expect(asked).toEqual([{ card_id: 'b1-01-g01', answer: 'pitkämpi' }])
  expect((await ui.find({ type: 'Text', text: /comparative of pitkä/ }))?.text).toContain('k becomes d')
  await ui.unmount()
})

test('k retires a card after answering, and undo brings it back', async ($, on) => {
  const posts: { url: string; body: unknown }[] = []
  on('http.fetch', (_, e) => {
    if (e.init?.method === 'POST') posts.push({ url: e.url, body: JSON.parse(e.init.body ?? '{}') })
    if (e.url.endsWith('/api/check')) return json({ verdict: 'correct', expected: 'pidempi', suggested_rating: 3 })
    if (e.url.includes('/api/next')) return json({ card: CARD, is_new: false, scope: '', due: 3, new_left: 0 })
    return json({})
  })
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('ui.status', () => ({ value: undefined }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  await $.prompt.submit({ text: 'x' })

  const ui = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await ui.input({ key: 'answer:b1-01-g01', text: 'pidempi', kind: 'submit' })
  await ui.press({ key: 'retire' })
  expect(posts.find(p => p.url.endsWith('/api/retire'))?.body).toEqual({ card_id: 'b1-01-g01' })
  expect((await ui.find({ type: 'Text', text: /Retired/ }))?.text).toContain('Pekka on ___')

  await ui.press({ key: 'undo' })
  expect(posts.find(p => p.url.endsWith('/api/unretire'))?.body).toEqual({ card_id: 'b1-01-g01' })
  expect(await ui.find({ type: 'Text', text: /Retired/ })).toBeUndefined()
  await ui.unmount()
})

// Session start with a fake server whose /api/version answers with version.
type Engine = Parameters<TestBody>[0]
type On = Parameters<TestBody>[1]

async function startWith($: Engine, on: On, version: (() => ReturnType<typeof json>) | null) {
  on('env.get', () => ({ value: '/home/u' }))
  on('process.run', () => ({ value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }))
  on('http.fetch', (_, e) => {
    if (e.url.endsWith('/api/version')) {
      return version ? version() : { value: { status: 404, ok: false, headers: {}, text: '404 page not found' } }
    }
    if (e.url.includes('/api/next')) return json({ card: CARD, is_new: false, due: 1, new_left: 0 })
    if (e.url.endsWith('/api/job')) return json(null)
    return json({ due: 1, new_left: 0, stats: { reviews_today: 0, streak_days: 0 } })
  })
  on('ui.status', () => ({ value: undefined }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('command.register', () => ({ value: { command: 'tauko' } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/', surface: 'terminal', isInteractive: true })
  await settle()
  await $.command.run({ command: 'tauko', args: '' })
  return $.ui.mount({ ...PANE, surface: 'terminal' })
}

test('a pane older than the server asks for a Claude Code restart', async ($, on) => {
  const ui = await startWith($, on, () => json({ version: 'v0.6.0' }))
  expect((await ui.find({ type: 'Text', text: /restart Claude Code/ }))?.text).toContain('updated to v0.6.0')
  await ui.press({ key: 'dismiss-notice' })
  expect(await ui.find({ type: 'Text', text: /restart Claude Code/ })).toBeUndefined()
  await ui.unmount()
})

test('a server without /api/version is older than the pane', async ($, on) => {
  const ui = await startWith($, on, null)
  expect((await ui.find({ type: 'Text', text: /older than this pane/ }))?.text).toContain('tauko update')
  await ui.unmount()
})

test('a newer release is mentioned; the same release says nothing', async ($, on) => {
  const ui = await startWith($, on, () => json({ version: 'v0.5.2', update: 'v0.5.3' }))
  expect((await ui.find({ type: 'Text', text: /is out/ }))?.text).toContain('tauko v0.5.3 is out')
  expect(await ui.find({ type: 'Text', text: /restart Claude Code|older than/ })).toBeUndefined()
  await ui.unmount()
})
