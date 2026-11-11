import { useEffect, useRef, useState } from 'react'
import { useCore } from './CoreContext'
import { UpdateBanner } from './UpdateBanner'
import {
  canCreateOn,
  canReconnect,
  displayPath,
  hostStatusText,
  sessionLabel,
  sortSessions,
  trustKeyParams,
  truncateMiddle,
  type Host,
  type Session
} from './sessions'

interface MenuState {
  x: number
  y: number
  host: Host
  session: Session
  confirmKill: boolean
}

function useDismiss(ref: React.RefObject<HTMLElement | null>, onClose: () => void): void {
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
  }, [ref, onClose])
}

interface HostMenuState {
  x: number
  y: number
  host: Host
  confirmRemove: boolean
}

function HostMenu({
  menu,
  onChange,
  onReconnect,
  onRemove,
  onClose
}: {
  menu: HostMenuState
  onChange: (m: HostMenuState) => void
  onReconnect: () => void
  onRemove: () => void
  onClose: () => void
}): React.JSX.Element {
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(ref, onClose)
  return (
    <div className="ctx-menu" ref={ref} style={{ left: menu.x, top: menu.y }} role="menu">
      {menu.confirmRemove ? (
        <>
          <div className="ctx-confirm">
            Remove “{menu.host.name}”? Sessions keep running on the host.
          </div>
          <button className="ctx-item danger" role="menuitem" autoFocus onClick={onRemove}>
            Remove
          </button>
          <button className="ctx-item" role="menuitem" onClick={onClose}>
            Cancel
          </button>
        </>
      ) : (
        <>
          <button
            className="ctx-item"
            role="menuitem"
            autoFocus
            disabled={!canReconnect(menu.host)}
            onClick={onReconnect}
          >
            Reconnect
          </button>
          <button
            className="ctx-item danger"
            role="menuitem"
            onClick={() => onChange({ ...menu, confirmRemove: true })}
          >
            Remove host…
          </button>
        </>
      )}
    </div>
  )
}

