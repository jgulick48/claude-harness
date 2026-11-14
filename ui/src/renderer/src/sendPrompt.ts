// Targets and selection for "Send prompt to sessions". Pure: no React or DOM imports.
import type { Session } from './sessions'

/** A session with the host it lives on. */
export interface PromptCandidate {
  host: string
  hostName: string
  session: Session
}

export interface SkippedTarget {
  target: PromptCandidate
  reason: string
}

export interface PromptTargets {
  send: PromptCandidate[]
  skip: SkippedTarget[]
}

/** Longest prompt the core accepts (after it cleans the text), in bytes. */
export const MAX_PROMPT_BYTES = 16 * 1024

export const selKey = (host: string, session: string): string => `${host}/${session}`

/** Why a session with this Claude status won't get a prompt; null when it will. Mirrors the core's rules. */
export function skipReason(status: string | undefined, allowWaiting: boolean): string | null {
  switch (status) {
    case 'idle':
      return null
    case 'waiting':
      return allowWaiting ? null : 'waiting for input, not included'
    case 'working':
      return 'Claude is working'
    case 'unknown':
      return "Claude's state is unknown"
    default:
      return "Claude isn't running"
  }
}

/**
 * Splits the selected sessions into those that get the prompt and those that
 * are skipped (with the reason). Order follows `sessions`; selected keys with
 * no matching session are ignored. The core re-checks every status when it sends.
 */
export function promptTargets(
  sessions: readonly PromptCandidate[],
  selection: ReadonlySet<string>,
  allowWaiting: boolean
): PromptTargets {
  const out: PromptTargets = { send: [], skip: [] }
  for (const c of sessions) {
    if (!selection.has(selKey(c.host, c.session.id))) continue
    const reason = skipReason(c.session.claudeStatus, allowWaiting)
    if (reason === null) out.send.push(c)
    else out.skip.push({ target: c, reason })
  }
  return out
}

/** Keys of the sessions whose Claude status is `status`. */
export function keysWithStatus(sessions: readonly PromptCandidate[], status: 'idle' | 'waiting'): Set<string> {
  return new Set(sessions.filter((c) => c.session.claudeStatus === status).map((c) => selKey(c.host, c.session.id)))
}

/** The selection without sessions that no longer exist; the same set when nothing was dropped. */
export function pruneSelection(selection: ReadonlySet<string>, sessions: readonly PromptCandidate[]): ReadonlySet<string> {
  if (selection.size === 0) return selection
  const live = new Set(sessions.map((c) => selKey(c.host, c.session.id)))
  const kept = [...selection].filter((k) => live.has(k))
  return kept.length === selection.size ? selection : new Set(kept)
}

/** Groups targets by host, keeping first-seen host order. */
export function groupByHost<T extends { host: string; hostName: string }>(items: readonly T[]): { host: string; hostName: string; items: T[] }[] {
  const groups: { host: string; hostName: string; items: T[] }[] = []
  for (const it of items) {
    let g = groups.find((x) => x.host === it.host)
    if (!g) groups.push((g = { host: it.host, hostName: it.hostName, items: [] }))
    g.items.push(it)
  }
  return groups
}

/** Byte length of the text as the core will count it (UTF-8). */
export function promptBytes(text: string): number {
  return new TextEncoder().encode(text).length
}
