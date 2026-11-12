// Keyboard bindings for the split layout, as a small table.
//
// macOS uses Cmd. On Windows and Linux plain Ctrl+D (EOF) and Ctrl+W (delete
// word) belong to the terminal, so those platforms use Ctrl+Shift+D/E/W.
import type { KeyLike } from './terminalKeys'
import type { Direction } from './layoutTree'

export type LayoutCommand =
  | { type: 'splitRight' }
  | { type: 'splitDown' }
  | { type: 'closePane' }
  | { type: 'focus'; dir: Direction }

interface Chord {
  key: string // lower-cased KeyboardEvent.key
  mod: boolean // Cmd on macOS, Ctrl elsewhere
  shift: boolean
  alt: boolean
}
interface Binding {
  command: LayoutCommand
  label: string
  mac: Chord
  other: Chord
  macText: string
  otherText: string
}

const ch = (key: string, mod: boolean, shift: boolean, alt: boolean): Chord => ({ key, mod, shift, alt })

export const BINDINGS: readonly Binding[] = [
  { command: { type: 'splitRight' }, label: 'Split right', mac: ch('d', true, false, false), other: ch('d', true, true, false), macText: '⌘D', otherText: 'Ctrl+Shift+D' },
  { command: { type: 'splitDown' }, label: 'Split down', mac: ch('d', true, true, false), other: ch('e', true, true, false), macText: '⇧⌘D', otherText: 'Ctrl+Shift+E' },
  { command: { type: 'closePane' }, label: 'Close pane', mac: ch('w', true, false, false), other: ch('w', true, true, false), macText: '⌘W', otherText: 'Ctrl+Shift+W' },
  { command: { type: 'focus', dir: 'left' }, label: 'Focus left', mac: ch('arrowleft', true, false, true), other: ch('arrowleft', true, false, true), macText: '⌥⌘←', otherText: 'Ctrl+Alt+←' },
  { command: { type: 'focus', dir: 'right' }, label: 'Focus right', mac: ch('arrowright', true, false, true), other: ch('arrowright', true, false, true), macText: '⌥⌘→', otherText: 'Ctrl+Alt+→' },
  { command: { type: 'focus', dir: 'up' }, label: 'Focus up', mac: ch('arrowup', true, false, true), other: ch('arrowup', true, false, true), macText: '⌥⌘↑', otherText: 'Ctrl+Alt+↑' },
  { command: { type: 'focus', dir: 'down' }, label: 'Focus down', mac: ch('arrowdown', true, false, true), other: ch('arrowdown', true, false, true), macText: '⌥⌘↓', otherText: 'Ctrl+Alt+↓' }
]

/** The layout command a keydown triggers on this platform, or null (the key goes to the terminal). */
export function layoutCommand(e: KeyLike, isMac: boolean): LayoutCommand | null {
  if (e.type !== 'keydown') return null
  const k = e.key.toLowerCase()
  for (const b of BINDINGS) {
    const c = isMac ? b.mac : b.other
    const modDown = isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
    if (c.key === k && c.mod === modDown && c.shift === e.shiftKey && c.alt === e.altKey) return b.command
  }
  return null
}

/** Rows for the empty-pane hint. */
export function shortcutHints(isMac: boolean): Array<{ keys: string; label: string }> {
  const pick = (c: LayoutCommand['type']): Binding[] => BINDINGS.filter((b) => b.command.type === c)
  const t = (b: Binding): string => (isMac ? b.macText : b.otherText)
  return [
    ...(['splitRight', 'splitDown', 'closePane'] as const).map((c) => ({ keys: t(pick(c)[0]), label: pick(c)[0].label })),
    { keys: `${isMac ? '⌥⌘' : 'Ctrl+Alt+'}←↑↓→`, label: 'Move focus' }
  ]
}
