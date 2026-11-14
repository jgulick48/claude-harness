// One shared clock for relative times ("waiting 12m"): a single interval for
// every subscriber, running only while something is subscribed.
import { useSyncExternalStore } from 'react'

export const NOW_TICK_MS = 30_000

let now = Date.now()
let timer: ReturnType<typeof setInterval> | null = null
const listeners = new Set<() => void>()

function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  if (!timer) {
    now = Date.now()
    timer = setInterval(() => {
      now = Date.now()
      for (const l of listeners) l()
    }, NOW_TICK_MS)
  }
  return () => {
    listeners.delete(fn)
    if (listeners.size === 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }
}

/** The current time in ms, updated every NOW_TICK_MS. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now)
}
