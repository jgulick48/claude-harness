import { createContext, useContext, useEffect, useReducer, useState } from 'react'
import { connectCore, type ClientState, type CoreClient } from './coreClient'
import type { Host, Session } from './sessions'
import { initialState, reducer, type Action, type AppState } from './store'

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
  const [state, dispatch] = useReducer(reducer, initialState)
  const [clientState, setClientState] = useState<ClientState>({ state: 'connecting' })
  const [client, setClient] = useState<CoreClient | null>(null)
  const [mainErr, setMainErr] = useState<string | null>(null)

  useEffect(() => {
    let conn: CoreClient | null = null
    let opening = false
    let cancelled = false
    const open = async (): Promise<void> => {
      // `opening` guards the await: the mount call and a 'ready' status event
      // can both arrive before either resolves, which would open two sockets.
      if (conn || opening || cancelled) return
      opening = true
      try {
        const info = await window.harness.getCoreInfo()
        if (cancelled || 'error' in info) return // not up yet / failed; status events decide
        conn = connectCore({ url: info.url, token: info.token, onState: setClientState })
        conn.onEvent('sessions.changed', (ev) =>
          dispatch({ type: 'sessions', host: String(ev.host), sessions: (ev.sessions ?? []) as Session[] })
        )
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
