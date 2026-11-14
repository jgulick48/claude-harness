import { useCallback, useEffect, useRef, useState } from 'react'
import { isFindKey } from './terminalKeys'
import type { ISearchOptions, SearchAddon } from '@xterm/addon-search'

interface Props {
  search: SearchAddon
  close: () => void
  onFocusTerminal: () => void
  // Bumped each time the shortcut is pressed, so an open bar re-focuses and selects its text.
  focusTick: number
  /** The terminal's current background color, to pick highlight colors that show on it. */
  background: () => string | undefined
}

/**
 * Whether a CSS color (#rgb or #rrggbb) is dark; false for anything else.
 * Terminal themes can differ from the app's light/dark mode (presets), so the
 * highlights follow the terminal's own background.
 */
export function isDarkColor(color: string | undefined): boolean {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color?.trim() ?? '')
  if (!m) return false
  const hex = m[1].length === 3 ? [...m[1]].map((c) => c + c).join('') : m[1]
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16))
  return 0.299 * r + 0.587 * g + 0.114 * b < 128
}

// Decoration colors must be #RRGGBB, so they cannot use CSS variables.
function decorations(dark: boolean): ISearchOptions['decorations'] {
  return dark
    ? {
        matchBackground: '#614d12',
        matchOverviewRuler: '#d29922',
        activeMatchBackground: '#9e6a03',
        activeMatchColorOverviewRuler: '#f2cc60'
      }
    : {
        matchBackground: '#fff3a3',
        matchOverviewRuler: '#d4a72c',
        activeMatchBackground: '#ffb347',
        activeMatchColorOverviewRuler: '#bf8700'
      }
}

interface Result {
  index: number
  count: number
}

export function TerminalSearch({ search, close, onFocusTerminal, focusTick, background }: Props): React.JSX.Element {
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [caseSensitive, setCase] = useState(false)
  const [wholeWord, setWord] = useState(false)
  const [regex, setRegex] = useState(false)
  const [result, setResult] = useState<Result | null>(null)

  const invalid = (() => {
    if (!regex || !query) return false
    try {
      new RegExp(query)
      return false
    } catch {
      return true
    }
  })()

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [focusTick])

  useEffect(() => {
    const d = search.onDidChangeResults((r) => setResult({ index: r.resultIndex, count: r.resultCount }))
    return () => d.dispose()
  }, [search])

  const find = useCallback(
    (dir: 'next' | 'prev', incremental = false): void => {
      if (!query || invalid) {
        search.clearDecorations()
        setResult(null)
        return
      }
      const opts: ISearchOptions = { caseSensitive, wholeWord, regex, incremental, decorations: decorations(isDarkColor(background())) }
      try {
        const found = dir === 'next' ? search.findNext(query, opts) : search.findPrevious(query, opts)
        if (!found) setResult({ index: -1, count: 0 })
      } catch {
        setResult(null)
      }
    },
    [search, query, invalid, caseSensitive, wholeWord, regex, background]
  )

  // Search as the query or an option changes.
  useEffect(() => {
    setResult(null)
    find('next', true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, caseSensitive, wholeWord, regex])

  const shut = (): void => {
    search.clearDecorations()
    search.clearActiveDecoration()
    close()
    onFocusTerminal()
  }

  const onKeyDown = (e: React.KeyboardEvent): void => {
    const next = (back: boolean): void => {
      e.preventDefault()
      e.stopPropagation()
      find(back ? 'prev' : 'next')
    }
    if (isFindKey(e.nativeEvent, navigator.platform.toLowerCase().includes('mac'))) {
      e.preventDefault()
      inputRef.current?.select()
    } else if (e.key === 'Enter' || e.key === 'F3') next(e.shiftKey)
    else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      shut()
    }
  }

  let status = ''
  if (invalid) status = 'Invalid regex'
  else if (query && result) {
    if (result.count === 0) status = 'No results'
    else if (result.index < 0) status = `${result.count} matches`
    else status = `${result.index + 1} of ${result.count}`
  }

  const toggle = (label: string, text: string, on: boolean, set: (v: boolean) => void): React.JSX.Element => (
    <button className="find-btn" aria-label={label} title={label} aria-pressed={on} onClick={() => set(!on)}>
      {text}
    </button>
  )

  return (
    <div className="term-find" role="search" aria-label="Find in terminal" onKeyDown={onKeyDown}>
      <input
        ref={inputRef}
        value={query}
        aria-label="Find in terminal"
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => setQuery(e.target.value)}
      />
      <span className={invalid || (query && result?.count === 0) ? 'find-count bad' : 'find-count'} aria-live="polite">
        {status}
      </span>
      {toggle('Match case', 'Aa', caseSensitive, setCase)}
      {toggle('Whole word', 'ab', wholeWord, setWord)}
      {toggle('Regular expression', '.*', regex, setRegex)}
      <button className="find-btn" aria-label="Previous match" title="Previous match (Shift+Enter)" onClick={() => find('prev')}>
        ↑
      </button>
      <button className="find-btn" aria-label="Next match" title="Next match (Enter)" onClick={() => find('next')}>
        ↓
      </button>
      <button className="find-btn" aria-label="Close find" title="Close (Escape)" onClick={shut}>
        ✕
      </button>
    </div>
  )
}
