import { useEffect, useRef, useState } from 'react'
import { useCore } from './CoreContext'
import { deleteNamedLayout, loadNamedLayouts, saveNamedLayout } from './layoutStorage'

/** "Layouts" menu: save the current layout under a name, switch, delete. */
export function LayoutsMenu(): React.JSX.Element {
  const { state, dispatch } = useCore()
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [name, setName] = useState('')
  const [items, setItems] = useState(() => loadNamedLayouts())
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    setItems(loadNamedLayouts())
    const onDown = (e: PointerEvent): void => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('pointerdown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  const close = (): void => {
    setOpen(false)
    setSaving(false)
    setName('')
  }
  const save = (): void => {
    if (!name.trim()) return
    setItems(saveNamedLayout(name, state.layout))
    setSaving(false)
    setName('')
  }

  return (
    <div className="layouts-menu" ref={box}>
      <button
        className="btn layouts-button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => (open ? close() : setOpen(true))}
      >
        Layouts ▾
      </button>
      {open && (
        <div className="layouts-popover" role="menu" aria-label="Layouts">
          {saving ? (
            <form
              className="layouts-save"
              onSubmit={(e) => {
                e.preventDefault()
                save()
              }}
            >
              <input
                autoFocus
                aria-label="Layout name"
                placeholder="Layout name"
                value={name}
                maxLength={80}
                onChange={(e) => setName(e.target.value)}
              />
              <button className="btn primary" type="submit" disabled={!name.trim()}>
                Save
              </button>
            </form>
          ) : (
            <button role="menuitem" className="layouts-item" onClick={() => setSaving(true)}>
              Save layout as…
            </button>
          )}
          <div className="layouts-sep" role="separator" />
          {items.length === 0 && <p className="muted layouts-none">No saved layouts</p>}
          {items.map((it) => (
            <div className="layouts-row" key={it.name}>
              <button
                role="menuitem"
                className="layouts-item"
                onClick={() => {
                  dispatch({ type: 'setLayout', layout: it.layout })
                  close()
                }}
              >
                {it.name}
              </button>
              <button
                className="pane-close"
                aria-label={`Delete layout ${it.name}`}
                title="Delete layout"
                onClick={() => setItems(deleteNamedLayout(it.name))}
              >
                ✕
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
