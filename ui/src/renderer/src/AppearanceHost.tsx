import { useEffect, useRef } from 'react'
import { appearanceStore, closeAppearance, lastOutsideFocus, useAppearanceOpen, useAppearanceSettings } from './appearance'
import { DEFAULT_FONT, MAX_FONT, MIN_FONT, type ThemeSource } from './appearanceModel'
import { MATCH_APP, PRESETS } from './terminalThemes'

const SOURCES: Array<{ id: ThemeSource; label: string }> = [
  { id: 'system', label: 'System' },
  { id: 'light', label: 'Light' },
  { id: 'dark', label: 'Dark' }
]

const SWATCH_KEYS = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white'] as const

function Swatch({ presetId }: { presetId: string }): React.JSX.Element {
  const preset = PRESETS.find((p) => p.id === presetId)
  if (!preset) {
    return (
      <div className="appearance-swatch" data-testid="term-swatch" aria-hidden="true">
        <span className="appearance-swatch-text">Follows the app theme</span>
      </div>
    )
  }
  const t = preset.theme
  return (
    <div className="appearance-swatch" data-testid="term-swatch" aria-hidden="true" style={{ background: t.background, color: t.foreground }}>
      <span className="appearance-swatch-text">$ claude</span>
      {SWATCH_KEYS.map((k) => (
        <i key={k} style={{ background: t[k] }} />
      ))}
    </div>
  )
}

/** Renders the Appearance dialog; mounted once in App, opened with openAppearance(). */
export function AppearanceHost(): React.JSX.Element | null {
  const open = useAppearanceOpen()
  return open ? <AppearanceDialog /> : null
}

function AppearanceDialog(): React.JSX.Element {
  const s = useAppearanceSettings()
  const dialogRef = useRef<HTMLDivElement>(null)

  // Focus starts in the dialog; it returns to what opened it on close.
  useEffect(() => {
    dialogRef.current?.querySelector<HTMLElement>('[aria-pressed="true"]')?.focus()
    return () => lastOutsideFocus()?.focus()
  }, [])

  const onKeyDown = (e: React.KeyboardEvent): void => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      closeAppearance()
    } else if (e.key === 'Tab') {
      // Keep Tab inside the dialog.
      const f = [...(dialogRef.current?.querySelectorAll<HTMLElement>('button, input, select') ?? [])].filter((x) => !x.hasAttribute('disabled'))
      if (f.length === 0) return
      const first = f[0]
      const last = f[f.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && closeAppearance()}>
      <div ref={dialogRef} className="modal appearance-dialog" role="dialog" aria-modal="true" aria-label="Appearance" onKeyDown={onKeyDown}>
        <h2>Appearance</h2>

        <div role="group" aria-label="Theme" className="appearance-field">
          <span className="appearance-label">Theme</span>
          <div className="appearance-segments">
            {SOURCES.map((o) => (
              <button
                key={o.id}
                type="button"
                className="appearance-segment"
                aria-pressed={s.appTheme === o.id}
                onClick={() => appearanceStore.update({ appTheme: o.id })}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>

        <label className="appearance-field">
          Terminal theme
          <select value={s.termTheme} onChange={(e) => appearanceStore.update({ termTheme: e.target.value })}>
            <option value={MATCH_APP}>Match app</option>
            {PRESETS.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </label>
        <Swatch presetId={s.termTheme} />

        <label className="appearance-field">
          Font size ({MIN_FONT} to {MAX_FONT})
          <span className="appearance-row">
            <input
              type="range"
              aria-label="Font size slider"
              min={MIN_FONT}
              max={MAX_FONT}
              step={1}
              value={s.fontSize}
              onChange={(e) => appearanceStore.update({ fontSize: e.target.value })}
            />
            <input
              type="number"
              className="appearance-number"
              aria-label="Font size"
              min={MIN_FONT}
              max={MAX_FONT}
              value={s.fontSize}
              onChange={(e) => e.target.value !== '' && appearanceStore.update({ fontSize: e.target.value })}
            />
            <button type="button" className="btn" onClick={() => appearanceStore.update({ fontSize: DEFAULT_FONT })}>
              Default
            </button>
          </span>
        </label>

        <label className="appearance-field">
          Font family
          <span className="appearance-row">
            <input
              type="text"
              aria-label="Font family"
              spellCheck={false}
              value={s.fontFamily}
              onChange={(e) => appearanceStore.update({ fontFamily: e.target.value })}
            />
            <button type="button" className="btn" onClick={() => appearanceStore.resetFont()}>
              Reset
            </button>
          </span>
        </label>
        <p className="help">Zoom the terminal with Ctrl/Cmd and +, - or 0.</p>

        <div className="modal-actions">
          <button type="button" className="btn primary" onClick={closeAppearance}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
