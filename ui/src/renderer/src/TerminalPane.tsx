import { useEffect, useRef, useState } from 'react'
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { useCore } from './CoreContext'
import { autoReattachAction, closedMessage, sessionLabel, type Session } from './sessions'

function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

function readTheme(): ITheme {
  return {
    background: cssVar('--term-bg', '#ffffff'),
    foreground: cssVar('--term-fg', '#1f2328'),
    cursor: cssVar('--term-cursor', '#1f2328'),
    selectionBackground: cssVar('--term-selection', '#b6d4fe')
  }
}

const FONT = 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace'

interface Props {
  host: string
  sessionId: string
  session: Session | undefined
  hostName: string
  hostConnected: boolean
}

export function TerminalPane({ host, sessionId, session, hostName, hostConnected }: Props): React.JSX.Element {
  const { client, dispatch } = useCore()
  const boxRef = useRef<HTMLDivElement>(null)
  const [ended, setEnded] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Bumped by Reattach to re-run the attach effect for the same session.
  const [attempt, setAttempt] = useState(0)
  // True while the current attach is an automatic one; a failure then means the session is gone.
  const autoRef = useRef(false)
  const sessionExists = session !== undefined

  // Reattach on its own once the host is back after "connection lost".
  const autoCount = useRef(0)
  useEffect(() => {
    if (!hostConnected) autoCount.current = 0
    const action = autoReattachAction({ ended, hostConnected, sessionExists })
    if (action === 'reattach') {
      // Bounded so a host that flaps while looking connected cannot spin.
      if (autoCount.current >= 3) return
      autoCount.current++
      autoRef.current = true
      setAttempt((n) => n + 1)
    } else if (action === 'gone') {
      setEnded('session ended')
    }
  }, [ended, hostConnected, sessionExists])

  useEffect(() => {
    if (!client || !boxRef.current) return
    const box = boxRef.current
    let disposed = false
    let stream: number | null = null
    let offStream: (() => void) | null = null
    setEnded(null)
    setError(null)
    const wasAuto = autoRef.current
    autoRef.current = false

    const term = new Terminal({
      fontFamily: FONT,
      fontSize: 13,
      cursorBlink: true,
      scrollback: 5000,
      theme: readTheme()
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(new WebLinksAddon((_e, uri) => window.open(uri)))
    term.open(box)
    const doFit = (): void => {
      try {
        fit.fit()
      } catch {
        /* container not laid out yet */
      }
    }
    doFit()

    term.onData((d) => {
      if (stream !== null) client.sendInput(stream, d)
    })
    term.onBinary((d) => {
      if (stream === null) return
      const bytes = new Uint8Array(d.length)
      for (let i = 0; i < d.length; i++) bytes[i] = d.charCodeAt(i) & 0xff
      client.sendInput(stream, bytes)
    })

    const offClosed = client.onEvent('term.closed', (ev) => {
      if (stream !== null && ev.stream === stream) {
        offStream?.()
        offStream = null
        setEnded(String(ev.reason ?? 'session ended'))
      }
    })

    client
      .request<{ stream: number }>('term.attach', { host, session: sessionId, cols: term.cols, rows: term.rows })
      .then((r) => {
        // Register synchronously: output frames may follow the result immediately.
        if (disposed) {
          client.request('term.detach', { stream: r.stream }).catch(() => {})
          return
        }
        stream = r.stream
        offStream = client.onStream(r.stream, (d) => term.write(d))
        term.focus()
      })
      .catch((e: Error) => {
        if (disposed) return
        if (wasAuto) setEnded('session ended')
        else setError(e.message)
      })

    let debounce: ReturnType<typeof setTimeout> | null = null
    // Refit only when the box itself changed size (to the whole pixel). This
    // breaks feedback loops where fitting changes layout that re-triggers the
    // observer, e.g. a scrollbar appearing and disappearing on Windows.
    let lastW = -1
    let lastH = -1
    const ro = new ResizeObserver((entries) => {
      const r = entries[entries.length - 1]?.contentRect
      if (r) {
        const w = Math.round(r.width)
        const h = Math.round(r.height)
        if (w === lastW && h === lastH) return
        lastW = w
        lastH = h
      }
      if (debounce) clearTimeout(debounce)
      debounce = setTimeout(() => {
        if (disposed) return
        const { cols, rows } = term
        doFit()
        if (stream !== null && (term.cols !== cols || term.rows !== rows)) {
          client.request('term.resize', { stream, cols: term.cols, rows: term.rows }).catch(() => {})
        }
      }, 100)
    })
    ro.observe(box)

    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onScheme = (): void => {
      term.options.theme = readTheme()
    }
    mq.addEventListener('change', onScheme)

    return () => {
      disposed = true
      if (debounce) clearTimeout(debounce)
      ro.disconnect()
      mq.removeEventListener('change', onScheme)
      offClosed()
      offStream?.()
      if (stream !== null) client.request('term.detach', { stream }).catch(() => {})
      term.dispose()
    }
  }, [client, host, sessionId, attempt])

  return (
    <div className="term-pane">
      <header className="term-header">
        <span className="term-title">{session ? sessionLabel(session) : sessionId}</span>
        {session && <span className="term-path">{session.cwd}</span>}
      </header>
      <div className="term-body">
        <div className="term-box" ref={boxRef} />
        {(ended !== null || error !== null) && (
          <div className="term-overlay">
            <p>{error !== null ? `Could not attach: ${error}` : closedMessage(ended ?? '', hostName)}</p>
            {error === null && ended === 'connection lost' && hostConnected && (
              <button className="btn primary" onClick={() => setAttempt((n) => n + 1)}>
                Reattach
              </button>
            )}
            <button className="btn" autoFocus onClick={() => dispatch({ type: 'attach', ref: null })}>
              Close
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
