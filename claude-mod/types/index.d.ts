export type Card = {
  id: string
  unit: string
  unit_title: string
  kind: string
  instruction: string
  prompt: string
  hint?: string
  answers: string[]
  translation?: string
  note?: string
  example?: string
  example_translation?: string
  self_grade?: boolean
}

export type Check = {
  verdict: 'correct' | 'close' | 'wrong'
  expected: string
  reason?: string
  suggested_rating: number
}

export type Phase = 'loading' | 'ask' | 'revealed' | 'empty' | 'offline'

export type View = {
  phase: Phase
  card: Card | null
  isNew: boolean
  focus: string
  courseTitle: string
  due: number
  newLeft: number
  check: Check | null
  answer: string
  message: string
}

export type Mode = 'type' | 'tap'

declare module 'claude-code' {
  interface PluginState {
    tauko: {
      view: Shaped<View>
      isBusy: boolean
      mode: Mode
      lastId: string
      reviewed: number
    }
  }
}
