import { useState } from 'react'
import { useCore } from './CoreContext'
import type { Host } from './sessions'

/** 'usage': hooks are already installed; the dialog adds usage recording. */
export type HooksMode = 'install' | 'update' | 'remove' | 'usage'

/** Confirm dialog for installing, updating or removing the Claude status hooks on a host. */
export function HooksDialog({
  host,
  mode,
  onClose
}: {
  host: Host
  mode: HooksMode
  onClose: () => void
}): React.JSX.Element {
  const { client } = useCore()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const remove = mode === 'remove'
  const [usage, setUsage] = useState(true)

  const run = (): void => {
    if (!client) return
    setBusy(true)
    setError(null)
    client
      .request(remove ? 'hosts.removeHooks' : 'hosts.installHooks', remove ? { host: host.id } : { host: host.id, usage })
      .then(onClose)
      .catch((e: Error) => {
        setBusy(false)
        setError(e.message)
      })
  }

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div
        className="modal"
        role="dialog"
        aria-label={remove ? 'Remove Claude status hooks' : 'Install Claude status hooks'}
        onKeyDown={(e) => e.key === 'Escape' && !busy && onClose()}
      >
        {remove ? (
          <>
            <h2>Remove Claude status hooks on {host.name}?</h2>
            <p className="help">
              This removes only the harness hook entries from <code>&lt;CLAUDE_CONFIG_DIR or ~/.claude&gt;/settings.json</code>{' '}
              on that host, and deletes <code>~/.claude/claude-harness/claude-status.sh</code>. Your other settings and
              hooks are left alone. You can install them again at any time.
            </p>
          </>
        ) : (
          <>
            <h2>
              {mode === 'usage'
                ? `Record Claude usage on ${host.name}?`
                : `${mode === 'update' ? 'Update' : 'Install'} Claude status hooks on ${host.name}?`}
            </h2>
            <p className="help">This lets the app show whether Claude is working, waiting or idle in each session. On that host it will:</p>
            <ul className="hooks-list">
              <li>
                add hook entries to <code>&lt;CLAUDE_CONFIG_DIR or ~/.claude&gt;/settings.json</code>, after saving a
                backup next to it
              </li>
              <li>
                write a small script to <code>~/.claude/claude-harness/claude-status.sh</code>
              </li>
            </ul>
            <p className="help">
              The hooks never affect Claude: the script always exits successfully and prints nothing. You can remove
              them at any time.
            </p>
            <label className="usage-check">
              <input type="checkbox" checked={usage} disabled={busy} onChange={(e) => setUsage(e.target.checked)} />
              <span>Also record Claude usage (plan limits, cost, context)</span>
            </label>
            <p className="help">
              Adds a status line command that saves what Claude shows its status line, then runs your own status line
              unchanged. Your current status line is restored when this is turned off.
            </p>
          </>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="modal-actions">
          <button type="button" className="btn" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className={`btn ${remove ? '' : 'primary'}`}
            autoFocus
            disabled={busy || !client}
            onClick={run}
          >
            {busy ? 'Working…' : remove ? 'Remove' : 'Install'}
          </button>
        </div>
      </div>
    </div>
  )
}
