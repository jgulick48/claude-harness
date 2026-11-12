import { useEffect, useState } from 'react'
import { useCore } from './CoreContext'
import { DirCombo } from './DirCombo'
import { loadString, RUN_AS_KEY_PREFIX, saveString } from './prefs'
import { canCreateOn, validRunAs, type Session } from './sessions'

export function NewTerminalDialog({ preferredHost }: { preferredHost?: string }): React.JSX.Element {
  const { state, dispatch, client } = useCore()
  const usable = state.hosts.filter(canCreateOn)
  const initial =
    usable.find((h) => h.id === preferredHost)?.id ?? usable.find((h) => h.id === state.active?.host)?.id ?? usable[0]?.id ?? ''
  const [host, setHost] = useState(initial)
  const [dir, setDir] = useState('~')
  const [name, setName] = useState('')
  const [runAs, setRunAs] = useState(() => loadString(RUN_AS_KEY_PREFIX + initial))
  const [command, setCommand] = useState<'shell' | 'claude'>('shell')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const close = (): void => dispatch({ type: 'closeDialog' })
  useEffect(() => {
    const key = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && !e.defaultPrevented) dispatch({ type: 'closeDialog' })
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [dispatch])

  const submit = async (e: React.FormEvent): Promise<void> => {
    e.preventDefault()
    if (!client || !host || busy) return
    const user = runAs.trim()
    if (user && !validRunAs(user)) {
      setError('Invalid user name')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const params: Record<string, unknown> = { host, dir: dir.trim() || '~', command }
      if (name.trim()) params.name = name.trim()
      if (user) params.runAs = user
      const s = await client.request<Session>('sessions.create', params)
      saveString(RUN_AS_KEY_PREFIX + host, user)
      dispatch({ type: 'upsertSession', host, session: s })
      dispatch({ type: 'attach', ref: { host, session: s.id } })
      dispatch({ type: 'closeDialog' })
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <form className="modal" role="dialog" aria-label="New terminal" onSubmit={submit}>
        <h2>New terminal</h2>
        <label>
          Host
          <select
            value={host}
            onChange={(e) => {
              setHost(e.target.value)
              setRunAs(loadString(RUN_AS_KEY_PREFIX + e.target.value))
            }}
          >
            {usable.map((h) => (
              <option key={h.id} value={h.id}>
                {h.name}
              </option>
            ))}
          </select>
        </label>
        <div className="field">
          <label htmlFor="new-term-dir">Directory</label>
          <DirCombo id="new-term-dir" host={host} value={dir} onChange={setDir} />
        </div>
        <label>
          Name (optional)
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Leave empty to show the directory"
          />
        </label>
        <label>
          Run as user (optional)
          <input
            value={runAs}
            onChange={(e) => setRunAs(e.target.value)}
            placeholder="e.g. deploy"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
          />
          <span className="field-hint">
            Switches with sudo -iu &lt;user&gt;. The directory is that user&apos;s; sudo may ask for a password in the
            terminal.
          </span>
        </label>
        <fieldset>
          <legend>Command</legend>
          <label className="radio">
            <input type="radio" name="cmd" checked={command === 'shell'} onChange={() => setCommand('shell')} />
            Shell
          </label>
          <label className="radio">
            <input type="radio" name="cmd" checked={command === 'claude'} onChange={() => setCommand('claude')} />
            Claude
          </label>
        </fieldset>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="modal-actions">
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={busy || !host}>
            {busy ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  )
}
