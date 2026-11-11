// Per-host collapsed state in localStorage. Every access is guarded: storage
// can be missing or throw (private windows, blocked site data).
export const COLLAPSE_KEY = 'harness.collapsedHosts'

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>

function defaultStorage(): StorageLike | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function loadCollapsed(getStorage: () => StorageLike | null = defaultStorage): Set<string> {
  try {
    const raw = getStorage()?.getItem(COLLAPSE_KEY)
    if (!raw) return new Set()
    const v: unknown = JSON.parse(raw)
    return Array.isArray(v) ? new Set(v.filter((x): x is string => typeof x === 'string')) : new Set()
  } catch {
    return new Set()
  }
}

export function saveCollapsed(ids: ReadonlySet<string>, getStorage: () => StorageLike | null = defaultStorage): void {
  try {
    getStorage()?.setItem(COLLAPSE_KEY, JSON.stringify([...ids]))
  } catch {
    /* app works without persistence */
  }
}

export function toggleCollapsed(ids: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(ids)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}
