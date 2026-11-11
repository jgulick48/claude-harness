// Appearance settings (M6): app theme override and terminal look.
import { useSyncExternalStore } from 'react'
import type { ITheme } from '@xterm/xterm'
import { zoomAction, type ThemeSource } from './appearanceModel'
import { createAppearanceStore } from './appearanceStore'

export interface TerminalAppearance {
  /** xterm theme to apply; undefined means "derive from the app's CSS tokens". */
  theme: ITheme | undefined
  fontSize: number
  fontFamily: string | undefined
}

/** Sets data-theme on <html> (or removes it for System) and syncs Electron's nativeTheme. */
function applyAppTheme(t: ThemeSource): void {
  if (typeof document === 'undefined') return
  if (t === 'system') document.documentElement.removeAttribute('data-theme')
  else document.documentElement.setAttribute('data-theme', t)
  try {
    window.harness?.setThemeSource(t)
  } catch {
    /* no bridge (tests) */
  }
}

// Created at module load so the theme is applied before first paint.
export const appearanceStore = createAppearanceStore({ onTheme: applyAppTheme })

if (typeof window !== 'undefined') {
  // Under "System" the OS switching scheme changes the CSS tokens; re-render so
  // terminals following the app re-read them.
  window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener('change', () => appearanceStore.refresh())

  // Zoom shortcuts, in the capture phase so they work while a terminal has focus.
  const isMac = navigator.platform.toLowerCase().includes('mac')
  window.addEventListener(
    'keydown',
    (e) => {
      const a = zoomAction(e, isMac)
      if (!a) return
      e.preventDefault()
      // Keep xterm from also sending the key to the shell.
      e.stopImmediatePropagation()
      appearanceStore.zoom(a)
    },
    true
  )
}

// Focus trail, so closing can return focus to what opened the dialog even when
// that was a menu item that has since unmounted (fall back to the menu's button).
const trail: HTMLElement[] = []
if (typeof document !== 'undefined') {
  document.addEventListener('focusin', (e) => {
    const t = e.target
    if (!(t instanceof HTMLElement) || t.closest('.appearance-dialog')) return
    if (trail[trail.length - 1] !== t) trail.push(t)
    if (trail.length > 8) trail.shift()
  })
}
/** The most recently focused element outside the dialog that is still in the page. */
export function lastOutsideFocus(): HTMLElement | undefined {
  return [...trail].reverse().find((el) => el.isConnected)
}

// Dialog open state.
let open = false
const openListeners = new Set<() => void>()
function setOpen(v: boolean): void {
  if (open === v) return
  open = v
  for (const l of [...openListeners]) l()
}

/** Opens the Appearance dialog (rendered by <AppearanceHost/>, mounted once in App). */
export function openAppearance(): void {
  setOpen(true)
}
export function closeAppearance(): void {
  setOpen(false)
}
export function useAppearanceOpen(): boolean {
  return useSyncExternalStore(
    (l) => {
      openListeners.add(l)
      return () => openListeners.delete(l)
    },
    () => open
  )
}

/**
 * Current terminal appearance; re-renders when it changes (setting change, or
 * the app theme switching). Every terminal applies it to term.options live.
 */
export function useTerminalAppearance(): TerminalAppearance {
  return useSyncExternalStore(appearanceStore.subscribe, appearanceStore.getSnapshot)
}

export function useAppearanceSettings() {
  return useSyncExternalStore(appearanceStore.subscribe, appearanceStore.getSettings)
}
