import { useEffect, useState } from 'react'
import { useCore } from './CoreContext'
import { sessionLabel, type Host, type Session } from './sessions'
import { statusInfo } from './claudeStatus'
import {
  groupByHost,
  MAX_PROMPT_BYTES,
  promptBytes,
  promptTargets,
  selKey,
  type PromptCandidate
} from './sendPrompt'

const IS_MAC = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform)

type Result = { ok: true } | { ok: false; error: string }

/**
 * Types one prompt into several sessions. `selection` (keys from selKey) is
 * what the user chose; statuses are read live, and the core checks them again
 * when it sends, so a session that became busy reports "busy" instead.
 */
export function SendPromptDialog({
  hosts,
  sessions,
  selection,
  onClose,
  onSent
}: {
  hosts: readonly Host[]
  sessions: Record<string, Session[]>
  selection: ReadonlySet<string>
  onClose: () => void
  /** Every target got the prompt. */
  onSent: () => void
}): React.JSX.Element {
  const { client } = useCore()
  const [text, setText] = useState('')
  const [allowWaiting, setAllowWaiting] = useState(false)
  const [busy, setBusy] = useState(false)
  // Set once sent: what happened to each target that was tried.
  const [results, setResults] = useState<Record<string, Result> | null>(null)

  const candidates: PromptCandidate[] = hosts.flatMap((h) =>
    (sessions[h.id] ?? []).map((session) => ({ host: h.id, hostName: h.name, session }))
  )
  const { send, skip } = promptTargets(candidates, selection, allowWaiting)
  const shown = [
    ...send.map((c) => ({ c, reason: null as string | null })),
    ...skip.map((s) => ({ c: s.target, reason: s.reason }))
  ]
  const groups = groupByHost(shown.map((s) => ({ ...s, host: s.c.host, hostName: s.c.hostName }))).map((g) => ({
    ...g,
    // Keep the sidebar's order within a host.
    items: [...g.items].sort(
      (a, b) =>
        candidates.indexOf(a.c) - candidates.indexOf(b.c)
    )
  }))
  const tooLong = promptBytes(text) > MAX_PROMPT_BYTES
  const canSend = !busy && !results && send.length > 0 && text.trim() !== '' && !tooLong

  const confirm = (): void => {
    if (!canSend || !client) return
    const targets = send
    setBusy(true)
    void Promise.all(
      targets.map(async (c): Promise<[string, Result]> => {
        try {
          await client.request('sessions.sendPrompt', {
            host: c.host,
            session: c.session.id,
            text,
            allowWaiting
          })
          return [selKey(c.host, c.session.id), { ok: true }]
        } catch (e) {
          return [selKey(c.host, c.session.id), { ok: false, error: (e as Error).message }]
        }
      })
    ).then((all) => {
      setBusy(false)
      setResults(Object.fromEntries(all))
      if (all.every(([, r]) => r.ok)) onSent()
    })
  }

  // Esc closes, wherever focus is (the textarea is disabled once sent).
  useEffect(() => {
    const key = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [busy, onClose])

  const sentCount = results ? Object.values(results).filter((r) => r.ok).length : 0
  const failedCount = results ? Object.keys(results).length - sentCount : 0

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div
        className="modal prompt-dialog"
        role="dialog"
        aria-label="Send prompt to sessions"
      >
        <h2>Send prompt to sessions</h2>
        <label>
          Prompt
          <textarea
            className="prompt-text"
            data-testid="prompt-text"
            autoFocus
            rows={5}
            value={text}
            disabled={busy || !!results}
            placeholder="Text to type into Claude in each session"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (IS_MAC ? e.metaKey : e.ctrlKey)) {
                e.preventDefault()
                confirm()
              }
            }}
          />
        </label>
        {tooLong && <p className="form-error">The prompt is longer than {MAX_PROMPT_BYTES / 1024} KiB.</p>}
        <label className="usage-check">
          <input
            type="checkbox"
            checked={allowWaiting}
            disabled={busy || !!results}
            onChange={(e) => setAllowWaiting(e.target.checked)}
          />
          <span>Include sessions waiting for input</span>
        </label>
        <p className="help">Text typed into a waiting session answers its question or permission prompt.</p>

        <div className="prompt-targets" data-testid="prompt-targets">
          {shown.length === 0 && (
            <p className="muted">No sessions to send to. Select sessions in the sidebar, or wait for one to be idle.</p>
          )}
          {groups.map((g) => (
            <div key={g.host} className="prompt-group">
              <div className="prompt-host">{g.hostName}</div>
              <ul>
                {g.items.map(({ c, reason }) => {
                  const key = selKey(c.host, c.session.id)
                  const r = results?.[key]
                  const info = statusInfo(c.session)
                  return (
                    <li
                      key={key}
                      data-testid="prompt-target"
                      data-state={r ? (r.ok ? 'sent' : 'failed') : reason === null ? 'send' : 'skip'}
                    >
                      <span className="prompt-name">{sessionLabel(c.session)}</span>
                      <span className="prompt-status">{info ? info.label : 'No Claude'}</span>
                      <span className={`prompt-outcome${r && !r.ok ? ' failed' : ''}${r?.ok ? ' sent' : ''}`}>
                        {r ? (r.ok ? '✓ Sent' : `Failed: ${r.error}`) : reason === null ? 'Will receive the prompt' : `Skipped: ${reason}`}
                      </span>
                    </li>
                  )
                })}
              </ul>
            </div>
          ))}
        </div>

        {results && (
          <p className={failedCount ? 'form-error' : 'help'} role="status" data-testid="prompt-summary">
            {failedCount
              ? `Sent to ${sentCount}, failed for ${failedCount}.`
              : `Sent to ${sentCount} session${sentCount === 1 ? '' : 's'}.`}
          </p>
        )}
        <div className="modal-actions">
          <button className="btn" onClick={onClose} disabled={busy}>
            Close
          </button>
          {!results && (
            <button className="btn primary" data-testid="prompt-send" disabled={!canSend} onClick={confirm}>
              {busy ? 'Sending…' : `Send to ${send.length} session${send.length === 1 ? '' : 's'}`}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
