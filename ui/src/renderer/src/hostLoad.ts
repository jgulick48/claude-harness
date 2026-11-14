// Pure helpers for the per-host CPU and memory view (protocol §5.5): which
// hosts show it, formatting, levels and the polling loop. No React or DOM imports
// except the injectable visibility hooks of createLoadPoller.
import { loadString, saveString, type StorageLike } from './prefs'

export const LOAD_HOSTS_KEY = 'harness.loadHosts'
export const LOAD_POLL_MS = 5000

/** Mirrors core's session.HostLoad (hosts.load). */
export interface HostLoad {
  at: number // unix ms
  cpuPct: number | null
  cores: number
  load1: number
  load5: number
  load15: number
  memTotal: number
  memUsed: number
}

export type LoadLevel = 'ok' | 'warn' | 'high'

/** The host ids stored as a JSON array of strings; bad data gives an empty set. */
export function parseLoadHosts(raw: string | null | undefined): Set<string> {
  if (!raw) return new Set()
  try {
    const v: unknown = JSON.parse(raw)
    if (!Array.isArray(v)) return new Set()
    return new Set(v.filter((x): x is string => typeof x === 'string' && x !== ''))
  } catch {
    return new Set()
  }
}

export function serializeLoadHosts(hosts: ReadonlySet<string>): string {
  return JSON.stringify([...hosts].sort())
}

export function loadLoadHosts(getStorage?: () => StorageLike | null): Set<string> {
  return parseLoadHosts(loadString(LOAD_HOSTS_KEY, '', getStorage))
}

export function saveLoadHosts(hosts: ReadonlySet<string>, getStorage?: () => StorageLike | null): void {
  saveString(LOAD_HOSTS_KEY, serializeLoadHosts(hosts), getStorage)
}

const GB = 1024 ** 3
const MB = 1024 ** 2

/** "6.1 GB", or "512 MB" under a gigabyte. */
export function formatBytes(n: number): string {
  if (n >= GB) return `${(n / GB).toFixed(1)} GB`
  return `${Math.round(n / MB)} MB`
}

/** "6.1 / 15.6 GB"; the unit is shared unless the two differ (e.g. "512 MB / 1.0 GB"). */
export function formatMem(used: number, total: number): string {
  const u = formatBytes(used)
  const t = formatBytes(total)
  const uu = u.split(' ')[1]
  return uu === t.split(' ')[1] ? `${u.split(' ')[0]} / ${t}` : `${u} / ${t}`
}

/** Integer percent, truncated like the usage bars: "23%". */
export function formatLoadPct(p: number): string {
  return `${Math.floor(Math.max(0, Math.min(100, p)))}%`
}

export function loadLevel(pct: number): LoadLevel {
  return pct >= 95 ? 'high' : pct >= 80 ? 'warn' : 'ok'
}

export function memPct(l: Pick<HostLoad, 'memUsed' | 'memTotal'>): number {
  return l.memTotal > 0 ? (100 * l.memUsed) / l.memTotal : 0
}

/** Tooltip: load average, cores and the age of the reading. */
export function loadTooltip(l: HostLoad, nowMs: number, relativeTime: (sec: number, nowMs: number) => string): string {
  const f = (n: number): string => n.toFixed(2)
  return [
    `Load average: ${f(l.load1)} ${f(l.load5)} ${f(l.load15)} (1/5/15 min)`,
    `${l.cores} ${l.cores === 1 ? 'core' : 'cores'}`,
    `Updated ${relativeTime(l.at / 1000, nowMs)}`
  ].join('\n')
}

export interface PollerDeps {
  request: () => Promise<HostLoad>
  onLoad: (l: HostLoad) => void
  onError: (message: string) => void
  /** The core said "unsupported": polling has stopped. */
  onUnsupported: () => void
  isVisible: () => boolean
  /** Calls cb on each visibility change; returns the unsubscribe. */
  onVisibility: (cb: () => void) => () => void
  intervalMs?: number
}

/**
 * Polls once now and then every intervalMs, only while visible (paused when
 * hidden, an immediate poll on becoming visible). One timer; requests never
 * overlap. Returns stop().
 */
export function createLoadPoller(d: PollerDeps): () => void {
  const every = d.intervalMs ?? LOAD_POLL_MS
  let stopped = false
  let busy = false
  let timer: ReturnType<typeof setInterval> | null = null

  const clear = (): void => {
    if (timer) clearInterval(timer)
    timer = null
  }
  const poll = (): void => {
    if (stopped || busy || !d.isVisible()) return
    busy = true
    d.request().then(
      (l) => {
        busy = false
        if (!stopped) d.onLoad(l)
      },
      (e: { code?: string; message?: string }) => {
        busy = false
        if (stopped) return
        if (e?.code === 'unsupported') {
          stop()
          d.onUnsupported()
        } else d.onError(e?.message ?? 'Load unavailable')
      }
    )
  }
  const start = (): void => {
    if (timer) return
    timer = setInterval(poll, every)
    poll()
  }
  const unsub = d.onVisibility(() => {
    if (stopped) return
    if (d.isVisible()) start()
    else clear()
  })
  function stop(): void {
    stopped = true
    clear()
    unsub()
  }
  if (d.isVisible()) start()
  return stop
}
