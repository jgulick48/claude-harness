// Pure state + key handling for the directory combobox (no React imports).

export interface DirEntry {
  path: string
  gitRepo?: boolean
  group?: 'Recent' | 'Repositories'
}

export interface ComboState {
  items: DirEntry[]
  /** The list is allowed to show (false after Esc / blur until the user types again). */
  open: boolean
  /** Highlighted index, -1 for none. */
  active: number
  /** Sequence number of the latest request; older responses are dropped. */
  seq: number
  error: string | null
}

export const initialCombo: ComboState = { items: [], open: true, active: -1, seq: 0, error: null }

export type ComboAction =
  | { type: 'request'; seq: number }
  | { type: 'results'; seq: number; items: DirEntry[] }
  | { type: 'failed'; seq: number; message: string }
  | { type: 'open' }
  | { type: 'close' }
  | { type: 'move'; delta: 1 | -1 }
  | { type: 'hover'; index: number }
  | { type: 'accepted' }

export function comboReducer(state: ComboState, a: ComboAction): ComboState {
  switch (a.type) {
    case 'request':
      return { ...state, seq: a.seq }
    case 'results':
      if (a.seq !== state.seq) return state
      return { ...state, items: a.items, active: -1, error: null }
    case 'failed':
      if (a.seq !== state.seq) return state
      return { ...state, items: [], active: -1, error: a.message }
    case 'open':
      return state.open ? state : { ...state, open: true }
    case 'close':
      return { ...state, open: false, active: -1 }
    case 'move': {
      if (!isVisible(state)) return { ...state, open: true }
      const last = state.items.length - 1
      const active = Math.max(-1, Math.min(last, state.active + a.delta))
      return { ...state, active }
    }
    case 'hover':
      return { ...state, active: a.index }
    case 'accepted':
      return { ...state, active: -1 }
  }
}

export function isVisible(s: ComboState): boolean {
  return s.open && s.items.length > 0
}

export interface KeyResult {
  state: ComboState
  /** Path to put in the input; the key must not submit the form. */
  accept: string | null
  /** The combobox consumed the key: preventDefault and stop it reaching the dialog. */
  handled: boolean
}

/**
 * Keyboard behaviour. Enter with nothing highlighted and Esc with the list
 * closed are not handled, so the form submits / the dialog closes.
 */
export function handleComboKey(state: ComboState, key: string): KeyResult {
  const none = { state, accept: null, handled: false }
  const hl = isVisible(state) && state.active >= 0 ? state.items[state.active] : undefined
  switch (key) {
    case 'ArrowDown':
      return { state: comboReducer(state, { type: 'move', delta: 1 }), accept: null, handled: true }
    case 'ArrowUp':
      if (!isVisible(state)) return none
      return { state: comboReducer(state, { type: 'move', delta: -1 }), accept: null, handled: true }
    case 'Tab':
    case 'ArrowRight':
    case 'Enter':
      if (!hl) return none
      return { state: comboReducer(state, { type: 'accepted' }), accept: hl.path, handled: true }
    case 'Escape':
      if (!isVisible(state)) return none
      return { state: comboReducer(state, { type: 'close' }), accept: null, handled: true }
    default:
      return none
  }
}

/** Empty or bare `~` shows suggestions instead of completions. */
export function wantsSuggestions(value: string): boolean {
  const v = value.trim()
  return v === '' || v === '~'
}

const norm = (p: string): string => (p.length > 1 ? p.replace(/\/+$/, '') : p)

/** Recent first, then repositories not already listed as recent. */
export function suggestItems(r: { recent?: string[]; repos?: string[] }): DirEntry[] {
  const recent = r.recent ?? []
  const seen = new Set(recent.map(norm))
  const repos = (r.repos ?? []).filter((p) => !seen.has(norm(p)))
  return [
    ...recent.map((path): DirEntry => ({ path, group: 'Recent' })),
    ...repos.map((path): DirEntry => ({ path, gitRepo: true, group: 'Repositories' }))
  ]
}

export function completionItems(r: Array<{ path: string; gitRepo?: boolean }> | null | undefined): DirEntry[] {
  return (r ?? []).map((e) => ({ path: e.path, gitRepo: !!e.gitRepo }))
}
