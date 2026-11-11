// Pure appearance logic: validation, clamping, persistence, zoom shortcuts.
import type { ITheme } from '@xterm/xterm'
import type { StorageLike } from './prefs'
import { MATCH_APP, PRESETS } from './terminalThemes'

export type ThemeSource = 'system' | 'light' | 'dark'
export const THEME_SOURCES: ThemeSource[] = ['system', 'light', 'dark']

export const MIN_FONT = 9
export const MAX_FONT = 24
export const DEFAULT_FONT = 13
export const DEFAULT_FAMILY = 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace'

export const THEME_KEY = 'harness.appTheme'
export const TERM_THEME_KEY = 'harness.termTheme'
export const FONT_SIZE_KEY = 'harness.termFontSize'
export const FONT_FAMILY_KEY = 'harness.termFontFamily'

export interface AppearanceSettings {
  appTheme: ThemeSource
  termTheme: string
  fontSize: number
  /** Always set; equals DEFAULT_FAMILY when unset. */
  fontFamily: string
}

export const DEFAULTS: AppearanceSettings = {
  appTheme: 'system',
  termTheme: MATCH_APP,
  fontSize: DEFAULT_FONT,
  fontFamily: DEFAULT_FAMILY
}

export function validThemeSource(v: unknown): ThemeSource {
  return v === 'light' || v === 'dark' ? v : 'system'
}

export function validPreset(v: unknown): string {
  return typeof v === 'string' && PRESETS.some((p) => p.id === v) ? v : MATCH_APP
}

export function clampFontSize(v: unknown): number {
  const n = typeof v === 'number' ? v : typeof v === 'string' && v.trim() !== '' ? Number(v) : NaN
  if (!Number.isFinite(n)) return DEFAULT_FONT
  return Math.min(MAX_FONT, Math.max(MIN_FONT, Math.round(n)))
}

export function validFamily(v: unknown): string {
  return typeof v === 'string' && v.trim() !== '' && v.length <= 500 ? v.trim() : DEFAULT_FAMILY
}

export function presetTheme(id: string): ITheme | undefined {
  return PRESETS.find((p) => p.id === id)?.theme
}

type Getter = () => StorageLike | null | undefined

function defaultStorage(): StorageLike | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

function loadString(key: string, getStorage: Getter = defaultStorage): string | null {
  try {
    return getStorage()?.getItem(key) ?? null
  } catch {
    return null
  }
}

function saveString(key: string, v: string, getStorage: Getter = defaultStorage): void {
  try {
    getStorage()?.setItem(key, v)
  } catch {
    /* the app works without persistence */
  }
}

export function loadAppearance(getStorage?: Getter): AppearanceSettings {
  return {
    appTheme: validThemeSource(loadString(THEME_KEY, getStorage)),
    termTheme: validPreset(loadString(TERM_THEME_KEY, getStorage)),
    fontSize: clampFontSize(loadString(FONT_SIZE_KEY, getStorage)),
    fontFamily: validFamily(loadString(FONT_FAMILY_KEY, getStorage))
  }
}

export function saveAppearance(s: AppearanceSettings, getStorage?: Getter): void {
  saveString(THEME_KEY, s.appTheme, getStorage)
  saveString(TERM_THEME_KEY, s.termTheme, getStorage)
  saveString(FONT_SIZE_KEY, String(s.fontSize), getStorage)
  saveString(FONT_FAMILY_KEY, s.fontFamily, getStorage)
}

export type ZoomAction = 'in' | 'out' | 'reset'

export interface ZoomKey {
  type: string
  key: string
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
  metaKey: boolean
}

/**
 * Terminal zoom shortcuts: Cmd on macOS, Ctrl elsewhere, with =/+ in, - out,
 * 0 reset. Ctrl+= and Ctrl+0 send nothing useful to a terminal. Ctrl+- would
 * reach the shell as ^_ (readline/emacs "undo"), which is the one real cost
 * of the shortcut on Windows/Linux; Ctrl+Shift+- (^_ again) is left alone.
 * Alt combinations are never taken (AltGr layouts report ctrl+alt).
 */
export function zoomAction(e: ZoomKey, isMac: boolean): ZoomAction | null {
  if (e.type !== 'keydown' || e.altKey) return null
  if (isMac ? !e.metaKey || e.ctrlKey : !e.ctrlKey || e.metaKey) return null
  if (e.key === '=' || e.key === '+') return 'in'
  if (e.key === '-' && !e.shiftKey) return 'out'
  if (e.key === '0' && !e.shiftKey) return 'reset'
  return null
}

export function applyZoom(size: number, a: ZoomAction): number {
  return a === 'reset' ? DEFAULT_FONT : clampFontSize(size + (a === 'in' ? 1 : -1))
}
