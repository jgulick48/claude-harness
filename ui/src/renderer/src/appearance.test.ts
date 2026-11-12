import { describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_FAMILY,
  DEFAULT_FONT,
  FONT_SIZE_KEY,
  TERM_THEME_KEY,
  THEME_KEY,
  applyZoom,
  clampFontSize,
  loadAppearance,
  validPreset,
  validThemeSource,
  zoomAction,
  type ZoomKey
} from './appearanceModel'
import { createAppearanceStore } from './appearanceStore'
import { PRESETS } from './terminalThemes'

function mem(init: Record<string, string> = {}) {
  const m = new Map(Object.entries(init))
  return { m, get: () => ({ getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }) }
}
const throwing = () => ({
  getItem: () => {
    throw new Error('denied')
  },
  setItem: () => {
    throw new Error('denied')
  }
})

describe('presets', () => {
  it('are complete xterm themes', () => {
    expect(PRESETS.map((p) => p.id)).toEqual(['solarized-dark', 'solarized-light', 'tomorrow-night', 'gruvbox-dark', 'high-contrast-light'])
    for (const p of PRESETS) {
      for (const k of ['background', 'foreground', 'cursor', 'selectionBackground', 'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white', 'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite'] as const) {
        expect(p.theme[k], `${p.id}.${k}`).toMatch(/^#[0-9a-f]{6}$/)
      }
    }
    expect(PRESETS[0].theme.background).toBe('#002b36')
  })
})

describe('validation', () => {
  it('unknown preset and theme fall back', () => {
    expect(validPreset('nope')).toBe('match-app')
    expect(validPreset(42)).toBe('match-app')
    expect(validPreset('gruvbox-dark')).toBe('gruvbox-dark')
    expect(validThemeSource('purple')).toBe('system')
    expect(validThemeSource('dark')).toBe('dark')
  })
  it('clamps font size', () => {
    expect(clampFontSize(2)).toBe(9)
    expect(clampFontSize(99)).toBe(24)
    expect(clampFontSize('15')).toBe(15)
    expect(clampFontSize(12.6)).toBe(13)
    expect(clampFontSize('abc')).toBe(DEFAULT_FONT)
    expect(clampFontSize(NaN)).toBe(DEFAULT_FONT)
    expect(clampFontSize(null)).toBe(DEFAULT_FONT)
  })
})

describe('persistence', () => {
  it('loads defaults from empty and garbage storage', () => {
    expect(loadAppearance(mem().get)).toEqual({ appTheme: 'system', termTheme: 'match-app', fontSize: 13, fontFamily: DEFAULT_FAMILY })
    const bad = mem({ [THEME_KEY]: 'x', [TERM_THEME_KEY]: 'y', [FONT_SIZE_KEY]: '500' })
    expect(loadAppearance(bad.get)).toMatchObject({ appTheme: 'system', termTheme: 'match-app', fontSize: 24 })
  })
  it('survives a throwing storage', () => {
    expect(loadAppearance(throwing).fontSize).toBe(13)
    const s = createAppearanceStore({ getStorage: throwing })
    expect(() => s.update({ fontSize: 20 })).not.toThrow()
    expect(s.getSettings().fontSize).toBe(20)
  })
  it('round-trips through storage', () => {
    const st = mem()
    const a = createAppearanceStore({ getStorage: st.get })
    a.update({ appTheme: 'dark', termTheme: 'tomorrow-night', fontSize: 17, fontFamily: 'Fira Code' })
    expect(createAppearanceStore({ getStorage: st.get }).getSettings()).toEqual({ appTheme: 'dark', termTheme: 'tomorrow-night', fontSize: 17, fontFamily: 'Fira Code' })
  })
})

describe('store', () => {
  it('notifies subscribers with a fresh snapshot and validates', () => {
    const onTheme = vi.fn()
    const s = createAppearanceStore({ getStorage: mem().get, onTheme })
    expect(onTheme).toHaveBeenCalledWith('system')
    const l = vi.fn()
    const off = s.subscribe(l)
    const before = s.getSnapshot()
    expect(before).toEqual({ theme: undefined, fontSize: 13, fontFamily: undefined })
    s.update({ termTheme: 'solarized-dark', fontSize: 100 })
    expect(l).toHaveBeenCalledTimes(1)
    expect(s.getSnapshot()).not.toBe(before)
    expect(s.getSnapshot().theme?.background).toBe('#002b36')
    expect(s.getSnapshot().fontSize).toBe(24)
    expect(s.getSnapshot()).toBe(s.getSnapshot())
    s.update({ appTheme: 'dark' })
    expect(onTheme).toHaveBeenLastCalledWith('dark')
    s.refresh()
    expect(l).toHaveBeenCalledTimes(3)
    off()
    s.update({ fontSize: 10 })
    expect(l).toHaveBeenCalledTimes(3)
  })
  it('custom family is exposed, reset returns undefined', () => {
    const s = createAppearanceStore({ getStorage: mem().get })
    s.update({ fontFamily: 'Fira Code' })
    expect(s.getSnapshot().fontFamily).toBe('Fira Code')
    s.resetFont()
    expect(s.getSnapshot().fontFamily).toBeUndefined()
    s.update({ fontFamily: '   ' })
    expect(s.getSettings().fontFamily).toBe(DEFAULT_FAMILY)
  })
  it('zoom steps, clamps and resets', () => {
    const s = createAppearanceStore({ getStorage: mem().get })
    s.zoom('in')
    expect(s.getSettings().fontSize).toBe(14)
    for (let i = 0; i < 30; i++) s.zoom('out')
    expect(s.getSettings().fontSize).toBe(9)
    s.zoom('reset')
    expect(s.getSettings().fontSize).toBe(13)
  })
})

describe('zoom shortcuts', () => {
  const k = (key: string, o: Partial<ZoomKey> = {}): ZoomKey => ({ type: 'keydown', key, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, ...o })
  it('Ctrl on Windows/Linux', () => {
    expect(zoomAction(k('=', { ctrlKey: true }), false)).toBe('in')
    expect(zoomAction(k('+', { ctrlKey: true, shiftKey: true }), false)).toBe('in')
    expect(zoomAction(k('-', { ctrlKey: true }), false)).toBe('out')
    expect(zoomAction(k('0', { ctrlKey: true }), false)).toBe('reset')
    expect(zoomAction(k('=', { metaKey: true }), false)).toBeNull()
    expect(zoomAction(k('=', {}), false)).toBeNull()
  })
  it('Cmd on macOS', () => {
    expect(zoomAction(k('=', { metaKey: true }), true)).toBe('in')
    expect(zoomAction(k('-', { metaKey: true }), true)).toBe('out')
    expect(zoomAction(k('0', { metaKey: true }), true)).toBe('reset')
    expect(zoomAction(k('=', { ctrlKey: true }), true)).toBeNull()
  })
  it('ignores Alt/AltGr, keyup and other keys', () => {
    expect(zoomAction(k('=', { ctrlKey: true, altKey: true }), false)).toBeNull()
    expect(zoomAction({ ...k('=', { ctrlKey: true }), type: 'keyup' }, false)).toBeNull()
    expect(zoomAction(k('c', { ctrlKey: true }), false)).toBeNull()
    expect(zoomAction(k('-', { ctrlKey: true, shiftKey: true }), false)).toBeNull()
  })
  it('applyZoom clamps', () => {
    expect(applyZoom(24, 'in')).toBe(24)
    expect(applyZoom(9, 'out')).toBe(9)
    expect(applyZoom(20, 'reset')).toBe(13)
  })
})
