// Pure helpers for the Claude usage UI (protocol §5.4): plan limits per host,
// formatting, levels and the "last known limits" store. No React or DOM imports.
import { loadString, saveString, type StorageLike } from './prefs'
import type { Host, Session, SpendWindow, UsageLimits, UsageWindow } from './sessions'

export const USAGE_LIMITS_KEY = 'harness.usageLimits'

export interface LimitsReading {
  at: number
  limits: UsageLimits
}

export type LimitLevel = 'ok' | 'warn' | 'high'

const WINDOWS = ['fiveHour', 'sevenDay', 'spend'] as const

/** `limits` without the windows that have already reset; null when none is left. */
export function liveLimits(limits: UsageLimits | undefined, nowMs: number): UsageLimits | null {
  if (!limits || typeof limits !== 'object') return null
  const out: Record<string, UsageWindow> = {}
  for (const k of WINDOWS) {
    const w = limits[k] as UsageWindow | undefined
    if (w && typeof w.usedPct === 'number' && typeof w.resetsAt === 'number' && w.resetsAt * 1000 > nowMs) out[k] = w
  }
  return Object.keys(out).length ? (out as UsageLimits) : null
}

/**
 * The host's limits: the reading with the largest `at` among its sessions that
 * have limits, minus windows already reset. Limits belong to the account, so
 * any session's reading will do; the newest is the best.
 */
export function hostReading(sessions: readonly Session[] | undefined, nowMs: number): LimitsReading | null {
  let best: Session | undefined
  for (const s of sessions ?? []) {
    const u = s.usage
    if (!u?.limits) continue
    if (!best || u.at > best.usage!.at) best = s
  }
  if (!best?.usage) return null
  const limits = liveLimits(best.usage.limits, nowMs)
  return limits ? { at: best.usage.at, limits } : null
}

export function hostLimits(sessions: readonly Session[] | undefined, nowMs: number): UsageLimits | null {
  return hostReading(sessions, nowMs)?.limits ?? null
}

/** "resets in 2h 14m", "resets in 3d 4h", "resets in 12m", "resets now". */
export function formatReset(resetsAtSec: number, nowMs: number): string {
  const secs = Math.floor(resetsAtSec - nowMs / 1000)
  if (secs <= 0) return 'resets now'
  const mins = Math.floor(secs / 60)
  if (mins < 1) return 'resets in <1m'
  const hours = Math.floor(mins / 60)
  if (hours < 1) return `resets in ${mins}m`
  if (hours < 24) return `resets in ${hours}h ${mins % 60}m`
  return `resets in ${Math.floor(hours / 24)}d ${hours % 24}h`
}

/** "$0.01", "$12.3", "<$0.01". */
export function formatCost(usd: number): string {
  if (usd < 0.005) return '<$0.01'
  if (usd < 10) return `$${usd.toFixed(2)}`
  return `$${usd.toFixed(1)}`
}

/** Integer percent, truncated so 99.9% never reads as the limit being reached: "42%". */
export function formatPct(p: number): string {
  return `${Math.floor(p)}%`
}

export function limitLevel(pct: number): LimitLevel {
  return pct >= 90 ? 'high' : pct >= 70 ? 'warn' : 'ok'
}

/** Clamped to 0-100 for drawing a bar. */
export function barPct(p: number): number {
  return Math.max(0, Math.min(100, p))
}

const money = (n: number): string => `$${Number.isInteger(n) ? n : n.toFixed(2)}`

export interface LimitLine {
  /** Short label for the compact indicator. */
  key: '5h' | '7d' | '$'
  label: string
  w: UsageWindow
  /** What follows the label, e.g. "23% used, resets in 2h 14m". */
  detail: string
  /** Full sentence for tooltips: "<label>: <detail>". */
  text: string
}

/** One line per window that is present, in fixed order. */
export function limitLines(limits: UsageLimits, nowMs: number): LimitLine[] {
  const out: LimitLine[] = []
  const add = (key: LimitLine['key'], label: string, w: UsageWindow, amount: string): void => {
    const detail = `${amount}, ${formatReset(w.resetsAt, nowMs)}`
    out.push({ key, label, w, detail, text: `${label}: ${detail}` })
  }
  if (limits.fiveHour) add('5h', '5-hour limit', limits.fiveHour, `${formatPct(limits.fiveHour.usedPct)} used`)
  if (limits.sevenDay) add('7d', 'Weekly limit', limits.sevenDay, `${formatPct(limits.sevenDay.usedPct)} used`)
  if (limits.spend) {
    const sp: SpendWindow = limits.spend
    const amount =
      sp.usedUsd !== undefined && sp.limitUsd !== undefined
        ? `${money(sp.usedUsd)} of ${money(sp.limitUsd)}${sp.period ? ` (${sp.period})` : ''}`
        : `${formatPct(sp.usedPct)} used`
    add('$', 'Spend', sp, amount)
  }
  return out
}

