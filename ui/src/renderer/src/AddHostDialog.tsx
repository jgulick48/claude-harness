import { useEffect, useState } from 'react'
import { useCore } from './CoreContext'
import {
  applyImportResults,
  describeConfigHost,
  hasImportable,
  importAliases,
  selectedAliases,
  toggleAlias,
  type ConfigHost
} from './hostImport'

export function AddHostDialog(): React.JSX.Element {
  const { dispatch, client } = useCore()
  const [target, setTarget] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [configHosts, setConfigHosts] = useState<ConfigHost[]>([])
  const [selection, setSelection] = useState<Set<string>>(new Set())
  const [importErrors, setImportErrors] = useState<Record<string, string>>({})

  useEffect(() => {
    if (!client) return
    let stale = false
    client
      .request<ConfigHost[]>('hosts.configHosts')
      .then((l) => {
        if (!stale) setConfigHosts(Array.isArray(l) ? l : [])
      })
      .catch(() => {
        /* no ssh config to offer; free text still works */
      })
    return () => {
      stale = true
    }
  }, [client])

  const picked = selectedAliases(selection, configHosts)
  const importSelected = async (): Promise<void> => {
    if (!client || busy || picked.length === 0) return
    setBusy(true)
    setError(null)
    const results = await importAliases(picked, (alias) => client.request('hosts.add', { target: alias }))
    const next = applyImportResults(configHosts, selection, results)
    setConfigHosts(next.list)
    setSelection(next.selection)
    setImportErrors(next.errors)
    setBusy(false)
    if (Object.keys(next.errors).length === 0) dispatch({ type: 'closeAddHost' })
  }

  const close = (): void => dispatch({ type: 'closeAddHost' })
  useEffect(() => {
    const key = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && !e.defaultPrevented) dispatch({ type: 'closeAddHost' })
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
        {hasImportable(configHosts) && (
          <div className="import-list" role="group" aria-label="From ~/.ssh/config">
            <span className="import-title">From ~/.ssh/config</span>
            <div className="import-rows">
              {configHosts.map((h) => (
                <label key={h.alias} className={`import-row${h.added ? ' disabled' : ''}`}>
                  <input
                    type="checkbox"
                    disabled={h.added || busy}
                    checked={h.added || selection.has(h.alias)}
                    onChange={() => setSelection((sel) => toggleAlias(sel, h.alias, configHosts))}
                  />
                  <span>{h.alias}</span>
                  <span className="import-detail">{h.added ? 'added' : describeConfigHost(h)}</span>
                  {importErrors[h.alias] && (
                    <span className="import-error" role="alert">
                      {importErrors[h.alias]}
                    </span>
                  )}
                </label>
              ))}
            </div>
            <div className="import-actions">
              <button
                type="button"
                className="btn"
                disabled={busy || picked.length === 0}
                onClick={() => void importSelected()}
              >
                Add selected
              </button>
            </div>
          </div>
        )}
        <label>
          Target
          <input
            autoFocus={!hasImportable(configHosts)}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="devbox  or  me@devbox.lan:2222"
            spellCheck={false}
            list="ssh-config-aliases"
          />
          <datalist id="ssh-config-aliases">
            {configHosts
              .filter((h) => !h.added)
              .map((h) => (
                <option key={h.alias} value={h.alias} />
              ))}
          </datalist>
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
