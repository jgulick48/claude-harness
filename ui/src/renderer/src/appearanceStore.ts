// Tiny external store for appearance settings (module-level value + listeners,
// consumed with useSyncExternalStore). Side effects are injected so it's testable.
import {
  DEFAULTS,
  DEFAULT_FAMILY,
  applyZoom,
  clampFontSize,
  loadAppearance,
  presetTheme,
  saveAppearance,
  validFamily,
  validPreset,
  validThemeSource,
  type AppearanceSettings,
  type ZoomAction
} from './appearanceModel'
import type { TerminalAppearance } from './appearance'

export interface StoreDeps {
  getStorage?: () => { getItem(k: string): string | null; setItem(k: string, v: string): void } | null
  /** Called with the app theme whenever it is (re)applied, including at creation. */
  onTheme?: (t: AppearanceSettings['appTheme']) => void
}

export function createAppearanceStore(deps: StoreDeps = {}) {
  let settings: AppearanceSettings = loadAppearance(deps.getStorage)
  const listeners = new Set<() => void>()
  const compute = (): TerminalAppearance => ({
    theme: presetTheme(settings.termTheme),
    fontSize: settings.fontSize,
    fontFamily: settings.fontFamily === DEFAULT_FAMILY ? undefined : settings.fontFamily
  })
  let snapshot = compute()
  deps.onTheme?.(settings.appTheme)

  const emit = (): void => {
    snapshot = compute()
    for (const l of [...listeners]) l()
  }

  return {
    subscribe(l: () => void): () => void {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    getSettings: (): AppearanceSettings => settings,
    getSnapshot: (): TerminalAppearance => snapshot,
    /** Applies validated changes, persists, notifies. */
    update(patch: Partial<{ [K in keyof AppearanceSettings]: unknown }>): void {
      const next: AppearanceSettings = {
        appTheme: 'appTheme' in patch ? validThemeSource(patch.appTheme) : settings.appTheme,
        termTheme: 'termTheme' in patch ? validPreset(patch.termTheme) : settings.termTheme,
        fontSize: 'fontSize' in patch ? clampFontSize(patch.fontSize) : settings.fontSize,
        fontFamily: 'fontFamily' in patch ? validFamily(patch.fontFamily) : settings.fontFamily
      }
      const themeChanged = next.appTheme !== settings.appTheme
      settings = next
      saveAppearance(settings, deps.getStorage)
      if (themeChanged) deps.onTheme?.(settings.appTheme)
      emit()
    },
    zoom(a: ZoomAction): void {
      this.update({ fontSize: applyZoom(settings.fontSize, a) })
    },
    resetFont(): void {
      this.update({ fontFamily: DEFAULTS.fontFamily })
    },
    /** The effective color scheme changed (OS switch under "System"): re-notify. */
    refresh: emit
  }
}

export type AppearanceStore = ReturnType<typeof createAppearanceStore>
