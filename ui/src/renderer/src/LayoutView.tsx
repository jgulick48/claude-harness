import { useRef } from 'react'
import { useCore } from './CoreContext'
import { dividers, leaves, MIN_FRACTION, rects, type DividerGeom, type Pane, type Rect } from './layoutTree'
import { shortcutHints } from './shortcuts'
import { TerminalPane } from './TerminalPane'

const DIVIDER_PX = 5
const isMac = (): boolean => navigator.platform.toLowerCase().includes('mac')

function LeafView({ pane, multi }: { pane: Pane; multi: boolean }): React.JSX.Element {
  const { state, dispatch } = useCore()
  const focused = state.layout.focus === pane.id
  const focus = (): void => {
    if (!focused) dispatch({ type: 'focusPane', id: pane.id })
  }
  const ref = pane.ref
  const host = ref ? state.hosts.find((h) => h.id === ref.host) : undefined
  let body: React.JSX.Element
  if (ref && pane.pending) {
    body = (
      <div className="pane-empty" data-testid="pane-waiting">
        <p className="muted">Waiting for {host?.name ?? ref.host} to connect…</p>
      </div>
    )
  } else if (ref) {
    body = (
      <TerminalPane
        key={`${ref.host}/${ref.session}`}
        paneId={pane.id}
        focused={focused}
        host={ref.host}
        sessionId={ref.session}
        hostName={host?.name ?? ref.host}
        hostConnected={host?.state === 'connected'}
        session={state.sessions[ref.host]?.find((s) => s.id === ref.session)}
      />
    )
  } else {
    body = (
      <div className="pane-empty" data-testid="empty-pane">
        {multi && (
          <button
            className="pane-close pane-close-float"
            aria-label="Close pane"
            title="Close pane"
            onClick={() => dispatch({ type: 'closePane', id: pane.id })}
          >
            ✕
          </button>
        )}
        {pane.gone ? <p className="pane-gone">Session no longer exists</p> : <p className="muted">Select or create a session</p>}
        <ul className="shortcut-hints" aria-label="Pane shortcuts">
          {shortcutHints(isMac()).map((h) => (
            <li key={h.label}>
              <kbd>{h.keys}</kbd> {h.label}
            </li>
          ))}
        </ul>
      </div>
    )
  }
  return (
    <div
      className={`pane-leaf${focused ? ' focused' : ''}`}
      data-pane-id={pane.id}
      data-focused={focused}
      onPointerDownCapture={focus}
      onFocusCapture={focus}
    >
      {body}
    </div>
  )
}

function Divider({ g }: { g: DividerGeom }): React.JSX.Element {
  const { dispatch } = useCore()
  const row = g.dir === 'row'
  const drag = useRef<{ start: number; base: number[]; usable: number } | null>(null)
  const move = (delta: number, base?: number[]): void =>
    dispatch({ type: 'resizeDivider', path: g.path, index: g.index, delta, base })
  const pct = Math.round(g.sizes[g.index] * 100)
  const style: React.CSSProperties = row
    ? { left: `${g.at * 100}%`, top: `${g.rect.y * 100}%`, height: `${g.rect.h * 100}%` }
    : { top: `${g.at * 100}%`, left: `${g.rect.x * 100}%`, width: `${g.rect.w * 100}%` }
  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation={row ? 'vertical' : 'horizontal'}
      aria-label={row ? 'Resize panes horizontally' : 'Resize panes vertically'}
      aria-valuenow={pct}
      aria-valuemin={Math.round(MIN_FRACTION * 100)}
      aria-valuemax={100 - Math.round(MIN_FRACTION * 100)}
      className={`pane-divider ${row ? 'vertical' : 'horizontal'}`}
      style={style}
      onPointerDown={(e) => {
        const box = e.currentTarget.parentElement
        if (!box) return
        e.preventDefault()
        e.currentTarget.setPointerCapture(e.pointerId)
        const r = box.getBoundingClientRect()
        // The split's own extent in pixels.
        const usable = Math.max(1, row ? r.width * g.rect.w : r.height * g.rect.h)
        drag.current = { start: row ? e.clientX : e.clientY, base: g.sizes.slice(), usable }
      }}
      onPointerMove={(e) => {
        const d = drag.current
        if (!d) return
        move(((row ? e.clientX : e.clientY) - d.start) / d.usable, d.base)
      }}
      onPointerUp={(e) => {
        drag.current = null
        if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId)
      }}
      onKeyDown={(e) => {
        const [dec, inc] = row ? ['ArrowLeft', 'ArrowRight'] : ['ArrowUp', 'ArrowDown']
        const step = e.shiftKey ? 0.1 : 0.02
        if (e.key === dec) move(-step)
        else if (e.key === inc) move(step)
        else return
        e.preventDefault()
      }}
    />
  )
}

/**
 * All panes are rendered flat and positioned by percentages, so changing the
 * tree (split, close, resize) moves terminals without remounting them.
 */
export function LayoutView(): React.JSX.Element {
  const { state } = useCore()
  const root = state.layout.root
  const m = new Map<string, Rect>()
  rects(root, { x: 0, y: 0, w: 1, h: 1 }, m)
  const panes = leaves(root)
  const multi = panes.length > 1
  return (
    <div className={`layout-root${multi ? ' multi' : ''}`} data-testid="layout-root">
      {panes.map((p) => {
        const r = m.get(p.id) as Rect
        return (
          <div
            key={p.id}
            className="pane-slot"
            style={{ left: `${r.x * 100}%`, top: `${r.y * 100}%`, width: `${r.w * 100}%`, height: `${r.h * 100}%` }}
          >
            <LeafView pane={p} multi={multi} />
          </div>
        )
      })}
      {dividers(root).map((g) => (
        <Divider key={`${g.path.join('.')}/${g.index}`} g={g} />
      ))}
    </div>
  )
}
