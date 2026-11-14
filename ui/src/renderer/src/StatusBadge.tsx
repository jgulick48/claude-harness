import { stateAge, statusInfo, statusTooltip } from './claudeStatus'
import type { Session } from './sessions'
import { useNow } from './useNow'

/**
 * Icon, text label and time in state ("Waiting 12m") for a session's Claude
 * status; renders nothing without one.
 */
export function StatusBadge({ session }: { session: Pick<Session, 'claudeStatus' | 'claudeStatusAt'> }): React.JSX.Element | null {
  const now = useNow()
  const info = statusInfo(session)
  if (!info) return null
  const age = info.status === 'unknown' ? '' : stateAge(session.claudeStatusAt, now)
  return (
    <span className={`status status-${info.status}`} title={statusTooltip(session, now)}>
      <span className="status-icon" aria-hidden="true">
        {info.icon}
      </span>
      <span className="status-label">{info.label}</span>
      {/* The space keeps the text "Waiting 12m" (flex drops it from layout). */}
      {age && ' '}
      {age && <span className="status-age">{age}</span>}
    </span>
  )
}
