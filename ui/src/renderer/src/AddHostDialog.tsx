import { useEffect, useState } from 'react'
import { useCore } from './CoreContext'

export function AddHostDialog(): React.JSX.Element {
  const { dispatch, client } = useCore()
  const [target, setTarget] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const close = (): void => dispatch({ type: 'closeAddHost' })
  useEffect(() => {
    const key = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') dispatch({ type: 'closeAddHost' })
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [dispatch])

  const submit = async (e: React.FormEvent): Promise<void> => {
    e.preventDefault()
    const t = target.trim()
    if (!client || !t || busy) return
    setBusy(true)
    setError(null)
    try {
      const params: Record<string, unknown> = { target: t }
      if (name.trim()) params.name = name.trim()
      await client.request('hosts.add', params)
      // The new host arrives through hosts.changed.
      dispatch({ type: 'closeAddHost' })
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <form className="modal" role="dialog" aria-label="Add host" onSubmit={submit}>
        <h2>Add host</h2>
        <label>
          Target
          <input
            autoFocus
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="devbox  or  me@devbox.lan:2222"
            spellCheck={false}
          />
          <span className="help">
            An ~/.ssh/config alias or user@host[:port]. Keys come from your SSH agent (Pageant on Windows).
          </span>
        </label>
        <label>
          Name (optional)
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Leave empty to use the target" />
        </label>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="modal-actions">
          <button type="button" className="btn" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={busy || !target.trim()}>
            {busy ? 'Adding…' : 'Add host'}
          </button>
        </div>
      </form>
    </div>
  )
}
