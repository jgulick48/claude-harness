import { createContext, useContext, useEffect, useReducer, useRef, useState } from 'react'
import { connectCore, type ClientState, type CoreClient } from './coreClient'
import {
  attentionCount,
  attentionTitle,
  createDebounce,
  isViewing,
  newlyWaiting,
  notificationText,
  NOTIFY_DEBOUNCE_MS
} from './claudeStatus'
import { loadFlag, NOTIFICATIONS_KEY } from './prefs'
import { displayPath, sessionLabel, shouldRetryOnResume, type Host, type Session } from './sessions'
import { visibleRefs } from './layout'
import { loadLayout, saveLayout } from './layoutStorage'
import { initialStateWith, reducer, type Action, type AppState } from './store'

interface Ctx {
  state: AppState
  dispatch: React.Dispatch<Action>
  client: CoreClient | null
  /** Footer text for the core status line. */
  status: string
  statusIsError: boolean
  connected: boolean
}

const CoreCtx = createContext<Ctx | null>(null)

export function useCore(): Ctx {
  const c = useContext(CoreCtx)
  if (!c) throw new Error('useCore outside CoreProvider')
  return c
}

export function CoreProvider({ children }: { children: React.ReactNode }): React.JSX.Element {
  const [state, dispatch] = useReducer(reducer, undefined, () => initialStateWith(loadLayout()))
  const [clientState, setClientState] = useState<ClientState>({ state: 'connecting' })
  const [client, setClient] = useState<CoreClient | null>(null)
  const [mainErr, setMainErr] = useState<string | null>(null)
  // Latest state for event handlers registered once per connection.
  const stateRef = useRef(state)
  stateRef.current = state

  useEffect(() => {
    let conn: CoreClient | null = null
    let opening = false
    let cancelled = false
    // Previous session list per host, for detecting transitions into waiting.
    // Fresh per connection, so a reconnect never counts as a transition.
    const prevSessions = new Map<string, Session[]>()
    const allow = createDebounce(NOTIFY_DEBOUNCE_MS)
    const notifyWaiting = (hostId: string, s: Session): void => {
      if (!loadFlag(NOTIFICATIONS_KEY, true)) return
      const st = stateRef.current
      if (visibleRefs(st).some((r) => isViewing(r, hostId, s.id, document.hasFocus()))) return
      if (!allow(`${hostId}/${s.id}`, Date.now())) return
      const host = st.hosts.find((h) => h.id === hostId)
      const { title, body } = notificationText(
        sessionLabel(s),
        host?.name ?? hostId,
        displayPath(s.cwd, host?.home ?? '')
      )
      try {
        const show = (): void => {
          const n = new Notification(title, { body, tag: `${hostId}/${s.id}` })
          n.onclick = () => {
            window.focus()
            window.harness.focusWindow()
            dispatch({ type: 'attach', ref: { host: hostId, session: s.id } })
          }
        }
        if (Notification.permission === 'granted') show()
        else if (Notification.permission === 'default')
          void Notification.requestPermission().then((p) => p === 'granted' && show())
      } catch {
        /* notifications unavailable */
      }
    }
    const open = async (): Promise<void> => {
      // `opening` guards the await: the mount call and a 'ready' status event
      // can both arrive before either resolves, which would open two sockets.
      if (conn || opening || cancelled) return
      opening = true
      try {
        const info = await window.harness.getCoreInfo()
        if (cancelled || 'error' in info) return // not up yet / failed; status events decide
        conn = connectCore({ url: info.url, token: info.token, onState: setClientState })
        conn.onEvent('sessions.changed', (ev) => {
          const host = String(ev.host)
          const next = (ev.sessions ?? []) as Session[]
          const fresh = newlyWaiting(prevSessions.get(host), next)
          prevSessions.set(host, next)
          dispatch({ type: 'sessions', host, sessions: next })
          for (const s of fresh) notifyWaiting(host, s)
        })
        conn.onEvent('hosts.changed', (ev) => {
          const hosts = (ev.hosts ?? []) as Host[]
          const ids = new Set(hosts.map((h) => h.id))
          for (const id of [...prevSessions.keys()]) if (!ids.has(id)) prevSessions.delete(id)
          dispatch({ type: 'hosts', hosts })
        })
        setClient(conn)
      } finally {
        opening = false
      }
    }
    const off = window.harness.onCoreStatus((s) => {
      if (s.state === 'ready') {
        setMainErr(null)
        void open()
      } else if (s.state === 'error') setMainErr(s.message)
      else if (s.state === 'exited') setMainErr(`core exited (code ${s.code ?? 'unknown'})`)
    })
    void open()
    return () => {
      cancelled = true
      off()
      conn?.close()
    }
  }, [])

  const connected = clientState.state === 'ready'

  // Auto-save the layout (debounced; flushed when the page goes away).
  const layoutRef = useRef(state.layout)
  layoutRef.current = state.layout
  useEffect(() => {
    const t = setTimeout(() => saveLayout(state.layout), 300)
    return () => clearTimeout(t)
  }, [state.layout])
  useEffect(() => {
    const flush = (): void => saveLayout(layoutRef.current)
    window.addEventListener('pagehide', flush)
    return () => window.removeEventListener('pagehide', flush)
  }, [])

  // Waiting count in the window title and the dock/launcher badge.
  const waiting = attentionCount(state.sessions)
  useEffect(() => {
    document.title = attentionTitle(waiting)
    try {
      window.harness.setAttentionCount(waiting)
    } catch {
      /* bridge unavailable */
    }
  }, [waiting])
  // Refresh the host list whenever we (re)connect.
  useEffect(() => {
    if (!connected || !client) return
    let stale = false
    client
      .request<Host[]>('hosts.list')
      .then((hosts) => {
        if (!stale) dispatch({ type: 'hosts', hosts })
      })
      .catch((e: Error) => console.warn('hosts.list failed:', e.message))
    return () => {
      stale = true
    }
  }, [connected, client])

  // After sleep or when the network returns, retry hosts without waiting for
  // their backoff timer.
  const hostsRef = useRef(state.hosts)
  hostsRef.current = state.hosts
  useEffect(() => {
    if (!connected || !client) return
    const retry = (): void => {
      for (const h of hostsRef.current) {
        if (shouldRetryOnResume(h)) client.request('hosts.connect', { host: h.id }).catch(() => {})
      }
    }
    const off = window.harness.onSystemResume(retry)
    window.addEventListener('online', retry)
    return () => {
      off()
      window.removeEventListener('online', retry)
    }
  }, [connected, client])

  let status = 'Core: starting…'
  if (mainErr) status = `Core: ${mainErr}`
  else if (clientState.state === 'error') status = `Core: ${clientState.message}`
  else if (clientState.state === 'ready') {
    const lat = clientState.latencyMs === null ? '…' : `${clientState.latencyMs}ms`
    status = `Core: connected · v${clientState.version} · protocol ${clientState.protocol} · ${lat}`
  }
  const statusIsError = /^Core: (?!connected|starting)/.test(status)

  return (
    <CoreCtx.Provider value={{ state, dispatch, client, status, statusIsError, connected }}>
      {children}
    </CoreCtx.Provider>
  )
}
