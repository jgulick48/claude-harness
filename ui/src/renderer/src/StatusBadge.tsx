import { statusInfo, statusTooltip } from './claudeStatus'
import type { Session } from './sessions'

/** Icon plus text label for a session's Claude status; renders nothing without one. */
export function StatusBadge({ session }: { session: Pick<Session, 'claudeStatus' | 'claudeStatusAt'> }): React.JSX.Element | null {
  const info = statusInfo(session)
  if (!info) return null
  return (
    <span className={`status status-${info.status}`} title={statusTooltip(session, Date.now())}>
      <span className="status-icon" aria-hidden="true">
        {info.icon}
      </span>
      <span className="status-label">{info.label}</span>
    </span>
  )
}
