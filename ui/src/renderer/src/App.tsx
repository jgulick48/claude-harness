import { useEffect, useState } from 'react'
import { UpdateBanner } from './UpdateBanner'
import { connectCore, type ClientState } from './coreClient'

function useCoreStatus(): string {
  const [client, setClient] = useState<ClientState>({ state: 'connecting' })
  const [mainErr, setMainErr] = useState<string | null>(null)

  useEffect(() => {
    let conn: { close(): void } | null = null
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
        conn = connectCore({ url: info.url, token: info.token, onState: setClient })
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

  if (mainErr) return `Core: ${mainErr}`
  if (client.state === 'error') return `Core: ${client.message}`
  if (client.state === 'ready') {
    const lat = client.latencyMs === null ? '…' : `${client.latencyMs}ms`
    return `Core: connected · v${client.version} · protocol ${client.protocol} · ${lat}`
  }
  return 'Core: starting…'
}

export function App(): React.JSX.Element {
  const status = useCoreStatus()
  const isError = /^Core: (?!connected|starting)/.test(status)
  return (
    <div className="app">
      <aside className="sidebar">
        <header className="sidebar-header">
          <span className="sidebar-title">SESSIONS</span>
          <button className="icon-button" disabled title="New terminal (coming soon)" aria-label="New terminal">
            +
          </button>
        </header>
        <div className="sidebar-body">
          <p className="muted">No hosts yet</p>
        </div>
        <UpdateBanner />
        <footer className={isError ? 'status-line error' : 'status-line'}>{status}</footer>
      </aside>
      <main className="main-pane">
        <p className="muted">Select or create a session</p>
      </main>
    </div>
  )
}
