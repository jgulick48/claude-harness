// Boolean UI preferences in localStorage. Every access is guarded: storage can
// be missing or throw (private windows, blocked site data).
export const ATTENTION_SORT_KEY = 'harness.attentionSort'
export const NOTIFICATIONS_KEY = 'harness.notifications'
export const SIDEBAR_RAIL_KEY = 'harness.sidebarRail'
export const SIDEBAR_WIDTH_KEY = 'harness.sidebarWidth'

export type StorageLike = Pick<Storage, 'getItem' | 'setItem'>

function defaultStorage(): StorageLike | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

export function loadFlag(
  key: string,
  fallback: boolean,
  getStorage: () => StorageLike | null = defaultStorage
): boolean {
  try {
    const raw = getStorage()?.getItem(key)
    return raw === '1' ? true : raw === '0' ? false : fallback
  } catch {
    return fallback
  }
}

export function saveFlag(key: string, v: boolean, getStorage: () => StorageLike | null = defaultStorage): void {
  try {
    getStorage()?.setItem(key, v ? '1' : '0')
  } catch {
    /* app works without persistence */
  }
}

/** Reads a finite number, falling back when missing, malformed or storage throws. */
export function loadNumber(key: string, fallback: number, getStorage: () => StorageLike | null = defaultStorage): number {
  try {
    const raw = getStorage()?.getItem(key)
    if (raw === null || raw === undefined || raw.trim() === '') return fallback
    const n = Number(raw)
    return Number.isFinite(n) ? n : fallback
  } catch {
    return fallback
  }
}

export function saveNumber(key: string, v: number, getStorage: () => StorageLike | null = defaultStorage): void {
  try {
    getStorage()?.setItem(key, String(v))
  } catch {
    /* app works without persistence */
  }
}
