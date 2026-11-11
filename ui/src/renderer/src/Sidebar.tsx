import { useEffect, useRef, useState } from 'react'
import { useCore } from './CoreContext'
import { UpdateBanner } from './UpdateBanner'
import { HooksDialog, type HooksMode } from './HooksDialog'
import { StatusBadge } from './StatusBadge'
import { canRemoveHooks, hooksNotice, orderSessions, waitingCount } from './claudeStatus'
import { ATTENTION_SORT_KEY, loadFlag, NOTIFICATIONS_KEY, saveFlag } from './prefs'
import { loadCollapsed, saveCollapsed, toggleCollapsed } from './collapse'
import {
  canCreateOn,
  canReconnect,
  displayPath,
  hostStatusText,
  reconnectText,
  sessionLabel,
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
  onRemoveHooks,
  onClose
}: {
  menu: HostMenuState
  onChange: (m: HostMenuState) => void
  onReconnect: () => void
  onRemove: () => void
  onRemoveHooks: () => void
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
          {menu.host.kind === 'ssh' && (
            <button
              className="ctx-item"
              role="menuitem"
              autoFocus
              disabled={!canReconnect(menu.host)}
              onClick={onReconnect}
            >
              Reconnect
            </button>
          )}
          {canRemoveHooks(menu.host) && (
            <button className="ctx-item" role="menuitem" autoFocus={menu.host.kind !== 'ssh'} onClick={onRemoveHooks}>
              Remove Claude status hooks…
            </button>
          )}
          {menu.host.kind === 'ssh' && (
            <button
              className="ctx-item danger"
              role="menuitem"
              onClick={() => onChange({ ...menu, confirmRemove: true })}
            >
              Remove host…
            </button>
          )}
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

function SettingsMenu({
  x,
  y,
  notify,
  onToggleNotify,
  onClose
}: {
  x: number
  y: number
  notify: boolean
  onToggleNotify: () => void
  onClose: () => void
}): React.JSX.Element {
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(ref, onClose)
  return (
    <div className="ctx-menu" ref={ref} style={{ left: x, top: y }} role="menu">
      <button className="ctx-item" role="menuitemcheckbox" aria-checked={notify} autoFocus onClick={onToggleNotify}>
        {notify ? '✓ ' : ''}Notifications
      </button>
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
  // Host groups the user folded away (persisted).
  const [folded, setFolded] = useState<Set<string>>(() => loadCollapsed())
  const toggleFold = (id: string): void =>
    setFolded((f) => {
      const n = toggleCollapsed(f, id)
      saveCollapsed(n)
      return n
    })
  // One-second tick while any host is counting down to an automatic retry.
  const [now, setNow] = useState(() => Date.now())
  const counting = state.hosts.some((h) => h.state === 'disconnected' && h.retryAt !== undefined)
  useEffect(() => {
    if (!counting) return
    setNow(Date.now())
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [counting])
  const [attentionSort, setAttentionSort] = useState(() => loadFlag(ATTENTION_SORT_KEY, false))
  const [notify, setNotify] = useState(() => loadFlag(NOTIFICATIONS_KEY, true))
  const [settings, setSettings] = useState<{ x: number; y: number } | null>(null)
  const [hooksDialog, setHooksDialog] = useState<{ host: Host; mode: HooksMode } | null>(null)
  // Hosts whose hooks notice the user dismissed with "Not now" (this run only).
  const [hooksDismissed, setHooksDismissed] = useState<Set<string>>(new Set())
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
          aria-pressed={attentionSort}
          title="Needs attention first"
          aria-label="Needs attention first"
          onClick={() =>
            setAttentionSort((v) => {
              saveFlag(ATTENTION_SORT_KEY, !v)
              return !v
            })
          }
        >
          ◉
        </button>
        <button
          className="icon-button"
          title="Settings"
          aria-label="Settings"
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect()
            setSettings({ x: Math.max(4, r.right - 200), y: r.bottom + 2 })
          }}
        >
          ⋯
        </button>
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
          const sessions = orderSessions(state.sessions[host.id] ?? [], attentionSort)
          const waiting = waitingCount(sessions)
          const notice = hooksNotice(host)
          const ok = host.state === 'connected'
          const retrying = host.state === 'disconnected' && host.retryAt !== undefined
          const statusText = retrying ? reconnectText(host.retryAt!, now) : hostStatusText(host)
          const isFolded = folded.has(host.id)
          const isSsh = host.kind === 'ssh'
          const hasMenu = isSsh || canRemoveHooks(host)
          // A changed key re-opens the panel: collapse is per host and fingerprint.
          const trustId = `${host.id}|${host.hostKey?.fingerprint ?? ''}`
          const openHostMenu = (x: number, y: number): void => {
            setMenu(null)
            setHostMenu({ x, y, host, confirmRemove: false })
          }
          return (
            <section className="host-group" data-testid="host-group" key={host.id}>
              <div
                className="host-header"
                onContextMenu={(e) => {
                  if (!hasMenu) return
                  e.preventDefault()
                  openHostMenu(e.clientX, e.clientY)
                }}
              >
                <button
                  className="chevron"
                  aria-expanded={!isFolded}
                  aria-label={`${isFolded ? 'Expand' : 'Collapse'} ${host.name}`}
                  onClick={() => toggleFold(host.id)}
                >
                  <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" className={isFolded ? 'folded' : ''}>
                    <path d="M2 3.5 5 6.5 8 3.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
                  </svg>
                </button>
                <span
                  className={`dot ${ok ? 'ok' : host.state === 'connecting' ? 'pending' : 'bad'}`}
                  title={ok ? 'Connected' : statusText}
                  aria-label={host.state.replace(/_/g, ' ')}
                />
                <span className="host-name" title={host.target} onClick={() => toggleFold(host.id)}>
                  {host.name}
                </span>
                {isFolded && waiting > 0 && (
                  <span className="waiting-badge" title={`${waiting} waiting`} aria-label={`${waiting} waiting`}>
                    {waiting}
                  </span>
                )}
                <span className="host-count">{ok ? sessions.length : ''}</span>
                {hasMenu && (
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
              {ok && notice && !hooksDismissed.has(host.id) && (
                <div className="hooks-notice" role="status">
                  <span>
                    {notice === 'install'
                      ? "Claude status hooks aren't installed"
                      : 'Claude status hooks are out of date'}
                  </span>
                  <button className="btn small" onClick={() => setHooksDialog({ host, mode: notice })}>
                    {notice === 'install' ? 'Install…' : 'Update…'}
                  </button>
                  <button className="link-button" onClick={() => setHooksDismissed((d) => new Set(d).add(host.id))}>
                    Not now
                  </button>
                </div>
              )}
              {host.state === 'connecting' && (
                <p className="host-status connecting" role="status">
                  <span className="spinner" aria-hidden="true" />
                  {statusText}
                </p>
              )}
              {(host.state === 'unavailable' || host.state === 'disconnected' || host.state === 'error') && (
                <div className="host-status error">
                  <span className="host-error-text" title={retrying ? host.error : undefined}>
                    {statusText}
                  </span>
                  {canReconnect(host) && (
                    <button
                      className="btn small"
                      onClick={() => client && act(client.request('hosts.connect', { host: host.id }))}
                    >
                      {retrying ? 'Reconnect now' : 'Reconnect'}
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
              {ok && !isFolded && sessions.length === 0 && (
                <p className="muted empty">No sessions. Click + to open a terminal</p>
              )}
              {ok &&
                !isFolded &&
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
                        <StatusBadge session={s} />
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
          onRemoveHooks={() => {
            const h = hostMenu.host
            setHostMenu(null)
            setHooksDialog({ host: h, mode: 'remove' })
          }}
          onRemove={() => {
            const h = hostMenu.host
            setHostMenu(null)
            if (client) act(client.request('hosts.remove', { host: h.id }))
          }}
        />
      )}
      {settings && (
        <SettingsMenu
          x={settings.x}
          y={settings.y}
          notify={notify}
          onToggleNotify={() => {
            const v = !notify
            saveFlag(NOTIFICATIONS_KEY, v)
            setNotify(v)
            setSettings(null)
            if (v && typeof Notification !== 'undefined' && Notification.permission === 'default')
              void Notification.requestPermission()
          }}
          onClose={() => setSettings(null)}
        />
      )}
      {hooksDialog && (
        <HooksDialog host={hooksDialog.host} mode={hooksDialog.mode} onClose={() => setHooksDialog(null)} />
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
