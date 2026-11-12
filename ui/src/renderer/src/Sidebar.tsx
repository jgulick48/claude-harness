import { useEffect, useRef, useState } from 'react'
import { claimFocus } from './focusClaims'
import { useCore } from './CoreContext'
import { UpdateBanner } from './UpdateBanner'
import { HooksDialog, type HooksMode } from './HooksDialog'
import { HostCheckDialog } from './HostCheckDialog'
import { canRunCheckFromStatus } from './hostCheck'
import { StatusBadge } from './StatusBadge'
import { canRemoveHooks, hooksNotice, orderSessions, statusInfo, waitingCount } from './claudeStatus'
import {
  ATTENTION_SORT_KEY,
  loadFlag,
  loadNumber,
  NOTIFICATIONS_KEY,
  saveFlag,
  saveNumber,
  SIDEBAR_RAIL_KEY,
  SIDEBAR_WIDTH_KEY
} from './prefs'
import { openAppearance } from './appearance'
import { visibleRefs } from './layout'
import { highlightParts, matchCandidate } from './sidebarFilter'
import {
  clampWidth,
  moveNav,
  navRefs,
  nthRef,
  RAIL_WIDTH,
  SIDEBAR_DEFAULT,
  SIDEBAR_MAX,
  SIDEBAR_MIN,
  shortcutFor,
  type NavRef
} from './sidebarNav'
import { loadCollapsed, saveCollapsed, toggleCollapsed } from './collapse'
import {
  canCreateOn,
  canReconnect,
  displayPath,
  hostStatusText,
  reconnectText,
  runAsSuffix,
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
  onCheck,
  onRemove,
  onRemoveHooks,
  onClose
}: {
  menu: HostMenuState
  onChange: (m: HostMenuState) => void
  onReconnect: () => void
  onCheck: () => void
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
          <button className="ctx-item" role="menuitem" autoFocus onClick={onCheck}>
            Check host…
          </button>
          {menu.host.kind === 'ssh' && (
            <button
              className="ctx-item"
              role="menuitem"
              disabled={!canReconnect(menu.host)}
              onClick={onReconnect}
            >
              Reconnect
            </button>
          )}
          {canRemoveHooks(menu.host) && (
            <button className="ctx-item" role="menuitem" onClick={onRemoveHooks}>
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
  onAppearance,
  onClose
}: {
  x: number
  y: number
  notify: boolean
  onToggleNotify: () => void
  onAppearance: () => void
  onClose: () => void
}): React.JSX.Element {
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(ref, onClose)
  return (
    <div className="ctx-menu" ref={ref} style={{ left: x, top: y }} role="menu">
      <button className="ctx-item" role="menuitemcheckbox" aria-checked={notify} autoFocus onClick={onToggleNotify}>
        {notify ? '✓ ' : ''}Notifications
      </button>
      <button className="ctx-item" role="menuitem" onClick={onAppearance}>
        Appearance…
      </button>
    </div>
  )
}

function ContextMenu({
  menu,
  open,
  onRename,
  onDisconnect,
  onKill,
  onSplit,
  onChange,
  onClose
}: {
  menu: MenuState
  /** The session is shown in a pane, so it can be disconnected. */
  open: boolean
  onSplit: (direction: 'right' | 'down') => void
  onRename: () => void
  onDisconnect: () => void
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
          <button className="ctx-item" role="menuitem" autoFocus onClick={() => onSplit('right')}>
            Open in split right
          </button>
          <button className="ctx-item" role="menuitem" onClick={() => onSplit('down')}>
            Open in split down
          </button>
          <button className="ctx-item" role="menuitem" onClick={onRename}>
            Rename…
          </button>
          {open && (
            <button
              className="ctx-item"
              role="menuitem"
              title="Close its panes; the tmux session keeps running"
              onClick={onDisconnect}
            >
              Disconnect
            </button>
          )}
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

function RailView({
  groups,
  active,
  visible,
  onToggle,
  onAttach,
  onContext
}: {
  groups: { host: Host; ok: boolean; items: { s: Session; label: string }[] }[]
  active: { host: string; session: string } | null
  visible: Set<string>
  onToggle: () => void
  onAttach: (r: NavRef) => void
  onContext: (e: React.MouseEvent, host: Host, session: Session) => void
}): React.JSX.Element {
  return (
    <>
      <header className="rail-header">
        <button
          className="icon-button"
          title="Expand sidebar (Ctrl/Cmd+B)"
          aria-label="Expand sidebar"
          onClick={onToggle}
        >
          »
        </button>
      </header>
      <div className="rail-body" data-testid="rail">
        {groups.map(({ host, ok, items }) => (
          <div className="rail-group" key={host.id}>
            <span
              className={`dot ${ok ? 'ok' : host.state === 'connecting' ? 'pending' : 'bad'}`}
              title={`${host.name}: ${ok ? 'Connected' : hostStatusText(host)}`}
              aria-label={`${host.name} ${host.state.replace(/_/g, ' ')}`}
              role="img"
            />
            {items.map(({ s, label }) => {
              const info = statusInfo(s)
              const isActive = active?.host === host.id && active.session === s.id
              const isOpen = !isActive && visible.has(`${host.id}/${s.id}`)
              const text = (info ? `${label} — ${info.label}` : label) + runAsSuffix(s)
              return (
                <button
                  key={s.id}
                  data-testid="rail-item"
                  className={`rail-item${isActive ? ' active' : ''}${isOpen ? ' open' : ''}${info ? ` status-${info.status}` : ''}`}
                  title={`${text} (${host.name})`}
                  aria-label={text}
                  aria-current={isActive ? 'true' : undefined}
                  onClick={() => onAttach({ host: host.id, session: s.id })}
                  onContextMenu={(e) => onContext(e, host, s)}
                >
                  {info ? (
                    <span className="status-icon" aria-hidden="true">
                      {info.icon}
                    </span>
                  ) : (
                    <span className="rail-dot" aria-hidden="true" />
                  )}
                </button>
              )
            })}
          </div>
        ))}
      </div>
    </>
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
  const [checkHost, setCheckHost] = useState<Host | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const act = (p: Promise<unknown>): void => {
    setActionError(null)
    p.catch((e: Error) => setActionError(e.message))
  }

  const [rail, setRail] = useState(() => loadFlag(SIDEBAR_RAIL_KEY, false))
  const [width, setWidth] = useState(() => clampWidth(loadNumber(SIDEBAR_WIDTH_KEY, SIDEBAR_DEFAULT)))
  const [filter, setFilter] = useState('')
  const filterRef = useRef<HTMLInputElement>(null)
  const asideRef = useRef<HTMLElement>(null)
  const pendingFilterFocus = useRef(false)

  const query = rail ? '' : filter
  const filtering = query.trim() !== ''
  const groups = state.hosts.map((host) => {
    const ok = host.state === 'connected'
    const items = ok
      ? orderSessions(state.sessions[host.id] ?? [], attentionSort).flatMap((s) => {
          const label = sessionLabel(s)
          const path = displayPath(s.cwd, host.home ?? '')
          const m = matchCandidate(query, { label, path, host: host.name })
          return m ? [{ s, label, path, labelIndices: m.labelIndices }] : []
        })
      : []
    return { host, ok, items }
  })
  // Hosts with no matching sessions are hidden while a filter is active.
  const shownGroups = filtering ? groups.filter((g) => g.items.length > 0) : groups
  // Filtering shows every match even in a folded group; the rail ignores folds.
  const isFoldedHost = (id: string): boolean => !filtering && !rail && folded.has(id)
  const nav = navRefs(
    shownGroups.map((g) => ({ host: g.host.id, folded: isFoldedHost(g.host.id), sessions: g.items.map((i) => i.s.id) }))
  )
  const visible = new Set(visibleRefs(state).map((r) => `${r.host}/${r.session}`))
  const attachRef = (r: NavRef): void => dispatch({ type: 'attach', ref: r })
  const splitRef = (r: NavRef, direction: 'right' | 'down' = 'right'): void =>
    dispatch({ type: 'openInSplit', ref: r, direction })

  const toggleRail = (): void =>
    setRail((v) => {
      saveFlag(SIDEBAR_RAIL_KEY, !v)
      return !v
    })

  const focusTerminal = (): void => {
    const t = document.querySelector<HTMLElement>('.xterm-helper-textarea')
    if (t) t.focus()
    else (document.activeElement as HTMLElement | null)?.blur()
  }

  // Global shortcuts. Capture phase + stopPropagation so they also work while
  // a terminal has focus without the keystroke reaching the shell.
  const latest = useRef({ nav, toggleRail, attachRef })
  latest.current = { nav, toggleRail, attachRef }
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      const sc = shortcutFor(e)
      if (!sc) return
      if (sc.kind === 'nth') {
        if (document.querySelector('[role="dialog"]')) return
        const r = nthRef(latest.current.nav, sc.n)
        if (!r) return
        e.preventDefault()
        e.stopPropagation()
        latest.current.attachRef(r)
        return
      }
      e.preventDefault()
      e.stopPropagation()
      if (sc.kind === 'rail') {
        latest.current.toggleRail()
      } else if (filterRef.current) {
        // The filter may already be focused (no focusin): claim focus
        // explicitly so a terminal whose attach is still in flight won't take it.
        claimFocus()
        filterRef.current.focus()
        filterRef.current.select()
      } else {
        // Rail is showing: expand, then focus once the input exists.
        pendingFilterFocus.current = true
        latest.current.toggleRail()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [])
  useEffect(() => {
    if (!rail && pendingFilterFocus.current) {
      pendingFilterFocus.current = false
      filterRef.current?.focus()
    }
  }, [rail])

  const focusRow = (r: NavRef): void => {
    const rows = Array.from(listRef.current?.querySelectorAll<HTMLElement>('[data-row]') ?? [])
    rows.find((el) => el.dataset.host === r.host && el.dataset.session === r.session)?.focus()
  }

  const onListKey = (e: React.KeyboardEvent): void => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
    const el = document.activeElement as HTMLElement | null
    const cur = el?.dataset.row !== undefined ? { host: el.dataset.host ?? '', session: el.dataset.session ?? '' } : null
    const dir = e.key === 'ArrowDown' ? 1 : -1
    const next = moveNav(nav, cur, dir)
    if (!next) return
    e.preventDefault()
    if (cur && dir === -1 && next.host === cur.host && next.session === cur.session) {
      filterRef.current?.focus()
      return
    }
    focusRow(next)
  }

  const onFilterKey = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Escape') {
      e.preventDefault()
      if (filter) setFilter('')
      else focusTerminal()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (nav[0]) (e.shiftKey ? splitRef : attachRef)(nav[0])
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (nav[0]) focusRow(nav[0])
    }
  }

  // Dragging (and arrow keys on the handle) only change the width; the
  // terminal pane's ResizeObserver refits on its own.
  const commitWidth = (w: number): void => {
    const c = clampWidth(w)
    setWidth(c)
    saveNumber(SIDEBAR_WIDTH_KEY, c)
  }
  const onResizeDown = (e: React.PointerEvent<HTMLDivElement>): void => {
    if (e.button !== 0) return
    e.preventDefault()
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    const left = asideRef.current?.getBoundingClientRect().left ?? 0
    let last = width
    const move = (ev: PointerEvent): void => {
      last = clampWidth(ev.clientX - left)
      setWidth(last)
    }
    const up = (): void => {
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerup', up)
      el.removeEventListener('pointercancel', up)
      saveNumber(SIDEBAR_WIDTH_KEY, last)
    }
    el.addEventListener('pointermove', move)
    el.addEventListener('pointerup', up)
    el.addEventListener('pointercancel', up)
  }
  const onResizeKey = (e: React.KeyboardEvent): void => {
    const step = e.shiftKey ? 48 : 16
    if (e.key === 'ArrowLeft') commitWidth(width - step)
    else if (e.key === 'ArrowRight') commitWidth(width + step)
    else if (e.key === 'Home') commitWidth(SIDEBAR_MIN)
    else if (e.key === 'End') commitWidth(SIDEBAR_MAX)
    else return
    e.preventDefault()
  }

  const defaultHost = state.active?.host

  return (
    <aside
      className={`sidebar${rail ? ' rail' : ''}`}
      ref={asideRef}
      style={{ ['--sidebar-width' as string]: `${rail ? RAIL_WIDTH : width}px` }}
    >
      {rail ? (
        <RailView
          groups={groups}
          active={state.active}
          visible={visible}
          onToggle={toggleRail}
          onAttach={attachRef}
          onContext={(e, host, session) => {
            e.preventDefault()
            setMenu({ x: e.clientX, y: e.clientY, host, session, confirmKill: false })
          }}
        />
      ) : (
      <>
      <header className="sidebar-header">
        <span className="sidebar-title">SESSIONS</span>
        <span className="header-actions">
        <button
          className="icon-button"
          title="Collapse sidebar (Ctrl/Cmd+B)"
          aria-label="Collapse sidebar"
          onClick={toggleRail}
        >
          «
        </button>
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
        {filtering && shownGroups.length === 0 && (
          <p className="muted no-matches" role="status">
            No matches
          </p>
        )}
        {shownGroups.map(({ host, items }) => {
          const sessions = items.map((i) => i.s)
          const waiting = waitingCount(sessions)
          const notice = hooksNotice(host)
          const ok = host.state === 'connected'
          const retrying = host.state === 'disconnected' && host.retryAt !== undefined
          const statusText = retrying ? reconnectText(host.retryAt!, now) : hostStatusText(host)
          const isFolded = isFoldedHost(host.id)
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
                  {canRunCheckFromStatus(host.state) && (
                    <button className="link-button" onClick={() => setCheckHost(host)}>
                      Run setup check
                    </button>
                  )}
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
              {ok && !isFolded && sessions.length === 0 && !filtering && (
                <p className="muted empty">No sessions. Click + to open a terminal</p>
              )}
              {ok &&
                !isFolded &&
                items.map(({ s, label, labelIndices }) => {
                  const isActive = state.active?.host === host.id && state.active.session === s.id
                  const isOpen = !isActive && visible.has(`${host.id}/${s.id}`)
                  const path = displayPath(s.cwd, host.home ?? '')
                  const isRenaming = renaming?.host === host.id && renaming.id === s.id
                  const attach = (): void => dispatch({ type: 'attach', ref: { host: host.id, session: s.id } })
                  return (
                    <div
                      key={s.id}
                      data-row
                      data-host={host.id}
                      data-session={s.id}
                      data-open={isOpen ? 'true' : undefined}
                      role="button"
                      tabIndex={0}
                      className={`row${isActive ? ' active' : ''}${isOpen ? ' open' : ''}`}
                      aria-current={isActive ? 'true' : undefined}
                      onClick={() => !isRenaming && attach()}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && !isRenaming && e.target === e.currentTarget) {
                          e.preventDefault()
                          if (e.shiftKey) splitRef({ host: host.id, session: s.id }, 'right')
                          else attach()
                        }
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
                          <span className="row-label">
                            {highlightParts(label, labelIndices).map((p, i) =>
                              p.hit ? <mark key={i}>{p.text}</mark> : <span key={i}>{p.text}</span>
                            )}
                          </span>
                        )}
                        {!s.managed && <span className="tag">external</span>}
                        {s.runAs && (
                          <span className="tag" title={`Runs as ${s.runAs}`}>
                            as {s.runAs}
                          </span>
                        )}
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
      <div className="sidebar-filter">
        <input
          ref={filterRef}
          className="filter-input"
          type="text"
          value={filter}
          placeholder="Filter…"
          aria-label="Filter sessions"
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={onFilterKey}
        />
        <kbd className="filter-hint" aria-hidden="true">
          {typeof navigator !== 'undefined' && /mac/i.test(navigator.platform) ? '⌘K' : 'Ctrl+K'}
        </kbd>
      </div>
      </>
      )}
      <div
        className="sidebar-resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sidebar"
        aria-valuemin={SIDEBAR_MIN}
        aria-valuemax={SIDEBAR_MAX}
        aria-valuenow={rail ? RAIL_WIDTH : width}
        tabIndex={rail ? -1 : 0}
        hidden={rail}
        onPointerDown={onResizeDown}
        onDoubleClick={() => commitWidth(SIDEBAR_DEFAULT)}
        onKeyDown={onResizeKey}
      />
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
          onCheck={() => {
            const h = hostMenu.host
            setHostMenu(null)
            setCheckHost(h)
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
          onAppearance={() => {
            setSettings(null)
            openAppearance()
          }}
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
      {checkHost && (
        <HostCheckDialog
          host={state.hosts.find((h) => h.id === checkHost.id) ?? checkHost}
          onClose={() => setCheckHost(null)}
          onInstallHooks={() => {
            const h = state.hosts.find((x) => x.id === checkHost.id) ?? checkHost
            setCheckHost(null)
            setHooksDialog({ host: h, mode: h.claudeHooks === 'outdated' ? 'update' : 'install' })
          }}
        />
      )}
      {hooksDialog && (
        <HooksDialog host={hooksDialog.host} mode={hooksDialog.mode} onClose={() => setHooksDialog(null)} />
      )}
      {menu && (
        <ContextMenu
          menu={menu}
          open={visible.has(`${menu.host.id}/${menu.session.id}`)}
          onChange={setMenu}
          onClose={() => setMenu(null)}
          onRename={() => {
            setRenaming({ host: menu.host.id, id: menu.session.id })
            setMenu(null)
          }}
          onSplit={(direction) => {
            const m = menu
            setMenu(null)
            dispatch({ type: 'openInSplit', ref: { host: m.host.id, session: m.session.id }, direction })
          }}
          onDisconnect={() => {
            const m = menu
            setMenu(null)
            dispatch({ type: 'disconnect', ref: { host: m.host.id, session: m.session.id } })
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
