// Pure helpers for the Claude status UI (protocol v5): labels, relative time,
// the "needs attention first" comparator, waiting-transition detection and
// notification gating. No React or DOM imports so everything is unit-testable.
import { sortSessions, type ClaudeHooks, type Host, type Session } from './sessions'

export const APP_TITLE = 'Claude Harness'
export const UNKNOWN_TOOLTIP = "Claude is running but status hooks aren't installed on this host"

export type ShownStatus = 'working' | 'waiting' | 'idle' | 'unknown'

export interface StatusInfo {
  status: ShownStatus
  icon: string
  label: string
}

const INFO: Record<ShownStatus, StatusInfo> = {
  working: { status: 'working', icon: '◐', label: 'Working' },
  waiting: { status: 'waiting', icon: '◉', label: 'Waiting' },
  idle: { status: 'idle', icon: '✓', label: 'Idle' },
  unknown: { status: 'unknown', icon: '?', label: 'Unknown' }
}

/** Icon and label for a session; null when Claude isn't running (or an unrecognised value). */
export function statusInfo(s: Pick<Session, 'claudeStatus'>): StatusInfo | null {
  const v = s.claudeStatus
  return v && Object.prototype.hasOwnProperty.call(INFO, v) ? INFO[v as ShownStatus] : null
}

/** Relative time such as "just now", "12s ago", "3m ago", "2h ago", "4d ago". */
export function relativeTime(thenSec: number, nowMs: number): string {
  const secs = Math.max(0, Math.floor(nowMs / 1000 - thenSec))
  if (secs < 5) return 'just now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

/**
 * How long a session has been in its state, compact for the row: "" under a
 * minute (or with no time), else "12m", "3h", "2d". A time in the future
 * (host clock ahead of ours) counts as "".
 */
export function stateAge(atSec: number | undefined, nowMs: number): string {
  if (!atSec) return ''
  const mins = Math.floor((nowMs / 1000 - atSec) / 60)
  if (!(mins >= 1)) return ''
  if (mins < 60) return `${mins}m`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

/** "Waiting 12m"-style text for the rail tooltip; "" with no status. */
export function statusText(s: Pick<Session, 'claudeStatus' | 'claudeStatusAt'>, nowMs: number): string {
  const info = statusInfo(s)
  if (!info) return ''
  const age = info.status === 'unknown' ? '' : stateAge(s.claudeStatusAt, nowMs)
  return age ? `${info.label} ${age}` : info.label
}

/** Tooltip for a status badge; empty when there is no status. */
export function statusTooltip(s: Pick<Session, 'claudeStatus' | 'claudeStatusAt'>, nowMs: number): string {
  const info = statusInfo(s)
  if (!info) return ''
  const parts: string[] = []
  if (info.status === 'unknown') parts.push(UNKNOWN_TOOLTIP)
  if (s.claudeStatusAt) {
    parts.push(`since ${relativeTime(s.claudeStatusAt, nowMs)}`)
  }
  return parts.length ? parts.join('\n') : info.label
}

const RANK: Record<string, number> = { waiting: 0, working: 1, idle: 2, unknown: 3 }

/** Rank for the attention sort: Waiting < Working < Idle < Unknown < none. */
export function attentionRank(s: Pick<Session, 'claudeStatus'>): number {
  const i = statusInfo(s)
  return i ? RANK[i.status] : 4
}

/** Comparator for "Needs attention first". Equal ranks compare 0 (stable sort keeps order). */
export function compareAttention(a: Pick<Session, 'claudeStatus'>, b: Pick<Session, 'claudeStatus'>): number {
  return attentionRank(a) - attentionRank(b)
}

/** Manual order, optionally re-sorted by attention with the manual order as tiebreak. Does not mutate. */
export function orderSessions(list: readonly Session[], needsAttentionFirst: boolean): Session[] {
  const base = sortSessions(list)
  return needsAttentionFirst ? base.sort(compareAttention) : base
}

/**
 * Sessions that newly became waiting. A session absent from `prev` was just
 * seen (app start, reconnect, new session) and never counts; with no `prev`
 * at all nothing does.
 */
export function newlyWaiting(prev: readonly Session[] | undefined, next: readonly Session[]): Session[] {
  if (!prev) return []
  const before = new Map(prev.map((s) => [s.id, s.claudeStatus]))
  return next.filter((s) => s.claudeStatus === 'waiting' && before.has(s.id) && before.get(s.id) !== 'waiting')
}

export function waitingCount(sessions: readonly Session[] | undefined): number {
  return (sessions ?? []).filter((s) => s.claudeStatus === 'waiting').length
}

/** Total waiting sessions across all hosts. */
export function attentionCount(sessions: Readonly<Record<string, readonly Session[]>>): number {
  let n = 0
  for (const list of Object.values(sessions)) n += waitingCount(list)
  return n
}

export function attentionTitle(n: number): string {
  return n > 0 ? `(${n}) ${APP_TITLE}` : APP_TITLE
}

export function notificationText(label: string, hostName: string, path: string): { title: string; body: string } {
  return { title: `${label} needs you`, body: `${hostName} · ${path}` }
}

/** Don't notify for the session the user is already looking at. */
export function isViewing(
  active: { host: string; session: string } | null,
  host: string,
  session: string,
  windowFocused: boolean
): boolean {
  return windowFocused && active?.host === host && active.session === session
}

/** Suppresses repeated notifications for one key inside a time window. */
export function createDebounce(windowMs: number): (key: string, nowMs: number) => boolean {
  const last = new Map<string, number>()
  return (key, nowMs) => {
    const t = last.get(key)
    if (t !== undefined && nowMs - t < windowMs) return false
    last.set(key, nowMs)
    return true
  }
}

export const NOTIFY_DEBOUNCE_MS = 10_000

export type HooksNotice = 'install' | 'update' | null

/** Which inline notice a host group shows; nothing for installed, unknown or absent. */
export function hooksNotice(h: Pick<Host, 'claudeHooks'>): HooksNotice {
  const v: ClaudeHooks | undefined = h.claudeHooks
  return v === 'missing' ? 'install' : v === 'outdated' ? 'update' : null
}

export function canRemoveHooks(h: Pick<Host, 'claudeHooks'>): boolean {
  return h.claudeHooks === 'installed' || h.claudeHooks === 'outdated'
}
