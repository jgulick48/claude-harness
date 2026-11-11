// Appearance settings (M6): app theme and terminal look. CONTRACT STUB: the
// appearance implementation replaces these bodies; the exports are fixed.
import type { ITheme } from '@xterm/xterm'

export interface TerminalAppearance {
  /** xterm theme to apply; undefined means "derive from the app's CSS tokens". */
  theme: ITheme | undefined
  fontSize: number
  fontFamily: string | undefined
}

/** Opens the Appearance dialog (rendered by <AppearanceHost/>, mounted once in App). */
export function openAppearance(): void {}

/**
 * Current terminal appearance; re-renders when it changes (setting change, or
 * the app theme switching). Every terminal applies it to term.options live.
 */
export function useTerminalAppearance(): TerminalAppearance {
  return { theme: undefined, fontSize: 13, fontFamily: undefined }
}
