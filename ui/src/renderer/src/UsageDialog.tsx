import { useState } from 'react'
import { relativeTime, statusInfo } from './claudeStatus'
import { sessionLabel, type Host, type Session } from './sessions'
import { useNow } from './useNow'
import { UsageHistoryView } from './UsageHistoryView'
import {
  barPct,
  canRecordUsage,
  formatCost,
  formatPct,
  limitLevel,
  limitLines,
  resolveLimits,
  usageSessions
} from './usage'

const EXPLANATION =
  'Plan limits appear for Claude Pro, Max and Ultra once Claude has answered in a session on a host with usage recording installed.'

/** Per-host plan limits and the per-session model, context and cost. */
export function UsageDialog({
  hosts,
  sessions,
  onClose,
  onInstall
}: {
  hosts: readonly Host[]
  sessions: Readonly<Record<string, readonly Session[]>>
  onClose: () => void
  onInstall: (host: Host) => void
}): React.JSX.Element {
  const now = useNow()
  const [tab, setTab] = useState<'now' | 'history'>('now')
  const rows = hosts.map((host) => {
    const list = sessions[host.id] ?? []
    return { host, limits: resolveLimits(host.id, list, now), running: usageSessions(list), needsInstall: canRecordUsage(host) }
  })
  const any = rows.some((r) => r.limits || r.running.length > 0)
  const shown = rows.filter((r) => r.limits || r.running.length > 0 || r.needsInstall)
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        className="modal usage-dialog"
        role="dialog"
        aria-label="Claude usage"
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
      >
        <h2>Claude usage</h2>
        <div className="usage-tabs" role="tablist" aria-label="Usage view">
          {(['now', 'history'] as const).map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={tab === t}
              className={`usage-tab${tab === t ? ' active' : ''}`}
              onClick={() => setTab(t)}
            >
              {t === 'now' ? 'Now' : 'History'}
            </button>
          ))}
        </div>
        {tab === 'history' && <UsageHistoryView hosts={hosts} />}
        {tab === 'now' && !any && <p className="help usage-empty">{EXPLANATION}</p>}
        {tab === 'now' && shown.map(({ host, limits, running, needsInstall }) => (
          <section className="usage-host" key={host.id} data-testid="usage-host" aria-label={host.name}>
            <h3>{host.name}</h3>
            {needsInstall && (
              <p className="usage-install">
                <span>Usage recording isn&apos;t installed on this host</span>
                <button type="button" className="btn small" onClick={() => onInstall(host)}>
                  Install…
                </button>
              </p>
            )}
            {limits && (
              <div className="usage-limits">
                {limitLines(limits.limits, now).map((l) => (
                  <div className="usage-limit" key={l.key} data-testid={`usage-limit-${l.key === '$' ? 'spend' : l.key}`}>
                    <div className="usage-limit-head">
                      <span>{l.label}</span>
                      <span className="usage-pct">{formatPct(l.w.usedPct)}</span>
                    </div>
                    <div className="usage-bar wide" aria-hidden="true">
                      <span className={`usage-fill ${limitLevel(l.w.usedPct)}`} style={{ width: `${barPct(l.w.usedPct)}%` }} />
                    </div>
                    <div className="usage-limit-text">{l.detail}</div>
                  </div>
                ))}
                <p className="help">
                  As of {relativeTime(limits.at, now)}
                  {limits.live ? '' : ' (Claude not running)'}
                </p>
              </div>
            )}
            {running.length > 0 && (
              <table className="usage-table">
                <thead>
                  <tr>
                    <th>Session</th>
                    <th>Model</th>
                    <th>Context</th>
                    <th>Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {running.map((s) => (
                    <tr key={s.id} title={statusInfo(s)?.label}>
                      <td className="usage-session">{sessionLabel(s)}</td>
                      <td>{s.usage?.model ?? '–'}</td>
                      <td>{s.usage?.contextPct !== undefined ? formatPct(s.usage.contextPct) : '–'}</td>
                      <td>{s.usage?.costUsd !== undefined ? formatCost(s.usage.costUsd) : '–'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        ))}
        <div className="modal-actions">
          <button type="button" className="btn primary" autoFocus onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
