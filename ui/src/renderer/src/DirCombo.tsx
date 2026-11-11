import { useEffect, useReducer, useRef } from 'react'
import { useCore } from './CoreContext'
import {
  comboReducer,
  completionItems,
  handleComboKey,
  initialCombo,
  isVisible,
  suggestItems,
  wantsSuggestions,
  type DirEntry
} from './dirComboState'

const DEBOUNCE_MS = 150

interface Props {
  id: string
  host: string
  value: string
  onChange: (v: string) => void
}

/** Directory input with remote path completion and suggestions. Free text always works. */
export function DirCombo({ id, host, value, onChange }: Props): React.JSX.Element {
  const { client } = useCore()
  const [s, dispatch] = useReducer(comboReducer, initialCombo)
  const seq = useRef(0)
  const suggestCache = useRef(new Map<string, DirEntry[]>())
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!client || !host) return
    const mySeq = ++seq.current
    dispatch({ type: 'request', seq: mySeq })
    const suggest = wantsSuggestions(value)
    const cached = suggest ? suggestCache.current.get(host) : undefined
    if (cached) {
      dispatch({ type: 'results', seq: mySeq, items: cached })
      return
    }
    const timer = setTimeout(
      () => {
        const req = suggest
          ? client.request<{ recent?: string[]; repos?: string[] }>('remote.suggestDirs', { host }).then((r) => {
              const items = suggestItems(r ?? {})
              suggestCache.current.set(host, items)
              return items
            })
          : client
              .request<Array<{ path: string; gitRepo?: boolean }>>('remote.completeDir', { host, prefix: value })
              .then(completionItems)
        req
          .then((items) => dispatch({ type: 'results', seq: mySeq, items }))
          .catch((e: Error) => dispatch({ type: 'failed', seq: mySeq, message: e.message }))
      },
      suggest ? 0 : DEBOUNCE_MS
    )
    return () => clearTimeout(timer)
  }, [client, host, value])

  const visible = isVisible(s)
  const listId = `${id}-list`
  const optId = (i: number): string => `${id}-opt-${i}`

  const accept = (path: string): void => {
    dispatch({ type: 'accepted' })
    onChange(path)
    inputRef.current?.focus()
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    const r = handleComboKey(s, e.key)
    if (!r.handled) return
    e.preventDefault()
    e.stopPropagation()
    if (r.accept !== null) {
      accept(r.accept)
      dispatch({ type: 'open' })
    } else {
      // Re-run the reducer transitions the key produced.
      if (r.state.open !== s.open) dispatch({ type: r.state.open ? 'open' : 'close' })
      if (r.state.active !== s.active) dispatch({ type: 'hover', index: r.state.active })
    }
  }

  let lastGroup: string | undefined
  return (
    <div className="combo">
      <input
        id={id}
        ref={inputRef}
        autoFocus
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={visible}
        aria-controls={listId}
        aria-activedescendant={visible && s.active >= 0 ? optId(s.active) : undefined}
        autoComplete="off"
        value={value}
        placeholder="~"
        spellCheck={false}
        onChange={(e) => {
          dispatch({ type: 'open' })
          onChange(e.target.value)
        }}
        onFocus={() => dispatch({ type: 'open' })}
        onBlur={() => dispatch({ type: 'close' })}
        onKeyDown={onKeyDown}
      />
      {visible && (
        <ul className="combo-list" id={listId} role="listbox" aria-label="Directory suggestions">
          {s.items.map((it, i) => {
            const header = it.group && it.group !== lastGroup ? it.group : null
            lastGroup = it.group
            return (
              <li key={`${it.group ?? ''}|${it.path}`} role="presentation" style={{ display: 'contents' }}>
                {header && <div className="combo-group" role="presentation">{header}</div>}
                <div
                  id={optId(i)}
                  role="option"
                  aria-selected={i === s.active}
                  className="combo-option"
                  // Keep focus in the input so blur does not close the list before the click lands.
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseMove={() => i !== s.active && dispatch({ type: 'hover', index: i })}
                  onClick={() => {
                    accept(it.path)
                    dispatch({ type: 'open' })
                  }}
                >
                  <span className="combo-path">{it.path}</span>
                  {it.gitRepo && <span className="badge">git</span>}
                </div>
              </li>
            )
          })}
        </ul>
      )}
      {s.error && <span className="combo-note">Can’t list directories: {s.error}</span>}
    </div>
  )
}
