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
  body?: string
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
  isPractice: boolean
  noCourses: boolean
  explanation: string
  isExplaining: boolean
  scope: string
  focus: string
  courseTitle: string
  due: number
  newLeft: number
  check: Check | null
  answer: string
  message: string
}

export type Mode = 'type' | 'tap'

export type RetiredRef = { id: string; prompt: string }

export type CourseInfo = {
  id: string
  title: string
  title_en?: string
  total: number
  seen: number
  due: number
}

declare module 'claude-code' {
  interface PluginState {
    tauko: {
      view: Shaped<View>
      isBusy: boolean
      mode: Mode
      lastId: string
      reviewed: number
      isInstalled: boolean
      isPicking: boolean
      courses: Shaped<CourseInfo[]>
      lastRetired: Shaped<RetiredRef | null>
    }
  }
}
