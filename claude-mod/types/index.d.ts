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

export type GalleryEntry = {
  id: string
  title: string
  description: string
  cards: number
  installed: boolean
}

// The course gallery as the add screen shows it.
export type Gallery = { entries: GalleryEntry[]; isLoading: boolean; error: string }

// A course being made or installed by the server (GET /api/job).
export type Job = {
  kind: 'gen' | 'add'
  label: string
  state: 'running' | 'done' | 'failed'
  step?: string
  error?: string
  course_id?: string
  course_title?: string
}

// A line above the card about versions: a stale pane, an old server, or a newer release.
export type Notice = { tone: 'warning' | 'info'; text: string }

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
      isAdding: boolean
      gallery: Shaped<Gallery>
      job: Shaped<Job | null>
      notice: Shaped<Notice | null>
    }
  }
}
