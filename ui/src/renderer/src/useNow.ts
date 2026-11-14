// One shared clock for relative times ("waiting 12m"): a single interval for
// every subscriber, running only while something is subscribed.
import { useSyncExternalStore } from 'react'

export const NOW_TICK_MS = 30_000

let tick = 0
let timer: ReturnType<typeof setInterval> | null = null
const listeners = new Set<() => void>()

function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  if (!timer) {
    timer = setInterval(() => {
      tick++
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

/**
 * The current time in ms. The component re-renders at least every
 * NOW_TICK_MS, and every render reads the clock afresh, so a status that just
 * changed is never measured against a stale time.
 */
export function useNow(): number {
  useSyncExternalStore(subscribe, () => tick)
  return Date.now()
}
