// Sidebar keyboard navigation and sizing math. Pure: no React or DOM imports.

export interface NavRef {
  host: string
  session: string
}

export interface NavGroup {
  host: string
  /** Folded groups show no rows, so keyboard navigation skips them. */
  folded: boolean
  sessions: readonly string[]
}

export const SIDEBAR_MIN = 200
export const SIDEBAR_MAX = 480
export const SIDEBAR_DEFAULT = 280
export const RAIL_WIDTH = 48

/** Visible sessions in sidebar order (host groups in order, folded groups skipped). */
export function navRefs(groups: readonly NavGroup[]): NavRef[] {
  return groups.flatMap((g) => (g.folded ? [] : g.sessions.map((session) => ({ host: g.host, session }))))
}

export function sameRef(a: NavRef | null | undefined, b: NavRef | null | undefined): boolean {
  return !!a && !!b && a.host === b.host && a.session === b.session
}

/** Moves one step through the list, clamped at both ends. Null for an empty list. */
export function moveNav(list: readonly NavRef[], current: NavRef | null, dir: 1 | -1): NavRef | null {
  if (list.length === 0) return null
  const i = current ? list.findIndex((r) => sameRef(r, current)) : -1
  if (i < 0) return dir === 1 ? list[0] : list[list.length - 1]
  return list[Math.min(list.length - 1, Math.max(0, i + dir))]
}

/** The Nth (1-based) visible session, or null when there are fewer. */
export function nthRef(list: readonly NavRef[], n: number): NavRef | null {
  return Number.isInteger(n) && n >= 1 && n <= list.length ? list[n - 1] : null
}

export type Shortcut = { kind: 'filter' } | { kind: 'rail' } | { kind: 'nth'; n: number }

export interface ShortcutKey {
  key: string
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
  altKey: boolean
}

/** Global sidebar shortcuts: Cmd/Ctrl+K filter, +B rail, +1..9 Nth session. */
export function shortcutFor(e: ShortcutKey): Shortcut | null {
  if (!(e.ctrlKey || e.metaKey) || e.shiftKey || e.altKey) return null
  const k = e.key.toLowerCase()
  if (k === 'k') return { kind: 'filter' }
  if (k === 'b') return { kind: 'rail' }
  if (/^[1-9]$/.test(k)) return { kind: 'nth', n: Number(k) }
  return null
}

export function clampWidth(w: number): number {
  if (!Number.isFinite(w)) return SIDEBAR_DEFAULT
  return Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, Math.round(w)))
}
