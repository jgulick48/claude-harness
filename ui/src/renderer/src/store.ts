// Pure app state + reducer (no React imports so it is unit-testable).
import type { Host, Session } from './sessions'

export interface ActiveRef {
  host: string
  session: string
}

export interface AppState {
  hosts: Host[]
  sessions: Record<string, Session[]>
  active: ActiveRef | null
  /** New-terminal dialog: null when closed; host is the preferred default. */
  dialog: { host?: string } | null
  /** Add-host dialog open. */
  addHostOpen: boolean
}

export const initialState: AppState = { hosts: [], sessions: {}, active: null, dialog: null, addHostOpen: false }

export type Action =
  | { type: 'hosts'; hosts: Host[] }
  | { type: 'sessions'; host: string; sessions: Session[] }
  | { type: 'upsertSession'; host: string; session: Session }
  | { type: 'attach'; ref: ActiveRef | null }
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
      const active = state.active && ids.has(state.active.host) ? state.active : null
      return { ...state, hosts: a.hosts, sessions, active }
    }
    case 'sessions':
      return { ...state, sessions: { ...state.sessions, [a.host]: a.sessions } }
    case 'upsertSession': {
      const cur = state.sessions[a.host] ?? []
      const next = cur.some((s) => s.id === a.session.id)
        ? cur.map((s) => (s.id === a.session.id ? a.session : s))
        : [...cur, a.session]
      return { ...state, sessions: { ...state.sessions, [a.host]: next } }
    }
    case 'attach':
      return { ...state, active: a.ref }
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
