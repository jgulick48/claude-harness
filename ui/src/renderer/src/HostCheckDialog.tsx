import { useCallback, useEffect, useState } from 'react'
import { useCore } from './CoreContext'
import { needsFix, overallStatus, hooksNeedInstall, statusView, summarize } from './hostCheck'
import type { Host, HostReport } from './sessions'

function CopyButton({ text }: { text: string }): React.JSX.Element {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className="btn small"
      onClick={() => {
        void window.harness.writeClipboard(text).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        })
      }}
    >
      {done ? 'Copied' : 'Copy'}
    </button>
  )
}

/** Runs `hosts.check` and shows the setup report. Never runs a fix itself. */
export function HostCheckDialog({
  host,
  onClose,
  onInstallHooks
}: {
  host: Host
  onClose: () => void
  onInstallHooks: () => void
}): React.JSX.Element {
  const { client } = useCore()
  const [report, setReport] = useState<HostReport | null>(null)
  const [running, setRunning] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const run = useCallback((): void => {
    if (!client) {
      setRunning(false)
      setError('Not connected to the core')
      return
    }
    setRunning(true)
    setError(null)
    client
      .request<HostReport>('hosts.check', { host: host.id })
      .then((r) => setReport(r))
      .catch((e: Error) => setError(e.message))
      .finally(() => setRunning(false))
  }, [client, host.id])

  useEffect(() => run(), [run])

  const items = report?.items ?? []
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        className="modal check-dialog"
        role="dialog"
        aria-label={`Setup check · ${host.name}`}
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
      >
        <h2>Setup check · {host.name}</h2>
        {running && (
          <p className="check-running" role="status">
            <span className="spinner" aria-hidden="true" />
            Checking…
          </p>
        )}
        {!running && report && (
          <p className={`check-summary ${overallStatus(items)}`} data-testid="check-summary">
            {summarize(items)}
          </p>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {report && (
          <ul className="check-list" aria-busy={running}>
            {items.map((i) => {
              const v = statusView(i.status)
              return (
                <li key={i.id} className={`check-item ${i.status}`} data-testid={`check-${i.id}`}>
                  <span className={`check-status ${i.status}`}>
                    <span aria-hidden="true">{v.icon}</span> {v.text}
                  </span>
                  <div className="check-body">
                    <div className="check-label">{i.label}</div>
                    <div className="check-detail">{i.detail}</div>
                    {needsFix(i) && i.fix && <div className="check-fix">{i.fix}</div>}
                    {needsFix(i) && i.fixCommand && (
                      <div className="check-cmd">
                        <code>{i.fixCommand}</code>
                        <CopyButton text={i.fixCommand} />
                      </div>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
        <p className="help">The app never runs these commands. Copy one and run it yourself.</p>
        <div className="modal-actions">
          {hooksNeedInstall(report) && (
            <button type="button" className="btn" onClick={onInstallHooks}>
              Install hooks…
            </button>
          )}
          <button type="button" className="btn" disabled={running} onClick={run}>
            Re-run
          </button>
          <button type="button" className="btn primary" autoFocus onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
