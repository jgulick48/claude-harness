// Framework-free helpers for the browser bridge, kept pure so they unit-test in node.

/** The core WebSocket URL for the page's own origin: http -> ws, https -> wss. */
export function wsUrlFor(loc: { protocol: string; host: string }): string {
  return `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}/ws`
}

/** True for http(s) URLs only (the only links the bridge will open or offer). */
export function isHttpLink(url: string | null | undefined): url is string {
  if (!url) return false
  try {
    const u = new URL(url)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/** POSIX single-quote quoting; an embedded ' becomes '\'' . */
export function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`
}

/** The one-liner that authorizes the server's key on a host. */
export function authorizedKeysCommand(key: string): string {
  return `echo ${shellQuote(key.trim())} >> ~/.ssh/authorized_keys`
}

export const RESUME_HIDDEN_MS = 30_000

interface Target {
  addEventListener(type: string, fn: () => void): void
  removeEventListener(type: string, fn: () => void): void
}

/**
 * Calls `cb` when the page becomes visible after being hidden longer than
 * `thresholdMs` (a sleeping laptop or a backgrounded tab), and on `online`.
 * Returns the unsubscribe.
 */
export function watchResume(
  doc: Target & { visibilityState: string },
  win: Target,
  cb: () => void,
  now: () => number = Date.now,
  thresholdMs: number = RESUME_HIDDEN_MS
): () => void {
  let hiddenAt: number | null = null
  const onVisibility = (): void => {
    if (doc.visibilityState === 'hidden') {
      hiddenAt ??= now()
      return
    }
    const was = hiddenAt
    hiddenAt = null
    if (was !== null && now() - was > thresholdMs) cb()
  }
  const onOnline = (): void => cb()
  doc.addEventListener('visibilitychange', onVisibility)
  win.addEventListener('online', onOnline)
  return () => {
    doc.removeEventListener('visibilitychange', onVisibility)
    win.removeEventListener('online', onOnline)
  }
}
