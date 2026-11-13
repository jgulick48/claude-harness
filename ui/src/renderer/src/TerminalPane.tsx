import { useEffect, useRef, useState } from 'react'
import { focusGeneration } from './focusClaims'
import { osc52Text } from './osc52'
import { clipboardAction } from './terminalKeys'
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { useCore } from './CoreContext'
import { useTerminalAppearance } from './appearance'
import { layoutCommand } from './shortcuts'
import { StatusBadge } from './StatusBadge'
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
  paneId: string
  focused: boolean
  host: string
  sessionId: string
  session: Session | undefined
  hostName: string
  hostConnected: boolean
}

export function TerminalPane({ paneId, focused, host, sessionId, session, hostName, hostConnected }: Props): React.JSX.Element {
  const { client, dispatch } = useCore()
  const boxRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  // Refits and tells the core; set by the attach effect, used on appearance changes.
  const refitRef = useRef<(() => void) | null>(null)
  const appearance = useTerminalAppearance()
  const appearanceRef = useRef(appearance)
  appearanceRef.current = appearance
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

    // Links: plain URLs (WebLinksAddon) and OSC 8 hyperlinks (linkHandler),
    // which Claude Code prints. Without a linkHandler xterm falls back to a
    // confirm() and an about:blank window that main blocks, so clicking did
    // nothing. Clicks open the link in the browser (main allows http(s) only);
    // the pointer's link is shown as a tooltip, since an OSC 8 link's text
    // need not match its target, and offered in the right-click menu.
    let hovered: string | null = null
    const hover = (_e: MouseEvent, uri: string): void => {
      hovered = uri
      box.title = uri
    }
    const leave = (): void => {
      hovered = null
      box.removeAttribute('title')
    }
    const open = (e: MouseEvent, uri: string): void => {
      // xterm activates on mouseup of any button; right-click is the menu.
      if (e.button !== 0) return
      void window.harness.openLink(uri)
    }
    const ap = appearanceRef.current
    const term = new Terminal({
      fontFamily: ap.fontFamily ?? FONT,
      fontSize: ap.fontSize,
      cursorBlink: true,
      scrollback: 5000,
      theme: ap.theme ?? readTheme(),
      linkHandler: { activate: open, hover, leave },
      // While a program has mouse tracking on (Claude Code full-screen), drags
      // go to the program. Shift+drag selects in the terminal anyway; on macOS
      // xterm uses Option+drag for that, but only with this option.
      macOptionClickForcesSelection: true
    })
    termRef.current = term
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(new WebLinksAddon(open, { hover, leave }))
    term.open(box)
    // Programs copy to the clipboard with OSC 52 (Claude Code does for its own
    // mouse selections); xterm ignores it unless handled. Writes only.
    term.parser.registerOscHandler(52, (data) => {
      const text = osc52Text(data)
      if (text !== null && !disposed) void window.harness.writeClipboard(text)
      return true
    })

    // Clipboard: see terminalKeys.ts for why Ctrl+V needs handling at all.
    const isMac = navigator.platform.toLowerCase().includes('mac')
    const paste = (): void => {
      void window.harness.readClipboard().then((text) => {
        // term.paste normalizes newlines and honors bracketed-paste mode, so
        // Claude Code receives a multi-line paste as one paste.
        if (text && !disposed) term.paste(text)
      })
    }
    const copy = (): void => {
      const sel = term.getSelection()
      if (sel) void window.harness.writeClipboard(sel)
      term.clearSelection()
    }
    term.attachCustomKeyEventHandler((e) => {
      // Layout shortcuts are handled on the window; keep them out of the terminal.
      if (layoutCommand(e, isMac)) return false
      const action = clipboardAction(e, isMac, term.hasSelection())
      if (!action) return true
      e.preventDefault()
      if (action === 'paste') paste()
      else copy()
      return false
    })
    const onContextMenu = (e: MouseEvent): void => {
      e.preventDefault()
      const link = hovered
      void window.harness.showTerminalMenu(term.hasSelection(), link).then((choice) => {
        if (choice === 'paste') paste()
        else if (choice === 'copy') copy()
        else if (choice === 'openLink' && link) void window.harness.openLink(link)
        else if (choice === 'copyLink' && link) void window.harness.writeClipboard(link)
        term.focus()
      })
    }
    box.addEventListener('contextmenu', onContextMenu)

    const doFit = (): void => {
      try {
        fit.fit()
      } catch {
        /* container not laid out yet */
      }
    }
    doFit()
    const refit = (): void => {
      const { cols, rows } = term
      doFit()
      if (stream !== null && (term.cols !== cols || term.rows !== rows)) {
        client.request('term.resize', { stream, cols: term.cols, rows: term.rows }).catch(() => {})
      }
    }
    refitRef.current = refit

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

    const attachFocusGen = focusGeneration()
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
        // Only take focus if nothing else claimed it while the attach was in
        // flight (see focusClaims.ts); otherwise e.g. typing in the filter
        // right after pressing Enter would be cut off mid-word.
        if (focusGeneration() === attachFocusGen || box.contains(document.activeElement)) term.focus()
      })
      .catch((e: Error) => {
        if (disposed) return
        // term.closed can arrive just before hosts.changed, so an automatic
        // reattach may run while the host is already down. That is still a
        // lost connection, not a vanished session: wait for the host again.
        if (wasAuto) setEnded(/connection lost|unavailable/i.test(e.message) ? 'connection lost' : 'session ended')
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
        refit()
      }, 100)
    })
    ro.observe(box)

    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onScheme = (): void => {
      if (!appearanceRef.current.theme) term.options.theme = readTheme()
    }
    mq.addEventListener('change', onScheme)

    return () => {
      disposed = true
      box.removeEventListener('contextmenu', onContextMenu)
      if (debounce) clearTimeout(debounce)
      ro.disconnect()
      mq.removeEventListener('change', onScheme)
      offClosed()
      offStream?.()
      if (stream !== null) client.request('term.detach', { stream }).catch(() => {})
      refitRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [client, host, sessionId, attempt])

  // Appearance changes apply live to this terminal, then refit.
  useEffect(() => {
    const term = termRef.current
    if (!term) return
    term.options.theme = appearance.theme ?? readTheme()
    term.options.fontSize = appearance.fontSize
    term.options.fontFamily = appearance.fontFamily ?? FONT
    refitRef.current?.()
  }, [appearance])

  // Keyboard focus follows the focused pane (also after a layout shortcut).
  useEffect(() => {
    if (focused) termRef.current?.focus()
  }, [focused])

  return (
    <div className="term-pane" data-testid="terminal-pane">
      <header className="term-header">
        <span className="term-titleline">
          <span className="term-title">{session ? sessionLabel(session) : sessionId}</span>
          {session?.runAs && (
            <span className="tag" title={`Runs as ${session.runAs}`}>
              as {session.runAs}
            </span>
          )}
          {session && <StatusBadge session={session} />}
          <span className="term-host">{hostName}</span>
          <button
            className="pane-close"
            aria-label="Close pane"
            title="Close pane (the session keeps running)"
            onClick={() => dispatch({ type: 'closePane', id: paneId })}
          >
            ✕
          </button>
        </span>
        {session && <span className="term-path">{session.cwd}</span>}
      </header>
      {/* A preset's background also fills the box padding, so no strip of the app color shows. */}
      <div className="term-body" style={appearance.theme?.background ? { background: appearance.theme.background } : undefined}>
        <div className="term-box" ref={boxRef} />
        {(ended !== null || error !== null) && (
          <div className="term-overlay">
            <p>{error !== null ? `Could not attach: ${error}` : closedMessage(ended ?? '', hostName)}</p>
            {error === null && ended === 'connection lost' && hostConnected && (
              <button className="btn primary" onClick={() => setAttempt((n) => n + 1)}>
                Reattach
              </button>
            )}
            <button className="btn" autoFocus onClick={() => dispatch({ type: 'clearPane', id: paneId })}>
              Close
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
