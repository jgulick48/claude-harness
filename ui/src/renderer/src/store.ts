// Pure app state + reducer (no React imports so it is unit-testable).
import type { Host, Session } from './sessions'
import {
  attachRef,
  clearPane,
  closePane,
  emptyLayout,
  focusedPane,
  focusDirection,
  focusPane,
  pruneHosts,
  reconcile,
  resizeDivider,
  splitFocused,
  type Direction,
  type Layout
} from './layoutTree'

export interface ActiveRef {
  host: string
  session: string
}

export interface AppState {
  hosts: Host[]
  sessions: Record<string, Session[]>
  /** The focused pane's session, or null. Always derived from `layout`. */
  active: ActiveRef | null
  /** Split panes; the focused one is what `attach` replaces. */
  layout: Layout
  /** Toast-like message (e.g. pane cap reached); cleared by dismissNotice. */
  notice: string | null
  /** New-terminal dialog: null when closed; host is the preferred default. */
  dialog: { host?: string } | null
  /** Add-host dialog open. */
  addHostOpen: boolean
}

export const initialState: AppState = {
  hosts: [],
  sessions: {},
  active: null,
  layout: emptyLayout(),
  notice: null,
  dialog: null,
  addHostOpen: false
}

/** State starting from a restored layout (refs wait for hosts and sessions). */
export function initialStateWith(layout: Layout | null): AppState {
  return layout ? withLayout(initialState, layout) : initialState
}

function withLayout(state: AppState, layout: Layout): AppState {
  if (layout === state.layout) return state
  return { ...state, layout, active: focusedPane(layout)?.ref ?? null }
}

export type Action =
  | { type: 'hosts'; hosts: Host[] }
  | { type: 'sessions'; host: string; sessions: Session[] }
  | { type: 'upsertSession'; host: string; session: Session }
  /** Show ref in the focused pane (the pane `active` refers to). */
  | { type: 'attach'; ref: ActiveRef | null }
  /** Open ref in a new pane next to the focused one, and focus it. */
  | { type: 'openInSplit'; ref: ActiveRef; direction: 'right' | 'down' }
  /** New empty pane next to the focused one. */
  | { type: 'split'; direction: 'right' | 'down' }
  | { type: 'closePane'; id?: string }
  /** Empty a pane (keeps the pane). */
  | { type: 'clearPane'; id: string }
  | { type: 'focusPane'; id: string }
  | { type: 'focusDirection'; dir: Direction }
  | { type: 'resizeDivider'; path: number[]; index: number; delta: number; base?: number[] }
  /** Replace the whole layout (a named layout was chosen). */
  | { type: 'setLayout'; layout: Layout }
  | { type: 'dismissNotice' }
  | { type: 'openDialog'; host?: string }
  | { type: 'closeDialog' }
  | { type: 'openAddHost' }
  | { type: 'closeAddHost' }

export function reducer(state: AppState, a: Action): AppState {
  switch (a.type) {
    case 'hosts': {
      // hosts.changed carries the full list: drop state for removed hosts.
      const ids = new Set(a.hosts.map((h) => h.id))
      const sessions = Object.fromEntries(Object.entries(state.sessions).filter(([id]) => ids.has(id)))
      const layout = reconcile(pruneHosts(state.layout, ids), a.hosts, sessions)
      return withLayout({ ...state, hosts: a.hosts, sessions }, layout)
    }
    case 'sessions': {
      const sessions = { ...state.sessions, [a.host]: a.sessions }
      return withLayout({ ...state, sessions }, reconcile(state.layout, state.hosts, sessions))
    }
    case 'upsertSession': {
      const cur = state.sessions[a.host] ?? []
      const next = cur.some((s) => s.id === a.session.id)
        ? cur.map((s) => (s.id === a.session.id ? a.session : s))
        : [...cur, a.session]
      const sessions = { ...state.sessions, [a.host]: next }
      return withLayout({ ...state, sessions }, reconcile(state.layout, state.hosts, sessions))
    }
    case 'attach':
      return withLayout(state, attachRef(state.layout, a.ref))
    case 'openInSplit':
    case 'split': {
      const r = splitFocused(state.layout, a.direction, a.type === 'openInSplit' ? a.ref : null)
      return withLayout(r.error ? { ...state, notice: r.error } : state, r.layout)
    }
    case 'closePane':
      return withLayout(state, closePane(state.layout, a.id ?? state.layout.focus))
    case 'clearPane':
      return withLayout(state, clearPane(state.layout, a.id))
    case 'focusPane':
      return withLayout(state, focusPane(state.layout, a.id))
    case 'focusDirection':
      return withLayout(state, focusDirection(state.layout, a.dir))
    case 'resizeDivider':
      return withLayout(state, resizeDivider(state.layout, a.path, a.index, a.delta, a.base))
    case 'setLayout':
      return withLayout(state, reconcile(a.layout, state.hosts, state.sessions))
    case 'dismissNotice':
      return state.notice ? { ...state, notice: null } : state
    case 'openDialog':
      return { ...state, dialog: { host: a.host } }
    case 'closeDialog':
      return { ...state, dialog: null }
    case 'openAddHost':
      return { ...state, addHostOpen: true }
    case 'closeAddHost':
      return { ...state, addHostOpen: false }
  }
}