function TrustPanel({
  host,
  onTrust,
  onCancel
}: {
  host: Host
  onTrust: (p: { host: string; fingerprint: string }) => void
  onCancel: () => void
}): React.JSX.Element | null {
  const key = host.hostKey
  const params = trustKeyParams(host)
  if (!key || !params) return null
  return (
    <div className="trust-panel" role="group" aria-label={`Verify host key for ${host.name}`}>
      <p>
        First connection to <strong>{key.address}</strong>. Verify this fingerprint before trusting:
      </p>
      <div className="fingerprint">
        <span className="fp-type">{key.keyType}</span>
        <span className="fp-value">{key.fingerprint}</span>
      </div>
      <div className="trust-actions">
        <button className="btn primary" onClick={() => onTrust(params)}>
          Trust and connect
        </button>
        <button className="btn" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  )
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
  useDismiss(ref, onClose)
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
  const [hostMenu, setHostMenu] = useState<HostMenuState | null>(null)
  // Hosts whose trust panel the user collapsed with Cancel (state is unchanged).
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
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
        <span className="header-actions">
        <button
          className="icon-button"
          disabled={!connected}
          title="Add host…"
          aria-label="Add host"
          onClick={() => dispatch({ type: 'openAddHost' })}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
            <rect x="2" y="2.5" width="12" height="4.5" rx="1" />
            <rect x="2" y="9" width="12" height="4.5" rx="1" />
            <circle cx="4.5" cy="4.75" r=".6" fill="currentColor" />
            <circle cx="4.5" cy="11.25" r=".6" fill="currentColor" />
          </svg>
        </button>
        <button
          className="icon-button"
          disabled={!connected}
          title="New terminal"
          aria-label="New terminal"
          onClick={() => dispatch({ type: 'openDialog', host: defaultHost })}
        >
          +
        </button>
        </span>
      </header>
      <div className="sidebar-body" ref={listRef} onKeyDown={onListKey}>
        {state.hosts.length === 0 && <p className="muted">{connected ? 'No hosts' : 'Waiting for core…'}</p>}
        {state.hosts.map((host) => {
          const sessions = sortSessions(state.sessions[host.id] ?? [])
          const ok = host.state === 'connected'
          const statusText = hostStatusText(host)
          const isSsh = host.kind === 'ssh'
          // A changed key re-opens the panel: collapse is per host and fingerprint.
          const trustId = `${host.id}|${host.hostKey?.fingerprint ?? ''}`
          const openHostMenu = (x: number, y: number): void => {
            setMenu(null)
            setHostMenu({ x, y, host, confirmRemove: false })
          }
          return (
            <section className="host-group" key={host.id}>
              <div
                className="host-header"
                onContextMenu={(e) => {
                  if (!isSsh) return
                  e.preventDefault()
                  openHostMenu(e.clientX, e.clientY)
                }}
              >
                <span
                  className={`dot ${ok ? 'ok' : host.state === 'connecting' ? 'pending' : 'bad'}`}
                  title={ok ? 'Connected' : statusText}
                  aria-label={host.state.replace(/_/g, ' ')}
                />
                <span className="host-name" title={host.target}>
                  {host.name}
                </span>
                <span className="host-count">{ok ? sessions.length : ''}</span>
                {isSsh && (
                  <button
                    className="icon-button small"
                    title={`Host actions for ${host.name}`}
                    aria-label={`Host actions for ${host.name}`}
                    onClick={(e) => {
                      const r = e.currentTarget.getBoundingClientRect()
                      openHostMenu(r.left, r.bottom + 2)
                    }}
                  >
                    ⋯
                  </button>
                )}
                <button
                  className="icon-button small"
                  disabled={!canCreateOn(host)}
                  title={`New terminal on ${host.name}`}
                  aria-label={`New terminal on ${host.name}`}
                  onClick={() => dispatch({ type: 'openDialog', host: host.id })}
                >
                  +
                </button>
              </div>
              {host.state === 'connecting' && (
                <p className="host-status connecting" role="status">
                  <span className="spinner" aria-hidden="true" />
                  {statusText}
                </p>
              )}
              {(host.state === 'unavailable' || host.state === 'disconnected' || host.state === 'error') && (
                <div className="host-status error">
                  <span className="host-error-text">{statusText}</span>
                  {canReconnect(host) && (
                    <button
                      className="btn small"
                      onClick={() => client && act(client.request('hosts.connect', { host: host.id }))}
                    >
                      Reconnect
                    </button>
                  )}
                </div>
              )}
              {host.state === 'host_key_unknown' &&
                (collapsed.has(trustId) ? (
                  <p className="host-status">
                    {statusText}.{' '}
                    <button
                      className="link-button"
                      onClick={() =>
                        setCollapsed((c) => {
                          const n = new Set(c)
                          n.delete(trustId)
                          return n
                        })
                      }
                    >
                      Review key
                    </button>
                  </p>
                ) : (
                  <TrustPanel
                    host={host}
                    onTrust={(p) => client && act(client.request('hosts.trustKey', p))}
                    onCancel={() => setCollapsed((c) => new Set(c).add(trustId))}
                  />
                ))}
              {ok && sessions.length === 0 && (
                <p className="muted empty">No sessions. Click + to open a terminal</p>
              )}
              {ok &&
                sessions.map((s) => {
                  const isActive = state.active?.host === host.id && state.active.session === s.id
                  const path = displayPath(s.cwd, host.home ?? '')
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
      <UpdateBanner />
      <footer className={statusIsError ? 'status-line error' : 'status-line'}>{status}</footer>
      {hostMenu && (
        <HostMenu
          menu={hostMenu}
          onChange={setHostMenu}
          onClose={() => setHostMenu(null)}
          onReconnect={() => {
            const h = hostMenu.host
            setHostMenu(null)
            if (client) act(client.request('hosts.connect', { host: h.id }))
          }}
          onRemove={() => {
            const h = hostMenu.host
            setHostMenu(null)
            if (client) act(client.request('hosts.remove', { host: h.id }))
          }}
        />
      )}
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