// --- last known limits, per host, in localStorage ---

type Store = Record<string, LimitsReading>

function readStore(getStorage?: () => StorageLike | null): Store {
  try {
    const v: unknown = JSON.parse(loadString(USAGE_LIMITS_KEY, '', getStorage) || '{}')
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Store) : {}
  } catch {
    return {}
  }
}

/** Remembers the limits seen for a host; an older reading never replaces a newer one. */
export function rememberLimits(
  hostId: string,
  limits: UsageLimits,
  at: number,
  getStorage?: () => StorageLike | null
): void {
  const store = readStore(getStorage)
  const prev = store[hostId]
  if (prev && typeof prev.at === 'number' && prev.at >= at) return
  store[hostId] = { at, limits }
  saveString(USAGE_LIMITS_KEY, JSON.stringify(store), getStorage)
}

/** The last limits seen for a host, minus windows that have since reset; null if none. */
export function lastLimits(hostId: string, nowMs: number, getStorage?: () => StorageLike | null): LimitsReading | null {
  const e = readStore(getStorage)[hostId]
  if (!e || typeof e.at !== 'number') return null
  const limits = liveLimits(e.limits, nowMs)
  return limits ? { at: e.at, limits } : null
}

export type ResolvedLimits = LimitsReading & { live: boolean }

/** Live limits when Claude is reporting them, else the last known ones. */
export function resolveLimits(
  hostId: string,
  sessions: readonly Session[] | undefined,
  nowMs: number,
  getStorage?: () => StorageLike | null
): ResolvedLimits | null {
  const live = hostReading(sessions, nowMs)
  if (live) return { ...live, live: true }
  const last = lastLimits(hostId, nowMs, getStorage)
  return last ? { ...last, live: false } : null
}

/** Tooltip text for a host's limits. */
export function limitsTooltip(
  r: ResolvedLimits,
  nowMs: number,
  relative: (atSec: number, nowMs: number) => string
): string {
  const lines = limitLines(r.limits, nowMs).map((l) => l.text)
  lines.push(`As of ${relative(r.at, nowMs)}${r.live ? '' : ' (Claude not running)'}`)
  return lines.join('\n')
}

// --- host menu / dialog predicates ---

/** Hooks are installed but usage recording isn't (or is out of date). */
export function canRecordUsage(h: Pick<Host, 'claudeHooks' | 'claudeUsage'>): boolean {
  const hooks = h.claudeHooks === 'installed' || h.claudeHooks === 'outdated'
  return hooks && (h.claudeUsage === 'missing' || h.claudeUsage === 'outdated')
}

export function canStopUsage(h: Pick<Host, 'claudeUsage'>): boolean {
  return h.claudeUsage === 'installed' || h.claudeUsage === 'outdated'
}

/** Sessions where Claude runs and reports usage. */
export function usageSessions(sessions: readonly Session[] | undefined): Session[] {
  return (sessions ?? []).filter((s) => s.usage)
}

/** "ctx 42% · $0.01" (missing parts omitted); '' when there is nothing to show. */
export function paneUsageText(u: Session['usage']): string {
  if (!u) return ''
  const parts: string[] = []
  if (u.contextPct !== undefined) parts.push(`ctx ${formatPct(u.contextPct)}`)
  if (u.costUsd !== undefined) parts.push(formatCost(u.costUsd))
  return parts.join(' · ')
}

/** "Context window 42% used · estimated cost $0.0123 (Opus)". */
export function paneUsageTooltip(u: Session['usage']): string {
  if (!u) return ''
  const parts: string[] = []
  if (u.contextPct !== undefined) parts.push(`Context window ${formatPct(u.contextPct)} used`)
  if (u.costUsd !== undefined) parts.push(`estimated cost $${u.costUsd.toFixed(4)}`)
  const text = parts.join(' · ')
  return u.model ? `${text}${text ? ' ' : ''}(${u.model})` : text
}
