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
}

export const initialState: AppState = { hosts: [], sessions: {}, active: null, dialog: null }

export type Action =
  | { type: 'hosts'; hosts: Host[] }
  | { type: 'sessions'; host: string; sessions: Session[] }
  | { type: 'upsertSession'; host: string; session: Session }
  | { type: 'attach'; ref: ActiveRef | null }
  | { type: 'openDialog'; host?: string }
  | { type: 'closeDialog' }

export function reducer(state: AppState, a: Action): AppState {
  switch (a.type) {
    case 'hosts':
      return { ...state, hosts: a.hosts }
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
  }
}
