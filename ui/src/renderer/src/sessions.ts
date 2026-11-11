// Shared protocol types and pure helpers for hosts and sessions.
export interface Host {
  id: string
  name: string
  state: 'connected' | 'unavailable'
  error?: string
  home: string
  tmuxVersion?: string
}

export interface Session {
  id: string
  name: string
  harnessName: string
  managed: boolean
  cwd: string
  windows: number
  attached: number
  created: number
}

function basename(p: string): string {
  const parts = p.split(/[\\/]/).filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

/** harnessName, else basename(cwd), else name. */
export function sessionLabel(s: Pick<Session, 'harnessName' | 'cwd' | 'name'>): string {
  return s.harnessName || basename(s.cwd) || s.name
}

/** Replace a leading home directory with `~`. */
export function displayPath(cwd: string, home: string): string {
  if (!cwd) return ''
  const h = home.length > 1 ? home.replace(/[\\/]+$/, '') : home
  if (!h || h === '/') return cwd
  if (cwd === h) return '~'
  if (cwd.startsWith(h + '/') || cwd.startsWith(h + '\\')) return '~' + cwd.slice(h.length)
  return cwd
}

/** Truncate in the middle with an ellipsis so the result is at most `max` chars. */
export function truncateMiddle(s: string, max: number): string {
  if (s.length <= max || max < 3) return s
  const keep = max - 1
  const tail = Math.ceil(keep / 2)
  const head = keep - tail
  return s.slice(0, head) + '…' + s.slice(s.length - tail)
}

/** Manual order: created ascending, ties by id. Does not mutate. */
export function sortSessions(list: readonly Session[]): Session[] {
  return [...list].sort(
    (a, b) => a.created - b.created || a.id.localeCompare(b.id, undefined, { numeric: true })
  )
}
