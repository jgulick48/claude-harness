import { useEffect } from 'react'
import { relativeTime } from './claudeStatus'
import type { Session } from './sessions'
import { useNow } from './useNow'
import { barPct, formatPct, limitLevel, limitLines, limitsTooltip, rememberLimits, resolveLimits } from './usage'

/** Compact plan-limit bars for a host group; nothing when the host has no limits. Click opens the Usage dialog. */
export function UsageIndicator({
  hostId,
  sessions,
  onOpen
}: {
  hostId: string
  sessions: readonly Session[]
  onOpen: () => void
}): React.JSX.Element | null {
  const now = useNow()
  const r = resolveLimits(hostId, sessions, now)
  const liveAt = r?.live ? r.at : 0
  const limits = r?.limits
  useEffect(() => {
    if (liveAt && limits) rememberLimits(hostId, limits, liveAt)
  }, [hostId, liveAt, limits])
  if (!r) return null
  const lines = limitLines(r.limits, now)
  const summary = lines.map((l) => `${l.key} ${formatPct(l.w.usedPct)}`).join(', ')
  return (
    <button
      type="button"
      className={`usage-ind${r.live ? '' : ' stale'}`}
      data-testid="usage-indicator"
      title={limitsTooltip(r, now, relativeTime)}
      aria-label={`Claude usage: ${summary}. Open details`}
      onClick={onOpen}
    >
      {lines.map((l) => (
        <span className="usage-item" key={l.key}>
          <span className="usage-key">{l.key}</span>
          <span className="usage-bar" aria-hidden="true">
            <span className={`usage-fill ${limitLevel(l.w.usedPct)}`} style={{ width: `${barPct(l.w.usedPct)}%` }} />
          </span>
          <span className="usage-pct">{formatPct(l.w.usedPct)}</span>
        </span>
      ))}
    </button>
  )
}
