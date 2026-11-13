import type { CoreInfo, HarnessApi, TerminalMenuChoice } from '../../src/preload/index.d'

/**
 * Every key of the real preload bridge. Typed as Record<keyof HarnessApi, true>
 * so adding or removing a method in HarnessApi is a compile error here until
 * this list (and the fake below) are updated. The electron suite also compares
 * it with the keys of the real window.harness.
 */
export const bridgeKeys: Record<keyof HarnessApi, true> = {
  getCoreInfo: true,
  onCoreStatus: true,
  onUpdateStatus: true,
  onSystemResume: true,
  checkForUpdates: true,
  installUpdate: true,
  downloadUpdate: true,
  setAttentionCount: true,
  focusWindow: true,
  readClipboard: true,
  writeClipboard: true,
  setThemeSource: true,
  showTerminalMenu: true,
  openLink: true
}

export interface E2EHooks {
  /** The callback the renderer registered with onSystemResume, if any. */
  resume: (() => void) | null
  /** Number of `term.resize` requests the page has sent to the core. */
  resizes: number
  /** Last value passed to setAttentionCount (waiting sessions), or -1. */
  attention: number
  /** How many times focusWindow was called (e.g. a notification click). */
  focused: number
  /** In-memory stand-in for the system clipboard. */
  clipboard: string
  /** Last value passed to setThemeSource, or null. */
  themeSource: 'system' | 'light' | 'dark' | null
  /** URLs passed to openLink, in order. */
  opened: string[]
  /** What the fake terminal menu returns; the link it was shown for. */
  menuChoice: TerminalMenuChoice
  menuLink: string | null
}

declare global {
  interface Window {
    __e2e: E2EHooks
  }
}

/**
 * Runs in the page (via addInitScript), so it must be self-contained. The
 * `HarnessApi` annotation makes the compiler reject a missing or extra member.
 */
export function installFakeBridge(info: CoreInfo): void {
  const hooks: E2EHooks = {
    resume: null,
    resizes: 0,
    attention: -1,
    focused: 0,
    clipboard: '',
    themeSource: null,
    opened: [],
    menuChoice: null,
    menuLink: null
  }
  window.__e2e = hooks
  // Count term.resize requests (JSON text frames), e.g. to catch refit loops.
  const send = WebSocket.prototype.send
  WebSocket.prototype.send = function (this: WebSocket, data: Parameters<WebSocket['send']>[0]): void {
    if (typeof data === 'string' && data.includes('"term.resize"')) hooks.resizes++
    return send.call(this, data)
  }
  const api: HarnessApi = {
    getCoreInfo: async () => info,
    onCoreStatus: (cb) => {
      setTimeout(() => cb({ state: 'ready' }), 0)
      return () => {}
    },
    onUpdateStatus: (cb) => {
      setTimeout(() => cb({ state: 'unsupported' }), 0)
      return () => {}
    },
    onSystemResume: (cb) => {
      hooks.resume = cb
      return () => {
        if (hooks.resume === cb) hooks.resume = null
      }
    },
    checkForUpdates: async () => {},
    installUpdate: async () => {},
    downloadUpdate: async () => {},
    setAttentionCount: (n) => {
      hooks.attention = n
    },
    focusWindow: () => {
      hooks.focused++
    },
    readClipboard: async () => hooks.clipboard,
    writeClipboard: async (text) => {
      hooks.clipboard = text
    },
    setThemeSource: (source) => {
      hooks.themeSource = source
    },
    showTerminalMenu: async (_hasSelection, link) => {
      hooks.menuLink = link ?? null
      return hooks.menuChoice
    },
    openLink: async (url) => {
      hooks.opened.push(url)
    }
  }
  window.harness = api
}
