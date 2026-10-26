import { expect, test } from 'claude-code/testing'

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
  expect((await ui.find({ type: 'Text', text: /not reachable/ }))?.text).toBe('tauko server not reachable')
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
  expect((await ui.find({ type: 'Text', text: /Your answer/ }))?.text).toBe('Your answer: when producer is bursty')
  expect(calls.some(u => u.endsWith('/api/check'))).toBe(false)

  await ui.press({ key: 'grade:4' })
  expect(reviews).toEqual([
    { card_id: 'go-01-q2', rating: 4, mode: 'type:claude-pane', answer: 'when producer is bursty', verdict: '' },
  ])
  await ui.unmount()
})
