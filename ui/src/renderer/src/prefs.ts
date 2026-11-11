// Boolean UI preferences in localStorage. Every access is guarded: storage can
// be missing or throw (private windows, blocked site data).
export const ATTENTION_SORT_KEY = 'harness.attentionSort'
export const NOTIFICATIONS_KEY = 'harness.notifications'

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
