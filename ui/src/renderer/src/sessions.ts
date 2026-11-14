// Shared protocol types and pure helpers for hosts and sessions.
export type HostState =
  | 'connected'
  | 'connecting'
  | 'disconnected'
  | 'unavailable'
  | 'host_key_unknown'
  | 'error'

export interface HostKey {
  address: string
  keyType: string
  fingerprint: string
}

export interface Host {
  id: string
  name: string
  kind: 'local' | 'ssh'
  target?: string
  state: HostState
  /** Reason for disconnected / unavailable / error. */
  error?: string
  hostKey?: HostKey
  /** Unix ms of the next automatic reconnect attempt (disconnected hosts only). */
  retryAt?: number
  home?: string
  tmuxVersion?: string
  /** Whether the Claude status hooks are installed; absent until checked. */
  claudeHooks?: ClaudeHooks
  /** Whether the status line wrapper that records Claude usage is installed; absent until checked. */
  claudeUsage?: ClaudeHooks
}

export type ClaudeHooks = 'installed' | 'outdated' | 'missing' | 'unknown'
export type ClaudeStatus = 'working' | 'waiting' | 'idle' | 'unknown' | ''

/** One-line status for a host header; empty when connected. */
export function hostStatusText(h: Pick<Host, 'state' | 'error'>): string {
  switch (h.state) {
    case 'connected':
      return ''
    case 'connecting':
      return 'Connecting…'
    case 'unavailable':
      return h.error || 'Host unavailable'
    case 'disconnected':
      return h.error || 'Disconnected'
    case 'error':
      return h.error || 'Connection error'
    case 'host_key_unknown':
      return 'Host key not trusted yet'
  }
}

/** Live countdown text for an automatic reconnect; never negative. */
export function reconnectText(retryAt: number, now: number): string {
  const secs = Math.max(0, Math.ceil((retryAt - now) / 1000))
  return `Reconnecting in ${secs}s…`
}

/** Error messages that retrying cannot fix (the user has to act). */
const PERMANENT_ERROR = /auth|host key|no ssh authentication|changed/i

export function isPermanentError(message: string | undefined): boolean {
  return !!message && PERMANENT_ERROR.test(message)
}

/**
 * Whether to call hosts.connect for a host after the machine wakes or the
 * network returns: SSH hosts that are disconnected, or in error with a cause
 * that is not obviously permanent.
 */
export function shouldRetryOnResume(h: Pick<Host, 'kind' | 'state' | 'error'>): boolean {
  if (h.kind !== 'ssh') return false
  if (h.state === 'disconnected') return true
  return h.state === 'error' && !isPermanentError(h.error)
}

export type ReattachAction = 'reattach' | 'gone' | 'none'

/**
 * What a terminal view whose stream ended should do automatically. Only a
 * 'connection lost' end is recovered, and only once the host is connected again.
 */
export function autoReattachAction(p: {
  ended: string | null
  hostConnected: boolean
  sessionExists: boolean
}): ReattachAction {
  if (p.ended !== 'connection lost' || !p.hostConnected) return 'none'
  return p.sessionExists ? 'reattach' : 'gone'
}

/** Sessions can only be created on connected hosts. */
export function canCreateOn(h: Pick<Host, 'state'>): boolean {
  return h.state === 'connected'
}

/** hosts.connect applies to hosts that are disconnected or in error. */
export function canReconnect(h: Pick<Host, 'kind' | 'state'>): boolean {
  return h.kind === 'ssh' && (h.state === 'disconnected' || h.state === 'error')
}

/**
 * Params for hosts.trustKey. Built from the same hostKey the trust panel
 * displays, so the fingerprint sent is exactly the one the user verified.
 * Null when there is nothing to trust.
 */
export function trustKeyParams(h: Pick<Host, 'id' | 'state' | 'hostKey'>): { host: string; fingerprint: string } | null {
  if (h.state !== 'host_key_unknown' || !h.hostKey?.fingerprint) return null
  return { host: h.id, fingerprint: h.hostKey.fingerprint }
}

/** Overlay text when a terminal stream closes. */
export function closedMessage(reason: string, hostName: string): string {
  return reason === 'connection lost' ? `Connection to ${hostName} lost` : 'Session ended'
}

export const SESSION_COLORS = ['red', 'orange', 'yellow', 'green', 'blue', 'purple'] as const
export type SessionColor = (typeof SESSION_COLORS)[number]

/** "Red" for "red"; the colour's display name. */
export function colorName(c: SessionColor): string {
  return c[0].toUpperCase() + c.slice(1)
}

export interface Session {
  id: string
  name: string
  harnessName: string
  managed: boolean
  cwd: string
  /** Git branch of cwd; absent when not a repo, detached, run-as, or not looked up yet. */
  gitBranch?: string
  windows: number
  attached: number
  created: number
  /** Protocol v5; absent or "" when Claude isn't running. */
  claudeStatus?: ClaudeStatus
  /** Unix seconds of the last status change. */
  claudeStatusAt?: number
  /** Unix user the session's shell runs as (via sudo); omitted for the login user. */
  runAs?: string
  /** Latest Claude usage reading; present only while Claude runs in the session. */
  usage?: SessionUsage
  /** Colour tag chosen by the user; absent when untagged. */
  color?: SessionColor
}

/** One rate-limit window; `resetsAt` is unix seconds. */
export interface UsageWindow {
  usedPct: number
  resetsAt: number
}

export interface SpendWindow extends UsageWindow {
  usedUsd?: number
  limitUsd?: number
  period?: string
}

export interface UsageLimits {
  fiveHour?: UsageWindow
  sevenDay?: UsageWindow
  spend?: SpendWindow
}

/** Protocol §5.4. Everything except `at` (unix seconds) is optional. */
export interface SessionUsage {
  at: number
  model?: string
  costUsd?: number
  contextPct?: number
  limits?: UsageLimits
}

function basename(p: string): string {
  const parts = p.split(/[\\/]/).filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

/** harnessName, else basename(cwd), else name. */
export function sessionLabel(s: Pick<Session, 'harnessName' | 'cwd' | 'name'>): string {
  return s.harnessName || basename(s.cwd) || s.name
}

const RUN_AS_RE = /^[A-Za-z_][A-Za-z0-9._-]{0,31}$/

/** Whether `name` is an acceptable Unix user name for "run as". */
export function validRunAs(name: string): boolean {
  return RUN_AS_RE.test(name)
}

/** " (as <user>)" suffix for tooltips; empty when the session isn't run as another user. */
export function runAsSuffix(s: Pick<Session, 'runAs'>): string {
  return s.runAs ? ` (as ${s.runAs})` : ''
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

export type CheckStatus = 'ok' | 'warn' | 'error'

/** One line of a host setup check (protocol v6 `hosts.check`). */
export interface CheckItem {
  id: string
  label: string
  status: CheckStatus
  detail: string
  fix?: string
  fixCommand?: string
}

export interface HostReport {
  host: string
  checkedAt: number
  items: CheckItem[]
}
