// Clipboard shortcuts for the terminal.
//
// xterm.js passes Ctrl+V through as the ^V control byte on Windows and Linux,
// so the browser never pastes. (In a shell ^V is "insert next key literally";
// Claude Code binds it to "paste image".) We intercept the usual terminal
// clipboard shortcuts ourselves. On macOS, Cmd+V / Cmd+C already go through
// the Edit menu and the browser's paste/copy events, so we leave them alone.

export interface KeyLike {
  type: string
  key: string
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
  metaKey: boolean
}

export type ClipboardAction = 'paste' | 'copy' | null

/**
 * What a keydown should do. `hasSelection` matters for Ctrl+C: like Windows
 * Terminal, it copies when text is selected and otherwise stays ^C (interrupt).
 */
export function clipboardAction(e: KeyLike, isMac: boolean, hasSelection: boolean): ClipboardAction {
  if (e.type !== 'keydown' || isMac || e.altKey || e.metaKey) return null
  const k = e.key.toLowerCase()
  if (e.shiftKey && !e.ctrlKey && k === 'insert') return 'paste'
  if (e.ctrlKey && !e.shiftKey && k === 'insert') return hasSelection ? 'copy' : null
  if (!e.ctrlKey) return null
  if (k === 'v') return 'paste' // Ctrl+V and Ctrl+Shift+V
  if (k === 'c') {
    if (e.shiftKey) return 'copy' // Ctrl+Shift+C always copies
    return hasSelection ? 'copy' : null
  }
  return null
}
