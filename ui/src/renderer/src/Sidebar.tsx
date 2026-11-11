import { useEffect, useRef, useState } from 'react'
import { useCore } from './CoreContext'
import { displayPath, sessionLabel, sortSessions, truncateMiddle, type Host, type Session } from './sessions'

interface MenuState {
  x: number
  y: number
  host: Host
  session: Session
  confirmKill: boolean
}

function ContextMenu({
  menu,
  onRename,
  onKill,
  onChange,
  onClose
}: {
  menu: MenuState
  onRename: () => void
  onKill: () => void
  onChange: (m: MenuState) => void
  onClose: () => void
}): React.JSX.Element {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const down = (e: MouseEvent): void => {
      if (!ref.current?.contains(e.target as Node)) onClose()
    }
    const key = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('mousedown', down)
    window.addEventListener('keydown', key)
    window.addEventListener('blur', onClose)
    window.addEventListener('resize', onClose)
    return () => {
      window.removeEventListener('mousedown', down)
      window.removeEventListener('keydown', key)
      window.removeEventListener('blur', onClose)
      window.removeEventListener('resize', onClose)
    }
  }, [onClose])
  return (
    <div className="ctx-menu" ref={ref} style={{ left: menu.x, top: menu.y }} role="menu">
      {menu.confirmKill ? (
        <>
          <div className="ctx-confirm">Kill “{sessionLabel(menu.session)}”? This ends its processes.</div>
          <button className="ctx-item danger" role="menuitem" autoFocus onClick={onKill}>
            Kill
          </button>
          <button className="ctx-item" role="menuitem" onClick={onClose}>
            Cancel
          </button>
        </>
      ) : (
        <>
          <button className="ctx-item" role="menuitem" autoFocus onClick={onRename}>
            Rename…
          </button>
          <button
            className="ctx-item danger"
            role="menuitem"
            onClick={() => onChange({ ...menu, confirmKill: true })}
          >
            Kill session
          </button>
        </>
      )}
    </div>
  )
}

function RenameInput({
  session,
  onDone
}: {
  session: Session
  onDone: (name: string | null) => void
}): React.JSX.Element {
  const [v, setV] = useState(session.harnessName)
  return (
    <input
      className="rename-input"
      autoFocus
      value={v}
      placeholder={sessionLabel({ ...session, harnessName: '' })}
      aria-label="Session name"
      onChange={(e) => setV(e.target.value)}
      onFocus={(e) => e.currentTarget.select()}
      onClick={(e) => e.stopPropagation()}
      onBlur={() => onDone(null)}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Enter') onDone(v.trim())
        else if (e.key === 'Escape') onDone(null)
      }}
    />
  )
}

export function Sidebar(): React.JSX.Element {
  const { state, dispatch, client, connected, status, statusIsError } = useCore()
  const [menu, setMenu] = useState<MenuState | null>(null)
  const [renaming, setRenaming] = useState<{ host: string; id: string } | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const act = (p: Promise<unknown>): void => {
    setActionError(null)
    p.catch((e: Error) => setActionError(e.message))
  }

  const onListKey = (e: React.KeyboardEvent): void => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
    const rows = Array.from(listRef.current?.querySelectorAll<HTMLElement>('[data-row]') ?? [])
    if (!rows.length) return
    e.preventDefault()
    const i = rows.indexOf(document.activeElement as HTMLElement)
    const next = e.key === 'ArrowDown' ? Math.min(rows.length - 1, i + 1) : Math.max(0, i < 0 ? 0 : i - 1)
    rows[next].focus()
  }

  const defaultHost = state.active?.host

  return (
    <aside className="sidebar">
      <header className="sidebar-header">
        <span className="sidebar-title">SESSIONS</span>
        <button
          className="icon-button"
          disabled={!connected}
          title="New terminal"
          aria-label="New terminal"
          onClick={() => dispatch({ type: 'openDialog', host: defaultHost })}
        >
          +
        </button>
      </header>
      <div className="sidebar-body" ref={listRef} onKeyDown={onListKey}>
        {state.hosts.length === 0 && <p className="muted">{connected ? 'No hosts' : 'Waiting for core…'}</p>}
        {state.hosts.map((host) => {
          const sessions = sortSessions(state.sessions[host.id] ?? [])
          const ok = host.state === 'connected'
          return (
            <section className="host-group" key={host.id}>
              <div className="host-header">
                <span
                  className={`dot ${ok ? 'ok' : 'bad'}`}
                  title={ok ? 'Connected' : `Unavailable${host.error ? `: ${host.error}` : ''}`}
                  aria-label={ok ? 'connected' : 'unavailable'}
                />
                <span className="host-name">{host.name}</span>
                <span className="host-count">{ok ? sessions.length : ''}</span>
                <button
                  className="icon-button small"
                  disabled={!ok}
                  title={`New terminal on ${host.name}`}
                  aria-label={`New terminal on ${host.name}`}
                  onClick={() => dispatch({ type: 'openDialog', host: host.id })}
                >
                  +
                </button>
              </div>
              {!ok && <p className="host-error">{host.error || 'Host unavailable'}</p>}
              {ok && sessions.length === 0 && (
                <p className="muted empty">No sessions. Click + to open a terminal</p>
              )}
              {ok &&
                sessions.map((s) => {
                  const isActive = state.active?.host === host.id && state.active.session === s.id
                  const path = displayPath(s.cwd, host.home)
                  const isRenaming = renaming?.host === host.id && renaming.id === s.id
                  const attach = (): void => dispatch({ type: 'attach', ref: { host: host.id, session: s.id } })
                  return (
                    <div
                      key={s.id}
                      data-row
                      role="button"
                      tabIndex={0}
                      className={`row${isActive ? ' active' : ''}`}
                      aria-current={isActive ? 'true' : undefined}
                      onClick={() => !isRenaming && attach()}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && !isRenaming) attach()
                      }}
                      onContextMenu={(e) => {
                        e.preventDefault()
                        setMenu({ x: e.clientX, y: e.clientY, host, session: s, confirmKill: false })
                      }}
                    >
                      <div className="row-top">
                        {isRenaming ? (
                          <RenameInput
                            session={s}
                            onDone={(name) => {
                              setRenaming(null)
                              if (name !== null && client)
                                act(client.request('sessions.rename', { host: host.id, session: s.id, name }))
                            }}
                          />
                        ) : (
                          <span className="row-label">{sessionLabel(s)}</span>
                        )}
                        {!s.managed && <span className="tag">external</span>}
                      </div>
                      <div className="row-path" title={s.cwd}>
                        {truncateMiddle(path, 36)}
                      </div>
                    </div>
                  )
                })}
            </section>
          )
        })}
      </div>
      {actionError && <div className="action-error">{actionError}</div>}
      <footer className={statusIsError ? 'status-line error' : 'status-line'}>{status}</footer>
      {menu && (
        <ContextMenu
          menu={menu}
          onChange={setMenu}
          onClose={() => setMenu(null)}
          onRename={() => {
            setRenaming({ host: menu.host.id, id: menu.session.id })
            setMenu(null)
          }}
          onKill={() => {
            const m = menu
            setMenu(null)
            if (client) act(client.request('sessions.kill', { host: m.host.id, session: m.session.id }))
          }}
        />
      )}
    </aside>
  )
}
