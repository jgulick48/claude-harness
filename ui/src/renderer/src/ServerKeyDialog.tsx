import { useState } from 'react'
import { authorizedKeysCommand } from './web/webPure'

function CopyButton({ text, label }: { text: string; label: string }): React.JSX.Element {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className="btn small"
      aria-label={label}
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

/** Web build only: the server's SSH public key, to authorize on each host. */
export function ServerKeyDialog({ sshPublicKey, onClose }: { sshPublicKey: string; onClose: () => void }): React.JSX.Element {
  const cmd = authorizedKeysCommand(sshPublicKey)
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        className="modal prompt-dialog"
        role="dialog"
        aria-label="Server SSH key"
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
      >
        <h2>Server SSH key</h2>
        <p className="help">
          This server connects to your hosts over SSH with this key. Add it on each host the server should reach,
          then add the host.
        </p>
        <div className="field">
          <label htmlFor="server-key">Public key</label>
          <textarea
            id="server-key"
            data-testid="server-key"
            readOnly
            rows={3}
            value={sshPublicKey}
            style={{ fontFamily: 'ui-monospace, monospace', fontSize: 12, resize: 'vertical' }}
            onFocus={(e) => e.currentTarget.select()}
          />
          <div className="modal-actions">
            <CopyButton text={sshPublicKey} label="Copy key" />
          </div>
        </div>
        <div className="field">
          <label htmlFor="server-key-cmd">Run on the host</label>
          <textarea
            id="server-key-cmd"
            data-testid="server-key-cmd"
            readOnly
            rows={3}
            value={cmd}
            style={{ fontFamily: 'ui-monospace, monospace', fontSize: 12, resize: 'vertical' }}
            onFocus={(e) => e.currentTarget.select()}
          />
          <div className="modal-actions">
            <CopyButton text={cmd} label="Copy command" />
          </div>
        </div>
        <div className="modal-actions">
          <button type="button" className="btn" autoFocus onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
